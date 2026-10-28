package exprreads

import (
	"go/parser"
	"go/token"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
)

// TestRead holds the read set to the engine's walk, one row per scoping
// rule: what is a var, what is a parameter read, what is a call.
func TestRead(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, src           string
		vars, params, funcs []string
		usesEnv             bool
	}{
		{name: "plain columns", src: "income / household_size", vars: []string{"income", "household_size"}},
		{name: "first-use order, no repeats", src: "b + a + b", vars: []string{"b", "a"}},
		{name: "string literal is not a read", src: `region == "north" and name != "x"`, vars: []string{"region", "name"}},
		{name: "keywords are not reads", src: "true and not false or x in [1, 2] and nil == q", vars: []string{"x", "q"}},
		{name: "let binding", src: "let y = a + 1; y * b", vars: []string{"a", "b"}},
		{name: "closure pointer", src: "map(xs, # * k)", vars: []string{"xs", "k"}},
		{name: "closure member pointer", src: "filter(xs, .age > lim)", vars: []string{"xs", "lim"}},
		{name: "closure predicate block", src: "all(xs, {# > 0})", vars: []string{"xs"}},
		{name: "member chain reads its root", src: `a.b.c + d["e"]`, vars: []string{"a", "d"}},
		{name: "$env members and dynamic index", src: `$env.x + $env["y"] + $env[z]`, vars: []string{"x", "y", "z"}, usesEnv: true},
		{name: "params member", src: "params.wave == wave", vars: []string{"wave"}, params: []string{"wave"}},
		{name: "params literal index", src: `params["k"] > 0`, params: []string{"k"}},
		{name: "params nested member reads the first name", src: "params.a.b", params: []string{"a"}},
		{name: "params dynamic index is a column read", src: "params[k]", vars: []string{"params", "k"}},
		{name: "bare params is a column read", src: "params", vars: []string{"params"}},
		{name: "bare params beside a member read", src: "params.a + len(params)", vars: []string{"params"}, params: []string{"a"}},
		{name: "let params shadows the namespace", src: "let params = 1; params.x"},
		{name: "calls inner first, built-ins are not calls", src: "f(g(a), b) + len(c) + abs(d)", vars: []string{"a", "b", "c", "d"}, funcs: []string{"g", "f"}},
		{name: "callee is not a var", src: "lookup(\"t\", id)", vars: []string{"id"}, funcs: []string{"lookup"}},
		{name: "method call on a value", src: "date(x).Year()", vars: []string{"x"}},
		{name: "ternary", src: "x ? y : z", vars: []string{"x", "y", "z"}},
		{name: "constant", src: "1 + 2"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got, err := Read(c.src)
			if err != nil {
				t.Fatalf("Read(%q): %v", c.src, err)
			}
			want := descriptor.ExprReads{Vars: nonNil(c.vars), Params: nonNil(c.params), Funcs: nonNil(c.funcs), UsesEnv: c.usesEnv}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("Read(%q) = %+v, want %+v", c.src, got, want)
			}
		})
	}
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// TestRead_ParseError: a source that does not parse is the compile's
// code with the reason and source in details, an empty (non-nil) read
// set, and no panic.
func TestRead_ParseError(t *testing.T) {
	t.Parallel()
	// An unrendered {{token}} and an arrow lambda (not expr-lang
	// syntax) are parse errors like any other.
	for _, src := range []string{"a +", "map(xs, x => x + y)", "{{wave}} == 1", "(", "", "   "} {
		got, err := Read(src)
		if err == nil {
			t.Fatalf("Read(%q): want a parse error, got %+v", src, got)
		}
		if err.Code != errors.PULSE_FORMULA_COMPILE_ERROR {
			t.Errorf("Read(%q) code = %s", src, err.Code)
		}
		if err.Details["expression"] != src || err.Details["compile_error"] == "" {
			t.Errorf("Read(%q) details = %v", src, err.Details)
		}
		if got.Vars == nil || got.Params == nil || got.Funcs == nil || len(got.Vars)+len(got.Params)+len(got.Funcs) != 0 {
			t.Errorf("Read(%q) = %+v, want an empty non-nil read set", src, got)
		}
	}
}

// TestExprReads_ImportBoundary keeps the package a no-execute leaf: the
// root facade and internal/descriptor's request read map both import
// it, so it may reach neither the engine nor the service tree.
func TestExprReads_ImportBoundary(t *testing.T) {
	t.Parallel()
	const module = "github.com/frankbardon/pulse"
	allowed := map[string]bool{
		module + "/descriptor":         true,
		module + "/errors":             true,
		module + "/internal/exprenv":   true,
		module + "/internal/exprident": true,
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range f.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			if strings.HasPrefix(path, module) && !allowed[path] {
				t.Errorf("%s imports %q; internal/exprreads may import only descriptor, errors, internal/exprenv and internal/exprident", name, path)
			}
		}
	}
}
