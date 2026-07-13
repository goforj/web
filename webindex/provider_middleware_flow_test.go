package webindex

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

// TestRunBindsProviderVariadicMiddlewareActuals preserves composition aliases, empty calls, duplicates, static expansions, and assignment-time values.
func TestRunBindsProviderVariadicMiddlewareActuals(t *testing.T) {
	root := t.TempDir()
	writeFixtureFiles(t, root, map[string]string{
		"go.mod": "module example.com/middlewareflow\n\ngo 1.24\n",
		"internal/report/controller.go": `package report
import (
	"net/http"
	forj "github.com/goforj/web"
)
type Controller struct{}
func (c *Controller) Routes(officer ...forj.Middleware) []forj.Route {
	return []forj.Route{
		forj.NewRoute(http.MethodGet, "/open", c.Show),
		forj.NewRoute(http.MethodPost, "/officer", c.Show, officer...),
	}
}
func (c *Controller) DuplicateRoutes(policies ...forj.Middleware) []forj.Route { return []forj.Route{forj.NewRoute(http.MethodPost, "/duplicate", c.Show, policies...)} }
func (c *Controller) EmptyRoutes(policies ...forj.Middleware) []forj.Route { return []forj.Route{forj.NewRoute(http.MethodPost, "/empty", c.Show, policies...)} }
func (c *Controller) SliceRoutes(policies ...forj.Middleware) []forj.Route { return []forj.Route{forj.NewRoute(http.MethodPost, "/slice", c.Show, policies...)} }
func (c *Controller) BeforeRoutes(policies ...forj.Middleware) []forj.Route { return []forj.Route{forj.NewRoute(http.MethodPost, "/before", c.Show, policies...)} }
func (c *Controller) AfterRoutes(policies ...forj.Middleware) []forj.Route { return []forj.Route{forj.NewRoute(http.MethodPost, "/after", c.Show, policies...)} }
func (c *Controller) Show(ctx forj.Context) error { return ctx.NoContent(http.StatusNoContent) }
`,
		"internal/policy/service.go": `package policy
import "github.com/goforj/web"
type Service struct{}
func (s *Service) Group(next web.Handler) web.Handler { return next }
func (s *Service) Officer(next web.Handler) web.Handler { return next }
func (s *Service) First(next web.Handler) web.Handler { return next }
func (s *Service) Second(next web.Handler) web.Handler { return next }
func (s *Service) Old(next web.Handler) web.Handler { return next }
func (s *Service) New(next web.Handler) web.Handler { return next }
`,
		"app/routes.go": `package app
import (
	"slices"
	forj "github.com/goforj/web"
	"example.com/middlewareflow/internal/policy"
	"example.com/middlewareflow/internal/report"
)
func ProvideRoutes(controller *report.Controller, service *policy.Service) []forj.RouteGroup {
	officer := service.Officer
	alias := officer
	primary := controller.Routes(alias)
	duplicate := controller.DuplicateRoutes(service.First, alias, service.First)
	empty := controller.EmptyRoutes()
	policies := []forj.Middleware{service.First, service.Second, service.First}
	sliced := controller.SliceRoutes(policies...)
	changing := service.Old
	before := controller.BeforeRoutes(changing)
	changing = service.New
	after := controller.AfterRoutes(changing)
	routes := slices.Concat(primary, duplicate, empty, sliced, before, after)
	return []forj.RouteGroup{forj.NewRouteGroup("/api", routes, service.Group)}
}
`,
	})

	manifest, err := Run(context.Background(), IndexOptions{Root: root, RouteCompositionPath: "app/routes.go", Strict: true})
	if err != nil {
		t.Fatalf("strict index provider middleware bindings: %v", err)
	}
	wanted := map[string][]string{
		"/api/open":      {"service.Group"},
		"/api/officer":   {"service.Group", "service.Officer"},
		"/api/duplicate": {"service.Group", "service.First", "service.Officer", "service.First"},
		"/api/empty":     {"service.Group"},
		"/api/slice":     {"service.Group", "service.First", "service.Second", "service.First"},
		"/api/before":    {"service.Group", "service.Old"},
		"/api/after":     {"service.Group", "service.New"},
	}
	for path, middlewares := range wanted {
		if got := operationByPath(t, manifest, path).Middleware; !reflect.DeepEqual(got, middlewares) {
			t.Fatalf("%s middleware = %v, want %v", path, got, middlewares)
		}
	}
	if manifestHasDiagnostic(manifest, "dynamic_middleware_expansion") {
		t.Fatalf("exact provider middleware actuals remained dynamic: %+v", manifest.Diagnostics)
	}
}

// TestRunKeepsUnknownOrDecoyProviderMiddlewareExpansionsDynamic substitutes actual evidence only for a proven framework Middleware formal.
func TestRunKeepsUnknownOrDecoyProviderMiddlewareExpansionsDynamic(t *testing.T) {
	root := t.TempDir()
	writeFixtureFiles(t, root, map[string]string{
		"go.mod": "module example.com/middlewareunknown\n\ngo 1.24\n",
		"internal/decoy/middleware.go": `package decoy
import "github.com/goforj/web"
type Middleware = web.Middleware
`,
		"internal/report/controller.go": `package report
import (
	"net/http"
	"github.com/goforj/web"
	"example.com/middlewareunknown/internal/decoy"
)
type Controller struct{}
func (c *Controller) UnknownRoutes(guards ...web.Middleware) []web.Route { return []web.Route{web.NewRoute(http.MethodPost, "/unknown", c.Show, guards...)} }
func (c *Controller) DecoyRoutes(guards ...decoy.Middleware) []web.Route { return []web.Route{web.NewRoute(http.MethodPost, "/decoy", c.Show, guards...)} }
func (c *Controller) Show(ctx web.Context) error { return ctx.NoContent(http.StatusNoContent) }
`,
		"internal/policy/service.go": `package policy
import "github.com/goforj/web"
type Service struct{}
func (s *Service) Group(next web.Handler) web.Handler { return next }
func (s *Service) Policy(next web.Handler) web.Handler { return next }
`,
		"app/routes.go": `package app
import (
	"slices"
	"github.com/goforj/web"
	"example.com/middlewareunknown/internal/policy"
	"example.com/middlewareunknown/internal/report"
)
func ProvideRoutes(controller *report.Controller, service *policy.Service, policies []web.Middleware) []web.RouteGroup {
	unknown := controller.UnknownRoutes(policies...)
	decoy := controller.DecoyRoutes(service.Policy)
	return []web.RouteGroup{web.NewRouteGroup("/api", slices.Concat(unknown, decoy), service.Group)}
}
`,
	})

	manifest, err := Run(context.Background(), IndexOptions{Root: root, RouteCompositionPath: "app/routes.go"})
	if err != nil {
		t.Fatalf("lenient index dynamic provider middleware: %v", err)
	}
	if got := operationByPath(t, manifest, "/api/unknown").Middleware; !reflect.DeepEqual(got, []string{"service.Group", "policies..."}) {
		t.Fatalf("unknown actual evidence = %v", got)
	}
	if got := operationByPath(t, manifest, "/api/decoy").Middleware; !reflect.DeepEqual(got, []string{"service.Group", "guards..."}) {
		t.Fatalf("decoy formal was incorrectly bound: %v", got)
	}
	dynamicCount := 0
	for _, diagnostic := range manifest.Diagnostics {
		if diagnostic.Code == "dynamic_middleware_expansion" {
			dynamicCount++
		}
	}
	if dynamicCount != 2 {
		t.Fatalf("dynamic middleware diagnostics = %d: %+v", dynamicCount, manifest.Diagnostics)
	}

	_, err = Run(context.Background(), IndexOptions{Root: root, RouteCompositionPath: "app/routes.go", Strict: true})
	var diagnosticsError *DiagnosticsError
	if !errors.As(err, &diagnosticsError) {
		t.Fatalf("strict dynamic middleware must fail with diagnostics, got %v", err)
	}
}

// TestRunInvalidatesMutatedMiddlewareSequencesAndSnapshotsSwaps prevents stale slice contents while preserving simultaneous scalar assignment semantics.
func TestRunInvalidatesMutatedMiddlewareSequencesAndSnapshotsSwaps(t *testing.T) {
	root := t.TempDir()
	writeFixtureFiles(t, root, map[string]string{
		"go.mod": "module example.com/middlewaremutation\n\ngo 1.24\n",
		"internal/report/controller.go": `package report
import (
	"net/http"
	"github.com/goforj/web"
)
type Controller struct{}
func (c *Controller) MutatedRoutes(guards ...web.Middleware) []web.Route { return []web.Route{web.NewRoute(http.MethodPost, "/mutated", c.Show, guards...)} }
func (c *Controller) EscapedRoutes(guards ...web.Middleware) []web.Route { return []web.Route{web.NewRoute(http.MethodPost, "/escaped", c.Show, guards...)} }
func (c *Controller) SwappedRoutes(guards ...web.Middleware) []web.Route { return []web.Route{web.NewRoute(http.MethodPost, "/swapped", c.Show, guards...)} }
func (c *Controller) Show(ctx web.Context) error { return ctx.NoContent(http.StatusNoContent) }
`,
		"internal/policy/service.go": `package policy
import "github.com/goforj/web"
type Service struct{}
func (s *Service) First(next web.Handler) web.Handler { return next }
func (s *Service) Second(next web.Handler) web.Handler { return next }
`,
		"app/routes.go": `package app
import (
	"slices"
	"github.com/goforj/web"
	"example.com/middlewaremutation/internal/policy"
	"example.com/middlewaremutation/internal/report"
)
func touch(policies []web.Middleware) {}
func ProvideRoutes(controller *report.Controller, service *policy.Service) []web.RouteGroup {
	policies := []web.Middleware{service.First}
	policies[0] = service.Second
	mutated := controller.MutatedRoutes(policies...)
	escapedPolicies := []web.Middleware{service.First}
	touch(escapedPolicies)
	escaped := controller.EscapedRoutes(escapedPolicies...)
	first := service.First
	second := service.Second
	first, second = second, first
	swapped := controller.SwappedRoutes(first, second)
	return []web.RouteGroup{web.NewRouteGroup("/api", slices.Concat(mutated, escaped, swapped))}
}
`,
	})

	manifest, err := Run(context.Background(), IndexOptions{Root: root, RouteCompositionPath: "app/routes.go"})
	if err != nil {
		t.Fatalf("lenient index mutated middleware evidence: %v", err)
	}
	if got := operationByPath(t, manifest, "/api/mutated").Middleware; !reflect.DeepEqual(got, []string{"policies..."}) {
		t.Fatalf("mutated sequence published stale values: %v", got)
	}
	if got := operationByPath(t, manifest, "/api/escaped").Middleware; !reflect.DeepEqual(got, []string{"escapedPolicies..."}) {
		t.Fatalf("escaped sequence published stale values: %v", got)
	}
	if got := operationByPath(t, manifest, "/api/swapped").Middleware; !reflect.DeepEqual(got, []string{"service.Second", "service.First"}) {
		t.Fatalf("simultaneous assignment was evaluated sequentially: %v", got)
	}
	dynamicCount := 0
	for _, diagnostic := range manifest.Diagnostics {
		if diagnostic.Code == "dynamic_middleware_expansion" {
			dynamicCount++
		}
	}
	if dynamicCount != 2 {
		t.Fatalf("mutated sequence diagnostics = %d: %+v", dynamicCount, manifest.Diagnostics)
	}
}

// TestRunDoesNotBindChangedProviderMiddlewareFormals keeps evidence dynamic when provider code can alter or expose the variadic slice before route construction.
func TestRunDoesNotBindChangedProviderMiddlewareFormals(t *testing.T) {
	root := t.TempDir()
	writeFixtureFiles(t, root, map[string]string{
		"go.mod": "module example.com/providerformalmovement\n\ngo 1.24\n",
		"internal/report/controller.go": `package report
import (
	"net/http"
	"github.com/goforj/web"
)
type Controller struct{}
func observe(guards []web.Middleware) {}
func (c *Controller) MutatedRoutes(guards ...web.Middleware) []web.Route {
	guards[0] = c.Local
	return []web.Route{web.NewRoute(http.MethodPost, "/mutated", c.Show, guards...)}
}
func (c *Controller) ReboundRoutes(guards ...web.Middleware) []web.Route {
	guards = append(guards, c.Local)
	return []web.Route{web.NewRoute(http.MethodPost, "/rebound", c.Show, guards...)}
}
func (c *Controller) EscapedRoutes(guards ...web.Middleware) []web.Route {
	observe(guards)
	return []web.Route{web.NewRoute(http.MethodPost, "/escaped", c.Show, guards...)}
}
func (c *Controller) Local(next web.Handler) web.Handler { return next }
func (c *Controller) Show(ctx web.Context) error { return ctx.NoContent(http.StatusNoContent) }
`,
		"internal/policy/service.go": `package policy
import "github.com/goforj/web"
type Service struct{}
func (s *Service) RequireAuth(next web.Handler) web.Handler { return next }
`,
		"app/routes.go": `package app
import (
	"slices"
	"github.com/goforj/web"
	"example.com/providerformalmovement/internal/policy"
	"example.com/providerformalmovement/internal/report"
)
func ProvideRoutes(controller *report.Controller, service *policy.Service) []web.RouteGroup {
	mutated := controller.MutatedRoutes(service.RequireAuth)
	rebound := controller.ReboundRoutes(service.RequireAuth)
	escaped := controller.EscapedRoutes(service.RequireAuth)
	return []web.RouteGroup{web.NewRouteGroup("/api", slices.Concat(mutated, rebound, escaped))}
}
`,
	})

	manifest, err := Run(context.Background(), IndexOptions{Root: root, RouteCompositionPath: "app/routes.go"})
	if err != nil {
		t.Fatalf("lenient index changed provider middleware formals: %v", err)
	}
	for _, path := range []string{"/api/mutated", "/api/rebound", "/api/escaped"} {
		if got := operationByPath(t, manifest, path).Middleware; !reflect.DeepEqual(got, []string{"guards..."}) {
			t.Fatalf("%s published a stale provider binding: %v", path, got)
		}
	}
	dynamicCount := 0
	for _, diagnostic := range manifest.Diagnostics {
		if diagnostic.Code == "dynamic_middleware_expansion" {
			dynamicCount++
		}
	}
	if dynamicCount != 3 {
		t.Fatalf("changed provider formal diagnostics = %d: %+v", dynamicCount, manifest.Diagnostics)
	}
}

// TestRunAppliesConservativeMiddlewareFlowToHistoricalRegistries keeps legacy AppRoutes provider calls aligned with modern mutation and assignment semantics.
func TestRunAppliesConservativeMiddlewareFlowToHistoricalRegistries(t *testing.T) {
	root := t.TempDir()
	writeFixtureFiles(t, root, map[string]string{
		"go.mod": "module example.com/historicalmiddlewareflow\n\ngo 1.24\n",
		"internal/report/controller.go": `package report
import (
	"net/http"
	"github.com/goforj/web"
)
type Controller struct{}
func (c *Controller) MutatedRoutes(guards ...web.Middleware) []web.Route { return []web.Route{web.NewRoute(http.MethodPost, "/mutated", c.Show, guards...)} }
func (c *Controller) SwappedRoutes(guards ...web.Middleware) []web.Route { return []web.Route{web.NewRoute(http.MethodPost, "/swapped", c.Show, guards...)} }
func (c *Controller) Show(ctx web.Context) error { return ctx.NoContent(http.StatusNoContent) }
`,
		"internal/policy/service.go": `package policy
import "github.com/goforj/web"
type Service struct{}
func (s *Service) First(next web.Handler) web.Handler { return next }
func (s *Service) Second(next web.Handler) web.Handler { return next }
`,
		"app/routes.go": `package app
import (
	"github.com/goforj/web"
	"example.com/historicalmiddlewareflow/internal/policy"
	"example.com/historicalmiddlewareflow/internal/report"
)
type AppRoutes struct {
	mutated []web.Route
	swapped []web.Route
}
func ProvideAppRoutes(controller *report.Controller, service *policy.Service) *AppRoutes {
	policies := []web.Middleware{service.First}
	policies[0] = service.Second
	mutated := controller.MutatedRoutes(policies...)
	first := service.First
	second := service.Second
	first, second = second, first
	swapped := controller.SwappedRoutes(first, second)
	return &AppRoutes{mutated: mutated, swapped: swapped}
}
func ProvideRoutes(routes *AppRoutes) []web.RouteGroup {
	return []web.RouteGroup{
		web.NewRouteGroup("/api", routes.mutated),
		web.NewRouteGroup("/api", routes.swapped),
	}
}
`,
	})

	manifest, err := Run(context.Background(), IndexOptions{Root: root, RouteCompositionPath: "app/routes.go"})
	if err != nil {
		t.Fatalf("lenient index historical middleware flow: %v", err)
	}
	if got := operationByPath(t, manifest, "/api/mutated").Middleware; !reflect.DeepEqual(got, []string{"policies..."}) {
		t.Fatalf("historical mutation published stale values: %v", got)
	}
	if got := operationByPath(t, manifest, "/api/swapped").Middleware; !reflect.DeepEqual(got, []string{"service.Second", "service.First"}) {
		t.Fatalf("historical simultaneous assignment was evaluated sequentially: %v", got)
	}
	dynamicCount := 0
	for _, diagnostic := range manifest.Diagnostics {
		if diagnostic.Code == "dynamic_middleware_expansion" {
			dynamicCount++
		}
	}
	if dynamicCount != 1 {
		t.Fatalf("historical mutation diagnostics = %d: %+v", dynamicCount, manifest.Diagnostics)
	}
}
