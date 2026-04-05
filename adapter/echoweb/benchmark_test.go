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

func BenchmarkEchoGroupAndRouteMiddleware(b *testing.B) {
	engine := echo.New()
	group := engine.Group("/api",
		func(next echo.HandlerFunc) echo.HandlerFunc {
			return func(c echo.Context) error {
				c.Set("group", "1")
				return next(c)
			}
		},
		func(next echo.HandlerFunc) echo.HandlerFunc {
			return func(c echo.Context) error {
				c.Response().Header().Set("X-Group", "1")
				return next(c)
			}
		},
	)
	group.GET("/users/:id", func(c echo.Context) error {
		if c.Get("group") == nil {
			b.Fatal("missing group middleware state")
		}
		if c.Get("route") == nil {
			b.Fatal("missing route middleware state")
		}
		return c.JSON(http.StatusOK, map[string]any{
			"id":     c.Param("id"),
			"method": c.Request().Method,
		})
	}, func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			c.Set("route", "1")
			return next(c)
		}
	}, func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			return next(c)
		}
	})

	req := httptest.NewRequest(http.MethodGet, "/api/users/42", nil)
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

func BenchmarkWebGroupAndRouteMiddleware(b *testing.B) {
	adapter := New()
	group := adapter.Router().Group("/api",
		func(next web.Handler) web.Handler {
			return func(r web.Context) error {
				r.Set("group", "1")
				return next(r)
			}
		},
		func(next web.Handler) web.Handler {
			return func(r web.Context) error {
				r.SetHeader("X-Group", "1")
				return next(r)
			}
		},
	)
	group.GET("/users/:id", func(r web.Context) error {
		if r.Get("group") == nil {
			b.Fatal("missing group middleware state")
		}
		return r.JSON(http.StatusOK, map[string]any{
			"id":     r.Param("id"),
			"method": r.Method(),
		})
	}, func(next web.Handler) web.Handler {
		return func(r web.Context) error {
			r.Set("route", "1")
			return next(r)
		}
	}, func(next web.Handler) web.Handler {
		return func(r web.Context) error {
			if r.Get("route") == nil {
				b.Fatal("missing route middleware state")
			}
			return next(r)
		}
	})

	req := httptest.NewRequest(http.MethodGet, "/api/users/42", nil)
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
