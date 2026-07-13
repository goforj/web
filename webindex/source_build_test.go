package webindex

import (
	"context"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// TestGoFlagsBuildTagsPreservesQuotedLists verifies GOFLAGS tokenization matches quoted and comma-delimited tag conventions.
func TestGoFlagsBuildTagsPreservesQuotedLists(t *testing.T) {
	got := goFlagsBuildTags(`-trimpath -tags='audit_tag second-tag third' -mod=mod`)
	want := []string{"audit_tag", "second-tag", "third"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("GOFLAGS tags = %v, want %v", got, want)
	}
	if got := goFlagsBuildTags(`-tags=first,second -tags final`); !reflect.DeepEqual(got, []string{"final"}) {
		t.Fatalf("repeated GOFLAGS tags = %v, want final assignment", got)
	}
	if got := goFlagsBuildTags(`--tags=first,second --tags final`); !reflect.DeepEqual(got, []string{"final"}) {
		t.Fatalf("double-dash GOFLAGS tags = %v, want final assignment", got)
	}
}

// TestSourceBuildEnvironmentRejectsUnmirroredInputs prevents GOFLAGS from selecting source unavailable to syntax discovery.
func TestSourceBuildEnvironmentRejectsUnmirroredInputs(t *testing.T) {
	for _, goFlags := range []string{
		"-overlay=overlay.json",
		"--overlay=overlay.json",
		"-modfile alternate.mod",
		"--modfile alternate.mod",
		"-race",
		"--race",
		"-msan",
		"--msan",
		"-asan",
		"--asan",
		"-compiler=gccgo",
		"--compiler=gccgo",
	} {
		if err := sourceBuildEnvironmentError(goFlags); err == nil {
			t.Fatalf("GOFLAGS %q were accepted", goFlags)
		}
	}
	if err := sourceBuildEnvironmentError("-trimpath -tags=dev"); err != nil {
		t.Fatalf("supported GOFLAGS were rejected: %v", err)
	}
}

// TestMatchActiveSourceFileRejectsUnmirroredGOFlags keeps standalone source selection as fail-closed as a complete index run.
func TestMatchActiveSourceFileRejectsUnmirroredGOFlags(t *testing.T) {
	root := t.TempDir()
	writeFixtureFiles(t, root, map[string]string{"active.go": "package buildscope\n"})
	t.Setenv("GOFLAGS", "--overlay=overlay.json")
	matched, err := MatchActiveSourceFile(root, "active.go")
	if err == nil || matched {
		t.Fatalf("MatchActiveSourceFile accepted unmirrored GOFLAGS: matched=%t err=%v", matched, err)
	}
}

// TestParseGoFilesHonorsBuildAndDirectoryEligibility keeps excluded source from leaking routes or diagnostics into the active build.
func TestParseGoFilesHonorsBuildAndDirectoryEligibility(t *testing.T) {
	root := t.TempDir()
	oppositeGOOS := "windows"
	if runtime.GOOS == "windows" {
		oppositeGOOS = "linux"
	}
	writeFixtureFiles(t, root, map[string]string{
		"go.mod":                           "module example.com/buildscope\n\ngo 1.24\n",
		"active.go":                        "package buildscope\n",
		"tagged.go":                        "//go:build audit_tag\n\npackage buildscope\n",
		"excluded.go":                      "//go:build never_enabled\n\npackage buildscope\n",
		"platform_" + oppositeGOOS + ".go": "package buildscope\n",
		"testdata/ignored.go":              "package ignored\n",
		".hidden/ignored.go":               "package ignored\n",
		"_generated/ignored.go":            "package ignored\n",
		"bin/ignored.go":                   "package ignored\n",
		"nested/go.mod":                    "module example.com/nested\n\ngo 1.24\n",
		"nested/ignored.go":                "package nested\n",
	})
	t.Setenv("GOFLAGS", `-tags='audit_tag another'`)
	for name, want := range map[string]bool{
		"active.go":                        true,
		"tagged.go":                        true,
		"excluded.go":                      false,
		"platform_" + oppositeGOOS + ".go": false,
	} {
		matched, err := MatchActiveSourceFile(root, name)
		if err != nil {
			t.Fatalf("match active source file %s: %v", name, err)
		}
		if matched != want {
			t.Errorf("MatchActiveSourceFile(%s) = %t, want %t", name, matched, want)
		}
	}

	parsed, _, diagnostics, err := parseGoFiles(context.Background(), root, nil)
	if err != nil {
		t.Fatalf("parse build-aware source: %v", err)
	}
	if len(diagnostics) != 0 {
		t.Fatalf("unexpected build diagnostics: %+v", diagnostics)
	}
	paths := make([]string, 0, len(parsed))
	for _, file := range parsed {
		relative, err := filepath.Rel(root, file.Path)
		if err != nil {
			t.Fatalf("relativize parsed path: %v", err)
		}
		paths = append(paths, filepath.ToSlash(relative))
	}
	sort.Strings(paths)
	if want := []string{"active.go", "tagged.go"}; !reflect.DeepEqual(paths, want) {
		t.Fatalf("eligible files = %v, want %v", paths, want)
	}
}

// TestMatchActiveSourceFileExplicitTagsOverrideGOFlags mirrors the precedence of a go command -tags argument over GOFLAGS.
func TestMatchActiveSourceFileExplicitTagsOverrideGOFlags(t *testing.T) {
	root := t.TempDir()
	writeFixtureFiles(t, root, map[string]string{
		"ambient.go":  "//go:build ambient\n\npackage buildscope\n",
		"explicit.go": "//go:build explicit\n\npackage buildscope\n",
	})
	t.Setenv("GOFLAGS", "-tags=ambient")

	ambientWithoutOverride, err := MatchActiveSourceFile(root, "ambient.go")
	if err != nil {
		t.Fatalf("match ambient GOFLAGS source: %v", err)
	}
	if !ambientWithoutOverride {
		t.Fatal("GOFLAGS tag did not select ambient source")
	}
	ambientWithOverride, err := MatchActiveSourceFile(root, "ambient.go", "explicit")
	if err != nil {
		t.Fatalf("match ambient source with explicit tags: %v", err)
	}
	if ambientWithOverride {
		t.Fatal("GOFLAGS tag remained active after explicit tag override")
	}
	explicit, err := MatchActiveSourceFile(root, "explicit.go", "explicit")
	if err != nil {
		t.Fatalf("match explicit source: %v", err)
	}
	if !explicit {
		t.Fatal("explicit tag did not select explicit source")
	}
}

// TestRunBuildTagsAlignSyntaxAndTypedPackages proves conditional routes and their checked schemas use one invocation build context.
func TestRunBuildTagsAlignSyntaxAndTypedPackages(t *testing.T) {
	root := t.TempDir()
	webRoot := typedFixtureWebRoot(t)
	writeFixtureFiles(t, root, map[string]string{
		"go.mod": "module example.com/taggedindex\n\ngo 1.25.0\n\nrequire github.com/goforj/web v0.0.0\nreplace github.com/goforj/web => " + filepath.ToSlash(webRoot) + "\n",
		"app/routes.go": `package app
import (
	"github.com/goforj/web"
	"example.com/taggedindex/internal/report"
)
func ProvideRoutes(controller *report.Controller) []web.RouteGroup {
	return []web.RouteGroup{web.NewRouteGroup("/api", controller.Routes())}
}
`,
		"internal/report/controller_explicit.go": `//go:build explicit

package report
import (
	"net/http"
	"github.com/goforj/web"
)
type Controller struct{}
type ExplicitResponse struct { Mode string ` + "`json:\"mode\"`" + ` }
func (*Controller) Routes() []web.Route { return []web.Route{web.NewRoute(http.MethodGet, "/explicit", show)} }
func show(ctx web.Context) error { return ctx.JSON(http.StatusOK, ExplicitResponse{Mode: "explicit"}) }
`,
		"internal/report/controller_ambient.go": `//go:build ambient

package report
import (
	"net/http"
	"github.com/goforj/web"
)
type Controller struct{}
type AmbientResponse struct { Mode string ` + "`json:\"mode\"`" + ` }
func (*Controller) Routes() []web.Route { return []web.Route{web.NewRoute(http.MethodGet, "/ambient", show)} }
func show(ctx web.Context) error { return ctx.JSON(http.StatusOK, AmbientResponse{Mode: "ambient"}) }
`,
	})
	t.Setenv("GOFLAGS", "-tags=ambient")

	manifest, err := Run(context.Background(), IndexOptions{
		Root:                 root,
		RouteCompositionPath: "app/routes.go",
		BuildTags:            []string{"explicit"},
	})
	if err != nil {
		t.Fatalf("index explicit build-tag surface: %v", err)
	}
	if len(manifest.Operations) != 1 || manifest.Operations[0].Path != "/api/explicit" {
		t.Fatalf("tag-selected operations = %+v, want /api/explicit", manifest.Operations)
	}
	identities := make([]string, 0, len(manifest.Schemas))
	for _, schema := range manifest.Schemas {
		identities = append(identities, schema.Identity)
	}
	joined := strings.Join(identities, "\n")
	if !strings.Contains(joined, "ExplicitResponse") || strings.Contains(joined, "AmbientResponse") {
		t.Fatalf("tag-selected schemas = %v, want only ExplicitResponse", identities)
	}
}
