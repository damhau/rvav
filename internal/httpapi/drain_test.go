package httpapi

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

// An API draining to apply newer connection settings (ADR-0030) is not
// ready, before it reads the database, so the load balancer sends requests
// to the others; its event streams end when it shuts down.
func TestDrainingIsNotReady(t *testing.T) {
	s := &Server{Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	s.Draining.Store(true)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("/readyz while draining = %d", rec.Code)
	}
	s.StopStreams()
	s.StopStreams() // Shutdown may run it again
	select {
	case <-s.stopping():
	default:
		t.Fatal("the streams weren't told to stop")
	}
}
