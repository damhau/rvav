package e2e

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"
	"go.temporal.io/sdk/testsuite"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/config"
	"github.com/damhau/rvav/internal/notify"
)

const slackSigning = "slack-signing-secret"

// fakeSlack speaks what rvav uses of Slack's Web API, and keeps what rvav
// sent: messages, updates, dialogs opened, answers to response URLs.
type fakeSlack struct {
	*httptest.Server
	mu       sync.Mutex
	users    map[string]string // email -> Slack user ID
	posts    []map[string]string
	updates  []map[string]string
	views    []map[string]string
	answers  []map[string]any
	timestep int
}

func startSlack(t *testing.T) *fakeSlack {
	f := &fakeSlack{users: map[string]string{"marc.keller@corp.example": "U-MKELLER"}}
	ok := func(w http.ResponseWriter, v map[string]any) {
		v["ok"] = true
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	form := func(r *http.Request) map[string]string {
		_ = r.ParseForm()
		out := map[string]string{}
		for k := range r.Form {
			out[k] = r.Form.Get(k)
		}
		return out
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth.test", func(w http.ResponseWriter, _ *http.Request) { ok(w, map[string]any{"team": "Corp"}) })
	mux.HandleFunc("/api/users.lookupByEmail", func(w http.ResponseWriter, r *http.Request) {
		id, found := f.users[form(r)["email"]]
		if !found {
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "users_not_found"})
			return
		}
		ok(w, map[string]any{"user": map[string]any{"id": id}})
	})
	mux.HandleFunc("/api/conversations.open", func(w http.ResponseWriter, r *http.Request) {
		ok(w, map[string]any{"channel": map[string]any{"id": "D-" + form(r)["users"]}})
	})
	mux.HandleFunc("/api/chat.postMessage", func(w http.ResponseWriter, r *http.Request) {
		p := form(r)
		f.mu.Lock()
		f.timestep++
		p["ts"] = fmt.Sprintf("1700000000.%06d", f.timestep)
		f.posts = append(f.posts, p)
		f.mu.Unlock()
		ok(w, map[string]any{"channel": p["channel"], "ts": p["ts"]})
	})
	mux.HandleFunc("/api/chat.update", func(w http.ResponseWriter, r *http.Request) {
		p := form(r)
		f.mu.Lock()
		f.updates = append(f.updates, p)
		f.mu.Unlock()
		ok(w, map[string]any{"channel": p["channel"], "ts": p["ts"], "text": p["text"]})
	})
	mux.HandleFunc("/api/views.open", func(w http.ResponseWriter, r *http.Request) {
		// Sent as JSON, not as a form.
		var body struct {
			TriggerID string          `json:"trigger_id"`
			View      json.RawMessage `json:"view"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		p := map[string]string{"trigger_id": body.TriggerID, "view": string(body.View)}
		f.mu.Lock()
		f.views = append(f.views, p)
		f.mu.Unlock()
		ok(w, map[string]any{"view": map[string]any{"id": "V1"}})
	})
	mux.HandleFunc("/respond/", func(w http.ResponseWriter, r *http.Request) {
		var m map[string]any
		_ = json.NewDecoder(r.Body).Decode(&m)
		f.mu.Lock()
		f.answers = append(f.answers, m)
		f.mu.Unlock()
		_, _ = io.WriteString(w, "ok")
	})
	f.Server = httptest.NewServer(mux)
	t.Cleanup(f.Close)
	return f
}

// interact is Slack calling rvav with an interaction, signed with secret
// at the given time.
func (f *fakeSlack) interact(t *testing.T, h *harness, payload map[string]any, secret string, at time.Time) (int, string) {
	t.Helper()
	b, _ := json.Marshal(payload)
	body := "payload=" + url.QueryEscape(string(b))
	ts := strconv.FormatInt(at.Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("v0:" + ts + ":" + body))
	req, _ := http.NewRequestWithContext(h.ctx, http.MethodPost, h.srv.URL+"/api/v1/integrations/slack/interactions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Slack-Request-Timestamp", ts)
	req.Header.Set("X-Slack-Signature", "v0="+hex.EncodeToString(mac.Sum(nil)))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(out)
}

// buttons are the actions of a message rvav posted, by ID.
func buttons(t *testing.T, post map[string]string) map[string]string {
	var blocks []map[string]any
	if err := json.Unmarshal([]byte(post["blocks"]), &blocks); err != nil {
		t.Fatalf("blocks %q: %v", post["blocks"], err)
	}
	out := map[string]string{}
	for _, b := range blocks {
		if els, ok := b["elements"].([]any); ok {
			for _, e := range els {
				m := e.(map[string]any)
				v, _ := m["value"].(string)
				out[m["action_id"].(string)] = v
			}
		}
	}
	return out
}

// TestSlackNotifications: approvers get a direct message in Slack, found by
// email, with Approve and Reject; Reject asks for the comment in a dialog;
// only the person it was sent to, in a request signed by Slack, decides
// (S11.4, ADR-0023).
func TestSlackNotifications(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		sl := startSlack(t)
		cfg := config.SlackConfig{BotToken: "xoxb-test", SigningSecret: slackSigning, APIURL: sl.URL + "/api/"}
		h.svc.Events.Channels = []string{"slack"}
		h.api.Slack = &notify.Slack{Config: cfg, Store: h.store}
		n := &notify.Notifier{Store: h.store, Log: h.svc.Log, PublicURL: "https://rvav.corp.example",
			Senders: map[string]notify.Sender{"slack": &notify.Slack{Config: cfg, Store: h.store}}}
		send := func() {
			t.Helper()
			if _, err := n.Once(ctx); err != nil {
				t.Fatal(err)
			}
		}
		var item api.CatalogItem
		h.json("GET", "/api/v1/catalog/items/rhel9-server?project_id=prj_web", "tnguyen", nil, 200, &item)
		request := func(role string, decide func(env *testsuite.TestWorkflowEnvironment, value string)) api.Request {
			t.Helper()
			var req api.Request
			h.json("POST", "/api/v1/requests", "tnguyen", api.RequestDraft{CatalogItemId: item.Id, VersionId: item.Version.Id,
				ProjectId: "prj_web", Environment: "prd", Inputs: map[string]any{"role": role, "size": "XL",
					"cluster": "GVA-PRD-CL02", "datastore": "vsan-gva-prd-cl02", "network": "dvpg-web-prd-vl210",
					"post_config": []string{"linux-baseline"}}}, 202, &req, "Idempotency-Key", uuid.NewString())
			h.runWorkflow(req.Id, func(env *testsuite.TestWorkflowEnvironment) {
				env.RegisterDelayedCallback(func() {
					send()
					sl.mu.Lock()
					last := sl.posts[len(sl.posts)-1]
					sl.mu.Unlock()
					if last["channel"] != "D-U-MKELLER" || !strings.Contains(last["blocks"], req.Number) {
						t.Errorf("the message = %v", last)
						return
					}
					b := buttons(t, last)
					if b[notify.SlackApprove] == "" || b[notify.SlackReject] != b[notify.SlackApprove] {
						t.Errorf("buttons = %v", b)
						return
					}
					decide(env, b[notify.SlackApprove])
				}, time.Minute)
			})
			h.json("GET", "/api/v1/requests/"+req.Id, "tnguyen", nil, 200, &req)
			return req
		}
		dialog := func(user, comment, meta string) map[string]any {
			return map[string]any{"type": "view_submission", "user": map[string]any{"id": user},
				"view": map[string]any{"callback_id": notify.SlackDialog, "private_metadata": meta,
					"state": map[string]any{"values": map[string]any{"comment": map[string]any{"comment": map[string]any{
						"type": "plain_text_input", "value": comment}}}}}}
		}

		// Rejected from Slack: the dialog asks for the comment, and the message says who rejected it.
		rejected := request("batch", func(env *testsuite.TestWorkflowEnvironment, value string) {
			press := map[string]any{"type": "block_actions", "user": map[string]any{"id": "U-MKELLER"}, "trigger_id": "T1",
				"response_url": sl.URL + "/respond/1", "container": map[string]any{"channel_id": "D-U-MKELLER",
					"message_ts": "1700000000.000001"}, "actions": []any{map[string]any{"block_id": "rvav_decide", "action_id": notify.SlackReject, "value": value}}}
			if code, _ := sl.interact(t, h, press, "wrong-secret", time.Now()); code != 401 {
				t.Errorf("a request signed with another secret = %d", code)
			}
			if code, _ := sl.interact(t, h, press, slackSigning, time.Now().Add(-10*time.Minute)); code != 401 {
				t.Errorf("a request ten minutes old = %d", code)
			}
			if code, _ := sl.interact(t, h, press, slackSigning, time.Now()); code != 200 {
				t.Errorf("Reject = %d", code)
			}
			sl.mu.Lock()
			opened := sl.views
			sl.mu.Unlock()
			if len(opened) != 1 || opened[0]["trigger_id"] != "T1" {
				t.Errorf("dialogs opened = %v", opened)
				return
			}
			var view struct {
				PrivateMetadata string `json:"private_metadata"`
			}
			_ = json.Unmarshal([]byte(opened[0]["view"]), &view)
			if _, body := sl.interact(t, h, dialog("U-MKELLER", "", view.PrivateMetadata), slackSigning, time.Now()); !strings.Contains(body, `"response_action":"errors"`) ||
				!strings.Contains(body, "Say why you reject it") {
				t.Errorf("a rejection without a comment = %s", body)
			}
			if _, body := sl.interact(t, h, dialog("U-SOMEONE", "No", view.PrivateMetadata), slackSigning, time.Now()); !strings.Contains(body, "sent to someone else") {
				t.Errorf("someone else = %s", body)
			}
			if code, body := sl.interact(t, h, dialog("U-MKELLER", "Use the batch cluster", view.PrivateMetadata), slackSigning, time.Now()); code != 200 || body != "" {
				t.Errorf("the dialog sent = %d %s", code, body)
			}
			sl.mu.Lock()
			updated := sl.updates
			sl.mu.Unlock()
			if len(updated) != 1 || updated[0]["ts"] != "1700000000.000001" || !strings.Contains(updated[0]["blocks"], "Rejected by Marc Keller") {
				t.Errorf("the message updated = %v", updated)
			}
			h.wf.deliver(env)
		})
		if rejected.Status != api.RequestStatusRejected {
			t.Fatalf("rejected from Slack = %s", rejected.Status)
		}

		// Approved from Slack with the button.
		approved := request("front", func(env *testsuite.TestWorkflowEnvironment, value string) {
			press := map[string]any{"type": "block_actions", "user": map[string]any{"id": "U-MKELLER"},
				"response_url": sl.URL + "/respond/2", "actions": []any{map[string]any{"block_id": "rvav_decide", "action_id": notify.SlackApprove, "value": value}}}
			if code, _ := sl.interact(t, h, press, slackSigning, time.Now()); code != 200 {
				t.Errorf("Approve = %d", code)
			}
			if code, _ := sl.interact(t, h, press, slackSigning, time.Now()); code != 200 {
				t.Errorf("Approve again = %d", code)
			}
			sl.mu.Lock()
			answers := sl.answers
			sl.mu.Unlock()
			if len(answers) != 2 || answers[0]["replace_original"] != true || !strings.Contains(fmt.Sprint(answers[0]["blocks"]), "Approved by Marc Keller") ||
				answers[1]["response_type"] != "ephemeral" || !strings.Contains(fmt.Sprint(answers[1]["text"]), "already decided") {
				t.Errorf("answers = %v", answers)
			}
			h.wf.deliver(env)
		})
		if approved.Status != api.RequestStatusSucceeded {
			t.Fatalf("approved from Slack = %s", approved.Status)
		}
		var channels []string
		rows, _ := h.store.Pool.Query(ctx, `select detail->>'channel' from audit_events where action in ('approval.approved', 'approval.rejected') order by at`)
		for rows.Next() {
			var ch string
			_ = rows.Scan(&ch)
			channels = append(channels, ch)
		}
		rows.Close()
		if strings.Join(channels, ",") != "slack,slack" {
			t.Fatalf("decisions audited with channels %v", channels)
		}
	})
}
