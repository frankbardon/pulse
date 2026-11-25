package main

import (
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

// Compose sites: places a change to a Compose-level slot (a
// types.ComposedRequest field: sweep, base, fits, banner, optimize,
// post_fit, …) reaches beyond its go/types references and json-name
// literals. Each rule reads the code for a shape, never a site list:
//
//   - strict-input family tables: declarations listing >=
//     composeFamilyMin distinct "composed.<slot>[.<key>]" literals (a
//     new slot, or a new object sub-block, adds a row);
//   - capability lists: files naming >= composeCapListMin distinct
//     capability:compose_* features (example profiles, the feature
//     table, gated-slot tables, MCP binds, skills);
//   - limits-parity site lists: test declarations naming >= 2 distinct
//     "compose" / "compose (<arm>)" runtime-site literals (a slot that
//     adds compose slots or work adds a parity case);
//   - predict ≡ runtime shared derivations: internal/descriptor
//     functions the runtime (internal/service) calls with a
//     ComposedRequest or one of its fields (ResultCatalog, …);
//   - skills and examples carrying the slot's json key ("sweep":).

const (
	composeFamilyMin  = 5
	composeCapListMin = 5
)

var composeSiteRe = regexp.MustCompile(`^compose(?: \(.+\))?$`)

// composeSection lists the compose sites of slot key (isNew: the slot is
// not declared yet). calls are the runtime's shared descriptor calls
// keyed on ComposedRequest (nil outside a type-checked tree).
func (r *repo) composeSection(key string, isNew bool, calls []sharedCall) (Section, bool) {
	sec := Section{
		Class: "compose",
		Title: fmt.Sprintf("Compose sites — a change to ComposedRequest.%s reaches these without a go/types reference to it", key),
		Hint: fmt.Sprintf("a new slot or object sub-block adds a strict-input family row (\"composed.%s[.<key>]\"); "+
			"a capability:compose_* gate joins every capability list (>= %d named); "+
			"a slot that adds compose slots or work adds a limits-parity case; "+
			"predict and runtime share the derivations below — change both sides together",
			key, composeCapListMin),
	}
	sites := map[string]*Site{}
	add := func(p, sym string, lines []int, note string) {
		s := sites[p]
		if s == nil {
			s = &Site{Path: p}
			sites[p] = s
		}
		for i := range s.Symbols {
			if s.Symbols[i].Name == sym {
				s.Symbols[i].Lines = dedupeInts(sortedInts(append(s.Symbols[i].Lines, lines...)))
				if !strings.Contains(s.Symbols[i].Note, note) {
					s.Symbols[i].Note += "; " + note
				}
				return
			}
		}
		s.Symbols = append(s.Symbols, Symbol{Name: sym, Lines: dedupeInts(sortedInts(lines)), Note: note})
	}
	line := func(pos token.Pos) int { return r.fset.Position(pos).Line }
	family := "composed." + key

	// strict-input family tables and limits-parity site lists
	for _, p := range r.files {
		if !strings.HasSuffix(p, ".go") || classify(p) == "history" {
			continue
		}
		g := r.parse(p)
		if g == nil {
			continue
		}
		test := strings.HasSuffix(p, "_test.go")
		for _, d := range g.file.Decls {
			fams := map[string]bool{}
			arms := map[string]bool{}
			var famLines, ownLines, armLines []int
			ast.Inspect(d, func(n ast.Node) bool {
				bl, ok := n.(*ast.BasicLit)
				if !ok || bl.Kind != token.STRING {
					return true
				}
				v, err := strconv.Unquote(bl.Value)
				if err != nil {
					return true
				}
				if strings.HasPrefix(v, "composed.") && !strings.ContainsAny(v, " {\"") {
					fams[v] = true
					famLines = append(famLines, line(bl.Pos()))
					if v == family || strings.HasPrefix(v, family+".") {
						ownLines = append(ownLines, line(bl.Pos()))
					}
				}
				if test && composeSiteRe.MatchString(v) {
					arms[v] = true
					armLines = append(armLines, line(bl.Pos()))
				}
				return true
			})
			sym := g.symbolAt(int(d.Pos()) - g.base)
			if len(fams) >= composeFamilyMin {
				if len(ownLines) > 0 {
					add(p, sym, ownLines, fmt.Sprintf("strict-input family table: %d composed.* families, names %q", len(fams), family))
				} else {
					add(p, sym, famLines[:1], fmt.Sprintf("strict-input family table: %d composed.* families — add a %q row", len(fams), family))
				}
			}
			if len(arms) >= 2 {
				add(p, sym, armLines[:1], fmt.Sprintf("limits-parity site list: %d compose runtime sites", len(arms)))
			}
		}
	}

	// capability lists
	capRe := regexp.MustCompile(`capability:compose_[a-z0-9_]+`)
	own := "capability:compose_" + key
	for _, p := range r.files {
		switch classify(p) {
		case "history", "golden", "docs-generated", "test":
			continue
		}
		src := r.read(p)
		if src == nil {
			continue
		}
		locs := capRe.FindAllIndex(src, -1)
		distinct := map[string]bool{}
		for _, l := range locs {
			distinct[string(src[l[0]:l[1]])] = true
		}
		if len(distinct) < composeCapListMin {
			continue
		}
		note := fmt.Sprintf("capability list: %d compose features", len(distinct))
		if distinct[own] {
			note += ", names " + own
		}
		var hs []hit
		for _, l := range locs {
			hs = append(hs, hit{Path: p, Off: l[0], Line: lineOf(src, l[0])})
		}
		for _, s := range r.resolve(p, hs) {
			if s.CommentOnly {
				continue
			}
			add(p, s.Name, s.Lines, note)
		}
	}

	// predict ≡ runtime shared derivations
	callers := map[string][]string{}
	for _, c := range calls {
		g := r.parse(c.Path)
		if g == nil {
			continue
		}
		arg := "the ComposedRequest"
		if c.Arg != "" {
			arg = "ComposedRequest." + c.Arg
		}
		desc := fmt.Sprintf("%s · %s (%s)", c.Path, g.symbolAt(c.Off), arg)
		if !contains(callers[c.Callee], desc) {
			callers[c.Callee] = append(callers[c.Callee], desc)
		}
	}
	for _, p := range r.files {
		if path.Dir(p) != "internal/descriptor" || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			continue
		}
		g := r.parse(p)
		if g == nil {
			continue
		}
		for _, d := range g.file.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Recv != nil || len(callers[fd.Name.Name]) == 0 {
				continue
			}
			cs := callers[fd.Name.Name]
			sort.Strings(cs)
			add(p, funcName(fd), []int{line(fd.Pos())}, "predict ≡ runtime shared derivation, runtime calls it from "+abbreviate(cs, 2))
		}
	}

	// skills and examples carrying the json key
	if !isNew {
		quoted := []byte(strconv.Quote(key) + ":")
		for _, p := range r.files {
			skill := strings.HasPrefix(p, "internal/skills/") && strings.HasSuffix(p, ".md")
			example := (strings.HasPrefix(p, "internal/examples/") || strings.HasPrefix(p, "examples/")) && strings.HasSuffix(p, ".json") && !strings.Contains(p, "/testdata/")
			if !skill && !example {
				continue
			}
			src := r.read(p)
			var hs []hit
			for off := 0; ; {
				i := bytes.Index(src[off:], quoted)
				if i < 0 {
					break
				}
				hs = append(hs, hit{Path: p, Off: off + i, Line: lineOf(src, off+i)})
				off += i + len(quoted)
			}
			for _, s := range r.resolve(p, hs) {
				add(p, s.Name, s.Lines, fmt.Sprintf("carries the %s key", strconv.Quote(key)))
			}
		}
	}

	var paths []string
	for p := range sites {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		sec.Sites = append(sec.Sites, *sites[p])
	}
	return sec, len(sec.Sites) > 0
}

// sectionTestGates resolves a section's test-file sites to the Test
// functions of the same file that read them (a helper table such as
// strictAcceptanceRows is not itself a test).
func (r *repo) sectionTestGates(why string, sec Section) []Gate {
	byDir := map[string][]string{}
	for _, s := range sec.Sites {
		if !strings.HasSuffix(s.Path, "_test.go") {
			continue
		}
		g := r.parse(s.Path)
		if g == nil {
			continue
		}
		for _, sym := range s.Symbols {
			if t := testName(sym.Name); t != "" {
				if !contains(byDir[path.Dir(s.Path)], t) {
					byDir[path.Dir(s.Path)] = append(byDir[path.Dir(s.Path)], t)
				}
				continue
			}
			ident := declIdent(sym.Name)
			if ident == "" {
				continue
			}
			for _, d := range g.file.Decls {
				fd, ok := d.(*ast.FuncDecl)
				if !ok || fd.Recv != nil || testName(funcName(fd)) == "" {
					continue
				}
				uses := false
				ast.Inspect(fd, func(n ast.Node) bool {
					if id, ok := n.(*ast.Ident); ok && id.Name == ident {
						uses = true
					}
					return !uses
				})
				if t := testName(funcName(fd)); uses && !contains(byDir[path.Dir(s.Path)], t) {
					byDir[path.Dir(s.Path)] = append(byDir[path.Dir(s.Path)], t)
				}
			}
		}
	}
	return gatesFromDirs(why, byDir)
}

// declIdent is the identifier a symbol name declares ("func f" -> f,
// "var rows" -> rows); "" for methods and anonymous symbols.
func declIdent(sym string) string {
	for _, pre := range []string{"func ", "var ", "const ", "type "} {
		if rest, ok := strings.CutPrefix(sym, pre); ok && !strings.HasPrefix(rest, "(") {
			if i := strings.IndexAny(rest, " ,("); i >= 0 {
				rest = rest[:i]
			}
			return rest
		}
	}
	return ""
}
