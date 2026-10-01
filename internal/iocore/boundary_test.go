package iocore_test

import (
	"bufio"
	"bytes"
	"go/parser"
	"go/token"
	"io/fs"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
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
// facade's adapter imports become a cycle. TestIOImportBoundary widens
// this to every package under internal/io/... (the export/null/set
// helpers included), so a new helper is covered without editing a list.
var belowFacade = []string{
	modulePrefix + "/internal/iocore",
	modulePrefix + "/internal/io",
	modulePrefix + "/internal/io/arrow",
	modulePrefix + "/internal/io/csv",
	modulePrefix + "/internal/io/excel",
	modulePrefix + "/internal/io/jsonarray",
	modulePrefix + "/internal/io/jsonshared",
	modulePrefix + "/internal/io/ndjson",
	modulePrefix + "/internal/io/parquet",
	modulePrefix + "/internal/io/spss",
	modulePrefix + "/internal/io/tsv",
}

// iocoreAllowed is the complete set of intra-module packages the
// contracts leaf may reach. It is a LEAF: anything wider and an adapter
// importing it would drag engine code under the facade. The .pulse codec
// counts as one unit: public encoding, its internal twin and the bridge
// between them.
var iocoreAllowed = map[string]bool{
	modulePrefix + "/internal/iocore":         true,
	modulePrefix + "/encoding":                true,
	modulePrefix + "/internal/encoding":       true,
	modulePrefix + "/internal/encodingbridge": true,
	modulePrefix + "/errors":                  true,
	modulePrefix + "/types":                   true,
}

// goListPackages expands a package pattern into its import paths.
func goListPackages(t *testing.T, pattern string) []string {
	t.Helper()
	out, err := exec.Command("go", "list", pattern).Output()
	if err != nil {
		t.Fatalf("go list %s: %v", pattern, err)
	}
	var pkgs []string
	for _, line := range strings.Split(string(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			pkgs = append(pkgs, line)
		}
	}
	if len(pkgs) == 0 {
		t.Fatalf("go list %s returned nothing", pattern)
	}
	return pkgs
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
				t.Errorf("internal/iocore depends on %s; the contracts leaf may reach only the encoding codec, errors and types", dep)
			}
		}
	}
}

// TestIOImportBoundary_FacadeReachesAdapters keeps the boundary from
// passing vacuously: the facade (through its factory) must really sit above
// the implementation and every adapter, or the check above inspects a graph
// that no longer has the shape it guards.
func TestIOImportBoundary_FacadeReachesAdapters(t *testing.T) {
	have := map[string]bool{}
	for _, dep := range goListDeps(t, publicIO) {
		have[dep] = true
	}
	for _, want := range belowFacade {
		if !have[want] {
			t.Errorf("public io does not reach %s; the facade no longer sits above it", want)
		}
	}
}

// TestIOImportBoundary_NoFileImportsFacade closes the gap go list -deps
// leaves open: a package's dependency graph omits its _test.go imports, so
// a test file under internal/io/... could import the public facade without
// tripping TestIOImportBoundary. This walk parses the import block of EVERY
// .go file under internal/io and internal/iocore, tests included. Tests
// reach removed or internal names through internal/io, never through the
// narrowed facade.
func TestIOImportBoundary_NoFileImportsFacade(t *testing.T) {
	scanned := 0
	for _, pkg := range []string{modulePrefix + "/internal/io", modulePrefix + "/internal/iocore"} {
		out, err := exec.Command("go", "list", "-f", "{{.Dir}}", pkg).Output()
		if err != nil {
			t.Fatalf("go list -f {{.Dir}} %s: %v", pkg, err)
		}
		root := strings.TrimSpace(string(out))
		err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if name := d.Name(); path != root && (name == "testdata" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")) {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") {
				return nil
			}
			f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if err != nil {
				return err
			}
			scanned++
			for _, imp := range f.Imports {
				if p, _ := strconv.Unquote(imp.Path.Value); p == publicIO {
					t.Errorf("%s imports the public io facade; import internal/io or internal/iocore instead", path)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", root, err)
		}
	}
	if scanned == 0 {
		t.Fatal("scanned no .go files; the boundary cannot inspect an empty tree")
	}
}
