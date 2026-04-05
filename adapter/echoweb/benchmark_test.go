package echoweb

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/goforj/web"
	"github.com/goforj/web/webmiddleware"
	"github.com/gorilla/websocket"
	echo "github.com/labstack/echo/v4"
	echomiddleware "github.com/labstack/echo/v4/middleware"
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

func BenchmarkEchoCompress(b *testing.B) {
	engine := echo.New()
	engine.Use(echomiddleware.Gzip())
	engine.GET("/gzip", func(c echo.Context) error {
		return c.String(http.StatusOK, strings.Repeat("x", 1024))
	})

	req := httptest.NewRequest(http.MethodGet, "/gzip", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, req)
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
	}
}

func BenchmarkWebCompress(b *testing.B) {
	adapter := New()
	adapter.Router().Use(webmiddleware.Compress())
	adapter.Router().GET("/gzip", func(r web.Context) error {
		return r.Text(http.StatusOK, strings.Repeat("x", 1024))
	})

	req := httptest.NewRequest(http.MethodGet, "/gzip", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		adapter.Echo().ServeHTTP(rec, req)
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
	}
}

func BenchmarkEchoBodyDump(b *testing.B) {
	engine := echo.New()
	engine.Use(echomiddleware.BodyDump(func(c echo.Context, reqBody, resBody []byte) {}))
	engine.POST("/dump", func(c echo.Context) error {
		payload, err := io.ReadAll(c.Request().Body)
		if err != nil {
			return err
		}
		return c.Blob(http.StatusOK, "application/octet-stream", payload)
	})

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest(http.MethodPost, "/dump", strings.NewReader(strings.Repeat("a", 256)))
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			b.Fatalf("status = %d", rec.Code)
		}
	}
}

func BenchmarkWebBodyDump(b *testing.B) {
	adapter := New()
	adapter.Router().Use(webmiddleware.BodyDump(func(r web.Context, reqBody, resBody []byte) {}))
	adapter.Router().POST("/dump", func(r web.Context) error {
		payload, err := io.ReadAll(r.Request().Body)
		if err != nil {
			return err
		}
		return r.Blob(http.StatusOK, "application/octet-stream", payload)
	})

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest(http.MethodPost, "/dump", strings.NewReader(strings.Repeat("a", 256)))
		rec := httptest.NewRecorder()
		adapter.Echo().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			b.Fatalf("status = %d", rec.Code)
		}
	}
}

func BenchmarkEchoWebSocketJSON(b *testing.B) {
	engine := echo.New()
	engine.GET("/ws", func(c echo.Context) error {
		upgrader := websocket.Upgrader{}
		conn, err := upgrader.Upgrade(c.Response(), c.Request(), nil)
		if err != nil {
			return err
		}
		defer conn.Close()

		var payload map[string]any
		if err := conn.ReadJSON(&payload); err != nil {
			return err
		}
		payload["ok"] = true
		return conn.WriteJSON(payload)
	})

	server := httptest.NewServer(engine)
	defer server.Close()
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws"

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
		if err != nil {
			b.Fatalf("dial: %v", err)
		}
		if err := conn.WriteJSON(map[string]any{"kind": "ping"}); err != nil {
			b.Fatalf("write json: %v", err)
		}
		var response map[string]any
		if err := conn.ReadJSON(&response); err != nil {
			b.Fatalf("read json: %v", err)
		}
		_ = conn.Close()
	}
}

func BenchmarkWebWebSocketJSON(b *testing.B) {
	adapter := New()
	adapter.Router().GETWS("/ws", func(r web.Context, conn web.WebSocketConn) error {
		var payload map[string]any
		if err := conn.ReadJSON(&payload); err != nil {
			return err
		}
		payload["ok"] = true
		return conn.WriteJSON(payload)
	})

	server := httptest.NewServer(adapter.Echo())
	defer server.Close()
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws"

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
		if err != nil {
			b.Fatalf("dial: %v", err)
		}
		if err := conn.WriteJSON(map[string]any{"kind": "ping"}); err != nil {
			b.Fatalf("write json: %v", err)
		}
		var response map[string]any
		if err := conn.ReadJSON(&response); err != nil {
			b.Fatalf("read json: %v", err)
		}
		_ = conn.Close()
	}
}

func BenchmarkEchoWebSocketJSONPersistent(b *testing.B) {
	engine := echo.New()
	engine.GET("/ws", func(c echo.Context) error {
		upgrader := websocket.Upgrader{}
		conn, err := upgrader.Upgrade(c.Response(), c.Request(), nil)
		if err != nil {
			return err
		}
		defer conn.Close()

		for {
			var payload map[string]any
			if err := conn.ReadJSON(&payload); err != nil {
				return nil
			}
			payload["ok"] = true
			if err := conn.WriteJSON(payload); err != nil {
				return err
			}
		}
	})

	server := httptest.NewServer(engine)
	defer server.Close()
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		b.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := conn.WriteJSON(map[string]any{"kind": "ping"}); err != nil {
			b.Fatalf("write json: %v", err)
		}
		var response map[string]any
		if err := conn.ReadJSON(&response); err != nil {
			b.Fatalf("read json: %v", err)
		}
	}
}

func BenchmarkWebWebSocketJSONPersistent(b *testing.B) {
	adapter := New()
	adapter.Router().GETWS("/ws", func(r web.Context, conn web.WebSocketConn) error {
		for {
			var payload map[string]any
			if err := conn.ReadJSON(&payload); err != nil {
				return nil
			}
			payload["ok"] = true
			if err := conn.WriteJSON(payload); err != nil {
				return err
			}
		}
	})

	server := httptest.NewServer(adapter.Echo())
	defer server.Close()
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		b.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := conn.WriteJSON(map[string]any{"kind": "ping"}); err != nil {
			b.Fatalf("write json: %v", err)
		}
		var response map[string]any
		if err := conn.ReadJSON(&response); err != nil {
			b.Fatalf("read json: %v", err)
		}
	}
}
