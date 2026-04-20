package echoweb

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/goforj/web"
	"github.com/gorilla/websocket"
	echo "github.com/labstack/echo/v5"
)

func TestRouterRegistersRouteAndContext(t *testing.T) {
	adapter := New()
	adapter.Router().GET("/users/:id", func(r web.Context) error {
		if got := r.Param("id"); got != "42" {
			t.Fatalf("param id = %q", got)
		}
		if got := r.Path(); got != "/users/:id" {
			t.Fatalf("path = %q", got)
		}
		if got := r.URI(); got != "/users/42" {
			t.Fatalf("uri = %q", got)
		}
		if got := r.Scheme(); got != "http" {
			t.Fatalf("scheme = %q", got)
		}
		if got := r.Host(); got != "example.com" {
			t.Fatalf("host = %q", got)
		}
		if got := r.Request(); got == nil {
			t.Fatal("request is nil")
		}
		if got := r.ResponseWriter(); got == nil {
			t.Fatal("response writer is nil")
		}
		if got := r.Response(); got == nil {
			t.Fatal("response is nil")
		}
		if got := r.Response().Writer(); got == nil {
			t.Fatal("response writer is nil")
		}
		if got := r.Response().Committed(); got {
			t.Fatal("response should not be committed before write")
		}
		return r.JSON(http.StatusOK, map[string]any{
			"id":     r.Param("id"),
			"method": r.Method(),
		})
	})

	req := httptest.NewRequest(http.MethodGet, "/users/42", nil)
	req.Host = "example.com"
	rec := httptest.NewRecorder()
	adapter.Echo().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); body != "{\"id\":\"42\",\"method\":\"GET\"}\n" {
		t.Fatalf("body = %q", body)
	}
}

func TestContextResponseExposesStatusSizeAndCommitted(t *testing.T) {
	adapter := New()
	adapter.Router().GET("/response", func(r web.Context) error {
		if err := r.Text(http.StatusAccepted, "ok"); err != nil {
			t.Fatalf("Text: %v", err)
		}
		if got := r.Response().StatusCode(); got != http.StatusAccepted {
			t.Fatalf("status = %d", got)
		}
		if got := r.Response().Size(); got != 2 {
			t.Fatalf("size = %d", got)
		}
		if !r.Response().Committed() {
			t.Fatal("response should be committed after write")
		}
		if got := r.StatusCode(); got != http.StatusAccepted {
			t.Fatalf("context status = %d", got)
		}
		return nil
	})

	req := httptest.NewRequest(http.MethodGet, "/response", nil)
	rec := httptest.NewRecorder()
	adapter.Echo().ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); body != "ok" {
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

	group.GET("/ping", func(r web.Context) error {
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

func TestRouterUseAppliesMiddleware(t *testing.T) {
	adapter := New()
	router := adapter.Router()
	router.Use(func(next web.Handler) web.Handler {
		return func(r web.Context) error {
			r.SetHeader("X-Web-MW", "on")
			return next(r)
		}
	})

	router.GET("/mw", func(r web.Context) error {
		return r.NoContent(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/mw", nil)
	rec := httptest.NewRecorder()
	adapter.Echo().ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := rec.Header().Get("X-Web-MW"); got != "on" {
		t.Fatalf("X-Web-MW = %q", got)
	}
}

func TestRouterPreRunsBeforeRouting(t *testing.T) {
	adapter := New()
	router := adapter.Router()
	router.Pre(func(next web.Handler) web.Handler {
		return func(r web.Context) error {
			req := r.Request()
			req.URL.Path = "/ready"
			req.RequestURI = "/ready"
			r.SetRequest(req)
			return next(r)
		}
	})
	router.GET("/ready", func(r web.Context) error {
		return r.Text(http.StatusOK, "pre")
	})

	req := httptest.NewRequest(http.MethodGet, "/pending", nil)
	rec := httptest.NewRecorder()
	adapter.Echo().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); body != "pre" {
		t.Fatalf("body = %q", body)
	}
}

func TestContextSupportsHeadersAndBlobResponses(t *testing.T) {
	adapter := New()
	adapter.Router().GET("/blob", func(r web.Context) error {
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
	adapter.Router().GET("/file", func(r web.Context) error {
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
	adapter.Router().GET("/cookie", func(r web.Context) error {
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
	adapter.Router().GETWS("/ws", func(r web.Context, conn web.WebSocketConn) error {
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

func TestRouterSupportsHeadAndMatch(t *testing.T) {
	adapter := New()
	router := adapter.Router()
	router.HEAD("/head", func(r web.Context) error {
		r.SetHeader("X-Method", r.Method())
		return r.NoContent(http.StatusNoContent)
	})
	router.Match([]string{http.MethodOptions, http.MethodTrace}, "/match", func(r web.Context) error {
		return r.Text(http.StatusOK, r.Method())
	})

	headReq := httptest.NewRequest(http.MethodHead, "/head", nil)
	headRec := httptest.NewRecorder()
	adapter.Echo().ServeHTTP(headRec, headReq)
	if headRec.Code != http.StatusNoContent {
		t.Fatalf("head status = %d", headRec.Code)
	}
	if got := headRec.Header().Get("X-Method"); got != http.MethodHead {
		t.Fatalf("X-Method = %q", got)
	}

	optionsReq := httptest.NewRequest(http.MethodOptions, "/match", nil)
	optionsRec := httptest.NewRecorder()
	adapter.Echo().ServeHTTP(optionsRec, optionsReq)
	if optionsRec.Code != http.StatusOK {
		t.Fatalf("options status = %d body=%s", optionsRec.Code, optionsRec.Body.String())
	}
	if body := optionsRec.Body.String(); body != http.MethodOptions {
		t.Fatalf("options body = %q", body)
	}

	traceReq := httptest.NewRequest(http.MethodTrace, "/match", nil)
	traceRec := httptest.NewRecorder()
	adapter.Echo().ServeHTTP(traceRec, traceReq)
	if traceRec.Code != http.StatusOK {
		t.Fatalf("trace status = %d body=%s", traceRec.Code, traceRec.Body.String())
	}
	if body := traceRec.Body.String(); body != http.MethodTrace {
		t.Fatalf("trace body = %q", body)
	}
}

func TestWrapAndNilAccessors(t *testing.T) {
	adapter := Wrap(nil)
	if adapter.Echo() == nil {
		t.Fatal("Wrap(nil) should create an echo engine")
	}
	if adapter.Router() == nil {
		t.Fatal("Wrap(nil) should create a router")
	}

	engine := echo.New()
	wrapped := Wrap(engine)
	if got := wrapped.Echo(); got != engine {
		t.Fatal("Wrap(existing) should keep the provided engine")
	}

	var nilAdapter *Adapter
	if nilAdapter.Echo() != nil {
		t.Fatal("nil adapter Echo() should return nil")
	}
	if nilAdapter.Router() != nil {
		t.Fatal("nil adapter Router() should return nil")
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/missing", nil)
	nilAdapter.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("nil adapter status = %d", rec.Code)
	}
}

func TestContextAdapterHelpers(t *testing.T) {
	adapter := New()
	adapter.Router().POST("/helpers/:id", func(r web.Context) error {
		if got, want := r.Context().Value("trace"), "trace-1"; got != want {
			t.Fatalf("Context().Value(trace) = %#v, want %q", got, want)
		}
		if got, want := r.Query("expand"), "roles"; got != want {
			t.Fatalf("Query(expand) = %q, want %q", got, want)
		}
		if got, want := r.Header("X-Mode"), "fast"; got != want {
			t.Fatalf("Header(X-Mode) = %q, want %q", got, want)
		}

		type payload struct {
			Name string `json:"name"`
		}
		var body payload
		if err := r.Bind(&body); err != nil {
			t.Fatalf("Bind(): %v", err)
		}
		if got, want := body.Name, "demo"; got != want {
			t.Fatalf("bound.Name = %q, want %q", got, want)
		}

		r.AddHeader("X-Added", "one")
		r.AddHeader("X-Added", "two")
		r.SetHeader("X-Set", "ok")

		if native, ok := UnwrapContext(r); !ok || native == nil {
			t.Fatal("UnwrapContext() failed")
		}
		if _, ok := r.Native().(*echo.Context); !ok {
			t.Fatalf("Native() type = %T", r.Native())
		}
		return r.HTML(http.StatusCreated, "<strong>"+body.Name+"</strong>")
	})

	req := httptest.NewRequest(http.MethodPost, "/helpers/42?expand=roles", bytes.NewBufferString(`{"name":"demo"}`))
	req = req.WithContext(context.WithValue(req.Context(), "trace", "trace-1"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Mode", "fast")
	rec := httptest.NewRecorder()
	adapter.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Values("X-Added"); len(got) != 2 {
		t.Fatalf("X-Added values = %#v", got)
	}
	if got, want := rec.Header().Get("X-Set"), "ok"; got != want {
		t.Fatalf("X-Set = %q, want %q", got, want)
	}
	if got := rec.Header().Get("Content-Type"); !strings.Contains(got, "text/html") {
		t.Fatalf("Content-Type = %q", got)
	}
}

func TestContextAdapterDisableReuse(t *testing.T) {
	adapted := &contextAdapter{reusable: true}
	adapted.DisableReuse()
	if adapted.reusable {
		t.Fatal("DisableReuse() should disable pooling")
	}
}

func TestContextAdapterRedirectAndResponseWriters(t *testing.T) {
	adapter := New()
	adapter.Router().GET("/redirect", func(r web.Context) error {
		current := r.ResponseWriter()
		r.SetResponseWriter(current)
		if got := r.Response().Header(); got == nil {
			t.Fatal("Response().Header() returned nil")
		}
		if got := r.Response().Native(); got == nil {
			t.Fatal("Response().Native() returned nil")
		}
		r.Response().SetWriter(current)
		return r.Redirect(http.StatusTemporaryRedirect, "/target")
	})

	req := httptest.NewRequest(http.MethodGet, "/redirect", nil)
	rec := httptest.NewRecorder()
	adapter.ServeHTTP(rec, req)

	if rec.Code != http.StatusTemporaryRedirect {
		t.Fatalf("status = %d", rec.Code)
	}
	if got, want := rec.Header().Get("Location"), "/target"; got != want {
		t.Fatalf("Location = %q, want %q", got, want)
	}
}

func TestRouterCoversHandleVariantsAndAny(t *testing.T) {
	adapter := New()
	router := adapter.Router()

	handler := func(r web.Context) error {
		return r.JSON(http.StatusOK, map[string]string{"method": r.Method()})
	}
	register := func(method, path string) {
		t.Helper()
		if err := router.Handle(method, path, handler); err != nil {
			t.Fatalf("Handle(%s): %v", method, err)
		}
	}

	register(http.MethodConnect, "/connect")
	register(http.MethodDelete, "/delete")
	register(http.MethodGet, "/get")
	register(http.MethodHead, "/head")
	register(http.MethodOptions, "/options")
	register(http.MethodPatch, "/patch")
	register(http.MethodPost, "/post")
	register(http.MethodPut, "/put")
	register(http.MethodTrace, "/trace")
	router.Any("/any", handler)

	for _, tc := range []struct {
		method string
		path   string
	}{
		{http.MethodConnect, "/connect"},
		{http.MethodDelete, "/delete"},
		{http.MethodGet, "/get"},
		{http.MethodHead, "/head"},
		{http.MethodOptions, "/options"},
		{http.MethodPatch, "/patch"},
		{http.MethodPost, "/post"},
		{http.MethodPut, "/put"},
		{http.MethodTrace, "/trace"},
		{http.MethodPost, "/any"},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		rec := httptest.NewRecorder()
		adapter.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s %s status = %d body=%s", tc.method, tc.path, rec.Code, rec.Body.String())
		}
		if tc.method != http.MethodHead {
			var payload map[string]string
			if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
				t.Fatalf("%s %s json: %v", tc.method, tc.path, err)
			}
			if got, want := payload["method"], tc.method; got != want {
				t.Fatalf("%s %s method = %q, want %q", tc.method, tc.path, got, want)
			}
		}
	}

	if err := router.Handle("BREW", "/coffee", handler); err == nil {
		t.Fatal("Handle() should reject unsupported methods")
	}
}

func TestWebSocketUnwrapHelpers(t *testing.T) {
	if conn, ok := UnwrapWebSocketConn(nil); ok || conn != nil {
		t.Fatalf("UnwrapWebSocketConn(nil) = (%v, %v)", conn, ok)
	}

	adapter := New()
	adapter.Router().GETWS("/ws-native", func(r web.Context, conn web.WebSocketConn) error {
		if _, ok := conn.Native().(*websocket.Conn); !ok {
			t.Fatalf("Native() type = %T", conn.Native())
		}
		native, ok := UnwrapWebSocketConn(conn)
		if !ok || native == nil {
			t.Fatal("UnwrapWebSocketConn() failed")
		}
		return conn.Close()
	})

	server := httptest.NewServer(adapter.Echo())
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws-native"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
}
