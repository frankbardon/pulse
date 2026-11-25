package pulse

import (
	"go/ast"
	"go/doc"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// facadeExamples pins the runnable GoDoc task examples an embedder sees
// in `go doc`: example function name -> package directory. Each must
// exist and carry a checked `// Output:` unless it is listed in
// compileOnlyExamples. An example found in one of these directories but
// missing here also fails, so a new example is pinned the day it lands
// and the set can only grow.
var facadeExamples = map[string]string{
	// Setup, reading and analysis (example_analysis_test.go).
	"ExampleNew":                    ".",
	"ExamplePulse_Open":             ".",
	"ExamplePulse_NewCohortBuilder": ".",
	"ExamplePulse_Process":          ".",
	"ExamplePulse_Compose":          ".",
	"ExamplePulse_Facet":            ".",
	"ExamplePulse_ProcessStream":    ".",
	"ExamplePulse_Predict":          ".",
	"ExamplePulse_Inspect":          ".",
	"ExamplePulse_Recommend":        ".",
	"ExamplePulse_Explain":          ".",
	// Data in and out (example_io_test.go).
	"ExamplePulse_Import": ".",
	"ExamplePulse_Export": ".",
	"ExamplePulse_Lookup": ".",
	"ExamplePulse_Synth":  ".",
	// Extending and operating (example_extend_test.go and the packages).
	"ExampleExtensions":           ".",
	"ExamplePulse_RenderTemplate": ".",
	"ExampleOptions":              ".",
	"ExampleHooks":                "observe",
	"ExampleServe":                "mcpserve",
}

// compileOnlyExamples are pinned examples that block forever when run
// (a served transport), so they compile without an Output comment.
var compileOnlyExamples = map[string]bool{
	"ExampleServe": true,
}

// TestFacadeExamplesPresent fails when a pinned task example is deleted
// or renamed, loses its checked output, or when an example is added to
// a pinned directory without being pinned.
func TestFacadeExamplesPresent(t *testing.T) {
	t.Parallel()

	dirs := map[string]bool{}
	for _, dir := range facadeExamples {
		dirs[dir] = true
	}
	found := map[string]string{} // name -> dir
	for dir := range dirs {
		for _, ex := range examplesIn(t, dir) {
			name := "Example" + ex.Name // go/doc drops the prefix
			found[name] = dir
			want, pinned := facadeExamples[name]
			switch {
			case !pinned || want != dir:
				t.Errorf("%s in %s is not pinned: add it to facadeExamples", name, dir)
			case compileOnlyExamples[name]:
				if ex.Output != "" || ex.EmptyOutput {
					t.Errorf("%s is listed compile-only but has an Output comment", name)
				}
			case ex.Output == "" && !ex.EmptyOutput:
				t.Errorf("%s in %s has no // Output: comment, so go test never checks it", name, dir)
			}
		}
	}
	var missing []string
	for name, dir := range facadeExamples {
		if found[name] != dir {
			missing = append(missing, name+" ("+dir+")")
		}
	}
	sort.Strings(missing)
	for _, m := range missing {
		t.Errorf("pinned example %s is missing; restore it or record why the set changed", m)
	}
}

// examplesIn returns the Example functions declared in dir's _test.go
// files, as go doc and go test see them.
func examplesIn(t *testing.T, dir string) []*doc.Example {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(dir, "*_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var files []*ast.File
	for _, path := range paths {
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(src), "func Example") {
			continue
		}
		f, err := parser.ParseFile(fset, path, src, parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, f)
	}
	if len(files) == 0 {
		t.Errorf("no example files in %s", dir)
	}
	return doc.Examples(files...)
}
