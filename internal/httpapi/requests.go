package httpapi

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/present"
	"github.com/damhau/rvav/internal/store"
)

func (s *Server) PreviewRequest(w http.ResponseWriter, r *http.Request) {
	var d api.RequestDraft
	if !s.decode(w, r, &d) {
		return
	}
	if d.Inputs == nil {
		d.Inputs = map[string]any{}
	}
	ev, err := s.Core.Evaluate(r.Context(), principal(r), d, false)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, ev.Preview())
}

func (s *Server) ListRequests(w http.ResponseWriter, r *http.Request, params api.ListRequestsParams) {
	p := principal(r)
	after, err := cursorOf(params.Cursor)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	f := store.RequestFilter{Projects: p.VisibleProjects(), ViewerID: p.User.ID, ProjectID: deref(params.ProjectId),
		Search: deref(params.Q), Limit: limitOf(params.Limit), After: after}
	if f.Projects != nil && len(f.Projects) == 0 {
		f.Projects = []string{}
	}
	if params.Status != nil {
		for _, st := range *params.Status {
			f.Statuses = append(f.Statuses, string(st))
		}
	}
	switch req := deref(params.Requester); req {
	case "":
	case "me":
		f.RequesterID = p.User.ID
	default:
		f.RequesterID = req
	}
	page, err := s.Store.ListRequests(r.Context(), f)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := api.RequestPage{Items: make([]api.Request, len(page.Items)), NextCursor: nextCursor(page.Next)}
	for i, row := range page.Items {
		out.Items[i] = present.Request(row, s.secretFields(r, row.VersionID))
	}
	writeJSON(w, http.StatusOK, out)
}

// CreateRequest submits a request. The Idempotency-Key makes retries safe:
// the same key and body return the first response; the same key with
// another body is refused.
func (s *Server) CreateRequest(w http.ResponseWriter, r *http.Request, params api.CreateRequestParams) {
	p := principal(r)
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		s.problem(w, r, http.StatusBadRequest, "bad-request", "The request body is too large", "", nil)
		return
	}
	sum := sha256.Sum256(bytes.TrimSpace(body))
	hash := hex.EncodeToString(sum[:])
	key := params.IdempotencyKey.String()

	rec, claimed, err := s.Store.ClaimIdempotencyKey(r.Context(), p.User.ID, key, hash)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if !claimed {
		switch {
		case rec.BodyHash != hash:
			s.problem(w, r, http.StatusConflict, "idempotency-key-reused", "This Idempotency-Key was used for another request",
				"Generate a new key for each new submission.", nil)
		case rec.StatusCode == nil:
			s.problem(w, r, http.StatusConflict, "in-progress", "This request is still being submitted",
				"Wait a moment, then retry with the same key.", nil)
		default:
			var out api.Request
			if json.Unmarshal(rec.Response, &out) == nil {
				w.Header().Set("Location", "/api/v1/requests/"+out.Id)
			}
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Idempotent-Replayed", "true")
			w.WriteHeader(*rec.StatusCode)
			_, _ = w.Write(rec.Response)
		}
		return
	}

	var d api.RequestDraft
	if err := json.Unmarshal(body, &d); err != nil {
		_ = s.Store.ReleaseIdempotencyKey(r.Context(), p.User.ID, key)
		s.problem(w, r, http.StatusBadRequest, "bad-request", "The request body is not valid JSON", err.Error(), nil)
		return
	}
	if d.Inputs == nil {
		d.Inputs = map[string]any{}
	}
	row, err := s.Core.Submit(r.Context(), p, d)
	if err != nil {
		// Let a corrected retry use the same key.
		_ = s.Store.ReleaseIdempotencyKey(r.Context(), p.User.ID, key)
		s.fail(w, r, err)
		return
	}
	out := present.Request(row, s.secretFields(r, row.VersionID))
	b, _ := json.Marshal(out)
	if err := s.Store.CompleteIdempotencyKey(r.Context(), p.User.ID, key, http.StatusAccepted, b); err != nil {
		s.Log.Warn("store idempotent response", "err", err)
	}
	w.Header().Set("Location", "/api/v1/requests/"+out.Id)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_, _ = w.Write(append(b, '\n'))
}

func (s *Server) visibleRequest(w http.ResponseWriter, r *http.Request, idOrNumber string) (store.RequestRow, bool) {
	row, err := s.Store.GetRequest(r.Context(), idOrNumber)
	if err == nil {
		var ok bool
		if ok, err = s.Core.CanReadRequest(r.Context(), principal(r), row); err == nil && !ok {
			err = store.ErrNotFound
		}
	}
	if err != nil {
		s.fail(w, r, err)
		return row, false
	}
	return row, true
}

func (s *Server) GetRequest(w http.ResponseWriter, r *http.Request, requestID api.RequestIdPath) {
	row, ok := s.visibleRequest(w, r, requestID)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, present.Request(row, s.secretFields(r, row.VersionID)))
}

func (s *Server) ListRequestSteps(w http.ResponseWriter, r *http.Request, requestID api.RequestIdPath) {
	row, ok := s.visibleRequest(w, r, requestID)
	if !ok {
		return
	}
	steps, err := s.Store.ListSteps(r.Context(), row.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": present.StepTree(steps)})
}

func (s *Server) GetStepLog(w http.ResponseWriter, r *http.Request, requestID api.RequestIdPath, stepID string, params api.GetStepLogParams) {
	row, ok := s.visibleRequest(w, r, requestID)
	if !ok {
		return
	}
	st, err := s.Store.GetStep(r.Context(), row.ID, stepID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	offset, limit := 0, 1000
	if params.Offset != nil && *params.Offset > 0 {
		offset = *params.Offset
	}
	if params.Limit != nil && *params.Limit > 0 && *params.Limit <= 5000 {
		limit = *params.Limit
	}
	lines, err := s.Store.LogLines(r.Context(), st.ID, offset, limit)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := api.LogChunk{StepId: st.ID, Offset: offset, NextOffset: offset + len(lines)}
	out.Lines = make([]struct {
		Kind *api.LogChunkLinesKind `json:"kind,omitempty"`
		N    int                    `json:"n"`
		Text string                 `json:"text"`
	}, len(lines))
	for i, l := range lines {
		k := api.LogChunkLinesKind(l.Kind)
		out.Lines[i].N, out.Lines[i].Text, out.Lines[i].Kind = l.N, l.Text, &k
		out.NextOffset = l.N + 1
	}
	switch st.Status {
	case "done", "failed", "skipped", "cancelled":
		out.Complete = len(lines) < limit
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) CancelRequest(w http.ResponseWriter, r *http.Request, requestID api.RequestIdPath) {
	row, err := s.Core.Cancel(r.Context(), principal(r), requestID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, present.Request(row, s.secretFields(r, row.VersionID)))
}
