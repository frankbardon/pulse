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

// decimalWant checks one result against a want: a string is a decimal's
// spelling, anything else must equal the result as is.
func decimalWant(t *testing.T, label string, got, want any) {
	t.Helper()
	if w, ok := want.(string); ok {
		if d, ok := got.(Decimal); !ok || d.String() != w {
			t.Errorf("%s = %#v (%T), want decimal %s", label, got, got, w)
		}
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s = %#v (%T), want %#v (%T)", label, got, got, want, want)
	}
}

// TestDecimal_BranchesAndBuiltins: a ternary or `??` that mixes a
// decimal with a number, a decimal laundered through an untyped value,
// and every numeric built-in over a decimal compile AND run, on the
// typed program and the null-row twin alike, to the same figure:
// abs / max / min / sum / mean / median / sort / in exact; ceil / floor
// / round to an integral decimal (round half-even, as every decimal
// rescale); int truncates; sqrt / log / exp / pow and the vector family
// read the decimal's float64 (their answers are irrational floats).
func TestDecimal_BranchesAndBuiltins(t *testing.T) {
	s := decimalTestSchema()
	env := decimalStdEnv(t, s) // a 0.1, price 3.60, cost 3.00, x 0.1
	for _, tc := range []struct {
		src  string
		want any
	}{
		// Ternary: the number branch is lifted, so the result is decimal.
		{`x > 0 ? price : 0`, "3.60"},
		{`x < 0 ? price : 0`, "0"},
		{`(x > 0 ? price : 0) * 2`, "7.20"},
		{`(x < 0 ? price : 1.5) * 2`, "3.0"},
		{`(x < 0 ? 0 : price) / 3`, "1.2000"},
		{`-(x > 0 ? price : 0)`, "-3.60"},
		{`float(x > 0 ? price : 0)`, 3.6},
		{`(x > 0 ? price : 0) > 3.5`, true},
		{`x > 0 ? price : x`, "3.60"},
		// `??` over an untyped default, and untyped launderings.
		{`(price ?? x) * 3`, "10.80"},
		{`{"a": price}.a * 2`, "7.20"},
		{`[price, x][0] - 1`, "2.60"},
		{`{"a": price}.a > 3.5`, true},
		{`float({"a": price}.a)`, 3.6},
		// Exact numeric built-ins.
		{`abs(-price)`, "3.60"},
		{`abs(price - 5)`, "1.40"},
		{`abs(x > 0 ? -price : 0)`, "3.60"},
		{`ceil(price)`, "4"},
		{`floor(price)`, "3"},
		{`ceil(-price)`, "-3"},
		{`floor(-price)`, "-4"},
		{`round(price)`, "4"},
		{`round(a * 25)`, "2"},   // 2.5: half to even
		{`round(a * 35)`, "4"},   // 3.5: half to even
		{`round(-a * 25)`, "-2"}, // -2.5: half to even
		{`ceil(cost)`, "3"},
		{`int(price)`, 3},
		{`int(-price)`, -3},
		{`max(price, 1)`, "3.60"},
		{`max(price, 5)`, "5"},
		{`max(price, cost)`, "3.60"},
		{`min(price, cost)`, "3.00"},
		{`min(price, x)`, "0.1"},
		{`max([price, cost, 4])`, "4"},
		{`min([price, cost], 2.5)`, "2.5"},
		{`max(price, 1) * 2`, "7.20"},
		{`sum([price, cost])`, "6.60"},
		{`sum([price, 1])`, "4.60"},
		{`sum([price, cost], # * 2)`, "13.20"},
		{`mean([price, cost])`, "3.3000"},
		{`mean(price, cost, 1)`, "2.5333"},
		{`median([price, cost, 1])`, "3.00"},
		{`median([price, cost])`, "3.3000"},
		{`sort([price, cost, 1])[0]`, 1},
		{`sort([price, cost])[0] == cost`, true},
		{`sort([price, cost], "desc")[0] == price`, true},
		{`sortBy([price, cost], #)[0] == cost`, true},
		{`sortBy([price, cost], -#)[0] == price`, true},
		{`price in [3.6, 1]`, true},
		{`price in [1, 2]`, false},
		{`3.6 in [cost, price]`, true},
		{`string(price)`, "3.60"},
		{`toJSON(price)`, "3.60"},
		// Float-domain built-ins read the decimal's float64.
		{`sqrt(price)`, math.Sqrt(3.6)},
		{`log(price)`, math.Log(3.6)},
		{`exp(a)`, math.Exp(0.1)},
		{`pow(price, 2)`, math.Pow(3.6, 2)},
		{`vsum([price, cost])`, 6.6},
		{`vmax([price, cost, x])`, 3.6},
	} {
		typed, twin, terr, werr := runBoth(t, s, tc.src, env)
		if terr != nil || werr != nil {
			t.Errorf("%s: typed err %v, twin err %v", tc.src, terr, werr)
			continue
		}
		if tc.src == `string(price)` || tc.src == `toJSON(price)` {
			if typed != tc.want || twin != tc.want {
				t.Errorf("%s = typed %#v, twin %#v, want %q", tc.src, typed, twin, tc.want)
			}
			continue
		}
		decimalWant(t, tc.src+" (typed)", typed, tc.want)
		decimalWant(t, tc.src+" (twin)", twin, tc.want)
	}
}

// TestDecimal_NullTwinBranches: on a row where price is null, the twin's
// `??` lifts an untyped default to a decimal exactly as the typed
// program lifts a number, and a built-in over the absent decimal is an
// evaluation error (never a number).
func TestDecimal_NullTwinBranches(t *testing.T) {
	s := decimalTestSchema()
	env := decimalStdEnv(t, s)
	delete(env, "price")
	for _, tc := range []struct {
		src  string
		want any
	}{
		{`(price ?? x) * 3`, "0.3"},
		{`price ?? x`, "0.1"},
		{`x > 0 ? cost : price`, "3.00"},
		{`(x < 0 ? cost : price) == nil`, true},
		{`max(price ?? 0, cost)`, "3.00"},
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
		decimalWant(t, tc.src, got, tc.want)
	}
	for _, src := range []string{`abs(price)`, `round(price)`, `max(price, 1)`, `sum([price, 1])`, `(x > 0 ? price : 0) * 2`} {
		q, err := compileTwin(t, s, src)
		if err != nil {
			t.Fatalf("twin compile %q: %v", src, err)
		}
		if got, err := expr.Run(q, env); err == nil {
			t.Errorf("%s over a null price = %#v, want an evaluation error", src, got)
		}
	}
}

// TestDecimal_RefusalsNameFloatEscape: an operation with no exact
// decimal meaning is refused at COMPILE (typed program — predict's and
// the build's compile), and the reason names float() as the escape.
func TestDecimal_RefusalsNameFloatEscape(t *testing.T) {
	s := decimalTestSchema()
	for _, src := range []string{`price % 2`, `2 % price`, `(x > 0 ? price : 0) % 2`} {
		_, err := compileTyped(t, s, src)
		if err == nil || !strings.Contains(err.Error(), "float(") {
			t.Errorf("compile %q: %v, want a refusal naming float()", src, err)
		}
	}
}

// TestDecimal_PatchHidesNoError: a patched decimal call never hides an
// ill-typed operand beneath it — expr-lang never re-checks a typed call
// — so the compile refuses with the error, at the position, an
// unpatched check reports.
func TestDecimal_PatchHidesNoError(t *testing.T) {
	s := decimalTestSchema()
	for _, tc := range []struct{ src, want string }{
		{`price / "x" > 1`, `invalid operation: / (mismatched types exprenv.Decimal and string) (1:7)`},
		{`price * len([x / "a"])`, `invalid operation: / (mismatched types float64 and string) (1:16)`},
		{`abs(price) + (x / "a")`, `invalid operation: / (mismatched types float64 and string) (1:17)`},
		{`(price * 2) + cat`, `invalid operation: + (mismatched types exprenv.Decimal and string) (1:13)`},
		{`max(price, "a")`, `invalid argument for max (type string) (1:1)`},
		{`price % 2 > 1`, `invalid operation: % over a decimal (types exprenv.Decimal and int); convert it with float() first, e.g. float(price) % 2 (1:7)`},
	} {
		_, err := compileTyped(t, s, tc.src)
		if err == nil {
			t.Errorf("compile %q: nil, want %s", tc.src, tc.want)
			continue
		}
		if got := CompileReason(err); got != tc.want {
			t.Errorf("compile %q:\n got  %s\n want %s", tc.src, got, tc.want)
		}
	}
}

// TestDecimal_IntOverflowCoded: int() of a decimal past int64 is
// PULSE_DECIMAL_OVERFLOW, never a wrapped integer.
func TestDecimal_IntOverflowCoded(t *testing.T) {
	s := decimalTestSchema()
	env := decimalStdEnv(t, s)
	typed, twin, terr, werr := runBoth(t, s, `int(big)`, env)
	for side, err := range map[string]error{"typed": terr, "twin": werr} {
		var ce *perr.CodedError
		if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_DECIMAL_OVERFLOW {
			t.Errorf("int(big) (%s): err %v (typed %v, twin %v), want PULSE_DECIMAL_OVERFLOW", side, err, typed, twin)
		}
	}
}
