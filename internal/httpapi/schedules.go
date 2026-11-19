package httpapi

import (
	"net/http"

	"github.com/damhau/rvav/internal/api"
)

func (s *Server) ListSchedules(w http.ResponseWriter, r *http.Request, params api.ListSchedulesParams) {
	items, err := s.Core.ListSchedules(r.Context(), principal(r), params.ProjectId)
	s.reply(w, r, http.StatusOK, api.ScheduleList{Items: items}, err)
}

func (s *Server) CreateSchedule(w http.ResponseWriter, r *http.Request) {
	var b api.ScheduleWrite
	if !s.decode(w, r, &b) {
		return
	}
	out, err := s.Core.CreateSchedule(r.Context(), principal(r), b)
	s.reply(w, r, http.StatusCreated, out, err)
}

func (s *Server) GetSchedule(w http.ResponseWriter, r *http.Request, id api.ScheduleIdPath) {
	out, err := s.Core.Schedule(r.Context(), principal(r), id)
	s.reply(w, r, http.StatusOK, out, err)
}

func (s *Server) DeleteSchedule(w http.ResponseWriter, r *http.Request, id api.ScheduleIdPath) {
	s.replyEmpty(w, r, s.Core.DeleteSchedule(r.Context(), principal(r), id))
}

func (s *Server) PauseSchedule(w http.ResponseWriter, r *http.Request, id api.ScheduleIdPath) {
	var b api.SchedulePause
	if r.ContentLength != 0 && !s.decode(w, r, &b) {
		return
	}
	note := ""
	if b.Note != nil {
		note = *b.Note
	}
	out, err := s.Core.PauseSchedule(r.Context(), principal(r), id, note)
	s.reply(w, r, http.StatusOK, out, err)
}

func (s *Server) ResumeSchedule(w http.ResponseWriter, r *http.Request, id api.ScheduleIdPath) {
	out, err := s.Core.ResumeSchedule(r.Context(), principal(r), id)
	s.reply(w, r, http.StatusOK, out, err)
}
