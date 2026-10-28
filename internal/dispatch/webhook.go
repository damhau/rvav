package dispatch

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/damhau/rvav/internal/hosts"
	"github.com/damhau/rvav/internal/secrets"
	"github.com/damhau/rvav/internal/store"
)

// SecretFunc opens a stored secret by name.
type SecretFunc func(ctx context.Context, name string) (string, error)

// StoredSecret opens secrets of the secret store (ADR-0019).
func StoredSecret(st *store.Store, box *secrets.Box) SecretFunc {
	return func(ctx context.Context, name string) (string, error) {
		if box == nil {
			return "", secrets.ErrNoKey
		}
		v, err := st.GetSecret(ctx, name)
		if err != nil {
			return "", fmt.Errorf("secret %s: %w", name, err)
		}
		plain, err := box.Open(v.Name, v.KeyID, v.Nonce, v.Ciphertext)
		return string(plain), err
	}
}

// Webhook posts events, signed, to allowed hosts.
type Webhook struct {
	// Hosts are RVAV_WEBHOOK_HOSTS: no list, no webhooks.
	Hosts  []string
	Secret SecretFunc
	Client *http.Client
}

// WebhookTarget is a subscription's webhook.
type WebhookTarget struct {
	URL    string `json:"url"`
	Secret string `json:"secret"`
}

// Sign gives the X-Rvav-Signature header of a body: the time, and the
// HMAC-SHA256 of "<time>.<body>" under the secret.
func Sign(secret string, at time.Time, body []byte) string {
	ts := strconv.FormatInt(at.Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + "."))
	mac.Write(body)
	return "t=" + ts + ",v1=" + hex.EncodeToString(mac.Sum(nil))
}

func (w *Webhook) client() *http.Client {
	if w.Client != nil {
		return w.Client
	}
	// No redirects: an allowed host can't send the event elsewhere.
	return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func (w *Webhook) Deliver(ctx context.Context, d Delivery) Result {
	sub, event := d.Sub, d.Event
	var t struct {
		Webhook *WebhookTarget `json:"webhook"`
	}
	if err := json.Unmarshal(sub.Target, &t); err != nil || t.Webhook == nil {
		return Result{Err: "not a webhook"}
	}
	u, err := url.Parse(t.Webhook.URL)
	if err != nil {
		return Result{Err: "Not a URL: " + err.Error()}
	}
	if !hosts.Allowed(w.Hosts, u) {
		return Result{Err: u.Host + " is not in RVAV_WEBHOOK_HOSTS."}
	}
	secret, err := w.Secret(ctx, t.Webhook.Secret)
	if err != nil {
		return Result{Err: "The signing secret can't be read: " + err.Error()}
	}
	var head struct {
		ID    string `json:"id"`
		Topic string `json:"topic"`
	}
	_ = json.Unmarshal(event, &head)
	ctx, cancel := context.WithTimeout(ctx, time.Duration(sub.TimeoutSeconds)*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(event))
	if err != nil {
		return Result{Err: err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "rvav")
	req.Header.Set("X-Rvav-Event-Id", head.ID)
	req.Header.Set("X-Rvav-Topic", head.Topic)
	req.Header.Set("X-Rvav-Signature", Sign(secret, time.Now(), event))
	resp, err := w.client().Do(req)
	if err != nil {
		return Result{Err: err.Error()}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, MaxResponse))
	res := Result{StatusCode: resp.StatusCode, Response: string(body), OK: resp.StatusCode >= 200 && resp.StatusCode < 300}
	if !res.OK {
		res.Err = fmt.Sprintf("%s answered %d", u.Host, resp.StatusCode)
		return res
	}
	if d.Blocking {
		return answer(res)
	}
	return res
}

// answer reads a blocking target's JSON answer; an empty one continues.
func answer(res Result) Result {
	res.Outcome = "continue"
	if len(bytes.TrimSpace([]byte(res.Response))) == 0 {
		return res
	}
	var a struct {
		Outcome    string            `json:"outcome"`
		Reason     string            `json:"reason"`
		Properties map[string]string `json:"properties"`
	}
	if err := json.Unmarshal([]byte(res.Response), &a); err != nil {
		res.OK, res.Err = false, "The answer isn't JSON: "+err.Error()
		return res
	}
	switch a.Outcome {
	case "", "continue":
		res.Properties = a.Properties
	case "stop":
		res.Outcome, res.Reason = "stop", a.Reason
		if res.Reason == "" {
			res.Reason = "Stopped by the subscription, without a reason."
		}
	default:
		res.OK, res.Err = false, fmt.Sprintf("The answer's outcome %q is neither continue nor stop.", a.Outcome)
	}
	return res
}
