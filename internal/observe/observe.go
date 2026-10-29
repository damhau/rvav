// Package observe is rvav's metrics and traces (S14.5,
// docs/operations/observability.md). Every process serves Prometheus
// metrics on its own address (RVAV_METRICS_ADDR, ":9090"), apart from the
// API's; traces go to an OpenTelemetry collector when the standard
// OTEL_EXPORTER_OTLP_ENDPOINT is set, from the API through Temporal into
// the activities and the calls they make.
package observe

import (
	"bufio"
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// Registry holds rvav's metrics and the Go runtime's.
var Registry = prometheus.NewRegistry()

var durations = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30}

var (
	httpRequests = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "rvav_http_requests_total",
		Help: "API calls, by route and status code."}, []string{"method", "route", "code"})
	httpDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "rvav_http_request_duration_seconds",
		Help: "How long API calls take, by route (live streams and the console excluded).", Buckets: durations},
		[]string{"method", "route"})
	steps = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "rvav_steps_total",
		Help: "Workflow steps run, by system and outcome (done or failed)."}, []string{"system", "outcome"})
	stepDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "rvav_step_duration_seconds",
		Help: "How long workflow steps take, by system.", Buckets: []float64{0.1, 0.5, 1, 5, 15, 30, 60, 120, 300, 900, 3600}},
		[]string{"system"})
	requestsFinished = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "rvav_requests_finished_total",
		Help: "Requests that ran to an end, by kind and outcome (succeeded, failed, cancelled)."}, []string{"kind", "outcome"})
	requestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "rvav_request_duration_seconds",
		Help: "How long requests ran, from start to end, by kind.", Buckets: []float64{5, 15, 30, 60, 120, 300, 600, 1200, 3600}},
		[]string{"kind"})
	calls = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "rvav_integration_calls_total",
		Help: "Calls to vCenter, EfficientIP, AWX and webhooks, by system and outcome (ok, error)."}, []string{"system", "outcome"})
	callDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "rvav_integration_call_duration_seconds",
		Help: "How long calls to integrations take, by system.", Buckets: durations}, []string{"system"})
	watchConnected = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "rvav_inventory_watch_connected",
		Help: "1 while the sync follows the vCenter's changes, else 0."}, []string{"vcenter"})
	lastFullRead = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "rvav_inventory_last_full_read_timestamp_seconds",
		Help: "When the sync last read the vCenter in full."}, []string{"vcenter"})
)

func init() {
	Registry.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		httpRequests, httpDuration, steps, stepDuration, requestsFinished, requestDuration, calls, callDuration,
		watchConnected, lastFullRead)
}

// Serve answers /metrics on addr until ctx is done ("" serves nothing).
func Serve(ctx context.Context, addr string, log *slog.Logger) {
	if addr == "" {
		return
	}
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(Registry, promhttp.HandlerOpts{}))
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Warn("metrics listener", "addr", addr, "err", err)
		}
	}()
}

// HTTP measures the API's calls by route, the pattern the router matched
// (never the path itself, which holds IDs), and traces them.
func HTTP(next http.Handler) http.Handler {
	measured := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &codeWriter{ResponseWriter: w, code: http.StatusOK}
		next.ServeHTTP(sw, r)
		route := r.Pattern // set by the router on this request
		if route == "" {
			route = "unmatched"
		} else if _, p, ok := strings.Cut(route, " "); ok {
			route = p
		}
		method := r.Method
		if !knownMethods[method] {
			method = "_OTHER" // a caller can't make up labels (S14 review)
		}
		httpRequests.WithLabelValues(method, route, strconv.Itoa(sw.code)).Inc()
		if !strings.HasSuffix(route, "/events") && !strings.Contains(route, "/console/") {
			httpDuration.WithLabelValues(method, route).Observe(time.Since(start).Seconds())
		}
	})
	return otelhttp.NewHandler(measured, "rvav-api", otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string {
		return r.Method + " " + r.URL.Path
	}))
}

var knownMethods = map[string]bool{http.MethodGet: true, http.MethodHead: true, http.MethodPost: true, http.MethodPut: true,
	http.MethodPatch: true, http.MethodDelete: true, http.MethodOptions: true}

type codeWriter struct {
	http.ResponseWriter
	code int
}

func (w *codeWriter) WriteHeader(code int) {
	w.code = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *codeWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *codeWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// Hijack lets the console proxy take over the connection.
func (w *codeWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hj, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("hijacking is not supported")
	}
	w.code = http.StatusSwitchingProtocols
	return hj.Hijack()
}

// Transport measures and traces an integration's outgoing HTTP calls.
func Transport(system string, base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return otelhttp.NewTransport(roundTripper{system: system, base: base})
}

type roundTripper struct {
	system string
	base   http.RoundTripper
}

func (t roundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	start := time.Now()
	resp, err := t.base.RoundTrip(r)
	outcome := "ok"
	if err != nil || resp.StatusCode >= 500 {
		outcome = "error"
	}
	Call(t.system, outcome, time.Since(start))
	return resp, err
}

// Call counts one call to an integration.
func Call(system, outcome string, d time.Duration) {
	calls.WithLabelValues(system, outcome).Inc()
	callDuration.WithLabelValues(system).Observe(d.Seconds())
}

// Step counts a workflow step by the system its key names (vsphere.clone,
// awx.job:linux-baseline, step:nodes/ipam.reserve@web01,
// component:web/vsphere.clone@web01...).
func Step(key string, failed bool, d time.Duration) {
	if i := strings.LastIndex(key, "/"); i >= 0 {
		key = key[i+1:]
	}
	system := key
	if i := strings.IndexAny(key, ".:@"); i >= 0 {
		system = key[:i]
	}
	outcome := "done"
	if failed {
		outcome = "failed"
	}
	steps.WithLabelValues(system, outcome).Inc()
	stepDuration.WithLabelValues(system).Observe(d.Seconds())
}

// RequestFinished counts a request that ran to an end.
func RequestFinished(kind, outcome string, ran time.Duration) {
	requestsFinished.WithLabelValues(kind, outcome).Inc()
	if ran > 0 {
		requestDuration.WithLabelValues(kind).Observe(ran.Seconds())
	}
}

// Watching says whether the sync follows a vCenter's changes.
func Watching(vcenter string, on bool) {
	v := 0.0
	if on {
		v = 1
	}
	watchConnected.WithLabelValues(vcenter).Set(v)
}

// FullRead records a full read of a vCenter.
func FullRead(vcenter string, at time.Time) {
	lastFullRead.WithLabelValues(vcenter).Set(float64(at.Unix()))
}

// Tracing sends traces to the OpenTelemetry collector named by the
// standard OTEL_EXPORTER_OTLP_ENDPOINT (HTTP); without it, nothing is
// traced. The returned function flushes them as the process stops.
func Tracing(ctx context.Context, process string) (func(context.Context) error, error) {
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	if os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") == "" && os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT") == "" {
		return func(context.Context) error { return nil }, nil
	}
	exp, err := otlptracehttp.New(ctx)
	if err != nil {
		return nil, err
	}
	res, _ := resource.Merge(resource.Default(), resource.NewSchemaless(semconv.ServiceName("rvav-"+process)))
	tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exp), sdktrace.WithResource(res))
	otel.SetTracerProvider(tp)
	return tp.Shutdown, nil
}
