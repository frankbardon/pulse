package pulse_test

import (
	"os/exec"
	"strings"
	"testing"
)

// cliOnlyImports are the package trees the root package's TEST graph must
// never reach. A module importing pulse (any embedder, the smoke module
// included) resolves the test dependencies of the packages it imports
// under `go mod tidy`, so a root _test.go importing internal/cli pulls
// the CLI framework into that module's go.sum. CLI end-to-end legs
// belong in internal/cli's own tests.
var cliOnlyImports = []string{
	"github.com/frankbardon/pulse/internal/cli",
	"github.com/frankbardon/pulse/cmd/",
	"github.com/urfave/cli",
}

// TestRootTestGraphExcludesCLI walks `go list -tags heavy -deps -test .`
// for the root package (its in-package and external _test.go files of
// both test tiers and everything they reach transitively) and fails if
// any CLI-only package is in it.
func TestRootTestGraphExcludesCLI(t *testing.T) {
	t.Parallel()

	out, err := exec.Command("go", "list", "-tags", "heavy", "-deps", "-test", ".").CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps -test .: %v\n%s", err, out)
	}
	sawRoot := false
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		pkg := strings.TrimSpace(line)
		if strings.HasPrefix(pkg, "github.com/frankbardon/pulse_test ") || pkg == "github.com/frankbardon/pulse.test" {
			sawRoot = true
		}
		for _, banned := range cliOnlyImports {
			if strings.HasPrefix(pkg, banned) {
				t.Errorf("root-package test graph reaches %q; move the CLI leg into internal/cli's tests", pkg)
			}
		}
	}
	if !sawRoot {
		t.Fatalf("go list output carries no root test package; the check would be vacuous:\n%s", out)
	}
}
