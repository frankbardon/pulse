package processing

import (
	"math"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/exprenv"
)

// expr_u64_test.go — a u64 field reaches an expression as its stored
// word (exprenv.U64, Record.exprU64): comparisons are exact past 2^53,
// and every expression over a value up to 2^53 evaluates exactly as it
// did when the field bound as its float64 echo — the f64 twin schema
// below IS that old binding (same float64 declaration, same value).

func u64ExprSchemas() (u64s, f64s *encoding.Schema) {
	u64s = &encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU64},
		{Name: "y", Type: encoding.FieldTypeF64, Nullable: true},
	}}
	f64s = &encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeF64},
		{Name: "y", Type: encoding.FieldTypeF64, Nullable: true},
	}}
	return u64s, f64s
}

// u64ExprRecord is one row: id = v (with its stored word when exact),
// y = 2.5 or null.
func u64ExprRecord(s *encoding.Schema, v uint64, nullY, exact bool) *Record {
	vals := map[string]float64{"id": float64(v)}
	nulls := map[string]bool{}
	if nullY {
		nulls["y"] = true
	} else {
		vals["y"] = 2.5
	}
	r := NewRecordWithNulls(s, vals, nulls)
	if exact {
		r.SetExactU64("id", v)
	}
	return r
}

type u64ExprOutcome struct {
	out       any
	nullInput bool
	err       string
}

func runU64Expr(t *testing.T, src, site string, s *encoding.Schema, params exprenv.Params, r *Record, cached bool) u64ExprOutcome {
	t.Helper()
	p, err := newExprProgram(src, site, s, nil, nil, exprenv.Bound{Params: params}.Options())
	if err != nil {
		return u64ExprOutcome{err: "build: " + err.Error()}
	}
	if cached {
		r.AllValues() // the buffered path: a record already carrying its map
	}
	out, nullInput, err := p.runRecord(r)
	o := u64ExprOutcome{out: out, nullInput: nullInput}
	if err != nil {
		o.err = err.Error()
	}
	return o
}

// TestU64Expr_ProgramMatchesFloatBinding: for values up to 2^53 every
// formula and filter over a u64 field — arithmetic, `%`, `**`, maths
// built-ins, comparisons, `in`, `??`, $env, a null-input row on the
// untyped twin — gives exactly the float64 binding's outcome: same
// value, same Go type, same error text; on the per-input and the cached
// AllValues path alike.
func TestU64Expr_ProgramMatchesFloatBinding(t *testing.T) {
	t.Parallel()

	u64s, f64s := u64ExprSchemas()
	params := exprenv.Params{"k": int64(7), "ids": []int64{1, 7}}
	formulas := []string{
		"id", "id + 1", "id * 2.5", "id / 3", "id % 2", "id % 0", "-id", "id ** 2", "exp(id / 1e16)", "log(id)",
		"sqrt(id)", "abs(id - 10)", "max(id, y)", "round(id / 7)", "string(id)", "toJSON(id)", "id ?? 0",
		"id > 5 ? id : 0", "id == params.k ? 1 : 0", "id + y", "y ?? id", "id < y ? 1 : 2", "$env.id + 1",
		"$env[\"id\"] * 2", "(y ?? 0) + id", "id + y * 2", "id in params.ids ? 1 : 0",
	}
	filters := []string{
		"id == 7", "id != 7", "id < 7", "id <= 7", "id > 7", "id >= 7", "id == params.k", "id in params.ids",
		"id in [1, 7]", "id % 2 == 0", "id > y", "id == nil", "id != nil", "id > 3 && y > 1", "id == 7 || y == nil",
		"id < y", "$env.id == 7", "id + 1 > 8",
	}
	values := []uint64{0, 1, 7, 8, 3 << 31, 1<<53 - 1, 1 << 53}
	check := func(site string, srcs []string) {
		for _, src := range srcs {
			for _, v := range values {
				for _, nullY := range []bool{false, true} {
					for _, cached := range []bool{false, true} {
						got := runU64Expr(t, src, site, u64s, params, u64ExprRecord(u64s, v, nullY, true), cached)
						want := runU64Expr(t, src, site, f64s, params, u64ExprRecord(f64s, v, nullY, false), cached)
						same := got.nullInput == want.nullInput && got.err == want.err
						if f, ok := got.out.(float64); ok {
							w, ok := want.out.(float64)
							same = same && ok && math.Float64bits(f) == math.Float64bits(w)
						} else {
							same = same && reflect.DeepEqual(got.out, want.out)
						}
						if !same {
							t.Errorf("%s %q id=%d nullY=%v cached=%v: %#v, float binding %#v", site, src, v, nullY, cached, got, want)
						}
					}
				}
			}
		}
	}
	check(exprenv.SiteFormula, formulas)
	check(exprenv.SiteFilter, filters)
}

// TestU64Expr_ProgramExactPast2p53: the stored word reaches the
// comparison on the per-input path, the cached AllValues path and the
// null-row twin; a record without the word (a rewritten column) keeps
// the echo and the historical result.
func TestU64Expr_ProgramExactPast2p53(t *testing.T) {
	t.Parallel()

	u64s, _ := u64ExprSchemas()
	params := exprenv.Params{"k": int64(9007199254740993), "top": uint64(math.MaxUint64 - 1)}
	cases := []struct {
		src  string
		v    uint64
		want bool
	}{
		{"id == params.k", 1<<53 + 1, true},
		{"id == params.k", 1 << 53, false},
		{"id == 9007199254740993", 1 << 53, false},
		{"id < params.top", math.MaxUint64, false},
		{"id < params.top", math.MaxUint64 - 2, true},
		{"id >= params.top", math.MaxUint64 - 1, true},
		{"id > 9223372036854775807", 1 << 63, true},
		{"id in [9007199254740993]", 1 << 53, false},
	}
	for _, c := range cases {
		for _, nullY := range []bool{false, true} {
			for _, cached := range []bool{false, true} {
				src := c.src + " && (y == nil || y > 0)"
				got := runU64Expr(t, src, exprenv.SiteFilter, u64s, params, u64ExprRecord(u64s, c.v, nullY, true), cached)
				if got.err != "" || got.out != c.want || got.nullInput != nullY {
					t.Errorf("%q id=%d nullY=%v cached=%v: %#v, want %v", src, c.v, nullY, cached, got, c.want)
				}
			}
		}
	}
	// No stored word: the echo, as before — 2^53 == 2^53+1 in float64.
	got := runU64Expr(t, "id == params.k", exprenv.SiteFilter, u64s, params, u64ExprRecord(u64s, 1<<53, false, false), false)
	if got.out != true {
		t.Fatalf("echo fallback: %#v, want the float64 verdict true", got)
	}
}

// TestU64Expr_RecordExprU64: the word is bound only beside the value it
// was decoded for.
func TestU64Expr_RecordExprU64(t *testing.T) {
	t.Parallel()

	u64s, _ := u64ExprSchemas()
	r := u64ExprRecord(u64s, 1<<53+1, false, true)
	if got := r.exprU64("id", float64(1<<53+1)); got != exprenv.U64(1<<53+1) {
		t.Fatalf("exprU64 = %#v", got)
	}
	if got := r.exprU64("id", 3.0); got != 3.0 {
		t.Fatalf("a value that is not the word's echo binds as itself: %#v", got)
	}
	if got := r.exprU64("y", 2.5); got != 2.5 {
		t.Fatalf("non-u64 field: %#v", got)
	}
	r.SetNumeric("id", 4) // a rewrite withdraws the word
	if got := r.exprU64("id", 4.0); got != 4.0 {
		t.Fatalf("rewritten column: %#v", got)
	}
	// Even a rewrite to the old word's own echo: the stale word never
	// comes back.
	r = u64ExprRecord(u64s, 1<<53+1, false, true)
	r.SetNumeric("id", float64(1<<53+1))
	if got := r.exprU64("id", float64(1<<53+1)); got != float64(1<<53) {
		t.Fatalf("rewritten to the echo: %#v", got)
	}
}
