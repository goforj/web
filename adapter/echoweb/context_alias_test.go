package echoweb

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/goforj/web"
	"github.com/goforj/web/webmiddleware"
	echo "github.com/labstack/echo/v5"
)

// contextAliasJSONSerializer records configured serializer use and can inject a serialization failure.
type contextAliasJSONSerializer struct {
	called bool
	body   string
	err    error
}

// contextAliasFallbackContext exposes an adapter without shadowing its Context method through an interface field name.
type contextAliasFallbackContext struct {
	*contextAdapter
}

// contextAliasNilDetacher provides a valid fallback context while simulating an adapter that no longer owns a request.
type contextAliasNilDetacher struct {
	contextAliasFallbackContext
}

// DetachedContext delegates to a nil adapter to exercise timeout middleware's unsupported-detachment fallback.
func (c contextAliasNilDetacher) DetachedContext() (web.Context, func()) {
	var adapted *contextAdapter
	return adapted.DetachedContext()
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

// TestContextAdapterNilReceiverGuards verifies optional lifecycle views remain safe when no request is owned.
func TestContextAdapterNilReceiverGuards(t *testing.T) {
	var adapted *contextAdapter
	if state := adapted.adapterState(); state.bound || state.boundContext != nil || state.request != nil || state.sourceName != "" {
		t.Fatalf("adapter state = %#v, want empty", state)
	}
	if state := adapted.timeoutState(); state != nil {
		t.Fatalf("timeout state = %#v, want nil", state)
	}
	if request := adapted.RawRequest(); request != nil {
		t.Fatalf("raw request = %#v, want nil", request)
	}

	detached, commit := adapted.DetachedContext()
	if detached != nil {
		t.Fatalf("detached context = %#v, want nil", detached)
	}
	commit()
	engine := echo.New()
	source := engine.NewContext(
		httptest.NewRequest(http.MethodGet, "/", nil),
		httptest.NewRecorder(),
	)
	acquireContextAdapter(source).commitTo(nil)

	var response *responseAdapter
	if response.StatusCode() != 0 || response.Size() != 0 || response.Committed() {
		t.Fatal("nil response unexpectedly exposed Echo response bookkeeping")
	}
	if native := response.Native(); native != nil {
		t.Fatalf("native response = %#v, want nil", native)
	}
	if native, ok := UnwrapContext(nil); ok || native != nil {
		t.Fatalf("UnwrapContext(nil) = (%#v, %v), want (nil, false)", native, ok)
	}
}

// TestContextAdapterNilDetachmentFallsBackInTimeout verifies a typed nil cannot escape into middleware execution.
func TestContextAdapterNilDetachmentFallsBackInTimeout(t *testing.T) {
	engine := echo.New()
	recorder := httptest.NewRecorder()
	native := engine.NewContext(httptest.NewRequest(http.MethodGet, "/timeout", nil), recorder)
	ctx := contextAliasNilDetacher{
		contextAliasFallbackContext: contextAliasFallbackContext{
			contextAdapter: acquireContextAdapter(native),
		},
	}
	middleware := webmiddleware.TimeoutWithConfig(webmiddleware.TimeoutConfig{
		Skipper: func(web.Context) bool { return false },
		Timeout: time.Second,
	})
	handler := middleware(func(ctx web.Context) error {
		return ctx.Text(http.StatusAccepted, "fallback")
	})
	if err := handler(ctx); err != nil {
		t.Fatalf("timeout handler: %v", err)
	}
	if recorder.Code != http.StatusAccepted || recorder.Body.String() != "fallback" {
		t.Fatalf("response = (%d, %q), want (202, fallback)", recorder.Code, recorder.Body.String())
	}
}

// TestContextAdapterTimeoutBindingLifecycle verifies direct timeout binding creates and clears adapter ownership state.
func TestContextAdapterTimeoutBindingLifecycle(t *testing.T) {
	type contextKey string

	engine := echo.New()
	healthyContext := context.WithValue(context.Background(), contextKey("phase"), "healthy")
	healthyRequest := httptest.NewRequest(http.MethodGet, "/timeout", nil).WithContext(healthyContext)
	native := engine.NewContext(healthyRequest, httptest.NewRecorder())
	adapted := acquireContextAdapter(native)

	adapted.RestoreTimeoutRequest()
	if native.Request() != healthyRequest {
		t.Fatal("restoring an unbound timeout changed the request")
	}

	timeoutContext, cancel := context.WithCancel(healthyContext)
	t.Cleanup(cancel)
	timeoutRequest := healthyRequest.WithContext(timeoutContext)
	adapted.BindTimeoutRequest(timeoutRequest, timeoutContext)
	if state := adapted.timeoutState(); state == nil || !state.bound {
		t.Fatalf("timeout state = %#v, want an active binding", state)
	}
	if adapted.Request() != timeoutRequest || adapted.Context() != timeoutContext {
		t.Fatal("timeout binding did not expose the derived request context")
	}

	adapted.RestoreTimeoutRequest()
	if got := adapted.Context().Value(contextKey("phase")); got != "healthy" {
		t.Fatalf("restored context value = %#v, want healthy", got)
	}
	if adapted.Request().Context() != healthyContext {
		t.Fatal("restored request did not recover its healthy context")
	}
	if state := adapted.timeoutState(); state == nil || state.bound {
		t.Fatalf("restored timeout state = %#v, want inactive ownership", state)
	}

	adapted.RestoreTimeoutRequest()
}

// TestContextAdapterDetachedContextSupportsEngineLessEchoContexts verifies Echo's documented test-context form remains usable.
func TestContextAdapterDetachedContextSupportsEngineLessEchoContexts(t *testing.T) {
	native := echo.NewContext(
		httptest.NewRequest(http.MethodGet, "/detached", nil),
		httptest.NewRecorder(),
	)
	adapted := acquireContextAdapter(native)
	detached, commit := adapted.DetachedContext()
	detachedNative, ok := UnwrapContext(detached)
	if !ok || detachedNative.Echo() == nil {
		t.Fatal("detached context did not acquire an independent Echo engine")
	}
	detached.Set("result", "committed")
	commit()
	if got := adapted.Get("result"); got != "committed" {
		t.Fatalf("committed state = %#v, want committed", got)
	}
}

// TestContextAdapterDetachedFallbackCloses verifies released work cannot consult the original Echo request store.
func TestContextAdapterDetachedFallbackCloses(t *testing.T) {
	engine := echo.New()
	native := engine.NewContext(
		httptest.NewRequest(http.MethodGet, "/detached", nil),
		httptest.NewRecorder(),
	)
	native.Set("native-before", "visible")
	native.Set("native-after", "hidden")
	detachedContext, _ := acquireContextAdapter(native).DetachedContext()
	detached := detachedContext.(*contextAdapter)
	if got := detached.Get("native-before"); got != "visible" {
		t.Fatalf("active fallback value = %#v, want visible", got)
	}

	detached.ReleaseDetachedContext()
	if got := detached.Get("native-after"); got != nil {
		t.Fatalf("released fallback value = %#v, want nil", got)
	}
}

// TestContextAdapterTracksMultipleUpdatedValues verifies detached commits preserve larger request stores and updates.
func TestContextAdapterTracksMultipleUpdatedValues(t *testing.T) {
	engine := echo.New()
	native := engine.NewContext(
		httptest.NewRequest(http.MethodGet, "/detached", nil),
		httptest.NewRecorder(),
	)
	adapted := acquireContextAdapter(native)
	adapted.Set("first", "one")
	adapted.Set("second", "two")
	adapted.Set("third", "three")
	adapted.Set("second", "updated")

	detached, commit := adapted.DetachedContext()
	detached.Set("third", "changed")
	detached.Set("fourth", "four")
	commit()

	for key, want := range map[string]string{
		"first":  "one",
		"second": "updated",
		"third":  "changed",
		"fourth": "four",
	} {
		if got := adapted.Get(key); got != want {
			t.Errorf("%s = %#v, want %q", key, got, want)
		}
	}

	trackedBefore := slices.Clone(native.Get(contextAdapterTrackedKeysKey).([]string))
	for _, key := range []string{contextAdapterStateKey, contextAdapterTrackedKeysKey, contextAdapterTimeoutStateKey} {
		trackContextAdapterKey(native, key)
	}
	if trackedAfter := native.Get(contextAdapterTrackedKeysKey).([]string); !slices.Equal(trackedBefore, trackedAfter) {
		t.Fatalf("reserved keys changed tracking metadata from %#v to %#v", trackedBefore, trackedAfter)
	}
}

// TestContextAdapterJSONSupportsRawResponseWriters verifies JSON retains Echo's fallback lifecycle for custom writers.
func TestContextAdapterJSONSupportsRawResponseWriters(t *testing.T) {
	engine := echo.New()
	recorder := httptest.NewRecorder()
	native := engine.NewContext(httptest.NewRequest(http.MethodGet, "/json", nil), recorder)
	native.SetResponse(recorder)
	adapted := acquireContextAdapter(native)
	response := adapted.Response()
	if response.StatusCode() != 0 || response.Size() != 0 || response.Committed() {
		t.Fatal("raw response writer unexpectedly exposed Echo response bookkeeping")
	}

	if err := adapted.JSON(http.StatusAccepted, map[string]bool{"ok": true}); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusAccepted)
	}
	if got := recorder.Body.String(); got != "{\"ok\":true}\n" {
		t.Fatalf("body = %q, want encoded JSON", got)
	}
	if got := recorder.Header().Get(echo.HeaderContentType); got != echo.MIMEApplicationJSON {
		t.Fatalf("Content-Type = %q, want %q", got, echo.MIMEApplicationJSON)
	}
}
