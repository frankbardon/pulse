package main

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
)

// siteKey is a (normalised path, symbol) pair.
type siteKey struct{ norm, sym string }

// catAnalysis is every operator's occurrence set for one category,
// with per-operator file names folded into "<op>" so the operators'
// sites can be compared.
type catAnalysis struct {
	c       *category
	byOp    []map[string][]hit // op -> path -> hits
	fileOps map[string]map[int]bool
	actual  map[string]map[int]string // norm -> op -> real path
	symOps  map[siteKey]map[int]bool
	symLine map[siteKey]map[int][]int
	symCmnt map[siteKey]bool // every hit of the symbol is inside a Go comment
	// enumerators / iterGates: the registry function's call sites
	enumerators map[string][]string // non-test path -> symbols calling AllFunc
	tables      []string            // category-wide table symbols (var / func names)
	exemplar    int
}

var digitsPrefix = regexp.MustCompile(`^[0-9]+_`)

// normPath folds an operator's own name out of a file's base name:
// internal/skills/op-mat-pca.md -> internal/skills/op-mat-<op>.md,
// internal/examples/matrices/09_pca.json -> …/NN_<op>.json.
func normPath(p string, op operator) string {
	dir, base := path.Split(p)
	for _, form := range []string{op.Snake, op.Kebab} {
		base = replaceBounded(base, form, "<op>")
	}
	if strings.Contains(base, "<op>") {
		base = digitsPrefix.ReplaceAllString(base, "NN_")
	}
	return dir + base
}

func isSep(b byte) bool { return b == '_' || b == '-' || b == '.' }

func replaceBounded(s, old, repl string) string {
	if old == "" {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		if strings.HasPrefix(s[i:], old) && (i == 0 || isSep(s[i-1])) && (i+len(old) == len(s) || isSep(s[i+len(old)])) {
			b.WriteString(repl)
			i += len(old)
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

func (r *repo) analyze(c *category) *catAnalysis {
	r.discoverConstAliases(c)
	all := make([]int, len(c.Ops))
	for i := range all {
		all[i] = i
	}
	a := &catAnalysis{
		c:       c,
		byOp:    make([]map[string][]hit, len(c.Ops)),
		fileOps: map[string]map[int]bool{},
		actual:  map[string]map[int]string{},
		symOps:  map[siteKey]map[int]bool{},
		symLine: map[siteKey]map[int][]int{},
		symCmnt: map[siteKey]bool{},
	}
	for i := range a.byOp {
		a.byOp[i] = map[string][]hit{}
	}
	for _, h := range r.scan(matcherFor(c, all), nil) {
		a.byOp[h.Op][h.Path] = append(a.byOp[h.Op][h.Path], h)
	}
	best := -1
	for i, files := range a.byOp {
		if len(files) > best {
			best, a.exemplar = len(files), i
		}
		for p, hs := range files {
			sort.Slice(hs, func(x, y int) bool { return hs[x].Off < hs[y].Off })
			n := normPath(p, c.Ops[i])
			add(a.fileOps, n, i)
			if a.actual[n] == nil {
				a.actual[n] = map[int]string{}
			}
			a.actual[n][i] = p
			for _, s := range r.resolve(p, hs) {
				k := siteKey{n, s.Name}
				if _, seen := a.symOps[k]; !seen {
					a.symCmnt[k] = true
				}
				addK(a.symOps, k, i)
				if a.symLine[k] == nil {
					a.symLine[k] = map[int][]int{}
				}
				a.symLine[k][i] = s.Lines
				if !s.CommentOnly {
					a.symCmnt[k] = false
				}
			}
		}
	}
	a.findEnumerators(r)
	return a
}

func add(m map[string]map[int]bool, k string, i int) {
	if m[k] == nil {
		m[k] = map[int]bool{}
	}
	m[k][i] = true
}

func addK(m map[siteKey]map[int]bool, k siteKey, i int) {
	if m[k] == nil {
		m[k] = map[int]bool{}
	}
	m[k][i] = true
}

// everyThreshold is the share of a category's operators a site must
// carry to count as a site EVERY operator lands in.
const everyThreshold = 0.75

// mostThreshold is the share above which a conditional site is still
// listed with its symbols (below it: one compact line).
const mostThreshold = 0.34

func (a *catAnalysis) n() int { return len(a.c.Ops) }

func (a *catAnalysis) cov(k int) string { return fmt.Sprintf("%d/%d ops", k, a.n()) }

func (a *catAnalysis) isEvery(k int) bool {
	return k >= 2 && float64(k) >= everyThreshold*float64(a.n())
}

// findEnumerators locates the registry function's call sites and the
// category-wide tables (non-test vars / package funcs most operators
// appear in) — the symbols the category gates iterate.
func (a *catAnalysis) findEnumerators(r *repo) {
	a.enumerators = map[string][]string{}
	m := &matcher{words: map[string][]int{a.c.AllFunc: {0}}}
	byPath := map[string][]hit{}
	for _, h := range r.scan(m, func(p string) bool { return strings.HasSuffix(p, ".go") && !strings.HasSuffix(p, "_test.go") }) {
		byPath[h.Path] = append(byPath[h.Path], h)
	}
	for p, hs := range byPath {
		for _, s := range r.resolve(p, hs) {
			if s.CommentOnly || s.Name == "func "+a.c.AllFunc {
				continue
			}
			a.enumerators[p] = append(a.enumerators[p], s.Name)
		}
	}
	seen := map[string]bool{}
	for k, ops := range a.symOps {
		if !a.isEvery(len(ops)) || a.symCmnt[k] || !strings.HasSuffix(k.norm, ".go") || strings.HasSuffix(k.norm, "_test.go") {
			continue
		}
		name := ""
		switch {
		case strings.HasPrefix(k.sym, "var "):
			name = strings.TrimPrefix(k.sym, "var ")
		case strings.HasPrefix(k.sym, "func ") && !strings.HasPrefix(k.sym, "func ("):
			name = strings.TrimPrefix(k.sym, "func ")
		}
		if len(name) >= 6 && name != a.c.AllFunc && !seen[path.Dir(k.norm)+"."+name] {
			seen[path.Dir(k.norm)+"."+name] = true
			a.tables = append(a.tables, path.Dir(k.norm)+"."+name)
		}
	}
	sort.Strings(a.tables)
}

// categoryGates are the test functions that iterate the registry
// function or reference a category-wide table: they cover every
// operator of the category, a new one included.
func (a *catAnalysis) categoryGates(r *repo) []Gate {
	tableDir := map[string][]string{} // name -> dirs it is declared in
	names := []string{a.c.AllFunc}
	for _, t := range a.tables {
		i := strings.LastIndex(t, ".")
		dir, name := t[:i], t[i+1:]
		if tableDir[name] == nil {
			names = append(names, name)
		}
		tableDir[name] = append(tableDir[name], dir)
	}
	m := &matcher{words: map[string][]int{}}
	for i, w := range names {
		m.words[w] = []int{i}
	}
	byPath := map[string][]hit{}
	for _, h := range r.scan(m, func(p string) bool { return strings.HasSuffix(p, "_test.go") }) {
		name := names[h.Op]
		exported := name[0] >= 'A' && name[0] <= 'Z'
		if !exported && !contains(tableDir[name], path.Dir(h.Path)) {
			continue
		}
		byPath[h.Path] = append(byPath[h.Path], h)
	}
	byDir := map[string][]string{}
	for p, hs := range byPath {
		for _, s := range r.resolve(p, hs) {
			if t := testName(s.Name); t != "" && !s.CommentOnly {
				byDir[path.Dir(p)] = append(byDir[path.Dir(p)], t)
			}
		}
	}
	return gatesFromDirs(fmt.Sprintf("category gates: iterate %s.%s or a category-wide table (%s)",
		path.Base(a.c.Dir), a.c.AllFunc, abbreviate(tableNames(a.tables), 4)), byDir)
}

func tableNames(ts []string) []string {
	var out []string
	for _, t := range ts {
		out = append(out, t[strings.LastIndex(t, ".")+1:])
	}
	return out
}

// testName extracts a Test/Benchmark/Fuzz function name from a symbol.
func testName(sym string) string {
	name, ok := strings.CutPrefix(sym, "func ")
	if !ok || strings.HasPrefix(name, "(") {
		return ""
	}
	for _, p := range []string{"Test", "Benchmark", "Fuzz"} {
		if strings.HasPrefix(name, p) {
			return name
		}
	}
	return ""
}

// enumeratorSection lists the non-test call sites of the registry
// function: they pick a new operator up automatically.
func (a *catAnalysis) enumeratorSection() Section {
	sec := Section{
		Class: "enumerators",
		Title: fmt.Sprintf("Enumerators — call %s.%s(), so a new operator flows through automatically (verify; usually no edit)", path.Base(a.c.Dir), a.c.AllFunc),
	}
	var paths []string
	for p := range a.enumerators {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		s := Site{Path: p}
		for _, sym := range a.enumerators[p] {
			s.Symbols = append(s.Symbols, Symbol{Name: sym})
		}
		sec.Sites = append(sec.Sites, s)
	}
	return sec
}
