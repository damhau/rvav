package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/core"
	"github.com/damhau/rvav/internal/notify"
)

// Notifications, their preferences and the Teams and Slack callbacks (S11,
// ADR-0023): answered 501 until built.

func (s *Server) ListNotifications(w http.ResponseWriter, r *http.Request, params api.ListNotificationsParams) {
	s.notBuilt(w, r)
}

func (s *Server) MarkNotificationsRead(w http.ResponseWriter, r *http.Request) { s.notBuilt(w, r) }

func (s *Server) GetNotificationPreferences(w http.ResponseWriter, r *http.Request) { s.notBuilt(w, r) }

func (s *Server) SetNotificationPreferences(w http.ResponseWriter, r *http.Request) { s.notBuilt(w, r) }

func (s *Server) TestNotificationChannel(w http.ResponseWriter, r *http.Request) { s.notBuilt(w, r) }

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

func (s *Server) SlackInteractions(w http.ResponseWriter, r *http.Request) { s.notBuilt(w, r) }
