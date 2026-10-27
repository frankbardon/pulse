//go:build heavy

package iocore_test

import (
	"sort"
	"strings"
	"testing"
)

// TestIOImportBoundary pins the direction the io split depends on: the
// public io facade sits ABOVE the adapters and the implementation, never
// below them. It fails if any package under the facade reaches the facade,
// and if the iocore leaf reaches anything in the module beyond encoding,
// errors and types.
func TestIOImportBoundary(t *testing.T) {
	under := map[string]bool{}
	for _, pkg := range belowFacade {
		under[pkg] = true
	}
	for _, pkg := range goListPackages(t, modulePrefix+"/internal/io/...") {
		under[pkg] = true
	}
	for _, want := range []string{
		modulePrefix + "/internal/io/exportoverlay",
		modulePrefix + "/internal/io/nullcell",
		modulePrefix + "/internal/io/settristate",
		modulePrefix + "/internal/io/setwide",
	} {
		if !under[want] {
			t.Errorf("%s is missing from the internal/io/... listing; the boundary no longer covers it", want)
		}
	}
	pkgs := make([]string, 0, len(under))
	for pkg := range under {
		pkgs = append(pkgs, pkg)
	}
	sort.Strings(pkgs)
	for _, pkg := range pkgs {
		for _, dep := range goListDeps(t, pkg) {
			if dep == publicIO {
				t.Errorf("%s depends on the public io facade; import internal/iocore (contracts) or internal/io (jobs) instead", pkg)
			}
		}
	}
	for _, dep := range goListDeps(t, modulePrefix+"/internal/iocore") {
		if strings.HasPrefix(dep, modulePrefix+"/") || dep == modulePrefix {
			if !iocoreAllowed[dep] {
				t.Errorf("internal/iocore depends on %s; the contracts leaf may reach only the encoding codec, errors and types (plus the stdlib-only internal/returnplan leaf types carries)", dep)
			}
		}
	}
}
