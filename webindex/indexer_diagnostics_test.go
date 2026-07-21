package webindex

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRunReportsParseFailuresWithoutLeakingCheckoutPaths verifies lenient indexing stays useful without making skipped syntax failures invisible.
func TestRunReportsParseFailuresWithoutLeakingCheckoutPaths(t *testing.T) {
	root := t.TempDir()
	writeFixtureFiles(t, root, map[string]string{
		"go.mod":                    "module example.com/diagnostics\n\ngo 1.25\n",
		"internal/broken/broken.go": "package broken\nfunc Broken(",
	})
	diagnosticsPath := filepath.Join(root, "build", "api_index.diagnostics.json")

	manifest, err := Run(context.Background(), IndexOptions{
		Root:            root,
		DiagnosticsPath: diagnosticsPath,
	})
	if err != nil {
		t.Fatalf("Run failed in lenient mode: %v", err)
	}
	if len(manifest.Diagnostics) == 0 || manifest.Diagnostics[0].Code != "parse_error" {
		t.Fatalf("expected parse_error diagnostic, got %#v", manifest.Diagnostics)
	}
	if manifest.Diagnostics[0].File != "internal/broken/broken.go" {
		t.Fatalf("expected relative diagnostic path, got %q", manifest.Diagnostics[0].File)
	}
	contents, err := os.ReadFile(diagnosticsPath)
	if err != nil {
		t.Fatalf("read diagnostics artifact: %v", err)
	}
	if strings.Contains(string(contents), filepath.ToSlash(root)) {
		t.Fatalf("diagnostics artifact leaked checkout root %q: %s", root, contents)
	}
}

// TestRunStrictPreservesPublishedArtifacts verifies strict diagnostics fail before any artifact is replaced.
func TestRunStrictPreservesPublishedArtifacts(t *testing.T) {
	root := t.TempDir()
	writeFixtureFiles(t, root, map[string]string{
		"go.mod":                    "module example.com/strict\n\ngo 1.25\n",
		"internal/broken/broken.go": "package broken\nfunc Broken(",
	})
	paths := []string{
		filepath.Join(root, "build", "api_index.json"),
		filepath.Join(root, "build", "api_index.diagnostics.json"),
		filepath.Join(root, "build", "openapi.json"),
	}
	for _, path := range paths {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir artifact directory: %v", err)
		}
		if err := os.WriteFile(path, []byte("previous\n"), 0o644); err != nil {
			t.Fatalf("write previous artifact: %v", err)
		}
	}

	manifest, err := Run(context.Background(), IndexOptions{
		Root:            root,
		OutPath:         paths[0],
		DiagnosticsPath: paths[1],
		OpenAPIPath:     paths[2],
		Strict:          true,
	})
	var diagnosticsErr *DiagnosticsError
	if !errors.As(err, &diagnosticsErr) {
		t.Fatalf("expected DiagnosticsError, got %T %v", err, err)
	}
	if len(manifest.Diagnostics) == 0 {
		t.Fatal("expected the failed manifest to retain diagnostics for callers")
	}
	for _, path := range paths {
		contents, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatalf("read preserved artifact %q: %v", path, readErr)
		}
		if string(contents) != "previous\n" {
			t.Fatalf("strict failure replaced %q with %q", path, contents)
		}
	}
}

// TestRunHonorsCanceledContextBeforePublication verifies canceled builds cannot publish a partial or stale candidate.
func TestRunHonorsCanceledContextBeforePublication(t *testing.T) {
	root := t.TempDir()
	writeFixtureFiles(t, root, map[string]string{
		"go.mod": "module example.com/canceled\n\ngo 1.25\n",
	})
	out := filepath.Join(root, "build", "api_index.json")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := Run(ctx, IndexOptions{Root: root, OutPath: out})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
	if _, statErr := os.Stat(out); !os.IsNotExist(statErr) {
		t.Fatalf("canceled run published an artifact: %v", statErr)
	}
}

// TestRunStopsTraversalAfterCancellation verifies cancellation is observed while a repository walk is in progress.
func TestRunStopsTraversalAfterCancellation(t *testing.T) {
	root := t.TempDir()
	writeFixtureFiles(t, root, map[string]string{
		"go.mod":                  "module example.com/canceledwalk\n\ngo 1.25\n",
		"internal/a/source.go":    "package a\n",
		"internal/b/source.go":    "package b\n",
		"internal/c/source.go":    "package c\n",
		"internal/deep/source.go": "package deep\n",
	})
	out := filepath.Join(root, "build", "api_index.json")
	ctx, cancel := context.WithCancel(context.Background())
	canceled := false

	_, err := Run(ctx, IndexOptions{
		Root:    root,
		OutPath: out,
		SkipDir: func(_ string, _ string) bool {
			if !canceled {
				canceled = true
				cancel()
			}
			return false
		},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected traversal cancellation, got %v", err)
	}
	if _, statErr := os.Stat(out); !os.IsNotExist(statErr) {
		t.Fatalf("canceled traversal published an artifact: %v", statErr)
	}
}

// TestRunRejectsDuplicateMethodPaths verifies an ambiguous route pair never degrades into last-write-wins OpenAPI output.
func TestRunRejectsDuplicateMethodPaths(t *testing.T) {
	root := t.TempDir()
	writeFixtureFiles(t, root, map[string]string{
		"go.mod": "module example.com/duplicates\n\ngo 1.25\n",
		"internal/things/controller.go": `package things
import (
	"net/http"
	"github.com/goforj/web"
)
type Controller struct{}
func (c *Controller) Routes() []web.Route {
	return []web.Route{
		web.NewRoute(http.MethodGet, "/things", c.First),
		web.NewRoute(http.MethodGet, "/things", c.Second),
	}
}
func (c *Controller) First(ctx web.Context) error { return ctx.NoContent(http.StatusNoContent) }
func (c *Controller) Second(ctx web.Context) error { return ctx.NoContent(http.StatusNoContent) }
`,
	})
	out := filepath.Join(root, "build", "openapi.json")

	_, err := Run(context.Background(), IndexOptions{Root: root, OpenAPIPath: out})
	var diagnosticsErr *DiagnosticsError
	if !errors.As(err, &diagnosticsErr) {
		t.Fatalf("expected duplicate DiagnosticsError, got %T %v", err, err)
	}
	found := false
	for _, diagnostic := range diagnosticsErr.Diagnostics {
		if diagnostic.Code == "duplicate_operation" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected duplicate_operation diagnostic, got %#v", diagnosticsErr.Diagnostics)
	}
	if _, statErr := os.Stat(out); !os.IsNotExist(statErr) {
		t.Fatalf("duplicate route run published OpenAPI: %v", statErr)
	}
}

// TestRunDiagnosesResponseLessHandlers verifies missing response evidence remains visible and never turns into an invented success status.
func TestRunDiagnosesResponseLessHandlers(t *testing.T) {
	root := t.TempDir()
	writeFixtureFiles(t, root, map[string]string{
		"go.mod": "module example.com/noresponse\n\ngo 1.25\n",
		"internal/tasks/controller.go": `package tasks
import (
	"net/http"
	"github.com/goforj/web"
)
type Controller struct{}
func (c *Controller) Routes() []web.Route {
	return []web.Route{web.NewRoute(http.MethodPost, "/tasks", c.Create)}
}
func (c *Controller) Create(ctx web.Context) error { return nil }
`,
	})

	manifest, err := Run(context.Background(), IndexOptions{Root: root})
	if err != nil {
		t.Fatalf("lenient response-less run failed: %v", err)
	}
	found := false
	for _, diagnostic := range manifest.Diagnostics {
		if diagnostic.Code == "handler_no_response" && diagnostic.Severity == "warn" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected handler_no_response warning, got %#v", manifest.Diagnostics)
	}
	document := toOpenAPI(manifest)
	responses := document.Paths["/tasks"]["post"].Responses
	if _, exists := responses["200"]; exists {
		t.Fatalf("response-less handler fabricated a 200 response: %#v", responses)
	}
	if _, exists := responses["default"]; !exists {
		t.Fatalf("response-less handler needs an honest default response: %#v", responses)
	}

	_, err = Run(context.Background(), IndexOptions{Root: root, Strict: true})
	var diagnosticsErr *DiagnosticsError
	if !errors.As(err, &diagnosticsErr) {
		t.Fatalf("strict response-less run should fail, got %T %v", err, err)
	}
}

// TestRunExcludesNestedClosureContracts keeps captured context calls from becoming evidence for the enclosing HTTP handler.
func TestRunExcludesNestedClosureContracts(t *testing.T) {
	root := t.TempDir()
	writeFixtureFiles(t, root, map[string]string{
		"go.mod": "module example.com/closurecontracts\n\ngo 1.25\n",
		"internal/tasks/controller.go": `package tasks
import (
	"net/http"
	"github.com/goforj/web"
)
type ghostPayload struct { Secret string ` + "`json:\"secret\"`" + ` }
type Controller struct{}
func register(callback func() error) {}
func (c *Controller) Routes() []web.Route {
	return []web.Route{
		web.NewRoute(http.MethodGet, "/real", c.Real),
		web.NewRoute(http.MethodGet, "/closure-only", c.ClosureOnly),
	}
}
func (c *Controller) Real(ctx web.Context) error {
	const status = http.StatusNoContent
	unused := func() error {
		const status = http.StatusInternalServerError
		_ = ctx.Param("ghost")
		_ = ctx.Query("unused")
		var body ghostPayload
		_ = ctx.Bind(&body)
		return ctx.JSON(status, ghostPayload{Secret: "unused"})
	}
	_ = unused
	register(func() error {
		_ = ctx.Query("callback")
		return ctx.NoContent(http.StatusBadGateway)
	})
	return ctx.NoContent(status)
}
func (c *Controller) ClosureOnly(ctx web.Context) error {
	callback := func() error { return ctx.NoContent(http.StatusCreated) }
	register(callback)
	return nil
}
`,
	})

	manifest, err := Run(context.Background(), IndexOptions{Root: root})
	if err != nil {
		t.Fatalf("lenient closure contract run failed: %v", err)
	}
	real := operationByPath(t, manifest, "/real")
	if len(real.Outputs.Responses) != 1 || real.Outputs.Responses[0].StatusCode != http.StatusNoContent {
		t.Fatalf("nested closures invented responses for outer handler: %+v", real.Outputs.Responses)
	}
	if len(real.Inputs.PathParams) != 0 || len(real.Inputs.QueryParams) != 0 || real.Inputs.Body != nil {
		t.Fatalf("nested closures invented request contracts for outer handler: %+v", real.Inputs)
	}
	closureOnly := operationByPath(t, manifest, "/closure-only")
	if len(closureOnly.Outputs.Responses) != 0 {
		t.Fatalf("callback-only handler invented responses: %+v", closureOnly.Outputs.Responses)
	}
	for _, schema := range manifest.Schemas {
		if schema.TypeName == "ghostPayload" {
			t.Fatalf("nested closure contract leaked into manifest schemas: %+v", schema)
		}
	}
	if !manifestHasDiagnostic(manifest, "handler_no_response") {
		t.Fatalf("callback-only handler must retain an honest missing-response diagnostic: %+v", manifest.Diagnostics)
	}
	if manifestHasDiagnostic(manifest, "handler_path_param_not_in_route") {
		t.Fatalf("nested closure path access leaked into handler validation: %+v", manifest.Diagnostics)
	}

	_, err = Run(context.Background(), IndexOptions{Root: root, Strict: true})
	var diagnosticsErr *DiagnosticsError
	if !errors.As(err, &diagnosticsErr) {
		t.Fatalf("strict callback-only handler should fail honestly, got %T %v", err, err)
	}
}

// TestDiagnosticsErrorFormatting verifies callers receive useful summaries for empty, singular, and plural diagnostic sets.
func TestDiagnosticsErrorFormatting(t *testing.T) {
	tests := []struct {
		name        string
		diagnostics []Diagnostic
		want        string
	}{
		{name: "empty", want: "API index validation failed"},
		{
			name: "file and line",
			diagnostics: []Diagnostic{{
				Code:    "invalid_route",
				File:    "app/routes.go",
				Line:    12,
				Message: "route cannot be indexed",
			}},
			want: "API index validation failed with 1 diagnostic; invalid_route at app/routes.go:12: route cannot be indexed",
		},
		{
			name: "line without file",
			diagnostics: []Diagnostic{
				{Code: "first", Line: 7, Message: "first problem"},
				{Code: "second", Message: "second problem"},
			},
			want: "API index validation failed with 2 diagnostics; first at line 7: first problem",
		},
		{
			name: "file without line",
			diagnostics: []Diagnostic{{
				Code:    "invalid_file",
				File:    "app/routes.go",
				Message: "file cannot be indexed",
			}},
			want: "API index validation failed with 1 diagnostic; invalid_file at app/routes.go: file cannot be indexed",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := (&DiagnosticsError{Diagnostics: test.diagnostics}).Error(); got != test.want {
				t.Fatalf("DiagnosticsError.Error() = %q, want %q", got, test.want)
			}
		})
	}
}
