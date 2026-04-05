package middleware

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
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

func TestBodyLimitRejectsLargeContentLength(t *testing.T) {
	adapter := echoweb.New()
	router := adapter.Router()
	router.Use(BodyLimit("4B"))
	router.POST("/limit", func(r web.Context) error {
		return r.Text(http.StatusOK, "ok")
	})

	req := httptest.NewRequest(http.MethodPost, "/limit", strings.NewReader("12345"))
	req.ContentLength = 5
	rec := httptest.NewRecorder()
	adapter.Echo().ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestBodyLimitAllowsSmallBody(t *testing.T) {
	adapter := echoweb.New()
	router := adapter.Router()
	router.Use(BodyLimit("8B"))
	router.POST("/limit", func(r web.Context) error {
		body, err := io.ReadAll(r.Request().Body)
		if err != nil {
			t.Fatalf("ReadAll: %v", err)
		}
		return r.Text(http.StatusOK, string(body))
	})

	req := httptest.NewRequest(http.MethodPost, "/limit", strings.NewReader("1234"))
	req.ContentLength = 4
	rec := httptest.NewRecorder()
	adapter.Echo().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); body != "1234" {
		t.Fatalf("body = %q", body)
	}
}

func TestMethodOverrideFromHeader(t *testing.T) {
	adapter := echoweb.New()
	router := adapter.Router()
	router.Use(MethodOverride())
	router.POST("/method", func(r web.Context) error {
		return r.Text(http.StatusOK, r.Method())
	})

	req := httptest.NewRequest(http.MethodPost, "/method", nil)
	req.Header.Set("X-HTTP-Method-Override", http.MethodDelete)
	rec := httptest.NewRecorder()
	adapter.Echo().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); body != http.MethodDelete {
		t.Fatalf("body = %q", body)
	}
}

func TestMethodOverrideDoesNotChangeNonPost(t *testing.T) {
	adapter := echoweb.New()
	router := adapter.Router()
	router.Use(MethodOverride())
	router.GET("/method", func(r web.Context) error {
		return r.Text(http.StatusOK, r.Method())
	})

	req := httptest.NewRequest(http.MethodGet, "/method", nil)
	req.Header.Set("X-HTTP-Method-Override", http.MethodDelete)
	rec := httptest.NewRecorder()
	adapter.Echo().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); body != http.MethodGet {
		t.Fatalf("body = %q", body)
	}
}

func TestHTTPSRedirectRedirectsToHTTPS(t *testing.T) {
	adapter := echoweb.New()
	router := adapter.Router()
	router.Pre(HTTPSRedirect())
	router.GET("/redirect", func(r web.Context) error {
		return r.Text(http.StatusOK, "ok")
	})

	req := httptest.NewRequest(http.MethodGet, "http://example.com/redirect?x=1", nil)
	rec := httptest.NewRecorder()
	adapter.Echo().ServeHTTP(rec, req)

	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "https://example.com/redirect?x=1" {
		t.Fatalf("Location = %q", got)
	}
}

func TestAddTrailingSlashRedirectsWhenConfigured(t *testing.T) {
	adapter := echoweb.New()
	router := adapter.Router()
	router.Pre(AddTrailingSlashWithConfig(TrailingSlashConfig{RedirectCode: http.StatusTemporaryRedirect}))
	router.GET("/docs/", func(r web.Context) error {
		return r.Text(http.StatusOK, "ok")
	})

	req := httptest.NewRequest(http.MethodGet, "/docs?x=1", nil)
	rec := httptest.NewRecorder()
	adapter.Echo().ServeHTTP(rec, req)

	if rec.Code != http.StatusTemporaryRedirect {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "/docs/?x=1" {
		t.Fatalf("Location = %q", got)
	}
}

func TestRemoveTrailingSlashMutatesRequestWhenNotRedirecting(t *testing.T) {
	adapter := echoweb.New()
	router := adapter.Router()
	router.Pre(RemoveTrailingSlash())
	router.Any("/resource", func(r web.Context) error {
		return r.Text(http.StatusOK, r.URI())
	})

	req := httptest.NewRequest(http.MethodGet, "/resource/?x=1", nil)
	rec := httptest.NewRecorder()
	adapter.Echo().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); body != "/resource?x=1" {
		t.Fatalf("body = %q", body)
	}
}

func TestRewriteChangesMatchingRouteBeforeRouting(t *testing.T) {
	adapter := echoweb.New()
	router := adapter.Router()
	router.Pre(Rewrite(map[string]string{
		"/old/*": "/new/$1",
	}))
	router.GET("/new/:id", func(r web.Context) error {
		return r.Text(http.StatusOK, r.Param("id"))
	})

	req := httptest.NewRequest(http.MethodGet, "/old/42", nil)
	rec := httptest.NewRecorder()
	adapter.Echo().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); body != "42" {
		t.Fatalf("body = %q", body)
	}
}

func TestRewriteWithRegexRulesPreservesQuery(t *testing.T) {
	adapter := echoweb.New()
	router := adapter.Router()
	router.Pre(RewriteWithConfig(RewriteConfig{
		RegexRules: map[*regexp.Regexp]string{
			regexp.MustCompile(`^/v1/items/(.*)$`): "/items/$1?source=rewritten",
		},
	}))
	router.GET("/items/:id", func(r web.Context) error {
		return r.Text(http.StatusOK, r.URI())
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/items/42", nil)
	rec := httptest.NewRecorder()
	adapter.Echo().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); body != "/items/42?source=rewritten" {
		t.Fatalf("body = %q", body)
	}
}

func TestSecureSetsExpectedHeaders(t *testing.T) {
	adapter := echoweb.New()
	router := adapter.Router()
	router.Use(SecureWithConfig(SecureConfig{
		XSSProtection:         "1; mode=block",
		ContentTypeNosniff:    "nosniff",
		XFrameOptions:         "SAMEORIGIN",
		HSTSMaxAge:            3600,
		ContentSecurityPolicy: "default-src 'self'",
		ReferrerPolicy:        "same-origin",
	}))
	router.GET("/secure", func(r web.Context) error {
		return r.NoContent(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/secure", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	rec := httptest.NewRecorder()
	adapter.Echo().ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := rec.Header().Get("X-XSS-Protection"); got != "1; mode=block" {
		t.Fatalf("X-XSS-Protection = %q", got)
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q", got)
	}
	if got := rec.Header().Get("X-Frame-Options"); got != "SAMEORIGIN" {
		t.Fatalf("X-Frame-Options = %q", got)
	}
	if got := rec.Header().Get("Strict-Transport-Security"); got != "max-age=3600; includeSubDomains" {
		t.Fatalf("Strict-Transport-Security = %q", got)
	}
	if got := rec.Header().Get("Content-Security-Policy"); got != "default-src 'self'" {
		t.Fatalf("Content-Security-Policy = %q", got)
	}
	if got := rec.Header().Get("Referrer-Policy"); got != "same-origin" {
		t.Fatalf("Referrer-Policy = %q", got)
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
func (c *stubContext) Scheme() string                            { return "http" }
func (c *stubContext) Host() string                              { return "example.com" }
func (c *stubContext) Param(name string) string                  { return "" }
func (c *stubContext) Query(name string) string                  { return "" }
func (c *stubContext) Header(name string) string                 { return c.headers.Get(name) }
func (c *stubContext) Cookie(name string) (*http.Cookie, error)  { return nil, http.ErrNoCookie }
func (c *stubContext) RealIP() string                            { return "127.0.0.1" }
func (c *stubContext) Request() *http.Request                    { return httptest.NewRequest(http.MethodGet, "/", nil) }
func (c *stubContext) SetRequest(request *http.Request)          {}
func (c *stubContext) ResponseWriter() http.ResponseWriter       { return httptest.NewRecorder() }
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
