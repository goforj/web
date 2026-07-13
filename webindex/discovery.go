package webindex

import (
	"fmt"
	"go/ast"
	"go/token"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// discoveredRoute preserves source and provider identity until composition scoping can decide whether the route belongs to the selected App.
type discoveredRoute struct {
	MethodExpr            string
	Path                  string
	HandlerExpr           string
	HandlerFunction       string
	HandlerPackageHint    string
	HandlerImportPathHint string
	HandlerReceiverHint   string
	HandlerReceiverExact  bool
	HandlerBareIdentifier bool
	HandlerIdentityKnown  bool
	Provider              routeProvider
	MiddlewareExprs       []string
	EnclosingFunction     string
	File                  string
	Line                  int
	CallPos               token.Pos
}

// discoveredHandler retains the declaration and import context needed for conservative contract inference.
type discoveredHandler struct {
	Package    string
	ImportPath string
	Receiver   string
	Name       string
	File       string
	Line       int
	Decl       *ast.FuncDecl
	Imports    map[string]string
}

// routeProvider identifies one exact route-producing method so public and protected providers on the same receiver remain distinct.
type routeProvider struct {
	Package    string
	ImportPath string
	Receiver   string
	Method     string
}

// routerMapping records the prefixes, middleware, and call evidence established by the selected App's route composition.
type routerMapping struct {
	PrefixByProvider           map[routeProvider]string
	MiddlewareByProvider       map[routeProvider][]string
	ProviderCalls              map[routeProvider]routeProviderCall
	MiddlewareSourceByProvider map[routeProvider]middlewareSource
	DefaultMiddlewares         []string
	DefaultMiddlewareSource    middlewareSource
	Diagnostics                []Diagnostic
	InlineByCall               map[token.Pos]routePlacement
}

// middlewareSource identifies the declaration that attaches a middleware sequence before individual expressions are normalized.
type middlewareSource struct {
	File     string
	Function string
	Receiver string
}

// discoverRoutesAndHandlers keeps a route's provider method separate from its handler so composition can distinguish public and protected route sets on one controller.
func discoverRoutesAndHandlers(fset *token.FileSet, parsed []*parsedFile, scope routeScope) ([]discoveredRoute, []discoveredHandler, []string, routerMapping) {
	var routes []discoveredRoute
	var handlers []discoveredHandler
	groupPrefixes := map[string]struct{}{}
	mapping := buildRouterMapping(parsed, scope)
	appendVariadicMiddlewareDiagnostic := func(call *ast.CallExpr, dynamic bool) {
		if !dynamic {
			return
		}
		position := fset.Position(call.Ellipsis)
		mapping.Diagnostics = append(mapping.Diagnostics, Diagnostic{
			Severity: "warn",
			Code:     "dynamic_middleware_expansion",
			Message:  "variadic middleware expansion cannot be indexed as a static runtime sequence",
			File:     filepath.ToSlash(position.Filename),
			Line:     position.Line,
		})
	}

	for _, pf := range parsed {
		imports := sourceImportPathsByAlias(pf.File)
		for _, decl := range pf.File.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if ok {
				pos := fset.Position(fn.Pos())
				handlers = append(handlers, discoveredHandler{
					Package:    pf.PackageName,
					ImportPath: scope.packageImportPath(pf.Path),
					Receiver:   receiverName(fn),
					Name:       fn.Name.Name,
					File:       filepath.ToSlash(pos.Filename),
					Line:       pos.Line,
					Decl:       fn,
					Imports:    imports,
				})
			}
		}

		for _, decl := range pf.File.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			provider := routeProviderFromDeclaration(pf, fn, scope)
			providerBinding, providerBindingKnown := providerMiddlewareBindingForDeclaration(fn, provider, mapping, imports)
			providerFlowRequired := provider.Receiver != "" && strings.HasSuffix(provider.Method, "Routes") && scope.includesProvider(provider)
			providerFlow := returnedRouteFlow{Supported: true}
			if providerFlowRequired {
				providerFlow = returnedRouteCalls(fn, imports)
				if !providerFlow.Supported {
					position := fset.Position(fn.Pos())
					code := "route_provider_unsupported_return_expression"
					message := fmt.Sprintf("route provider %s.%s does not return a statically traceable route slice", provider.Receiver, provider.Method)
					if providerFlow.UnsafeMutationOrEscape {
						code = "route_provider_unsafe_route_value_flow"
						message = fmt.Sprintf("route provider %s.%s mutates or exposes a tracked route value before return", provider.Receiver, provider.Method)
					} else if providerFlow.UnsupportedControlFlow {
						code = "route_provider_unsupported_control_flow"
						message = fmt.Sprintf("route provider %s.%s uses branch or loop control flow that cannot be indexed safely", provider.Receiver, provider.Method)
					}
					mapping.Diagnostics = append(mapping.Diagnostics, Diagnostic{
						Severity: "warn",
						Code:     code,
						Message:  message,
						File:     filepath.ToSlash(position.Filename),
						Line:     position.Line,
					})
				}
				for callPosition, count := range providerFlow.Calls {
					if count < 2 {
						continue
					}
					position := fset.Position(callPosition)
					mapping.Diagnostics = append(mapping.Diagnostics, Diagnostic{
						Severity: "error",
						Code:     "route_provider_duplicate_returned_route",
						Message:  fmt.Sprintf("route provider %s.%s returns one route call %d times", provider.Receiver, provider.Method, count),
						File:     filepath.ToSlash(position.Filename),
						Line:     position.Line,
					})
				}
			}
			localTypes := collectFuncVarTypes(fn)
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				if _, nestedFunction := n.(*ast.FuncLit); nestedFunction {
					return false
				}
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				constructor, ok := frameworkConstructorName(call, imports)
				if !ok {
					return true
				}
				if providerFlowRequired {
					if !providerFlow.Supported {
						return true
					}
					if _, returned := providerFlow.Calls[call.Pos()]; !returned {
						return true
					}
				}
				switch constructor {
				case "NewRoute":
					if len(call.Args) < 3 {
						return true
					}
					pos := fset.Position(call.Pos())
					path, pathOK := staticStringLiteral(call.Args[1])
					if !pathOK || path == "" {
						mapping.Diagnostics = append(mapping.Diagnostics, Diagnostic{
							Severity: "warn",
							Code:     "unsupported_route_path",
							Message:  "route path expression (" + exprString(call.Args[1]) + ") must be a non-empty string literal",
							File:     filepath.ToSlash(pos.Filename),
							Line:     pos.Line,
						})
						return true
					}
					methodExpression, methodOK := canonicalRouteMethodExpression(call.Args[0], imports)
					if !methodOK {
						mapping.Diagnostics = append(mapping.Diagnostics, Diagnostic{
							Severity: "warn",
							Code:     "unsupported_route_method",
							Message:  "route method expression (" + exprString(call.Args[0]) + ") is not a supported HTTP method literal or net/http constant",
							File:     filepath.ToSlash(pos.Filename),
							Line:     pos.Line,
						})
						return true
					}
					if normalizeMethodExpr(methodExpression) == "connect" {
						mapping.Diagnostics = append(mapping.Diagnostics, Diagnostic{
							Severity: "warn",
							Code:     "unrepresentable_openapi_method",
							Message:  "CONNECT is registered at runtime but has no OpenAPI Path Item operation field",
							File:     filepath.ToSlash(pos.Filename),
							Line:     pos.Line,
						})
					}
					handlerExpr := exprString(call.Args[2])
					handlerFn := methodNameFromHandlerExpr(handlerExpr)
					hintPkg, hintRecv, receiverExact, identityKnown := inferHandlerHints(call.Args[2], localTypes, pf.PackageName, importPathsByAlias(pf))
					hintImport := inferHandlerImportHint(pf, hintPkg, scope)
					middlewares, dynamicMiddleware := resolvedMiddlewareExprsForCall(call, 3, providerBinding, providerBindingKnown)
					route := discoveredRoute{
						MethodExpr:            methodExpression,
						Path:                  path,
						HandlerExpr:           handlerExpr,
						HandlerFunction:       handlerFn,
						HandlerPackageHint:    hintPkg,
						HandlerImportPathHint: hintImport,
						HandlerReceiverHint:   hintRecv,
						HandlerReceiverExact:  receiverExact,
						HandlerBareIdentifier: isBareHandlerIdentifier(call.Args[2]),
						HandlerIdentityKnown:  identityKnown,
						Provider:              provider,
						MiddlewareExprs:       middlewares,
						EnclosingFunction:     fn.Name.Name,
						File:                  filepath.ToSlash(pos.Filename),
						Line:                  pos.Line,
						CallPos:               call.Pos(),
					}
					if scope.includesRoute(route) {
						appendVariadicMiddlewareDiagnostic(call, dynamicMiddleware)
						routes = append(routes, route)
					}
				case "NewWebSocketRoute":
					if len(call.Args) < 2 {
						return true
					}
					pos := fset.Position(call.Pos())
					path, pathOK := staticStringLiteral(call.Args[0])
					if !pathOK || path == "" {
						mapping.Diagnostics = append(mapping.Diagnostics, Diagnostic{
							Severity: "warn",
							Code:     "unsupported_route_path",
							Message:  "WebSocket route path expression (" + exprString(call.Args[0]) + ") must be a non-empty string literal",
							File:     filepath.ToSlash(pos.Filename),
							Line:     pos.Line,
						})
						return true
					}
					handlerExpr := exprString(call.Args[1])
					handlerFn := methodNameFromHandlerExpr(handlerExpr)
					hintPkg, hintRecv, receiverExact, identityKnown := inferHandlerHints(call.Args[1], localTypes, pf.PackageName, importPathsByAlias(pf))
					hintImport := inferHandlerImportHint(pf, hintPkg, scope)
					middlewares, dynamicMiddleware := resolvedMiddlewareExprsForCall(call, 2, providerBinding, providerBindingKnown)
					route := discoveredRoute{
						MethodExpr:            `"GETWS"`,
						Path:                  path,
						HandlerExpr:           handlerExpr,
						HandlerFunction:       handlerFn,
						HandlerPackageHint:    hintPkg,
						HandlerImportPathHint: hintImport,
						HandlerReceiverHint:   hintRecv,
						HandlerReceiverExact:  receiverExact,
						HandlerBareIdentifier: isBareHandlerIdentifier(call.Args[1]),
						HandlerIdentityKnown:  identityKnown,
						Provider:              provider,
						MiddlewareExprs:       middlewares,
						EnclosingFunction:     fn.Name.Name,
						File:                  filepath.ToSlash(pos.Filename),
						Line:                  pos.Line,
						CallPos:               call.Pos(),
					}
					if scope.includesRoute(route) {
						appendVariadicMiddlewareDiagnostic(call, dynamicMiddleware)
						routes = append(routes, route)
					}
				case "NewRouteGroup":
					if scope.includesGroupCall(pf.Path, call.Pos()) && len(call.Args) > 0 {
						appendVariadicMiddlewareDiagnostic(call, call.Ellipsis != token.NoPos && len(call.Args) > 2)
						if prefix, ok := staticStringLiteral(call.Args[0]); ok && prefix != "" {
							groupPrefixes[prefix] = struct{}{}
						}
					}
				}
				return true
			})
		}
	}

	prefixes := make([]string, 0, len(groupPrefixes))
	for p := range groupPrefixes {
		prefixes = append(prefixes, p)
	}
	sort.Strings(prefixes)
	mapping.Diagnostics = append(mapping.Diagnostics, scopedProviderDeclarationDiagnostics(scope, handlers)...)
	mapping.Diagnostics = append(mapping.Diagnostics, ambiguousProviderMappingDiagnostics(routes, mapping)...)
	return routes, handlers, prefixes, mapping
}

// scopedProviderDeclarationDiagnostics reports composition references that cannot resolve to a concrete route-provider method.
func scopedProviderDeclarationDiagnostics(scope routeScope, handlers []discoveredHandler) []Diagnostic {
	if !scope.active {
		return nil
	}
	diagnostics := make([]Diagnostic, 0)
	for _, provider := range scope.providers {
		found := false
		for _, handler := range handlers {
			declaration := routeProvider{
				Package:    handler.Package,
				ImportPath: handler.ImportPath,
				Receiver:   handler.Receiver,
				Method:     handler.Name,
			}
			if routeProvidersMatch(declaration, provider) {
				found = true
				break
			}
		}
		if found {
			continue
		}
		diagnostics = append(diagnostics, Diagnostic{
			Severity: "warn",
			Code:     "route_provider_not_found",
			Message:  fmt.Sprintf("composition route provider %s.%s has no matching method declaration", provider.Receiver, provider.Method),
		})
	}
	sortDiagnostics(diagnostics)
	return diagnostics
}

// ambiguousProviderMappingDiagnostics prevents map iteration from choosing policy when incomplete source identity matches multiple providers.
func ambiguousProviderMappingDiagnostics(routes []discoveredRoute, mapping routerMapping) []Diagnostic {
	diagnostics := make([]Diagnostic, 0)
	seen := map[string]struct{}{}
	for _, route := range routes {
		if _, inline := mapping.InlineByCall[route.CallPos]; inline {
			continue
		}
		matches := map[routeProvider]struct{}{}
		if _, exact := mapping.PrefixByProvider[route.Provider]; !exact {
			for provider := range mapping.PrefixByProvider {
				if routeProvidersMatch(route.Provider, provider) {
					matches[provider] = struct{}{}
				}
			}
		}
		if _, exact := mapping.MiddlewareByProvider[route.Provider]; !exact {
			for provider := range mapping.MiddlewareByProvider {
				if routeProvidersMatch(route.Provider, provider) {
					matches[provider] = struct{}{}
				}
			}
		}
		if _, exact := mapping.ProviderCalls[route.Provider]; !exact {
			for provider := range mapping.ProviderCalls {
				if routeProvidersMatch(route.Provider, provider) {
					matches[provider] = struct{}{}
				}
			}
		}
		if len(matches) < 2 {
			continue
		}
		key := route.File + "|" + intToString(route.Line)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		diagnostics = append(diagnostics, Diagnostic{
			Severity: "warn",
			Code:     "ambiguous_route_provider_mapping",
			Message:  fmt.Sprintf("route provider %s.%s matches multiple composition policies", route.Provider.Receiver, route.Provider.Method),
			File:     route.File,
			Line:     route.Line,
		})
	}
	sortDiagnostics(diagnostics)
	return diagnostics
}

// inferHandlerImportHint resolves local and imported handler package hints so same-named packages cannot exchange handler metadata across app boundaries.
func inferHandlerImportHint(pf *parsedFile, packageHint string, scope routeScope) string {
	if packageHint == "" {
		return ""
	}
	if packageHint == pf.PackageName {
		return scope.packageImportPath(pf.Path)
	}
	return importPathsByAlias(pf)[packageHint]
}

// middlewareExprs preserves exact source expressions because a function value and a zero-argument factory have different runtime behavior.
func middlewareExprs(args []ast.Expr) []string {
	if len(args) == 0 {
		return nil
	}
	out := make([]string, 0, len(args))
	for _, arg := range args {
		out = append(out, exprString(arg))
	}
	return out
}

// middlewareExprsForCall retains an ellipsis marker because one slice expression represents an unknown runtime middleware sequence.
func middlewareExprsForCall(call *ast.CallExpr, start int) []string {
	if call == nil || start >= len(call.Args) {
		return nil
	}
	middlewares := middlewareExprs(call.Args[start:])
	if call.Ellipsis != token.NoPos && len(middlewares) > 0 {
		middlewares[len(middlewares)-1] += "..."
	}
	return middlewares
}

// providerMiddlewareBinding links one proven variadic framework middleware formal to the selected composition actual sequence.
type providerMiddlewareBinding struct {
	Formal       string
	FormalObject *ast.Object
	Expressions  []string
	Evidence     string
	Exact        bool
}

// providerMiddlewareBindingForDeclaration binds only a final variadic framework Middleware parameter on the exact selected provider invocation.
func providerMiddlewareBindingForDeclaration(function *ast.FuncDecl, provider routeProvider, mapping routerMapping, imports map[string]string) (providerMiddlewareBinding, bool) {
	formal, fixedCount, ok := variadicMiddlewareFormal(function, imports)
	if !ok {
		return providerMiddlewareBinding{}, false
	}
	if !providerMiddlewareFormalRemainsUnchanged(function, formal, imports) {
		return providerMiddlewareBinding{}, false
	}
	providerCall, ok := selectedProviderCall(provider, mapping.ProviderCalls)
	if !ok {
		return providerMiddlewareBinding{}, false
	}
	binding := providerMiddlewareBinding{Formal: formal.Name, FormalObject: formal.Obj, Exact: true}
	arguments := providerCall.Arguments
	if len(arguments) < fixedCount {
		return providerMiddlewareBinding{}, false
	}
	if len(arguments) == fixedCount {
		return binding, true
	}
	last := arguments[len(arguments)-1]
	if last.Expanded {
		if len(arguments) != fixedCount+1 {
			return providerMiddlewareBinding{}, false
		}
		binding.Exact = last.Exact
		binding.Expressions = append([]string(nil), last.Expressions...)
		binding.Evidence = last.Evidence
		return binding, true
	}
	for _, argument := range arguments[fixedCount:] {
		if argument.Expanded || !argument.Exact || len(argument.Expressions) != 1 {
			binding.Exact = false
			binding.Evidence = argument.Evidence
			return binding, true
		}
		binding.Expressions = append(binding.Expressions, argument.Expressions[0])
	}
	return binding, true
}

// providerMiddlewareFormalRemainsUnchanged permits substitution only when every use is a direct route-constructor expansion that observes the original variadic slice.
func providerMiddlewareFormalRemainsUnchanged(function *ast.FuncDecl, formal *ast.Ident, imports map[string]string) bool {
	if function == nil || function.Body == nil || formal == nil {
		return false
	}
	allowed := map[token.Pos]struct{}{}
	ast.Inspect(function.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || call.Ellipsis == token.NoPos || len(call.Args) == 0 {
			return true
		}
		constructor, frameworkCall := frameworkConstructorName(call, imports)
		if !frameworkCall {
			return true
		}
		middlewareStart := 3
		if constructor == "NewWebSocketRoute" {
			middlewareStart = 2
		} else if constructor != "NewRoute" {
			return true
		}
		if len(call.Args)-1 < middlewareStart {
			return true
		}
		identifier, matches := middlewareFormalIdentifier(call.Args[len(call.Args)-1], formal)
		if matches {
			allowed[identifier.Pos()] = struct{}{}
		}
		return true
	})

	unchanged := true
	ast.Inspect(function.Body, func(node ast.Node) bool {
		identifier, ok := node.(*ast.Ident)
		if !ok || !middlewareFormalIdentifiersMatch(identifier, formal) {
			return true
		}
		if _, directExpansion := allowed[identifier.Pos()]; !directExpansion {
			unchanged = false
			return false
		}
		return true
	})
	return unchanged
}

// middlewareFormalIdentifier unwraps parentheses and returns the matching formal identifier used by an expansion.
func middlewareFormalIdentifier(expression ast.Expr, formal *ast.Ident) (*ast.Ident, bool) {
	for {
		parenthesized, ok := expression.(*ast.ParenExpr)
		if !ok {
			break
		}
		expression = parenthesized.X
	}
	identifier, ok := expression.(*ast.Ident)
	return identifier, ok && middlewareFormalIdentifiersMatch(identifier, formal)
}

// middlewareFormalIdentifiersMatch prefers parser object identity so local shadowing does not invalidate or inherit a provider parameter binding.
func middlewareFormalIdentifiersMatch(identifier *ast.Ident, formal *ast.Ident) bool {
	if identifier == nil || formal == nil || identifier.Name != formal.Name {
		return false
	}
	if identifier.Obj != nil && formal.Obj != nil {
		return identifier.Obj == formal.Obj
	}
	return true
}

// variadicMiddlewareFormal returns the final framework Middleware variadic identifier and the number of preceding ordinary parameters.
func variadicMiddlewareFormal(function *ast.FuncDecl, imports map[string]string) (*ast.Ident, int, bool) {
	if function == nil || function.Type == nil || function.Type.Params == nil || len(function.Type.Params.List) == 0 {
		return nil, 0, false
	}
	parameters := function.Type.Params.List
	variadic := parameters[len(parameters)-1]
	ellipsis, ok := variadic.Type.(*ast.Ellipsis)
	if !ok || len(variadic.Names) != 1 || !isFrameworkMiddlewareType(ellipsis.Elt, imports) {
		return nil, 0, false
	}
	fixedCount := 0
	for _, parameter := range parameters[:len(parameters)-1] {
		if len(parameter.Names) == 0 {
			fixedCount++
			continue
		}
		fixedCount += len(parameter.Names)
	}
	return variadic.Names[0], fixedCount, true
}

// selectedProviderCall resolves exact import identity first and permits one incomplete-source fallback without map-order selection.
func selectedProviderCall(provider routeProvider, calls map[routeProvider]routeProviderCall) (routeProviderCall, bool) {
	if call, exact := calls[provider]; exact {
		return cloneRouteProviderCall(call), true
	}
	matches := make([]routeProviderCall, 0)
	for candidate, call := range calls {
		if routeProvidersMatch(provider, candidate) {
			matches = append(matches, cloneRouteProviderCall(call))
		}
	}
	if len(matches) != 1 {
		return routeProviderCall{}, false
	}
	return matches[0], true
}

// resolvedMiddlewareExprsForCall substitutes only the exact bound formal expansion and keeps all other variadic evidence dynamic.
func resolvedMiddlewareExprsForCall(call *ast.CallExpr, start int, binding providerMiddlewareBinding, bindingKnown bool) ([]string, bool) {
	if call == nil || start >= len(call.Args) {
		return nil, false
	}
	if call.Ellipsis == token.NoPos {
		return middlewareExprsForCall(call, start), false
	}
	lastIndex := len(call.Args) - 1
	middlewares := middlewareExprs(call.Args[start:lastIndex])
	expansion := call.Args[lastIndex]
	if bindingKnown && middlewareExpansionMatchesFormal(expansion, binding) {
		if binding.Exact {
			middlewares = append(middlewares, binding.Expressions...)
			return middlewares, false
		}
		evidence := binding.Evidence
		if evidence == "" {
			evidence = exprString(expansion) + "..."
		}
		middlewares = append(middlewares, evidence)
		return middlewares, true
	}
	middlewares = append(middlewares, exprString(expansion)+"...")
	return middlewares, true
}

// middlewareExpansionMatchesFormal uses parser object identity when available so a shadowing local cannot inherit a provider binding by name.
func middlewareExpansionMatchesFormal(expression ast.Expr, binding providerMiddlewareBinding) bool {
	for {
		parenthesized, ok := expression.(*ast.ParenExpr)
		if !ok {
			break
		}
		expression = parenthesized.X
	}
	identifier, ok := expression.(*ast.Ident)
	if !ok || identifier.Name != binding.Formal {
		return false
	}
	if identifier.Obj != nil && binding.FormalObject != nil {
		return identifier.Obj == binding.FormalObject
	}
	return true
}

// extractStringLiteral rejects computed values so dynamic route evidence remains diagnostic instead of guessed.
func extractStringLiteral(expr ast.Expr) string {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return ""
	}
	s, err := strconv.Unquote(lit.Value)
	if err != nil {
		return ""
	}
	return s
}

// receiverName contributes method ownership when package/function names alone cannot disambiguate handlers.
func receiverName(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return ""
	}
	return typeNameFromExpr(fn.Recv.List[0].Type)
}

// collectFuncVarTypes retains receiver and parameter types needed to interpret selector-based handler expressions.
func collectFuncVarTypes(fn *ast.FuncDecl) map[string]string {
	out := map[string]string{}
	if fn.Recv != nil {
		for _, field := range fn.Recv.List {
			t := typeNameFromExpr(field.Type)
			for _, n := range field.Names {
				out[n.Name] = t
			}
		}
	}
	if fn.Type != nil && fn.Type.Params != nil {
		for _, field := range fn.Type.Params.List {
			t := typeNameFromExpr(field.Type)
			for _, n := range field.Names {
				out[n.Name] = t
			}
		}
	}
	return out
}

// inferHandlerHints distinguishes package functions from receiver methods because equal symbol names cannot safely exchange handler contracts.
func inferHandlerHints(handlerExpr ast.Expr, locals map[string]string, defaultPkg string, imports map[string]string) (string, string, bool, bool) {
	if _, ok := handlerExpr.(*ast.Ident); ok {
		return defaultPkg, "", true, true
	}
	sel, ok := handlerExpr.(*ast.SelectorExpr)
	if !ok {
		return "", "", false, false
	}
	xid, ok := sel.X.(*ast.Ident)
	if !ok {
		return "", "", true, false
	}
	typ := locals[xid.Name]
	if typ == "" {
		if _, imported := imports[xid.Name]; imported {
			return xid.Name, "", true, true
		}
		return "", "", true, false
	}
	typ = strings.TrimPrefix(typ, "*")
	parts := strings.Split(typ, ".")
	if len(parts) == 2 {
		return parts[0], parts[1], true, true
	}
	return defaultPkg, typ, true, true
}

// isBareHandlerIdentifier records the only handler syntax that can use a receiverless ambiguity fallback in incomplete source.
func isBareHandlerIdentifier(handlerExpr ast.Expr) bool {
	_, ok := handlerExpr.(*ast.Ident)
	return ok
}

// joinPath preserves Echo's exact group-prefix concatenation so the canonical manifest matches the route registered at runtime.
func joinPath(prefix, path string) string {
	return prefix + path
}
