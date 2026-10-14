package main

import (
	"bufio"
	"bytes"
	"fmt"
	"go/ast"
	"go/token"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Derived sites: places a change must visit that do NOT spell the
// operator's name, found by following what the named sites declare —
// predicates and size estimates (their callers), capability param enums
// (the constants' users), R oracle fixtures (the tests' string
// literals), benchmarks and Makefile targets (the sites' packages).

// callSite is one call of a function by name.
type callSite struct {
	Path string
	Sym  string
	Line int
}

// callIndex maps a bare function / method name to every non-test call
// of it (an *ast.CallExpr whose callee is that identifier or selector),
// so a struct FIELD of the same name is never a caller.
func (r *repo) callIndex() map[string][]callSite {
	if r.calls != nil {
		return r.calls
	}
	r.calls = map[string][]callSite{}
	for _, p := range r.files {
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			continue
		}
		g := r.parse(p)
		if g == nil {
			continue
		}
		ast.Inspect(g.file, func(n ast.Node) bool {
			ce, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			name := ""
			switch f := ce.Fun.(type) {
			case *ast.Ident:
				name = f.Name
			case *ast.SelectorExpr:
				name = f.Sel.Name
			case *ast.IndexExpr:
				if id, ok := f.X.(*ast.Ident); ok {
					name = id.Name
				}
			}
			if name != "" {
				off := int(ce.Pos()) - g.base
				r.calls[name] = append(r.calls[name], callSite{Path: p, Sym: g.symbolAt(off), Line: r.fset.Position(ce.Pos()).Line})
			}
			return true
		})
	}
	return r.calls
}

// funcDecl returns the declaration named sym ("func X" / "func (T) X") in p.
func (r *repo) funcDecl(p, sym string) *ast.FuncDecl {
	g := r.parse(p)
	if g == nil {
		return nil
	}
	for _, d := range g.file.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && funcName(fd) == sym {
			return fd
		}
	}
	return nil
}

// isPredicate reports a function returning exactly one bool: the shape of
// a classifier the engine branches on (IsArrayValued, ComparisonFitted).
func isPredicate(fd *ast.FuncDecl) bool {
	if fd == nil || fd.Type.Results == nil || len(fd.Type.Results.List) != 1 || len(fd.Type.Results.List[0].Names) > 1 {
		return false
	}
	id, ok := fd.Type.Results.List[0].Type.(*ast.Ident)
	return ok && id.Name == "bool"
}

// estimateNameRe picks a declaration that sizes operator state: the
// memory estimates predict and the limits reproduce by hand.
var estimateNameRe = regexp.MustCompile(`(?:Bytes|bytes)$|(?i:estimat|footprint|memory)`)

// predicateMaxDepth bounds the caller walk (predicate -> predicate -> …).
const predicateMaxDepth = 4

// commonNameCallers is the call count past which a name is too generic
// to expand (a method name shared by unrelated types).
const commonNameCallers = 80

// derivedSection lists the callers of the operator's selective
// predicates (transitively, through further predicates) and of the size
// estimates declared in its files.
func (r *repo) derivedSection(a *catAnalysis, idx int, listed map[string]bool) (Section, []string) {
	sec := Section{
		Class: "derived",
		Title: "Reached without the name — callers of its predicates and size estimates (check each branch / re-derive each figure)",
		Hint:  "a predicate naming the operator (and naming few of its category) is followed through further bool predicates; an estimate is a *Bytes / estimate / memory declaration in an operator file",
	}
	calls := r.callIndex()
	type root struct {
		name, via string
		pred      bool
	}
	var roots []root
	var notes []string
	op := a.c.Ops[idx]
	seenRoot := map[string]bool{}
	var paths []string
	for p := range a.byOp[idx] {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	estimates := map[string]*Site{}
	addEstimate := func(p string, sym Symbol) {
		if estimates[p] == nil {
			estimates[p] = &Site{Path: p, Note: "size estimates (re-derive if the operator's state changes)"}
		}
		estimates[p].Symbols = append(estimates[p].Symbols, sym)
	}
	for _, p := range paths {
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			continue
		}
		g := r.parse(p)
		if g == nil {
			continue
		}
		n := normPath(p, op)
		for _, s := range r.resolve(p, a.byOp[idx][p]) {
			if s.CommentOnly || !strings.HasPrefix(s.Name, "func ") {
				continue
			}
			fd := r.funcDecl(p, s.Name)
			if !isPredicate(fd) {
				continue
			}
			// selective: a predicate naming most of the category is a
			// category table (Streamable, Mergeable), not a property.
			if k := len(a.symOps[siteKey{n, s.Name}]); k > 1 && a.isEvery(k) {
				continue
			}
			if !seenRoot[fd.Name.Name] {
				seenRoot[fd.Name.Name] = true
				roots = append(roots, root{fd.Name.Name, fd.Name.Name + " (names " + op.Name + ", " + p + ")", true})
			}
		}
		// estimates: every sizing declaration in a file the operator lands in
		for _, d := range g.file.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				if estimateNameRe.MatchString(d.Name.Name) {
					addEstimate(p, Symbol{Name: funcName(d), Lines: []int{r.fset.Position(d.Pos()).Line}})
					if !seenRoot[d.Name.Name] {
						seenRoot[d.Name.Name] = true
						roots = append(roots, root{d.Name.Name, d.Name.Name + " (estimate, " + p + ")", false})
					}
				}
			case *ast.GenDecl:
				if d.Tok != token.CONST && d.Tok != token.VAR {
					continue
				}
				for _, sp := range d.Specs {
					vs, ok := sp.(*ast.ValueSpec)
					if !ok {
						continue
					}
					for _, id := range vs.Names {
						if estimateNameRe.MatchString(id.Name) {
							addEstimate(p, Symbol{Name: d.Tok.String() + " " + id.Name, Lines: []int{r.fset.Position(id.Pos()).Line}})
						}
					}
				}
			}
		}
	}
	for _, p := range paths {
		if s := estimates[p]; s != nil {
			sec.Sites = append(sec.Sites, *s)
		}
	}

	byPath := map[string]*Site{}
	var order []string
	add := func(cs callSite, note string) {
		if listed[cs.Path+"\x00"+cs.Sym] {
			return
		}
		s, ok := byPath[cs.Path]
		if !ok {
			s = &Site{Path: cs.Path}
			byPath[cs.Path] = s
			order = append(order, cs.Path)
		}
		for i := range s.Symbols {
			if s.Symbols[i].Name == cs.Sym {
				if !containsInt(s.Symbols[i].Lines, cs.Line) {
					s.Symbols[i].Lines = append(s.Symbols[i].Lines, cs.Line)
				}
				return
			}
		}
		s.Symbols = append(s.Symbols, Symbol{Name: cs.Sym, Lines: []int{cs.Line}, Note: note})
	}
	visited := map[string]bool{}
	var walk func(name, chain string, pred bool, depth int)
	walk = func(name, chain string, pred bool, depth int) {
		if visited[name] {
			return
		}
		visited[name] = true
		cs := calls[name]
		if len(cs) > commonNameCallers {
			notes = append(notes, fmt.Sprintf("%s has %d call sites — name too common to expand", name, len(cs)))
			return
		}
		for _, c := range cs {
			if c.Sym == "func "+name || strings.HasSuffix(c.Sym, ") "+name) {
				continue // recursion inside the declaration itself
			}
			add(c, "via "+chain)
			if !pred || depth >= predicateMaxDepth || !strings.HasPrefix(c.Sym, "func ") {
				continue
			}
			if fd := r.funcDecl(c.Path, c.Sym); isPredicate(fd) {
				walk(fd.Name.Name, fd.Name.Name+" ← "+chain, true, depth+1)
			}
		}
	}
	for _, rt := range roots {
		walk(rt.name, rt.via, rt.pred, 1)
	}
	sort.Strings(order)
	for _, p := range order {
		s := byPath[p]
		for i := range s.Symbols {
			sort.Ints(s.Symbols[i].Lines)
		}
		sec.Sites = append(sec.Sites, *s)
	}
	return sec, notes
}

func containsInt(xs []int, x int) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

// enumParam is one enum param an operator's capability declares.
type enumParam struct {
	Param  string
	Path   string
	Line   int
	Expr   string
	Values []string
	Consts []string // Go constant identifiers spelling the values
}

// paramEnums reads the operator's capability declaration
// (internal/descriptor/capabilities_*.go) by AST: every Param literal
// with EnumValues inside the literal whose Name is the operator, plus
// those of same-package helpers that literal references.
func (r *repo) paramEnums(op operator) []enumParam {
	const capDir = "internal/descriptor"
	tops := map[string]ast.Node{} // package-level decl name -> node
	type capFile struct {
		p string
		g *goFile
	}
	var files []capFile
	for _, p := range r.files {
		if path.Dir(p) != capDir || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			continue
		}
		g := r.parse(p)
		if g == nil {
			continue
		}
		files = append(files, capFile{p, g})
		for _, d := range g.file.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				if d.Recv == nil {
					tops[d.Name.Name] = d
				}
			case *ast.GenDecl:
				for _, sp := range d.Specs {
					if vs, ok := sp.(*ast.ValueSpec); ok {
						for _, id := range vs.Names {
							tops[id.Name] = vs
						}
					}
				}
			}
		}
	}
	names := map[string]bool{op.Name: true}
	for _, c := range op.Consts {
		names[c] = true
	}
	namesOp := func(e ast.Expr) bool {
		found := false
		ast.Inspect(e, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.Ident:
				found = found || names[n.Name]
			case *ast.BasicLit:
				if v, err := strconv.Unquote(n.Value); err == nil && names[v] {
					found = true
				}
			}
			return !found
		})
		return found
	}
	var out []enumParam
	seen := map[string]bool{}
	nodeFile := func(n ast.Node) capFile {
		for _, f := range files {
			if n.Pos() >= f.g.file.Pos() && n.Pos() < f.g.file.End() {
				return f
			}
		}
		return capFile{}
	}
	var locals map[string]ast.Node // `name := …` inside the declaring function
	var collect func(n ast.Node, depth int)
	collect = func(n ast.Node, depth int) {
		f := nodeFile(n)
		if f.g == nil {
			return
		}
		ast.Inspect(n, func(x ast.Node) bool {
			switch x := x.(type) {
			case *ast.CompositeLit:
				var pname string
				var enum ast.Expr
				for _, e := range x.Elts {
					kv, ok := e.(*ast.KeyValueExpr)
					if !ok {
						continue
					}
					k, ok := kv.Key.(*ast.Ident)
					if !ok {
						continue
					}
					switch k.Name {
					case "Name":
						if bl, ok := kv.Value.(*ast.BasicLit); ok {
							pname, _ = strconv.Unquote(bl.Value)
						}
					case "EnumValues":
						enum = kv.Value
					}
				}
				if enum != nil && pname != "" && !seen[pname] {
					seen[pname] = true
					ep := enumParam{Param: pname, Path: f.p, Line: r.fset.Position(enum.Pos()).Line,
						Expr: string(r.read(f.p)[int(enum.Pos())-f.g.base : int(enum.End())-f.g.base])}
					r.resolveEnum(f.p, f.g, enum, &ep)
					out = append(out, ep)
				}
			case *ast.Ident:
				if depth == 0 {
					if d, ok := locals[x.Name]; ok {
						collect(d, 1)
					} else if d, ok := tops[x.Name]; ok && d != n {
						collect(d, 1)
					}
				}
			}
			return true
		})
	}
	for _, f := range files {
		ast.Inspect(f.g.file, func(n ast.Node) bool {
			if fd, ok := n.(*ast.FuncDecl); ok {
				locals = map[string]ast.Node{}
				if fd.Body != nil {
					ast.Inspect(fd.Body, func(x ast.Node) bool {
						if as, ok := x.(*ast.AssignStmt); ok && as.Tok == token.DEFINE && len(as.Lhs) == len(as.Rhs) {
							for i, l := range as.Lhs {
								if id, ok := l.(*ast.Ident); ok {
									locals[id.Name] = as.Rhs[i]
								}
							}
						}
						return true
					})
				}
				return true
			}
			cl, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			for _, e := range cl.Elts {
				if kv, ok := e.(*ast.KeyValueExpr); ok {
					if k, ok := kv.Key.(*ast.Ident); ok && k.Name == "Name" && namesOp(kv.Value) {
						collect(cl, 0)
						return false
					}
				}
			}
			return true
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Param < out[j].Param })
	return out
}

// resolveEnum fills an enum param's values and the constants spelling
// them: a pkg.Func() call is resolved to the constants its body lists, a
// []string literal element by element.
func (r *repo) resolveEnum(p string, g *goFile, e ast.Expr, ep *enumParam) {
	imports := map[string]string{} // local name -> repo dir
	for _, im := range g.file.Imports {
		ip, _ := strconv.Unquote(im.Path.Value)
		if !strings.HasPrefix(ip, modulePath+"/") {
			continue
		}
		dir := strings.TrimPrefix(ip, modulePath+"/")
		name := path.Base(dir)
		if im.Name != nil {
			name = im.Name.Name
		}
		imports[name] = dir
	}
	addConst := func(dir, name string) {
		v, ok := r.constValue(dir, name)
		if !ok {
			return
		}
		ep.Values = append(ep.Values, v)
		ep.Consts = append(ep.Consts, name)
	}
	switch e := e.(type) {
	case *ast.CallExpr:
		sel, ok := e.Fun.(*ast.SelectorExpr)
		if !ok {
			return
		}
		x, ok := sel.X.(*ast.Ident)
		if !ok {
			return
		}
		dir, ok := imports[x.Name]
		if !ok {
			return
		}
		for _, f := range r.files {
			if path.Dir(f) != dir || !strings.HasSuffix(f, ".go") || strings.HasSuffix(f, "_test.go") {
				continue
			}
			fd := r.funcDecl(f, "func "+sel.Sel.Name)
			if fd == nil || fd.Body == nil {
				continue
			}
			// only a returned literal lists the values; a function that
			// derives them (a registry walk) is reported by expression
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				ret, ok := n.(*ast.ReturnStmt)
				if !ok {
					return true
				}
				for _, res := range ret.Results {
					if cl, ok := res.(*ast.CompositeLit); ok {
						for _, el := range cl.Elts {
							if id, ok := el.(*ast.Ident); ok {
								addConst(dir, id.Name)
							}
						}
					}
				}
				return false
			})
			return
		}
	case *ast.CompositeLit:
		for _, el := range e.Elts {
			switch el := el.(type) {
			case *ast.BasicLit:
				if v, err := strconv.Unquote(el.Value); err == nil {
					ep.Values = append(ep.Values, v)
				}
			case *ast.SelectorExpr:
				if x, ok := el.X.(*ast.Ident); ok {
					if dir, ok := imports[x.Name]; ok {
						addConst(dir, el.Sel.Name)
					}
				}
			case *ast.Ident:
				addConst(path.Dir(p), el.Name)
			}
		}
	}
}

// constValue returns the string value of constant name declared in dir.
func (r *repo) constValue(dir, name string) (string, bool) {
	if r.consts == nil {
		r.consts = map[string]map[string]string{}
	}
	m, ok := r.consts[dir]
	if !ok {
		m = map[string]string{}
		for _, f := range r.files {
			if path.Dir(f) != dir || !strings.HasSuffix(f, ".go") || strings.HasSuffix(f, "_test.go") {
				continue
			}
			g := r.parse(f)
			if g == nil {
				continue
			}
			for _, d := range g.file.Decls {
				gd, ok := d.(*ast.GenDecl)
				if !ok || gd.Tok != token.CONST {
					continue
				}
				for _, sp := range gd.Specs {
					vs := sp.(*ast.ValueSpec)
					for i, id := range vs.Names {
						if i < len(vs.Values) {
							if bl, ok := vs.Values[i].(*ast.BasicLit); ok && bl.Kind == token.STRING {
								if v, err := strconv.Unquote(bl.Value); err == nil {
									m[id.Name] = v
								}
							}
						}
					}
				}
			}
		}
		r.consts[dir] = m
	}
	v, ok := m[name]
	return v, ok
}

// paramEnumSection lists the operator's enum params and every non-test
// declaration using one of the constants that spell their values — the
// dispatch arms, validators and refusals a new or renamed value needs.
func (r *repo) paramEnumSection(op operator) (Section, bool) {
	eps := r.paramEnums(op)
	if len(eps) == 0 {
		return Section{}, false
	}
	sec := Section{
		Class: "param-enums",
		Title: "Param enums — every value is a branch: its dispatch arm, validation, skill ## Params line and an example or test",
		Hint:  "from the operator's capability declaration (EnumValues); the sites below use a constant spelling a value",
	}
	declared := Site{Path: eps[0].Path}
	m := &matcher{words: map[string][]int{}}
	var consts []string
	for _, ep := range eps {
		note := ep.Expr
		if len(ep.Values) > 0 && !strings.HasPrefix(ep.Expr, "[]string{\"") {
			note += " = " + strings.Join(ep.Values, ", ")
		}
		declared.Symbols = append(declared.Symbols, Symbol{Name: "params." + ep.Param, Lines: []int{ep.Line}, Note: note})
		for _, c := range ep.Consts {
			if len(c) >= 6 && m.words[c] == nil {
				m.words[c] = []int{len(consts)}
				consts = append(consts, c)
			}
		}
	}
	sec.Sites = append(sec.Sites, declared)
	if len(consts) == 0 {
		return sec, true
	}
	byPath := map[string][]hit{}
	for _, h := range r.scan(m, func(p string) bool { return strings.HasSuffix(p, ".go") && !strings.HasSuffix(p, "_test.go") }) {
		byPath[h.Path] = append(byPath[h.Path], h)
	}
	var paths []string
	for p := range byPath {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		hs := byPath[p]
		s := Site{Path: p}
		g := r.parse(p)
		used := map[string][]string{}
		for _, h := range hs {
			if g == nil {
				continue
			}
			sym := g.symbolAt(h.Off)
			if !contains(used[sym], consts[h.Op]) {
				used[sym] = append(used[sym], consts[h.Op])
			}
		}
		for _, sh := range r.resolve(p, hs) {
			if sh.CommentOnly {
				continue
			}
			sort.Strings(used[sh.Name])
			s.Symbols = append(s.Symbols, Symbol{Name: sh.Name, Lines: sh.Lines, Note: "uses " + strings.Join(used[sh.Name], ", ")})
		}
		if len(s.Symbols) > 0 {
			sec.Sites = append(sec.Sites, s)
		}
	}
	return sec, true
}

// oracleRefDir holds the R oracle goldens (`make reference`).
const oracleRefDir = "internal/processing/testdata/reference/"

var stringLitRe = regexp.MustCompile(`"([A-Za-z0-9_./-]+)"`)

// oracleSection lists the R oracle fixtures the operator's tests read
// (by string literal) or that name the operator, and the scripts under
// scripts/reference/ that write them.
//
// A test file named for the operator is read whole (testDecls maps it to
// no symbols); any other only in the declarations naming the operator, so
// a shared oracle test listing many fixtures contributes just the ones
// the operator's own cases open.
func (r *repo) oracleSection(testDecls map[string][]string, namedFixtures []string) (Section, []string) {
	stems := map[string]string{} // fixture stem -> path
	for _, p := range r.files {
		if strings.HasPrefix(p, oracleRefDir) && path.Dir(p)+"/" == oracleRefDir && path.Ext(p) == ".json" {
			stems[strings.TrimSuffix(path.Base(p), ".json")] = p
		}
	}
	readers := map[string][]string{} // fixture path -> tests
	for _, p := range namedFixtures {
		if _, ok := readers[p]; !ok && strings.HasPrefix(p, oracleRefDir) {
			readers[p] = nil
		}
	}
	var testPaths []string
	for p := range testDecls {
		testPaths = append(testPaths, p)
	}
	sort.Strings(testPaths)
	for _, t := range testPaths {
		src := r.read(t) // a file named for the operator: whole
		if syms := testDecls[t]; len(syms) > 0 {
			src = nil
			for _, sym := range syms {
				src = append(src, r.declSource(t, sym)...)
			}
		}
		for _, m := range stringLitRe.FindAllSubmatch(src, -1) {
			stem := strings.TrimSuffix(path.Base(string(m[1])), ".json")
			if f, ok := stems[stem]; ok && !contains(readers[f], t) {
				readers[f] = append(readers[f], t)
			}
		}
	}
	if len(readers) == 0 {
		return Section{}, nil
	}
	sec := Section{
		Class: "oracle",
		Title: "R oracle fixtures — regenerate with `make reference`, compare within the documented tolerance, never hand-edit",
		Hint:  "fixtures the operator's tests read (or that name it) and the scripts/reference/ generators that write them",
	}
	var fixtures []string
	for f := range readers {
		fixtures = append(fixtures, f)
	}
	sort.Strings(fixtures)
	gens := map[string][]string{}
	for _, f := range fixtures {
		note := "names the operator"
		if len(readers[f]) > 0 {
			sort.Strings(readers[f])
			note = "read by " + strings.Join(readers[f], ", ")
		}
		sec.Sites = append(sec.Sites, Site{Path: f, Note: note})
		stem := strings.TrimSuffix(path.Base(f), ".json")
		stemRe := regexp.MustCompile(`(^|[^A-Za-z0-9_])` + regexp.QuoteMeta(stem) + `([^A-Za-z0-9_]|$)`)
		for _, p := range r.files {
			if strings.HasPrefix(p, "scripts/reference/") && stemRe.Match(r.read(p)) && !contains(gens[p], stem) {
				gens[p] = append(gens[p], stem)
			}
		}
	}
	var gp []string
	for p := range gens {
		gp = append(gp, p)
	}
	sort.Strings(gp)
	for _, p := range gp {
		sec.Sites = append(sec.Sites, Site{Path: p, Note: "writes " + strings.Join(gens[p], ", ")})
	}
	return sec, fixtures
}

// declSource returns the source of the top-level declaration named sym
// (as symbolAt names it) in p, doc comment excluded.
func (r *repo) declSource(p, sym string) []byte {
	g := r.parse(p)
	src := r.read(p)
	if g == nil || src == nil {
		return nil
	}
	for _, d := range g.file.Decls {
		name := ""
		switch d := d.(type) {
		case *ast.FuncDecl:
			name = funcName(d)
		case *ast.GenDecl:
			name = genName(d, d.Pos())
		}
		if name == sym {
			return src[int(d.Pos())-g.base : int(d.End())-g.base]
		}
	}
	return nil
}

// benchGates are the benchmarks of the packages the operator's non-test
// Go sites live in, plus the Makefile targets that run those packages
// (a target running every package, like `test`, is omitted).
func (r *repo) benchGates(dirs []string) []Gate {
	idx := r.testIndex()
	benchDirs := map[string][]string{}
	for name, ds := range idx {
		if !strings.HasPrefix(name, "Benchmark") {
			continue
		}
		for _, d := range ds {
			if contains(dirs, d) {
				benchDirs[d] = append(benchDirs[d], name)
			}
		}
	}
	var out []Gate
	var bd []string
	for d := range benchDirs {
		bd = append(bd, d)
	}
	sort.Strings(bd)
	for _, d := range bd {
		pkg := "./" + d + "/"
		if d == "." {
			pkg = "./"
		}
		bs := benchDirs[d]
		sort.Strings(bs)
		out = append(out, Gate{Why: "benchmarks in the operator's packages — rerun (benchstat vs a baseline) on a hot-path change", Package: d, Tests: bs,
			Command: "go test -count=1 -run '^$' -bench . -benchmem " + pkg + "   # " + abbreviate(bs, 3)})
	}
	for _, mt := range r.makeTargets() {
		var hit []string
		for _, d := range dirs {
			if mt.covers(d) && !contains(hit, d) {
				hit = append(hit, d)
			}
		}
		if len(hit) > 0 && !mt.all {
			sort.Strings(hit)
			out = append(out, Gate{Why: "Makefile targets that run the operator's packages", Package: ".",
				Command: "make " + mt.name + "   # runs " + abbreviate(hit, 4)})
		}
	}
	return out
}

// makeTarget is one Makefile rule with the package patterns its recipe runs.
type makeTarget struct {
	name     string
	patterns []string // "./internal/service/...", ".", "./encoding/..."
	all      bool     // runs ./... (every package)
}

var makePkgRe = regexp.MustCompile(`(?:^|\s)(\.(?:/[A-Za-z0-9_./-]*)?)(?:\s|$)`)

func (r *repo) makeTargets() []makeTarget {
	src := r.read("Makefile")
	if src == nil {
		return nil
	}
	var out []makeTarget
	var cur *makeTarget
	sc := bufio.NewScanner(bytes.NewReader(src))
	ruleRe := regexp.MustCompile(`^([A-Za-z0-9_.-]+):(?:[^=]|$)`)
	for sc.Scan() {
		line := sc.Text()
		if m := ruleRe.FindStringSubmatch(line); m != nil && !strings.HasPrefix(m[1], ".") {
			out = append(out, makeTarget{name: m[1]})
			cur = &out[len(out)-1]
			continue
		}
		if cur == nil || !strings.HasPrefix(line, "\t") || strings.Contains(line, "cd ") {
			if !strings.HasPrefix(line, "\t") {
				cur = nil
			}
			continue
		}
		if !strings.Contains(line, "$(GO) test") && !strings.Contains(line, "go test") {
			continue
		}
		for _, f := range strings.Fields(line) {
			if m := makePkgRe.FindStringSubmatch(" " + f + " "); m != nil {
				if m[1] == "./..." {
					cur.all = true
				}
				cur.patterns = append(cur.patterns, m[1])
			}
		}
	}
	var keep []makeTarget
	for _, t := range out {
		if len(t.patterns) > 0 {
			keep = append(keep, t)
		}
	}
	return keep
}

// covers reports whether the target's package patterns include dir.
func (t makeTarget) covers(dir string) bool {
	for _, p := range t.patterns {
		switch {
		case p == "./...":
			return true
		case p == ".":
			if dir == "." {
				return true
			}
		case strings.HasSuffix(p, "/..."):
			base := strings.TrimSuffix(strings.TrimPrefix(p, "./"), "/...")
			if dir == base || strings.HasPrefix(dir, base+"/") {
				return true
			}
		default:
			if strings.TrimSuffix(strings.TrimPrefix(p, "./"), "/") == dir {
				return true
			}
		}
	}
	return false
}

var exampleNumRe = regexp.MustCompile(`^([0-9]+)_`)

// nextExampleNumber is the next free NN_ prefix in dir, zero-padded to
// the width its files use.
func (r *repo) nextExampleNumber(dir string) string {
	maxN, width := 0, 2
	for _, p := range r.files {
		if path.Dir(p) != dir {
			continue
		}
		if m := exampleNumRe.FindStringSubmatch(path.Base(p)); m != nil {
			n, _ := strconv.Atoi(m[1])
			if n > maxN {
				maxN = n
			}
			if len(m[1]) > width {
				width = len(m[1])
			}
		}
	}
	return fmt.Sprintf("%0*d", width, maxN+1)
}
