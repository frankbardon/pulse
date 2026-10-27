package regmmm_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fmahashReport cross-compiles dir's package for GOARCH=arm64 with the
// compiler's contraction report on and returns the report lines.
func fmahashReport(t *testing.T, dir, importPath string) []string {
	t.Helper()
	return fmahashRun(t, dir, "build", importPath)
}

// fmahashRun runs `go <verb>` (build, or test -c) with the contraction
// report on and returns the report lines.
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

// TestMMMHill_NoFMAContraction asks the compiler: the model cross-compiled
// for arm64 (which contracts a*b + c into a fused multiply-add unless the
// product is an explicit float64(...)) must report no contraction, so a
// fit's bits are the same on every architecture. A probe first proves
// the flag still reports one.
func TestMMMHill_NoFMAContraction(t *testing.T) {
	t.Parallel()

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
	if got := fmahashReport(t, wd, "github.com/frankbardon/pulse/internal/regmmm"); len(got) > 0 {
		t.Errorf("the arm64 compiler contracted %d multiply-adds in regmmm; wrap each product in float64(...):\n%s",
			len(got), strings.Join(got, "\n"))
	}
}

// fmahashTest is fmahashReport over the package's test binary, so the
// in-package test files are compiled (and reported) too.
func fmahashTest(t *testing.T, dir, importPath string) []string {
	t.Helper()
	return fmahashRun(t, dir, "test -c", importPath)
}
