package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"path"
	"sort"
	"strings"
)

// Example fixture cohorts: the CSV + schema pairs under fixtureDir that
// every example reads through a built .pulse. A new or changed cohort is
// registered in lists that never name a single operator or slot — the
// integration build helper, fixtures/build.sh, the README cohort table, a
// fuzz test's cohort subset, the generator's case list — so each is found
// by shape:
//
//   - the cohort set is every fixtureDir/<stem>.csv with a matching
//     fixtureDir/schemas/<stem>.json;
//   - a mention is a whole-token cohort stem that is cohort-shaped:
//     quoted or backticked, or followed by .pulse / .csv / .json, or one
//     of at least bareListMin distinct bare stems on one line (a shell
//     word list);
//   - a registration list is a Go declaration (comments excluded) or a
//     run of consecutive non-Go lines holding mentions of at least
//     listMin distinct cohorts.
const (
	fixtureDir  = "internal/examples/fixtures"
	listMin     = 3
	bareListMin = 5
	// fullListPct: a list naming at least this share of the cohorts is a
	// complete registration; one that skips an existing cohort warns.
	fullListPct = 75
)

// fixtureCohorts returns the fixture cohort stems, sorted.
func (r *repo) fixtureCohorts() []string {
	have := map[string]bool{}
	for _, p := range r.files {
		if path.Dir(p) == fixtureDir+"/schemas" && path.Ext(p) == ".json" {
			have[strings.TrimSuffix(path.Base(p), ".json")] = true
		}
	}
	var out []string
	for _, p := range r.files {
		if path.Dir(p) == fixtureDir && path.Ext(p) == ".csv" {
			if stem := strings.TrimSuffix(path.Base(p), ".csv"); have[stem] {
				out = append(out, stem)
			}
		}
	}
	sort.Strings(out)
	return out
}

// isFixtureData reports whether p is a cohort's own CSV or schema.
func isFixtureData(p string) bool {
	d := path.Dir(p)
	return d == fixtureDir && path.Ext(p) == ".csv" || d == fixtureDir+"/schemas"
}

// mention is one cohort-shaped stem occurrence.
type mention struct {
	stem string
	off  int // byte offset in the file
	line int // 1-based
}

// cohortMentions returns the cohort-shaped mentions in src.
func cohortMentions(src []byte, cohorts []string) []mention {
	var out []mention
	off := 0
	for i, line := range bytes.Split(src, []byte("\n")) {
		type tok struct {
			stem   string
			at     int
			shaped bool
		}
		var toks []tok
		bare := map[string]bool{}
		for _, stem := range cohorts {
			for from := 0; ; {
				j := bytes.Index(line[from:], []byte(stem))
				if j < 0 {
					break
				}
				at, end := from+j, from+j+len(stem)
				from = end
				if at > 0 && isWord(line[at-1]) || end < len(line) && isWord(line[end]) {
					continue
				}
				var prev, next byte
				if at > 0 {
					prev = line[at-1]
				}
				if end < len(line) {
					next = line[end]
				}
				rest := line[end:]
				file := bytes.HasPrefix(rest, []byte(".pulse")) || bytes.HasPrefix(rest, []byte(".csv")) || bytes.HasPrefix(rest, []byte(".json"))
				quoted := prev != 0 && strings.IndexByte("\"'`", prev) >= 0 && (next == prev || file)
				if bytes.HasPrefix(bytes.TrimSpace(line), []byte("|")) {
					// A Markdown table row lists a cohort only in its
					// first cell; elsewhere in a row it is a use.
					first := bytes.IndexByte(bytes.TrimPrefix(bytes.TrimSpace(line), []byte("|")), '|')
					lead := len(line) - len(bytes.TrimLeft(line, " \t")) + 1
					if first >= 0 && at > lead+first {
						continue
					}
				}
				shaped := quoted || file
				toks = append(toks, tok{stem, at, shaped})
				bare[stem] = true
			}
		}
		wordList := len(bare) >= bareListMin
		for _, t := range toks {
			if t.shaped || wordList {
				out = append(out, mention{stem: t.stem, off: off + t.at, line: i + 1})
			}
		}
		off += len(line) + 1
	}
	return out
}

// cohortList is one registration list.
type cohortList struct {
	Path   string
	Symbol string
	Lines  []int
	Names  map[string]bool
}

// cohortLists finds every registration list in the tree.
func (r *repo) cohortLists(cohorts []string, all bool) []cohortList {
	var out []cohortList
	for _, p := range r.files {
		if isFixtureData(p) || strings.HasPrefix(p, "docs/book/") || (!all && isHistory(p)) {
			continue
		}
		src := r.read(p)
		if src == nil {
			continue
		}
		ms := cohortMentions(src, cohorts)
		if len(ms) < listMin {
			continue
		}
		var groups []cohortList
		if strings.HasSuffix(p, ".go") {
			g := r.parse(p)
			if g == nil {
				continue
			}
			bySym := map[string]int{}
			for _, m := range ms {
				if g.inComment(m.off) {
					continue
				}
				sym := g.symbolAt(m.off)
				k, ok := bySym[sym]
				if !ok {
					k = len(groups)
					bySym[sym] = k
					groups = append(groups, cohortList{Path: p, Symbol: sym, Names: map[string]bool{}})
				}
				groups[k].Lines = append(groups[k].Lines, m.line)
				groups[k].Names[m.stem] = true
			}
		} else {
			for _, m := range ms {
				n := len(groups)
				if n == 0 || last(groups[n-1].Lines) < m.line-1 {
					groups = append(groups, cohortList{Path: p, Names: map[string]bool{}})
					n++
				}
				groups[n-1].Lines = append(groups[n-1].Lines, m.line)
				groups[n-1].Names[m.stem] = true
			}
			for k := range groups {
				groups[k].Symbol = listSymbol(p, src, groups[k].Lines[0])
			}
		}
		for _, gr := range groups {
			if len(gr.Names) >= listMin {
				gr.Lines = dedupeInts(gr.Lines)
				out = append(out, gr)
			}
		}
	}
	return out
}

// listSymbol names a non-Go list by its Markdown heading or, for any
// other file, the text of its first line.
func listSymbol(p string, src []byte, line int) string {
	if strings.HasSuffix(p, ".md") {
		if h := mdHeading(src, line); h != "" {
			return h
		}
	}
	text := strings.TrimSpace(strings.Split(string(src), "\n")[line-1])
	if len(text) > 40 {
		text = text[:40] + "…"
	}
	return text
}

func last(xs []int) int { return xs[len(xs)-1] }

// testsNaming returns, per package directory, the Test / Fuzz functions
// of dir's test files whose body uses the identifier ident.
func (r *repo) testsNaming(dir, ident string) []string {
	var out []string
	for _, p := range r.files {
		if path.Dir(p) != dir || !strings.HasSuffix(p, "_test.go") {
			continue
		}
		g := r.parse(p)
		if g == nil {
			continue
		}
		for _, d := range g.file.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Body == nil || !(strings.HasPrefix(fd.Name.Name, "Test") || strings.HasPrefix(fd.Name.Name, "Fuzz")) {
				continue
			}
			uses := false
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				if id, ok := n.(*ast.Ident); ok && id.Name == ident {
					uses = true
				}
				return !uses
			})
			if uses {
				out = append(out, fd.Name.Name)
			}
		}
	}
	return out
}

// symbolIdent is the identifier a Go symbol ("func name", "var name")
// declares, or "".
func symbolIdent(sym string) string {
	f := strings.Fields(sym)
	if len(f) == 2 && (f[0] == "func" || f[0] == "var" || f[0] == "const") {
		return f[1]
	}
	return ""
}

// fixtureReport is the `fixture <name>` checklist: the cohort's files,
// every registration list (and whether it names the cohort), the files
// that read it, and the gates every fixture change reaches.
func fixtureReport(r *repo, name string, opts options) (*Report, error) {
	if name == "" || strings.ContainsAny(name, "/. ") {
		return nil, fmt.Errorf("fixture: %q is not a cohort stem (use the CSV's base name without .csv)", name)
	}
	cohorts := r.fixtureCohorts()
	existing := contains(cohorts, name)
	rep := &Report{Mode: "fixture", Target: name}
	state := "NEW cohort: add it to every complete list below"
	if existing {
		state = "existing cohort"
	}
	rep.Summary = append(rep.Summary,
		fmt.Sprintf("%s; %d fixture cohorts (%s/<stem>.csv + schemas/<stem>.json)", state, len(cohorts), fixtureDir))

	files := Section{Class: "fixture-files", Title: "Cohort files"}
	for _, p := range []string{fixtureDir + "/" + name + ".csv", fixtureDir + "/schemas/" + name + ".json"} {
		note := "create"
		if contains(r.files, p) {
			note = ""
		}
		files.Sites = append(files.Sites, Site{Path: p, Note: note})
	}
	rep.Sections = append(rep.Sections, files)

	all := cohorts
	if !existing {
		all = append(append([]string{}, cohorts...), name)
		sort.Strings(all)
	}
	lists := r.cohortLists(all, opts.All)
	reg := Section{Class: "fixture-lists", Title: "Registration lists",
		Hint: fmt.Sprintf("found by shape: a declaration or run of lines naming >= %d cohorts; complete = >= %d%% of them", listMin, fullListPct)}
	gateDirs := map[string][]string{}
	for _, l := range lists {
		n := 0
		for _, c := range cohorts {
			if l.Names[c] {
				n++
			}
		}
		complete := n*100 >= fullListPct*len(cohorts)
		cov := "subset"
		if complete {
			cov = "complete"
		}
		note := fmt.Sprintf("names %d/%d", n, len(cohorts))
		switch {
		case l.Names[name]:
			note += ", has " + name
		case complete:
			note += ", MISSING " + name
			rep.Warnings = append(rep.Warnings, fmt.Sprintf("%s (%s) lists %d/%d cohorts but not %s", l.Path, l.Symbol, n, len(cohorts), name))
		default:
			note += ", a subset: add " + name + " only if it belongs"
		}
		reg.Sites = append(reg.Sites, Site{Path: l.Path, Coverage: cov, Symbols: []Symbol{{Name: l.Symbol, Lines: l.Lines, Note: note}}})
		if id := symbolIdent(l.Symbol); id != "" && strings.HasSuffix(l.Path, ".go") {
			if ts := r.testsNaming(path.Dir(l.Path), id); len(ts) > 0 {
				gateDirs[path.Dir(l.Path)] = append(gateDirs[path.Dir(l.Path)], ts...)
			}
		}
	}
	rep.Sections = append(rep.Sections, reg)

	if existing {
		uses := Section{Class: "fixture-uses", Title: "Readers (files naming " + name + ".pulse / .csv / schema)"}
		inList := map[string]bool{}
		for _, l := range lists {
			inList[l.Path] = true
		}
		for _, p := range r.files {
			if isFixtureData(p) || inList[p] || strings.HasPrefix(p, "docs/book/") || (!opts.All && isHistory(p)) {
				continue
			}
			src := r.read(p)
			if src == nil {
				continue
			}
			lines := fileMentions(src, name)
			if len(lines) > 0 {
				uses.Sites = append(uses.Sites, Site{Path: p, Symbols: []Symbol{{Lines: dedupeInts(lines)}}})
			}
		}
		rep.Sections = append(rep.Sections, uses)
	}

	// Gates: the fixture package's own tests, the tests that read each Go
	// list, and every-example runners (each example reads a cohort).
	for _, p := range r.files {
		if path.Dir(p) == fixtureDir && strings.HasSuffix(p, "_test.go") {
			if g := r.parse(p); g != nil {
				for _, d := range g.file.Decls {
					if fd, ok := d.(*ast.FuncDecl); ok && fd.Recv == nil && strings.HasPrefix(fd.Name.Name, "Test") {
						gateDirs[fixtureDir] = append(gateDirs[fixtureDir], fd.Name.Name)
					}
				}
			}
		}
	}
	rep.Gates = append(rep.Gates, gatesFromDirs("fixture cohort: its generator tests and the tests reading each registration list", gateDirs)...)
	rep.Gates = append(rep.Gates, r.runnerGates()...)

	// Regenerate: a main package under fixtureDir/cmd writes the
	// generated CSVs.
	var cmds []string
	for _, p := range r.files {
		if strings.HasPrefix(p, fixtureDir+"/cmd/") && strings.HasSuffix(p, ".go") && !strings.HasSuffix(p, "_test.go") {
			if g := r.parse(p); g != nil && g.file.Name.Name == "main" {
				cmds = append(cmds, "go run ./"+path.Dir(p))
			}
		}
	}
	for _, c := range dedupe(cmds) {
		rep.Regenerate = append(rep.Regenerate, Regen{Command: c, Note: "generated CSVs (a hand-written CSV is edited in place)", Paths: []string{fixtureDir + "/*.csv"}})
	}
	rep.Manual = append(rep.Manual, "docs/src/internals/regenerating-goldens.md")
	return rep, nil
}

// isHistory reports a roadmap / history file the default view folds away.
func isHistory(p string) bool {
	return strings.HasPrefix(p, "docs/roadmap/") || strings.HasSuffix(p, "/history.md")
}

// fileMentions returns the lines naming the cohort's files: <stem>.pulse,
// <stem>.csv or schemas/<stem>.json, the stem a whole token.
func fileMentions(src []byte, stem string) []int {
	var out []int
	for i, line := range bytes.Split(src, []byte("\n")) {
		for from := 0; ; {
			j := bytes.Index(line[from:], []byte(stem))
			if j < 0 {
				break
			}
			at, end := from+j, from+j+len(stem)
			from = end
			if at > 0 && isWord(line[at-1]) {
				continue
			}
			rest := line[end:]
			if bytes.HasPrefix(rest, []byte(".pulse")) || bytes.HasPrefix(rest, []byte(".csv")) ||
				bytes.HasPrefix(rest, []byte(".json")) && bytes.HasSuffix(line[:at], []byte("schemas/")) {
				if len(out) == 0 || out[len(out)-1] != i+1 {
					out = append(out, i+1)
				}
				break
			}
		}
	}
	return out
}
