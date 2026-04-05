package echoweb

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/goforj/web"
	echo "github.com/labstack/echo/v4"
)

func BenchmarkEchoPlainText(b *testing.B) {
	engine := echo.New()
	engine.GET("/plain", func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest(http.MethodGet, "/plain", nil)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			b.Fatalf("status = %d", rec.Code)
		}
	}
}

func BenchmarkWebPlainText(b *testing.B) {
	adapter := New()
	adapter.Router().GET("/plain", func(r web.Context) error {
		return r.Text(http.StatusOK, "ok")
	})

	req := httptest.NewRequest(http.MethodGet, "/plain", nil)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		adapter.Echo().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			b.Fatalf("status = %d", rec.Code)
		}
	}
}

func BenchmarkEchoParamsJSON(b *testing.B) {
	engine := echo.New()
	engine.GET("/users/:id", func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]any{
			"id":     c.Param("id"),
			"method": c.Request().Method,
		})
	})

	req := httptest.NewRequest(http.MethodGet, "/users/42", nil)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			b.Fatalf("status = %d", rec.Code)
		}
	}
}

func BenchmarkWebParamsJSON(b *testing.B) {
	adapter := New()
	adapter.Router().GET("/users/:id", func(r web.Context) error {
		return r.JSON(http.StatusOK, map[string]any{
			"id":     r.Param("id"),
			"method": r.Method(),
		})
	})

	req := httptest.NewRequest(http.MethodGet, "/users/42", nil)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		adapter.Echo().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			b.Fatalf("status = %d", rec.Code)
		}
	}
}

func BenchmarkEchoMiddlewareChain(b *testing.B) {
	engine := echo.New()
	engine.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			c.Set("a", "1")
			return next(c)
		}
	})
	engine.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			c.Response().Header().Set("X-B", "1")
			return next(c)
		}
	})
	engine.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if c.Get("a") == nil {
				b.Fatal("missing context value")
			}
			return next(c)
		}
	})
	engine.GET("/chain", func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest(http.MethodGet, "/chain", nil)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			b.Fatalf("status = %d", rec.Code)
		}
	}
}

func BenchmarkWebMiddlewareChain(b *testing.B) {
	adapter := New()
	adapter.Router().Use(func(next web.Handler) web.Handler {
		return func(r web.Context) error {
			r.Set("a", "1")
			return next(r)
		}
	})
	adapter.Router().Use(func(next web.Handler) web.Handler {
		return func(r web.Context) error {
			r.SetHeader("X-B", "1")
			return next(r)
		}
	})
	adapter.Router().Use(func(next web.Handler) web.Handler {
		return func(r web.Context) error {
			if r.Get("a") == nil {
				b.Fatal("missing context value")
			}
			return next(r)
		}
	})
	adapter.Router().GET("/chain", func(r web.Context) error {
		return r.Text(http.StatusOK, "ok")
	})

	req := httptest.NewRequest(http.MethodGet, "/chain", nil)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		adapter.Echo().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			b.Fatalf("status = %d", rec.Code)
		}
	}
}

func BenchmarkWebMiddlewareChainSingleUse(b *testing.B) {
	adapter := New()
	adapter.Router().Use(
		func(next web.Handler) web.Handler {
			return func(r web.Context) error {
				r.Set("a", "1")
				return next(r)
			}
		},
		func(next web.Handler) web.Handler {
			return func(r web.Context) error {
				r.SetHeader("X-B", "1")
				return next(r)
			}
		},
		func(next web.Handler) web.Handler {
			return func(r web.Context) error {
				if r.Get("a") == nil {
					b.Fatal("missing context value")
				}
				return next(r)
			}
		},
	)
	adapter.Router().GET("/chain", func(r web.Context) error {
		return r.Text(http.StatusOK, "ok")
	})

	req := httptest.NewRequest(http.MethodGet, "/chain", nil)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		adapter.Echo().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			b.Fatalf("status = %d", rec.Code)
		}
	}
}
