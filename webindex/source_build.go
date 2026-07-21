package webindex

import (
	"fmt"
	"go/build"
	"os"
	"sort"
	"strconv"
	"strings"
)

// sourceBuildEnvironmentError rejects Go environment flags whose alternate inputs syntax discovery cannot mirror.
func sourceBuildEnvironmentError(goFlags string) error {
	for _, field := range splitGoFlags(goFlags) {
		flag := field
		if strings.HasPrefix(flag, "--") {
			flag = "-" + strings.TrimPrefix(flag, "--")
		}
		switch {
		case flag == "-overlay" || strings.HasPrefix(flag, "-overlay="):
			return fmt.Errorf("GOFLAGS %s is not supported because API indexing cannot mirror source overlays", field)
		case flag == "-modfile" || strings.HasPrefix(flag, "-modfile="):
			return fmt.Errorf("GOFLAGS %s is not supported because API indexing cannot mirror alternate module files", field)
		case flag == "-race" || flag == "-msan" || flag == "-asan":
			return fmt.Errorf("GOFLAGS %s is not supported because API indexing cannot mirror implicit instrumentation build tags", field)
		case flag == "-compiler" || strings.HasPrefix(flag, "-compiler="):
			return fmt.Errorf("GOFLAGS %s is not supported because API indexing cannot mirror alternate compiler constraints", field)
		}
	}
	return nil
}

// MatchActiveSourceFile reports whether the Go command would include one source file under the indexing process's active build environment.
// Explicit build tags take the same precedence over GOFLAGS as a command-line -tags flag.
func MatchActiveSourceFile(directory string, name string, buildTags ...string) (bool, error) {
	if err := sourceBuildEnvironmentError(os.Getenv("GOFLAGS")); err != nil {
		return false, err
	}
	buildContext := activeSourceBuildContext(buildTags...)
	return buildContext.MatchFile(directory, name)
}

// activeSourceBuildContext mirrors the environment used by go/packages so syntax discovery cannot index files excluded from the current build.
func activeSourceBuildContext(buildTags ...string) build.Context {
	return activeSourceBuildContextWithEnvironment(nil, buildTags...)
}

// activeSourceBuildContextWithEnvironment uses a captured go env snapshot when cache-backed indexing must not observe later process configuration changes.
func activeSourceBuildContextWithEnvironment(environment map[string]string, buildTags ...string) build.Context {
	buildContext := build.Default
	if value := strings.TrimSpace(sourceBuildEnvironmentValue(environment, "GOOS")); value != "" {
		buildContext.GOOS = value
	}
	if value := strings.TrimSpace(sourceBuildEnvironmentValue(environment, "GOARCH")); value != "" {
		buildContext.GOARCH = value
	}
	if value := strings.TrimSpace(sourceBuildEnvironmentValue(environment, "CGO_ENABLED")); value != "" {
		enabled, err := strconv.ParseBool(value)
		if err == nil {
			buildContext.CgoEnabled = enabled
		}
	}
	effectiveTags := goFlagsBuildTags(sourceBuildEnvironmentValue(environment, "GOFLAGS"))
	if explicitTags := normalizeSourceBuildTags(buildTags); len(explicitTags) > 0 {
		effectiveTags = explicitTags
	}
	buildContext.BuildTags = dedupeSortedStrings(append(buildContext.BuildTags, effectiveTags...))
	return buildContext
}

// sourceBuildEnvironmentValue falls back to the live process only for non-cached callers that have no immutable environment snapshot.
func sourceBuildEnvironmentValue(environment map[string]string, name string) string {
	if environment != nil {
		return environment[name]
	}
	return os.Getenv(name)
}

// pinnedSourceBuildEnvironment disables later GOENV reads while preserving non-Go process settings needed to launch the toolchain.
func pinnedSourceBuildEnvironment(environment map[string]string) []string {
	if environment == nil {
		return nil
	}
	values := make(map[string]string, len(os.Environ())+len(environment)+1)
	for _, entry := range os.Environ() {
		name, value, ok := strings.Cut(entry, "=")
		if ok {
			values[name] = value
		}
	}
	for name, value := range environment {
		values[name] = value
	}
	delete(values, "GOGCCFLAGS")
	values["GOENV"] = "off"
	values["GOPACKAGESDRIVER"] = "off"
	values["GOWORK"] = "off"
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]string, 0, len(names))
	for _, name := range names {
		result = append(result, name+"="+values[name])
	}
	return result
}

// sourceBuildFlags returns the explicit go command flags needed to keep focused type loading aligned with syntax discovery.
func sourceBuildFlags(buildTags []string) []string {
	tags := normalizeSourceBuildTags(buildTags)
	if len(tags) == 0 {
		return nil
	}
	return []string{"-tags=" + strings.Join(tags, ",")}
}

// normalizeSourceBuildTags accepts the comma- and whitespace-separated forms supported by the Go command.
func normalizeSourceBuildTags(values []string) []string {
	tags := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.Trim(strings.TrimSpace(value), "'\"")
		for _, tag := range strings.FieldsFunc(value, func(character rune) bool {
			return character == ',' || character == ' ' || character == '\t'
		}) {
			if tag = strings.TrimSpace(tag); tag != "" {
				tags = append(tags, tag)
			}
		}
	}
	return dedupeSortedStrings(tags)
}

// goFlagsBuildTags extracts the supported single- and double-dash tag forms consumed by the Go command.
func goFlagsBuildTags(goFlags string) []string {
	fields := splitGoFlags(goFlags)
	tags := make([]string, 0)
	for index := 0; index < len(fields); index++ {
		value := ""
		found := false
		switch {
		case strings.HasPrefix(fields[index], "-tags=") || strings.HasPrefix(fields[index], "--tags="):
			value = fields[index][strings.IndexByte(fields[index], '=')+1:]
			found = true
		case (fields[index] == "-tags" || fields[index] == "--tags") && index+1 < len(fields):
			index++
			value = fields[index]
			found = true
		}
		if !found {
			continue
		}
		tags = tags[:0]
		value = strings.Trim(value, "'\"")
		for _, tag := range strings.FieldsFunc(value, func(character rune) bool {
			return character == ',' || character == ' ' || character == '\t'
		}) {
			if tag = strings.TrimSpace(tag); tag != "" {
				tags = append(tags, tag)
			}
		}
	}
	return dedupeSortedStrings(tags)
}

// splitGoFlags preserves quoted tag lists while applying the whitespace tokenization expected by GOFLAGS.
func splitGoFlags(value string) []string {
	fields := make([]string, 0)
	var current strings.Builder
	var quote rune
	escaped := false
	flush := func() {
		if current.Len() == 0 {
			return
		}
		fields = append(fields, current.String())
		current.Reset()
	}
	for _, character := range value {
		if escaped {
			current.WriteRune(character)
			escaped = false
			continue
		}
		if character == '\\' && quote != '\'' {
			escaped = true
			continue
		}
		if quote != 0 {
			if character == quote {
				quote = 0
				continue
			}
			current.WriteRune(character)
			continue
		}
		if character == '\'' || character == '"' {
			quote = character
			continue
		}
		if character == ' ' || character == '\t' || character == '\n' || character == '\r' {
			flush()
			continue
		}
		current.WriteRune(character)
	}
	if escaped {
		current.WriteRune('\\')
	}
	flush()
	return fields
}

// sourceDirectoryIgnored follows `go list` directory eligibility before applying framework-specific expensive-tree exclusions.
func sourceDirectoryIgnored(name string) bool {
	if name == "" || name == "." || name == string(os.PathSeparator) {
		return false
	}
	if name == "testdata" || name == "vendor" || name == "node_modules" || name == "templates" || name == ".cache" || name == "tmp" || name == "bin" {
		return true
	}
	return strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")
}
