// Package notify sends notifications on their channels (ADR-0023): email,
// Microsoft Teams and Slack. A notification and its deliveries are written
// together; the notifier claims due deliveries, renders their templates in
// the user's language and sends them, retrying for a day.
package notify

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/damhau/rvav/internal/store"
)

// Sender sends rendered messages on one channel.
type Sender interface {
	// Send sends m and says where it went (an address, a user) and the
	// channel's ID of what it sent, if any.
	Send(ctx context.Context, m Message) (sentTo, externalID string, err error)
}

// Message is a delivery rendered for its user.
type Message struct {
	Delivery store.Delivery
	Data     Data
	// Subject, Text and HTML are the email's; a card has Title, Short (the
	// kind's text alone) and Facts.
	Subject, Title, Text, HTML, Short string
}

// Fact is a line of a card: what it is about.
type Fact struct{ Label, Value string }

// Facts are what a message is about, labelled in its language.
func (m Message) Facts() []Fact {
	l := labelsFor(m.Data.Locale)
	s := m.Data.Subject
	var out []Fact
	add := func(label, value string) {
		if value != "" {
			out = append(out, Fact{label, value})
		}
	}
	if s.RequestNumber != "" {
		add(l.Request, s.RequestNumber+", "+s.RequestTitle)
	}
	add(l.Item, s.ItemName)
	add(l.Deployment, s.DeploymentName)
	if s.ProjectName != "" {
		add(l.Project, s.ProjectName+" ("+s.Environment+")")
	}
	return out
}

// permanent is a failure that trying again won't fix.
type permanent struct{ error }

// Permanent marks an error as not worth trying again (no address, a user
// not found on the channel).
func Permanent(err error) error { return permanent{err} }

// IsPermanent says whether err was marked Permanent.
func IsPermanent(err error) bool { return errors.As(err, &permanent{}) }

// Back-off between attempts; the last repeats until a delivery is a day
// old.
var backoff = []time.Duration{30 * time.Second, 2 * time.Minute, 10 * time.Minute, 30 * time.Minute, time.Hour}

// GiveUpAfter is how long a delivery is tried: an approval is still in the
// inbox after it, and a reminder that late has lost its use.
const GiveUpAfter = 24 * time.Hour

// Retention is how long read notifications and finished deliveries are kept.
const Retention = 90 * 24 * time.Hour

// Notifier delivers notifications.
type Notifier struct {
	Store   *store.Store
	Senders map[string]Sender
	// SendersNow, when set, gives the channels as saved now, in place of
	// Senders (ADR-0030).
	SendersNow func(context.Context) map[string]Sender
	Log        *slog.Logger
	// PublicURL starts the links in messages.
	PublicURL string
	// Interval between rounds; 2 s when zero.
	Interval time.Duration
	// Now is the clock; time.Now when nil.
	Now func() time.Time
	// Workers is how many deliveries are sent at once; 8 when zero.
	Workers int

	init     sync.Once
	slots    chan struct{}
	inFlight sync.WaitGroup
}

func (n *Notifier) now() time.Time {
	if n.Now != nil {
		return n.Now()
	}
	return time.Now()
}

// Run delivers until the context ends, pruning every hour.
func (n *Notifier) Run(ctx context.Context) {
	interval := n.Interval
	if interval == 0 {
		interval = 2 * time.Second
	}
	tick, prune := time.NewTicker(interval), time.NewTicker(time.Hour)
	defer tick.Stop()
	defer prune.Stop()
	for {
		if _, err := n.round(ctx); err != nil && ctx.Err() == nil {
			n.Log.Warn("notify", "err", err)
		}
		select {
		case <-ctx.Done():
			n.inFlight.Wait()
			return
		case <-tick.C:
		case <-prune.C:
			if err := n.Store.PruneNotifications(ctx, n.now().Add(-Retention)); err != nil {
				n.Log.Warn("prune notifications", "err", err)
			}
		}
	}
}

// Once sends what is due and waits for it; it returns how many it sent or
// tried.
func (n *Notifier) Once(ctx context.Context) (int, error) {
	c, err := n.round(ctx)
	n.inFlight.Wait()
	return c, err
}

// round claims as many due deliveries as there are free workers and starts
// them: each starts as soon as it is claimed, within its lease.
func (n *Notifier) round(ctx context.Context) (int, error) {
	n.init.Do(func() {
		w := n.Workers
		if w == 0 {
			w = 8
		}
		n.slots = make(chan struct{}, w)
	})
	free := cap(n.slots) - len(n.slots)
	if free == 0 {
		return 0, nil
	}
	ds, err := n.Store.ClaimDeliveries(ctx, free, n.now(), lease)
	if err != nil {
		return 0, err
	}
	for _, d := range ds {
		n.slots <- struct{}{}
		n.inFlight.Add(1)
		go func() {
			defer n.inFlight.Done()
			defer func() { <-n.slots }()
			n.deliver(ctx, d)
		}()
	}
	return len(ds), nil
}

// lease is how long a delivery is the notifier's once claimed; a send
// stops before it ends, so no other round sends it too.
const lease = 2 * time.Minute

func (n *Notifier) deliver(ctx context.Context, d store.Delivery) {
	sctx, cancel := context.WithTimeout(ctx, lease-15*time.Second)
	defer cancel()
	sentTo, externalID, err := n.send(sctx, d)
	ctx = context.WithoutCancel(ctx) // what was sent is recorded, even on shutdown
	u := store.DeliveryUpdate{Status: "sent"}
	if sentTo != "" {
		u.SentTo = &sentTo
	}
	if externalID != "" {
		u.ExternalID = &externalID
	}
	if err != nil {
		msg := err.Error()
		u.Error = &msg
		switch {
		case IsPermanent(err) || n.now().Sub(d.CreatedAt) >= GiveUpAfter:
			u.Status = "failed"
		default:
			next := n.now().Add(backoff[min(d.Attempts, len(backoff))-1])
			u.Status, u.NextAttempt = "queued", &next
		}
		n.Log.Warn("notification not sent", "channel", d.Channel, "notification", d.Notification.ID, "err", err)
	}
	if err := n.Store.RecordDelivery(ctx, d.ID, d.LeaseUntil, u); err != nil {
		n.Log.Warn("record notification delivery", "delivery", d.ID, "err", err)
	}
}

// Current are the channels set up now, by name.
func (n *Notifier) Current(ctx context.Context) map[string]Sender {
	if n.SendersNow != nil {
		return n.SendersNow(ctx)
	}
	return n.Senders
}

func (n *Notifier) send(ctx context.Context, d store.Delivery) (string, string, error) {
	s, ok := n.Current(ctx)[d.Channel]
	if !ok {
		return "", "", Permanent(errors.New(d.Channel + " is not set up"))
	}
	m, err := n.Render(ctx, d)
	if err != nil {
		return "", "", err
	}
	return s.Send(ctx, m)
}

// Render renders a delivery's templates in its user's language.
func (n *Notifier) Render(ctx context.Context, d store.Delivery) (Message, error) {
	subject, err := n.Store.NotificationSubjectOf(ctx, d.Notification)
	if err != nil {
		return Message{}, err
	}
	data := Data{Kind: d.Notification.Kind, Locale: d.Locale, UserName: d.UserName, Title: d.Notification.Title,
		Body: d.Notification.Body, Level: d.Notification.Level, Link: n.PublicURL + d.Notification.Link,
		Preferences: n.PublicURL + "/notifications", Subject: subject, Extra: map[string]any{}}
	_ = json.Unmarshal(d.Notification.Data, &data.Extra)
	m, err := render(d.Notification.Kind, d.Locale, data)
	if err != nil {
		return Message{}, Permanent(err) // a template that can't render won't later
	}
	m.Delivery = d
	return m, nil
}
