package integration_test

import (
	"bufio"
	"bytes"
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// recipeImports maps each package qualifier a recipe Go snippet may use to
// its import path. A snippet naming a qualifier missing here fails the
// check rather than being silently skipped.
var recipeImports = map[string]string{
	"descriptor": "github.com/frankbardon/pulse/descriptor",
	"errors":     "github.com/frankbardon/pulse/errors",
	"extend":     "github.com/frankbardon/pulse/extend",
	"fmt":        "fmt",
	"pulse":      "github.com/frankbardon/pulse",
	"types":      "github.com/frankbardon/pulse/types",
}

// TestAddingOverlayRecipe_GoSnippetsCompile: every ```go block of
// docs/src/internals/adding-overlay.md type-checks against the public API
// as one package. A block opening with a declaration (`func`, `type`,
// `var`, `const`) is a top-level declaration; any other block is a
// statement list, wrapped in its own function with every `:=` name marked
// used. Imports follow the qualifiers the blocks use (recipeImports).
// Export data comes from `go list -export`, so nothing is written into the
// module. The recipe cannot drift from the API it teaches; its request
// block is pinned beside it (TestAddingOverlayRecipe_RequestRuns).
func TestAddingOverlayRecipe_GoSnippetsCompile(t *testing.T) {
	t.Parallel()

	doc, err := os.ReadFile("docs/src/internals/adding-overlay.md")
	if err != nil {
		t.Fatal(err)
	}
	src, imports, err := assembleRecipeSnippets(doc)
	if err != nil {
		t.Fatal(err)
	}

	exports, err := recipeExportData(imports)
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "recipe.go", src, 0)
	if err != nil {
		t.Fatalf("assembled recipe does not parse: %v\n%s", err, numbered(src))
	}
	var typeErrs []string
	conf := types.Config{
		Importer: importer.ForCompiler(fset, "gc", func(path string) (io.ReadCloser, error) {
			f, ok := exports[path]
			if !ok {
				return nil, fmt.Errorf("no export data for %s", path)
			}
			return os.Open(f)
		}),
		Error: func(err error) { typeErrs = append(typeErrs, err.Error()) },
	}
	_, _ = conf.Check("recipe", fset, []*ast.File{file}, nil)
	if len(typeErrs) > 0 {
		t.Fatalf("recipe Go snippets do not compile:\n  %s\n\nassembled source:\n%s",
			strings.Join(typeErrs, "\n  "), numbered(src))
	}
}

// assembleRecipeSnippets joins a markdown document's ```go blocks into one
// Go source file and returns it with the import paths it needs.
func assembleRecipeSnippets(doc []byte) (string, []string, error) {
	blocks := regexp.MustCompile("(?s)```go\n(.*?)```").FindAllSubmatch(doc, -1)
	if len(blocks) == 0 {
		return "", nil, fmt.Errorf("recipe has no go blocks")
	}
	declOpener := regexp.MustCompile(`^(func|type|var|const)\b`)
	var body strings.Builder
	for i, b := range blocks {
		code := string(b[1])
		if declOpener.MatchString(strings.TrimSpace(code)) {
			body.WriteString(code + "\n")
			continue
		}
		used, err := definedNames(code)
		if err != nil {
			return "", nil, fmt.Errorf("go block %d: %v", i+1, err)
		}
		fmt.Fprintf(&body, "func recipeSnippet%d() {\n%s", i+1, code)
		for _, n := range used {
			fmt.Fprintf(&body, "\t_ = %s\n", n)
		}
		body.WriteString("}\n\n")
	}

	qualifiers, err := usedQualifiers(body.String())
	if err != nil {
		return "", nil, err
	}
	var paths []string
	var src strings.Builder
	src.WriteString("package recipe\n\nimport (\n")
	for _, q := range qualifiers {
		path, ok := recipeImports[q]
		if !ok {
			return "", nil, fmt.Errorf("recipe uses package %q, which recipeImports does not map", q)
		}
		paths = append(paths, path)
		fmt.Fprintf(&src, "\t%s %q\n", q, path)
	}
	src.WriteString(")\n\n")
	src.WriteString(body.String())
	return src.String(), paths, nil
}

// definedNames returns the names a statement list defines with `:=`.
func definedNames(stmts string) ([]string, error) {
	f, err := parser.ParseFile(token.NewFileSet(), "", "package p\nfunc _() {\n"+stmts+"\n}", 0)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, s := range f.Decls[0].(*ast.FuncDecl).Body.List {
		if as, ok := s.(*ast.AssignStmt); ok && as.Tok == token.DEFINE {
			for _, l := range as.Lhs {
				if id, ok := l.(*ast.Ident); ok && id.Name != "_" {
					names = append(names, id.Name)
				}
			}
		}
	}
	return names, nil
}

// usedQualifiers returns the sorted identifiers used as the left side of a
// selector that the code does not declare itself (package qualifiers).
func usedQualifiers(code string) ([]string, error) {
	f, err := parser.ParseFile(token.NewFileSet(), "", "package p\n"+code, 0)
	if err != nil {
		return nil, err
	}
	local := map[string]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.Field:
			for _, id := range n.Names {
				local[id.Name] = true
			}
		case *ast.AssignStmt:
			if n.Tok == token.DEFINE {
				for _, l := range n.Lhs {
					if id, ok := l.(*ast.Ident); ok {
						local[id.Name] = true
					}
				}
			}
		case *ast.ValueSpec:
			for _, id := range n.Names {
				local[id.Name] = true
			}
		}
		return true
	})
	seen := map[string]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok {
			if id, ok := sel.X.(*ast.Ident); ok && !local[id.Name] {
				seen[id.Name] = true
			}
		}
		return true
	})
	out := make([]string, 0, len(seen))
	for q := range seen {
		out = append(out, q)
	}
	sort.Strings(out)
	return out, nil
}

// recipeExportData maps every package the imports reach to its compiler
// export-data file, via `go list -export -deps` from the module root.
func recipeExportData(imports []string) (map[string]string, error) {
	args := append([]string{"list", "-export", "-deps", "-f", "{{.ImportPath}}\t{{.Export}}"}, imports...)
	var stderr bytes.Buffer
	cmd := exec.Command("go", args...)
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list -export: %v\n%s", err, stderr.String())
	}
	exports := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		path, file, ok := strings.Cut(sc.Text(), "\t")
		if ok && file != "" {
			exports[path] = file
		}
	}
	return exports, sc.Err()
}

// numbered prefixes each source line with its 1-based number, matching the
// positions type errors report.
func numbered(src string) string {
	var b strings.Builder
	for i, l := range strings.Split(src, "\n") {
		fmt.Fprintf(&b, "%4d  %s\n", i+1, l)
	}
	return b.String()
}
