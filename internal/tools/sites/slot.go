package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"math"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

const typesPkg = modulePath + "/types"

// slotManual is the reading list for a slot change (manual by nature).
var slotManual = []string{
	udIndex,
	".claude/reference/update-demand/payload-and-return.md",
	".claude/reference/predict-inspect.md",
	refFeatures,
	".claude/reference/architecture.md",
}

var slotManualResponse = []string{udComps, refComps}

// slotManualCompose is the extra reading for a Compose-level slot.
var slotManualCompose = []string{
	".claude/reference/update-demand/compose.md",
	".claude/reference/execution-modes/compose.md",
}

// slotRef is one go/types-resolved reference to a field of the slot
// struct (selector or composite-literal key).
type slotRef struct {
	Path  string
	Off   int
	Field string
}

// typedTree is the module type-checked from source, every dependency
// read from compiler export data (`go list -export`).
type typedTree struct {
	fset  *token.FileSet
	files map[string]*ast.File // repo-relative path -> AST
	refs  []slotRef
	calls []sharedCall
}

type listPkg struct {
	ImportPath   string
	Dir          string
	Name         string
	Export       string
	ForTest      string
	GoFiles      []string
	TestGoFiles  []string
	XTestGoFiles []string
	Module       *struct{ Path string }
}

func goList(root string, args ...string) ([]listPkg, error) {
	cmd := exec.Command("go", append([]string{"list", "-e", "-export", "-json=ImportPath,Dir,Name,Export,ForTest,GoFiles,TestGoFiles,XTestGoFiles,Module"}, args...)...)
	cmd.Dir = root
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list: %v: %s", err, stderr.String())
	}
	var pkgs []listPkg
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		var p listPkg
		if err := dec.Decode(&p); err == io.EOF {
			break
		} else if err != nil {
			return nil, err
		}
		pkgs = append(pkgs, p)
	}
	return pkgs, nil
}

// typedModule is the module type-checked once per repo: the shared
// file set and ASTs plus every field reference, keyed by the
// types-package struct that declares the field.
type typedModule struct {
	fset  *token.FileSet
	files map[string]*ast.File
	refs  map[string][]slotRef
	// calls are the runtime's (internal/service) calls into
	// internal/descriptor functions handed a types struct or one of its
	// fields, keyed by that struct: the predict ≡ runtime shared
	// derivations (descx.ResultCatalog(composed.Requests, …)).
	calls map[string][]sharedCall
}

// sharedCall is one runtime call into a descriptor function.
type sharedCall struct {
	Callee string // descriptor function name
	Arg    string // the struct field handed over ("" = the whole struct)
	Path   string // caller file
	Off    int
}

// typeCheck returns the module's references to fields of the named
// struct in the types package. The module is type-checked once per repo
// (typeCheckAll, memoised) whatever the struct; every typedTree shares
// its read-only file set and ASTs.
func (r *repo) typeCheck(structName string) (*typedTree, error) {
	r.typedOnce.Do(func() { r.typed, r.typedErr = r.typeCheckAll() })
	if r.typedErr != nil {
		return nil, r.typedErr
	}
	return &typedTree{fset: r.typed.fset, files: r.typed.files, refs: r.typed.refs[structName], calls: r.typed.calls[structName]}, nil
}

// typeCheckAll type-checks every module package (with its in-package and
// external tests) and records each reference to a field of any struct
// in the types package, in source order per struct.
func (r *repo) typeCheckAll() (*typedModule, error) {
	pkgs, err := goList(r.root, "-deps", "./...")
	if err != nil {
		return nil, err
	}
	exports := map[string]string{}
	var mine []listPkg
	for _, p := range pkgs {
		if p.Export != "" {
			exports[p.ImportPath] = p.Export
		}
		if p.Module != nil && p.Module.Path == modulePath && p.ForTest == "" {
			mine = append(mine, p)
		}
	}
	t := &typedModule{fset: token.NewFileSet(), files: map[string]*ast.File{}, refs: map[string][]slotRef{}, calls: map[string][]sharedCall{}}
	parse := func(dir string, names []string) []*ast.File {
		var out []*ast.File
		for _, n := range names {
			abs := filepath.Join(dir, n)
			rel, err := filepath.Rel(r.root, abs)
			if err != nil {
				continue
			}
			rel = filepath.ToSlash(rel)
			if skipPath(rel) {
				continue
			}
			src, err := os.ReadFile(abs)
			if err != nil {
				continue
			}
			f, _ := parser.ParseFile(t.fset, rel, src, parser.ParseComments|parser.SkipObjectResolution)
			if f != nil {
				t.files[rel] = f
				out = append(out, f)
			}
		}
		return out
	}
	type unit struct {
		path  string
		files []*ast.File
	}
	var units []unit
	missing := map[string]bool{}
	for _, p := range mine {
		in := parse(p.Dir, append(append([]string{}, p.GoFiles...), p.TestGoFiles...))
		x := parse(p.Dir, p.XTestGoFiles)
		for _, fs := range [][]*ast.File{in, x} {
			for _, f := range fs {
				for _, im := range f.Imports {
					ip, _ := strconv.Unquote(im.Path.Value)
					if _, ok := exports[ip]; !ok && ip != "C" && ip != "unsafe" {
						missing[ip] = true
					}
				}
			}
		}
		if len(in) > 0 {
			units = append(units, unit{p.ImportPath, in})
		}
		if len(x) > 0 {
			units = append(units, unit{p.ImportPath + "_test", x})
		}
	}
	if len(missing) > 0 {
		var args []string
		for ip := range missing {
			args = append(args, ip)
		}
		sort.Strings(args)
		more, err := goList(r.root, append([]string{"-deps"}, args...)...)
		if err != nil {
			return nil, err
		}
		for _, p := range more {
			if p.Export != "" {
				exports[p.ImportPath] = p.Export
			}
		}
	}
	imp := importer.ForCompiler(t.fset, "gc", func(ip string) (io.ReadCloser, error) {
		f, ok := exports[ip]
		if !ok {
			return nil, fmt.Errorf("no export data for %s", ip)
		}
		return os.Open(f)
	})
	for _, u := range units {
		info := &types.Info{Selections: map[*ast.SelectorExpr]*types.Selection{}, Types: map[ast.Expr]types.TypeAndValue{}, Uses: map[*ast.Ident]types.Object{}}
		conf := types.Config{Importer: imp, Error: func(error) {}, FakeImportC: true}
		_, _ = conf.Check(u.path, t.fset, u.files, info)
		for _, f := range u.files {
			rel := t.fset.File(f.Pos()).Name()
			base := t.fset.File(f.Pos()).Base()
			runtime := strings.HasPrefix(rel, "internal/service/") && !strings.HasSuffix(rel, "_test.go")
			ast.Inspect(f, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.CallExpr:
					if runtime {
						recordSharedCall(t, info, n, rel, base)
					}
				case *ast.SelectorExpr:
					sel := info.Selections[n]
					if sel == nil || sel.Kind() != types.FieldVal {
						return true
					}
					if name := owner(sel); name != "" {
						t.refs[name] = append(t.refs[name], slotRef{rel, int(n.Sel.Pos()) - base, sel.Obj().Name()})
					}
				case *ast.CompositeLit:
					tv, ok := info.Types[n]
					if !ok {
						return true
					}
					name := namedIn(tv.Type)
					if name == "" {
						return true
					}
					for _, e := range n.Elts {
						if kv, ok := e.(*ast.KeyValueExpr); ok {
							if id, ok := kv.Key.(*ast.Ident); ok {
								t.refs[name] = append(t.refs[name], slotRef{rel, int(id.Pos()) - base, id.Name})
							}
						}
					}
				}
				return true
			})
		}
	}
	return t, nil
}

const descriptorPkg = modulePath + "/internal/descriptor"

// recordSharedCall records a call to an internal/descriptor function
// under every types struct an argument is (or is a field of).
func recordSharedCall(t *typedModule, info *types.Info, ce *ast.CallExpr, rel string, base int) {
	sel, ok := ce.Fun.(*ast.SelectorExpr)
	if !ok {
		return
	}
	fn, ok := info.Uses[sel.Sel].(*types.Func)
	if !ok || fn.Pkg() == nil || fn.Pkg().Path() != descriptorPkg {
		return
	}
	if sig, ok := fn.Type().(*types.Signature); !ok || sig.Recv() != nil {
		return
	}
	type key struct{ name, field string }
	seen := map[key]bool{}
	for _, a := range ce.Args {
		var keys []key
		if as, ok := a.(*ast.SelectorExpr); ok {
			if s := info.Selections[as]; s != nil && s.Kind() == types.FieldVal {
				keys = append(keys, key{owner(s), s.Obj().Name()})
			}
		}
		if tv, ok := info.Types[a]; ok {
			keys = append(keys, key{namedIn(tv.Type), ""})
		}
		for _, k := range keys {
			if k.name != "" && !seen[k] {
				seen[k] = true
				t.calls[k.name] = append(t.calls[k.name], sharedCall{Callee: fn.Name(), Arg: k.field, Path: rel, Off: int(ce.Pos()) - base})
			}
		}
	}
}

// owner names the types-package struct declaring a selected field,
// walking embedded promotions.
func owner(sel *types.Selection) string {
	t := sel.Recv()
	idx := sel.Index()
	for _, i := range idx[:len(idx)-1] {
		st, ok := deref(t).Underlying().(*types.Struct)
		if !ok {
			return ""
		}
		t = st.Field(i).Type()
	}
	return namedIn(t)
}

func deref(t types.Type) types.Type {
	if p, ok := t.Underlying().(*types.Pointer); ok {
		return p.Elem()
	}
	if p, ok := t.(*types.Pointer); ok {
		return p.Elem()
	}
	return t
}

// namedIn returns the type name when t (or *t) is a named type of the
// types package, else "".
func namedIn(t types.Type) string {
	if t == nil {
		return ""
	}
	t = deref(t)
	n, ok := t.(*types.Named)
	if !ok || n.Obj().Pkg() == nil || n.Obj().Pkg().Path() != typesPkg {
		return ""
	}
	return n.Obj().Name()
}

// structField is one declared field of the slot struct.
type structField struct {
	Name string
	JSON string
	Type string
	Line int
	Path string
}

func (r *repo) structFields(structName string) ([]structField, error) {
	for _, p := range r.files {
		if path.Dir(p) != "types" || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			continue
		}
		g := r.parse(p)
		if g == nil {
			continue
		}
		for _, d := range g.file.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok || gd.Tok != token.TYPE {
				continue
			}
			for _, s := range gd.Specs {
				ts := s.(*ast.TypeSpec)
				st, ok := ts.Type.(*ast.StructType)
				if !ok || ts.Name.Name != structName {
					continue
				}
				var out []structField
				for _, f := range st.Fields.List {
					tag := ""
					if f.Tag != nil {
						raw, _ := strconv.Unquote(f.Tag.Value)
						tag, _, _ = strings.Cut(reflect.StructTag(raw).Get("json"), ",")
					}
					src := r.read(p)
					typ := string(src[int(f.Type.Pos())-g.base : int(f.Type.End())-g.base])
					for _, n := range f.Names {
						out = append(out, structField{Name: n.Name, JSON: tag, Type: typ, Line: r.fset.Position(n.Pos()).Line, Path: p})
					}
				}
				return out, nil
			}
		}
	}
	return nil, fmt.Errorf("struct types.%s not found", structName)
}

// slotOwner is one declared field a slot name resolves to.
type slotOwner struct {
	Struct string
	Field  structField
}

// slotRoots are the structs a slot root names, searched in order: a
// `request` slot not on types.Request is looked up on
// types.ComposedRequest (the Compose-level slots: sweep, base, fits, …).
func slotRoots(which string) ([]string, error) {
	switch strings.ToLower(which) {
	case "request", "req":
		return []string{"Request", "ComposedRequest"}, nil
	case "compose", "composed":
		return []string{"ComposedRequest"}, nil
	case "response", "resp":
		return []string{"Response"}, nil
	}
	return nil, fmt.Errorf("slot root must be request, compose or response, got %q", which)
}

// resolveSlot maps a slot name to the root struct whose per-slot walkers
// apply and the declared field(s) it names: a field of a root struct
// first; else every field of that name on a types struct reachable from
// the roots (an item-level key such as `for_each`, root ""); else no
// owner — a NEW slot on the first root.
func (r *repo) resolveSlot(which, name string) (string, []slotOwner, error) {
	roots, err := slotRoots(which)
	if err != nil {
		return "", nil, err
	}
	match := func(f structField) bool { return strings.EqualFold(f.Name, name) || f.JSON == name }
	for _, rt := range roots {
		fields, err := r.structFields(rt)
		if err != nil {
			return "", nil, err
		}
		var hit *structField
		for i := range fields {
			if match(fields[i]) {
				hit = &fields[i]
			}
		}
		if hit != nil {
			return rt, []slotOwner{{rt, *hit}}, nil
		}
	}
	ti := r.typesIndex()
	seen := map[string]bool{}
	queue := append([]string{}, roots...)
	for _, rt := range roots {
		seen[rt] = true
	}
	var nested []string
	for len(queue) > 0 {
		s := queue[0]
		queue = queue[1:]
		for _, f := range ti.structs[s] {
			t := baseType(f.Type)
			if _, ok := ti.structs[t]; ok && !seen[t] {
				seen[t] = true
				nested = append(nested, t)
				queue = append(queue, t)
			}
		}
	}
	sort.Strings(nested)
	var owners []slotOwner
	for _, s := range nested {
		fields, err := r.structFields(s)
		if err != nil {
			continue
		}
		for _, f := range fields {
			if match(f) {
				owners = append(owners, slotOwner{s, f})
			}
		}
	}
	if len(owners) > 0 {
		return "", owners, nil
	}
	return roots[0], nil, nil
}

func slotReport(r *repo, which, name string, opts options) (*Report, error) {
	root, owners, err := r.resolveSlot(which, name)
	if err != nil {
		return nil, err
	}
	var fields []structField // the root's fields (per-slot walkers)
	if root != "" {
		if fields, err = r.structFields(root); err != nil {
			return nil, err
		}
	}
	viewOf := root
	if viewOf == "" {
		viewOf = owners[0].Struct
	}
	tt, err := r.typeCheck(viewOf)
	if err != nil {
		return nil, err
	}
	rep := &Report{Mode: "slot", Target: name}
	if root != "" {
		rep.Target = root + "." + name
	}
	ownsRef := map[string]bool{} // Struct\x00Field of every owner
	for _, o := range owners {
		ownsRef[o.Struct+"\x00"+o.Field.Name] = true
	}

	// per-slot walkers: declarations referencing many of the root's fields
	type declKey struct{ path, sym string }
	declFields := map[declKey]map[string]bool{}
	declLines := map[declKey][]int{}
	refsOf := map[string][]slotRef{} // struct -> refs
	if root != "" {
		refsOf[root] = tt.refs
	}
	for _, o := range owners {
		if _, ok := refsOf[o.Struct]; !ok {
			v, err := r.typeCheck(o.Struct)
			if err != nil {
				return nil, err
			}
			refsOf[o.Struct] = v.refs
		}
	}
	for st, refs := range refsOf {
		for _, ref := range refs {
			g := r.parse(ref.Path)
			if g == nil {
				continue
			}
			k := declKey{ref.Path, g.symbolAt(ref.Off)}
			if st == root {
				if declFields[k] == nil {
					declFields[k] = map[string]bool{}
				}
				declFields[k][ref.Field] = true
			}
			if ownsRef[st+"\x00"+ref.Field] {
				declLines[k] = append(declLines[k], r.fset.Position(token.Pos(g.base+ref.Off)).Line)
			}
		}
	}
	// string-literal slot-name tables ("matrices", "vectors", … listed
	// together) count as walkers too: profile slot tokens, gated slots,
	// fixup paths
	tagOf := map[string]string{}
	for _, f := range fields {
		if f.JSON != "" && f.JSON != "-" {
			tagOf[strconv.Quote(f.JSON)] = f.Name
		}
	}
	for p, f := range tt.files {
		if len(tagOf) == 0 {
			break
		}
		g := r.parse(p)
		if g == nil {
			continue
		}
		base := tt.fset.File(f.Pos()).Base()
		ast.Inspect(f, func(n ast.Node) bool {
			if _, ok := n.(*ast.ImportSpec); ok {
				return false // `import "sort"` is a package path, never the slot
			}
			bl, ok := n.(*ast.BasicLit)
			if !ok || bl.Kind != token.STRING {
				return true
			}
			if fname, ok := tagOf[bl.Value]; ok {
				k := declKey{p, g.symbolAt(int(bl.Pos()) - base)}
				if declFields[k] == nil {
					declFields[k] = map[string]bool{}
				}
				declFields[k][fname] = true
			}
			return true
		})
	}
	walkerMin := int(math.Max(6, math.Ceil(0.25*float64(len(fields)))))
	var walkers []declKey
	for k, fs := range declFields {
		if len(fs) >= walkerMin {
			walkers = append(walkers, k)
		}
	}
	sort.Slice(walkers, func(i, j int) bool {
		if walkers[i].path != walkers[j].path {
			return walkers[i].path < walkers[j].path
		}
		return walkers[i].sym < walkers[j].sym
	})

	byClass := map[string][]Site{}
	classes := map[string]bool{}
	testsByDir := map[string][]string{}
	var regenPaths []string
	listed := map[string]bool{}
	addSym := func(p, sym string, lines []int, note string) {
		cl := classify(p)
		classes[cl] = true
		if cl == "test" {
			if t := testName(sym); t != "" {
				testsByDir[path.Dir(p)] = append(testsByDir[path.Dir(p)], t)
			}
			if !opts.All {
				return
			}
		}
		if (cl == "golden" || cl == "docs-generated") && !contains(regenPaths, p) {
			regenPaths = append(regenPaths, p)
			if !opts.All {
				return
			}
		}
		if cl == "history" && !opts.All {
			return
		}
		sites := byClass[cl]
		i := -1
		for j := range sites {
			if sites[j].Path == p {
				i = j
			}
		}
		if i < 0 {
			sites = append(sites, Site{Path: p})
			i = len(sites) - 1
		}
		listed[p+"\x00"+sym] = true
		for k := range sites[i].Symbols {
			if s := &sites[i].Symbols[k]; s.Name == sym {
				s.Lines = append(s.Lines, lines...)
				sort.Ints(s.Lines)
				s.Lines = dedupeInts(s.Lines)
				if note != "" && !strings.Contains(s.Note, note) {
					s.Note = strings.TrimSpace(s.Note + " " + note)
				}
				byClass[cl] = sites
				return
			}
		}
		sites[i].Symbols = append(sites[i].Symbols, Symbol{Name: sym, Lines: lines, Note: note})
		byClass[cl] = sites
	}

	jsonNames := map[string]bool{}
	if len(owners) == 0 {
		rep.Summary = []string{
			fmt.Sprintf("no field %q on types.%s — NEW slot template: add the field to types.%s, then extend every per-slot walker below", name, root, root),
			fmt.Sprintf("walkers = declarations referencing >= %d of the %d %s fields (go/types-resolved field use or json-name string literal)", walkerMin, len(fields), root),
		}
		addSym(fields[0].Path, "type "+root, []int{fields[0].Line}, "declare the field + json tag (omitempty: additive wire)")
		regenPaths = append(regenPaths, "descriptor/testdata/payload-schema.json", "internal/apigolden/testdata/public_api.txt")
		for _, k := range walkers {
			addSym(k.path, k.sym, nil, fmt.Sprintf("[%d/%d fields]", len(declFields[k]), len(fields)))
		}
	} else {
		for _, o := range owners {
			rep.Summary = append(rep.Summary, fmt.Sprintf("types.%s.%s %s `json:\"%s\"` — %s L%d", o.Struct, o.Field.Name, o.Field.Type, o.Field.JSON, o.Field.Path, o.Field.Line))
			if o.Field.JSON != "" && o.Field.JSON != "-" {
				jsonNames[o.Field.JSON] = true
			}
		}
		if root == "" {
			rep.Summary = append(rep.Summary, fmt.Sprintf("an item-level key: declared on %d types struct(s) reachable from the %s root; no per-slot walker applies", len(owners), which))
		}
		rep.Summary = append(rep.Summary, "Go references are go/types-resolved (promotions included); string literals equal to the json name and Markdown mentions are textual")
		var keys []declKey
		for k := range declLines {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool {
			if keys[i].path != keys[j].path {
				return keys[i].path < keys[j].path
			}
			return keys[i].sym < keys[j].sym
		})
		for _, k := range keys {
			ls := declLines[k]
			sort.Ints(ls)
			addSym(k.path, k.sym, dedupeInts(ls), "")
		}
		var paths []string
		for p := range tt.files {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		// string literals equal to a json name
		for _, j := range sortedKeys(jsonNames) {
			lit := strconv.Quote(j)
			for _, p := range paths {
				f := tt.files[p]
				base := tt.fset.File(f.Pos()).Base()
				g := r.parse(p)
				symLines := map[string][]int{}
				var order []string
				ast.Inspect(f, func(n ast.Node) bool {
					if _, ok := n.(*ast.ImportSpec); ok {
						return false // a stdlib / module path equal to the json name (`sort`)
					}
					bl, ok := n.(*ast.BasicLit)
					if !ok || bl.Kind != token.STRING || bl.Value != lit {
						return true
					}
					sym := "(file)"
					if g != nil {
						sym = g.symbolAt(int(bl.Pos()) - base)
					}
					if _, ok := symLines[sym]; !ok {
						order = append(order, sym)
					}
					symLines[sym] = append(symLines[sym], tt.fset.Position(bl.Pos()).Line)
					return true
				})
				for _, sym := range order {
					addSym(p, sym, symLines[sym], "string literal "+lit)
				}
			}
		}
		// prose + goldens: "Request.Field" spelled out
		var quals []string
		for _, o := range owners {
			quals = append(quals, o.Struct+"."+o.Field.Name)
		}
		for _, qual := range quals {
			for _, p := range r.files {
				if strings.HasSuffix(p, ".go") {
					continue
				}
				src := r.read(p)
				if src == nil || !bytes.Contains(src, []byte(qual)) {
					continue
				}
				var hs []hit
				for off := 0; ; {
					i := bytes.Index(src[off:], []byte(qual))
					if i < 0 {
						break
					}
					at := off + i
					end := at + len(qual)
					if (at == 0 || !isWord(src[at-1])) && (end == len(src) || !isWord(src[end])) {
						hs = append(hs, hit{Path: p, Off: at, Line: bytes.Count(src[:at], []byte("\n")) + 1})
					}
					off = end
				}
				for _, s := range r.resolve(p, hs) {
					addSym(p, s.Name, s.Lines, "")
				}
			}
		}
		for _, j := range sortedKeys(jsonNames) {
			// update-demand rows naming the json name in backticks
			tick := "`" + j + "`"
			for _, p := range r.files {
				if !strings.HasPrefix(p, ".claude/reference/update-demand") {
					continue
				}
				src := r.read(p)
				var hs []hit
				for i, line := range strings.Split(string(src), "\n") {
					if strings.HasPrefix(line, "|") && strings.Contains(line, tick) && !containsAny(line, quals) {
						hs = append(hs, hit{Path: p, Line: i + 1})
					}
				}
				for _, s := range r.resolve(p, hs) {
					addSym(p, s.Name, s.Lines, "table row names "+tick)
				}
			}
			// goldens carrying the json name as a key
			for _, g := range []string{"descriptor/testdata/payload-schema.json", "descriptor/testdata/manifest.json"} {
				if src := r.read(g); src != nil && bytes.Contains(src, []byte(strconv.Quote(j)+":")) && !contains(regenPaths, g) {
					regenPaths = append(regenPaths, g)
				}
			}
		}
	}
	if len(owners) > 0 && root != "" {
		// walkers that skip this slot: candidates for a missing site
		fname := owners[0].Field.Name
		for _, k := range walkers {
			if !declFields[k][fname] && classify(k.path) != "test" && !listed[k.path+"\x00"+k.sym] {
				rep.Warnings = append(rep.Warnings, fmt.Sprintf("per-slot walker does not reference %s (check): %s · %s [%d/%d fields]",
					fname, k.path, k.sym, len(declFields[k]), len(fields)))
			}
		}
	}
	for cl := range byClass {
		sites := byClass[cl]
		sort.Slice(sites, func(i, j int) bool { return sites[i].Path < sites[j].Path })
	}
	rep.Sections = sectionsFrom(byClass)
	var composeGates []Gate
	if root == "ComposedRequest" {
		key := name
		if len(owners) > 0 {
			key = owners[0].Field.JSON
		}
		if sec, ok := r.composeSection(key, len(owners) == 0, tt.calls); ok {
			rep.Sections = append(rep.Sections, sec)
			composeGates = r.sectionTestGates("tests that read the compose-section test tables", sec)
		}
	}
	why := "tests that reference the slot (go/types-resolved or by json name)"
	if len(owners) == 0 {
		why = "tests that exercise the per-slot walkers (add a case for the new slot)"
	}
	rep.Gates = append(rep.Gates, gatesFromDirs(why, testsByDir)...)
	rep.Gates = append(rep.Gates, composeGates...)
	rep.Gates = append(rep.Gates, r.staticGates(classes)...)
	r.wholeRepo(rep, classes, listed)
	if len(owners) == 0 {
		rep.Gates = append(rep.Gates, r.namedGates("payload contract gates", "TestPayloadSchemaGolden", "TestPublicAPIGolden", "TestUpdateDemandTableCovers")...)
	}
	rep.Regenerate = r.regens(regenPaths)
	rep.Manual = append([]string{}, slotManual...)
	switch root {
	case "Response":
		rep.Manual = append(rep.Manual, slotManualResponse...)
	case "ComposedRequest":
		rep.Manual = append(rep.Manual, slotManualCompose...)
	}
	return rep, nil
}

func containsAny(s string, subs []string) bool {
	for _, x := range subs {
		if strings.Contains(s, x) {
			return true
		}
	}
	return false
}

func sortedKeys(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (r *repo) namedGates(why string, names ...string) []Gate {
	byDir := map[string][]string{}
	for _, n := range names {
		for _, d := range r.testIndex()[n] {
			byDir[d] = append(byDir[d], n)
		}
	}
	return gatesFromDirs(why, byDir)
}

func dedupeInts(xs []int) []int {
	var out []int
	for i, x := range xs {
		if i == 0 || x != xs[i-1] {
			out = append(out, x)
		}
	}
	return out
}
