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

// typeCheck type-checks every module package (with its in-package and
// external tests) and records each reference to a field of the named
// struct in the types package.
func (r *repo) typeCheck(structName string) (*typedTree, error) {
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
	t := &typedTree{fset: token.NewFileSet(), files: map[string]*ast.File{}}
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
		info := &types.Info{Selections: map[*ast.SelectorExpr]*types.Selection{}, Types: map[ast.Expr]types.TypeAndValue{}}
		conf := types.Config{Importer: imp, Error: func(error) {}, FakeImportC: true}
		_, _ = conf.Check(u.path, t.fset, u.files, info)
		for _, f := range u.files {
			rel := t.fset.File(f.Pos()).Name()
			base := t.fset.File(f.Pos()).Base()
			ast.Inspect(f, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.SelectorExpr:
					sel := info.Selections[n]
					if sel == nil || sel.Kind() != types.FieldVal {
						return true
					}
					if owner(sel) == structName {
						t.refs = append(t.refs, slotRef{rel, int(n.Sel.Pos()) - base, sel.Obj().Name()})
					}
				case *ast.CompositeLit:
					tv, ok := info.Types[n]
					if !ok || namedIn(tv.Type) != structName {
						return true
					}
					for _, e := range n.Elts {
						if kv, ok := e.(*ast.KeyValueExpr); ok {
							if id, ok := kv.Key.(*ast.Ident); ok {
								t.refs = append(t.refs, slotRef{rel, int(id.Pos()) - base, id.Name})
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

func slotReport(r *repo, which, name string, opts options) (*Report, error) {
	var structName string
	switch strings.ToLower(which) {
	case "request", "req":
		structName = "Request"
	case "response", "resp":
		structName = "Response"
	default:
		return nil, fmt.Errorf("slot root must be request or response, got %q", which)
	}
	fields, err := r.structFields(structName)
	if err != nil {
		return nil, err
	}
	var field *structField
	for i, f := range fields {
		if strings.EqualFold(f.Name, name) || f.JSON == name {
			field = &fields[i]
		}
	}
	tt, err := r.typeCheck(structName)
	if err != nil {
		return nil, err
	}
	rep := &Report{Mode: "slot", Target: structName + "." + name}

	// per-slot walkers: declarations referencing many of the struct's fields
	type declKey struct{ path, sym string }
	declFields := map[declKey]map[string]bool{}
	declLines := map[declKey][]int{}
	for _, ref := range tt.refs {
		g := r.parse(ref.Path)
		if g == nil {
			continue
		}
		k := declKey{ref.Path, g.symbolAt(ref.Off)}
		if declFields[k] == nil {
			declFields[k] = map[string]bool{}
		}
		declFields[k][ref.Field] = true
		if field != nil && ref.Field == field.Name {
			declLines[k] = append(declLines[k], r.fset.Position(token.Pos(g.base+ref.Off)).Line)
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
		g := r.parse(p)
		if g == nil {
			continue
		}
		base := tt.fset.File(f.Pos()).Base()
		ast.Inspect(f, func(n ast.Node) bool {
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

	if field == nil {
		rep.Summary = []string{
			fmt.Sprintf("no field %q on types.%s — NEW slot template: add the field to types.%s, then extend every per-slot walker below", name, structName, structName),
			fmt.Sprintf("walkers = declarations referencing >= %d of the %d %s fields (go/types-resolved field use or json-name string literal)", walkerMin, len(fields), structName),
		}
		addSym(fields[0].Path, "type "+structName, []int{fields[0].Line}, "declare the field + json tag (omitempty: additive wire)")
		regenPaths = append(regenPaths, "descriptor/testdata/payload-schema.json", "internal/apigolden/testdata/public_api.txt")
		for _, k := range walkers {
			addSym(k.path, k.sym, nil, fmt.Sprintf("[%d/%d fields]", len(declFields[k]), len(fields)))
		}
	} else {
		rep.Summary = []string{
			fmt.Sprintf("types.%s.%s %s `json:\"%s\"` — %s L%d", structName, field.Name, field.Type, field.JSON, field.Path, field.Line),
			"Go references are go/types-resolved (promotions included); string literals equal to the json name and Markdown mentions are textual",
		}
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
		// string literals equal to the json name
		if field.JSON != "" && field.JSON != "-" {
			lit := strconv.Quote(field.JSON)
			var paths []string
			for p := range tt.files {
				paths = append(paths, p)
			}
			sort.Strings(paths)
			for _, p := range paths {
				f := tt.files[p]
				base := tt.fset.File(f.Pos()).Base()
				g := r.parse(p)
				symLines := map[string][]int{}
				var order []string
				ast.Inspect(f, func(n ast.Node) bool {
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
		qual := structName + "." + field.Name
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
		// update-demand rows naming the json name in backticks
		if field.JSON != "" {
			tick := "`" + field.JSON + "`"
			for _, p := range r.files {
				if !strings.HasPrefix(p, ".claude/reference/update-demand") {
					continue
				}
				src := r.read(p)
				var hs []hit
				for i, line := range strings.Split(string(src), "\n") {
					if strings.HasPrefix(line, "|") && strings.Contains(line, tick) && !strings.Contains(line, qual) {
						hs = append(hs, hit{Path: p, Line: i + 1})
					}
				}
				for _, s := range r.resolve(p, hs) {
					addSym(p, s.Name, s.Lines, "table row names "+tick)
				}
			}
		}
		// goldens carrying the json name as a key
		for _, g := range []string{"descriptor/testdata/payload-schema.json", "descriptor/testdata/manifest.json"} {
			if src := r.read(g); src != nil && bytes.Contains(src, []byte(strconv.Quote(field.JSON)+":")) && !contains(regenPaths, g) {
				regenPaths = append(regenPaths, g)
			}
		}
	}
	if field != nil {
		// walkers that skip this slot: candidates for a missing site
		for _, k := range walkers {
			if !declFields[k][field.Name] && classify(k.path) != "test" && !listed[k.path+"\x00"+k.sym] {
				rep.Warnings = append(rep.Warnings, fmt.Sprintf("per-slot walker does not reference %s (check): %s · %s [%d/%d fields]",
					field.Name, k.path, k.sym, len(declFields[k]), len(fields)))
			}
		}
	}
	for cl := range byClass {
		sites := byClass[cl]
		sort.Slice(sites, func(i, j int) bool { return sites[i].Path < sites[j].Path })
	}
	rep.Sections = sectionsFrom(byClass)
	why := "tests that reference the slot (go/types-resolved or by json name)"
	if field == nil {
		why = "tests that exercise the per-slot walkers (add a case for the new slot)"
	}
	rep.Gates = append(rep.Gates, gatesFromDirs(why, testsByDir)...)
	rep.Gates = append(rep.Gates, r.staticGates(classes)...)
	if field == nil {
		rep.Gates = append(rep.Gates, r.namedGates("payload contract gates", "TestPayloadSchemaGolden", "TestPublicAPIGolden", "TestUpdateDemandTableCovers")...)
	}
	rep.Regenerate = r.regens(regenPaths)
	rep.Manual = append([]string{}, slotManual...)
	if structName == "Response" {
		rep.Manual = append(rep.Manual, slotManualResponse...)
	}
	return rep, nil
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
