package obsprom

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestObspromImportBoundary: the exporter is a leaf over the standard
// library and the public observe package — no Prometheus client, no
// other Pulse package — so the core module stays vendor-free.
func TestObspromImportBoundary(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		af, err := parser.ParseFile(token.NewFileSet(), f, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		checked++
		for _, imp := range af.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			if path == "github.com/frankbardon/pulse/observe" {
				continue
			}
			if strings.Contains(strings.SplitN(path, "/", 2)[0], ".") {
				t.Errorf("%s imports %q; obsprom may import only the standard library and observe", f, path)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no obsprom source files found")
	}
}
