package webtest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestContextExposesPathParamsQueryAndJSON(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/users/42?expand=1", http.NoBody)
	req.Host = "example.com"
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-For", "203.0.113.9, 10.0.0.1")
	rec := httptest.NewRecorder()

	ctx := NewContext(req, rec, "/users/:id", PathParams{"id": "42"})
	if got := ctx.Param("id"); got != "42" {
		t.Fatalf("Param(id) = %q", got)
	}
	if got := ctx.Query("expand"); got != "1" {
		t.Fatalf("Query(expand) = %q", got)
	}
	if got := ctx.Path(); got != "/users/:id" {
		t.Fatalf("Path() = %q", got)
	}
	if got := ctx.URI(); got != "/users/42?expand=1" {
		t.Fatalf("URI() = %q", got)
	}
	if got := ctx.Scheme(); got != "https" {
		t.Fatalf("Scheme() = %q", got)
	}
	if got := ctx.RealIP(); got != "203.0.113.9" {
		t.Fatalf("RealIP() = %q", got)
	}

	if err := ctx.JSON(http.StatusCreated, map[string]any{"ok": true}); err != nil {
		t.Fatalf("JSON() error = %v", err)
	}
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d", rec.Code)
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}
	if ok, _ := payload["ok"].(bool); !ok {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestContextSupportsStateAndResponseMetadata(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	rec := httptest.NewRecorder()
	ctx := NewContext(req, rec, "/ping", nil)

	ctx.Set("trace", "abc")
	if got := ctx.Get("trace"); got != "abc" {
		t.Fatalf("Get(trace) = %#v", got)
	}
	if err := ctx.Text(http.StatusAccepted, "pong"); err != nil {
		t.Fatalf("Text() error = %v", err)
	}
	if got := ctx.Response().StatusCode(); got != http.StatusAccepted {
		t.Fatalf("Response().StatusCode() = %d", got)
	}
	if got := ctx.StatusCode(); got != http.StatusAccepted {
		t.Fatalf("StatusCode() = %d", got)
	}
	if got := ctx.Response().Size(); got != int64(len("pong")) {
		t.Fatalf("Response().Size() = %d", got)
	}
	if !ctx.Response().Committed() {
		t.Fatal("expected response to be committed")
	}
}
