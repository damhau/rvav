package store

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/damhau/rvav/internal/ids"
)

// Notifications (ADR-0023): kept for the app's centre, delivered on the
// channels each user chose.

// NewNotification is a notification to keep and send.
type NewNotification struct {
	UserID, Kind, Title, Body, Level, Link string
	RequestID, DeploymentID, ApprovalID    *string
	Data                                   map[string]any
	// DedupeKey makes a second notification for the same occasion nothing.
	DedupeKey string
}

// Notification is a kept notification.
type Notification struct {
	ID           string          `db:"id"`
	UserID       string          `db:"user_id"`
	Kind         string          `db:"kind"`
	Title        string          `db:"title"`
	Body         string          `db:"body"`
	Level        string          `db:"level"`
	Link         string          `db:"link"`
	RequestID    *string         `db:"request_id"`
	DeploymentID *string         `db:"deployment_id"`
	ApprovalID   *string         `db:"approval_id"`
	Data         json.RawMessage `db:"data"`
	CreatedAt    time.Time       `db:"created_at"`
	ReadAt       *time.Time      `db:"read_at"`
}

const notificationCols = `id, user_id, kind, title, body, level, link, request_id, deployment_id, approval_id, data, created_at, read_at`

// DefaultChannels are the channels besides the app for a kind when the
// user chose nothing (ADR-0023).
func DefaultChannels(kind string) []string {
	switch kind {
	case "approval_needed":
		return []string{"email", "teams", "slack"}
	case "request_succeeded", "request_failed", "request_rejected", "lease_expiring", "lease_expired":
		return []string{"email"}
	}
	return []string{}
}

// NotificationPreferences are a user's choices.
type NotificationPreferences struct {
	Locale string
	// Rules are the channels per kind the user chose; kinds missing keep
	// the defaults.
	Rules map[string][]string
}

// Channels are the channels a kind goes on for these preferences.
func (p NotificationPreferences) Channels(kind string) []string {
	if c, ok := p.Rules[kind]; ok {
		return c
	}
	return DefaultChannels(kind)
}

func (s *Store) GetNotificationPreferences(ctx context.Context, db DB, userID string) (NotificationPreferences, error) {
	p := NotificationPreferences{Locale: "en", Rules: map[string][]string{}}
	var rules []byte
	err := db.QueryRow(ctx, `select locale, rules from notification_preferences where user_id = $1`, userID).Scan(&p.Locale, &rules)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, nil
	}
	if err != nil {
		return p, err
	}
	_ = json.Unmarshal(rules, &p.Rules)
	return p, nil
}

func (s *Store) SetNotificationPreferences(ctx context.Context, userID string, p NotificationPreferences) error {
	rules, err := json.Marshal(p.Rules)
	if err != nil {
		return err
	}
	_, err = s.Pool.Exec(ctx, `insert into notification_preferences (user_id, locale, rules) values ($1, $2, $3)
		on conflict (user_id) do update set locale = excluded.locale, rules = excluded.rules, updated_at = now()`,
		userID, p.Locale, rules)
	return err
}

// InsertNotification keeps a notification and queues a delivery on each
// of the user's channels for its kind that is among those set up. It
// returns the notification, and false when its occasion was notified
// already.
func (s *Store) InsertNotification(ctx context.Context, n NewNotification, setUp []string) (Notification, bool, error) {
	var out Notification
	created := false
	err := s.Tx(ctx, func(tx pgx.Tx) error {
		data, _ := json.Marshal(n.Data)
		if n.Data == nil {
			data = []byte("{}")
		}
		var dedupe *string
		if n.DedupeKey != "" {
			dedupe = &n.DedupeKey
		}
		rows, _ := tx.Query(ctx, `insert into notifications (id, user_id, kind, title, body, level, link, request_id,
				deployment_id, approval_id, data, dedupe_key)
			values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
			on conflict (user_id, dedupe_key) do nothing returning `+notificationCols,
			ids.New(ids.Notification), n.UserID, n.Kind, n.Title, n.Body, n.Level, n.Link, n.RequestID, n.DeploymentID,
			n.ApprovalID, data, dedupe)
		var err error
		out, err = pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[Notification])
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		created = true
		prefs, err := s.GetNotificationPreferences(ctx, tx, n.UserID)
		if err != nil {
			return err
		}
		for _, ch := range prefs.Channels(n.Kind) {
			if !slices.Contains(setUp, ch) {
				continue
			}
			if _, err := tx.Exec(ctx, `insert into notification_deliveries (notification_id, channel) values ($1, $2)
				on conflict do nothing`, out.ID, ch); err != nil {
				return err
			}
		}
		return nil
	})
	return out, created, err
}

// Delivery is a notification to send on a channel, with what rendering it
// needs.
type Delivery struct {
	ID           int64
	Channel      string
	Attempts     int
	CreatedAt    time.Time
	LeaseUntil   *time.Time
	Notification Notification
	UserName     string
	UserEmail    string
	Locale       string
}

// ClaimDeliveries takes deliveries due at the given time, and those a
// notifier took but didn't finish in time: each is the caller's until its
// lease ends.
func (s *Store) ClaimDeliveries(ctx context.Context, limit int, now time.Time, lease time.Duration) ([]Delivery, error) {
	rows, err := s.Pool.Query(ctx, `with due as (
			select id from notification_deliveries
			where (status = 'queued' and next_attempt_at <= $2) or (status = 'sending' and lease_until < $2)
			order by next_attempt_at limit $1 for update skip locked)
		update notification_deliveries d set status = 'sending', attempts = d.attempts + 1,
			lease_until = $2::timestamptz + $3::interval
		from due where d.id = due.id
		returning d.id`, limit, now, lease.String())
	if err != nil {
		return nil, err
	}
	claimed, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil || len(claimed) == 0 {
		return nil, err
	}
	rows, _ = s.Pool.Query(ctx, `select d.id, d.channel, d.attempts, d.created_at, d.lease_until,
			n.id, n.user_id, n.kind, n.title, n.body, n.level, n.link, n.request_id, n.deployment_id, n.approval_id, n.data,
			n.created_at, n.read_at, u.name, u.email, coalesce(p.locale, 'en')
		from notification_deliveries d join notifications n on n.id = d.notification_id join users u on u.id = n.user_id
		left join notification_preferences p on p.user_id = n.user_id
		where d.id = any($1) order by d.id`, claimed)
	defer rows.Close()
	var out []Delivery
	for rows.Next() {
		var d Delivery
		n := &d.Notification
		if err := rows.Scan(&d.ID, &d.Channel, &d.Attempts, &d.CreatedAt, &d.LeaseUntil, &n.ID, &n.UserID, &n.Kind, &n.Title,
			&n.Body, &n.Level, &n.Link, &n.RequestID, &n.DeploymentID, &n.ApprovalID, &n.Data, &n.CreatedAt, &n.ReadAt,
			&d.UserName, &d.UserEmail, &d.Locale); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// DeliveryUpdate is where a delivery stands after an attempt.
type DeliveryUpdate struct {
	Status      string // queued, sent or failed
	NextAttempt *time.Time
	Error       *string
	SentTo      *string
	ExternalID  *string
}

// RecordDelivery moves a delivery on, while the caller still holds the
// lease it claimed it with.
func (s *Store) RecordDelivery(ctx context.Context, id int64, lease *time.Time, u DeliveryUpdate) error {
	_, err := s.Pool.Exec(ctx, `update notification_deliveries set status = $2, lease_until = null,
			next_attempt_at = coalesce($3, next_attempt_at), error = $4, sent_to = coalesce($5, sent_to),
			external_id = coalesce($6, external_id), sent_at = case when $2 = 'sent' then now() end
		where id = $1 and ($7::timestamptz is null or lease_until = $7)`, id, u.Status, u.NextAttempt, u.Error, u.SentTo,
		u.ExternalID, lease)
	return err
}

// PruneNotifications deletes read notifications, and finished deliveries,
// older than before.
func (s *Store) PruneNotifications(ctx context.Context, before time.Time) error {
	if _, err := s.Pool.Exec(ctx, `delete from notification_deliveries where status in ('sent', 'failed') and created_at < $1`,
		before); err != nil {
		return err
	}
	_, err := s.Pool.Exec(ctx, `delete from notifications n where n.read_at is not null and n.created_at < $1
		and not exists (select 1 from notification_deliveries d where d.notification_id = n.id)`, before)
	return err
}

// NotificationSubject is what a notification is about, for its templates.
type NotificationSubject struct {
	RequestNumber, RequestTitle, ItemName, ProjectName, Environment, RequestError string
	DeploymentName                                                                string
	LeaseExpiresAt                                                                *time.Time
	ApprovalName                                                                  string
	ApprovalReasons                                                               []string
}

// NotificationSubjectOf loads the request, deployment and approval level a
// notification is about.
func (s *Store) NotificationSubjectOf(ctx context.Context, n Notification) (NotificationSubject, error) {
	var out NotificationSubject
	if n.RequestID != nil {
		err := s.Pool.QueryRow(ctx, `select r.number, r.title, coalesce(i.name, ''), p.name, r.environment,
				coalesce(r.error_message, '')
			from requests r join projects p on p.id = r.project_id left join catalog_items i on i.id = r.item_id
			where r.id = $1`, *n.RequestID).Scan(&out.RequestNumber, &out.RequestTitle, &out.ItemName, &out.ProjectName,
			&out.Environment, &out.RequestError)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return out, err
		}
	}
	if n.DeploymentID != nil {
		err := s.Pool.QueryRow(ctx, `select d.name, d.lease_expires_at, p.name, d.environment
			from deployments d join projects p on p.id = d.project_id where d.id = $1`, *n.DeploymentID).Scan(
			&out.DeploymentName, &out.LeaseExpiresAt, &out.ProjectName, &out.Environment)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return out, err
		}
	}
	if n.ApprovalID != nil {
		err := s.Pool.QueryRow(ctx, `select name, reasons from approvals where id = $1`, *n.ApprovalID).Scan(
			&out.ApprovalName, &out.ApprovalReasons)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return out, err
		}
	}
	return out, nil
}

// ChannelIdentity is who a user is on Teams or Slack.
type ChannelIdentity struct {
	UserID, Channel, ExternalID string
	ConversationID              *string
}

// GetChannelIdentity finds who a user is on a channel (ErrNotFound when not
// known yet).
func (s *Store) GetChannelIdentity(ctx context.Context, userID, channel string) (ChannelIdentity, error) {
	out := ChannelIdentity{UserID: userID, Channel: channel}
	err := s.Pool.QueryRow(ctx, `select external_id, conversation_id from user_channels where user_id = $1 and channel = $2`,
		userID, channel).Scan(&out.ExternalID, &out.ConversationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	return out, err
}

// SetChannelIdentity records who a user is on a channel; an ID another user
// had moves to this one (an account handed over).
func (s *Store) SetChannelIdentity(ctx context.Context, c ChannelIdentity) error {
	return s.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `delete from user_channels where channel = $1 and external_id = $2 and user_id <> $3`,
			c.Channel, c.ExternalID, c.UserID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `insert into user_channels (user_id, channel, external_id, conversation_id) values ($1, $2, $3, $4)
			on conflict (user_id, channel) do update set external_id = excluded.external_id,
				conversation_id = coalesce(excluded.conversation_id, user_channels.conversation_id), updated_at = now()`,
			c.UserID, c.Channel, c.ExternalID, c.ConversationID)
		return err
	})
}

// UserOnChannel is the user rvav knows by an ID on a channel.
func (s *Store) UserOnChannel(ctx context.Context, channel, externalID string) (string, error) {
	var id string
	err := s.Pool.QueryRow(ctx, `select user_id from user_channels where channel = $1 and external_id = $2`, channel,
		externalID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return id, err
}

// SentApprovalOnChannel says whether rvav sent a user the approval on a
// channel: only they may decide from what it sent.
func (s *Store) SentApprovalOnChannel(ctx context.Context, approvalID, userID, channel string) (bool, error) {
	var ok bool
	err := s.Pool.QueryRow(ctx, `select exists (select 1 from notifications n join notification_deliveries d on d.notification_id = n.id
		where n.approval_id = $1 and n.user_id = $2 and n.kind = 'approval_needed' and d.channel = $3 and d.status = 'sent')`,
		approvalID, userID, channel).Scan(&ok)
	return ok, err
}

// ListNotifications is a page of a user's notifications, newest first.
func (s *Store) ListNotifications(ctx context.Context, userID string, unread bool, after *Cursor, limit int) (Page[Notification], int, error) {
	var afterTime time.Time
	afterID := ""
	if after != nil {
		t, err := time.Parse(time.RFC3339Nano, after.Value)
		if err != nil {
			return Page[Notification]{}, 0, ErrBadCursor
		}
		afterTime, afterID = t, after.ID
	}
	rows, _ := s.Pool.Query(ctx, `select `+notificationCols+` from notifications
		where user_id = $1 and (not $2 or read_at is null) and ($3 = '' or (created_at, id) < ($4, $3))
		order by created_at desc, id desc limit $5`, userID, unread, afterID, afterTime, limit+1)
	list, err := pgx.CollectRows(rows, pgx.RowToStructByName[Notification])
	if err != nil {
		return Page[Notification]{}, 0, err
	}
	count, err := s.UnreadNotifications(ctx, userID)
	if err != nil {
		return Page[Notification]{}, 0, err
	}
	return paginate(list, limit, func(n Notification) Cursor {
		return Cursor{Value: n.CreatedAt.Format(time.RFC3339Nano), ID: n.ID}
	}), count, nil
}

// UnreadNotifications counts a user's unread notifications.
func (s *Store) UnreadNotifications(ctx context.Context, userID string) (int, error) {
	var n int
	err := s.Pool.QueryRow(ctx, `select count(*) from notifications where user_id = $1 and read_at is null`, userID).Scan(&n)
	return n, err
}

// MarkNotificationsRead marks a user's notifications read: those listed, or
// all of them. It returns how many are left unread.
func (s *Store) MarkNotificationsRead(ctx context.Context, userID string, ids []string, all bool) (int, error) {
	if _, err := s.Pool.Exec(ctx, `update notifications set read_at = now()
		where user_id = $1 and read_at is null and ($2 or id = any($3))`, userID, all, ids); err != nil {
		return 0, err
	}
	return s.UnreadNotifications(ctx, userID)
}

// ChannelProblem is why the latest delivery to a user on a channel failed
// for good, when it did ("" otherwise): not in the directory, no address.
func (s *Store) ChannelProblem(ctx context.Context, userID, channel string) (string, error) {
	var status, problem string
	err := s.Pool.QueryRow(ctx, `select d.status, coalesce(d.error, '') from notification_deliveries d
		join notifications n on n.id = d.notification_id
		where n.user_id = $1 and d.channel = $2 and d.status in ('sent', 'failed')
		order by coalesce(d.sent_at, d.created_at) desc limit 1`, userID, channel).Scan(&status, &problem)
	if errors.Is(err, pgx.ErrNoRows) || status == "sent" {
		return "", nil
	}
	return problem, err
}
