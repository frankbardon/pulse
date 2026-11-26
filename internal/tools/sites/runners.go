package main

import (
	"go/ast"
	"go/token"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Whole-repo runners and floors: tests that never name an operator or a
// slot but run over everything a change of one kind reaches, so a site
// list built from names misses them. Each is found by its shape:
//
//   - every-example runners — Test functions that enumerate the whole
//     embedded example library (a call to internal/examples' All, or a
//     walk of the internal/examples tree), directly or through a helper
//     declared in the same package's tests; listed whenever a site is an
//     example;
//   - call-site floors — Test functions that parse Go source (go/parser)
//     and hold the name of a module function as a string literal (the
//     callee they count and check at every call site:
//     TestReadSchemaCallersThreadHeaderVersion holds "ReadSchema");
//     listed when a site calls that function.

const examplesPkg = modulePath + "/internal/examples"

// exampleRunners returns, per package directory, the Test functions
// that run every example.
func (r *repo) exampleRunners() map[string][]string {
	out := map[string][]string{}
	// helpers: non-Test funcs (in test files) that enumerate, per dir
	type fn struct {
		dir, name string
		decl      *ast.FuncDecl
		alias     string
	}
	var tests []fn
	helpers := map[string]map[string]bool{}
	for _, p := range r.files {
		if !strings.HasSuffix(p, "_test.go") {
			continue
		}
		g := r.parse(p)
		if g == nil {
			continue
		}
		alias := importName(g.file, examplesPkg)
		if alias == "" && path.Dir(p) == "internal/examples" {
			alias = "." // the package's own tests call All() unqualified
		}
		for _, d := range g.file.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Body == nil || fd.Recv != nil {
				continue
			}
			f := fn{dir: path.Dir(p), name: fd.Name.Name, decl: fd, alias: alias}
			if strings.HasPrefix(f.name, "Test") {
				tests = append(tests, f)
			} else if enumeratesExamples(fd, alias) {
				if helpers[f.dir] == nil {
					helpers[f.dir] = map[string]bool{}
				}
				helpers[f.dir][f.name] = true
			}
		}
	}
	for _, f := range tests {
		if enumeratesExamples(f.decl, f.alias) || callsAny(f.decl, helpers[f.dir]) {
			out[f.dir] = append(out[f.dir], f.name)
		}
	}
	for d := range out {
		sort.Strings(out[d])
	}
	return out
}

// enumeratesExamples reports whether fd calls the example library's All
// (qualified by alias; "." = unqualified, inside the package), or both
// lists a directory (Walk, WalkDir, Glob, ReadDir) and names the
// internal/examples tree as a whole: the literal "internal/examples", or
// Join("internal", "examples", …) whose further literal elements are
// all glob patterns (a literal directory or file name there reads one
// example or one category, not every one).
func enumeratesExamples(fd *ast.FuncDecl, alias string) bool {
	all, lists, tree := false, false, false
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.CallExpr:
			switch f := x.Fun.(type) {
			case *ast.SelectorExpr:
				if id, ok := f.X.(*ast.Ident); ok && alias != "" && alias != "." && id.Name == alias && f.Sel.Name == "All" {
					all = true
				}
				switch f.Sel.Name {
				case "Walk", "WalkDir", "Glob", "ReadDir":
					lists = true
				case "Join":
					if len(x.Args) >= 2 && litValue(x.Args[0]) == "internal" && litValue(x.Args[1]) == "examples" {
						whole := true
						for _, a := range x.Args[2:] {
							if v := litValue(a); v != "" && !strings.ContainsAny(v, "*?[") {
								whole = false
							}
						}
						tree = tree || whole
					}
				}
			case *ast.Ident:
				if alias == "." && f.Name == "All" && len(x.Args) == 0 {
					all = true
				}
			}
		case *ast.BasicLit:
			if v := litValue(x); v == "internal/examples" || v == "internal/examples/" {
				tree = true
			}
		}
		return true
	})
	return all || lists && tree
}

func litValue(e ast.Expr) string {
	bl, ok := e.(*ast.BasicLit)
	if !ok || bl.Kind != token.STRING {
		return ""
	}
	s, err := strconv.Unquote(bl.Value)
	if err != nil {
		return ""
	}
	return s
}

func callsAny(fd *ast.FuncDecl, names map[string]bool) bool {
	if len(names) == 0 {
		return false
	}
	found := false
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		if ce, ok := n.(*ast.CallExpr); ok {
			if id, ok := ce.Fun.(*ast.Ident); ok && names[id.Name] {
				found = true
			}
		}
		return !found
	})
	return found
}

// runnerGates are the every-example runners as gates.
func (r *repo) runnerGates() []Gate {
	return gatesFromDirs("whole-repo runners: every example runs through these (an examples change reaches them without naming them)", r.exampleRunners())
}

// floor is one call-site floor: a Test function that parses Go source
// and checks every call of Callee.
type floor struct {
	Dir, Test, Callee string
}

var exportedIdentRe = regexp.MustCompile(`^[A-Z][A-Za-z0-9_]*$`)

// callFloors returns every call-site floor of the module: a Test
// function in a file importing go/parser whose string literals include
// the name of a function the module declares and calls outside tests.
func (r *repo) callFloors() []floor {
	calls := r.callIndex()
	declared := r.moduleFuncs()
	var out []floor
	for _, p := range r.files {
		if !strings.HasSuffix(p, "_test.go") {
			continue
		}
		g := r.parse(p)
		if g == nil || importName(g.file, "go/parser") == "" {
			continue
		}
		for _, d := range g.file.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Body == nil || fd.Recv != nil || !strings.HasPrefix(fd.Name.Name, "Test") {
				continue
			}
			if !walksSource(fd) {
				continue
			}
			seen := map[string]bool{}
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				bl, ok := n.(*ast.BasicLit)
				if !ok {
					return true
				}
				v := litValue(bl)
				if !exportedIdentRe.MatchString(v) || seen[v] || !declared[v] || len(calls[v]) == 0 {
					return true
				}
				seen[v] = true
				out = append(out, floor{Dir: path.Dir(p), Test: fd.Name.Name, Callee: v})
				return true
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Callee != b.Callee {
			return a.Callee < b.Callee
		}
		if a.Dir != b.Dir {
			return a.Dir < b.Dir
		}
		return a.Test < b.Test
	})
	return out
}

// walksSource reports whether fd walks a directory tree (Walk, WalkDir)
// and parses what it finds (ParseFile, ParseDir): a whole-tree source
// scan, not a test of one function.
func walksSource(fd *ast.FuncDecl) bool {
	walk, parse := false, false
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		if ce, ok := n.(*ast.CallExpr); ok {
			if se, ok := ce.Fun.(*ast.SelectorExpr); ok {
				switch se.Sel.Name {
				case "Walk", "WalkDir":
					walk = true
				case "ParseFile", "ParseDir":
					parse = true
				}
			}
		}
		return true
	})
	return walk && parse
}

// moduleFuncs is the set of function names (not methods) declared in
// the module's non-test Go files.
func (r *repo) moduleFuncs() map[string]bool {
	out := map[string]bool{}
	for _, p := range r.files {
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			continue
		}
		g := r.parse(p)
		if g == nil {
			continue
		}
		for _, d := range g.file.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok && fd.Recv == nil {
				out[fd.Name.Name] = true
			}
		}
	}
	return out
}

// floorSection lists, for every call-site floor, the calls of its callee
// in the files already listed as sites (listed keys are path + "\x00" +
// symbol), with the floor tests as gates. The match is by file, not by
// declaration: a read path reaches ReadSchema through a helper beside the
// declaration that names the slot. A floor whose callee no listed file
// calls is not printed: the change does not reach it.
func (r *repo) floorSection(listed map[string]bool) (Section, []Gate, bool) {
	sec := Section{Class: "floors", Title: "Call-site floors — whole-tree gates that check every call of a function these site files call (rerun when you add, move or change such a call)"}
	files := map[string]bool{}
	for k := range listed {
		p, _, _ := strings.Cut(k, "\x00")
		files[p] = true
	}
	calls := r.callIndex()
	byCallee := map[string][]floor{}
	var callees []string
	for _, f := range r.callFloors() {
		if len(byCallee[f.Callee]) == 0 {
			callees = append(callees, f.Callee)
		}
		byCallee[f.Callee] = append(byCallee[f.Callee], f)
	}
	add := &sectionAdder{}
	gateDirs := map[string][]string{}
	for _, c := range callees {
		var tests []string
		for _, f := range byCallee[c] {
			tests = append(tests, f.Test)
		}
		hit := false
		for _, cs := range calls[c] {
			if !files[cs.Path] {
				continue
			}
			hit = true
			add.add(cs.Path, cs.Sym, []int{cs.Line}, "calls "+c+" — checked by "+strings.Join(dedupe(tests), ", "))
		}
		if hit {
			for _, f := range byCallee[c] {
				gateDirs[f.Dir] = append(gateDirs[f.Dir], f.Test)
			}
		}
	}
	sec.Sites = add.list()
	return sec, gatesFromDirs("call-site floors: every call of a function the sites call is checked here", gateDirs), len(sec.Sites) > 0
}

// wholeRepo appends the whole-repo runners and floors a report's sites
// reach: the floor section and its gates for the listed declarations,
// and the every-example runners when an example is among the sites.
func (r *repo) wholeRepo(rep *Report, classes, listed map[string]bool) {
	if sec, gates, ok := r.floorSection(listed); ok {
		rep.Sections = append(rep.Sections, sec)
		rep.Gates = append(rep.Gates, gates...)
	}
	if classes["examples"] {
		rep.Gates = append(rep.Gates, r.runnerGates()...)
	}
}

// Per-case attribution inside a shared generator: a hand-written script
// (.py / .R) under testdata/ or scripts/ that builds fixtures for many
// operators names each one in a case table. Every hit is attributed to
// its case, found by the table's shape:
//
//   - a row whose first element is the case key: ("t_split", {"type": …});
//   - a branch on the case variable above it in the same function:
//     if case == "ov_symmetry":;
//   - a comment heading a block: the block's first quoted key
//     (# TEST_T one-sample. → out("t_one", …));
//   - a top-level assignment: its name (OV_SYM_INTERVAL = 4  # …);
//
// prefixed with the enclosing function (def name / name <- function).

var (
	scriptRowKeyRe = regexp.MustCompile(`^\s*[\(\[]\s*"([A-Za-z0-9_.\-]+)"\s*,`)
	scriptCaseRe   = regexp.MustCompile(`\bcase\s*==\s*"([A-Za-z0-9_.\-]+)"`)
	scriptLitRe    = regexp.MustCompile(`"([a-z][A-Za-z0-9_.\-]*)"`)
	scriptAssignRe = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_.]*)\s*(?:=|<-)`)
	pyDefRe        = regexp.MustCompile(`^(\s*)def\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
	rFuncRe        = regexp.MustCompile(`^(\s*)([A-Za-z_.][A-Za-z0-9_.]*)\s*(?:<-|=)\s*function\s*\(`)
)

func isScript(p string) bool {
	switch path.Ext(p) {
	case ".py", ".R":
		return strings.Contains(p, "/testdata/") || strings.HasPrefix(p, "scripts/")
	}
	return false
}

// scriptCase names the case-table entry holding 1-based line ln of a
// generator script.
func scriptCase(src []byte, ln int) string {
	lines := strings.Split(string(src), "\n")
	if ln < 1 || ln > len(lines) {
		return "(top level)"
	}
	cur := lines[ln-1]
	trimmed := strings.TrimSpace(cur)
	isComment := strings.HasPrefix(trimmed, "#")
	if isComment {
		header := true
		for i := 1; i < ln; i++ {
			if t := strings.TrimSpace(lines[i-1]); t != "" && !strings.HasPrefix(t, "#") {
				header = false
				break
			}
		}
		if header {
			return "(file header)"
		}
	}
	// The enclosing function: a def above whose indent is below that of
	// every code line between it and the hit (a code line at or left of
	// a def's indent closes its body: Python's block rule, and R's
	// closing brace or next top-level statement).
	fn, fnLine := "", 0
	minIndent := indentOf(cur)
	if isComment || trimmed == "" {
		minIndent = 1 << 30
	}
	for i := ln - 1; i >= 1; i-- {
		l := lines[i-1]
		t := strings.TrimSpace(l)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		var m []string
		if m = pyDefRe.FindStringSubmatch(l); m == nil {
			m = rFuncRe.FindStringSubmatch(l)
		}
		if m != nil && !strings.HasSuffix(t, ":") && !strings.HasSuffix(t, "{") {
			m = nil // a one-line function is code, not an enclosing body
		}
		if m != nil && len(m[1]) < minIndent {
			fn, fnLine = m[2], i
			break
		}
		if in := indentOf(l); in < minIndent {
			minIndent = in
		}
		if minIndent == 0 {
			break
		}
	}
	key := ""
	switch {
	case isComment:
		// the first quoted key of the block the comment heads
		for i, n := ln, 0; i < len(lines) && n < 40; i, n = i+1, n+1 {
			t := strings.TrimSpace(lines[i])
			if t == "" {
				break
			}
			if strings.HasPrefix(t, "#") {
				continue
			}
			if m := scriptLitRe.FindStringSubmatch(lines[i]); m != nil {
				key = m[1]
				break
			}
		}
	case scriptRowKeyRe.MatchString(cur):
		key = scriptRowKeyRe.FindStringSubmatch(cur)[1]
	case fn != "":
		for i := ln; i > fnLine; i-- {
			if m := scriptCaseRe.FindStringSubmatch(lines[i-1]); m != nil {
				key = m[1]
				break
			}
		}
	default:
		if m := scriptAssignRe.FindStringSubmatch(cur); m != nil {
			return m[1]
		}
	}
	var parts []string
	if fn != "" {
		parts = append(parts, fn)
	}
	if key != "" {
		parts = append(parts, "case "+key)
	}
	if len(parts) == 0 {
		return "(top level)"
	}
	return strings.Join(parts, " › ")
}

func indentOf(l string) int { return len(l) - len(strings.TrimLeft(l, " \t")) }

// generatorSection attributes the operator's hits in every generator
// script among paths to their case-table entries.
func (r *repo) generatorSection(paths []string, files map[string][]hit) (Section, bool) {
	sec := Section{Class: "generator-cases", Title: "Shared generators — the case-table entries that name it (edit those cases, then rerun the generator)"}
	add := &sectionAdder{}
	for _, p := range paths {
		if !isScript(p) {
			continue
		}
		src := r.read(p)
		for _, h := range files[p] {
			add.add(p, scriptCase(src, h.Line), []int{h.Line}, "")
		}
	}
	sec.Sites = add.list()
	return sec, len(sec.Sites) > 0
}
