// Package fmagate is the shared helper behind the FMA-contraction gates
// of the FMA-free packages (linalg/detmath, mcmc, optimize,
// internal/statdist, internal/distlagparams, internal/sem,
// internal/regmmm). Test code only: nothing outside a _test.go file may
// import it (TestFMAGate_TestOnlyImporters).
//
// Go permits `a + b*c` to be contracted into one fused multiply-add;
// arm64 does it and amd64 does not, so an unbarriered product makes a
// result arm64-only. The compiler's own word is the check: the package
// is cross-compiled for linux/arm64 with -gcflags=<pkg>=-d=fmahash=vy,
// which prints one "fmahash triggered" line per contraction it applied.
// The helper owns that cross-compile and the anti-vacuity probe (a
// scratch module holding `a + b*c` must report one, or a renamed flag
// would make every gate silently pass); each caller keeps its own file
// list and assertion over the returned lines.
//
// repo.go adds the repo-wide scans (ScanFMA, BannedCalls) and the
// reviewed allowlist they apply (testdata/numeric-allowlist.txt);
// TestFMAReport_RepoWide and TestBannedMathReport_RepoWide report them.
package fmagate

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// marker is the substring of every contraction line the compiler prints.
const marker = "fmahash triggered"

// probeSrc is the probe module's one contractible multiply-add.
const probeSrc = "package fmaprobe\n\nfunc F(a, b, c float64) float64 { return a + b*c }\n"

var (
	probeOnce sync.Once
	probeErr  error
)

// probe proves, once per process, that the flag still reports a
// contraction; every later call reuses the verdict.
func probe() error {
	probeOnce.Do(func() {
		dir, err := os.MkdirTemp("", "fmaprobe")
		if err != nil {
			probeErr = err
			return
		}
		defer func() { _ = os.RemoveAll(dir) }()
		if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module fmaprobe\n\ngo 1.26\n"), 0o644); err != nil {
			probeErr = err
			return
		}
		if err := os.WriteFile(filepath.Join(dir, "p.go"), []byte(probeSrc), 0o644); err != nil {
			probeErr = err
			return
		}
		lines, err := run(dir, []string{"build"}, "fmaprobe")
		switch {
		case err != nil:
			probeErr = err
		case len(lines) == 0:
			probeErr = fmt.Errorf("the -d=fmahash=vy probe reported no contraction for `a + b*c` on arm64: " +
				"the compiler flag changed and this gate is vacuous")
		}
	})
	return probeErr
}

// Build cross-compiles the package in the test's working directory
// (its own package directory) for linux/arm64 with the contraction
// report on for importPath, and returns the report lines. It fails the
// test when the probe sees no contraction or the build fails.
func Build(t testing.TB, importPath string) []string {
	t.Helper()
	return report(t, []string{"build"}, importPath)
}

// TestBinary is Build over `go test -c`, so the package's in-package
// and external test files are compiled, and reported, too.
func TestBinary(t testing.TB, importPath string) []string {
	t.Helper()
	return report(t, []string{"test", "-c"}, importPath)
}

func report(t testing.TB, verb []string, importPath string) []string {
	t.Helper()
	if err := probe(); err != nil {
		t.Fatal(err)
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	lines, err := run(wd, verb, importPath)
	if err != nil {
		t.Fatal(err)
	}
	return lines
}

// run is `go <verb> -o /dev/null -gcflags=<importPath>=-d=fmahash=vy .`
// in dir for linux/arm64, returning the contraction lines.
func run(dir string, verb []string, importPath string) ([]string, error) {
	args := append(append([]string{}, verb...), "-o", os.DevNull, "-gcflags="+importPath+"=-d=fmahash=vy", ".")
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH=arm64", "CGO_ENABLED=0", "GOFLAGS=")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("cross-compile %s for arm64: %v\n%s", importPath, err, out.String())
	}
	var lines []string
	for l := range strings.SplitSeq(out.String(), "\n") {
		if strings.Contains(l, marker) {
			lines = append(lines, l)
		}
	}
	return lines, nil
}
