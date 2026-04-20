package webtest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeResponseWriter struct{}

func (fakeResponseWriter) Header() http.Header       { return http.Header{} }
func (fakeResponseWriter) Write([]byte) (int, error) { return 0, nil }
func (fakeResponseWriter) WriteHeader(int)           {}

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

func TestContextCoversRequestResponseHelpers(t *testing.T) {
	t.Run("request accessors and mutation", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/submit?via=query", strings.NewReader(`{"name":"demo"}`))
		req = req.WithContext(context.WithValue(req.Context(), "trace", "ctx"))
		req.Host = "api.example.com"
		req.Header.Set("X-Trace", "trace-1")
		req.AddCookie(&http.Cookie{Name: "session", Value: "abc"})
		rec := httptest.NewRecorder()
		ctx := NewContext(req, rec, "/submit", nil)

		if got, want := ctx.Context().Value("trace"), "ctx"; got != want {
			t.Fatalf("Context().Value(trace) = %#v, want %q", got, want)
		}
		if got, want := ctx.Method(), http.MethodPost; got != want {
			t.Fatalf("Method() = %q, want %q", got, want)
		}
		if got, want := ctx.Host(), "api.example.com"; got != want {
			t.Fatalf("Host() = %q, want %q", got, want)
		}
		if got, want := ctx.Header("X-Trace"), "trace-1"; got != want {
			t.Fatalf("Header(X-Trace) = %q, want %q", got, want)
		}
		cookie, err := ctx.Cookie("session")
		if err != nil {
			t.Fatalf("Cookie(session): %v", err)
		}
		if got, want := cookie.Value, "abc"; got != want {
			t.Fatalf("cookie value = %q, want %q", got, want)
		}
		if got := ctx.Request(); got != req {
			t.Fatal("Request() did not return original request")
		}
		if got := ctx.ResponseWriter(); got != rec {
			t.Fatal("ResponseWriter() did not return original recorder")
		}
		if got := ctx.Response().Writer(); got != rec {
			t.Fatal("Response().Writer() did not return original recorder")
		}
		if got := ctx.Response().Header(); got == nil {
			t.Fatal("Response().Header() returned nil")
		}

		replacement := httptest.NewRequest(http.MethodPut, "/submit?via=replaced", strings.NewReader(`{"name":"updated"}`))
		ctx.SetRequest(replacement)
		if got := ctx.Request(); got != replacement {
			t.Fatal("SetRequest() did not update request")
		}

		nextRec := httptest.NewRecorder()
		ctx.SetResponseWriter(nextRec)
		if got := ctx.ResponseWriter(); got != nextRec {
			t.Fatal("SetResponseWriter() did not update recorder")
		}
		if got := ctx.Response().Writer(); got != nextRec {
			t.Fatal("Response().Writer() did not update recorder")
		}

		type payload struct {
			Name string `json:"name"`
		}
		var bound payload
		if err := ctx.Bind(&bound); err != nil {
			t.Fatalf("Bind(): %v", err)
		}
		if got, want := bound.Name, "updated"; got != want {
			t.Fatalf("bound.Name = %q, want %q", got, want)
		}
	})

	t.Run("response helpers", func(t *testing.T) {
		testFile := filepath.Join(t.TempDir(), "sample.txt")
		if err := os.WriteFile(testFile, []byte("file-body"), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		t.Run("headers and cookies", func(t *testing.T) {
			ctx := NewContext(nil, nil, "/headers", nil)
			ctx.AddHeader("X-Trace", "one")
			ctx.AddHeader("X-Trace", "two")
			ctx.SetHeader("X-One", "1")
			ctx.SetCookie(&http.Cookie{Name: "mode", Value: "test", Path: "/"})

			if got := ctx.Response().Header().Values("X-Trace"); len(got) != 2 {
				t.Fatalf("X-Trace values = %#v", got)
			}
			if got, want := ctx.Response().Header().Get("X-One"), "1"; got != want {
				t.Fatalf("X-One = %q, want %q", got, want)
			}
			if got := ctx.Response().Header().Get("Set-Cookie"); !strings.Contains(got, "mode=test") {
				t.Fatalf("Set-Cookie = %q", got)
			}
			if got := ctx.Native(); got != ctx.ResponseWriter() {
				t.Fatal("Native() did not return recorder")
			}
			if got := ctx.Response().Native(); got != ctx.ResponseWriter() {
				t.Fatal("Response().Native() did not return recorder")
			}
		})

		t.Run("blob html and no content", func(t *testing.T) {
			ctx := NewContext(nil, nil, "/blob", nil)
			if err := ctx.Blob(http.StatusAccepted, "text/csv", []byte("a,b")); err != nil {
				t.Fatalf("Blob(): %v", err)
			}
			if got, want := ctx.StatusCode(), http.StatusAccepted; got != want {
				t.Fatalf("status = %d, want %d", got, want)
			}
			if got, want := ctx.Response().Header().Get("Content-Type"), "text/csv"; got != want {
				t.Fatalf("Content-Type = %q, want %q", got, want)
			}
			if got, want := ctx.Response().Writer().(*httptest.ResponseRecorder).Body.String(), "a,b"; got != want {
				t.Fatalf("blob body = %q, want %q", got, want)
			}

			ctx = NewContext(nil, nil, "/html", nil)
			if err := ctx.HTML(http.StatusCreated, "<h1>ok</h1>"); err != nil {
				t.Fatalf("HTML(): %v", err)
			}
			if got := ctx.Response().Header().Get("Content-Type"); !strings.Contains(got, "text/html") {
				t.Fatalf("html content type = %q", got)
			}

			ctx = NewContext(nil, nil, "/empty", nil)
			if err := ctx.NoContent(http.StatusNoContent); err != nil {
				t.Fatalf("NoContent(): %v", err)
			}
			if got, want := ctx.StatusCode(), http.StatusNoContent; got != want {
				t.Fatalf("status = %d, want %d", got, want)
			}
		})

		t.Run("redirect and file", func(t *testing.T) {
			ctx := NewContext(httptest.NewRequest(http.MethodGet, "/old", nil), nil, "/old", nil)
			if err := ctx.Redirect(http.StatusTemporaryRedirect, "/new"); err != nil {
				t.Fatalf("Redirect(): %v", err)
			}
			if got, want := ctx.Response().Header().Get("Location"), "/new"; got != want {
				t.Fatalf("Location = %q, want %q", got, want)
			}

			ctx = NewContext(httptest.NewRequest(http.MethodGet, "/file", nil), nil, "/file", nil)
			if err := ctx.File(testFile); err != nil {
				t.Fatalf("File(): %v", err)
			}
			if got, want := ctx.Response().Writer().(*httptest.ResponseRecorder).Body.String(), "file-body"; got != want {
				t.Fatalf("file body = %q, want %q", got, want)
			}
		})

		t.Run("response set writer panics on unsupported type", func(t *testing.T) {
			ctx := NewContext(nil, nil, "/panic", nil)
			defer func() {
				if recover() == nil {
					t.Fatal("expected panic from unsupported response writer")
				}
			}()
			ctx.Response().SetWriter(fakeResponseWriter{})
		})
	})
}

func TestNewContextProvidesDefaults(t *testing.T) {
	ctx := NewContext(nil, nil, "/", nil)

	if got, want := ctx.Method(), http.MethodGet; got != want {
		t.Fatalf("Method() = %q, want %q", got, want)
	}
	if got := ctx.Request(); got == nil {
		t.Fatal("Request() returned nil")
	}
	if got := ctx.ResponseWriter(); got == nil {
		t.Fatal("ResponseWriter() returned nil")
	}
	if got := ctx.Param("missing"); got != "" {
		t.Fatalf("Param(missing) = %q", got)
	}
}
