package core

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/ids"
	"github.com/damhau/rvav/internal/store"
)

// Notifications (ADR-0023): deciding from a Teams or Slack card.

type channelKey struct{}

// fromChannel is the channel a decision came from, for the audit log.
func fromChannel(ctx context.Context) string {
	ch, _ := ctx.Value(channelKey{}).(string)
	return ch
}

// Why a decision from a card wasn't taken, for the card's answer.
var (
	ErrCardNotYours = errors.New("not_yours")
	ErrCardDecided  = errors.New("decided")
	ErrCardComment  = errors.New("comment")
)

// DecideFromChannel decides an approval from a card on a channel, as the
// user rvav sent it to: externalID must be who rvav knows them as there,
// and the inbox's rules apply. It returns who decided.
func (s *Service) DecideFromChannel(ctx context.Context, channel, externalID, approvalID string, approve bool, comment string) (string, error) {
	userID, err := s.Store.UserOnChannel(ctx, channel, externalID)
	if errors.Is(err, store.ErrNotFound) {
		return "", ErrCardNotYours
	}
	if err != nil {
		return "", err
	}
	sent, err := s.Store.SentApprovalOnChannel(ctx, approvalID, userID, channel)
	if err != nil {
		return "", err
	}
	if !sent {
		return "", ErrCardNotYours
	}
	p, err := auth.PrincipalFor(ctx, s.Store, userID)
	if err != nil {
		return "", err
	}
	d := api.ApprovalDecision{Decision: api.ApprovalDecisionDecisionReject}
	if approve {
		d.Decision = api.ApprovalDecisionDecisionApprove
	}
	if comment != "" {
		d.Comment = &comment
	}
	_, err = s.DecideApproval(context.WithValue(ctx, channelKey{}, channel), p, approvalID, d)
	var conflict *ConflictError
	var invalid *ValidationError
	switch {
	case err == nil:
		return p.User.Name, nil
	case errors.As(err, &conflict):
		return "", ErrCardDecided
	case errors.As(err, &invalid) && !approve && comment == "":
		return "", ErrCardComment
	case errors.Is(err, ErrForbidden), errors.Is(err, ErrNotFound):
		return "", ErrCardNotYours
	}
	return "", err
}

// NotificationKinds are the kinds of notifications, in the order people
// meet them.
var NotificationKinds = []api.NotificationKind{api.KindRequestSubmitted, api.KindApprovalNeeded, api.KindRequestApproved,
	api.KindRequestRejected, api.KindRequestSucceeded, api.KindRequestFailed, api.KindRequestRolledBack,
	api.KindLeaseExpiring, api.KindLeaseExpired, api.KindOwnerChanged}

// notificationChannels are the channels besides the app.
var notificationChannels = []string{"email", "teams", "slack"}

func presentNotification(n store.Notification) api.Notification {
	out := api.Notification{Id: n.ID, Kind: api.NotificationKind(n.Kind), Title: n.Title, Level: api.NotificationLevel(n.Level),
		CreatedAt: n.CreatedAt, ReadAt: n.ReadAt}
	if n.Body != "" {
		out.Body = &n.Body
	}
	if n.Link != "" {
		out.Link = &n.Link
	}
	return out
}

// Notifications are the caller's, newest first.
func (s *Service) Notifications(ctx context.Context, p *auth.Principal, params api.ListNotificationsParams) (api.NotificationPage, error) {
	after, err := store.DecodeCursor(deref(params.Cursor))
	if err != nil {
		return api.NotificationPage{}, &ValidationError{Errors: []api.FieldError{fieldErr("/cursor", "invalid", "Not a cursor from this list.")}}
	}
	limit := 50
	if params.Limit != nil {
		limit = *params.Limit
	}
	unread := params.Unread != nil && *params.Unread
	page, count, err := s.Store.ListNotifications(ctx, p.User.ID, unread, after, limit)
	if err != nil {
		return api.NotificationPage{}, err
	}
	out := api.NotificationPage{Items: make([]api.Notification, 0, len(page.Items)), Unread: count}
	for _, n := range page.Items {
		out.Items = append(out.Items, presentNotification(n))
	}
	if page.Next != nil {
		next := page.Next.Encode()
		out.NextCursor = &next
	}
	return out, nil
}

// MarkNotificationsRead marks the caller's notifications read.
func (s *Service) MarkNotificationsRead(ctx context.Context, p *auth.Principal, r api.NotificationsRead) (api.NotificationCount, error) {
	all := r.All != nil && *r.All
	var list []string
	if r.Ids != nil {
		list = *r.Ids
	}
	if !all && len(list) == 0 {
		return api.NotificationCount{}, &ValidationError{Errors: []api.FieldError{fieldErr("/ids", "required", "Say which, or all.")}}
	}
	n, err := s.Store.MarkNotificationsRead(ctx, p.User.ID, list, all)
	return api.NotificationCount{Unread: n}, err
}

// setUpChannels are the channels rvav can send on.
func (s *Service) setUpChannels() []string {
	if s.Notifier == nil {
		return nil
	}
	var out []string
	for _, ch := range notificationChannels {
		if _, ok := s.Notifier.Senders[ch]; ok {
			out = append(out, ch)
		}
	}
	return out
}

// NotificationPreferences are the caller's channels per kind, and which
// channels can reach them.
func (s *Service) NotificationPreferences(ctx context.Context, p *auth.Principal) (api.NotificationPreferences, error) {
	prefs, err := s.Store.GetNotificationPreferences(ctx, s.Store.Pool, p.User.ID)
	if err != nil {
		return api.NotificationPreferences{}, err
	}
	out := api.NotificationPreferences{Locale: api.NotificationPreferencesLocale(prefs.Locale)}
	for _, kind := range NotificationKinds {
		_, chosen := prefs.Rules[string(kind)]
		rule := api.NotificationRule{Kind: kind, Channels: []api.NotificationChannel{}}
		for _, ch := range prefs.Channels(string(kind)) {
			rule.Channels = append(rule.Channels, api.NotificationChannel(ch))
		}
		out.Rules = append(out.Rules, struct {
			Channels []api.NotificationChannel `json:"channels"`
			Default  bool                      `json:"default"`
			Kind     api.NotificationKind      `json:"kind"`
		}{Channels: rule.Channels, Default: !chosen, Kind: kind})
	}
	setUp := s.setUpChannels()
	names := map[string]string{"email": "Email", "teams": "Teams", "slack": "Slack"}
	for _, ch := range notificationChannels {
		c := struct {
			Address   *string                 `json:"address,omitempty"`
			Available bool                    `json:"available"`
			Channel   api.NotificationChannel `json:"channel"`
			Reason    *string                 `json:"reason,omitempty"`
		}{Channel: api.NotificationChannel(ch)}
		problem := ""
		switch {
		case !slices.Contains(setUp, ch):
			problem = names[ch] + " is not set up in rvav: ask an administrator."
		case p.User.Email == "":
			problem = "Your account has no email address, so rvav can't find you there."
		default:
			if problem, err = s.Store.ChannelProblem(ctx, p.User.ID, ch); err != nil {
				return out, err
			}
			if problem != "" && !strings.Contains(problem, "not in the") {
				problem = "" // a passing failure, tried again next time
			}
		}
		if problem != "" {
			c.Reason = &problem
		} else {
			c.Available = true
			c.Address = &p.User.Email
		}
		out.Channels = append(out.Channels, c)
	}
	return out, nil
}

// SetNotificationPreferences saves the caller's choices; kinds left out
// keep the defaults.
func (s *Service) SetNotificationPreferences(ctx context.Context, p *auth.Principal, w api.NotificationPreferencesWrite) (api.NotificationPreferences, error) {
	var errs []api.FieldError
	if w.Locale != "en" && w.Locale != "fr" {
		errs = append(errs, fieldErr("/locale", "invalid", "en or fr."))
	}
	rules := map[string][]string{}
	for i, r := range w.Rules {
		at := "/rules/" + strconv.Itoa(i)
		if !slices.Contains(NotificationKinds, r.Kind) {
			errs = append(errs, fieldErr(at+"/kind", "invalid", string(r.Kind)+" is not a kind of notification."))
			continue
		}
		if _, dup := rules[string(r.Kind)]; dup {
			errs = append(errs, fieldErr(at+"/kind", "duplicate", "One rule per kind."))
			continue
		}
		chs := []string{}
		for j, ch := range r.Channels {
			switch {
			case ch == api.ChannelInApp:
				errs = append(errs, fieldErr(at+"/channels/"+strconv.Itoa(j), "invalid", "In the app is always on."))
			case !slices.Contains(notificationChannels, string(ch)):
				errs = append(errs, fieldErr(at+"/channels/"+strconv.Itoa(j), "invalid", string(ch)+" is not a channel."))
			case !slices.Contains(chs, string(ch)):
				chs = append(chs, string(ch))
			}
		}
		rules[string(r.Kind)] = chs
	}
	if len(errs) > 0 {
		return api.NotificationPreferences{}, &ValidationError{Errors: errs}
	}
	if err := s.Store.SetNotificationPreferences(ctx, p.User.ID, store.NotificationPreferences{Locale: string(w.Locale),
		Rules: rules}); err != nil {
		return api.NotificationPreferences{}, err
	}
	return s.NotificationPreferences(ctx, p)
}

// TestNotificationChannel sends the caller a test message on a channel,
// now, and says how it went.
func (s *Service) TestNotificationChannel(ctx context.Context, p *auth.Principal, channel api.NotificationChannel) (api.IntegrationCheck, error) {
	out := api.IntegrationCheck{Name: string(channel)}
	if !slices.Contains(notificationChannels, string(channel)) {
		return out, &ValidationError{Errors: []api.FieldError{fieldErr("/channel", "invalid", "Email, teams or slack.")}}
	}
	if s.Notifier == nil || s.Notifier.Senders[string(channel)] == nil {
		out.Message = string(channel) + " is not set up in rvav."
		return out, nil
	}
	prefs, err := s.Store.GetNotificationPreferences(ctx, s.Store.Pool, p.User.ID)
	if err != nil {
		return out, err
	}
	d := store.Delivery{Channel: string(channel), UserName: p.User.Name, UserEmail: p.User.Email, Locale: prefs.Locale,
		Notification: store.Notification{ID: ids.New(ids.Notification), UserID: p.User.ID, Kind: "test", Title: "Test",
			Level: "info", Link: "/notifications", Data: []byte("{}")}}
	m, err := s.Notifier.Render(ctx, d)
	if err != nil {
		return out, err
	}
	to, _, err := s.Notifier.Senders[string(channel)].Send(ctx, m)
	if err != nil {
		out.Message = err.Error()
		return out, nil
	}
	out.Ok, out.Message = true, "Sent to "+to
	return out, nil
}
