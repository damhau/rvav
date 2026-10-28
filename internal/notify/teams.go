package notify

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
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"

	"github.com/damhau/rvav/internal/config"
	"github.com/damhau/rvav/internal/store"
)

// Microsoft Teams through an Azure bot (ADR-0023): rvav finds the user in
// Microsoft Graph by email, opens a one-to-one conversation through the Bot
// Connector and sends an Adaptive Card; approval cards have Approve and
// Reject, which Teams sends back to rvav's messaging endpoint.

// Teams sends notifications to Microsoft Teams.
type Teams struct {
	Config config.TeamsConfig
	Store  *store.Store
	// Client is the HTTP client; http.DefaultClient when nil.
	Client *http.Client

	once       sync.Once
	bot, graph oauth2.TokenSource
}

var errNotFound = errors.New("not found")

func (t *Teams) httpClient() *http.Client {
	if t.Client != nil {
		return t.Client
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (t *Teams) tokens() {
	t.once.Do(func() {
		cc := func(scope string) oauth2.TokenSource {
			c := clientcredentials.Config{ClientID: t.Config.AppID, ClientSecret: t.Config.AppPassword,
				TokenURL: t.Config.LoginURL + "/" + url.PathEscape(t.Config.TenantID) + "/oauth2/v2.0/token", Scopes: []string{scope}}
			return c.TokenSource(context.WithValue(context.Background(), oauth2.HTTPClient, t.httpClient()))
		}
		t.bot, t.graph = cc("https://api.botframework.com/.default"), cc("https://graph.microsoft.com/.default")
	})
}

func (t *Teams) call(ctx context.Context, ts oauth2.TokenSource, method, u string, body, out any) error {
	tok, err := ts.Token()
	if err != nil {
		return fmt.Errorf("sign in as the bot: %w", err)
	}
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := t.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return errNotFound
	case resp.StatusCode >= 300:
		return fmt.Errorf("%s %s answered %d: %s", method, u, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	if out != nil {
		return json.Unmarshal(b, out)
	}
	return nil
}

// identity is the user in Teams, found by their email once, with their
// conversation with the bot.
func (t *Teams) identity(ctx context.Context, d store.Delivery) (store.ChannelIdentity, error) {
	id, err := t.Store.GetChannelIdentity(ctx, d.Notification.UserID, "teams")
	if errors.Is(err, store.ErrNotFound) {
		if d.UserEmail == "" {
			return id, Permanent(errors.New("the user has no email address to find them in Teams"))
		}
		// By mail or sign-in name: they differ for some people.
		var found struct {
			Value []struct {
				ID string `json:"id"`
			} `json:"value"`
		}
		q := strings.ReplaceAll(d.UserEmail, "'", "''")
		filter := url.Values{"$filter": {"mail eq '" + q + "' or userPrincipalName eq '" + q + "'"}, "$select": {"id"}}
		err := t.call(ctx, t.graph, http.MethodGet, t.Config.GraphURL+"/v1.0/users?"+filter.Encode(), nil, &found)
		if errors.Is(err, errNotFound) || err == nil && (len(found.Value) == 0 || found.Value[0].ID == "") {
			return id, Permanent(fmt.Errorf("%s is not in the tenant's directory", d.UserEmail))
		}
		if err != nil {
			return id, err
		}
		id = store.ChannelIdentity{UserID: d.Notification.UserID, Channel: "teams", ExternalID: found.Value[0].ID}
	} else if err != nil {
		return id, err
	}
	if id.ConversationID == nil {
		var conv struct {
			ID string `json:"id"`
		}
		tenant := map[string]any{"id": t.Config.TenantID}
		if err := t.call(ctx, t.bot, http.MethodPost, t.Config.ServiceURL+"/v3/conversations", map[string]any{
			"isGroup": false, "bot": map[string]any{"id": "28:" + t.Config.AppID}, "members": []any{map[string]any{"id": id.ExternalID}},
			"tenantId": t.Config.TenantID, "channelData": map[string]any{"tenant": tenant}}, &conv); err != nil {
			return id, fmt.Errorf("open a conversation with %s: %w", d.UserEmail, err)
		}
		id.ConversationID = &conv.ID
	}
	return id, t.Store.SetChannelIdentity(ctx, id)
}

// Send sends m as an Adaptive Card in the user's conversation with the bot.
func (t *Teams) Send(ctx context.Context, m Message) (string, string, error) {
	t.tokens()
	id, err := t.identity(ctx, m.Delivery)
	if err != nil {
		return "", "", err
	}
	var sent struct {
		ID string `json:"id"`
	}
	conv := *id.ConversationID
	if err := t.call(ctx, t.bot, http.MethodPost, t.Config.ServiceURL+"/v3/conversations/"+url.PathEscape(conv)+"/activities",
		map[string]any{"type": "message", "summary": m.Subject, "attachments": []any{map[string]any{
			"contentType": "application/vnd.microsoft.card.adaptive", "content": Card(m)}}}, &sent); err != nil {
		return id.ExternalID, "", err
	}
	return id.ExternalID, conv + "/" + sent.ID, nil
}

// CardData is what an approval card's buttons send back.
type CardData struct {
	ApprovalID string `json:"approval_id"`
	Locale     string `json:"locale"`
	Title      string `json:"title"`
	Link       string `json:"link"`
}

func adaptive(body, actions []any) map[string]any {
	return map[string]any{"type": "AdaptiveCard", "$schema": "http://adaptivecards.io/schemas/adaptive-card.json",
		"version": "1.4", "body": body, "actions": actions}
}

// markdown keeps titles, reasons and comments from being read as links or
// emphasis in a card's text.
var markdown = strings.NewReplacer(`\`, `\\`, "[", `\[`, "]", `\]`, "(", `\(`, ")", `\)`, "*", `\*`, "_", `\_`,
	"`", "\\`", "#", `\#`, ">", `\>`, "~", `\~`)

func text(s string, extra map[string]any) map[string]any {
	t := map[string]any{"type": "TextBlock", "text": markdown.Replace(s), "wrap": true}
	for k, v := range extra {
		t[k] = v
	}
	return t
}

// Card is a notification as an Adaptive Card: an approval's has a comment
// box, Approve and Reject.
func Card(m Message) map[string]any {
	l := labelsFor(m.Data.Locale)
	body := []any{text(m.Subject, map[string]any{"weight": "Bolder", "size": "Medium"}), text(m.Short, nil)}
	var facts []any
	for _, f := range m.Facts() {
		facts = append(facts, map[string]any{"title": markdown.Replace(f.Label), "value": markdown.Replace(f.Value)})
	}
	if len(facts) > 0 {
		body = append(body, map[string]any{"type": "FactSet", "facts": facts})
	}
	actions := []any{}
	if n := m.Delivery.Notification; n.Kind == "approval_needed" && n.ApprovalID != nil {
		data := CardData{ApprovalID: *n.ApprovalID, Locale: m.Data.Locale, Title: m.Subject, Link: m.Data.Link}
		return approvalCard(l, data, body, "")
	}
	actions = append(actions, map[string]any{"type": "Action.OpenUrl", "title": l.Open, "url": m.Data.Link})
	return adaptive(body, actions)
}

func approvalCard(l labels, data CardData, body []any, problem string) map[string]any {
	if problem != "" {
		body = append(body, text(problem, map[string]any{"color": "Attention"}))
	}
	body = append(body, map[string]any{"type": "Input.Text", "id": "comment", "placeholder": l.Comment, "isMultiline": true})
	return adaptive(body, []any{
		map[string]any{"type": "Action.Execute", "title": l.Approve, "verb": "approve", "data": data, "style": "positive"},
		map[string]any{"type": "Action.Execute", "title": l.Reject, "verb": "reject", "data": data, "style": "destructive"},
		map[string]any{"type": "Action.OpenUrl", "title": l.Open, "url": data.Link},
	})
}

// DecidedCard replaces an approval card once it was decided from it.
func DecidedCard(data CardData, approved bool, by string) map[string]any {
	l := labelsFor(data.Locale)
	outcome := fmt.Sprintf(l.Rejected, by)
	if approved {
		outcome = fmt.Sprintf(l.Approved, by)
	}
	color := "Attention"
	if approved {
		color = "Good"
	}
	return adaptive([]any{text(data.Title, map[string]any{"weight": "Bolder", "size": "Medium"}),
		text(outcome, map[string]any{"color": color})},
		[]any{map[string]any{"type": "Action.OpenUrl", "title": l.Open, "url": data.Link}})
}

// ProblemCard is the approval card again, saying why the decision wasn't
// taken; problem is one of "comment", "decided", "not_yours" or a message.
func ProblemCard(data CardData, problem string) map[string]any {
	l := labelsFor(data.Locale)
	switch problem {
	case "comment":
		problem = l.NeedComment
	case "decided":
		return adaptive([]any{text(data.Title, map[string]any{"weight": "Bolder", "size": "Medium"}), text(l.Decided, nil)},
			[]any{map[string]any{"type": "Action.OpenUrl", "title": l.Open, "url": data.Link}})
	case "not_yours":
		problem = l.NotYours
	default:
		problem = fmt.Sprintf(l.Failed, problem)
	}
	return approvalCard(l, data, []any{text(data.Title, map[string]any{"weight": "Bolder", "size": "Medium"})}, problem)
}

// TeamsVerifier checks the Bot Framework token of a call from Teams: its
// signature with the Bot Framework's keys, its issuer, its audience (the
// bot's app ID), its expiry, and that it was issued for the service URL
// the call names.
type TeamsVerifier struct {
	AppID, OpenIDURL string
	// TenantID, when set, is the only tenant whose calls are answered.
	TenantID string
	Client   *http.Client

	mu       sync.Mutex
	verifier *oidc.IDTokenVerifier
}

func (v *TeamsVerifier) load(ctx context.Context) (*oidc.IDTokenVerifier, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.verifier != nil {
		return v.verifier, nil
	}
	client := v.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.OpenIDURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var meta struct {
		Issuer  string   `json:"issuer"`
		JWKSURI string   `json:"jwks_uri"`
		Algs    []string `json:"id_token_signing_alg_values_supported"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil || meta.JWKSURI == "" || meta.Issuer == "" {
		return nil, fmt.Errorf("the Bot Framework's OpenID configuration can't be read: %v", err)
	}
	keys := oidc.NewRemoteKeySet(oidc.ClientContext(context.Background(), client), meta.JWKSURI)
	v.verifier = oidc.NewVerifier(meta.Issuer, keys, &oidc.Config{ClientID: v.AppID, SupportedSigningAlgs: meta.Algs})
	return v.verifier, nil
}

// Activity is what rvav checks of a call besides its token: it comes from
// Teams, in rvav's tenant.
type Activity struct {
	ChannelID, ServiceURL, TenantID string
}

// Verify checks the Authorization header of a call naming serviceURL.
func (v *TeamsVerifier) Verify(ctx context.Context, authorization string, a Activity) error {
	// Any channel of the bot gets tokens for its app ID: only Teams' calls,
	// from the tenant, are answered.
	if a.ChannelID != "msteams" {
		return fmt.Errorf("a call from the %q channel, not Teams", a.ChannelID)
	}
	if v.TenantID != "" && a.TenantID != v.TenantID {
		return fmt.Errorf("a call from another tenant (%q)", a.TenantID)
	}
	serviceURL := a.ServiceURL
	raw, ok := strings.CutPrefix(authorization, "Bearer ")
	if !ok || raw == "" {
		return errors.New("no Bot Framework token")
	}
	verifier, err := v.load(ctx)
	if err != nil {
		return err
	}
	tok, err := verifier.Verify(ctx, raw)
	if err != nil {
		return err
	}
	var claims struct {
		ServiceURL string `json:"serviceurl"`
	}
	if err := tok.Claims(&claims); err != nil {
		return err
	}
	if claims.ServiceURL == "" || strings.TrimRight(claims.ServiceURL, "/") != strings.TrimRight(serviceURL, "/") {
		return errors.New("the token wasn't issued for this service URL")
	}
	return nil
}

// Test signs in as the bot, for the Bot Connector and for Graph.
func (t *Teams) Test(context.Context) error {
	t.tokens()
	if _, err := t.bot.Token(); err != nil {
		return err
	}
	_, err := t.graph.Token()
	return err
}
