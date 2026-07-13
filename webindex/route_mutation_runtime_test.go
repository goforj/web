package webindex_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/goforj/web/webindex"
	"github.com/goforj/web/webindex/testfixture/routemutation/app"
	"github.com/goforj/web/webindex/testfixture/routemutation/controllers"
)

// TestRunFailsClosedWhenReturnedRoutesAreMutated proves runtime replacement cannot retain the discarded route's path, middleware, or security provenance.
func TestRunFailsClosedWhenReturnedRoutesAreMutated(t *testing.T) {
	fixtureRoot := routeMutationFixtureRoot(t)
	runtimeGroups := app.ProvideRoutes(&controllers.Controller{})
	if len(runtimeGroups) != 1 || len(runtimeGroups[0].Routes()) != 1 {
		t.Fatalf("runtime groups = %+v", runtimeGroups)
	}
	runtimeRoute := runtimeGroups[0].Routes()[0]
	if runtimeRoute.Path() != "/public" || len(runtimeRoute.Middlewares()) != 0 {
		t.Fatalf("runtime route = %s middlewares=%d", runtimeRoute.Path(), len(runtimeRoute.Middlewares()))
	}

	manifest, err := webindex.Run(context.Background(), webindex.IndexOptions{
		Root:                 fixtureRoot,
		RouteCompositionPath: filepath.Join(fixtureRoot, "app", "routes.go"),
	})
	if err != nil {
		t.Fatalf("lenient route-mutation index: %v", err)
	}
	if len(manifest.Operations) != 0 {
		t.Fatalf("unsafe provider published stale operations: %+v", manifest.Operations)
	}
	foundUnsafeFlow := false
	for _, diagnostic := range manifest.Diagnostics {
		if diagnostic.Code == "route_provider_unsafe_route_value_flow" {
			foundUnsafeFlow = true
		}
	}
	if !foundUnsafeFlow {
		t.Fatalf("unsafe provider diagnostic missing: %+v", manifest.Diagnostics)
	}

	_, err = webindex.Run(context.Background(), webindex.IndexOptions{
		Root:                 fixtureRoot,
		RouteCompositionPath: filepath.Join(fixtureRoot, "app", "routes.go"),
		Strict:               true,
	})
	var diagnosticsError *webindex.DiagnosticsError
	if !errors.As(err, &diagnosticsError) {
		t.Fatalf("strict route-mutation index error = %T %v", err, err)
	}

	openAPIPath := filepath.Join(t.TempDir(), "openapi.json")
	_, err = webindex.Run(context.Background(), webindex.IndexOptions{
		Root:                 fixtureRoot,
		RouteCompositionPath: filepath.Join(fixtureRoot, "app", "routes.go"),
		OpenAPIPath:          openAPIPath,
		OpenAPI: webindex.OpenAPIOptions{
			SecuritySchemes: map[string]webindex.OpenAPISecurityScheme{
				"session": {Type: "apiKey", In: "cookie", Name: "session"},
			},
			MiddlewareSecurityRules: []webindex.OpenAPIMiddlewareSecurityRule{{
				Expression: "c.RequireAuth",
				SourceFile: "controllers/controller.go",
				Function:   "Routes",
				Requirements: []webindex.OpenAPISecurityRequirement{{
					"session": {},
				}},
			}},
		},
	})
	var projectionError *webindex.OpenAPIProjectionError
	if !errors.As(err, &projectionError) {
		t.Fatalf("stale scoped security rule error = %T %v", err, err)
	}
	if _, statErr := os.Stat(openAPIPath); !os.IsNotExist(statErr) {
		t.Fatalf("unsafe provider published OpenAPI: %v", statErr)
	}
}

// routeMutationFixtureRoot finds the checked-in runtime fixture independently of the test process working directory.
func routeMutationFixtureRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve route mutation test source path")
	}
	return filepath.Join(filepath.Dir(filename), "testfixture", "routemutation")
}
