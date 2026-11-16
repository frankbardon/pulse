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

// Spec sites: places a change to a category's request spec struct
// (types.RegressionSpec — a new key, an object sub-block, a coded
// refusal, a closed vocabulary or a capability) reaches without naming
// any operator. Everything is derived: the spec is the types struct
// carrying the registry's element type that a types.Request field holds,
// and each rule below reads the code for a shape, never a site list.
//
//   - refusal pipelines: declarations calling >= specPipelineMin of the
//     spec's checkers (funcs taking the spec and returning
//     *errors.CodedError) — predict and runtime run the same checks;
//   - field-list derivers: funcs from the spec to a []string (the
//     fields it reads or keys rows by);
//   - code tables: var / const declarations listing >= specCodeMin error codes of
//     the spec's family (*_REGRESSION_*);
//   - vocabulary registrars: callers of a closed vocabulary reachable
//     from the spec's fields (types.AllCovarianceTypes);
//   - strict-input families: string literals "request.<slot>[.<key>]";
//   - capability lists: files naming >= 2 capability:<family>_* features.

const (
	specPipelineMin = 3
	specCodeMin     = 3
)

// specInfo is a category's derived request spec.
type specInfo struct {
	Struct string // RegressionSpec
	Slot   string // json name of the types.Request field holding it (regressions)
	Family string // REGRESSION: the error-code segment, lower-cased the capability prefix
}

// typesIndex is the types package's struct fields (field -> type text)
// and its vocabulary functions (element type -> no-arg func returning
// a slice of it).
type typesIndex struct {
	structs map[string][]structField
	vocab   map[string]string
}

func (r *repo) typesIndex() typesIndex {
	ti := typesIndex{structs: map[string][]structField{}, vocab: map[string]string{}}
	for _, p := range r.files {
		if path.Dir(p) != "types" || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			continue
		}
		g := r.parse(p)
		if g == nil {
			continue
		}
		src := r.read(p)
		for _, d := range g.file.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				if d.Recv != nil || d.Type.Params.NumFields() != 0 || d.Type.Results.NumFields() != 1 {
					continue
				}
				at, ok := d.Type.Results.List[0].Type.(*ast.ArrayType)
				if !ok || at.Len != nil {
					continue
				}
				if id, ok := at.Elt.(*ast.Ident); ok && !predeclared(id.Name) {
					if cur, ok := ti.vocab[id.Name]; !ok || (!strings.HasPrefix(cur, "All") && strings.HasPrefix(d.Name.Name, "All")) {
						ti.vocab[id.Name] = d.Name.Name
					}
				}
			case *ast.GenDecl:
				if d.Tok != token.TYPE {
					continue
				}
				for _, s := range d.Specs {
					ts := s.(*ast.TypeSpec)
					st, ok := ts.Type.(*ast.StructType)
					if !ok {
						continue
					}
					var fs []structField
					for _, f := range st.Fields.List {
						tag := ""
						if f.Tag != nil {
							raw, _ := strconv.Unquote(f.Tag.Value)
							tag = jsonName(raw)
						}
						typ := string(src[int(f.Type.Pos())-g.base : int(f.Type.End())-g.base])
						for _, n := range f.Names {
							fs = append(fs, structField{Name: n.Name, JSON: tag, Type: typ, Path: p})
						}
					}
					ti.structs[ts.Name.Name] = fs
				}
			}
		}
	}
	return ti
}

var jsonTagRe = regexp.MustCompile(`json:"([^",]*)`)

// predeclared reports a Go builtin type name: a func returning
// []string is a list, not a closed vocabulary of a named type.
func predeclared(name string) bool {
	return name != "" && name[0] >= 'a' && name[0] <= 'z'
}

func jsonName(tag string) string {
	if m := jsonTagRe.FindStringSubmatch(tag); m != nil {
		return m[1]
	}
	return ""
}

// baseType strips pointer and slice markers: "[]*RegressionSpec" -> RegressionSpec.
func baseType(t string) string {
	return strings.TrimLeft(t, "[]*")
}

// registryElem is the element type of a category's registry function
// (AllRegressionTypes() []RegressionType -> RegressionType).
func (r *repo) registryElem(def categoryDef) string {
	for _, p := range r.files {
		if path.Dir(p) != def.Dir || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			continue
		}
		g := r.parse(p)
		if g == nil {
			continue
		}
		for _, d := range g.file.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Recv != nil || fd.Name.Name != def.AllFunc || fd.Type.Results.NumFields() != 1 {
				continue
			}
			if at, ok := fd.Type.Results.List[0].Type.(*ast.ArrayType); ok {
				if id, ok := at.Elt.(*ast.Ident); ok {
					return id.Name
				}
			}
		}
	}
	return ""
}

// findSpec derives a category's spec: the types struct with a field of
// the registry's element type that a types.Request field holds.
func (r *repo) findSpec(def categoryDef, ti typesIndex) (specInfo, bool) {
	if def.Dir != "types" {
		return specInfo{}, false
	}
	elem := r.registryElem(def)
	if elem == "" {
		return specInfo{}, false
	}
	for _, rf := range ti.structs["Request"] {
		s := baseType(rf.Type)
		if rf.JSON == "" || rf.JSON == "-" {
			continue
		}
		for _, f := range ti.structs[s] {
			if baseType(f.Type) == elem {
				return specInfo{Struct: s, Slot: rf.JSON, Family: upperSnake(strings.TrimSuffix(s, "Spec"))}, true
			}
		}
	}
	return specInfo{}, false
}

func upperSnake(s string) string {
	var b strings.Builder
	for i, c := range s {
		if i > 0 && c >= 'A' && c <= 'Z' {
			b.WriteByte('_')
		}
		b.WriteRune(c)
	}
	return strings.ToUpper(b.String())
}

// importName is the local name a file binds an import path to ("" when
// it does not import it).
func importName(f *ast.File, ip string) string {
	for _, im := range f.Imports {
		if p, _ := strconv.Unquote(im.Path.Value); p == ip {
			if im.Name != nil {
				return im.Name.Name
			}
			return path.Base(ip)
		}
	}
	return ""
}

// isSpecType reports a type expression naming the spec (pointer or
// value), qualified unless the file is in the types package.
func isSpecType(e ast.Expr, typesName, spec string) bool {
	if s, ok := e.(*ast.StarExpr); ok {
		e = s.X
	}
	switch e := e.(type) {
	case *ast.Ident:
		return typesName == "" && e.Name == spec
	case *ast.SelectorExpr:
		x, ok := e.X.(*ast.Ident)
		return ok && typesName != "" && x.Name == typesName && e.Sel.Name == spec
	}
	return false
}

func takesSpec(fd *ast.FuncDecl, typesName, spec string) bool {
	for _, f := range fd.Type.Params.List {
		if isSpecType(f.Type, typesName, spec) {
			return true
		}
	}
	return false
}

// returnsCoded reports a result list holding *errors.CodedError.
func returnsCoded(fd *ast.FuncDecl, errorsName string) bool {
	if fd.Type.Results == nil {
		return false
	}
	for _, f := range fd.Type.Results.List {
		st, ok := f.Type.(*ast.StarExpr)
		if !ok {
			continue
		}
		switch x := st.X.(type) {
		case *ast.Ident:
			if errorsName == "" && x.Name == "CodedError" {
				return true
			}
		case *ast.SelectorExpr:
			if id, ok := x.X.(*ast.Ident); ok && errorsName != "" && id.Name == errorsName && x.Sel.Name == "CodedError" {
				return true
			}
		}
	}
	return false
}

// returnsStrings reports exactly one []string result.
func returnsStrings(fd *ast.FuncDecl) bool {
	if fd.Type.Results.NumFields() != 1 {
		return false
	}
	at, ok := fd.Type.Results.List[0].Type.(*ast.ArrayType)
	if !ok || at.Len != nil {
		return false
	}
	id, ok := at.Elt.(*ast.Ident)
	return ok && id.Name == "string"
}

var codeIdentRe = regexp.MustCompile(`^(?:CLI|DATA|ENCODING|PROCESSING|PULSE|SERVICE)_[A-Z0-9_]+$`)

// specSection lists the spec sites of a category (ok false when the
// category has no derivable spec or no site).
func (r *repo) specSection(def categoryDef) (Section, bool) {
	ti := r.typesIndex()
	sp, ok := r.findSpec(def, ti)
	if !ok {
		return Section{}, false
	}
	sec := Section{
		Class: "spec",
		Title: fmt.Sprintf("Spec sites — a change to types.%s (a key, an object sub-block, a coded refusal, a vocabulary or a capability) reaches these without naming an operator", sp.Struct),
		Hint: fmt.Sprintf("a new spec rule's checker joins every refusal pipeline (decls calling >= %d checkers: funcs taking the spec, returning *errors.CodedError) — predict and runtime alike; "+
			"a key naming a cohort field joins every field-list deriver (spec -> []string); a new *_%s_* code joins every code table (>= %d codes); "+
			"a closed vocabulary registers beside the vocabulary registrars; an object sub-block adds a strict-input family row (\"request.%s.<key>\"); "+
			"a capability:%s_* feature joins every capability list (>= 2 named)",
			specPipelineMin, sp.Family, specCodeMin, sp.Slot, strings.ToLower(sp.Family)),
	}
	typesIP, errorsIP := modulePath+"/types", modulePath+"/errors"
	segment := "_" + sp.Family + "_"
	isCode := func(name string) bool {
		return codeIdentRe.MatchString(name) && (strings.Contains(name, segment) || strings.HasSuffix(name, "_"+sp.Family))
	}

	// vocabularies reachable from the spec's fields (the registry's own
	// element type excluded: the Enumerators section covers it)
	elem := r.registryElem(def)
	vocabField := map[string]string{} // vocab func -> Struct.Field
	seen := map[string]bool{}
	var walk func(s string)
	walk = func(s string) {
		if seen[s] {
			return
		}
		seen[s] = true
		for _, f := range ti.structs[s] {
			t := baseType(f.Type)
			if _, ok := ti.structs[t]; ok {
				walk(t)
				continue
			}
			if fn, ok := ti.vocab[t]; ok && t != elem {
				if _, dup := vocabField[fn]; !dup {
					vocabField[fn] = s + "." + f.Name
				}
			}
		}
	}
	walk(sp.Struct)

	type checker struct{ dir, name string }
	checkers := map[checker]bool{}
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

	var goFiles []string
	for _, p := range r.files {
		if strings.HasSuffix(p, ".go") && classify(p) != "history" {
			goFiles = append(goFiles, p)
		}
	}
	// pass 1: checkers, derivers, code tables, strict families
	for _, p := range goFiles {
		g := r.parse(p)
		if g == nil {
			continue
		}
		test := strings.HasSuffix(p, "_test.go")
		inTypes := path.Dir(p) == "types"
		tn := importName(g.file, typesIP)
		if inTypes {
			tn = ""
		} else if tn == "" {
			tn = "\x00" // the file cannot name the spec
		}
		en := importName(g.file, errorsIP)
		if path.Dir(p) == "errors" {
			en = ""
		} else if en == "" {
			en = "\x00"
		}
		for _, d := range g.file.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok && !test && tn != "\x00" && takesSpec(fd, tn, sp.Struct) {
				if fd.Recv == nil && en != "\x00" && returnsCoded(fd, en) {
					checkers[checker{path.Dir(p), fd.Name.Name}] = true
				}
				if returnsStrings(fd) {
					add(p, funcName(fd), []int{line(fd.Pos())}, "field-list deriver")
				}
			}
			// code tables: var / const declarations (a function naming a
			// few codes is a refusal, not a table)
			if gd, ok := d.(*ast.GenDecl); ok && !test && (gd.Tok == token.VAR || gd.Tok == token.CONST) {
				codes := map[string]bool{}
				var first token.Pos
				ast.Inspect(d, func(n ast.Node) bool {
					var name string
					switch n := n.(type) {
					case *ast.SelectorExpr:
						if x, ok := n.X.(*ast.Ident); ok && en != "" && x.Name == en {
							name = n.Sel.Name
						}
					case *ast.Ident:
						if en == "" {
							name = n.Name
						}
					}
					if name != "" && isCode(name) {
						if len(codes) == 0 {
							first = n.Pos()
						}
						codes[name] = true
					}
					return true
				})
				if len(codes) >= specCodeMin {
					add(p, g.symbolAt(int(first)-g.base), []int{line(first)}, fmt.Sprintf("code table: %d codes", len(codes)))
				}
			}
			// strict-input families
			ast.Inspect(d, func(n ast.Node) bool {
				bl, ok := n.(*ast.BasicLit)
				if !ok || bl.Kind != token.STRING {
					return true
				}
				v, err := strconv.Unquote(bl.Value)
				if err != nil {
					return true
				}
				fam := "request." + sp.Slot
				if v == fam || strings.HasPrefix(v, fam+".") {
					add(p, g.symbolAt(int(bl.Pos())-g.base), []int{line(bl.Pos())}, fmt.Sprintf("strict-input family %q", fam+".<key>"))
				}
				return true
			})
		}
	}
	// pass 2: refusal pipelines and vocabulary registrars
	for _, p := range goFiles {
		if strings.HasSuffix(p, "_test.go") {
			continue
		}
		g := r.parse(p)
		if g == nil {
			continue
		}
		alias := map[string]string{} // local import name -> repo dir
		for _, im := range g.file.Imports {
			ip, _ := strconv.Unquote(im.Path.Value)
			dir, ok := strings.CutPrefix(ip, modulePath+"/")
			if !ok {
				continue
			}
			name := path.Base(ip)
			if im.Name != nil {
				name = im.Name.Name
			}
			alias[name] = dir
		}
		tn := importName(g.file, typesIP)
		for _, d := range g.file.Decls {
			called := map[checker]bool{}
			vocabs := map[string]int{}
			ast.Inspect(d, func(n ast.Node) bool {
				ce, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				var c checker
				switch f := ce.Fun.(type) {
				case *ast.Ident:
					c = checker{path.Dir(p), f.Name}
				case *ast.SelectorExpr:
					x, ok := f.X.(*ast.Ident)
					if !ok {
						return true
					}
					dir, ok := alias[x.Name]
					if !ok {
						return true
					}
					c = checker{dir, f.Sel.Name}
					if tn != "" && x.Name == tn {
						if _, ok := vocabField[f.Sel.Name]; ok && vocabs[f.Sel.Name] == 0 {
							vocabs[f.Sel.Name] = line(ce.Pos())
						}
					}
				}
				if checkers[c] {
					called[c] = true
				}
				return true
			})
			if len(called) >= specPipelineMin {
				var names []string
				for c := range called {
					names = append(names, path.Base(c.dir)+"."+c.name)
				}
				sort.Strings(names)
				add(p, g.symbolAt(int(d.Pos())-g.base), []int{line(d.Pos())},
					fmt.Sprintf("refusal pipeline: %d spec checkers (%s)", len(names), abbreviate(names, 3)))
			}
			var fns []string
			for fn := range vocabs {
				fns = append(fns, fn)
			}
			sort.Strings(fns)
			for _, fn := range fns {
				add(p, g.symbolAt(int(d.Pos())-g.base), []int{vocabs[fn]},
					fmt.Sprintf("vocabulary registrar: types.%s (%s)", fn, vocabField[fn]))
			}
		}
	}
	// capability lists
	capRe := regexp.MustCompile(`capability:` + strings.ToLower(sp.Family) + `_[a-z0-9_]+`)
	for _, p := range r.files {
		switch classify(p) {
		case "history", "golden", "docs-generated":
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
		if len(distinct) < 2 {
			continue
		}
		var hs []hit
		for _, l := range locs {
			hs = append(hs, hit{Path: p, Off: l[0], Line: lineOf(src, l[0])})
		}
		for _, s := range r.resolve(p, hs) {
			if s.CommentOnly {
				continue
			}
			add(p, s.Name, s.Lines, fmt.Sprintf("capability list: %d features", len(distinct)))
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

func lineOf(src []byte, off int) int {
	return strings.Count(string(src[:off]), "\n") + 1
}

func sortedInts(xs []int) []int {
	sort.Ints(xs)
	return xs
}
