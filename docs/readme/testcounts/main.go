//go:build ignore
// +build ignore

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	testCountStart = "<!-- test-count:embed:start -->"
	testCountEnd   = "<!-- test-count:embed:end -->"
)

func main() {
	if err := run(); err != nil {
		fmt.Println("Error:", err)
		os.Exit(1)
	}
	fmt.Println("✔ Test badges updated from executed test runs")
}

func run() error {
	root, err := findRoot()
	if err != nil {
		return err
	}

	unitCount, err := countRunEvents(root)
	if err != nil {
		return err
	}

	readmePath := filepath.Join(root, "README.md")
	data, err := os.ReadFile(readmePath)
	if err != nil {
		return err
	}

	out, err := replaceSection(string(data), testCountStart, testCountEnd, renderBadges(unitCount))
	if err != nil {
		return err
	}

	return os.WriteFile(readmePath, []byte(out), 0o644)
}

func findRoot() (string, error) {
	wd, _ := os.Getwd()
	for _, c := range []string{wd, filepath.Join(wd, ".."), filepath.Join(wd, "..", ".."), filepath.Join(wd, "..", "..", "..")} {
		c = filepath.Clean(c)
		if fileExists(filepath.Join(c, "go.mod")) && fileExists(filepath.Join(c, "README.md")) && fileExists(filepath.Join(c, "router.go")) {
			return c, nil
		}
	}
	return "", fmt.Errorf("could not find project root")
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func countRunEvents(root string) (int, error) {
	cmd := exec.Command("go", "test", "./...", "-run", "Test|Example", "-count=1", "-json")
	cmd.Dir = root

	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	if err := cmd.Run(); err != nil {
		return 0, fmt.Errorf("go test ./... -json: %w\n%s", err, out.String())
	}

	var total int
	dec := json.NewDecoder(bytes.NewReader(out.Bytes()))
	for dec.More() {
		var event struct {
			Action string `json:"Action"`
			Test   string `json:"Test"`
		}
		if err := dec.Decode(&event); err != nil {
			return 0, err
		}
		if event.Action == "run" && event.Test != "" {
			total++
		}
	}
	return total, nil
}

func renderBadges(unitCount int) string {
	return strings.Join([]string{
		fmt.Sprintf(`<img src="https://img.shields.io/badge/unit_tests-%d-brightgreen" alt="Unit tests (executed count)">`, unitCount),
		`<img src="https://img.shields.io/badge/integration_tests-0-blue" alt="Integration tests (executed count)">`,
	}, "\n")
}

func replaceSection(input, start, end, replacement string) (string, error) {
	si := strings.Index(input, start)
	ei := strings.Index(input, end)
	if si < 0 || ei < 0 || ei < si {
		return "", fmt.Errorf("missing marker pair %q ... %q", start, end)
	}
	var out strings.Builder
	out.WriteString(input[:si+len(start)])
	out.WriteString("\n")
	out.WriteString(replacement)
	out.WriteString("\n")
	out.WriteString(input[ei:])
	return out.String(), nil
}
