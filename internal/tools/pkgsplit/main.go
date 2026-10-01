// Command pkgsplit is the in-module AST rewriter used to split a public
// package in place: the kept subset stays in the public directory, the
// remainder moves to an internal twin that imports the public package.
// It is a development tool only (never imported, never shipped).
//
// Usage — a split is four mechanical passes plus compile-driven fixups:
//
//	# 1. list the exported package-level names a directory declares
//	go run ./internal/tools/pkgsplit decls -dir internal/encoding > /tmp/remainder.txt
//	go run ./internal/tools/pkgsplit decls -dir encoding -all > /tmp/kept.txt
//
//	# 2. qualify the moved files' references to names that stayed public
//	go run ./internal/tools/pkgsplit qualify -dir internal/encoding \
//	    -import github.com/frankbardon/pulse/encoding -names /tmp/kept.txt
//
//	# 3. turn method calls into package-function calls inside the twin
//	go run ./internal/tools/pkgsplit methods -dir internal/encoding \
//	    -methods BuildDecodePlan,ReadRecordAt
//
//	# 4. repoint every other file in the module at the twin
//	go run ./internal/tools/pkgsplit rewrite -root . \
//	    -from github.com/frankbardon/pulse/encoding \
//	    -to github.com/frankbardon/pulse/internal/encoding -alias encx \
//	    -names /tmp/remainder.txt -methods BuildDecodePlan,ReadRecordAt \
//	    -exclude internal/encoding
//
// Declarations that must change sides inside a file that otherwise stays
// put move with `move` (doc comments travel with them; a new -dst file
// gets the -src import block, so run goimports on both afterwards):
//
//	go run ./internal/tools/pkgsplit move -src encoding/header.go \
//	    -dst internal/encoding/preamble.go -names ReadPreamble,Schema.Logical
//
// The passes are purely syntactic (go/parser + go/format; no type
// checker, so the module needs no golang.org/x/tools dependency): a
// selector `<local>.Name` is rewritten only when <local> is the file's
// import name for -from and is not shadowed by a declaration in the
// file; a method call `x.M(args)` becomes `alias.M(x, args)` for every
// -methods name, so those names must be unique to the converted
// receiver types across the module. The compiler is the check — run
// `go build ./... && go vet ./...` after each pass.
package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: pkgsplit decls|move|qualify|methods|rewrite [flags]")
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "decls":
		err = runDecls(os.Args[2:])
	case "qualify":
		err = runQualify(os.Args[2:])
	case "methods":
		err = runMethods(os.Args[2:])
	case "rewrite":
		err = runRewrite(os.Args[2:])
	case "move":
		err = runMove(os.Args[2:])
	default:
		err = fmt.Errorf("unknown subcommand %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "pkgsplit:", err)
		os.Exit(1)
	}
}

// ---------------------------------------------------------------- decls

func runDecls(args []string) error {
	fs := flag.NewFlagSet("decls", flag.ExitOnError)
	dir := fs.String("dir", "", "package directory")
	all := fs.Bool("all", false, "include unexported names")
	tests := fs.Bool("tests", false, "include _test.go files")
	_ = fs.Parse(args)
	names, err := DeclaredNames(*dir, *all, *tests)
	if err != nil {
		return err
	}
	for _, n := range names {
		fmt.Println(n)
	}
	return nil
}

// DeclaredNames returns the sorted package-level names (types, funcs,
// vars, consts — never methods) declared by the .go files in dir.
func DeclaredNames(dir string, unexported, tests bool) ([]string, error) {
	files, err := goFiles(dir, tests)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	fset := token.NewFileSet()
	for _, path := range files {
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return nil, err
		}
		for _, n := range topLevelNames(f) {
			if n == "_" || n == "init" || (!unexported && !ast.IsExported(n)) {
				continue
			}
			seen[n] = true
		}
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out, nil
}

func topLevelNames(f *ast.File) []string {
	var out []string
	for _, d := range f.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			if d.Recv == nil {
				out = append(out, d.Name.Name)
			}
		case *ast.GenDecl:
			for _, s := range d.Specs {
				switch s := s.(type) {
				case *ast.TypeSpec:
					out = append(out, s.Name.Name)
				case *ast.ValueSpec:
					for _, n := range s.Names {
						out = append(out, n.Name)
					}
				}
			}
		}
	}
	return out
}

// ----------------------------------------------------------------- move

func runMove(args []string) error {
	fs := flag.NewFlagSet("move", flag.ExitOnError)
	srcPath := fs.String("src", "", "file the declarations leave")
	dstPath := fs.String("dst", "", "file the declarations are appended to (created when absent)")
	names := fs.String("names", "", "comma-separated names; methods as Recv.Name")
	pkg := fs.String("pkg", "", "package clause for a new -dst (default: -src's)")
	_ = fs.Parse(args)
	src, err := os.ReadFile(*srcPath)
	if err != nil {
		return err
	}
	var dst []byte
	if b, err := os.ReadFile(*dstPath); err == nil {
		dst = b
	}
	newSrc, newDst, err := Move(src, dst, splitSet(*names), *pkg)
	if err != nil {
		return err
	}
	if err := os.WriteFile(*srcPath, newSrc, 0o644); err != nil {
		return err
	}
	return os.WriteFile(*dstPath, newDst, 0o644)
}

// Move cuts the named top-level declarations (with their doc comments)
// out of src and appends them to dst. A nil dst starts a new file in
// package pkg (src's package when empty) carrying src's imports. Every
// name must match exactly one declaration; a GenDecl moves whole, so a
// grouped const/var/type block moves only when every spec is named.
func Move(src, dst []byte, names map[string]bool, pkg string) ([]byte, []byte, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "", src, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return nil, nil, err
	}
	type span struct{ start, end int }
	var cuts []span
	found := map[string]bool{}
	for _, d := range f.Decls {
		var declNames []string
		var doc *ast.CommentGroup
		switch d := d.(type) {
		case *ast.FuncDecl:
			doc = d.Doc
			if d.Recv != nil {
				declNames = []string{recvTypeName(d) + "." + d.Name.Name}
			} else {
				declNames = []string{d.Name.Name}
			}
		case *ast.GenDecl:
			if d.Tok == token.IMPORT {
				continue
			}
			doc = d.Doc
			for _, s := range d.Specs {
				switch s := s.(type) {
				case *ast.TypeSpec:
					declNames = append(declNames, s.Name.Name)
				case *ast.ValueSpec:
					for _, n := range s.Names {
						declNames = append(declNames, n.Name)
					}
				}
			}
		}
		hit := 0
		for _, n := range declNames {
			if names[n] {
				hit++
			}
		}
		if hit == 0 {
			continue
		}
		if hit != len(declNames) {
			return nil, nil, fmt.Errorf("declaration %v is only partly named; split it first", declNames)
		}
		start := d.Pos()
		if doc != nil {
			start = doc.Pos()
		}
		cuts = append(cuts, span{fset.Position(start).Offset, fset.Position(d.End()).Offset})
		for _, n := range declNames {
			found[n] = true
		}
	}
	for n := range names {
		if !found[n] {
			return nil, nil, fmt.Errorf("no declaration named %s", n)
		}
	}
	var moved bytes.Buffer
	var kept bytes.Buffer
	prev := 0
	for _, c := range cuts {
		kept.Write(src[prev:c.start])
		moved.WriteString("\n")
		moved.Write(src[c.start:c.end])
		moved.WriteString("\n")
		prev = c.end
	}
	kept.Write(src[prev:])
	if dst == nil {
		if pkg == "" {
			pkg = f.Name.Name
		}
		var hdr bytes.Buffer
		fmt.Fprintf(&hdr, "package %s\n", pkg)
		for _, d := range f.Decls {
			if g, ok := d.(*ast.GenDecl); ok && g.Tok == token.IMPORT {
				hdr.WriteString("\n")
				hdr.Write(src[fset.Position(g.Pos()).Offset:fset.Position(g.End()).Offset])
				hdr.WriteString("\n")
			}
		}
		dst = hdr.Bytes()
	}
	newDst := append(append([]byte{}, dst...), moved.Bytes()...)
	outSrc, err := format.Source(kept.Bytes())
	if err != nil {
		return nil, nil, fmt.Errorf("reformat src: %w", err)
	}
	outDst, err := format.Source(newDst)
	if err != nil {
		return nil, nil, fmt.Errorf("reformat dst: %w", err)
	}
	return outSrc, outDst, nil
}

func recvTypeName(fd *ast.FuncDecl) string {
	t := fd.Recv.List[0].Type
	if s, ok := t.(*ast.StarExpr); ok {
		t = s.X
	}
	switch x := t.(type) {
	case *ast.IndexExpr:
		t = x.X
	case *ast.IndexListExpr:
		t = x.X
	}
	if id, ok := t.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}

// -------------------------------------------------------------- qualify

func runQualify(args []string) error {
	fs := flag.NewFlagSet("qualify", flag.ExitOnError)
	dir := fs.String("dir", "", "directory of the moved files")
	imp := fs.String("import", "", "import path of the package the names stayed in")
	name := fs.String("name", "", "package name to qualify with (default: last path element)")
	namesFile := fs.String("names", "", "file listing the names that stayed (one per line)")
	_ = fs.Parse(args)
	names, err := readNames(*namesFile)
	if err != nil {
		return err
	}
	qual := *name
	if qual == "" {
		qual = filepath.Base(*imp)
	}
	files, err := goFiles(*dir, true)
	if err != nil {
		return err
	}
	for _, path := range files {
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out, changed, err := Qualify(src, *imp, qual, names)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if changed {
			if err := os.WriteFile(path, out, 0o644); err != nil {
				return err
			}
		}
	}
	return nil
}

// Qualify rewrites every free (file-unresolved) identifier in src whose
// name is in names to qual.Name and adds an import of importPath named
// qual. Composite-literal keys, selectors and declarations are never
// free, so only true package-level references change.
func Qualify(src []byte, importPath, qual string, names map[string]bool) ([]byte, bool, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "", src, parser.ParseComments)
	if err != nil {
		return nil, false, err
	}
	declared := map[string]bool{}
	for _, n := range topLevelNames(f) {
		declared[n] = true
	}
	free := map[*ast.Ident]bool{}
	for _, id := range f.Unresolved {
		if names[id.Name] && !declared[id.Name] {
			free[id] = true
		}
	}
	if len(free) == 0 {
		return src, false, nil
	}
	ast.Inspect(f, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && free[id] {
			id.Name = qual + "." + id.Name
		}
		return true
	})
	var buf bytes.Buffer
	if err := format.Node(&buf, fset, f); err != nil {
		return nil, false, err
	}
	out := buf.Bytes()
	if importName(f, importPath) == "" {
		alias := ""
		if qual != filepath.Base(importPath) {
			alias = qual
		}
		out = addImport(out, alias, importPath)
	}
	res, err := format.Source(out)
	if err != nil {
		return nil, false, fmt.Errorf("reformat: %w", err)
	}
	return res, true, nil
}

// -------------------------------------------------------------- methods

func runMethods(args []string) error {
	fs := flag.NewFlagSet("methods", flag.ExitOnError)
	dir := fs.String("dir", "", "package directory whose own calls are converted")
	methods := fs.String("methods", "", "comma-separated method names converted to functions")
	_ = fs.Parse(args)
	files, err := goFiles(*dir, true)
	if err != nil {
		return err
	}
	opts := Options{Methods: splitSet(*methods)}
	for _, path := range files {
		if err := rewritePath(path, opts); err != nil {
			return err
		}
	}
	return nil
}

// -------------------------------------------------------------- rewrite

func runRewrite(args []string) error {
	fs := flag.NewFlagSet("rewrite", flag.ExitOnError)
	root := fs.String("root", ".", "module root to walk")
	from := fs.String("from", "", "import path the names moved out of")
	to := fs.String("to", "", "import path the names moved into")
	alias := fs.String("alias", "", "import name for -to")
	namesFile := fs.String("names", "", "file listing the moved names (one per line)")
	methods := fs.String("methods", "", "comma-separated method names converted to -to functions")
	exclude := fs.String("exclude", "", "comma-separated root-relative directories to skip")
	_ = fs.Parse(args)
	names, err := readNames(*namesFile)
	if err != nil {
		return err
	}
	opts := Options{From: *from, To: *to, Alias: *alias, Names: names, Methods: splitSet(*methods)}
	skip := map[string]bool{}
	for d := range splitSet(*exclude) {
		skip[filepath.Clean(filepath.Join(*root, d))] = true
	}
	return filepath.WalkDir(*root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			base := d.Name()
			if skip[filepath.Clean(path)] || (path != *root && (strings.HasPrefix(base, ".") || base == "testdata" || base == "vendor")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		return rewritePath(path, opts)
	})
}

// Options drives Rewrite.
type Options struct {
	From, To, Alias string          // import paths and the import name for To
	Names           map[string]bool // selectors <from>.Name rewritten to <alias>.Name
	Methods         map[string]bool // x.M(args) rewritten to <alias>.M(x, args) (M(x, args) when Alias is ""); a declaration of method M becomes a function taking the receiver first
}

func rewritePath(path string, opts Options) error {
	src, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	out, changed, err := Rewrite(src, opts)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if changed {
		return os.WriteFile(path, out, 0o644)
	}
	return nil
}

// Rewrite applies opts to one file's source and fixes its imports.
func Rewrite(src []byte, opts Options) ([]byte, bool, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "", src, parser.ParseComments)
	if err != nil {
		return nil, false, err
	}
	fromName := ""
	if opts.From != "" {
		fromName = importName(f, opts.From)
	}
	imports := map[string]bool{}
	for _, is := range f.Imports {
		imports[localName(is)] = true
	}
	changed := false
	for _, d := range f.Decls {
		// A converted method's own declaration: the receiver becomes the
		// first parameter.
		if fd, ok := d.(*ast.FuncDecl); ok && fd.Recv != nil && opts.Methods[fd.Name.Name] {
			fd.Type.Params.List = append([]*ast.Field{fd.Recv.List[0]}, fd.Type.Params.List...)
			fd.Recv = nil
			changed = true
		}
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.SelectorExpr:
			if fromName == "" {
				return true
			}
			if x, ok := n.X.(*ast.Ident); ok && x.Name == fromName && x.Obj == nil && opts.Names[n.Sel.Name] {
				x.Name = opts.Alias
				changed = true
			}
		case *ast.CallExpr:
			sel, ok := n.Fun.(*ast.SelectorExpr)
			if !ok || !opts.Methods[sel.Sel.Name] {
				return true
			}
			if x, ok := sel.X.(*ast.Ident); ok && x.Obj == nil && imports[x.Name] {
				return true // already a package-qualified call
			}
			recv := sel.X
			if opts.Alias == "" {
				n.Fun = &ast.Ident{NamePos: recv.Pos(), Name: sel.Sel.Name}
			} else {
				n.Fun = &ast.SelectorExpr{
					X:   &ast.Ident{NamePos: recv.Pos(), Name: opts.Alias},
					Sel: &ast.Ident{NamePos: recv.Pos(), Name: sel.Sel.Name},
				}
			}
			n.Args = append([]ast.Expr{recv}, n.Args...)
			changed = true
		}
		return true
	})
	if !changed {
		return src, false, nil
	}
	var buf bytes.Buffer
	if err := format.Node(&buf, fset, f); err != nil {
		return nil, false, err
	}
	out := buf.Bytes()
	if opts.Alias != "" && opts.To != "" && importName(f, opts.To) == "" {
		out = addImport(out, opts.Alias, opts.To)
	}
	if fromName != "" && !stillUsed(f, fromName) {
		out = removeImport(out, opts.From)
	}
	res, err := format.Source(out)
	if err != nil {
		return nil, false, fmt.Errorf("reformat: %w\n%s", err, out)
	}
	return res, true, nil
}

// ---------------------------------------------------------------- helpers

func stillUsed(f *ast.File, name string) bool {
	used := false
	ast.Inspect(f, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok {
			if x, ok := sel.X.(*ast.Ident); ok && x.Name == name && x.Obj == nil {
				used = true
			}
		}
		return !used
	})
	return used
}

func localName(is *ast.ImportSpec) string {
	if is.Name != nil {
		return is.Name.Name
	}
	p, _ := strconv.Unquote(is.Path.Value)
	return filepath.Base(p)
}

func importName(f *ast.File, path string) string {
	for _, is := range f.Imports {
		if p, _ := strconv.Unquote(is.Path.Value); p == path {
			return localName(is)
		}
	}
	return ""
}

// addImport inserts an import of path (named alias when non-empty) into
// the file's first import declaration, in a group of its own when that
// declaration so far holds only standard-library paths.
func addImport(src []byte, alias, path string) []byte {
	spec := strconv.Quote(path)
	if alias != "" {
		spec = alias + " " + spec
	}
	lines := strings.Split(string(src), "\n")
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if t == "import (" {
			end := i + 1
			for end < len(lines) && strings.TrimSpace(lines[end]) != ")" {
				end++
			}
			ins := []string{"\t" + spec}
			if !strings.Contains(lines[end-1], ".") || strings.TrimSpace(lines[end-1]) == "" {
				ins = []string{"", "\t" + spec}
			}
			return []byte(strings.Join(splice(lines, end, ins), "\n"))
		}
		if strings.HasPrefix(t, "import ") {
			lines[i] = "import (\n\t" + strings.TrimPrefix(t, "import ") + "\n\n\t" + spec + "\n)"
			return []byte(strings.Join(lines, "\n"))
		}
	}
	for i, l := range lines {
		if strings.HasPrefix(l, "package ") {
			return []byte(strings.Join(splice(lines, i+1, []string{"", "import " + spec}), "\n"))
		}
	}
	return src
}

func removeImport(src []byte, path string) []byte {
	q := strconv.Quote(path)
	var out []string
	sc := bufio.NewScanner(bytes.NewReader(src))
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		l := sc.Text()
		t := strings.TrimSpace(l)
		if fs := strings.Fields(t); len(fs) > 0 && len(fs) <= 3 && fs[len(fs)-1] == q &&
			(len(fs) < 3 || fs[0] == "import") {
			continue
		}
		out = append(out, l)
	}
	return []byte(strings.Join(out, "\n") + "\n")
}

func splice(lines []string, at int, ins []string) []string {
	out := make([]string, 0, len(lines)+len(ins))
	out = append(out, lines[:at]...)
	out = append(out, ins...)
	return append(out, lines[at:]...)
}

func goFiles(dir string, tests bool) ([]string, error) {
	m, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		return nil, err
	}
	var out []string
	for _, p := range m {
		if !tests && strings.HasSuffix(p, "_test.go") {
			continue
		}
		out = append(out, p)
	}
	return out, nil
}

func readNames(path string) (map[string]bool, error) {
	if path == "" {
		return map[string]bool{}, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, l := range strings.Split(string(b), "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
			out[l] = true
		}
	}
	return out, nil
}

func splitSet(s string) map[string]bool {
	out := map[string]bool{}
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out[p] = true
		}
	}
	return out
}
