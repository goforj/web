package webprometheus

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/goforj/web"
	"github.com/goforj/web/adapter/echoweb"
	"github.com/goforj/web/webtest"
	"github.com/prometheus/client_golang/prometheus"
)

func TestMiddlewareAndHandlerExposePrometheusMetrics(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics, err := New(Config{
		Registerer:         registry,
		Gatherer:           registry,
		DisableCompression: true,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	adapter := echoweb.New()
	router := adapter.Router()
	router.Use(metrics.Middleware())
	router.GET("/users/:id", func(r web.Context) error {
		return r.Text(http.StatusCreated, "ok")
	})
	router.GET("/metrics", metrics.Handler())

	req := httptest.NewRequest(http.MethodGet, "/users/42", nil)
	req.Host = "example.com"
	rec := httptest.NewRecorder()
	adapter.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}

	metricsReq := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	metricsRec := httptest.NewRecorder()
	adapter.ServeHTTP(metricsRec, metricsReq)

	if metricsRec.Code != http.StatusOK {
		t.Fatalf("metrics status = %d body=%s", metricsRec.Code, metricsRec.Body.String())
	}

	body, err := io.ReadAll(metricsRec.Body)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	text := string(body)

	if !strings.Contains(text, `web_requests_total{code="201",host="example.com",method="GET",url="/users/:id"} 1`) {
		t.Fatalf("metrics output missing request counter:\n%s", text)
	}
	if !strings.Contains(text, `web_requests_in_flight{method="GET",url="/users/:id"} 0`) {
		t.Fatalf("metrics output missing in-flight gauge:\n%s", text)
	}
	if !strings.Contains(text, `web_request_duration_seconds_count{code="201",host="example.com",method="GET",url="/users/:id"} 1`) {
		t.Fatalf("metrics output missing duration histogram count:\n%s", text)
	}
	if !strings.Contains(text, `web_response_size_bytes_count{code="201",host="example.com",method="GET",url="/users/:id"} 1`) {
		t.Fatalf("metrics output missing response size histogram count:\n%s", text)
	}
	if !strings.Contains(text, `web_request_size_bytes_count{code="201",host="example.com",method="GET",url="/users/:id"} 1`) {
		t.Fatalf("metrics output missing request size histogram count:\n%s", text)
	}
}

func TestMiddlewareNormalizesErrorStatus(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics, err := New(Config{
		Registerer: registry,
		Gatherer:   registry,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	adapter := echoweb.New()
	router := adapter.Router()
	router.Use(metrics.Middleware())
	router.GET("/boom", func(r web.Context) error {
		return http.ErrAbortHandler
	})
	router.GET("/metrics", metrics.Handler())

	req := httptest.NewRequest(http.MethodGet, "/boom", nil)
	req.Host = "example.com"
	rec := httptest.NewRecorder()
	adapter.ServeHTTP(rec, req)

	metricsReq := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	metricsRec := httptest.NewRecorder()
	adapter.ServeHTTP(metricsRec, metricsReq)

	body, err := io.ReadAll(metricsRec.Body)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	text := string(body)
	if !strings.Contains(text, `web_requests_total{code="500",host="example.com",method="GET",url="/boom"} 1`) {
		t.Fatalf("metrics output missing normalized error status:\n%s", text)
	}
}

func TestMiddlewareUsesRequestPathFor404ByDefault(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics, err := New(Config{
		Registerer: registry,
		Gatherer:   registry,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	adapter := echoweb.New()
	router := adapter.Router()
	router.Use(metrics.Middleware())
	router.GET("/metrics", metrics.Handler())

	req := httptest.NewRequest(http.MethodGet, "/missing", nil)
	req.Host = "example.com"
	rec := httptest.NewRecorder()
	adapter.ServeHTTP(rec, req)

	metricsReq := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	metricsRec := httptest.NewRecorder()
	adapter.ServeHTTP(metricsRec, metricsReq)

	body, err := io.ReadAll(metricsRec.Body)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	text := string(body)
	if !strings.Contains(text, `web_requests_total{code="404",host="example.com",method="GET",url="/missing"} 1`) {
		t.Fatalf("metrics output missing 404 request-path label:\n%s", text)
	}
}

func TestMiddlewareSupportsCustomLabelsAndHooks(t *testing.T) {
	registry := prometheus.NewRegistry()
	var beforeCalled bool
	var afterCalled bool
	metrics, err := New(Config{
		Registerer: registry,
		Gatherer:   registry,
		BeforeNext: func(r web.Context) {
			beforeCalled = true
			r.Set("tenant", "alpha")
		},
		AfterNext: func(r web.Context, err error) {
			afterCalled = true
		},
		LabelFuncs: map[string]LabelValueFunc{
			"tenant": func(r web.Context, err error) string {
				value, _ := r.Get("tenant").(string)
				return value
			},
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	adapter := echoweb.New()
	router := adapter.Router()
	router.Use(metrics.Middleware())
	router.GET("/custom", func(r web.Context) error {
		return r.NoContent(http.StatusNoContent)
	})
	router.GET("/metrics", metrics.Handler())

	req := httptest.NewRequest(http.MethodGet, "/custom", nil)
	req.Host = "example.com"
	rec := httptest.NewRecorder()
	adapter.ServeHTTP(rec, req)

	if !beforeCalled || !afterCalled {
		t.Fatalf("hooks called = before:%v after:%v", beforeCalled, afterCalled)
	}

	metricsReq := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	metricsRec := httptest.NewRecorder()
	adapter.ServeHTTP(metricsRec, metricsReq)

	body, err := io.ReadAll(metricsRec.Body)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	text := string(body)
	if !strings.Contains(text, `web_requests_total{code="204",host="example.com",method="GET",tenant="alpha",url="/custom"} 1`) {
		t.Fatalf("metrics output missing custom label:\n%s", text)
	}
}

func TestRunPushGatewayGathererRequiresURL(t *testing.T) {
	err := RunPushGatewayGatherer(context.Background(), PushGatewayConfig{})
	if err == nil || !strings.Contains(err.Error(), "push gateway URL is missing") {
		t.Fatalf("err = %v", err)
	}
}

func TestRunPushGatewayGathererPostsMetrics(t *testing.T) {
	registry := prometheus.NewRegistry()
	counter := prometheus.NewCounter(prometheus.CounterOpts{Name: "test_counter_total", Help: "test"})
	if err := registry.Register(counter); err != nil {
		t.Fatalf("Register: %v", err)
	}
	counter.Inc()

	requests := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("ReadAll: %v", err)
		}
		requests <- string(body)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- RunPushGatewayGatherer(ctx, PushGatewayConfig{
			PushGatewayURL: server.URL,
			PushInterval:   10 * time.Millisecond,
			Gatherer:       registry,
		})
	}()

	select {
	case body := <-requests:
		if !strings.Contains(body, "test_counter_total") {
			t.Fatalf("push body = %s", body)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for push request")
	}

	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}

func TestDefaultConvenienceAPIsAndHelpers(t *testing.T) {
	t.Run("default singleton and convenience wrappers", func(t *testing.T) {
		if got := Default(); got != Default() {
			t.Fatal("Default() should return the singleton instance")
		}
		if got := Middleware(); got == nil {
			t.Fatal("Middleware() returned nil")
		}
		if got := Handler(); got == nil {
			t.Fatal("Handler() returned nil")
		}
		if got := MustNew(Config{Registerer: prometheus.NewRegistry(), Gatherer: prometheus.NewRegistry()}); got == nil {
			t.Fatal("MustNew() returned nil")
		}
	})

	t.Run("write gathered metrics and defaults", func(t *testing.T) {
		registry := prometheus.NewRegistry()
		counter := prometheus.NewCounter(prometheus.CounterOpts{Name: "demo_total", Help: "demo"})
		registry.MustRegister(counter)
		counter.Inc()

		var out bytes.Buffer
		if err := WriteGatheredMetrics(&out, registry); err != nil {
			t.Fatalf("WriteGatheredMetrics(): %v", err)
		}
		if !strings.Contains(out.String(), "demo_total") {
			t.Fatalf("metrics output = %s", out.String())
		}

		cfg := withDefaults(Config{})
		if cfg.Namespace != defaultNamespace {
			t.Fatalf("Namespace = %q", cfg.Namespace)
		}
		if cfg.Registerer == nil || cfg.Gatherer == nil || cfg.CounterOptsFunc == nil || cfg.HistogramOptsFunc == nil || cfg.timeNow == nil {
			t.Fatal("withDefaults() did not populate defaults")
		}
	})

	t.Run("route and status helpers", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/raw/path?expand=1", nil)
		ctx := webtest.NewContext(req, nil, "", nil)
		if got, want := routeLabel(ctx, false), "/raw/path"; got != want {
			t.Fatalf("routeLabel(request path) = %q, want %q", got, want)
		}
		if got, want := routeLabel(ctx, true), "/raw/path?expand=1"; got != want {
			t.Fatalf("routeLabel(uri) = %q, want %q", got, want)
		}
		ctx = webtest.NewContext(nil, nil, "", nil)
		if got, want := routeLabel(ctx, true), "/"; got != want {
			t.Fatalf("routeLabel(default uri) = %q, want %q", got, want)
		}

		if got := normalizeStatus(false, 0, "", errors.New("boom")); got != http.StatusNotFound {
			t.Fatalf("normalizeStatus(not found) = %d", got)
		}
		if got := normalizeStatus(false, 0, "/path", errors.New("boom")); got != http.StatusInternalServerError {
			t.Fatalf("normalizeStatus(internal) = %d", got)
		}
		if got := normalizeStatus(true, http.StatusAccepted, "/path", nil); got != http.StatusAccepted {
			t.Fatalf("normalizeStatus(committed) = %d", got)
		}
		if got := normalizeSize(-1); got != 0 {
			t.Fatalf("normalizeSize(-1) = %d", got)
		}
		if got := normalizeSize(12); got != 12 {
			t.Fatalf("normalizeSize(12) = %d", got)
		}
	})

	t.Run("bucket size and request size helpers", func(t *testing.T) {
		fallback := []float64{1, 2, 3}
		if got := bucketsOrDefault(nil, fallback); len(got) != len(fallback) {
			t.Fatalf("bucketsOrDefault(nil) len = %d", len(got))
		}
		if got := bucketsOrDefault([]float64{9}, fallback); len(got) != 1 || got[0] != 9 {
			t.Fatalf("bucketsOrDefault(custom) = %#v", got)
		}

		req := httptest.NewRequest(http.MethodPost, "/docs", strings.NewReader("body"))
		req.Host = "example.com"
		req.Header.Set("X-Test", "true")
		if got := computeApproximateRequestSize(req); got <= 0 {
			t.Fatalf("computeApproximateRequestSize() = %d", got)
		}
		if got := computeApproximateRequestSize(nil); got != 0 {
			t.Fatalf("computeApproximateRequestSize(nil) = %d", got)
		}
	})

	t.Run("register collector reuses already registered instance", func(t *testing.T) {
		registry := prometheus.NewRegistry()
		counter := prometheus.NewCounter(prometheus.CounterOpts{Name: "dup_total", Help: "dup"})
		reused, err := registerCollector(registry, counter)
		if err != nil {
			t.Fatalf("registerCollector(first): %v", err)
		}
		again := prometheus.NewCounter(prometheus.CounterOpts{Name: "dup_total", Help: "dup"})
		reusedAgain, err := registerCollector(registry, again)
		if err != nil {
			t.Fatalf("registerCollector(second): %v", err)
		}
		if reusedAgain != reused {
			t.Fatal("registerCollector() should reuse the existing collector")
		}
	})

	t.Run("must new panics on registration failure", func(t *testing.T) {
		defer func() {
			if recover() == nil {
				t.Fatal("MustNew() should panic when New() fails")
			}
		}()
		_ = MustNew(Config{Registerer: failingRegisterer{err: errors.New("register failed")}, Gatherer: prometheus.NewRegistry()})
	})

	t.Run("run push gateway handler paths", func(t *testing.T) {
		registry := prometheus.NewRegistry()
		counter := prometheus.NewCounter(prometheus.CounterOpts{Name: "push_total", Help: "push"})
		registry.MustRegister(counter)
		counter.Inc()

		handlerErr := errors.New("stop")
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
		}))
		defer server.Close()

		err := RunPushGatewayGatherer(context.Background(), PushGatewayConfig{
			PushGatewayURL: server.URL,
			PushInterval:   time.Millisecond,
			Gatherer:       registry,
			ErrorHandler: func(err error) error {
				if !strings.Contains(err.Error(), "did not succeed") {
					t.Fatalf("unexpected handler error = %v", err)
				}
				return handlerErr
			},
		})
		if !errors.Is(err, handlerErr) {
			t.Fatalf("RunPushGatewayGatherer() error = %v", err)
		}
	})

	t.Run("misc helpers", func(t *testing.T) {
		if got := containsAt([]string{"a", "b"}, "z"); got != -1 {
			t.Fatalf("containsAt() = %d", got)
		}
		ctx := requestOnlyContext{}
		if got, want := routeLabel(ctx, true), "unknown"; got != want {
			t.Fatalf("routeLabel(unknown) = %q, want %q", got, want)
		}

		registry := failingRegisterer{err: errors.New("boom")}
		_, err := registerCollector[prometheus.Collector](registry, prometheus.NewCounter(prometheus.CounterOpts{Name: "collector_total", Help: "collector"}))
		if err == nil || !strings.Contains(err.Error(), "boom") {
			t.Fatalf("registerCollector() error = %v", err)
		}
	})
}

type failingRegisterer struct {
	err error
}

func (f failingRegisterer) Register(prometheus.Collector) error { return f.err }
func (f failingRegisterer) MustRegister(...prometheus.Collector) {}
func (f failingRegisterer) Unregister(prometheus.Collector) bool { return false }

type requestOnlyContext struct{}

func (requestOnlyContext) Context() context.Context                    { return context.Background() }
func (requestOnlyContext) Method() string                              { return http.MethodGet }
func (requestOnlyContext) Path() string                                { return "" }
func (requestOnlyContext) URI() string                                 { return "" }
func (requestOnlyContext) Scheme() string                              { return "http" }
func (requestOnlyContext) Host() string                                { return "example.com" }
func (requestOnlyContext) Param(string) string                         { return "" }
func (requestOnlyContext) Query(string) string                         { return "" }
func (requestOnlyContext) Header(string) string                        { return "" }
func (requestOnlyContext) Cookie(string) (*http.Cookie, error)         { return nil, http.ErrNoCookie }
func (requestOnlyContext) RealIP() string                              { return "127.0.0.1" }
func (requestOnlyContext) Request() *http.Request                      { return nil }
func (requestOnlyContext) SetRequest(*http.Request)                    {}
func (requestOnlyContext) Response() web.Response                      { return nil }
func (requestOnlyContext) ResponseWriter() http.ResponseWriter         { return httptest.NewRecorder() }
func (requestOnlyContext) SetResponseWriter(http.ResponseWriter)       {}
func (requestOnlyContext) Bind(any) error                              { return nil }
func (requestOnlyContext) Set(string, any)                             {}
func (requestOnlyContext) Get(string) any                              { return nil }
func (requestOnlyContext) AddHeader(string, string)                    {}
func (requestOnlyContext) SetHeader(string, string)                    {}
func (requestOnlyContext) SetCookie(*http.Cookie)                      {}
func (requestOnlyContext) JSON(int, any) error                         { return nil }
func (requestOnlyContext) Blob(int, string, []byte) error              { return nil }
func (requestOnlyContext) File(string) error                           { return nil }
func (requestOnlyContext) Text(int, string) error                      { return nil }
func (requestOnlyContext) HTML(int, string) error                      { return nil }
func (requestOnlyContext) NoContent(int) error                         { return nil }
func (requestOnlyContext) Redirect(int, string) error                  { return nil }
func (requestOnlyContext) StatusCode() int                             { return 0 }
func (requestOnlyContext) Native() any                                 { return nil }

var _ prometheus.Registerer = failingRegisterer{}
var _ web.Context = requestOnlyContext{}
