package webindex_test

import (
	"context"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/goforj/web"
	"github.com/goforj/web/webindex"
	"github.com/goforj/web/webindex/testfixture/routeparity/app"
	"github.com/goforj/web/webindex/testfixture/routeparity/controllers"
)

// TestRunMatchesRuntimeRouteComposition proves source indexing exposes the same method/path surface as runtime registration.
func TestRunMatchesRuntimeRouteComposition(t *testing.T) {
	fixtureRoot := routeParityFixtureRoot(t)
	outputRoot := t.TempDir()
	manifest, err := webindex.Run(context.Background(), webindex.IndexOptions{
		Root:                 fixtureRoot,
		RouteCompositionPath: filepath.Join(fixtureRoot, "app", "routes.go"),
		OutPath:              filepath.Join(outputRoot, "api_index.json"),
		DiagnosticsPath:      filepath.Join(outputRoot, "api_index.diagnostics.json"),
	})
	if err != nil {
		t.Fatalf("index parity fixture: %v", err)
	}

	runtimeGroups := app.ProvideRoutes(&controllers.PublicController{}, &controllers.AccountController{})
	if got, want := indexedMethodPaths(manifest), runtimeMethodPaths(runtimeGroups); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("indexed routes differ from runtime routes:\nindexed: %v\nruntime: %v", got, want)
	}
	for _, operation := range manifest.Operations {
		if operation.Path == "/api/v1/internal/accounts/:id" {
			t.Fatal("unreturned provider leaked into the indexed runtime surface")
		}
		if operation.Path == "/api/v1/accounts/:id" && len(operation.Middleware) != 2 {
			t.Fatalf("protected route middleware = %v, want group plus route middleware", operation.Middleware)
		}
	}
}

// routeParityFixtureRoot finds the checked-in source fixture independently of the test process working directory.
func routeParityFixtureRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve parity test source path")
	}
	return filepath.Join(filepath.Dir(filename), "testfixture", "routeparity")
}

// indexedMethodPaths converts index paths to the same stable representation used for runtime routes.
func indexedMethodPaths(manifest webindex.Manifest) []string {
	entries := make([]string, 0, len(manifest.Operations))
	for _, operation := range manifest.Operations {
		entries = append(entries, strings.ToUpper(operation.Method)+" "+operation.Path)
	}
	sort.Strings(entries)
	return entries
}

// runtimeMethodPaths flattens the actual route groups the runtime passes to web.RegisterRoutes.
func runtimeMethodPaths(groups []web.RouteGroup) []string {
	entries := make([]string, 0)
	for index := range groups {
		group := &groups[index]
		routes := group.Routes()
		for routeIndex := range routes {
			route := &routes[routeIndex]
			entries = append(entries, route.Method()+" "+group.RoutePrefix()+route.Path())
		}
	}
	sort.Strings(entries)
	return entries
}
