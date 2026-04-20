package webmiddleware

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/goforj/web"
	"github.com/goforj/web/adapter/echoweb"
	"github.com/goforj/web/webtest"
	"golang.org/x/time/rate"
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

func TestCORSAdditionalBranches(t *testing.T) {
	t.Run("preflight without origin short circuits", func(t *testing.T) {
		adapter := echoweb.New()
		router := adapter.Router()
		router.Use(CORS())
		router.OPTIONS("/cors", func(r web.Context) error {
			return r.Text(http.StatusOK, "should-not-run")
		})

		req := httptest.NewRequest(http.MethodOptions, "/cors", nil)
		rec := httptest.NewRecorder()
		adapter.Echo().ServeHTTP(rec, req)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("simple request exposes headers", func(t *testing.T) {
		adapter := echoweb.New()
		router := adapter.Router()
		router.Use(CORSWithConfig(CORSConfig{
			AllowOrigins:  []string{"https://example.com"},
			ExposeHeaders: []string{"X-Trace"},
		}))
		router.GET("/cors", func(r web.Context) error { return r.NoContent(http.StatusNoContent) })

		req := httptest.NewRequest(http.MethodGet, "/cors", nil)
		req.Header.Set("Origin", "https://example.com")
		rec := httptest.NewRecorder()
		adapter.Echo().ServeHTTP(rec, req)
		if got := rec.Header().Get("Access-Control-Expose-Headers"); got != "X-Trace" {
			t.Fatalf("Access-Control-Expose-Headers = %q", got)
		}
	})

	t.Run("preflight uses configured headers and max age", func(t *testing.T) {
		adapter := echoweb.New()
		router := adapter.Router()
		router.Use(CORSWithConfig(CORSConfig{
			AllowOrigins: []string{"*"},
			AllowMethods: []string{http.MethodGet, http.MethodOptions},
			AllowHeaders: []string{"Authorization", "X-Trace"},
			MaxAge:       600,
		}))
		router.OPTIONS("/cors", func(r web.Context) error { return nil })

		req := httptest.NewRequest(http.MethodOptions, "/cors", nil)
		req.Header.Set("Origin", "https://other.example.com")
		rec := httptest.NewRecorder()
		adapter.Echo().ServeHTTP(rec, req)
		if got := rec.Header().Get("Access-Control-Allow-Headers"); got != "Authorization,X-Trace" {
			t.Fatalf("Access-Control-Allow-Headers = %q", got)
		}
		if got := rec.Header().Get("Access-Control-Max-Age"); got != "600" {
			t.Fatalf("Access-Control-Max-Age = %q", got)
		}
	})

	t.Run("denied origin falls through", func(t *testing.T) {
		adapter := echoweb.New()
		router := adapter.Router()
		router.Use(CORSWithConfig(CORSConfig{AllowOrigins: []string{"https://good.example.com"}}))
		router.GET("/cors", func(r web.Context) error { return r.Text(http.StatusOK, "ok") })

		req := httptest.NewRequest(http.MethodGet, "/cors", nil)
		req.Header.Set("Origin", "https://bad.example.com")
		rec := httptest.NewRecorder()
		adapter.Echo().ServeHTTP(rec, req)
		if rec.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Fatalf("unexpected allow origin header = %q", rec.Header().Get("Access-Control-Allow-Origin"))
		}
	})
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

func TestKeyAuthAdditionalBranches(t *testing.T) {
	t.Run("error handler can continue on ignored error", func(t *testing.T) {
		adapter := echoweb.New()
		router := adapter.Router()
		router.Use(KeyAuthWithConfig(KeyAuthConfig{
			Validator: func(auth string, r web.Context) (bool, error) { return false, nil },
			ErrorHandler: func(err error, r web.Context) error {
				return nil
			},
			ContinueOnIgnoredError: true,
		}))
		router.GET("/key", func(r web.Context) error { return r.NoContent(http.StatusAccepted) })

		req := httptest.NewRequest(http.MethodGet, "/key", nil)
		rec := httptest.NewRecorder()
		adapter.Echo().ServeHTTP(rec, req)
		if rec.Code != http.StatusAccepted {
			t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("validator errors become last validator error", func(t *testing.T) {
		wantErr := errors.New("validator failed")
		mw := KeyAuthWithConfig(KeyAuthConfig{
			KeyLookup: "query:token",
			Validator: func(auth string, r web.Context) (bool, error) {
				return false, wantErr
			},
			ErrorHandler: func(err error, r web.Context) error {
				return err
			},
		})
		req := httptest.NewRequest(http.MethodGet, "/key?token=q-token", nil)
		ctx := webtest.NewContext(req, nil, "/key", nil)
		err := mw(func(r web.Context) error { return nil })(ctx)
		if !errors.Is(err, wantErr) {
			t.Fatalf("KeyAuthWithConfig() error = %v", err)
		}
	})
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

func TestRateLimiterAllowsThenDenies(t *testing.T) {
	store := NewRateLimiterMemoryStoreWithConfig(RateLimiterMemoryStoreConfig{
		Rate:      rate.Limit(1),
		Burst:     1,
		ExpiresIn: time.Minute,
	})
	adapter := echoweb.New()
	router := adapter.Router()
	router.Use(RateLimiter(store))
	router.GET("/limited", func(r web.Context) error {
		return r.Text(http.StatusOK, "ok")
	})

	firstReq := httptest.NewRequest(http.MethodGet, "/limited", nil)
	firstReq.Header.Set("X-Forwarded-For", "203.0.113.10")
	firstRec := httptest.NewRecorder()
	adapter.Echo().ServeHTTP(firstRec, firstReq)

	if firstRec.Code != http.StatusOK {
		t.Fatalf("first status = %d body=%s", firstRec.Code, firstRec.Body.String())
	}

	secondReq := httptest.NewRequest(http.MethodGet, "/limited", nil)
	secondReq.Header.Set("X-Forwarded-For", "203.0.113.10")
	secondRec := httptest.NewRecorder()
	adapter.Echo().ServeHTTP(secondRec, secondReq)

	if secondRec.Code != http.StatusTooManyRequests {
		t.Fatalf("second status = %d body=%s", secondRec.Code, secondRec.Body.String())
	}
	if body := strings.TrimSpace(secondRec.Body.String()); body != `{"error":"rate limit exceeded"}` {
		t.Fatalf("second body = %q", body)
	}
}

func TestRateLimiterCustomExtractorErrorHandler(t *testing.T) {
	adapter := echoweb.New()
	router := adapter.Router()
	router.Use(RateLimiterWithConfig(RateLimiterConfig{
		Store: NewRateLimiterMemoryStore(rate.Limit(1)),
		IdentifierExtractor: func(r web.Context) (string, error) {
			return "", errors.New("boom")
		},
	}))
	router.GET("/limited", func(r web.Context) error {
		return r.Text(http.StatusOK, "ok")
	})

	req := httptest.NewRequest(http.MethodGet, "/limited", nil)
	rec := httptest.NewRecorder()
	adapter.Echo().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if body := strings.TrimSpace(rec.Body.String()); body != `{"error":"error while extracting identifier"}` {
		t.Fatalf("body = %q", body)
	}
}

func TestContextTimeoutReturnsServiceUnavailableOnDeadlineExceeded(t *testing.T) {
	adapter := echoweb.New()
	router := adapter.Router()
	router.Use(ContextTimeout(5 * time.Millisecond))
	router.GET("/timeout", func(r web.Context) error {
		<-r.Request().Context().Done()
		return r.Request().Context().Err()
	})

	req := httptest.NewRequest(http.MethodGet, "/timeout", nil)
	rec := httptest.NewRecorder()
	adapter.Echo().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if body := strings.TrimSpace(rec.Body.String()); body != `{"error":"service unavailable"}` {
		t.Fatalf("body = %q", body)
	}
}

func TestTimeoutReturnsServiceUnavailableWhenHandlerRunsTooLong(t *testing.T) {
	adapter := echoweb.New()
	router := adapter.Router()
	router.Use(TimeoutWithConfig(TimeoutConfig{
		Timeout:      5 * time.Millisecond,
		ErrorMessage: "timeout",
	}))
	router.GET("/timeout", func(r web.Context) error {
		time.Sleep(25 * time.Millisecond)
		return r.Text(http.StatusOK, "late")
	})

	req := httptest.NewRequest(http.MethodGet, "/timeout", nil)
	rec := httptest.NewRecorder()
	adapter.Echo().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if body := strings.TrimSpace(rec.Body.String()); body != "timeout" {
		t.Fatalf("body = %q", body)
	}
}

func TestDecompressInflatesGzipRequestBody(t *testing.T) {
	var compressed bytes.Buffer
	zw := gzip.NewWriter(&compressed)
	if _, err := zw.Write([]byte(`{"name":"gopher"}`)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	adapter := echoweb.New()
	router := adapter.Router()
	router.Use(Decompress())
	router.POST("/inflate", func(r web.Context) error {
		body, err := io.ReadAll(r.Request().Body)
		if err != nil {
			t.Fatalf("ReadAll: %v", err)
		}
		return r.Text(http.StatusOK, string(body))
	})

	req := httptest.NewRequest(http.MethodPost, "/inflate", bytes.NewReader(compressed.Bytes()))
	req.Header.Set("Content-Encoding", "gzip")
	rec := httptest.NewRecorder()
	adapter.Echo().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); body != `{"name":"gopher"}` {
		t.Fatalf("body = %q", body)
	}
}

func TestGzipCompressesResponseWhenAccepted(t *testing.T) {
	adapter := echoweb.New()
	router := adapter.Router()
	router.Use(Gzip())
	router.GET("/gzip", func(r web.Context) error {
		return r.Text(http.StatusOK, strings.Repeat("gopher", 8))
	})

	req := httptest.NewRequest(http.MethodGet, "/gzip", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	adapter.Echo().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q", got)
	}

	zr, err := gzip.NewReader(bytes.NewReader(rec.Body.Bytes()))
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	defer zr.Close()

	body, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if got := string(body); got != strings.Repeat("gopher", 8) {
		t.Fatalf("body = %q", got)
	}
}

func TestGzipLeavesShortResponsePlainWhenBelowMinLength(t *testing.T) {
	adapter := echoweb.New()
	router := adapter.Router()
	router.Use(GzipWithConfig(GzipConfig{MinLength: 128}))
	router.GET("/gzip", func(r web.Context) error {
		return r.Text(http.StatusOK, "tiny")
	})

	req := httptest.NewRequest(http.MethodGet, "/gzip", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	adapter.Echo().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("Content-Encoding = %q", got)
	}
	if body := rec.Body.String(); body != "tiny" {
		t.Fatalf("body = %q", body)
	}
}

func TestBodyDumpCapturesRequestAndResponseBodies(t *testing.T) {
	var capturedRequest []byte
	var capturedResponse []byte

	adapter := echoweb.New()
	router := adapter.Router()
	router.Use(BodyDump(func(r web.Context, reqBody []byte, resBody []byte) {
		capturedRequest = append([]byte(nil), reqBody...)
		capturedResponse = append([]byte(nil), resBody...)
	}))
	router.POST("/dump", func(r web.Context) error {
		body, err := io.ReadAll(r.Request().Body)
		if err != nil {
			t.Fatalf("ReadAll: %v", err)
		}
		return r.Text(http.StatusOK, strings.ToUpper(string(body)))
	})

	req := httptest.NewRequest(http.MethodPost, "/dump", strings.NewReader("hello"))
	rec := httptest.NewRecorder()
	adapter.Echo().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if got := string(capturedRequest); got != "hello" {
		t.Fatalf("captured request = %q", got)
	}
	if got := string(capturedResponse); got != "HELLO" {
		t.Fatalf("captured response = %q", got)
	}
}

func TestCSRFSetsTokenCookieAndContextOnSafeRequest(t *testing.T) {
	adapter := echoweb.New()
	router := adapter.Router()
	router.Use(CSRF())
	router.GET("/csrf", func(r web.Context) error {
		token, _ := r.Get("csrf").(string)
		return r.Text(http.StatusOK, token)
	})

	req := httptest.NewRequest(http.MethodGet, "/csrf", nil)
	rec := httptest.NewRecorder()
	adapter.Echo().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if token := strings.TrimSpace(rec.Body.String()); token == "" {
		t.Fatal("csrf token missing from context output")
	}
	if got := rec.Header().Get("Set-Cookie"); !strings.Contains(got, "_csrf=") {
		t.Fatalf("Set-Cookie = %q", got)
	}
}

func TestCSRFAcceptsMatchingHeaderToken(t *testing.T) {
	adapter := echoweb.New()
	router := adapter.Router()
	router.Use(CSRF())
	router.POST("/csrf", func(r web.Context) error {
		return r.NoContent(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodPost, "/csrf", nil)
	req.AddCookie(&http.Cookie{Name: "_csrf", Value: "known-token"})
	req.Header.Set("X-CSRF-Token", "known-token")
	rec := httptest.NewRecorder()
	adapter.Echo().ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestCSRFFailsOnMissingOrInvalidToken(t *testing.T) {
	adapter := echoweb.New()
	router := adapter.Router()
	router.Use(CSRF())
	router.POST("/csrf", func(r web.Context) error {
		return r.NoContent(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodPost, "/csrf", nil)
	req.AddCookie(&http.Cookie{Name: "_csrf", Value: "known-token"})
	req.Header.Set("X-CSRF-Token", "wrong-token")
	rec := httptest.NewRecorder()
	adapter.Echo().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if body := strings.TrimSpace(rec.Body.String()); body != `{"error":"invalid csrf token"}` {
		t.Fatalf("body = %q", body)
	}
}

func TestStaticServesFilesAndHTML5Fallback(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(pathJoin(root, "index.html"), []byte("index"), 0o644); err != nil {
		t.Fatalf("WriteFile index: %v", err)
	}
	if err := os.WriteFile(pathJoin(root, "app.js"), []byte("console.log('ok')"), 0o644); err != nil {
		t.Fatalf("WriteFile app.js: %v", err)
	}

	adapter := echoweb.New()
	router := adapter.Router()
	router.Use(StaticWithConfig(StaticConfig{
		Root:  root,
		HTML5: true,
	}))
	router.GET("/api/ping", func(r web.Context) error {
		return r.Text(http.StatusOK, "pong")
	})

	fileReq := httptest.NewRequest(http.MethodGet, "/app.js", nil)
	fileRec := httptest.NewRecorder()
	adapter.Echo().ServeHTTP(fileRec, fileReq)
	if fileRec.Code != http.StatusOK || fileRec.Body.String() != "console.log('ok')" {
		t.Fatalf("file response = %d %q", fileRec.Code, fileRec.Body.String())
	}

	html5Req := httptest.NewRequest(http.MethodGet, "/dashboard/monitors", nil)
	html5Rec := httptest.NewRecorder()
	adapter.Echo().ServeHTTP(html5Rec, html5Req)
	if html5Rec.Code != http.StatusOK || html5Rec.Body.String() != "index" {
		t.Fatalf("html5 response = %d %q", html5Rec.Code, html5Rec.Body.String())
	}
}

func TestStaticBrowseListsDirectoryContents(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(pathJoin(root, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.Mkdir(pathJoin(root, "nested"), 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}

	adapter := echoweb.New()
	router := adapter.Router()
	router.Use(StaticWithConfig(StaticConfig{
		Root:   root,
		Browse: true,
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	adapter.Echo().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "a.txt") || !strings.Contains(rec.Body.String(), "nested/") {
		t.Fatalf("body = %q", rec.Body.String())
	}
}

func TestConvenienceMiddlewareWrappersAndHelpers(t *testing.T) {
	t.Run("cors wrapper", func(t *testing.T) {
		adapter := echoweb.New()
		adapter.Router().Use(CORS())
		adapter.Router().GET("/cors", func(r web.Context) error { return r.NoContent(http.StatusNoContent) })

		req := httptest.NewRequest(http.MethodOptions, "/cors", nil)
		req.Header.Set("Origin", "https://example.com")
		req.Header.Set("Access-Control-Request-Method", http.MethodGet)
		rec := httptest.NewRecorder()
		adapter.ServeHTTP(rec, req)

		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
			t.Fatalf("Access-Control-Allow-Origin = %q", got)
		}
	})

	t.Run("redirect wrappers", func(t *testing.T) {
		testCases := []struct {
			name     string
			mw       web.Middleware
			url      string
			wantCode int
			wantLoc  string
		}{
			{"https_www", HTTPSWWWRedirect(), "http://example.com/docs", http.StatusMovedPermanently, "https://www.example.com/docs"},
			{"https_www_config", HTTPSWWWRedirectWithConfig(RedirectConfig{Code: http.StatusTemporaryRedirect}), "http://example.com/docs", http.StatusTemporaryRedirect, "https://www.example.com/docs"},
			{"https_non_www", HTTPSNonWWWRedirect(), "http://www.example.com/docs", http.StatusMovedPermanently, "https://example.com/docs"},
			{"https_non_www_config", HTTPSNonWWWRedirectWithConfig(RedirectConfig{Code: http.StatusTemporaryRedirect}), "http://www.example.com/docs", http.StatusTemporaryRedirect, "https://example.com/docs"},
			{"www", WWWRedirect(), "http://example.com/docs", http.StatusMovedPermanently, "http://www.example.com/docs"},
			{"www_config", WWWRedirectWithConfig(RedirectConfig{Code: http.StatusTemporaryRedirect}), "http://example.com/docs", http.StatusTemporaryRedirect, "http://www.example.com/docs"},
			{"non_www", NonWWWRedirect(), "http://www.example.com/docs", http.StatusMovedPermanently, "http://example.com/docs"},
			{"non_www_config", NonWWWRedirectWithConfig(RedirectConfig{Code: http.StatusTemporaryRedirect}), "http://www.example.com/docs", http.StatusTemporaryRedirect, "http://example.com/docs"},
		}
		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				ctx := webtest.NewContext(httptest.NewRequest(http.MethodGet, tc.url, nil), nil, "/docs", nil)
				if err := tc.mw(func(c web.Context) error { return c.NoContent(http.StatusNoContent) })(ctx); err != nil {
					t.Fatalf("middleware error = %v", err)
				}
				if got := ctx.StatusCode(); got != tc.wantCode {
					t.Fatalf("status = %d, want %d", got, tc.wantCode)
				}
				if got := ctx.Response().Header().Get("Location"); got != tc.wantLoc {
					t.Fatalf("Location = %q, want %q", got, tc.wantLoc)
				}
			})
		}
	})

	t.Run("method override getters", func(t *testing.T) {
		ctx := webtest.NewContext(httptest.NewRequest(http.MethodPost, "/?_method=PATCH", strings.NewReader("_method=DELETE")), nil, "/", nil)
		ctx.Request().Header.Set("Content-Type", "application/x-www-form-urlencoded")
		ctx.Request().Header.Set("X-HTTP-Method-Override", http.MethodPut)

		if got, want := MethodFromForm("_method")(ctx), http.MethodDelete; got != want {
			t.Fatalf("MethodFromForm() = %q, want %q", got, want)
		}
		if got, want := MethodFromQuery("_method")(ctx), http.MethodPatch; got != want {
			t.Fatalf("MethodFromQuery() = %q, want %q", got, want)
		}
	})

	t.Run("secure wrapper", func(t *testing.T) {
		ctx := webtest.NewContext(httptest.NewRequest(http.MethodGet, "https://example.com", nil), nil, "/", nil)
		if err := Secure()(func(c web.Context) error { return c.NoContent(http.StatusNoContent) })(ctx); err != nil {
			t.Fatalf("Secure() error = %v", err)
		}
		if got, want := ctx.Response().Header().Get("X-Frame-Options"), "SAMEORIGIN"; got != want {
			t.Fatalf("X-Frame-Options = %q, want %q", got, want)
		}
	})

	t.Run("add trailing slash wrapper", func(t *testing.T) {
		ctx := webtest.NewContext(httptest.NewRequest(http.MethodGet, "http://example.com/docs", nil), nil, "/docs", nil)
		if err := AddTrailingSlash()(func(c web.Context) error { return c.NoContent(http.StatusNoContent) })(ctx); err != nil {
			t.Fatalf("AddTrailingSlash() error = %v", err)
		}
		if got, want := ctx.Request().URL.Path, "/docs/"; got != want {
			t.Fatalf("Path = %q, want %q", got, want)
		}
	})

	t.Run("static wrapper", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(dir+"/hello.txt", []byte("hello"), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		ctx := webtest.NewContext(httptest.NewRequest(http.MethodGet, "/hello.txt", nil), nil, "/hello.txt", nil)
		if err := Static(dir)(func(c web.Context) error { return c.NoContent(http.StatusNotFound) })(ctx); err != nil {
			t.Fatalf("Static() error = %v", err)
		}
		if got, want := strings.TrimSpace(ctx.ResponseWriter().(*httptest.ResponseRecorder).Body.String()), "hello"; got != want {
			t.Fatalf("body = %q, want %q", got, want)
		}
	})

	t.Run("timeout wrapper", func(t *testing.T) {
		ctx := webtest.NewContext(nil, nil, "/", nil)
		if err := Timeout()(func(c web.Context) error { return c.NoContent(http.StatusAccepted) })(ctx); err != nil {
			t.Fatalf("Timeout() error = %v", err)
		}
		if got, want := ctx.StatusCode(), http.StatusAccepted; got != want {
			t.Fatalf("status = %d, want %d", got, want)
		}
	})

	t.Run("compress wrapper", func(t *testing.T) {
		adapter := echoweb.New()
		adapter.Router().Use(Compress())
		adapter.Router().GET("/", func(r web.Context) error { return r.Text(http.StatusOK, "hello") })

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Accept-Encoding", "gzip")
		rec := httptest.NewRecorder()
		adapter.ServeHTTP(rec, req)

		if got, want := rec.Header().Get("Content-Encoding"), "gzip"; got != want {
			t.Fatalf("Content-Encoding = %q, want %q", got, want)
		}
	})

	t.Run("balancer helpers", func(t *testing.T) {
		targetA := &ProxyTarget{Name: "a", URL: mustParseURL(t, "http://a.example.com")}
		targetB := &ProxyTarget{Name: "b", URL: mustParseURL(t, "http://b.example.com")}

		random := NewRandomBalancer([]*ProxyTarget{targetA})
		if got := random.Next(nil); got != targetA {
			t.Fatalf("random.Next() = %#v, want %#v", got, targetA)
		}
		if ok := random.AddTarget(targetA); ok {
			t.Fatal("AddTarget should reject duplicate names")
		}
		if ok := random.AddTarget(targetB); !ok {
			t.Fatal("AddTarget should accept a new target")
		}
		if ok := random.RemoveTarget("b"); !ok {
			t.Fatal("RemoveTarget should remove existing target")
		}
		if ok := random.RemoveTarget("missing"); ok {
			t.Fatal("RemoveTarget should reject missing target")
		}
	})

	t.Run("invalid config error", func(t *testing.T) {
		if got, want := invalidConfigError("bad config").Error(), "web: bad config"; got != want {
			t.Fatalf("invalidConfigError() = %q, want %q", got, want)
		}
	})
}

func TestMiddlewareInternalHelpers(t *testing.T) {
	t.Run("parse body limit", func(t *testing.T) {
		if got, err := parseBodyLimit("2KB"); err != nil || got != 2<<10 {
			t.Fatalf("parseBodyLimit(2KB) = (%d, %v)", got, err)
		}
		if got, err := parseBodyLimit("3MB"); err != nil || got != 3<<20 {
			t.Fatalf("parseBodyLimit(3MB) = (%d, %v)", got, err)
		}
		if got, err := parseBodyLimit("1GB"); err != nil || got != 1<<30 {
			t.Fatalf("parseBodyLimit(1GB) = (%d, %v)", got, err)
		}
		if got, err := parseBodyLimit("4B"); err != nil || got != 4 {
			t.Fatalf("parseBodyLimit(4B) = (%d, %v)", got, err)
		}
		if _, err := parseBodyLimit(""); err == nil {
			t.Fatal("parseBodyLimit() should reject empty input")
		}
		if _, err := parseBodyLimit("0"); err == nil {
			t.Fatal("parseBodyLimit() should reject zero")
		}
		if _, err := parseBodyLimit("bogus"); err == nil {
			t.Fatal("parseBodyLimit() should reject invalid input")
		}
	})

	t.Run("request id generator", func(t *testing.T) {
		if got := defaultRequestIDGenerator(); len(got) != 32 {
			t.Fatalf("defaultRequestIDGenerator() len = %d", len(got))
		}
	})

	t.Run("request is https", func(t *testing.T) {
		ctx := webtest.NewContext(httptest.NewRequest(http.MethodGet, "https://example.com", nil), nil, "/", nil)
		if !requestIsHTTPS(ctx) {
			t.Fatal("requestIsHTTPS() should detect https scheme")
		}
		ctx = webtest.NewContext(httptest.NewRequest(http.MethodGet, "http://example.com", nil), nil, "/", nil)
		ctx.Request().Header.Set("X-Forwarded-Proto", "https")
		if !requestIsHTTPS(ctx) {
			t.Fatal("requestIsHTTPS() should detect forwarded https")
		}
		if requestIsHTTPS(requestIsHTTPOnlyContext{}) {
			t.Fatal("requestIsHTTPS() should be false without a request")
		}
	})

	t.Run("csrf random string", func(t *testing.T) {
		if got := randomString(8); len(got) != 8 {
			t.Fatalf("randomString(8) len = %d", len(got))
		}
		if got := randomString(0); got != "" {
			t.Fatalf("randomString(0) = %q", got)
		}
	})

	t.Run("param extractor", func(t *testing.T) {
		extractor := paramExtractor("id")
		ctx := webtest.NewContext(nil, nil, "/users/:id", webtest.PathParams{"id": "42"})
		values, err := extractor(ctx)
		if err != nil {
			t.Fatalf("paramExtractor(): %v", err)
		}
		if len(values) != 1 || values[0] != "42" {
			t.Fatalf("values = %#v", values)
		}
	})

	t.Run("normalize extractor error", func(t *testing.T) {
		testCases := []struct {
			err  error
			want string
		}{
			{nil, "missing key"},
			{errQueryExtractorValueMissing, "missing key in the query string"},
			{errCookieExtractorValueMissing, "missing key in cookies"},
			{errFormExtractorValueMissing, "missing key in the form"},
			{errHeaderExtractorValueMissing, "missing key in request header"},
			{errHeaderExtractorValueInvalid, "invalid key in the request header"},
		}
		for _, tc := range testCases {
			if got := normalizeExtractorError(tc.err).Error(); got != tc.want {
				t.Fatalf("normalizeExtractorError(%v) = %q, want %q", tc.err, got, tc.want)
			}
		}
	})

	t.Run("key auth missing unwrap", func(t *testing.T) {
		err := &ErrKeyAuthMissing{Err: errors.New("missing")}
		if !errors.Is(err, err.Err) {
			t.Fatal("ErrKeyAuthMissing should unwrap its inner error")
		}
	})

	t.Run("cors origin helpers", func(t *testing.T) {
		allowed, err := corsAllowedOrigin("https://api.example.com", CORSConfig{
			AllowOrigins: []string{"https://*.example.com"},
		}, nil)
		if err != nil {
			t.Fatalf("corsAllowedOrigin(): %v", err)
		}
		if got, want := allowed, "https://api.example.com"; got != want {
			t.Fatalf("corsAllowedOrigin() = %q, want %q", got, want)
		}
		if !corsMatchSubdomain("https://api.example.com", "https://*.example.com") {
			t.Fatal("corsMatchSubdomain() should match subdomains")
		}
		allowed, err = corsAllowedOrigin("https://api.example.com", CORSConfig{
			AllowOriginFunc: func(origin string) (bool, error) { return false, nil },
		}, nil)
		if err != nil || allowed != "" {
			t.Fatalf("AllowOriginFunc false = (%q, %v)", allowed, err)
		}
		wantErr := errors.New("origin failed")
		_, err = corsAllowedOrigin("https://api.example.com", CORSConfig{
			AllowOriginFunc: func(origin string) (bool, error) { return false, wantErr },
		}, nil)
		if !errors.Is(err, wantErr) {
			t.Fatalf("corsAllowedOrigin() error = %v", err)
		}
		allowed, err = corsAllowedOrigin("https://img.example.com", CORSConfig{
			AllowOrigins: []string{"https://example.com"},
		}, []*regexp.Regexp{regexp.MustCompile(`^https://.*\.example\.com$`)})
		if err != nil || allowed != "https://img.example.com" {
			t.Fatalf("regex corsAllowedOrigin() = (%q, %v)", allowed, err)
		}
	})

	t.Run("body dump writer helpers", func(t *testing.T) {
		rec := newFancyRecorder()
		writer := &bodyDumpResponseWriter{
			Writer:         io.Discard,
			ResponseWriter: rec,
		}
		writer.Flush()
		if _, _, err := writer.Hijack(); err != nil {
			t.Fatalf("Hijack(): %v", err)
		}
	})

	t.Run("body dump flush panics when unsupported", func(t *testing.T) {
		writer := &bodyDumpResponseWriter{
			Writer:         io.Discard,
			ResponseWriter: noFlushWriter{},
		}
		defer func() {
			if recover() == nil {
				t.Fatal("Flush() should panic when flushing is unsupported")
			}
		}()
		writer.Flush()
	})

	t.Run("gzip writer helpers", func(t *testing.T) {
		rec := newFancyRecorder()
		buffer := &bytes.Buffer{}
		gz := gzip.NewWriter(buffer)
		writer := &gzipResponseWriter{
			Writer:         gz,
			ResponseWriter: rec,
			buffer:         &bytes.Buffer{},
			minLength:      1,
		}
		if _, err := writer.Write([]byte("hello")); err != nil {
			t.Fatalf("Write(): %v", err)
		}
		writer.Flush()
		if _, _, err := writer.Hijack(); err != nil {
			t.Fatalf("Hijack(): %v", err)
		}
		if err := writer.Push("/assets/app.js", nil); err != nil {
			t.Fatalf("Push(): %v", err)
		}
		_ = gz.Close()
	})

	t.Run("gzip writer flush and push fallbacks", func(t *testing.T) {
		rec := newFancyRecorder()
		writer := &gzipResponseWriter{
			Writer:         bytes.NewBuffer(nil),
			ResponseWriter: rec,
			buffer:         bytes.NewBufferString("hello"),
			minLength:      10,
		}
		writer.Flush()
		if got := rec.Header().Get("Content-Encoding"); got != gzipScheme {
			t.Fatalf("Content-Encoding = %q", got)
		}
		if err := (&gzipResponseWriter{ResponseWriter: httptest.NewRecorder()}).Push("/assets/app.js", nil); !errors.Is(err, http.ErrNotSupported) {
			t.Fatalf("Push() error = %v", err)
		}
	})

	t.Run("ignorable writer", func(t *testing.T) {
		rec := httptest.NewRecorder()
		writer := &ignorableWriter{ResponseWriter: rec}
		writer.Ignore(true)
		writer.WriteHeader(http.StatusCreated)
		if _, err := writer.Write([]byte("ignored")); err != nil {
			t.Fatalf("Write(): %v", err)
		}
		if rec.Code == http.StatusCreated || rec.Body.Len() != 0 {
			t.Fatalf("ignore should suppress writes: code=%d body=%q", rec.Code, rec.Body.String())
		}
	})

	t.Run("request id handler and defaults", func(t *testing.T) {
		var handled string
		ctx := webtest.NewContext(nil, nil, "/", nil)
		err := RequestIDWithConfig(RequestIDConfig{
			RequestIDHandler: func(c web.Context, id string) { handled = id },
		})(func(c web.Context) error { return c.NoContent(http.StatusNoContent) })(ctx)
		if err != nil {
			t.Fatalf("RequestIDWithConfig() error = %v", err)
		}
		if handled == "" {
			t.Fatal("RequestIDHandler should receive the generated id")
		}
		if got := ctx.Response().Header().Get("X-Request-ID"); got == "" {
			t.Fatal("expected generated X-Request-ID header")
		}
	})

	t.Run("request id config defaults and custom keys", func(t *testing.T) {
		ctx := webtest.NewContext(nil, nil, "/", nil)
		ctx.Request().Header.Set("X-Custom-ID", "provided")
		err := RequestIDWithConfig(RequestIDConfig{
			TargetHeader: "X-Custom-ID",
			ContextKey:   "custom_id",
		})(func(c web.Context) error {
			if got := c.Get("custom_id"); got != "provided" {
				t.Fatalf("custom_id = %#v", got)
			}
			return c.NoContent(http.StatusNoContent)
		})(ctx)
		if err != nil {
			t.Fatalf("RequestIDWithConfig(custom): %v", err)
		}
		if got := ctx.Response().Header().Get("X-Custom-ID"); got != "provided" {
			t.Fatalf("X-Custom-ID = %q", got)
		}
	})

	t.Run("rate limiter cleanup", func(t *testing.T) {
		now := time.Now()
		store := &RateLimiterMemoryStore{
			visitors: map[string]*visitor{
				"stale": {Limiter: rate.NewLimiter(rate.Every(time.Second), 1), lastSeen: now.Add(-10 * time.Minute)},
				"fresh": {Limiter: rate.NewLimiter(rate.Every(time.Second), 1), lastSeen: now},
			},
			expiresIn:   time.Minute,
			lastCleanup: now.Add(-2 * time.Minute),
			timeNow:     func() time.Time { return now },
		}
		store.cleanupStaleVisitors()
		if _, ok := store.visitors["stale"]; ok {
			t.Fatal("cleanupStaleVisitors() should remove stale entries")
		}
		if _, ok := store.visitors["fresh"]; !ok {
			t.Fatal("cleanupStaleVisitors() should keep fresh entries")
		}
	})

	t.Run("proxy balancer edge cases", func(t *testing.T) {
		random := NewRandomBalancer(nil)
		if got := random.Next(nil); got != nil {
			t.Fatalf("random.Next(nil) = %#v", got)
		}

		rr := NewRoundRobinBalancer([]*ProxyTarget{
			{Name: "a", URL: mustParseURL(t, "http://a.example.com")},
			{Name: "b", URL: mustParseURL(t, "http://b.example.com")},
		})
		if got := rr.Next(nil).Name; got != "a" {
			t.Fatalf("round robin first = %q", got)
		}
		if got := rr.Next(nil).Name; got != "b" {
			t.Fatalf("round robin second = %q", got)
		}
		if got := rr.Next(nil).Name; got != "a" {
			t.Fatalf("round robin reset = %q", got)
		}
	})

	t.Run("timeout handler panic restores writer", func(t *testing.T) {
		ctx := newMutableContext(httptest.NewRequest(http.MethodGet, "/", nil))
		original := ctx.ResponseWriter()
		handler := timeoutHandler{
			writer:  &ignorableWriter{ResponseWriter: original},
			ctx:     ctx,
			handler: func(c web.Context) error { panic("boom") },
			errCh:   make(chan error, 1),
		}
		defer func() {
			if recover() == nil {
				t.Fatal("expected panic from timeoutHandler")
			}
			if ctx.ResponseWriter() != original {
				t.Fatal("timeoutHandler should restore original writer after panic")
			}
		}()
		handler.ServeHTTP(handler.writer, ctx.Request())
	})

	t.Run("timeout handler returns error and success paths", func(t *testing.T) {
		ctx := newMutableContext(httptest.NewRequest(http.MethodGet, "/", nil))
		original := ctx.ResponseWriter()
		errCh := make(chan error, 1)
		handler := timeoutHandler{
			writer:  &ignorableWriter{ResponseWriter: original},
			ctx:     ctx,
			handler: func(c web.Context) error { return errors.New("boom") },
			errCh:   errCh,
		}
		handler.ServeHTTP(handler.writer, ctx.Request())
		if got := <-errCh; got == nil || got.Error() != "boom" {
			t.Fatalf("timeoutHandler error = %v", got)
		}
		if ctx.ResponseWriter() != original {
			t.Fatal("timeoutHandler should restore original writer on error")
		}

		errCh = make(chan error, 1)
		handler = timeoutHandler{
			writer:  &ignorableWriter{ResponseWriter: original},
			ctx:     ctx,
			handler: func(c web.Context) error { return c.NoContent(http.StatusAccepted) },
			errCh:   errCh,
		}
		handler.ServeHTTP(handler.writer, ctx.Request())
		select {
		case err := <-errCh:
			t.Fatalf("unexpected timeoutHandler error: %v", err)
		default:
		}
	})

	t.Run("basic auth challenge helper", func(t *testing.T) {
		if got, want := basicAuthChallenge(defaultAuthRealm), "basic realm=Restricted"; got != want {
			t.Fatalf("basicAuthChallenge(default) = %q, want %q", got, want)
		}
		if got, want := basicAuthChallenge("Admin Area"), `basic realm="Admin Area"`; got != want {
			t.Fatalf("basicAuthChallenge(custom) = %q, want %q", got, want)
		}
	})

	t.Run("extractor helpers", func(t *testing.T) {
		if extractors, err := createExtractors("", ""); err != nil || extractors != nil {
			t.Fatalf("createExtractors(empty) = (%#v, %v)", extractors, err)
		}
		if _, err := createExtractors("bogus", ""); err == nil {
			t.Fatal("createExtractors() should reject malformed lookup")
		}
		if _, err := createExtractors("unknown:value", ""); err == nil {
			t.Fatal("createExtractors() should reject unsupported source")
		}

		if _, _, prefix, err := parseLookup("header:Authorization", "Bearer"); err != nil || prefix != "Bearer " {
			t.Fatalf("parseLookup(auth) = (%q, %v)", prefix, err)
		}
		if _, _, _, err := parseLookup("header:", ""); err == nil {
			t.Fatal("parseLookup() should reject empty names")
		}

		req := httptest.NewRequest(http.MethodGet, "/?token=a&token=b&token=c", nil)
		req.Header["Authorization"] = []string{"Bearer first", "Basic nope"}
		ctx := webtest.NewContext(req, nil, "/items/:id", webtest.PathParams{"id": "42"})

		values, err := headerExtractor("Authorization", "Bearer ")(ctx)
		if err != nil || len(values) != 1 || values[0] != "first" {
			t.Fatalf("headerExtractor() = (%#v, %v)", values, err)
		}
		if _, err := headerExtractor("Authorization", "Token ")(ctx); !errors.Is(err, errHeaderExtractorValueInvalid) {
			t.Fatalf("headerExtractor invalid prefix err = %v", err)
		}
		if _, err := headerExtractor("Authorization", "")(requestIsHTTPOnlyContext{}); !errors.Is(err, errHeaderExtractorValueMissing) {
			t.Fatalf("headerExtractor missing req err = %v", err)
		}

		values, err = queryExtractor("token")(ctx)
		if err != nil || len(values) != 3 {
			t.Fatalf("queryExtractor() = (%#v, %v)", values, err)
		}
		if _, err := queryExtractor("token")(requestIsHTTPOnlyContext{}); !errors.Is(err, errQueryExtractorValueMissing) {
			t.Fatalf("queryExtractor missing req err = %v", err)
		}

		if _, err := paramExtractor("missing")(ctx); !errors.Is(err, errParamExtractorValueMissing) {
			t.Fatalf("paramExtractor missing err = %v", err)
		}
		if _, err := cookieExtractor("missing")(ctx); !errors.Is(err, errCookieExtractorValueMissing) {
			t.Fatalf("cookieExtractor missing err = %v", err)
		}

		formReq := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("token= one &token=&token=two"))
		formReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		formCtx := webtest.NewContext(formReq, nil, "/", nil)
		values, err = formExtractor("token")(formCtx)
		if err != nil || len(values) != 2 || values[0] != "one" || values[1] != "two" {
			t.Fatalf("formExtractor() = (%#v, %v)", values, err)
		}

		badReq := httptest.NewRequest(http.MethodPost, "/", nil)
		badReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		badReq.Body = failingReadCloser{err: errors.New("read failed")}
		if _, err := formExtractor("token")(webtest.NewContext(badReq, nil, "/", nil)); err == nil {
			t.Fatal("formExtractor() should surface ParseForm errors")
		}
	})

	t.Run("decompress helper branches", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
		req.Header.Set("Content-Encoding", GZIPEncoding)
		ctx := webtest.NewContext(req, nil, "/", nil)
		called := false
		err := DecompressWithConfig(DecompressConfig{
			GzipDecompressPool: bogusDecompressPool{},
		})(func(c web.Context) error {
			called = true
			return nil
		})(ctx)
		if err != nil || ctx.StatusCode() != http.StatusInternalServerError || called {
			t.Fatalf("DecompressWithConfig(invalid pool) err=%v status=%d called=%v", err, ctx.StatusCode(), called)
		}

		req = httptest.NewRequest(http.MethodPost, "/", http.NoBody)
		req.Header.Set("Content-Encoding", GZIPEncoding)
		ctx = webtest.NewContext(req, nil, "/", nil)
		called = false
		err = DecompressWithConfig(DecompressConfig{})(func(c web.Context) error {
			called = true
			return c.NoContent(http.StatusAccepted)
		})(ctx)
		if err != nil || !called || ctx.StatusCode() != http.StatusAccepted {
			t.Fatalf("DecompressWithConfig(eof) err=%v status=%d called=%v", err, ctx.StatusCode(), called)
		}
	})

	t.Run("redirect helper branches", func(t *testing.T) {
		ctx := webtest.NewContext(httptest.NewRequest(http.MethodGet, "https://example.com/docs", nil), nil, "/docs", nil)
		err := HTTPSRedirectWithConfig(RedirectConfig{})(func(c web.Context) error {
			return c.NoContent(http.StatusAccepted)
		})(ctx)
		if err != nil || ctx.StatusCode() != http.StatusAccepted {
			t.Fatalf("HTTPSRedirectWithConfig(no-op) err=%v status=%d", err, ctx.StatusCode())
		}

		ctx = webtest.NewContext(httptest.NewRequest(http.MethodGet, "http://www.example.com/docs", nil), nil, "/docs", nil)
		err = NonWWWRedirectWithConfig(RedirectConfig{})(func(c web.Context) error { return nil })(ctx)
		if err != nil || ctx.StatusCode() != http.StatusMovedPermanently {
			t.Fatalf("NonWWWRedirectWithConfig(default code) err=%v status=%d", err, ctx.StatusCode())
		}
	})

	t.Run("method from form edge cases", func(t *testing.T) {
		if got := MethodFromForm("_method")(requestIsHTTPOnlyContext{}); got != "" {
			t.Fatalf("MethodFromForm(nil req) = %q", got)
		}

		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Body = failingReadCloser{err: errors.New("read failed")}
		if got := MethodFromForm("_method")(webtest.NewContext(req, nil, "/", nil)); got != "" {
			t.Fatalf("MethodFromForm(parse error) = %q", got)
		}
	})

	t.Run("context timeout helper branches", func(t *testing.T) {
		defer func() {
			if recover() == nil {
				t.Fatal("ContextTimeoutWithConfig() should panic on zero timeout")
			}
		}()
		_ = ContextTimeoutWithConfig(ContextTimeoutConfig{})
	})

	t.Run("context timeout skipper nil request and deadline handling", func(t *testing.T) {
		called := false
		ctx := requestIsHTTPOnlyContext{}
		err := ContextTimeoutWithConfig(ContextTimeoutConfig{
			Timeout: time.Millisecond,
			Skipper: func(web.Context) bool { return true },
		})(func(c web.Context) error {
			called = true
			return nil
		})(ctx)
		if err != nil || !called {
			t.Fatalf("ContextTimeoutWithConfig(skipper) err=%v called=%v", err, called)
		}

		called = false
		err = ContextTimeoutWithConfig(ContextTimeoutConfig{
			Timeout: time.Millisecond,
		})(func(c web.Context) error {
			called = true
			return nil
		})(ctx)
		if err != nil || !called {
			t.Fatalf("ContextTimeoutWithConfig(nil request) err=%v called=%v", err, called)
		}

		timeoutReq := httptest.NewRequest(http.MethodGet, "/", nil)
		timeoutCtx, cancel := context.WithDeadline(timeoutReq.Context(), time.Now().Add(-time.Millisecond))
		defer cancel()
		timeoutReq = timeoutReq.WithContext(timeoutCtx)
		timeoutWebCtx := webtest.NewContext(timeoutReq, nil, "/", nil)
		err = ContextTimeoutWithConfig(ContextTimeoutConfig{
			Timeout: time.Millisecond,
		})(func(c web.Context) error {
			return context.DeadlineExceeded
		})(timeoutWebCtx)
		if err != nil {
			t.Fatalf("ContextTimeoutWithConfig(deadline) err=%v", err)
		}
		if got := timeoutWebCtx.StatusCode(); got != http.StatusServiceUnavailable {
			t.Fatalf("status = %d", got)
		}
	})
}

func TestStaticWithConfigInternalBranches(t *testing.T) {
	t.Run("skipper bypass", func(t *testing.T) {
		called := false
		mw := StaticWithConfig(StaticConfig{
			Root:    ".",
			Skipper: func(web.Context) bool { return true },
		})
		ctx := webtest.NewContext(httptest.NewRequest(http.MethodGet, "/missing", nil), nil, "/missing", nil)
		err := mw(func(c web.Context) error {
			called = true
			return c.NoContent(http.StatusAccepted)
		})(ctx)
		if err != nil || !called || ctx.StatusCode() != http.StatusAccepted {
			t.Fatalf("skipper branch = called:%v status:%d err:%v", called, ctx.StatusCode(), err)
		}
	})

	t.Run("html5 fallback serves index", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(dir+"/index.html", []byte("<h1>spa</h1>"), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		ctx := webtest.NewContext(httptest.NewRequest(http.MethodGet, "/missing", nil), nil, "/missing", nil)
		err := StaticWithConfig(StaticConfig{Root: dir, HTML5: true})(func(c web.Context) error {
			return fs.ErrNotExist
		})(ctx)
		if err != nil {
			t.Fatalf("StaticWithConfig(html5): %v", err)
		}
		if got := strings.TrimSpace(ctx.ResponseWriter().(*httptest.ResponseRecorder).Body.String()); got != "<h1>spa</h1>" {
			t.Fatalf("body = %q", got)
		}
	})

	t.Run("ignore base strips route prefix", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(dir+"/index.html", []byte("<h1>root</h1>"), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		req := httptest.NewRequest(http.MethodGet, "/assets", nil)
		ctx := webtest.NewContext(req, nil, "/assets/*", webtest.PathParams{"*": "assets"})
		err := StaticWithConfig(StaticConfig{Root: dir, IgnoreBase: true})(func(c web.Context) error {
			return c.NoContent(http.StatusNotFound)
		})(ctx)
		if err != nil {
			t.Fatalf("StaticWithConfig(ignore base): %v", err)
		}
		if got := strings.TrimSpace(ctx.ResponseWriter().(*httptest.ResponseRecorder).Body.String()); got != "<h1>root</h1>" {
			t.Fatalf("body = %q", got)
		}
	})

	t.Run("non ignorable file errors bubble", func(t *testing.T) {
		ctx := webtest.NewContext(httptest.NewRequest(http.MethodGet, "/file", nil), nil, "/file", nil)
		wantErr := errors.New("boom")
		mw := StaticWithConfig(StaticConfig{Filesystem: failingFS{err: wantErr}})
		err := mw(func(c web.Context) error { return nil })(ctx)
		if !errors.Is(err, wantErr) {
			t.Fatalf("StaticWithConfig(non-ignorable) = %v", err)
		}
	})

	t.Run("ignorable permission error falls through", func(t *testing.T) {
		called := false
		ctx := webtest.NewContext(httptest.NewRequest(http.MethodGet, "/file", nil), nil, "/file", nil)
		mw := StaticWithConfig(StaticConfig{Filesystem: failingFS{err: fs.ErrPermission}})
		err := mw(func(c web.Context) error {
			called = true
			return c.NoContent(http.StatusAccepted)
		})(ctx)
		if err != nil || !called || ctx.StatusCode() != http.StatusAccepted {
			t.Fatalf("StaticWithConfig(permission) err=%v called=%v status=%d", err, called, ctx.StatusCode())
		}
	})

	t.Run("browse renders directory listing", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(dir+"/hello.txt", []byte("hello"), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		ctx := webtest.NewContext(httptest.NewRequest(http.MethodGet, "/", nil), nil, "/", nil)
		err := StaticWithConfig(StaticConfig{Root: dir, Browse: true})(func(c web.Context) error {
			return c.NoContent(http.StatusNotFound)
		})(ctx)
		if err != nil {
			t.Fatalf("StaticWithConfig(browse): %v", err)
		}
		body := ctx.ResponseWriter().(*httptest.ResponseRecorder).Body.String()
		if !strings.Contains(body, "hello.txt") {
			t.Fatalf("body = %q", body)
		}
	})
}

func TestProxyWithConfigInternalBranches(t *testing.T) {
	t.Run("skipper bypass", func(t *testing.T) {
		called := false
		mw := ProxyWithConfig(ProxyConfig{
			Balancer: NewRandomBalancer([]*ProxyTarget{{Name: "a", URL: mustParseURL(t, "http://example.com")}}),
			Skipper:  func(web.Context) bool { return true },
		})
		ctx := webtest.NewContext(httptest.NewRequest(http.MethodGet, "/", nil), nil, "/", nil)
		err := mw(func(c web.Context) error {
			called = true
			return c.NoContent(http.StatusAccepted)
		})(ctx)
		if err != nil || !called || ctx.StatusCode() != http.StatusAccepted {
			t.Fatalf("skipper branch = called:%v status:%d err:%v", called, ctx.StatusCode(), err)
		}
	})

	t.Run("missing target hits error handler", func(t *testing.T) {
		wantErr := errors.New("proxy target unavailable")
		mw := ProxyWithConfig(ProxyConfig{
			Balancer: emptyBalancer{},
			ErrorHandler: func(c web.Context, err error) error {
				return err
			},
		})
		ctx := webtest.NewContext(httptest.NewRequest(http.MethodGet, "/", nil), nil, "/", nil)
		err := mw(func(c web.Context) error { return nil })(ctx)
		if err == nil || !strings.Contains(err.Error(), wantErr.Error()) {
			t.Fatalf("ProxyWithConfig(missing target) = %v", err)
		}
	})

	t.Run("rewrite parse error hits handler", func(t *testing.T) {
		wantErr := errors.New("handled rewrite")
		target := &ProxyTarget{Name: "a", URL: mustParseURL(t, "http://example.com")}
		mw := ProxyWithConfig(ProxyConfig{
			Balancer: NewRandomBalancer([]*ProxyTarget{target}),
			RegexRewrite: map[*regexp.Regexp]string{
				regexp.MustCompile(`^/bad$`): "://bad",
			},
			ErrorHandler: func(c web.Context, err error) error {
				return wantErr
			},
		})
		ctx := webtest.NewContext(httptest.NewRequest(http.MethodGet, "/bad", nil), nil, "/", nil)
		err := mw(func(c web.Context) error { return nil })(ctx)
		if !errors.Is(err, wantErr) {
			t.Fatalf("ProxyWithConfig(rewrite error) = %v", err)
		}
	})

	t.Run("director and modify response", func(t *testing.T) {
		var sawModify bool
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if got := r.Header.Get("X-Real-IP"); got == "" {
				t.Fatal("expected X-Real-IP header")
			}
			if got := r.Header.Get("X-Forwarded-Proto"); got == "" {
				t.Fatal("expected X-Forwarded-Proto header")
			}
			fmt.Fprintf(w, "ok:%s?%s", r.URL.Path, r.URL.RawQuery)
		}))
		defer upstream.Close()

		mw := ProxyWithConfig(ProxyConfig{
			Balancer: NewRandomBalancer([]*ProxyTarget{{Name: "upstream", URL: mustParseURL(t, upstream.URL)}}),
			ModifyResponse: func(res *http.Response) error {
				sawModify = true
				res.Header.Set("X-Proxy", "on")
				return nil
			},
		})

		req := httptest.NewRequest(http.MethodGet, "/docs?expand=1", nil)
		req.RemoteAddr = "203.0.113.10:1234"
		ctx := webtest.NewContext(req, nil, "/", nil)
		err := mw(func(c web.Context) error { return nil })(ctx)
		if err != nil {
			t.Fatalf("ProxyWithConfig(proxy): %v", err)
		}
		if !sawModify {
			t.Fatal("ModifyResponse should run")
		}
		rec := ctx.ResponseWriter().(*httptest.ResponseRecorder)
		if got := rec.Header().Get("X-Proxy"); got != "on" {
			t.Fatalf("X-Proxy = %q", got)
		}
		if got := rec.Body.String(); !strings.Contains(got, "ok:/docs?expand=1") {
			t.Fatalf("body = %q", got)
		}
	})
}

func TestRewriteRequestInternalBranches(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://example.com/docs", nil)
	req.RequestURI = "http://example.com/docs"
	err := rewriteRequest(map[*regexp.Regexp]string{
		regexp.MustCompile(`^/docs$`): "/v2/docs",
	}, req)
	if err != nil {
		t.Fatalf("rewriteRequest(abs uri): %v", err)
	}
	if got, want := req.URL.Path, "/v2/docs"; got != want {
		t.Fatalf("path = %q, want %q", got, want)
	}
	if got, want := sanitizeURI("//cdn/assets"), "/cdn/assets"; got != want {
		t.Fatalf("sanitizeURI() = %q, want %q", got, want)
	}
}

type emptyBalancer struct{}

func (emptyBalancer) AddTarget(*ProxyTarget) bool   { return false }
func (emptyBalancer) RemoveTarget(string) bool      { return false }
func (emptyBalancer) Next(web.Context) *ProxyTarget { return nil }

type failingFS struct{ err error }

func (f failingFS) Open(string) (http.File, error) { return nil, f.err }

type noFlushWriter struct{}

func (noFlushWriter) Header() http.Header       { return http.Header{} }
func (noFlushWriter) Write([]byte) (int, error) { return 0, nil }
func (noFlushWriter) WriteHeader(int)           {}

type failingReadCloser struct{ err error }

func (f failingReadCloser) Read([]byte) (int, error) { return 0, f.err }
func (f failingReadCloser) Close() error             { return nil }

type bogusDecompressPool struct{}

func (bogusDecompressPool) gzipDecompressPool() sync.Pool {
	return sync.Pool{New: func() any { return "bogus" }}
}

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("url.Parse(%q): %v", raw, err)
	}
	return parsed
}

type fancyRecorder struct {
	*httptest.ResponseRecorder
	pushes []string
}

func newFancyRecorder() *fancyRecorder {
	return &fancyRecorder{ResponseRecorder: httptest.NewRecorder()}
}

func (r *fancyRecorder) Flush() {}

func (r *fancyRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	server, client := net.Pipe()
	reader := bufio.NewReader(client)
	writer := bufio.NewWriter(client)
	return server, bufio.NewReadWriter(reader, writer), nil
}

func (r *fancyRecorder) Push(target string, _ *http.PushOptions) error {
	r.pushes = append(r.pushes, target)
	return nil
}

type requestIsHTTPOnlyContext struct{}

func (requestIsHTTPOnlyContext) Context() context.Context              { return context.Background() }
func (requestIsHTTPOnlyContext) Method() string                        { return http.MethodGet }
func (requestIsHTTPOnlyContext) Path() string                          { return "/" }
func (requestIsHTTPOnlyContext) URI() string                           { return "/" }
func (requestIsHTTPOnlyContext) Scheme() string                        { return "http" }
func (requestIsHTTPOnlyContext) Host() string                          { return "example.com" }
func (requestIsHTTPOnlyContext) Param(string) string                   { return "" }
func (requestIsHTTPOnlyContext) Query(string) string                   { return "" }
func (requestIsHTTPOnlyContext) Header(string) string                  { return "" }
func (requestIsHTTPOnlyContext) Cookie(string) (*http.Cookie, error)   { return nil, http.ErrNoCookie }
func (requestIsHTTPOnlyContext) RealIP() string                        { return "127.0.0.1" }
func (requestIsHTTPOnlyContext) Request() *http.Request                { return nil }
func (requestIsHTTPOnlyContext) SetRequest(*http.Request)              {}
func (requestIsHTTPOnlyContext) Response() web.Response                { return nil }
func (requestIsHTTPOnlyContext) ResponseWriter() http.ResponseWriter   { return httptest.NewRecorder() }
func (requestIsHTTPOnlyContext) SetResponseWriter(http.ResponseWriter) {}
func (requestIsHTTPOnlyContext) Bind(any) error                        { return nil }
func (requestIsHTTPOnlyContext) Set(string, any)                       {}
func (requestIsHTTPOnlyContext) Get(string) any                        { return nil }
func (requestIsHTTPOnlyContext) AddHeader(string, string)              {}
func (requestIsHTTPOnlyContext) SetHeader(string, string)              {}
func (requestIsHTTPOnlyContext) SetCookie(*http.Cookie)                {}
func (requestIsHTTPOnlyContext) JSON(int, any) error                   { return nil }
func (requestIsHTTPOnlyContext) Blob(int, string, []byte) error        { return nil }
func (requestIsHTTPOnlyContext) File(string) error                     { return nil }
func (requestIsHTTPOnlyContext) Text(int, string) error                { return nil }
func (requestIsHTTPOnlyContext) HTML(int, string) error                { return nil }
func (requestIsHTTPOnlyContext) NoContent(int) error                   { return nil }
func (requestIsHTTPOnlyContext) Redirect(int, string) error            { return nil }
func (requestIsHTTPOnlyContext) StatusCode() int                       { return 0 }
func (requestIsHTTPOnlyContext) Native() any                           { return nil }

type mutableContext struct {
	request *http.Request
	writer  http.ResponseWriter
	values  map[string]any
}

func newMutableContext(req *http.Request) *mutableContext {
	return &mutableContext{
		request: req,
		writer:  httptest.NewRecorder(),
		values:  map[string]any{},
	}
}

func (c *mutableContext) Context() context.Context                 { return c.request.Context() }
func (c *mutableContext) Method() string                           { return c.request.Method }
func (c *mutableContext) Path() string                             { return c.request.URL.Path }
func (c *mutableContext) URI() string                              { return c.request.URL.RequestURI() }
func (c *mutableContext) Scheme() string                           { return "http" }
func (c *mutableContext) Host() string                             { return c.request.Host }
func (c *mutableContext) Param(string) string                      { return "" }
func (c *mutableContext) Query(name string) string                 { return c.request.URL.Query().Get(name) }
func (c *mutableContext) Header(name string) string                { return c.request.Header.Get(name) }
func (c *mutableContext) Cookie(name string) (*http.Cookie, error) { return c.request.Cookie(name) }
func (c *mutableContext) RealIP() string                           { return "127.0.0.1" }
func (c *mutableContext) Request() *http.Request                   { return c.request }
func (c *mutableContext) SetRequest(req *http.Request)             { c.request = req }
func (c *mutableContext) Response() web.Response                   { return mutableResponse{ctx: c} }
func (c *mutableContext) ResponseWriter() http.ResponseWriter      { return c.writer }
func (c *mutableContext) SetResponseWriter(w http.ResponseWriter)  { c.writer = w }
func (c *mutableContext) Bind(any) error                           { return nil }
func (c *mutableContext) Set(key string, value any)                { c.values[key] = value }
func (c *mutableContext) Get(key string) any                       { return c.values[key] }
func (c *mutableContext) AddHeader(name, value string)             { c.writer.Header().Add(name, value) }
func (c *mutableContext) SetHeader(name, value string)             { c.writer.Header().Set(name, value) }
func (c *mutableContext) SetCookie(cookie *http.Cookie)            { http.SetCookie(c.writer, cookie) }
func (c *mutableContext) JSON(code int, payload any) error         { c.writer.WriteHeader(code); return nil }
func (c *mutableContext) Blob(code int, contentType string, body []byte) error {
	c.writer.Header().Set("Content-Type", contentType)
	c.writer.WriteHeader(code)
	_, err := c.writer.Write(body)
	return err
}
func (c *mutableContext) File(string) error { return nil }
func (c *mutableContext) Text(code int, body string) error {
	c.writer.WriteHeader(code)
	_, err := c.writer.Write([]byte(body))
	return err
}
func (c *mutableContext) HTML(code int, body string) error {
	c.writer.WriteHeader(code)
	_, err := c.writer.Write([]byte(body))
	return err
}
func (c *mutableContext) NoContent(code int) error { c.writer.WriteHeader(code); return nil }
func (c *mutableContext) Redirect(code int, url string) error {
	http.Redirect(c.writer, c.request, url, code)
	return nil
}
func (c *mutableContext) StatusCode() int {
	if rec, ok := c.writer.(*httptest.ResponseRecorder); ok {
		return rec.Code
	}
	return 0
}
func (c *mutableContext) Native() any { return c.writer }

type mutableResponse struct{ ctx *mutableContext }

func (r mutableResponse) Header() http.Header             { return r.ctx.writer.Header() }
func (r mutableResponse) Writer() http.ResponseWriter     { return r.ctx.writer }
func (r mutableResponse) SetWriter(w http.ResponseWriter) { r.ctx.writer = w }
func (r mutableResponse) StatusCode() int                 { return r.ctx.StatusCode() }
func (r mutableResponse) Size() int64 {
	if rec, ok := r.ctx.writer.(*httptest.ResponseRecorder); ok {
		return int64(rec.Body.Len())
	}
	return 0
}
func (r mutableResponse) Committed() bool {
	if rec, ok := r.ctx.writer.(*httptest.ResponseRecorder); ok {
		return rec.Code != 0
	}
	return false
}
func (r mutableResponse) Native() any { return r.ctx.writer }

func TestProxyForwardsRequestToBackend(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(r.URL.Path + "?" + r.URL.RawQuery))
	}))
	defer backend.Close()

	targetURL, err := url.Parse(backend.URL)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	adapter := echoweb.New()
	router := adapter.Router()
	router.Use(Proxy(NewRoundRobinBalancer([]*ProxyTarget{{
		Name: "backend",
		URL:  targetURL,
	}})))
	router.GET("/*", func(r web.Context) error {
		return r.NoContent(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/proxy/path?x=1", nil)
	rec := httptest.NewRecorder()
	adapter.Echo().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); body != "/proxy/path?x=1" {
		t.Fatalf("body = %q", body)
	}
}

func TestProxyRewriteAdjustsBackendPath(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(r.URL.Path))
	}))
	defer backend.Close()

	targetURL, err := url.Parse(backend.URL)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	adapter := echoweb.New()
	router := adapter.Router()
	router.Pre(Rewrite(map[string]string{
		"/old/*": "/new/$1",
	}))
	router.Use(Proxy(NewRoundRobinBalancer([]*ProxyTarget{{
		Name: "backend",
		URL:  targetURL,
	}})))
	router.GET("/*", func(r web.Context) error {
		return r.NoContent(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/old/path", nil)
	rec := httptest.NewRecorder()
	adapter.Echo().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); body != "/new/path" {
		t.Fatalf("body = %q", body)
	}
}

func TestCreateExtractorsReadsHeaderQueryCookieAndFormValues(t *testing.T) {
	extractors, err := CreateExtractors("header:X-Api-Key,query:key,cookie:session,form:key")
	if err != nil {
		t.Fatalf("CreateExtractors: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/extract?key=query-value", strings.NewReader("key=form-value"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Api-Key", "header-value")
	req.AddCookie(&http.Cookie{Name: "session", Value: "cookie-value"})

	adapter := echoweb.New()
	router := adapter.Router()
	router.POST("/extract", func(r web.Context) error {
		var values []string
		for _, extractor := range extractors {
			extracted, err := extractor(r)
			if err != nil {
				t.Fatalf("extractor error: %v", err)
			}
			values = append(values, extracted...)
		}
		return r.Text(http.StatusOK, strings.Join(values, ","))
	})

	rec := httptest.NewRecorder()
	adapter.Echo().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); body != "header-value,query-value,cookie-value,form-value" {
		t.Fatalf("body = %q", body)
	}
}

func TestErrorBodyDumpCapturesOnlyFailedResponses(t *testing.T) {
	var capturedStatus int
	var capturedBody []byte

	adapter := echoweb.New()
	router := adapter.Router()
	router.Use(ErrorBodyDump(func(r web.Context, status int, body []byte) {
		capturedStatus = status
		capturedBody = append([]byte(nil), body...)
	}))
	router.GET("/ok", func(r web.Context) error {
		return r.Text(http.StatusOK, "ok")
	})
	router.GET("/fail", func(r web.Context) error {
		return r.Text(http.StatusBadGateway, "boom")
	})

	okReq := httptest.NewRequest(http.MethodGet, "/ok", nil)
	okRec := httptest.NewRecorder()
	adapter.Echo().ServeHTTP(okRec, okReq)
	if capturedStatus != 0 || len(capturedBody) != 0 {
		t.Fatalf("unexpected capture on ok response: status=%d body=%q", capturedStatus, string(capturedBody))
	}

	failReq := httptest.NewRequest(http.MethodGet, "/fail", nil)
	failRec := httptest.NewRecorder()
	adapter.Echo().ServeHTTP(failRec, failReq)

	if failRec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d body=%s", failRec.Code, failRec.Body.String())
	}
	if capturedStatus != http.StatusBadGateway {
		t.Fatalf("captured status = %d", capturedStatus)
	}
	if string(capturedBody) != "boom" {
		t.Fatalf("captured body = %q", string(capturedBody))
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

type stubResponse struct {
	headers http.Header
	writer  http.ResponseWriter
}

func newStubContext() *stubContext {
	return &stubContext{
		headers: http.Header{},
		values:  map[string]any{},
	}
}

func (c *stubContext) Context() context.Context                 { return context.Background() }
func (c *stubContext) Method() string                           { return http.MethodGet }
func (c *stubContext) Path() string                             { return "/" }
func (c *stubContext) URI() string                              { return "/" }
func (c *stubContext) Scheme() string                           { return "http" }
func (c *stubContext) Host() string                             { return "example.com" }
func (c *stubContext) Param(name string) string                 { return "" }
func (c *stubContext) Query(name string) string                 { return "" }
func (c *stubContext) Header(name string) string                { return c.headers.Get(name) }
func (c *stubContext) Cookie(name string) (*http.Cookie, error) { return nil, http.ErrNoCookie }
func (c *stubContext) RealIP() string                           { return "127.0.0.1" }
func (c *stubContext) Request() *http.Request                   { return httptest.NewRequest(http.MethodGet, "/", nil) }
func (c *stubContext) SetRequest(request *http.Request)         {}
func (c *stubContext) Response() web.Response {
	return &stubResponse{
		headers: c.headers,
		writer:  httptest.NewRecorder(),
	}
}
func (c *stubContext) ResponseWriter() http.ResponseWriter          { return httptest.NewRecorder() }
func (c *stubContext) SetResponseWriter(writer http.ResponseWriter) {}
func (c *stubContext) Bind(target any) error                        { return nil }
func (c *stubContext) Set(key string, value any)                    { c.values[key] = value }
func (c *stubContext) Get(key string) any                           { return c.values[key] }
func (c *stubContext) AddHeader(name string, value string)          { c.headers.Add(name, value) }
func (c *stubContext) SetHeader(name string, value string)          { c.headers.Set(name, value) }
func (c *stubContext) SetCookie(cookie *http.Cookie)                {}
func (c *stubContext) JSON(code int, payload any) error             { return nil }
func (c *stubContext) Blob(code int, contentType string, body []byte) error {
	return nil
}
func (c *stubContext) File(path string) error              { return nil }
func (c *stubContext) Text(code int, body string) error    { return nil }
func (c *stubContext) HTML(code int, body string) error    { return nil }
func (c *stubContext) NoContent(code int) error            { return nil }
func (c *stubContext) Redirect(code int, url string) error { return nil }
func (c *stubContext) StatusCode() int                     { return http.StatusOK }
func (c *stubContext) Native() any                         { return nil }

func (r *stubResponse) Header() http.Header                  { return r.headers }
func (r *stubResponse) Writer() http.ResponseWriter          { return r.writer }
func (r *stubResponse) SetWriter(writer http.ResponseWriter) { r.writer = writer }
func (r *stubResponse) StatusCode() int                      { return http.StatusOK }
func (r *stubResponse) Size() int64                          { return 0 }
func (r *stubResponse) Committed() bool                      { return false }
func (r *stubResponse) Native() any                          { return nil }

func pathJoin(parts ...string) string {
	return strings.Join(parts, string(os.PathSeparator))
}
