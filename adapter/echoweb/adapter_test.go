package echoweb

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/goforj/web"
	echo "github.com/labstack/echo/v4"
)

func TestRouterRegistersRouteAndContext(t *testing.T) {
	adapter := New()
	adapter.Router().Get("/users/:id", func(r web.Context) error {
		if got := r.Param("id"); got != "42" {
			t.Fatalf("param id = %q", got)
		}
		if got := r.Path(); got != "/users/:id" {
			t.Fatalf("path = %q", got)
		}
		return r.JSON(http.StatusOK, map[string]any{
			"id":     r.Param("id"),
			"method": r.Method(),
		})
	})

	req := httptest.NewRequest(http.MethodGet, "/users/42", nil)
	rec := httptest.NewRecorder()
	adapter.Echo().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); body != "{\"id\":\"42\",\"method\":\"GET\"}\n" {
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

	group.Get("/ping", func(r web.Context) error {
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

func TestWrapMiddlewareBridgesEchoMiddleware(t *testing.T) {
	adapter := New()
	router := adapter.Router()
	router.Use(WrapMiddleware(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			c.Response().Header().Set("X-Echo-MW", "on")
			return next(c)
		}
	}))

	router.Get("/mw", func(r web.Context) error {
		return r.NoContent(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/mw", nil)
	rec := httptest.NewRecorder()
	adapter.Echo().ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := rec.Header().Get("X-Echo-MW"); got != "on" {
		t.Fatalf("X-Echo-MW = %q", got)
	}
}

func TestContextSupportsHeadersAndBlobResponses(t *testing.T) {
	adapter := New()
	adapter.Router().Get("/blob", func(r web.Context) error {
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
