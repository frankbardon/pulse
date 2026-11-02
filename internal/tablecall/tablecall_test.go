package tablecall

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/expr-lang/expr"

	perr "github.com/frankbardon/pulse/errors"
)

// fakeCatalog: lookup tables "grid" (axes lo/hi × an unknown axis) and
// "free" (no axes); range tables "num" (numeric) and "day" (date).
type fakeCatalog struct{ lookup bool }

func (f fakeCatalog) HasLookup() bool { return f.lookup }

func (f fakeCatalog) LookupAxes(name string) ([][]string, bool) {
	switch name {
	case "grid":
		return [][]string{{"lo", "hi"}, nil}, true
	case "free":
		return nil, true
	}
	return nil, false
}

func (f fakeCatalog) RangeKind(name string) (string, bool) {
	switch name {
	case "num":
		return "numeric", true
	case "day":
		return "date", true
	}
	return "", false
}

// TestRefusal_Cases: each literal fault, and what is left to
// evaluation (a computed argument, an axis whose values are unknown, a
// table without axes, lookup() not the built-in).
func TestRefusal_Cases(t *testing.T) {
	t.Parallel()

	cat := fakeCatalog{lookup: true}
	for _, c := range []struct {
		src  string
		cat  Catalog
		code perr.Code
		got  string // KIND_MISMATCH details.got
	}{
		{`lookup("nope", "lo", "x")`, cat, perr.PULSE_LOOKUP_TABLE_UNKNOWN, ""},
		{`lookup("nope", "lo", "x")`, fakeCatalog{}, "", ""},
		{`lookup("grid", "zz", "x")`, cat, perr.PULSE_LOOKUP_MISS, ""},
		{`lookup("grid", "lo", "anything")`, cat, "", ""},
		{`lookup("grid", "lo")`, cat, perr.PULSE_LOOKUP_MISS, ""},
		{`lookup("grid", "lo|x")`, cat, "", ""},
		{`lookup("grid", true, nil)`, cat, perr.PULSE_LOOKUP_MISS, ""},
		{`lookup("grid", v, "zz")`, cat, "", ""},
		{`lookup("free", "q")`, cat, "", ""},
		{`lookup(v, "q")`, cat, "", ""},
		{`lookup()`, cat, "", ""},
		{`range_label("nope", v)`, cat, perr.PULSE_RANGE_TABLE_UNKNOWN, ""},
		{`range_label("nope", v)`, nil, "", ""},
		{`range_label(s, v)`, cat, "", ""},
		{`range_label("num", "x")`, cat, perr.PULSE_RANGE_TABLE_KIND_MISMATCH, "string"},
		{`range_label("day", false)`, cat, perr.PULSE_RANGE_TABLE_KIND_MISMATCH, "bool"},
		{`range_label("num", s)`, cat, perr.PULSE_RANGE_TABLE_KIND_MISMATCH, "string"},
		{`range_label("num", xs)`, cat, perr.PULSE_RANGE_TABLE_KIND_MISMATCH, "list"},
		{`range_label("num", is)`, cat, perr.PULSE_RANGE_TABLE_KIND_MISMATCH, "[]int"},
		{`range_label("num", v)`, cat, "", ""},
		{`range_label("num", lookup("grid", "zz", 1))`, cat, perr.PULSE_LOOKUP_MISS, ""},
	} {
		env := map[string]any{"v": 1.0, "s": "", "xs": []string{}, "is": []int{}}
		prog, err := expr.Compile(c.src, expr.Env(env),
			expr.Function("lookup", func(...any) (any, error) { return nil, nil }),
			expr.Function("range_label", func(...any) (any, error) { return nil, nil }))
		if err != nil {
			t.Fatalf("%s: %v", c.src, err)
		}
		ce := Refusal(prog.Node(), c.cat)
		switch {
		case c.code == "" && ce != nil:
			t.Errorf("%s: %v, want none", c.src, ce)
		case c.code != "" && (ce == nil || ce.Code != c.code):
			t.Errorf("%s: %v, want %s", c.src, ce, c.code)
		case c.got != "" && ce.Details["got"] != c.got:
			t.Errorf("%s: got %v, want %s", c.src, ce.Details["got"], c.got)
		}
	}
}

// TestSourceRefusal_Unparsed: a source that does not parse, or never
// names a reader, is no refusal.
func TestSourceRefusal_Unparsed(t *testing.T) {
	t.Parallel()

	cat := fakeCatalog{lookup: true}
	for _, src := range []string{`lookup("nope"`, `1 + 2`} {
		if ce := SourceRefusal(src, cat); ce != nil {
			t.Errorf("%s: %v", src, ce)
		}
	}
	if ce := SourceRefusal(`lookup("nope", "a")`, cat); ce == nil || ce.Code != perr.PULSE_LOOKUP_TABLE_UNKNOWN {
		t.Errorf("parsed unknown table: %v", ce)
	}
}

// TestTablecallImportBoundary keeps the package a no-execute leaf, so
// predict (internal/descriptor), the overlay gate (internal/
// formulaident) and the optimize rules (internal/optblock) may import
// it: stdlib, the expr-lang parser / ast, errors and the expression /
// range leaves only — never the engine or the descriptor packages.
func TestTablecallImportBoundary(t *testing.T) {
	t.Parallel()

	allowed := map[string]bool{
		"fmt": true, "reflect": true, "strconv": true, "strings": true,
		"github.com/expr-lang/expr/ast": true, "github.com/expr-lang/expr/parser": true,
		"github.com/frankbardon/pulse/errors":             true,
		"github.com/frankbardon/pulse/internal/daterange": true,
		"github.com/frankbardon/pulse/internal/exprenv":   true,
	}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		af, err := parser.ParseFile(fset, f, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range af.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			if !allowed[path] {
				t.Errorf("%s imports %s; tablecall must stay a no-execute leaf", f, path)
			}
		}
	}
}
