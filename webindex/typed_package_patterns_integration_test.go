package webindex

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// TestTypedSchemaRegistryLoadsAdHocHandler verifies the file-query fallback retains checked contracts outside a Go module.
func TestTypedSchemaRegistryLoadsAdHocHandler(t *testing.T) {
	root := t.TempDir()
	handlerFile := filepath.Join(root, "handler.go")
	writeTypedFixtureFiles(t, root, map[string]string{
		"handler.go": `package adhoc

// Payload is the request and response contract used by the ad-hoc handler.
type Payload struct {
	Value string ` + "`json:\"value\"`" + `
}

// Context provides the contract calls needed by the focused type loader.
type Context struct{}

// Bind accepts a request contract.
func (Context) Bind(value any) error { return nil }

// JSON emits a response contract.
func (Context) JSON(status int, value any) error { return nil }

// Handle binds and emits the package-local contract.
func Handle(ctx Context) error {
	request := Payload{}
	if err := ctx.Bind(&request); err != nil {
		return err
	}
	return ctx.JSON(200, request)
}
`,
	})

	registry, err := loadTypedSchemaRegistry(context.Background(), typedSchemaLoadOptions{
		Root:         root,
		HandlerFiles: []string{handlerFile},
	})
	if err != nil {
		t.Fatalf("load ad-hoc typed schema registry: %v", err)
	}
	if diagnostics := registry.diagnosticsSnapshot(); len(diagnostics) != 0 {
		t.Fatalf("expected a fully checked ad-hoc package, got %+v", diagnostics)
	}

	fset, bindArgument, responseArgument := parseTypedHandlerExpressions(t, handlerFile, "Handle")
	request, ok := registry.resolveExpression(fset, bindSchemaExpression(bindArgument))
	if !ok {
		t.Fatal("expected checked ad-hoc request expression")
	}
	response, ok := registry.resolveExpression(fset, responseArgument)
	if !ok {
		t.Fatal("expected checked ad-hoc response expression")
	}
	if request.TypeIdentity != response.TypeIdentity || !strings.HasSuffix(request.TypeIdentity, ".Payload") {
		t.Fatalf("unexpected ad-hoc type identities request=%q response=%q", request.TypeIdentity, response.TypeIdentity)
	}
}
