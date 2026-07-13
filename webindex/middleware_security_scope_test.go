package webindex

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestRunScopesMiddlewareSecurityToItsSourceDeclaration proves identical provider-local spelling cannot inherit group authentication policy.
func TestRunScopesMiddlewareSecurityToItsSourceDeclaration(t *testing.T) {
	root := t.TempDir()
	writeFixtureFiles(t, root, map[string]string{
		"go.mod": "module example.com/scopedsecurity\n\ngo 1.25\n",
		"internal/auth/service.go": `package auth
import "github.com/goforj/web"
type Service struct{}
func (s *Service) RequireAuth(next web.Handler) web.Handler { return next }
`,
		"internal/report/controller.go": `package report
import (
	"net/http"
	"github.com/goforj/web"
)
type Controller struct{}
type Service struct{}
func (s *Service) RequireAuth(next web.Handler) web.Handler { return next }
func (c *Controller) PublicRoutes(authService *Service) []web.Route {
	return []web.Route{web.NewRoute(http.MethodGet, "/public", c.Public, authService.RequireAuth)}
}
func (c *Controller) ProtectedRoutes() []web.Route {
	return []web.Route{web.NewRoute(http.MethodGet, "/protected", c.Protected)}
}
func (c *Controller) Public(ctx web.Context) error { return ctx.NoContent(http.StatusNoContent) }
func (c *Controller) Protected(ctx web.Context) error { return ctx.NoContent(http.StatusNoContent) }
`,
		"app/routes.go": `package app
import (
	"net/http"
	"github.com/goforj/web"
	"example.com/scopedsecurity/internal/auth"
	"example.com/scopedsecurity/internal/report"
)
func ProvideRoutes(controller *report.Controller, authService *auth.Service, customAuthService *report.Service) []web.RouteGroup {
	public := controller.PublicRoutes(customAuthService)
	protected := controller.ProtectedRoutes()
	return []web.RouteGroup{
		web.NewRouteGroup("/api", public),
		web.NewRouteGroup("/api", protected, authService.RequireAuth),
		web.NewRouteGroup("/api", []web.Route{web.NewRoute(http.MethodGet, "/inline", inline)}, authService.RequireAuth),
	}
}
func inline(ctx web.Context) error { return ctx.NoContent(http.StatusNoContent) }
`,
	})

	options := scopedCookieSecurityOptions()
	openAPIPath := filepath.Join(t.TempDir(), "openapi.json")
	manifest, err := Run(context.Background(), IndexOptions{
		Root:                 root,
		RouteCompositionPath: "app/routes.go",
		OpenAPIPath:          openAPIPath,
		OpenAPI:              options,
		Strict:               true,
	})
	if err != nil {
		t.Fatalf("run source-scoped middleware security: %v", err)
	}
	if got := operationByPath(t, manifest, "/api/public").Middleware; !reflect.DeepEqual(got, []string{"authService.RequireAuth"}) {
		t.Fatalf("provider-local collision evidence = %v", got)
	}
	if got := operationByPath(t, manifest, "/api/protected").Middleware; !reflect.DeepEqual(got, []string{"authService.RequireAuth"}) {
		t.Fatalf("group middleware evidence = %v", got)
	}

	rawDocument, err := os.ReadFile(openAPIPath)
	if err != nil {
		t.Fatalf("read projected OpenAPI: %v", err)
	}
	var document OpenAPIDocument
	if err := json.Unmarshal(rawDocument, &document); err != nil {
		t.Fatalf("decode projected OpenAPI: %v", err)
	}
	if document.Paths["/api/public"]["get"].Security != nil {
		t.Fatalf("provider-local identical spelling inherited group security: %+v", document.Paths["/api/public"]["get"].Security)
	}
	for _, path := range []string{"/api/protected", "/api/inline"} {
		security := document.Paths[path]["get"].Security
		if security == nil || !reflect.DeepEqual(*security, []OpenAPISecurityRequirement{{"forjSession": {}}}) {
			t.Fatalf("%s group security = %+v", path, security)
		}
	}
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	if strings.Contains(string(manifestJSON), "middlewareProvenance") || strings.Contains(string(manifestJSON), "MiddlewareSource") {
		t.Fatalf("internal middleware provenance leaked into manifest JSON: %s", manifestJSON)
	}
}

// TestRunScopesHistoricalMiddlewareSecurity retains source identity through the generated AppRoutes registry flow.
func TestRunScopesHistoricalMiddlewareSecurity(t *testing.T) {
	root := t.TempDir()
	writeFixtureFiles(t, root, map[string]string{
		"go.mod": "module example.com/historicalsecurity\n\ngo 1.25\n",
		"internal/auth/service.go": `package auth
import "github.com/goforj/web"
type Service struct{}
func (s *Service) RequireAuth(next web.Handler) web.Handler { return next }
`,
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
	"example.com/historicalsecurity/internal/auth"
	"example.com/historicalsecurity/internal/report"
)
type AppRoutes struct { reports []web.Route }
func ProvideAppRoutes(controller *report.Controller) *AppRoutes {
	var reports []web.Route
	reports = append(reports, controller.Routes()...)
	return &AppRoutes{reports: reports}
}
func ProvideRoutes(routes *AppRoutes, authService *auth.Service) []web.RouteGroup {
	var groups []web.RouteGroup
	if len(routes.reports) > 0 {
		groups = append(groups, web.NewRouteGroup("/api", routes.reports, authService.RequireAuth))
	}
	return groups
}
`,
	})

	manifest, err := Run(context.Background(), IndexOptions{Root: root, RouteCompositionPath: "app/routes.go", Strict: true})
	if err != nil {
		t.Fatalf("run historical source-scoped middleware security fixture: %v", err)
	}
	document, err := ProjectOpenAPI(manifest, scopedCookieSecurityOptions())
	if err != nil {
		t.Fatalf("project historical source-scoped middleware security: %v", err)
	}
	security := document.Paths["/api/report"]["get"].Security
	if security == nil || !reflect.DeepEqual(*security, []OpenAPISecurityRequirement{{"forjSession": {}}}) {
		t.Fatalf("historical group security = %+v", security)
	}
}

// TestProjectOpenAPIRejectsUnsafeScopedMiddlewareSecurityRules keeps stale and overlapping selectors from silently changing authentication policy.
func TestProjectOpenAPIRejectsUnsafeScopedMiddlewareSecurityRules(t *testing.T) {
	requirements := []OpenAPISecurityRequirement{{"forjSession": {}}}
	operation := Operation{
		Method:     "GET",
		Path:       "/one",
		Middleware: []string{"authService.RequireAuth"},
		Outputs:    OutputShape{Responses: []ResponseShape{{StatusCode: 204}}},
		middlewareProvenance: []middlewareProvenance{{
			Expression: "authService.RequireAuth",
			File:       "app/routes.go",
			Function:   "ProvideRoutes",
			Receiver:   "First",
		}},
	}
	second := operation
	second.Path = "/two"
	second.middlewareProvenance = []middlewareProvenance{{
		Expression: "authService.RequireAuth",
		File:       "app/routes.go",
		Function:   "ProvideRoutes",
		Receiver:   "Second",
	}}
	base := OpenAPIOptions{SecuritySchemes: map[string]OpenAPISecurityScheme{
		"forjSession": {Type: "apiKey", In: "cookie", Name: "goforj_session"},
	}}

	tests := []struct {
		name       string
		operations []Operation
		configure  func(*OpenAPIOptions)
		problem    string
	}{
		{
			name:       "unmatched",
			operations: []Operation{operation},
			configure: func(options *OpenAPIOptions) {
				options.MiddlewareSecurityRules = []OpenAPIMiddlewareSecurityRule{{Expression: "authService.RequireAuth", SourceFile: "app/other.go", Function: "ProvideRoutes", Requirements: requirements}}
			},
			problem: "did not match",
		},
		{
			name:       "duplicate scoped rules",
			operations: []Operation{operation},
			configure: func(options *OpenAPIOptions) {
				rule := OpenAPIMiddlewareSecurityRule{Expression: "authService.RequireAuth", SourceFile: "app/routes.go", Function: "ProvideRoutes", Requirements: requirements}
				options.MiddlewareSecurityRules = []OpenAPIMiddlewareSecurityRule{rule, rule}
			},
			problem: "overlaps another scoped security rule",
		},
		{
			name:       "global overlap",
			operations: []Operation{operation},
			configure: func(options *OpenAPIOptions) {
				options.MiddlewareSecurity = map[string][]OpenAPISecurityRequirement{"authService.RequireAuth": requirements}
				options.MiddlewareSecurityRules = []OpenAPIMiddlewareSecurityRule{{Expression: "authService.RequireAuth", SourceFile: "app/routes.go", Function: "ProvideRoutes", Requirements: requirements}}
			},
			problem: "overlaps global security mapping",
		},
		{
			name:       "ambiguous declarations",
			operations: []Operation{operation, second},
			configure: func(options *OpenAPIOptions) {
				options.MiddlewareSecurityRules = []OpenAPIMiddlewareSecurityRule{{Expression: "authService.RequireAuth", SourceFile: "app/routes.go", Function: "ProvideRoutes", Requirements: requirements}}
			},
			problem: "matched 2 enclosing declarations",
		},
		{
			name:       "sole same-file receiver lookalike",
			operations: []Operation{second},
			configure: func(options *OpenAPIOptions) {
				options.MiddlewareSecurityRules = []OpenAPIMiddlewareSecurityRule{{Expression: "authService.RequireAuth", SourceFile: "app/routes.go", Function: "ProvideRoutes", Receiver: "First", Requirements: requirements}}
			},
			problem: "did not match",
		},
		{
			name:       "receiver keyword",
			operations: []Operation{operation},
			configure: func(options *OpenAPIOptions) {
				options.MiddlewareSecurityRules = []OpenAPIMiddlewareSecurityRule{{Expression: "authService.RequireAuth", SourceFile: "app/routes.go", Function: "ProvideRoutes", Receiver: "type", Requirements: requirements}}
			},
			problem: "receiver must be a Go-style identifier",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			options := base
			test.configure(&options)
			_, err := ProjectOpenAPI(Manifest{Operations: test.operations}, options)
			var projectionError *OpenAPIProjectionError
			if !errors.As(err, &projectionError) || !strings.Contains(err.Error(), test.problem) {
				t.Fatalf("projection error = %v, want %q", err, test.problem)
			}
		})
	}
}

// TestProjectOpenAPIScopedMiddlewareSecuritySelectsReceiver proves one method receiver cannot lend policy to a same-file method lookalike.
func TestProjectOpenAPIScopedMiddlewareSecuritySelectsReceiver(t *testing.T) {
	requirements := []OpenAPISecurityRequirement{{"forjSession": {}}}
	first := Operation{
		Method:     "GET",
		Path:       "/first",
		Middleware: []string{"c.auth.RequireAuth"},
		Outputs:    OutputShape{Responses: []ResponseShape{{StatusCode: 204}}},
		middlewareProvenance: []middlewareProvenance{{
			Expression: "c.auth.RequireAuth",
			File:       "internal/auth/controller.go",
			Function:   "Routes",
			Receiver:   "Controller",
		}},
	}
	lookalike := first
	lookalike.Path = "/lookalike"
	lookalike.middlewareProvenance = []middlewareProvenance{{
		Expression: "c.auth.RequireAuth",
		File:       "internal/auth/controller.go",
		Function:   "Routes",
		Receiver:   "OtherController",
	}}
	options := OpenAPIOptions{
		SecuritySchemes: map[string]OpenAPISecurityScheme{
			"forjSession": {Type: "apiKey", In: "cookie", Name: "goforj_session"},
		},
		MiddlewareSecurityRules: []OpenAPIMiddlewareSecurityRule{{
			Expression:   "c.auth.RequireAuth",
			SourceFile:   "internal/auth/controller.go",
			Function:     "Routes",
			Receiver:     "Controller",
			Requirements: requirements,
		}},
	}
	document, err := ProjectOpenAPI(Manifest{Operations: []Operation{first, lookalike}}, options)
	if err != nil {
		t.Fatalf("project receiver-scoped middleware security: %v", err)
	}
	if security := document.Paths["/first"]["get"].Security; security == nil || !reflect.DeepEqual(*security, requirements) {
		t.Fatalf("selected receiver security = %#v, want %#v", security, requirements)
	}
	if security := document.Paths["/lookalike"]["get"].Security; security != nil {
		t.Fatalf("same-file receiver lookalike inherited security: %#v", *security)
	}

	unicodeOperation := first
	unicodeOperation.Path = "/unicode"
	unicodeOperation.middlewareProvenance = []middlewareProvenance{{
		Expression: "c.auth.RequireAuth",
		File:       "internal/auth/controller.go",
		Function:   "Routes",
		Receiver:   "控制器",
	}}
	options.MiddlewareSecurityRules[0].Receiver = "控制器"
	document, err = ProjectOpenAPI(Manifest{Operations: []Operation{unicodeOperation}}, options)
	if err != nil {
		t.Fatalf("project Unicode receiver-scoped middleware security: %v", err)
	}
	if security := document.Paths["/unicode"]["get"].Security; security == nil || !reflect.DeepEqual(*security, requirements) {
		t.Fatalf("Unicode receiver security = %#v, want %#v", security, requirements)
	}
}

// scopedCookieSecurityOptions returns the public configuration shape GoForj can emit for its generated route-group authentication policy.
func scopedCookieSecurityOptions() OpenAPIOptions {
	return OpenAPIOptions{
		SecuritySchemes: map[string]OpenAPISecurityScheme{
			"forjSession": {Type: "apiKey", In: "cookie", Name: "goforj_session"},
		},
		MiddlewareSecurityRules: []OpenAPIMiddlewareSecurityRule{{
			Expression: "authService.RequireAuth",
			SourceFile: "app/routes.go",
			Function:   "ProvideRoutes",
			Requirements: []OpenAPISecurityRequirement{{
				"forjSession": {},
			}},
		}},
	}
}
