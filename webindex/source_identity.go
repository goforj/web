package webindex

import (
	"go/ast"
	"go/token"
	"net/http"
	"strconv"
)

// webFrameworkImportPath anchors constructor and context recognition to the real framework package instead of source aliases.
const webFrameworkImportPath = "github.com/goforj/web"

// standardHTTPMethods maps standard-library selector names to canonical wire values without evaluating arbitrary expressions.
var standardHTTPMethods = map[string]string{
	"MethodConnect": http.MethodConnect,
	"MethodDelete":  http.MethodDelete,
	"MethodGet":     http.MethodGet,
	"MethodHead":    http.MethodHead,
	"MethodOptions": http.MethodOptions,
	"MethodPatch":   http.MethodPatch,
	"MethodPost":    http.MethodPost,
	"MethodPut":     http.MethodPut,
	"MethodTrace":   http.MethodTrace,
}

// staticStringLiteral distinguishes an intentionally empty literal from a dynamic expression that cannot be indexed safely.
func staticStringLiteral(expression ast.Expr) (string, bool) {
	literal, ok := expression.(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return "", false
	}
	value, err := strconv.Unquote(literal.Value)
	if err != nil {
		return "", false
	}
	return value, true
}

// importedSelector identifies a selector by its imported package path rather than trusting a source qualifier that a decoy package can reuse.
func importedSelector(expression ast.Expr, imports map[string]string) (string, string, bool) {
	selector, ok := expression.(*ast.SelectorExpr)
	if !ok {
		return "", "", false
	}
	qualifier, ok := selector.X.(*ast.Ident)
	if !ok {
		return "", "", false
	}
	if qualifier.Obj != nil {
		return "", "", false
	}
	importPath, ok := imports[qualifier.Name]
	if !ok {
		return "", "", false
	}
	return importPath, selector.Sel.Name, true
}

// frameworkConstructorName recognizes route constructors through the exact GoForj web import while allowing any explicit source alias, including the historical `http` alias.
func frameworkConstructorName(call *ast.CallExpr, imports map[string]string) (string, bool) {
	if call == nil {
		return "", false
	}
	importPath, name, ok := importedSelector(call.Fun, imports)
	if !ok || importPath != webFrameworkImportPath {
		return "", false
	}
	switch name {
	case "NewRoute", "NewWebSocketRoute", "NewRouteGroup":
		return name, true
	default:
		return "", false
	}
}

// canonicalRouteMethodExpression resolves standard-library HTTP aliases while leaving literals and unsupported expressions visible to diagnostics.
func canonicalRouteMethodExpression(expression ast.Expr, imports map[string]string) (string, bool) {
	if literal, ok := staticStringLiteral(expression); ok {
		method := normalizeMethodExpr(literal)
		return method, isOpenAPIMethod(method) || method == "connect"
	}
	importPath, name, ok := importedSelector(expression, imports)
	if !ok || importPath != "net/http" {
		return exprString(expression), false
	}
	method, ok := standardHTTPMethods[name]
	if !ok {
		return exprString(expression), false
	}
	return method, true
}

// isStandardSlicesConcat recognizes slices.Concat through the exact standard-library import and any legal alias.
func isStandardSlicesConcat(call *ast.CallExpr, imports map[string]string) bool {
	if call == nil {
		return false
	}
	importPath, name, ok := importedSelector(call.Fun, imports)
	return ok && importPath == "slices" && name == "Concat"
}
