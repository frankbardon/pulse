package main

import (
	"fmt"
	"go/ast"
	"go/token"
	"path"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// Field types: the encoding.FieldType constants, each spelled by its
// constant (FieldTypeDecimal128), its wire name (decimal128; an ordinary
// word such as `date` or `u8` only when quoted or backticked) and its
// atomic skill (type-decimal128). Read by AST from fieldTypeFile: the
// typed constant block and the FieldType.String switch that names them.
const fieldTypeFile = "encoding/field_type.go"

var fieldTypeFamily = family{
	Mode:   "field-type",
	Noun:   "field type",
	Source: fieldTypeFile + " FieldType consts + String()",
	Gates: []string{"TestSkillsCoverAllFieldTypes", "TestOperatorHasAtomicSkill", "TestNoTypeOnlyWidthInStridePaths",
		"TestPublicAPIGolden", "TestPayloadSchemaGolden"},
	Manual: []string{
		".claude/reference/byte-layout.md",
		".claude/reference/update-demand/format-and-index.md",
		".claude/reference/update-demand/io-import.md",
		refSkills,
		"docs/src/internals/adding-field-type.md",
	},
}

// wordSpelled reports a wire name distinctive enough to match as a bare
// token: it carries an underscore, or a digit and at least six letters
// (decimal128). u8 / f64 / date / datetime are matched only quoted.
func wordSpelled(name string) bool {
	if strings.Contains(name, "_") {
		return true
	}
	return len(name) >= 6 && strings.IndexFunc(name, unicode.IsDigit) >= 0
}

func fieldTypeMember(wire, konst string) member {
	m := member{Name: wire, Stems: []string{"type-" + kebab(wire)}}
	if konst != "" {
		m.Words = append(m.Words, konst)
	}
	if wordSpelled(wire) {
		m.Words = append(m.Words, wire)
	} else {
		m.Ticked = append(m.Ticked, wire)
	}
	return m
}

// loadFieldTypes reads the field-type family.
func (r *repo) loadFieldTypes() (*family, error) {
	g := r.parse(fieldTypeFile)
	if g == nil {
		return nil, fmt.Errorf("field-type: %s not found", fieldTypeFile)
	}
	var consts []string
	wire := map[string]string{}
	for _, d := range g.file.Decls {
		switch d := d.(type) {
		case *ast.GenDecl:
			if d.Tok != token.CONST {
				continue
			}
			typed := false
			for _, s := range d.Specs {
				vs := s.(*ast.ValueSpec)
				if id, ok := vs.Type.(*ast.Ident); ok && id.Name == "FieldType" {
					typed = true
				}
				for _, n := range vs.Names {
					if typed && n.IsExported() {
						consts = append(consts, n.Name)
					}
				}
			}
		case *ast.FuncDecl:
			if d.Name.Name != "String" || d.Recv == nil || d.Body == nil {
				continue
			}
			ast.Inspect(d.Body, func(n ast.Node) bool {
				cc, ok := n.(*ast.CaseClause)
				if !ok || len(cc.List) != 1 || len(cc.Body) != 1 {
					return true
				}
				id, ok := cc.List[0].(*ast.Ident)
				ret, ok2 := cc.Body[0].(*ast.ReturnStmt)
				if !ok || !ok2 || len(ret.Results) != 1 {
					return true
				}
				if bl, ok := ret.Results[0].(*ast.BasicLit); ok && bl.Kind == token.STRING {
					wire[id.Name], _ = strconv.Unquote(bl.Value)
				}
				return true
			})
		}
	}
	f := fieldTypeFamily
	for _, k := range consts {
		if w, ok := wire[k]; ok {
			f.Members = append(f.Members, fieldTypeMember(w, k))
		}
	}
	f.newMember = func(name string) member {
		name = strings.ToLower(name)
		return fieldTypeMember(name, "")
	}
	return &f, nil
}

// fieldTypeReport is the `field-type <name>` checklist.
func fieldTypeReport(r *repo, name string, opts options) (*Report, error) {
	f, err := r.loadFieldTypes()
	if err != nil {
		return nil, err
	}
	return r.familyReport(f, name, opts, func(c *famCtx, rep *Report) {
		if sec, ok := r.fieldTypeReach(c); ok {
			rep.Sections = append(rep.Sections, sec)
		}
		if sec, ok := r.numericFormatters(c); ok {
			rep.Sections = append(rep.Sections, sec)
		}
	})
}

// typeWord is the leading letters of a wire name (decimal128 ->
// decimal) when long enough to name helpers (>= 6 letters: decimal,
// datetime, categorical, packed); "" otherwise.
func typeWord(wire string) string {
	i := strings.IndexFunc(wire, func(r rune) bool { return !unicode.IsLetter(r) })
	if i < 0 {
		i = len(wire)
	}
	if i < 6 {
		return ""
	}
	return wire[:i]
}

// fieldTypeReach lists what a field-type change reaches without naming
// the type:
//
//   - selective predicates — bool funcs naming the type's constant and
//     under half the family (facetIsNumeric) — and their callers, and
//     the callers of each caller that is a coded refusal (a func
//     returning *errors.CodedError: FacetFieldTypeRefusal, whose callers
//     are predict and the runtime);
//   - type-word helpers — non-test plain funcs (methods share bare names)
//     whose name carries the type's word (decimalCellFloat) — and their
//     generic callers, those whose own name does not carry it (toFloat64,
//     scalarFromCell, coerceFloat64, …), when they sit in two files or more.
func (r *repo) fieldTypeReach(c *famCtx) (Section, bool) {
	sec := Section{Class: "field-type-reach", Title: "Reached without the name — predicates, refusals and type-word helpers (check each branch)",
		Hint: "callers of a selective predicate naming the type, the callers of a coded refusal among them, and the callers of a shared helper named for the type"}
	var add sectionAdder
	n := len(c.f.Members)
	note := func(cs callSite, via string) {
		if !c.listed[cs.Path+"\x00"+cs.Sym] {
			add.add(cs.Path, cs.Sym, []int{cs.Line}, via)
		}
	}
	done := map[string]bool{} // path\x00func already followed
	// selective predicates naming the type
	for _, p := range sortedHitPaths(c.files) {
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			continue
		}
		g := r.parse(p)
		if g == nil {
			continue
		}
		for _, d := range g.file.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || !isPredicate(fd) {
				continue
			}
			k := c.membersIn(p, fd)
			if !k[c.idx] || len(k)*2 >= n {
				continue
			}
			done[p+"\x00"+funcName(fd)] = true
			cs, common := r.callersOf(fd, p)
			if common {
				add.add(p, funcName(fd), nil, fmt.Sprintf("predicate with over %d callers — too common to expand", commonNameCallers))
				continue
			}
			for _, s := range cs {
				note(s, fmt.Sprintf("via predicate %s (%s, names %d/%d types)", fd.Name.Name, p, len(k), n))
				caller := r.funcDecl(s.Path, s.Sym)
				if caller == nil || caller.Recv != nil || !returnsCodedErr(caller) || done[s.Path+"\x00"+s.Sym] {
					continue
				}
				done[s.Path+"\x00"+s.Sym] = true
				if cs2, common := r.callersOf(caller, s.Path); !common {
					for _, s2 := range cs2 {
						note(s2, "via refusal "+caller.Name.Name+" (predict and runtime must agree)")
					}
				}
			}
		}
	}
	// type-word helpers: callers that do not carry the word themselves
	// (the generic readers special-casing this type)
	if w := typeWord(c.target.Name); w != "" {
		for _, p := range r.files {
			if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				continue
			}
			g := r.parse(p)
			if g == nil {
				continue
			}
			for _, d := range g.file.Decls {
				fd, ok := d.(*ast.FuncDecl)
				if !ok || fd.Recv != nil || done[p+"\x00"+funcName(fd)] || !strings.Contains(strings.ToLower(fd.Name.Name), w) {
					continue
				}
				cs, common := r.callersOf(fd, p)
				if common {
					continue
				}
				var generic []callSite
				files := map[string]bool{}
				for _, s := range cs {
					if !strings.Contains(strings.ToLower(s.Sym), w) {
						generic = append(generic, s)
						files[s.Path] = true
					}
				}
				if len(files) < 2 {
					continue
				}
				add.add(p, funcName(fd), nil, "helper named for "+w)
				for _, s := range generic {
					note(s, "via helper "+fd.Name.Name)
				}
			}
		}
	}
	sec.Sites = add.list()
	return sec, len(sec.Sites) > 0
}

// callersOf returns the non-test calls of a declaration: anywhere for an
// exported plain func, in its own package for an unexported name or a
// method (a bare name is shared by unrelated types). common reports a
// name past commonNameCallers.
func (r *repo) callersOf(fd *ast.FuncDecl, p string) ([]callSite, bool) {
	cs := r.callIndex()[fd.Name.Name]
	if fd.Recv != nil || !fd.Name.IsExported() {
		var own []callSite
		for _, s := range cs {
			if path.Dir(s.Path) == path.Dir(p) {
				own = append(own, s)
			}
		}
		cs = own
	}
	return cs, len(cs) > commonNameCallers
}

// membersIn returns the members a declaration names outside comments.
func (c *famCtx) membersIn(p string, d ast.Node) map[int]bool {
	g := c.r.parse(p)
	out := map[int]bool{}
	for _, h := range c.byPath[p] {
		pos := token.Pos(g.base + h.Off)
		if pos >= d.Pos() && pos < d.End() && !g.inComment(h.Off) {
			out[h.Op] = true
		}
	}
	return out
}

// returnsCodedErr reports a func whose last result is *errors.CodedError
// (any import name): the shape of a refusal rule.
func returnsCodedErr(fd *ast.FuncDecl) bool {
	if fd.Type.Results == nil || len(fd.Type.Results.List) == 0 {
		return false
	}
	st, ok := fd.Type.Results.List[len(fd.Type.Results.List)-1].Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	se, ok := st.X.(*ast.SelectorExpr)
	return ok && se.Sel.Name == "CodedError"
}

// numericFormatters lists the func(float64) string declarations in the
// files that switch over field types (a family table) or branch on a
// FieldType predicate method: a type read as a float64 (decimal
// through decimalCellFloat) renders its group keys and labels through
// them (formatRoundedNumeric), so a new numeric type's key or label rule
// lands there.
func (r *repo) numericFormatters(c *famCtx) (Section, bool) {
	sec := Section{Class: "field-type-formatters", Title: "Numeric key / label formatters — func(float64) string beside a field-type dispatch (a type read as float64 is keyed and labelled here)"}
	dispatch := map[string]bool{}
	for _, t := range c.tables {
		if strings.HasSuffix(t.Path, ".go") && !strings.HasSuffix(t.Path, "_test.go") {
			dispatch[t.Path] = true
		}
	}
	// ... or a file calling a FieldType predicate method (IsCategorical,
	// IsNumeric, …): it branches on the type without naming one
	for _, m := range r.fieldTypePredicates() {
		for _, cs := range r.callIndex()[m] {
			dispatch[cs.Path] = true
		}
	}
	var paths []string
	for p := range dispatch {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		g := r.parse(p)
		if g == nil {
			continue
		}
		var syms []Symbol
		for _, d := range g.file.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || !floatToString(fd) {
				continue
			}
			syms = append(syms, Symbol{Name: funcName(fd), Lines: []int{r.fset.Position(fd.Pos()).Line}})
		}
		if len(syms) > 0 && path.Ext(p) == ".go" {
			sec.Sites = append(sec.Sites, Site{Path: p, Symbols: syms})
		}
	}
	return sec, len(sec.Sites) > 0
}

// fieldTypePredicates names the bool methods declared on FieldType.
func (r *repo) fieldTypePredicates() []string {
	var out []string
	if g := r.parse(fieldTypeFile); g != nil {
		for _, d := range g.file.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok && fd.Recv != nil && isPredicate(fd) && strings.HasPrefix(funcName(fd), "func (FieldType) ") {
				out = append(out, fd.Name.Name)
			}
		}
	}
	return out
}

// floatToString reports a func(float64) string.
func floatToString(fd *ast.FuncDecl) bool {
	ft := fd.Type
	if ft.Params == nil || len(ft.Params.List) != 1 || len(ft.Params.List[0].Names) > 1 ||
		ft.Results == nil || len(ft.Results.List) != 1 || len(ft.Results.List[0].Names) > 1 {
		return false
	}
	in, ok1 := ft.Params.List[0].Type.(*ast.Ident)
	out, ok2 := ft.Results.List[0].Type.(*ast.Ident)
	return ok1 && ok2 && in.Name == "float64" && out.Name == "string"
}
