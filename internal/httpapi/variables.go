package httpapi

import (
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/core"
)

func (s *Server) ListVariables(w http.ResponseWriter, r *http.Request) {
	items, err := s.Core.Variables(r.Context(), principal(r))
	s.reply(w, r, http.StatusOK, api.VariableList{Items: items}, err)
}

func (s *Server) CreateVariable(w http.ResponseWriter, r *http.Request) {
	var b api.VariableCreate
	if !s.decodeStrict(w, r, &b) {
		return
	}
	out, err := s.Core.CreateVariable(r.Context(), principal(r), b)
	s.reply(w, r, http.StatusCreated, out, err)
}

func (s *Server) GetVariable(w http.ResponseWriter, r *http.Request, key string) {
	out, err := s.Core.Variable(r.Context(), principal(r), key)
	s.reply(w, r, http.StatusOK, out, err)
}

func (s *Server) UpdateVariable(w http.ResponseWriter, r *http.Request, key string) {
	var b api.VariableUpdate
	if !s.decodeStrict(w, r, &b) {
		return
	}
	out, err := s.Core.UpdateVariable(r.Context(), principal(r), key, b)
	s.reply(w, r, http.StatusOK, out, err)
}

func (s *Server) DeleteVariable(w http.ResponseWriter, r *http.Request, key string) {
	s.replyEmpty(w, r, s.Core.DeleteVariable(r.Context(), principal(r), key))
}

// PutVariableFile adds a shared variable that holds a file, or replaces its
// file. No body larger than the largest file, with room for a description
// and the parts' headers, is read.
func (s *Server) PutVariableFile(w http.ResponseWriter, r *http.Request, key string) {
	out, created, err := s.Core.PutVariableFile(r.Context(), principal(r), key, func() (core.VariableUpload, error) {
		return readVariableUpload(w, r)
	})
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	s.reply(w, r, status, out, err)
}

func readVariableUpload(w http.ResponseWriter, r *http.Request) (core.VariableUpload, error) {
	var up core.VariableUpload
	tooLarge := &core.TooLargeError{Msg: fmt.Sprintf("The file is larger than %d KiB, the most a variable holds.", core.MaxVariableFile>>10)}
	if r.ContentLength > core.MaxVariableFile+64<<10 {
		return up, tooLarge
	}
	r.Body = http.MaxBytesReader(w, r.Body, core.MaxVariableFile+64<<10)
	form, err := r.MultipartReader()
	if err != nil {
		return up, &core.BadRequestError{Msg: "Send the file as multipart/form-data, in a part named file."}
	}
	read := func(rd io.Reader, limit int64) ([]byte, error) {
		b, err := io.ReadAll(io.LimitReader(rd, limit+1))
		var mbe *http.MaxBytesError
		switch {
		case errors.As(err, &mbe):
			return nil, tooLarge
		case err != nil:
			return nil, &core.BadRequestError{Msg: "The body can't be read: " + err.Error()}
		}
		return b, nil
	}
	for {
		part, err := form.NextPart()
		var mbe *http.MaxBytesError
		switch {
		case errors.Is(err, io.EOF):
			return up, nil
		case errors.As(err, &mbe):
			return up, tooLarge
		case err != nil:
			return up, &core.BadRequestError{Msg: "The body isn't multipart/form-data rvav can read: " + err.Error()}
		}
		switch part.FormName() {
		case "file":
			if up.File {
				return up, &core.BadRequestError{Msg: "Send one file."}
			}
			up.File, up.FileName = true, part.FileName()
			if up.Content, err = read(part, core.MaxVariableFile); err != nil {
				return up, err
			}
			if len(up.Content) > core.MaxVariableFile {
				return up, tooLarge
			}
		case "description":
			b, err := read(part, 4<<10)
			if err != nil {
				return up, err
			}
			d := string(b)
			up.Description = &d
		default:
			return up, &core.BadRequestError{Msg: fmt.Sprintf("The body has a part %q this operation doesn't take: send file and description.", part.FormName())}
		}
	}
}

// DownloadVariableFile sends the file a shared variable holds as a
// download: never shown in the browser, whatever it holds.
func (s *Server) DownloadVariableFile(w http.ResponseWriter, r *http.Request, key string) {
	f, content, err := s.Core.VariableFile(r.Context(), principal(r), key)
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
