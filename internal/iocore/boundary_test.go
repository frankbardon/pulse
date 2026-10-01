package iocore_test

import (
	"bufio"
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

const modulePrefix = "github.com/frankbardon/pulse"

// publicIO is the alias facade. It imports every format adapter (its
// factory builds any of them from a typed Format), so nothing an adapter
// or the implementation depends on may import it back.
const publicIO = modulePrefix + "/io"

// belowFacade lists the packages that sit UNDER the public io facade: the
// contracts leaf, the job implementation, and every format adapter plus
// the shared JSON helper. Each must stay free of the facade, or the
// facade's adapter imports become a cycle.
var belowFacade = []string{
	modulePrefix + "/internal/iocore",
	modulePrefix + "/internal/io",
	modulePrefix + "/io/arrow",
	modulePrefix + "/io/csv",
	modulePrefix + "/io/excel",
	modulePrefix + "/io/jsonarray",
	modulePrefix + "/io/jsonshared",
	modulePrefix + "/io/ndjson",
	modulePrefix + "/io/parquet",
	modulePrefix + "/io/spss",
	modulePrefix + "/io/tsv",
}

// iocoreAllowed is the complete set of intra-module packages the
// contracts leaf may reach. It is a LEAF: anything wider and an adapter
// importing it would drag engine code under the facade.
var iocoreAllowed = map[string]bool{
	modulePrefix + "/internal/iocore": true,
	modulePrefix + "/encoding":        true,
	modulePrefix + "/errors":          true,
	modulePrefix + "/types":           true,
}

func goListDeps(t *testing.T, pkg string) []string {
	t.Helper()
	cmd := exec.Command("go", "list", "-deps", pkg)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("go list -deps %s: %v\nstderr: %s", pkg, err, stderr.String())
	}
	var deps []string
	sc := bufio.NewScanner(&stdout)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" {
			deps = append(deps, line)
		}
	}
	if len(deps) == 0 {
		t.Fatalf("go list -deps %s returned nothing; the boundary cannot inspect an empty graph", pkg)
	}
	return deps
}

// TestIOImportBoundary pins the direction the io split depends on: the
// public io facade sits ABOVE the adapters and the implementation, never
// below them. It fails if any package under the facade reaches the facade,
// and if the iocore leaf reaches anything in the module beyond encoding,
// errors and types.
func TestIOImportBoundary(t *testing.T) {
	for _, pkg := range belowFacade {
		for _, dep := range goListDeps(t, pkg) {
			if dep == publicIO {
				t.Errorf("%s depends on the public io facade; import internal/iocore (contracts) or internal/io (jobs) instead", pkg)
			}
		}
	}
	for _, dep := range goListDeps(t, modulePrefix+"/internal/iocore") {
		if strings.HasPrefix(dep, modulePrefix+"/") || dep == modulePrefix {
			if !iocoreAllowed[dep] {
				t.Errorf("internal/iocore depends on %s; the contracts leaf may reach only encoding, errors and types", dep)
			}
		}
	}
}

// TestIOImportBoundary_FacadeReachesAdapters keeps the boundary from
// passing vacuously: the facade must really sit above the implementation,
// or the check above inspects a graph that no longer has the shape it
// guards.
func TestIOImportBoundary_FacadeReachesAdapters(t *testing.T) {
	have := map[string]bool{}
	for _, dep := range goListDeps(t, publicIO) {
		have[dep] = true
	}
	for _, want := range []string{modulePrefix + "/internal/io", modulePrefix + "/internal/iocore"} {
		if !have[want] {
			t.Errorf("public io does not reach %s; the facade no longer sits above it", want)
		}
	}
}
