// Package apigolden renders the exported Go surface of every public
// package in the Pulse module as a deterministic, line-oriented text dump.
//
// The dump backs TestPublicAPIGolden, which freezes the public surface in
// testdata/public_api.txt. It exists because apidiff cannot see a field or
// method change behind a type alias whose target lives under internal/: the
// alias name is unchanged, so apidiff reports nothing. Here every alias is
// rendered as its public name PLUS the target's full exported shape (fields
// with tags, method sets, interface methods), so a change to the aliased
// type changes the golden.
//
// Format: one fact per line, every line self-contained and prefixed with its
// package, so a golden diff reads without context. Packages are sorted by
// path, objects by name, members by name. Module-internal package paths are
// shortened from the module path to "pulse" (root "pulse", "pulse/io/csv",
// ...); third-party and standard-library paths are printed in full. A
// reference to an aliased type keeps its alias spelling (go/types preserves
// aliases), so a pure package move behind an unchanged public alias only
// rewrites the "type X = <target>" header and embedded-field lines.
//
// Loading uses `go list -export` plus the standard library gc importer, so
// the package adds no module dependency. GOOS/GOARCH are pinned so a
// build-tagged file can never leak a per-platform symbol into the golden.
package apigolden

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/importer"
	"go/token"
	"go/types"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
)

// Platform pinned for loading. Every Pulse package is pure Go
// (CGO_ENABLED=0); pinning keeps the dump identical on any host.
const (
	pinnedGOOS   = "linux"
	pinnedGOARCH = "amd64"
)

// listedPackage is the subset of `go list -json` output Load consumes.
type listedPackage struct {
	ImportPath string
	Export     string
	Standard   bool
	Error      *struct{ Err string }
}

// IsPublicPackage reports whether importPath (inside modulePath) is part of
// the public surface: not under an internal/ segment, not under cmd/, not a
// test variant.
func IsPublicPackage(modulePath, importPath string) bool {
	if importPath != modulePath && !strings.HasPrefix(importPath, modulePath+"/") {
		return false
	}
	if strings.HasSuffix(importPath, ".test") || strings.Contains(importPath, " [") {
		return false
	}
	rel := strings.TrimPrefix(strings.TrimPrefix(importPath, modulePath), "/")
	for _, seg := range strings.Split(rel, "/") {
		if seg == "internal" || seg == "testdata" {
			return false
		}
	}
	return rel != "cmd" && !strings.HasPrefix(rel, "cmd/")
}

// Load type-checks (from compiler export data) every public package of the
// module rooted at moduleDir and returns them sorted by import path.
func Load(moduleDir, modulePath string) ([]*types.Package, error) {
	cmd := exec.Command("go", "list", "-export", "-deps",
		"-json=ImportPath,Export,Standard,Error", "./...")
	cmd.Dir = moduleDir
	cmd.Env = append(os.Environ(),
		"GOOS="+pinnedGOOS, "GOARCH="+pinnedGOARCH, "CGO_ENABLED=0")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list -export: %v\n%s", err, stderr.String())
	}

	exports := map[string]string{}
	var public []string
	dec := json.NewDecoder(bytes.NewReader(out))
	for dec.More() {
		var p listedPackage
		if err := dec.Decode(&p); err != nil {
			return nil, fmt.Errorf("decoding go list output: %w", err)
		}
		if p.Error != nil {
			return nil, fmt.Errorf("go list: %s: %s", p.ImportPath, p.Error.Err)
		}
		exports[p.ImportPath] = p.Export
		if IsPublicPackage(modulePath, p.ImportPath) {
			public = append(public, p.ImportPath)
		}
	}
	sort.Strings(public)

	fset := token.NewFileSet()
	imp := importer.ForCompiler(fset, "gc", func(path string) (io.ReadCloser, error) {
		file, ok := exports[path]
		if !ok || file == "" {
			return nil, fmt.Errorf("no export data for %q", path)
		}
		return os.Open(file)
	})
	pkgs := make([]*types.Package, 0, len(public))
	for _, path := range public {
		pkg, err := imp.Import(path)
		if err != nil {
			return nil, fmt.Errorf("importing %s: %w", path, err)
		}
		pkgs = append(pkgs, pkg)
	}
	return pkgs, nil
}

// Render produces the deterministic text dump for pkgs. modulePath is
// shortened to "pulse" in every rendered package path.
func Render(modulePath string, pkgs []*types.Package) []byte {
	r := renderer{modulePath: modulePath}
	sorted := append([]*types.Package(nil), pkgs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Path() < sorted[j].Path() })
	for _, pkg := range sorted {
		r.pkg(pkg)
	}
	return r.buf.Bytes()
}

type renderer struct {
	modulePath string
	buf        bytes.Buffer
	prefix     string
}

func (r *renderer) short(path string) string {
	if path == r.modulePath {
		return "pulse"
	}
	if strings.HasPrefix(path, r.modulePath+"/") {
		return "pulse/" + strings.TrimPrefix(path, r.modulePath+"/")
	}
	return path
}

func (r *renderer) qualifier(p *types.Package) string { return r.short(p.Path()) }

func (r *renderer) typ(t types.Type) string { return types.TypeString(t, r.qualifier) }

func (r *renderer) line(format string, args ...any) {
	r.buf.WriteString(r.prefix)
	fmt.Fprintf(&r.buf, format, args...)
	r.buf.WriteByte('\n')
}

func (r *renderer) pkg(pkg *types.Package) {
	r.prefix = r.short(pkg.Path()) + ": "
	r.line("package %s", pkg.Name())
	scope := pkg.Scope()
	for _, name := range scope.Names() { // Names() is sorted
		obj := scope.Lookup(name)
		if !obj.Exported() {
			continue
		}
		switch o := obj.(type) {
		case *types.Const:
			r.line("const %s %s = %s", o.Name(), r.typ(o.Type()), o.Val().ExactString())
		case *types.Var:
			r.line("var %s %s", o.Name(), r.typ(o.Type()))
		case *types.Func:
			r.line("func %s%s", o.Name(), r.signature(o.Type().(*types.Signature)))
		case *types.TypeName:
			r.typeName(o)
		}
	}
}

// signature renders a func signature without the leading "func" keyword,
// including type parameters.
func (r *renderer) signature(sig *types.Signature) string {
	var b bytes.Buffer
	if tps := sig.TypeParams(); tps != nil && tps.Len() > 0 {
		b.WriteString(r.tparams(tps))
	}
	types.WriteSignature(&b, sig, r.qualifier)
	return b.String()
}

func (r *renderer) tparams(tps *types.TypeParamList) string {
	parts := make([]string, tps.Len())
	for i := range tps.Len() {
		tp := tps.At(i)
		parts[i] = tp.Obj().Name() + " " + r.typ(tp.Constraint())
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func (r *renderer) typeName(o *types.TypeName) {
	name := o.Name()
	if o.IsAlias() {
		alias, _ := o.Type().(*types.Alias)
		head := name
		if alias != nil {
			if tps := alias.TypeParams(); tps != nil && tps.Len() > 0 {
				head += r.tparams(tps)
			}
			r.line("type %s = %s", head, r.typ(alias.Rhs()))
		} else {
			r.line("type %s = %s", head, r.typ(o.Type()))
		}
		// Expand the target's full shape under the PUBLIC name, so a change
		// behind the alias changes the dump. The header shows only the
		// target's name, so restate its underlying kind too.
		target := types.Unalias(o.Type())
		if _, ok := target.(*types.Named); ok {
			r.line("type %s underlying %s", name, r.kind(target.Underlying()))
		}
		r.shape(name, target)
		return
	}
	head := name
	if named, ok := o.Type().(*types.Named); ok {
		if tps := named.TypeParams(); tps != nil && tps.Len() > 0 {
			head += r.tparams(tps)
		}
	}
	r.line("type %s %s", head, r.kind(o.Type().Underlying()))
	r.shape(name, o.Type())
}

// kind is the one-word underlying kind for struct/interface, the full
// underlying type otherwise (func, map, basic, ...).
func (r *renderer) kind(u types.Type) string {
	switch u.(type) {
	case *types.Struct:
		return "struct"
	case *types.Interface:
		return "interface"
	}
	return r.typ(u)
}

// shape renders fields, methods and interface methods of t, labelled with
// the public name.
func (r *renderer) shape(name string, t types.Type) {
	switch u := t.Underlying().(type) {
	case *types.Struct:
		r.fields(name, "", u, 0)
	case *types.Interface:
		var methods []string
		unexported := false
		for i := range u.NumMethods() {
			m := u.Method(i)
			if !m.Exported() {
				unexported = true
				continue
			}
			methods = append(methods, fmt.Sprintf("type %s imethod %s%s",
				name, m.Name(), r.signature(m.Type().(*types.Signature))))
		}
		if unexported {
			methods = append(methods, fmt.Sprintf("type %s imethod <unexported>", name))
		}
		sort.Strings(methods)
		for _, m := range methods {
			r.line("%s", m)
		}
		return
	}
	r.methods(name, t)
}

// fields renders the exported fields of s sorted by name. Embedded fields
// are listed and their promoted fields flattened beneath them (depth-capped).
func (r *renderer) fields(name, path string, s *types.Struct, depth int) {
	type row struct {
		key  string
		text string
		emb  *types.Struct
		sub  string
	}
	var rows []row
	for i := range s.NumFields() {
		f := s.Field(i)
		tag := ""
		if tg := s.Tag(i); tg != "" {
			tag = " " + fmt.Sprintf("%q", tg)
		}
		fpath := path + f.Name()
		switch {
		case f.Embedded():
			var emb *types.Struct
			if st, ok := types.Unalias(derefType(f.Type())).Underlying().(*types.Struct); ok {
				emb = st
			}
			rows = append(rows, row{
				key:  fpath,
				text: fmt.Sprintf("type %s embed %s%s", name, r.typ(f.Type()), tag),
				emb:  emb, sub: fpath + ".",
			})
		case f.Exported():
			rows = append(rows, row{
				key:  fpath,
				text: fmt.Sprintf("type %s field %s %s%s", name, fpath, r.typ(f.Type()), tag),
			})
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].key < rows[j].key })
	for _, rw := range rows {
		r.line("%s", rw.text)
		if rw.emb != nil && depth < 4 {
			r.fields(name, rw.sub, rw.emb, depth+1)
		}
	}
}

func derefType(t types.Type) types.Type {
	if p, ok := t.(*types.Pointer); ok {
		return p.Elem()
	}
	return t
}

// methods renders the exported method set of t and *t (promoted methods
// included), marking the receiver kind.
func (r *renderer) methods(name string, t types.Type) {
	valueSet := types.NewMethodSet(t)
	ptrSet := types.NewMethodSet(types.NewPointer(t))
	var lines []string
	for i := range ptrSet.Len() {
		sel := ptrSet.At(i)
		fn := sel.Obj().(*types.Func)
		if !fn.Exported() {
			continue
		}
		recv := "*" + name
		if valueSet.Lookup(fn.Pkg(), fn.Name()) != nil {
			recv = name
		}
		lines = append(lines, fmt.Sprintf("type %s method (%s) %s%s",
			name, recv, fn.Name(), r.signature(sel.Type().(*types.Signature))))
	}
	sort.Strings(lines)
	for _, l := range lines {
		r.line("%s", l)
	}
}
