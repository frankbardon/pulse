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

// I/O: two families behind one `io <name>` mode.
//
//   - Formats: the pio.Format constants listed in formatFile's `formats`
//     slice (csv … spss), each spelled by its constant (FormatCSV) and its
//     wire name (`csv`, quoted or backticked), with its adapter package
//     internal/io/<name>/ and that package's importers.
//   - Hooks: the exported interfaces of internal/iocore (Reader,
//     SchemaAwareReader, SidecarEmitter, …), each spelled by its name, with
//     its implementers (the types declaring every method of its method
//     set, embedded interfaces included) and the job pipelines that
//     type-assert to it (internal/io import / convert / widen / export).
//
// A name matching neither is a NEW format (lower case) or a NEW hook (any
// upper-case letter): the report lists the tables it joins and, for a
// hook, every reader / writer adapter that may implement it and every
// pipeline that dispatches on a sibling hook.
const (
	formatFile = "io/format.go"
	iocoreDir  = "internal/iocore"
	ioDir      = "internal/io"
)

var ioManual = []string{
	".claude/reference/byte-layout.md",
	".claude/reference/update-demand/io-import.md",
	".claude/reference/update-demand/io-export.md",
	".claude/reference/architecture.md",
	"docs/roadmap/v1.0.0-api-and-release/03-embedder-migration.md",
	"docs/src/internals/adding-io-format.md",
}

var ioGates = []string{"TestIOImportBoundary", "TestIOImportBoundary_FacadeReachesAdapters", "TestIOImportBoundary_NoFileImportsFacade", "TestPublicAPIGolden"}

func formatMember(wire, konst string) member {
	m := member{Name: wire, Ticked: []string{wire}}
	if konst != "" {
		m.Words = []string{konst}
	}
	return m
}

// loadFormats reads the format family.
func (r *repo) loadFormats() (*family, error) {
	g := r.parse(formatFile)
	if g == nil {
		return nil, fmt.Errorf("io: %s not found", formatFile)
	}
	value := map[string]string{}
	var listed []string
	for _, d := range g.file.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, s := range gd.Specs {
			vs, ok := s.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, n := range vs.Names {
				if gd.Tok == token.CONST && i < len(vs.Values) {
					if bl, ok := vs.Values[i].(*ast.BasicLit); ok && bl.Kind == token.STRING {
						value[n.Name], _ = strconv.Unquote(bl.Value)
					}
				}
				if gd.Tok == token.VAR && n.Name == "formats" && i < len(vs.Values) {
					if cl, ok := vs.Values[i].(*ast.CompositeLit); ok {
						for _, e := range cl.Elts {
							if id, ok := e.(*ast.Ident); ok {
								listed = append(listed, id.Name)
							}
						}
					}
				}
			}
		}
	}
	f := family{Mode: "io", Noun: "I/O format", Source: formatFile + " formats", Gates: ioGates, Manual: ioManual}
	for _, k := range listed {
		if v, ok := value[k]; ok {
			f.Members = append(f.Members, formatMember(v, k))
		}
	}
	f.newMember = func(name string) member { return formatMember(strings.ToLower(name), "") }
	return &f, nil
}

// hookIface is one iocore interface: its own methods and embeds.
type hookIface struct {
	Path    string
	Methods []string
	Embeds  []string
}

// iocoreHooks reads every exported interface of internal/iocore.
func (r *repo) iocoreHooks() map[string]hookIface {
	out := map[string]hookIface{}
	for _, p := range r.files {
		if path.Dir(p) != iocoreDir || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
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
				it, ok := ts.Type.(*ast.InterfaceType)
				if !ok || !ts.Name.IsExported() {
					continue
				}
				h := hookIface{Path: p}
				for _, m := range it.Methods.List {
					if len(m.Names) == 0 {
						if id, ok := m.Type.(*ast.Ident); ok {
							h.Embeds = append(h.Embeds, id.Name)
						}
						continue
					}
					for _, n := range m.Names {
						h.Methods = append(h.Methods, n.Name)
					}
				}
				out[ts.Name.Name] = h
			}
		}
	}
	return out
}

// methodSet resolves a hook's methods through its embeds.
func methodSet(hooks map[string]hookIface, name string, seen map[string]bool) []string {
	if seen[name] {
		return nil
	}
	seen[name] = true
	h := hooks[name]
	out := append([]string{}, h.Methods...)
	for _, e := range h.Embeds {
		out = append(out, methodSet(hooks, e, seen)...)
	}
	return out
}

// loadHooks reads the hook family.
func (r *repo) loadHooks(hooks map[string]hookIface) *family {
	var names []string
	for n := range hooks {
		names = append(names, n)
	}
	sort.Strings(names)
	f := family{Mode: "io", Noun: "iocore hook", Source: iocoreDir + " exported interfaces", Gates: ioGates, Manual: ioManual}
	for _, n := range names {
		f.Members = append(f.Members, member{Name: n, Words: []string{n}})
	}
	f.newMember = func(name string) member { return member{Name: name, Words: []string{name}} }
	return &f
}

// methodIndex maps package dir -> receiver type -> declared method names
// (non-test files).
func (r *repo) methodIndex() map[string]map[string]map[string]bool {
	out := map[string]map[string]map[string]bool{}
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
			if !ok || fd.Recv == nil {
				continue
			}
			recv := strings.TrimPrefix(strings.TrimSuffix(strings.TrimPrefix(funcName(fd), "func ("), ") "+fd.Name.Name), "*")
			dir := path.Dir(p)
			if out[dir] == nil {
				out[dir] = map[string]map[string]bool{}
			}
			if out[dir][recv] == nil {
				out[dir][recv] = map[string]bool{}
			}
			out[dir][recv][fd.Name.Name] = true
		}
	}
	return out
}

// implementers lists the types (as "dir.Type") declaring every method.
func implementers(idx map[string]map[string]map[string]bool, methods []string) []string {
	if len(methods) == 0 {
		return nil
	}
	var out []string
	for dir, types := range idx {
		for t, ms := range types {
			ok := true
			for _, m := range methods {
				if !ms[m] {
					ok = false
					break
				}
			}
			if ok {
				out = append(out, dir+"."+t)
			}
		}
	}
	sort.Strings(out)
	return out
}

// ioReport is the `io <format|hook>` checklist.
func ioReport(r *repo, name string, opts options) (*Report, error) {
	formats, err := r.loadFormats()
	if err != nil {
		return nil, err
	}
	hooks := r.iocoreHooks()
	hookFam := r.loadHooks(hooks)
	isHook := hookFam.lookup(name) >= 0 || formats.lookup(name) < 0 && strings.IndexFunc(name, unicode.IsUpper) >= 0
	if !isHook {
		return r.familyReport(formats, name, opts, func(c *famCtx, rep *Report) { r.formatSections(c, rep) })
	}
	return r.familyReport(hookFam, name, opts, func(c *famCtx, rep *Report) { r.hookSections(c, rep, hooks) })
}

// formatSections adds the adapter package (to create, for a NEW format)
// and the files importing it.
func (r *repo) formatSections(c *famCtx, rep *Report) {
	dir := ioDir + "/" + c.target.Name
	sec := Section{Class: "io-adapter", Title: "Adapter package " + dir + "/ (reader + writer) and its importers"}
	have := false
	for _, p := range r.files {
		if path.Dir(p) == dir && strings.HasSuffix(p, ".go") && !strings.HasSuffix(p, "_test.go") {
			have = true
			sec.Sites = append(sec.Sites, Site{Path: p})
		}
	}
	if !have {
		sec.Sites = append(sec.Sites, Site{Path: dir + "/" + c.target.Name + ".go", Note: "create: implement iocore.Reader and iocore.Writer (" + "copy a sibling such as " + ioDir + "/" + c.ms[0].Name + ")"})
	}
	ip := strconv.Quote(modulePath + "/" + dir)
	for _, p := range r.files {
		if path.Dir(p) == dir || !strings.HasSuffix(p, ".go") {
			continue
		}
		g := r.parse(p)
		if g == nil {
			continue
		}
		for _, im := range g.file.Imports {
			if im.Path.Value == ip {
				sec.Sites = append(sec.Sites, Site{Path: p, Note: "imports the adapter"})
				if strings.HasSuffix(p, "_test.go") {
					c.testsByDir[path.Dir(p)] = append(c.testsByDir[path.Dir(p)], r.testFuncsIn(p)...)
				}
			}
		}
	}
	rep.Sections = append(rep.Sections, sec)
	if have {
		c.gates = append(c.gates, Gate{Why: "the adapter package's own tests", Package: dir, Command: goTestCmd(dir, nil)})
	}
}

// testFuncsIn lists a test file's Test functions.
func (r *repo) testFuncsIn(p string) []string {
	var out []string
	if g := r.parse(p); g != nil {
		for _, d := range g.file.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok && fd.Recv == nil && strings.HasPrefix(fd.Name.Name, "Test") {
				out = append(out, fd.Name.Name)
			}
		}
	}
	return out
}

// hookSections adds the hook's implementers (for a NEW hook, the reader
// or writer adapters that may implement it) and the pipelines that
// type-assert to it (for a NEW hook, those asserting any sibling).
func (r *repo) hookSections(c *famCtx, rep *Report, hooks map[string]hookIface) {
	idx := r.methodIndex()
	impl := Section{Class: "io-implementers", Title: "Implementers — types declaring every method of " + c.target.Name}
	var methods []string
	if !c.isNew {
		methods = methodSet(hooks, c.target.Name, map[string]bool{})
	}
	targets := implementers(idx, methods)
	if c.isNew {
		impl.Title = "Adapters that may implement the NEW hook — every iocore.Reader and iocore.Writer implementer"
		targets = append(implementers(idx, methodSet(hooks, "Reader", map[string]bool{})), implementers(idx, methodSet(hooks, "Writer", map[string]bool{}))...)
		sort.Strings(targets)
		targets = dedupe(targets)
	}
	var add sectionAdder
	for _, t := range targets {
		dir, typ := t[:strings.LastIndex(t, ".")], t[strings.LastIndex(t, ".")+1:]
		if dir != iocoreDir {
			add.add(dir+"/", "type "+typ, nil, "")
		}
	}
	impl.Sites = add.list()
	if len(impl.Sites) > 0 {
		rep.Sections = append(rep.Sections, impl)
	}

	// pipelines: non-test declarations type-asserting to a hook
	pipe := Section{Class: "io-pipelines", Title: "Pipelines that type-assert to " + c.target.Name + " (import / convert / widen / export jobs)"}
	if c.isNew {
		pipe.Title = "Pipelines that type-assert to a sibling hook — where a NEW hook is consulted"
	}
	names := map[string]bool{}
	for i, m := range c.ms {
		if (c.isNew && i != c.idx) || (!c.isNew && i == c.idx) {
			names[m.Name] = true
		}
	}
	var pipes sectionAdder
	for _, p := range r.files {
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			continue
		}
		g := r.parse(p)
		if g == nil {
			continue
		}
		ast.Inspect(g.file, func(n ast.Node) bool {
			ta, ok := n.(*ast.TypeAssertExpr)
			if !ok || ta.Type == nil {
				return true
			}
			var id string
			switch t := ta.Type.(type) {
			case *ast.Ident:
				id = t.Name
			case *ast.SelectorExpr:
				id = t.Sel.Name
			}
			if names[id] {
				off := int(ta.Pos()) - g.base
				pipes.add(p, g.symbolAt(off), []int{r.fset.Position(ta.Pos()).Line}, "asserts "+id)
			}
			return true
		})
	}
	pipe.Sites = pipes.list()
	if len(pipe.Sites) > 0 {
		rep.Sections = append(rep.Sections, pipe)
	}
}
