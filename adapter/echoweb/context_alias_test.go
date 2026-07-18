package echoweb

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/goforj/web"
	echo "github.com/labstack/echo/v5"
)

// contextAliasJSONSerializer records configured serializer use and can inject a serialization failure.
type contextAliasJSONSerializer struct {
	called bool
	body   string
	err    error
}

// Serialize records the call before writing the configured body or returning an error.
func (s *contextAliasJSONSerializer) Serialize(c *echo.Context, _ any, _ string) error {
	s.called = true
	if s.err != nil {
		return s.err
	}
	_, err := c.Response().Write([]byte(s.body))
	return err
}

// Deserialize is unused by response tests and satisfies Echo's serializer contract.
func (s *contextAliasJSONSerializer) Deserialize(_ *echo.Context, _ any) error {
	return nil
}

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

// TestContextAdapterResponseFastPathsPreserveContentType verifies optimized writers retain caller-owned headers.
func TestContextAdapterResponseFastPathsPreserveContentType(t *testing.T) {
	tests := []struct {
		name  string
		write func(*contextAdapter) error
	}{
		{name: "text", write: func(c *contextAdapter) error { return c.Text(http.StatusAccepted, "text") }},
		{name: "html", write: func(c *contextAdapter) error { return c.HTML(http.StatusAccepted, "<b>html</b>") }},
		{name: "blob", write: func(c *contextAdapter) error {
			return c.Blob(http.StatusAccepted, "application/octet-stream", []byte("blob"))
		}},
		{name: "json", write: func(c *contextAdapter) error { return c.JSON(http.StatusAccepted, map[string]string{"kind": "json"}) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			engine := echo.New()
			recorder := httptest.NewRecorder()
			native := engine.NewContext(httptest.NewRequest(http.MethodGet, "/response", nil), recorder)
			native.Response().Header().Set(echo.HeaderContentType, "application/custom")
			if err := test.write(acquireContextAdapter(native)); err != nil {
				t.Fatal(err)
			}
			if got := recorder.Code; got != http.StatusAccepted {
				t.Fatalf("status = %d, want %d", got, http.StatusAccepted)
			}
			if got := recorder.Header().Get(echo.HeaderContentType); got != "application/custom" {
				t.Fatalf("Content-Type = %q, want application/custom", got)
			}
		})
	}
}

// TestContextAdapterResponseFastPathTracksBeforeReplacement verifies body writes follow Echo response swaps.
func TestContextAdapterResponseFastPathTracksBeforeReplacement(t *testing.T) {
	engine := echo.New()
	originalRecorder := httptest.NewRecorder()
	native := engine.NewContext(httptest.NewRequest(http.MethodGet, "/response", nil), originalRecorder)
	originalResponse, err := echo.UnwrapResponse(native.Response())
	if err != nil {
		t.Fatal(err)
	}
	replacementRecorder := httptest.NewRecorder()
	replacementResponse := echo.NewResponse(replacementRecorder, engine.Logger)
	originalResponse.Before(func() {
		native.SetResponse(replacementResponse)
	})

	if err := acquireContextAdapter(native).Text(http.StatusAccepted, "replacement"); err != nil {
		t.Fatal(err)
	}
	if originalRecorder.Code != http.StatusAccepted || originalRecorder.Body.Len() != 0 {
		t.Fatalf("original response = (%d, %q), want empty 202", originalRecorder.Code, originalRecorder.Body.String())
	}
	if replacementRecorder.Code != http.StatusOK || replacementRecorder.Body.String() != "replacement" {
		t.Fatalf("replacement response = (%d, %q), want (200, replacement)", replacementRecorder.Code, replacementRecorder.Body.String())
	}
}

// TestContextAdapterJSONUsesConfiguredSerializer verifies optimization does not replace Echo serialization semantics.
func TestContextAdapterJSONUsesConfiguredSerializer(t *testing.T) {
	engine := echo.New()
	serializer := &contextAliasJSONSerializer{body: "configured\n"}
	engine.JSONSerializer = serializer
	recorder := httptest.NewRecorder()
	native := engine.NewContext(httptest.NewRequest(http.MethodGet, "/json", nil), recorder)
	if err := acquireContextAdapter(native).JSON(http.StatusCreated, map[string]bool{"ok": true}); err != nil {
		t.Fatal(err)
	}
	if !serializer.called {
		t.Fatal("configured serializer was not called")
	}
	if got := recorder.Code; got != http.StatusCreated {
		t.Fatalf("status = %d, want %d", got, http.StatusCreated)
	}
	if got := recorder.Body.String(); got != "configured\n" {
		t.Fatalf("body = %q, want configured serializer output", got)
	}
	if got := recorder.Header().Get(echo.HeaderContentType); got != echo.MIMEApplicationJSON {
		t.Fatalf("Content-Type = %q, want %q", got, echo.MIMEApplicationJSON)
	}
}

// TestContextAdapterJSONDelaysStatusOnSerializerFailure verifies errors leave the response uncommitted.
func TestContextAdapterJSONDelaysStatusOnSerializerFailure(t *testing.T) {
	wantErr := errors.New("serialize failed")
	engine := echo.New()
	engine.JSONSerializer = &contextAliasJSONSerializer{err: wantErr}
	recorder := httptest.NewRecorder()
	native := engine.NewContext(httptest.NewRequest(http.MethodGet, "/json", nil), recorder)
	err := acquireContextAdapter(native).JSON(http.StatusCreated, map[string]bool{"ok": true})
	if !errors.Is(err, wantErr) {
		t.Fatalf("JSON error = %v, want %v", err, wantErr)
	}
	response, unwrapErr := echo.UnwrapResponse(native.Response())
	if unwrapErr != nil {
		t.Fatal(unwrapErr)
	}
	if response.Status != http.StatusCreated || response.Committed {
		t.Fatalf("response = status:%d committed:%t, want proposed uncommitted %d", response.Status, response.Committed, http.StatusCreated)
	}
	if recorder.Body.Len() != 0 {
		t.Fatalf("body = %q, want empty", recorder.Body.String())
	}
}
