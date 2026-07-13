package webindex

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"testing"
)

// TestRunResolvesBareHandlersAsPackageFunctions verifies a provider receiver cannot steal a bare function's contract while explicit selectors remain method-specific.
func TestRunResolvesBareHandlersAsPackageFunctions(t *testing.T) {
	root := t.TempDir()
	writeFixtureFiles(t, root, map[string]string{
		"go.mod": "module example.com/identity\n\ngo 1.24\n",
		"internal/health/controller.go": `package health
import (
	"net/http"
	"github.com/goforj/web"
)
type Controller struct{}
func (c *Controller) Routes() []web.Route {
	return []web.Route{
		web.NewRoute(http.MethodGet, "/function", health),
		web.NewRoute(http.MethodGet, "/method", c.health),
	}
}
func health(ctx web.Context) error { return ctx.NoContent(http.StatusNoContent) }
func (c *Controller) health(ctx web.Context) error { return ctx.Text(http.StatusCreated, "method") }
`,
	})

	manifest, err := Run(context.Background(), IndexOptions{Root: root, Strict: true})
	if err != nil {
		t.Fatalf("strict index semantic handler identities: %v", err)
	}
	function := operationByPath(t, manifest, "/function")
	if function.Handler.Receiver != "" || function.Handler.Function != "health" {
		t.Fatalf("bare handler resolved to a receiver method: %+v", function.Handler)
	}
	if got := function.Outputs.Responses; len(got) != 1 || got[0].Source != "web.NoContent" || got[0].StatusCode != httpStatusNoContent {
		t.Fatalf("bare handler response came from the wrong declaration: %+v", got)
	}
	method := operationByPath(t, manifest, "/method")
	if method.Handler.Receiver != "Controller" || method.Handler.Function != "health" {
		t.Fatalf("selector handler lost its receiver identity: %+v", method.Handler)
	}
	if got := method.Outputs.Responses; len(got) != 1 || got[0].Source != "web.Text" || got[0].StatusCode != httpStatusCreated {
		t.Fatalf("selector handler response came from the wrong declaration: %+v", got)
	}
}

// TestSourceIdentityRejectsLexicallyShadowedImports verifies local names cannot impersonate imported web, net/http, or slices packages.
func TestSourceIdentityRejectsLexicallyShadowedImports(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "routes.go", `package routes
import (
	web "github.com/goforj/web"
	http "net/http"
	slices "slices"
	decoy "example.com/decoy/web"
)
func real() {
	_ = web.NewRoute(http.MethodGet, "/real", handler)
	_ = slices.Concat([]int{}, []int{})
}
func shadowed() {
	web := factory{}
	http := methods{}
	slices := collection{}
	_ = web.NewRoute(http.MethodGet, "/decoy", handler)
	_ = slices.Concat([]int{}, []int{})
}
func wrongImport() {
	_ = decoy.NewRoute(http.MethodGet, "/decoy", handler)
}
`, 0)
	if err != nil {
		t.Fatalf("parse shadow fixture: %v", err)
	}
	imports := sourceImportPathsByAlias(file)
	results := map[string]struct {
		constructor bool
		method      bool
		concat      bool
	}{}
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Body == nil {
			continue
		}
		result := results[function.Name.Name]
		ast.Inspect(function.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			if _, recognized := frameworkConstructorName(call, imports); recognized {
				result.constructor = true
				if len(call.Args) > 0 {
					_, result.method = canonicalRouteMethodExpression(call.Args[0], imports)
				}
			}
			if isStandardSlicesConcat(call, imports) {
				result.concat = true
			}
			return true
		})
		results[function.Name.Name] = result
	}
	if got := results["real"]; !got.constructor || !got.method || !got.concat {
		t.Fatalf("real imports were not recognized: %+v", got)
	}
	if got := results["shadowed"]; got.constructor || got.method || got.concat {
		t.Fatalf("lexically shadowed imports were trusted: %+v", got)
	}
	if got := results["wrongImport"]; got.constructor {
		t.Fatalf("decoy web import was trusted: %+v", got)
	}
}

// TestRunRecognizesAliasedWebContextImport verifies native analysis follows the framework import path rather than requiring the conventional web qualifier.
func TestRunRecognizesAliasedWebContextImport(t *testing.T) {
	root := t.TempDir()
	writeFixtureFiles(t, root, map[string]string{
		"go.mod": "module example.com/contextalias\n\ngo 1.24\n",
		"internal/report/routes.go": `package report
import (
	"net/http"
	"github.com/goforj/web"
)
type Controller struct{}
func (c *Controller) Routes() []web.Route {
	return []web.Route{web.NewRoute(http.MethodGet, "/aliased", c.Show)}
}
`,
		"internal/report/handler.go": `package report
import (
	"net/http"
	forj "github.com/goforj/web"
)
func (c *Controller) Show(ctx forj.Context) error {
	_ = ctx.Query("format")
	return ctx.NoContent(http.StatusNoContent)
}
`,
	})

	manifest, err := Run(context.Background(), IndexOptions{Root: root, Strict: true})
	if err != nil {
		t.Fatalf("strict index aliased context: %v", err)
	}
	operation := operationByPath(t, manifest, "/aliased")
	if !reflect.DeepEqual(operation.Inputs.QueryParams, []Parameter{{Name: "format", In: "query", Required: false, Confidence: "high"}}) {
		t.Fatalf("aliased native context was not analyzed: %+v", operation.Inputs.QueryParams)
	}
	if len(operation.Outputs.Responses) != 1 || operation.Outputs.Responses[0].Source != "web.NoContent" {
		t.Fatalf("aliased native response was not analyzed: %+v", operation.Outputs.Responses)
	}
}

// TestRunRejectsDecoyContextQualifier verifies a package named web cannot impersonate the framework context contract.
func TestRunRejectsDecoyContextQualifier(t *testing.T) {
	root := t.TempDir()
	writeFixtureFiles(t, root, map[string]string{
		"go.mod": "module example.com/contextdecoy\n\ngo 1.24\n",
		"internal/report/routes.go": `package report
import (
	"net/http"
	"github.com/goforj/web"
)
type Controller struct{}
func (c *Controller) Routes() []web.Route {
	return []web.Route{web.NewRoute(http.MethodGet, "/decoy", c.Show)}
}
`,
		"internal/report/handler.go": `package report
import (
	"net/http"
	web "example.com/not-the-framework"
)
func (c *Controller) Show(ctx web.Context) error {
	_ = ctx.Query("format")
	return ctx.NoContent(http.StatusNoContent)
}
`,
	})

	manifest, err := Run(context.Background(), IndexOptions{Root: root})
	if err != nil {
		t.Fatalf("index decoy context fixture: %v", err)
	}
	operation := operationByPath(t, manifest, "/decoy")
	if len(operation.Inputs.QueryParams) != 0 || len(operation.Outputs.Responses) != 0 {
		t.Fatalf("decoy context contributed a framework contract: %+v", operation)
	}
	if !manifestHasDiagnostic(manifest, "handler_no_response") {
		t.Fatalf("decoy context must remain visible as unresolved: %+v", manifest.Diagnostics)
	}
}

// TestRunPreservesParameterizedMiddlewareIdentity verifies policy arguments remain available for exact role-specific security mappings.
func TestRunPreservesParameterizedMiddlewareIdentity(t *testing.T) {
	root := t.TempDir()
	writeFixtureFiles(t, root, map[string]string{
		"go.mod": "module example.com/middlewareidentity\n\ngo 1.24\n",
		"internal/report/controller.go": `package report
import (
	"net/http"
	"github.com/goforj/web"
	"example.com/auth"
)
type Controller struct{}
func (c *Controller) Routes() []web.Route {
	return []web.Route{
		web.NewRoute(http.MethodGet, "/admin", c.Admin, auth.RequireRole("admin")),
		web.NewRoute(http.MethodGet, "/editor", c.Editor, auth.RequireRole("editor")),
	}
}
func (c *Controller) Admin(ctx web.Context) error { return ctx.NoContent(http.StatusNoContent) }
func (c *Controller) Editor(ctx web.Context) error { return ctx.NoContent(http.StatusNoContent) }
`,
	})

	manifest, err := Run(context.Background(), IndexOptions{Root: root, Strict: true})
	if err != nil {
		t.Fatalf("strict index parameterized middleware: %v", err)
	}
	admin := operationByPath(t, manifest, "/admin")
	editor := operationByPath(t, manifest, "/editor")
	if !reflect.DeepEqual(admin.Middleware, []string{`auth.RequireRole("admin")`}) {
		t.Fatalf("admin middleware lost its argument: %+v", admin.Middleware)
	}
	if !reflect.DeepEqual(editor.Middleware, []string{`auth.RequireRole("editor")`}) {
		t.Fatalf("editor middleware lost its argument: %+v", editor.Middleware)
	}

	document, err := ProjectOpenAPI(manifest, OpenAPIOptions{
		SecuritySchemes: map[string]OpenAPISecurityScheme{
			"adminRole":  {Type: "http", Scheme: "bearer"},
			"editorRole": {Type: "http", Scheme: "bearer"},
		},
		MiddlewareSecurity: map[string][]OpenAPISecurityRequirement{
			`auth.RequireRole("admin")`:  {{"adminRole": nil}},
			`auth.RequireRole("editor")`: {{"editorRole": nil}},
		},
	})
	if err != nil {
		t.Fatalf("project parameterized middleware security: %v", err)
	}
	if got := document.Paths["/admin"]["get"].Security; got == nil || !reflect.DeepEqual(*got, []OpenAPISecurityRequirement{{"adminRole": {}}}) {
		t.Fatalf("admin role mapping did not match exactly: %+v", got)
	}
	if got := document.Paths["/editor"]["get"].Security; got == nil || !reflect.DeepEqual(*got, []OpenAPISecurityRequirement{{"editorRole": {}}}) {
		t.Fatalf("editor role mapping did not match exactly: %+v", got)
	}
}

// TestRunPreservesMiddlewareCallsMultiplicityAndExpansionEvidence keeps runtime-distinct expressions visible and diagnoses unknown variadic sequences.
func TestRunPreservesMiddlewareCallsMultiplicityAndExpansionEvidence(t *testing.T) {
	root := t.TempDir()
	writeFixtureFiles(t, root, map[string]string{
		"go.mod": "module example.com/middlewareevidence\n\ngo 1.24\n",
		"controller.go": `package middlewareevidence
import (
	"net/http"
	"github.com/goforj/web"
)
type Controller struct{}
func (c *Controller) Routes() []web.Route {
	middlewares := []web.Middleware{auth, auth}
	return []web.Route{
		web.NewRoute(http.MethodGet, "/exact", c.Show, auth, factory(), auth),
		web.NewRoute(http.MethodGet, "/expanded", c.Show, middlewares...),
	}
}
func (c *Controller) Show(ctx web.Context) error { return ctx.NoContent(http.StatusNoContent) }
func auth(next web.Handler) web.Handler { return next }
func factory() web.Middleware { return auth }
`,
	})

	manifest, err := Run(context.Background(), IndexOptions{Root: root})
	if err != nil {
		t.Fatalf("index middleware evidence: %v", err)
	}
	if got := operationByPath(t, manifest, "/exact").Middleware; !reflect.DeepEqual(got, []string{"auth", "factory()", "auth"}) {
		t.Fatalf("exact middleware evidence = %v", got)
	}
	if got := operationByPath(t, manifest, "/expanded").Middleware; !reflect.DeepEqual(got, []string{"middlewares..."}) {
		t.Fatalf("variadic middleware evidence = %v", got)
	}
	if !manifestHasDiagnostic(manifest, "dynamic_middleware_expansion") {
		t.Fatalf("missing variadic middleware diagnostic: %+v", manifest.Diagnostics)
	}
}

const (
	// httpStatusCreated keeps fixture expectations readable without importing net/http into generated source assertions.
	httpStatusCreated = 201
	// httpStatusNoContent keeps response-less fixture expectations explicit.
	httpStatusNoContent = 204
)

// manifestHasDiagnostic reports whether an index finding contains the requested stable code.
func manifestHasDiagnostic(manifest Manifest, code string) bool {
	for _, diagnostic := range manifest.Diagnostics {
		if diagnostic.Code == code {
			return true
		}
	}
	return false
}
