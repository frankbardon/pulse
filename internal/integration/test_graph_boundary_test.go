package integration_test

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

// testGraphTargets are the packages whose test graphs must stay CLI-free:
// the root package (its in-package tests and the MCP bridge external test
// file) and internal/integration, which holds the black-box suite that
// used to be the root's external test package. No embedder imports
// internal/integration, so its graph cannot reach an embedder's go.sum;
// it is gated anyway so a CLI leg keeps landing in internal/cli's tests
// rather than drifting into the integration suite.
var testGraphTargets = []struct {
	pkg      string // go list argument (cwd is the repository root)
	testMain string // the generated test-main package proving the graph was listed
}{
	{".", "github.com/frankbardon/pulse.test"},
	{"./internal/integration", "github.com/frankbardon/pulse/internal/integration.test"},
}

// TestRootTestGraphExcludesCLI walks `go list -tags heavy -deps -test` for
// each target (its _test.go files of both tiers and everything they reach
// transitively) and fails if any CLI-only package is in it.
func TestRootTestGraphExcludesCLI(t *testing.T) {
	t.Parallel()

	for _, target := range testGraphTargets {
		out, err := exec.Command("go", "list", "-tags", "heavy", "-deps", "-test", target.pkg).CombinedOutput()
		if err != nil {
			t.Fatalf("go list -deps -test %s: %v\n%s", target.pkg, err, out)
		}
		sawTestMain := false
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			pkg := strings.TrimSpace(line)
			if pkg == target.testMain {
				sawTestMain = true
			}
			for _, banned := range cliOnlyImports {
				if strings.HasPrefix(pkg, banned) {
					t.Errorf("%s test graph reaches %q; move the CLI leg into internal/cli's tests", target.pkg, pkg)
				}
			}
		}
		if !sawTestMain {
			t.Fatalf("go list output for %s carries no %s; the check would be vacuous:\n%s", target.pkg, target.testMain, out)
		}
	}
}
