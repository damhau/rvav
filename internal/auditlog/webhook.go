package auditlog

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/dispatch"
	"github.com/damhau/rvav/internal/hosts"
)

// Body is what a webhook gets: a batch of events, oldest first.
type Body struct {
	Contract string  `json:"contract"`
	Events   []Event `json:"events"`
}

type webhookSender struct {
	f      *Forwarder
	url    *url.URL
	secret string
}

func (f *Forwarder) webhook(ctx context.Context, c api.AuditWebhook) (sender, error) {
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
	return &webhookSender{f: f, url: u, secret: secret}, nil
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
	resp, err := w.f.client().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", fmt.Errorf("%s answered %d", w.url.Host, resp.StatusCode)
	}
	return fmt.Sprintf("%s answered %d.", w.url.Host, resp.StatusCode), nil
}

func (w *webhookSender) close() error { return nil }
