package mcmc_test

import (
	"bytes"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// mcmcAllowed reports whether a non-stdlib import is permitted in
// package mcmc: Pulse's coded errors, the public linear-algebra core and
// the deterministic math kernels — never the engine (internal/processing,
// internal/service) nor any model package, so an embedder importing mcmc
// drags in nothing else.
func mcmcAllowed(path string) bool {
	switch path {
	case "github.com/frankbardon/pulse/errors",
		"github.com/frankbardon/pulse/linalg",
		"github.com/frankbardon/pulse/linalg/detmath":
		return true
	}
	return false
}

// TestMCMC_ImportBoundary fails when a non-test file in mcmc imports
// anything outside the standard library and the allowlist.
func TestMCMC_ImportBoundary(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	checked := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		af, err := parser.ParseFile(fset, f, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		checked++
		for _, imp := range af.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			if first, _, _ := strings.Cut(path, "/"); !strings.Contains(first, ".") {
				continue
			}
			if !mcmcAllowed(path) {
				t.Errorf("%s imports %q; mcmc may import only the standard library, "+
					"pulse/errors, pulse/linalg and pulse/linalg/detmath", f, path)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no mcmc source files found")
	}
}

// fmahashReport cross-compiles dir's package for GOARCH=arm64 with the
// compiler's contraction report on and returns the report lines.
func fmahashReport(t *testing.T, dir, importPath string) []string {
	t.Helper()
	return fmahashRun(t, dir, "build", importPath)
}

// fmahashRun is fmahashReport for `go <verb>`: "build" compiles the
// package, "test -c" also its in-package and external test files.
func fmahashRun(t *testing.T, dir, verb, importPath string) []string {
	t.Helper()
	args := append(strings.Fields(verb), "-o", os.DevNull, "-gcflags="+importPath+"=-d=fmahash=vy", ".")
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH=arm64", "CGO_ENABLED=0", "GOFLAGS=")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("cross-compile for arm64: %v\n%s", err, out.String())
	}
	var lines []string
	for l := range strings.SplitSeq(out.String(), "\n") {
		if strings.Contains(l, "fmahash triggered") {
			lines = append(lines, l)
		}
	}
	return lines
}

// TestMCMC_NoFMAContraction asks the compiler: the package
// cross-compiled for arm64 (which contracts a*b + c into a fused
// multiply-add unless the product is an explicit float64(...)) must
// report no contraction, so the package's bits are the same on every
// architecture. A probe first proves the flag still reports one.
func TestMCMC_NoFMAContraction(t *testing.T) {
	probe := t.TempDir()
	if err := os.WriteFile(filepath.Join(probe, "go.mod"), []byte("module fmaprobe\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(probe, "p.go"), []byte("package fmaprobe\n\nfunc F(a, b, c float64) float64 { return a + b*c }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if len(fmahashReport(t, probe, "fmaprobe")) == 0 {
		t.Fatal("the -d=fmahash=vy probe reported no contraction for `a + b*c`: the flag changed and this gate is vacuous")
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if got := fmahashReport(t, wd, "github.com/frankbardon/pulse/mcmc"); len(got) > 0 {
		t.Errorf("the arm64 compiler contracted %d multiply-adds in mcmc; wrap each product in float64(...):\n%s",
			len(got), strings.Join(got, "\n"))
	}
}

// TestNUTS_TestModelsNoFMAContraction holds the test targets (and every
// other test file) to the package's FMA-free rule: TestNUTS_GoldenHash
// pins draws computed through them, so a contraction in a test model
// would make the golden hash arm64-only. TestMCMC_NoFMAContraction's
// probe proves the flag still reports.
func TestNUTS_TestModelsNoFMAContraction(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if got := fmahashRun(t, wd, "test -c", "github.com/frankbardon/pulse/mcmc"); len(got) > 0 {
		t.Errorf("the arm64 compiler contracted %d multiply-adds in mcmc or its tests; wrap each product in float64(...):\n%s",
			len(got), strings.Join(got, "\n"))
	}
}
