//go:build heavy

package exprident_test

import (
	"sort"
	"strings"
	"testing"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/builtin"

	"github.com/frankbardon/pulse/internal/exprenv"
	"github.com/frankbardon/pulse/internal/exprident"
)

// TestNullableOperands_FiresIffNullRowAbortsFullCorpus is the fast
// pin over the WHOLE generated corpus.
func TestNullableOperands_FiresIffNullRowAbortsFullCorpus(t *testing.T) {
	t.Parallel()

	pinCorpusVerdicts(t, 1, 2000, 700, 1000)
}

// TestNilRejectingFunctions_MatchRunning re-derives the nil-rejecting
// table by running: every expr-lang built-in and every Pulse expression
// function, at arity 1..3, with x in each argument position and every
// combination of filler arguments for the rest. Where the typed call
// compiles and runs on a present x, the untyped null-row program runs
// it with x absent (nil); an error must be exactly
// NilRejectingArgument(name, arity, pos), every position must answer
// one way, and every table entry must be observed (lookup's keys need a
// registered table — the integration parity test pins them).
func TestNilRejectingFunctions_MatchRunning(t *testing.T) {
	t.Parallel()

	names := map[string]bool{}
	for _, n := range builtin.Names {
		names[n] = true
	}
	for _, n := range exprenv.Names() {
		names[n] = true
	}
	for _, n := range exprenv.SetHelperNames() {
		names[n] = true
	}
	for _, n := range exprenv.MathNames() {
		names[n] = true
	}
	var sorted []string
	for n := range names {
		sorted = append(sorted, n)
	}
	sort.Strings(sorted)

	fillers := []string{"2", "z", `"a"`, `["a", "b"]`, `"month"`, "# > 1", `# == "a"`, "#", "[1, 2]", "true", `"x"`, `"l2"`}
	values := []any{3.5, "b", "1", "2020-01-02", "1h", "MQ==", []string{"a", "b"}, []float64{1, 2}}
	type key struct {
		name       string
		arity, pos int
	}
	seen := map[key]map[bool]string{}
	for _, name := range sorted {
		for arity := 1; arity <= 3; arity++ {
			for pos := 0; pos < arity; pos++ {
				for _, args := range fillCombos(arity, pos, fillers) {
					src := name + "(" + strings.Join(args, ", ") + ")"
					for _, v := range values {
						decl := map[string]any{"x": v, "z": 0.0}
						typed, err := pinCompile(src, true, decl)
						if err != nil {
							continue
						}
						if _, err := expr.Run(typed, map[string]any{"x": v, "z": 2.0}); err != nil {
							continue
						}
						untyped, err := pinCompile(src, false, decl)
						if err != nil {
							t.Fatalf("%s compiles typed but not untyped: %v", src, err)
						}
						_, nilErr := expr.Run(untyped, map[string]any{"z": 2.0})
						k := key{name, arity, pos}
						if seen[k] == nil {
							seen[k] = map[bool]string{}
						}
						seen[k][nilErr != nil] = src
					}
				}
			}
		}
	}
	observed := map[string]bool{}
	for k, outcomes := range seen {
		if len(outcomes) > 1 {
			t.Errorf("%s arity %d position %d answers both ways: rejects %q, takes %q", k.name, k.arity, k.pos, outcomes[true], outcomes[false])
			continue
		}
		for rejects, src := range outcomes {
			if rejects {
				observed[k.name] = true
			}
			if want := exprident.NilRejectingArgument(k.name, k.arity, k.pos); want != rejects {
				t.Errorf("%s with a nil x: rejects=%v, table says %v", src, rejects, want)
			}
		}
	}
	for _, n := range exprident.NilRejectingFunctions() {
		if !observed[n] && n != "lookup" {
			t.Errorf("table entry %s was never observed rejecting nil", n)
		}
	}
}

// fillCombos is every argument list of arity with "x" at pos and a
// filler at every other position.
func fillCombos(arity, pos int, fillers []string) [][]string {
	var out [][]string
	var rec func(i int, cur []string)
	rec = func(i int, cur []string) {
		if i == arity {
			out = append(out, append([]string(nil), cur...))
			return
		}
		if i == pos {
			rec(i+1, append(cur, "x"))
			return
		}
		for _, f := range fillers {
			rec(i+1, append(cur, f))
		}
	}
	rec(0, nil)
	return out
}
