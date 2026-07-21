package webindex

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// TestEnclosingGoModuleRequiresARegularBoundary verifies malformed module markers cannot enable unsafe directory batching.
func TestEnclosingGoModuleRequiresARegularBoundary(t *testing.T) {
	root := t.TempDir()
	child := filepath.Join(root, "handlers")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatalf("create handler directory: %v", err)
	}
	if err := os.Mkdir(filepath.Join(root, "go.mod"), 0o755); err != nil {
		t.Fatalf("create malformed module marker: %v", err)
	}
	if module := enclosingGoModule(child); module != "" {
		t.Fatalf("module root = %q, want no module for a non-file go.mod", module)
	}
	if err := os.Remove(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("remove malformed module marker: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/module\n"), 0o644); err != nil {
		t.Fatalf("write module marker: %v", err)
	}
	if module := enclosingGoModule(child); module != root {
		t.Fatalf("module root = %q, want %q", module, root)
	}
}

// TestTypedPackagePatternsCanonicalizeDeduplicateAndSortDirectories verifies one package query represents every handler directory.
func TestTypedPackagePatternsCanonicalizeDeduplicateAndSortDirectories(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	writeTypedFixtureFiles(t, root, map[string]string{
		"go.mod": "module example.com/patterns\n\ngo 1.25.0\n",
	})

	alpha := filepath.Join(root, "alpha")
	zeta := filepath.Join(root, "zeta")
	patterns := typedPackagePatterns(root, []string{
		filepath.Join("zeta", "handler.go"),
		filepath.Join("alpha", "second.go"),
		filepath.Join(alpha, "first.go"),
		filepath.Join("zeta", "..", "alpha", "third.go"),
	})
	want := []string{alpha, zeta}
	if !reflect.DeepEqual(patterns, want) {
		t.Fatalf("unexpected typed package patterns:\n got: %#v\nwant: %#v", patterns, want)
	}

	for _, pattern := range patterns {
		if !filepath.IsAbs(pattern) || filepath.Clean(pattern) != pattern {
			t.Fatalf("typed package pattern must be a canonical absolute directory: %q", pattern)
		}
		if strings.HasPrefix(pattern, "file=") {
			t.Fatalf("typed package pattern must not use a file query: %q", pattern)
		}
	}
}

// TestTypedPackagePatternsIgnoreBlankAndInvalidFiles verifies unusable discovery entries cannot widen package loading to the working directory.
func TestTypedPackagePatternsIgnoreBlankAndInvalidFiles(t *testing.T) {
	patterns := typedPackagePatterns(t.TempDir(), []string{"", " ", "\t", "invalid\x00handler.go"})
	if len(patterns) != 0 {
		t.Fatalf("expected blank and invalid files to be ignored, got %#v", patterns)
	}
}

// TestTypedPackagePatternsPreserveAdHocFileQueries verifies non-module packages retain go/packages' named-file fallback behavior.
func TestTypedPackagePatternsPreserveAdHocFileQueries(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "handlers", "first.go")
	second := filepath.Join(root, "handlers", "second.go")
	writeTypedFixtureFiles(t, root, map[string]string{
		"handlers/first.go":  "package handlers\n",
		"handlers/second.go": "package handlers\n",
	})

	patterns := typedPackagePatterns(root, []string{second, first})
	want := []string{"file=" + first, "file=" + second}
	sort.Strings(want)
	if !reflect.DeepEqual(patterns, want) {
		t.Fatalf("unexpected ad-hoc package patterns:\n got: %#v\nwant: %#v", patterns, want)
	}
}

// TestTypedPackagePatternsHonorDisabledModuleMode verifies a go.mod is not treated as active when the Go command is explicitly in GOPATH mode.
func TestTypedPackagePatternsHonorDisabledModuleMode(t *testing.T) {
	root := t.TempDir()
	handler := filepath.Join(root, "handlers", "handler.go")
	writeTypedFixtureFiles(t, root, map[string]string{
		"go.mod":              "module example.com/disabled\n\ngo 1.25.0\n",
		"handlers/handler.go": "package handlers\n",
	})
	t.Setenv("GO111MODULE", "off")

	patterns := typedPackagePatterns(root, []string{handler})
	want := []string{"file=" + handler}
	if !reflect.DeepEqual(patterns, want) {
		t.Fatalf("unexpected GOPATH-mode package patterns:\n got: %#v\nwant: %#v", patterns, want)
	}
}

// TestTypedPackagePatternsFallBackAcrossUnsafeModuleBoundaries verifies batching cannot reinterpret nested, external, or wildcard-shaped package paths.
func TestTypedPackagePatternsFallBackAcrossUnsafeModuleBoundaries(t *testing.T) {
	root := t.TempDir()
	external := t.TempDir()
	rootHandler := filepath.Join(root, "handlers", "handler.go")
	nestedHandler := filepath.Join(root, "nested", "handlers", "handler.go")
	wildcardHandler := filepath.Join(root, "handlers...legacy", "handler.go")
	externalHandler := filepath.Join(external, "handlers", "handler.go")
	writeTypedFixtureFiles(t, root, map[string]string{
		"go.mod":                       "module example.com/root\n\ngo 1.25.0\n",
		"handlers/handler.go":          "package handlers\n",
		"handlers...legacy/handler.go": "package handlerslegacy\n",
		"nested/go.mod":                "module example.com/nested\n\ngo 1.25.0\n",
		"nested/handlers/handler.go":   "package handlers\n",
	})
	writeTypedFixtureFiles(t, external, map[string]string{
		"go.mod":              "module example.com/external\n\ngo 1.25.0\n",
		"handlers/handler.go": "package handlers\n",
	})

	patterns := typedPackagePatterns(root, []string{externalHandler, wildcardHandler, nestedHandler, rootHandler})
	want := []string{
		filepath.Dir(rootHandler),
		"file=" + nestedHandler,
		"file=" + wildcardHandler,
		"file=" + externalHandler,
	}
	sort.Strings(want)
	if !reflect.DeepEqual(patterns, want) {
		t.Fatalf("unexpected mixed package patterns:\n got: %#v\nwant: %#v", patterns, want)
	}
}
