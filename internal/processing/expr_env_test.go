package processing

import (
	"errors"
	"strings"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/exprenv"
)

// Direct unit coverage for lookupBuiltin (the reflective trampoline's
// moved with it to internal/exprenv, extension_test.go). The exp_env
// paths flow through the runtime processor in extensions_expr_test.go,
// but the builtin has branches that are easier to exercise here.

func TestLookupBuiltin_NoArgsError(t *testing.T) {
	r := &ExtensionRegistry{
		LookupTables: map[string]LookupTable{"t": {Rows: map[string]float64{"x": 1}}},
	}
	_, err := r.lookupBuiltin()
	if err == nil {
		t.Fatal("expected error on 0-arg lookup call")
	}
	if !strings.Contains(err.Error(), "table name") {
		t.Errorf("err = %v; expected 'table name' in message", err)
	}
}

func TestLookupBuiltin_NonStringTableError(t *testing.T) {
	r := &ExtensionRegistry{
		LookupTables: map[string]LookupTable{"t": {Rows: map[string]float64{"x": 1}}},
	}
	_, err := r.lookupBuiltin(42, "k")
	if err == nil {
		t.Fatal("expected error on non-string table arg")
	}
}

func TestLookupBuiltin_UnknownTable(t *testing.T) {
	r := &ExtensionRegistry{
		LookupTables: map[string]LookupTable{"t": {Rows: map[string]float64{"x": 1}}},
	}
	_, err := r.lookupBuiltin("notthere", "k")
	if !perr.HasCode(err, perr.PULSE_LOOKUP_TABLE_UNKNOWN) {
		t.Errorf("err = %v; expected PULSE_LOOKUP_TABLE_UNKNOWN", err)
	}
}

func TestLookupBuiltin_Miss(t *testing.T) {
	r := &ExtensionRegistry{
		LookupTables: map[string]LookupTable{"t": {Rows: map[string]float64{"x": 1}}},
	}
	_, err := r.lookupBuiltin("t", "y")
	if !perr.HasCode(err, perr.PULSE_LOOKUP_MISS) {
		t.Errorf("err = %v; expected PULSE_LOOKUP_MISS", err)
	}
}

func TestLookupBuiltin_Hit(t *testing.T) {
	r := &ExtensionRegistry{
		LookupTables: map[string]LookupTable{"t": {Rows: map[string]float64{"x": 9}}},
	}
	v, err := r.lookupBuiltin("t", "x")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if v != 9.0 {
		t.Errorf("got %v, want 9.0", v)
	}
}

func TestLookupBuiltin_FuncBackedError(t *testing.T) {
	sentinel := errors.New("table broke")
	r := &ExtensionRegistry{
		LookupTables: map[string]LookupTable{
			"f": {Lookup: func(keys ...string) (float64, bool, error) {
				return 0, false, sentinel
			}},
		},
	}
	_, err := r.lookupBuiltin("f", "x")
	if !perr.HasCode(err, perr.PULSE_LOOKUP_MISS) {
		t.Errorf("err = %v; expected PULSE_LOOKUP_MISS wrap", err)
	}
}

// setExprHelperCount is the number of always-on options contributed by
// ExprOptions: the eight set helpers (has_any / has_all / has_none /
// popcount / set_union / set_intersect / set_diff / set_xor) plus the
// `%` float-modulo builtin and its patcher (internal/exprenv mod.go), plus the
// `**` / `^` power builtins and their patcher (pow.go — 3), plus one per
// reserved vector / scalar-maths built-in (internal/exprenv), plus the
// exact decimal operators (internal/exprenv decimal.go: ten binary
// operator builtins, float / lift / lift_dyn / negate / the `%`
// refusal / `in`, and their patcher — 17) and the twelve decimal
// built-in overrides (decimal_builtins.go: abs ceil floor round int
// max min mean median sum sort sortBy), plus the exact u64 comparisons
// (internal/exprenv u64.go: the float64 echo, six comparisons, `in`,
// the wrap and compare patchers and the retype pass — 11). Bump this
// constant when the set-helper roster changes.
var setExprHelperCount = 8 + 2 + 3 + len(exprenv.Names()) + 17 + 12 + 11

func TestExprOptions_NilRegistry(t *testing.T) {
	var r *ExtensionRegistry
	opts := r.ExprOptions()
	if len(opts) != setExprHelperCount {
		t.Errorf("nil registry must yield %d built-in set helpers, got %d", setExprHelperCount, len(opts))
	}
}

func TestExprOptions_NoEntries(t *testing.T) {
	r := &ExtensionRegistry{}
	opts := r.ExprOptions()
	if len(opts) != setExprHelperCount {
		t.Errorf("empty registry must yield %d built-in set helpers, got %d", setExprHelperCount, len(opts))
	}
}

func TestExprOptions_SkipsNilFn(t *testing.T) {
	r := &ExtensionRegistry{
		ExprFunctions: []ExprFunction{
			{Name: "good", Fn: func() float64 { return 1 }},
			{Name: "bad", Fn: nil},
		},
	}
	opts := r.ExprOptions()
	want := setExprHelperCount + 1
	if len(opts) != want {
		t.Errorf("expected %d options (set helpers + 1 custom, nil Fn skipped), got %d", want, len(opts))
	}
}

func TestExprOptions_LookupBuiltinAddedWhenTablesPresent(t *testing.T) {
	r := &ExtensionRegistry{
		LookupTables: map[string]LookupTable{"t": {Rows: map[string]float64{"x": 1}}},
	}
	opts := r.ExprOptions()
	if len(opts) == 0 {
		t.Fatal("expected lookup() option when LookupTables registered")
	}
}
