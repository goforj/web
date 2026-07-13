package webindex

import (
	"encoding/json"
	"fmt"
	"go/token"
	"path"
	"sort"
	"strconv"
	"strings"
)

// resolvedMiddlewareSecurity indexes one validated scoped policy by projected operation and middleware occurrence.
type resolvedMiddlewareSecurity map[int]map[int][]OpenAPISecurityRequirement

// resolveMiddlewareSecurity checks global compatibility mappings and resolves source-scoped rules without relying on expression spelling alone.
func resolveMiddlewareSecurity(operations []Operation, options OpenAPIOptions) (resolvedMiddlewareSecurity, []string) {
	problems := make([]string, 0)
	resolved := resolvedMiddlewareSecurity{}
	usedMiddleware := map[string]struct{}{}
	for _, operation := range operations {
		for _, middleware := range operation.Middleware {
			usedMiddleware[middleware] = struct{}{}
		}
	}
	for _, middleware := range sortedMapKeys(options.MiddlewareSecurity) {
		if strings.TrimSpace(middleware) == "" {
			problems = append(problems, "security mapping middleware expression cannot be empty")
			continue
		}
		if _, exists := usedMiddleware[middleware]; !exists {
			problems = append(problems, fmt.Sprintf("security mapping references middleware %q that is not used by an indexed operation", middleware))
		}
		requirements := options.MiddlewareSecurity[middleware]
		if len(requirements) == 0 {
			problems = append(problems, fmt.Sprintf("middleware %q security policy cannot be empty; omit the mapping when it does not enforce security", middleware))
			continue
		}
		problems = append(problems, validateOpenAPISecurityRequirements("middleware "+strconv.Quote(middleware), requirements, options.SecuritySchemes)...)
	}

	for ruleIndex, rule := range options.MiddlewareSecurityRules {
		expression := strings.TrimSpace(rule.Expression)
		function := strings.TrimSpace(rule.Function)
		receiver := strings.TrimPrefix(strings.TrimSpace(rule.Receiver), "*")
		sourceFile, sourceFileValid := normalizedMiddlewareSecuritySourceFile(rule.SourceFile)
		context := fmt.Sprintf("middleware security rule %d", ruleIndex+1)
		selectorValid := true
		if expression == "" {
			problems = append(problems, context+" expression cannot be empty")
			selectorValid = false
		}
		if !sourceFileValid {
			problems = append(problems, context+" source file must be a project-relative Go source path")
			selectorValid = false
		}
		if function == "" {
			problems = append(problems, context+" function cannot be empty")
			selectorValid = false
		}
		if receiver != "" && !token.IsIdentifier(receiver) {
			problems = append(problems, context+" receiver must be a Go-style identifier when provided")
			selectorValid = false
		}
		if len(rule.Requirements) == 0 {
			problems = append(problems, context+" security policy cannot be empty")
		} else {
			problems = append(problems, validateOpenAPISecurityRequirements(context, rule.Requirements, options.SecuritySchemes)...)
		}
		if !selectorValid {
			continue
		}
		if _, global := options.MiddlewareSecurity[expression]; global {
			problems = append(problems, fmt.Sprintf("%s overlaps global security mapping for middleware %q", context, expression))
		}

		type match struct {
			operationIndex  int
			middlewareIndex int
		}
		matches := make([]match, 0)
		declarations := map[string]struct{}{}
		for operationIndex, operation := range operations {
			for middlewareIndex, provenance := range operation.middlewareProvenance {
				if middlewareIndex >= len(operation.Middleware) || operation.Middleware[middlewareIndex] != provenance.Expression {
					continue
				}
				provenanceFile, valid := normalizedMiddlewareSecuritySourceFile(provenance.File)
				provenanceReceiver := strings.TrimPrefix(strings.TrimSpace(provenance.Receiver), "*")
				if !valid || provenance.Expression != expression || provenanceFile != sourceFile || provenance.Function != function || receiver != "" && provenanceReceiver != receiver {
					continue
				}
				declaration := provenanceFile + "|" + provenance.Function + "|" + provenance.Receiver
				declarations[declaration] = struct{}{}
				matches = append(matches, match{operationIndex: operationIndex, middlewareIndex: middlewareIndex})
			}
		}
		selectorLocation := fmt.Sprintf("%s in %s", sourceFile, function)
		if receiver != "" {
			selectorLocation += " on receiver " + strconv.Quote(receiver)
		}
		if len(matches) == 0 {
			problems = append(problems, fmt.Sprintf("%s did not match middleware %q at %s", context, expression, selectorLocation))
			continue
		}
		if len(declarations) > 1 {
			problems = append(problems, fmt.Sprintf("%s matched %d enclosing declarations for middleware %q at %s", context, len(declarations), expression, selectorLocation))
		}
		for _, matched := range matches {
			if resolved[matched.operationIndex] == nil {
				resolved[matched.operationIndex] = map[int][]OpenAPISecurityRequirement{}
			}
			if _, duplicate := resolved[matched.operationIndex][matched.middlewareIndex]; duplicate {
				problems = append(problems, fmt.Sprintf("%s overlaps another scoped security rule for middleware %q at %s", context, expression, selectorLocation))
				continue
			}
			resolved[matched.operationIndex][matched.middlewareIndex] = cloneSecurityRequirements(rule.Requirements)
		}
	}
	return resolved, problems
}

// normalizedMiddlewareSecuritySourceFile canonicalizes slash-separated project paths while rejecting absolute or parent-escaping selectors.
func normalizedMiddlewareSecuritySourceFile(sourceFile string) (string, bool) {
	value := strings.ReplaceAll(strings.TrimSpace(sourceFile), "\\", "/")
	if value == "" || path.IsAbs(value) || len(value) >= 3 && value[1] == ':' && value[2] == '/' {
		return "", false
	}
	cleaned := path.Clean(value)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", false
	}
	return cleaned, true
}

// resolveOpenAPIOperationOverrides requires every selector to resolve once so broad or stale configuration is visible.
func resolveOpenAPIOperationOverrides(operations []Operation, overrides []OpenAPIOperationOverride) (map[int]OpenAPIOperationOverride, []string) {
	resolved := map[int]OpenAPIOperationOverride{}
	problems := make([]string, 0)
	for _, override := range overrides {
		if openAPIOperationSelectorEmpty(override.Match) {
			problems = append(problems, "empty OpenAPI operation selector is not allowed")
			continue
		}
		matches := make([]int, 0)
		for index, operation := range operations {
			if openAPIOperationSelectorMatches(override.Match, operation) {
				matches = append(matches, index)
			}
		}
		selector := describeOpenAPIOperationSelector(override.Match)
		switch len(matches) {
		case 0:
			problems = append(problems, selector+" did not match an indexed operation")
		case 1:
			index := matches[0]
			if _, exists := resolved[index]; exists {
				problems = append(problems, selector+" targets an operation that already has an override")
				continue
			}
			resolved[index] = override
		default:
			problems = append(problems, fmt.Sprintf("%s matched %d operations; add import path, receiver, method, or path", selector, len(matches)))
		}
	}
	return resolved, problems
}

// openAPIOperationSelectorEmpty prevents a one-operation manifest from making an accidental global override look precise.
func openAPIOperationSelectorEmpty(selector OpenAPIOperationSelector) bool {
	return strings.TrimSpace(selector.ImportPath) == "" &&
		strings.TrimSpace(selector.Package) == "" &&
		strings.TrimSpace(selector.Receiver) == "" &&
		strings.TrimSpace(selector.Function) == "" &&
		strings.TrimSpace(selector.Method) == "" &&
		strings.TrimSpace(selector.Path) == ""
}

// openAPIOperationSelectorMatches compares semantic handler identity and optional route disambiguators.
func openAPIOperationSelectorMatches(selector OpenAPIOperationSelector, operation Operation) bool {
	if selector.ImportPath != "" && selector.ImportPath != operation.Handler.ImportPath {
		return false
	}
	if selector.Package != "" && selector.Package != operation.Handler.Package {
		return false
	}
	if selector.Receiver != "" && strings.TrimPrefix(selector.Receiver, "*") != strings.TrimPrefix(operation.Handler.Receiver, "*") {
		return false
	}
	if selector.Function != "" && selector.Function != operation.Handler.Function {
		return false
	}
	if selector.Method != "" && !strings.EqualFold(selector.Method, operation.Method) {
		return false
	}
	if selector.Path != "" && toOpenAPIPath(selector.Path) != toOpenAPIPath(operation.Path) {
		return false
	}
	return true
}

// describeOpenAPIOperationSelector renders configuration evidence without exposing machine-specific source paths.
func describeOpenAPIOperationSelector(selector OpenAPIOperationSelector) string {
	parts := make([]string, 0, 6)
	if selector.ImportPath != "" {
		parts = append(parts, "import="+strconv.Quote(selector.ImportPath))
	}
	if selector.Package != "" {
		parts = append(parts, "package="+strconv.Quote(selector.Package))
	}
	if selector.Receiver != "" {
		parts = append(parts, "receiver="+strconv.Quote(selector.Receiver))
	}
	if selector.Function != "" {
		parts = append(parts, "function="+strconv.Quote(selector.Function))
	}
	if selector.Method != "" {
		parts = append(parts, "method="+strconv.Quote(strings.ToUpper(selector.Method)))
	}
	if selector.Path != "" {
		parts = append(parts, "path="+strconv.Quote(toOpenAPIPath(selector.Path)))
	}
	if len(parts) == 0 {
		return "empty OpenAPI operation selector"
	}
	return "OpenAPI operation selector (" + strings.Join(parts, ", ") + ")"
}

// applyOpenAPIOperationOverride applies only explicit fields and reports references to nonexistent inferred inputs.
func applyOpenAPIOperationOverride(operation OpenAPIOp, override OpenAPIOperationOverride) (OpenAPIOp, []string) {
	problems := make([]string, 0)
	if override.Summary != "" {
		operation.Summary = override.Summary
	}
	if override.Description != "" {
		operation.Description = override.Description
	}
	if len(override.Tags) > 0 {
		operation.Tags = cleanOpenAPITags(override.Tags)
	}
	for _, parameterOverride := range override.Parameters {
		matched := false
		for index := range operation.Parameters {
			parameter := &operation.Parameters[index]
			if parameter.In != parameterOverride.In || parameter.Name != parameterOverride.Name {
				continue
			}
			matched = true
			if parameterOverride.Required != nil {
				if parameter.In == "path" && !*parameterOverride.Required {
					problems = append(problems, fmt.Sprintf("path parameter %q cannot be optional", parameter.Name))
				} else {
					parameter.Required = *parameterOverride.Required
				}
			}
			if parameterOverride.Schema != nil {
				schema, problem := explicitOpenAPISchema(parameterOverride.Schema)
				if problem != "" {
					problems = append(problems, fmt.Sprintf("parameter %s %q %s", parameterOverride.In, parameterOverride.Name, problem))
				} else {
					parameter.Schema = schema
				}
			}
			if parameterOverride.Example != nil {
				parameter.Example = parameterOverride.Example
			}
		}
		if !matched {
			problems = append(problems, fmt.Sprintf("parameter %s %q was not discovered", parameterOverride.In, parameterOverride.Name))
		}
	}
	if override.RequestBody != nil {
		var bodyProblems []string
		operation.RequestBody, bodyProblems = applyOpenAPIRequestBodyOverride(operation.RequestBody, *override.RequestBody)
		problems = append(problems, bodyProblems...)
	}
	if override.ReplaceResponses {
		operation.Responses = map[string]map[string]any{}
	}
	for _, status := range sortedMapKeys(override.Responses) {
		if !isOpenAPIResponseKey(status) {
			problems = append(problems, fmt.Sprintf("response key %q must be default or a three-digit HTTP status pattern", status))
			continue
		}
		responseOverride := override.Responses[status]
		if responseOverride.Remove {
			if responseOverride.Description != "" || responseOverride.MediaType != "" || responseOverride.Schema != nil || responseOverride.Example != nil {
				problems = append(problems, fmt.Sprintf("response %q cannot combine Remove with replacement fields", status))
				continue
			}
			if _, exists := operation.Responses[status]; !exists {
				problems = append(problems, fmt.Sprintf("response %q cannot be removed because it is not present", status))
				continue
			}
			delete(operation.Responses, status)
			continue
		}
		response, responseProblem := applyOpenAPIResponseOverride(operation.Responses[status], status, responseOverride)
		if responseProblem != "" {
			problems = append(problems, fmt.Sprintf("response %q: %s", status, responseProblem))
			continue
		}
		operation.Responses[status] = response
	}
	if len(operation.Responses) == 0 {
		problems = append(problems, "operation must retain at least one response")
	}
	if override.Security != nil {
		requirements := cloneSecurityRequirements(override.Security.Requirements)
		if emptySecurityRequirementIndex(requirements) >= 0 {
			problems = append(problems, "security policy cannot contain an empty requirement object; use an empty requirements array for an explicitly public operation")
		} else {
			operation.Security = &requirements
		}
	}
	return operation, problems
}

// cleanOpenAPITags removes blank duplicates while retaining the author-supplied display order.
func cleanOpenAPITags(tags []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(tags))
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		if _, exists := seen[tag]; exists {
			continue
		}
		seen[tag] = struct{}{}
		out = append(out, tag)
	}
	return out
}

// applyOpenAPIRequestBodyOverride refines existing evidence or creates a body only when a schema or example is supplied.
func applyOpenAPIRequestBodyOverride(body map[string]any, override OpenAPIRequestBodyOverride) (map[string]any, []string) {
	problems := make([]string, 0)
	if override.Remove {
		if override.Required != nil || override.MediaType != "" || override.Schema != nil || override.Example != nil {
			return body, []string{"request body cannot combine Remove with replacement fields"}
		}
		if body == nil {
			return nil, []string{"request body cannot be removed because it is not present"}
		}
		return nil, nil
	}
	if body == nil && override.Schema == nil && override.Example == nil {
		return nil, []string{"request body override cannot create a body without a schema or example"}
	}
	if body == nil {
		body = map[string]any{"content": map[string]any{}}
	} else {
		body = cloneStringAnyMap(body)
	}
	if override.Required != nil {
		if *override.Required {
			body["required"] = true
		} else {
			delete(body, "required")
		}
	}
	if override.Schema == nil && override.Example == nil && override.MediaType == "" {
		return body, problems
	}
	content := mutableOpenAPIContent(body)
	mediaType, mediaProblem := selectOpenAPIMediaType(content, override.MediaType, "application/json")
	if mediaProblem != "" {
		problems = append(problems, mediaProblem)
		return body, problems
	}
	media := mutableOpenAPIMedia(content, mediaType)
	if strings.TrimSpace(override.MediaType) != "" {
		if wildcard, exists := content["*/*"]; exists && len(content) == 1 && mediaType != "*/*" {
			if wildcardMedia, ok := wildcard.(map[string]any); ok {
				media = cloneStringAnyMap(wildcardMedia)
			}
			delete(content, "*/*")
		}
	}
	if override.Schema != nil {
		schema, problem := explicitOpenAPISchema(override.Schema)
		if problem != "" {
			return body, append(problems, problem)
		}
		media["schema"] = schema
	}
	if override.Example != nil {
		media["example"] = override.Example
	}
	content[mediaType] = media
	body["content"] = content
	return body, problems
}

// applyOpenAPIResponseOverride refines or creates one response without disturbing other observed media types.
func applyOpenAPIResponseOverride(response map[string]any, status string, override OpenAPIResponseOverride) (map[string]any, string) {
	if response == nil {
		description := override.Description
		if description == "" {
			if code, err := strconv.Atoi(status); err == nil {
				description = openAPIResponseDescription(code)
			} else {
				description = "Response"
			}
		}
		response = map[string]any{"description": description}
	} else {
		response = cloneStringAnyMap(response)
	}
	if override.Description != "" {
		response["description"] = override.Description
	}
	if override.Schema == nil && override.Example == nil && override.MediaType == "" {
		return response, ""
	}
	content := mutableOpenAPIContent(response)
	mediaType, mediaProblem := selectOpenAPIMediaType(content, override.MediaType, "*/*")
	if mediaProblem != "" {
		return response, mediaProblem
	}
	media := mutableOpenAPIMedia(content, mediaType)
	if override.Schema != nil {
		schema, problem := explicitOpenAPISchema(override.Schema)
		if problem != "" {
			return response, problem
		}
		media["schema"] = schema
	}
	if override.Example != nil {
		media["example"] = override.Example
	}
	content[mediaType] = media
	response["content"] = content
	return response, ""
}

// mutableOpenAPIContent clones the content map because projections must not mutate manifest-owned schema evidence.
func mutableOpenAPIContent(container map[string]any) map[string]any {
	current, _ := container["content"].(map[string]any)
	out := make(map[string]any, len(current))
	for mediaType, media := range current {
		out[mediaType] = media
	}
	return out
}

// mutableOpenAPIMedia clones one media object before adding an explicit schema or example.
func mutableOpenAPIMedia(content map[string]any, mediaType string) map[string]any {
	current, _ := content[mediaType].(map[string]any)
	return cloneStringAnyMap(current)
}

// selectOpenAPIMediaType reuses an unambiguous observed media type unless configuration names one explicitly.
func selectOpenAPIMediaType(content map[string]any, configured string, fallback string) (string, string) {
	if strings.TrimSpace(configured) != "" {
		configured = strings.TrimSpace(configured)
		if normalized, valid := canonicalOpenAPIMediaType(configured); valid {
			return normalized, ""
		}
		return configured, ""
	}
	if len(content) == 1 {
		for mediaType := range content {
			return mediaType, ""
		}
	}
	if len(content) > 1 {
		return "", "media type is required because the discovered contract has multiple content types"
	}
	return fallback, ""
}

// explicitOpenAPISchema rejects malformed configuration instead of silently changing an explicit contract.
func explicitOpenAPISchema(schema any) (map[string]any, string) {
	cleaned, ok := cleanOpenAPISchema(schema).(map[string]any)
	if !ok {
		return nil, "schema must be an OpenAPI schema object"
	}
	return cleaned, ""
}

// cloneStringAnyMap makes a shallow map copy before targeted nested values are replaced.
func cloneStringAnyMap(values map[string]any) map[string]any {
	out := make(map[string]any, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

// isOpenAPIResponseKey accepts exact statuses and OpenAPI status-class patterns such as 2XX.
func isOpenAPIResponseKey(status string) bool {
	if status == "default" {
		return true
	}
	if len(status) != 3 || status[0] < '1' || status[0] > '5' {
		return false
	}
	if status[1:] == "XX" {
		return true
	}
	return status[1] >= '0' && status[1] <= '9' && status[2] >= '0' && status[2] <= '9'
}

// securityForMiddlewares combines exact middleware policies as AND while retaining each policy's OR alternatives.
func securityForMiddlewares(middlewares []string, mappings map[string][]OpenAPISecurityRequirement) *[]OpenAPISecurityRequirement {
	var combined *[]OpenAPISecurityRequirement
	seenMiddleware := map[string]struct{}{}
	for _, middleware := range middlewares {
		if _, seen := seenMiddleware[middleware]; seen {
			continue
		}
		seenMiddleware[middleware] = struct{}{}
		alternatives, exists := mappings[middleware]
		if !exists {
			continue
		}
		alternatives = nonEmptySecurityRequirements(alternatives)
		if len(alternatives) == 0 {
			continue
		}
		combined = combineMiddlewareSecurityPolicies(combined, alternatives)
	}
	return combined
}

// securityForOperationMiddlewares combines global compatibility mappings with policies resolved to individual source-provenanced occurrences.
func securityForOperationMiddlewares(operation Operation, global map[string][]OpenAPISecurityRequirement, scoped map[int][]OpenAPISecurityRequirement) *[]OpenAPISecurityRequirement {
	combined := securityForMiddlewares(operation.Middleware, global)
	for middlewareIndex := range operation.Middleware {
		requirements, exists := scoped[middlewareIndex]
		if !exists {
			continue
		}
		combined = combineMiddlewareSecurityPolicies(combined, requirements)
	}
	return combined
}

// combineMiddlewareSecurityPolicies joins policies as AND while retaining the OR alternatives within each policy.
func combineMiddlewareSecurityPolicies(combined *[]OpenAPISecurityRequirement, requirements []OpenAPISecurityRequirement) *[]OpenAPISecurityRequirement {
	alternatives := nonEmptySecurityRequirements(requirements)
	if len(alternatives) == 0 {
		return combined
	}
	if combined == nil {
		canonical := canonicalSecurityRequirements(cloneSecurityRequirements(alternatives))
		return &canonical
	}
	next := make([]OpenAPISecurityRequirement, 0, len(*combined)*len(alternatives))
	for _, existing := range *combined {
		for _, alternative := range alternatives {
			next = append(next, mergeSecurityRequirement(existing, alternative))
		}
	}
	canonical := canonicalSecurityRequirements(next)
	return &canonical
}

// nonEmptySecurityRequirements keeps malformed mappings from accidentally projecting an explicitly public operation before validation rejects them.
func nonEmptySecurityRequirements(requirements []OpenAPISecurityRequirement) []OpenAPISecurityRequirement {
	out := make([]OpenAPISecurityRequirement, 0, len(requirements))
	for _, requirement := range requirements {
		if len(requirement) == 0 {
			continue
		}
		out = append(out, requirement)
	}
	return out
}

// emptySecurityRequirementIndex reports malformed `{}` alternatives while preserving an empty requirement slice as explicit public policy.
func emptySecurityRequirementIndex(requirements []OpenAPISecurityRequirement) int {
	for index, requirement := range requirements {
		if len(requirement) == 0 {
			return index
		}
	}
	return -1
}

// mergeSecurityRequirement joins schemes and scope sets for middleware policies that must all pass.
func mergeSecurityRequirement(left, right OpenAPISecurityRequirement) OpenAPISecurityRequirement {
	merged := cloneSecurityRequirement(left)
	for scheme, scopes := range right {
		merged[scheme] = dedupeSortedStrings(append(merged[scheme], scopes...))
	}
	return merged
}

// cloneSecurityRequirements detaches nested scope slices from caller-owned configuration.
func cloneSecurityRequirements(requirements []OpenAPISecurityRequirement) []OpenAPISecurityRequirement {
	out := make([]OpenAPISecurityRequirement, 0, len(requirements))
	for _, requirement := range requirements {
		out = append(out, cloneSecurityRequirement(requirement))
	}
	return out
}

// cloneSecurityRequirement detaches one requirement and normalizes its scope order.
func cloneSecurityRequirement(requirement OpenAPISecurityRequirement) OpenAPISecurityRequirement {
	out := make(OpenAPISecurityRequirement, len(requirement))
	for scheme, scopes := range requirement {
		out[scheme] = dedupeSortedStrings(scopes)
	}
	return out
}

// canonicalSecurityRequirements removes duplicate alternatives and makes slice ordering independent of map iteration.
func canonicalSecurityRequirements(requirements []OpenAPISecurityRequirement) []OpenAPISecurityRequirement {
	byFingerprint := map[string]OpenAPISecurityRequirement{}
	for _, requirement := range requirements {
		canonical := cloneSecurityRequirement(requirement)
		data, _ := json.Marshal(canonical)
		byFingerprint[string(data)] = canonical
	}
	keys := sortedMapKeys(byFingerprint)
	out := make([]OpenAPISecurityRequirement, 0, len(keys))
	for _, candidateKey := range keys {
		candidate := byFingerprint[candidateKey]
		redundant := false
		for _, otherKey := range keys {
			if candidateKey == otherKey {
				continue
			}
			if securityRequirementSubsumes(byFingerprint[otherKey], candidate) {
				redundant = true
				break
			}
		}
		if !redundant {
			out = append(out, candidate)
		}
	}
	return out
}

// securityRequirementSubsumes reports whether the left alternative is weaker and therefore makes the right alternative redundant.
func securityRequirementSubsumes(left, right OpenAPISecurityRequirement) bool {
	if len(left) > len(right) {
		return false
	}
	for scheme, leftScopes := range left {
		rightScopes, exists := right[scheme]
		if !exists || !stringSetContains(rightScopes, leftScopes) {
			return false
		}
	}
	return true
}

// stringSetContains reports whether every required scope is present in the candidate superset.
func stringSetContains(values, required []string) bool {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	for _, value := range required {
		if _, exists := set[value]; !exists {
			return false
		}
	}
	return true
}

// dedupeStrings removes duplicate diagnostics while retaining deterministic sorted input order.
func dedupeStrings(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

// dedupeSortedStrings makes OAuth and OpenID scope arrays deterministic.
func dedupeSortedStrings(values []string) []string {
	out := append([]string(nil), values...)
	sort.Strings(out)
	return dedupeStrings(out)
}
