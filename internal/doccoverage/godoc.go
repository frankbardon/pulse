package doccoverage

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"sort"
	"strings"

	"github.com/spf13/afero"
)

// ModulePath is the Go module the public API dump describes.
const ModulePath = "github.com/frankbardon/pulse"

// goSource indexes the doc comments of Go packages read from the module
// tree, parsed lazily by repo-relative directory.
type goSource struct {
	fsys afero.Fs
	pkgs map[string]*pkgIndex
}

type typeDecl struct {
	spec *ast.TypeSpec
	gen  *ast.GenDecl
	file *ast.File
}

type pkgIndex struct {
	dir     string
	types   map[string]typeDecl
	values  map[string]bool // const / var name -> documented
	funcs   map[string]bool // func name -> documented
	methods map[string]bool // "Type.Method" -> documented
	imports map[*ast.File]map[string]string
}

func newGoSource(fsys afero.Fs) *goSource {
	return &goSource{fsys: fsys, pkgs: map[string]*pkgIndex{}}
}

// dirOf maps a dump package path ("pulse", "pulse/types",
// "pulse/internal/service") or a full import path inside the module to its
// repo-relative directory; ok is false outside the module.
func dirOf(pkgPath string) (string, bool) {
	switch {
	case pkgPath == "pulse" || pkgPath == ModulePath:
		return ".", true
	case strings.HasPrefix(pkgPath, ModulePath+"/"):
		return strings.TrimPrefix(pkgPath, ModulePath+"/"), true
	case strings.HasPrefix(pkgPath, "pulse/"):
		return strings.TrimPrefix(pkgPath, "pulse/"), true
	}
	return "", false
}

func hasText(cgs ...*ast.CommentGroup) bool {
	for _, cg := range cgs {
		if cg != nil && strings.TrimSpace(cg.Text()) != "" {
			return true
		}
	}
	return false
}

func (g *goSource) pkg(dir string) (*pkgIndex, error) {
	if p, ok := g.pkgs[dir]; ok {
		return p, nil
	}
	infos, err := afero.ReadDir(g.fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("doccoverage: read package %s: %w", dir, err)
	}
	names := make([]string, 0, len(infos))
	for _, fi := range infos {
		n := fi.Name()
		if fi.IsDir() || !strings.HasSuffix(n, ".go") || strings.HasSuffix(n, "_test.go") {
			continue
		}
		names = append(names, n)
	}
	sort.Strings(names)
	p := &pkgIndex{dir: dir, types: map[string]typeDecl{}, values: map[string]bool{}, funcs: map[string]bool{},
		methods: map[string]bool{}, imports: map[*ast.File]map[string]string{}}
	fset := token.NewFileSet()
	for _, n := range names {
		src, err := afero.ReadFile(g.fsys, path.Join(dir, n))
		if err != nil {
			return nil, err
		}
		f, err := parser.ParseFile(fset, n, src, parser.ParseComments)
		if err != nil {
			return nil, fmt.Errorf("doccoverage: parse %s/%s: %w", dir, n, err)
		}
		imps := map[string]string{}
		for _, is := range f.Imports {
			ip := strings.Trim(is.Path.Value, `"`)
			name := path.Base(ip)
			if is.Name != nil {
				name = is.Name.Name
			}
			imps[name] = ip
		}
		p.imports[f] = imps
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				doc := hasText(d.Doc)
				if d.Recv == nil {
					p.funcs[d.Name.Name] = p.funcs[d.Name.Name] || doc
					continue
				}
				if recv := recvName(d.Recv.List[0].Type); recv != "" {
					k := recv + "." + d.Name.Name
					p.methods[k] = p.methods[k] || doc
				}
			case *ast.GenDecl:
				for _, s := range d.Specs {
					switch s := s.(type) {
					case *ast.TypeSpec:
						if prev, ok := p.types[s.Name.Name]; !ok || !hasText(prev.spec.Doc, prev.spec.Comment, prev.gen.Doc) {
							p.types[s.Name.Name] = typeDecl{spec: s, gen: d, file: f}
						}
					case *ast.ValueSpec:
						doc := hasText(s.Doc, s.Comment, d.Doc)
						for _, id := range s.Names {
							p.values[id.Name] = p.values[id.Name] || doc
						}
					}
				}
			}
		}
	}
	g.pkgs[dir] = p
	return p, nil
}

func recvName(e ast.Expr) string {
	for {
		switch t := e.(type) {
		case *ast.StarExpr:
			e = t.X
		case *ast.IndexExpr:
			e = t.X
		case *ast.IndexListExpr:
			e = t.X
		case *ast.Ident:
			return t.Name
		default:
			return ""
		}
	}
}

// typeDoc reports whether type name in dir is documented.
func (g *goSource) typeDoc(dir, name string) (bool, error) {
	p, err := g.pkg(dir)
	if err != nil {
		return false, err
	}
	td, ok := p.types[name]
	if !ok {
		return false, nil
	}
	return hasText(td.spec.Doc, td.spec.Comment, td.gen.Doc), nil
}

func (g *goSource) funcDoc(dir, name string) (bool, error) {
	p, err := g.pkg(dir)
	if err != nil {
		return false, err
	}
	return p.funcs[name], nil
}

func (g *goSource) valueDoc(dir, name string) (bool, error) {
	p, err := g.pkg(dir)
	if err != nil {
		return false, err
	}
	return p.values[name], nil
}

// fieldDoc reports whether field of struct type typeName in dir carries a
// doc or trailing comment.
func (g *goSource) fieldDoc(dir, typeName, field string) (bool, error) {
	p, err := g.pkg(dir)
	if err != nil {
		return false, err
	}
	td, ok := p.types[typeName]
	if !ok {
		return false, nil
	}
	st, ok := td.spec.Type.(*ast.StructType)
	if !ok {
		return false, nil
	}
	for _, f := range st.Fields.List {
		for _, n := range f.Names {
			if n.Name == field {
				return hasText(f.Doc, f.Comment), nil
			}
		}
	}
	return false, nil
}

// methodDoc reports whether method on typeName (declared in dir) is
// documented, following aliases to their target and embeddings to the type
// that declares a promoted method.
func (g *goSource) methodDoc(dir, typeName, method string) (bool, error) {
	return g.methodDocDepth(dir, typeName, method, 0)
}

func (g *goSource) methodDocDepth(dir, typeName, method string, depth int) (bool, error) {
	if depth > 8 {
		return false, nil
	}
	p, err := g.pkg(dir)
	if err != nil {
		return false, err
	}
	if doc, ok := p.methods[typeName+"."+method]; ok {
		return doc, nil
	}
	td, ok := p.types[typeName]
	if !ok {
		return false, nil
	}
	if td.spec.Assign.IsValid() {
		if d, n, ok := g.resolve(p, td.file, td.spec.Type); ok {
			return g.methodDocDepth(d, n, method, depth+1)
		}
		return false, nil
	}
	var embeds []ast.Expr
	switch t := td.spec.Type.(type) {
	case *ast.StructType:
		for _, f := range t.Fields.List {
			if len(f.Names) == 0 {
				embeds = append(embeds, f.Type)
			}
		}
	case *ast.InterfaceType:
		for _, f := range t.Methods.List {
			for _, n := range f.Names {
				if n.Name == method {
					return hasText(f.Doc, f.Comment), nil
				}
			}
			if len(f.Names) == 0 {
				embeds = append(embeds, f.Type)
			}
		}
	}
	for _, e := range embeds {
		d, n, ok := g.resolve(p, td.file, e)
		if !ok {
			continue
		}
		if doc, err := g.methodDocDepth(d, n, method, depth+1); err != nil || doc {
			return doc, err
		}
	}
	return false, nil
}

// resolve maps a type expression in file f of package p to the directory
// and name of the type it names; ok is false outside the module.
func (g *goSource) resolve(p *pkgIndex, f *ast.File, e ast.Expr) (string, string, bool) {
	for {
		switch t := e.(type) {
		case *ast.StarExpr:
			e = t.X
		case *ast.IndexExpr:
			e = t.X
		case *ast.IndexListExpr:
			e = t.X
		case *ast.Ident:
			return p.dir, t.Name, true
		case *ast.SelectorExpr:
			x, ok := t.X.(*ast.Ident)
			if !ok {
				return "", "", false
			}
			d, ok := dirOf(p.imports[f][x.Name])
			return d, t.Sel.Name, ok
		default:
			return "", "", false
		}
	}
}
