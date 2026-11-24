package main

import (
	"fmt"
	"go/ast"
	"path"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Sites an operator reaches through what it declares or calls rather
// than through its name:
//
//   - param index: every param its capability declares (not only the
//     enums), the struct fields that decode it in the operator's own
//     sites, and the decoders of each FORM its description spells as
//     an object key ({"centroid": …}) — a new param or form lands there;
//   - shared primitives: the module functions the operator's own files
//     (named for it) call outside types / errors and their own package
//     (internal/seriesfield, internal/statdist, …), with the tests that
//     exercise them;
//   - metric consumers: every declaration that registers, resolves or
//     reports a linalg/metric consumer, for an operator that consumes
//     the metric registry (or a new operator of a category that does).

const metricDir = "linalg/metric"

// formKeyRe matches an object opening on its first key: {"centroid".
var formKeyRe = regexp.MustCompile(`^\{\s*"([a-z][a-z0-9_]*)"`)

// formKeys are the object keys a param description spells as forms —
// {"centroid": {"by": …}} gives centroid, never the nested by.
func formKeys(desc string) []string {
	var out []string
	depth := 0
	for i := 0; i < len(desc); i++ {
		switch desc[i] {
		case '{':
			if depth == 0 {
				if m := formKeyRe.FindStringSubmatch(desc[i:]); m != nil && !contains(out, m[1]) {
					out = append(out, m[1])
				}
			}
			depth++
		case '}':
			if depth > 0 {
				depth--
			}
		}
	}
	return out
}

// sectionAdder accumulates sites by path and symbol.
type sectionAdder struct {
	sites map[string]*Site
}

func (s *sectionAdder) add(p, sym string, lines []int, note string) {
	if s.sites == nil {
		s.sites = map[string]*Site{}
	}
	site := s.sites[p]
	if site == nil {
		site = &Site{Path: p}
		s.sites[p] = site
	}
	for i := range site.Symbols {
		if site.Symbols[i].Name == sym {
			site.Symbols[i].Lines = dedupeInts(sortedInts(append(site.Symbols[i].Lines, lines...)))
			if note != "" && !strings.Contains(site.Symbols[i].Note, note) {
				site.Symbols[i].Note += "; " + note
			}
			return
		}
	}
	site.Symbols = append(site.Symbols, Symbol{Name: sym, Lines: dedupeInts(sortedInts(lines)), Note: note})
}

func (s *sectionAdder) list() []Site {
	var paths []string
	for p := range s.sites {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var out []Site
	for _, p := range paths {
		out = append(out, *s.sites[p])
	}
	return out
}

// jsonTagged maps every json tag of a struct field in a Go file
// (named or anonymous struct types) to the declarations holding it.
func (r *repo) jsonTagged(p string) map[string]map[string][]int {
	g := r.parse(p)
	if g == nil {
		return nil
	}
	out := map[string]map[string][]int{}
	ast.Inspect(g.file, func(n ast.Node) bool {
		st, ok := n.(*ast.StructType)
		if !ok {
			return true
		}
		for _, f := range st.Fields.List {
			if f.Tag == nil {
				continue
			}
			raw, _ := strconv.Unquote(f.Tag.Value)
			tag, _, _ := strings.Cut(reflect.StructTag(raw).Get("json"), ",")
			if tag == "" || tag == "-" {
				continue
			}
			sym := g.symbolAt(int(f.Pos()) - g.base)
			if out[tag] == nil {
				out[tag] = map[string][]int{}
			}
			out[tag][sym] = append(out[tag][sym], r.fset.Position(f.Pos()).Line)
		}
		return true
	})
	return out
}

// paramIndexSection lists every capability param of the operator and
// its decoders among opFiles (the files naming the operator).
func (r *repo) paramIndexSection(op operator, opFiles []string) (Section, bool) {
	_, params := r.capParams(op)
	if len(params) == 0 {
		return Section{}, false
	}
	sec := Section{
		Class: "params",
		Title: "Param index — every declared param, its decoders and the decoders of each form: a new param or a new form lands at all of them",
		Hint:  "from the capability declaration (every Param, not only enums); decoders are struct fields json-tagged with the param — or with a form key its description spells as {\"key\": …} — in the files that name the operator",
	}
	tagged := map[string]map[string]map[string][]int{} // path -> tag -> sym -> lines
	var decoders []string
	for _, p := range opFiles {
		// the wire types decode request slots, never an operator's params
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") || classify(p) == "history" || path.Dir(p) == "types" {
			continue
		}
		if t := r.jsonTagged(p); len(t) > 0 {
			tagged[p] = t
			decoders = append(decoders, p)
		}
	}
	sort.Strings(decoders)
	var sa sectionAdder
	for _, prm := range params {
		forms := formKeys(prm.Desc)
		note := prm.Type
		if len(forms) > 0 {
			note += "; form keys {" + strings.Join(forms, ", ") + "}"
		}
		sa.add(prm.Path, "params."+prm.Name, []int{prm.Line}, note)
		for _, p := range decoders {
			for sym, ls := range tagged[p][prm.Name] {
				sa.add(p, sym, ls, "decodes params."+prm.Name)
			}
			for _, fk := range forms {
				if fk == prm.Name {
					continue
				}
				for sym, ls := range tagged[p][fk] {
					sa.add(p, sym, ls, fmt.Sprintf("decodes params.%s form {%q}", prm.Name, fk))
				}
			}
		}
	}
	sec.Sites = sa.list()
	return sec, true
}

// moduleImports maps a file's local import names to module dirs.
func moduleImports(f *ast.File) map[string]string {
	out := map[string]string{}
	for _, im := range f.Imports {
		ip, _ := strconv.Unquote(im.Path.Value)
		dir, ok := strings.CutPrefix(ip, modulePath+"/")
		if !ok {
			continue
		}
		name := path.Base(ip)
		if im.Name != nil {
			name = im.Name.Name
		}
		out[name] = dir
	}
	return out
}

// primitiveSection lists the module functions the operator's own files
// (opFiles whose name folds to <op>) call in other packages, skipping
// the wire types, the error codes and sites already listed.
func (r *repo) primitiveSection(op operator, opFiles []string, listed map[string]bool) (Section, []Gate, bool) {
	type callee struct{ dir, fn string }
	callers := map[callee][]string{}
	var order []callee
	for _, p := range opFiles {
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") || normPath(p, op) == p {
			continue
		}
		g := r.parse(p)
		if g == nil {
			continue
		}
		imports := moduleImports(g.file)
		ast.Inspect(g.file, func(n ast.Node) bool {
			ce, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := ce.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			x, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			dir, ok := imports[x.Name]
			if !ok || dir == "types" || dir == "errors" || dir == path.Dir(p) {
				return true
			}
			c := callee{dir, sel.Sel.Name}
			from := p + " · " + g.symbolAt(int(ce.Pos())-g.base)
			if _, seen := callers[c]; !seen {
				order = append(order, c)
			}
			if !contains(callers[c], from) {
				callers[c] = append(callers[c], from)
			}
			return true
		})
	}
	if len(order) == 0 {
		return Section{}, nil, false
	}
	sec := Section{
		Class: "primitives",
		Title: "Shared primitives — module functions its own files call in other packages: a change to the behaviour it relies on reaches them and their tests",
		Hint:  "from the calls in the files named for the operator (types / errors excluded); the gates are the tests of those packages that call the same functions",
	}
	var sa sectionAdder
	byDir := map[string][]string{}
	for _, c := range order {
		sited := false
		for _, p := range r.files {
			if path.Dir(p) != c.dir || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				continue
			}
			if fd := r.funcDecl(p, "func "+c.fn); fd != nil && !listed[p+"\x00func "+c.fn] {
				sited = true
			}
		}
		if !sited {
			continue // a method, a variable or a site already listed
		}
		for _, p := range r.files {
			if path.Dir(p) != c.dir || !strings.HasSuffix(p, ".go") {
				continue
			}
			g := r.parse(p)
			if g == nil {
				continue
			}
			if !strings.HasSuffix(p, "_test.go") {
				fd := r.funcDecl(p, "func "+c.fn)
				if fd == nil || listed[p+"\x00func "+c.fn] {
					continue
				}
				cs := callers[c]
				sort.Strings(cs)
				sa.add(p, "func "+c.fn, []int{r.fset.Position(fd.Pos()).Line}, "called from "+abbreviate(cs, 2))
				continue
			}
			for _, d := range g.file.Decls {
				fd, ok := d.(*ast.FuncDecl)
				if !ok || testName(funcName(fd)) == "" || fd.Body == nil {
					continue
				}
				uses := false
				ast.Inspect(fd.Body, func(n ast.Node) bool {
					if id, ok := n.(*ast.Ident); ok && id.Name == c.fn {
						uses = true
					}
					return !uses
				})
				if t := testName(funcName(fd)); uses && !contains(byDir[c.dir], t) {
					byDir[c.dir] = append(byDir[c.dir], t)
				}
			}
		}
	}
	sec.Sites = sa.list()
	return sec, gatesFromDirs("tests of the shared primitives it calls", byDir), len(sec.Sites) > 0
}

// isMetricConsumer reports an operator a linalg/metric Consumer constant
// spells (metric.ConsumerVecDistance = "ATTR_VEC_DISTANCE").
func (r *repo) isMetricConsumer(op operator) bool {
	for _, c := range op.Consts {
		if v, ok := r.constValue(metricDir, c); ok && v == op.Name {
			return true
		}
	}
	return false
}

// metricConsumerAPI reports a linalg/metric identifier of the consumer
// surface: the Consumer type, its constants, Consumers, Accepted and
// ResolveFor.
func metricConsumerAPI(name string) bool {
	switch name {
	case "Consumers", "Accepted", "ResolveFor":
		return true
	}
	return strings.HasPrefix(name, "Consumer")
}

// metricConsumerSection lists every declaration of the metric-registry
// consumer surface: the registry's own consumer tables, every use of the
// consumer API elsewhere, and the callers of functions whose signature
// carries a metric.Consumer (one level). Gates are the tests naming the
// consumer API.
func (r *repo) metricConsumerSection() (Section, []Gate, bool) {
	sec := Section{
		Class: "metric-consumers",
		Title: "Metric consumers — a linalg/metric consumer is registered, resolved and reported at every site below",
		Hint:  "a new consumer adds its Consumer constant to the registry tables, resolves through the consumer API and surfaces in the manifest's consumer lists; derived from the consumer API's uses, never from a list",
	}
	var sa sectionAdder
	wrappers := map[string]bool{} // funcs whose signature carries metric.Consumer
	byDir := map[string][]string{}
	for _, p := range r.files {
		if !strings.HasSuffix(p, ".go") || classify(p) == "history" || strings.HasPrefix(p, selfDir) {
			continue
		}
		g := r.parse(p)
		if g == nil {
			continue
		}
		test := strings.HasSuffix(p, "_test.go")
		inMetric := path.Dir(p) == metricDir
		alias := ""
		if !inMetric {
			alias = importName(g.file, modulePath+"/"+metricDir)
			if alias == "" {
				continue
			}
		}
		for _, d := range g.file.Decls {
			used := map[string]bool{}
			ast.Inspect(d, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.SelectorExpr:
					if x, ok := n.X.(*ast.Ident); ok && !inMetric && x.Name == alias && metricConsumerAPI(n.Sel.Name) {
						used[n.Sel.Name] = true
					}
				case *ast.Ident:
					if inMetric && strings.HasPrefix(n.Name, "Consumer") && n.Name != "Consumer" {
						if _, ok := r.constValue(metricDir, n.Name); ok {
							used[n.Name] = true
						}
					}
				}
				return true
			})
			if len(used) == 0 {
				continue
			}
			sym := g.symbolAt(int(d.Pos()) - g.base)
			if test {
				if t := testName(sym); t != "" && !contains(byDir[path.Dir(p)], t) {
					byDir[path.Dir(p)] = append(byDir[path.Dir(p)], t)
				}
				continue
			}
			if inMetric && len(used) < 2 {
				continue // one constant's own declaration, not a consumer table
			}
			names := sortedKeys(used)
			note := "uses metric." + abbreviate(names, 3)
			if inMetric {
				note = fmt.Sprintf("consumer registry table: %d consumers", len(names))
			}
			sa.add(p, sym, []int{r.fset.Position(d.Pos()).Line}, note)
			if fd, ok := d.(*ast.FuncDecl); ok && !inMetric && signatureNames(fd.Type, alias, "Consumer") {
				wrappers[fd.Name.Name] = true
			}
		}
	}
	idx := r.callIndex()
	for _, w := range sortedKeys(wrappers) {
		for _, c := range idx[w] {
			if path.Dir(c.Path) == metricDir || strings.HasPrefix(c.Path, selfDir) || classify(c.Path) == "history" {
				continue
			}
			sa.add(c.Path, c.Sym, []int{c.Line}, "calls "+w+" (takes a metric.Consumer)")
		}
	}
	sec.Sites = sa.list()
	return sec, gatesFromDirs("tests naming the metric consumer API", byDir), len(sec.Sites) > 0
}

// signatureNames reports a func type whose params or results mention
// alias.name.
func signatureNames(ft *ast.FuncType, alias, name string) bool {
	found := false
	ast.Inspect(ft, func(n ast.Node) bool {
		if s, ok := n.(*ast.SelectorExpr); ok {
			if x, ok := s.X.(*ast.Ident); ok && x.Name == alias && s.Sel.Name == name {
				found = true
			}
		}
		return !found
	})
	return found
}

// categoryConsumesMetrics reports a category one of whose operators is
// a linalg/metric consumer.
func (r *repo) categoryConsumesMetrics(c *category) bool {
	for _, op := range c.Ops {
		if r.isMetricConsumer(op) {
			return true
		}
	}
	return false
}
