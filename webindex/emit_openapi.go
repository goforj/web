package webindex

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// OpenAPIDocument is the OpenAPI 3.0 projection generated from the canonical API index.
type OpenAPIDocument struct {
	OpenAPI    string                          `json:"openapi"`
	Info       map[string]string               `json:"info"`
	Paths      map[string]map[string]OpenAPIOp `json:"paths"`
	Components map[string]any                  `json:"components,omitempty"`
}

// OpenAPIOp is the operation shape emitted for one indexed route.
type OpenAPIOp struct {
	OperationID string                        `json:"operationId"`
	Summary     string                        `json:"summary,omitempty"`
	Description string                        `json:"description,omitempty"`
	Tags        []string                      `json:"tags,omitempty"`
	Parameters  []OpenAPIParameter            `json:"parameters,omitempty"`
	RequestBody map[string]any                `json:"requestBody,omitempty"`
	Responses   map[string]map[string]any     `json:"responses"`
	Security    *[]OpenAPISecurityRequirement `json:"security,omitempty"`
}

// OpenAPIParameter is a projected path, query, header, or cookie parameter.
type OpenAPIParameter struct {
	Name     string         `json:"name"`
	In       string         `json:"in"`
	Required bool           `json:"required,omitempty"`
	Schema   map[string]any `json:"schema,omitempty"`
	Example  any            `json:"example,omitempty"`
}

// ProjectOpenAPI projects a manifest with validated metadata, security, and contract overrides.
func ProjectOpenAPI(manifest Manifest, options OpenAPIOptions) (OpenAPIDocument, error) {
	options = normalizedOpenAPIOptions(options)
	document := newOpenAPIDocument(options.Info)
	problems := validateOpenAPISecuritySchemes(options.SecuritySchemes)

	components, componentProblems := manifestComponentSchemas(manifest.Schemas)
	if len(components) > 0 {
		document.Components = map[string]any{"schemas": components}
	}
	if len(options.SecuritySchemes) > 0 {
		if document.Components == nil {
			document.Components = map[string]any{}
		}
		document.Components["securitySchemes"] = cloneSecuritySchemes(options.SecuritySchemes)
	}

	candidates := openAPIProjectionCandidates(manifest.Operations)
	projectedOperations := make([]Operation, 0, len(candidates))
	for _, candidate := range candidates {
		projectedOperations = append(projectedOperations, candidate.Operation)
	}
	scopedMiddlewareSecurity, middlewareSecurityProblems := resolveMiddlewareSecurity(projectedOperations, options)
	problems = append(problems, middlewareSecurityProblems...)
	overrides, overrideProblems := resolveOpenAPIOperationOverrides(projectedOperations, options.Operations)
	problems = append(problems, overrideProblems...)
	operationIDs := semanticOperationIDs(projectedOperations)
	for index, candidate := range candidates {
		operation := candidate.Operation
		method := candidate.Method
		path := candidate.Path
		if document.Paths[path] == nil {
			document.Paths[path] = map[string]OpenAPIOp{}
		}
		if _, exists := document.Paths[path][method]; exists {
			problems = append(problems, fmt.Sprintf("multiple operations project to %s %s", strings.ToUpper(method), path))
			continue
		}

		projected := OpenAPIOp{
			OperationID: operationIDs[index],
			Parameters:  toOpenAPIParameters(operation.Inputs),
			RequestBody: toOpenAPIRequestBody(operation.Inputs),
			Responses:   toOpenAPIResponses(operation.Outputs),
			Security:    securityForOperationMiddlewares(operation, options.MiddlewareSecurity, scopedMiddlewareSecurity[index]),
		}
		if operation.Metadata != nil {
			projected.Summary = operation.Metadata.Summary
			projected.Description = operation.Metadata.Description
			projected.Tags = cleanOpenAPITags(operation.Metadata.Tags)
			if operation.Metadata.Security != nil {
				requirements := cloneSecurityRequirements(operation.Metadata.Security.Requirements)
				if emptySecurityRequirementIndex(requirements) >= 0 {
					problems = append(problems, fmt.Sprintf("operation %s %s metadata security cannot contain an empty requirement object", strings.ToUpper(method), path))
				} else {
					projected.Security = &requirements
				}
			}
		}
		if override, exists := overrides[index]; exists {
			var applyProblems []string
			projected, applyProblems = applyOpenAPIOperationOverride(projected, override)
			for _, problem := range applyProblems {
				problems = append(problems, fmt.Sprintf("override for %s %s: %s", strings.ToUpper(method), path, problem))
			}
		}
		if projected.Security != nil {
			problems = append(problems, validateOpenAPISecurityRequirements("operation "+strings.ToUpper(method)+" "+path, *projected.Security, options.SecuritySchemes)...)
		}
		problems = append(problems, openAPIProjectedOperationProblems(path, method, projected)...)
		document.Paths[path][method] = projected
	}

	reachableComponents := pruneOpenAPIComponentSchemas(&document)
	pruneOpenAPISecuritySchemes(&document)
	for _, name := range reachableComponents {
		problems = append(problems, componentProblems[name]...)
	}
	problems = append(problems, openAPIDocumentProblems(document)...)
	if len(problems) > 0 {
		sort.Strings(problems)
		return document, &OpenAPIProjectionError{Problems: dedupeStrings(problems)}
	}
	return document, nil
}

// isOpenAPIMethod limits Path Item keys to the operations defined by OpenAPI 3.0.
func isOpenAPIMethod(method string) bool {
	switch method {
	case "get", "put", "post", "delete", "options", "head", "patch", "trace":
		return true
	default:
		return false
	}
}

// toOpenAPI preserves the original projection helper for callers that do not need explicit options.
func toOpenAPI(manifest Manifest) OpenAPIDocument {
	document, _ := ProjectOpenAPI(manifest, OpenAPIOptions{})
	return document
}

// toOpenAPIWithTitle preserves the original title helper while using the validated projection path.
func toOpenAPIWithTitle(manifest Manifest, title string) OpenAPIDocument {
	document, _ := ProjectOpenAPI(manifest, OpenAPIOptions{Info: OpenAPIInfoOptions{Title: title}})
	return document
}

// newOpenAPIDocument applies stable defaults while omitting empty optional info fields.
func newOpenAPIDocument(info OpenAPIInfoOptions) OpenAPIDocument {
	values := map[string]string{
		"title":   info.Title,
		"version": info.Version,
	}
	if strings.TrimSpace(info.Description) != "" {
		values["description"] = info.Description
	}
	return OpenAPIDocument{
		OpenAPI: "3.0.3",
		Info:    values,
		Paths:   map[string]map[string]OpenAPIOp{},
	}
}

// manifestComponentSchemas seeds components only from canonical named contracts in Manifest v2.
func manifestComponentSchemas(schemas []Schema) (map[string]any, map[string][]string) {
	components := map[string]any{}
	owners := map[string]string{}
	problems := map[string][]string{}
	for _, schema := range schemas {
		name := strings.TrimSpace(schema.Name)
		if !isCodegenSafeIdentifier(name) {
			problems[name] = append(problems[name], fmt.Sprintf("schema %q does not have a codegen-safe component name", name))
		}
		if owner, exists := owners[name]; exists {
			problems[name] = append(problems[name], fmt.Sprintf("component name %q is shared by schema identities %q and %q", name, owner, schema.Identity))
			continue
		}
		owners[name] = schema.Identity
		components[name] = cleanOpenAPISchema(schema.Definition)
	}
	if len(components) == 0 {
		return nil, problems
	}
	return components, problems
}

// cloneSecuritySchemes detaches caller configuration before it becomes part of the document.
func cloneSecuritySchemes(schemes map[string]OpenAPISecurityScheme) map[string]OpenAPISecurityScheme {
	cloned := make(map[string]OpenAPISecurityScheme, len(schemes))
	for name, scheme := range schemes {
		if scheme.Flows != nil {
			if flows, err := normalizeOpenAPIJSONValue(scheme.Flows); err == nil {
				scheme.Flows = flows
			}
		}
		cloned[name] = scheme
	}
	return cloned
}

// openAPIResponseDescription uses standard HTTP semantics while keeping unresolved statuses explicit.
func openAPIResponseDescription(statusCode int) string {
	if statusCode <= 0 {
		return "Response status not statically resolved"
	}
	if description := http.StatusText(statusCode); description != "" {
		return description
	}
	return fmt.Sprintf("HTTP %d response", statusCode)
}

// toOpenAPIResponses projects only observed responses and uses default when a status cannot be resolved.
func toOpenAPIResponses(outputs OutputShape) map[string]map[string]any {
	responses := map[string]map[string]any{}
	if len(outputs.Responses) == 0 {
		responses["default"] = map[string]any{"description": "Response not statically resolved"}
		return responses
	}
	sorted := append([]ResponseShape(nil), outputs.Responses...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].StatusCode == sorted[j].StatusCode {
			if sorted[i].ContentType == sorted[j].ContentType {
				return schemaFingerprintForProjection(sorted[i].Schema) < schemaFingerprintForProjection(sorted[j].Schema)
			}
			return sorted[i].ContentType < sorted[j].ContentType
		}
		return sorted[i].StatusCode < sorted[j].StatusCode
	})
	for _, response := range sorted {
		code := "default"
		if response.StatusCode >= 100 && response.StatusCode <= 599 {
			code = strconv.Itoa(response.StatusCode)
		}
		projected := map[string]any{"description": openAPIResponseDescription(response.StatusCode)}
		if contentType, schema := responseContent(response); schema != nil && (response.StatusCode == 0 || httpStatusAllowsContent(response.StatusCode)) {
			if normalized, valid := canonicalOpenAPIMediaType(contentType); valid {
				projected["content"] = map[string]any{
					normalized: map[string]any{"schema": cleanOpenAPISchema(schema)},
				}
			}
		}
		if existing, exists := responses[code]; exists {
			responses[code] = mergeOpenAPIResponse(existing, projected)
		} else {
			responses[code] = projected
		}
	}
	return responses
}

// responseContent maps native and compatibility response methods to honest media shapes.
func responseContent(response ResponseShape) (string, any) {
	if response.Schema != nil {
		contentType := response.ContentType
		if contentType == "" {
			contentType = "application/json"
		}
		return contentType, schemaObjectOrUnknown(response.Schema)
	}
	if response.TypeName != "" {
		contentType := response.ContentType
		if contentType == "" {
			contentType = "application/json"
		}
		return contentType, map[string]any{}
	}
	if response.ContentType != "" {
		return response.ContentType, map[string]any{"type": "string", "format": "binary"}
	}
	switch response.Source {
	case "web.Text", "echo.String":
		return "text/plain", map[string]any{"type": "string"}
	case "web.HTML", "echo.HTML":
		return "text/html", map[string]any{"type": "string"}
	case "echo.XML":
		return "application/xml", map[string]any{}
	case "web.File", "echo.File":
		return "application/octet-stream", map[string]any{"type": "string", "format": "binary"}
	case "web.Blob", "echo.Blob":
		return "", nil
	default:
		return "", nil
	}
}

// toOpenAPIRequestBody projects typed schema evidence without manufacturing a named component.
func toOpenAPIRequestBody(inputs InputShape) map[string]any {
	if inputs.Body == nil {
		return nil
	}
	var schema any
	if inputs.Body.Schema != nil {
		schema = schemaObjectOrUnknown(inputs.Body.Schema)
	} else {
		if inputs.Body.TypeName == "" {
			return nil
		}
		schema = map[string]any{}
	}
	content := map[string]any{
		"*/*": map[string]any{"schema": map[string]any{}},
	}
	if typedSchema, ok := schema.(map[string]any); !ok || len(typedSchema) > 0 {
		content["application/json"] = map[string]any{"schema": schema}
	}
	return map[string]any{"content": content}
}

// mergeOpenAPIResponse retains every media shape observed for one status.
func mergeOpenAPIResponse(existing, incoming map[string]any) map[string]any {
	out := map[string]any{"description": "Response"}
	if description, ok := existing["description"].(string); ok && description != "" {
		out["description"] = description
	}
	if description, ok := incoming["description"].(string); ok && description != "" {
		out["description"] = description
	}

	content := map[string]any{}
	if existingContent, ok := existing["content"].(map[string]any); ok {
		for contentType, body := range existingContent {
			content[contentType] = body
		}
	}
	if incomingContent, ok := incoming["content"].(map[string]any); ok {
		for contentType, value := range incomingContent {
			newBody, _ := value.(map[string]any)
			if oldValue, exists := content[contentType]; exists {
				oldBody, _ := oldValue.(map[string]any)
				content[contentType] = mergeOpenAPIContentBody(oldBody, newBody)
			} else {
				content[contentType] = newBody
			}
		}
	}
	if len(content) > 0 {
		out["content"] = content
	}
	return out
}

// mergeOpenAPIContentBody uses anyOf because inferred alternatives are not proven mutually exclusive.
func mergeOpenAPIContentBody(existing, incoming map[string]any) map[string]any {
	if len(existing) == 0 {
		return incoming
	}
	if len(incoming) == 0 {
		return existing
	}
	oldSchema, oldOK := existing["schema"]
	newSchema, newOK := incoming["schema"]
	if !oldOK {
		return incoming
	}
	if !newOK {
		return existing
	}
	if schemasEquivalent(oldSchema, newSchema) {
		return existing
	}

	anyOf := make([]any, 0, 2)
	anyOf = appendOpenAPIAnyOfAlternatives(anyOf, oldSchema)
	anyOf = appendOpenAPIAnyOfAlternatives(anyOf, newSchema)
	return map[string]any{"schema": map[string]any{"anyOf": dedupeSchemas(anyOf)}}
}

// appendOpenAPIAnyOfAlternatives flattens pure union wrappers while preserving schemas whose sibling keywords constrain the union.
func appendOpenAPIAnyOfAlternatives(alternatives []any, schema any) []any {
	object, ok := schema.(map[string]any)
	if !ok || len(object) != 1 {
		return append(alternatives, schema)
	}
	anyOf, ok := object["anyOf"].([]any)
	if !ok {
		return append(alternatives, schema)
	}
	return append(alternatives, anyOf...)
}

// schemasEquivalent compares projected schemas without using equality as component identity.
func schemasEquivalent(left, right any) bool {
	leftMap, leftOK := left.(map[string]any)
	rightMap, rightOK := right.(map[string]any)
	if !leftOK || !rightOK {
		return false
	}
	return schemaFingerprintForProjection(leftMap) == schemaFingerprintForProjection(rightMap)
}

// dedupeSchemas removes duplicate alternatives inside oneOf while retaining their first observed order.
func dedupeSchemas(items []any) []any {
	seen := map[string]struct{}{}
	out := make([]any, 0, len(items))
	for _, item := range items {
		object, ok := item.(map[string]any)
		if !ok {
			out = append(out, item)
			continue
		}
		fingerprint := schemaFingerprintForProjection(object)
		if fingerprint == "" {
			out = append(out, item)
			continue
		}
		if _, exists := seen[fingerprint]; exists {
			continue
		}
		seen[fingerprint] = struct{}{}
		out = append(out, item)
	}
	return out
}

// schemaFingerprintForProjection provides deterministic comparison only within one operation response.
func schemaFingerprintForProjection(schema any) string {
	data, err := json.Marshal(schema)
	if err != nil {
		return ""
	}
	return string(data)
}

// toOpenAPIPath converts router parameters into OpenAPI path-template parameters.
func toOpenAPIPath(path string) string {
	parts := strings.Split(path, "/")
	for index, part := range parts {
		if strings.HasPrefix(part, ":") && len(part) > 1 {
			parts[index] = "{" + strings.TrimPrefix(part, ":") + "}"
			continue
		}
		if strings.HasPrefix(part, "*") && len(part) > 1 {
			parts[index] = "{" + strings.TrimPrefix(part, "*") + "}"
		}
	}
	return strings.Join(parts, "/")
}

// toOpenAPIParameters projects observed parameter names and keeps only path parameters implicitly required.
func toOpenAPIParameters(inputs InputShape) []OpenAPIParameter {
	out := make([]OpenAPIParameter, 0, len(inputs.PathParams)+len(inputs.QueryParams)+len(inputs.Headers)+len(inputs.Cookies))
	seen := map[string]struct{}{}
	appendParameters := func(in string, parameters []Parameter, forceRequired bool) {
		for _, parameter := range parameters {
			identityName := parameter.Name
			if in == "header" {
				if !isHTTPToken(parameter.Name) {
					continue
				}
				identityName = strings.ToLower(identityName)
				if isReservedOpenAPIHeader(identityName) {
					continue
				}
			}
			key := in + "|" + identityName
			if parameter.Name == "" {
				continue
			}
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			required := parameter.Required || forceRequired
			out = append(out, OpenAPIParameter{
				Name:     parameter.Name,
				In:       in,
				Required: required,
				Schema:   map[string]any{"type": "string"},
			})
		}
	}
	appendParameters("path", inputs.PathParams, true)
	appendParameters("query", inputs.QueryParams, false)
	appendParameters("header", inputs.Headers, false)
	appendParameters("cookie", inputs.Cookies, false)
	return out
}

// cleanOpenAPISchema copies schema values while removing legacy Go-syntax projection hints.
func cleanOpenAPISchema(value any) any {
	switch current := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(current))
		for key, child := range current {
			if key == "x-forj-type" {
				continue
			}
			out[key] = cleanOpenAPISchema(child)
		}
		return out
	case []any:
		out := make([]any, len(current))
		for index, child := range current {
			out[index] = cleanOpenAPISchema(child)
		}
		return out
	case []string:
		return append([]string(nil), current...)
	default:
		return current
	}
}

// schemaObjectOrUnknown preserves valid schema maps and degrades malformed analysis evidence to an unconstrained schema.
func schemaObjectOrUnknown(value any) map[string]any {
	cleaned, ok := cleanOpenAPISchema(value).(map[string]any)
	if !ok {
		return map[string]any{}
	}
	return cleaned
}

// semanticOperationIDs creates readable IDs from handler symbols and resolves collisions independently of traversal order.
func semanticOperationIDs(operations []Operation) map[int]string {
	groups := map[string][]int{}
	for index, operation := range operations {
		base := semanticOperationBase(operation)
		groups[base] = append(groups[base], index)
	}

	ids := make(map[int]string, len(operations))
	for base, indexes := range groups {
		if len(indexes) == 1 {
			ids[indexes[0]] = base
			continue
		}
		roles := map[string][]int{}
		for _, index := range indexes {
			role := collisionRole(operations[index])
			roles[role] = append(roles[role], index)
		}
		for role, roleIndexes := range roles {
			candidate := base + role
			if len(roleIndexes) == 1 {
				ids[roleIndexes[0]] = candidate
				continue
			}
			for _, index := range roleIndexes {
				identity := semanticOperationIdentity(operations[index])
				digest := sha256.Sum256([]byte(identity))
				ids[index] = candidate + "_" + fmt.Sprintf("%x", digest[:4])
			}
		}
	}
	collisions := map[string][]int{}
	for index, identifier := range ids {
		collisions[identifier] = append(collisions[identifier], index)
	}
	for identifier, indexes := range collisions {
		if len(indexes) < 2 {
			continue
		}
		for _, index := range indexes {
			digest := sha256.Sum256([]byte(semanticOperationIdentity(operations[index])))
			ids[index] = identifier + "_" + fmt.Sprintf("%x", digest[:4])
		}
	}
	return ids
}

// semanticOperationBase favors handler symbols because route changes should not rename generated client methods unnecessarily.
func semanticOperationBase(operation Operation) string {
	packageName := operation.Handler.Package
	if packageName == "" && operation.Handler.ImportPath != "" {
		parts := strings.Split(strings.Trim(operation.Handler.ImportPath, "/"), "/")
		packageName = parts[len(parts)-1]
	}
	parts := []string{packageName, strings.TrimPrefix(operation.Handler.Receiver, "*"), operation.Handler.Function}
	base := lowerCamelIdentifier(parts...)
	if base != "" {
		return base
	}
	fallback := lowerCamelIdentifier("operation", strings.ToLower(operation.Method), openAPIPathRole(operation.Path))
	if fallback == "" {
		return "operation"
	}
	return fallback
}

// collisionRole adds readable route meaning only when one handler-derived base is reused.
func collisionRole(operation Operation) string {
	role := upperCamelIdentifier("at", openAPIPathRole(operation.Path), strings.ToLower(operation.Method))
	if role == "" {
		return "AtRoute"
	}
	return role
}

// semanticOperationIdentity includes import identity before the hash fallback so same-named packages remain isolated.
func semanticOperationIdentity(operation Operation) string {
	return strings.Join([]string{
		operation.Handler.ImportPath,
		operation.Handler.Package,
		strings.TrimPrefix(operation.Handler.Receiver, "*"),
		operation.Handler.Function,
		strings.ToUpper(operation.Method),
		toOpenAPIPath(operation.Path),
	}, "|")
}

// openAPIPathRole turns static and parameter path segments into a readable identifier fragment.
func openAPIPathRole(path string) string {
	parts := strings.Split(strings.Trim(toOpenAPIPath(path), "/"), "/")
	roleParts := make([]string, 0, len(parts)*2)
	for _, part := range parts {
		if strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}") {
			roleParts = append(roleParts, "by", strings.TrimSuffix(strings.TrimPrefix(part, "{"), "}"))
			continue
		}
		roleParts = append(roleParts, part)
	}
	return upperCamelIdentifier(roleParts...)
}

// lowerCamelIdentifier produces a portable operation identifier from semantic tokens.
func lowerCamelIdentifier(values ...string) string {
	identifier := upperCamelIdentifier(values...)
	if identifier == "" {
		return ""
	}
	runes := []rune(identifier)
	runes[0] = unicode.ToLower(runes[0])
	if unicode.IsDigit(runes[0]) {
		return "operation" + identifier
	}
	return string(runes)
}

// upperCamelIdentifier removes Go and route punctuation without leaking raw syntax into generated names.
func upperCamelIdentifier(values ...string) string {
	parts := make([]string, 0)
	for _, value := range values {
		var current []rune
		flush := func() {
			if len(current) == 0 {
				return
			}
			current[0] = unicode.ToUpper(current[0])
			parts = append(parts, string(current))
			current = nil
		}
		for _, character := range value {
			if isASCIIIdentifierCharacter(character) {
				current = append(current, character)
				continue
			}
			flush()
		}
		flush()
	}
	return strings.Join(parts, "")
}

// isCodegenSafeIdentifier enforces the conservative identifier subset shared by common client generators.
func isCodegenSafeIdentifier(value string) bool {
	if value == "" {
		return false
	}
	for index, character := range value {
		if character == '_' || character >= 'A' && character <= 'Z' || character >= 'a' && character <= 'z' || index > 0 && character >= '0' && character <= '9' {
			continue
		}
		return false
	}
	return true
}

// isASCIIIdentifierCharacter excludes Unicode letters that are legal Go identifiers but unsupported by many client generators.
func isASCIIIdentifierCharacter(character rune) bool {
	return character >= 'A' && character <= 'Z' || character >= 'a' && character <= 'z' || character >= '0' && character <= '9'
}
