package httpapi

import (
	"net/http"

	"github.com/damhau/rvav/internal/api"
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

func (s *Server) TeamsMessages(w http.ResponseWriter, r *http.Request) { s.notBuilt(w, r) }

func (s *Server) SlackInteractions(w http.ResponseWriter, r *http.Request) { s.notBuilt(w, r) }
