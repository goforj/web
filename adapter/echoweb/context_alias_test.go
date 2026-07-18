package echoweb

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/goforj/web"
	echo "github.com/labstack/echo/v5"
)

// TestContextAdapterBindContextPreservesRawRequest verifies lazy context rebinding retains raw request ownership.
func TestContextAdapterBindContextPreservesRawRequest(t *testing.T) {
	type contextKey string

	adapter := New()
	adapter.Router().GET("/context", func(r web.Context) error {
		original := r.Request()
		first := context.WithValue(r.Context(), contextKey("bound"), "first")
		web.BindContext(r, first)

		if got := r.Context().Value(contextKey("bound")); got != "first" {
			t.Fatalf("first Context value = %#v", got)
		}
		native, ok := UnwrapContext(r)
		if !ok || native.Request() != original {
			t.Fatal("BindContext eagerly replaced Echo's raw request")
		}
		if got := r.Request().Context().Value(contextKey("bound")); got != "first" {
			t.Fatalf("first Request context value = %#v", got)
		}
		if got := web.RawRequest(r); got != original {
			t.Fatal("first RawRequest did not preserve the original request")
		}
		if got := r.Request(); got == original {
			t.Fatal("bound Request reused the raw request pointer")
		}

		second := context.WithValue(r.Context(), contextKey("bound"), "second")
		web.BindContext(r, second)
		if got := r.Context().Value(contextKey("bound")); got != "second" {
			t.Fatalf("second Context value = %#v", got)
		}
		if got := web.RawRequest(r); got != original {
			t.Fatal("second RawRequest did not preserve the original request")
		}

		replacement := httptest.NewRequest(http.MethodGet, "/replacement", nil)
		r.SetRequest(replacement)
		if got := web.RawRequest(r); got != replacement {
			t.Fatal("RawRequest did not follow SetRequest")
		}
		if got := r.Request().Context().Value(contextKey("bound")); got != "second" {
			t.Fatalf("replacement Request context value = %#v", got)
		}

		setter := r.(interface{ SetContext(context.Context) })
		setter.SetContext(nil)
		if got := r.Request(); got != replacement {
			t.Fatal("clearing the bound context did not restore the raw request")
		}
		if got := web.RawRequest(r); got != replacement {
			t.Fatal("clearing the bound context changed RawRequest")
		}
		return r.NoContent(http.StatusNoContent)
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/context", nil)
	adapter.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d", recorder.Code)
	}
}

// TestContextAdapterStateResetsWithEchoContext verifies Echo pooling cannot leak adapter-only state.
func TestContextAdapterStateResetsWithEchoContext(t *testing.T) {
	type contextKey string

	engine := echo.New()
	firstRequest := httptest.NewRequest(http.MethodGet, "/first", nil)
	native := engine.NewContext(firstRequest, httptest.NewRecorder())
	adapted := acquireContextAdapter(native)
	web.BindContext(adapted, context.WithValue(context.Background(), contextKey("request"), "first"))
	adapted.SetAppSourceName("http")
	if got := adapted.AppSourceName(); got != "http" {
		t.Fatalf("initial source name = %q, want http", got)
	}

	secondRequest := httptest.NewRequest(http.MethodGet, "/second", nil)
	native.Reset(secondRequest, httptest.NewRecorder())
	if got := adapted.Context().Value(contextKey("request")); got != nil {
		t.Fatalf("reset Context value = %#v", got)
	}
	if got := adapted.AppSourceName(); got != "" {
		t.Fatalf("reset source name = %q", got)
	}
	if got := web.RawRequest(adapted); got != secondRequest {
		t.Fatal("reset RawRequest did not use the new Echo request")
	}
}

// TestContextAdapterResponseViewTracksWriterReplacement verifies response views follow the active native writer.
func TestContextAdapterResponseViewTracksWriterReplacement(t *testing.T) {
	engine := echo.New()
	native := engine.NewContext(
		httptest.NewRequest(http.MethodGet, "/response", nil),
		httptest.NewRecorder(),
	)
	adapted := acquireContextAdapter(native)
	response := adapted.Response()

	replacement := httptest.NewRecorder()
	adapted.SetResponseWriter(echo.NewResponse(replacement, engine.Logger))
	if got := response.Writer(); got != native.Response() {
		t.Fatal("response view did not track SetResponseWriter")
	}
	if err := adapted.Text(http.StatusAccepted, "ok"); err != nil {
		t.Fatalf("Text: %v", err)
	}
	if got := response.StatusCode(); got != http.StatusAccepted {
		t.Fatalf("status = %d", got)
	}
	if got := response.Size(); got != 2 {
		t.Fatalf("size = %d", got)
	}
	if !response.Committed() {
		t.Fatal("response was not committed")
	}
}
