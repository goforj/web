package webindex

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

// TestRunRejectsMutatedOrEscapedProviderRouteValues prevents stale returned constructors while preserving simultaneous assignment semantics.
func TestRunRejectsMutatedOrEscapedProviderRouteValues(t *testing.T) {
	root := t.TempDir()
	writeFixtureFiles(t, root, map[string]string{
		"go.mod": "module example.com/providerrouteflow\n\ngo 1.25\n",
		"internal/report/controller.go": `package report
import (
	"net/http"
	"github.com/goforj/web"
)
type Controller struct{}
func replace(routes []web.Route, replacement web.Route) { routes[0] = replacement }
func (c *Controller) MutatedRoutes() []web.Route {
	protected := web.NewRoute(http.MethodGet, "/protected", c.Protected, c.RequireAuth)
	public := web.NewRoute(http.MethodGet, "/public", c.Public)
	routes := []web.Route{protected}
	routes[0] = public
	return routes
}
func (c *Controller) CopiedRoutes() []web.Route {
	protected := web.NewRoute(http.MethodGet, "/protected", c.Protected, c.RequireAuth)
	public := web.NewRoute(http.MethodGet, "/public", c.Public)
	routes := []web.Route{protected}
	copy(routes, []web.Route{public})
	return routes
}
func (c *Controller) EscapedRoutes() []web.Route {
	protected := web.NewRoute(http.MethodGet, "/protected", c.Protected, c.RequireAuth)
	public := web.NewRoute(http.MethodGet, "/public", c.Public)
	routes := []web.Route{protected}
	replace(routes, public)
	return routes
}
func (c *Controller) SlicedRoutes() []web.Route {
	protected := web.NewRoute(http.MethodGet, "/protected", c.Protected, c.RequireAuth)
	public := web.NewRoute(http.MethodGet, "/public", c.Public)
	routes := []web.Route{protected}
	alias := routes[:0]
	alias = append(alias, public)
	return routes
}
func (c *Controller) AddressedRoutes() []web.Route {
	protected := web.NewRoute(http.MethodGet, "/protected", c.Protected, c.RequireAuth)
	public := web.NewRoute(http.MethodGet, "/public", c.Public)
	routes := []web.Route{protected}
	pointer := &routes[0]
	*pointer = public
	return routes
}
func (c *Controller) NestedRoutes() []web.Route {
	protected := web.NewRoute(http.MethodGet, "/protected", c.Protected, c.RequireAuth)
	public := web.NewRoute(http.MethodGet, "/public", c.Public)
	routes := []web.Route{protected}
	{
		routes[0] = public
	}
	return routes
}
func (c *Controller) SwappedRoutes() []web.Route {
	protected := web.NewRoute(http.MethodGet, "/protected", c.Protected, c.RequireAuth)
	public := web.NewRoute(http.MethodGet, "/public", c.Public)
	first := []web.Route{protected}
	second := []web.Route{public}
	first, second = second, first
	return second
}
func (c *Controller) Protected(ctx web.Context) error { return ctx.NoContent(http.StatusNoContent) }
func (c *Controller) Public(ctx web.Context) error { return ctx.NoContent(http.StatusNoContent) }
func (c *Controller) RequireAuth(next web.Handler) web.Handler { return next }
`,
		"app/routes.go": `package app
import (
	"github.com/goforj/web"
	"example.com/providerrouteflow/internal/report"
)
func ProvideRoutes(controller *report.Controller) []web.RouteGroup {
	return []web.RouteGroup{
		web.NewRouteGroup("/mutated", controller.MutatedRoutes()),
		web.NewRouteGroup("/copied", controller.CopiedRoutes()),
		web.NewRouteGroup("/escaped", controller.EscapedRoutes()),
		web.NewRouteGroup("/sliced", controller.SlicedRoutes()),
		web.NewRouteGroup("/addressed", controller.AddressedRoutes()),
		web.NewRouteGroup("/nested", controller.NestedRoutes()),
		web.NewRouteGroup("/swapped", controller.SwappedRoutes()),
	}
}
`,
	})

	manifest, err := Run(context.Background(), IndexOptions{Root: root, RouteCompositionPath: "app/routes.go"})
	if err != nil {
		t.Fatalf("lenient provider route flow: %v", err)
	}
	if len(manifest.Operations) != 1 || manifest.Operations[0].Path != "/swapped/protected" {
		t.Fatalf("unsafe or sequential route flow leaked into operations: %+v", manifest.Operations)
	}
	if got := manifest.Operations[0].Middleware; !reflect.DeepEqual(got, []string{"c.RequireAuth"}) {
		t.Fatalf("simultaneous route swap middleware = %v", got)
	}
	unsafeDiagnostics := 0
	controlDiagnostics := 0
	for _, diagnostic := range manifest.Diagnostics {
		switch diagnostic.Code {
		case "route_provider_unsafe_route_value_flow":
			unsafeDiagnostics++
		case "route_provider_unsupported_control_flow":
			controlDiagnostics++
		}
	}
	if unsafeDiagnostics != 5 || controlDiagnostics != 1 {
		t.Fatalf("provider route flow diagnostics unsafe=%d control=%d: %+v", unsafeDiagnostics, controlDiagnostics, manifest.Diagnostics)
	}

	_, err = Run(context.Background(), IndexOptions{Root: root, RouteCompositionPath: "app/routes.go", Strict: true})
	var diagnosticsError *DiagnosticsError
	if !errors.As(err, &diagnosticsError) {
		t.Fatalf("strict provider route flow error = %T %v", err, err)
	}
}

// TestRunExcludesRouteConstructorsInsideClosures keeps unscoped callback implementation details out of the runtime route surface.
func TestRunExcludesRouteConstructorsInsideClosures(t *testing.T) {
	root := t.TempDir()
	writeFixtureFiles(t, root, map[string]string{
		"go.mod": "module example.com/routeclosures\n\ngo 1.25\n",
		"routes/routes.go": `package routes
import (
	"net/http"
	"github.com/goforj/web"
)
func register(callback func()) {}
func Routes() []web.Route {
	real := web.NewRoute(http.MethodGet, "/real", realHandler)
	unused := func() web.Route { return web.NewRoute(http.MethodGet, "/unused", ghostHandler) }
	_ = unused
	register(func() { _ = web.NewRoute(http.MethodGet, "/callback", ghostHandler) })
	return []web.Route{real}
}
func BuildCallbacks() {
	register(func() { _ = web.NewRoute(http.MethodGet, "/closure-only", ghostHandler) })
}
func realHandler(ctx web.Context) error { return ctx.NoContent(http.StatusNoContent) }
func ghostHandler(ctx web.Context) error { return ctx.NoContent(http.StatusInternalServerError) }
`,
	})

	manifest, err := Run(context.Background(), IndexOptions{Root: root, Strict: true})
	if err != nil {
		t.Fatalf("strict route closure index: %v", err)
	}
	if len(manifest.Operations) != 1 || manifest.Operations[0].Path != "/real" {
		t.Fatalf("nested route constructors leaked into operations: %+v", manifest.Operations)
	}
}

// TestRunRejectsMutatedInlineCompositionRoutes applies the same fail-closed route-value policy before group placement is recorded.
func TestRunRejectsMutatedInlineCompositionRoutes(t *testing.T) {
	root := t.TempDir()
	writeFixtureFiles(t, root, map[string]string{
		"go.mod": "module example.com/inlineroutemutation\n\ngo 1.25\n",
		"app/routes.go": `package app
import (
	"net/http"
	"github.com/goforj/web"
)
func ProvideRoutes() []web.RouteGroup {
	protected := web.NewRoute(http.MethodGet, "/protected", protectedHandler, requireAuth)
	public := web.NewRoute(http.MethodGet, "/public", publicHandler)
	routes := []web.Route{protected}
	routes[0] = public
	return []web.RouteGroup{web.NewRouteGroup("/api", routes)}
}
func protectedHandler(ctx web.Context) error { return ctx.NoContent(http.StatusNoContent) }
func publicHandler(ctx web.Context) error { return ctx.NoContent(http.StatusNoContent) }
func requireAuth(next web.Handler) web.Handler { return next }
`,
	})

	manifest, err := Run(context.Background(), IndexOptions{Root: root, RouteCompositionPath: "app/routes.go"})
	if err != nil {
		t.Fatalf("lenient inline route mutation: %v", err)
	}
	if len(manifest.Operations) != 0 || !manifestHasDiagnostic(manifest, "route_composition_unsafe_route_value_flow") {
		t.Fatalf("inline route mutation did not fail closed: operations=%+v diagnostics=%+v", manifest.Operations, manifest.Diagnostics)
	}
	_, err = Run(context.Background(), IndexOptions{Root: root, RouteCompositionPath: "app/routes.go", Strict: true})
	var diagnosticsError *DiagnosticsError
	if !errors.As(err, &diagnosticsError) {
		t.Fatalf("strict inline route mutation error = %T %v", err, err)
	}
}
