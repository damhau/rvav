package core

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/brand"
	"github.com/damhau/rvav/internal/notify"
	"github.com/damhau/rvav/internal/store"
)

// The portal's branding and the notification templates administrators
// edit (ADR-0059).

// MaxPortalName is the longest portal name: it fits the menu's header.
const MaxPortalName = 40

func palette(p brand.Palette) api.AccentPalette {
	return api.AccentPalette{Accent: p.Accent, AccentFg: p.AccentFg, AccentSoft: p.AccentSoft}
}

func brandingOf(b store.Branding) api.Branding {
	light, dark := brand.Rvav()
	if b.Accent != "" {
		if l, d, err := brand.Accents(b.Accent); err == nil {
			light, dark = l, d
		}
	}
	out := api.Branding{Name: cmp.Or(b.Name, notify.DefaultPortal), Light: palette(light), Dark: palette(dark)}
	if b.Accent != "" {
		out.Accent = &b.Accent
	}
	logo := func(variant string, l *store.BrandLogo) *string {
		if l == nil {
			return nil
		}
		return new("/api/v1/branding/logos/" + variant + "?v=" + l.SHA[:16])
	}
	out.LogoLightUrl, out.LogoDarkUrl = logo("light", b.LogoLight), logo("dark", b.LogoDark)
	return out
}

// Branding is the portal's branding, for anyone, and its ETag.
func (s *Service) Branding(ctx context.Context) (api.Branding, string, error) {
	b, err := s.Store.GetBranding(ctx, false)
	if err != nil {
		return api.Branding{}, "", err
	}
	out := brandingOf(b)
	j, _ := json.Marshal(out)
	sum := sha256.Sum256(j)
	return out, `"` + hex.EncodeToString(sum[:8]) + `"`, nil
}

// BrandingLogo is a logo, for anyone.
func (s *Service) BrandingLogo(ctx context.Context, variant api.LogoVariant) (store.BrandLogo, error) {
	b, err := s.Store.GetBranding(ctx, true)
	if err != nil {
		return store.BrandLogo{}, err
	}
	l := b.LogoLight
	if variant == api.LogoDark {
		l = b.LogoDark
	}
	if l == nil {
		return store.BrandLogo{}, fmt.Errorf("%w: there is no %s logo", ErrNotFound, variant)
	}
	return *l, nil
}

// MessageBrand is the branding messages show.
func (s *Service) MessageBrand(ctx context.Context) (notify.Brand, error) {
	b, err := s.Store.GetBranding(ctx, true)
	if err != nil {
		return notify.Brand{}, err
	}
	return notify.BrandOf(b), nil
}

func mayBrand(p *auth.Principal) error {
	if !p.Everywhere(auth.SystemManage) {
		return fmt.Errorf("%w: only administrators set the branding and the notification templates", ErrForbidden)
	}
	return nil
}

func accentCheck(accent string) api.AccentCheck {
	l, d, err := brand.Accents(accent)
	var ce *brand.ContrastError
	switch {
	case errors.As(err, &ce):
		out := api.AccentCheck{Ratio: ratio(ce.Ratio), Message: new(err.Error())}
		if ce.Suggestion != "" {
			out.Suggestion = &ce.Suggestion
		}
		return out
	case err != nil:
		return api.AccentCheck{Message: new(err.Error())}
	}
	return api.AccentCheck{Ok: true, Ratio: ratio(brand.Worst(l)), Light: new(palette(l)), Dark: new(palette(d))}
}

func ratio(r float64) float32 { return float32(math.Floor(r*100) / 100) }

// CheckBrandingAccent says how an accent reads in both themes.
func (s *Service) CheckBrandingAccent(_ context.Context, p *auth.Principal, q api.AccentCheckQuery) (api.AccentCheck, error) {
	if err := mayBrand(p); err != nil {
		return api.AccentCheck{}, err
	}
	return accentCheck(strings.TrimSpace(q.Accent)), nil
}

// SetBranding saves the portal's name and accent; audited.
func (s *Service) SetBranding(ctx context.Context, p *auth.Principal, w api.BrandingWrite) (api.Branding, error) {
	if err := mayBrand(p); err != nil {
		return api.Branding{}, err
	}
	name := strings.Join(strings.Fields(w.Name), " ")
	accent := strings.TrimSpace(w.Accent)
	var errs []api.FieldError
	switch {
	case len([]rune(name)) > MaxPortalName:
		errs = append(errs, fieldErr("/name", "too_long", fmt.Sprintf("A portal's name is %d characters at most: the menu shows it.", MaxPortalName)))
	case strings.ContainsFunc(name, unicode.IsControl):
		errs = append(errs, fieldErr("/name", "invalid", "A portal's name has no control characters."))
	}
	if accent != "" {
		if c := accentCheck(accent); !c.Ok {
			errs = append(errs, fieldErr("/accent", "contrast", deref(c.Message)))
		} else {
			accent, _ = brand.Normalize(accent)
		}
	}
	if len(errs) > 0 {
		return api.Branding{}, &ValidationError{Errors: errs}
	}
	before, err := s.Store.GetBranding(ctx, false)
	if err != nil {
		return api.Branding{}, err
	}
	if err := s.Store.Tx(ctx, func(tx pgx.Tx) error {
		if err := s.Store.SetBranding(ctx, tx, name, accent, &p.User.ID); err != nil {
			return err
		}
		return s.Store.Audit(ctx, tx, store.AuditEvent{ActorID: &p.User.ID, Action: "settings.branding.update", TargetType: "branding",
			TargetID: "branding", Detail: map[string]any{"name": name, "accent": accent, "was": map[string]any{"name": before.Name,
				"accent": before.Accent}}})
	}); err != nil {
		return api.Branding{}, err
	}
	out, _, err := s.Branding(ctx)
	return out, err
}

// SetBrandingLogo saves a logo, checked; audited.
func (s *Service) SetBrandingLogo(ctx context.Context, p *auth.Principal, variant api.LogoVariant, size int64, body io.Reader) (api.Branding, error) {
	if err := mayBrand(p); err != nil {
		return api.Branding{}, err
	}
	if !slices.Contains([]api.LogoVariant{api.LogoLight, api.LogoDark}, variant) {
		return api.Branding{}, &BadRequestError{Msg: "A logo is for the light or the dark theme."}
	}
	tooLarge := &TooLargeError{Msg: fmt.Sprintf("A logo is %d KB at most: emails carry it.", brand.MaxLogoSize>>10)}
	if size > brand.MaxLogoSize {
		return api.Branding{}, tooLarge
	}
	content, err := io.ReadAll(io.LimitReader(body, brand.MaxLogoSize+1))
	if err != nil {
		return api.Branding{}, err
	}
	if len(content) > brand.MaxLogoSize {
		return api.Branding{}, tooLarge
	}
	logo, err := brand.CheckLogo(content)
	var le *brand.LogoError
	switch {
	case errors.As(err, &le) && le.Type:
		return api.Branding{}, &UnsupportedTypeError{Msg: le.Msg}
	case err != nil:
		return api.Branding{}, &ValidationError{Errors: []api.FieldError{fieldErr("", "invalid", err.Error())}}
	}
	sum := sha256.Sum256(content)
	if err := s.Store.Tx(ctx, func(tx pgx.Tx) error {
		if err := s.Store.SetBrandLogo(ctx, tx, variant == api.LogoDark, &store.BrandLogo{ContentType: logo.ContentType,
			Content: content}, &p.User.ID); err != nil {
			return err
		}
		return s.Store.Audit(ctx, tx, store.AuditEvent{ActorID: &p.User.ID, Action: "settings.branding_logo.set", TargetType: "branding",
			TargetID: "logo_" + string(variant), Detail: map[string]any{"variant": variant, "content_type": logo.ContentType,
				"size": len(content), "sha256": hex.EncodeToString(sum[:])}})
	}); err != nil {
		return api.Branding{}, err
	}
	out, _, err := s.Branding(ctx)
	return out, err
}

// DeleteBrandingLogo removes a logo; audited.
func (s *Service) DeleteBrandingLogo(ctx context.Context, p *auth.Principal, variant api.LogoVariant) (api.Branding, error) {
	if err := mayBrand(p); err != nil {
		return api.Branding{}, err
	}
	if !slices.Contains([]api.LogoVariant{api.LogoLight, api.LogoDark}, variant) {
		return api.Branding{}, &BadRequestError{Msg: "A logo is for the light or the dark theme."}
	}
	if err := s.Store.Tx(ctx, func(tx pgx.Tx) error {
		if err := s.Store.SetBrandLogo(ctx, tx, variant == api.LogoDark, nil, &p.User.ID); err != nil {
			return err
		}
		return s.Store.Audit(ctx, tx, store.AuditEvent{ActorID: &p.User.ID, Action: "settings.branding_logo.remove", TargetType: "branding",
			TargetID: "logo_" + string(variant), Detail: map[string]any{"variant": variant}})
	}); err != nil {
		return api.Branding{}, err
	}
	out, _, err := s.Branding(ctx)
	return out, err
}

func templateKind(kind api.NotificationKind) (notify.Kind, error) {
	k, ok := notify.KindOf(string(kind))
	if !ok {
		return k, fmt.Errorf("%w: %s is not a kind of notification", ErrNotFound, kind)
	}
	return k, nil
}

func templateTarget(channel api.TemplateChannel, locale api.TemplateLocale) error {
	var errs []api.FieldError
	if !slices.Contains(notify.Channels, string(channel)) {
		errs = append(errs, fieldErr("/channel", "invalid", "Email, teams or slack."))
	}
	if !slices.Contains(notify.Locales, string(locale)) {
		errs = append(errs, fieldErr("/locale", "invalid", "en or fr."))
	}
	if len(errs) > 0 {
		return &ValidationError{Errors: errs}
	}
	return nil
}

func variablesOut(vs []notify.Variable) []api.TemplateVariable {
	out := make([]api.TemplateVariable, len(vs))
	for i, v := range vs {
		out[i] = api.TemplateVariable{Name: v.Name, Type: v.Type, Description: v.Description}
	}
	return out
}

func partsOut(p notify.Parts) api.TemplateParts {
	out := api.TemplateParts{Subject: p.Subject, Text: p.Text}
	if p.HTML != "" {
		out.Html = &p.HTML
	}
	return out
}

// NotificationTemplates lists the kinds whose templates administrators
// edit, which of them are edited and the variables each may use.
func (s *Service) NotificationTemplates(ctx context.Context, p *auth.Principal) (api.NotificationTemplateCatalog, error) {
	if err := mayBrand(p); err != nil {
		return api.NotificationTemplateCatalog{}, err
	}
	edited, err := s.Store.NotificationTemplates(ctx)
	if err != nil {
		return api.NotificationTemplateCatalog{}, err
	}
	out := api.NotificationTemplateCatalog{Functions: variablesOut(notify.Functions), Kinds: []api.NotificationTemplateKind{}}
	for _, k := range notify.Kinds {
		kind := api.NotificationTemplateKind{Kind: api.NotificationKind(k.Kind), Name: k.Name, When: k.When,
			Variables: variablesOut(k.Variables), Templates: []api.NotificationTemplateSummary{}}
		for _, channel := range notify.Channels {
			for _, locale := range notify.Locales {
				sum := api.NotificationTemplateSummary{Channel: api.TemplateChannel(channel), Locale: api.TemplateLocale(locale)}
				if i := slices.IndexFunc(edited, func(t store.NotificationTemplate) bool {
					return t.Kind == k.Kind && t.Channel == channel && t.Locale == locale
				}); i >= 0 {
					sum.Edited, sum.UpdatedAt, sum.UpdatedBy = true, &edited[i].UpdatedAt, edited[i].UpdatedByName
				}
				kind.Templates = append(kind.Templates, sum)
			}
		}
		out.Kinds = append(out.Kinds, kind)
	}
	return out, nil
}

// NotificationTemplate is a kind's template for a channel and language:
// the edited one, else the built-in one.
func (s *Service) NotificationTemplate(ctx context.Context, p *auth.Principal, kind api.NotificationKind, channel api.TemplateChannel,
	locale api.TemplateLocale) (api.NotificationTemplate, error) {
	if err := mayBrand(p); err != nil {
		return api.NotificationTemplate{}, err
	}
	if _, err := templateKind(kind); err != nil {
		return api.NotificationTemplate{}, err
	}
	if err := templateTarget(channel, locale); err != nil {
		return api.NotificationTemplate{}, err
	}
	def, _ := notify.Default(string(kind), string(channel), string(locale))
	d := partsOut(def)
	out := api.NotificationTemplate{Kind: kind, Channel: channel, Locale: locale, Default: d, Subject: d.Subject, Text: d.Text,
		Html: d.Html}
	t, err := s.Store.GetNotificationTemplate(ctx, string(kind), string(channel), string(locale))
	switch {
	case errors.Is(err, store.ErrNotFound):
		return out, nil
	case err != nil:
		return api.NotificationTemplate{}, err
	}
	e := partsOut(notify.Parts{Subject: t.Subject, Text: t.Text, HTML: t.HTML})
	out.Edited, out.Subject, out.Text, out.Html, out.UpdatedAt, out.UpdatedBy = true, e.Subject, e.Text, e.Html, &t.UpdatedAt, t.UpdatedByName
	return out, nil
}

// sentence is a template problem as a field shows it: "It is empty."
func sentence(s string) string {
	r, size := utf8.DecodeRuneInString(s)
	s = string(unicode.ToUpper(r)) + s[size:]
	if !strings.HasSuffix(s, ".") {
		s += "."
	}
	return s
}

// checkedTemplate is a template to save or preview: its problems, per
// part, are a ValidationError.
func checkedTemplate(kind api.NotificationKind, w api.NotificationTemplateWrite) (notify.Parts, error) {
	if _, err := templateKind(kind); err != nil {
		return notify.Parts{}, err
	}
	if err := templateTarget(w.Channel, w.Locale); err != nil {
		return notify.Parts{}, err
	}
	parts := notify.Parts{Subject: w.Subject, Text: w.Text, HTML: deref(w.Html)}
	if problems := notify.Check(string(kind), string(w.Channel), string(w.Locale), parts); len(problems) > 0 {
		errs := make([]api.FieldError, len(problems))
		for i, pr := range problems {
			errs[i] = fieldErr("/"+pr.Part, "template", sentence(pr.Message))
		}
		return parts, &ValidationError{Errors: errs}
	}
	return parts, nil
}

// SetNotificationTemplate saves a kind's template for a channel and
// language, once checked; audited.
func (s *Service) SetNotificationTemplate(ctx context.Context, p *auth.Principal, kind api.NotificationKind,
	w api.NotificationTemplateWrite) (api.NotificationTemplate, error) {
	if err := mayBrand(p); err != nil {
		return api.NotificationTemplate{}, err
	}
	parts, err := checkedTemplate(kind, w)
	if err != nil {
		return api.NotificationTemplate{}, err
	}
	if err := s.Store.Tx(ctx, func(tx pgx.Tx) error {
		if err := s.Store.SetNotificationTemplate(ctx, tx, store.NotificationTemplate{Kind: string(kind), Channel: string(w.Channel),
			Locale: string(w.Locale), Subject: parts.Subject, Text: parts.Text, HTML: parts.HTML, UpdatedBy: &p.User.ID}); err != nil {
			return err
		}
		return s.Store.Audit(ctx, tx, store.AuditEvent{ActorID: &p.User.ID, Action: "settings.notification_template.update",
			TargetType: "notification_template", TargetID: string(kind) + "/" + string(w.Channel) + "/" + string(w.Locale),
			Detail: map[string]any{"kind": kind, "channel": w.Channel, "locale": w.Locale, "subject": parts.Subject}})
	}); err != nil {
		return api.NotificationTemplate{}, err
	}
	return s.NotificationTemplate(ctx, p, kind, w.Channel, w.Locale)
}

// ResetNotificationTemplate goes back to the built-in template; audited
// when it was edited.
func (s *Service) ResetNotificationTemplate(ctx context.Context, p *auth.Principal, kind api.NotificationKind, channel api.TemplateChannel,
	locale api.TemplateLocale) (api.NotificationTemplate, error) {
	if err := mayBrand(p); err != nil {
		return api.NotificationTemplate{}, err
	}
	if _, err := templateKind(kind); err != nil {
		return api.NotificationTemplate{}, err
	}
	if err := templateTarget(channel, locale); err != nil {
		return api.NotificationTemplate{}, err
	}
	if err := s.Store.Tx(ctx, func(tx pgx.Tx) error {
		deleted, err := s.Store.DeleteNotificationTemplate(ctx, tx, string(kind), string(channel), string(locale))
		if err != nil || !deleted {
			return err
		}
		return s.Store.Audit(ctx, tx, store.AuditEvent{ActorID: &p.User.ID, Action: "settings.notification_template.reset",
			TargetType: "notification_template", TargetID: string(kind) + "/" + string(channel) + "/" + string(locale),
			Detail: map[string]any{"kind": kind, "channel": channel, "locale": locale}})
	}); err != nil {
		return api.NotificationTemplate{}, err
	}
	return s.NotificationTemplate(ctx, p, kind, channel, locale)
}

// PreviewNotificationTemplate writes a template with its kind's sample, as
// it would be sent, with the portal's branding.
func (s *Service) PreviewNotificationTemplate(ctx context.Context, p *auth.Principal, kind api.NotificationKind,
	w api.NotificationTemplateWrite) (api.NotificationTemplatePreview, error) {
	if err := mayBrand(p); err != nil {
		return api.NotificationTemplatePreview{}, err
	}
	parts, err := checkedTemplate(kind, w)
	if err != nil {
		return api.NotificationTemplatePreview{}, err
	}
	b, err := s.MessageBrand(ctx)
	if err != nil {
		return api.NotificationTemplatePreview{}, err
	}
	pv, problems := notify.PreviewOf(string(kind), string(w.Channel), string(w.Locale), parts, b, s.Config.PublicURL)
	if len(problems) > 0 {
		return api.NotificationTemplatePreview{}, &ValidationError{Errors: []api.FieldError{
			fieldErr("/"+problems[0].Part, "template", sentence(problems[0].Message))}}
	}
	out := api.NotificationTemplatePreview{Subject: pv.Message.Subject, Text: pv.Message.Text}
	switch w.Channel {
	case api.TemplateEmail:
		out.Html = &pv.Message.HTML
	case api.TemplateTeams:
		out.Card = &pv.Card
	case api.TemplateSlack:
		var blocks []map[string]any
		j, _ := json.Marshal(pv.Blocks)
		_ = json.Unmarshal(j, &blocks)
		out.Blocks = &blocks
	}
	return out, nil
}
