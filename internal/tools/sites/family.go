package main

import (
	"bytes"
	"fmt"
	"path"
	"sort"
	"strings"
)

// Families: closed sets of registered names that are not operators — the
// field types (encoding.FieldType), the error codes (errors.Code), the MCP
// tools (toolmeta.Tool*), the I/O formats (pio.Format) and the iocore
// reader / writer hooks. A member's sites are found like an operator's —
// whole-token occurrences of every spelling, each resolved to its
// enclosing declaration or heading — and the registration lists the
// family shares are found by shape:
//
//   - a family table is a Go declaration (comments excluded) or a run of
//     consecutive non-Go lines naming at least familyTableMin distinct
//     members (a Markdown table row counts only its first cell);
//   - a table naming at least fullListPct of the members is complete: an
//     existing member it skips is a warning, and a NEW member joins every
//     one; a smaller one is a subset (a switch arm, a refusal list), which
//     a new member joins only if it belongs.
//
// Each mode adds the sites it reaches without the name (field-type
// predicates and helpers, code-domain tables, a hook's implementers).

// familyTableMin is the distinct members a declaration or line run must
// name to be a family table.
const familyTableMin = 3

// member is one registered name of a family.
type member struct {
	Name   string   // wire / registry name (decimal128, PULSE_SLOT_FAILED, pulse_inspect)
	Words  []string // identifier tokens spelling it (Go constants, distinctive wire names)
	Ticked []string // ordinary words matched only "quoted" or `ticked` (date, csv)
	Stems  []string // atomic-skill stems (type-decimal128, tool-inspect)
}

// family is one registry of members plus its hand-kept parts: the
// structural gates every change of it reruns and its reading list (both
// pinned by TestFamilyGatesAndManualExist).
type family struct {
	Mode    string
	Noun    string // "field type"
	Source  string // where the members are read from (summary)
	Members []member
	Gates   []string
	Manual  []string
	// NoNewSubsets: a NEW member's report lists only the complete tables
	// (a subset — any declaration naming three error codes — is too
	// common to be a site; the mode lists its own narrower tables).
	NoNewSubsets bool
	// newMember derives the spellings of a name the registry does not
	// hold yet (a NEW member: its partial sites, if any, still show).
	newMember func(name string) member
}

// lookup returns the member a name spells (its wire name or a Go
// constant), or -1.
func (f *family) lookup(name string) int {
	for i, m := range f.Members {
		if m.Name == name || contains(m.Words, name) {
			return i
		}
	}
	for i, m := range f.Members {
		if strings.EqualFold(m.Name, name) {
			return i
		}
	}
	return -1
}

func (f *family) matcher(ms []member) *matcher {
	mt := &matcher{words: map[string][]int{}, stems: map[string][]int{}, quoted: map[string][]int{}, ticked: map[string][]int{}}
	for i, m := range ms {
		for _, w := range m.Words {
			mt.words[w] = append(mt.words[w], i)
		}
		for _, w := range m.Ticked {
			mt.ticked[w] = append(mt.ticked[w], i)
		}
		for _, s := range m.Stems {
			mt.stems[s] = append(mt.stems[s], i)
		}
	}
	return mt
}

// famTable is one family table.
type famTable struct {
	Path   string
	Symbol string
	Lines  []int
	Has    map[int]bool // member indexes it names
}

// mdFirstCell reports whether the offset sits outside a Markdown table
// row or inside its first cell.
func mdFirstCell(src []byte, off int) bool {
	start := bytes.LastIndexByte(src[:off], '\n') + 1
	line := src[start:off]
	trim := bytes.TrimLeft(line, " \t")
	if !bytes.HasPrefix(trim, []byte("|")) {
		return true
	}
	return bytes.Count(trim, []byte("|")) == 1
}

// familyTables groups every hit into tables (see the package comment).
func (r *repo) familyTables(byPath map[string][]hit, all bool) []famTable {
	var paths []string
	for p := range byPath {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var out []famTable
	for _, p := range paths {
		if cl := classify(p); cl == "golden" || cl == "docs-generated" || strings.HasPrefix(p, "docs/book/") || (!all && isHistory(p)) {
			continue // regenerated, never registered by hand
		}
		hs := byPath[p]
		sort.Slice(hs, func(i, j int) bool { return hs[i].Off < hs[j].Off })
		var groups []famTable
		if strings.HasSuffix(p, ".go") {
			g := r.parse(p)
			if g == nil {
				continue
			}
			bySym := map[string]int{}
			for _, h := range hs {
				if g.inComment(h.Off) {
					continue
				}
				sym := g.symbolAt(h.Off)
				k, ok := bySym[sym]
				if !ok {
					k = len(groups)
					bySym[sym] = k
					groups = append(groups, famTable{Path: p, Symbol: sym, Has: map[int]bool{}})
				}
				groups[k].Lines = append(groups[k].Lines, h.Line)
				groups[k].Has[h.Op] = true
			}
		} else {
			src := r.read(p)
			for _, h := range hs {
				if path.Ext(p) == ".md" && !mdFirstCell(src, h.Off) {
					continue
				}
				n := len(groups)
				if n == 0 || last(groups[n-1].Lines) < h.Line-1 {
					groups = append(groups, famTable{Path: p, Has: map[int]bool{}})
					n++
				}
				groups[n-1].Lines = append(groups[n-1].Lines, h.Line)
				groups[n-1].Has[h.Op] = true
			}
			for k := range groups {
				groups[k].Symbol = listSymbol(p, src, groups[k].Lines[0])
			}
		}
		for _, t := range groups {
			if len(t.Has) >= familyTableMin {
				t.Lines = dedupeInts(t.Lines)
				out = append(out, t)
			}
		}
	}
	return out
}

// famCtx is a family report under construction; the mode hooks read
// and extend it.
type famCtx struct {
	r          *repo
	f          *family
	opts       options
	idx        int // the target's index into ms
	isNew      bool
	ms         []member // the registry's members, plus the NEW target
	target     member
	byPath     map[string][]hit // every member's hits
	files      map[string][]hit // the target's hits
	tables     []famTable
	listed     map[string]bool // path\x00symbol already a site
	classes    map[string]bool
	testsByDir map[string][]string
	regen      []string
	gates      []Gate
}

// complete reports a table naming at least fullListPct of the registry.
func (c *famCtx) complete(t famTable) bool {
	n := 0
	for i := range t.Has {
		if i < len(c.f.Members) {
			n++
		}
	}
	return n*100 >= fullListPct*len(c.f.Members)
}

func (c *famCtx) named(t famTable) int {
	n := 0
	for i := range t.Has {
		if i < len(c.f.Members) {
			n++
		}
	}
	return n
}

// familyReport is the checklist of one member (existing or NEW) of a
// family; hook adds the mode's own sections before the gates are cut.
func (r *repo) familyReport(f *family, name string, opts options, hook func(*famCtx, *Report)) (*Report, error) {
	if len(f.Members) < familyTableMin {
		return nil, fmt.Errorf("%s: only %d members read from %s", f.Mode, len(f.Members), f.Source)
	}
	c := &famCtx{r: r, f: f, opts: opts, ms: append([]member{}, f.Members...),
		listed: map[string]bool{}, classes: map[string]bool{}, testsByDir: map[string][]string{}}
	c.idx = f.lookup(name)
	if c.idx < 0 {
		c.isNew = true
		c.idx = len(c.ms)
		c.ms = append(c.ms, f.newMember(name))
	}
	c.target = c.ms[c.idx]
	rep := &Report{Mode: f.Mode, Target: c.target.Name}
	spell := append(append(append([]string{}, c.target.Words...), quoteAll(c.target.Ticked)...), c.target.Stems...)
	state := "existing " + f.Noun
	if c.isNew {
		state = "NEW " + f.Noun + ": add it to every complete table below"
	}
	rep.Summary = []string{
		fmt.Sprintf("%s; %d registered (%s) · matched as %s", state, len(f.Members), f.Source, strings.Join(spell, ", ")),
		fmt.Sprintf("tables = declarations / line runs naming >= %d %ss; complete = >= %d%% of them", familyTableMin, f.Noun, fullListPct),
	}

	c.byPath = map[string][]hit{}
	c.files = map[string][]hit{}
	for _, h := range r.scan(f.matcher(c.ms), nil) {
		c.byPath[h.Path] = append(c.byPath[h.Path], h)
		if h.Op == c.idx {
			c.files[h.Path] = append(c.files[h.Path], h)
		}
	}

	// Named sites, by class (tests -> gates, goldens -> regenerate).
	byClass := map[string][]Site{}
	var history []string
	for _, p := range sortedHitPaths(c.files) {
		cl := classify(p)
		c.classes[cl] = true
		if cl == "history" {
			history = append(history, p)
			if !opts.All {
				continue
			}
		}
		if (cl == "golden" || cl == "docs-generated") && !contains(c.regen, p) {
			c.regen = append(c.regen, p)
		}
		site := Site{Path: p}
		for _, s := range r.resolve(p, c.files[p]) {
			c.listed[p+"\x00"+s.Name] = true
			sym := Symbol{Name: s.Name, Lines: s.Lines}
			if s.CommentOnly {
				sym.Note = "(comment only)"
			}
			if cl == "test" {
				if t := testName(s.Name); t != "" {
					c.testsByDir[path.Dir(p)] = append(c.testsByDir[path.Dir(p)], t)
				}
			}
			site.Symbols = append(site.Symbols, sym)
		}
		if (cl == "test" || cl == "golden" || cl == "docs-generated") && !opts.All {
			continue
		}
		byClass[cl] = append(byClass[cl], site)
	}
	rep.Sections = sectionsFrom(byClass)

	// Family tables.
	c.tables = r.familyTables(c.byPath, opts.All)
	sec := Section{Class: "family-tables", Title: "Registration tables (" + f.Noun + "s listed together) — found by shape",
		Hint: fmt.Sprintf("complete = names >= %d%% of the %d %ss; a NEW %s joins every complete one and checks each subset (a subset naming an existing one is listed above; --all lists every table)", fullListPct, len(f.Members), f.Noun, f.Noun)}
	tableGates := map[string][]string{}
	skipped := 0
	for _, t := range c.tables {
		k, full := c.named(t), c.complete(t)
		note := fmt.Sprintf("names %d/%d", k, len(f.Members))
		switch {
		case t.Has[c.idx] && !full && !c.isNew && !opts.All:
			continue // a subset naming it is a named site above
		case t.Has[c.idx]:
			note += ", has " + c.target.Name
		case full:
			note += ", MISSING " + c.target.Name
			if !strings.HasSuffix(t.Path, "_test.go") && !c.isNew {
				rep.Warnings = append(rep.Warnings, fmt.Sprintf("%s (%s) lists %d/%d %ss but not %s", t.Path, t.Symbol, k, len(f.Members), f.Noun, c.target.Name))
			}
		case (c.isNew && !f.NoNewSubsets) || opts.All:
			note += ", a subset: add " + c.target.Name + " only if it belongs"
		default:
			skipped++
			continue
		}
		cov := "subset"
		if full {
			cov = "complete"
		}
		sec.Sites = append(sec.Sites, Site{Path: t.Path, Coverage: cov, Symbols: []Symbol{{Name: t.Symbol, Lines: t.Lines, Note: note}}})
		if id := symbolIdent(t.Symbol); id != "" && strings.HasSuffix(t.Path, ".go") && !strings.HasSuffix(t.Path, "_test.go") {
			if ts := r.testsNaming(path.Dir(t.Path), id); len(ts) > 0 {
				tableGates[path.Dir(t.Path)] = append(tableGates[path.Dir(t.Path)], ts...)
			}
		}
		if testName(t.Symbol) != "" {
			tableGates[path.Dir(t.Path)] = append(tableGates[path.Dir(t.Path)], testName(t.Symbol))
		}
	}
	sortSites(sec.Sites)
	rep.Sections = append(rep.Sections, sec)
	if skipped > 0 {
		rep.Omitted = append(rep.Omitted, fmt.Sprintf("%d subset tables do not name %s — pass --all to list them", skipped, c.target.Name))
	}

	// Norm files: files most members land in (outside Go comments), e.g.
	// the alias block of every hook in io/facade.go, whose entries are
	// separate declarations and so no table.
	inTable := map[string]bool{}
	for _, t := range c.tables {
		if c.complete(t) {
			inTable[t.Path] = true
		}
	}
	norm := Section{Class: "family-norm", Title: "Files most " + f.Noun + "s land in (outside a single table)"}
	for _, p := range sortedHitPaths(c.byPath) {
		cl := classify(p)
		if inTable[p] || !classByID(cl).Edit || cl == "history" {
			continue
		}
		g := (*goFile)(nil)
		if strings.HasSuffix(p, ".go") {
			g = r.parse(p)
		}
		has := map[int]bool{}
		for _, h := range c.byPath[p] {
			if h.Op < len(f.Members) && (g == nil || !g.inComment(h.Off)) {
				has[h.Op] = true
			}
		}
		if len(has)*100 < fullListPct*len(f.Members) {
			continue
		}
		note := fmt.Sprintf("names %d/%d", len(has), len(f.Members))
		switch {
		case len(c.files[p]) > 0:
			if !c.isNew && !opts.All {
				continue
			}
			note += ", has " + c.target.Name
		default:
			note += ", MISSING " + c.target.Name
			if !c.isNew {
				rep.Warnings = append(rep.Warnings, fmt.Sprintf("%s names %d/%d %ss but not %s", p, len(has), len(f.Members), f.Noun, c.target.Name))
			}
		}
		norm.Sites = append(norm.Sites, Site{Path: p, Coverage: "norm", Note: note})
	}
	if len(norm.Sites) > 0 {
		rep.Sections = append(rep.Sections, norm)
	}

	if hook != nil {
		hook(c, rep)
	}

	rep.Gates = append(rep.Gates, gatesFromDirs("tests that name "+c.target.Name, c.testsByDir)...)
	rep.Gates = append(rep.Gates, gatesFromDirs("tests that read a registration table", tableGates)...)
	rep.Gates = append(rep.Gates, c.gates...)
	rep.Gates = append(rep.Gates, r.namedGates(f.Noun+" gates (every "+f.Noun+" change)", f.Gates...)...)
	rep.Gates = append(rep.Gates, r.staticGates(c.classes)...)
	r.wholeRepo(rep, c.classes, c.listed)
	rep.Regenerate = r.regens(c.regen)
	rep.Manual = append([]string{}, f.Manual...)
	if len(history) > 0 && !opts.All {
		rep.Omitted = append(rep.Omitted, fmt.Sprintf("%d history files (docs/roadmap, CHANGELOG) also name %s — pass --all to list them", len(history), c.target.Name))
	}
	return rep, nil
}

func quoteAll(xs []string) []string {
	var out []string
	for _, x := range xs {
		out = append(out, "`"+x+"`")
	}
	return out
}

func sortedHitPaths(m map[string][]hit) []string {
	var out []string
	for p := range m {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func sortSites(s []Site) {
	sort.SliceStable(s, func(i, j int) bool {
		if (s[i].Coverage == "complete") != (s[j].Coverage == "complete") {
			return s[i].Coverage == "complete"
		}
		return s[i].Path < s[j].Path
	})
}

// kebab turns a snake / lower name into the skill-stem form.
func kebab(s string) string { return strings.ReplaceAll(strings.ToLower(s), "_", "-") }

// familyModes are the family report modes by CLI name.
var familyModes = map[string]func(*repo, string, options) (*Report, error){
	"field-type": fieldTypeReport,
	"error-code": errorCodeReport,
	"mcp-tool":   mcpToolReport,
	"io":         ioReport,
}
