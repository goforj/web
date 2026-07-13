package webindex

import (
	"encoding/json"
	"fmt"
	"math/big"
	"mime"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// openAPIProjectionCandidate keeps manifest ordering and semantic identity separate from the normalized Path Item keys.
type openAPIProjectionCandidate struct {
	Operation Operation
	Method    string
	Path      string
}

// openAPIProjectionCandidates omits source routes that cannot become faithful OpenAPI Path Items while preserving them in the canonical manifest.
func openAPIProjectionCandidates(operations []Operation) []openAPIProjectionCandidate {
	candidates := make([]openAPIProjectionCandidate, 0, len(operations))
	for _, operation := range operations {
		method := normalizeMethodExpr(operation.Method)
		if !isOpenAPIMethod(method) || !openAPIRoutePathProjectable(operation.Path) {
			continue
		}
		candidates = append(candidates, openAPIProjectionCandidate{
			Operation: operation,
			Method:    method,
			Path:      toOpenAPIPath(operation.Path),
		})
	}
	return candidates
}

// openAPIRoutePathProjectable accepts double-slash hygiene issues but rejects syntax with no faithful OpenAPI template semantics.
func openAPIRoutePathProjectable(path string) bool {
	if len(routeTemplateProblems(path)) > 0 {
		return false
	}
	for _, segment := range strings.Split(path, "/") {
		if strings.HasPrefix(segment, "*") {
			return false
		}
	}
	return true
}

// openAPISourcePolicyDiagnostics records source-derived projection losses so lenient output remains valid and strict indexing can reject them.
func openAPISourcePolicyDiagnostics(operation Operation) []Diagnostic {
	diagnostics := make([]Diagnostic, 0)
	seen := map[string]struct{}{}
	appendDiagnostic := func(code, message string) {
		key := code + "|" + message
		if _, duplicate := seen[key]; duplicate {
			return
		}
		seen[key] = struct{}{}
		diagnostics = append(diagnostics, Diagnostic{
			Severity:  "warn",
			Code:      code,
			Message:   message,
			File:      operation.Handler.File,
			Line:      operation.Handler.Line,
			Operation: operation.ID,
		})
	}
	headers := map[string]string{}
	for _, parameter := range operation.Inputs.Headers {
		lowerName := strings.ToLower(parameter.Name)
		if !isHTTPToken(parameter.Name) {
			appendDiagnostic("invalid_header_parameter", fmt.Sprintf("header %q is not a valid HTTP field name and was omitted from OpenAPI", parameter.Name))
		}
		if isReservedOpenAPIHeader(lowerName) {
			appendDiagnostic("reserved_openapi_header", fmt.Sprintf("header %q is represented through OpenAPI media or security configuration and was omitted as a parameter", parameter.Name))
		}
		if previous, duplicate := headers[lowerName]; duplicate {
			appendDiagnostic("duplicate_header_parameter", fmt.Sprintf("headers %q and %q are equivalent under HTTP case-insensitive matching", previous, parameter.Name))
		} else {
			headers[lowerName] = parameter.Name
		}
	}
	for _, response := range operation.Outputs.Responses {
		if response.StatusCode != 0 && (response.StatusCode < 100 || response.StatusCode > 599) {
			appendDiagnostic("invalid_response_status", fmt.Sprintf("response status %d is outside the HTTP range 100-599 and projects to default", response.StatusCode))
		}
		if response.ContentType != "" {
			if _, valid := canonicalOpenAPIMediaType(response.ContentType); !valid {
				appendDiagnostic("invalid_response_content_type", fmt.Sprintf("response content type %q is invalid and was omitted from OpenAPI", response.ContentType))
			}
		}
		if response.StatusCode >= 100 && response.StatusCode <= 599 && !httpStatusAllowsContent(response.StatusCode) {
			if _, schema := responseContent(response); schema != nil {
				appendDiagnostic("response_content_not_allowed", fmt.Sprintf("response status %d cannot carry content, so the inferred body was omitted from OpenAPI", response.StatusCode))
			}
		}
	}
	sort.Slice(diagnostics, func(i, j int) bool {
		if diagnostics[i].Code == diagnostics[j].Code {
			return diagnostics[i].Message < diagnostics[j].Message
		}
		return diagnostics[i].Code < diagnostics[j].Code
	})
	return diagnostics
}

// isReservedOpenAPIHeader reports request headers represented elsewhere in an OpenAPI operation.
func isReservedOpenAPIHeader(lowerName string) bool {
	switch strings.ToLower(lowerName) {
	case "accept", "content-type", "authorization":
		return true
	default:
		return false
	}
}

// openAPIProjectedOperationProblems validates one complete Operation Object after explicit overrides have been applied.
func openAPIProjectedOperationProblems(path, method string, operation OpenAPIOp) []string {
	context := "operation " + strings.ToUpper(method) + " " + path
	problems := make([]string, 0)
	if strings.TrimSpace(operation.OperationID) == "" {
		problems = append(problems, context+" requires a non-empty operationId")
	} else if !isCodegenSafeIdentifier(operation.OperationID) {
		problems = append(problems, fmt.Sprintf("%s operationId %q is not codegen-safe", context, operation.OperationID))
	}
	templateNames, templateProblems := openAPIPathTemplateNames(path)
	for _, problem := range templateProblems {
		problems = append(problems, context+" "+problem)
	}
	templateSet := make(map[string]struct{}, len(templateNames))
	for _, name := range templateNames {
		templateSet[name] = struct{}{}
	}
	pathParameters := map[string]struct{}{}
	seenParameters := map[string]struct{}{}
	for _, parameter := range operation.Parameters {
		name := strings.TrimSpace(parameter.Name)
		identityName := name
		if parameter.In == "header" {
			identityName = strings.ToLower(name)
		}
		key := parameter.In + "|" + identityName
		if _, duplicate := seenParameters[key]; duplicate {
			problems = append(problems, fmt.Sprintf("%s repeats parameter %s %q", context, parameter.In, name))
			continue
		}
		seenParameters[key] = struct{}{}
		if name == "" {
			problems = append(problems, context+" has a parameter with an empty name")
		}
		switch parameter.In {
		case "path":
			pathParameters[name] = struct{}{}
			if !parameter.Required {
				problems = append(problems, fmt.Sprintf("%s path parameter %q must be required", context, name))
			}
		case "query", "cookie":
		case "header":
			if !isHTTPToken(name) {
				problems = append(problems, fmt.Sprintf("%s header parameter %q is not a valid HTTP field name", context, name))
			}
			if isReservedOpenAPIHeader(name) {
				problems = append(problems, fmt.Sprintf("%s header parameter %q is reserved by OpenAPI", context, name))
			}
		default:
			problems = append(problems, fmt.Sprintf("%s parameter %q has unsupported location %q", context, name, parameter.In))
		}
		problems = append(problems, openAPISchemaProblems(context+" parameter "+parameter.In+" "+strconv.Quote(name), parameter.Schema)...)
		if parameter.Example != nil {
			problems = append(problems, openAPIJSONValueProblems(context+" parameter "+parameter.In+" "+strconv.Quote(name)+" example", parameter.Example)...)
		}
	}
	for _, name := range templateNames {
		if _, exists := pathParameters[name]; !exists {
			problems = append(problems, fmt.Sprintf("%s path template {%s} has no matching path parameter", context, name))
		}
	}
	for name := range pathParameters {
		if _, exists := templateSet[name]; !exists {
			problems = append(problems, fmt.Sprintf("%s path parameter %q does not appear in the path template", context, name))
		}
	}
	if operation.RequestBody != nil {
		problems = append(problems, openAPIRequestBodyProblems(context, operation.RequestBody)...)
	}
	if len(operation.Responses) == 0 {
		problems = append(problems, context+" requires at least one response")
	}
	for _, status := range sortedMapKeys(operation.Responses) {
		responseContext := context + " response " + status
		if !isOpenAPIResponseKey(status) {
			problems = append(problems, fmt.Sprintf("%s has invalid response key %q", context, status))
		}
		response := operation.Responses[status]
		description, ok := response["description"].(string)
		if !ok || strings.TrimSpace(description) == "" {
			problems = append(problems, responseContext+" requires a non-empty description")
		}
		if content, present := response["content"]; present {
			if statusCode, err := strconv.Atoi(status); err == nil && !httpStatusAllowsContent(statusCode) {
				problems = append(problems, fmt.Sprintf("%s cannot define content for status %d", context, statusCode))
			}
			problems = append(problems, openAPIContentProblems(responseContext, content)...)
		}
	}
	return problems
}

// openAPIPathTemplateNames parses OpenAPI template expressions while rejecting unmatched or nested braces.
func openAPIPathTemplateNames(path string) ([]string, []string) {
	names := make([]string, 0)
	problems := make([]string, 0)
	seen := map[string]struct{}{}
	for index := 0; index < len(path); {
		switch path[index] {
		case '{':
			end := strings.IndexByte(path[index+1:], '}')
			if end < 0 {
				return names, append(problems, "has an unmatched { in its path template")
			}
			end += index + 1
			name := path[index+1 : end]
			if name == "" || strings.ContainsAny(name, "{}/") || strings.TrimSpace(name) != name {
				problems = append(problems, "has an empty or nested path template expression")
			} else if _, duplicate := seen[name]; !duplicate {
				seen[name] = struct{}{}
				names = append(names, name)
			}
			index = end + 1
		case '}':
			problems = append(problems, "has an unmatched } in its path template")
			index++
		default:
			index++
		}
	}
	return names, problems
}

// openAPIRequestBodyProblems validates the Request Body Object and its media contracts.
func openAPIRequestBodyProblems(context string, body map[string]any) []string {
	problems := make([]string, 0)
	if required, present := body["required"]; present {
		if _, ok := required.(bool); !ok {
			problems = append(problems, context+" request body required must be boolean")
		}
	}
	content, present := body["content"]
	if !present {
		return append(problems, context+" request body requires content")
	}
	problems = append(problems, openAPIContentProblems(context+" request body", content)...)
	return problems
}

// openAPIContentProblems validates media type keys and recursively checks their schema and example values.
func openAPIContentProblems(context string, raw any) []string {
	content, ok := raw.(map[string]any)
	if !ok {
		return []string{context + " content must be an object"}
	}
	if len(content) == 0 {
		return []string{context + " content must contain at least one media type"}
	}
	problems := make([]string, 0)
	seen := map[string]string{}
	for _, mediaType := range sortedMapKeys(content) {
		normalized, valid := canonicalOpenAPIMediaType(mediaType)
		if !valid {
			problems = append(problems, fmt.Sprintf("%s has invalid media type %q", context, mediaType))
		} else if previous, duplicate := seen[normalized]; duplicate {
			problems = append(problems, fmt.Sprintf("%s repeats equivalent media types %q and %q", context, previous, mediaType))
		} else {
			seen[normalized] = mediaType
		}
		media, ok := content[mediaType].(map[string]any)
		if !ok {
			problems = append(problems, fmt.Sprintf("%s media type %q must contain a Media Type Object", context, mediaType))
			continue
		}
		if schema, present := media["schema"]; present {
			problems = append(problems, openAPISchemaProblems(context+" media type "+strconv.Quote(mediaType), schema)...)
		}
		if example, present := media["example"]; present {
			problems = append(problems, openAPIJSONValueProblems(context+" media type "+strconv.Quote(mediaType)+" example", example)...)
		}
	}
	return problems
}

// canonicalOpenAPIMediaType validates a media type or range and returns its deterministic serialization.
func canonicalOpenAPIMediaType(value string) (string, bool) {
	if value == "" || strings.TrimSpace(value) != value {
		return "", false
	}
	base, parameters, err := mime.ParseMediaType(value)
	if err != nil {
		return "", false
	}
	typeAndSubtype := strings.Split(base, "/")
	if len(typeAndSubtype) != 2 || !isHTTPToken(typeAndSubtype[0]) || !isHTTPToken(typeAndSubtype[1]) {
		return "", false
	}
	if typeAndSubtype[0] == "*" && typeAndSubtype[1] != "*" {
		return "", false
	}
	base = strings.ToLower(base)
	formatted := mime.FormatMediaType(base, parameters)
	if formatted == "" {
		return "", false
	}
	return formatted, true
}

// httpStatusAllowsContent applies HTTP payload prohibitions that OpenAPI generators cannot infer from a response status alone.
func httpStatusAllowsContent(status int) bool {
	return status >= 200 && status != 204 && status != 205 && status != 304
}

// openAPIDocumentProblems validates cross-operation uniqueness, path equivalence, component schemas, and local references.
func openAPIDocumentProblems(document OpenAPIDocument) []string {
	problems := make([]string, 0)
	operationIDs := map[string]string{}
	templateShapes := map[string]string{}
	for _, path := range sortedMapKeys(document.Paths) {
		shape := openAPIPathTemplateShape(path)
		if previous, exists := templateShapes[shape]; exists && previous != path {
			problems = append(problems, fmt.Sprintf("OpenAPI paths %q and %q have the same templated hierarchy", previous, path))
		} else {
			templateShapes[shape] = path
		}
		for _, method := range sortedMapKeys(document.Paths[path]) {
			operation := document.Paths[path][method]
			location := strings.ToUpper(method) + " " + path
			if previous, exists := operationIDs[operation.OperationID]; exists {
				problems = append(problems, fmt.Sprintf("operationId %q is shared by %s and %s", operation.OperationID, previous, location))
			} else {
				operationIDs[operation.OperationID] = location
			}
		}
	}
	if rawSchemas, exists := document.Components["schemas"]; exists {
		schemas, ok := rawSchemas.(map[string]any)
		if !ok {
			problems = append(problems, "components.schemas must be an object")
		} else {
			for _, name := range sortedMapKeys(schemas) {
				problems = append(problems, openAPISchemaProblems("component schema "+strconv.Quote(name), schemas[name])...)
			}
		}
	}
	problems = append(problems, openAPILocalReferenceProblems(document)...)
	return problems
}

// pruneOpenAPIComponentSchemas retains only components reachable from projected operations and their transitive schema dependencies.
func pruneOpenAPIComponentSchemas(document *OpenAPIDocument) []string {
	if document == nil || document.Components == nil {
		return nil
	}
	rawSchemas, exists := document.Components["schemas"]
	if !exists {
		return nil
	}
	schemas, ok := rawSchemas.(map[string]any)
	if !ok {
		return nil
	}
	reachable := map[string]struct{}{}
	queue := openAPIOperationSchemaComponentDependencies(document.Paths)
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		if _, visited := reachable[name]; visited {
			continue
		}
		reachable[name] = struct{}{}
		definition, present := schemas[name]
		if !present {
			continue
		}
		normalized, normalizeErr := normalizeOpenAPIJSONValue(definition)
		if normalizeErr != nil {
			continue
		}
		queue = append(queue, openAPISchemaComponentDependencies(normalized)...)
	}
	for name := range schemas {
		if _, keep := reachable[name]; !keep {
			delete(schemas, name)
		}
	}
	if len(schemas) == 0 {
		delete(document.Components, "schemas")
		if len(document.Components) == 0 {
			document.Components = nil
		}
		return nil
	}
	return sortedMapKeys(schemas)
}

// openAPIOperationSchemaComponentDependencies collects component references only from schema-bearing operation fields, never from example payloads.
func openAPIOperationSchemaComponentDependencies(paths map[string]map[string]OpenAPIOp) []string {
	dependencies := map[string]struct{}{}
	appendSchema := func(value any) {
		normalized, err := normalizeOpenAPIJSONValue(value)
		if err != nil {
			return
		}
		for _, name := range openAPISchemaComponentDependencies(normalized) {
			dependencies[name] = struct{}{}
		}
	}
	for _, path := range sortedMapKeys(paths) {
		for _, method := range sortedMapKeys(paths[path]) {
			operation := paths[path][method]
			for _, parameter := range operation.Parameters {
				appendSchema(parameter.Schema)
			}
			visitOpenAPIContentSchemas(operation.RequestBody["content"], appendSchema)
			for _, status := range sortedMapKeys(operation.Responses) {
				visitOpenAPIContentSchemas(operation.Responses[status]["content"], appendSchema)
			}
		}
	}
	return sortedMapKeys(dependencies)
}

// visitOpenAPIContentSchemas visits only Schema Objects inside an OpenAPI Content Object.
func visitOpenAPIContentSchemas(raw any, visit func(any)) {
	content, ok := raw.(map[string]any)
	if !ok {
		return
	}
	for _, mediaType := range sortedMapKeys(content) {
		media, ok := content[mediaType].(map[string]any)
		if !ok {
			continue
		}
		if schema, present := media["schema"]; present {
			visit(schema)
		}
	}
}

// pruneOpenAPISecuritySchemes removes definitions that no projected operation references after overrides and route omission.
func pruneOpenAPISecuritySchemes(document *OpenAPIDocument) {
	if document == nil || document.Components == nil {
		return
	}
	rawSchemes, exists := document.Components["securitySchemes"]
	if !exists {
		return
	}
	schemes, ok := rawSchemes.(map[string]OpenAPISecurityScheme)
	if !ok {
		return
	}
	used := map[string]struct{}{}
	for _, methods := range document.Paths {
		for _, operation := range methods {
			if operation.Security == nil {
				continue
			}
			for _, requirement := range *operation.Security {
				for name := range requirement {
					used[name] = struct{}{}
				}
			}
		}
	}
	for name := range schemes {
		if _, keep := used[name]; !keep {
			delete(schemes, name)
		}
	}
	if len(schemes) == 0 {
		delete(document.Components, "securitySchemes")
		if len(document.Components) == 0 {
			document.Components = nil
		}
	}
}

// openAPISchemaComponentDependencies collects component refs and discriminator mappings from one normalized Schema Object.
func openAPISchemaComponentDependencies(value any) []string {
	dependencies := map[string]struct{}{}
	var visit func(map[string]any)
	visit = func(schema map[string]any) {
		if reference, ok := schema["$ref"].(string); ok {
			if name, local := openAPISchemaComponentName(reference); local {
				dependencies[name] = struct{}{}
			}
		}
		if discriminator, ok := schema["discriminator"].(map[string]any); ok {
			if mapping, ok := discriminator["mapping"].(map[string]any); ok {
				for _, rawTarget := range mapping {
					target, ok := rawTarget.(string)
					if !ok {
						continue
					}
					if name, local := openAPISchemaComponentName(target); local {
						dependencies[name] = struct{}{}
					} else if isOpenAPIComponentKey(target) {
						dependencies[target] = struct{}{}
					}
				}
			}
		}
		for _, keyword := range []string{"allOf", "oneOf", "anyOf"} {
			alternatives, _ := schema[keyword].([]any)
			for _, alternative := range alternatives {
				if child, ok := alternative.(map[string]any); ok {
					visit(child)
				}
			}
		}
		for _, keyword := range []string{"not", "items", "additionalProperties"} {
			if child, ok := schema[keyword].(map[string]any); ok {
				visit(child)
			}
		}
		if properties, ok := schema["properties"].(map[string]any); ok {
			for _, name := range sortedMapKeys(properties) {
				if child, ok := properties[name].(map[string]any); ok {
					visit(child)
				}
			}
		}
	}
	if schema, ok := value.(map[string]any); ok {
		visit(schema)
	}
	return sortedMapKeys(dependencies)
}

// openAPISchemaComponentName extracts the first component token from a local schema reference, including nested JSON Pointers.
func openAPISchemaComponentName(reference string) (string, bool) {
	if !strings.HasPrefix(reference, "#/components/schemas/") {
		return "", false
	}
	fragment, err := url.PathUnescape(strings.TrimPrefix(reference, "#"))
	if err != nil {
		return "", false
	}
	tokens := strings.Split(strings.TrimPrefix(fragment, "/"), "/")
	if len(tokens) < 3 || tokens[0] != "components" || tokens[1] != "schemas" {
		return "", false
	}
	name, err := decodeOpenAPIJSONPointerToken(tokens[2])
	if err != nil {
		return "", false
	}
	return name, true
}

// openAPIPathTemplateShape removes parameter names so equivalent templated paths cannot coexist illegally.
func openAPIPathTemplateShape(path string) string {
	var builder strings.Builder
	for index := 0; index < len(path); {
		if path[index] != '{' {
			builder.WriteByte(path[index])
			index++
			continue
		}
		end := strings.IndexByte(path[index+1:], '}')
		if end < 0 {
			builder.WriteString(path[index:])
			break
		}
		builder.WriteString("{}")
		index += end + 2
	}
	return builder.String()
}

// openAPISchemaProblems validates OpenAPI 3.0 Schema Objects recursively after converting typed Go containers to JSON shapes.
func openAPISchemaProblems(context string, value any) []string {
	normalized, err := normalizeOpenAPIJSONValue(value)
	if err != nil {
		return []string{fmt.Sprintf("%s is not JSON-compatible: %v", context, err)}
	}
	schema, ok := normalized.(map[string]any)
	if !ok {
		return []string{context + " must be an OpenAPI schema object"}
	}
	return normalizedOpenAPISchemaProblems(context, schema)
}

// normalizedOpenAPISchemaProblems validates the fixed OpenAPI 3.0 Schema Object keyword set without accepting 3.1-only JSON Schema syntax.
func normalizedOpenAPISchemaProblems(context string, schema map[string]any) []string {
	problems := make([]string, 0)
	allowed := map[string]struct{}{
		"$ref": {}, "title": {}, "multipleOf": {}, "maximum": {}, "exclusiveMaximum": {}, "minimum": {}, "exclusiveMinimum": {},
		"maxLength": {}, "minLength": {}, "pattern": {}, "maxItems": {}, "minItems": {}, "uniqueItems": {}, "maxProperties": {},
		"minProperties": {}, "required": {}, "enum": {}, "type": {}, "allOf": {}, "oneOf": {}, "anyOf": {}, "not": {}, "items": {},
		"properties": {}, "additionalProperties": {}, "description": {}, "format": {}, "default": {}, "nullable": {}, "discriminator": {},
		"readOnly": {}, "writeOnly": {}, "xml": {}, "externalDocs": {}, "example": {}, "deprecated": {},
	}
	for _, key := range sortedMapKeys(schema) {
		if _, known := allowed[key]; known || strings.HasPrefix(key, "x-") {
			continue
		}
		problems = append(problems, fmt.Sprintf("%s uses unsupported OpenAPI 3.0 schema keyword %q", context, key))
	}
	if reference, present := schema["$ref"]; present {
		value, ok := reference.(string)
		if !ok || strings.TrimSpace(value) == "" {
			problems = append(problems, context+" $ref must be a non-empty string")
		}
		for key := range schema {
			if key != "$ref" {
				problems = append(problems, fmt.Sprintf("%s $ref cannot have sibling field %q in OpenAPI 3.0", context, key))
			}
		}
		return problems
	}
	for _, keyword := range []string{"title", "pattern", "description", "format"} {
		if value, present := schema[keyword]; present {
			if text, ok := value.(string); !ok || keyword == "format" && strings.TrimSpace(text) == "" {
				problems = append(problems, fmt.Sprintf("%s %s must be %s", context, keyword, openAPIStringKeywordExpectation(keyword)))
			}
		}
	}
	for _, keyword := range []string{"exclusiveMaximum", "exclusiveMinimum", "uniqueItems", "nullable", "readOnly", "writeOnly", "deprecated"} {
		if value, present := schema[keyword]; present {
			if _, ok := value.(bool); !ok {
				problems = append(problems, fmt.Sprintf("%s %s must be boolean", context, keyword))
			}
		}
	}
	if readOnly, _ := schema["readOnly"].(bool); readOnly {
		if writeOnly, _ := schema["writeOnly"].(bool); writeOnly {
			problems = append(problems, context+" cannot be both readOnly and writeOnly")
		}
	}
	declaredType := ""
	declaredTypeValid := false
	if value, present := schema["type"]; present {
		declaredType, _ = value.(string)
		switch declaredType {
		case "integer", "number", "string", "boolean", "array", "object":
			declaredTypeValid = true
			problems = append(problems, openAPISchemaTypeKeywordProblems(context, schema, declaredType)...)
		default:
			problems = append(problems, fmt.Sprintf("%s type %q is not supported by OpenAPI 3.0", context, declaredType))
		}
	}
	for _, keyword := range []string{"multipleOf", "maximum", "minimum"} {
		if value, present := schema[keyword]; present {
			number, ok := openAPINumber(value)
			if !ok || keyword == "multipleOf" && number <= 0 {
				problems = append(problems, fmt.Sprintf("%s %s must be %s", context, keyword, openAPINumberKeywordExpectation(keyword)))
			}
		}
	}
	for _, keyword := range []string{"maxLength", "minLength", "maxItems", "minItems", "maxProperties", "minProperties"} {
		if value, present := schema[keyword]; present && !isNonnegativeJSONInteger(value) {
			problems = append(problems, fmt.Sprintf("%s %s must be a non-negative integer", context, keyword))
		}
	}
	problems = append(problems, openAPIBoundProblems(context, schema, "minimum", "maximum")...)
	problems = append(problems, openAPIBoundProblems(context, schema, "minLength", "maxLength")...)
	problems = append(problems, openAPIBoundProblems(context, schema, "minItems", "maxItems")...)
	problems = append(problems, openAPIBoundProblems(context, schema, "minProperties", "maxProperties")...)
	if required, present := schema["required"]; present {
		problems = append(problems, openAPIStringArrayProblems(context+" required", required, true)...)
	}
	if enum, present := schema["enum"]; present {
		values, ok := enum.([]any)
		if !ok || len(values) == 0 {
			problems = append(problems, context+" enum must be a non-empty array")
		} else {
			seen := map[string]struct{}{}
			duplicateReported := false
			for index, value := range values {
				if declaredTypeValid && !openAPISchemaValueConformsToType(value, declaredType, openAPISchemaNullable(schema)) {
					problems = append(problems, fmt.Sprintf("%s enum[%d] must conform to schema type %q", context, index, declaredType))
				}
				fingerprint, err := json.Marshal(value)
				if err != nil {
					problems = append(problems, context+" enum contains a non-JSON value")
					continue
				}
				if _, duplicate := seen[string(fingerprint)]; duplicate {
					if !duplicateReported {
						problems = append(problems, context+" enum contains duplicate values")
						duplicateReported = true
					}
					continue
				}
				seen[string(fingerprint)] = struct{}{}
			}
		}
	}
	if declaredTypeValid {
		nullable := openAPISchemaNullable(schema)
		if value, present := schema["default"]; present && !openAPISchemaValueConformsToType(value, declaredType, nullable) {
			problems = append(problems, fmt.Sprintf("%s default must conform to schema type %q", context, declaredType))
		}
		if value, present := schema["example"]; present && !openAPISchemaExampleConformsToType(value, declaredType, nullable) {
			problems = append(problems, fmt.Sprintf("%s example must conform to schema type %q", context, declaredType))
		}
	}
	for _, keyword := range []string{"allOf", "oneOf", "anyOf"} {
		if raw, present := schema[keyword]; present {
			alternatives, ok := raw.([]any)
			if !ok || len(alternatives) == 0 {
				problems = append(problems, fmt.Sprintf("%s %s must be a non-empty schema array", context, keyword))
				continue
			}
			for index, alternative := range alternatives {
				child, ok := alternative.(map[string]any)
				if !ok {
					problems = append(problems, fmt.Sprintf("%s %s[%d] must be a schema object", context, keyword, index))
					continue
				}
				problems = append(problems, normalizedOpenAPISchemaProblems(fmt.Sprintf("%s %s[%d]", context, keyword, index), child)...)
			}
		}
	}
	if raw, present := schema["not"]; present {
		child, ok := raw.(map[string]any)
		if !ok {
			problems = append(problems, context+" not must be a schema object")
		} else {
			problems = append(problems, normalizedOpenAPISchemaProblems(context+" not", child)...)
		}
	}
	if raw, present := schema["items"]; present {
		child, ok := raw.(map[string]any)
		if !ok {
			problems = append(problems, context+" items must be a schema object")
		} else {
			problems = append(problems, normalizedOpenAPISchemaProblems(context+" items", child)...)
		}
	} else if declaredType == "array" {
		problems = append(problems, context+" array schema requires items")
	}
	if raw, present := schema["properties"]; present {
		properties, ok := raw.(map[string]any)
		if !ok {
			problems = append(problems, context+" properties must be an object")
		} else {
			for _, name := range sortedMapKeys(properties) {
				child, ok := properties[name].(map[string]any)
				if !ok {
					problems = append(problems, fmt.Sprintf("%s property %q must be a schema object", context, name))
					continue
				}
				problems = append(problems, normalizedOpenAPISchemaProblems(context+" property "+strconv.Quote(name), child)...)
			}
		}
	}
	if raw, present := schema["additionalProperties"]; present {
		switch value := raw.(type) {
		case bool:
		case map[string]any:
			problems = append(problems, normalizedOpenAPISchemaProblems(context+" additionalProperties", value)...)
		default:
			problems = append(problems, context+" additionalProperties must be boolean or a schema object")
		}
	}
	if raw, present := schema["discriminator"]; present {
		problems = append(problems, openAPIDiscriminatorProblems(context, raw)...)
	}
	for _, keyword := range []string{"xml", "externalDocs"} {
		if raw, present := schema[keyword]; present {
			if _, ok := raw.(map[string]any); !ok {
				problems = append(problems, fmt.Sprintf("%s %s must be an object", context, keyword))
			}
		}
	}
	return problems
}

// openAPISchemaNullable enables null instances only when OpenAPI's explicit nullable flag is valid and true.
func openAPISchemaNullable(schema map[string]any) bool {
	nullable, _ := schema["nullable"].(bool)
	return nullable
}

// openAPISchemaValueConformsToType applies OpenAPI's JSON instance types without losing integer semantics during normalization.
func openAPISchemaValueConformsToType(value any, declaredType string, nullable bool) bool {
	if value == nil {
		return nullable
	}
	switch declaredType {
	case "integer":
		number, ok := value.(json.Number)
		if !ok {
			return false
		}
		parsed, valid := new(big.Rat).SetString(number.String())
		return valid && parsed.IsInt()
	case "number":
		_, ok := value.(json.Number)
		return ok
	case "string":
		_, ok := value.(string)
		return ok
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	case "object":
		_, ok := value.(map[string]any)
		return ok
	default:
		return false
	}
}

// openAPISchemaExampleConformsToType preserves OpenAPI 3.0's string escape hatch for examples that lack a natural JSON or YAML representation.
func openAPISchemaExampleConformsToType(value any, declaredType string, nullable bool) bool {
	if _, stringRepresentation := value.(string); stringRepresentation && declaredType != "string" {
		return true
	}
	return openAPISchemaValueConformsToType(value, declaredType, nullable)
}

// openAPISchemaTypeKeywordProblems rejects structural constraints that cannot apply to an explicitly declared schema type.
func openAPISchemaTypeKeywordProblems(context string, schema map[string]any, declaredType string) []string {
	keywordTypes := map[string][]string{
		"multipleOf":           {"integer", "number"},
		"maximum":              {"integer", "number"},
		"exclusiveMaximum":     {"integer", "number"},
		"minimum":              {"integer", "number"},
		"exclusiveMinimum":     {"integer", "number"},
		"maxLength":            {"string"},
		"minLength":            {"string"},
		"pattern":              {"string"},
		"maxItems":             {"array"},
		"minItems":             {"array"},
		"uniqueItems":          {"array"},
		"items":                {"array"},
		"maxProperties":        {"object"},
		"minProperties":        {"object"},
		"required":             {"object"},
		"properties":           {"object"},
		"additionalProperties": {"object"},
	}
	problems := make([]string, 0)
	for _, keyword := range sortedMapKeys(keywordTypes) {
		if _, present := schema[keyword]; !present {
			continue
		}
		allowedTypes := keywordTypes[keyword]
		compatible := false
		for _, allowedType := range allowedTypes {
			if declaredType == allowedType {
				compatible = true
				break
			}
		}
		if !compatible {
			problems = append(problems, fmt.Sprintf("%s %s cannot constrain schema type %q", context, keyword, declaredType))
		}
	}
	return problems
}

// openAPIStringKeywordExpectation keeps schema diagnostics grammatical without spreading special cases through validation.
func openAPIStringKeywordExpectation(keyword string) string {
	if keyword == "format" {
		return "a non-empty string"
	}
	return "a string"
}

// openAPINumberKeywordExpectation describes the positive constraint unique to multipleOf.
func openAPINumberKeywordExpectation(keyword string) string {
	if keyword == "multipleOf" {
		return "a positive number"
	}
	return "a number"
}

// openAPINumber converts JSON numbers for finite range comparisons.
func openAPINumber(value any) (float64, bool) {
	switch number := value.(type) {
	case json.Number:
		parsed, err := number.Float64()
		return parsed, err == nil
	case float64:
		return number, true
	default:
		return 0, false
	}
}

// isNonnegativeJSONInteger validates integer-valued schema bounds after JSON normalization.
func isNonnegativeJSONInteger(value any) bool {
	number, ok := value.(json.Number)
	if !ok {
		return false
	}
	_, err := strconv.ParseUint(number.String(), 10, 64)
	return err == nil
}

// openAPIBoundProblems validates paired minimum and maximum keywords when both are present.
func openAPIBoundProblems(context string, schema map[string]any, minimumKey, maximumKey string) []string {
	minimumRaw, minimumPresent := schema[minimumKey]
	maximumRaw, maximumPresent := schema[maximumKey]
	if !minimumPresent || !maximumPresent {
		return nil
	}
	minimum, minimumOK := openAPINumber(minimumRaw)
	maximum, maximumOK := openAPINumber(maximumRaw)
	if !minimumOK || !maximumOK || minimum <= maximum {
		return nil
	}
	return []string{fmt.Sprintf("%s %s cannot exceed %s", context, minimumKey, maximumKey)}
}

// openAPIStringArrayProblems validates non-empty, unique string arrays used by required and similar fixed fields.
func openAPIStringArrayProblems(context string, value any, requireNonempty bool) []string {
	items, ok := value.([]any)
	if !ok || requireNonempty && len(items) == 0 {
		return []string{context + " must be a non-empty string array"}
	}
	problems := make([]string, 0)
	seen := map[string]struct{}{}
	for _, item := range items {
		text, ok := item.(string)
		if !ok || strings.TrimSpace(text) == "" {
			problems = append(problems, context+" must contain only non-empty strings")
			continue
		}
		if _, duplicate := seen[text]; duplicate {
			problems = append(problems, fmt.Sprintf("%s repeats %q", context, text))
			continue
		}
		seen[text] = struct{}{}
	}
	return problems
}

// openAPIDiscriminatorProblems validates the required property name and optional string mapping.
func openAPIDiscriminatorProblems(context string, value any) []string {
	discriminator, ok := value.(map[string]any)
	if !ok {
		return []string{context + " discriminator must be an object"}
	}
	problems := make([]string, 0)
	propertyName, ok := discriminator["propertyName"].(string)
	if !ok || strings.TrimSpace(propertyName) == "" {
		problems = append(problems, context+" discriminator requires a non-empty propertyName")
	}
	for _, key := range sortedMapKeys(discriminator) {
		if key == "propertyName" || key == "mapping" || strings.HasPrefix(key, "x-") {
			continue
		}
		problems = append(problems, fmt.Sprintf("%s discriminator has unsupported field %q", context, key))
	}
	if rawMapping, present := discriminator["mapping"]; present {
		mapping, ok := rawMapping.(map[string]any)
		if !ok {
			problems = append(problems, context+" discriminator mapping must be an object")
		} else {
			for _, key := range sortedMapKeys(mapping) {
				if target, ok := mapping[key].(string); !ok || strings.TrimSpace(target) == "" {
					problems = append(problems, fmt.Sprintf("%s discriminator mapping %q must be a non-empty string", context, key))
				}
			}
		}
	}
	return problems
}

// openAPIJSONValueProblems reports values that cannot be serialized into an OpenAPI JSON document.
func openAPIJSONValueProblems(context string, value any) []string {
	if _, err := normalizeOpenAPIJSONValue(value); err != nil {
		return []string{fmt.Sprintf("%s is not JSON-compatible: %v", context, err)}
	}
	return nil
}

// openAPILocalReferenceProblems resolves every local Reference Object as an RFC JSON Pointer against the final document.
func openAPILocalReferenceProblems(document OpenAPIDocument) []string {
	normalized, err := normalizeOpenAPIJSONValue(document)
	if err != nil {
		return []string{fmt.Sprintf("OpenAPI document is not JSON-compatible: %v", err)}
	}
	root, ok := normalized.(map[string]any)
	if !ok {
		return []string{"OpenAPI document must serialize as an object"}
	}
	references := openAPIDocumentSchemaReferenceLocations(document)
	refs := sortedMapKeys(references)
	problems := make([]string, 0)
	for _, reference := range refs {
		if !strings.HasPrefix(reference, "#") {
			continue
		}
		target, resolveErr := resolveOpenAPIJSONPointer(root, reference)
		if resolveErr != nil {
			for _, location := range references[reference] {
				problems = append(problems, fmt.Sprintf("%s reference %q cannot be resolved: %v", location, reference, resolveErr))
			}
			continue
		}
		targetSchema, schemaObject := target.(map[string]any)
		if !schemaObject {
			for _, location := range references[reference] {
				problems = append(problems, fmt.Sprintf("%s reference %q does not target an object", location, reference))
			}
			continue
		}
		if targetProblems := normalizedOpenAPISchemaProblems("reference target", targetSchema); len(targetProblems) > 0 {
			for _, location := range references[reference] {
				problems = append(problems, fmt.Sprintf("%s reference %q does not target a valid Schema Object", location, reference))
			}
		}
	}
	return problems
}

// openAPIDocumentSchemaReferenceLocations records references only from schema-bearing fields so example JSON remains opaque data.
func openAPIDocumentSchemaReferenceLocations(document OpenAPIDocument) map[string][]string {
	references := map[string][]string{}
	appendSchema := func(value any, location string) {
		normalized, err := normalizeOpenAPIJSONValue(value)
		if err != nil {
			return
		}
		if schema, ok := normalized.(map[string]any); ok {
			collectOpenAPISchemaReferenceLocations(schema, location, references)
		}
	}
	for _, path := range sortedMapKeys(document.Paths) {
		for _, method := range sortedMapKeys(document.Paths[path]) {
			operation := document.Paths[path][method]
			operationLocation := fmt.Sprintf("$.paths[%q].%s", path, method)
			for index, parameter := range operation.Parameters {
				appendSchema(parameter.Schema, fmt.Sprintf("%s.parameters[%d].schema", operationLocation, index))
			}
			visitOpenAPIContentSchemaLocations(operation.RequestBody["content"], operationLocation+".requestBody.content", appendSchema)
			for _, status := range sortedMapKeys(operation.Responses) {
				visitOpenAPIContentSchemaLocations(operation.Responses[status]["content"], fmt.Sprintf("%s.responses[%q].content", operationLocation, status), appendSchema)
			}
		}
	}
	if schemas, ok := document.Components["schemas"].(map[string]any); ok {
		for _, name := range sortedMapKeys(schemas) {
			appendSchema(schemas[name], fmt.Sprintf("$.components.schemas[%q]", name))
		}
	}
	return references
}

// visitOpenAPIContentSchemaLocations visits Content Object schemas with stable diagnostic locations.
func visitOpenAPIContentSchemaLocations(raw any, location string, visit func(any, string)) {
	content, ok := raw.(map[string]any)
	if !ok {
		return
	}
	for _, mediaType := range sortedMapKeys(content) {
		media, ok := content[mediaType].(map[string]any)
		if !ok {
			continue
		}
		if schema, present := media["schema"]; present {
			visit(schema, fmt.Sprintf("%s[%q].schema", location, mediaType))
		}
	}
}

// collectOpenAPISchemaReferenceLocations walks only recursive Schema Object positions and discriminator mapping references.
func collectOpenAPISchemaReferenceLocations(schema map[string]any, location string, references map[string][]string) {
	if reference, ok := schema["$ref"].(string); ok {
		references[reference] = append(references[reference], location+".$ref")
	}
	if discriminator, ok := schema["discriminator"].(map[string]any); ok {
		if mapping, ok := discriminator["mapping"].(map[string]any); ok {
			for _, name := range sortedMapKeys(mapping) {
				target, ok := mapping[name].(string)
				if ok && strings.HasPrefix(target, "#") {
					references[target] = append(references[target], fmt.Sprintf("%s.discriminator.mapping[%q]", location, name))
				}
			}
		}
	}
	for _, keyword := range []string{"allOf", "oneOf", "anyOf"} {
		alternatives, _ := schema[keyword].([]any)
		for index, alternative := range alternatives {
			if child, ok := alternative.(map[string]any); ok {
				collectOpenAPISchemaReferenceLocations(child, fmt.Sprintf("%s.%s[%d]", location, keyword, index), references)
			}
		}
	}
	for _, keyword := range []string{"not", "items", "additionalProperties"} {
		if child, ok := schema[keyword].(map[string]any); ok {
			collectOpenAPISchemaReferenceLocations(child, location+"."+keyword, references)
		}
	}
	if properties, ok := schema["properties"].(map[string]any); ok {
		for _, name := range sortedMapKeys(properties) {
			if child, ok := properties[name].(map[string]any); ok {
				collectOpenAPISchemaReferenceLocations(child, fmt.Sprintf("%s.properties[%q]", location, name), references)
			}
		}
	}
}

// resolveOpenAPIJSONPointer resolves URI-fragment JSON Pointers, including escaped nested component locations.
func resolveOpenAPIJSONPointer(root map[string]any, reference string) (any, error) {
	if reference == "#" {
		return root, nil
	}
	if !strings.HasPrefix(reference, "#/") {
		return nil, fmt.Errorf("local references must use a #/ JSON Pointer")
	}
	fragment, err := url.PathUnescape(strings.TrimPrefix(reference, "#"))
	if err != nil {
		return nil, fmt.Errorf("invalid URI escaping")
	}
	current := any(root)
	for _, rawToken := range strings.Split(strings.TrimPrefix(fragment, "/"), "/") {
		token, tokenErr := decodeOpenAPIJSONPointerToken(rawToken)
		if tokenErr != nil {
			return nil, tokenErr
		}
		switch container := current.(type) {
		case map[string]any:
			next, exists := container[token]
			if !exists {
				return nil, fmt.Errorf("token %q does not exist", token)
			}
			current = next
		case []any:
			index, parseErr := strconv.Atoi(token)
			if parseErr != nil || index < 0 || index >= len(container) {
				return nil, fmt.Errorf("array index %q does not exist", token)
			}
			current = container[index]
		default:
			return nil, fmt.Errorf("token %q traverses a scalar value", token)
		}
	}
	return current, nil
}

// decodeOpenAPIJSONPointerToken applies RFC 6901 escapes while rejecting malformed tildes.
func decodeOpenAPIJSONPointerToken(value string) (string, error) {
	var builder strings.Builder
	for index := 0; index < len(value); index++ {
		if value[index] != '~' {
			builder.WriteByte(value[index])
			continue
		}
		if index+1 >= len(value) {
			return "", fmt.Errorf("JSON Pointer token %q has a trailing ~", value)
		}
		index++
		switch value[index] {
		case '0':
			builder.WriteByte('~')
		case '1':
			builder.WriteByte('/')
		default:
			return "", fmt.Errorf("JSON Pointer token %q has invalid ~%c escape", value, value[index])
		}
	}
	return builder.String(), nil
}
