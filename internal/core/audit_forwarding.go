package core

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auditlog"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/hosts"
	"github.com/damhau/rvav/internal/ids"
	"github.com/damhau/rvav/internal/store"
	"github.com/damhau/rvav/internal/trust"
)

// Audit forwarding (ADR-0058): administrators send audit events to syslog
// servers and webhooks; auditors see where they go and how it goes.

var hostName = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,62}[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]{0,62}[A-Za-z0-9])?)*$`)

// headerName is an HTTP header's name (RFC 9110's token).
var headerName = regexp.MustCompile("^[!#$%&'*+.^_`|~0-9A-Za-z-]{1,64}$")

// The headers rvav sets on a webhook's calls itself, or that HTTP owns.
var ownHeaders = []string{"content-type", "content-length", "host", "user-agent", "transfer-encoding", "connection"}

func mayReadAuditDestinations(p *auth.Principal) bool {
	return p.Everywhere(auth.IntegrationsManage) || p.Everywhere(auth.AuditRead)
}

func presentAuditDestination(d store.AuditDestination) api.AuditDestination {
	out := api.AuditDestination{Id: d.ID, Name: d.Name, Enabled: d.Enabled, Kind: api.AuditDestinationKind(d.Kind),
		Categories: make([]api.AuditCategory, len(d.Categories)), CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt,
		State: api.AuditDeliveryState{Backlog: d.Backlog, SetAside: d.SetAside, SetAsideReason: d.SetAsideReason,
			Delivered: d.Delivered, Failures: d.Failures, LastSuccessAt: d.LastSuccessAt, LastError: d.LastError,
			LastErrorAt: d.LastErrorAt}}
	for i, c := range d.Categories {
		out.Categories[i] = api.AuditCategory(c)
	}
	if d.Failures > 0 && d.Backlog > 0 {
		out.State.NextAttemptAt = &d.NextAttemptAt
	}
	switch d.Kind {
	case "syslog":
		var c api.AuditSyslog
		if json.Unmarshal(d.Settings, &c) == nil {
			out.Syslog = &c
		}
	case "webhook":
		var c api.AuditWebhook
		if json.Unmarshal(d.Settings, &c) == nil {
			c.Url, c.AuthValue = auditlog.RedactURL(c.Url), nil
			out.Webhook = &c
		}
	}
	return out
}

// shownSettings are a destination's settings as readers see them, and the
// audit log records them.
func shownSettings(d store.AuditDestination) json.RawMessage {
	shown := presentAuditDestination(d)
	var b []byte
	if shown.Webhook != nil {
		b, _ = json.Marshal(shown.Webhook)
	} else {
		b, _ = json.Marshal(shown.Syslog)
	}
	return b
}

func (s *Service) AuditDestinations(ctx context.Context, p *auth.Principal) ([]api.AuditDestination, error) {
	if !mayReadAuditDestinations(p) {
		return nil, ErrForbidden
	}
	list, err := s.Store.AuditDestinations(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]api.AuditDestination, len(list))
	for i, d := range list {
		out[i] = presentAuditDestination(d)
	}
	return out, nil
}

func (s *Service) GetAuditDestination(ctx context.Context, p *auth.Principal, id string) (api.AuditDestination, error) {
	if !mayReadAuditDestinations(p) {
		return api.AuditDestination{}, ErrForbidden
	}
	d, err := s.auditDestination(ctx, id)
	return presentAuditDestination(d), err
}

func (s *Service) auditDestination(ctx context.Context, id string) (store.AuditDestination, error) {
	d, err := s.Store.GetAuditDestination(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return d, ErrNotFound
	}
	return d, err
}

// SaveAuditDestination creates (id empty) or replaces a destination.
func (s *Service) SaveAuditDestination(ctx context.Context, p *auth.Principal, id string, w api.AuditDestinationWrite) (api.AuditDestination, error) {
	if !p.Everywhere(auth.IntegrationsManage) {
		return api.AuditDestination{}, ErrForbidden
	}
	action := "settings.audit_destination.create"
	var before *store.AuditDestination
	if id != "" {
		b, err := s.auditDestination(ctx, id)
		if err != nil {
			return api.AuditDestination{}, err
		}
		before, action = &b, "settings.audit_destination.update"
	} else {
		id = ids.New(ids.AuditDestination)
	}
	d := store.AuditDestination{ID: id, Name: strings.TrimSpace(w.Name), Enabled: w.Enabled == nil || *w.Enabled, Categories: []string{}}
	var errs []api.FieldError
	if d.Name == "" || utf8.RuneCountInString(d.Name) > 80 {
		errs = append(errs, fieldErr("/name", "required", "Give the destination a name of at most 80 characters."))
	}
	if w.Categories != nil {
		for i, c := range *w.Categories {
			if !c.Valid() {
				errs = append(errs, fieldErr("/categories/"+strconv.Itoa(i), "enum", "Not a category: "+string(c)+"."))
			} else if !slices.Contains(d.Categories, string(c)) {
				d.Categories = append(d.Categories, string(c))
			}
		}
		slices.Sort(d.Categories)
	}
	var settings any
	switch {
	case (w.Syslog == nil) == (w.Webhook == nil):
		errs = append(errs, fieldErr("/syslog", "one_of", "Give a syslog server or a webhook, one of them."))
	case w.Syslog != nil:
		c, more, err := s.checkSyslog(ctx, *w.Syslog)
		if err != nil {
			return api.AuditDestination{}, err
		}
		d.Kind, settings, errs = "syslog", c, append(errs, more...)
	default:
		c, more, err := s.checkAuditWebhook(ctx, &d, before, *w.Webhook)
		if err != nil {
			return api.AuditDestination{}, err
		}
		d.Kind, settings, errs = "webhook", c, append(errs, more...)
	}
	if len(errs) > 0 {
		return api.AuditDestination{}, &ValidationError{Errors: errs}
	}
	d.Settings, _ = json.Marshal(settings)
	if err := s.Store.SaveAuditDestination(ctx, d); store.IsUniqueViolation(err) {
		return api.AuditDestination{}, &ValidationError{Errors: []api.FieldError{
			fieldErr("/name", "taken", "Another destination is called "+d.Name+".")}}
	} else if err != nil {
		return api.AuditDestination{}, err
	}
	s.audit(ctx, p, action, "audit_destination", id, nil, map[string]any{"name": d.Name, "kind": d.Kind,
		"enabled": d.Enabled, "categories": d.Categories, "settings": shownSettings(d)})
	saved, err := s.auditDestination(ctx, id)
	return presentAuditDestination(saved), err
}

// checkSyslog checks a syslog server and fills what it leaves out.
func (s *Service) checkSyslog(ctx context.Context, c api.AuditSyslog) (api.AuditSyslog, []api.FieldError, error) {
	var errs []api.FieldError
	c.Host = strings.TrimSpace(c.Host)
	if c.Host == "" || len(c.Host) > 253 || (net.ParseIP(c.Host) == nil && !hostName.MatchString(c.Host)) {
		errs = append(errs, fieldErr("/syslog/host", "invalid", "A host name or an IP address, such as siem.corp.example."))
	}
	if c.Transport != nil && !c.Transport.Valid() {
		errs = append(errs, fieldErr("/syslog/transport", "enum", "tls, tcp or udp."))
		c.Transport = nil
	}
	if c.Port != nil && (*c.Port < 1 || *c.Port > 65535) {
		errs = append(errs, fieldErr("/syslog/port", "range", "A port from 1 to 65535."))
	}
	if c.Facility != nil && !c.Facility.Valid() {
		errs = append(errs, fieldErr("/syslog/facility", "enum", "A syslog facility, such as local0 or audit."))
		c.Facility = nil
	}
	if c.AppName != nil {
		if a := *c.AppName; a == "" || len(a) > 48 || strings.ContainsFunc(a, func(r rune) bool { return r < 33 || r > 126 }) {
			errs = append(errs, fieldErr("/syslog/app_name", "invalid", "At most 48 printable ASCII characters, no space."))
		}
	}
	c = auditlog.SyslogDefaults(c)
	tlsOnly := func(field, what string) {
		errs = append(errs, fieldErr("/syslog/"+field, "tls_only", what+" is for TLS only."))
	}
	clean := func(v *string) *string {
		if v == nil || strings.TrimSpace(*v) == "" {
			return nil
		}
		t := strings.TrimSpace(*v)
		return &t
	}
	c.CaCertificate, c.ClientCertificate, c.ClientKeySecret = clean(c.CaCertificate), clean(c.ClientCertificate), clean(c.ClientKeySecret)
	isTLS := *c.Transport == api.SyslogTLS
	if c.CaCertificate != nil {
		if !isTLS {
			tlsOnly("ca_certificate", "A CA certificate")
		} else if certs, err := trust.Parse(auditlog.Unfold(*c.CaCertificate)); err != nil || len(certs) == 0 {
			why := "no certificate"
			if err != nil {
				why = err.Error()
			}
			errs = append(errs, fieldErr("/syslog/ca_certificate", "invalid", "Not CA certificates in PEM: "+why+"."))
		}
	}
	switch {
	case c.ClientCertificate == nil && c.ClientKeySecret == nil:
	case !isTLS:
		tlsOnly("client_certificate", "A client certificate")
	case c.ClientCertificate == nil || c.ClientKeySecret == nil:
		errs = append(errs, fieldErr("/syslog/client_certificate", "pair", "Give the client certificate with the stored secret that holds its key."))
	default:
		if _, err := s.Store.GetSecret(ctx, *c.ClientKeySecret); errors.Is(err, store.ErrNotFound) {
			errs = append(errs, fieldErr("/syslog/client_key_secret", "unknown", "There is no stored secret "+*c.ClientKeySecret+"."))
			break
		} else if err != nil {
			return c, nil, err
		}
		if s.AuditLog != nil {
			if _, err := s.AuditLog.ClientCertificate(ctx, *c.ClientCertificate, *c.ClientKeySecret); err != nil {
				errs = append(errs, fieldErr("/syslog/client_certificate", "invalid", "This certificate and key can't be used: "+err.Error()+"."))
			}
		}
	}
	return c, errs, nil
}

// checkAuditWebhook checks a webhook and seals its authentication header's
// value into d. A URL sent back as readers see it keeps the stored one, and
// a value not given the stored one, while the URL's origin is the same.
func (s *Service) checkAuditWebhook(ctx context.Context, d, before *store.AuditDestination, c api.AuditWebhook) (api.AuditWebhook,
	[]api.FieldError, error) {
	var errs []api.FieldError
	c.Url, c.Secret = strings.TrimSpace(c.Url), strings.TrimSpace(c.Secret)
	var stored *api.AuditWebhook
	if before != nil && before.Kind == "webhook" {
		var b api.AuditWebhook
		if json.Unmarshal(before.Settings, &b) == nil {
			stored = &b
		}
	}
	if stored != nil && c.Url == auditlog.RedactURL(stored.Url) {
		c.Url = stored.Url
	}
	u, err := url.Parse(c.Url)
	switch {
	case err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http"):
		errs = append(errs, fieldErr("/webhook/url", "invalid", "A URL such as https://siem.corp.example/rvav."))
	case u.User != nil:
		errs = append(errs, fieldErr("/webhook/url", "credentials",
			"Leave the user name and password out of the URL: auditors would see them. Send the credential in an authentication header."))
	case strings.Contains(u.RawQuery, "="+auditlog.Redacted):
		errs = append(errs, fieldErr("/webhook/url", "redacted",
			"Type the query's values again: they are kept only when the URL is sent back as it is shown."))
	case !hosts.Allowed(s.liveConfig(ctx).WebhookHosts, u):
		errs = append(errs, fieldErr("/webhook/url", "host", u.Host+" is not one of the hosts webhooks may call (Administration › Integrations › Allowed hosts)."))
	}
	more, err := s.sealAuthHeader(d, before, stored, &c, u)
	if err != nil {
		return c, nil, err
	}
	errs = append(errs, more...)
	if c.Secret == "" {
		errs = append(errs, fieldErr("/webhook/secret", "required", "Its calls are signed: give the stored secret to sign them with."))
	} else if _, err := s.Store.GetSecret(ctx, c.Secret); errors.Is(err, store.ErrNotFound) {
		errs = append(errs, fieldErr("/webhook/secret", "unknown", "There is no stored secret "+c.Secret+"."))
	} else if err != nil {
		return c, nil, err
	}
	return c, errs, nil
}

// sealAuthHeader seals the header's value given into d, or keeps the one
// stored while the URL's scheme, host and port stay the same: a value
// isn't sent to a server it wasn't given for.
func (s *Service) sealAuthHeader(d, before *store.AuditDestination, stored, c *api.AuditWebhook, u *url.URL) ([]api.FieldError, error) {
	header, value := strings.TrimSpace(deref(c.AuthHeader)), deref(c.AuthValue)
	c.AuthHeader, c.AuthValue = nil, nil
	if header == "" {
		if value != "" {
			return []api.FieldError{fieldErr("/webhook/auth_header", "required", "Name the header the value goes in, such as Authorization.")}, nil
		}
		return nil, nil
	}
	c.AuthHeader = &header
	lower := strings.ToLower(header)
	switch {
	case !headerName.MatchString(header):
		return []api.FieldError{fieldErr("/webhook/auth_header", "invalid", "A header's name: letters, digits and - at most 64.")}, nil
	case slices.Contains(ownHeaders, lower) || strings.HasPrefix(lower, "x-rvav-"):
		return []api.FieldError{fieldErr("/webhook/auth_header", "reserved", "rvav sets "+header+" itself: name another header.")}, nil
	}
	if value == "" {
		if before != nil && before.AuthCiphertext != nil && stored != nil && sameOrigin(stored.Url, u) {
			d.AuthKeyID, d.AuthNonce, d.AuthCiphertext = before.AuthKeyID, before.AuthNonce, before.AuthCiphertext
			return nil, nil
		}
		return []api.FieldError{fieldErr("/webhook/auth_value", "required", "Give the header's value: a new URL's server doesn't get the one kept.")}, nil
	}
	if len(value) > 4096 || strings.ContainsFunc(value, func(r rune) bool { return r < 0x20 && r != '\t' || r == 0x7f }) {
		return []api.FieldError{fieldErr("/webhook/auth_value", "invalid", "At most 4,096 characters, on one line.")}, nil
	}
	if s.Secrets == nil {
		return nil, &ConflictError{Msg: "rvav has no secrets key (RVAV_SECRETS_KEY) to keep the header's value."}
	}
	var err error
	d.AuthKeyID, d.AuthNonce, d.AuthCiphertext, err = s.Secrets.Seal(auditlog.AuthSecretName(d.ID), []byte(value))
	return nil, err
}

// sameOrigin says whether a stored URL has u's scheme, host and port.
func sameOrigin(stored string, u *url.URL) bool {
	was, err := url.Parse(stored)
	return err == nil && u != nil && strings.EqualFold(was.Scheme, u.Scheme) && strings.EqualFold(was.Host, u.Host)
}

func (s *Service) DeleteAuditDestination(ctx context.Context, p *auth.Principal, id string) error {
	if !p.Everywhere(auth.IntegrationsManage) {
		return ErrForbidden
	}
	d, err := s.auditDestination(ctx, id)
	if err != nil {
		return err
	}
	if err := s.Store.DeleteAuditDestination(ctx, id); err != nil {
		return err
	}
	s.auditGone(ctx, p, "settings.audit_destination.delete", "audit_destination", id, d.Name, nil, map[string]any{"name": d.Name,
		"kind": d.Kind, "dropped": d.Backlog + d.SetAside})
	return nil
}

// TestAuditDestination sends a made-up event to a destination, now, once.
func (s *Service) TestAuditDestination(ctx context.Context, p *auth.Principal, id string) (api.AuditDestinationTest, error) {
	if !p.Everywhere(auth.IntegrationsManage) {
		return api.AuditDestinationTest{}, ErrForbidden
	}
	d, err := s.auditDestination(ctx, id)
	if err != nil {
		return api.AuditDestinationTest{}, err
	}
	if s.AuditLog == nil {
		return api.AuditDestinationTest{}, &ConflictError{Msg: "This rvav can't send audit events."}
	}
	const action = "settings.audit_destination.test"
	ev := auditlog.Event{ID: "test", At: time.Now().UTC(), Category: store.AuditCategory(action), Action: action,
		Actor: &api.UserRef{Id: p.User.ID, Name: p.User.Name}, TargetType: "audit_destination", TargetID: d.ID,
		Detail: json.RawMessage(`{"test":true}`)}
	start := time.Now()
	msg, err := s.AuditLog.Send(ctx, d, []auditlog.Event{ev})
	out := api.AuditDestinationTest{Ok: err == nil, Message: msg, DurationMs: int(time.Since(start).Milliseconds())}
	if err != nil {
		out.Message = err.Error()
	}
	s.audit(ctx, p, action, "audit_destination", d.ID, nil, map[string]any{"name": d.Name, "ok": out.Ok})
	return out, nil
}
