package echoweb

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/goforj/web"
	echo "github.com/labstack/echo/v5"
)

func BenchmarkEchoLivePlainText(b *testing.B) {
	engine := echo.New()
	engine.GET("/plain", func(c *echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})
	runLiveLoopbackBenchmark(b, engine, "/plain")
}

func BenchmarkWebLivePlainText(b *testing.B) {
	adapter := New()
	adapter.Router().GET("/plain", func(r web.Context) error {
		return r.Text(http.StatusOK, "ok")
	})
	runLiveLoopbackBenchmark(b, adapter.Echo(), "/plain")
}

func BenchmarkEchoLiveMiddlewareChain(b *testing.B) {
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
				return echo.ErrInternalServerError
			}
			return next(c)
		}
	})
	engine.GET("/chain", func(c *echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})
	runLiveLoopbackBenchmark(b, engine, "/chain")
}

func BenchmarkWebLiveMiddlewareChain(b *testing.B) {
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
					return echo.ErrInternalServerError
				}
				return next(r)
			}
		},
	)
	adapter.Router().GET("/chain", func(r web.Context) error {
		return r.Text(http.StatusOK, "ok")
	})
	runLiveLoopbackBenchmark(b, adapter.Echo(), "/chain")
}

func runLiveLoopbackBenchmark(b *testing.B, handler http.Handler, path string) {
	server := httptest.NewServer(handler)
	defer server.Close()

	client := server.Client()
	transport, _ := client.Transport.(*http.Transport)
	if transport == nil {
		transport = http.DefaultTransport.(*http.Transport).Clone()
		client.Transport = transport
	} else {
		transport = transport.Clone()
		client.Transport = transport
	}
	transport.DisableCompression = true
	transport.MaxIdleConns = 256
	transport.MaxIdleConnsPerHost = 256
	transport.MaxConnsPerHost = 256

	url := server.URL + path
	var failures atomic.Int64

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			resp, err := client.Get(url)
			if err != nil {
				failures.Add(1)
				continue
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				failures.Add(1)
			}
		}
	})
	if got := failures.Load(); got != 0 {
		b.Fatalf("live loopback failures = %d", got)
	}
}
