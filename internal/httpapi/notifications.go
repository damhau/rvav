package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/slack-go/slack"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/core"
	"github.com/damhau/rvav/internal/notify"
)

// Notifications, their preferences and the Teams and Slack callbacks (S11,
// ADR-0023): answered 501 until built.

func (s *Server) ListNotifications(w http.ResponseWriter, r *http.Request, params api.ListNotificationsParams) {
	page, err := s.Core.Notifications(r.Context(), principal(r), params)
	s.reply(w, r, http.StatusOK, page, err)
}

func (s *Server) MarkNotificationsRead(w http.ResponseWriter, r *http.Request) {
	var b api.NotificationsRead
	if !s.decode(w, r, &b) {
		return
	}
	n, err := s.Core.MarkNotificationsRead(r.Context(), principal(r), b)
	s.reply(w, r, http.StatusOK, n, err)
}

func (s *Server) GetNotificationPreferences(w http.ResponseWriter, r *http.Request) {
	prefs, err := s.Core.NotificationPreferences(r.Context(), principal(r))
	s.reply(w, r, http.StatusOK, prefs, err)
}

func (s *Server) SetNotificationPreferences(w http.ResponseWriter, r *http.Request) {
	var b api.NotificationPreferencesWrite
	if !s.decode(w, r, &b) {
		return
	}
	prefs, err := s.Core.SetNotificationPreferences(r.Context(), principal(r), b)
	s.reply(w, r, http.StatusOK, prefs, err)
}

func (s *Server) TestNotificationChannel(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Channel api.NotificationChannel `json:"channel"`
	}
	if !s.decode(w, r, &b) {
		return
	}
	check, err := s.Core.TestNotificationChannel(r.Context(), principal(r), b.Channel)
	s.reply(w, r, http.StatusOK, check, err)
}

// TeamsMessages answers rvav's Teams bot: Approve and Reject on an
// approval card decide it as the user it was sent to (ADR-0023).
func (s *Server) TeamsMessages(w http.ResponseWriter, r *http.Request) {
	if s.Teams == nil {
		s.problem(w, r, http.StatusNotFound, "not_found", "Teams is not set up", "", nil)
		return
	}
	var act struct {
		Type       string `json:"type"`
		Name       string `json:"name"`
		ServiceURL string `json:"serviceUrl"`
		From       struct {
			AADObjectID string `json:"aadObjectId"`
		} `json:"from"`
		Value struct {
			Action struct {
				Verb string          `json:"verb"`
				Data json.RawMessage `json:"data"`
			} `json:"action"`
		} `json:"value"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&act); err != nil {
		s.problem(w, r, http.StatusBadRequest, "bad_request", "Not a Bot Framework activity", err.Error(), nil)
		return
	}
	if err := s.Teams.Verify(r.Context(), r.Header.Get("Authorization"), act.ServiceURL); err != nil {
		s.Log.Warn("a Teams call was refused", "err", err)
		s.problem(w, r, http.StatusUnauthorized, "unauthenticated", "Not a call from Teams", "", nil)
		return
	}
	if act.Type != "invoke" || act.Name != "adaptiveCard/action" {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	var data struct {
		notify.CardData
		Comment string `json:"comment"`
	}
	_ = json.Unmarshal(act.Value.Action.Data, &data)
	approve := act.Value.Action.Verb == "approve"
	by, err := s.Core.DecideFromChannel(r.Context(), "teams", act.From.AADObjectID, data.ApprovalID, approve,
		strings.TrimSpace(data.Comment))
	card := notify.DecidedCard(data.CardData, approve, by)
	if err != nil {
		why := err.Error()
		if !errors.Is(err, core.ErrCardNotYours) && !errors.Is(err, core.ErrCardDecided) && !errors.Is(err, core.ErrCardComment) {
			s.Log.Error("decide from Teams", "approval", data.ApprovalID, "err", err)
			why = "rvav couldn't record it; try again in rvav"
		}
		card = notify.ProblemCard(data.CardData, why)
	}
	writeJSON(w, http.StatusOK, map[string]any{"statusCode": 200, "type": "application/vnd.microsoft.card.adaptive", "value": card})
}

// SlackInteractions answers rvav's Slack app: Approve decides at once,
// Reject asks for the comment in a dialog, which decides when sent
// (ADR-0023).
func (s *Server) SlackInteractions(w http.ResponseWriter, r *http.Request) {
	if s.Slack == nil {
		s.problem(w, r, http.StatusNotFound, "not_found", "Slack is not set up", "", nil)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		s.problem(w, r, http.StatusBadRequest, "bad_request", "Can't read the request", err.Error(), nil)
		return
	}
	if err := notify.VerifySlack(r.Header, body, s.Slack.Config.SigningSecret); err != nil {
		s.Log.Warn("a Slack call was refused", "err", err)
		s.problem(w, r, http.StatusUnauthorized, "unauthenticated", "Not a call from Slack", "", nil)
		return
	}
	form, _ := url.ParseQuery(string(body))
	var cb slack.InteractionCallback
	if err := json.Unmarshal([]byte(form.Get("payload")), &cb); err != nil {
		s.problem(w, r, http.StatusBadRequest, "bad_request", "Not a Slack interaction", err.Error(), nil)
		return
	}
	ctx := r.Context()
	why := func(err error) string {
		if errors.Is(err, core.ErrCardNotYours) || errors.Is(err, core.ErrCardDecided) || errors.Is(err, core.ErrCardComment) {
			return err.Error()
		}
		s.Log.Error("decide from Slack", "err", err)
		return "rvav couldn't record it; try again in rvav"
	}
	switch cb.Type {
	case slack.InteractionTypeBlockActions:
		if len(cb.ActionCallback.BlockActions) == 0 {
			break
		}
		a := cb.ActionCallback.BlockActions[0]
		var data notify.CardData
		_ = json.Unmarshal([]byte(a.Value), &data)
		switch a.ActionID {
		case notify.SlackApprove:
			by, err := s.Core.DecideFromChannel(ctx, "slack", cb.User.ID, data.ApprovalID, true, "")
			msg := &slack.WebhookMessage{ReplaceOriginal: true, Text: data.Title,
				Blocks: &slack.Blocks{BlockSet: notify.DecidedBlocks(data, true, by)}}
			if err != nil {
				msg = &slack.WebhookMessage{ResponseType: slack.ResponseTypeEphemeral, Text: notify.ProblemText(data.Locale, why(err))}
			}
			if err := notify.Answer(ctx, cb.ResponseURL, msg); err != nil {
				s.Log.Warn("answer Slack", "err", err)
			}
		case notify.SlackReject:
			if _, err := s.Slack.API().OpenViewContext(ctx, cb.TriggerID, notify.RejectDialog(data, cb.Container.ChannelID,
				cb.Container.MessageTs)); err != nil {
				s.Log.Warn("open Slack's reject dialog", "err", err)
			}
		}
	case slack.InteractionTypeViewSubmission:
		if cb.View.CallbackID != notify.SlackDialog {
			break
		}
		var meta struct {
			notify.CardData
			Channel string `json:"channel"`
			TS      string `json:"ts"`
		}
		_ = json.Unmarshal([]byte(cb.View.PrivateMetadata), &meta)
		comment := ""
		if cb.View.State != nil {
			comment = strings.TrimSpace(cb.View.State.Values["comment"]["comment"].Value)
		}
		if comment == "" {
			writeJSON(w, http.StatusOK, slack.NewErrorsViewSubmissionResponse(map[string]string{
				"comment": notify.ProblemText(meta.Locale, "comment")}))
			return
		}
		by, err := s.Core.DecideFromChannel(ctx, "slack", cb.User.ID, meta.ApprovalID, false, comment)
		if err != nil {
			writeJSON(w, http.StatusOK, slack.NewErrorsViewSubmissionResponse(map[string]string{
				"comment": notify.ProblemText(meta.Locale, why(err))}))
			return
		}
		if meta.Channel != "" && meta.TS != "" {
			if _, _, _, err := s.Slack.API().UpdateMessageContext(ctx, meta.Channel, meta.TS, slack.MsgOptionText(meta.Title, false),
				slack.MsgOptionBlocks(notify.DecidedBlocks(meta.CardData, false, by)...)); err != nil {
				s.Log.Warn("update the Slack message", "err", err)
			}
		}
	}
	w.WriteHeader(http.StatusOK)
}

// Request drafts.

func (s *Server) ListRequestDrafts(w http.ResponseWriter, r *http.Request) {
	items, err := s.Core.RequestDrafts(r.Context(), principal(r))
	s.reply(w, r, http.StatusOK, api.SavedDraftList{Items: items}, err)
}

func (s *Server) CreateRequestDraft(w http.ResponseWriter, r *http.Request) {
	var b api.SavedDraftWrite
	if !s.decode(w, r, &b) {
		return
	}
	d, err := s.Core.SaveRequestDraft(r.Context(), principal(r), "", b)
	s.reply(w, r, http.StatusCreated, d, err)
}

func (s *Server) GetRequestDraft(w http.ResponseWriter, r *http.Request, id api.IdPath) {
	d, err := s.Core.GetRequestDraft(r.Context(), principal(r), id)
	s.reply(w, r, http.StatusOK, d, err)
}

func (s *Server) UpdateRequestDraft(w http.ResponseWriter, r *http.Request, id api.IdPath) {
	var b api.SavedDraftWrite
	if !s.decode(w, r, &b) {
		return
	}
	d, err := s.Core.SaveRequestDraft(r.Context(), principal(r), id, b)
	s.reply(w, r, http.StatusOK, d, err)
}

func (s *Server) DeleteRequestDraft(w http.ResponseWriter, r *http.Request, id api.IdPath) {
	s.replyEmpty(w, r, s.Core.DeleteRequestDraft(r.Context(), principal(r), id))
}
