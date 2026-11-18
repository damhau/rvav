package httpapi

import (
	"errors"
	"mime"
	"net/http"
	"strconv"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/core"
)

// UploadFormFile keeps a file for a form's file field (ADR-0040). No body
// larger than the largest file a field takes is read.
func (s *Server) UploadFormFile(w http.ResponseWriter, r *http.Request, params api.UploadFormFileParams) {
	body := http.MaxBytesReader(w, r.Body, core.MaxFileSize)
	out, err := s.Core.UploadFormFile(r.Context(), principal(r), params, r.ContentLength, body)
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		err = &core.TooLargeError{Msg: "The file is larger than any file field takes."}
	}
	s.reply(w, r, http.StatusCreated, out, err)
}

// DownloadFile sends a request's file as a download: never shown in the
// browser, whatever it holds.
func (s *Server) DownloadFile(w http.ResponseWriter, r *http.Request, fileID string) {
	f, content, err := s.Core.DownloadFile(r.Context(), principal(r), fileID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "application/octet-stream")
	disposition := mime.FormatMediaType("attachment", map[string]string{"filename": f.Name})
	if disposition == "" {
		disposition = "attachment"
	}
	h.Set("Content-Disposition", disposition)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	h.Set("Cache-Control", "private, no-store")
	h.Set("Content-Length", strconv.Itoa(len(content)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(content)
}
