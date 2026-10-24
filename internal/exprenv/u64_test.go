package exprenv

import (
	"fmt"
	"math"
	"math/big"
	"reflect"
	"testing"

	"github.com/expr-lang/expr"
	exprtypes "github.com/expr-lang/expr/types"
	"github.com/expr-lang/expr/vm"

	"github.com/frankbardon/pulse/encoding"
)

// u64_test.go — a u64 field binds as its stored word (U64): every
// comparison is exact over the full range, everything else reads the
// float64 echo exactly as the field always bound.

// u64TestDecl is the declaration a schema {id u64, id2 u64, x f64,
// dec decimal128} gives, or — echo — the float64 declaration a u64
// field had before.
func u64TestDecl(echo bool) exprtypes.Map {
	s := &encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU64},
		{Name: "id2", Type: encoding.FieldTypeU64},
		{Name: "x", Type: encoding.FieldTypeF64},
		{Name: "dec", Type: encoding.FieldTypeDecimal128, Scale: 2},
	}}
	decl := Prototype(s)
	if echo {
		decl["id"] = exprtypes.TypeOf(float64(0))
		decl["id2"] = exprtypes.TypeOf(float64(0))
	}
	return decl
}

func u64Compile(t *testing.T, src string, decl exprtypes.Map, params Params) (*vm.Program, error) {
	t.Helper()
	opts := append([]expr.Option{expr.Env(decl)}, Bound{Params: params}.Options()...)
	return expr.Compile(src, opts...)
}

func u64Run(t *testing.T, src string, decl exprtypes.Map, params Params, env map[string]any) (any, error) {
	t.Helper()
	prog, err := u64Compile(t, src, decl, params)
	if err != nil {
		return nil, fmt.Errorf("compile: %w", err)
	}
	return expr.Run(prog, env)
}

// bigOf is the exact value of an integer or float operand.
func bigOf(v any) *big.Float {
	switch x := v.(type) {
	case U64:
		return new(big.Float).SetUint64(uint64(x))
	case uint64:
		return new(big.Float).SetUint64(x)
	case int64:
		return new(big.Float).SetInt64(x)
	case int:
		return new(big.Float).SetInt64(int64(x))
	case float64:
		return big.NewFloat(x)
	}
	panic(fmt.Sprintf("bigOf %T", v))
}

var u64Ops = []string{"==", "!=", "<", "<=", ">", ">="}

func exactVerdict(op string, a, b any) bool {
	if f, ok := a.(float64); ok && math.IsNaN(f) {
		return op == "!="
	}
	if f, ok := b.(float64); ok && math.IsNaN(f) {
		return op == "!="
	}
	c := bigOf(a).Cmp(bigOf(b))
	switch op {
	case "==":
		return c == 0
	case "!=":
		return c != 0
	case "<":
		return c < 0
	case "<=":
		return c <= 0
	case ">":
		return c > 0
	}
	return c >= 0
}

var u64Values = []uint64{
	0, 1, 7, 1<<53 - 1, 1 << 53, 1<<53 + 1, 1<<53 + 2,
	1<<63 - 1, 1 << 63, 1<<63 + 1, math.MaxUint64 - 1, math.MaxUint64,
}

// TestU64Expr_ComparisonsExact: every comparison of a u64 field against
// an int64 / uint64 param, an integer literal, a float, a negative
// number, NaN and another u64 field is the exact verdict, in either
// operand order — on the typed program and on the null-row twin.
func TestU64Expr_ComparisonsExact(t *testing.T) {
	others := []struct {
		src    string
		params Params
		value  func(id2 uint64) any
	}{
		{"params.k", Params{"k": int64(9007199254740993)}, func(uint64) any { return int64(9007199254740993) }},
		{"params.k", Params{"k": int64(-3)}, func(uint64) any { return int64(-3) }},
		{"params.k", Params{"k": uint64(math.MaxUint64 - 1)}, func(uint64) any { return uint64(math.MaxUint64 - 1) }},
		{"params.k", Params{"k": uint64(1<<63 + 1)}, func(uint64) any { return uint64(1<<63 + 1) }},
		{"params.k", Params{"k": 7.5}, func(uint64) any { return 7.5 }},
		{"6.5", nil, func(uint64) any { return 6.5 }},
		{"-0.5", nil, func(uint64) any { return -0.5 }},
		{"9007199254740993", nil, func(uint64) any { return int64(9007199254740993) }},
		{"9223372036854775807", nil, func(uint64) any { return int64(math.MaxInt64) }},
		{"-1", nil, func(uint64) any { return int64(-1) }},
		{"9007199254740992.0", nil, func(uint64) any { return 9007199254740992.0 }},
		{"1.8446744073709552e19", nil, func(uint64) any { return 1.8446744073709552e19 }},
		{"x", nil, func(uint64) any { return 9223372036854775808.0 }},
		{"x", nil, func(uint64) any { return math.NaN() }},
		{"id2", nil, func(id2 uint64) any { return U64(id2) }},
	}
	for _, twin := range []bool{false, true} {
		decl := u64TestDecl(false)
		if twin {
			for k, v := range decl {
				decl[k] = Untyped(v)
			}
		}
		for _, o := range others {
			for _, op := range u64Ops {
				for _, swap := range []bool{false, true} {
					src := "id " + op + " " + o.src
					if swap {
						src = o.src + " " + op + " id"
					}
					prog, err := u64Compile(t, src, decl, o.params)
					if err != nil {
						t.Fatalf("twin=%v %s: %v", twin, src, err)
					}
					for i, v := range u64Values {
						id2 := u64Values[len(u64Values)-1-i]
						if i%3 == 0 {
							id2 = v
						}
						env := map[string]any{"id": U64(v), "id2": U64(id2)}
						other := o.value(id2)
						if o.src == "x" {
							env["x"] = other
						}
						got, err := expr.Run(prog, env)
						if err != nil {
							t.Fatalf("twin=%v %s @ %d: %v", twin, src, v, err)
						}
						want := exactVerdict(op, U64(v), other)
						if swap {
							want = exactVerdict(op, other, U64(v))
						}
						if got != want {
							t.Errorf("twin=%v %s @ id=%d other=%v: %v, want %v", twin, src, v, other, got, want)
						}
					}
				}
			}
		}
	}
}

// TestU64Expr_InExact: `id in <list>` matches by exact equality.
func TestU64Expr_InExact(t *testing.T) {
	decl := u64TestDecl(false)
	cases := []struct {
		src    string
		params Params
		id     uint64
		want   bool
	}{
		{"id in params.ids", Params{"ids": []uint64{1, math.MaxUint64}}, math.MaxUint64, true},
		{"id in params.ids", Params{"ids": []uint64{1, math.MaxUint64}}, math.MaxUint64 - 1, false},
		{"id in params.ids", Params{"ids": []int64{9007199254740993}}, 1 << 53, false},
		{"id in params.ids", Params{"ids": []int64{9007199254740993}}, 1<<53 + 1, true},
		{"id in [9007199254740993, 4]", nil, 1 << 53, false},
		{"id in [9007199254740993, 4]", nil, 4, true},
		{"id not in [9007199254740993]", nil, 1 << 53, true},
		{"id in [id2]", nil, 1<<53 + 1, false},
		{"id in 1..3", nil, 2, true},
	}
	for _, c := range cases {
		got, err := u64Run(t, c.src, decl, c.params, map[string]any{"id": U64(c.id), "id2": U64(1 << 53)})
		if err != nil {
			t.Fatalf("%s: %v", c.src, err)
		}
		if got != c.want {
			t.Errorf("%s @ %d: %v, want %v", c.src, c.id, got, c.want)
		}
	}
}

// u64EchoExprs are expressions whose result over a u64 field must be
// exactly what the float64 echo gave: same value, same Go type, same
// static result type, same compile / evaluation error.
var u64EchoExprs = []string{
	"id", "id + 1", "id - 5", "id * 2.5", "id / 3", "id % 2", "id % 7 == 3", "-id", "id ** 2", "id ^ 0.5",
	"exp(id / 1e16)", "log(id)", "sqrt(id)", "abs(id)", "max(id, x)", "min(id, 1)", "round(id / 7)",
	"floor(id / 3)", "int(id)", "float(id)", "string(id)", "toJSON(id)", "id ?? 0", "id > 5 ? id : 0",
	"let a = id; a + 1", "let a = id; a == 7", "id + id2", "id * id2", "id == 7", "id < x", "id in [1, 7]",
	"id == nil", "id != nil", "[id, x]", "{\"k\": id}", "sum([id, 1])", "id + dec", "dec == id", "id < dec",
	"dec * id", "id % dec", "id + \"a\"", "id == \"a\"", "id < \"a\"", "id[0]", "id.k", "len(id)", "id && true",
	"id matches \"1\"", "-id == -7", "id in \"abc\"", "params.k + id", "id - params.k == 0", "id == params.k",
	"id / params.k", "id % params.k", "id ** params.k",
	"id < nil", "nil == id", "id in {\"a\": 1}", "id in nil", "id in x", "id in [x]", "id > id2 ? id : id2",
	"filter([1, 7, 9], # < id)", "count([id, id2], # > 3)", "id == x ? 1 : 0", "$env.id + 1", "$env[\"id\"] == 7",
	"$env.id == params.k", "(id ?? 0) == 7", "id == id2 - 1", "id == id2 + \"a\"", "id < x * 2 && id2 >= 3",
	"not (id in [7])", "id != 7 || x == nil",
}

// TestU64Expr_EchoEverywhereElse: for every value up to 2^53 (and, for
// all but comparisons, beyond), the typed program and the null-row twin
// give the bound-as-float64 result bit for bit, with the same result
// types and the same error text.
func TestU64Expr_EchoEverywhereElse(t *testing.T) {
	params := Params{"k": int64(3)}
	dec := Decimal{Value: encoding.NewDecimal128FromInt(1250), Scale: 2}
	for _, twin := range []bool{false, true} {
		now, was := u64TestDecl(false), u64TestDecl(true)
		if twin {
			for k := range now {
				now[k], was[k] = Untyped(now[k]), Untyped(was[k])
			}
		}
		for _, src := range u64EchoExprs {
			pNow, errNow := u64Compile(t, src, now, params)
			pWas, errWas := u64Compile(t, src, was, params)
			if fmt.Sprint(errNow) != fmt.Sprint(errWas) {
				t.Errorf("twin=%v %s: compile error %v, was %v", twin, src, errNow, errWas)
				continue
			}
			if errNow != nil {
				continue
			}
			if a, b := pNow.Node().Type(), pWas.Node().Type(); a != b {
				t.Errorf("twin=%v %s: result type %v, was %v", twin, src, a, b)
			}
			for _, v := range []uint64{0, 1, 7, 3 << 31, 1<<53 - 1, 1 << 53} {
				for _, null := range []bool{false, true} {
					if null && !twin {
						continue
					}
					envNow := map[string]any{"id": U64(v), "id2": U64(v + 1), "x": 4.5, "dec": dec}
					envWas := map[string]any{"id": float64(v), "id2": float64(v + 1), "x": 4.5, "dec": dec}
					if null {
						delete(envNow, "id")
						delete(envWas, "id")
					}
					gotNow, eNow := expr.Run(pNow, envNow)
					gotWas, eWas := expr.Run(pWas, envWas)
					if fmt.Sprint(eNow) != fmt.Sprint(eWas) {
						t.Errorf("twin=%v null=%v %s @ %d: error %v, was %v", twin, null, src, v, eNow, eWas)
						continue
					}
					if !sameResult(gotNow, gotWas) {
						t.Errorf("twin=%v null=%v %s @ %d: %#v (%T), was %#v (%T)", twin, null, src, v, gotNow, gotNow, gotWas, gotWas)
					}
				}
			}
		}
	}
}

// sameResult is deep equality with float64 compared by bits (NaN
// included).
func sameResult(a, b any) bool {
	if fa, ok := a.(float64); ok {
		fb, ok := b.(float64)
		return ok && math.Float64bits(fa) == math.Float64bits(fb)
	}
	return reflect.DeepEqual(a, b)
}

// TestU64Expr_EchoValueFallsBack: a u64 name declared U64 whose env
// carries the float64 echo (no stored word: a rewritten column, an
// $env read) evaluates exactly as the float64 declaration did.
func TestU64Expr_EchoValueFallsBack(t *testing.T) {
	now, was := u64TestDecl(false), u64TestDecl(true)
	params := Params{"k": int64(9007199254740993)}
	for _, src := range []string{"id == params.k", "id < params.k", "id in [9007199254740993]", "id + 1", "id == id2"} {
		for _, v := range []uint64{7, 1 << 53, 1<<53 + 2} {
			env := map[string]any{"id": float64(v), "id2": float64(1<<53 + 1)}
			gotNow, eNow := u64Run(t, src, now, params, env)
			gotWas, eWas := u64Run(t, src, was, params, env)
			if fmt.Sprint(eNow) != fmt.Sprint(eWas) || !sameResult(gotNow, gotWas) {
				t.Errorf("%s @ %d: %v/%v, was %v/%v", src, v, gotNow, eNow, gotWas, eWas)
			}
		}
	}
}

// TestU64Expr_ValueTypeAndTwin pins the declarations: a u64 field
// declares U64, its twin the recognisable u64 operand.
func TestU64Expr_ValueTypeAndTwin(t *testing.T) {
	f := &encoding.Field{Name: "id", Type: encoding.FieldTypeU64}
	if got := ValueType(f); got != reflect.TypeOf(U64(0)) {
		t.Fatalf("ValueType(u64) = %v", got)
	}
	if got := Untyped(exprtypes.TypeOf(U64(0))); got != untypedU64 {
		t.Fatalf("Untyped(U64) = %v", got)
	}
	set := &encoding.Field{Name: "s", Type: encoding.FieldTypeSetU64}
	if got := ValueType(set); got != reflect.TypeOf(uint64(0)) {
		t.Fatalf("ValueType(set_u64) = %v, want uint64 (unchanged)", got)
	}
}
