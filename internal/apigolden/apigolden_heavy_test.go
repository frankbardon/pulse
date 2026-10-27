//go:build heavy

package apigolden

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPublicAPIGolden freezes the exported shape of every public
// (non-internal/, non-cmd/) package in the module, root aliases expanded
// into their targets' fields and method sets. Regenerate with
//
//	go test ./internal/apigolden/ -run TestPublicAPIGolden -update
//
// ONLY when you intentionally change the public surface; the diff is the
// surface delta reviewers read.
func TestPublicAPIGolden(t *testing.T) {
	pkgs, err := Load(filepath.Join("..", ".."), modulePath)
	if err != nil {
		t.Fatalf("loading public packages: %v", err)
	}
	if len(pkgs) == 0 {
		t.Fatal("no public packages loaded")
	}
	got := Render(modulePath, pkgs)

	// Determinism: a second render of the same packages is byte-identical.
	if again := Render(modulePath, pkgs); string(again) != string(got) {
		t.Fatal("Render is not deterministic across calls")
	}

	goldenPath := filepath.Join("testdata", goldenName)
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatalf("creating testdata: %v", err)
		}
		if err := os.WriteFile(goldenPath, appendHash(got), 0o644); err != nil {
			t.Fatalf("writing golden: %v", err)
		}
		return
	}
	content, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("reading golden %s: %v (run with -update to create)", goldenPath, err)
	}
	want, _ := splitHash(content)
	if string(got) != string(want) {
		t.Errorf("public API surface changed; if intentional, regenerate with "+
			"`go test ./internal/apigolden/ -run TestPublicAPIGolden -update`.\n%s",
			lineDiff(string(want), string(got)))
	}
}

// TestRootSurfaceNamesNoProcessing pins the extension-contract boundary:
// no exported root-package (pulse) signature names a type from the
// engine — internal/processing or its feature/ and window/
// subpackages (public before U02b moved them). Extension authors
// reach every operator contract through the public extend package.
func TestRootSurfaceNamesNoProcessing(t *testing.T) {
	pkgs, err := Load(filepath.Join("..", ".."), modulePath)
	if err != nil {
		t.Fatalf("loading public packages: %v", err)
	}
	rootLines := 0
	for _, line := range strings.Split(string(Render(modulePath, pkgs)), "\n") {
		if !strings.HasPrefix(line, "pulse: ") {
			continue
		}
		rootLines++
		if strings.Contains(line, "/processing") {
			t.Errorf("root surface names an engine type: %s", line)
		}
	}
	if rootLines == 0 {
		t.Fatal("no root-package lines rendered")
	}
}
