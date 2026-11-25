package main

import (
	"strings"
	"testing"
)

// runnersFixture is a miniature tree with one site (and one near miss)
// per whole-repo rule: every-example runners and call-site floors.
var runnersFixture = map[string]string{
	"internal/examples/library.go": `package examples

func All() []int { return nil }
`,
	"encoding/schema.go": `package encoding

func ReadSchema(v byte) error { return nil }
func Helper() error { return nil }
`,
	"internal/io/reader.go": `package io

import "github.com/frankbardon/pulse/encoding"

func readOne() error { return encoding.ReadSchema(1) }
func other() error { return encoding.Helper() }
`,
	"internal/integration/runner_test.go": `package integration_test

import (
	"path/filepath"
	"testing"

	"github.com/frankbardon/pulse/internal/examples"
)

func TestEveryExampleDirect(t *testing.T) { _ = examples.All() }

func TestEveryExampleViaHelper(t *testing.T) { _ = allBodies() }

func allBodies() []int { return examples.All() }

func TestWalksTheTree(t *testing.T) {
	_, _ = filepath.Glob(filepath.Join("internal", "examples", dir(), "*.json"))
}

func dir() string { return "x" }

// near miss: one named example, not every one
func TestOneExample(t *testing.T) {
	_, _ = filepath.Glob(filepath.Join("internal", "examples", "crosstab", "01_x.json"))
}
`,
	"internal/examples/library_test.go": `package examples

import "testing"

func TestInPackage(t *testing.T) { _ = All() }
`,
	"format_gate_test.go": `package pulse

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"testing"
)

func TestReadSchemaFloor(t *testing.T) {
	readers := map[string]bool{"ReadSchema": true, "NotAModuleFunc": true}
	_ = filepath.WalkDir(".", func(p string, d fs.DirEntry, err error) error {
		_, _ = parser.ParseFile(token.NewFileSet(), p, nil, 0)
		return nil
	})
	_ = readers
}

// near miss: names a module function but walks no tree
func TestHelperUnit(t *testing.T) { _ = "Helper"; _, _ = parser.ParseFile(token.NewFileSet(), "x.go", nil, 0) }
`,
}

func TestExampleRunners_Rules(t *testing.T) {
	t.Parallel()
	r := fixtureRepo(t, runnersFixture)
	got := r.exampleRunners()
	want := map[string]string{
		"internal/integration": "TestEveryExampleDirect,TestEveryExampleViaHelper,TestWalksTheTree",
		"internal/examples":    "TestInPackage",
	}
	if len(got) != len(want) {
		t.Errorf("runners = %v, want %v", got, want)
	}
	for d, w := range want {
		if g := strings.Join(got[d], ","); g != w {
			t.Errorf("runners in %s = %s, want %s (TestOneExample reads one example)", d, g, w)
		}
	}
}

func TestFloorSection_Rules(t *testing.T) {
	t.Parallel()
	r := fixtureRepo(t, runnersFixture)
	fl := r.callFloors()
	if len(fl) != 1 || fl[0] != (floor{Dir: ".", Test: "TestReadSchemaFloor", Callee: "ReadSchema"}) {
		t.Fatalf("floors = %+v, want only TestReadSchemaFloor on ReadSchema (TestHelperUnit walks no tree)", fl)
	}
	// A listed file calling the callee reaches the floor, whichever of
	// its declarations is the site.
	sec, gates, ok := r.floorSection(map[string]bool{"internal/io/reader.go\x00func other": true})
	if !ok || len(sec.Sites) != 1 || sec.Sites[0].Path != "internal/io/reader.go" || sec.Sites[0].Symbols[0].Name != "func readOne" ||
		!strings.Contains(sec.Sites[0].Symbols[0].Note, "TestReadSchemaFloor") {
		t.Errorf("floor section = %+v, want internal/io/reader.go · func readOne naming TestReadSchemaFloor", sec)
	}
	if len(gates) != 1 || gates[0].Package != "." || strings.Join(gates[0].Tests, ",") != "TestReadSchemaFloor" {
		t.Errorf("floor gates = %+v", gates)
	}
	// A listed file that never calls it (declaring it is no call)
	// reaches nothing.
	if _, _, ok := r.floorSection(map[string]bool{"encoding/schema.go\x00func Helper": true}); ok {
		t.Error("a file that never calls ReadSchema reached its floor")
	}
}

func TestWholeRepo_RunnersOnlyForExamples(t *testing.T) {
	t.Parallel()
	r := fixtureRepo(t, runnersFixture)
	rep := &Report{}
	r.wholeRepo(rep, map[string]bool{"engine": true}, nil)
	if len(rep.Gates) != 0 {
		t.Errorf("no example site, yet gates %+v", rep.Gates)
	}
	r.wholeRepo(rep, map[string]bool{"examples": true}, nil)
	if len(rep.Gates) != 2 || !strings.Contains(rep.Gates[0].Why, "whole-repo runners") {
		t.Errorf("an example site must list the runners: %+v", rep.Gates)
	}
}

func TestScriptCase(t *testing.T) {
	t.Parallel()
	py := `# Generator for many operators: AGG_X header.
CONST_W = 4  # AGG_X's width

TABLE = [
    ("t_split", {"type": "AGG_X"}),
]

def request(case):
    if case == "ov_a":
        return {"kind": "AGG_X"}
    if case == "ov_b":
        body = [
            {"kind": "AGG_X"},
        ]
        return body
`
	r := `out <- function(case, v) cat(case, v)

# AGG_X one-sample.
tt <- 1
out("t_one", tt)

fit <- function(d) {
  x <- "AGG_X"
}
write_golden("x", list(primitive = "AGG_X"))
`
	for _, tc := range []struct {
		name, src string
		line      int
		want      string
	}{
		{"file header", py, 1, "(file header)"},
		{"top-level assignment", py, 2, "CONST_W"},
		{"row key", py, 5, "case t_split"},
		{"case branch", py, 10, "request › case ov_a"},
		{"nested under a case branch", py, 13, "request › case ov_b"},
		{"comment heads a block; a one-line function encloses nothing", r, 3, "case t_one"},
		{"inside an R function", r, 8, "fit"},
		{"after the R function closed", r, 10, "(top level)"},
	} {
		if got := scriptCase([]byte(tc.src), tc.line); got != tc.want {
			t.Errorf("%s: scriptCase(L%d) = %q, want %q", tc.name, tc.line, got, tc.want)
		}
	}
	for p, want := range map[string]bool{
		"internal/service/testdata/weight_reference/gen_weight_reference.py": true,
		"scripts/reference/gen_symmetry.R":                                   true,
		"internal/processing/testdata/reference/x.json":                      false,
		"tools/x.py": false,
	} {
		if isScript(p) != want {
			t.Errorf("isScript(%s) = %v, want %v", p, !want, want)
		}
	}
}
