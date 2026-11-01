package fmagate

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const importPath = "github.com/frankbardon/pulse/internal/testutil/fmagate"

// TestFMAGate_TestOnlyImporters keeps the helper test-only: no non-test
// file of the module imports it (so no public package can reach it),
// and the FMA gates of every FMA-free package do.
func TestFMAGate_TestOnlyImporters(t *testing.T) {
	t.Parallel()

	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	testers := map[string]bool{}
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if p != root && (strings.HasPrefix(name, ".") || name == "testdata" || name == "vendor") {
				return filepath.SkipDir
			}
			if p != root {
				if _, err := os.Stat(filepath.Join(p, "go.mod")); err == nil {
					return filepath.SkipDir // a nested module cannot import internal/
				}
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") {
			return nil
		}
		f, err := parser.ParseFile(fset, p, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imp := range f.Imports {
			if path, _ := strconv.Unquote(imp.Path.Value); path != importPath {
				continue
			}
			rel, _ := filepath.Rel(root, p)
			if !strings.HasSuffix(p, "_test.go") {
				t.Errorf("%s imports %s outside a _test.go file; the helper is test-only", rel, importPath)
				continue
			}
			testers[filepath.ToSlash(filepath.Dir(rel))] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{
		"linalg/detmath", "mcmc", "optimize", "internal/statdist",
		"internal/distlagparams", "internal/sem", "internal/regmmm", "internal/mmmfixture",
	} {
		if !testers[dir] {
			t.Errorf("%s has no FMA gate through fmagate", dir)
		}
	}
}
