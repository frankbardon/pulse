package integration_test

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/internal/testhook"
)

// update is this test binary's -update flag (story goldens read it through
// updateGoldens). The root package declares its own in
// docs_generated_current_test.go; the two binaries are separate, so each
// declares one.
var update = flag.Bool("update", false, "rewrite the story goldens from the current runtime")

// modulePath is the module whose root the integration tests run from.
const modulePath = "github.com/frankbardon/pulse"

// TestMain runs every integration test from the repository root and forces
// operation panic recovery OFF for every *Pulse this binary builds through
// New.
//
// The working directory: these tests used to live in the root package, and
// their repo-relative paths (testdata/, docs/, internal/skills, .claude/,
// CLAUDE.md, whole-tree walkers rooted at ".") and golden writes assume the
// repository root as cwd. TestMain walks up to the go.mod declaring
// modulePath and changes into it, so a path never silently resolves against
// internal/integration.
//
// Panic recovery: an engine panic must crash the ordinary test that hit it,
// never surface as a recovered error a test could silently tolerate — the
// same guard the root package's testmain_test.go installs for its binary.
func TestMain(m *testing.M) {
	root, err := findModuleRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, "integration TestMain:", err)
		os.Exit(2)
	}
	if err := os.Chdir(root); err != nil {
		fmt.Fprintln(os.Stderr, "integration TestMain: chdir:", err)
		os.Exit(2)
	}
	testhook.ForcePanicRecoveryOff(true)
	os.Exit(m.Run())
}

// findModuleRoot walks up from the working directory to the directory whose
// go.mod declares modulePath. A nested module's go.mod (another module path)
// is stepped over.
func findModuleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if declaresModule(filepath.Join(dir, "go.mod")) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no go.mod declaring module %s above the working directory", modulePath)
		}
		dir = parent
	}
}

func declaresModule(gomod string) bool {
	f, err := os.Open(gomod)
	if err != nil {
		return false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if rest, ok := strings.CutPrefix(line, "module "); ok {
			return strings.Trim(strings.TrimSpace(rest), `"`) == modulePath
		}
	}
	return false
}

// TestMainForcesPanicRecoveryOff pins TestMain's guard: every instance this
// binary builds has operation panic recovery disabled, so an engine panic
// crashes the test that hit it.
func TestMainForcesPanicRecoveryOff(t *testing.T) {
	t.Parallel()

	p, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if !testhook.Service(p).PanicRecoveryDisabled() {
		t.Fatal("integration TestMain left operation panic recovery ON")
	}
}

// TestMainRunsFromModuleRoot pins TestMain's chdir: the repo-relative
// paths every walker and golden in this package uses resolve against the
// module root, never internal/integration.
func TestMainRunsFromModuleRoot(t *testing.T) {
	t.Parallel()

	if !declaresModule("go.mod") {
		t.Fatalf("working directory has no go.mod declaring %s", modulePath)
	}
	if _, err := os.Stat("CLAUDE.md"); err != nil {
		t.Fatalf("working directory is not the repository root: %v", err)
	}
}
