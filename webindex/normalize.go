package webindex

import (
	"fmt"
	"go/ast"
	"go/token"
	"sort"
	"strings"

	"github.com/goforj/str"
)

// handlerResolution retains both the selected symbol and ambiguity evidence used by normalization diagnostics.
type handlerResolution struct {
	Handler    discoveredHandler
	Candidates []discoveredHandler
	Found      bool
}

// normalize converts scoped routes with the same handler resolution used to select packages for focused type loading.
func normalize(routes []discoveredRoute, handlers []discoveredHandler, prefixes []string, mapping routerMapping, fset *token.FileSet, registry *typedSchemaRegistry) ([]Operation, []Diagnostic) {
	diag := make([]Diagnostic, 0)
	handlerByName := indexHandlersByName(handlers)

	effectivePrefix := ""
	if len(prefixes) == 1 {
		effectivePrefix = prefixes[0]
	}

	ops := make([]Operation, 0, len(routes))
	for _, r := range routes {
		method := normalizeMethodExpr(r.MethodExpr)
		if method == "getws" {
			continue
		}
		path := r.Path
		prefix := routePrefix(r, mapping, effectivePrefix)
		path = joinPath(prefix, r.Path)
		normalizedMethod := str.Of(method).ToUpper().String()
		opID := fmt.Sprintf("%s:%s", normalizedMethod, path)
		for _, problem := range routeTemplateProblems(path) {
			diag = append(diag, Diagnostic{
				Severity:  "warn",
				Code:      "invalid_route_template",
				Message:   problem,
				File:      r.File,
				Line:      r.Line,
				Operation: opID,
			})
		}
		for _, wildcard := range routeCatchAllParameters(path) {
			diag = append(diag, Diagnostic{
				Severity:  "warn",
				Code:      "unrepresentable_route_wildcard",
				Message:   fmt.Sprintf("catch-all route parameter %q has no faithful OpenAPI path-template representation", wildcard),
				File:      r.File,
				Line:      r.Line,
				Operation: opID,
			})
		}
		handlerFn := r.HandlerFunction
		if handlerFn == "" {
			handlerFn = methodNameFromHandlerExpr(r.HandlerExpr)
		}

		op := Operation{
			ID:     opID,
			Method: normalizedMethod,
			Path:   path,
			Handler: HandlerRef{
				Expression: r.HandlerExpr,
				Function:   handlerFn,
				File:       r.File,
				Line:       r.Line,
			},
			Inputs:  InputShape{},
			Outputs: OutputShape{},
		}
		groupMiddlewares, groupMiddlewareSource := routeMiddlewares(r, mapping)
		routeMiddlewareSource := middlewareSource{File: r.File, Function: r.EnclosingFunction, Receiver: r.Provider.Receiver}
		op.Middleware, op.middlewareProvenance = mergeMiddlewares(groupMiddlewares, groupMiddlewareSource, r.MiddlewareExprs, routeMiddlewareSource)

		resolution := resolveHandlerForRoute(r, handlerByName)
		if !resolution.Found {
			diag = append(diag, Diagnostic{
				Severity:  "warn",
				Code:      "handler_not_found",
				Message:   fmt.Sprintf("unable to resolve handler %q", r.HandlerExpr),
				File:      r.File,
				Line:      r.Line,
				Operation: opID,
			})
			ops = append(ops, op)
			continue
		}
		if len(resolution.Candidates) > 1 {
			diag = append(diag, Diagnostic{
				Severity:  "warn",
				Code:      "handler_ambiguous",
				Message:   fmt.Sprintf("multiple handlers matched %q, using first", r.HandlerExpr),
				File:      r.File,
				Line:      r.Line,
				Operation: opID,
			})
		}

		h := resolution.Handler
		op.Handler.Package = h.Package
		op.Handler.ImportPath = h.ImportPath
		op.Handler.Receiver = h.Receiver
		op.Handler.Function = h.Name
		op.Handler.File = h.File
		op.Handler.Line = h.Line
		metadata, metadataProblems := operationMetadataFromHandler(h.Decl, h.Package)
		op.Metadata = metadata
		for _, problem := range metadataProblems {
			diag = append(diag, Diagnostic{
				Severity:  "warn",
				Code:      "invalid_openapi_annotation",
				Message:   problem,
				File:      h.File,
				Line:      h.Line,
				Operation: opID,
			})
		}

		analyzed := analyzeHandlerWithTypes(h.Decl, handlerSchemaContext{FileSet: fset, Registry: registry, Imports: h.Imports})
		routePathParameters := extractPathParamsFromRoute(op.Path)
		routePathNames := map[string]struct{}{}
		for _, parameter := range routePathParameters {
			routePathNames[parameter.Name] = struct{}{}
		}
		for _, parameter := range analyzed.PathParams {
			if _, declared := routePathNames[parameter.Name]; declared {
				continue
			}
			diag = append(diag, Diagnostic{
				Severity:  "warn",
				Code:      "handler_path_param_not_in_route",
				Message:   fmt.Sprintf("handler reads path parameter %q but the route template does not declare it", parameter.Name),
				File:      h.File,
				Line:      h.Line,
				Operation: opID,
			})
		}
		op.Inputs.PathParams = mergePathParams(routePathParameters, analyzed.PathParams)
		op.Inputs.QueryParams = analyzed.QueryParams
		op.Inputs.Headers = analyzed.Headers
		op.Inputs.Cookies = analyzed.Cookies
		op.Inputs.Body = analyzed.Body
		dynamicSeen := map[string]struct{}{}
		for _, d := range analyzed.Dynamic {
			key := d.Kind + "|" + d.Expr
			if _, exists := dynamicSeen[key]; exists {
				continue
			}
			dynamicSeen[key] = struct{}{}
			diag = append(diag, Diagnostic{
				Severity:  "warn",
				Code:      "dynamic_param_key",
				Message:   fmt.Sprintf("dynamic %s parameter key (%s) could not be inferred", d.Kind, d.Expr),
				File:      h.File,
				Line:      h.Line,
				Operation: opID,
			})
		}
		issueSeen := map[string]struct{}{}
		for _, issue := range analyzed.Issues {
			key := issue.Code + "|" + issue.Message
			if _, exists := issueSeen[key]; exists {
				continue
			}
			issueSeen[key] = struct{}{}
			diag = append(diag, Diagnostic{
				Severity:  "warn",
				Code:      issue.Code,
				Message:   issue.Message,
				File:      h.File,
				Line:      h.Line,
				Operation: opID,
			})
		}
		op.Outputs.Responses = analyzed.Responses
		diag = append(diag, openAPISourcePolicyDiagnostics(op)...)
		if len(op.Outputs.Responses) == 0 {
			diag = append(diag, Diagnostic{
				Severity:  "warn",
				Code:      "handler_no_response",
				Message:   "handler has no statically resolved web.Context response",
				File:      h.File,
				Line:      h.Line,
				Operation: opID,
			})
		}

		ops = append(ops, op)
	}

	sort.Slice(ops, func(i, j int) bool {
		if ops[i].Path == ops[j].Path {
			return ops[i].Method < ops[j].Method
		}
		return ops[i].Path < ops[j].Path
	})

	return ops, diag
}

// indexHandlersByName builds the shared symbol lookup for package selection and operation normalization.
func indexHandlersByName(handlers []discoveredHandler) map[string][]discoveredHandler {
	indexed := map[string][]discoveredHandler{}
	for _, handler := range handlers {
		indexed[handler.Name] = append(indexed[handler.Name], handler)
	}
	return indexed
}

// resolveHandlerForRoute applies one deterministic selection policy before both package loading and operation analysis.
func resolveHandlerForRoute(route discoveredRoute, handlersByName map[string][]discoveredHandler) handlerResolution {
	if !route.HandlerIdentityKnown {
		return handlerResolution{}
	}
	handlerFunction := route.HandlerFunction
	if handlerFunction == "" {
		handlerFunction = methodNameFromHandlerExpr(route.HandlerExpr)
	}
	allCandidates := handlersByName[handlerFunction]
	candidates := filterHandlerCandidates(allCandidates, route.HandlerImportPathHint, route.HandlerPackageHint, route.HandlerReceiverHint, route.HandlerReceiverExact)
	// In incomplete source a bare identifier may lack its declaring package, but it still cannot denote a method.
	if len(candidates) == 0 && route.HandlerBareIdentifier {
		candidates = filterHandlerCandidates(allCandidates, "", "", "", true)
	} else if len(candidates) == 0 && !route.HandlerReceiverExact && (route.HandlerImportPathHint == "" || route.HandlerReceiverHint == "") {
		candidates = allCandidates
	}
	if len(candidates) == 0 {
		return handlerResolution{}
	}
	return handlerResolution{
		Handler:    pickBestCandidate(candidates, route.HandlerImportPathHint, route.HandlerPackageHint, route.HandlerReceiverHint),
		Candidates: candidates,
		Found:      true,
	}
}

// selectedHandlerFiles returns only handler packages reachable from scoped HTTP routes, excluding inactive Apps and WebSockets.
func selectedHandlerFiles(routes []discoveredRoute, handlers []discoveredHandler) []string {
	handlersByName := indexHandlersByName(handlers)
	seen := map[string]struct{}{}
	files := make([]string, 0)
	for _, route := range routes {
		if normalizeMethodExpr(route.MethodExpr) == "getws" {
			continue
		}
		resolution := resolveHandlerForRoute(route, handlersByName)
		if !resolution.Found || resolution.Handler.File == "" {
			continue
		}
		if _, exists := seen[resolution.Handler.File]; exists {
			continue
		}
		seen[resolution.Handler.File] = struct{}{}
		files = append(files, resolution.Handler.File)
	}
	sort.Strings(files)
	return files
}

// selectedHandlerContractExpressions returns exact Bind targets and JSON values reached through resolved HTTP handlers.
func selectedHandlerContractExpressions(routes []discoveredRoute, handlers []discoveredHandler, fset *token.FileSet) []typedSourceRange {
	ranges := make([]typedSourceRange, 0)
	if fset == nil {
		return ranges
	}
	handlersByName := indexHandlersByName(handlers)
	seen := map[string]struct{}{}
	for _, route := range routes {
		if normalizeMethodExpr(route.MethodExpr) == "getws" {
			continue
		}
		resolution := resolveHandlerForRoute(route, handlersByName)
		if !resolution.Found || resolution.Handler.Decl == nil || resolution.Handler.Decl.Body == nil {
			continue
		}
		handler := resolution.Handler
		contextReceivers := handlerContextReceivers(handler.Decl, handler.Imports)
		ast.Inspect(handler.Decl.Body, func(node ast.Node) bool {
			if _, nestedFunction := node.(*ast.FuncLit); nestedFunction {
				// Contract roots must match the outer handler evidence collected by analyzeHandlerWithTypes.
				return false
			}
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			api, direct := contextAPIForReceiver(selector.X, contextReceivers)
			if !direct {
				return true
			}
			var expression ast.Expr
			switch selector.Sel.Name {
			case "Bind":
				if len(call.Args) == 1 {
					expression = bindSchemaExpression(call.Args[0])
				}
			case "JSON":
				if supportsResponseMethod(api, selector.Sel.Name) && len(call.Args) >= 2 {
					expression = call.Args[1]
				}
			}
			if expression == nil {
				return true
			}
			source, ok := typedRangeForNode(fset, expression)
			if !ok {
				return true
			}
			key := typedSourceKey(source)
			if _, exists := seen[key]; exists {
				return true
			}
			seen[key] = struct{}{}
			ranges = append(ranges, source)
			return true
		})
	}
	sort.Slice(ranges, func(i, j int) bool {
		return typedSourceKey(ranges[i]) < typedSourceKey(ranges[j])
	})
	return ranges
}

// routePrefix applies composition policy by the route-producing method because one controller can expose route sets under different groups.
func routePrefix(r discoveredRoute, mapping routerMapping, fallback string) string {
	if placement, ok := mapping.InlineByCall[r.CallPos]; ok {
		return placement.Prefix
	}
	if prefix, ok := mapping.PrefixByProvider[r.Provider]; ok {
		return prefix
	}
	matches := matchingProviderPrefixes(r.Provider, mapping.PrefixByProvider)
	if len(matches) == 1 {
		return matches[0]
	}
	if fallback != "" {
		return fallback
	}
	return ""
}

// routeMiddlewares keeps public and protected route-provider methods independent even when their handlers share a receiver type.
func routeMiddlewares(r discoveredRoute, mapping routerMapping) ([]string, middlewareSource) {
	if placement, ok := mapping.InlineByCall[r.CallPos]; ok {
		return append([]string(nil), placement.Middlewares...), placement.MiddlewareSource
	}
	if middlewares, ok := mapping.MiddlewareByProvider[r.Provider]; ok {
		return append([]string(nil), middlewares...), mapping.MiddlewareSourceByProvider[r.Provider]
	}
	matches := matchingProviderMiddlewares(r.Provider, mapping.MiddlewareByProvider)
	if len(matches) == 1 {
		sources := matchingProviderMiddlewareSources(r.Provider, mapping.MiddlewareSourceByProvider)
		if len(sources) == 1 {
			return matches[0], sources[0]
		}
		return matches[0], middlewareSource{}
	}
	if len(mapping.DefaultMiddlewares) > 0 {
		return append([]string(nil), mapping.DefaultMiddlewares...), mapping.DefaultMiddlewareSource
	}
	return nil, middlewareSource{}
}

// mergeMiddlewares preserves runtime order and multiplicity because repeated middleware executes repeatedly.
func mergeMiddlewares(groupMws []string, groupSource middlewareSource, routeMws []string, routeSource middlewareSource) ([]string, []middlewareProvenance) {
	if len(groupMws) == 0 && len(routeMws) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(groupMws)+len(routeMws))
	provenance := make([]middlewareProvenance, 0, len(groupMws)+len(routeMws))
	appendOne := func(name string, source middlewareSource) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		out = append(out, name)
		provenance = append(provenance, middlewareProvenance{
			Expression: name,
			File:       source.File,
			Function:   source.Function,
			Receiver:   source.Receiver,
		})
	}
	for _, mw := range groupMws {
		appendOne(mw, groupSource)
	}
	for _, mw := range routeMws {
		appendOne(mw, routeSource)
	}
	return out, provenance
}

// matchingProviderPrefixes returns stable fallback candidates when incomplete syntax prevents exact import identity.
func matchingProviderPrefixes(provider routeProvider, prefixes map[routeProvider]string) []string {
	matched := make([]string, 0)
	for candidate, prefix := range prefixes {
		if routeProvidersMatch(provider, candidate) {
			matched = append(matched, prefix)
		}
	}
	sort.Strings(matched)
	return matched
}

// matchingProviderMiddlewares returns stable fallback candidates when incomplete syntax prevents exact import identity.
func matchingProviderMiddlewares(provider routeProvider, middlewares map[routeProvider][]string) [][]string {
	type candidate struct {
		key   string
		value []string
	}
	matched := make([]candidate, 0)
	for owner, values := range middlewares {
		if !routeProvidersMatch(provider, owner) {
			continue
		}
		matched = append(matched, candidate{
			key:   owner.ImportPath + "|" + owner.Package + "|" + owner.Receiver + "|" + owner.Method,
			value: append([]string(nil), values...),
		})
	}
	sort.Slice(matched, func(i, j int) bool { return matched[i].key < matched[j].key })
	values := make([][]string, 0, len(matched))
	for _, match := range matched {
		values = append(values, match.value)
	}
	return values
}

// matchingProviderMiddlewareSources returns stable source candidates using the same incomplete-identity fallback as middleware values.
func matchingProviderMiddlewareSources(provider routeProvider, sources map[routeProvider]middlewareSource) []middlewareSource {
	type candidate struct {
		key   string
		value middlewareSource
	}
	matched := make([]candidate, 0)
	for owner, source := range sources {
		if !routeProvidersMatch(provider, owner) {
			continue
		}
		matched = append(matched, candidate{
			key:   owner.ImportPath + "|" + owner.Package + "|" + owner.Receiver + "|" + owner.Method,
			value: source,
		})
	}
	sort.Slice(matched, func(i, j int) bool { return matched[i].key < matched[j].key })
	values := make([]middlewareSource, 0, len(matched))
	for _, match := range matched {
		values = append(values, match.value)
	}
	return values
}

// filterHandlerCandidates uses import and receiver identity when syntax proves them, including an explicitly receiverless package function.
func filterHandlerCandidates(candidates []discoveredHandler, importHint, pkgHint, recvHint string, receiverExact bool) []discoveredHandler {
	out := make([]discoveredHandler, 0, len(candidates))
	for _, c := range candidates {
		if importHint != "" && c.ImportPath != importHint {
			continue
		}
		if importHint == "" && pkgHint != "" && c.Package != pkgHint {
			continue
		}
		if receiverExact {
			if strings.TrimPrefix(c.Receiver, "*") != recvHint {
				continue
			}
		} else if recvHint != "" && strings.TrimPrefix(c.Receiver, "*") != recvHint {
			continue
		}
		out = append(out, c)
	}
	return out
}

// pickBestCandidate makes ambiguity fallback deterministic while preferring exact import, package, and receiver identity in that order.
func pickBestCandidate(candidates []discoveredHandler, importHint, pkgHint, recvHint string) discoveredHandler {
	candidates = append([]discoveredHandler(nil), candidates...)
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].ImportPath == candidates[j].ImportPath {
			if candidates[i].File == candidates[j].File {
				return candidates[i].Line < candidates[j].Line
			}
			return candidates[i].File < candidates[j].File
		}
		return candidates[i].ImportPath < candidates[j].ImportPath
	})
	if len(candidates) == 1 {
		return candidates[0]
	}
	for _, c := range candidates {
		if importHint != "" && c.ImportPath == importHint && strings.TrimPrefix(c.Receiver, "*") == recvHint {
			return c
		}
	}
	for _, c := range candidates {
		if pkgHint != "" && recvHint != "" && c.Package == pkgHint && strings.TrimPrefix(c.Receiver, "*") == recvHint {
			return c
		}
	}
	for _, c := range candidates {
		if recvHint != "" && strings.TrimPrefix(c.Receiver, "*") == recvHint {
			return c
		}
	}
	return candidates[0]
}

// extractPathParamsFromRoute keeps router-declared parameters even when a handler never reads them directly.
func extractPathParamsFromRoute(path string) []Parameter {
	parts := strings.Split(path, "/")
	out := make([]Parameter, 0, len(parts))
	seen := map[string]struct{}{}
	for _, part := range parts {
		if part == "" {
			continue
		}
		name := ""
		switch {
		case strings.HasPrefix(part, ":"):
			name = strings.TrimPrefix(part, ":")
		case strings.HasPrefix(part, "*"):
			name = strings.TrimPrefix(part, "*")
		}
		if name == "" {
			continue
		}
		if !validRouteParameterName(name) {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, Parameter{
			Name:       name,
			In:         "path",
			Required:   true,
			Confidence: "high",
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// routeTemplateProblems reports router syntax that cannot be projected to a valid and faithful OpenAPI path.
func routeTemplateProblems(path string) []string {
	problems := make([]string, 0)
	if path == "" {
		return []string{"route path is empty"}
	}
	if !strings.HasPrefix(path, "/") {
		problems = append(problems, fmt.Sprintf("route path %q must begin with '/'", path))
	}
	if strings.ContainsAny(path, "?#") {
		problems = append(problems, fmt.Sprintf("route path %q contains a query or fragment delimiter", path))
	}
	problems = append(problems, routePathCharacterProblems(path)...)
	parts := strings.Split(path, "/")
	seen := map[string]struct{}{}
	for index, part := range parts {
		if strings.ContainsAny(part, "{}") {
			problems = append(problems, fmt.Sprintf("route segment %q contains brace syntax reserved by OpenAPI", part))
		}
		if !strings.HasPrefix(part, ":") && !strings.HasPrefix(part, "*") {
			continue
		}
		name := part[1:]
		if name == "" {
			problems = append(problems, fmt.Sprintf("route segment %q has no parameter name", part))
			continue
		}
		if !validRouteParameterName(name) {
			problems = append(problems, fmt.Sprintf("route parameter name %q contains unsupported characters", name))
			continue
		}
		if _, duplicate := seen[name]; duplicate {
			problems = append(problems, fmt.Sprintf("route parameter %q is declared more than once", name))
		} else {
			seen[name] = struct{}{}
		}
		if strings.HasPrefix(part, "*") && index != len(parts)-1 {
			problems = append(problems, fmt.Sprintf("wildcard route parameter %q must be the final segment", name))
		}
	}
	sort.Strings(problems)
	return problems
}

// routePathCharacterProblems enforces RFC 3986 path characters and complete percent escapes so tooling does not reinterpret the runtime path.
func routePathCharacterProblems(path string) []string {
	const allowed = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-._~!$&'()*+,;=:@/"
	for index := 0; index < len(path); index++ {
		character := path[index]
		if character == '%' {
			if index+2 >= len(path) || !isHexDigit(path[index+1]) || !isHexDigit(path[index+2]) {
				return []string{fmt.Sprintf("route path %q contains a malformed percent escape", path)}
			}
			index += 2
			continue
		}
		if character == '{' || character == '}' || character == '?' || character == '#' {
			continue
		}
		if character >= 0x80 || !strings.ContainsRune(allowed, rune(character)) {
			return []string{fmt.Sprintf("route path %q contains URI-unsafe character %q", path, character)}
		}
	}
	return nil
}

// isHexDigit recognizes the only byte values permitted in a URI percent escape.
func isHexDigit(character byte) bool {
	return character >= '0' && character <= '9' || character >= 'a' && character <= 'f' || character >= 'A' && character <= 'F'
}

// routeCatchAllParameters returns valid named wildcards that remain faithful in the manifest but cannot be projected as ordinary OpenAPI parameters.
func routeCatchAllParameters(path string) []string {
	parameters := make([]string, 0)
	for _, part := range strings.Split(path, "/") {
		if !strings.HasPrefix(part, "*") || len(part) < 2 {
			continue
		}
		name := part[1:]
		if validRouteParameterName(name) {
			parameters = append(parameters, name)
		}
	}
	sort.Strings(parameters)
	return parameters
}

// validRouteParameterName accepts portable Echo/OpenAPI names while rejecting delimiters that alter either template grammar.
func validRouteParameterName(name string) bool {
	if name == "" {
		return false
	}
	for _, character := range name {
		switch {
		case character >= 'a' && character <= 'z':
		case character >= 'A' && character <= 'Z':
		case character >= '0' && character <= '9':
		case character == '_', character == '-', character == '.':
		default:
			return false
		}
	}
	return true
}

// mergePathParams combines declaration and use evidence while retaining the strongest supported confidence.
func mergePathParams(fromRoute []Parameter, fromHandler []Parameter) []Parameter {
	merged := map[string]Parameter{}
	for _, p := range fromRoute {
		merged[p.Name] = p
	}
	for _, p := range fromHandler {
		if existing, ok := merged[p.Name]; ok {
			// Prefer higher confidence labels when there is a conflict.
			if existing.Confidence == "medium" && p.Confidence == "high" {
				merged[p.Name] = p
			}
			continue
		}
	}
	out := make([]Parameter, 0, len(merged))
	for _, p := range merged {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
