package echoweb

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/goforj/web"
	"github.com/goforj/web/webmiddleware"
	echo "github.com/labstack/echo/v5"
	echomiddleware "github.com/labstack/echo/v5/middleware"
)

func BenchmarkEchoRepresentative(b *testing.B) {
	b.Run("plain_text", benchmarkEchoPlainTextRepresentative)
	b.Run("params_json", benchmarkEchoParamsJSONRepresentative)
	b.Run("middleware_chain", benchmarkEchoMiddlewareChainRepresentative)
	b.Run("group_route_middleware", benchmarkEchoGroupAndRouteRepresentative)
	b.Run("compress", benchmarkEchoCompressRepresentative)
}

func BenchmarkWebRepresentative(b *testing.B) {
	b.Run("plain_text", benchmarkWebPlainTextRepresentative)
	b.Run("params_json", benchmarkWebParamsJSONRepresentative)
	b.Run("middleware_chain", benchmarkWebMiddlewareChainRepresentative)
	b.Run("group_route_middleware", benchmarkWebGroupAndRouteRepresentative)
	b.Run("compress", benchmarkWebCompressRepresentative)
}

func benchmarkEchoPlainTextRepresentative(b *testing.B) {
	engine := echo.New()
	engine.GET("/plain", func(c *echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest(http.MethodGet, "/plain", nil)
	runHTTPBenchmark(b, engine, func() *http.Request { return req }, func(rec *httptest.ResponseRecorder) {
		if rec.Code != http.StatusOK {
			b.Fatalf("status = %d", rec.Code)
		}
	})
}

func benchmarkWebPlainTextRepresentative(b *testing.B) {
	adapter := New()
	adapter.Router().GET("/plain", func(c web.Context) error {
		return c.Text(http.StatusOK, "ok")
	})

	req := httptest.NewRequest(http.MethodGet, "/plain", nil)
	runHTTPBenchmark(b, adapter.Echo(), func() *http.Request { return req }, func(rec *httptest.ResponseRecorder) {
		if rec.Code != http.StatusOK {
			b.Fatalf("status = %d", rec.Code)
		}
	})
}

func benchmarkEchoParamsJSONRepresentative(b *testing.B) {
	engine := echo.New()
	engine.GET("/users/:id", func(c *echo.Context) error {
		return c.JSON(http.StatusOK, map[string]any{
			"id":     c.Param("id"),
			"method": c.Request().Method,
		})
	})

	req := httptest.NewRequest(http.MethodGet, "/users/42", nil)
	runHTTPBenchmark(b, engine, func() *http.Request { return req }, func(rec *httptest.ResponseRecorder) {
		if rec.Code != http.StatusOK {
			b.Fatalf("status = %d", rec.Code)
		}
	})
}

func benchmarkWebParamsJSONRepresentative(b *testing.B) {
	adapter := New()
	adapter.Router().GET("/users/:id", func(c web.Context) error {
		return c.JSON(http.StatusOK, map[string]any{
			"id":     c.Param("id"),
			"method": c.Method(),
		})
	})

	req := httptest.NewRequest(http.MethodGet, "/users/42", nil)
	runHTTPBenchmark(b, adapter.Echo(), func() *http.Request { return req }, func(rec *httptest.ResponseRecorder) {
		if rec.Code != http.StatusOK {
			b.Fatalf("status = %d", rec.Code)
		}
	})
}

func benchmarkEchoMiddlewareChainRepresentative(b *testing.B) {
	engine := echo.New()
	engine.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			c.Set("a", "1")
			return next(c)
		}
	})
	engine.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			c.Response().Header().Set("X-B", "1")
			return next(c)
		}
	})
	engine.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			if c.Get("a") == nil {
				b.Fatal("missing context value")
			}
			return next(c)
		}
	})
	engine.GET("/chain", func(c *echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest(http.MethodGet, "/chain", nil)
	runHTTPBenchmark(b, engine, func() *http.Request { return req }, func(rec *httptest.ResponseRecorder) {
		if rec.Code != http.StatusOK {
			b.Fatalf("status = %d", rec.Code)
		}
	})
}

func benchmarkWebMiddlewareChainRepresentative(b *testing.B) {
	adapter := New()
	adapter.Router().Use(
		func(next web.Handler) web.Handler {
			return func(c web.Context) error {
				c.Set("a", "1")
				return next(c)
			}
		},
		func(next web.Handler) web.Handler {
			return func(c web.Context) error {
				c.SetHeader("X-B", "1")
				return next(c)
			}
		},
		func(next web.Handler) web.Handler {
			return func(c web.Context) error {
				if c.Get("a") == nil {
					b.Fatal("missing context value")
				}
				return next(c)
			}
		},
	)
	adapter.Router().GET("/chain", func(c web.Context) error {
		return c.Text(http.StatusOK, "ok")
	})

	req := httptest.NewRequest(http.MethodGet, "/chain", nil)
	runHTTPBenchmark(b, adapter.Echo(), func() *http.Request { return req }, func(rec *httptest.ResponseRecorder) {
		if rec.Code != http.StatusOK {
			b.Fatalf("status = %d", rec.Code)
		}
	})
}

func benchmarkEchoGroupAndRouteRepresentative(b *testing.B) {
	engine := echo.New()
	group := engine.Group("/api",
		func(next echo.HandlerFunc) echo.HandlerFunc {
			return func(c *echo.Context) error {
				c.Set("group", "1")
				return next(c)
			}
		},
		func(next echo.HandlerFunc) echo.HandlerFunc {
			return func(c *echo.Context) error {
				c.Response().Header().Set("X-Group", "1")
				return next(c)
			}
		},
	)
	group.GET("/users/:id", func(c *echo.Context) error {
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
		return func(c *echo.Context) error {
			c.Set("route", "1")
			return next(c)
		}
	})

	req := httptest.NewRequest(http.MethodGet, "/api/users/42", nil)
	runHTTPBenchmark(b, engine, func() *http.Request { return req }, func(rec *httptest.ResponseRecorder) {
		if rec.Code != http.StatusOK {
			b.Fatalf("status = %d", rec.Code)
		}
	})
}

func benchmarkWebGroupAndRouteRepresentative(b *testing.B) {
	adapter := New()
	group := adapter.Router().Group("/api",
		func(next web.Handler) web.Handler {
			return func(c web.Context) error {
				c.Set("group", "1")
				return next(c)
			}
		},
		func(next web.Handler) web.Handler {
			return func(c web.Context) error {
				c.SetHeader("X-Group", "1")
				return next(c)
			}
		},
	)
	group.GET("/users/:id", func(c web.Context) error {
		if c.Get("group") == nil {
			b.Fatal("missing group middleware state")
		}
		if c.Get("route") == nil {
			b.Fatal("missing route middleware state")
		}
		return c.JSON(http.StatusOK, map[string]any{
			"id":     c.Param("id"),
			"method": c.Method(),
		})
	}, func(next web.Handler) web.Handler {
		return func(c web.Context) error {
			c.Set("route", "1")
			return next(c)
		}
	})

	req := httptest.NewRequest(http.MethodGet, "/api/users/42", nil)
	runHTTPBenchmark(b, adapter.Echo(), func() *http.Request { return req }, func(rec *httptest.ResponseRecorder) {
		if rec.Code != http.StatusOK {
			b.Fatalf("status = %d", rec.Code)
		}
	})
}

func benchmarkEchoCompressRepresentative(b *testing.B) {
	engine := echo.New()
	engine.Use(echomiddleware.Gzip())
	engine.GET("/gzip", func(c *echo.Context) error {
		return c.String(http.StatusOK, strings.Repeat("x", 1024))
	})

	req := httptest.NewRequest(http.MethodGet, "/gzip", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	runHTTPBenchmark(b, engine, func() *http.Request { return req }, func(rec *httptest.ResponseRecorder) {
		if rec.Code != http.StatusOK {
			b.Fatalf("status = %d", rec.Code)
		}
		reader, err := gzip.NewReader(bytes.NewReader(rec.Body.Bytes()))
		if err != nil {
			b.Fatalf("gzip reader: %v", err)
		}
		body, err := io.ReadAll(reader)
		_ = reader.Close()
		if err != nil {
			b.Fatalf("read gzip body: %v", err)
		}
		if len(body) != 1024 {
			b.Fatalf("body len = %d", len(body))
		}
	})
}

func benchmarkWebCompressRepresentative(b *testing.B) {
	adapter := New()
	adapter.Router().Use(webmiddleware.Compress())
	adapter.Router().GET("/gzip", func(c web.Context) error {
		return c.Text(http.StatusOK, strings.Repeat("x", 1024))
	})

	req := httptest.NewRequest(http.MethodGet, "/gzip", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	runHTTPBenchmark(b, adapter.Echo(), func() *http.Request { return req }, func(rec *httptest.ResponseRecorder) {
		if rec.Code != http.StatusOK {
			b.Fatalf("status = %d", rec.Code)
		}
		reader, err := gzip.NewReader(bytes.NewReader(rec.Body.Bytes()))
		if err != nil {
			b.Fatalf("gzip reader: %v", err)
		}
		body, err := io.ReadAll(reader)
		_ = reader.Close()
		if err != nil {
			b.Fatalf("read gzip body: %v", err)
		}
		if len(body) != 1024 {
			b.Fatalf("body len = %d", len(body))
		}
	})
}

func runHTTPBenchmark(b *testing.B, handler http.Handler, newRequest func() *http.Request, assert func(*httptest.ResponseRecorder)) {
	b.ReportAllocs()
	b.ResetTimer()
	start := time.Now()
	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, newRequest())
		assert(rec)
	}
	reportRequestsPerSecond(b, start)
}

func reportRequestsPerSecond(b *testing.B, start time.Time) {
	elapsed := time.Since(start)
	if elapsed <= 0 {
		return
	}
	b.ReportMetric(float64(b.N)/elapsed.Seconds(), "req/s")
}
