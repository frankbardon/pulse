package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// fuzzFixture: targets in the root and ./a (one heavy-tagged), plus
// look-alikes that are not targets and dirs every scan skips.
func fuzzFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, root, "x_test.go", fastHdr+"func FuzzRoot(f *testing.F) {}\nfunc Fuzzlower(f *testing.F) {}\nfunc FuzzWrongParam(t *testing.T) {}\nfunc TestNotFuzz(f *testing.F) {}\n")
	writeFile(t, root, "a/a_heavy_test.go", heavyHdr+"func FuzzHeavy(f *testing.F) {}\n")
	writeFile(t, root, "a/b/b_test.go", fastHdr+"type s struct{}\n\nfunc (s) FuzzMethod(f *testing.F) {}\n\nconst src = `\nfunc FuzzInString(f *testing.F) {}\n`\n\nfunc Fuzz(f *testing.F) {}\n")
	writeFile(t, root, "a/fuzz.go", "package p\n\nimport \"testing\"\n\nfunc FuzzNotATestFile(f *testing.F) {}\n")
	writeFile(t, root, "a/testdata/t_test.go", fastHdr+"func FuzzData(f *testing.F) {}\n")
	writeFile(t, root, "nested/go.mod", "module nested\n")
	writeFile(t, root, "nested/n_test.go", fastHdr+"func FuzzNested(f *testing.F) {}\n")
	return root
}

func TestScanFuzzTargets_Fixture(t *testing.T) {
	t.Parallel()
	got, err := scanFuzzTargets(fuzzFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	want := []fuzzTarget{{".", "FuzzRoot"}, {"./a", "FuzzHeavy"}, {"./a/b", "Fuzz"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("scan = %v, want %v", got, want)
	}
}

func TestParseFuzzMatrix(t *testing.T) {
	t.Parallel()
	src := []byte(`jobs:
  fuzz:
    strategy:
      matrix:
        # - { pkg: ".", target: FuzzCommented }
        include:
          - { pkg: ".", target: FuzzRoot }
          -   {pkg: "./a",target: FuzzHeavy}
    steps:
      - run: echo "${{ matrix.target }}"
`)
	got, err := parseFuzzMatrix(src)
	if err != nil {
		t.Fatal(err)
	}
	want := []fuzzTarget{{".", "FuzzRoot"}, {"./a", "FuzzHeavy"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("matrix = %v, want %v", got, want)
	}
	for name, bad := range map[string]string{
		"block-style leg": "include:\n  - pkg: \".\"\n    target: FuzzRoot\n",
		"unquoted pkg":    "  - { pkg: ., target: FuzzRoot }\n",
		"no legs":         "jobs: {}\n",
	} {
		if _, err := parseFuzzMatrix([]byte(bad)); err == nil {
			t.Errorf("%s: parsed without error", name)
		}
	}
}

func TestFuzzProblems(t *testing.T) {
	t.Parallel()
	tree := []fuzzTarget{{".", "FuzzA"}, {"./p", "FuzzB"}}
	if probs := fuzzProblems(tree, tree); len(probs) != 0 {
		t.Fatalf("in-sync matrix: %v", probs)
	}
	probs := fuzzProblems(tree, []fuzzTarget{{".", "FuzzA"}, {".", "FuzzA"}, {"./q", "FuzzB"}})
	joined := strings.Join(probs, "\n")
	for _, want := range []string{"leg . FuzzA is listed twice", "leg ./q FuzzB names no fuzz target", "fuzz target ./p FuzzB has no matrix leg"} {
		if !strings.Contains(joined, want) {
			t.Errorf("problems lack %q:\n%s", want, joined)
		}
	}
	if len(probs) != 3 {
		t.Fatalf("want 3 problems, got %d:\n%s", len(probs), joined)
	}
}

func TestFuzzCheck_Fixture(t *testing.T) {
	t.Parallel()
	root := fuzzFixture(t)
	wf := filepath.Join(root, filepath.FromSlash(fuzzWorkflow))
	writeFile(t, root, fuzzWorkflow, "include:\n  - { pkg: \".\", target: FuzzRoot }\n  - { pkg: \"./a\", target: FuzzHeavy }\n")
	if err := fuzzCheck(root, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "./a/b Fuzz has no matrix leg") {
		t.Fatalf("missing leg: err = %v", err)
	}
	if err := os.WriteFile(wf, []byte("include:\n  - { pkg: \".\", target: FuzzRoot }\n  - { pkg: \"./a\", target: FuzzHeavy }\n  - { pkg: \"./a/b\", target: Fuzz }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := fuzzCheck(root, &out); err != nil {
		t.Fatalf("in sync: %v", err)
	}
	if !strings.Contains(out.String(), "3 targets") {
		t.Fatalf("output = %q", out.String())
	}
}

// TestRepoFuzzMatrix_InSync: .github/workflows/fuzz.yml has exactly one
// leg per fuzz target in the module (`citest fuzz-check`).
func TestRepoFuzzMatrix_InSync(t *testing.T) {
	t.Parallel()
	root := filepath.Join("..", "..", "..")
	if err := fuzzCheck(root, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	tree, err := scanFuzzTargets(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(tree) < 10 {
		t.Fatalf("found %d fuzz targets: scan is broken", len(tree))
	}
}
