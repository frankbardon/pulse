package pathq

import (
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

// pathWords are the identifier words and message nouns that name a
// filesystem path: an argument whose last camelCase / snake_case word is
// one of these, or a %q whose preceding message text ends in one, is a
// path.
var pathWords = map[string]bool{
	"path": true, "paths": true, "dir": true, "dirs": true, "directory": true,
	"file": true, "filename": true, "files": true, "root": true, "folder": true,
	"jail": true, "fpath": true, "abs": true,
}

// quotedPathAllowlist holds the %q / strconv.Quote sites that look like a
// path but are not a filesystem path, keyed "<module-relative file>|<arg
// expression>", each with the reason it may keep %q. A key that no longer
// matches a site fails the gate, so the list cannot go stale.
var quotedPathAllowlist = map[string]string{
	"internal/apigolden/apigolden.go|path":                 "a Go import path (always forward slashes), not a filesystem path",
	"internal/descriptor/interpretation_validate.go|field": "a Response part path (dotted JSON keys), not a filesystem path",
	"internal/returnshape/apply.go|path":                   "a return include path (dotted JSON keys), not a filesystem path",
	"internal/tools/pkgsplit/main.go|path":                 "writes a Go import spec into source code, not a message",
}

// quotedPathSkipDirs are module trees the gate does not walk: generated
// or vendored trees and the nested embedder smoke module (its own module).
var quotedPathSkipDirs = map[string]bool{"testdata": true, "docs": true, "tmp": true, "dist": true, "node_modules": true}

// TestNoQuotedPathVerbs: fmt's %q (and strconv.Quote) escapes every
// backslash, so a Windows path in a message reads doubled. Every message
// builder that formats a filesystem path quotes it with pathq.Quote; a %q
// (or strconv.Quote) whose argument is named like a path, or whose
// preceding message text ends in a path noun ("source file %q"), fails
// here unless quotedPathAllowlist names it with a reason. Test files are
// out of scope.
func TestNoQuotedPathVerbs(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..")
	hits, used := scanQuotedPaths(t, root)
	for _, h := range hits {
		t.Errorf("%s: %%q (or strconv.Quote) of a filesystem path escapes every backslash on Windows; quote it with pathq.Quote(%s) and format it with %%s, or add %q to quotedPathAllowlist with the reason it is not a path", h.pos, h.arg, h.key)
	}
	for k := range quotedPathAllowlist {
		if !used[k] {
			t.Errorf("quotedPathAllowlist entry %q matches no %%q site; remove it", k)
		}
	}
}

type quotedPathHit struct {
	pos, arg, key string
}

func scanQuotedPaths(t *testing.T, root string) ([]quotedPathHit, map[string]bool) {
	t.Helper()
	fset := token.NewFileSet()
	var hits []quotedPathHit
	used := map[string]bool{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel != "." && (strings.HasPrefix(d.Name(), ".") || quotedPathSkipDirs[d.Name()] || rel == "internal/embeddersmoke") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, "_test.go") {
			return nil
		}
		f, perr := parser.ParseFile(fset, p, nil, 0)
		if perr != nil {
			return perr
		}
		ast.Inspect(f, func(n ast.Node) bool {
			c, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			for _, arg := range quotedPathArgs(c) {
				text := exprText(fset, arg)
				key := rel + "|" + text
				if _, ok := quotedPathAllowlist[key]; ok {
					used[key] = true
					continue
				}
				hits = append(hits, quotedPathHit{pos: rel + ":" + strconv.Itoa(fset.Position(arg.Pos()).Line), arg: text, key: key})
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return hits, used
}

// quotedPathArgs returns the arguments of c that a %q verb of its format
// literal (or a strconv.Quote call) formats and that name a path.
func quotedPathArgs(c *ast.CallExpr) []ast.Expr {
	if sel, ok := c.Fun.(*ast.SelectorExpr); ok && len(c.Args) == 1 {
		if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "strconv" && sel.Sel.Name == "Quote" {
			if pathNamed(c.Args[0]) {
				return c.Args
			}
			return nil
		}
	}
	for i, a := range c.Args {
		format, ok := stringLit(a)
		if !ok || !strings.Contains(format, "%") {
			continue
		}
		var out []ast.Expr
		for _, v := range formatVerbs(format) {
			idx := i + 1 + v.arg
			if v.verb != 'q' || idx >= len(c.Args) {
				continue
			}
			if pathNamed(c.Args[idx]) || pathNoun(v.before) {
				out = append(out, c.Args[idx])
			}
		}
		return out
	}
	return nil
}

// stringLit returns the value of a string literal or a + concatenation
// of string literals.
func stringLit(e ast.Expr) (string, bool) {
	switch x := e.(type) {
	case *ast.BasicLit:
		if x.Kind != token.STRING {
			return "", false
		}
		s, err := strconv.Unquote(x.Value)
		return s, err == nil
	case *ast.BinaryExpr:
		if x.Op != token.ADD {
			return "", false
		}
		l, ok := stringLit(x.X)
		if !ok {
			return "", false
		}
		r, ok := stringLit(x.Y)
		return l + r, ok
	case *ast.ParenExpr:
		return stringLit(x.X)
	}
	return "", false
}

type formatVerb struct {
	verb   rune
	arg    int    // 0-based operand index after the format
	before string // the literal text between the previous verb and this one
}

// formatVerbs parses fmt verbs: flags, width / precision (a `*` consumes
// an operand) and an explicit [n] operand index.
func formatVerbs(format string) []formatVerb {
	var out []formatVerb
	next := 0
	lit := strings.Builder{}
	rs := []rune(format)
	for i := 0; i < len(rs); i++ {
		if rs[i] != '%' {
			lit.WriteRune(rs[i])
			continue
		}
		i++
		if i < len(rs) && rs[i] == '%' {
			lit.WriteRune('%')
			continue
		}
		for i < len(rs) {
			r := rs[i]
			switch {
			case strings.ContainsRune("+-# 0.", r) || unicode.IsDigit(r):
				i++
				continue
			case r == '*':
				next++
				i++
				continue
			case r == '[':
				end := strings.IndexRune(string(rs[i:]), ']')
				if end > 0 {
					if n, err := strconv.Atoi(string(rs[i+1 : i+end])); err == nil {
						next = n - 1
					}
					i += end + 1
					continue
				}
			}
			break
		}
		if i >= len(rs) {
			break
		}
		out = append(out, formatVerb{verb: rs[i], arg: next, before: lit.String()})
		next++
		lit.Reset()
	}
	return out
}

// pathNamed reports an operand whose name ends in a path word: an
// identifier or field (`dir`, `opts.SourcePath`), a call of one
// (`x.Path()`), or a path/filepath function call (`filepath.Join(…)`).
func pathNamed(e ast.Expr) bool {
	switch x := e.(type) {
	case *ast.Ident:
		return lastWordIsPath(x.Name)
	case *ast.SelectorExpr:
		return lastWordIsPath(x.Sel.Name)
	case *ast.CallExpr:
		if sel, ok := x.Fun.(*ast.SelectorExpr); ok {
			if pkg, ok := sel.X.(*ast.Ident); ok && (pkg.Name == "filepath" || pkg.Name == "path") {
				return true
			}
		}
		return pathNamed(x.Fun)
	case *ast.ParenExpr:
		return pathNamed(x.X)
	}
	return false
}

// pathNoun reports message text that ends in a path noun ("source file ",
// "dir ", "path=").
func pathNoun(before string) bool {
	s := strings.TrimRightFunc(before, func(r rune) bool { return !unicode.IsLetter(r) })
	j := strings.LastIndexFunc(s, func(r rune) bool { return !unicode.IsLetter(r) })
	return pathWords[strings.ToLower(s[j+1:])]
}

func lastWordIsPath(name string) bool {
	words := splitWords(name)
	return len(words) > 0 && pathWords[strings.ToLower(words[len(words)-1])]
}

// splitWords splits a camelCase / snake_case identifier into words.
func splitWords(name string) []string {
	var words []string
	cur := []rune{}
	rs := []rune(name)
	for i, r := range rs {
		switch {
		case r == '_':
			if len(cur) > 0 {
				words = append(words, string(cur))
			}
			cur = cur[:0]
			continue
		case unicode.IsUpper(r) && len(cur) > 0 && (unicode.IsLower(cur[len(cur)-1]) || (i+1 < len(rs) && unicode.IsLower(rs[i+1]))):
			words = append(words, string(cur))
			cur = []rune{}
		}
		cur = append(cur, r)
	}
	if len(cur) > 0 {
		words = append(words, string(cur))
	}
	return words
}

func exprText(fset *token.FileSet, e ast.Expr) string {
	var b strings.Builder
	_ = printer.Fprint(&b, fset, e)
	return b.String()
}

// TestQuotedPathDetector pins what the gate flags: a %q (or
// strconv.Quote) of an operand named like a path, or after a path noun;
// never a %q of a name, a %s of a path or a %%q literal.
func TestQuotedPathDetector(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		src  string
		want []string
	}{
		{`fmt.Sprintf("reading %q: %v", opts.SourcePath, err)`, []string{"opts.SourcePath"}},
		{`fmt.Errorf("jail %q", m.jailRoot)`, []string{"m.jailRoot"}},
		{`fmt.Sprintf("save path %q %s", p, what)`, []string{"p"}},
		{`errors.New(code, fmt.Sprintf("dir " + "%q", d))`, []string{"d"}},
		{`fmt.Sprintf("%[2]q then %[1]s", name, dir)`, []string{"dir"}},
		{`fmt.Sprintf("%*d %q", w, n, filepath.Join(a, b))`, []string{"filepath.Join(a, b)"}},
		{`strconv.Quote(path)`, []string{"path"}},
		{`fmt.Sprintf("field %q of %s", name, path)`, nil},
		{`fmt.Sprintf("profile %q", profile)`, nil},
		{`fmt.Sprintf("100%%q %q", name)`, nil},
		{`strconv.Quote(name)`, nil},
	} {
		e, err := parser.ParseExpr(tc.src)
		if err != nil {
			t.Fatalf("%s: %v", tc.src, err)
		}
		var got []string
		ast.Inspect(e, func(n ast.Node) bool {
			if c, ok := n.(*ast.CallExpr); ok {
				for _, a := range quotedPathArgs(c) {
					got = append(got, exprText(token.NewFileSet(), a))
				}
			}
			return true
		})
		if strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Errorf("%s: flagged %v, want %v", tc.src, got, tc.want)
		}
	}
}
