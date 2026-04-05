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

func TestBasicAuthAllowsValidCredentials(t *testing.T) {
	adapter := echoweb.New()
	router := adapter.Router()
	router.Use(BasicAuth(func(username string, password string, r web.Context) (bool, error) {
		r.Set("username", username)
		return username == "admin" && password == "secret", nil
	}))
	router.GET("/basic", func(r web.Context) error {
		if got := r.Get("username"); got != "admin" {
			t.Fatalf("username = %#v", got)
		}
		return r.Text(http.StatusOK, "ok")
	})

	req := httptest.NewRequest(http.MethodGet, "/basic", nil)
	req.SetBasicAuth("admin", "secret")
	rec := httptest.NewRecorder()
	adapter.Echo().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); body != "ok" {
		t.Fatalf("body = %q", body)
	}
}

func TestBasicAuthRejectsMissingCredentials(t *testing.T) {
	adapter := echoweb.New()
	router := adapter.Router()
	router.Use(BasicAuth(func(username string, password string, r web.Context) (bool, error) {
		return true, nil
	}))
	router.GET("/basic", func(r web.Context) error {
		return r.Text(http.StatusOK, "ok")
	})

	req := httptest.NewRequest(http.MethodGet, "/basic", nil)
	rec := httptest.NewRecorder()
	adapter.Echo().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("WWW-Authenticate"); got != "basic realm=Restricted" {
		t.Fatalf("WWW-Authenticate = %q", got)
	}
}

func TestBasicAuthRejectsInvalidBase64(t *testing.T) {
	adapter := echoweb.New()
	router := adapter.Router()
	router.Use(BasicAuth(func(username string, password string, r web.Context) (bool, error) {
		return true, nil
	}))
	router.GET("/basic", func(r web.Context) error {
		return r.Text(http.StatusOK, "ok")
	})

	req := httptest.NewRequest(http.MethodGet, "/basic", nil)
	req.Header.Set("Authorization", "Basic !!!")
	rec := httptest.NewRecorder()
	adapter.Echo().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("WWW-Authenticate"); got != "basic realm=Restricted" {
		t.Fatalf("WWW-Authenticate = %q", got)
	}
}

func TestKeyAuthAcceptsBearerFromHeader(t *testing.T) {
	adapter := echoweb.New()
	router := adapter.Router()
	router.Use(KeyAuth(func(auth string, r web.Context) (bool, error) {
		r.Set("auth", auth)
		return auth == "token-123", nil
	}))
	router.GET("/key", func(r web.Context) error {
		if got := r.Get("auth"); got != "token-123" {
			t.Fatalf("auth = %#v", got)
		}
		return r.Text(http.StatusOK, "ok")
	})

	req := httptest.NewRequest(http.MethodGet, "/key", nil)
	req.Header.Set("Authorization", "Bearer token-123")
	rec := httptest.NewRecorder()
	adapter.Echo().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestKeyAuthAcceptsQueryLookup(t *testing.T) {
	adapter := echoweb.New()
	router := adapter.Router()
	router.Use(KeyAuthWithConfig(KeyAuthConfig{
		KeyLookup: "query:token",
		Validator: func(auth string, r web.Context) (bool, error) {
			return auth == "q-token", nil
		},
	}))
	router.GET("/key", func(r web.Context) error {
		return r.Text(http.StatusOK, "ok")
	})

	req := httptest.NewRequest(http.MethodGet, "/key?token=q-token", nil)
	rec := httptest.NewRecorder()
	adapter.Echo().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestKeyAuthRejectsMissingKey(t *testing.T) {
	adapter := echoweb.New()
	router := adapter.Router()
	router.Use(KeyAuth(func(auth string, r web.Context) (bool, error) {
		return true, nil
	}))
	router.GET("/key", func(r web.Context) error {
		return r.Text(http.StatusOK, "ok")
	})

	req := httptest.NewRequest(http.MethodGet, "/key", nil)
	rec := httptest.NewRecorder()
	adapter.Echo().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); !strings.Contains(body, "missing key") {
		t.Fatalf("body = %q", body)
	}
}

func TestKeyAuthRejectsInvalidKey(t *testing.T) {
	adapter := echoweb.New()
	router := adapter.Router()
	router.Use(KeyAuth(func(auth string, r web.Context) (bool, error) {
		return false, nil
	}))
	router.GET("/key", func(r web.Context) error {
		return r.Text(http.StatusOK, "ok")
	})

	req := httptest.NewRequest(http.MethodGet, "/key", nil)
	req.Header.Set("Authorization", "Bearer nope")
	rec := httptest.NewRecorder()
	adapter.Echo().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestRequestLoggerCapturesStatusURIAndMethod(t *testing.T) {
	adapter := echoweb.New()
	router := adapter.Router()

	var captured RequestLoggerValues
	router.Use(RequestLoggerWithConfig(RequestLoggerConfig{
		LogValuesFunc: func(r web.Context, values RequestLoggerValues) error {
			captured = values
			return nil
		},
	}))
	router.GET("/logger", func(r web.Context) error {
		return r.Text(http.StatusCreated, "created")
	})

	req := httptest.NewRequest(http.MethodGet, "/logger?x=1", nil)
	rec := httptest.NewRecorder()
	adapter.Echo().ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d", rec.Code)
	}
	if captured.Status != http.StatusCreated {
		t.Fatalf("captured status = %d", captured.Status)
	}
	if captured.Method != http.MethodGet {
		t.Fatalf("captured method = %q", captured.Method)
	}
	if captured.URI != "/logger?x=1" {
		t.Fatalf("captured uri = %q", captured.URI)
	}
	if captured.Latency < 0 {
		t.Fatalf("captured latency = %v", captured.Latency)
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
func (c *stubContext) URI() string                               { return "/" }
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
func (c *stubContext) StatusCode() int                      { return http.StatusOK }
func (c *stubContext) Native() any                          { return nil }
