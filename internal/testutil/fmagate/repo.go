package fmagate

// Repo-wide scans. The per-package gates above ask about one package;
// the scans below ask about a scope of the whole module (every non-test
// package minus cmd/ and internal/cli by default) and report two kinds
// of finding:
//
//   - "fma": a multiply-add the arm64 compiler contracted (ScanFMA);
//   - "math.<Name>": a reference to a stdlib transcendental whose bits
//     differ across architectures (BannedCalls; math.Sqrt, Abs, Floor
//     and the other IEEE-exact functions are allowed).
//
// A finding is excused by an allowlist entry (ParseAllowlist, format in
// testdata/numeric-allowlist.txt). An entry names a kind, a file and the
// enclosing top-level function, never a line, so it survives line
// drift; an in-scope entry that excuses nothing is stale (Apply).

import (
	"bufio"
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// KindFMA is the Finding.Kind of a compiler-reported contraction.
const KindFMA = "fma"

// PackageFunc is the Finding.Func of a site outside every function
// (a package-level var initialiser).
const PackageFunc = "(package)"

// BannedMath is the set of stdlib math functions whose results are not
// bit-identical across architectures (per-arch assembly, or portable Go
// the arm64 compiler contracts). The deterministic twins live in
// linalg/detmath. Sqrt, Abs, Floor, Ceil, Trunc, Mod, Frexp, Ldexp,
// Pow10 and the other exact functions are not listed.
var BannedMath = map[string]bool{
	"Exp": true, "Exp2": true, "Expm1": true,
	"Log": true, "Log2": true, "Log10": true, "Log1p": true,
	"Pow": true, "Erf": true, "Erfc": true, "Erfinv": true, "Erfcinv": true,
	"Lgamma": true, "Gamma": true,
	"Sin": true, "Cos": true, "Tan": true, "Sincos": true,
	"Asin": true, "Acos": true, "Atan": true, "Atan2": true,
	"Sinh": true, "Cosh": true, "Tanh": true,
	"Asinh": true, "Acosh": true, "Atanh": true,
	"Cbrt": true, "Hypot": true,
	"J0": true, "J1": true, "Jn": true, "Y0": true, "Y1": true, "Yn": true,
}

// excludedDirs are module-relative directories (and their subtrees)
// outside every repo-wide scan: the CLI adapter carries no figures.
var excludedDirs = []string{"cmd", "internal/cli"}

// Finding is one site a scan reports.
type Finding struct {
	Kind string // KindFMA or "math.<Name>"
	File string // slash path relative to the module root
	Line int
	Col  int
	Func string // enclosing top-level declaration: "F", "T.M" or PackageFunc
	// Detail is the compiler's inline chain for an fma finding (outer
	// call site first), empty for a banned call.
	Detail string
	// N is how many contractions the compiler reported at this
	// position (inlining can fuse several ops at one call site).
	N int
}

// Pkg is the finding's module-relative package directory.
func (f Finding) Pkg() string { return path.Dir(f.File) }

func (f Finding) String() string {
	s := fmt.Sprintf("%s:%d:%d %s %s", f.File, f.Line, f.Col, f.Func, f.Kind)
	if f.N > 1 {
		s += fmt.Sprintf(" x%d", f.N)
	}
	if f.Detail != "" && strings.Contains(f.Detail, ";") {
		s += " [inlined: " + f.Detail + "]"
	}
	return s
}

// Scope selects the module-relative package directories (and,
// optionally, single files) a scan covers.
type Scope struct {
	Dirs  map[string]bool // slash dirs; "." is the module root
	Files map[string]bool // when non-empty, only these slash files
}

// Has reports whether file (slash, module-relative) is in scope.
func (s Scope) Has(file string) bool {
	if len(s.Files) > 0 {
		return s.Files[file]
	}
	return s.Dirs[path.Dir(file)]
}

// ModuleRoot walks up from the working directory to the directory
// holding go.mod.
func ModuleRoot(t testing.TB) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the working directory")
		}
		dir = parent
	}
}

// modulePath reads the module line of root/go.mod.
func modulePath(root string) (string, error) {
	b, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return "", err
	}
	for l := range strings.SplitSeq(string(b), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(l), "module "); ok {
			return strings.Trim(strings.TrimSpace(rest), `"`), nil
		}
	}
	return "", fmt.Errorf("%s/go.mod has no module line", root)
}

// ResolveScope turns a comma-separated list of patterns into a Scope
// over root. A pattern is "./..." (everything), "./a/b/..." (a subtree),
// "./a/b" (one package) or "./a/b/c.go" (one file); the "./" is
// optional. Directories holding no non-test .go file, testdata,
// hidden and vendor directories, nested modules and excludedDirs are
// never in scope.
func ResolveScope(root, patterns string) (Scope, error) {
	all, err := packageDirs(root)
	if err != nil {
		return Scope{}, err
	}
	s := Scope{Dirs: map[string]bool{}, Files: map[string]bool{}}
	var dirOnly bool
	for p := range strings.SplitSeq(patterns, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		p = path.Clean(strings.TrimPrefix(filepath.ToSlash(p), "./"))
		switch {
		case strings.HasSuffix(p, ".go"):
			if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(p))); err != nil {
				return Scope{}, fmt.Errorf("scope file %s: %v", p, err)
			}
			s.Files[p] = true
			s.Dirs[path.Dir(p)] = true
		case p == "..." || strings.HasSuffix(p, "/..."):
			dirOnly = true
			prefix := strings.TrimSuffix(strings.TrimSuffix(p, "..."), "/")
			n := 0
			for d := range all {
				if prefix == "" || d == prefix || strings.HasPrefix(d, prefix+"/") {
					s.Dirs[d] = true
					n++
				}
			}
			if n == 0 {
				return Scope{}, fmt.Errorf("scope pattern %s matches no package", p)
			}
		default:
			dirOnly = true
			if !all[p] {
				return Scope{}, fmt.Errorf("scope pattern %s is not a scanned package directory", p)
			}
			s.Dirs[p] = true
		}
	}
	if dirOnly && len(s.Files) > 0 {
		return Scope{}, fmt.Errorf("scope %q mixes file and package patterns", patterns)
	}
	if len(s.Dirs) == 0 {
		return Scope{}, fmt.Errorf("scope %q is empty", patterns)
	}
	return s, nil
}

// packageDirs lists every slash dir under root holding a non-test .go
// file, minus the never-scanned directories.
func packageDirs(root string) (map[string]bool, error) {
	dirsMu.Lock()
	defer dirsMu.Unlock()
	if d, ok := dirsCache[root]; ok {
		return d, nil
	}
	d, err := walkPackageDirs(root)
	if err == nil {
		dirsCache[root] = d
	}
	return d, err
}

var (
	dirsMu    sync.Mutex
	dirsCache = map[string]map[string]bool{}
)

func walkPackageDirs(root string) (map[string]bool, error) {
	dirs := map[string]bool{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if p == root {
				return nil
			}
			name := d.Name()
			if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") || name == "testdata" || name == "vendor" {
				return filepath.SkipDir
			}
			for _, x := range excludedDirs {
				if rel == x {
					return filepath.SkipDir
				}
			}
			if _, err := os.Stat(filepath.Join(p, "go.mod")); err == nil {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(rel, ".go") && !strings.HasSuffix(rel, "_test.go") {
			dirs[path.Dir(rel)] = true
		}
		return nil
	})
	return dirs, err
}

// funcIndex maps positions in one file to the enclosing top-level
// declaration's name.
type funcIndex struct {
	tf    *token.File
	decls []declSpan
}

type declSpan struct {
	pos, end token.Pos
	name     string
}

func newFuncIndex(fset *token.FileSet, f *ast.File) *funcIndex {
	ix := &funcIndex{tf: fset.File(f.Pos())}
	for _, d := range f.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok {
			ix.decls = append(ix.decls, declSpan{fd.Pos(), fd.End(), funcName(fd)})
		}
	}
	return ix
}

// at names the declaration enclosing line:col (1-based, byte column).
func (ix *funcIndex) at(line, col int) string {
	if line < 1 || line > ix.tf.LineCount() {
		return PackageFunc
	}
	pos := ix.tf.LineStart(line) + token.Pos(col-1)
	return ix.of(pos)
}

func (ix *funcIndex) of(pos token.Pos) string {
	for _, d := range ix.decls {
		if d.pos <= pos && pos < d.end {
			return d.name
		}
	}
	return PackageFunc
}

// funcName is "F" for a function and "T.M" for a method (pointer and
// type parameters dropped).
func funcName(fd *ast.FuncDecl) string {
	if fd.Recv == nil || len(fd.Recv.List) == 0 {
		return fd.Name.Name
	}
	t := fd.Recv.List[0].Type
	for {
		switch x := t.(type) {
		case *ast.StarExpr:
			t = x.X
			continue
		case *ast.IndexExpr:
			t = x.X
			continue
		case *ast.IndexListExpr:
			t = x.X
			continue
		case *ast.ParenExpr:
			t = x.X
			continue
		}
		break
	}
	if id, ok := t.(*ast.Ident); ok {
		return id.Name + "." + fd.Name.Name
	}
	return fd.Name.Name
}

// BannedCalls is the source scan: every reference to a BannedMath
// function of the stdlib "math" package (aliased imports included,
// calls and method values alike) in the scope's non-test files.
func BannedCalls(root string, scope Scope) ([]Finding, error) {
	var out []Finding
	fset := token.NewFileSet()
	dirs := make([]string, 0, len(scope.Dirs))
	for d := range scope.Dirs {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	for _, d := range dirs {
		ents, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(d)))
		if err != nil {
			return nil, err
		}
		for _, e := range ents {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			rel := path.Join(d, name)
			if !scope.Has(rel) {
				continue
			}
			src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
			if err != nil {
				return nil, err
			}
			if !bytes.Contains(src, []byte(`"math"`)) {
				continue
			}
			got, err := bannedInFile(fset, rel, src)
			if err != nil {
				return nil, err
			}
			out = append(out, got...)
		}
	}
	sortFindings(out)
	return out, nil
}

func bannedInFile(fset *token.FileSet, rel string, src []byte) ([]Finding, error) {
	head, err := parser.ParseFile(token.NewFileSet(), rel, src, parser.ImportsOnly)
	if err != nil {
		return nil, err
	}
	local := ""
	for _, imp := range head.Imports {
		if p, _ := strconv.Unquote(imp.Path.Value); p != "math" {
			continue
		}
		local = "math"
		if imp.Name != nil {
			local = imp.Name.Name
		}
	}
	if local == "" || local == "_" || local == "." || !mentionsBanned(src, local) {
		return nil, nil
	}
	f, err := parser.ParseFile(fset, rel, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	ix := newFuncIndex(fset, f)
	var out []Finding
	ast.Inspect(f, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		id, ok := sel.X.(*ast.Ident)
		if !ok || id.Name != local || !BannedMath[sel.Sel.Name] {
			return true
		}
		p := fset.Position(sel.Pos())
		out = append(out, Finding{
			Kind: "math." + sel.Sel.Name, File: rel, Line: p.Line, Col: p.Column,
			Func: ix.of(sel.Pos()), N: 1,
		})
		return true
	})
	return out, nil
}

// mentionsBanned is the cheap byte prefilter before a full parse: some
// "<local>.<BannedName>" not preceded by an identifier byte. It may
// over-report (a comment, a string); the AST pass decides.
func mentionsBanned(src []byte, local string) bool {
	needle := []byte(local + ".")
	for i := 0; ; {
		j := bytes.Index(src[i:], needle)
		if j < 0 {
			return false
		}
		at := i + j
		i = at + len(needle)
		if at > 0 && isIdentByte(src[at-1]) {
			continue
		}
		k := i
		for k < len(src) && isIdentByte(src[k]) {
			k++
		}
		if BannedMath[string(src[i:k])] {
			return true
		}
	}
}

func isIdentByte(c byte) bool {
	return c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= 0x80
}

// ScanFMA cross-compiles every scope package for linux/arm64 with the
// contraction report on for the whole module and returns one finding
// per contracted source position inside the scope (the outer call site
// of an inline chain). The probe runs first.
func ScanFMA(t testing.TB, root string, scope Scope) []Finding {
	t.Helper()
	if err := probe(); err != nil {
		t.Fatal(err)
	}
	out, err := scanFMA(root, scope)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func scanFMA(root string, scope Scope) ([]Finding, error) {
	mod, err := modulePath(root)
	if err != nil {
		return nil, err
	}
	var pkgs []string
	for d := range scope.Dirs {
		if d == "." {
			pkgs = append(pkgs, mod)
		} else {
			pkgs = append(pkgs, mod+"/"+d)
		}
	}
	sort.Strings(pkgs)
	// Packages whose files all carry other build constraints have
	// nothing to compile for linux/arm64; go build would refuse them.
	listed, err := goCmd(root, append([]string{"list", "-e", "-f", "{{.ImportPath}}\t{{.Dir}}\t{{len .GoFiles}}"}, pkgs...)...)
	if err != nil {
		return nil, err
	}
	pkgDir := map[string]string{}
	var build []string
	for l := range strings.SplitSeq(strings.TrimSpace(listed), "\n") {
		parts := strings.Split(l, "\t")
		if len(parts) != 3 || parts[2] == "0" {
			continue
		}
		pkgDir[parts[0]] = parts[1]
		build = append(build, parts[0])
	}
	if len(build) == 0 {
		return nil, nil
	}
	raw, err := goCmd(root, append([]string{"build", "-gcflags=" + mod + "/...=-d=fmahash=vy"}, build...)...)
	if err != nil {
		return nil, err
	}
	return parseFMA(root, raw, pkgDir, scope)
}

// goCmd runs go in dir for linux/arm64 and returns combined output.
func goCmd(dir string, args ...string) (string, error) {
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH=arm64", "CGO_ENABLED=0", "GOFLAGS=")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("go %s (linux/arm64): %v\n%s", strings.Join(args[:1], " "), err, out.String())
	}
	return out.String(), nil
}

// parseFMA reads `go build` output: "# <importpath>" headers and
// "fmahash triggered <pos>[; <pos>...] <hash>" lines, where a position
// is relative to the module root or (when replayed from the cache) to
// the package directory.
func parseFMA(root, raw string, pkgDir map[string]string, scope Scope) ([]Finding, error) {
	fset := token.NewFileSet()
	indexes := map[string]*funcIndex{}
	byPos := map[string]*Finding{}
	cur := ""
	for l := range strings.SplitSeq(raw, "\n") {
		if rest, ok := strings.CutPrefix(l, "# "); ok {
			cur = strings.Fields(rest)[0]
			continue
		}
		i := strings.Index(l, marker)
		if i < 0 {
			continue
		}
		fields := strings.Fields(l[i+len(marker):])
		if len(fields) < 2 {
			return nil, fmt.Errorf("unparsed contraction line %q", l)
		}
		chain := strings.Join(fields[:len(fields)-1], " ")
		outer := strings.TrimSuffix(fields[0], ";")
		file, line, col, err := splitPos(outer)
		if err != nil {
			return nil, fmt.Errorf("%v in %q", err, l)
		}
		abs := filepath.Join(root, filepath.FromSlash(file))
		if _, err := os.Stat(abs); err != nil || filepath.IsAbs(file) {
			if filepath.IsAbs(file) {
				abs = file
			} else if d := pkgDir[cur]; d != "" {
				abs = filepath.Join(d, filepath.FromSlash(file))
			}
		}
		relOS, err := filepath.Rel(root, abs)
		if err != nil || strings.HasPrefix(relOS, "..") {
			continue // a dependency's own file: not ours to barrier
		}
		rel := filepath.ToSlash(relOS)
		if !scope.Has(rel) {
			continue
		}
		key := fmt.Sprintf("%s:%d:%d", rel, line, col)
		if f := byPos[key]; f != nil {
			f.N++
			continue
		}
		ix := indexes[rel]
		if ix == nil {
			af, err := parser.ParseFile(fset, abs, nil, parser.SkipObjectResolution)
			if err != nil {
				return nil, err
			}
			ix = newFuncIndex(fset, af)
			indexes[rel] = ix
		}
		byPos[key] = &Finding{Kind: KindFMA, File: rel, Line: line, Col: col, Func: ix.at(line, col), Detail: chain, N: 1}
	}
	out := make([]Finding, 0, len(byPos))
	for _, f := range byPos {
		out = append(out, *f)
	}
	sortFindings(out)
	return out, nil
}

// splitPos splits "file:line:col".
func splitPos(s string) (string, int, int, error) {
	j := strings.LastIndex(s, ":")
	i := strings.LastIndex(s[:max(j, 0)], ":")
	if i < 0 || j < 0 {
		return "", 0, 0, fmt.Errorf("position %q is not file:line:col", s)
	}
	line, err1 := strconv.Atoi(s[i+1 : j])
	col, err2 := strconv.Atoi(s[j+1:])
	if err1 != nil || err2 != nil {
		return "", 0, 0, fmt.Errorf("position %q is not file:line:col", s)
	}
	return s[:i], line, col, nil
}

func sortFindings(fs []Finding) {
	sort.Slice(fs, func(i, j int) bool {
		a, b := fs[i], fs[j]
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		if a.Col != b.Col {
			return a.Col < b.Col
		}
		return a.Kind < b.Kind
	})
}

// Entry is one allowlist line.
type Entry struct {
	Kind   string // KindFMA or "math.<Name>"
	File   string // slash path relative to the module root
	Func   string // enclosing top-level declaration, as Finding.Func
	Reason string
	Line   int // the entry's line in the allowlist file
}

func (e Entry) String() string {
	return fmt.Sprintf("allowlist line %d: %s %s %s", e.Line, e.Kind, e.File, e.Func)
}

func (e Entry) matches(f Finding) bool {
	return e.Kind == f.Kind && e.File == f.File && e.Func == f.Func
}

// ParseAllowlist reads the allowlist format: one entry per line,
//
//	<kind> <file> <func> <reason...>
//
// where kind is "fma" or "math.<Name>" (a BannedMath name), file is
// module-relative with forward slashes, func is the enclosing top-level
// declaration ("F", "T.M" or "(package)"), and reason is the rest of
// the line (required). Blank lines and lines starting with '#' are
// skipped. Duplicates and malformed lines are errors.
func ParseAllowlist(r io.Reader) ([]Entry, error) {
	var out []Entry
	seen := map[string]int{}
	sc := bufio.NewScanner(r)
	n := 0
	for sc.Scan() {
		n++
		l := strings.TrimSpace(sc.Text())
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		fields := strings.Fields(l)
		if len(fields) < 4 {
			return nil, fmt.Errorf("allowlist line %d: want `<kind> <file> <func> <reason>`, got %q", n, l)
		}
		kind, file, fn := fields[0], fields[1], fields[2]
		if kind != KindFMA {
			name, ok := strings.CutPrefix(kind, "math.")
			if !ok || !BannedMath[name] {
				return nil, fmt.Errorf("allowlist line %d: kind %q is neither %q nor a banned math.<Name>", n, kind, KindFMA)
			}
		}
		if file != path.Clean(file) || path.IsAbs(file) || strings.HasPrefix(file, "..") || strings.Contains(file, `\`) || !strings.HasSuffix(file, ".go") {
			return nil, fmt.Errorf("allowlist line %d: file %q is not a clean module-relative .go path", n, file)
		}
		key := kind + " " + file + " " + fn
		if prev, dup := seen[key]; dup {
			return nil, fmt.Errorf("allowlist line %d duplicates line %d (%s)", n, prev, key)
		}
		seen[key] = n
		reason := l
		for range 3 {
			reason = strings.TrimLeft(reason, " \t")
			reason = reason[strings.IndexAny(reason, " \t"):]
		}
		reason = strings.TrimSpace(reason)
		out = append(out, Entry{Kind: kind, File: file, Func: fn, Reason: reason, Line: n})
	}
	return out, sc.Err()
}

// Apply splits findings into excused and unlisted ones and returns the
// stale entries: those of a kind keep() accepts, whose file is in
// scope, that excuse no finding.
func Apply(entries []Entry, findings []Finding, scope Scope, keep func(kind string) bool) (listed, unlisted []Finding, stale []Entry) {
	used := make([]bool, len(entries))
	for _, f := range findings {
		hit := false
		for i, e := range entries {
			if e.matches(f) {
				used[i], hit = true, true
			}
		}
		if hit {
			listed = append(listed, f)
		} else {
			unlisted = append(unlisted, f)
		}
	}
	for i, e := range entries {
		if !used[i] && keep(e.Kind) && scope.Has(e.File) {
			stale = append(stale, e)
		}
	}
	return listed, unlisted, stale
}

// Report renders findings as per-package counts followed by each site,
// unlisted first.
func Report(title string, listed, unlisted []Finding) string {
	var b strings.Builder
	perPkg := map[string][2]int{}
	for _, f := range unlisted {
		c := perPkg[f.Pkg()]
		c[0]++
		perPkg[f.Pkg()] = c
	}
	for _, f := range listed {
		c := perPkg[f.Pkg()]
		c[1]++
		perPkg[f.Pkg()] = c
	}
	pkgs := make([]string, 0, len(perPkg))
	for p := range perPkg {
		pkgs = append(pkgs, p)
	}
	sort.Strings(pkgs)
	fmt.Fprintf(&b, "%s: %d unlisted, %d allowlisted, %d packages\n", title, len(unlisted), len(listed), len(pkgs))
	for _, p := range pkgs {
		fmt.Fprintf(&b, "  %-48s unlisted %4d  allowlisted %4d\n", p, perPkg[p][0], perPkg[p][1])
	}
	for _, f := range unlisted {
		fmt.Fprintf(&b, "  UNLISTED %s\n", f)
	}
	for _, f := range listed {
		fmt.Fprintf(&b, "  allowed  %s\n", f)
	}
	return b.String()
}
