package e2e

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"
	"go.temporal.io/sdk/testsuite"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/config"
	"github.com/damhau/rvav/internal/notify"
)

// fakeTeams speaks what rvav uses of Microsoft: sign-in, Graph's users,
// the Bot Connector, and the Bot Framework's OpenID configuration, whose
// key signs the tokens of the calls it makes to rvav.
type fakeTeams struct {
	*httptest.Server
	key      *rsa.PrivateKey
	users    map[string]string // email -> Entra object ID
	mu       sync.Mutex
	cards    map[string][]map[string]any // conversation -> cards sent
	scopes   []string
	convWith map[string]string // conversation -> member
}

const botFramework = "https://api.botframework.com"

func startTeams(t *testing.T) *fakeTeams {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeTeams{key: key, users: map[string]string{"marc.keller@corp.example": "oid-mkeller",
		"thomas.nguyen@corp.example": "oid-tnguyen"}, cards: map[string][]map[string]any{}, convWith: map[string]string{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /openid", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"issuer": botFramework, "jwks_uri": f.URL + "/keys",
			"id_token_signing_alg_values_supported": []string{"RS256"}})
	})
	mux.HandleFunc("GET /keys", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "k1",
			Algorithm: "RS256", Use: "sig"}}})
	})
	mux.HandleFunc("POST /tenant-1/oauth2/v2.0/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.mu.Lock()
		f.scopes = append(f.scopes, r.Form.Get("scope"))
		f.mu.Unlock()
		if r.Form.Get("client_secret") != "app-secret" {
			http.Error(w, `{"error":"invalid_client"}`, http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "tok", "token_type": "Bearer", "expires_in": 3600})
	})
	mux.HandleFunc("GET /v1.0/users/{email}", func(w http.ResponseWriter, r *http.Request) {
		id, ok := f.users[r.PathValue("email")]
		if !ok {
			http.Error(w, `{"error":{"code":"Request_ResourceNotFound"}}`, http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"id": id})
	})
	mux.HandleFunc("POST /smba/v3/conversations", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Members []struct {
				ID string `json:"id"`
			} `json:"members"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		conv := "a:conv-" + body.Members[0].ID
		f.mu.Lock()
		f.convWith[conv] = body.Members[0].ID
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]string{"id": conv})
	})
	mux.HandleFunc("POST /smba/v3/conversations/{conv}/activities", func(w http.ResponseWriter, r *http.Request) {
		var act struct {
			Attachments []struct {
				Content map[string]any `json:"content"`
			} `json:"attachments"`
		}
		_ = json.NewDecoder(r.Body).Decode(&act)
		f.mu.Lock()
		conv := r.PathValue("conv")
		f.cards[conv] = append(f.cards[conv], act.Attachments[0].Content)
		n := len(f.cards[conv])
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]string{"id": fmt.Sprintf("act-%d", n)})
	})
	f.Server = httptest.NewServer(mux)
	t.Cleanup(f.Close)
	return f
}

func (f *fakeTeams) cardsFor(oid string) []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cards["a:conv-"+oid]
}

// token is a Bot Framework token for a call naming serviceURL, signed with
// key (the fake's own unless given).
func (f *fakeTeams) token(t *testing.T, serviceURL string, key *rsa.PrivateKey) string {
	if key == nil {
		key = f.key
	}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: jose.JSONWebKey{Key: key, KeyID: "k1"}},
		(&jose.SignerOptions{}).WithType("JWT"))
	if err != nil {
		t.Fatal(err)
	}
	claims, _ := json.Marshal(map[string]any{"iss": botFramework, "aud": "app-1", "exp": time.Now().Add(5 * time.Minute).Unix(),
		"nbf": time.Now().Add(-time.Minute).Unix(), "serviceurl": serviceURL})
	jws, err := signer.Sign(claims)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := jws.CompactSerialize()
	return raw
}

// press is someone pressing a card's button in Teams.
func (f *fakeTeams) press(t *testing.T, h *harness, oid, verb string, data map[string]any, token string) (int, map[string]any) {
	t.Helper()
	act := map[string]any{"type": "invoke", "name": "adaptiveCard/action", "serviceUrl": f.URL + "/smba",
		"from":  map[string]any{"id": "29:" + oid, "aadObjectId": oid},
		"value": map[string]any{"action": map[string]any{"type": "Action.Execute", "verb": verb, "data": data}}}
	b, _ := json.Marshal(act)
	req, _ := http.NewRequestWithContext(h.ctx, http.MethodPost, h.srv.URL+"/api/v1/integrations/teams/messages", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// cardText is all a card's text blocks.
func cardText(card map[string]any) string {
	var parts []string
	body, _ := card["body"].([]any)
	for _, b := range body {
		if m, ok := b.(map[string]any); ok {
			if s, ok := m["text"].(string); ok {
				parts = append(parts, s)
			}
		}
	}
	return strings.Join(parts, " | ")
}

// TestTeamsNotifications: approvers get an Adaptive Card in Teams, found by
// their email, and approve or reject from it as the inbox does; only the
// person it was sent to, with a token from the Bot Framework for that
// service, decides (S11.3, ADR-0023).
func TestTeamsNotifications(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		teams := startTeams(t)
		h.svc.Events.Channels = []string{"teams"}
		h.api.Teams = &notify.TeamsVerifier{AppID: "app-1", OpenIDURL: teams.URL + "/openid"}
		n := &notify.Notifier{Store: h.store, Log: h.svc.Log, PublicURL: "https://rvav.corp.example",
			Senders: map[string]notify.Sender{"teams": &notify.Teams{Store: h.store, Config: config.TeamsConfig{AppID: "app-1",
				TenantID: "tenant-1", AppPassword: "app-secret", ServiceURL: teams.URL + "/smba", LoginURL: teams.URL,
				GraphURL: teams.URL}}}}
		send := func() {
			t.Helper()
			if _, err := n.Once(ctx); err != nil {
				t.Fatal(err)
			}
		}

		var item api.CatalogItem
		h.json("GET", "/api/v1/catalog/items/rhel9-server?project_id=prj_web", "tnguyen", nil, 200, &item)
		var req api.Request
		h.json("POST", "/api/v1/requests", "tnguyen", api.RequestDraft{CatalogItemId: item.Id, VersionId: item.Version.Id,
			ProjectId: "prj_web", Environment: "prd", Inputs: map[string]any{"role": "front", "size": "XL",
				"cluster": "GVA-PRD-CL02", "datastore": "vsan-gva-prd-cl02", "network": "dvpg-web-prd-vl210",
				"post_config": []string{"linux-baseline"}}}, 202, &req, "Idempotency-Key", uuid.NewString())
		h.runWorkflow(req.Id, func(env *testsuite.TestWorkflowEnvironment) {
			env.RegisterDelayedCallback(func() {
				send()
				cards := teams.cardsFor("oid-mkeller")
				if len(cards) != 1 {
					var status, why string
					_ = h.store.Pool.QueryRow(ctx, `select d.status, coalesce(d.error, '') from notification_deliveries d
						join notifications n on n.id = d.notification_id where n.user_id = 'usr_mkeller'`).Scan(&status, &why)
					t.Errorf("Marc got %d cards (%s: %s)", len(cards), status, why)
					return
				}
				card := cards[0]
				actions, _ := card["actions"].([]any)
				if len(actions) != 3 || !strings.Contains(cardText(card), "Approval needed") ||
					!strings.Contains(fmt.Sprint(card["body"]), req.Number) {
					t.Errorf("the approval card = %v", card)
					return
				}
				data := actions[0].(map[string]any)["data"].(map[string]any)
				approvalID := data["approval_id"].(string)
				good := teams.token(t, teams.URL+"/smba", nil)

				// Calls that aren't Teams', or not for this service, are refused.
				other, _ := rsa.GenerateKey(rand.Reader, 2048)
				if code, _ := teams.press(t, h, "oid-mkeller", "approve", data, teams.token(t, teams.URL+"/smba", other)); code != 401 {
					t.Errorf("a token signed by another key = %d", code)
				}
				if code, _ := teams.press(t, h, "oid-mkeller", "approve", data, teams.token(t, "https://evil.example/", nil)); code != 401 {
					t.Errorf("a token for another service = %d", code)
				}
				if code, _ := teams.press(t, h, "oid-mkeller", "approve", data, ""); code != 401 {
					t.Errorf("no token = %d", code)
				}
				// Someone it wasn't sent to; a rejection without a comment.
				code, out := teams.press(t, h, "oid-tnguyen", "approve", data, good)
				if code != 200 || !strings.Contains(cardText(out["value"].(map[string]any)), "sent to someone else") {
					t.Errorf("someone else = %d %v", code, out)
				}
				_, out = teams.press(t, h, "oid-mkeller", "reject", data, good)
				if !strings.Contains(cardText(out["value"].(map[string]any)), "Say why you reject it") {
					t.Errorf("a rejection without a comment = %v", out)
				}
				// Approved from Teams, with a comment typed on the card.
				withComment := map[string]any{}
				for k, v := range data {
					withComment[k] = v
				}
				withComment["comment"] = "Fine for Black Friday"
				_, out = teams.press(t, h, "oid-mkeller", "approve", withComment, good)
				if v, _ := out["value"].(map[string]any); !strings.Contains(cardText(v), "Approved by Marc Keller") {
					t.Errorf("approved from Teams = %v", out)
				}
				_, out = teams.press(t, h, "oid-mkeller", "reject", withComment, good)
				if v, _ := out["value"].(map[string]any); !strings.Contains(cardText(v), "already decided") {
					t.Errorf("decided twice = %v", out)
				}
				var detail api.Approval
				h.json("GET", "/api/v1/approvals/"+approvalID, "mkeller", nil, 200, &detail)
				if detail.Status != api.ApprovalStatusApproved {
					t.Errorf("the approval = %s", detail.Status)
				}
				var channel, comment string
				_ = h.store.Pool.QueryRow(ctx, `select detail->>'channel', detail->>'comment' from audit_events
					where action = 'approval.approved' and target_id = $1`, approvalID).Scan(&channel, &comment)
				if channel != "teams" || comment != "Fine for Black Friday" {
					t.Errorf("audited: channel %q, comment %q", channel, comment)
				}
				h.wf.deliver(env)
			}, time.Minute)
		})
		h.json("GET", "/api/v1/requests/"+req.Id, "tnguyen", nil, 200, &req)
		if req.Status != api.RequestStatusSucceeded {
			t.Fatalf("the request approved from Teams = %s", req.Status)
		}
		if teams.convWith["a:conv-oid-mkeller"] != "oid-mkeller" || !strings.Contains(strings.Join(teams.scopes, " "), "api.botframework.com") ||
			!strings.Contains(strings.Join(teams.scopes, " "), "graph.microsoft.com") {
			t.Fatalf("conversations %v, scopes %v", teams.convWith, teams.scopes)
		}

		// Someone not in the tenant: kept in the app, not retried.
		h.svc.Events.Notify(ctx, newNotification("usr_lmartin", "approval_needed", "test:teams-unknown"))
		send()
		var status, why string
		_ = h.store.Pool.QueryRow(ctx, `select d.status, d.error from notification_deliveries d join notifications n
			on n.id = d.notification_id where n.dedupe_key = 'test:teams-unknown'`).Scan(&status, &why)
		if status != "failed" || !strings.Contains(why, "not in the tenant's directory") {
			t.Fatalf("someone not in the tenant = %s %s", status, why)
		}
	})
}
