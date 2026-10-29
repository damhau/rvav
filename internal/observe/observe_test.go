package observe

import (
	"net/http"
	"net/http/httptest"
	"testing"

	dto "github.com/prometheus/client_model/go"
)

// TestMethodLabel: a made-up HTTP method is counted as _OTHER, so callers
// can't add series (S14 review).
func TestMethodLabel(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) })
	h := HTTP(mux)
	for _, m := range []string{"A1", "A2", "GET"} {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(m, "/api/x", nil))
	}
	count := func(method string) float64 {
		var m dto.Metric
		c, err := httpRequests.GetMetricWithLabelValues(method, "/api/", "404")
		if err != nil {
			t.Fatal(err)
		}
		_ = c.Write(&m)
		return m.GetCounter().GetValue()
	}
	if count("_OTHER") != 2 || count("GET") != 1 || count("A1") != 0 {
		t.Fatalf("_OTHER %v, GET %v, A1 %v", count("_OTHER"), count("GET"), count("A1"))
	}
}
