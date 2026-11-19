package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Notification templates administrators edit, and the portal's branding
// (ADR-0059).

// NotificationTemplate replaces a built-in template of a kind, channel and
// language.
type NotificationTemplate struct {
	Kind          string    `db:"kind"`
	Channel       string    `db:"channel"`
	Locale        string    `db:"locale"`
	Subject       string    `db:"subject"`
	Text          string    `db:"text_body"`
	HTML          string    `db:"html_body"`
	UpdatedAt     time.Time `db:"updated_at"`
	UpdatedBy     *string   `db:"updated_by"`
	UpdatedByName *string   `db:"updated_by_name"`
}

const templateColumns = `t.kind, t.channel, t.locale, t.subject, t.text_body, t.html_body, t.updated_at, t.updated_by,
	u.name as updated_by_name from notification_templates t left join users u on u.id = t.updated_by`

// NotificationTemplates are the edited templates.
func (s *Store) NotificationTemplates(ctx context.Context) ([]NotificationTemplate, error) {
	rows, _ := s.Pool.Query(ctx, `select `+templateColumns+` order by t.kind, t.channel, t.locale`)
	return pgx.CollectRows(rows, pgx.RowToStructByName[NotificationTemplate])
}

// GetNotificationTemplate is the edited template of a kind, channel and
// language: ErrNotFound when it has none.
func (s *Store) GetNotificationTemplate(ctx context.Context, kind, channel, locale string) (NotificationTemplate, error) {
	rows, _ := s.Pool.Query(ctx, `select `+templateColumns+` where t.kind = $1 and t.channel = $2 and t.locale = $3`,
		kind, channel, locale)
	t, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[NotificationTemplate])
	if errors.Is(err, pgx.ErrNoRows) {
		return t, ErrNotFound
	}
	return t, err
}

// SetNotificationTemplate saves an edited template.
func (s *Store) SetNotificationTemplate(ctx context.Context, db DB, t NotificationTemplate) error {
	_, err := db.Exec(ctx, `insert into notification_templates (kind, channel, locale, subject, text_body, html_body, updated_by)
		values ($1, $2, $3, $4, $5, $6, $7)
		on conflict (kind, channel, locale) do update set subject = excluded.subject, text_body = excluded.text_body,
			html_body = excluded.html_body, updated_at = now(), updated_by = excluded.updated_by`,
		t.Kind, t.Channel, t.Locale, t.Subject, t.Text, t.HTML, t.UpdatedBy)
	return err
}

// DeleteNotificationTemplate goes back to the built-in template: false
// when it wasn't edited.
func (s *Store) DeleteNotificationTemplate(ctx context.Context, db DB, kind, channel, locale string) (bool, error) {
	tag, err := db.Exec(ctx, `delete from notification_templates where kind = $1 and channel = $2 and locale = $3`,
		kind, channel, locale)
	return tag.RowsAffected() == 1, err
}

// BrandLogo is a logo picture.
type BrandLogo struct {
	ContentType string
	// SHA is its content's SHA-256, in hex.
	SHA     string
	Content []byte
}

// Branding is the portal's name, accent colour and logos; empty is rvav's
// own.
type Branding struct {
	Name, Accent        string
	LogoLight, LogoDark *BrandLogo
}

// GetBranding is the portal's branding, with its logos' content when asked:
// without, only their type and hash.
func (s *Store) GetBranding(ctx context.Context, content bool) (Branding, error) {
	var b Branding
	var light, dark []byte
	var lightType, darkType, lightSHA, darkSHA *string
	err := s.Pool.QueryRow(ctx, `select name, accent, logo_light_type, encode(sha256(logo_light), 'hex'),
		case when $1 then logo_light end, logo_dark_type, encode(sha256(logo_dark), 'hex'), case when $1 then logo_dark end
		from branding`, content).Scan(&b.Name, &b.Accent, &lightType, &lightSHA, &light, &darkType, &darkSHA, &dark)
	if errors.Is(err, pgx.ErrNoRows) {
		return Branding{}, nil
	}
	if err != nil {
		return b, err
	}
	if lightType != nil {
		b.LogoLight = &BrandLogo{ContentType: *lightType, SHA: *lightSHA, Content: light}
	}
	if darkType != nil {
		b.LogoDark = &BrandLogo{ContentType: *darkType, SHA: *darkSHA, Content: dark}
	}
	return b, nil
}

// SetBranding saves the portal's name and accent colour.
func (s *Store) SetBranding(ctx context.Context, db DB, name, accent string, userID *string) error {
	_, err := db.Exec(ctx, `insert into branding (name, accent, updated_by) values ($1, $2, $3)
		on conflict (id) do update set name = excluded.name, accent = excluded.accent, updated_at = now(),
			updated_by = excluded.updated_by`, name, accent, userID)
	return err
}

// SetBrandLogo saves or, when nil, removes the light or dark logo.
func (s *Store) SetBrandLogo(ctx context.Context, db DB, dark bool, logo *BrandLogo, userID *string) error {
	var content []byte
	var contentType *string
	if logo != nil {
		content, contentType = logo.Content, &logo.ContentType
	}
	q := `insert into branding (logo_light, logo_light_type, updated_by) values ($1, $2, $3)
		on conflict (id) do update set logo_light = excluded.logo_light, logo_light_type = excluded.logo_light_type,
			updated_at = now(), updated_by = excluded.updated_by`
	if dark {
		q = `insert into branding (logo_dark, logo_dark_type, updated_by) values ($1, $2, $3)
		on conflict (id) do update set logo_dark = excluded.logo_dark, logo_dark_type = excluded.logo_dark_type,
			updated_at = now(), updated_by = excluded.updated_by`
	}
	_, err := db.Exec(ctx, q, content, contentType, userID)
	return err
}
