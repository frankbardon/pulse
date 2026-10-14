package main

import (
	"bufio"
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const modulePath = "github.com/frankbardon/pulse"

// selfDir is excluded from every listing: the tool's own sources and
// tests name operators only as fixtures.
const selfDir = "internal/tools/sites/"

// repo is the tracked tree (plus untracked, non-ignored files, so a
// story's freshly created files are visible) with lazily loaded
// contents and parsed Go files.
type repo struct {
	root    string
	files   []string // repo-relative, slash-separated, sorted
	content map[string][]byte
	parsed  map[string]*goFile
	fset    *token.FileSet

	testFuncs map[string][]string          // test func name -> package dirs declaring it
	calls     map[string][]callSite        // callee name -> non-test calls (callIndex)
	consts    map[string]map[string]string // dir -> const name -> string value
}

func openRepo(root string) (*repo, error) {
	if root == "" {
		wd, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		root = wd
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	for dir := root; ; dir = filepath.Dir(dir) {
		if b, err := os.ReadFile(filepath.Join(dir, "go.mod")); err == nil && bytes.HasPrefix(b, []byte("module "+modulePath+"\n")) {
			root = dir
			break
		}
		if filepath.Dir(dir) == dir {
			return nil, fmt.Errorf("no %s go.mod above %s", modulePath, root)
		}
	}
	cmd := exec.Command("git", "ls-files", "--cached", "--others", "--exclude-standard", "-z")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-files: %w", err)
	}
	r := &repo{root: root, content: map[string][]byte{}, parsed: map[string]*goFile{}, fset: token.NewFileSet()}
	seen := map[string]bool{}
	for _, p := range strings.Split(string(out), "\x00") {
		if p == "" || seen[p] || skipPath(p) {
			continue
		}
		seen[p] = true
		r.files = append(r.files, p)
	}
	sort.Strings(r.files)
	return r, nil
}

var binaryExt = map[string]bool{
	".pulse": true, ".zip": true, ".zst": true, ".sav": true, ".xlsx": true, ".parquet": true,
	".arrow": true, ".png": true, ".jpg": true, ".gif": true, ".ico": true, ".pdf": true, ".gz": true,
}

func skipPath(p string) bool {
	return strings.HasPrefix(p, ".planning/") || strings.HasPrefix(p, ".git/") ||
		strings.HasPrefix(p, selfDir) || binaryExt[path.Ext(p)]
}

// read returns a file's contents, or nil for a missing or binary file.
func (r *repo) read(p string) []byte {
	if b, ok := r.content[p]; ok {
		return b
	}
	b, err := os.ReadFile(filepath.Join(r.root, filepath.FromSlash(p)))
	if err != nil {
		b = nil
	}
	head := b
	if len(head) > 8000 {
		head = head[:8000]
	}
	if bytes.IndexByte(head, 0) >= 0 {
		b = nil
	}
	r.content[p] = b
	return b
}

// goFile is a parsed Go source with its comment ranges.
type goFile struct {
	file     *ast.File
	base     int // token.Pos of offset 0
	comments [][2]int
}

func (r *repo) parse(p string) *goFile {
	if g, ok := r.parsed[p]; ok {
		return g
	}
	src := r.read(p)
	var g *goFile
	if src != nil {
		f, err := parser.ParseFile(r.fset, p, src, parser.ParseComments|parser.SkipObjectResolution)
		if f != nil && (err == nil || f.Decls != nil) {
			base := r.fset.File(f.Pos()).Base()
			g = &goFile{file: f, base: base}
			for _, cg := range f.Comments {
				g.comments = append(g.comments, [2]int{int(cg.Pos()) - base, int(cg.End()) - base})
			}
		}
	}
	r.parsed[p] = g
	return g
}

func (g *goFile) inComment(off int) bool {
	for _, c := range g.comments {
		if off >= c[0] && off < c[1] {
			return true
		}
	}
	return false
}

// symbolAt names the top-level declaration enclosing a byte offset:
// "func Name", "func (T) Name", "var name", "type Name", "const block
// of Type" (typed constants, so every operator's constant shares one
// symbol) or "const Name".
func (g *goFile) symbolAt(off int) string {
	pos := token.Pos(g.base + off)
	for _, d := range g.file.Decls {
		if pos < d.Pos() || pos >= d.End() {
			// a doc comment belongs to its declaration
			if fd, ok := d.(*ast.FuncDecl); ok && fd.Doc != nil && pos >= fd.Doc.Pos() && pos < fd.Pos() {
				return funcName(fd)
			}
			if gd, ok := d.(*ast.GenDecl); ok && gd.Doc != nil && pos >= gd.Doc.Pos() && pos < gd.Pos() {
				return genName(gd, gd.Pos())
			}
			continue
		}
		switch d := d.(type) {
		case *ast.FuncDecl:
			return funcName(d)
		case *ast.GenDecl:
			return genName(d, pos)
		}
	}
	return "(file comment)"
}

func funcName(fd *ast.FuncDecl) string {
	if fd.Recv != nil && len(fd.Recv.List) == 1 {
		t := fd.Recv.List[0].Type
		if s, ok := t.(*ast.StarExpr); ok {
			t = s.X
		}
		if ix, ok := t.(*ast.IndexExpr); ok {
			t = ix.X
		}
		if id, ok := t.(*ast.Ident); ok {
			return "func (" + id.Name + ") " + fd.Name.Name
		}
	}
	return "func " + fd.Name.Name
}

func genName(d *ast.GenDecl, pos token.Pos) string {
	var spec ast.Spec
	for _, s := range d.Specs {
		if pos >= s.Pos() && pos < s.End() {
			spec = s
			break
		}
	}
	if spec == nil && len(d.Specs) > 0 {
		spec = d.Specs[0]
	}
	switch s := spec.(type) {
	case *ast.TypeSpec:
		return "type " + s.Name.Name
	case *ast.ValueSpec:
		if d.Tok == token.CONST {
			if id, ok := s.Type.(*ast.Ident); ok {
				return "const block of " + id.Name
			}
			// an untyped spec inside a typed block (iota continuation)
			for _, o := range d.Specs {
				if vs, ok := o.(*ast.ValueSpec); ok {
					if id, ok := vs.Type.(*ast.Ident); ok {
						return "const block of " + id.Name
					}
				}
			}
			return "const " + s.Names[0].Name
		}
		return "var " + s.Names[0].Name
	case *ast.ImportSpec:
		return "import"
	}
	return d.Tok.String()
}

// mdHeading names the Markdown heading a line falls under ("" before
// the first heading). Fenced code is skipped.
func mdHeading(src []byte, line int) string {
	sc := bufio.NewScanner(bytes.NewReader(src))
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	heading, fence := "", false
	for n := 1; sc.Scan() && n <= line; n++ {
		t := sc.Text()
		if strings.HasPrefix(strings.TrimSpace(t), "```") {
			fence = !fence
			continue
		}
		if !fence && strings.HasPrefix(t, "#") {
			heading = strings.TrimSpace(t)
		}
	}
	return heading
}

var testFuncRe = regexp.MustCompile(`(?m)^func ((?:Test|Benchmark|Fuzz)\w*)\(`)

// testIndex maps every Test/Benchmark/Fuzz function to the package
// directories declaring it.
func (r *repo) testIndex() map[string][]string {
	if r.testFuncs != nil {
		return r.testFuncs
	}
	r.testFuncs = map[string][]string{}
	for _, p := range r.files {
		if !strings.HasSuffix(p, "_test.go") {
			continue
		}
		for _, m := range testFuncRe.FindAllSubmatch(r.read(p), -1) {
			name, dir := string(m[1]), path.Dir(p)
			if !contains(r.testFuncs[name], dir) {
				r.testFuncs[name] = append(r.testFuncs[name], dir)
			}
		}
	}
	return r.testFuncs
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

// goTestCmd renders the rerun command for a package directory.
func goTestCmd(dir string, tests []string) string {
	pkg := "./" + dir + "/"
	if dir == "." {
		pkg = "./"
	}
	sort.Strings(tests)
	tests = dedupe(tests)
	if len(tests) == 0 {
		return "go test -count=1 " + pkg
	}
	return "go test -count=1 " + pkg + " -run '^(" + strings.Join(tests, "|") + ")$'"
}

func dedupe(xs []string) []string {
	var out []string
	for i, x := range xs {
		if i == 0 || x != xs[i-1] {
			out = append(out, x)
		}
	}
	return out
}
