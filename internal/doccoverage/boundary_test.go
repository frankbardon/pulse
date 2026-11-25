package doccoverage

import (
	"go/parser"
	"go/token"
	"path"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/afero"
)

// noExecuteDirs are the no-execute descriptor packages (repo-relative)
// that must never import the generator.
var noExecuteDirs = []string{"internal/descriptor", "descriptor"}

const selfImport = ModulePath + "/internal/doccoverage"

// importersOf lists the Go files (test files included) under dirs whose
// import block names banned or a package below it.
func importersOf(fsys afero.Fs, dirs []string, banned string) ([]string, error) {
	var out []string
	fset := token.NewFileSet()
	for _, dir := range dirs {
		infos, err := afero.ReadDir(fsys, dir)
		if err != nil {
			return nil, err
		}
		for _, fi := range infos {
			if fi.IsDir() || !strings.HasSuffix(fi.Name(), ".go") {
				continue
			}
			p := path.Join(dir, fi.Name())
			src, err := afero.ReadFile(fsys, p)
			if err != nil {
				return nil, err
			}
			f, err := parser.ParseFile(fset, p, src, parser.ImportsOnly)
			if err != nil {
				return nil, err
			}
			for _, is := range f.Imports {
				ip := strings.Trim(is.Path.Value, `"`)
				if ip == banned || strings.HasPrefix(ip, banned+"/") {
					out = append(out, p)
				}
			}
		}
	}
	sort.Strings(out)
	return out, nil
}

// TestDocCoverageNotImportedByDescriptor: the generator builds a *Pulse
// and walks the CLI, so it sits above the engine; the no-execute
// descriptor packages must never import it.
func TestDocCoverageNotImportedByDescriptor(t *testing.T) {
	t.Parallel()
	got, err := importersOf(afero.NewBasePathFs(afero.NewOsFs(), "../.."), noExecuteDirs, selfImport)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range got {
		t.Errorf("%s imports %s: the no-execute descriptor packages must not import documentation tooling", p, selfImport)
	}

	// The scan itself sees an import (aliased, in a test file) when one exists.
	mem := afero.NewMemMapFs()
	_ = afero.WriteFile(mem, "descriptor/x_test.go", []byte("package descriptor\n\nimport dc \""+selfImport+"\"\n\nvar _ = dc.PagePath\n"), 0o644)
	_ = afero.WriteFile(mem, "descriptor/y.go", []byte("package descriptor\n\nimport \"strings\"\n\nvar _ = strings.ToLower\n"), 0o644)
	if got, err := importersOf(mem, []string{"descriptor"}, selfImport); err != nil || len(got) != 1 || got[0] != "descriptor/x_test.go" {
		t.Fatalf("importersOf = %v, %v; want [descriptor/x_test.go]", got, err)
	}
}
