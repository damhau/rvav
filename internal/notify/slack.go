package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/slack-go/slack"

	"github.com/damhau/rvav/internal/config"
	"github.com/damhau/rvav/internal/store"
)

// Slack through a Slack app (ADR-0023): rvav finds the user by email,
// writes to them in a direct message in Block Kit; approval messages have
// Approve and Reject, and Reject asks for the comment in a dialog.

// Slack action and dialog IDs rvav answers.
const (
	SlackApprove = "rvav_approve"
	SlackReject  = "rvav_reject"
	SlackDialog  = "rvav_reject_comment"
)

// Slack sends notifications to Slack.
type Slack struct {
	Config config.SlackConfig
	Store  *store.Store
	// Client is the HTTP client; a 30 s one when nil.
	Client *http.Client
}

// API is Slack's Web API as the bot.
func (s *Slack) API() *slack.Client {
	c := s.Client
	if c == nil {
		c = &http.Client{Timeout: 30 * time.Second}
	}
	return slack.New(s.Config.BotToken, slack.OptionAPIURL(s.Config.APIURL), slack.OptionHTTPClient(c))
}

// Test signs in as the bot and says which workspace it is in.
func (s *Slack) Test(ctx context.Context) (string, error) {
	r, err := s.API().AuthTestContext(ctx)
	if err != nil {
		return "", err
	}
	return r.Team, nil
}

func (s *Slack) identity(ctx context.Context, api *slack.Client, d store.Delivery) (store.ChannelIdentity, error) {
	id, err := s.Store.GetChannelIdentity(ctx, d.Notification.UserID, "slack")
	if errors.Is(err, store.ErrNotFound) {
		if d.UserEmail == "" {
			return id, Permanent(errors.New("the user has no email address to find them in Slack"))
		}
		u, err := api.GetUserByEmailContext(ctx, d.UserEmail)
		if err != nil {
			if strings.Contains(err.Error(), "users_not_found") {
				return id, Permanent(fmt.Errorf("%s is not in the Slack workspace", d.UserEmail))
			}
			return id, err
		}
		id = store.ChannelIdentity{UserID: d.Notification.UserID, Channel: "slack", ExternalID: u.ID}
	} else if err != nil {
		return id, err
	}
	if id.ConversationID == nil {
		ch, _, _, err := api.OpenConversationContext(ctx, &slack.OpenConversationParameters{Users: []string{id.ExternalID}})
		if err != nil {
			return id, fmt.Errorf("open a direct message with %s: %w", d.UserEmail, err)
		}
		id.ConversationID = &ch.ID
	}
	return id, s.Store.SetChannelIdentity(ctx, id)
}

// Send writes m to the user in a direct message.
func (s *Slack) Send(ctx context.Context, m Message) (string, string, error) {
	api := s.API()
	id, err := s.identity(ctx, api, m.Delivery)
	if err != nil {
		return "", "", err
	}
	channel, ts, err := api.PostMessageContext(ctx, *id.ConversationID, slack.MsgOptionText(m.Subject, false),
		slack.MsgOptionBlocks(Blocks(m)...))
	if err != nil {
		return id.ExternalID, "", err
	}
	return id.ExternalID, channel + "/" + ts, nil
}

func plain(s string) *slack.TextBlockObject {
	return slack.NewTextBlockObject(slack.PlainTextType, s, false, false)
}
func md(s string) *slack.TextBlockObject {
	return slack.NewTextBlockObject(slack.MarkdownType, s, false, false)
}

// escape keeps names and comments from being read as Slack markup.
func escape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

func openButton(l labels, link string) *slack.ButtonBlockElement {
	b := slack.NewButtonBlockElement("rvav_open", "open", plain(l.Open))
	b.URL = link
	return b
}

// Blocks are a notification in Block Kit: an approval's has Approve and
// Reject, whose value is what the buttons send back.
func Blocks(m Message) []slack.Block {
	l := labelsFor(m.Data.Locale)
	// Within Slack's limits: 3000 characters a section, 2000 a field.
	blocks := []slack.Block{slack.NewHeaderBlock(plain(truncate(m.Subject, 150))),
		slack.NewSectionBlock(md(truncate(escape(m.Short), 2900)), nil, nil)}
	var fields []*slack.TextBlockObject
	for _, f := range m.Facts() {
		fields = append(fields, md(truncate("*"+escape(f.Label)+"*\n"+escape(f.Value), 1900)))
	}
	if len(fields) > 0 {
		blocks = append(blocks, slack.NewSectionBlock(nil, fields, nil))
	}
	if n := m.Delivery.Notification; n.Kind == "approval_needed" && n.ApprovalID != nil {
		data, _ := json.Marshal(CardData{ApprovalID: *n.ApprovalID, Locale: m.Data.Locale, Title: truncate(m.Subject, 150),
			Link: m.Data.Link})
		return append(blocks, slackDecideButtons(l, string(data)))
	}
	return append(blocks, slack.NewActionBlock("rvav", openButton(l, m.Data.Link)))
}

func slackDecideButtons(l labels, value string) slack.Block {
	approve := slack.NewButtonBlockElement(SlackApprove, value, plain(l.Approve)).WithStyle(slack.StylePrimary)
	reject := slack.NewButtonBlockElement(SlackReject, value, plain(l.Reject)).WithStyle(slack.StyleDanger)
	var data CardData
	_ = json.Unmarshal([]byte(value), &data)
	return slack.NewActionBlock("rvav_decide", approve, reject, openButton(l, data.Link))
}

// DecidedBlocks replace an approval message once decided from it.
func DecidedBlocks(data CardData, approved bool, by string) []slack.Block {
	l := labelsFor(data.Locale)
	outcome := ":x: " + fmt.Sprintf(l.Rejected, escape(by))
	if approved {
		outcome = ":white_check_mark: " + fmt.Sprintf(l.Approved, escape(by))
	}
	return []slack.Block{slack.NewHeaderBlock(plain(data.Title)), slack.NewSectionBlock(md(outcome), nil, nil),
		slack.NewActionBlock("rvav", openButton(l, data.Link))}
}

// ProblemText says why a decision from Slack wasn't taken; problem is one
// of "comment", "decided", "not_yours" or a message.
func ProblemText(locale, problem string) string {
	l := labelsFor(locale)
	switch problem {
	case "comment":
		return l.NeedComment
	case "decided":
		return l.Decided
	case "not_yours":
		return l.NotYours
	}
	return fmt.Sprintf(l.Failed, problem)
}

// RejectDialog asks for the comment of a rejection; it carries the
// message it came from.
func RejectDialog(data CardData, channel, ts string) slack.ModalViewRequest {
	l := labelsFor(data.Locale)
	meta, _ := json.Marshal(map[string]string{"approval_id": data.ApprovalID, "locale": data.Locale, "title": data.Title,
		"link": data.Link, "channel": channel, "ts": ts})
	input := slack.NewPlainTextInputBlockElement(plain(l.Comment), "comment")
	input.Multiline = true
	return slack.ModalViewRequest{Type: slack.VTModal, Title: plain(truncate(l.Reject, 24)), Submit: plain(l.Reject),
		Close: plain("×"), CallbackID: SlackDialog, PrivateMetadata: string(meta),
		Blocks: slack.Blocks{BlockSet: []slack.Block{slack.NewSectionBlock(md(escape(data.Title)), nil, nil),
			slack.NewInputBlock("comment", plain(l.Comment), nil, input)}}}
}

// VerifySlack checks a request from Slack: its signature over the body
// with the signing secret, made in the last five minutes.
func VerifySlack(header http.Header, body []byte, signingSecret string) error {
	if signingSecret == "" {
		return errors.New("no signing secret is set")
	}
	sv, err := slack.NewSecretsVerifier(header, signingSecret)
	if err != nil {
		return err
	}
	if _, err := sv.Write(body); err != nil {
		return err
	}
	return sv.Ensure()
}

// Answer posts to an interaction's response URL.
func Answer(ctx context.Context, responseURL string, msg *slack.WebhookMessage) error {
	if responseURL == "" {
		return nil
	}
	return slack.PostWebhookContext(ctx, responseURL, msg)
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
