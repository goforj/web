package webindex

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRunIndexesRoutesAndHandlerMetadata exercises the native route, request, and response indexing path end to end.
func TestRunIndexesRoutesAndHandlerMetadata(t *testing.T) {
	root := t.TempDir()

	files := map[string]string{
		"go.mod": "module example.com/test\n\ngo 1.24\n",
		"internal/hello/controller.go": `package hello

import (
	"net/http"
	"github.com/goforj/web"
)

type Controller struct {}

func (c *Controller) Routes() []web.Route {
	return []web.Route{
		web.NewRoute(http.MethodGet, "/hello/:name", c.Hello),
	}
}

type requestPayload struct { Name string ` + "`json:\"name\"`" + ` }
type responsePayload struct { Message string ` + "`json:\"message\"`" + ` }

func (c *Controller) Hello(ctx web.Context) error {
	name := ctx.Param("name")
	filter := ctx.Query("filter")
	var req requestPayload
	if err := ctx.Bind(&req); err != nil {
		return ctx.JSON(http.StatusBadRequest, map[string]string{"error":"bad request"})
	}
	_ = name
	_ = filter
	return ctx.JSON(http.StatusOK, responsePayload{Message: "ok"})
}
`,
		"internal/router/routes_registry.go": `package router
import "github.com/goforj/web"

func ProvideRoutes() []any {
	groups := []any{}
	groups = append(groups, web.NewRouteGroup("/api/v1", nil))
	return groups
}
`,
	}

	for rel, contents := range files {
		abs := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(abs, []byte(contents), 0644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	out := filepath.Join(root, "build", "api_index.json")
	diag := filepath.Join(root, "build", "api_index.diagnostics.json")
	openapi := filepath.Join(root, "build", "openapi.json")

	manifest, err := Run(context.Background(), IndexOptions{
		Root:            root,
		OutPath:         out,
		DiagnosticsPath: diag,
		OpenAPIPath:     openapi,
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if len(manifest.Operations) != 1 {
		t.Fatalf("expected 1 operation, got %d", len(manifest.Operations))
	}

	op := manifest.Operations[0]
	if op.Path != "/api/v1/hello/:name" {
		t.Fatalf("unexpected path: %s", op.Path)
	}
	if op.Method != "GET" {
		t.Fatalf("unexpected method: %s", op.Method)
	}
	if op.Handler.Function != "Hello" {
		t.Fatalf("unexpected handler function: %s", op.Handler.Function)
	}
	if len(op.Inputs.PathParams) != 1 || op.Inputs.PathParams[0].Name != "name" {
		t.Fatalf("expected path param name")
	}
	if len(op.Inputs.QueryParams) != 1 || op.Inputs.QueryParams[0].Name != "filter" {
		t.Fatalf("expected query param filter")
	}
	if op.Inputs.Body == nil || op.Inputs.Body.TypeName != "requestPayload" {
		t.Fatalf("expected request body type requestPayload, got %+v", op.Inputs.Body)
	}
	if len(op.Outputs.Responses) < 2 {
		t.Fatalf("expected at least 2 responses, got %d", len(op.Outputs.Responses))
	}

	for _, path := range []string{out, diag, openapi} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected output file %s: %v", path, err)
		}
	}
}

// TestRunSkipsCustomDirs verifies caller-owned source exclusions cannot leak ignored routes into published contracts.
func TestRunSkipsCustomDirs(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/test\n\ngo 1.25\n",
		"internal/hello/controller.go": `package hello
import (
	"net/http"
	"github.com/goforj/web"
)
type Controller struct{}
func (c *Controller) Routes() []any {
	return []any{
		web.NewRoute(http.MethodGet, "/hello", c.Hello),
	}
}
func (c *Controller) Hello(ctx any) error { return nil }`,
		"_data/private/ignored.go": `package private
func broken(`,
	}
	writeFixtureFiles(t, root, files)

	privateDir := filepath.Join(root, "_data", "private")
	if err := os.Chmod(privateDir, 0); err != nil {
		t.Fatalf("chmod private dir: %v", err)
	}
	defer func() { _ = os.Chmod(privateDir, 0o755) }()

	manifest, err := Run(context.Background(), IndexOptions{
		Root: root,
		SkipDir: func(_ string, name string) bool {
			return name == "_data"
		},
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if len(manifest.Operations) != 1 {
		t.Fatalf("expected one operation, got %d", len(manifest.Operations))
	}
}

// TestRunMapsRoutesToSpecificGroupsByControllerOwner verifies composition prefixes follow exact providers instead of receiver-name guesses.
func TestRunMapsRoutesToSpecificGroupsByControllerOwner(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/test\n\ngo 1.24\n",
		"internal/hello/controller.go": `package hello
import (
	"net/http"
	"github.com/goforj/web"
)
type Controller struct{}
func (c *Controller) Routes() []any {
	return []any{
		web.NewRoute(http.MethodGet, "/things", c.Index),
	}
}
func (c *Controller) Index(ctx any) error { return nil }`,
		"internal/admin/controller.go": `package admin
import (
	"net/http"
	"github.com/goforj/web"
)
type Controller struct{}
func (c *Controller) Routes() []any {
	return []any{
		web.NewRoute(http.MethodGet, "/things", c.Index),
	}
}
func (c *Controller) Index(ctx any) error { return nil }`,
		"internal/router/routes_registry.go": `package router
import "github.com/goforj/web"
func ProvideAppRoutes(
	helloController *hello.Controller,
	adminController *admin.Controller,
) *AppRoutes {
	var app []any
	var adminRoutes []any
	app = append(app, helloController.Routes()...)
	adminRoutes = append(adminRoutes, adminController.Routes()...)
	return &AppRoutes{
		app: app,
		admin: adminRoutes,
	}
}
type AppRoutes struct {
	app []any
	admin []any
}
func ProvideRoutes(r *AppRoutes) []any {
	groups := []any{}
	groups = append(groups, web.NewRouteGroup("/api/v1", r.app))
	groups = append(groups, web.NewRouteGroup("/api/admin", r.admin))
	return groups
}
`,
	}
	for rel, contents := range files {
		abs := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(abs, []byte(contents), 0644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	manifest, err := Run(context.Background(), IndexOptions{Root: root})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if len(manifest.Operations) != 2 {
		t.Fatalf("expected 2 operations, got %d", len(manifest.Operations))
	}

	found := map[string]bool{}
	for _, op := range manifest.Operations {
		found[op.Path] = true
	}
	if !found["/api/v1/things"] {
		t.Fatalf("expected /api/v1/things route")
	}
	if !found["/api/admin/things"] {
		t.Fatalf("expected /api/admin/things route")
	}
}

// TestRunKeepsGeneratedPublicAndProtectedRouteProvidersDistinct verifies the generated app composition shape without collapsing policy to the controller owner.
func TestRunKeepsGeneratedPublicAndProtectedRouteProvidersDistinct(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/test\n\ngo 1.25\n",
		"internal/monitoring/controller.go": `package monitoring
import (
	"net/http"
	"github.com/goforj/web"
)
type Controller struct{}
func (c *Controller) PublicRoutes() []web.Route {
	return []web.Route{web.NewRoute(http.MethodGet, "/status", c.Status)}
}
func (c *Controller) ProtectedRoutes() []web.Route {
	return []web.Route{web.NewRoute(http.MethodGet, "/monitors", c.Index)}
}
func (c *Controller) InternalRoutes() []web.Route {
	return []web.Route{web.NewRoute(http.MethodGet, "/internal", c.Internal)}
}
func (c *Controller) Status(ctx any) error { return nil }
func (c *Controller) Index(ctx any) error { return nil }
func (c *Controller) Internal(ctx any) error { return nil }`,
		"internal/archive/monitoring/controller.go": `package monitoring
import (
	"net/http"
	"github.com/goforj/web"
)
type Controller struct{}
func (c *Controller) PublicRoutes() []web.Route {
	return []web.Route{web.NewRoute(http.MethodGet, "/archive-leak", c.Status)}
}
func (c *Controller) Status(ctx any) error { return nil }`,
		"internal/auth/service.go": `package auth
type Service struct{}
func (s *Service) RequireAuth(next any) any { return next }`,
		"internal/starterui/controller.go": `package starterui
import (
	"net/http"
	"github.com/goforj/web"
)
type Controller struct{}
func (c *Controller) Routes() []web.Route {
	return []web.Route{web.NewRoute(http.MethodGet, "/home", c.Home)}
}
func (c *Controller) Home(ctx any) error { return nil }`,
		"app/routes.go": `package app
import "github.com/goforj/web"
import (
	"slices"
	"github.com/goforj/web"
	"example.com/test/internal/auth"
	"example.com/test/internal/monitoring"
	"example.com/test/internal/starterui"
)
func ProvideRoutes(
	monitoringController *monitoring.Controller,
	starterUIController *starterui.Controller,
	authService *auth.Service,
) []web.RouteGroup {
	var pageRoutes = starterUIController.Routes()
	groups := []web.RouteGroup{
		web.NewRouteGroup("", pageRoutes),
	}
	publicRoutes := slices.Concat(monitoringController.PublicRoutes())
	groups = append(groups, web.NewRouteGroup("/api/v1", publicRoutes))
	protectedRoutes := slices.Concat(monitoringController.ProtectedRoutes())
	groups = append(groups, web.NewRouteGroup("/api/v1", protectedRoutes, authService.RequireAuth))
	unusedRoutes := slices.Concat(monitoringController.InternalRoutes())
	_ = unusedRoutes
	return groups
}`,
	}
	writeFixtureFiles(t, root, files)

	manifest, err := Run(context.Background(), IndexOptions{
		Root:                 root,
		RouteCompositionPath: "app/routes.go",
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if len(manifest.Operations) != 3 {
		t.Fatalf("expected only the three returned provider methods, got %d operations: %#v", len(manifest.Operations), manifest.Operations)
	}

	operations := map[string]Operation{}
	for _, operation := range manifest.Operations {
		operations[operation.Path] = operation
	}
	publicOperation, ok := operations["/api/v1/status"]
	if !ok {
		t.Fatalf("expected returned public route, got %#v", operations)
	}
	if len(publicOperation.Middleware) != 0 {
		t.Fatalf("expected public route without group middleware, got %#v", publicOperation.Middleware)
	}
	if !strings.HasSuffix(filepath.ToSlash(publicOperation.Handler.File), "internal/monitoring/controller.go") {
		t.Fatalf("expected handler metadata from the selected import path, got %q", publicOperation.Handler.File)
	}
	pageOperation, ok := operations["/home"]
	if !ok {
		t.Fatalf("expected root-prefix page route from the initial group composite, got %#v", operations)
	}
	if len(pageOperation.Middleware) != 0 {
		t.Fatalf("expected page route without group middleware, got %#v", pageOperation.Middleware)
	}
	protectedOperation, ok := operations["/api/v1/monitors"]
	if !ok {
		t.Fatalf("expected returned protected route, got %#v", operations)
	}
	if len(protectedOperation.Middleware) != 1 || protectedOperation.Middleware[0] != "authService.RequireAuth" {
		t.Fatalf("expected protected middleware only on protected route, got %#v", protectedOperation.Middleware)
	}
	if _, ok := operations["/api/v1/internal"]; ok {
		t.Fatalf("unused route variables must not enter the selected app index")
	}
	if _, ok := operations["/api/v1/archive-leak"]; ok {
		t.Fatalf("same-named packages outside the selected import must not enter the app index")
	}
	for _, diagnostic := range manifest.Diagnostics {
		if diagnostic.Code == "handler_ambiguous" && diagnostic.Operation == publicOperation.ID {
			t.Fatalf("exact handler import identity should prevent ambiguity: %#v", diagnostic)
		}
	}
}

// TestRunDiagnosesUnsupportedRouteComposition ensures an opaque returned expression reports the outer flow boundary without treating its arguments as returned groups.
func TestRunDiagnosesUnsupportedRouteComposition(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/test\n\ngo 1.25\n",
		"internal/hello/controller.go": `package hello
import (
	"net/http"
	"github.com/goforj/web"
)
type Controller struct{}
func (c *Controller) Routes() []web.Route {
	return []web.Route{web.NewRoute(http.MethodGet, "/hello", c.Index)}
}
func (c *Controller) Index(ctx any) error { return nil }`,
		"app/routes.go": `package app
import "github.com/goforj/web"
import (
	"github.com/goforj/web"
	"example.com/test/internal/hello"
)
const routePrefix = "/api"
func decorate(routes []web.Route) []web.Route { return routes }
func finish(groups []web.RouteGroup) []web.RouteGroup { return groups }
func ProvideRoutes(helloController *hello.Controller) []web.RouteGroup {
	var wrappedRoutes = decorate(helloController.Routes())
	groups := []web.RouteGroup{
		web.NewRouteGroup(routePrefix, helloController.Routes()),
		web.NewRouteGroup("/api", wrappedRoutes),
	}
	return finish(groups)
}`,
	}
	writeFixtureFiles(t, root, files)

	manifest, err := Run(context.Background(), IndexOptions{
		Root:                 root,
		RouteCompositionPath: "app/routes.go",
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	wantedCodes := map[string]bool{
		"route_composition_unsupported_return_expression": false,
		"route_composition_no_returned_groups":            false,
	}
	for _, diagnostic := range manifest.Diagnostics {
		if _, ok := wantedCodes[diagnostic.Code]; ok {
			wantedCodes[diagnostic.Code] = true
		}
		if diagnostic.Code == "route_composition_dynamic_prefix" || diagnostic.Code == "route_composition_unsupported_routes_expression" {
			t.Fatalf("opaque return flow must not activate nested group diagnostics, got %#v", manifest.Diagnostics)
		}
	}
	for code, found := range wantedCodes {
		if !found {
			t.Fatalf("expected %s diagnostic, got %#v", code, manifest.Diagnostics)
		}
	}
}

// TestRunScopesRoutesToCompositionFile verifies selecting one App excludes providers reachable only from another App entrypoint.
func TestRunScopesRoutesToCompositionFile(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/test\n\ngo 1.25\n",
		"internal/hello/controller.go": `package hello
import (
	"net/http"
	"github.com/goforj/web"
)
type Controller struct{}
func (c *Controller) Routes() []any {
	return []any{
		web.NewRoute(http.MethodGet, "/hello", c.Index),
	}
}
func (c *Controller) Index(ctx any) error { return nil }`,
		"internal/reports/controller.go": `package reports
import (
	"net/http"
	"github.com/goforj/web"
)
type Controller struct{}
func (c *Controller) Routes() []any {
	return []any{
		web.NewRoute(http.MethodGet, "/reports", c.Index),
	}
}
func (c *Controller) Index(ctx any) error { return nil }`,
		"internal/leak/controller.go": `package leak
import (
	"net/http"
	"github.com/goforj/web"
)
type Controller struct{}
func (c *Controller) Routes() []any {
	return []any{
		web.NewRoute(http.MethodGet, "/leak", c.Index),
	}
}
func (c *Controller) Index(ctx any) error { return nil }`,
		"app/routes.go": `package app
import "github.com/goforj/web"
func ProvideRoutes(helloController *hello.Controller) []web.RouteGroup {
	return []web.RouteGroup{
		web.NewRouteGroup("/api/v1", helloController.Routes()),
	}
}`,
		"app/customer-portal/routes.go": `package customerportal
import "github.com/goforj/web"
import "slices"
func ProvideRoutes(reportsController *reports.Controller, leakController *leak.Controller, authService *auth.Service) []web.RouteGroup {
	deadRoutes := slices.Concat(leakController.Routes())
	_ = deadRoutes
	reportRoutes := slices.Concat(reportsController.Routes())
	return []web.RouteGroup{
		web.NewRouteGroup("/api/customer", reportRoutes, authService.RequireAuth),
	}
}`,
	}
	writeFixtureFiles(t, root, files)

	manifest, err := Run(context.Background(), IndexOptions{
		Root:                 root,
		RouteCompositionPath: "app/customer-portal/routes.go",
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if len(manifest.Operations) != 1 {
		t.Fatalf("expected one operation, got %d", len(manifest.Operations))
	}
	op := manifest.Operations[0]
	if op.Path != "/api/customer/reports" {
		t.Fatalf("expected customer route only, got %s", op.Path)
	}
	if len(op.Middleware) != 1 || op.Middleware[0] != "authService.RequireAuth" {
		t.Fatalf("expected scoped middleware, got %#v", op.Middleware)
	}
}

// TestRunFallsBackToUnprefixedPathWhenGroupMappingMissing verifies syntax-only indexing remains useful when no composition boundary is requested.
func TestRunFallsBackToUnprefixedPathWhenGroupMappingMissing(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/test\n\ngo 1.24\n",
		"internal/hello/controller.go": `package hello
import (
	"net/http"
	"github.com/goforj/web"
)
type Controller struct{}
func (c *Controller) Routes() []any {
	return []any{
		web.NewRoute(http.MethodGet, "/raw", c.Raw),
	}
}
func (c *Controller) Raw(ctx any) error { return nil }`,
		"internal/router/routes_registry.go": `package router
import "github.com/goforj/web"
func ProvideRoutes() []any {
	groups := []any{}
	groups = append(groups, web.NewRouteGroup("/api/v1", nil))
	groups = append(groups, web.NewRouteGroup("/api/admin", nil))
	return groups
}
`,
	}
	for rel, contents := range files {
		abs := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(abs, []byte(contents), 0644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	manifest, err := Run(context.Background(), IndexOptions{Root: root})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if len(manifest.Operations) != 1 {
		t.Fatalf("expected 1 operation, got %d", len(manifest.Operations))
	}
	if manifest.Operations[0].Path != "/raw" {
		t.Fatalf("expected unprefixed fallback path /raw, got %s", manifest.Operations[0].Path)
	}
}

// TestRunEmitsAmbiguousHandlerDiagnostic verifies duplicate handler names fail closed instead of attaching a guessed contract.
func TestRunEmitsAmbiguousHandlerDiagnostic(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/test\n\ngo 1.24\n",
		"internal/a/handler.go": `package a
func Ping(ctx any) error { return nil }`,
		"internal/b/handler.go": `package b
func Ping(ctx any) error { return nil }`,
		"internal/router/routes.go": `package router
import "github.com/goforj/web"
import (
	"net/http"
	"github.com/goforj/web"
)
func Routes() []any {
	return []any{
		web.NewRoute(http.MethodGet, "/ping", Ping),
	}
}
`,
	}
	for rel, contents := range files {
		abs := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(abs, []byte(contents), 0644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	manifest, err := Run(context.Background(), IndexOptions{Root: root})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	found := false
	for _, d := range manifest.Diagnostics {
		if d.Code == "handler_ambiguous" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected handler_ambiguous diagnostic, got %+v", manifest.Diagnostics)
	}
}

// TestRunExtractsStringAndNoContentResponses verifies native text and bodyless response methods retain their statuses.
func TestRunExtractsStringAndNoContentResponses(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/test\n\ngo 1.24\n",
		"internal/hello/controller.go": `package hello
import (
	"net/http"
	"github.com/goforj/web"
)
type Controller struct{}
func (c *Controller) Routes() []web.Route {
	return []web.Route{
		web.NewRoute(http.MethodGet, "/status", c.Status),
	}
}
func (c *Controller) Status(ctx web.Context) error {
	if ctx.Query("fmt") == "text" {
		return ctx.Text(http.StatusOK, "ok")
	}
	return ctx.NoContent(http.StatusNoContent)
}`,
	}
	for rel, contents := range files {
		abs := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(abs, []byte(contents), 0644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	manifest, err := Run(context.Background(), IndexOptions{Root: root})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if len(manifest.Operations) != 1 {
		t.Fatalf("expected 1 operation, got %d", len(manifest.Operations))
	}
	op := manifest.Operations[0]
	has200 := false
	has204 := false
	for _, r := range op.Outputs.Responses {
		if r.StatusCode == 200 {
			has200 = true
		}
		if r.StatusCode == 204 {
			has204 = true
		}
	}
	if !has200 || !has204 {
		t.Fatalf("expected 200 and 204 responses, got %+v", op.Outputs.Responses)
	}
}

// TestRunWritesExpectedJSONShape protects the manifest contract consumed by downstream artifact tooling.
func TestRunWritesExpectedJSONShape(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/test\n\ngo 1.24\n",
		"internal/hello/controller.go": `package hello
import (
	"net/http"
	"github.com/goforj/web"
)
type Controller struct{}
func (c *Controller) Routes() []any {
	return []any{
		web.NewRoute(http.MethodGet, "/shape", c.Shape),
	}
}
func (c *Controller) Shape(ctx any) error { return nil }`,
	}
	for rel, contents := range files {
		abs := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(abs, []byte(contents), 0644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	out := filepath.Join(root, "build", "api_index.json")
	_, err := Run(context.Background(), IndexOptions{Root: root, OutPath: out})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read out: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}
	if decoded["version"] == nil || decoded["operations"] == nil || decoded["diagnostics"] == nil {
		t.Fatalf("missing required top-level keys in manifest: %v", decoded)
	}
}

// TestRunExtractsPathParamsFromRouteTemplate verifies declared route segments remain authoritative even when a handler never reads them.
func TestRunExtractsPathParamsFromRouteTemplate(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/test\n\ngo 1.24\n",
		"internal/hello/controller.go": `package hello
import (
	"net/http"
	"github.com/goforj/web"
)
type Controller struct{}
func (c *Controller) Routes() []any {
	return []any{
		web.NewRoute(http.MethodGet, "/teams/:teamID/users/:userID", c.Show),
	}
}
func (c *Controller) Show(ctx any) error { return nil }`,
	}
	for rel, contents := range files {
		abs := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(abs, []byte(contents), 0644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	manifest, err := Run(context.Background(), IndexOptions{Root: root})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if len(manifest.Operations) != 1 {
		t.Fatalf("expected 1 operation, got %d", len(manifest.Operations))
	}
	op := manifest.Operations[0]
	if len(op.Inputs.PathParams) != 2 {
		t.Fatalf("expected 2 path params, got %+v", op.Inputs.PathParams)
	}
	if op.Inputs.PathParams[0].Name != "teamID" || op.Inputs.PathParams[1].Name != "userID" {
		t.Fatalf("unexpected path params: %+v", op.Inputs.PathParams)
	}
}

// TestRunExtractsQueryParamsFromNativeQuery verifies the app-facing context API is the primary query evidence.
func TestRunExtractsQueryParamsFromNativeQuery(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/test\n\ngo 1.24\n",
		"internal/hello/controller.go": `package hello
import (
	"net/http"
	"github.com/goforj/web"
)
type Controller struct{}
func (c *Controller) Routes() []web.Route {
	return []web.Route{
		web.NewRoute(http.MethodGet, "/search", c.Search),
	}
}
func (c *Controller) Search(ctx web.Context) error {
	_ = ctx.Query("page")
	return ctx.NoContent(http.StatusNoContent)
}`,
	}
	for rel, contents := range files {
		abs := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(abs, []byte(contents), 0644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	manifest, err := Run(context.Background(), IndexOptions{Root: root})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if len(manifest.Operations) != 1 {
		t.Fatalf("expected 1 operation, got %d", len(manifest.Operations))
	}
	op := manifest.Operations[0]
	if len(op.Inputs.QueryParams) != 1 || op.Inputs.QueryParams[0].Name != "page" {
		t.Fatalf("expected query param page, got %+v", op.Inputs.QueryParams)
	}
}

// TestRunEmitsDynamicParamDiagnostics verifies native dynamic keys remain explicit instead of becoming guessed names.
func TestRunEmitsDynamicParamDiagnostics(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/test\n\ngo 1.24\n",
		"internal/hello/controller.go": `package hello
import (
	"net/http"
	"github.com/goforj/web"
)
type Controller struct{}
func (c *Controller) Routes() []web.Route {
	return []web.Route{
		web.NewRoute(http.MethodGet, "/search/:id", c.Search),
	}
}
func (c *Controller) Search(ctx web.Context) error {
	key := "q"
	_ = ctx.Query(key)
	headerKey := "X-Request-ID"
	_ = ctx.Header(headerKey)
	paramName := "id"
	_ = ctx.Param(paramName)
	return ctx.NoContent(http.StatusNoContent)
}`,
	}
	for rel, contents := range files {
		abs := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(abs, []byte(contents), 0644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	manifest, err := Run(context.Background(), IndexOptions{Root: root})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	codes := map[string]bool{}
	for _, d := range manifest.Diagnostics {
		if d.Code == "dynamic_param_key" {
			codes[d.Message] = true
		}
	}
	if len(codes) < 3 {
		t.Fatalf("expected dynamic param diagnostics, got %+v", manifest.Diagnostics)
	}
}
