package webindex

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// syntheticWarmIndexBudget protects watcher feedback while leaving headroom for slower shared CI runners.
const syntheticWarmIndexBudget = 2 * time.Second

// TestRunSyntheticWarmBudget keeps ordinary development re-indexing comfortably below the watcher feedback budget.
func TestRunSyntheticWarmBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("skip filesystem performance budget in short mode")
	}
	root := t.TempDir()
	if err := writeSyntheticRepo(root, 80, 3); err != nil {
		t.Fatalf("write synthetic repo: %v", err)
	}
	options := IndexOptions{
		Root:            root,
		OutPath:         filepath.Join(root, "build", "api_index.json"),
		DiagnosticsPath: filepath.Join(root, "build", "api_index.diagnostics.json"),
		OpenAPIPath:     filepath.Join(root, "build", "openapi.json"),
	}
	if _, err := Run(context.Background(), options); err != nil {
		t.Fatalf("warm synthetic index: %v", err)
	}

	started := time.Now()
	if _, err := Run(context.Background(), options); err != nil {
		t.Fatalf("measured synthetic index: %v", err)
	}
	if elapsed := time.Since(started); elapsed > syntheticWarmIndexBudget {
		t.Fatalf("warm indexing took %s, budget is %s for 240 routes", elapsed, syntheticWarmIndexBudget)
	}
}

// BenchmarkRunSyntheticMediumRepo tracks the warm indexing cost for a representative generated project.
func BenchmarkRunSyntheticMediumRepo(b *testing.B) {
	benchmarkRunSyntheticRepo(b, 150, 4)
}

// BenchmarkRunSyntheticLargeRepo makes route-discovery scaling regressions visible without loading typed contracts.
func BenchmarkRunSyntheticLargeRepo(b *testing.B) {
	benchmarkRunSyntheticRepo(b, 500, 5)
}

// benchmarkRunSyntheticRepo excludes fixture construction so results describe repeat indexing work.
func benchmarkRunSyntheticRepo(b *testing.B, controllerCount, routesPerController int) {
	b.Helper()
	root := b.TempDir()
	if err := writeSyntheticRepo(root, controllerCount, routesPerController); err != nil {
		b.Fatalf("write synthetic repo: %v", err)
	}
	opts := IndexOptions{
		Root:            root,
		OutPath:         filepath.Join(root, "build", "api_index.json"),
		DiagnosticsPath: filepath.Join(root, "build", "api_index.diagnostics.json"),
		OpenAPIPath:     filepath.Join(root, "build", "openapi.json"),
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Run(context.Background(), opts); err != nil {
			b.Fatalf("Run failed: %v", err)
		}
	}
}

// writeSyntheticRepo creates native web.Context handlers without requiring external fixture dependencies.
func writeSyntheticRepo(root string, controllerCount, routesPerController int) error {
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/bench\n\ngo 1.24\n"), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("APP_NAME=Synthetic Benchmark\n"), 0o644); err != nil {
		return err
	}
	for c := 0; c < controllerCount; c++ {
		pkg := fmt.Sprintf("c%d", c)
		dir := filepath.Join(root, "internal", pkg)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		src := "package " + pkg + "\n\n" +
			"import (\n\t\"net/http\"\n\t\"github.com/goforj/web\"\n)\n\n" +
			"type payload struct { Name string `json:\"name\"`; Enabled bool `json:\"enabled,omitempty\"` }\n" +
			"// Controller owns the synthetic routes for this benchmark package.\n" +
			"type Controller struct{}\n\n" +
			"// Routes returns the synthetic routes measured by the benchmark.\n" +
			"func (c *Controller) Routes() []web.Route {\n\treturn []web.Route{\n"
		for r := 0; r < routesPerController; r++ {
			src += fmt.Sprintf("\t\tweb.NewRoute(http.MethodGet, \"/%s/r%d/:id\", c.H%d),\n", pkg, r, r)
		}
		src += "\t}\n}\n\n"
		for r := 0; r < routesPerController; r++ {
			src += fmt.Sprintf("// H%d exercises native parameter and response analysis.\nfunc (c *Controller) H%d(ctx web.Context) error {\n", r, r) +
				"\tif ctx.Query(\"full\") == \"1\" {\n" +
				"\t\treturn ctx.Text(http.StatusOK, ctx.Param(\"id\")+\":full\")\n" +
				"\t}\n" +
				"\treturn ctx.Text(http.StatusOK, ctx.Param(\"id\"))\n" +
				"}\n\n"
		}
		if err := os.WriteFile(filepath.Join(dir, "controller.go"), []byte(src), 0o644); err != nil {
			return err
		}
	}
	return nil
}
