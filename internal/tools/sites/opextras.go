package main

import (
	"fmt"
	"go/ast"
	"go/token"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Operator sites found by shape that never spell a site the named ones
// lead to: copies of a declared default, the oracle suite a new fixture's
// test joins, and the book's category catalogue tables.

// defaultCopySection lists, in the non-test declarations naming the
// operator, a numeric literal equal to a Default* constant the
// operator's own files declare, inside a branch keyed on the operator —
// a switch case listing it, or an if whose condition names it: a second
// copy of the default (a bucket count of 4 beside
// opconfig.DefaultQuantileBuckets) that drifts when the constant
// changes. Call the constant, or the helper that applies it, instead.
func (r *repo) defaultCopySection(op operator, opFiles []string) (Section, bool) {
	sec := Section{Class: "default-copies", Title: "Default copies — a literal equal to a declared default, in a branch keyed on the operator (call the constant instead)"}
	aliases := map[string]bool{op.Name: true}
	for _, k := range op.Consts {
		aliases[k] = true
	}
	type def struct{ name, path string }
	defaults := map[float64][]def{}
	var goFiles []string
	for _, p := range opFiles {
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			continue
		}
		goFiles = append(goFiles, p)
		g := r.parse(p)
		if g == nil {
			continue
		}
		for _, d := range g.file.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}
			for _, s := range gd.Specs {
				vs := s.(*ast.ValueSpec)
				for i, n := range vs.Names {
					if !strings.HasPrefix(strings.ToLower(n.Name), "default") || i >= len(vs.Values) {
						continue
					}
					if v, ok := numLit(vs.Values[i]); ok {
						defaults[v] = append(defaults[v], def{n.Name, p})
					}
				}
			}
		}
	}
	if len(defaults) == 0 {
		return sec, false
	}
	names := func(e ast.Node) bool {
		hit := false
		ast.Inspect(e, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.Ident:
				hit = hit || aliases[x.Name]
			case *ast.BasicLit:
				if v, err := strconv.Unquote(x.Value); err == nil && x.Kind == token.STRING {
					hit = hit || aliases[v]
				}
			}
			return !hit
		})
		return hit
	}
	var add sectionAdder
	for _, p := range goFiles {
		g := r.parse(p)
		if g == nil {
			continue
		}
		scan := func(body []ast.Stmt) {
			for _, st := range body {
				ast.Inspect(st, func(n ast.Node) bool {
					bl, ok := n.(*ast.BasicLit)
					if !ok {
						return true
					}
					v, ok := numLit(bl)
					if !ok {
						return true
					}
					for _, d := range defaults[v] {
						add.add(p, g.symbolAt(int(bl.Pos())-g.base), []int{r.fset.Position(bl.Pos()).Line},
							fmt.Sprintf("literal %s = %s (%s)", bl.Value, d.name, d.path))
					}
					return true
				})
			}
		}
		ast.Inspect(g.file, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.CaseClause:
				for _, e := range x.List {
					if names(e) {
						scan(x.Body)
						break
					}
				}
			case *ast.IfStmt:
				if names(x.Cond) {
					scan(x.Body.List)
				}
			}
			return true
		})
	}
	sec.Sites = add.list()
	return sec, len(sec.Sites) > 0
}

// numLit reads an INT or FLOAT literal.
func numLit(e ast.Expr) (float64, bool) {
	bl, ok := e.(*ast.BasicLit)
	if !ok || (bl.Kind != token.INT && bl.Kind != token.FLOAT) {
		return 0, false
	}
	v, err := strconv.ParseFloat(strings.ReplaceAll(bl.Value, "_", ""), 64)
	if err != nil {
		if i, err := strconv.ParseInt(strings.ReplaceAll(bl.Value, "_", ""), 0, 64); err == nil {
			return float64(i), true
		}
		return 0, false
	}
	return v, true
}

var oracleTestRe = regexp.MustCompile(`(?m)^func (TestReferenceOracle_\w*)\(`)

// oracleSuiteMin is the TestReferenceOracle_* functions a test file must
// declare to be an oracle suite.
const oracleSuiteMin = 2

// oracleSuiteSection lists the oracle suites the operator reaches: test
// files declaring at least oracleSuiteMin TestReferenceOracle_* functions
// that name the operator or open one of its fixtures. A NEW oracle
// fixture for the operator (another mode, another param) gets its test
// there, beside its siblings, or `make reference` writes a JSON no test
// reads.
func (r *repo) oracleSuiteSection(op operator, files map[string][]hit, fixtures []string) (Section, []Gate, bool) {
	sec := Section{Class: "oracle-suites", Title: "Oracle suites — a new R oracle fixture for it gets its TestReferenceOracle_* test here"}
	byDir := map[string][]string{}
	for _, p := range r.files {
		if !strings.HasSuffix(p, "_test.go") {
			continue
		}
		src := r.read(p)
		ms := oracleTestRe.FindAllSubmatch(src, -1)
		if len(ms) < oracleSuiteMin {
			continue
		}
		why := ""
		if len(files[p]) > 0 {
			why = "names " + op.Name
		}
		for _, f := range fixtures {
			stem := strings.TrimSuffix(path.Base(f), ".json")
			if why == "" && strings.Contains(string(src), strconv.Quote(stem)) {
				why = "opens " + stem
			}
		}
		if why == "" {
			continue
		}
		var names []string
		for _, m := range ms {
			names = append(names, string(m[1]))
		}
		sec.Sites = append(sec.Sites, Site{Path: p, Note: fmt.Sprintf("%d oracle tests; %s", len(names), why)})
		byDir[path.Dir(p)] = append(byDir[path.Dir(p)], names...)
	}
	return sec, gatesFromDirs("the oracle suites (rerun after `make reference`)", byDir), len(sec.Sites) > 0
}

var catalogueRowRe = regexp.MustCompile("`([A-Z]+_)\\*`")

// catalogueSection lists the Markdown tables whose rows name at least
// three categories by prefix (`AGG_*`, `MAT_*`, …) — the bound-enum
// table of the MCP book page, the skill-routing table — with the
// category's row, or — when the table has none but rows for at least
// fullListPct of the categories — a warning: a table covering most
// categories that skips this one is a gap (or a deliberate omission to
// state).
func (r *repo) catalogueSection(c *category) (Section, []string, bool) {
	sec := Section{Class: "catalogues", Title: "Category catalogue tables — rows keyed by operator prefix (`" + c.Prefix + "*`)"}
	if c.Prefix == "" {
		return sec, nil, false
	}
	prefixes := map[string]bool{}
	for _, d := range categoryTable {
		if d.Prefix != "" {
			prefixes[d.Prefix] = true
		}
	}
	var warns []string
	for _, p := range r.files {
		if path.Ext(p) != ".md" || isHistory(p) || classify(p) == "docs-generated" {
			continue
		}
		src := r.read(p)
		if src == nil || !strings.Contains(string(src), "_*`") {
			continue
		}
		lines := strings.Split(string(src), "\n")
		for i := 0; i < len(lines); {
			if !strings.HasPrefix(strings.TrimSpace(lines[i]), "|") {
				i++
				continue
			}
			start := i
			seen := map[string]bool{}
			var mine []int
			// a feature-fence comment line inside a table continues it
			row := func(k int) bool {
				t := strings.TrimSpace(lines[k])
				return strings.HasPrefix(t, "|") || strings.HasPrefix(t, "<!--") && strings.HasSuffix(t, "-->") && k+1 < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[k+1]), "|")
			}
			for ; i < len(lines) && row(i); i++ {
				for _, m := range catalogueRowRe.FindAllStringSubmatch(lines[i], -1) {
					if prefixes[m[1]] {
						seen[m[1]] = true
						if m[1] == c.Prefix {
							mine = append(mine, i+1)
						}
					}
				}
			}
			if len(seen) < familyTableMin {
				continue
			}
			heading := mdHeading(src, start+1)
			note := fmt.Sprintf("rows for %d/%d categories", len(seen), len(prefixes))
			if len(mine) == 0 {
				note += ", NO `" + c.Prefix + "*` row"
				if len(seen)*100 < fullListPct*len(prefixes) {
					note += " (a partial table: add one only if it belongs)"
					mine = []int{start + 1}
					sec.Sites = append(sec.Sites, Site{Path: p, Symbols: []Symbol{{Name: heading, Lines: mine, Note: note}}})
					continue
				}
				warns = append(warns, fmt.Sprintf("%s (%s, L%d) has rows for %d categories but none for `%s*`", p, heading, start+1, len(seen), c.Prefix))
				mine = []int{start + 1}
			}
			sec.Sites = append(sec.Sites, Site{Path: p, Symbols: []Symbol{{Name: heading, Lines: mine, Note: note}}})
		}
	}
	sort.SliceStable(sec.Sites, func(i, j int) bool { return sec.Sites[i].Path < sec.Sites[j].Path })
	return sec, warns, len(sec.Sites) > 0
}
