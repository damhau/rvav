package auditlog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/dispatch"
	"github.com/damhau/rvav/internal/fips"
	"github.com/damhau/rvav/internal/hosts"
	"github.com/damhau/rvav/internal/store"
)

// Body is what a webhook gets: a batch of events, oldest first.
type Body struct {
	Contract string  `json:"contract"`
	Events   []Event `json:"events"`
}

type webhookSender struct {
	f                     *Forwarder
	url                   *url.URL
	secret                string
	authHeader, authValue string
}

// Redacted is what readers see of a webhook's URL: each value of its query
// is REDACTED, and so is a password in it.
const Redacted = "REDACTED"

// RedactURL hides the values of a URL's query, which some SIEMs take a
// token in, keeping their order; and a password in it.
func RedactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	if u.User != nil {
		if _, ok := u.User.Password(); ok {
			u.User = url.UserPassword(u.User.Username(), Redacted)
		}
	}
	if u.RawQuery != "" {
		pairs := strings.Split(u.RawQuery, "&")
		for i, p := range pairs {
			if k, v, ok := strings.Cut(p, "="); ok && v != "" {
				pairs[i] = k + "=" + Redacted
			}
		}
		u.RawQuery = strings.Join(pairs, "&")
	}
	u.Fragment, u.RawFragment = "", ""
	return u.String()
}

// AuthSecretName is what a destination's header value is sealed under.
func AuthSecretName(id string) string { return "audit_destination:" + id }

func (f *Forwarder) webhook(ctx context.Context, d store.AuditDestination, c api.AuditWebhook) (sender, error) {
	u, err := url.Parse(c.Url)
	if err != nil {
		return nil, fmt.Errorf("not a URL: %w", err)
	}
	var allowed []string
	if f.WebhookHosts != nil {
		allowed = f.WebhookHosts(ctx)
	}
	if !hosts.Allowed(allowed, u) {
		return nil, fmt.Errorf("%s is not one of the hosts webhooks may call (Administration › Integrations › Allowed hosts)", u.Host)
	}
	if f.Secret == nil {
		return nil, fmt.Errorf("this rvav can't read stored secrets")
	}
	secret, err := f.Secret(ctx, c.Secret)
	if err != nil {
		return nil, fmt.Errorf("the signing secret can't be read: %w", err)
	}
	if err := fips.CheckHMACKey([]byte(secret)); err != nil {
		return nil, fmt.Errorf("the signing secret %s: %w", c.Secret, err)
	}
	w := &webhookSender{f: f, url: u, secret: secret}
	if h := deref(c.AuthHeader); h != "" && d.AuthCiphertext != nil {
		if f.Box == nil {
			return nil, errors.New("this rvav has no secrets key to open the authentication header's value")
		}
		v, err := f.Box.Open(AuthSecretName(d.ID), d.AuthKeyID, d.AuthNonce, d.AuthCiphertext)
		if err != nil {
			return nil, fmt.Errorf("the authentication header's value can't be opened: %w", err)
		}
		w.authHeader, w.authValue = h, string(v)
	}
	return w, nil
}

func (w *webhookSender) send(ctx context.Context, events []Event) (string, error) {
	body, err := json.Marshal(Body{Contract: Contract, Events: events})
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, w.f.timeout())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.url.String(), bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "rvav")
	req.Header.Set("X-Rvav-Topic", "audit")
	req.Header.Set("X-Rvav-Signature", dispatch.Sign(w.secret, time.Now(), body))
	if w.authHeader != "" {
		req.Header.Set(w.authHeader, w.authValue)
	}
	resp, err := w.f.client().Do(req)
	if err != nil {
		// The URL in the error goes to the destination's state and the log.
		var ue *url.Error
		if errors.As(err, &ue) {
			ue.URL = RedactURL(ue.URL)
		}
		return "", err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	switch {
	case resp.StatusCode == http.StatusRequestEntityTooLarge:
		return "", &tooLargeError{fmt.Sprintf("%s answered 413: too large for it", w.url.Host)}
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return "", fmt.Errorf("%s answered %d", w.url.Host, resp.StatusCode)
	}
	return fmt.Sprintf("%s answered %d.", w.url.Host, resp.StatusCode), nil
}

func (w *webhookSender) oversize(Event) string { return "" }

func (w *webhookSender) close() error { return nil }
