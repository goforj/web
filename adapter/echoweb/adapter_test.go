package echoweb

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/goforj/web"
	"github.com/gorilla/websocket"
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

func TestContextSupportsFileResponses(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "web-file-*")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	if _, err := file.WriteString("file-body"); err != nil {
		t.Fatalf("WriteString: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	adapter := New()
	adapter.Router().Get("/file", func(r web.Context) error {
		return r.File(file.Name())
	})

	req := httptest.NewRequest(http.MethodGet, "/file", nil)
	rec := httptest.NewRecorder()
	adapter.Echo().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if body := rec.Body.String(); body != "file-body" {
		t.Fatalf("body = %q", body)
	}
}

func TestContextSupportsCookiesAndRealIP(t *testing.T) {
	adapter := New()
	adapter.Router().Get("/cookie", func(r web.Context) error {
		r.SetCookie(&http.Cookie{
			Name:  "session",
			Value: "abc123",
			Path:  "/",
		})
		cookie, err := r.Cookie("incoming")
		if err != nil {
			t.Fatalf("Cookie: %v", err)
		}
		return r.JSON(http.StatusOK, map[string]any{
			"incoming": cookie.Value,
			"real_ip":  r.RealIP(),
		})
	})

	req := httptest.NewRequest(http.MethodGet, "/cookie", nil)
	req.AddCookie(&http.Cookie{Name: "incoming", Value: "present"})
	req.Header.Set("X-Forwarded-For", "203.0.113.10")
	rec := httptest.NewRecorder()
	adapter.Echo().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := rec.Header().Get("Set-Cookie"); !strings.Contains(got, "session=abc123") {
		t.Fatalf("Set-Cookie = %q", got)
	}
	if body := rec.Body.String(); body != "{\"incoming\":\"present\",\"real_ip\":\"203.0.113.10\"}\n" {
		t.Fatalf("body = %q", body)
	}
}

func TestRouterRegistersWebSocketRoute(t *testing.T) {
	adapter := New()
	adapter.Router().GetWS("/ws", func(r web.Context, conn web.WebSocketConn) error {
		var payload map[string]any
		if err := conn.ReadJSON(&payload); err != nil {
			t.Fatalf("ReadJSON: %v", err)
		}
		payload["path"] = r.Path()
		if err := conn.WriteJSON(payload); err != nil {
			t.Fatalf("WriteJSON: %v", err)
		}
		return conn.Close()
	})

	server := httptest.NewServer(adapter.Echo())
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	if err := conn.WriteJSON(map[string]any{"kind": "ping"}); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}

	var response map[string]any
	if err := conn.ReadJSON(&response); err != nil {
		t.Fatalf("ReadJSON: %v", err)
	}

	if got := response["kind"]; got != "ping" {
		t.Fatalf("kind = %#v", got)
	}
	if got := response["path"]; got != "/ws" {
		t.Fatalf("path = %#v", got)
	}
}
