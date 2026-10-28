// Package httpapi serves the rvav REST API (generated from api/openapi.yaml)
// and the live event stream.
package httpapi

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/core"
	"github.com/damhau/rvav/internal/events"
	"github.com/damhau/rvav/internal/notify"
	"github.com/damhau/rvav/internal/present"
	"github.com/damhau/rvav/internal/store"
)

type Server struct {
	Store *store.Store
	Core  *core.Service
	Hub   *events.Hub
	Auth  auth.Authenticator
	Log   *slog.Logger
	// AuthMode is reported in the X-Rvav-Auth-Mode header ("oidc" or "dev").
	AuthMode    string
	CORSOrigins []string
	// Heartbeat is the SSE keep-alive interval (15 s when zero).
	Heartbeat time.Duration
	// WMKSSDKURLs are the scripts of VMware's HTML console client, in load
	// order; none means only VMRC links (ADR-0012). ConsoleInsecure skips
	// the ESXi certificate check when vCenter gives no thumbprint.
	WMKSSDKURLs     []string
	ConsoleInsecure bool
	// Teams checks the calls of rvav's Teams bot; nil when Teams isn't set
	// up (ADR-0023).
	Teams *notify.TeamsVerifier
	// Slack answers rvav's Slack app; nil when Slack isn't set up.
	Slack *notify.Slack

	secrets  sync.Map // version ID -> map[string]bool
	consoles consoleTokens
}

var _ api.ServerInterface = (*Server)(nil)

type ctxKey int

const requestIDKey ctxKey = 0

func requestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

// Handler returns the full HTTP handler: /api/v1 plus health checks.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if err := s.Store.Pool.Ping(r.Context()); err != nil {
			http.Error(w, "database unavailable", http.StatusServiceUnavailable)
			return
		}
		// OIDC: not ready until the identity provider has been discovered.
		if a, ok := s.Auth.(interface{ Ready(context.Context) error }); ok {
			if err := a.Ready(r.Context()); err != nil {
				http.Error(w, "identity provider unavailable", http.StatusServiceUnavailable)
				return
			}
		}
		w.WriteHeader(http.StatusNoContent)
	})
	api.HandlerWithOptions(s, api.StdHTTPServerOptions{
		BaseURL:     "/api/v1",
		BaseRouter:  mux,
		Middlewares: []api.MiddlewareFunc{s.authenticate},
		ErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
			s.problem(w, r, http.StatusBadRequest, "bad-request", "The request is not valid", err.Error(), nil)
		},
	})
	// Unknown /api paths answer with a problem, not the mux's plain text.
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		s.problem(w, r, http.StatusNotFound, "not-found", "Not found", "There is no API endpoint at "+r.URL.Path+".", nil)
	})
	return s.recover(s.withRequestID(s.cors(mux)))
}

func (s *Server) withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-Id")
		if id == "" || len(id) > 64 {
			b := make([]byte, 8)
			_, _ = rand.Read(b)
			id = hex.EncodeToString(b)
		}
		w.Header().Set("X-Request-Id", id)
		if s.AuthMode != "" {
			w.Header().Set("X-Rvav-Auth-Mode", s.AuthMode)
		}
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r.WithContext(context.WithValue(r.Context(), requestIDKey, id)))
		if r.URL.Path != "/healthz" && r.URL.Path != "/readyz" {
			s.Log.Info("http", "method", r.Method, "path", r.URL.Path, "status", sw.status,
				"duration_ms", time.Since(start).Milliseconds(), "request_id", id)
		}
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// Hijack lets the console proxy take over the connection.
func (w *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hj, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("hijacking is not supported")
	}
	w.status = http.StatusSwitchingProtocols
	return hj.Hijack()
}

// Flush lets the event stream push data through the logging wrapper.
func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *Server) recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				s.Log.Error("panic", "value", fmt.Sprint(v), "stack", string(debug.Stack()))
				s.problem(w, r, http.StatusInternalServerError, "internal", "Something went wrong on our side",
					"The error was logged. Quote the request ID if you contact support.", nil)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *Server) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && (slices.Contains(s.CORSOrigins, origin) || slices.Contains(s.CORSOrigins, "*")) {
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			h.Set("Vary", "Origin")
			h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Idempotency-Key, Last-Event-ID, X-Request-Id, "+auth.DevUserHeader)
			h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			h.Set("Access-Control-Expose-Headers", "Location, X-Request-Id, X-Rvav-Auth-Mode")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The console's WebSocket carries its own single-use token (ADR-0012);
		// Teams and Slack sign their calls (ADR-0023).
		if strings.HasPrefix(r.URL.Path, "/api/v1/console/") || strings.HasPrefix(r.URL.Path, "/api/v1/integrations/") {
			next.ServeHTTP(w, r)
			return
		}
		p, err := s.Auth.Authenticate(r)
		if err != nil {
			if errors.Is(err, auth.ErrProviderUnavailable) {
				s.Log.Warn("authenticate", "err", err, "request_id", requestID(r.Context()))
				w.Header().Set("Retry-After", "5")
				s.problem(w, r, http.StatusServiceUnavailable, "unavailable", "Sign-in is unavailable",
					"The identity provider can't be reached. Try again in a moment.", nil)
				return
			}
			if !errors.Is(err, auth.ErrUnauthenticated) {
				s.Log.Error("authenticate", "err", err, "request_id", requestID(r.Context()))
				s.problem(w, r, http.StatusInternalServerError, "internal", "Could not check your sign-in", "Try again in a moment.", nil)
				return
			}
			w.Header().Set("WWW-Authenticate", `Bearer realm="rvav"`)
			s.problem(w, r, http.StatusUnauthorized, "unauthenticated", "Sign in required",
				"Send a valid access token in the Authorization header.", nil)
			return
		}
		if p.Scope == auth.ScopeInventory && !(r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/ansible/inventory")) {
			s.problem(w, r, http.StatusForbidden, "forbidden", "This token only reads the Ansible inventory",
				"It was made with the inventory scope (rvav api-token -scope inventory).", nil)
			return
		}
		next.ServeHTTP(w, r.WithContext(auth.WithPrincipal(r.Context(), p)))
	})
}

func principal(r *http.Request) *auth.Principal { return auth.From(r.Context()) }

// problem writes an RFC 9457 problem response.
func (s *Server) problem(w http.ResponseWriter, r *http.Request, status int, typ, title, detail string, errs []api.FieldError) {
	p := api.Problem{
		Type:      "https://rvav.dev/problems/" + typ,
		Title:     title,
		Status:    status,
		RequestId: requestID(r.Context()),
		Instance:  present.Ptr(r.URL.Path),
	}
	if detail != "" {
		p.Detail = &detail
	}
	if errs != nil {
		p.Errors = &errs
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(p)
}

// fail maps an error from the core or store to a problem response.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	var (
		ve  *core.ValidationError
		ce  *core.ConflictError
		be  *core.BadRequestError
		ue  *core.UpstreamError
		msg = err.Error()
	)
	switch {
	case errors.Is(err, core.ErrNotFound), errors.Is(err, store.ErrNotFound):
		s.problem(w, r, http.StatusNotFound, "not-found", "Not found", "It does not exist, or you cannot see it.", nil)
	case errors.Is(err, core.ErrForbidden):
		detail := strings.TrimPrefix(msg, core.ErrForbidden.Error()+": ")
		if detail == msg {
			detail = "You do not have permission to do this"
		}
		s.problem(w, r, http.StatusForbidden, "forbidden", "Not allowed", upperFirst(detail)+".", nil)
	case errors.As(err, &ve):
		s.problem(w, r, http.StatusUnprocessableEntity, "validation", "Some inputs need attention", ve.Error(), ve.Errors)
	case errors.As(err, &ce):
		s.problem(w, r, http.StatusConflict, "conflict", "Not possible right now", ce.Msg, nil)
	case errors.As(err, &be):
		s.problem(w, r, http.StatusBadRequest, "bad-request", "The request is not valid", be.Msg, nil)
	case errors.Is(err, store.ErrBadCursor):
		s.problem(w, r, http.StatusBadRequest, "bad-request", "The request is not valid", "The cursor is not valid. Start again from the first page.", nil)
	case errors.As(err, &ue):
		s.Log.Warn("upstream error", "system", ue.System, "err", ue.Err, "request_id", requestID(r.Context()))
		s.problem(w, r, http.StatusBadGateway, "upstream", ue.System+" did not answer", "Try again in a moment. If it keeps failing, tell the platform team.", nil)
	case errors.Is(err, context.Canceled):
		// client went away
	default:
		s.Log.Error("request failed", "err", err, "path", r.URL.Path, "request_id", requestID(r.Context()))
		s.problem(w, r, http.StatusInternalServerError, "internal", "Something went wrong on our side",
			"The error was logged. Quote the request ID if you contact support.", nil)
	}
}

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// decode reads a JSON body of at most 1 MB.
func (s *Server) decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := dec.Decode(v); err != nil {
		s.problem(w, r, http.StatusBadRequest, "bad-request", "The request body is not valid JSON", err.Error(), nil)
		return false
	}
	return true
}

func limitOf(l *api.Limit) int {
	if l == nil || *l < 1 {
		return 50
	}
	if *l > 200 {
		return 200
	}
	return *l
}

func cursorOf(c *api.Cursor) (*store.Cursor, error) {
	if c == nil {
		return nil, nil
	}
	return store.DecodeCursor(*c)
}

func nextCursor(c *store.Cursor) *string {
	if c == nil {
		return nil
	}
	s := c.Encode()
	return &s
}

func (s *Server) secretFields(r *http.Request, versionID string) map[string]bool {
	if v, ok := s.secrets.Load(versionID); ok {
		return v.(map[string]bool)
	}
	ver, err := s.Store.GetVersion(r.Context(), versionID)
	if err != nil {
		return map[string]bool{}
	}
	m := present.SecretFields(ver.UISchema)
	s.secrets.Store(versionID, m)
	return m
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
