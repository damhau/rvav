package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/brand"
	"github.com/damhau/rvav/internal/core"
)

// GetBranding answers anyone, for the sign-in page: revalidated on each
// load, 304 while it is the same.
func (s *Server) GetBranding(w http.ResponseWriter, r *http.Request, params api.GetBrandingParams) {
	out, etag, err := s.Core.Branding(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	h := w.Header()
	h.Set("ETag", etag)
	h.Set("Cache-Control", "no-cache")
	if params.IfNoneMatch != nil && etagMatches(*params.IfNoneMatch, etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func etagMatches(header, etag string) bool {
	for _, t := range strings.Split(header, ",") {
		t = strings.TrimPrefix(strings.TrimSpace(t), "W/")
		if t == etag || t == "*" {
			return true
		}
	}
	return false
}

// GetBrandingLogo sends a logo as it was checked, never sniffed as another
// type, in a sandbox when opened alone. Its URL names its content.
func (s *Server) GetBrandingLogo(w http.ResponseWriter, r *http.Request, variant api.LogoVariant,
	params api.GetBrandingLogoParams) {
	logo, err := s.Core.BrandingLogo(r.Context(), variant)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	h := w.Header()
	h.Set("Content-Type", logo.ContentType)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	h.Set("ETag", `"`+logo.SHA[:16]+`"`)
	if params.V != nil && *params.V != "" && strings.HasPrefix(logo.SHA, *params.V) {
		h.Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		h.Set("Cache-Control", "no-cache")
	}
	if etagMatches(r.Header.Get("If-None-Match"), `"`+logo.SHA[:16]+`"`) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	h.Set("Content-Length", strconv.Itoa(len(logo.Content)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(logo.Content)
}

func (s *Server) SetBranding(w http.ResponseWriter, r *http.Request) {
	var b api.BrandingWrite
	if !s.decode(w, r, &b) {
		return
	}
	out, err := s.Core.SetBranding(r.Context(), principal(r), b)
	s.reply(w, r, http.StatusOK, out, err)
}

func (s *Server) CheckBrandingAccent(w http.ResponseWriter, r *http.Request) {
	var b api.AccentCheckQuery
	if !s.decode(w, r, &b) {
		return
	}
	out, err := s.Core.CheckBrandingAccent(r.Context(), principal(r), b)
	s.reply(w, r, http.StatusOK, out, err)
}

func (s *Server) SetBrandingLogo(w http.ResponseWriter, r *http.Request, variant api.LogoVariant) {
	body := http.MaxBytesReader(w, r.Body, brand.MaxLogoSize+1)
	out, err := s.Core.SetBrandingLogo(r.Context(), principal(r), variant, r.ContentLength, body)
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		err = &core.TooLargeError{Msg: "A logo is 256 KB at most: emails carry it."}
	}
	s.reply(w, r, http.StatusOK, out, err)
}

func (s *Server) DeleteBrandingLogo(w http.ResponseWriter, r *http.Request, variant api.LogoVariant) {
	out, err := s.Core.DeleteBrandingLogo(r.Context(), principal(r), variant)
	s.reply(w, r, http.StatusOK, out, err)
}

func (s *Server) ListNotificationTemplates(w http.ResponseWriter, r *http.Request) {
	out, err := s.Core.NotificationTemplates(r.Context(), principal(r))
	s.reply(w, r, http.StatusOK, out, err)
}

func (s *Server) GetNotificationTemplate(w http.ResponseWriter, r *http.Request, kind api.NotificationKind,
	params api.GetNotificationTemplateParams) {
	out, err := s.Core.NotificationTemplate(r.Context(), principal(r), kind, params.Channel, params.Locale)
	s.reply(w, r, http.StatusOK, out, err)
}

func (s *Server) SetNotificationTemplate(w http.ResponseWriter, r *http.Request, kind api.NotificationKind) {
	var b api.NotificationTemplateWrite
	if !s.decode(w, r, &b) {
		return
	}
	out, err := s.Core.SetNotificationTemplate(r.Context(), principal(r), kind, b)
	s.reply(w, r, http.StatusOK, out, err)
}

func (s *Server) ResetNotificationTemplate(w http.ResponseWriter, r *http.Request, kind api.NotificationKind,
	params api.ResetNotificationTemplateParams) {
	out, err := s.Core.ResetNotificationTemplate(r.Context(), principal(r), kind, params.Channel, params.Locale)
	s.reply(w, r, http.StatusOK, out, err)
}

func (s *Server) PreviewNotificationTemplate(w http.ResponseWriter, r *http.Request, kind api.NotificationKind) {
	var b api.NotificationTemplateWrite
	if !s.decode(w, r, &b) {
		return
	}
	out, err := s.Core.PreviewNotificationTemplate(r.Context(), principal(r), kind, b)
	s.reply(w, r, http.StatusOK, out, err)
}
