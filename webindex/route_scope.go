package webindex

import (
	"fmt"
	"go/ast"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// routeScope represents the selected App boundary used to exclude route providers owned by other entrypoints.
type routeScope struct {
	active          bool
	compositionFile string
	root            string
	modulePath      string
	providers       []routeProvider
	diagnostics     []Diagnostic
	placements      map[routeProvider]routePlacement
	providerCalls   map[routeProvider]routeProviderCall
	inlineRoutes    map[token.Pos]routePlacement
	groupCalls      map[token.Pos]struct{}
}

// scopedRouteGroup retains exact composition evidence before it is reduced to provider placements and diagnostics.
type scopedRouteGroup struct {
	Providers           []routeProvider
	ProviderOccurrences []routeProvider
	ProviderCalls       []routeProviderCall
	InlineCalls         []token.Pos
	InlineCallCounts    routeCallSet
	Prefix              string
	Middlewares         []string
	MiddlewareSource    middlewareSource
	CallPos             token.Pos
}

// routePlacement captures the prefix and middleware that composition assigns to a route provider or inline route.
type routePlacement struct {
	Prefix           string
	Middlewares      []string
	MiddlewareSource middlewareSource
}

// routeProviderCall retains the selected provider invocation so variadic middleware formals can be bound to exact composition actuals.
type routeProviderCall struct {
	Provider  routeProvider
	Arguments []routeCallArgument
	Pos       token.Pos
}

// routeCallArgument distinguishes ordinary actuals from exact or unresolved slice expansion evidence.
type routeCallArgument struct {
	Expressions []string
	Expanded    bool
	Exact       bool
	Evidence    string
}

// middlewareValue captures an immutable scalar alias or a statically enumerable middleware sequence at one composition statement.
type middlewareValue struct {
	Expressions []string
	Sequence    bool
	Exact       bool
	Evidence    string
}

// guardedRouteGroupAppend describes the generated len-guarded append whose branch changes only whether an empty route slice creates an empty group.
type guardedRouteGroupAppend struct {
	Target string
	Group  *ast.CallExpr
}

// routeProviderSet provides exact provider membership without collapsing distinct receiver methods.
type routeProviderSet map[routeProvider]struct{}

// routeCallSet counts source calls so duplicated or conditionally reused providers can be diagnosed conservatively.
type routeCallSet map[token.Pos]int

// returnedRouteFlow summarizes whether a route-producing function can be followed without guessing about runtime control flow or mutation.
type returnedRouteFlow struct {
	Calls                  routeCallSet
	HasReturn              bool
	Supported              bool
	UnsupportedControlFlow bool
	UnsafeMutationOrEscape bool
}

// isSliceTypeExpression recognizes a zero-valued slice declaration that can safely seed append-based generated flow.
func isSliceTypeExpression(expression ast.Expr) bool {
	array, ok := expression.(*ast.ArrayType)
	return ok && array.Len == nil
}

// newRouteScope resolves the selected app's composition file before discovery so route methods from other app entrypoints cannot leak into its API index.
func newRouteScope(root string, compositionPath string, parsed []*parsedFile) (routeScope, error) {
	return newRouteScopeWithModulePath(root, compositionPath, parsed, modulePathFromRoot(root))
}

// newRouteScopeWithModulePath binds cache-backed route identity to the snapshotted main module directive.
func newRouteScopeWithModulePath(root string, compositionPath string, parsed []*parsedFile, modulePath string) (routeScope, error) {
	root = filepath.Clean(root)
	scope := routeScope{
		root:       root,
		modulePath: modulePath,
	}
	if compositionPath == "" {
		return scope, nil
	}
	path := compositionPath
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	path = filepath.ToSlash(filepath.Clean(path))

	for _, pf := range parsed {
		if filepath.ToSlash(filepath.Clean(pf.Path)) != path {
			continue
		}
		scope.active = true
		scope.compositionFile = path
		groups := collectCompositionRouteGroups(pf, scope)
		scope.providers = providersFromScopedRouteGroups(groups)
		scope.placements, scope.inlineRoutes = scopedRoutePlacements(groups)
		scope.providerCalls = providerCallsFromScopedRouteGroups(groups)
		scope.groupCalls = scopedRouteGroupCalls(groups)
		scope.diagnostics = routeCompositionDiagnostics(pf, scope)
		scope.diagnostics = append(scope.diagnostics, historicalRouteCompositionDiagnostics(pf)...)
		scope.diagnostics = append(scope.diagnostics, scopedRoutePlacementDiagnostics(groups)...)
		scope.diagnostics = append(scope.diagnostics, compositionEntrypointDiagnostics(pf)...)
		return scope, nil
	}
	return routeScope{}, fmt.Errorf("route composition file not found: %s", compositionPath)
}

// scopedRouteGroupCalls records only returned group constructors so dead composition calls cannot contribute policy diagnostics.
func scopedRouteGroupCalls(groups []scopedRouteGroup) map[token.Pos]struct{} {
	calls := make(map[token.Pos]struct{}, len(groups))
	for _, group := range groups {
		if group.CallPos != token.NoPos {
			calls[group.CallPos] = struct{}{}
		}
	}
	return calls
}

// historicalRouteCompositionDiagnostics makes unsupported AppRoutes registry control flow explicit instead of publishing a partial field-to-provider map.
func historicalRouteCompositionDiagnostics(pf *parsedFile) []Diagnostic {
	diagnostics := make([]Diagnostic, 0)
	for _, declaration := range pf.File.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Body == nil || function.Name.Name != "ProvideAppRoutes" {
			continue
		}
		variables := map[string]bool{}
		hasReturn := false
		supportedReturn := false
		unsupportedControl := false
		for _, statement := range function.Body.List {
			switch statement := statement.(type) {
			case *ast.AssignStmt:
				for index, left := range statement.Lhs {
					identifier, ok := left.(*ast.Ident)
					if !ok || identifier.Name == "_" || index >= len(statement.Rhs) {
						continue
					}
					variables[identifier.Name] = appRoutesExpressionSupported(statement.Rhs[index], variables)
				}
			case *ast.DeclStmt:
				general, ok := statement.Decl.(*ast.GenDecl)
				if !ok {
					continue
				}
				for _, rawSpecification := range general.Specs {
					specification, ok := rawSpecification.(*ast.ValueSpec)
					if !ok {
						continue
					}
					for index, name := range specification.Names {
						if name.Name == "_" || index >= len(specification.Values) {
							continue
						}
						variables[name.Name] = appRoutesExpressionSupported(specification.Values[index], variables)
					}
				}
			case *ast.ReturnStmt:
				hasReturn = true
				for _, result := range statement.Results {
					supportedReturn = supportedReturn || appRoutesExpressionSupported(result, variables)
				}
			case *ast.BlockStmt, *ast.IfStmt, *ast.ForStmt, *ast.RangeStmt, *ast.SwitchStmt, *ast.TypeSwitchStmt, *ast.SelectStmt, *ast.LabeledStmt:
				unsupportedControl = true
			}
		}
		if unsupportedControl {
			diagnostics = append(diagnostics, Diagnostic{
				Severity: "warn",
				Code:     "route_registry_unsupported_control_flow",
				Message:  "ProvideAppRoutes uses branch or loop control flow that cannot be indexed safely",
				File:     filepath.ToSlash(pf.Path),
			})
		}
		if !hasReturn || !supportedReturn {
			diagnostics = append(diagnostics, Diagnostic{
				Severity: "warn",
				Code:     "route_registry_unsupported_return_expression",
				Message:  "ProvideAppRoutes does not return a statically supported AppRoutes composite",
				File:     filepath.ToSlash(pf.Path),
			})
		}
	}
	sortDiagnostics(diagnostics)
	return diagnostics
}

// appRoutesExpressionSupported follows direct or assigned AppRoutes composites while leaving route-field contents to provider tracing.
func appRoutesExpressionSupported(expression ast.Expr, variables map[string]bool) bool {
	switch value := expression.(type) {
	case *ast.Ident:
		return variables[value.Name]
	case *ast.ParenExpr:
		return appRoutesExpressionSupported(value.X, variables)
	case *ast.UnaryExpr:
		return appRoutesExpressionSupported(value.X, variables)
	case *ast.CompositeLit:
		typeName := typeNameFromExpr(value.Type)
		return typeName == "AppRoutes" || typeName == "router.AppRoutes"
	}
	return false
}

// routeCompositionDiagnostics surfaces composition expressions outside the supported static vocabulary instead of silently publishing an incomplete app index.
func routeCompositionDiagnostics(pf *parsedFile, scope routeScope) []Diagnostic {
	diagnostics := make([]Diagnostic, 0)
	seen := map[string]struct{}{}
	appendDiagnostic := func(code string, message string) {
		key := code + "|" + message
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		diagnostics = append(diagnostics, Diagnostic{
			Severity: "warn",
			Code:     code,
			Message:  message,
			File:     filepath.ToSlash(pf.Path),
		})
	}

	for _, decl := range pf.File.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil || fn.Name.Name != "ProvideRoutes" {
			continue
		}
		paramProviders := routeParamProviders(pf, fn, scope)
		imports := sourceImportPathsByAlias(pf.File)
		returnedGroupCalls := returnedCompositionGroupCalls(fn, imports)
		routeVarSupport := map[string]bool{}
		for field := range historicalRouteFieldProviders(pf, scope) {
			for _, name := range functionParameterNames(fn) {
				routeVarSupport[name+"."+field] = true
			}
		}
		groupVarSupport := map[string]bool{}
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			switch value := node.(type) {
			case *ast.FuncLit:
				return false
			case *ast.BlockStmt:
				if value == fn.Body {
					return true
				}
				appendDiagnostic(
					"route_composition_unsupported_control_flow",
					"ProvideRoutes uses a nested scope that cannot be indexed safely",
				)
				return false
			case *ast.IfStmt:
				guarded, guardedOK := guardedRouteGroupAppendFromIf(value, imports)
				if guardedOK && groupVarSupport[guarded.Target] && len(guarded.Group.Args) >= 2 {
					_, prefixOK := routeGroupPrefixLiteral(guarded.Group.Args[0])
					routesOK := routeCompositionRoutesExprSupported(guarded.Group.Args[1], paramProviders, routeVarSupport, imports)
					if prefixOK && routesOK {
						groupVarSupport[guarded.Target] = true
						return false
					}
				}
				appendDiagnostic(
					"route_composition_unsupported_control_flow",
					"ProvideRoutes uses branch or loop control flow that cannot be indexed safely",
				)
				return false
			case *ast.ForStmt, *ast.RangeStmt, *ast.SwitchStmt, *ast.TypeSwitchStmt, *ast.SelectStmt, *ast.LabeledStmt:
				appendDiagnostic(
					"route_composition_unsupported_control_flow",
					"ProvideRoutes uses branch or loop control flow that cannot be indexed safely",
				)
				return false
			case *ast.AssignStmt:
				trackedRoutes := routeSupportCallSets(routeVarSupport)
				if routeExpressionsEscapeTrackedCalls(value.Rhs, trackedRoutes, imports) || routeAssignmentEscapesOrMutatesTrackedCalls(value, trackedRoutes, paramProviders, imports) {
					appendDiagnostic(
						"route_composition_unsafe_route_value_flow",
						"ProvideRoutes mutates or exposes a tracked route value before return",
					)
				}
				for i, lhs := range value.Lhs {
					ident, ok := lhs.(*ast.Ident)
					if !ok || ident.Name == "_" || i >= len(value.Rhs) {
						continue
					}
					routeVarSupport[ident.Name] = routeCompositionRoutesExprSupported(value.Rhs[i], paramProviders, routeVarSupport, imports)
					groupVarSupport[ident.Name] = routeCompositionGroupsExprSupported(value.Rhs[i], groupVarSupport, imports)
				}
			case *ast.ValueSpec:
				if routeExpressionsEscapeTrackedCalls(value.Values, routeSupportCallSets(routeVarSupport), imports) {
					appendDiagnostic(
						"route_composition_unsafe_route_value_flow",
						"ProvideRoutes mutates or exposes a tracked route value before return",
					)
				}
				if len(value.Values) == 0 && isSliceTypeExpression(value.Type) {
					for _, name := range value.Names {
						routeVarSupport[name.Name] = true
						groupVarSupport[name.Name] = true
					}
				}
				for i, name := range value.Names {
					if name.Name == "_" || i >= len(value.Values) {
						continue
					}
					routeVarSupport[name.Name] = routeCompositionRoutesExprSupported(value.Values[i], paramProviders, routeVarSupport, imports)
					groupVarSupport[name.Name] = routeCompositionGroupsExprSupported(value.Values[i], groupVarSupport, imports)
				}
			case *ast.CallExpr:
				if !isRouteGroupCall(value, imports) || len(value.Args) < 2 {
					return true
				}
				if _, returned := returnedGroupCalls[value.Pos()]; !returned {
					return true
				}
				if _, ok := routeGroupPrefixLiteral(value.Args[0]); !ok {
					appendDiagnostic(
						"route_composition_dynamic_prefix",
						fmt.Sprintf("route group prefix %q is dynamic and cannot be indexed", exprString(value.Args[0])),
					)
				}
				if !routeCompositionRoutesExprSupported(value.Args[1], paramProviders, routeVarSupport, imports) {
					appendDiagnostic(
						"route_composition_unsupported_routes_expression",
						fmt.Sprintf("route group expression %q is not supported; use a route variable, append, slices.Concat, a composite literal, or a controller *Routes method", exprString(value.Args[1])),
					)
				}
			case *ast.ReturnStmt:
				for _, result := range value.Results {
					if routeCompositionGroupsExprSupported(result, groupVarSupport, imports) {
						continue
					}
					appendDiagnostic(
						"route_composition_unsupported_return_expression",
						fmt.Sprintf("returned route-group expression %q is not supported; use a group variable, append, slices.Concat, or a composite literal", exprString(result)),
					)
				}
			case *ast.ExprStmt:
				if routeExpressionsEscapeTrackedCalls([]ast.Expr{value.X}, routeSupportCallSets(routeVarSupport), imports) {
					appendDiagnostic(
						"route_composition_unsafe_route_value_flow",
						"ProvideRoutes mutates or exposes a tracked route value before return",
					)
				}
			case *ast.DeferStmt:
				if routeExpressionsEscapeTrackedCalls([]ast.Expr{value.Call}, routeSupportCallSets(routeVarSupport), imports) {
					appendDiagnostic(
						"route_composition_unsafe_route_value_flow",
						"ProvideRoutes mutates or exposes a tracked route value before return",
					)
				}
			case *ast.GoStmt:
				if routeExpressionsEscapeTrackedCalls([]ast.Expr{value.Call}, routeSupportCallSets(routeVarSupport), imports) {
					appendDiagnostic(
						"route_composition_unsafe_route_value_flow",
						"ProvideRoutes mutates or exposes a tracked route value before return",
					)
				}
			case *ast.SendStmt:
				if routeExpressionReferencesTrackedCalls(value.Value, routeSupportCallSets(routeVarSupport)) {
					appendDiagnostic(
						"route_composition_unsafe_route_value_flow",
						"ProvideRoutes mutates or exposes a tracked route value before return",
					)
				}
			}
			return true
		})
	}

	sort.Slice(diagnostics, func(i, j int) bool {
		if diagnostics[i].Code == diagnostics[j].Code {
			return diagnostics[i].Message < diagnostics[j].Message
		}
		return diagnostics[i].Code < diagnostics[j].Code
	})
	return diagnostics
}

// routeSupportCallSets adapts composition support state to the identity-only mutation and escape checks used by provider return flow.
func routeSupportCallSets(support map[string]bool) map[string]routeCallSet {
	tracked := map[string]routeCallSet{}
	for name, supported := range support {
		if supported {
			tracked[name] = routeCallSet{}
		}
	}
	return tracked
}

// returnedCompositionGroupCalls traces group constructor positions through top-level generated variables without requiring the group itself to be statically valid.
func returnedCompositionGroupCalls(function *ast.FuncDecl, imports map[string]string) routeCallSet {
	variables := map[string]routeCallSet{}
	returned := routeCallSet{}
	for _, statement := range function.Body.List {
		switch statement := statement.(type) {
		case *ast.AssignStmt:
			for index, left := range statement.Lhs {
				identifier, ok := left.(*ast.Ident)
				if !ok || identifier.Name == "_" || index >= len(statement.Rhs) {
					continue
				}
				calls, supported := compositionGroupCallsFromExpr(statement.Rhs[index], variables, imports)
				if supported {
					variables[identifier.Name] = calls
				} else {
					delete(variables, identifier.Name)
				}
			}
		case *ast.DeclStmt:
			declaration, ok := statement.Decl.(*ast.GenDecl)
			if !ok {
				continue
			}
			for _, rawSpecification := range declaration.Specs {
				specification, ok := rawSpecification.(*ast.ValueSpec)
				if !ok {
					continue
				}
				if len(specification.Values) == 0 && isSliceTypeExpression(specification.Type) {
					for _, name := range specification.Names {
						variables[name.Name] = routeCallSet{}
					}
				}
				for index, name := range specification.Names {
					if name.Name == "_" || index >= len(specification.Values) {
						continue
					}
					calls, supported := compositionGroupCallsFromExpr(specification.Values[index], variables, imports)
					if supported {
						variables[name.Name] = calls
					}
				}
			}
		case *ast.ReturnStmt:
			for _, result := range statement.Results {
				calls, supported := compositionGroupCallsFromExpr(result, variables, imports)
				if supported {
					mergeRouteCallSet(returned, calls)
				}
			}
		case *ast.IfStmt:
			guarded, ok := guardedRouteGroupAppendFromIf(statement, imports)
			if !ok {
				continue
			}
			calls, known := variables[guarded.Target]
			if !known {
				continue
			}
			calls = copyRouteCallSet(calls)
			calls[guarded.Group.Pos()]++
			variables[guarded.Target] = calls
		}
	}
	return returned
}

// compositionGroupCallsFromExpr propagates group positions through the supported slice vocabulary while leaving validation to composition diagnostics.
func compositionGroupCallsFromExpr(expression ast.Expr, variables map[string]routeCallSet, imports map[string]string) (routeCallSet, bool) {
	switch value := expression.(type) {
	case *ast.Ident:
		if value.Name == "nil" {
			return routeCallSet{}, true
		}
		calls, known := variables[value.Name]
		return copyRouteCallSet(calls), known
	case *ast.CallExpr:
		if isRouteGroupCall(value, imports) {
			return routeCallSet{value.Pos(): 1}, true
		}
		if !isAppendCall(value) && !isSlicesConcatCall(value, imports) {
			return nil, false
		}
		calls := routeCallSet{}
		for _, argument := range value.Args {
			argumentCalls, supported := compositionGroupCallsFromExpr(argument, variables, imports)
			if !supported {
				return nil, false
			}
			mergeRouteCallSet(calls, argumentCalls)
		}
		return calls, true
	case *ast.CompositeLit:
		calls := routeCallSet{}
		for _, element := range value.Elts {
			elementExpression, ok := element.(ast.Expr)
			if !ok {
				return nil, false
			}
			elementCalls, supported := compositionGroupCallsFromExpr(elementExpression, variables, imports)
			if !supported {
				return nil, false
			}
			mergeRouteCallSet(calls, elementCalls)
		}
		return calls, true
	case *ast.KeyValueExpr:
		return compositionGroupCallsFromExpr(value.Value, variables, imports)
	case *ast.ParenExpr:
		return compositionGroupCallsFromExpr(value.X, variables, imports)
	case *ast.UnaryExpr:
		return compositionGroupCallsFromExpr(value.X, variables, imports)
	}
	return nil, false
}

// guardedRouteGroupAppendFromIf recognizes GoForj's historical `if len(routes) > 0` wrapper only when the guarded value is the exact slice passed to one group append.
func guardedRouteGroupAppendFromIf(statement *ast.IfStmt, imports map[string]string) (guardedRouteGroupAppend, bool) {
	if statement == nil || statement.Init != nil || statement.Else != nil || statement.Body == nil || len(statement.Body.List) != 1 {
		return guardedRouteGroupAppend{}, false
	}
	guardedRoutes, ok := positiveLengthGuardExpression(statement.Cond)
	if !ok {
		return guardedRouteGroupAppend{}, false
	}
	assignment, ok := statement.Body.List[0].(*ast.AssignStmt)
	if !ok || assignment.Tok != token.ASSIGN || len(assignment.Lhs) != 1 || len(assignment.Rhs) != 1 {
		return guardedRouteGroupAppend{}, false
	}
	target, ok := assignment.Lhs[0].(*ast.Ident)
	if !ok || target.Name == "_" {
		return guardedRouteGroupAppend{}, false
	}
	appendCall, ok := assignment.Rhs[0].(*ast.CallExpr)
	if !ok || !isAppendCall(appendCall) || len(appendCall.Args) != 2 {
		return guardedRouteGroupAppend{}, false
	}
	accumulator, ok := appendCall.Args[0].(*ast.Ident)
	if !ok || accumulator.Name != target.Name {
		return guardedRouteGroupAppend{}, false
	}
	group, ok := appendCall.Args[1].(*ast.CallExpr)
	if !ok || !isRouteGroupCall(group, imports) || len(group.Args) < 2 {
		return guardedRouteGroupAppend{}, false
	}
	if exprString(guardedRoutes) != exprString(group.Args[1]) {
		return guardedRouteGroupAppend{}, false
	}
	return guardedRouteGroupAppend{Target: target.Name, Group: group}, true
}

// positiveLengthGuardExpression returns the slice from the exact positive length check emitted by the historical route registry.
func positiveLengthGuardExpression(expression ast.Expr) (ast.Expr, bool) {
	binary, ok := expression.(*ast.BinaryExpr)
	if !ok {
		return nil, false
	}
	if binary.Op == token.GTR {
		routes, lengthOK := lengthCallExpression(binary.X)
		if lengthOK && isIntegerZero(binary.Y) {
			return routes, true
		}
	}
	return nil, false
}

// lengthCallExpression accepts only the predeclared len function so a local helper cannot impersonate the generated guard.
func lengthCallExpression(expression ast.Expr) (ast.Expr, bool) {
	call, ok := expression.(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		return nil, false
	}
	identifier, ok := call.Fun.(*ast.Ident)
	if !ok || identifier.Name != "len" || identifier.Obj != nil {
		return nil, false
	}
	return call.Args[0], true
}

// isIntegerZero recognizes the untyped zero literal used by generated positive-length checks.
func isIntegerZero(expression ast.Expr) bool {
	literal, ok := expression.(*ast.BasicLit)
	return ok && literal.Kind == token.INT && literal.Value == "0"
}

// routeCompositionRoutesExprSupported defines the route-slice expressions the static evaluator can trace without executing project code.
func routeCompositionRoutesExprSupported(expr ast.Expr, paramProviders map[string]routeProvider, varSupport map[string]bool, imports map[string]string) bool {
	if _, ok := routeProviderFromRoutesArg(expr, paramProviders); ok {
		return true
	}
	switch value := expr.(type) {
	case *ast.Ident:
		if value.Name == "nil" {
			return true
		}
		supported, known := varSupport[value.Name]
		return known && supported
	case *ast.SelectorExpr:
		supported, known := varSupport[exprString(value)]
		return known && supported
	case *ast.CallExpr:
		if isDirectRouteCall(value, imports) {
			return true
		}
		if !isAppendCall(value) && !isSlicesConcatCall(value, imports) {
			return false
		}
		for _, arg := range value.Args {
			if !routeCompositionRoutesExprSupported(arg, paramProviders, varSupport, imports) {
				return false
			}
		}
		return true
	case *ast.CompositeLit:
		for _, element := range value.Elts {
			elementExpr, ok := element.(ast.Expr)
			if ok && !routeCompositionRoutesExprSupported(elementExpr, paramProviders, varSupport, imports) {
				return false
			}
		}
		return true
	case *ast.KeyValueExpr:
		return routeCompositionRoutesExprSupported(value.Value, paramProviders, varSupport, imports)
	case *ast.ParenExpr:
		return routeCompositionRoutesExprSupported(value.X, paramProviders, varSupport, imports)
	case *ast.UnaryExpr:
		return routeCompositionRoutesExprSupported(value.X, paramProviders, varSupport, imports)
	}
	return false
}

// routeCompositionGroupsExprSupported defines the group-list expressions that can be followed to a returned app composition.
func routeCompositionGroupsExprSupported(expr ast.Expr, varSupport map[string]bool, imports map[string]string) bool {
	switch value := expr.(type) {
	case *ast.Ident:
		if value.Name == "nil" {
			return true
		}
		supported, known := varSupport[value.Name]
		return known && supported
	case *ast.CallExpr:
		if isRouteGroupCall(value, imports) {
			return true
		}
		if !isAppendCall(value) && !isSlicesConcatCall(value, imports) {
			return false
		}
		for _, arg := range value.Args {
			if !routeCompositionGroupsExprSupported(arg, varSupport, imports) {
				return false
			}
		}
		return true
	case *ast.CompositeLit:
		for _, element := range value.Elts {
			elementExpr, ok := element.(ast.Expr)
			if ok && !routeCompositionGroupsExprSupported(elementExpr, varSupport, imports) {
				return false
			}
		}
		return true
	case *ast.KeyValueExpr:
		return routeCompositionGroupsExprSupported(value.Value, varSupport, imports)
	case *ast.ParenExpr:
		return routeCompositionGroupsExprSupported(value.X, varSupport, imports)
	case *ast.UnaryExpr:
		return routeCompositionGroupsExprSupported(value.X, varSupport, imports)
	}
	return false
}

// isRouteGroupCall recognizes the framework group constructor shared by current and historical composition source.
func isRouteGroupCall(call *ast.CallExpr, imports map[string]string) bool {
	name, ok := frameworkConstructorName(call, imports)
	return ok && name == "NewRouteGroup"
}

// isDirectRouteCall permits route literals in composition files because discovery can index them without tracing a provider method.
func isDirectRouteCall(call *ast.CallExpr, imports map[string]string) bool {
	name, ok := frameworkConstructorName(call, imports)
	return ok && (name == "NewRoute" || name == "NewWebSocketRoute")
}

// includesRoute accepts only the exact provider methods reachable from the selected composition file while still allowing routes declared inline there.
func (s routeScope) includesRoute(route discoveredRoute) bool {
	if !s.active {
		return true
	}
	if filepath.ToSlash(filepath.Clean(route.File)) == s.compositionFile {
		_, included := s.inlineRoutes[route.CallPos]
		return included
	}
	return s.includesProvider(route.Provider)
}

// includesProvider reports whether a route-producing method is reachable from the selected composition entrypoint.
func (s routeScope) includesProvider(candidate routeProvider) bool {
	if !s.active {
		return true
	}
	for _, provider := range s.providers {
		if routeProvidersMatch(candidate, provider) {
			return true
		}
	}
	return false
}

// includesGroupCall restricts composition evidence to group constructors that flow to the selected entrypoint return.
func (s routeScope) includesGroupCall(path string, position token.Pos) bool {
	if !s.active {
		return true
	}
	if filepath.ToSlash(filepath.Clean(path)) != s.compositionFile {
		return false
	}
	_, included := s.groupCalls[position]
	return included
}

// packageImportPath identifies parsed packages by module import path, preventing equal package names in separate app trees from being treated as the same provider.
func (s routeScope) packageImportPath(filePath string) string {
	if s.root == "" || s.modulePath == "" {
		return ""
	}
	dir := filepath.Dir(filepath.Clean(filePath))
	rel, err := filepath.Rel(s.root, dir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ""
	}
	if rel == "." {
		return s.modulePath
	}
	return strings.TrimSuffix(s.modulePath, "/") + "/" + filepath.ToSlash(rel)
}

// modulePathFromRoot reads only the module directive because route identity does not require loading or type-checking the project.
func modulePathFromRoot(root string) string {
	contents, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return ""
	}
	return modulePathFromData(contents)
}

// modulePathFromData reads only the module directive from an immutable go.mod snapshot.
func modulePathFromData(contents []byte) string {
	for _, line := range strings.Split(string(contents), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "module" {
			return strings.TrimSpace(fields[1])
		}
	}
	return ""
}

// collectCompositionRouteGroups traces only groups returned by the selected composition entrypoint.
func collectCompositionRouteGroups(pf *parsedFile, scope routeScope) []scopedRouteGroup {
	groups := make([]scopedRouteGroup, 0)
	for _, decl := range pf.File.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil || fn.Name.Name != "ProvideRoutes" {
			continue
		}
		groups = append(groups, returnedScopedRouteGroups(pf, fn, scope)...)
	}
	return groups
}

// providersFromScopedRouteGroups returns deterministic provider identity without discarding placement evidence from the groups themselves.
func providersFromScopedRouteGroups(groups []scopedRouteGroup) []routeProvider {
	providers := routeProviderSet{}
	for _, group := range groups {
		mergeRouteProviderSet(providers, group.Providers)
	}
	return routeProviderSetValues(providers)
}

// providerCallsFromScopedRouteGroups records the first source-selected invocation after duplicate placement diagnostics make repeated providers publication-blocking.
func providerCallsFromScopedRouteGroups(groups []scopedRouteGroup) map[routeProvider]routeProviderCall {
	calls := map[routeProvider]routeProviderCall{}
	for _, group := range groups {
		for _, call := range group.ProviderCalls {
			if _, exists := calls[call.Provider]; exists {
				continue
			}
			calls[call.Provider] = cloneRouteProviderCall(call)
		}
	}
	return calls
}

// cloneRouteProviderCall prevents later evaluator updates from mutating invocation evidence already attached to a placement.
func cloneRouteProviderCall(call routeProviderCall) routeProviderCall {
	cloned := call
	cloned.Arguments = make([]routeCallArgument, len(call.Arguments))
	for index, argument := range call.Arguments {
		cloned.Arguments[index] = argument
		cloned.Arguments[index].Expressions = append([]string(nil), argument.Expressions...)
	}
	return cloned
}

// scopedRoutePlacements indexes the first placement only after duplicate placement validation has made ambiguity publication-blocking.
func scopedRoutePlacements(groups []scopedRouteGroup) (map[routeProvider]routePlacement, map[token.Pos]routePlacement) {
	providers := map[routeProvider]routePlacement{}
	inline := map[token.Pos]routePlacement{}
	for _, group := range groups {
		placement := routePlacement{
			Prefix:           group.Prefix,
			Middlewares:      append([]string(nil), group.Middlewares...),
			MiddlewareSource: group.MiddlewareSource,
		}
		for _, provider := range group.Providers {
			if _, exists := providers[provider]; !exists {
				providers[provider] = placement
			}
		}
		for _, call := range group.InlineCalls {
			if _, exists := inline[call]; !exists {
				inline[call] = placement
			}
		}
	}
	return providers, inline
}

// scopedRoutePlacementDiagnostics rejects provider or inline route reuse across groups because a single normalized route cannot represent multiple runtime placements safely.
func scopedRoutePlacementDiagnostics(groups []scopedRouteGroup) []Diagnostic {
	providerCounts := map[routeProvider]int{}
	inlineCounts := map[token.Pos]int{}
	for _, group := range groups {
		providers := group.ProviderOccurrences
		if providers == nil {
			providers = group.Providers
		}
		for _, provider := range providers {
			providerCounts[provider]++
		}
		if group.InlineCallCounts != nil {
			for call, count := range group.InlineCallCounts {
				inlineCounts[call] += count
			}
			continue
		}
		for _, call := range group.InlineCalls {
			inlineCounts[call]++
		}
	}
	diagnostics := make([]Diagnostic, 0)
	for provider, count := range providerCounts {
		if count < 2 {
			continue
		}
		diagnostics = append(diagnostics, Diagnostic{
			Severity: "error",
			Code:     "route_provider_multiple_placements",
			Message:  fmt.Sprintf("route provider %s.%s has %d returned placements; use each provider method exactly once", provider.Receiver, provider.Method, count),
		})
	}
	for _, count := range inlineCounts {
		if count < 2 {
			continue
		}
		diagnostics = append(diagnostics, Diagnostic{
			Severity: "error",
			Code:     "inline_route_multiple_placements",
			Message:  "one inline route is reused by multiple returned groups and cannot be projected unambiguously",
		})
	}
	sortDiagnostics(diagnostics)
	return diagnostics
}

// compositionEntrypointDiagnostics prevents a missing or untraceable selected app entrypoint from looking like a valid empty API.
func compositionEntrypointDiagnostics(pf *parsedFile) []Diagnostic {
	state := compositionReturnState(pf)
	found := state.Found
	for _, declaration := range pf.File.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && function.Name.Name == "ProvideRoutes" && function.Body != nil {
			found = true
			break
		}
	}
	if !found {
		return []Diagnostic{{
			Severity: "warn",
			Code:     "route_composition_entrypoint_not_found",
			Message:  "selected composition file does not define ProvideRoutes",
			File:     filepath.ToSlash(pf.Path),
		}}
	}
	if !state.HasReturn || !state.Supported {
		return []Diagnostic{{
			Severity: "warn",
			Code:     "route_composition_no_returned_groups",
			Message:  "ProvideRoutes does not return a statically supported route group",
			File:     filepath.ToSlash(pf.Path),
		}}
	}
	return nil
}

// routeCompositionReturnState distinguishes a valid empty App route set from a missing or unsupported composition entrypoint.
type routeCompositionReturnState struct {
	Found     bool
	HasReturn bool
	Supported bool
}

// compositionReturnState distinguishes an explicitly empty returned group slice from a missing or untraceable composition result.
func compositionReturnState(pf *parsedFile) routeCompositionReturnState {
	state := routeCompositionReturnState{}
	imports := sourceImportPathsByAlias(pf.File)
	for _, declaration := range pf.File.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name.Name != "ProvideRoutes" || function.Body == nil {
			continue
		}
		state.Found = true
		variables := map[string]bool{}
		for _, statement := range function.Body.List {
			switch statement := statement.(type) {
			case *ast.AssignStmt:
				for index, left := range statement.Lhs {
					identifier, ok := left.(*ast.Ident)
					if !ok || identifier.Name == "_" || index >= len(statement.Rhs) {
						continue
					}
					variables[identifier.Name] = routeCompositionGroupsExprSupported(statement.Rhs[index], variables, imports)
				}
			case *ast.DeclStmt:
				declaration, ok := statement.Decl.(*ast.GenDecl)
				if !ok {
					continue
				}
				for _, specification := range declaration.Specs {
					value, ok := specification.(*ast.ValueSpec)
					if !ok {
						continue
					}
					if len(value.Values) == 0 && isSliceTypeExpression(value.Type) {
						for _, name := range value.Names {
							variables[name.Name] = true
						}
					}
					for index, name := range value.Names {
						if name.Name == "_" || index >= len(value.Values) {
							continue
						}
						variables[name.Name] = routeCompositionGroupsExprSupported(value.Values[index], variables, imports)
					}
				}
			case *ast.ReturnStmt:
				state.HasReturn = true
				for _, result := range statement.Results {
					if routeCompositionGroupsExprSupported(result, variables, imports) {
						state.Supported = true
					}
				}
			case *ast.IfStmt:
				guarded, ok := guardedRouteGroupAppendFromIf(statement, imports)
				if !ok || !variables[guarded.Target] {
					continue
				}
				variables[guarded.Target] = true
			}
		}
	}
	return state
}

// returnedScopedRouteGroups evaluates the small composition vocabulary emitted by GoForj: variables, append, slices.Concat, composite literals, and route groups.
func returnedScopedRouteGroups(pf *parsedFile, fn *ast.FuncDecl, scope routeScope) []scopedRouteGroup {
	paramProviders := routeParamProviders(pf, fn, scope)
	imports := sourceImportPathsByAlias(pf.File)
	routeVarProviders := map[string]routeProviderSet{}
	routeVarProviderOccurrences := map[string][]routeProvider{}
	routeVarProviderCalls := map[string][]routeProviderCall{}
	routeVarCalls := map[string]routeCallSet{}
	middlewareValues := compositionMiddlewareParameterValues(fn, imports)
	for field, providers := range historicalRouteFieldProviders(pf, scope) {
		for _, name := range functionParameterNames(fn) {
			key := name + "." + field
			routeVarProviders[key] = providerSliceToSet(providers)
			routeVarProviderOccurrences[key] = append([]routeProvider(nil), providers...)
			routeVarCalls[key] = routeCallSet{}
		}
	}
	for field, calls := range historicalRouteFieldCalls(pf, scope) {
		for _, name := range functionParameterNames(fn) {
			routeVarProviderCalls[name+"."+field] = cloneRouteProviderCalls(calls)
		}
	}
	groupVarGroups := map[string][]scopedRouteGroup{}
	returned := make([]scopedRouteGroup, 0)
	invalidateRouteValues := func() {
		routeVarProviders = map[string]routeProviderSet{}
		routeVarProviderOccurrences = map[string][]routeProvider{}
		routeVarProviderCalls = map[string][]routeProviderCall{}
		routeVarCalls = map[string]routeCallSet{}
		groupVarGroups = map[string][]scopedRouteGroup{}
	}

	for _, statement := range fn.Body.List {
		switch statement := statement.(type) {
		case *ast.AssignStmt:
			type assignmentValue struct {
				providers            []routeProvider
				providerOccurrences  []routeProvider
				providerCalls        []routeProviderCall
				routeCalls           routeCallSet
				routeCallsSupported  bool
				groups               []scopedRouteGroup
				middleware           middlewareValue
				middlewareSupported  bool
				rightHandSidePresent bool
			}
			unsafeRouteValueFlow := routeExpressionsEscapeTrackedCalls(statement.Rhs, routeVarCalls, imports) || routeAssignmentEscapesOrMutatesTrackedCalls(statement, routeVarCalls, paramProviders, imports)
			evaluationMiddleware := middlewareValues
			if middlewareExpressionsEscapeSequence(statement.Rhs, middlewareValues, paramProviders, imports, false) {
				evaluationMiddleware = cloneMiddlewareValues(middlewareValues)
				invalidateMiddlewareSequences(evaluationMiddleware)
			}
			values := make([]assignmentValue, len(statement.Lhs))
			for index := range statement.Lhs {
				if index >= len(statement.Rhs) {
					continue
				}
				expression := statement.Rhs[index]
				values[index].rightHandSidePresent = true
				values[index].providers = routeProvidersFromExpr(expression, paramProviders, routeVarProviders, imports)
				values[index].providerOccurrences = routeProviderOccurrencesFromExpr(expression, paramProviders, routeVarProviderOccurrences, imports)
				values[index].routeCalls, values[index].routeCallsSupported = routeCallPositionsFromExpr(expression, paramProviders, routeVarCalls, imports)
				values[index].providerCalls = routeProviderCallsFromExpr(expression, paramProviders, routeVarProviderCalls, evaluationMiddleware, imports)
				values[index].groups = routeGroupsFromExpr(expression, paramProviders, routeVarProviders, routeVarProviderOccurrences, routeVarProviderCalls, evaluationMiddleware, routeVarCalls, groupVarGroups, imports)
				values[index].middleware, values[index].middlewareSupported = compositionMiddlewareValueFromExpr(expression, paramProviders, evaluationMiddleware, imports)
			}
			mutatesIndexedValue := false
			for index, lhs := range statement.Lhs {
				ident, ok := lhs.(*ast.Ident)
				if !ok {
					mutatesIndexedValue = true
					continue
				}
				if ident.Name == "_" {
					continue
				}
				value := values[index]
				delete(routeVarProviders, ident.Name)
				delete(routeVarProviderOccurrences, ident.Name)
				delete(routeVarProviderCalls, ident.Name)
				delete(routeVarCalls, ident.Name)
				delete(groupVarGroups, ident.Name)
				delete(middlewareValues, ident.Name)
				if !value.rightHandSidePresent {
					continue
				}
				if len(value.providers) > 0 {
					routeVarProviders[ident.Name] = providerSliceToSet(value.providers)
				}
				if value.providerOccurrences != nil {
					routeVarProviderOccurrences[ident.Name] = value.providerOccurrences
				}
				if value.providerCalls != nil {
					routeVarProviderCalls[ident.Name] = value.providerCalls
				}
				if value.routeCallsSupported {
					routeVarCalls[ident.Name] = value.routeCalls
				}
				if len(value.groups) > 0 {
					groupVarGroups[ident.Name] = value.groups
				}
				if value.middlewareSupported {
					middlewareValues[ident.Name] = value.middleware
				}
			}
			if mutatesIndexedValue || middlewareExpressionsEscapeSequence(statement.Rhs, middlewareValues, paramProviders, imports, true) {
				invalidateMiddlewareSequences(middlewareValues)
			}
			if unsafeRouteValueFlow {
				invalidateRouteValues()
			}
		case *ast.DeclStmt:
			declaration, ok := statement.Decl.(*ast.GenDecl)
			if !ok {
				continue
			}
			for _, specification := range declaration.Specs {
				value, ok := specification.(*ast.ValueSpec)
				if !ok {
					continue
				}
				unsafeRouteValueFlow := routeExpressionsEscapeTrackedCalls(value.Values, routeVarCalls, imports)
				if len(value.Values) == 0 && isSliceTypeExpression(value.Type) {
					for _, name := range value.Names {
						routeVarProviders[name.Name] = routeProviderSet{}
						routeVarProviderOccurrences[name.Name] = []routeProvider{}
						routeVarProviderCalls[name.Name] = []routeProviderCall{}
						routeVarCalls[name.Name] = routeCallSet{}
						groupVarGroups[name.Name] = []scopedRouteGroup{}
						if isFrameworkMiddlewareSliceType(value.Type, imports) {
							middlewareValues[name.Name] = middlewareValue{Expressions: []string{}, Sequence: true, Exact: true}
						}
					}
				}
				for i, name := range value.Names {
					if name.Name == "_" || i >= len(value.Values) {
						continue
					}
					expr := value.Values[i]
					if providers := routeProvidersFromExpr(expr, paramProviders, routeVarProviders, imports); len(providers) > 0 {
						routeVarProviders[name.Name] = providerSliceToSet(providers)
					}
					if providers := routeProviderOccurrencesFromExpr(expr, paramProviders, routeVarProviderOccurrences, imports); providers != nil {
						routeVarProviderOccurrences[name.Name] = providers
					}
					if calls := routeProviderCallsFromExpr(expr, paramProviders, routeVarProviderCalls, middlewareValues, imports); calls != nil {
						routeVarProviderCalls[name.Name] = calls
					}
					if calls, supported := routeCallPositionsFromExpr(expr, paramProviders, routeVarCalls, imports); supported {
						routeVarCalls[name.Name] = calls
					}
					if groups := routeGroupsFromExpr(expr, paramProviders, routeVarProviders, routeVarProviderOccurrences, routeVarProviderCalls, middlewareValues, routeVarCalls, groupVarGroups, imports); len(groups) > 0 {
						groupVarGroups[name.Name] = groups
					}
					if middlewareValue, supported := compositionMiddlewareValueFromExpr(expr, paramProviders, middlewareValues, imports); supported {
						middlewareValues[name.Name] = middlewareValue
					}
				}
				if unsafeRouteValueFlow {
					invalidateRouteValues()
				}
			}
		case *ast.ReturnStmt:
			for _, result := range statement.Results {
				returned = append(returned, routeGroupsFromExpr(result, paramProviders, routeVarProviders, routeVarProviderOccurrences, routeVarProviderCalls, middlewareValues, routeVarCalls, groupVarGroups, imports)...)
			}
		case *ast.ExprStmt:
			if middlewareExpressionsEscapeSequence([]ast.Expr{statement.X}, middlewareValues, paramProviders, imports, true) {
				invalidateMiddlewareSequences(middlewareValues)
			}
			if routeExpressionsEscapeTrackedCalls([]ast.Expr{statement.X}, routeVarCalls, imports) {
				invalidateRouteValues()
			}
		case *ast.DeferStmt:
			if routeExpressionsEscapeTrackedCalls([]ast.Expr{statement.Call}, routeVarCalls, imports) {
				invalidateRouteValues()
			}
		case *ast.GoStmt:
			if routeExpressionsEscapeTrackedCalls([]ast.Expr{statement.Call}, routeVarCalls, imports) {
				invalidateRouteValues()
			}
		case *ast.SendStmt:
			if routeExpressionReferencesTrackedCalls(statement.Value, routeVarCalls) {
				invalidateRouteValues()
			}
		case *ast.IfStmt:
			guarded, ok := guardedRouteGroupAppendFromIf(statement, imports)
			if !ok {
				invalidateRouteValues()
				continue
			}
			existing, known := groupVarGroups[guarded.Target]
			if !known {
				continue
			}
			groups := routeGroupsFromExpr(guarded.Group, paramProviders, routeVarProviders, routeVarProviderOccurrences, routeVarProviderCalls, middlewareValues, routeVarCalls, groupVarGroups, imports)
			groupVarGroups[guarded.Target] = append(existing, groups...)
		case *ast.BlockStmt, *ast.ForStmt, *ast.RangeStmt, *ast.SwitchStmt, *ast.TypeSwitchStmt, *ast.SelectStmt, *ast.LabeledStmt:
			invalidateRouteValues()
		}
	}

	source := middlewareSource{File: pf.Path, Function: fn.Name.Name, Receiver: receiverName(fn)}
	for index := range returned {
		returned[index].MiddlewareSource = source
	}
	return returned
}

// routeCallPositionsFromExpr follows inline route calls through the same generated slice vocabulary used for provider references.
func routeCallPositionsFromExpr(expr ast.Expr, paramProviders map[string]routeProvider, variables map[string]routeCallSet, imports map[string]string) (routeCallSet, bool) {
	if _, provider := routeProviderFromRoutesArg(expr, paramProviders); provider {
		return routeCallSet{}, true
	}
	switch value := expr.(type) {
	case *ast.Ident:
		if value.Name == "nil" {
			return routeCallSet{}, true
		}
		calls, known := variables[value.Name]
		return copyRouteCallSet(calls), known
	case *ast.SelectorExpr:
		calls, known := variables[exprString(value)]
		return copyRouteCallSet(calls), known
	case *ast.CallExpr:
		if isDirectRouteCall(value, imports) {
			return routeCallSet{value.Pos(): 1}, true
		}
		if !isAppendCall(value) && !isSlicesConcatCall(value, imports) {
			return nil, false
		}
		calls := routeCallSet{}
		for _, argument := range value.Args {
			argumentCalls, supported := routeCallPositionsFromExpr(argument, paramProviders, variables, imports)
			if !supported {
				return nil, false
			}
			mergeRouteCallSet(calls, argumentCalls)
		}
		return calls, true
	case *ast.CompositeLit:
		calls := routeCallSet{}
		for _, element := range value.Elts {
			elementExpression, ok := element.(ast.Expr)
			if !ok {
				return nil, false
			}
			elementCalls, supported := routeCallPositionsFromExpr(elementExpression, paramProviders, variables, imports)
			if !supported {
				return nil, false
			}
			mergeRouteCallSet(calls, elementCalls)
		}
		return calls, true
	case *ast.KeyValueExpr:
		return routeCallPositionsFromExpr(value.Value, paramProviders, variables, imports)
	case *ast.ParenExpr:
		return routeCallPositionsFromExpr(value.X, paramProviders, variables, imports)
	case *ast.UnaryExpr:
		return routeCallPositionsFromExpr(value.X, paramProviders, variables, imports)
	}
	return nil, false
}

// routeCallsOrEmpty isolates inline-call evidence from provider expressions, which are tracked independently by exact provider identity.
func routeCallsOrEmpty(expr ast.Expr, paramProviders map[string]routeProvider, variables map[string]routeCallSet, imports map[string]string) routeCallSet {
	calls, supported := routeCallPositionsFromExpr(expr, paramProviders, variables, imports)
	if !supported {
		return nil
	}
	return calls
}

// copyRouteCallSet prevents later variable assignments from mutating evidence already attached to another expression.
func copyRouteCallSet(source routeCallSet) routeCallSet {
	if source == nil {
		return nil
	}
	copied := make(routeCallSet, len(source))
	mergeRouteCallSet(copied, source)
	return copied
}

// mergeRouteCallSet unions statically reachable inline route calls across supported slice expressions.
func mergeRouteCallSet(target routeCallSet, source routeCallSet) {
	for position, count := range source {
		target[position] += count
	}
}

// routeCallSetValues returns source-order call positions for deterministic placement and diagnostics.
func routeCallSetValues(calls routeCallSet) []token.Pos {
	positions := make([]token.Pos, 0, len(calls))
	for position := range calls {
		positions = append(positions, position)
	}
	sort.Slice(positions, func(i, j int) bool { return positions[i] < positions[j] })
	return positions
}

// returnedRouteCalls evaluates only top-level straight-line statements so nested branches cannot overwrite route variables or activate dead calls.
func returnedRouteCalls(fn *ast.FuncDecl, imports map[string]string) returnedRouteFlow {
	flow := returnedRouteFlow{Calls: routeCallSet{}, Supported: true}
	variables := map[string]routeCallSet{}
	for _, statement := range fn.Body.List {
		switch statement := statement.(type) {
		case *ast.AssignStmt:
			type assignmentValue struct {
				calls     routeCallSet
				supported bool
				present   bool
			}
			values := make([]assignmentValue, len(statement.Lhs))
			for index := range statement.Lhs {
				if index >= len(statement.Rhs) {
					continue
				}
				values[index].present = true
				values[index].calls, values[index].supported = routeCallPositionsFromExpr(statement.Rhs[index], nil, variables, imports)
			}
			if routeExpressionsEscapeTrackedCalls(statement.Rhs, variables, imports) || routeAssignmentEscapesOrMutatesTrackedCalls(statement, variables, nil, imports) {
				flow.UnsafeMutationOrEscape = true
				flow.Supported = false
			}
			for index, left := range statement.Lhs {
				identifier, ok := left.(*ast.Ident)
				if !ok || identifier.Name == "_" {
					continue
				}
				value := values[index]
				if !value.present || !value.supported {
					delete(variables, identifier.Name)
					continue
				}
				variables[identifier.Name] = value.calls
			}
		case *ast.DeclStmt:
			declaration, ok := statement.Decl.(*ast.GenDecl)
			if !ok {
				continue
			}
			for _, specification := range declaration.Specs {
				value, ok := specification.(*ast.ValueSpec)
				if !ok {
					continue
				}
				if len(value.Values) == 0 && isSliceTypeExpression(value.Type) {
					for _, name := range value.Names {
						variables[name.Name] = routeCallSet{}
					}
				}
				if routeExpressionsEscapeTrackedCalls(value.Values, variables, imports) {
					flow.UnsafeMutationOrEscape = true
					flow.Supported = false
				}
				type declarationValue struct {
					calls     routeCallSet
					supported bool
					present   bool
				}
				values := make([]declarationValue, len(value.Names))
				for index := range value.Names {
					if index >= len(value.Values) {
						continue
					}
					values[index].present = true
					values[index].calls, values[index].supported = routeCallPositionsFromExpr(value.Values[index], nil, variables, imports)
				}
				for index, name := range value.Names {
					if name.Name == "_" {
						continue
					}
					declaration := values[index]
					if !declaration.present {
						continue
					}
					if !declaration.supported {
						delete(variables, name.Name)
						continue
					}
					variables[name.Name] = declaration.calls
				}
			}
		case *ast.ReturnStmt:
			flow.HasReturn = true
			resultSupported := false
			for _, result := range statement.Results {
				calls, supported := routeCallPositionsFromExpr(result, nil, variables, imports)
				if !supported {
					continue
				}
				resultSupported = true
				mergeRouteCallSet(flow.Calls, calls)
			}
			if !resultSupported {
				flow.Supported = false
			}
		case *ast.ExprStmt:
			if routeExpressionsEscapeTrackedCalls([]ast.Expr{statement.X}, variables, imports) {
				flow.UnsafeMutationOrEscape = true
				flow.Supported = false
			}
		case *ast.DeferStmt:
			if routeExpressionsEscapeTrackedCalls([]ast.Expr{statement.Call}, variables, imports) {
				flow.UnsafeMutationOrEscape = true
				flow.Supported = false
			}
		case *ast.GoStmt:
			if routeExpressionsEscapeTrackedCalls([]ast.Expr{statement.Call}, variables, imports) {
				flow.UnsafeMutationOrEscape = true
				flow.Supported = false
			}
		case *ast.SendStmt:
			if routeExpressionReferencesTrackedCalls(statement.Value, variables) {
				flow.UnsafeMutationOrEscape = true
				flow.Supported = false
			}
		case *ast.BlockStmt, *ast.IfStmt, *ast.ForStmt, *ast.RangeStmt, *ast.SwitchStmt, *ast.TypeSwitchStmt, *ast.SelectStmt, *ast.LabeledStmt:
			flow.UnsupportedControlFlow = true
			flow.Supported = false
		}
	}
	if !flow.HasReturn {
		flow.Supported = false
	}
	return flow
}

// routeAssignmentEscapesOrMutatesTrackedCalls rejects indexed mutation and storage that can expose a tracked route value outside the bounded alias vocabulary.
func routeAssignmentEscapesOrMutatesTrackedCalls(statement *ast.AssignStmt, variables map[string]routeCallSet, paramProviders map[string]routeProvider, imports map[string]string) bool {
	if statement == nil {
		return false
	}
	for index, left := range statement.Lhs {
		if identifier, ok := left.(*ast.Ident); ok {
			if identifier.Name == "_" || statement.Tok != token.ASSIGN {
				continue
			}
			if _, tracked := variables[identifier.Name]; tracked {
				continue
			}
			if index < len(statement.Rhs) && routeExpressionProducesTrackedCalls(statement.Rhs[index], variables, paramProviders, imports) {
				return true
			}
			continue
		}
		if routeMutationRootIsTracked(left, variables) {
			return true
		}
		if index < len(statement.Rhs) && routeExpressionProducesTrackedCalls(statement.Rhs[index], variables, paramProviders, imports) {
			return true
		}
	}
	return false
}

// routeExpressionProducesTrackedCalls distinguishes a route-valued alias from read-only observations such as len(routes).
func routeExpressionProducesTrackedCalls(expression ast.Expr, variables map[string]routeCallSet, paramProviders map[string]routeProvider, imports map[string]string) bool {
	calls, supported := routeCallPositionsFromExpr(expression, paramProviders, variables, imports)
	return supported && len(calls) > 0
}

// routeMutationRootIsTracked follows assignable wrappers to determine whether an indexed or dereferenced write can alter a tracked route value.
func routeMutationRootIsTracked(expression ast.Expr, variables map[string]routeCallSet) bool {
	switch value := expression.(type) {
	case *ast.Ident:
		_, tracked := variables[value.Name]
		return tracked
	case *ast.IndexExpr:
		return routeMutationRootIsTracked(value.X, variables)
	case *ast.IndexListExpr:
		return routeMutationRootIsTracked(value.X, variables)
	case *ast.SelectorExpr:
		return routeMutationRootIsTracked(value.X, variables)
	case *ast.StarExpr:
		return routeMutationRootIsTracked(value.X, variables)
	case *ast.ParenExpr:
		return routeMutationRootIsTracked(value.X, variables)
	}
	return false
}

// routeExpressionsEscapeTrackedCalls reports opaque calls or closures that can observe or mutate tracked route values before return.
func routeExpressionsEscapeTrackedCalls(expressions []ast.Expr, variables map[string]routeCallSet, imports map[string]string) bool {
	escaped := false
	for _, expression := range expressions {
		ast.Inspect(expression, func(node ast.Node) bool {
			if escaped {
				return false
			}
			switch value := node.(type) {
			case *ast.SliceExpr:
				if routeExpressionReferencesTrackedCalls(value.X, variables) {
					escaped = true
					return false
				}
			case *ast.IndexExpr:
				if routeExpressionReferencesTrackedCalls(value.X, variables) {
					escaped = true
					return false
				}
			case *ast.SelectorExpr:
				if routeExpressionReferencesTrackedCalls(value.X, variables) {
					escaped = true
					return false
				}
			case *ast.UnaryExpr:
				if value.Op == token.AND && routeExpressionReferencesTrackedCalls(value.X, variables) {
					escaped = true
					return false
				}
			}
			if function, ok := node.(*ast.FuncLit); ok {
				if routeExpressionReferencesTrackedCalls(function.Body, variables) {
					escaped = true
				}
				return false
			}
			call, ok := node.(*ast.CallExpr)
			if !ok || !routeCallReferencesTrackedCalls(call, variables) {
				return true
			}
			constructor, frameworkConstructor := frameworkConstructorName(call, imports)
			if isAppendCall(call) || isSlicesConcatCall(call, imports) || isReadOnlyRouteBuiltin(call) || frameworkConstructor && constructor == "NewRouteGroup" {
				return true
			}
			escaped = true
			return false
		})
		if escaped {
			return true
		}
	}
	return false
}

// routeCallReferencesTrackedCalls checks call arguments because invoking a same-named function is harmless unless a tracked value reaches it.
func routeCallReferencesTrackedCalls(call *ast.CallExpr, variables map[string]routeCallSet) bool {
	for _, argument := range call.Args {
		if routeExpressionReferencesTrackedCalls(argument, variables) {
			return true
		}
	}
	return false
}

// routeExpressionReferencesTrackedCalls reports references to variables whose value contains statically selected route constructors.
func routeExpressionReferencesTrackedCalls(node ast.Node, variables map[string]routeCallSet) bool {
	found := false
	ast.Inspect(node, func(candidate ast.Node) bool {
		if found {
			return false
		}
		identifier, ok := candidate.(*ast.Ident)
		if !ok {
			return true
		}
		if _, tracked := variables[identifier.Name]; tracked {
			found = true
			return false
		}
		return true
	})
	return found
}

// isReadOnlyRouteBuiltin permits observation that cannot change a tracked route slice or make it reachable elsewhere.
func isReadOnlyRouteBuiltin(call *ast.CallExpr) bool {
	identifier, ok := call.Fun.(*ast.Ident)
	if !ok || identifier.Obj != nil {
		return false
	}
	return identifier.Name == "len" || identifier.Name == "cap"
}

// routeGroupsFromExpr resolves group-producing expressions without treating unrelated calls elsewhere in the composition file as active routes.
func routeGroupsFromExpr(expr ast.Expr, paramProviders map[string]routeProvider, routeVarProviders map[string]routeProviderSet, routeVarProviderOccurrences map[string][]routeProvider, routeVarProviderCalls map[string][]routeProviderCall, middlewareValues map[string]middlewareValue, routeVarCalls map[string]routeCallSet, groupVarGroups map[string][]scopedRouteGroup, imports map[string]string) []scopedRouteGroup {
	switch value := expr.(type) {
	case *ast.CallExpr:
		if group, ok := routeGroupFromCall(value, paramProviders, routeVarProviders, routeVarProviderOccurrences, routeVarProviderCalls, middlewareValues, routeVarCalls, imports); ok {
			return []scopedRouteGroup{group}
		}
		if isAppendCall(value) || isSlicesConcatCall(value, imports) {
			groups := make([]scopedRouteGroup, 0)
			for _, arg := range value.Args {
				groups = append(groups, routeGroupsFromExpr(arg, paramProviders, routeVarProviders, routeVarProviderOccurrences, routeVarProviderCalls, middlewareValues, routeVarCalls, groupVarGroups, imports)...)
			}
			return groups
		}
	case *ast.CompositeLit:
		groups := make([]scopedRouteGroup, 0, len(value.Elts))
		for _, element := range value.Elts {
			elementExpr, ok := element.(ast.Expr)
			if !ok {
				continue
			}
			groups = append(groups, routeGroupsFromExpr(elementExpr, paramProviders, routeVarProviders, routeVarProviderOccurrences, routeVarProviderCalls, middlewareValues, routeVarCalls, groupVarGroups, imports)...)
		}
		return groups
	case *ast.Ident:
		return append([]scopedRouteGroup(nil), groupVarGroups[value.Name]...)
	case *ast.KeyValueExpr:
		return routeGroupsFromExpr(value.Value, paramProviders, routeVarProviders, routeVarProviderOccurrences, routeVarProviderCalls, middlewareValues, routeVarCalls, groupVarGroups, imports)
	case *ast.ParenExpr:
		return routeGroupsFromExpr(value.X, paramProviders, routeVarProviders, routeVarProviderOccurrences, routeVarProviderCalls, middlewareValues, routeVarCalls, groupVarGroups, imports)
	case *ast.UnaryExpr:
		return routeGroupsFromExpr(value.X, paramProviders, routeVarProviders, routeVarProviderOccurrences, routeVarProviderCalls, middlewareValues, routeVarCalls, groupVarGroups, imports)
	}
	return nil
}

// routeGroupFromCall captures the prefix, provider methods, and middleware attached by web.NewRouteGroup or its historical http alias.
func routeGroupFromCall(call *ast.CallExpr, paramProviders map[string]routeProvider, routeVarProviders map[string]routeProviderSet, routeVarProviderOccurrences map[string][]routeProvider, routeVarProviderCalls map[string][]routeProviderCall, middlewareValues map[string]middlewareValue, routeVarCalls map[string]routeCallSet, imports map[string]string) (scopedRouteGroup, bool) {
	if len(call.Args) < 2 || !isRouteGroupCall(call, imports) {
		return scopedRouteGroup{}, false
	}
	prefix, ok := routeGroupPrefixLiteral(call.Args[0])
	if !ok {
		return scopedRouteGroup{}, false
	}
	inlineCalls := routeCallsOrEmpty(call.Args[1], paramProviders, routeVarCalls, imports)
	return scopedRouteGroup{
		Providers:           routeProvidersFromExpr(call.Args[1], paramProviders, routeVarProviders, imports),
		ProviderOccurrences: routeProviderOccurrencesFromExpr(call.Args[1], paramProviders, routeVarProviderOccurrences, imports),
		ProviderCalls:       routeProviderCallsFromExpr(call.Args[1], paramProviders, routeVarProviderCalls, middlewareValues, imports),
		InlineCalls:         routeCallSetValues(inlineCalls),
		InlineCallCounts:    inlineCalls,
		Prefix:              prefix,
		Middlewares:         middlewareExprsForCall(call, 2),
		CallPos:             call.Pos(),
	}, true
}

// routeGroupPrefixLiteral distinguishes a supported empty root prefix from a dynamic prefix that static composition cannot resolve.
func routeGroupPrefixLiteral(expr ast.Expr) (string, bool) {
	literal, ok := expr.(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return "", false
	}
	value, err := strconv.Unquote(literal.Value)
	if err != nil {
		return "", false
	}
	return value, true
}

// routeParamProviders resolves parameter aliases to import paths when imports are present and retains package names for syntax-only fixtures.
func routeParamProviders(pf *parsedFile, fn *ast.FuncDecl, scope routeScope) map[string]routeProvider {
	providers := map[string]routeProvider{}
	if fn.Type == nil || fn.Type.Params == nil {
		return providers
	}
	imports := importPathsByAlias(pf)
	for _, parameter := range fn.Type.Params.List {
		typeName := typeNameFromExpr(parameter.Type)
		parts := strings.Split(typeName, ".")
		provider := routeProvider{}
		switch len(parts) {
		case 1:
			provider.Package = pf.PackageName
			provider.ImportPath = scope.packageImportPath(pf.Path)
			provider.Receiver = parts[0]
		case 2:
			provider.Package = parts[0]
			provider.ImportPath = imports[parts[0]]
			provider.Receiver = parts[1]
		default:
			continue
		}
		for _, name := range parameter.Names {
			providers[name.Name] = provider
		}
	}
	return providers
}

// functionParameterNames returns declared parameter identifiers without assuming every generated function has parameters.
func functionParameterNames(function *ast.FuncDecl) []string {
	if function == nil || function.Type == nil || function.Type.Params == nil {
		return nil
	}
	names := make([]string, 0)
	for _, parameter := range function.Type.Params.List {
		for _, name := range parameter.Names {
			names = append(names, name.Name)
		}
	}
	return names
}

// historicalRouteFieldProviders resolves the generated AppRoutes registry so scoped indexing retains compatibility with historical composition files.
func historicalRouteFieldProviders(pf *parsedFile, scope routeScope) map[string][]routeProvider {
	fields := map[string][]routeProvider{}
	for _, declaration := range pf.File.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Body == nil || function.Name.Name != "ProvideAppRoutes" {
			continue
		}
		for field, providers := range returnedHistoricalRouteFields(pf, function, scope) {
			fields[field] = append(fields[field], providers...)
		}
	}
	for field := range fields {
		sort.Slice(fields[field], func(i, j int) bool {
			left := fields[field][i]
			right := fields[field][j]
			return left.ImportPath+"|"+left.Package+"|"+left.Receiver+"|"+left.Method < right.ImportPath+"|"+right.Package+"|"+right.Receiver+"|"+right.Method
		})
	}
	return fields
}

// historicalRouteFieldCalls preserves provider actuals stored in returned AppRoutes fields so legacy registries receive the same middleware binding as direct composition.
func historicalRouteFieldCalls(pf *parsedFile, scope routeScope) map[string][]routeProviderCall {
	fields := map[string][]routeProviderCall{}
	for _, declaration := range pf.File.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Body == nil || function.Name.Name != "ProvideAppRoutes" {
			continue
		}
		for field, calls := range returnedHistoricalRouteFieldCalls(pf, function, scope) {
			fields[field] = append(fields[field], cloneRouteProviderCalls(calls)...)
		}
	}
	for field := range fields {
		sort.SliceStable(fields[field], func(i, j int) bool { return fields[field][i].Pos < fields[field][j].Pos })
	}
	return fields
}

// importPathsByAlias preserves explicit aliases and default import names so provider matching can use full package identity when source imports are valid.
func importPathsByAlias(pf *parsedFile) map[string]string {
	imports := map[string]string{}
	for _, spec := range pf.File.Imports {
		importPath, err := strconv.Unquote(spec.Path.Value)
		if err != nil || importPath == "" {
			continue
		}
		alias := path.Base(importPath)
		if spec.Name != nil {
			alias = spec.Name.Name
		}
		if alias == "_" || alias == "." {
			continue
		}
		imports[alias] = importPath
	}
	return imports
}

// routeProvidersFromExpr resolves route-producing expressions and carries provider sets through variables used by append and slices.Concat.
func routeProvidersFromExpr(expr ast.Expr, paramProviders map[string]routeProvider, varProviders map[string]routeProviderSet, imports map[string]string) []routeProvider {
	if provider, ok := routeProviderFromRoutesArg(expr, paramProviders); ok {
		return []routeProvider{provider}
	}
	switch value := expr.(type) {
	case *ast.Ident:
		return routeProviderSetValues(varProviders[value.Name])
	case *ast.SelectorExpr:
		return routeProviderSetValues(varProviders[exprString(value)])
	case *ast.CallExpr:
		if !isAppendCall(value) && !isSlicesConcatCall(value, imports) {
			return nil
		}
		providers := routeProviderSet{}
		for _, arg := range value.Args {
			mergeRouteProviderSet(providers, routeProvidersFromExpr(arg, paramProviders, varProviders, imports))
		}
		return routeProviderSetValues(providers)
	case *ast.CompositeLit:
		providers := routeProviderSet{}
		for _, element := range value.Elts {
			elementExpr, ok := element.(ast.Expr)
			if !ok {
				continue
			}
			mergeRouteProviderSet(providers, routeProvidersFromExpr(elementExpr, paramProviders, varProviders, imports))
		}
		return routeProviderSetValues(providers)
	case *ast.KeyValueExpr:
		return routeProvidersFromExpr(value.Value, paramProviders, varProviders, imports)
	case *ast.ParenExpr:
		return routeProvidersFromExpr(value.X, paramProviders, varProviders, imports)
	case *ast.UnaryExpr:
		return routeProvidersFromExpr(value.X, paramProviders, varProviders, imports)
	}
	return nil
}

// routeProviderOccurrencesFromExpr preserves repeated provider calls so runtime route duplication cannot collapse into one arbitrary placement.
func routeProviderOccurrencesFromExpr(expr ast.Expr, paramProviders map[string]routeProvider, variables map[string][]routeProvider, imports map[string]string) []routeProvider {
	if provider, ok := routeProviderFromRoutesArg(expr, paramProviders); ok {
		return []routeProvider{provider}
	}
	switch value := expr.(type) {
	case *ast.Ident:
		if value.Name == "nil" {
			return []routeProvider{}
		}
		providers, known := variables[value.Name]
		if !known {
			return nil
		}
		return append([]routeProvider{}, providers...)
	case *ast.SelectorExpr:
		providers, known := variables[exprString(value)]
		if !known {
			return nil
		}
		return append([]routeProvider{}, providers...)
	case *ast.CallExpr:
		if isDirectRouteCall(value, imports) {
			return []routeProvider{}
		}
		if !isAppendCall(value) && !isSlicesConcatCall(value, imports) {
			return nil
		}
		providers := make([]routeProvider, 0)
		for _, argument := range value.Args {
			argumentProviders := routeProviderOccurrencesFromExpr(argument, paramProviders, variables, imports)
			if argumentProviders == nil {
				return nil
			}
			providers = append(providers, argumentProviders...)
		}
		return providers
	case *ast.CompositeLit:
		providers := make([]routeProvider, 0)
		for _, element := range value.Elts {
			elementExpression, ok := element.(ast.Expr)
			if !ok {
				return nil
			}
			elementProviders := routeProviderOccurrencesFromExpr(elementExpression, paramProviders, variables, imports)
			if elementProviders == nil {
				return nil
			}
			providers = append(providers, elementProviders...)
		}
		return providers
	case *ast.KeyValueExpr:
		return routeProviderOccurrencesFromExpr(value.Value, paramProviders, variables, imports)
	case *ast.ParenExpr:
		return routeProviderOccurrencesFromExpr(value.X, paramProviders, variables, imports)
	case *ast.UnaryExpr:
		return routeProviderOccurrencesFromExpr(value.X, paramProviders, variables, imports)
	}
	return nil
}

// routeProviderCallsFromExpr preserves selected provider invocations and their middleware actuals through the same route-slice vocabulary as provider identity.
func routeProviderCallsFromExpr(expr ast.Expr, paramProviders map[string]routeProvider, variables map[string][]routeProviderCall, middlewareValues map[string]middlewareValue, imports map[string]string) []routeProviderCall {
	if provider, ok := routeProviderFromRoutesArg(expr, paramProviders); ok {
		call, called := expr.(*ast.CallExpr)
		if !called {
			return nil
		}
		return []routeProviderCall{{
			Provider:  provider,
			Arguments: routeProviderCallArguments(call, middlewareValues, imports),
			Pos:       call.Pos(),
		}}
	}
	switch value := expr.(type) {
	case *ast.Ident:
		if value.Name == "nil" {
			return []routeProviderCall{}
		}
		calls, known := variables[value.Name]
		if !known {
			return nil
		}
		return cloneRouteProviderCalls(calls)
	case *ast.SelectorExpr:
		calls, known := variables[exprString(value)]
		if !known {
			return nil
		}
		return cloneRouteProviderCalls(calls)
	case *ast.CallExpr:
		if isDirectRouteCall(value, imports) {
			return []routeProviderCall{}
		}
		if !isAppendCall(value) && !isSlicesConcatCall(value, imports) {
			return nil
		}
		calls := make([]routeProviderCall, 0)
		for _, argument := range value.Args {
			argumentCalls := routeProviderCallsFromExpr(argument, paramProviders, variables, middlewareValues, imports)
			if argumentCalls == nil {
				return nil
			}
			calls = append(calls, argumentCalls...)
		}
		return calls
	case *ast.CompositeLit:
		calls := make([]routeProviderCall, 0)
		for _, element := range value.Elts {
			elementExpression, ok := element.(ast.Expr)
			if !ok {
				return nil
			}
			elementCalls := routeProviderCallsFromExpr(elementExpression, paramProviders, variables, middlewareValues, imports)
			if elementCalls == nil {
				return nil
			}
			calls = append(calls, elementCalls...)
		}
		return calls
	case *ast.KeyValueExpr:
		return routeProviderCallsFromExpr(value.Value, paramProviders, variables, middlewareValues, imports)
	case *ast.ParenExpr:
		return routeProviderCallsFromExpr(value.X, paramProviders, variables, middlewareValues, imports)
	case *ast.UnaryExpr:
		return routeProviderCallsFromExpr(value.X, paramProviders, variables, middlewareValues, imports)
	}
	return nil
}

// routeProviderCallArguments resolves scalar aliases and statically enumerable final slice expansions without executing project helpers.
func routeProviderCallArguments(call *ast.CallExpr, middlewareValues map[string]middlewareValue, imports map[string]string) []routeCallArgument {
	arguments := make([]routeCallArgument, 0, len(call.Args))
	for index, expression := range call.Args {
		expanded := call.Ellipsis != token.NoPos && index == len(call.Args)-1
		if !expanded {
			resolved := middlewareScalarExpression(expression, middlewareValues)
			arguments = append(arguments, routeCallArgument{Expressions: []string{resolved}, Exact: true, Evidence: resolved})
			continue
		}
		sequence, supported := compositionMiddlewareSequenceFromExpr(expression, middlewareValues, imports)
		if supported && sequence.Exact {
			arguments = append(arguments, routeCallArgument{
				Expressions: append([]string(nil), sequence.Expressions...),
				Expanded:    true,
				Exact:       true,
				Evidence:    exprString(expression) + "...",
			})
			continue
		}
		arguments = append(arguments, routeCallArgument{
			Expanded: true,
			Exact:    false,
			Evidence: exprString(expression) + "...",
		})
	}
	return arguments
}

// cloneRouteProviderCalls preserves assignment-time actuals when route variables are later reassigned.
func cloneRouteProviderCalls(calls []routeProviderCall) []routeProviderCall {
	if calls == nil {
		return nil
	}
	cloned := make([]routeProviderCall, len(calls))
	for index, call := range calls {
		cloned[index] = cloneRouteProviderCall(call)
	}
	return cloned
}

// compositionMiddlewareParameterValues marks framework middleware parameters as scalar evidence or unresolved sequences available to composition calls.
func compositionMiddlewareParameterValues(function *ast.FuncDecl, imports map[string]string) map[string]middlewareValue {
	values := map[string]middlewareValue{}
	if function == nil || function.Type == nil || function.Type.Params == nil {
		return values
	}
	for _, parameter := range function.Type.Params.List {
		sequence := false
		middleware := false
		switch parameterType := parameter.Type.(type) {
		case *ast.ArrayType:
			sequence = parameterType.Len == nil
			middleware = sequence && isFrameworkMiddlewareType(parameterType.Elt, imports)
		case *ast.Ellipsis:
			sequence = true
			middleware = isFrameworkMiddlewareType(parameterType.Elt, imports)
		default:
			middleware = isFrameworkMiddlewareType(parameter.Type, imports)
		}
		if !middleware {
			continue
		}
		for _, name := range parameter.Names {
			if sequence {
				values[name.Name] = middlewareValue{Sequence: true, Exact: false, Evidence: name.Name + "..."}
				continue
			}
			values[name.Name] = middlewareValue{Expressions: []string{name.Name}, Exact: true, Evidence: name.Name}
		}
	}
	return values
}

// compositionMiddlewareValueFromExpr captures safe scalar copies and immutable sequence construction used before a selected provider invocation.
func compositionMiddlewareValueFromExpr(expression ast.Expr, paramProviders map[string]routeProvider, variables map[string]middlewareValue, imports map[string]string) (middlewareValue, bool) {
	if sequence, supported := compositionMiddlewareSequenceFromExpr(expression, variables, imports); supported {
		return sequence, true
	}
	if _, provider := routeProviderFromRoutesArg(expression, paramProviders); provider {
		return middlewareValue{}, false
	}
	if call, ok := expression.(*ast.CallExpr); ok {
		if isAppendCall(call) || isSlicesConcatCall(call, imports) {
			return middlewareValue{}, false
		}
		if _, constructor := frameworkConstructorName(call, imports); constructor {
			return middlewareValue{}, false
		}
	}
	if identifier, ok := expression.(*ast.Ident); ok {
		if known, exists := variables[identifier.Name]; exists {
			return cloneMiddlewareValue(known), true
		}
	}
	switch expression.(type) {
	case *ast.Ident, *ast.SelectorExpr, *ast.CallExpr, *ast.IndexExpr, *ast.IndexListExpr, *ast.ParenExpr, *ast.UnaryExpr, *ast.FuncLit:
		resolved := middlewareScalarExpression(expression, variables)
		return middlewareValue{Expressions: []string{resolved}, Exact: true, Evidence: resolved}, true
	}
	return middlewareValue{}, false
}

// compositionMiddlewareSequenceFromExpr follows only direct unkeyed literals, append, slices.Concat, and assignment-time sequence aliases.
func compositionMiddlewareSequenceFromExpr(expression ast.Expr, variables map[string]middlewareValue, imports map[string]string) (middlewareValue, bool) {
	switch value := expression.(type) {
	case *ast.Ident:
		known, exists := variables[value.Name]
		if !exists || !known.Sequence {
			return middlewareValue{}, false
		}
		return cloneMiddlewareValue(known), true
	case *ast.ParenExpr:
		return compositionMiddlewareSequenceFromExpr(value.X, variables, imports)
	case *ast.CompositeLit:
		if !isFrameworkMiddlewareSliceType(value.Type, imports) {
			return middlewareValue{}, false
		}
		expressions := make([]string, 0, len(value.Elts))
		for _, element := range value.Elts {
			if _, keyed := element.(*ast.KeyValueExpr); keyed {
				return middlewareValue{Sequence: true, Exact: false, Evidence: exprString(expression) + "..."}, true
			}
			elementExpression, ok := element.(ast.Expr)
			if !ok {
				return middlewareValue{Sequence: true, Exact: false, Evidence: exprString(expression) + "..."}, true
			}
			expressions = append(expressions, middlewareScalarExpression(elementExpression, variables))
		}
		return middlewareValue{Expressions: expressions, Sequence: true, Exact: true}, true
	case *ast.CallExpr:
		if isAppendCall(value) {
			if len(value.Args) == 0 {
				return middlewareValue{}, false
			}
			sequence, supported := compositionMiddlewareSequenceFromExpr(value.Args[0], variables, imports)
			if !supported || !sequence.Exact {
				return middlewareValue{Sequence: true, Exact: false, Evidence: exprString(expression) + "..."}, true
			}
			expressions := append([]string(nil), sequence.Expressions...)
			for index, argument := range value.Args[1:] {
				last := index == len(value.Args[1:])-1
				if last && value.Ellipsis != token.NoPos {
					expanded, expansionSupported := compositionMiddlewareSequenceFromExpr(argument, variables, imports)
					if !expansionSupported || !expanded.Exact {
						return middlewareValue{Sequence: true, Exact: false, Evidence: exprString(expression) + "..."}, true
					}
					expressions = append(expressions, expanded.Expressions...)
					continue
				}
				expressions = append(expressions, middlewareScalarExpression(argument, variables))
			}
			return middlewareValue{Expressions: expressions, Sequence: true, Exact: true}, true
		}
		if isSlicesConcatCall(value, imports) {
			expressions := make([]string, 0)
			for _, argument := range value.Args {
				sequence, supported := compositionMiddlewareSequenceFromExpr(argument, variables, imports)
				if !supported || !sequence.Exact {
					return middlewareValue{Sequence: true, Exact: false, Evidence: exprString(expression) + "..."}, true
				}
				expressions = append(expressions, sequence.Expressions...)
			}
			return middlewareValue{Expressions: expressions, Sequence: true, Exact: true}, true
		}
	}
	return middlewareValue{}, false
}

// middlewareScalarExpression substitutes only previously captured scalar aliases and otherwise retains the author expression verbatim.
func middlewareScalarExpression(expression ast.Expr, variables map[string]middlewareValue) string {
	identifier, ok := expression.(*ast.Ident)
	if ok {
		known, exists := variables[identifier.Name]
		if exists && !known.Sequence && known.Exact && len(known.Expressions) == 1 {
			return known.Expressions[0]
		}
	}
	if parenthesized, ok := expression.(*ast.ParenExpr); ok {
		return middlewareScalarExpression(parenthesized.X, variables)
	}
	return exprString(expression)
}

// cloneMiddlewareValue isolates captured aliases from later assignment updates while preserving runtime order and duplicates.
func cloneMiddlewareValue(value middlewareValue) middlewareValue {
	value.Expressions = append([]string(nil), value.Expressions...)
	return value
}

// cloneMiddlewareValues creates an evaluation snapshot so simultaneous assignments observe every pre-assignment alias.
func cloneMiddlewareValues(values map[string]middlewareValue) map[string]middlewareValue {
	cloned := make(map[string]middlewareValue, len(values))
	for name, value := range values {
		cloned[name] = cloneMiddlewareValue(value)
	}
	return cloned
}

// invalidateMiddlewareSequences prevents a mutated or escaped slice from retaining stale enumerable middleware evidence.
func invalidateMiddlewareSequences(values map[string]middlewareValue) {
	for name, value := range values {
		if !value.Sequence {
			continue
		}
		value.Exact = false
		value.Expressions = nil
		value.Evidence = name + "..."
		values[name] = value
	}
}

// middlewareExpressionsEscapeSequence reports calls that can observe or mutate a known middleware slice outside the bounded append and slices.Concat vocabulary.
func middlewareExpressionsEscapeSequence(expressions []ast.Expr, values map[string]middlewareValue, paramProviders map[string]routeProvider, imports map[string]string, includeProviders bool) bool {
	escaped := false
	for _, expression := range expressions {
		ast.Inspect(expression, func(node ast.Node) bool {
			if escaped {
				return false
			}
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			if !callReferencesMiddlewareSequence(call, values) {
				return true
			}
			if _, provider := routeProviderFromRoutesArg(call, paramProviders); provider {
				if includeProviders {
					escaped = true
				}
				return false
			}
			if isAppendCall(call) || isSlicesConcatCall(call, imports) {
				return true
			}
			if _, constructor := frameworkConstructorName(call, imports); constructor {
				return true
			}
			escaped = true
			return false
		})
		if escaped {
			return true
		}
	}
	return false
}

// callReferencesMiddlewareSequence checks only call arguments so a same-named selector method cannot impersonate a local slice.
func callReferencesMiddlewareSequence(call *ast.CallExpr, values map[string]middlewareValue) bool {
	for _, argument := range call.Args {
		found := false
		ast.Inspect(argument, func(node ast.Node) bool {
			identifier, ok := node.(*ast.Ident)
			if !ok {
				return true
			}
			value, exists := values[identifier.Name]
			if exists && value.Sequence {
				found = true
				return false
			}
			return true
		})
		if found {
			return true
		}
	}
	return false
}

// isFrameworkMiddlewareType recognizes Middleware through the exact framework import and any legal source alias.
func isFrameworkMiddlewareType(expression ast.Expr, imports map[string]string) bool {
	importPath, name, ok := importedSelector(expression, imports)
	return ok && importPath == webFrameworkImportPath && name == "Middleware"
}

// isFrameworkMiddlewareSliceType recognizes only slices whose element is the exact framework Middleware type.
func isFrameworkMiddlewareSliceType(expression ast.Expr, imports map[string]string) bool {
	array, ok := expression.(*ast.ArrayType)
	return ok && array.Len == nil && isFrameworkMiddlewareType(array.Elt, imports)
}

// routeProviderFromRoutesArg records the invoked provider method instead of collapsing every method on a controller to its owner type.
func routeProviderFromRoutesArg(expr ast.Expr, paramProviders map[string]routeProvider) (routeProvider, bool) {
	selector := (*ast.SelectorExpr)(nil)
	switch value := expr.(type) {
	case *ast.CallExpr:
		selector, _ = value.Fun.(*ast.SelectorExpr)
	case *ast.SelectorExpr:
		selector = value
	}
	if selector == nil {
		return routeProvider{}, false
	}
	if !strings.HasSuffix(selector.Sel.Name, "Routes") {
		return routeProvider{}, false
	}
	receiver, ok := selector.X.(*ast.Ident)
	if !ok {
		return routeProvider{}, false
	}
	provider, ok := paramProviders[receiver.Name]
	if !ok || provider.Receiver == "" {
		return routeProvider{}, false
	}
	provider.Method = selector.Sel.Name
	return provider, true
}

// routeProviderFromDeclaration gives each discovered route the method that produced it, which is distinct from the handler method the route invokes.
func routeProviderFromDeclaration(pf *parsedFile, fn *ast.FuncDecl, scope routeScope) routeProvider {
	receiver := receiverName(fn)
	if receiver == "" {
		return routeProvider{}
	}
	return routeProvider{
		Package:    pf.PackageName,
		ImportPath: scope.packageImportPath(pf.Path),
		Receiver:   receiver,
		Method:     fn.Name.Name,
	}
}

// routeProvidersMatch prefers exact import identity and falls back to package names only when one side came from incomplete syntax-only source.
func routeProvidersMatch(left routeProvider, right routeProvider) bool {
	if left.Receiver == "" || left.Method == "" || left.Receiver != right.Receiver || left.Method != right.Method {
		return false
	}
	if left.ImportPath != "" && right.ImportPath != "" {
		return left.ImportPath == right.ImportPath
	}
	leftPackages := routeProviderPackageNames(left)
	for packageName := range routeProviderPackageNames(right) {
		if _, ok := leftPackages[packageName]; ok {
			return true
		}
	}
	return false
}

// routeProviderPackageNames supplies a safe fallback for fixtures that omit imports while avoiding owner-only matching in valid project source.
func routeProviderPackageNames(provider routeProvider) map[string]struct{} {
	names := map[string]struct{}{}
	if provider.Package != "" {
		names[provider.Package] = struct{}{}
	}
	if provider.ImportPath != "" {
		names[path.Base(provider.ImportPath)] = struct{}{}
	}
	return names
}

// isAppendCall recognizes Go's route-slice accumulation convention without mistaking package methods named append for the builtin.
func isAppendCall(call *ast.CallExpr) bool {
	ident, ok := call.Fun.(*ast.Ident)
	return ok && ident.Name == "append" && ident.Obj == nil
}

// isSlicesConcatCall recognizes generated GoForj composition without coupling provider discovery to every possible helper call.
func isSlicesConcatCall(call *ast.CallExpr, imports map[string]string) bool {
	return isStandardSlicesConcat(call, imports)
}

// providerSliceToSet deduplicates providers while variables are propagated through composition expressions.
func providerSliceToSet(providers []routeProvider) routeProviderSet {
	set := routeProviderSet{}
	mergeRouteProviderSet(set, providers)
	return set
}

// mergeRouteProviderSet unions statically reachable providers across composition expressions.
func mergeRouteProviderSet(target routeProviderSet, providers []routeProvider) {
	for _, provider := range providers {
		if provider.Receiver == "" || provider.Method == "" {
			continue
		}
		target[provider] = struct{}{}
	}
}

// routeProviderSetValues returns deterministic provider ordering so indexing output does not depend on map iteration.
func routeProviderSetValues(providers routeProviderSet) []routeProvider {
	if len(providers) == 0 {
		return nil
	}
	out := make([]routeProvider, 0, len(providers))
	for provider := range providers {
		out = append(out, provider)
	}
	sort.Slice(out, func(i, j int) bool {
		left := out[i].ImportPath + "|" + out[i].Package + "|" + out[i].Receiver + "|" + out[i].Method
		right := out[j].ImportPath + "|" + out[j].Package + "|" + out[j].Receiver + "|" + out[j].Method
		return left < right
	})
	return out
}
