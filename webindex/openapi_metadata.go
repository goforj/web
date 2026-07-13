package webindex

import (
	"fmt"
	"go/ast"
	"strings"
	"unicode"
	"unicode/utf8"
)

// OperationMetadata records human-authored contract context independently of route discovery evidence.
type OperationMetadata struct {
	Summary     string                 `json:"summary,omitempty"`
	Description string                 `json:"description,omitempty"`
	Tags        []string               `json:"tags,omitempty"`
	Security    *OpenAPISecurityPolicy `json:"security,omitempty"`
}

// operationMetadataFromHandler extracts prose and explicit OpenAPI directives from one handler declaration.
func operationMetadataFromHandler(function *ast.FuncDecl, packageName string) (*OperationMetadata, []string) {
	if function == nil {
		return nil, nil
	}
	metadata := &OperationMetadata{}
	lines := openAPIDocLines(function.Doc)
	prose := make([]string, 0, len(lines))
	tags := make([]string, 0)
	security := make([]OpenAPISecurityRequirement, 0)
	descriptionDirectives := make([]string, 0)
	problems := make([]string, 0)
	explicitSummary := ""
	explicitPublic := false
	inExample := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)
		if strings.HasPrefix(lower, "@openapi.") {
			inExample = false
			name, value, _ := strings.Cut(trimmed, " ")
			name = strings.ToLower(strings.TrimSpace(name))
			value = strings.TrimSpace(value)
			switch name {
			case "@openapi.summary":
				if value == "" {
					problems = append(problems, "@openapi.summary requires text")
				} else {
					explicitSummary = value
				}
			case "@openapi.description":
				if value == "" {
					problems = append(problems, "@openapi.description requires text")
				} else {
					descriptionDirectives = append(descriptionDirectives, value)
				}
			case "@openapi.tag", "@openapi.tags":
				if value == "" {
					problems = append(problems, name+" requires a tag")
				} else {
					tags = append(tags, splitOpenAPITags(value)...)
				}
			case "@openapi.security":
				requirement, public, problem := parseOpenAPISecurityDirective(value)
				if problem != "" {
					problems = append(problems, problem)
				} else if public {
					explicitPublic = true
				} else {
					security = append(security, requirement)
				}
			default:
				problems = append(problems, fmt.Sprintf("unknown OpenAPI directive %q", name))
			}
			continue
		}
		if strings.HasPrefix(lower, "@group ") {
			if value := strings.TrimSpace(trimmed[len("@group "):]); value != "" {
				tags = append(tags, value)
			}
			continue
		}
		if strings.HasPrefix(lower, "example:") {
			inExample = true
			continue
		}
		if strings.HasPrefix(trimmed, "@") || inExample {
			continue
		}
		prose = append(prose, trimmed)
	}

	metadata.Summary, metadata.Description = openAPISummaryAndDescription(prose)
	metadata.Summary = openAPIHandlerSummary(function.Name.Name, metadata.Summary)
	if explicitSummary != "" {
		metadata.Summary = explicitSummary
	}
	if len(descriptionDirectives) > 0 {
		metadata.Description = strings.Join(descriptionDirectives, "\n")
	}
	metadata.Tags = cleanOpenAPITags(tags)
	if len(metadata.Tags) == 0 && packageName != "" {
		metadata.Tags = []string{upperCamelIdentifier(packageName)}
	}
	if explicitPublic && len(security) > 0 {
		problems = append(problems, "@openapi.security none cannot be combined with named security schemes")
	}
	if explicitPublic && len(security) == 0 {
		metadata.Security = &OpenAPISecurityPolicy{Requirements: []OpenAPISecurityRequirement{}}
	} else if !explicitPublic && len(security) > 0 {
		metadata.Security = &OpenAPISecurityPolicy{Requirements: canonicalSecurityRequirements(security)}
	}
	if metadata.Summary == "" && metadata.Description == "" && len(metadata.Tags) == 0 && metadata.Security == nil {
		return nil, problems
	}
	return metadata, problems
}

// openAPIHandlerSummary removes Go's required declaration-name prefix because Scalar already presents the operationId beside human-facing prose.
func openAPIHandlerSummary(functionName string, summary string) string {
	functionName = strings.TrimSpace(functionName)
	summary = strings.TrimSpace(summary)
	if functionName == "" || !strings.HasPrefix(summary, functionName) || len(summary) == len(functionName) {
		return summary
	}
	remainder := summary[len(functionName):]
	separator, _ := utf8.DecodeRuneInString(remainder)
	if !unicode.IsSpace(separator) {
		return summary
	}
	remainder = strings.TrimSpace(remainder)
	if remainder == "" {
		return summary
	}
	runes := []rune(remainder)
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}

// openAPIDocLines normalizes line and block comments while retaining paragraph boundaries.
func openAPIDocLines(group *ast.CommentGroup) []string {
	if group == nil {
		return nil
	}
	lines := make([]string, 0, len(group.List))
	for _, comment := range group.List {
		text := comment.Text
		switch {
		case strings.HasPrefix(text, "//"):
			lines = append(lines, strings.TrimSpace(strings.TrimPrefix(text, "//")))
		case strings.HasPrefix(text, "/*"):
			text = strings.TrimPrefix(text, "/*")
			text = strings.TrimSuffix(text, "*/")
			for _, line := range strings.Split(text, "\n") {
				line = strings.TrimSpace(line)
				line = strings.TrimSpace(strings.TrimPrefix(line, "*"))
				lines = append(lines, line)
			}
		}
	}
	return lines
}

// openAPISummaryAndDescription uses the first prose sentence as summary and retains all remaining prose as description.
func openAPISummaryAndDescription(lines []string) (string, string) {
	paragraphs := make([]string, 0)
	current := make([]string, 0)
	flush := func() {
		if len(current) == 0 {
			return
		}
		paragraphs = append(paragraphs, strings.Join(current, " "))
		current = nil
	}
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			flush()
			continue
		}
		current = append(current, line)
	}
	flush()
	if len(paragraphs) == 0 {
		return "", ""
	}
	first := paragraphs[0]
	boundary := openAPISentenceBoundary(first)
	if boundary < 0 {
		return first, strings.Join(paragraphs[1:], "\n\n")
	}
	summary := strings.TrimSpace(first[:boundary])
	remainder := strings.TrimSpace(first[boundary:])
	descriptionParts := make([]string, 0, len(paragraphs))
	if remainder != "" {
		descriptionParts = append(descriptionParts, remainder)
	}
	descriptionParts = append(descriptionParts, paragraphs[1:]...)
	return summary, strings.Join(descriptionParts, "\n\n")
}

// openAPISentenceBoundary finds punctuation followed by whitespace or the end of the first paragraph.
func openAPISentenceBoundary(value string) int {
	for index, character := range value {
		if character != '.' && character != '!' && character != '?' {
			continue
		}
		next := index + len(string(character))
		if next == len(value) || next < len(value) && unicode.IsSpace(rune(value[next])) {
			return next
		}
	}
	return -1
}

// splitOpenAPITags accepts repeated directives and comma-separated display names.
func splitOpenAPITags(value string) []string {
	parts := strings.Split(value, ",")
	tags := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			tags = append(tags, part)
		}
	}
	return tags
}

// parseOpenAPISecurityDirective parses one OR alternative using `scheme [scope ...]` syntax.
func parseOpenAPISecurityDirective(value string) (OpenAPISecurityRequirement, bool, string) {
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return nil, false, "@openapi.security requires a scheme name or none"
	}
	if strings.EqualFold(fields[0], "none") {
		if len(fields) != 1 {
			return nil, false, "@openapi.security none does not accept scopes"
		}
		return nil, true, ""
	}
	scopes := make([]string, 0)
	for _, field := range fields[1:] {
		for _, scope := range strings.Split(field, ",") {
			if scope = strings.TrimSpace(scope); scope != "" {
				scopes = append(scopes, scope)
			}
		}
	}
	return OpenAPISecurityRequirement{fields[0]: dedupeSortedStrings(scopes)}, false, ""
}
