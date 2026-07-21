package webindex

import (
	"context"
	"go/ast"
	"go/token"
	"reflect"
	"testing"
)

// TestTypedSourceSelectionNormalizesRanges verifies malformed and redundant roots cannot widen expression indexing.
func TestTypedSourceSelectionNormalizesRanges(t *testing.T) {
	file := "/tmp/selection.go"
	selection := newTypedSourceSelection([]typedSourceRange{
		{},
		{File: file, StartOffset: -1, EndOffset: 10},
		{File: file, StartOffset: 10, EndOffset: 10},
		{File: file, StartOffset: 10, EndOffset: 30},
		{File: file, StartOffset: 10, EndOffset: 40},
		{File: file, StartOffset: 15, EndOffset: 20},
		{File: file, StartOffset: 30, EndOffset: 50},
	})
	want := []typedSourceRange{
		{File: file, StartOffset: 10, EndOffset: 40},
		{File: file, StartOffset: 30, EndOffset: 50},
	}
	if !reflect.DeepEqual(selection[file], want) {
		t.Fatalf("normalized source ranges = %#v, want %#v", selection[file], want)
	}
	if !selection.contains(typedSourceRange{File: file, StartOffset: 12, EndOffset: 25}) {
		t.Fatal("normalized selection lost a nested expression")
	}
	if selection.contains(typedSourceRange{File: file, StartOffset: 5, EndOffset: 20}) {
		t.Fatal("normalized selection accepted a partial overlap")
	}
	if selection.contains(typedSourceRange{File: "/tmp/other.go", StartOffset: 12, EndOffset: 25}) {
		t.Fatal("normalized selection accepted another source file")
	}
	if !typedSourceSelection(nil).contains(typedSourceRange{}) {
		t.Fatal("nil selection must retain package-wide indexing")
	}
}

// TestTypedNodeOffsetsRejectInvalidPositions verifies AST pruning cannot compare nodes outside their owning token file.
func TestTypedNodeOffsetsRejectInvalidPositions(t *testing.T) {
	fset := token.NewFileSet()
	file := fset.AddFile("handler.go", -1, 10)
	valid := &ast.Ident{NamePos: file.Pos(2), Name: "ctx"}
	start, end, ok := typedNodeOffsets(file, valid)
	if !ok || start != 2 || end != 5 {
		t.Fatalf("valid node offsets = %d, %d, %t, want 2, 5, true", start, end, ok)
	}

	invalid := []struct {
		name string
		file *token.File
		node ast.Node
	}{
		{name: "nil file", node: valid},
		{name: "nil node", file: file},
		{name: "no position", file: file, node: &ast.Ident{Name: "ctx"}},
		{name: "before file", file: file, node: &ast.Ident{NamePos: token.Pos(file.Base() - 1), Name: "ctx"}},
		{name: "past file", file: file, node: &ast.Ident{NamePos: file.Pos(9), Name: "ctx"}},
	}
	for _, testCase := range invalid {
		t.Run(testCase.name, func(t *testing.T) {
			if _, _, ok := typedNodeOffsets(testCase.file, testCase.node); ok {
				t.Fatal("invalid node positions were accepted")
			}
		})
	}
}

// TestTypedSchemaRegistryIndexesOnlySelectedExpressionTrees verifies app-scoped loading excludes unrelated checked syntax from the registry.
func TestTypedSchemaRegistryIndexesOnlySelectedExpressionTrees(t *testing.T) {
	root, handlerFile := writeTypedSchemaFixture(t)
	fset, _, response := parseTypedHandlerExpressions(t, handlerFile, "Handle")
	responseSource, ok := typedRangeForNode(fset, response)
	if !ok {
		t.Fatal("locate selected response expression")
	}
	selection := newTypedSourceSelection([]typedSourceRange{responseSource})
	registry, err := loadTypedSchemaRegistry(context.Background(), typedSchemaLoadOptions{
		Root:                root,
		HandlerFiles:        []string{handlerFile},
		ContractExpressions: []typedSourceRange{responseSource},
	})
	if err != nil {
		t.Fatalf("load selected typed schema registry: %v", err)
	}
	if len(registry.expressions) == 0 {
		t.Fatal("selected response did not retain checked expressions")
	}
	for key, expression := range registry.expressions {
		if !selection.contains(expression.Source) {
			t.Fatalf("expression %q escaped selected response range: %+v", key, expression.Source)
		}
	}
	resolved, found := registry.resolveJSONExpression(fset, response)
	if !found || resolved.Schema["type"] != "object" {
		t.Fatalf("selected response lost its checked schema: found=%t schema=%+v", found, resolved.Schema)
	}

	unrelatedSet, _, unrelatedResponse := parseTypedHandlerExpressions(t, handlerFile, "CallResult")
	if _, _, found := registry.lookupExpression(unrelatedSet, unrelatedResponse); found {
		t.Fatal("unselected response remained in the expression registry")
	}
	components := componentsByIdentity(registry.componentSnapshot())
	if _, exists := components["example.com/typed/contracts.User"]; exists {
		t.Fatal("request-only component remained reachable from a response-only selection")
	}
	for _, identity := range []string{
		"example.com/typed/contracts.Page[example.com/typed/alpha.User]",
		"example.com/typed/contracts.Page[example.com/typed/beta.User]",
	} {
		if _, exists := components[identity]; !exists {
			t.Fatalf("selected response component %q was not indexed", identity)
		}
	}
}

// TestTypedSchemaRegistryNilSelectionIndexesPackageContracts verifies direct registry callers retain package-wide expression discovery.
func TestTypedSchemaRegistryNilSelectionIndexesPackageContracts(t *testing.T) {
	root, handlerFile := writeTypedSchemaFixture(t)
	registry, err := loadTypedSchemaRegistry(context.Background(), typedSchemaLoadOptions{
		Root:         root,
		HandlerFiles: []string{handlerFile},
	})
	if err != nil {
		t.Fatalf("load package-wide typed schema registry: %v", err)
	}
	for _, functionName := range []string{"Handle", "CallResult", "Unknown"} {
		fset, _, response := parseTypedHandlerExpressions(t, handlerFile, functionName)
		if _, _, found := registry.lookupExpression(fset, response); !found {
			t.Fatalf("nil selection omitted %s response", functionName)
		}
	}
	fset, _, response := parseTypedHandlerExpressions(t, handlerFile, "CallResult")
	resolved, found := registry.resolveExpression(fset, response)
	if !found || resolved.TypeIdentity != "example.com/typed/contracts.User" {
		t.Fatalf("nil selection lost package-wide resolution: found=%t identity=%q", found, resolved.TypeIdentity)
	}
}

// TestReachableContractExpressionsSelectsRootsAndDescendants verifies containment excludes partial overlaps and unrelated files.
func TestReachableContractExpressionsSelectsRootsAndDescendants(t *testing.T) {
	file := "/tmp/selected.go"
	firstRoot := typedSourceRange{File: file, StartOffset: 10, EndOffset: 30}
	secondRoot := typedSourceRange{File: file, StartOffset: 20, EndOffset: 40}
	sources := []typedSourceRange{
		firstRoot,
		secondRoot,
		{File: file, StartOffset: 15, EndOffset: 25},
		{File: file, StartOffset: 25, EndOffset: 35},
		{File: file, StartOffset: 15, EndOffset: 35},
		{File: file, StartOffset: 45, EndOffset: 55},
		{File: "/tmp/other.go", StartOffset: 20, EndOffset: 30},
	}
	registry := &typedSchemaRegistry{expressions: map[string]typedExpression{}}
	for _, source := range sources {
		registry.expressions[typedSourceKey(source)] = typedExpression{Source: source}
	}
	reachable := registry.reachableContractExpressions(nil, newTypedSourceSelection([]typedSourceRange{firstRoot, secondRoot}))
	got := make([]string, 0, len(reachable))
	for _, expression := range reachable {
		got = append(got, typedSourceKey(expression.Source))
	}
	want := []string{
		typedSourceKey(firstRoot),
		typedSourceKey(sources[2]),
		typedSourceKey(secondRoot),
		typedSourceKey(sources[3]),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("reachable expression keys = %#v, want %#v", got, want)
	}
}
