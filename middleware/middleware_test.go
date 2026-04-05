package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/goforj/web"
	"github.com/goforj/web/adapter/echoweb"
)

func TestRequestIDUsesIncomingOrGeneratedValue(t *testing.T) {
	adapter := echoweb.New()
	router := adapter.Router()
	router.Use(RequestID())
	router.GET("/request-id", func(r web.Context) error {
		if got := r.Get("request_id"); got == nil || got == "" {
			t.Fatalf("request_id context value missing: %#v", got)
		}
		return r.NoContent(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/request-id", nil)
	req.Header.Set("X-Request-ID", "abc-123")
	rec := httptest.NewRecorder()
	adapter.Echo().ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := rec.Header().Get("X-Request-ID"); got != "abc-123" {
		t.Fatalf("X-Request-ID = %q", got)
	}
}

func TestCORSHandlesPreflightAndSimpleRequests(t *testing.T) {
	adapter := echoweb.New()
	router := adapter.Router()
	router.Use(CORSWithConfig(CORSConfig{
		AllowOrigins:     []string{"https://example.com"},
		AllowMethods:     []string{http.MethodGet, http.MethodPost, http.MethodOptions},
		AllowCredentials: true,
	}))
	router.GET("/cors", func(r web.Context) error {
		return r.Text(http.StatusOK, "ok")
	})

	preflight := httptest.NewRequest(http.MethodOptions, "/cors", nil)
	preflight.Header.Set("Origin", "https://example.com")
	preflight.Header.Set("Access-Control-Request-Headers", "Authorization")
	preflightRec := httptest.NewRecorder()
	adapter.Echo().ServeHTTP(preflightRec, preflight)
	if preflightRec.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d", preflightRec.Code)
	}
	if got := preflightRec.Header().Get("Access-Control-Allow-Origin"); got != "https://example.com" {
		t.Fatalf("Access-Control-Allow-Origin = %q", got)
	}
	if got := preflightRec.Header().Get("Access-Control-Allow-Headers"); got != "Authorization" {
		t.Fatalf("Access-Control-Allow-Headers = %q", got)
	}
	if got := preflightRec.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Fatalf("Access-Control-Allow-Credentials = %q", got)
	}

	simple := httptest.NewRequest(http.MethodGet, "/cors", nil)
	simple.Header.Set("Origin", "https://example.com")
	simpleRec := httptest.NewRecorder()
	adapter.Echo().ServeHTTP(simpleRec, simple)
	if simpleRec.Code != http.StatusOK {
		t.Fatalf("simple status = %d", simpleRec.Code)
	}
	if got := simpleRec.Header().Get("Access-Control-Allow-Origin"); got != "https://example.com" {
		t.Fatalf("simple Access-Control-Allow-Origin = %q", got)
	}
}

func TestRecoverReturnsRecoveredError(t *testing.T) {
	ctx := newStubContext()
	handler := Recover()(func(r web.Context) error {
		panic("boom")
	})

	err := handler(ctx)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Fatalf("error = %v", err)
	}
}

func TestRecoverWithConfigCanSwallowError(t *testing.T) {
	ctx := newStubContext()
	called := false
	handler := RecoverWithConfig(RecoverConfig{
		HandleError: func(r web.Context, err error, stack []byte) error {
			called = true
			if !strings.Contains(err.Error(), "boom") {
				t.Fatalf("err = %v", err)
			}
			if len(stack) == 0 {
				t.Fatal("expected stack")
			}
			return nil
		},
	})(func(r web.Context) error {
		panic(errors.New("boom"))
	})

	if err := handler(ctx); err != nil {
		t.Fatalf("handler error = %v", err)
	}
	if !called {
		t.Fatal("expected recover handler to be called")
	}
}

type stubContext struct {
	headers http.Header
	values  map[string]any
}

func newStubContext() *stubContext {
	return &stubContext{
		headers: http.Header{},
		values:  map[string]any{},
	}
}

func (c *stubContext) Context() context.Context                  { return context.Background() }
func (c *stubContext) Method() string                            { return http.MethodGet }
func (c *stubContext) Path() string                              { return "/" }
func (c *stubContext) Host() string                              { return "example.com" }
func (c *stubContext) Param(name string) string                  { return "" }
func (c *stubContext) Query(name string) string                  { return "" }
func (c *stubContext) Header(name string) string                 { return c.headers.Get(name) }
func (c *stubContext) Cookie(name string) (*http.Cookie, error)  { return nil, http.ErrNoCookie }
func (c *stubContext) RealIP() string                            { return "127.0.0.1" }
func (c *stubContext) Bind(target any) error                     { return nil }
func (c *stubContext) Set(key string, value any)                 { c.values[key] = value }
func (c *stubContext) Get(key string) any                        { return c.values[key] }
func (c *stubContext) AddHeader(name string, value string)       { c.headers.Add(name, value) }
func (c *stubContext) SetHeader(name string, value string)       { c.headers.Set(name, value) }
func (c *stubContext) SetCookie(cookie *http.Cookie)             {}
func (c *stubContext) JSON(code int, payload any) error          { return nil }
func (c *stubContext) Blob(code int, contentType string, body []byte) error {
	return nil
}
func (c *stubContext) File(path string) error               { return nil }
func (c *stubContext) Text(code int, body string) error     { return nil }
func (c *stubContext) HTML(code int, body string) error     { return nil }
func (c *stubContext) NoContent(code int) error             { return nil }
func (c *stubContext) Redirect(code int, url string) error  { return nil }
func (c *stubContext) Native() any                          { return nil }
