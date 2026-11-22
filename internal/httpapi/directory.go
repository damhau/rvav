package httpapi

import (
	"net/http"

	"github.com/damhau/rvav/internal/api"
)

func (s *Server) ListAdPlacements(w http.ResponseWriter, r *http.Request, params api.ListAdPlacementsParams) {
	items, err := s.Core.ADPlacements(r.Context(), principal(r), deref(params.ProjectId))
	s.reply(w, r, http.StatusOK, api.AdPlacementList{Items: items}, err)
}

func (s *Server) CreateAdPlacement(w http.ResponseWriter, r *http.Request) {
	var b api.AdPlacementWrite
	if !s.decodeStrict(w, r, &b) {
		return
	}
	out, err := s.Core.SaveADPlacement(r.Context(), principal(r), "", b)
	s.reply(w, r, http.StatusCreated, out, err)
}

func (s *Server) UpdateAdPlacement(w http.ResponseWriter, r *http.Request, id api.IdPath) {
	var b api.AdPlacementWrite
	if !s.decodeStrict(w, r, &b) {
		return
	}
	out, err := s.Core.SaveADPlacement(r.Context(), principal(r), id, b)
	s.reply(w, r, http.StatusOK, out, err)
}

func (s *Server) DeleteAdPlacement(w http.ResponseWriter, r *http.Request, id api.IdPath) {
	s.replyEmpty(w, r, s.Core.DeleteADPlacement(r.Context(), principal(r), id))
}
