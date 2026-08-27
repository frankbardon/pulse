package spsstest

import (
	"bufio"
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

const (
	modulePrefix = "github.com/frankbardon/pulse"
	selfPkg      = modulePrefix + "/internal/spsstest"
)

// TestSPSSTest_NotInLibraryBuild verifies — rather than asserts in a comment —
// the claim that this package is test-only.
//
// Two independent checks, because they can fail separately:
//
//  1. No non-test file anywhere in the module imports it. `go list` reports
//     ordinary imports separately from test imports, so a package that only
//     uses spsstest from a _test.go file does not show up here.
//  2. It is absent from the transitive dependency graph of the CLI binary and
//     of the library root, which is what "does not ship" actually means: the
//     linker never sees it.
//
// The internal/ path element additionally makes it unimportable from outside
// the module, but that is enforced by the toolchain and needs no test.
func TestSPSSTest_NotInLibraryBuild(t *testing.T) {
	t.Run("no non-test file imports it", func(t *testing.T) {
		out := goList(t, "-f", "{{.ImportPath}}\t{{join .Imports \" \"}}", modulePrefix+"/...")
		checked := 0
		for _, line := range out {
			path, imports, ok := strings.Cut(line, "\t")
			if !ok {
				continue
			}
			checked++
			if path == selfPkg {
				continue // the package's own files are not an import of it
			}
			for _, imp := range strings.Fields(imports) {
				if imp == selfPkg {
					t.Errorf("package %s imports %s from a non-test file. "+
						"This package is a test fixture generator; importing it from production "+
						"code would link a synthetic .sav writer into the shipped binary.", path, selfPkg)
				}
			}
		}
		if checked == 0 {
			t.Fatal("go list returned no packages — the firewall cannot inspect an empty graph")
		}
	})

	for _, target := range []string{modulePrefix, modulePrefix + "/cmd/pulse"} {
		t.Run("absent from the build graph of "+target, func(t *testing.T) {
			deps := goList(t, "-deps", target)
			if len(deps) == 0 {
				t.Fatalf("go list -deps %s returned nothing", target)
			}
			for _, dep := range deps {
				if strings.TrimSpace(dep) == selfPkg {
					t.Errorf("%s transitively depends on %s; the fixture generator must not ship", target, selfPkg)
				}
			}
		})
	}
}

// goList shells out to `go list` and returns its non-empty output lines.
func goList(t *testing.T, args ...string) []string {
	t.Helper()
	cmd := exec.Command("go", append([]string{"list"}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("go list %s failed: %v\nstderr: %s", strings.Join(args, " "), err, stderr.String())
	}
	var lines []string
	sc := bufio.NewScanner(&stdout)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		if line := sc.Text(); strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scanning go list output: %v", err)
	}
	return lines
}
