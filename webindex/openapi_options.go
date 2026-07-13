package webindex

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
)

// OpenAPIOptions controls metadata and explicit contract overrides applied while projecting a manifest.
// Security is never inferred from middleware names: declare each scheme and map middleware explicitly.
// MiddlewareSecurityRules are preferred when identical expressions can occur in different source
// declarations; MiddlewareSecurity retains global expression-only compatibility. An operation override
// selector must match exactly once; use Method and Path when one handler serves several routes.
// Set ReplaceResponses when explicit responses supersede an unresolved or otherwise ambiguous set.
// Projection overrides refine OpenAPI only: they do not suppress Manifest diagnostics or make strict
// indexing accept unresolved source evidence such as a user-defined JSON marshaler.
type OpenAPIOptions struct {
	Info                    OpenAPIInfoOptions
	Operations              []OpenAPIOperationOverride
	SecuritySchemes         map[string]OpenAPISecurityScheme
	MiddlewareSecurity      map[string][]OpenAPISecurityRequirement
	MiddlewareSecurityRules []OpenAPIMiddlewareSecurityRule
}

// OpenAPIInfoOptions controls the human-facing document metadata.
type OpenAPIInfoOptions struct {
	Title       string
	Version     string
	Description string
}

// OpenAPIOperationSelector identifies exactly one operation without depending on its internal diagnostic ID.
type OpenAPIOperationSelector struct {
	ImportPath string
	Package    string
	Receiver   string
	Function   string
	Method     string
	Path       string
}

// OpenAPIOperationOverride replaces evidence that source analysis cannot express unambiguously.
type OpenAPIOperationOverride struct {
	Match            OpenAPIOperationSelector
	Summary          string
	Description      string
	Tags             []string
	Parameters       []OpenAPIParameterOverride
	RequestBody      *OpenAPIRequestBodyOverride
	ReplaceResponses bool
	Responses        map[string]OpenAPIResponseOverride
	Security         *OpenAPISecurityPolicy
}

// OpenAPIParameterOverride refines one discovered path, query, header, or cookie parameter.
type OpenAPIParameterOverride struct {
	In       string
	Name     string
	Required *bool
	Schema   any
	Example  any
}

// OpenAPIRequestBodyOverride refines body requiredness or supplies an ambiguous media contract.
type OpenAPIRequestBodyOverride struct {
	Remove    bool
	Required  *bool
	MediaType string
	Schema    any
	Example   any
}

// OpenAPIResponseOverride refines one status response or supplies an ambiguous media contract.
type OpenAPIResponseOverride struct {
	Description string
	MediaType   string
	Schema      any
	Example     any
	Remove      bool
}

// OpenAPISecurityPolicy lists alternative requirements; schemes within one requirement are jointly required.
type OpenAPISecurityPolicy struct {
	Requirements []OpenAPISecurityRequirement `json:"requirements"`
}

// OpenAPISecurityRequirement maps security scheme names to their required OAuth or OpenID scopes.
type OpenAPISecurityRequirement map[string][]string

// OpenAPIMiddlewareSecurityRule maps middleware attached by one source declaration to an explicit security policy.
// SourceFile must be a project-relative slash-separated Go file, Function is the enclosing declaration name,
// Receiver optionally restricts a method declaration, and Expression must exactly match Operation.Middleware.
type OpenAPIMiddlewareSecurityRule struct {
	Expression   string
	SourceFile   string
	Function     string
	Receiver     string
	Requirements []OpenAPISecurityRequirement
}

// OpenAPISecurityScheme describes an OpenAPI 3.0 security scheme without inferring policy from middleware names.
type OpenAPISecurityScheme struct {
	Type             string `json:"type"`
	Description      string `json:"description,omitempty"`
	Name             string `json:"name,omitempty"`
	In               string `json:"in,omitempty"`
	Scheme           string `json:"scheme,omitempty"`
	BearerFormat     string `json:"bearerFormat,omitempty"`
	OpenIDConnectURL string `json:"openIdConnectUrl,omitempty"`
	Flows            any    `json:"flows,omitempty"`
}

// OpenAPIProjectionError reports invalid or ambiguous explicit OpenAPI configuration.
type OpenAPIProjectionError struct {
	Problems []string
}

// Error formats every projection problem so configuration can be corrected in one pass.
func (e *OpenAPIProjectionError) Error() string {
	if e == nil || len(e.Problems) == 0 {
		return "OpenAPI projection failed"
	}
	if len(e.Problems) == 1 {
		return "OpenAPI projection failed: " + e.Problems[0]
	}
	return fmt.Sprintf("OpenAPI projection failed with %d problems: %s", len(e.Problems), strings.Join(e.Problems, "; "))
}

// normalizedOpenAPIOptions applies stable document defaults without mutating caller-owned maps or slices.
func normalizedOpenAPIOptions(options OpenAPIOptions) OpenAPIOptions {
	if strings.TrimSpace(options.Info.Title) == "" {
		options.Info.Title = "Forj Generated API"
	} else {
		options.Info.Title = strings.TrimSpace(options.Info.Title)
	}
	if strings.TrimSpace(options.Info.Version) == "" {
		options.Info.Version = "1.0.0"
	} else {
		options.Info.Version = strings.TrimSpace(options.Info.Version)
	}
	options.Info.Description = strings.TrimSpace(options.Info.Description)
	return options
}

// validateOpenAPISecuritySchemes rejects malformed definitions before they can create an unusable document.
func validateOpenAPISecuritySchemes(schemes map[string]OpenAPISecurityScheme) []string {
	problems := make([]string, 0)
	names := sortedMapKeys(schemes)
	for _, name := range names {
		scheme := schemes[name]
		if !isOpenAPIComponentKey(name) {
			problems = append(problems, fmt.Sprintf("security scheme name %q must contain only letters, digits, dot, hyphen, or underscore", name))
			continue
		}
		switch scheme.Type {
		case "apiKey":
			if strings.TrimSpace(scheme.Name) == "" || scheme.In != "query" && scheme.In != "header" && scheme.In != "cookie" {
				problems = append(problems, fmt.Sprintf("security scheme %q of type apiKey requires name and in=query|header|cookie", name))
			}
			if scheme.In == "header" || scheme.In == "cookie" {
				if !isHTTPToken(scheme.Name) {
					problems = append(problems, fmt.Sprintf("security scheme %q apiKey name %q is not a valid %s name", name, scheme.Name, scheme.In))
				}
			}
			problems = append(problems, forbiddenSecuritySchemeFieldProblems(name, scheme, "scheme", "bearerFormat", "openIdConnectUrl", "flows")...)
		case "http":
			if strings.TrimSpace(scheme.Scheme) != scheme.Scheme || !isHTTPToken(scheme.Scheme) {
				problems = append(problems, fmt.Sprintf("security scheme %q of type http requires scheme", name))
			}
			if scheme.BearerFormat != "" && !strings.EqualFold(strings.TrimSpace(scheme.Scheme), "bearer") {
				problems = append(problems, fmt.Sprintf("security scheme %q may set bearerFormat only when scheme is bearer", name))
			}
			problems = append(problems, forbiddenSecuritySchemeFieldProblems(name, scheme, "name", "in", "openIdConnectUrl", "flows")...)
		case "oauth2":
			if scheme.Flows == nil {
				problems = append(problems, fmt.Sprintf("security scheme %q of type oauth2 requires flows", name))
			} else {
				flowProblems, _ := validateOpenAPIOAuthFlows(name, scheme.Flows)
				problems = append(problems, flowProblems...)
			}
			problems = append(problems, forbiddenSecuritySchemeFieldProblems(name, scheme, "name", "in", "scheme", "bearerFormat", "openIdConnectUrl")...)
		case "openIdConnect":
			if strings.TrimSpace(scheme.OpenIDConnectURL) == "" {
				problems = append(problems, fmt.Sprintf("security scheme %q of type openIdConnect requires openIdConnectUrl", name))
			} else if problem := validateOpenAPIURL(scheme.OpenIDConnectURL); problem != "" {
				problems = append(problems, fmt.Sprintf("security scheme %q openIdConnectUrl %s", name, problem))
			}
			problems = append(problems, forbiddenSecuritySchemeFieldProblems(name, scheme, "name", "in", "scheme", "bearerFormat", "flows")...)
		default:
			problems = append(problems, fmt.Sprintf("security scheme %q has unsupported type %q", name, scheme.Type))
		}
	}
	return problems
}

// validateOpenAPISecurityRequirements ensures every policy references a declared scheme.
func validateOpenAPISecurityRequirements(context string, requirements []OpenAPISecurityRequirement, schemes map[string]OpenAPISecurityScheme) []string {
	problems := make([]string, 0)
	for index, requirement := range requirements {
		if len(requirement) == 0 {
			problems = append(problems, fmt.Sprintf("%s security requirement %d cannot be an empty object; use an empty security array for an explicitly public operation", context, index+1))
			continue
		}
		for _, name := range sortedMapKeys(requirement) {
			if !isOpenAPIComponentKey(name) {
				problems = append(problems, fmt.Sprintf("%s references invalid security scheme name %q", context, name))
			}
			scheme, exists := schemes[name]
			if !exists {
				problems = append(problems, fmt.Sprintf("%s references unknown security scheme %q", context, name))
				continue
			}
			scopes := requirement[name]
			seenScopes := map[string]struct{}{}
			for _, scope := range scopes {
				if !isOAuthScopeToken(scope) {
					problems = append(problems, fmt.Sprintf("%s security scheme %q contains invalid scope %q", context, name, scope))
					continue
				}
				if _, duplicate := seenScopes[scope]; duplicate {
					problems = append(problems, fmt.Sprintf("%s security scheme %q repeats scope %q", context, name, scope))
					continue
				}
				seenScopes[scope] = struct{}{}
			}
			switch scheme.Type {
			case "apiKey", "http":
				if len(scopes) > 0 {
					problems = append(problems, fmt.Sprintf("%s security scheme %q of type %s cannot require scopes", context, name, scheme.Type))
				}
			case "oauth2":
				_, declaredScopes := validateOpenAPIOAuthFlows(name, scheme.Flows)
				for _, scope := range scopes {
					if _, declared := declaredScopes[scope]; !declared {
						problems = append(problems, fmt.Sprintf("%s security scheme %q requires undeclared OAuth scope %q", context, name, scope))
					}
				}
			}
		}
	}
	return problems
}

// forbiddenSecuritySchemeFieldProblems rejects fields that OpenAPI does not permit for the selected security scheme type.
func forbiddenSecuritySchemeFieldProblems(name string, scheme OpenAPISecurityScheme, fields ...string) []string {
	problems := make([]string, 0)
	for _, field := range fields {
		present := false
		switch field {
		case "name":
			present = scheme.Name != ""
		case "in":
			present = scheme.In != ""
		case "scheme":
			present = scheme.Scheme != ""
		case "bearerFormat":
			present = scheme.BearerFormat != ""
		case "openIdConnectUrl":
			present = scheme.OpenIDConnectURL != ""
		case "flows":
			present = scheme.Flows != nil
		}
		if present {
			problems = append(problems, fmt.Sprintf("security scheme %q of type %s cannot set %s", name, scheme.Type, field))
		}
	}
	return problems
}

// validateOpenAPIOAuthFlows validates the conditional OAuth Flow Object fields and returns every declared scope.
func validateOpenAPIOAuthFlows(schemeName string, value any) ([]string, map[string]struct{}) {
	problems := make([]string, 0)
	declaredScopes := map[string]struct{}{}
	flows, err := normalizeOpenAPIObject(value)
	if err != nil {
		return []string{fmt.Sprintf("security scheme %q flows must be a JSON object: %v", schemeName, err)}, declaredScopes
	}
	flowCount := 0
	for _, flowName := range sortedMapKeys(flows) {
		if strings.HasPrefix(flowName, "x-") {
			continue
		}
		requiresAuthorizationURL := false
		requiresTokenURL := false
		switch flowName {
		case "implicit":
			requiresAuthorizationURL = true
		case "password", "clientCredentials":
			requiresTokenURL = true
		case "authorizationCode":
			requiresAuthorizationURL = true
			requiresTokenURL = true
		default:
			problems = append(problems, fmt.Sprintf("security scheme %q has unsupported OAuth flow %q", schemeName, flowName))
			continue
		}
		flowCount++
		flow, flowErr := normalizeOpenAPIObject(flows[flowName])
		if flowErr != nil {
			problems = append(problems, fmt.Sprintf("security scheme %q OAuth flow %q must be an object", schemeName, flowName))
			continue
		}
		for _, field := range sortedMapKeys(flow) {
			switch field {
			case "authorizationUrl", "tokenUrl", "refreshUrl", "scopes":
			default:
				if !strings.HasPrefix(field, "x-") {
					problems = append(problems, fmt.Sprintf("security scheme %q OAuth flow %q has unsupported field %q", schemeName, flowName, field))
				}
			}
		}
		if requiresAuthorizationURL {
			problems = append(problems, validateOpenAPIFlowURL(schemeName, flowName, flow, "authorizationUrl")...)
		} else if _, present := flow["authorizationUrl"]; present {
			problems = append(problems, fmt.Sprintf("security scheme %q OAuth flow %q cannot set authorizationUrl", schemeName, flowName))
		}
		if requiresTokenURL {
			problems = append(problems, validateOpenAPIFlowURL(schemeName, flowName, flow, "tokenUrl")...)
		} else if _, present := flow["tokenUrl"]; present {
			problems = append(problems, fmt.Sprintf("security scheme %q OAuth flow %q cannot set tokenUrl", schemeName, flowName))
		}
		if _, present := flow["refreshUrl"]; present {
			problems = append(problems, validateOpenAPIFlowURL(schemeName, flowName, flow, "refreshUrl")...)
		}
		rawScopes, present := flow["scopes"]
		if !present {
			problems = append(problems, fmt.Sprintf("security scheme %q OAuth flow %q requires scopes", schemeName, flowName))
			continue
		}
		scopes, scopesErr := normalizeOpenAPIObject(rawScopes)
		if scopesErr != nil {
			problems = append(problems, fmt.Sprintf("security scheme %q OAuth flow %q scopes must be an object", schemeName, flowName))
			continue
		}
		for _, scope := range sortedMapKeys(scopes) {
			if !isOAuthScopeToken(scope) {
				problems = append(problems, fmt.Sprintf("security scheme %q OAuth flow %q has invalid scope name %q", schemeName, flowName, scope))
				continue
			}
			if _, ok := scopes[scope].(string); !ok {
				problems = append(problems, fmt.Sprintf("security scheme %q OAuth flow %q scope %q description must be a string", schemeName, flowName, scope))
				continue
			}
			declaredScopes[scope] = struct{}{}
		}
	}
	if flowCount == 0 {
		problems = append(problems, fmt.Sprintf("security scheme %q requires at least one OAuth flow", schemeName))
	}
	return problems, declaredScopes
}

// validateOpenAPIFlowURL validates one conditionally required URL in an OAuth Flow Object.
func validateOpenAPIFlowURL(schemeName, flowName string, flow map[string]any, field string) []string {
	raw, present := flow[field]
	if !present {
		return []string{fmt.Sprintf("security scheme %q OAuth flow %q requires %s", schemeName, flowName, field)}
	}
	value, ok := raw.(string)
	if !ok {
		return []string{fmt.Sprintf("security scheme %q OAuth flow %q %s must be a string URL", schemeName, flowName, field)}
	}
	if problem := validateOpenAPIURL(value); problem != "" {
		return []string{fmt.Sprintf("security scheme %q OAuth flow %q %s %s", schemeName, flowName, field, problem)}
	}
	return nil
}

// validateOpenAPIURL accepts absolute and relative URI references while rejecting blank or malformed values.
func validateOpenAPIURL(value string) string {
	if strings.TrimSpace(value) == "" {
		return "cannot be empty"
	}
	if _, err := url.ParseRequestURI(value); err != nil {
		return "must be a valid URI reference"
	}
	return ""
}

// normalizeOpenAPIObject converts structs and typed maps into the JSON object shape OpenAPI will actually serialize.
func normalizeOpenAPIObject(value any) (map[string]any, error) {
	normalized, err := normalizeOpenAPIJSONValue(value)
	if err != nil {
		return nil, err
	}
	object, ok := normalized.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("got %T", normalized)
	}
	return object, nil
}

// normalizeOpenAPIJSONValue verifies JSON compatibility and returns maps, arrays, and numbers in one traversable representation.
func normalizeOpenAPIJSONValue(value any) (any, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var normalized any
	if err := decoder.Decode(&normalized); err != nil {
		return nil, err
	}
	return normalized, nil
}

// isOpenAPIComponentKey applies the key grammar shared by OpenAPI component maps.
func isOpenAPIComponentKey(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if character >= 'A' && character <= 'Z' || character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '.' || character == '-' || character == '_' {
			continue
		}
		return false
	}
	return true
}

// isHTTPToken applies the RFC token grammar used by HTTP authentication schemes and header names.
func isHTTPToken(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if character > 127 || character <= 32 {
			return false
		}
		switch character {
		case '(', ')', '<', '>', '@', ',', ';', ':', '\\', '"', '/', '[', ']', '?', '=', '{', '}':
			return false
		}
	}
	return true
}

// isOAuthScopeToken applies the RFC scope-token grammar used by OAuth authorization requests.
func isOAuthScopeToken(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if character == '!' || character >= '#' && character <= '[' || character >= ']' && character <= '~' {
			continue
		}
		return false
	}
	return true
}

// sortedMapKeys returns deterministic string keys for validation and projection decisions.
func sortedMapKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
