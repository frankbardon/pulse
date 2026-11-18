package exprident_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/expr-lang/expr"
	exprtypes "github.com/expr-lang/expr/types"
	"github.com/expr-lang/expr/vm"

	"github.com/frankbardon/pulse/internal/exprenv"
	"github.com/frankbardon/pulse/internal/exprident"
	"github.com/frankbardon/pulse/internal/totalcall"
)

// The emulated runtime (internal/processing/expr_program.go): a row
// whose fields are all present runs the program compiled against the
// typed prototype; a row where one is null (absent from the env) runs
// the untyped twin, every name declared `any`. Both carry the shared
// expression options (exprenv.Options: the Pulse functions and
// patchers).

// pinTyped is the prototype the corpus compiles against: x, y nullable
// numbers, z a non-null number, c a nullable categorical label, s a
// nullable set's labels.
var pinTyped = map[string]any{"x": 0.0, "y": 0.0, "z": 0.0, "c": "", "s": []string{}}

func pinNullable(name string) bool { return name != "z" }

func pinCompile(src string, typed bool, decl map[string]any) (*vm.Program, error) {
	m := exprtypes.Map{}
	for k, v := range decl {
		if typed {
			m[k] = exprtypes.TypeOf(v)
		} else {
			m[k] = exprtypes.Any
		}
	}
	return expr.Compile(src, append([]expr.Option{expr.Env(m)}, exprenv.Options()...)...)
}

// pinRows is every combination of null / present values the corpus
// runs over (x, y ∈ {nil, 1.5, 5.5}, z ∈ {1.5, 5.5}, c ∈ {nil,
// north}, s ∈ {nil, [t1 t2]}), so every guard branch and every
// short-circuit is taken on some null row.
func pinRows() []map[string]any {
	var rows []map[string]any
	nums := []any{nil, 1.5, 5.5}
	cats := []any{nil, "north"}
	sets := []any{nil, []string{"t1", "t2"}}
	for _, x := range nums {
		for _, y := range nums {
			for _, z := range []any{1.5, 5.5} {
				for _, c := range cats {
					for _, s := range sets {
						row := map[string]any{}
						for k, v := range map[string]any{"x": x, "y": y, "z": z, "c": c, "s": s} {
							if v != nil {
								row[k] = v
							}
						}
						rows = append(rows, row)
					}
				}
			}
		}
	}
	return rows
}

// pinCorpus generates the expressions: every operation over the
// nullable and non-null atoms, each bare and under every guard shape.
func pinCorpus() []string {
	var ops []string
	nums := []string{"x", "y", "z", "2"}
	for _, a := range nums {
		for _, b := range nums {
			if a == b && a == "2" {
				continue
			}
			for _, op := range []string{"+", "-", "*", "/", "%", "**", "<", ">=", "==", "!=", "??"} {
				ops = append(ops, a+" "+op+" "+b)
			}
			ops = append(ops, "max("+a+", "+b+")", "("+a+" ?? "+b+") * 2")
		}
		ops = append(ops, "-"+a, "+"+a, "abs("+a+")", "max("+a+")", "sin("+a+")", "round("+a+")",
			"string("+a+")", "hill("+a+", 2, 3)", a+" in [1.5, 2]", "log1p("+a+")")
	}
	ops = append(ops,
		`c + "!"`, `c < "m"`, `c == "north"`, `c != "north"`, `c matches "^n"`, `c contains "o"`,
		`len(c)`, `upper(c)`, `c in ["north"]`, `c ?? "none"`, `string(c)`,
		`"t1" in s`, `len(s)`, `s[0]`, `s[1:]`, `s?.[0]`, `first(s)`, `has_any(s, ["t1"])`,
		`has_any(["t1"], s)`, `popcount(s)`, `map(s, # + "!")`, `count(s, # == "t1")`,
		`map(["a", "b"], x * 2)`, `sum([1, 2], x)`, `sortBy(["a", "b"], x)`, `groupBy(["a"], x)`,
		`let v = x; v * 2`, `let v = x; v != nil ? v * 2 : 0`, `$env.x + 1`, `$env["y"] > 1`,
		`x * 2; z`, `z; x * 2`,
	)
	guards := []string{
		"%s",
		"x != nil ? (%s) : nil",
		"x == nil ? nil : (%s)",
		"!(x == nil) ? (%s) : nil",
		"x != nil && y != nil ? (%s) : nil",
		"x == nil || y == nil ? nil : (%s)",
		"c != nil ? (%s) : nil",
		"s == nil ? nil : (%s)",
		"(%s) ?? 0",
		"z > 2 ? (%s) : nil",
		"x != nil && (%s)",
		"x == nil || (%s)",
		"y > 1 && (%s)",
		"(%s) && x != nil",
	}
	var out []string
	for _, op := range ops {
		for _, g := range guards {
			out = append(out, strings.ReplaceAll(g, "%s", op))
		}
	}
	return out
}

// pinAborts runs src over rows the way the runtime does. ok=false when
// the corpus entry does not fit (the typed compile fails, or a row with
// every field present errors — no null involved); aborts reports an
// evaluation error on some row with a null field.
func pinAborts(src string, rows []map[string]any) (aborts, ok bool) {
	typed, err := pinCompile(src, true, pinTyped)
	if err != nil {
		return false, false
	}
	untyped, err := pinCompile(src, false, pinTyped)
	if err != nil {
		return false, false
	}
	for _, row := range rows {
		prog := typed
		if len(row) < len(pinTyped) {
			prog = untyped
		}
		if _, err := expr.Run(prog, row); err != nil {
			if prog == typed {
				return false, false
			}
			aborts = true
		}
	}
	return aborts, true
}

// TestNullableOperands_FiresIffNullRowAborts is the pin: over a
// generated corpus of expressions × null rows, NullableOperands
// reports a read exactly when the runtime's null-row program raises an
// evaluation error on some row — the advisory fires ⇔ the run aborts.
// The fast tier judges every third corpus entry (every operation keeps
// most of its guard shapes); TestNullableOperands_FiresIffNullRowAbortsFullCorpus
// (heavy) judges them all.
func TestNullableOperands_FiresIffNullRowAborts(t *testing.T) {
	t.Parallel()

	pinCorpusVerdicts(t, 3, 700, 250, 350)
}

// pinCorpusVerdicts judges every stride-th corpus entry and requires at
// least minJudged judged, minFired aborting and minSilent running
// clean, so the pin is never vacuous.
func pinCorpusVerdicts(t *testing.T, stride, minJudged, minFired, minSilent int) {
	t.Helper()
	rows := pinRows()
	judged, fired := 0, 0
	for i, src := range pinCorpus() {
		if i%stride != 0 {
			continue
		}
		aborts, ok := pinAborts(src, rows)
		if !ok {
			continue
		}
		reads, parsed := exprident.NullableOperands(src, pinNullable)
		if !parsed {
			t.Fatalf("%q compiled but did not parse", src)
		}
		judged++
		if aborts {
			fired++
		}
		if (len(reads) > 0) != aborts {
			t.Errorf("%q: reads %v, run aborts on a null row = %v", src, reads, aborts)
		}
	}
	if judged < minJudged || fired < minFired || judged-fired < minSilent {
		t.Fatalf("corpus judged %d expressions, %d aborting: too few to pin", judged, fired)
	}
}

// TestNilRejectingFunctions_Pinned runs one call per table rule (and per
// nil-taking neighbour) with a nil argument through the untyped program:
// an error exactly where NilRejectingArgument says so. The full
// re-derivation over every built-in is TestNilRejectingFunctions_MatchRunning
// (heavy).
func TestNilRejectingFunctions_Pinned(t *testing.T) {
	t.Parallel()

	cases := []struct {
		src        string
		name       string
		arity, pos int
	}{
		{`abs(x)`, "abs", 1, 0},
		{`pow(x, 2)`, "pow", 2, 0},
		{`max(x)`, "max", 1, 0},
		{`max(2, x)`, "max", 2, 1},
		{`len(c)`, "len", 1, 0},
		{`trim(c)`, "trim", 1, 0},
		{`has_any(s, ["a"])`, "has_any", 2, 0},
		{`has_any(["a"], s)`, "has_any", 2, 1},
		{`map(s, #)`, "map", 2, 0},
		{`map(["a", "b"], x)`, "map", 2, 1},
		{`sortBy(["a", "b"], x)`, "sortBy", 2, 1},
		{`sum([1, 2], x)`, "sum", 2, 1},
		{`sin(x)`, "sin", 1, 0},
		{`hill(x, 2, 3)`, "hill", 3, 0},
		{`string(x)`, "string", 1, 0},
		{`first(s)`, "first", 1, 0},
		{`set_union(s, ["a"])`, "set_union", 2, 0},
		{`weekday(x)`, "weekday", 1, 0},
	}
	for _, c := range cases {
		prog, err := pinCompile(c.src, false, pinTyped)
		if err != nil {
			t.Fatalf("%s: %v", c.src, err)
		}
		_, runErr := expr.Run(prog, map[string]any{"z": 1.5})
		if want := exprident.NilRejectingArgument(c.name, c.arity, c.pos); (runErr != nil) != want {
			t.Errorf("%s with a nil argument: error %v, table says rejects=%v", c.src, runErr, want)
		}
	}
}

// TestNullableOperands_TotalName: the walker's total() name is
// internal/totalcall's.
func TestNullableOperands_TotalName(t *testing.T) {
	t.Parallel()

	if reads, _ := exprident.NullableOperands(totalcall.Func+"(x * 2)", pinNullable); len(reads) != 0 {
		t.Fatalf("%s() argument judged: %v", totalcall.Func, reads)
	}
	if !slices.Contains(exprident.NilRejectingFunctions(), "abs") {
		t.Fatal("NilRejectingFunctions lacks abs")
	}
}
