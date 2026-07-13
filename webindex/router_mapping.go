package webindex

import (
	"go/ast"
	"go/token"
	"path/filepath"
)

// buildRouterMapping projects composition prefixes and middleware onto exact provider methods, preserving separate public and protected methods on one controller.
func buildRouterMapping(parsed []*parsedFile, scope routeScope) routerMapping {
	if scope.active {
		mapping := routerMapping{
			PrefixByProvider:           map[routeProvider]string{},
			MiddlewareByProvider:       map[routeProvider][]string{},
			MiddlewareSourceByProvider: map[routeProvider]middlewareSource{},
			ProviderCalls:              map[routeProvider]routeProviderCall{},
			InlineByCall:               map[token.Pos]routePlacement{},
		}
		for provider, placement := range scope.placements {
			mapping.PrefixByProvider[provider] = placement.Prefix
			mapping.MiddlewareByProvider[provider] = append([]string(nil), placement.Middlewares...)
			mapping.MiddlewareSourceByProvider[provider] = placement.MiddlewareSource
		}
		for position, placement := range scope.inlineRoutes {
			mapping.InlineByCall[position] = routePlacement{
				Prefix:           placement.Prefix,
				Middlewares:      append([]string(nil), placement.Middlewares...),
				MiddlewareSource: placement.MiddlewareSource,
			}
		}
		for provider, call := range scope.providerCalls {
			mapping.ProviderCalls[provider] = cloneRouteProviderCall(call)
		}
		return mapping
	}
	fieldToPrefix := map[string]string{}
	fieldToMiddlewares := map[string][]string{}
	fieldToMiddlewareSource := map[string]middlewareSource{}
	providerToField := map[routeProvider]string{}
	directProviderToPrefix := map[routeProvider]string{}
	directProviderToMiddlewares := map[routeProvider][]string{}
	directProviderToMiddlewareSource := map[routeProvider]middlewareSource{}
	directProviderCalls := map[routeProvider]routeProviderCall{}

	for _, pf := range parsed {
		if scope.active && filepath.ToSlash(filepath.Clean(pf.Path)) != scope.compositionFile {
			continue
		}
		if !scope.active && pf.PackageName != "router" {
			continue
		}
		for _, decl := range pf.File.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			switch fn.Name.Name {
			case "ProvideRoutes":
				parseProvideRoutes(fieldToPrefix, fieldToMiddlewares, fieldToMiddlewareSource, pf, fn)
				parseScopedProvideRoutes(directProviderToPrefix, directProviderToMiddlewares, directProviderToMiddlewareSource, directProviderCalls, pf, fn, scope)
			case "ProvideAppRoutes":
				parseProvideAppRoutes(providerToField, pf, fn, scope)
			}
		}
	}

	mapping := routerMapping{
		PrefixByProvider:           map[routeProvider]string{},
		MiddlewareByProvider:       map[routeProvider][]string{},
		MiddlewareSourceByProvider: map[routeProvider]middlewareSource{},
		ProviderCalls:              map[routeProvider]routeProviderCall{},
	}
	for provider, field := range providerToField {
		if prefix, ok := fieldToPrefix[field]; ok {
			mapping.PrefixByProvider[provider] = prefix
		}
		if middlewares, ok := fieldToMiddlewares[field]; ok && len(middlewares) > 0 {
			mapping.MiddlewareByProvider[provider] = append([]string(nil), middlewares...)
			mapping.MiddlewareSourceByProvider[provider] = fieldToMiddlewareSource[field]
		}
	}
	for provider, prefix := range directProviderToPrefix {
		mapping.PrefixByProvider[provider] = prefix
	}
	for provider, middlewares := range directProviderToMiddlewares {
		if len(middlewares) > 0 {
			mapping.MiddlewareByProvider[provider] = append([]string(nil), middlewares...)
			mapping.MiddlewareSourceByProvider[provider] = directProviderToMiddlewareSource[provider]
		}
	}
	for provider, call := range directProviderCalls {
		mapping.ProviderCalls[provider] = cloneRouteProviderCall(call)
	}
	if len(fieldToMiddlewares) == 1 {
		for field, middlewares := range fieldToMiddlewares {
			mapping.DefaultMiddlewares = append([]string(nil), middlewares...)
			mapping.DefaultMiddlewareSource = fieldToMiddlewareSource[field]
		}
	}
	return mapping
}

// parseScopedProvideRoutes reads returned groups from a selected app composition and retains provider-method-specific policy.
func parseScopedProvideRoutes(providerToPrefix map[routeProvider]string, providerToMiddlewares map[routeProvider][]string, providerToMiddlewareSource map[routeProvider]middlewareSource, providerCalls map[routeProvider]routeProviderCall, pf *parsedFile, fn *ast.FuncDecl, scope routeScope) {
	for _, group := range returnedScopedRouteGroups(pf, fn, scope) {
		for _, provider := range group.Providers {
			providerToPrefix[provider] = group.Prefix
			if len(group.Middlewares) > 0 {
				providerToMiddlewares[provider] = append([]string(nil), group.Middlewares...)
				providerToMiddlewareSource[provider] = group.MiddlewareSource
			}
		}
		for _, call := range group.ProviderCalls {
			if _, exists := providerCalls[call.Provider]; !exists {
				providerCalls[call.Provider] = cloneRouteProviderCall(call)
			}
		}
	}
}

// parseProvideRoutes supports the historical AppRoutes field registry while newer app composition is handled directly from returned groups.
func parseProvideRoutes(fieldToPrefix map[string]string, fieldToMiddlewares map[string][]string, fieldToMiddlewareSource map[string]middlewareSource, pf *parsedFile, fn *ast.FuncDecl) {
	imports := sourceImportPathsByAlias(pf.File)
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || len(call.Args) < 2 || !isRouteGroupCall(call, imports) {
			return true
		}
		prefix, ok := routeGroupPrefixLiteral(call.Args[0])
		if !ok {
			return true
		}
		fieldSelector, ok := call.Args[1].(*ast.SelectorExpr)
		if !ok {
			return true
		}
		field := fieldSelector.Sel.Name
		if field != "" {
			fieldToPrefix[field] = prefix
			fieldToMiddlewares[field] = middlewareExprsForCall(call, 2)
			fieldToMiddlewareSource[field] = middlewareSource{File: pf.Path, Function: fn.Name.Name, Receiver: receiverName(fn)}
		}
		return true
	})
}

// parseProvideAppRoutes traces provider methods through accumulated variables and AppRoutes composite fields for compatibility with the historical registry shape.
func parseProvideAppRoutes(providerToField map[routeProvider]string, pf *parsedFile, fn *ast.FuncDecl, scope routeScope) {
	for field, providers := range returnedHistoricalRouteFields(pf, fn, scope) {
		for _, provider := range providers {
			providerToField[provider] = field
		}
	}
}

// returnedHistoricalRouteFields evaluates only top-level assignments and the AppRoutes value returned by the historical registry provider.
func returnedHistoricalRouteFields(pf *parsedFile, fn *ast.FuncDecl, scope routeScope) map[string][]routeProvider {
	paramProviders := routeParamProviders(pf, fn, scope)
	imports := sourceImportPathsByAlias(pf.File)
	varProviders := map[string][]routeProvider{}
	fieldVariables := map[string]map[string][]routeProvider{}
	returned := map[string][]routeProvider{}

	for _, statement := range fn.Body.List {
		switch value := statement.(type) {
		case *ast.AssignStmt:
			for i, lhs := range value.Lhs {
				ident, ok := lhs.(*ast.Ident)
				if !ok || ident.Name == "_" || i >= len(value.Rhs) {
					continue
				}
				providers := routeProviderOccurrencesFromExpr(value.Rhs[i], paramProviders, varProviders, imports)
				fields, fieldsSupported := appRouteFieldOccurrencesFromExpr(value.Rhs[i], varProviders, fieldVariables, imports)
				delete(varProviders, ident.Name)
				delete(fieldVariables, ident.Name)
				if providers != nil {
					varProviders[ident.Name] = providers
				}
				if fieldsSupported {
					fieldVariables[ident.Name] = fields
				}
			}
		case *ast.DeclStmt:
			declaration, ok := value.Decl.(*ast.GenDecl)
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
						varProviders[name.Name] = []routeProvider{}
					}
				}
				for i, name := range specification.Names {
					if name.Name == "_" || i >= len(specification.Values) {
						continue
					}
					providers := routeProviderOccurrencesFromExpr(specification.Values[i], paramProviders, varProviders, imports)
					if providers != nil {
						varProviders[name.Name] = providers
					}
					if fields, supported := appRouteFieldOccurrencesFromExpr(specification.Values[i], varProviders, fieldVariables, imports); supported {
						fieldVariables[name.Name] = fields
					}
				}
			}
		case *ast.ReturnStmt:
			for _, result := range value.Results {
				fields, supported := appRouteFieldOccurrencesFromExpr(result, varProviders, fieldVariables, imports)
				if !supported {
					continue
				}
				for field, providers := range fields {
					returned[field] = append(returned[field], providers...)
				}
			}
		}
	}
	return returned
}

// returnedHistoricalRouteFieldCalls traces provider invocations and middleware actuals through the AppRoutes value returned by a legacy registry.
func returnedHistoricalRouteFieldCalls(pf *parsedFile, fn *ast.FuncDecl, scope routeScope) map[string][]routeProviderCall {
	paramProviders := routeParamProviders(pf, fn, scope)
	imports := sourceImportPathsByAlias(pf.File)
	variableCalls := map[string][]routeProviderCall{}
	fieldVariables := map[string]map[string][]routeProviderCall{}
	middlewareValues := compositionMiddlewareParameterValues(fn, imports)
	returned := map[string][]routeProviderCall{}

	for _, statement := range fn.Body.List {
		switch value := statement.(type) {
		case *ast.AssignStmt:
			type assignmentValue struct {
				calls               []routeProviderCall
				fields              map[string][]routeProviderCall
				fieldsSupported     bool
				middleware          middlewareValue
				middlewareSupported bool
				rightHandSide       bool
			}
			evaluationMiddleware := middlewareValues
			if middlewareExpressionsEscapeSequence(value.Rhs, middlewareValues, paramProviders, imports, false) {
				evaluationMiddleware = cloneMiddlewareValues(middlewareValues)
				invalidateMiddlewareSequences(evaluationMiddleware)
			}
			values := make([]assignmentValue, len(value.Lhs))
			for index := range value.Lhs {
				if index >= len(value.Rhs) {
					continue
				}
				expression := value.Rhs[index]
				values[index].rightHandSide = true
				values[index].calls = routeProviderCallsFromExpr(expression, paramProviders, variableCalls, evaluationMiddleware, imports)
				values[index].fields, values[index].fieldsSupported = appRouteFieldCallOccurrencesFromExpr(expression, variableCalls, fieldVariables, imports)
				values[index].middleware, values[index].middlewareSupported = compositionMiddlewareValueFromExpr(expression, paramProviders, evaluationMiddleware, imports)
			}
			mutatesIndexedValue := false
			for index, left := range value.Lhs {
				identifier, ok := left.(*ast.Ident)
				if !ok {
					mutatesIndexedValue = true
					continue
				}
				if identifier.Name == "_" {
					continue
				}
				delete(variableCalls, identifier.Name)
				delete(fieldVariables, identifier.Name)
				delete(middlewareValues, identifier.Name)
				assigned := values[index]
				if !assigned.rightHandSide {
					continue
				}
				if assigned.calls != nil {
					variableCalls[identifier.Name] = assigned.calls
				}
				if assigned.fieldsSupported {
					fieldVariables[identifier.Name] = assigned.fields
				}
				if assigned.middlewareSupported {
					middlewareValues[identifier.Name] = assigned.middleware
				}
			}
			if mutatesIndexedValue || middlewareExpressionsEscapeSequence(value.Rhs, middlewareValues, paramProviders, imports, true) {
				invalidateMiddlewareSequences(middlewareValues)
			}
		case *ast.DeclStmt:
			declaration, ok := value.Decl.(*ast.GenDecl)
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
						variableCalls[name.Name] = []routeProviderCall{}
						if isFrameworkMiddlewareSliceType(specification.Type, imports) {
							middlewareValues[name.Name] = middlewareValue{Expressions: []string{}, Sequence: true, Exact: true}
						}
					}
				}
				for index, name := range specification.Names {
					if name.Name == "_" || index >= len(specification.Values) {
						continue
					}
					expression := specification.Values[index]
					if calls := routeProviderCallsFromExpr(expression, paramProviders, variableCalls, middlewareValues, imports); calls != nil {
						variableCalls[name.Name] = calls
					}
					if fields, supported := appRouteFieldCallOccurrencesFromExpr(expression, variableCalls, fieldVariables, imports); supported {
						fieldVariables[name.Name] = fields
					}
					if middlewareValue, supported := compositionMiddlewareValueFromExpr(expression, paramProviders, middlewareValues, imports); supported {
						middlewareValues[name.Name] = middlewareValue
					}
				}
			}
		case *ast.ReturnStmt:
			for _, result := range value.Results {
				fields, supported := appRouteFieldCallOccurrencesFromExpr(result, variableCalls, fieldVariables, imports)
				if !supported {
					continue
				}
				for field, calls := range fields {
					returned[field] = append(returned[field], cloneRouteProviderCalls(calls)...)
				}
			}
		case *ast.ExprStmt:
			if middlewareExpressionsEscapeSequence([]ast.Expr{value.X}, middlewareValues, paramProviders, imports, true) {
				invalidateMiddlewareSequences(middlewareValues)
			}
		}
	}
	return returned
}

// appRouteFieldCallOccurrencesFromExpr follows returned AppRoutes composites while preserving each selected provider invocation.
func appRouteFieldCallOccurrencesFromExpr(expression ast.Expr, routeVariables map[string][]routeProviderCall, fieldVariables map[string]map[string][]routeProviderCall, imports map[string]string) (map[string][]routeProviderCall, bool) {
	switch value := expression.(type) {
	case *ast.Ident:
		fields, known := fieldVariables[value.Name]
		return fields, known
	case *ast.ParenExpr:
		return appRouteFieldCallOccurrencesFromExpr(value.X, routeVariables, fieldVariables, imports)
	case *ast.UnaryExpr:
		return appRouteFieldCallOccurrencesFromExpr(value.X, routeVariables, fieldVariables, imports)
	case *ast.CompositeLit:
		typeName := typeNameFromExpr(value.Type)
		if typeName != "AppRoutes" && typeName != "router.AppRoutes" {
			return nil, false
		}
		fields := map[string][]routeProviderCall{}
		for _, element := range value.Elts {
			keyValue, ok := element.(*ast.KeyValueExpr)
			if !ok {
				return nil, false
			}
			field, ok := keyValue.Key.(*ast.Ident)
			if !ok {
				return nil, false
			}
			calls := routeProviderCallsFromExpr(keyValue.Value, nil, routeVariables, nil, imports)
			if calls == nil {
				return nil, false
			}
			fields[field.Name] = calls
		}
		return fields, true
	}
	return nil, false
}

// appRouteFieldOccurrencesFromExpr follows returned historical AppRoutes composites without deduplicating repeated runtime provider calls.
func appRouteFieldOccurrencesFromExpr(expression ast.Expr, routeVariables map[string][]routeProvider, fieldVariables map[string]map[string][]routeProvider, imports map[string]string) (map[string][]routeProvider, bool) {
	switch value := expression.(type) {
	case *ast.Ident:
		fields, known := fieldVariables[value.Name]
		return fields, known
	case *ast.ParenExpr:
		return appRouteFieldOccurrencesFromExpr(value.X, routeVariables, fieldVariables, imports)
	case *ast.UnaryExpr:
		return appRouteFieldOccurrencesFromExpr(value.X, routeVariables, fieldVariables, imports)
	case *ast.CompositeLit:
		typeName := typeNameFromExpr(value.Type)
		if typeName != "AppRoutes" && typeName != "router.AppRoutes" {
			return nil, false
		}
		fields := map[string][]routeProvider{}
		for _, element := range value.Elts {
			keyValue, ok := element.(*ast.KeyValueExpr)
			if !ok {
				return nil, false
			}
			field, ok := keyValue.Key.(*ast.Ident)
			if !ok {
				return nil, false
			}
			providers := routeProviderOccurrencesFromExpr(keyValue.Value, nil, routeVariables, imports)
			if providers == nil {
				return nil, false
			}
			fields[field.Name] = providers
		}
		return fields, true
	}
	return nil, false
}
