package exprenv

import (
	stderrors "errors"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/expr-lang/expr"
	exprtypes "github.com/expr-lang/expr/types"
	"github.com/expr-lang/expr/vm"

	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
)

// decimalTestSchema is a money schema: a / b / c at scale 1-2, price /
// cost at scale 2, a float x, a categorical label, and big at scale 0.
func decimalTestSchema() *encoding.Schema {
	dict := encoding.NewDictionary()
	_, _ = dict.Add("a")
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "a", Type: encoding.FieldTypeDecimal128, Precision: 38, Scale: 1, Nullable: true},
		{Name: "b", Type: encoding.FieldTypeDecimal128, Precision: 38, Scale: 1},
		{Name: "c", Type: encoding.FieldTypeDecimal128, Precision: 38, Scale: 2},
		{Name: "price", Type: encoding.FieldTypeDecimal128, Precision: 38, Scale: 2, Nullable: true},
		{Name: "cost", Type: encoding.FieldTypeDecimal128, Precision: 38, Scale: 2},
		{Name: "tiny", Type: encoding.FieldTypeDecimal128, Precision: 38, Scale: 4},
		{Name: "zero", Type: encoding.FieldTypeDecimal128, Precision: 38, Scale: 2},
		{Name: "big", Type: encoding.FieldTypeDecimal128, Precision: 38, Scale: 0},
		{Name: "x", Type: encoding.FieldTypeF64, Nullable: true},
		{Name: "cat", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict},
	}}
}

// decimalTestEnv binds every field through Bind, the record binding's
// lift, from its decimal spelling at the field's scale.
func decimalTestEnv(t *testing.T, s *encoding.Schema, vals map[string]string) map[string]any {
	t.Helper()
	env := map[string]any{"x": 0.1, "cat": "a"}
	for name, lit := range vals {
		f := s.Field(name)
		d, sc, err := encoding.ParseDecimal128(lit)
		if err != nil {
			t.Fatal(err)
		}
		d, err = d.Rescale(sc, f.Scale)
		if err != nil {
			t.Fatal(err)
		}
		env[name] = Bind(f, d)
	}
	return env
}

func decimalStdEnv(t *testing.T, s *encoding.Schema) map[string]any {
	return decimalTestEnv(t, s, map[string]string{
		"a": "0.1", "b": "0.2", "c": "0.30", "price": "3.60", "cost": "3.00",
		"tiny": "0.0004", "zero": "0.00", "big": "90000000000000000000000000000000000000",
	})
}

func compileTyped(t *testing.T, s *encoding.Schema, src string) (*vm.Program, error) {
	t.Helper()
	return expr.Compile(src, append([]expr.Option{expr.Env(Prototype(s))}, Options()...)...)
}

// compileTwin compiles src as the runtime's null-row twin does: every
// declared name re-declared through Untyped.
func compileTwin(t *testing.T, s *encoding.Schema, src string) (*vm.Program, error) {
	t.Helper()
	decl := exprtypes.Map{}
	for k, v := range Prototype(s) {
		decl[k] = Untyped(v)
	}
	return expr.Compile(src, append([]expr.Option{expr.Env(decl)}, Options()...)...)
}

func runBoth(t *testing.T, s *encoding.Schema, src string, env map[string]any) (typed, twin any, typedErr, twinErr error) {
	t.Helper()
	p, err := compileTyped(t, s, src)
	if err != nil {
		t.Fatalf("typed compile %q: %v", src, err)
	}
	q, err := compileTwin(t, s, src)
	if err != nil {
		t.Fatalf("twin compile %q: %v", src, err)
	}
	typed, typedErr = expr.Run(p, env)
	twin, twinErr = expr.Run(q, env)
	return
}

// TestDecimal_ExactOracle: decimal arithmetic and comparison are exact
// where float64 is not, and the null-row twin (every name declared
// through Untyped) gives the typed program's result to the byte.
func TestDecimal_ExactOracle(t *testing.T) {
	s := decimalTestSchema()
	env := decimalStdEnv(t, s)
	// The float64 oracle is wrong on each of these.
	p1, p2, n := 0.1, 0.2, 33.0
	if p1+p2 == 0.3 || p1*n == 3.3 {
		t.Fatal("float64 oracle unexpectedly exact")
	}
	for _, tc := range []struct {
		src  string
		want any // bool, float64, or a decimal string
	}{
		{`a + b == c`, true},
		{`a * 33 == 3.3`, true},
		{`a + b != c`, false},
		{`a + b`, "0.3"},
		{`c - a - b`, "0.00"},
		{`price / cost == 1.2`, true},
		{`price / cost`, "1.2000"},
		{`price / cost > 1.2`, false},
		{`price / cost >= 1.2`, true},
		{`price / cost < 1.21`, true},
		{`price / cost <= 1.19`, false},
		{`price * 2`, "7.20"},
		{`2 * price`, "7.20"},
		{`price * 1.5`, "5.400"},
		{`price + 0.1`, "3.70"},
		{`1 - price`, "-2.60"},
		{`price / 7`, "0.5143"},
		{`10 / price`, "2.7778"},
		{`tiny / 8`, "0.0000"},     // 0.00005: half, rounds to even 0
		{`tiny * 3 / 8`, "0.0002"}, // 0.00015: half, rounds to even 2
		{`price * cost`, "10.8000"},
		{`-price`, "-3.60"},
		{`price > x`, true},
		{`x < price`, true},
		{`price == 3.6`, true},
		{`price == 3.60`, true},
		{`3.6 == price`, true},
		{`float(a + b)`, 0.3},
		{`float(price / cost)`, 1.2},
		{`float(price) * 1.5`, 5.4},
		{`float(2)`, 2.0},
		{`float("1.5")`, 1.5},
		{`(price ?? 0) * 2`, "7.20"},
		{`let t = price * 2; t / 4`, "1.8000"},
		{`price == nil`, false},
		{`price != nil && price > 3.5`, true},
	} {
		typed, twin, terr, werr := runBoth(t, s, tc.src, env)
		if terr != nil || werr != nil {
			t.Errorf("%s: typed err %v, twin err %v", tc.src, terr, werr)
			continue
		}
		for side, got := range map[string]any{"typed": typed, "twin": twin} {
			switch w := tc.want.(type) {
			case string:
				if d, ok := got.(Decimal); !ok || d.String() != w {
					t.Errorf("%s (%s) = %#v, want decimal %s", tc.src, side, got, w)
				}
			default:
				if got != tc.want {
					t.Errorf("%s (%s) = %#v (%T), want %#v", tc.src, side, got, got, tc.want)
				}
			}
		}
	}
}

// TestDecimal_NullTwin: on a row where a decimal is null (absent from the
// env) the twin keeps expr-lang's nil rules — `== nil` true, `??`
// substitutes (lifted to a decimal, so the arithmetic stays exact) — and
// an operator over nil is an evaluation error, never a number.
func TestDecimal_NullTwin(t *testing.T) {
	s := decimalTestSchema()
	env := decimalStdEnv(t, s)
	delete(env, "price")
	for _, tc := range []struct {
		src  string
		want any
	}{
		{`price == nil`, true},
		{`price != nil`, false},
		{`(price ?? 0) * 2`, "0"},
		{`(price ?? cost) / 3`, "1.0000"},
		{`price ?? 1.5`, "1.5"},
	} {
		q, err := compileTwin(t, s, tc.src)
		if err != nil {
			t.Fatalf("twin compile %q: %v", tc.src, err)
		}
		got, err := expr.Run(q, env)
		if err != nil {
			t.Errorf("%s: %v", tc.src, err)
			continue
		}
		if w, ok := tc.want.(string); ok {
			if d, ok := got.(Decimal); !ok || d.String() != w {
				t.Errorf("%s = %#v, want decimal %s", tc.src, got, w)
			}
		} else if got != tc.want {
			t.Errorf("%s = %#v, want %#v", tc.src, got, tc.want)
		}
	}
	for _, src := range []string{`price * 2`, `price > 1`, `float(price)`, `-price`} {
		q, err := compileTwin(t, s, src)
		if err != nil {
			t.Fatalf("twin compile %q: %v", src, err)
		}
		if got, err := expr.Run(q, env); err == nil {
			t.Errorf("%s over a null price = %#v, want an evaluation error", src, got)
		}
	}
}

// TestDecimal_CodedFailures: a zero divisor and an out-of-range result
// raise the decimal codes, reachable through the evaluation error.
func TestDecimal_CodedFailures(t *testing.T) {
	s := decimalTestSchema()
	env := decimalStdEnv(t, s)
	for _, tc := range []struct {
		src  string
		code perr.Code
	}{
		{`price / zero`, perr.PULSE_DECIMAL_DIVIDE_BY_ZERO},
		{`price / 0`, perr.PULSE_DECIMAL_DIVIDE_BY_ZERO},
		{`big * 10`, perr.PULSE_DECIMAL_OVERFLOW},
		{`big + big`, perr.PULSE_DECIMAL_OVERFLOW},
	} {
		typed, twin, terr, werr := runBoth(t, s, tc.src, env)
		for side, err := range map[string]error{"typed": terr, "twin": werr} {
			var ce *perr.CodedError
			if !stderrors.As(err, &ce) || ce.Code != tc.code {
				t.Errorf("%s (%s): err %v (typed %v, twin %v), want %s", tc.src, side, err, typed, twin, tc.code)
			}
		}
	}
	// A non-finite number has no decimal value.
	env["x"] = math.Inf(1)
	if _, _, terr, werr := runBoth(t, s, `price * x`, env); terr == nil || werr == nil {
		t.Errorf("price * +Inf: typed %v, twin %v, want evaluation errors", terr, werr)
	}
}

// TestDecimal_CompileRefusals: an ill-typed decimal expression fails at
// compile (predict's and the runtime build's compile alike).
func TestDecimal_CompileRefusals(t *testing.T) {
	s := decimalTestSchema()
	for _, src := range []string{
		`price / "x"`,
		`price + cat`,
		`price > "1"`,
		`price + true`,
		`price % 2`,
		`price == "3.60"`,
	} {
		if _, err := compileTyped(t, s, src); err == nil || !strings.Contains(err.Error(), "invalid operation") {
			t.Errorf("compile %q: %v, want an invalid-operation compile error", src, err)
		}
	}
}

// TestDecimal_NonDecimalUntouched: an expression with no decimal operand
// compiles to the very program it compiled to before (no decimal call is
// patched in), so its run-time cost is unchanged.
func TestDecimal_NonDecimalUntouched(t *testing.T) {
	s := decimalTestSchema()
	for _, src := range []string{
		`x + 1 > 2`, `(x ?? 0) * 2`, `float(x) / 3`, `-x`, `x == nil`, `cat == "a"`,
	} {
		for side, compile := range map[string]func(*testing.T, *encoding.Schema, string) (*vm.Program, error){"typed": compileTyped, "twin": compileTwin} {
			p, err := compile(t, s, src)
			if err != nil {
				t.Fatalf("%s %q: %v", side, src, err)
			}
			if dis := p.Disassemble(); strings.Contains(dis, decimalFuncPrefix) {
				t.Errorf("%s %q patched a decimal call:\n%s", side, src, dis)
			}
		}
	}
}

// TestDecimal_BindAndValueType: Bind lifts a decimal128 field's value to
// a Decimal at the field's scale (the declared ValueType); every other
// value passes through untouched.
func TestDecimal_BindAndValueType(t *testing.T) {
	s := decimalTestSchema()
	f := s.Field("price")
	d := encoding.NewDecimal128FromInt(360)
	got := Bind(f, d)
	if reflect.TypeOf(got) != ValueType(f) {
		t.Fatalf("Bind type %T, ValueType %v", got, ValueType(f))
	}
	if dd := got.(Decimal); dd.Scale != 2 || dd.String() != "3.60" || dd.Float64() != 3.6 {
		t.Errorf("Bind = %#v (%s, %v)", dd, dd.String(), dd.Float64())
	}
	if got := Bind(s.Field("x"), 1.5); got != 1.5 {
		t.Errorf("Bind(float) = %#v", got)
	}
	if got := Bind(nil, d); !reflect.DeepEqual(got, d) {
		t.Errorf("Bind(nil field) = %#v, want the value untouched", got)
	}
	if got := Bind(f, 1.25); got != 1.25 {
		t.Errorf("Bind(decimal field, non-decimal value) = %#v", got)
	}
	if Untyped(Prototype(s)["x"]) != exprtypes.Any {
		t.Error("Untyped(float64) is not Any")
	}
	if Untyped(Prototype(s)["price"]) == exprtypes.Any {
		t.Error("Untyped(Decimal) is Any; the twin would lose the decimal operators")
	}
}

// TestDecimal_MulScaleCap: a product past scale 38 is held to 38 by
// banker's rounding — half to even, either sign.
func TestDecimal_MulScaleCap(t *testing.T) {
	at20 := func(m int64) Decimal { return Decimal{Value: encoding.NewDecimal128FromInt(m), Scale: 20} }
	for _, tc := range []struct {
		a, b int64
		want int64 // mantissa at scale 38
	}{
		{15, 10, 2},   // 1.50 → 2 (odd quotient rounds up)
		{25, 10, 2},   // 2.50 → 2 (even quotient stays)
		{26, 10, 3},   // 2.60 → 3
		{24, 10, 2},   // 2.40 → 2
		{-15, 10, -2}, // -1.50 → -2
		{-25, 10, -2}, // -2.50 → -2
	} {
		got, err := decimalMul(at20(tc.a), at20(tc.b))
		if err != nil {
			t.Fatal(err)
		}
		d := got.(Decimal)
		if d.Scale != 38 || d.Value.Mantissa().Int64() != tc.want {
			t.Errorf("%d e-20 * %d e-20 = %s e-%d, want %d e-38", tc.a, tc.b, d.Value.Mantissa(), d.Scale, tc.want)
		}
	}
}

// TestDecimal_EmbedderFunctionsSeeDecimal128: an embedder ExprFunction
// receives a decimal argument as the public encoding.Decimal128 it always
// did — never the env's internal binding.
func TestDecimal_EmbedderFunctionsSeeDecimal128(t *testing.T) {
	s := decimalTestSchema()
	var seen any
	opts := append([]expr.Option{expr.Env(Prototype(s))}, Options()...)
	opts = append(opts, ExtensionFunctions([]NamedFunc{{Name: "probe", Fn: func(args ...any) (any, error) {
		seen = args[0]
		return 1.0, nil
	}}})...)
	p, err := expr.Compile(`probe(price)`, opts...)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := expr.Run(p, decimalStdEnv(t, s)); err != nil {
		t.Fatal(err)
	}
	if d, ok := seen.(encoding.Decimal128); !ok || d.String(2) != "3.60" {
		t.Fatalf("embedder saw %#v (%T), want encoding.Decimal128 360", seen, seen)
	}
}
