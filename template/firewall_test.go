package template_test

import (
	"bufio"
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

// modulePrefix is this module's import-path root. Every dependency under
// it is intra-module and therefore subject to the allow-list below.
const modulePrefix = "github.com/frankbardon/pulse"

// allowedIntraModuleDeps is the complete set of intra-module packages the
// template package may depend on, transitively. The template package is a
// document model plus a renderer: it reads declarations, substitutes
// values, and decodes into a request struct. It never executes anything,
// so it needs the public request/response shapes and the coded-error
// system and nothing else.
//
// Keeping the ceiling this low is the point. It keeps the package
// dependency-light, keeps template rendering free of any execution
// pathway, and means an embedder pulling in template does not drag the
// processing engine along with it.
var allowedIntraModuleDeps = map[string]bool{
	modulePrefix + "/template": true, // the package itself
	modulePrefix + "/types":    true, // public request roots
	modulePrefix + "/errors":   true, // CodedError system
}

// forbiddenDeps are the packages named explicitly in the import ceiling.
// They are a subset of "anything not on the allow-list", listed separately
// so a breach reports the intent that was violated rather than a bare
// allow-list miss.
var forbiddenDeps = []string{
	modulePrefix + "/descriptor",
	modulePrefix + "/processing",
	modulePrefix + "/service",
}

// listDeps shells out to `go list -deps` and returns the full transitive
// dependency set of a package (the package itself included).
func listDeps(t *testing.T, pkg string) []string {
	t.Helper()
	cmd := exec.Command("go", "list", "-deps", pkg)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("go list -deps %s failed: %v\nstderr: %s", pkg, err, stderr.String())
	}
	var deps []string
	sc := bufio.NewScanner(&stdout)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" {
			deps = append(deps, line)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan go list output for %s: %v", pkg, err)
	}
	if len(deps) == 0 {
		t.Fatalf("go list -deps %s returned no dependencies — the firewall cannot inspect an empty graph", pkg)
	}
	return deps
}

// TestTemplatePackage_ImportBoundary is the import firewall for the
// template package: every intra-module dependency in its transitive graph
// must sit on the allow-list, and the three explicitly banned packages
// must be absent. A future edit that reaches for the processing registry
// or the service orchestrator to "just check one thing" fails here.
func TestTemplatePackage_ImportBoundary(t *testing.T) {
	deps := listDeps(t, modulePrefix+"/template")

	have := make(map[string]bool, len(deps))
	for _, dep := range deps {
		have[dep] = true
		if !strings.HasPrefix(dep, modulePrefix) {
			continue // stdlib and third-party are unconstrained
		}
		if !allowedIntraModuleDeps[dep] {
			t.Errorf("FIREWALL BREACH: template/ transitively imports %q, which is not on the allow-list. "+
				"The template package may depend only on %s/types and %s/errors.",
				dep, modulePrefix, modulePrefix)
		}
	}

	for _, banned := range forbiddenDeps {
		if have[banned] {
			t.Errorf("FIREWALL BREACH: template/ transitively imports forbidden package %q. "+
				"Template rendering is declaration-and-substitution only; it must never reach an execution pathway.", banned)
		}
	}
}
