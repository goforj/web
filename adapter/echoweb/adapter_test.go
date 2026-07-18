package echoweb

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/goforj/web"
	"github.com/gorilla/websocket"
	echo "github.com/labstack/echo/v5"
)

func TestRouterRegistersRouteAndContext(t *testing.T) {
	adapter := New()
	adapter.Router().GET("/users/:id", func(r web.Context) error {
		if got := r.Param("id"); got != "42" {
			t.Fatalf("param id = %q", got)
		}
		if got := r.Path(); got != "/users/:id" {
			t.Fatalf("path = %q", got)
		}
		if got := r.URI(); got != "/users/42" {
			t.Fatalf("uri = %q", got)
		}
		if got := r.Scheme(); got != "http" {
			t.Fatalf("scheme = %q", got)
		}
		if got := r.Host(); got != "example.com" {
			t.Fatalf("host = %q", got)
		}
		if got := r.Request(); got == nil {
			t.Fatal("request is nil")
		}
		if got := r.ResponseWriter(); got == nil {
			t.Fatal("response writer is nil")
		}
		if got := r.Response(); got == nil {
			t.Fatal("response is nil")
		}
		if got := r.Response().Writer(); got == nil {
			t.Fatal("response writer is nil")
		}
		if got := r.Response().Committed(); got {
			t.Fatal("response should not be committed before write")
		}
		return r.JSON(http.StatusOK, map[string]any{
			"id":     r.Param("id"),
			"method": r.Method(),
		})
	})

	req := httptest.NewRequest(http.MethodGet, "/users/42", nil)
	req.Host = "example.com"
	rec := httptest.NewRecorder()
	adapter.Echo().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); body != "{\"id\":\"42\",\"method\":\"GET\"}\n" {
		t.Fatalf("body = %q", body)
	}
}

func TestContextResponseExposesStatusSizeAndCommitted(t *testing.T) {
	adapter := New()
	adapter.Router().GET("/response", func(r web.Context) error {
		if err := r.Text(http.StatusAccepted, "ok"); err != nil {
			t.Fatalf("Text: %v", err)
		}
		if got := r.Response().StatusCode(); got != http.StatusAccepted {
			t.Fatalf("status = %d", got)
		}
		if got := r.Response().Size(); got != 2 {
			t.Fatalf("size = %d", got)
		}
		if !r.Response().Committed() {
			t.Fatal("response should be committed after write")
		}
		if got := r.StatusCode(); got != http.StatusAccepted {
			t.Fatalf("context status = %d", got)
		}
		return nil
	})

	req := httptest.NewRequest(http.MethodGet, "/response", nil)
	rec := httptest.NewRecorder()
	adapter.Echo().ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); body != "ok" {
		t.Fatalf("body = %q", body)
	}
}

func TestRouterGroupAndMiddleware(t *testing.T) {
	adapter := New()
	group := adapter.Router().Group("/api", func(next web.Handler) web.Handler {
		return func(r web.Context) error {
			r.Set("trace", "group")
			return next(r)
		}
	})

	group.GET("/ping", func(r web.Context) error {
		if got := r.Get("trace"); got != "group" {
			t.Fatalf("trace = %#v", got)
		}
		return r.Text(http.StatusOK, "pong")
	})

	req := httptest.NewRequest(http.MethodGet, "/api/ping", nil)
	rec := httptest.NewRecorder()
	adapter.Echo().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); body != "pong" {
		t.Fatalf("body = %q", body)
	}
}

func TestRouterUseAppliesMiddleware(t *testing.T) {
	adapter := New()
	router := adapter.Router()
	router.Use(func(next web.Handler) web.Handler {
		return func(r web.Context) error {
			r.SetHeader("X-Web-MW", "on")
			return next(r)
		}
	})

	router.GET("/mw", func(r web.Context) error {
		return r.NoContent(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/mw", nil)
	rec := httptest.NewRecorder()
	adapter.Echo().ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := rec.Header().Get("X-Web-MW"); got != "on" {
		t.Fatalf("X-Web-MW = %q", got)
	}
}

// TestRouterUseConstructsMiddlewareOnceForEveryDispatchKind verifies one shared root middleware lifecycle.
func TestRouterUseConstructsMiddlewareOnceForEveryDispatchKind(t *testing.T) {
	adapter := New()
	router := adapter.Router()
	constructed := 0
	handled := 0
	router.Use(func(next web.Handler) web.Handler {
		constructed++
		return func(r web.Context) error {
			handled++
			native, ok := UnwrapContext(r)
			if !ok || native == nil || r.Native() != native {
				t.Fatalf("middleware context did not unwrap for %s %s", r.Method(), r.URI())
			}
			return next(r)
		}
	})
	if constructed != 1 {
		t.Fatalf("middleware constructed %d times during registration, want 1", constructed)
	}

	router.GET("/one", func(r web.Context) error {
		return r.NoContent(http.StatusNoContent)
	})
	router.GET("/two", func(r web.Context) error {
		return r.NoContent(http.StatusNoContent)
	})
	adapter.Echo().GET("/native", func(c *echo.Context) error {
		return c.NoContent(http.StatusAccepted)
	})

	requests := []struct {
		method string
		path   string
		status int
	}{
		{method: http.MethodGet, path: "/one", status: http.StatusNoContent},
		{method: http.MethodGet, path: "/two", status: http.StatusNoContent},
		{method: http.MethodGet, path: "/native", status: http.StatusAccepted},
		{method: http.MethodPost, path: "/one", status: http.StatusMethodNotAllowed},
		{method: http.MethodGet, path: "/missing", status: http.StatusNotFound},
	}
	for _, request := range requests {
		req := httptest.NewRequest(request.method, request.path, nil)
		rec := httptest.NewRecorder()
		adapter.Echo().ServeHTTP(rec, req)
		if rec.Code != request.status {
			t.Fatalf("%s %s status = %d, want %d", request.method, request.path, rec.Code, request.status)
		}
	}

	if constructed != 1 {
		t.Fatalf("middleware constructed %d times, want 1", constructed)
	}
	if handled != len(requests) {
		t.Fatalf("middleware handled %d requests, want %d", handled, len(requests))
	}
}

// TestRouterUseAppendsWithoutReconstructingMiddleware verifies late registration preserves existing middleware state.
func TestRouterUseAppendsWithoutReconstructingMiddleware(t *testing.T) {
	adapter := New()
	router := adapter.Router()
	firstConstructed := 0
	router.Use(func(next web.Handler) web.Handler {
		firstConstructed++
		return func(r web.Context) error {
			r.SetHeader("X-First", "on")
			return next(r)
		}
	})
	router.GET("/cached", func(r web.Context) error {
		return r.NoContent(http.StatusNoContent)
	})

	request := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/cached", nil)
		rec := httptest.NewRecorder()
		adapter.Echo().ServeHTTP(rec, req)
		return rec
	}

	first := request()
	if first.Code != http.StatusNoContent {
		t.Fatalf("first status = %d", first.Code)
	}
	if got := first.Header().Get("X-First"); got != "on" {
		t.Fatalf("first X-First = %q", got)
	}

	secondConstructed := 0
	router.Use(func(next web.Handler) web.Handler {
		secondConstructed++
		return func(r web.Context) error {
			r.SetHeader("X-Second", "on")
			return next(r)
		}
	})
	if firstConstructed != 1 || secondConstructed != 1 {
		t.Fatalf("constructor counts = first:%d second:%d, want 1 each", firstConstructed, secondConstructed)
	}

	second := request()
	if second.Code != http.StatusNoContent {
		t.Fatalf("second status = %d", second.Code)
	}
	if got := second.Header().Get("X-First"); got != "on" {
		t.Fatalf("second X-First = %q", got)
	}
	if got := second.Header().Get("X-Second"); got != "on" {
		t.Fatalf("second X-Second = %q", got)
	}
	if firstConstructed != 1 || secondConstructed != 1 {
		t.Fatalf("constructor counts after requests = first:%d second:%d, want 1 each", firstConstructed, secondConstructed)
	}
}

// TestRouterUseAfterRouteRegistration verifies root middleware applies to routes that already exist.
func TestRouterUseAfterRouteRegistration(t *testing.T) {
	adapter := New()
	router := adapter.Router()
	router.GET("/late", func(r web.Context) error {
		return r.NoContent(http.StatusNoContent)
	})

	before := httptest.NewRecorder()
	adapter.Echo().ServeHTTP(before, httptest.NewRequest(http.MethodGet, "/late", nil))
	if before.Code != http.StatusNoContent {
		t.Fatalf("before middleware status = %d", before.Code)
	}
	if got := before.Header().Get("X-Late"); got != "" {
		t.Fatalf("before middleware X-Late = %q", got)
	}

	constructed := 0
	handled := 0
	router.Use(func(next web.Handler) web.Handler {
		constructed++
		return func(r web.Context) error {
			handled++
			r.SetHeader("X-Late", "on")
			return next(r)
		}
	})
	if constructed != 1 {
		t.Fatalf("middleware constructed %d times during registration, want 1", constructed)
	}

	for range 2 {
		req := httptest.NewRequest(http.MethodGet, "/late", nil)
		rec := httptest.NewRecorder()
		adapter.Echo().ServeHTTP(rec, req)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d", rec.Code)
		}
		if got := rec.Header().Get("X-Late"); got != "on" {
			t.Fatalf("X-Late = %q", got)
		}
	}
	if constructed != 1 || handled != 2 {
		t.Fatalf("lifecycle counts = constructed:%d handled:%d, want 1 and 2", constructed, handled)
	}
}

// TestRouterUseDispatchesOriginallySelectedMethodAfterMutation verifies method overrides cannot select another handler.
func TestRouterUseDispatchesOriginallySelectedMethodAfterMutation(t *testing.T) {
	adapter := New()
	router := adapter.Router()
	router.Use(func(next web.Handler) web.Handler {
		return func(r web.Context) error {
			request := r.Request()
			request.Method = http.MethodDelete
			r.SetRequest(request)
			return next(r)
		}
	})
	router.POST("/method", func(r web.Context) error {
		return r.Text(http.StatusOK, "post:"+r.Method())
	})
	router.DELETE("/method", func(r web.Context) error {
		return r.Text(http.StatusOK, "delete")
	})

	rec := httptest.NewRecorder()
	adapter.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/method", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Body.String(); got != "post:DELETE" {
		t.Fatalf("body = %q, want originally selected POST handler", got)
	}
}

// TestRouterUseDispatchesOriginallySelectedPathAfterMutation verifies public path mutations cannot select another handler.
func TestRouterUseDispatchesOriginallySelectedPathAfterMutation(t *testing.T) {
	testCases := []struct {
		name         string
		mutatePath   bool
		clearPattern bool
		pattern      string
	}{
		{name: "path disagrees with request pattern", mutatePath: true},
		{name: "request pattern disagrees with path", pattern: "/other"},
		{name: "replacement request has no pattern", mutatePath: true, clearPattern: true},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			adapter := New()
			router := adapter.Router()
			router.Use(func(next web.Handler) web.Handler {
				return func(r web.Context) error {
					native, ok := UnwrapContext(r)
					if !ok {
						return echo.ErrInternalServerError
					}
					if testCase.mutatePath {
						native.SetPath("/other")
					}
					if testCase.clearPattern {
						request := r.Request().Clone(r.Context())
						request.Pattern = ""
						r.SetRequest(request)
					} else if testCase.pattern != "" {
						request := r.Request()
						request.Pattern = testCase.pattern
						r.SetRequest(request)
					}
					return next(r)
				}
			})
			router.GET("/selected", func(r web.Context) error {
				return r.Text(http.StatusOK, "selected")
			})
			router.GET("/other", func(r web.Context) error {
				return r.Text(http.StatusOK, "other")
			})

			rec := httptest.NewRecorder()
			adapter.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/selected", nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
			}
			if got := rec.Body.String(); got != "selected" {
				t.Fatalf("body = %q, want originally selected handler", got)
			}
		})
	}
}

// TestRouterUseAppliesMiddlewareToUnmatchedRoutes verifies root middleware observes 404 and 405 dispatches.
func TestRouterUseAppliesMiddlewareToUnmatchedRoutes(t *testing.T) {
	adapter := New()
	router := adapter.Router()
	router.Use(func(next web.Handler) web.Handler {
		return func(r web.Context) error {
			r.SetHeader("X-Unmatched", "on")
			return next(r)
		}
	})

	for range 2 {
		req := httptest.NewRequest(http.MethodGet, "/missing", nil)
		rec := httptest.NewRecorder()
		adapter.Echo().ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d", rec.Code)
		}
		if got := rec.Header().Get("X-Unmatched"); got != "on" {
			t.Fatalf("X-Unmatched = %q", got)
		}
	}
}

// TestRouterUseWithLaterEchoMiddlewarePreservesSelectedRoute verifies Web and native middleware compose in registration order.
func TestRouterUseWithLaterEchoMiddlewarePreservesSelectedRoute(t *testing.T) {
	adapter := New()
	router := adapter.Router()
	events := make([]string, 0, 5)
	constructed := 0
	router.Use(func(next web.Handler) web.Handler {
		constructed++
		return func(r web.Context) error {
			events = append(events, "web-before")
			err := next(r)
			events = append(events, "web-after")
			return err
		}
	})
	adapter.Echo().Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			events = append(events, "echo-before")
			err := next(c)
			events = append(events, "echo-after")
			return err
		}
	})
	router.GET("/one", func(r web.Context) error {
		events = append(events, "handler-one")
		return r.Text(http.StatusOK, "one")
	})
	router.GET("/two", func(r web.Context) error {
		events = append(events, "handler-two")
		return r.Text(http.StatusOK, "two")
	})

	for _, path := range []string{"/one", "/two", "/one"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		adapter.Echo().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d", path, rec.Code)
		}
		if got := rec.Body.String(); got != path[1:] {
			t.Fatalf("%s body = %q", path, got)
		}
		want := []string{"web-before", "echo-before", "handler-" + path[1:], "echo-after", "web-after"}
		if got := strings.Join(events, ","); got != strings.Join(want, ",") {
			t.Fatalf("%s events = %q, want %q", path, got, strings.Join(want, ","))
		}
		events = events[:0]
	}
	if constructed != 1 {
		t.Fatalf("middleware constructed %d times, want 1", constructed)
	}
}

// TestRouterUseWithEarlierEchoMiddlewarePreservesOrder verifies wrapped engines preserve earlier native middleware ordering.
func TestRouterUseWithEarlierEchoMiddlewarePreservesOrder(t *testing.T) {
	engine := echo.New()
	events := make([]string, 0, 5)
	engine.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			events = append(events, "echo-before")
			err := next(c)
			events = append(events, "echo-after")
			return err
		}
	})

	adapter := Wrap(engine)
	constructed := 0
	adapter.Router().Use(func(next web.Handler) web.Handler {
		constructed++
		return func(r web.Context) error {
			events = append(events, "web-before")
			err := next(r)
			events = append(events, "web-after")
			return err
		}
	})
	adapter.Router().GET("/ordered", func(r web.Context) error {
		events = append(events, "handler")
		return r.NoContent(http.StatusNoContent)
	})

	rec := httptest.NewRecorder()
	adapter.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ordered", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d", rec.Code)
	}
	want := "echo-before,web-before,handler,web-after,echo-after"
	if got := strings.Join(events, ","); got != want {
		t.Fatalf("events = %q, want %q", got, want)
	}
	if constructed != 1 {
		t.Fatalf("middleware constructed %d times, want 1", constructed)
	}
}

// TestRouterUseSupportsMultipleAdaptersOnOneEchoEngine verifies each wrapper keeps an independent root chain.
func TestRouterUseSupportsMultipleAdaptersOnOneEchoEngine(t *testing.T) {
	engine := echo.New()
	first := Wrap(engine)
	firstConstructed := 0
	first.Router().Use(func(next web.Handler) web.Handler {
		firstConstructed++
		return func(r web.Context) error {
			r.SetHeader("X-First-Adapter", "on")
			return next(r)
		}
	})

	second := Wrap(engine)
	secondConstructed := 0
	second.Router().Use(func(next web.Handler) web.Handler {
		secondConstructed++
		return func(r web.Context) error {
			r.SetHeader("X-Second-Adapter", "on")
			return next(r)
		}
	})
	second.Router().GET("/shared", func(r web.Context) error {
		return r.NoContent(http.StatusNoContent)
	})

	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/shared", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := rec.Header().Get("X-First-Adapter"); got != "on" {
		t.Fatalf("X-First-Adapter = %q", got)
	}
	if got := rec.Header().Get("X-Second-Adapter"); got != "on" {
		t.Fatalf("X-Second-Adapter = %q", got)
	}
	if firstConstructed != 1 || secondConstructed != 1 {
		t.Fatalf("constructor counts = first:%d second:%d, want 1 each", firstConstructed, secondConstructed)
	}
}

func TestRouterUsePropagatesBoundContextToRouteHandler(t *testing.T) {
	type contextKey struct{}

	adapter := New()
	router := adapter.Router()
	router.Use(func(next web.Handler) web.Handler {
		return func(r web.Context) error {
			web.BindContext(r, context.WithValue(r.Context(), contextKey{}, "root"))
			return next(r)
		}
	})

	router.GET("/mw-context", func(r web.Context) error {
		if got := r.Context().Value(contextKey{}); got != "root" {
			t.Fatalf("context value = %#v, want root", got)
		}
		if got := r.Request().Context().Value(contextKey{}); got != "root" {
			t.Fatalf("request context value = %#v, want root", got)
		}
		return r.NoContent(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/mw-context", nil)
	rec := httptest.NewRecorder()
	adapter.Echo().ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestRouterUsePropagatesAppSourceNameToRouteHandler(t *testing.T) {
	adapter := New()
	router := adapter.Router()
	router.Use(func(next web.Handler) web.Handler {
		return func(r web.Context) error {
			carrier, ok := r.(interface{ SetAppSourceName(string) })
			if !ok {
				t.Fatal("context does not support app source names")
			}
			carrier.SetAppSourceName("http")
			return next(r)
		}
	})

	router.GET("/mw-source", func(r web.Context) error {
		provider, ok := r.Context().(interface{ AppSourceName() string })
		if !ok {
			t.Fatal("request context does not expose app source name")
		}
		if got := provider.AppSourceName(); got != "http" {
			t.Fatalf("app source name = %q, want http", got)
		}
		return r.NoContent(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/mw-source", nil)
	rec := httptest.NewRecorder()
	adapter.Echo().ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestRouterPreRunsBeforeRouting(t *testing.T) {
	adapter := New()
	router := adapter.Router()
	constructed := 0
	handled := 0
	router.Pre(func(next web.Handler) web.Handler {
		constructed++
		return func(r web.Context) error {
			handled++
			req := r.Request()
			req.URL.Path = "/ready"
			req.RequestURI = "/ready"
			r.SetRequest(req)
			return next(r)
		}
	})
	if constructed != 1 {
		t.Fatalf("middleware constructed %d times during registration, want 1", constructed)
	}
	router.GET("/ready", func(r web.Context) error {
		return r.Text(http.StatusOK, "pre")
	})

	for range 2 {
		req := httptest.NewRequest(http.MethodGet, "/pending", nil)
		rec := httptest.NewRecorder()
		adapter.Echo().ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
		}
		if body := rec.Body.String(); body != "pre" {
			t.Fatalf("body = %q", body)
		}
	}
	if constructed != 1 || handled != 2 {
		t.Fatalf("lifecycle counts = constructed:%d handled:%d, want 1 and 2", constructed, handled)
	}
}

func TestContextSupportsHeadersAndBlobResponses(t *testing.T) {
	adapter := New()
	adapter.Router().GET("/blob", func(r web.Context) error {
		r.SetHeader("Cache-Control", "public, max-age=60")
		return r.Blob(http.StatusOK, "image/x-icon", []byte{1, 2, 3})
	})

	req := httptest.NewRequest(http.MethodGet, "/blob", nil)
	rec := httptest.NewRecorder()
	adapter.Echo().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); got != "public, max-age=60" {
		t.Fatalf("Cache-Control = %q", got)
	}
	if got := rec.Header().Get("Content-Type"); got != "image/x-icon" {
		t.Fatalf("Content-Type = %q", got)
	}
	if body := rec.Body.String(); body != string([]byte{1, 2, 3}) {
		t.Fatalf("body = %q", body)
	}
}

func TestContextSupportsFileResponses(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "web-file-*")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	if _, err := file.WriteString("file-body"); err != nil {
		t.Fatalf("WriteString: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	adapter := New()
	adapter.Router().GET("/file", func(r web.Context) error {
		return r.File(file.Name())
	})

	req := httptest.NewRequest(http.MethodGet, "/file", nil)
	rec := httptest.NewRecorder()
	adapter.Echo().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if body := rec.Body.String(); body != "file-body" {
		t.Fatalf("body = %q", body)
	}
}

func TestContextSupportsCookiesAndRealIP(t *testing.T) {
	adapter := New()
	adapter.Router().GET("/cookie", func(r web.Context) error {
		r.SetCookie(&http.Cookie{
			Name:  "session",
			Value: "abc123",
			Path:  "/",
		})
		cookie, err := r.Cookie("incoming")
		if err != nil {
			t.Fatalf("Cookie: %v", err)
		}
		return r.JSON(http.StatusOK, map[string]any{
			"incoming": cookie.Value,
			"real_ip":  r.RealIP(),
		})
	})

	req := httptest.NewRequest(http.MethodGet, "/cookie", nil)
	req.AddCookie(&http.Cookie{Name: "incoming", Value: "present"})
	req.Header.Set("X-Forwarded-For", "203.0.113.10")
	rec := httptest.NewRecorder()
	adapter.Echo().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := rec.Header().Get("Set-Cookie"); !strings.Contains(got, "session=abc123") {
		t.Fatalf("Set-Cookie = %q", got)
	}
	if body := rec.Body.String(); body != "{\"incoming\":\"present\",\"real_ip\":\"203.0.113.10\"}\n" {
		t.Fatalf("body = %q", body)
	}
}

func TestRouterRegistersWebSocketRoute(t *testing.T) {
	adapter := New()
	handlerErr := make(chan error, 1)
	adapter.Router().GETWS("/ws", func(r web.Context, conn web.WebSocketConn) error {
		var payload map[string]any
		if err := conn.ReadJSON(&payload); err != nil {
			handlerErr <- err
			return err
		}
		payload["path"] = r.Path()
		if err := conn.WriteJSON(payload); err != nil {
			handlerErr <- err
			return err
		}
		err := conn.Close()
		handlerErr <- err
		return err
	})

	server := httptest.NewServer(adapter.Echo())
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	if err := conn.WriteJSON(map[string]any{"kind": "ping"}); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}

	var response map[string]any
	if err := conn.ReadJSON(&response); err != nil {
		t.Fatalf("ReadJSON: %v", err)
	}

	if got := response["kind"]; got != "ping" {
		t.Fatalf("kind = %#v", got)
	}
	if got := response["path"]; got != "/ws" {
		t.Fatalf("path = %#v", got)
	}
	if err := <-handlerErr; err != nil {
		t.Fatalf("websocket handler: %v", err)
	}
}

// TestRouterWebSocketMiddlewareConstructsOnceAndPreservesOrder verifies middleware surrounds the upgrade and handler once.
func TestRouterWebSocketMiddlewareConstructsOnceAndPreservesOrder(t *testing.T) {
	adapter := New()
	router := adapter.Router()
	var eventsMu sync.Mutex
	events := make([]string, 0, 7)
	done := make(chan struct{})
	rootConstructed := 0
	groupConstructed := 0
	routeConstructed := 0
	middleware := func(name string, constructed *int) web.Middleware {
		return func(next web.Handler) web.Handler {
			(*constructed)++
			return func(r web.Context) error {
				eventsMu.Lock()
				events = append(events, name+"-before")
				eventsMu.Unlock()
				err := next(r)
				eventsMu.Lock()
				events = append(events, name+"-after")
				eventsMu.Unlock()
				if name == "root" {
					close(done)
				}
				return err
			}
		}
	}

	router.Use(middleware("root", &rootConstructed))
	group := router.Group("/api", middleware("group", &groupConstructed))
	group.GETWS("/ws", func(r web.Context, conn web.WebSocketConn) error {
		eventsMu.Lock()
		events = append(events, "handler")
		eventsMu.Unlock()
		return conn.WriteJSON(map[string]string{"status": "ok"})
	}, middleware("route", &routeConstructed))
	if rootConstructed != 1 || groupConstructed != 1 || routeConstructed != 1 {
		t.Fatalf("constructor counts = root:%d group:%d route:%d, want 1 each", rootConstructed, groupConstructed, routeConstructed)
	}

	server := httptest.NewServer(adapter)
	defer server.Close()
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
	var response map[string]string
	if err := conn.ReadJSON(&response); err != nil {
		t.Fatalf("ReadJSON: %v", err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("middleware chain did not finish")
	}

	eventsMu.Lock()
	got := strings.Join(events, ",")
	eventsMu.Unlock()
	want := "root-before,group-before,route-before,handler,route-after,group-after,root-after"
	if got != want {
		t.Fatalf("events = %q, want %q", got, want)
	}
	if rootConstructed != 1 || groupConstructed != 1 || routeConstructed != 1 {
		t.Fatalf("constructor counts after request = root:%d group:%d route:%d, want 1 each", rootConstructed, groupConstructed, routeConstructed)
	}
}

// TestRouterWebSocketRouteMiddlewareRejectsBeforeUpgrade verifies middleware can reject a handshake before status 101.
func TestRouterWebSocketRouteMiddlewareRejectsBeforeUpgrade(t *testing.T) {
	adapter := New()
	constructed := 0
	var invoked atomic.Int32
	var handlerCalled atomic.Bool
	adapter.Router().GETWS("/reject", func(r web.Context, conn web.WebSocketConn) error {
		handlerCalled.Store(true)
		return nil
	}, func(next web.Handler) web.Handler {
		constructed++
		return func(r web.Context) error {
			invoked.Add(1)
			return r.Text(http.StatusUnauthorized, "denied")
		}
	})
	if constructed != 1 {
		t.Fatalf("middleware constructed %d times during registration, want 1", constructed)
	}

	server := httptest.NewServer(adapter)
	defer server.Close()
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/reject"
	conn, response, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if conn != nil {
		_ = conn.Close()
	}
	if err == nil {
		t.Fatal("Dial succeeded despite route middleware rejection")
	}
	if response == nil {
		t.Fatal("Dial returned no HTTP response")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusUnauthorized)
	}
	if constructed != 1 || invoked.Load() != 1 {
		t.Fatalf("middleware lifecycle = constructed:%d invoked:%d, want 1 each", constructed, invoked.Load())
	}
	if handlerCalled.Load() {
		t.Fatal("websocket handler ran after middleware rejection")
	}
}

func TestRouterSupportsHeadAndMatch(t *testing.T) {
	adapter := New()
	router := adapter.Router()
	router.HEAD("/head", func(r web.Context) error {
		r.SetHeader("X-Method", r.Method())
		return r.NoContent(http.StatusNoContent)
	})
	router.Match([]string{http.MethodOptions, http.MethodTrace}, "/match", func(r web.Context) error {
		return r.Text(http.StatusOK, r.Method())
	})

	headReq := httptest.NewRequest(http.MethodHead, "/head", nil)
	headRec := httptest.NewRecorder()
	adapter.Echo().ServeHTTP(headRec, headReq)
	if headRec.Code != http.StatusNoContent {
		t.Fatalf("head status = %d", headRec.Code)
	}
	if got := headRec.Header().Get("X-Method"); got != http.MethodHead {
		t.Fatalf("X-Method = %q", got)
	}

	optionsReq := httptest.NewRequest(http.MethodOptions, "/match", nil)
	optionsRec := httptest.NewRecorder()
	adapter.Echo().ServeHTTP(optionsRec, optionsReq)
	if optionsRec.Code != http.StatusOK {
		t.Fatalf("options status = %d body=%s", optionsRec.Code, optionsRec.Body.String())
	}
	if body := optionsRec.Body.String(); body != http.MethodOptions {
		t.Fatalf("options body = %q", body)
	}

	traceReq := httptest.NewRequest(http.MethodTrace, "/match", nil)
	traceRec := httptest.NewRecorder()
	adapter.Echo().ServeHTTP(traceRec, traceReq)
	if traceRec.Code != http.StatusOK {
		t.Fatalf("trace status = %d body=%s", traceRec.Code, traceRec.Body.String())
	}
	if body := traceRec.Body.String(); body != http.MethodTrace {
		t.Fatalf("trace body = %q", body)
	}
}

// TestRouterDispatchesAnyExactMatchAndDuplicateRoutes verifies route table ambiguity and registration errors remain correct.
func TestRouterDispatchesAnyExactMatchAndDuplicateRoutes(t *testing.T) {
	adapter := New()
	router := adapter.Router()
	router.Use(func(next web.Handler) web.Handler {
		return func(r web.Context) error {
			return next(r)
		}
	})
	router.Any("/mixed", func(r web.Context) error {
		return r.Text(http.StatusOK, "any")
	})
	router.GET("/mixed", func(r web.Context) error {
		return r.Text(http.StatusOK, "exact")
	})
	router.Match([]string{http.MethodGet, http.MethodPost}, "/match-shared", func(r web.Context) error {
		return r.Text(http.StatusOK, "match")
	})
	router.GET("/duplicate", func(r web.Context) error {
		return r.Text(http.StatusOK, "first")
	})
	duplicateRejected := false
	func() {
		defer func() {
			duplicateRejected = recover() != nil
		}()
		router.GET("/duplicate", func(r web.Context) error {
			return r.Text(http.StatusOK, "second")
		})
	}()
	if !duplicateRejected {
		t.Fatal("duplicate route registration was not rejected")
	}

	testCases := []struct {
		method string
		path   string
		body   string
	}{
		{method: http.MethodGet, path: "/mixed", body: "exact"},
		{method: http.MethodPost, path: "/mixed", body: "any"},
		{method: http.MethodGet, path: "/match-shared", body: "match"},
		{method: http.MethodPost, path: "/match-shared", body: "match"},
		{method: http.MethodGet, path: "/duplicate", body: "first"},
	}
	for _, testCase := range testCases {
		rec := httptest.NewRecorder()
		adapter.ServeHTTP(rec, httptest.NewRequest(testCase.method, testCase.path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s %s status = %d body=%s", testCase.method, testCase.path, rec.Code, rec.Body.String())
		}
		if got := rec.Body.String(); got != testCase.body {
			t.Fatalf("%s %s body = %q, want %q", testCase.method, testCase.path, got, testCase.body)
		}
	}
}

func TestWrapAndNilAccessors(t *testing.T) {
	adapter := Wrap(nil)
	if adapter.Echo() == nil {
		t.Fatal("Wrap(nil) should create an echo engine")
	}
	if adapter.Router() == nil {
		t.Fatal("Wrap(nil) should create a router")
	}

	engine := echo.New()
	wrapped := Wrap(engine)
	if got := wrapped.Echo(); got != engine {
		t.Fatal("Wrap(existing) should keep the provided engine")
	}

	var nilAdapter *Adapter
	if nilAdapter.Echo() != nil {
		t.Fatal("nil adapter Echo() should return nil")
	}
	if nilAdapter.Router() != nil {
		t.Fatal("nil adapter Router() should return nil")
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/missing", nil)
	nilAdapter.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("nil adapter status = %d", rec.Code)
	}
}

func TestContextAdapterHelpers(t *testing.T) {
	adapter := New()
	adapter.Router().POST("/helpers/:id", func(r web.Context) error {
		if got, want := r.Context().Value("trace"), "trace-1"; got != want {
			t.Fatalf("Context().Value(trace) = %#v, want %q", got, want)
		}
		if got, want := r.Query("expand"), "roles"; got != want {
			t.Fatalf("Query(expand) = %q, want %q", got, want)
		}
		if got, want := r.Header("X-Mode"), "fast"; got != want {
			t.Fatalf("Header(X-Mode) = %q, want %q", got, want)
		}

		type payload struct {
			Name string `json:"name"`
		}
		var body payload
		if err := r.Bind(&body); err != nil {
			t.Fatalf("Bind(): %v", err)
		}
		if got, want := body.Name, "demo"; got != want {
			t.Fatalf("bound.Name = %q, want %q", got, want)
		}

		r.AddHeader("X-Added", "one")
		r.AddHeader("X-Added", "two")
		r.SetHeader("X-Set", "ok")

		if native, ok := UnwrapContext(r); !ok || native == nil {
			t.Fatal("UnwrapContext() failed")
		}
		if _, ok := r.Native().(*echo.Context); !ok {
			t.Fatalf("Native() type = %T", r.Native())
		}
		return r.HTML(http.StatusCreated, "<strong>"+body.Name+"</strong>")
	})

	req := httptest.NewRequest(http.MethodPost, "/helpers/42?expand=roles", bytes.NewBufferString(`{"name":"demo"}`))
	req = req.WithContext(context.WithValue(req.Context(), "trace", "trace-1"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Mode", "fast")
	rec := httptest.NewRecorder()
	adapter.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Values("X-Added"); len(got) != 2 {
		t.Fatalf("X-Added values = %#v", got)
	}
	if got, want := rec.Header().Get("X-Set"), "ok"; got != want {
		t.Fatalf("X-Set = %q, want %q", got, want)
	}
	if got := rec.Header().Get("Content-Type"); !strings.Contains(got, "text/html") {
		t.Fatalf("Content-Type = %q", got)
	}
}

func TestContextAdapterDisableReuse(t *testing.T) {
	engine := echo.New()
	native := engine.NewContext(
		httptest.NewRequest(http.MethodGet, "/", nil),
		httptest.NewRecorder(),
	)
	adapted := acquireContextAdapter(native)
	adapted.DisableReuse()
	if got, ok := UnwrapContext(adapted); !ok || got != native {
		t.Fatal("DisableReuse() changed the zero-allocation context view")
	}
}

func TestContextAdapterRedirectAndResponseWriters(t *testing.T) {
	adapter := New()
	adapter.Router().GET("/redirect", func(r web.Context) error {
		current := r.ResponseWriter()
		r.SetResponseWriter(current)
		if got := r.Response().Header(); got == nil {
			t.Fatal("Response().Header() returned nil")
		}
		if got := r.Response().Native(); got == nil {
			t.Fatal("Response().Native() returned nil")
		}
		r.Response().SetWriter(current)
		return r.Redirect(http.StatusTemporaryRedirect, "/target")
	})

	req := httptest.NewRequest(http.MethodGet, "/redirect", nil)
	rec := httptest.NewRecorder()
	adapter.ServeHTTP(rec, req)

	if rec.Code != http.StatusTemporaryRedirect {
		t.Fatalf("status = %d", rec.Code)
	}
	if got, want := rec.Header().Get("Location"), "/target"; got != want {
		t.Fatalf("Location = %q, want %q", got, want)
	}
}

func TestRouterCoversHandleVariantsAndAny(t *testing.T) {
	adapter := New()
	router := adapter.Router()

	handler := func(r web.Context) error {
		return r.JSON(http.StatusOK, map[string]string{"method": r.Method()})
	}
	register := func(method, path string) {
		t.Helper()
		if err := router.Handle(method, path, handler); err != nil {
			t.Fatalf("Handle(%s): %v", method, err)
		}
	}

	register(http.MethodConnect, "/connect")
	register(http.MethodDelete, "/delete")
	register(http.MethodGet, "/get")
	register(http.MethodHead, "/head")
	register(http.MethodOptions, "/options")
	register(http.MethodPatch, "/patch")
	register(http.MethodPost, "/post")
	register(http.MethodPut, "/put")
	register(http.MethodTrace, "/trace")
	router.Any("/any", handler)

	for _, tc := range []struct {
		method string
		path   string
	}{
		{http.MethodConnect, "/connect"},
		{http.MethodDelete, "/delete"},
		{http.MethodGet, "/get"},
		{http.MethodHead, "/head"},
		{http.MethodOptions, "/options"},
		{http.MethodPatch, "/patch"},
		{http.MethodPost, "/post"},
		{http.MethodPut, "/put"},
		{http.MethodTrace, "/trace"},
		{http.MethodPost, "/any"},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		rec := httptest.NewRecorder()
		adapter.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s %s status = %d body=%s", tc.method, tc.path, rec.Code, rec.Body.String())
		}
		if tc.method != http.MethodHead {
			var payload map[string]string
			if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
				t.Fatalf("%s %s json: %v", tc.method, tc.path, err)
			}
			if got, want := payload["method"], tc.method; got != want {
				t.Fatalf("%s %s method = %q, want %q", tc.method, tc.path, got, want)
			}
		}
	}

	if err := router.Handle("BREW", "/coffee", handler); err == nil {
		t.Fatal("Handle() should reject unsupported methods")
	}
}

func TestWebSocketUnwrapHelpers(t *testing.T) {
	if conn, ok := UnwrapWebSocketConn(nil); ok || conn != nil {
		t.Fatalf("UnwrapWebSocketConn(nil) = (%v, %v)", conn, ok)
	}

	adapter := New()
	handlerValid := make(chan bool, 1)
	adapter.Router().GETWS("/ws-native", func(r web.Context, conn web.WebSocketConn) error {
		_, nativeTypeOK := conn.Native().(*websocket.Conn)
		native, ok := UnwrapWebSocketConn(conn)
		handlerValid <- nativeTypeOK && ok && native != nil
		return conn.Close()
	})

	server := httptest.NewServer(adapter.Echo())
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws-native"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
	if valid := <-handlerValid; !valid {
		t.Fatal("websocket native unwrap helpers returned an invalid connection")
	}
}
