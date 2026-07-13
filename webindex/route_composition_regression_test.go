package webindex

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// TestRunScopesInlineAndProviderRoutesToReturnedValues excludes dead calls while preserving exact returned placement policy.
func TestRunScopesInlineAndProviderRoutesToReturnedValues(t *testing.T) {
	root := t.TempDir()
	writeFixtureFiles(t, root, map[string]string{
		"go.mod": "module example.com/returned\n\ngo 1.24\n",
		"internal/report/controller.go": `package report
import (
	"net/http"
	"github.com/goforj/web"
)
type Controller struct{}
func (c *Controller) Routes() []web.Route {
	live := web.NewRoute(http.MethodGet, "/provider-live", c.Live)
	dead := web.NewRoute(http.MethodGet, "/provider-dead", c.Dead)
	_ = dead
	return []web.Route{live}
}
func (c *Controller) Live(ctx web.Context) error { return ctx.NoContent(http.StatusNoContent) }
func (c *Controller) Dead(ctx web.Context) error { return ctx.NoContent(http.StatusNoContent) }
`,
		"app/routes.go": `package app
import (
	"net/http"
	"slices"
	"github.com/goforj/web"
	"example.com/returned/internal/report"
)
func ProvideRoutes(controller *report.Controller) []web.RouteGroup {
	providerRoutes := slices.Concat(controller.Routes())
	inlineRoutes := []web.Route{web.NewRoute(http.MethodGet, "/inline-live", inline)}
	dead := web.NewRoute(http.MethodGet, "/inline-dead", inline)
	_ = dead
	groups := []web.RouteGroup{
		web.NewRouteGroup("/api", providerRoutes, requireAuth),
		web.NewRouteGroup("/inline", inlineRoutes, requireInline()),
	}
	_ = web.NewRouteGroup("/dead", []web.Route{web.NewRoute(http.MethodGet, "/nested-dead", inline)})
	return slices.Concat(groups)
}
func inline(ctx web.Context) error { return ctx.NoContent(http.StatusNoContent) }
func requireAuth(next web.Handler) web.Handler { return next }
func requireInline() web.Middleware { return func(next web.Handler) web.Handler { return next } }
`,
	})

	manifest, err := Run(context.Background(), IndexOptions{Root: root, RouteCompositionPath: "app/routes.go"})
	if err != nil {
		t.Fatalf("index returned-only routes: %v", err)
	}
	if len(manifest.Operations) != 2 {
		t.Fatalf("unexpected returned operation count: %d (%+v)", len(manifest.Operations), manifest.Operations)
	}
	provider := operationByPath(t, manifest, "/api/provider-live")
	if !reflect.DeepEqual(provider.Middleware, []string{"requireAuth"}) {
		t.Fatalf("provider placement middleware = %v", provider.Middleware)
	}
	inline := operationByPath(t, manifest, "/inline/inline-live")
	if !reflect.DeepEqual(inline.Middleware, []string{"requireInline()"}) {
		t.Fatalf("inline placement middleware = %v", inline.Middleware)
	}
	for _, dead := range []string{"/api/provider-dead", "/inline/inline-dead", "/dead/nested-dead"} {
		for _, operation := range manifest.Operations {
			if operation.Path == dead {
				t.Fatalf("dead route %s leaked into manifest", dead)
			}
		}
	}
}

// TestRunDiagnosesUnsupportedRouteControlFlow refuses to flatten branch-local assignments into returned route evidence.
func TestRunDiagnosesUnsupportedRouteControlFlow(t *testing.T) {
	root := t.TempDir()
	writeFixtureFiles(t, root, map[string]string{
		"go.mod": "module example.com/flow\n\ngo 1.24\n",
		"internal/report/controller.go": `package report
import (
	"net/http"
	"github.com/goforj/web"
)
type Controller struct{}
func (c *Controller) Routes() []web.Route {
	if enabled() {
		return []web.Route{web.NewRoute(http.MethodGet, "/branch", c.Show)}
	}
	return nil
}
func (c *Controller) Show(ctx web.Context) error { return ctx.NoContent(http.StatusNoContent) }
func enabled() bool { return true }
`,
		"app/routes.go": `package app
import (
	"github.com/goforj/web"
	"example.com/flow/internal/report"
)
func ProvideRoutes(controller *report.Controller) []web.RouteGroup {
	groups := []web.RouteGroup{}
	if enabled() {
		groups = append(groups, web.NewRouteGroup("/api", controller.Routes()))
	}
	return groups
}
func enabled() bool { return true }
`,
	})

	manifest, err := Run(context.Background(), IndexOptions{Root: root, RouteCompositionPath: "app/routes.go"})
	if err != nil {
		t.Fatalf("lenient index unsupported flow: %v", err)
	}
	if len(manifest.Operations) != 0 {
		t.Fatalf("branch-local routes leaked into manifest: %+v", manifest.Operations)
	}
	if !manifestHasDiagnostic(manifest, "route_composition_unsupported_control_flow") {
		t.Fatalf("missing composition control-flow diagnostic: %+v", manifest.Diagnostics)
	}
}

// TestRunScopesHistoricalAppRoutes resolves only fields returned by the generated registry provider.
func TestRunScopesHistoricalAppRoutes(t *testing.T) {
	root := t.TempDir()
	writeFixtureFiles(t, root, map[string]string{
		"go.mod": "module example.com/historical\n\ngo 1.24\n",
		"internal/report/controller.go": `package report
import (
	"net/http"
	"github.com/goforj/web"
)
type Controller struct{}
func (c *Controller) Routes() []web.Route { return []web.Route{web.NewRoute(http.MethodGet, "/report", c.Show)} }
func (c *Controller) DeadRoutes() []web.Route { return []web.Route{web.NewRoute(http.MethodGet, "/dead", c.Show)} }
func (c *Controller) Show(ctx web.Context) error { return ctx.NoContent(http.StatusNoContent) }
`,
		"app/routes.go": `package app
import (
	"github.com/goforj/web"
	"example.com/historical/internal/report"
)
type AppRoutes struct { app []web.Route }
func ProvideAppRoutes(controller *report.Controller) *AppRoutes {
	app := append([]web.Route{}, controller.Routes()...)
	dead := append([]web.Route{}, controller.DeadRoutes()...)
	_ = &AppRoutes{app: dead}
	return &AppRoutes{app: app}
}
func ProvideRoutes(routes *AppRoutes) []web.RouteGroup {
	return []web.RouteGroup{web.NewRouteGroup("/api", routes.app)}
}
`,
	})

	manifest, err := Run(context.Background(), IndexOptions{Root: root, RouteCompositionPath: "app/routes.go"})
	if err != nil {
		t.Fatalf("index scoped historical composition: %v", err)
	}
	if len(manifest.Operations) != 1 || manifest.Operations[0].Path != "/api/report" {
		t.Fatalf("historical composition mismatch: %+v", manifest.Operations)
	}
}

// TestRunScopesHistoricalGuardedAppRoutes preserves the exact legacy registry guards because skipping an empty group cannot remove a concrete returned route.
func TestRunScopesHistoricalGuardedAppRoutes(t *testing.T) {
	root := t.TempDir()
	writeFixtureFiles(t, root, map[string]string{
		"go.mod": "module example.com/historicalguard\n\ngo 1.24\n",
		"internal/report/controller.go": `package report
import (
	"net/http"
	"github.com/goforj/web"
)
type Controller struct{}
func (c *Controller) PublicRoutes() []web.Route { return []web.Route{web.NewRoute(http.MethodGet, "/public", c.Public)} }
func (c *Controller) ProtectedRoutes(officer ...web.Middleware) []web.Route { return []web.Route{web.NewRoute(http.MethodGet, "/protected", c.Protected, officer...)} }
func (c *Controller) Public(ctx web.Context) error { return ctx.NoContent(http.StatusNoContent) }
func (c *Controller) Protected(ctx web.Context) error { return ctx.NoContent(http.StatusNoContent) }
`,
		"internal/auth/service.go": `package auth
import "github.com/goforj/web"
type Service struct{}
func (s *Service) RequireAuth(next web.Handler) web.Handler { return next }
func (s *Service) RequireOfficer(next web.Handler) web.Handler { return next }
`,
		"app/routes.go": `package app
import (
	"github.com/goforj/web"
	"example.com/historicalguard/internal/auth"
	"example.com/historicalguard/internal/report"
)
type AppRoutes struct {
	public []web.Route
	protected []web.Route
}
func ProvideAppRoutes(controller *report.Controller, authService *auth.Service) *AppRoutes {
	var public []web.Route
	var protected []web.Route
	public = append(public, controller.PublicRoutes()...)
	protected = append(protected, controller.ProtectedRoutes(authService.RequireOfficer)...)
	return &AppRoutes{public: public, protected: protected}
}
func ProvideRoutes(routes *AppRoutes, authService *auth.Service) []web.RouteGroup {
	var groups []web.RouteGroup
	if len(routes.public) > 0 {
		groups = append(groups, web.NewRouteGroup("/api/v1", routes.public))
	}
	if len(routes.protected) > 0 {
		groups = append(groups, web.NewRouteGroup("/api/v1", routes.protected, authService.RequireAuth))
	}
	return groups
}
`,
	})

	manifest, err := Run(context.Background(), IndexOptions{Root: root, RouteCompositionPath: "app/routes.go", Strict: true})
	if err != nil {
		t.Fatalf("strict index historical guarded composition: %v", err)
	}
	if len(manifest.Operations) != 2 {
		t.Fatalf("historical guarded operation count = %d: %+v", len(manifest.Operations), manifest.Operations)
	}
	if middleware := operationByPath(t, manifest, "/api/v1/public").Middleware; len(middleware) != 0 {
		t.Fatalf("public historical middleware = %v", middleware)
	}
	if middleware := operationByPath(t, manifest, "/api/v1/protected").Middleware; !reflect.DeepEqual(middleware, []string{"authService.RequireAuth", "authService.RequireOfficer"}) {
		t.Fatalf("protected historical middleware = %v", middleware)
	}
	for _, diagnostic := range manifest.Diagnostics {
		if diagnostic.Code == "route_composition_unsupported_control_flow" || diagnostic.Code == "route_composition_no_returned_groups" {
			t.Fatalf("historical guard reported unsupported flow: %+v", manifest.Diagnostics)
		}
	}
}

// TestRunRejectsRepeatedProviderPlacement prevents one source route from receiving arbitrary policy from multiple returned groups.
func TestRunRejectsRepeatedProviderPlacement(t *testing.T) {
	root := t.TempDir()
	writeFixtureFiles(t, root, map[string]string{
		"go.mod": "module example.com/repeated\n\ngo 1.24\n",
		"internal/report/controller.go": `package report
import (
	"net/http"
	"github.com/goforj/web"
)
type Controller struct{}
func (c *Controller) Routes() []web.Route { return []web.Route{web.NewRoute(http.MethodGet, "/report", c.Show)} }
func (c *Controller) Show(ctx web.Context) error { return ctx.NoContent(http.StatusNoContent) }
`,
		"app/routes.go": `package app
import (
	"github.com/goforj/web"
	"example.com/repeated/internal/report"
)
func ProvideRoutes(controller *report.Controller) []web.RouteGroup {
	return []web.RouteGroup{
		web.NewRouteGroup("/one", controller.Routes()),
		web.NewRouteGroup("/two", controller.Routes()),
	}
}
`,
	})

	_, err := Run(context.Background(), IndexOptions{Root: root, RouteCompositionPath: "app/routes.go"})
	assertDiagnosticsErrorCode(t, err, "route_provider_multiple_placements")
}

// TestRouteTemplateProblemsRejectsUnprojectableSyntax validates names, wildcard placement, braces, and exact concatenation artifacts.
func TestRouteTemplateProblemsRejectsUnprojectableSyntax(t *testing.T) {
	for path, valid := range map[string]bool{
		"/users/:id":            true,
		"/assets/*rest":         true,
		"users/:id":             false,
		"/users/:":              false,
		"/assets/*":             false,
		"/assets/*rest/tail":    false,
		"/users/:id/orders/:id": false,
		"/users/{id}":           false,
		"/api//users":           true,
		"/users?active=true":    false,
		"/users#section":        false,
		"/users/hello%20world":  true,
		"/users/bad%2":          false,
		"/users/raw space":      false,
		"/users/[id]":           false,
	} {
		problems := routeTemplateProblems(path)
		if valid && len(problems) > 0 {
			t.Fatalf("valid route %q reported problems: %v", path, problems)
		}
		if !valid && len(problems) == 0 {
			t.Fatalf("invalid route %q had no problem", path)
		}
	}
	if got := routeCatchAllParameters("/assets/*rest"); !reflect.DeepEqual(got, []string{"rest"}) {
		t.Fatalf("catch-all parameters = %v", got)
	}
}

// TestRunDiagnosesHandlerPathParameterMismatch omits handler-only path parameters from the published route contract.
func TestRunDiagnosesHandlerPathParameterMismatch(t *testing.T) {
	root := t.TempDir()
	writeFixtureFiles(t, root, map[string]string{
		"go.mod": "module example.com/pathmismatch\n\ngo 1.24\n",
		"controller.go": `package pathmismatch
import (
	"net/http"
	"github.com/goforj/web"
)
type Controller struct{}
func (c *Controller) Routes() []web.Route {
	return []web.Route{
		web.NewRoute(http.MethodGet, "/users/:id", c.Show),
		web.NewRoute(http.MethodGet, "/assets/*rest", c.Show),
	}
}
func (c *Controller) Show(ctx web.Context) error {
	_, _ = ctx.Param("id"), ctx.Param("missing")
	return ctx.NoContent(http.StatusNoContent)
}
`,
	})

	manifest, err := Run(context.Background(), IndexOptions{Root: root})
	if err != nil {
		t.Fatalf("index path mismatch: %v", err)
	}
	operation := operationByPath(t, manifest, "/users/:id")
	if len(operation.Inputs.PathParams) != 1 || operation.Inputs.PathParams[0].Name != "id" {
		t.Fatalf("handler-only path parameter entered contract: %+v", operation.Inputs.PathParams)
	}
	if !manifestHasDiagnostic(manifest, "handler_path_param_not_in_route") {
		t.Fatalf("missing path mismatch diagnostic: %+v", manifest.Diagnostics)
	}
	if wildcard := operationByPath(t, manifest, "/assets/*rest"); len(wildcard.Inputs.PathParams) != 1 || wildcard.Inputs.PathParams[0].Name != "rest" {
		t.Fatalf("named wildcard parameter missing from manifest: %+v", wildcard.Inputs.PathParams)
	}
	if !manifestHasDiagnostic(manifest, "unrepresentable_route_wildcard") {
		t.Fatalf("missing wildcard projection diagnostic: %+v", manifest.Diagnostics)
	}
}

// TestRunRetainsConnectWithProjectionDiagnostic keeps runtime-only methods visible without presenting them as ordinary OpenAPI operations.
func TestRunRetainsConnectWithProjectionDiagnostic(t *testing.T) {
	root := t.TempDir()
	writeFixtureFiles(t, root, map[string]string{
		"go.mod": "module example.com/connectroute\n\ngo 1.24\n",
		"controller.go": `package connectroute
import (
	"net/http"
	"github.com/goforj/web"
)
type Controller struct{}
func (c *Controller) Routes() []web.Route { return []web.Route{web.NewRoute(http.MethodConnect, "/tunnel", c.Show)} }
func (c *Controller) Show(ctx web.Context) error { return ctx.NoContent(http.StatusNoContent) }
`,
	})
	manifest, err := Run(context.Background(), IndexOptions{Root: root})
	if err != nil {
		t.Fatalf("index CONNECT route: %v", err)
	}
	if len(manifest.Operations) != 1 || manifest.Operations[0].Method != "CONNECT" {
		t.Fatalf("CONNECT route missing from manifest: %+v", manifest.Operations)
	}
	if !manifestHasDiagnostic(manifest, "unrepresentable_openapi_method") {
		t.Fatalf("CONNECT projection diagnostic missing: %+v", manifest.Diagnostics)
	}
}

// TestRunRejectsEquivalentRouteTemplateShapes catches paths OpenAPI considers identical even when parameter labels differ.
func TestRunRejectsEquivalentRouteTemplateShapes(t *testing.T) {
	root := t.TempDir()
	writeFixtureFiles(t, root, map[string]string{
		"go.mod": "module example.com/pathshape\n\ngo 1.24\n",
		"controller.go": `package pathshape
import (
	"net/http"
	"github.com/goforj/web"
)
type Controller struct{}
func (c *Controller) Routes() []web.Route {
	return []web.Route{
		web.NewRoute(http.MethodGet, "/users/:id", c.Show),
		web.NewRoute(http.MethodGet, "/users/:name", c.Show),
	}
}
func (c *Controller) Show(ctx web.Context) error { return ctx.NoContent(http.StatusNoContent) }
`,
	})

	_, err := Run(context.Background(), IndexOptions{Root: root})
	assertDiagnosticsErrorCode(t, err, "duplicate_operation")
}

// TestRunCompositionEntrypointDiagnosticsDistinguishExplicitEmpty verifies zero-endpoint apps are valid while a missing entrypoint stays visible.
func TestRunCompositionEntrypointDiagnosticsDistinguishExplicitEmpty(t *testing.T) {
	t.Run("explicit empty", func(t *testing.T) {
		root := t.TempDir()
		writeFixtureFiles(t, root, map[string]string{
			"go.mod": "module example.com/empty\n\ngo 1.24\n",
			"app/routes.go": `package app
import "github.com/goforj/web"
func ProvideRoutes() []web.RouteGroup { return []web.RouteGroup{} }
`,
		})
		manifest, err := Run(context.Background(), IndexOptions{Root: root, RouteCompositionPath: "app/routes.go"})
		if err != nil {
			t.Fatalf("index explicit empty composition: %v", err)
		}
		if manifestHasDiagnostic(manifest, "route_composition_no_returned_groups") {
			t.Fatalf("explicit empty composition was treated as untraceable: %+v", manifest.Diagnostics)
		}
	})

	t.Run("missing entrypoint", func(t *testing.T) {
		root := t.TempDir()
		writeFixtureFiles(t, root, map[string]string{
			"go.mod":        "module example.com/missingentry\n\ngo 1.24\n",
			"app/routes.go": "package app\n",
		})
		manifest, err := Run(context.Background(), IndexOptions{Root: root, RouteCompositionPath: "app/routes.go"})
		if err != nil {
			t.Fatalf("index missing composition entrypoint: %v", err)
		}
		if !manifestHasDiagnostic(manifest, "route_composition_entrypoint_not_found") {
			t.Fatalf("missing entrypoint diagnostic absent: %+v", manifest.Diagnostics)
		}
	})
}

// TestRunDiagnosesMissingRouteProviderDeclaration reports a composition reference that has no parsed provider method.
func TestRunDiagnosesMissingRouteProviderDeclaration(t *testing.T) {
	root := t.TempDir()
	writeFixtureFiles(t, root, map[string]string{
		"go.mod": "module example.com/missingprovider\n\ngo 1.24\n",
		"app/routes.go": `package app
import (
	"github.com/goforj/web"
	missing "example.com/missingprovider/internal/missing"
)
func ProvideRoutes(controller *missing.Controller) []web.RouteGroup {
	return []web.RouteGroup{web.NewRouteGroup("/api", controller.Routes())}
}
`,
	})
	manifest, err := Run(context.Background(), IndexOptions{Root: root, RouteCompositionPath: "app/routes.go"})
	if err != nil {
		t.Fatalf("lenient index missing provider: %v", err)
	}
	if !manifestHasDiagnostic(manifest, "route_provider_not_found") {
		t.Fatalf("missing provider diagnostic absent: %+v", manifest.Diagnostics)
	}
}

// TestRunReportsMalformedSelectedComposition distinguishes an existing unparseable app entrypoint from a missing composition file and never publishes artifacts.
func TestRunReportsMalformedSelectedComposition(t *testing.T) {
	root := t.TempDir()
	output := t.TempDir()
	writeFixtureFiles(t, root, map[string]string{
		"go.mod":        "module example.com/malformedcomposition\n\ngo 1.24\n",
		"app/routes.go": "package app\nfunc ProvideRoutes( {\n",
	})
	outPath := filepath.Join(output, "api_index.json")
	manifest, err := Run(context.Background(), IndexOptions{
		Root:                 root,
		RouteCompositionPath: "app/routes.go",
		OutPath:              outPath,
	})
	var diagnosticsError *DiagnosticsError
	if !errors.As(err, &diagnosticsError) {
		t.Fatalf("malformed selected composition error = %v", err)
	}
	if len(diagnosticsError.Diagnostics) == 0 || diagnosticsError.Diagnostics[0].Code != "parse_error" {
		t.Fatalf("malformed selected composition diagnostics = %+v", diagnosticsError.Diagnostics)
	}
	for _, diagnostic := range diagnosticsError.Diagnostics {
		if diagnostic.File != "app/routes.go" || diagnostic.Severity != "error" {
			t.Fatalf("selected parse diagnostic is not project-relative and blocking: %+v", diagnostic)
		}
	}
	if len(manifest.Diagnostics) == 0 {
		t.Fatal("malformed selected composition diagnostics missing from manifest candidate")
	}
	if _, statErr := os.Stat(outPath); !os.IsNotExist(statErr) {
		t.Fatalf("malformed selected composition published %s: %v", outPath, statErr)
	}
}

// TestRunMatchesGenericProviderReceiverBases keeps instantiated method receivers aligned with composition parameter types.
func TestRunMatchesGenericProviderReceiverBases(t *testing.T) {
	root := t.TempDir()
	writeFixtureFiles(t, root, map[string]string{
		"go.mod": "module example.com/genericprovider\n\ngo 1.24\n",
		"internal/report/controller.go": `package report
import (
	"net/http"
	"github.com/goforj/web"
)
type Controller[T any] struct{}
func (c *Controller[T]) Routes() []web.Route { return []web.Route{web.NewRoute(http.MethodGet, "/report", c.Show)} }
func (c *Controller[T]) Show(ctx web.Context) error { return ctx.NoContent(http.StatusNoContent) }
`,
		"app/routes.go": `package app
import (
	"github.com/goforj/web"
	"example.com/genericprovider/internal/report"
)
func ProvideRoutes(controller *report.Controller[string]) []web.RouteGroup {
	return []web.RouteGroup{web.NewRouteGroup("/api", controller.Routes())}
}
`,
	})
	manifest, err := Run(context.Background(), IndexOptions{Root: root, RouteCompositionPath: "app/routes.go"})
	if err != nil {
		t.Fatalf("index generic route provider: %v", err)
	}
	if len(manifest.Operations) != 1 || manifest.Operations[0].Path != "/api/report" {
		t.Fatalf("generic provider route mismatch: %+v", manifest.Operations)
	}
}

// assertDiagnosticsErrorCode verifies a hard index diagnostic without coupling tests to message text.
func assertDiagnosticsErrorCode(t *testing.T, err error, code string) {
	t.Helper()
	var diagnosticsError *DiagnosticsError
	if !errors.As(err, &diagnosticsError) {
		t.Fatalf("expected diagnostics error %s, got %v", code, err)
	}
	for _, diagnostic := range diagnosticsError.Diagnostics {
		if diagnostic.Code == code {
			return
		}
	}
	t.Fatalf("missing diagnostic %s: %+v", code, diagnosticsError.Diagnostics)
}
