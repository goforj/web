package webprometheus

import (
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
