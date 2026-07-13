package webindex

import (
	"encoding/json"
	"go/ast"
	"go/constant"
	"go/token"
	"net/http"
	"path"
	"sort"
	"strconv"
	"strings"
)

// analyzedHandler retains inferred contract evidence separately from serialized operations so diagnostics can reject ambiguous source safely.
type analyzedHandler struct {
	PathParams  []Parameter
	QueryParams []Parameter
	Headers     []Parameter
	Cookies     []Parameter
	Body        *BodyShape
	Responses   []ResponseShape
	Dynamic     []analyzedDynamicParam
	Issues      []analyzedIssue
}

// analyzedDynamicParam records a parameter expression whose runtime name cannot be represented as a stable OpenAPI parameter.
type analyzedDynamicParam struct {
	Kind string
	Expr string
}

// analyzedIssue carries stable diagnostic identity from handler analysis into the final manifest.
type analyzedIssue struct {
	Code    string
	Message string
}

// handlerSchemaContext connects the fast handler walk to optional checked type information without changing serialized models.
type handlerSchemaContext struct {
	FileSet  *token.FileSet
	Registry *typedSchemaRegistry
	Imports  map[string]string
}

// contextAPI identifies the framework surface exposed by a handler parameter so similarly named methods are not conflated.
type contextAPI string

const (
	// contextAPIWeb selects GoForj's native context contract.
	contextAPIWeb contextAPI = "web"
	// contextAPIEcho selects the deliberate Echo compatibility contract.
	contextAPIEcho contextAPI = "echo"
)

// analyzeHandler preserves the syntax-only entry point used by focused inference tests and incomplete packages.
func analyzeHandler(fn *ast.FuncDecl) analyzedHandler {
	return analyzeHandlerWithTypes(fn, handlerSchemaContext{})
}

// analyzeHandlerWithTypes limits inference to declared HTTP contexts and enriches contract expressions when checked types are available.
func analyzeHandlerWithTypes(fn *ast.FuncDecl, schemaContext handlerSchemaContext) analyzedHandler {
	out := analyzedHandler{}
	if fn == nil || fn.Body == nil {
		return out
	}

	contextReceivers := handlerContextReceivers(fn, schemaContext.Imports)
	localTypes := collectLocalTypes(fn.Body)
	localConstants := collectLocalConstants(fn.Body, schemaContext.Imports)
	pathSeen := map[string]struct{}{}
	querySeen := map[string]struct{}{}
	headerSeen := map[string]struct{}{}
	cookieSeen := map[string]struct{}{}
	respSeen := map[string]struct{}{}
	bodySeen := map[string]struct{}{}
	bodyCandidates := make([]BodyShape, 0, 1)

	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if _, nestedFunction := n.(*ast.FuncLit); nestedFunction {
			// A closure owns its own execution and return contract, even when it captures the handler context.
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}

		api, directContextCall := contextAPIForReceiver(sel.X, contextReceivers)
		if directContextCall {
			switch sel.Sel.Name {
			case "Param":
				recordParameter(&out.PathParams, &out.Dynamic, pathSeen, call.Args, localConstants, "path", true, "high")
			case "Query":
				if api == contextAPIWeb {
					recordParameter(&out.QueryParams, &out.Dynamic, querySeen, call.Args, localConstants, "query", false, "high")
				}
			case "QueryParam":
				if api == contextAPIEcho {
					recordParameter(&out.QueryParams, &out.Dynamic, querySeen, call.Args, localConstants, "query", false, "high")
				}
			case "Header":
				if api == contextAPIWeb {
					recordParameter(&out.Headers, &out.Dynamic, headerSeen, call.Args, localConstants, "header", false, "high")
				}
			case "Cookie":
				recordParameter(&out.Cookies, &out.Dynamic, cookieSeen, call.Args, localConstants, "cookie", false, "high")
			case "Bind":
				if len(call.Args) == 1 {
					body := analyzeRequestBody(api, call.Args[0], localTypes, schemaContext)
					if body != nil {
						key := body.TypeName + "|" + schemaFingerprint(body.Schema)
						if _, exists := bodySeen[key]; !exists {
							bodySeen[key] = struct{}{}
							bodyCandidates = append(bodyCandidates, *body)
						}
					}
				}
			default:
				analyzeResponseCall(&out, respSeen, api, sel.Sel.Name, call.Args, localTypes, localConstants, schemaContext)
			}
			return true
		}

		if kind, ok := echoGetParameterKind(sel, contextReceivers); ok {
			switch kind {
			case "query":
				recordParameter(&out.QueryParams, &out.Dynamic, querySeen, call.Args, localConstants, kind, false, "medium")
			case "header":
				recordParameter(&out.Headers, &out.Dynamic, headerSeen, call.Args, localConstants, kind, false, "medium")
			}
		}
		return true
	})
	out.Body = mergeAnalyzedRequestBodies(&out, bodyCandidates)

	sort.Slice(out.PathParams, func(i, j int) bool { return out.PathParams[i].Name < out.PathParams[j].Name })
	sort.Slice(out.QueryParams, func(i, j int) bool { return out.QueryParams[i].Name < out.QueryParams[j].Name })
	sort.Slice(out.Headers, func(i, j int) bool { return out.Headers[i].Name < out.Headers[j].Name })
	sort.Slice(out.Cookies, func(i, j int) bool { return out.Cookies[i].Name < out.Cookies[j].Name })
	sort.Slice(out.Responses, func(i, j int) bool {
		if out.Responses[i].StatusCode == out.Responses[j].StatusCode {
			if out.Responses[i].Source == out.Responses[j].Source {
				if out.Responses[i].TypeName == out.Responses[j].TypeName {
					if out.Responses[i].ContentType == out.Responses[j].ContentType {
						return schemaFingerprint(out.Responses[i].Schema) < schemaFingerprint(out.Responses[j].Schema)
					}
					return out.Responses[i].ContentType < out.Responses[j].ContentType
				}
				return out.Responses[i].TypeName < out.Responses[j].TypeName
			}
			return out.Responses[i].Source < out.Responses[j].Source
		}
		return out.Responses[i].StatusCode < out.Responses[j].StatusCode
	})

	return out
}

// mergeAnalyzedRequestBodies retains every distinct Bind target and falls back to an unconstrained body when any branch lacks a trustworthy schema.
func mergeAnalyzedRequestBodies(out *analyzedHandler, bodies []BodyShape) *BodyShape {
	if len(bodies) == 0 {
		return nil
	}
	if len(bodies) == 1 {
		body := bodies[0]
		return &body
	}
	sort.Slice(bodies, func(i, j int) bool {
		left := bodies[i].TypeName + "|" + schemaFingerprint(bodies[i].Schema)
		right := bodies[j].TypeName + "|" + schemaFingerprint(bodies[j].Schema)
		return left < right
	})
	typeNames := make([]string, 0, len(bodies))
	schemas := make([]any, 0, len(bodies))
	allSchemasKnown := true
	for _, body := range bodies {
		if body.TypeName != "" {
			typeNames = append(typeNames, body.TypeName)
		}
		schema, ok := body.Schema.(map[string]any)
		if !ok || len(schema) == 0 {
			allSchemasKnown = false
			continue
		}
		schemas = append(schemas, schema)
	}
	message := "handler binds multiple request contracts"
	if len(typeNames) > 0 {
		message += ": " + strings.Join(typeNames, ", ")
	}
	out.Issues = append(out.Issues, analyzedIssue{Code: "ambiguous_request_body", Message: message})
	merged := &BodyShape{Source: bodies[0].Source, Confidence: "low", Schema: map[string]any{}}
	if allSchemasKnown {
		merged.Schema = map[string]any{"anyOf": schemas}
	}
	return merged
}

// analyzeRequestBody prefers exact checked types while retaining the AST identity when package loading is unavailable.
func analyzeRequestBody(api contextAPI, argument ast.Expr, localTypes map[string]string, schemaContext handlerSchemaContext) *BodyShape {
	body := &BodyShape{
		TypeName:   inferArgTypeName(argument, localTypes),
		Source:     string(api) + ".Bind",
		Confidence: "high",
	}
	if schemaContext.Registry == nil || schemaContext.FileSet == nil {
		if body.TypeName == "" {
			return nil
		}
		return body
	}
	resolved, ok := schemaContext.Registry.resolveExpression(schemaContext.FileSet, bindSchemaExpression(argument))
	if !ok {
		if body.TypeName == "" {
			body.Schema = map[string]any{}
			body.Confidence = "low"
		}
		return body
	}
	body.Schema = resolved.Schema
	body.TypeName = preferredContractTypeName(body.TypeName, bindSchemaExpression(argument), resolved.TypeName)
	body.Confidence = resolved.Confidence
	return body
}

// preferredContractTypeName keeps concise AST names for variables while replacing constructor guesses and raw anonymous declarations with checked identities.
func preferredContractTypeName(inferred string, expression ast.Expr, checked string) string {
	trimmed := strings.TrimSpace(inferred)
	if trimmed == "" || strings.HasPrefix(trimmed, "struct{") || strings.HasPrefix(trimmed, "struct {") {
		return checked
	}
	if _, ok := expression.(*ast.CallExpr); ok {
		return checked
	}
	return inferred
}

// bindSchemaExpression strips the address used as Bind's mutation target because it is not JSON-level request nullability.
func bindSchemaExpression(argument ast.Expr) ast.Expr {
	if unary, ok := argument.(*ast.UnaryExpr); ok && unary.Op == token.AND {
		return unary.X
	}
	return argument
}

// handlerContextReceivers identifies contexts by their imported package identity so aliases work and same-named decoy packages cannot contribute contracts.
func handlerContextReceivers(fn *ast.FuncDecl, imports map[string]string) map[string]contextAPI {
	receivers := map[string]contextAPI{}
	if fn == nil || fn.Type == nil || fn.Type.Params == nil {
		return receivers
	}
	for _, field := range fn.Type.Params.List {
		api, ok := contextAPIFromType(field.Type, imports)
		if !ok {
			continue
		}
		for _, name := range field.Names {
			receivers[name.Name] = api
		}
	}
	return receivers
}

// contextAPIFromType keeps native and Echo compatibility inference tied to exact import paths because their similarly named methods have different contracts.
func contextAPIFromType(expr ast.Expr, imports map[string]string) (contextAPI, bool) {
	if pointer, ok := expr.(*ast.StarExpr); ok {
		expr = pointer.X
	}
	selector, ok := expr.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Context" {
		return "", false
	}
	packageName, ok := selector.X.(*ast.Ident)
	if !ok {
		return "", false
	}
	switch imports[packageName.Name] {
	case "github.com/goforj/web":
		return contextAPIWeb, true
	case "github.com/labstack/echo/v4", "github.com/labstack/echo/v5":
		return contextAPIEcho, true
	default:
		return "", false
	}
}

// sourceImportPathsByAlias records source qualifiers and accounts for known versioned paths whose declared package name differs from the path base.
func sourceImportPathsByAlias(file *ast.File) map[string]string {
	imports := map[string]string{}
	if file == nil {
		return imports
	}
	for _, spec := range file.Imports {
		importPath, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		defaultAlias := path.Base(importPath)
		switch importPath {
		case "github.com/labstack/echo/v4", "github.com/labstack/echo/v5":
			defaultAlias = "echo"
		}
		alias := defaultAlias
		if spec.Name != nil {
			alias = spec.Name.Name
		}
		if alias == "" || alias == "_" || alias == "." {
			continue
		}
		imports[alias] = importPath
	}
	return imports
}

// contextAPIForReceiver rejects selectors rooted in services or payloads that happen to reuse HTTP-context method names.
func contextAPIForReceiver(expr ast.Expr, receivers map[string]contextAPI) (contextAPI, bool) {
	identifier, ok := expr.(*ast.Ident)
	if !ok {
		return "", false
	}
	api, ok := receivers[identifier.Name]
	return api, ok
}

// echoGetParameterKind recognizes only the two legacy Echo access chains retained for existing applications.
func echoGetParameterKind(selector *ast.SelectorExpr, receivers map[string]contextAPI) (string, bool) {
	if selector == nil || selector.Sel.Name != "Get" {
		return "", false
	}
	if queryCall, ok := selector.X.(*ast.CallExpr); ok {
		querySelector, ok := queryCall.Fun.(*ast.SelectorExpr)
		if ok && querySelector.Sel.Name == "QueryParams" {
			if api, direct := contextAPIForReceiver(querySelector.X, receivers); direct && api == contextAPIEcho {
				return "query", true
			}
		}
	}
	headerSelector, ok := selector.X.(*ast.SelectorExpr)
	if !ok || headerSelector.Sel.Name != "Header" {
		return "", false
	}
	requestCall, ok := headerSelector.X.(*ast.CallExpr)
	if !ok {
		return "", false
	}
	requestSelector, ok := requestCall.Fun.(*ast.SelectorExpr)
	if !ok || requestSelector.Sel.Name != "Request" {
		return "", false
	}
	if api, direct := contextAPIForReceiver(requestSelector.X, receivers); direct && api == contextAPIEcho {
		return "header", true
	}
	return "", false
}

// recordParameter preserves unknown keys as diagnostics because inventing parameter names would make the generated contract misleading.
func recordParameter(target *[]Parameter, dynamic *[]analyzedDynamicParam, seen map[string]struct{}, args []ast.Expr, localConstants map[string]constant.Value, kind string, required bool, confidence string) {
	if len(args) != 1 {
		return
	}
	name, resolved := evaluateStringConstant(args[0], localConstants, nil)
	if !resolved || name == "" {
		*dynamic = append(*dynamic, analyzedDynamicParam{Kind: kind, Expr: exprString(args[0])})
		return
	}
	if _, ok := seen[name]; ok {
		return
	}
	seen[name] = struct{}{}
	*target = append(*target, Parameter{
		Name:       name,
		In:         kind,
		Required:   required,
		Confidence: confidence,
	})
}

// analyzeResponseCall records status-unknown responses instead of dropping them so the projection can expose an honest default response.
func analyzeResponseCall(out *analyzedHandler, seen map[string]struct{}, api contextAPI, method string, args []ast.Expr, localTypes map[string]string, localConstants map[string]constant.Value, schemaContext handlerSchemaContext) {
	if !supportsResponseMethod(api, method) {
		return
	}

	response := ResponseShape{Source: string(api) + "." + method, Confidence: "high"}
	if method == "File" {
		if len(args) != 1 {
			return
		}
		response.ContentType = "application/octet-stream"
		response.Confidence = "medium"
		recordAnalyzedResponse(out, seen, response)
		return
	}
	if len(args) == 0 {
		return
	}

	status, ok := parseStatusCodeWithConstants(args[0], localConstants, schemaContext.Imports)
	if ok {
		response.StatusCode = status
	} else {
		response.Confidence = "low"
		out.Issues = append(out.Issues, analyzedIssue{
			Code:    "unresolved_status_code",
			Message: "response status expression (" + exprString(args[0]) + ") could not be evaluated",
		})
	}

	switch method {
	case "JSON":
		if len(args) > 1 {
			response.TypeName = inferArgTypeName(args[1], localTypes)
			response.Schema = inferJSONSchemaExpr(args[1])
			applyTypedResponseSchema(&response, args[1], schemaContext)
			if response.TypeName == "" && response.Confidence == "high" {
				response.Confidence = "medium"
			}
		}
	case "Blob":
		if len(args) > 1 {
			if contentType, resolved := evaluateStringConstant(args[1], localConstants, schemaContext.Imports); resolved {
				response.ContentType = contentType
			} else {
				if response.Confidence == "high" {
					response.Confidence = "medium"
				}
				out.Issues = append(out.Issues, analyzedIssue{
					Code:    "unresolved_response_content_type",
					Message: "blob content type expression (" + exprString(args[1]) + ") could not be evaluated",
				})
			}
		}
	}
	recordAnalyzedResponse(out, seen, response)
}

// applyTypedResponseSchema replaces syntax guesses with literal-aware checked schemas when focused type information is available.
func applyTypedResponseSchema(response *ResponseShape, argument ast.Expr, schemaContext handlerSchemaContext) {
	if response == nil || schemaContext.Registry == nil || schemaContext.FileSet == nil {
		return
	}
	resolved, ok := schemaContext.Registry.resolveJSONExpression(schemaContext.FileSet, argument)
	if !ok {
		return
	}
	response.Schema = resolved.Schema
	response.TypeName = preferredContractTypeName(response.TypeName, argument, resolved.TypeName)
	if resolved.Confidence == "low" {
		response.Confidence = "low"
	}
}

// supportsResponseMethod makes compatibility additions deliberate so native Context growth does not silently inherit Echo semantics.
func supportsResponseMethod(api contextAPI, method string) bool {
	switch api {
	case contextAPIWeb:
		switch method {
		case "JSON", "Blob", "Text", "HTML", "NoContent", "Redirect", "File":
			return true
		}
	case contextAPIEcho:
		switch method {
		case "JSON", "Blob", "String", "HTML", "NoContent", "Redirect", "File", "XML":
			return true
		}
	}
	return false
}

// recordAnalyzedResponse deduplicates identical branches while retaining distinct media types for the same status.
func recordAnalyzedResponse(out *analyzedHandler, seen map[string]struct{}, response ResponseShape) {
	key := strconv.Itoa(response.StatusCode) + "|" + response.TypeName + "|" + response.ContentType + "|" + response.Source + "|" + schemaFingerprint(response.Schema)
	if _, ok := seen[key]; ok {
		return
	}
	seen[key] = struct{}{}
	out.Responses = append(out.Responses, response)
}

// collectLocalTypes retains declared payload identities because AST-only indexing cannot recover them after a value is passed to Bind or JSON.
func collectLocalTypes(body *ast.BlockStmt) map[string]string {
	out := map[string]string{}
	for _, stmt := range body.List {
		switch s := stmt.(type) {
		case *ast.DeclStmt:
			gen, ok := s.Decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.VAR {
				continue
			}
			for _, spec := range gen.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				typeName := typeNameFromExpr(vs.Type)
				for idx, name := range vs.Names {
					if name == nil {
						continue
					}
					if typeName != "" {
						out[name.Name] = typeName
						continue
					}
					if idx < len(vs.Values) {
						if inferred := inferExprTypeName(vs.Values[idx], out); inferred != "" {
							out[name.Name] = inferred
						}
					}
				}
			}
		case *ast.AssignStmt:
			if s.Tok != token.DEFINE && s.Tok != token.ASSIGN {
				continue
			}
			for i := range s.Lhs {
				id, ok := s.Lhs[i].(*ast.Ident)
				if !ok || i >= len(s.Rhs) {
					continue
				}
				// Keep an existing variable type on plain assignment to avoid
				// downgrading typed variables into function-call names.
				if s.Tok == token.ASSIGN {
					if _, exists := out[id.Name]; exists {
						continue
					}
				}
				if inferred := inferExprTypeName(s.Rhs[i], out); inferred != "" {
					out[id.Name] = inferred
				}
			}
		}
	}
	return out
}

// collectLocalConstants resolves function-local constants that commonly wrap HTTP statuses and response media types without loading package types.
func collectLocalConstants(body *ast.BlockStmt, importSets ...map[string]string) map[string]constant.Value {
	values := map[string]constant.Value{}
	var imports map[string]string
	if len(importSets) > 0 {
		imports = importSets[0]
	}
	if body == nil {
		return values
	}
	ambiguous := map[string]struct{}{}
	declarationCounts := map[string]int{}
	markNestedConstants := func(node ast.Node) {
		ast.Inspect(node, func(nested ast.Node) bool {
			if _, nestedFunction := nested.(*ast.FuncLit); nestedFunction {
				return false
			}
			declaration, ok := nested.(*ast.GenDecl)
			if !ok || declaration.Tok != token.CONST {
				return true
			}
			for _, rawSpec := range declaration.Specs {
				spec, ok := rawSpec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for _, name := range spec.Names {
					ambiguous[name.Name] = struct{}{}
				}
			}
			return false
		})
	}
	for _, statement := range body.List {
		declarationStatement, ok := statement.(*ast.DeclStmt)
		if !ok {
			markNestedConstants(statement)
			continue
		}
		declaration, ok := declarationStatement.Decl.(*ast.GenDecl)
		if !ok || declaration.Tok != token.CONST {
			markNestedConstants(statement)
			continue
		}
		for _, rawSpec := range declaration.Specs {
			spec, ok := rawSpec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for _, name := range spec.Names {
				declarationCounts[name.Name]++
				if declarationCounts[name.Name] > 1 {
					ambiguous[name.Name] = struct{}{}
				}
			}
			for _, expression := range spec.Values {
				markNestedConstants(expression)
			}
		}
	}
	for _, statement := range body.List {
		declarationStatement, ok := statement.(*ast.DeclStmt)
		if !ok {
			continue
		}
		declaration, ok := declarationStatement.Decl.(*ast.GenDecl)
		if !ok || declaration.Tok != token.CONST {
			continue
		}
		var previousExpressions []ast.Expr
		for specIndex, rawSpec := range declaration.Specs {
			spec, ok := rawSpec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			expressions := spec.Values
			if len(expressions) == 0 {
				expressions = previousExpressions
			} else {
				previousExpressions = expressions
			}
			previousIota, hadIota := values["iota"]
			values["iota"] = constant.MakeInt64(int64(specIndex))
			for index, name := range spec.Names {
				if _, unsafe := ambiguous[name.Name]; unsafe {
					delete(values, name.Name)
					continue
				}
				if index >= len(expressions) {
					continue
				}
				if value, resolved := evaluateConstantExpression(expressions[index], values, imports); resolved {
					values[name.Name] = value
				}
			}
			if hadIota {
				values["iota"] = previousIota
			} else {
				delete(values, "iota")
			}
		}
	}
	return values
}

// parseStatusCode preserves the original helper boundary for focused tests while the analyzer supplies any local constants separately.
func parseStatusCode(expr ast.Expr) int {
	status, ok := parseStatusCodeWithConstants(expr, nil, map[string]string{"http": "net/http"})
	if !ok {
		return 0
	}
	return status
}

// parseStatusCodeWithConstants accepts Go integer constant expressions so aliases and arithmetic are not silently discarded.
func parseStatusCodeWithConstants(expr ast.Expr, localConstants map[string]constant.Value, importSets ...map[string]string) (int, bool) {
	var imports map[string]string
	if len(importSets) > 0 {
		imports = importSets[0]
	}
	value, ok := evaluateConstantExpression(expr, localConstants, imports)
	if !ok || value.Kind() != constant.Int {
		return 0, false
	}
	status, exact := constant.Int64Val(value)
	if !exact || int64(int(status)) != status {
		return 0, false
	}
	return int(status), true
}

// evaluateStringConstant preserves literal media types while leaving package-level or runtime expressions explicitly unresolved.
func evaluateStringConstant(expr ast.Expr, localConstants map[string]constant.Value, imports map[string]string) (string, bool) {
	value, ok := evaluateConstantExpression(expr, localConstants, imports)
	if !ok || value.Kind() != constant.String {
		return "", false
	}
	return constant.StringVal(value), true
}

// evaluateConstantExpression implements the safe AST subset needed by statuses and media types without pretending to be a full type checker.
func evaluateConstantExpression(expr ast.Expr, localConstants map[string]constant.Value, imports map[string]string) (constant.Value, bool) {
	switch value := expr.(type) {
	case *ast.BasicLit:
		resolved := constant.MakeFromLiteral(value.Value, value.Kind, 0)
		if resolved.Kind() == constant.Unknown {
			return nil, false
		}
		return resolved, true
	case *ast.Ident:
		resolved, ok := localConstants[value.Name]
		return resolved, ok
	case *ast.ParenExpr:
		return evaluateConstantExpression(value.X, localConstants, imports)
	case *ast.SelectorExpr:
		packageName, ok := value.X.(*ast.Ident)
		if !ok || packageName.Obj != nil || imports[packageName.Name] != "net/http" {
			return nil, false
		}
		status, ok := httpStatusMap[value.Sel.Name]
		if !ok {
			return nil, false
		}
		return constant.MakeInt64(int64(status)), true
	case *ast.UnaryExpr:
		operand, ok := evaluateConstantExpression(value.X, localConstants, imports)
		if !ok || operand.Kind() != constant.Int {
			return nil, false
		}
		switch value.Op {
		case token.ADD, token.SUB, token.XOR:
			return constant.UnaryOp(value.Op, operand, 0), true
		default:
			return nil, false
		}
	case *ast.BinaryExpr:
		left, leftOK := evaluateConstantExpression(value.X, localConstants, imports)
		right, rightOK := evaluateConstantExpression(value.Y, localConstants, imports)
		if !leftOK || !rightOK {
			return nil, false
		}
		if value.Op == token.SHL || value.Op == token.SHR {
			if left.Kind() != constant.Int || right.Kind() != constant.Int {
				return nil, false
			}
			shift, exact := constant.Uint64Val(right)
			if !exact || shift > uint64(^uint(0)) {
				return nil, false
			}
			return constant.Shift(left, value.Op, uint(shift)), true
		}
		if (value.Op == token.QUO || value.Op == token.REM) && constant.Sign(right) == 0 {
			return nil, false
		}
		if value.Op == token.ADD && left.Kind() == constant.String && right.Kind() == constant.String {
			return constant.BinaryOp(left, value.Op, right), true
		}
		if left.Kind() != constant.Int || right.Kind() != constant.Int {
			return nil, false
		}
		switch value.Op {
		case token.ADD, token.SUB, token.MUL, token.QUO, token.REM, token.AND, token.OR, token.XOR, token.AND_NOT:
			return constant.BinaryOp(left, value.Op, right), true
		default:
			return nil, false
		}
	default:
		return nil, false
	}
}

// inferArgTypeName unwraps pointer-taking call sites because Bind conventionally receives an address while schemas describe the underlying payload.
func inferArgTypeName(expr ast.Expr, locals map[string]string) string {
	switch e := expr.(type) {
	case *ast.UnaryExpr:
		if e.Op == token.AND {
			return inferExprTypeName(e.X, locals)
		}
	}
	return inferExprTypeName(expr, locals)
}

// inferExprTypeName keeps source-level names stable for the later schema registry rather than inventing anonymous component identities here.
func inferExprTypeName(expr ast.Expr, locals map[string]string) string {
	switch e := expr.(type) {
	case *ast.Ident:
		if t, ok := locals[e.Name]; ok {
			return t
		}
	case *ast.CompositeLit:
		return typeNameFromExpr(e.Type)
	case *ast.CallExpr:
		// new(T)
		if id, ok := e.Fun.(*ast.Ident); ok && id.Name == "new" && len(e.Args) == 1 {
			return typeNameFromExpr(e.Args[0])
		}
		// constructor: pkg.NewX() -> pkg.NewX
		return exprString(e.Fun)
	case *ast.SelectorExpr:
		return exprString(e)
	}
	return ""
}

// inferJSONSchemaExpr extracts only shapes that are evident from syntax so unresolved values remain unconstrained instead of being mislabeled as strings.
func inferJSONSchemaExpr(expr ast.Expr) any {
	switch e := expr.(type) {
	case *ast.UnaryExpr:
		if e.Op == token.AND {
			return inferJSONSchemaExpr(e.X)
		}
	case *ast.CompositeLit:
		switch t := e.Type.(type) {
		case *ast.MapType:
			if key, ok := t.Key.(*ast.Ident); ok && key.Name == "string" {
				props := map[string]any{}
				required := make([]string, 0, len(e.Elts))
				for _, elt := range e.Elts {
					kv, ok := elt.(*ast.KeyValueExpr)
					if !ok {
						return map[string]any{}
					}
					name, ok := syntaxStringMapKey(kv.Key)
					if !ok {
						return map[string]any{
							"type":                 "object",
							"additionalProperties": map[string]any{},
						}
					}
					if _, exists := props[name]; !exists {
						required = append(required, name)
					}
					if schema := inferJSONSchemaExpr(kv.Value); schema != nil {
						props[name] = schema
					} else {
						props[name] = map[string]any{}
					}
				}
				sort.Strings(required)
				out := map[string]any{
					"type":                 "object",
					"properties":           props,
					"additionalProperties": false,
				}
				if len(required) > 0 {
					out["required"] = required
				}
				return out
			}
		case *ast.ArrayType:
			if t.Len == nil {
				if element, ok := t.Elt.(*ast.Ident); ok && element.Name == "byte" {
					return map[string]any{"type": "string", "format": "byte"}
				}
			}
			items := make([]map[string]any, 0, len(e.Elts))
			for _, element := range e.Elts {
				if keyed, ok := element.(*ast.KeyValueExpr); ok {
					element = keyed.Value
				}
				inferred, ok := inferJSONSchemaExpr(element).(map[string]any)
				if !ok {
					items = append(items, map[string]any{})
					continue
				}
				items = append(items, inferred)
			}
			item := map[string]any{}
			if len(items) > 0 {
				item = combinedJSONItemSchema(items)
			}
			return map[string]any{
				"type":  "array",
				"items": item,
			}
		}
	case *ast.BasicLit:
		switch e.Kind {
		case token.STRING:
			return map[string]any{"type": "string"}
		case token.INT, token.CHAR:
			return map[string]any{"type": "integer"}
		case token.FLOAT:
			return map[string]any{"type": "number"}
		}
	case *ast.Ident:
		switch e.Name {
		case "true", "false":
			return map[string]any{"type": "boolean"}
		case "nil":
			return map[string]any{"nullable": true}
		}
	}
	return nil
}

// syntaxStringMapKey accepts even an empty literal key while rejecting identifiers whose constant values require type information.
func syntaxStringMapKey(expression ast.Expr) (string, bool) {
	literal, ok := expression.(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return "", false
	}
	value, err := strconv.Unquote(literal.Value)
	return value, err == nil
}

// schemaFingerprint makes response deduplication deterministic without coupling analysis to OpenAPI component naming.
func schemaFingerprint(schema any) string {
	if schema == nil {
		return ""
	}
	data, err := json.Marshal(schema)
	if err != nil {
		return ""
	}
	return string(data)
}

// httpStatusMap mirrors net/http's exported status constants so every standard status remains available to AST-only evaluation.
var httpStatusMap = map[string]int{
	"StatusContinue":                      http.StatusContinue,
	"StatusSwitchingProtocols":            http.StatusSwitchingProtocols,
	"StatusProcessing":                    http.StatusProcessing,
	"StatusEarlyHints":                    http.StatusEarlyHints,
	"StatusOK":                            http.StatusOK,
	"StatusCreated":                       http.StatusCreated,
	"StatusAccepted":                      http.StatusAccepted,
	"StatusNonAuthoritativeInfo":          http.StatusNonAuthoritativeInfo,
	"StatusNoContent":                     http.StatusNoContent,
	"StatusResetContent":                  http.StatusResetContent,
	"StatusPartialContent":                http.StatusPartialContent,
	"StatusMultiStatus":                   http.StatusMultiStatus,
	"StatusAlreadyReported":               http.StatusAlreadyReported,
	"StatusIMUsed":                        http.StatusIMUsed,
	"StatusMultipleChoices":               http.StatusMultipleChoices,
	"StatusMovedPermanently":              http.StatusMovedPermanently,
	"StatusFound":                         http.StatusFound,
	"StatusSeeOther":                      http.StatusSeeOther,
	"StatusNotModified":                   http.StatusNotModified,
	"StatusUseProxy":                      http.StatusUseProxy,
	"StatusTemporaryRedirect":             http.StatusTemporaryRedirect,
	"StatusPermanentRedirect":             http.StatusPermanentRedirect,
	"StatusBadRequest":                    http.StatusBadRequest,
	"StatusUnauthorized":                  http.StatusUnauthorized,
	"StatusPaymentRequired":               http.StatusPaymentRequired,
	"StatusForbidden":                     http.StatusForbidden,
	"StatusNotFound":                      http.StatusNotFound,
	"StatusMethodNotAllowed":              http.StatusMethodNotAllowed,
	"StatusNotAcceptable":                 http.StatusNotAcceptable,
	"StatusProxyAuthRequired":             http.StatusProxyAuthRequired,
	"StatusRequestTimeout":                http.StatusRequestTimeout,
	"StatusConflict":                      http.StatusConflict,
	"StatusGone":                          http.StatusGone,
	"StatusLengthRequired":                http.StatusLengthRequired,
	"StatusPreconditionFailed":            http.StatusPreconditionFailed,
	"StatusRequestEntityTooLarge":         http.StatusRequestEntityTooLarge,
	"StatusRequestURITooLong":             http.StatusRequestURITooLong,
	"StatusUnsupportedMediaType":          http.StatusUnsupportedMediaType,
	"StatusRequestedRangeNotSatisfiable":  http.StatusRequestedRangeNotSatisfiable,
	"StatusExpectationFailed":             http.StatusExpectationFailed,
	"StatusTeapot":                        http.StatusTeapot,
	"StatusMisdirectedRequest":            http.StatusMisdirectedRequest,
	"StatusUnprocessableEntity":           http.StatusUnprocessableEntity,
	"StatusLocked":                        http.StatusLocked,
	"StatusFailedDependency":              http.StatusFailedDependency,
	"StatusTooEarly":                      http.StatusTooEarly,
	"StatusUpgradeRequired":               http.StatusUpgradeRequired,
	"StatusPreconditionRequired":          http.StatusPreconditionRequired,
	"StatusTooManyRequests":               http.StatusTooManyRequests,
	"StatusRequestHeaderFieldsTooLarge":   http.StatusRequestHeaderFieldsTooLarge,
	"StatusUnavailableForLegalReasons":    http.StatusUnavailableForLegalReasons,
	"StatusInternalServerError":           http.StatusInternalServerError,
	"StatusNotImplemented":                http.StatusNotImplemented,
	"StatusBadGateway":                    http.StatusBadGateway,
	"StatusServiceUnavailable":            http.StatusServiceUnavailable,
	"StatusGatewayTimeout":                http.StatusGatewayTimeout,
	"StatusHTTPVersionNotSupported":       http.StatusHTTPVersionNotSupported,
	"StatusVariantAlsoNegotiates":         http.StatusVariantAlsoNegotiates,
	"StatusInsufficientStorage":           http.StatusInsufficientStorage,
	"StatusLoopDetected":                  http.StatusLoopDetected,
	"StatusNotExtended":                   http.StatusNotExtended,
	"StatusNetworkAuthenticationRequired": http.StatusNetworkAuthenticationRequired,
}
