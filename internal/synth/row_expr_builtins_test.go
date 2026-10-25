package synth_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"math"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/exprenv"
	"github.com/frankbardon/pulse/internal/synth"
	"github.com/frankbardon/pulse/linalg/detmath"
)

// mathCase is one row-environment function: src is an expression over
// the f64 field x, want its value computed independently.
type mathCase struct {
	name     string
	src      string
	want     func(x float64) float64
	discrete bool // few distinct values: the constraint need not reject
}

func hillWant(x float64) float64 {
	v, err := exprenv.Impl("hill")(x, 1.5, 2.0)
	if err != nil {
		panic(err)
	}
	return v.(float64)
}

var rowMathCases = []mathCase{
	{"sqrt", "sqrt(x)", math.Sqrt, false},
	{"exp", "exp(x)", detmath.Exp, false},
	{"log", "log(x)", detmath.Log, false},
	{"log1p", "log1p(x)", detmath.Log1p, false},
	{"pow", "pow(x, 1.7)", func(x float64) float64 { return detmath.Pow(x, 1.7) }, false},
	{"hill", "hill(x, 1.5, 2)", hillWant, false},
	{"sin", "sin(x)", detmath.Sin, false},
	{"cos", "cos(x)", detmath.Cos, false},
	{"tan", "tan(x / 2)", func(x float64) float64 { return detmath.Tan(x / 2) }, false},
	{"atan2", "atan2(x, 1.3)", func(x float64) float64 { return detmath.Atan2(x, 1.3) }, false},
	{"pi", "pi() * x", func(x float64) float64 { return math.Pi * x }, false},
	{"e", "e() * x", func(x float64) float64 { return math.E * x }, false},
	{"**", "x ** 1.7", func(x float64) float64 { return detmath.Pow(x, 1.7) }, false},
	{"^", "x ^ 1.7", func(x float64) float64 { return detmath.Pow(x, 1.7) }, false},
	{"% float", "x % 0.7", func(x float64) float64 { return math.Mod(x, 0.7) }, false},
	{"% float negative dividend", "-x % 0.7", func(x float64) float64 { return math.Mod(-x, 0.7) }, false},
	{"% int", "int(x * 10) % 3", func(x float64) float64 { return float64(int(x*10) % 3) }, true},
}

// quantileOf is the q-quantile of want over a fine grid of the x range.
func quantileOf(want func(float64) float64, q float64) float64 {
	const n = 2001
	vals := make([]float64, n)
	for i := range vals {
		vals[i] = want(0.5 + 2.5*float64(i)/float64(n-1))
	}
	sort.Float64s(vals)
	return vals[int(q*float64(n-1))]
}

func lit(f float64) string { return strconv.FormatFloat(f, 'g', -1, 64) }

// TestSynthExprMath_EveryFunctionInSetExprWhenAndConstraint: each
// shared built-in and operator in all three expression slots, against
// independently computed detmath values. `y` is drawn far from every
// function's range (~1000), so a row the gate skipped is recognisable.
func TestSynthExprMath_EveryFunctionInSetExprWhenAndConstraint(t *testing.T) {
	for _, c := range rowMathCases {
		t.Run(c.name, func(t *testing.T) {
			gate := quantileOf(c.want, 0.6)
			floor := quantileOf(c.want, 0.2)
			spec := &synth.Spec{
				RowCount: 400,
				Fields: []synth.FieldSpec{
					{Name: "x", Type: "f64", Distribution: synth.DistUniform,
						Params: map[string]any{"min": 0.5, "max": 3.0}},
					{Name: "y", Type: "f64", Distribution: synth.DistNormal,
						Params: map[string]any{"mean": 1000.0, "std": 1.0}},
				},
				Rules: []synth.RuleSpec{{
					When:    "(" + c.src + ") < " + lit(gate),
					SetExpr: map[string]string{"y": c.src},
				}},
				Constraints: []synth.ConstraintSpec{{Expr: "(" + c.src + ") >= " + lit(floor)}},
			}
			data, res, err := synth.SynthBytes(spec, synth.Options{Seed: 17})
			if err != nil {
				t.Fatalf("SynthBytes: %v", err)
			}
			xs, ys := readF64Field(t, data, "x"), readF64Field(t, data, "y")
			fired := 0
			for i, x := range xs {
				w := c.want(x)
				if w < floor {
					t.Fatalf("row %d: f(%v) = %v < constraint floor %v survived", i, x, w, floor)
				}
				if w < gate {
					fired++
					if math.Float64bits(ys[i]) != math.Float64bits(w) {
						t.Fatalf("row %d: set_expr wrote %v, want %v (bits %x vs %x)",
							i, ys[i], w, math.Float64bits(ys[i]), math.Float64bits(w))
					}
				} else if ys[i] == w {
					t.Fatalf("row %d: the gate should not have fired (f = %v >= %v)", i, w, gate)
				}
			}
			if fired == 0 || fired == len(xs) {
				t.Fatalf("gate fired on %d of %d rows; the when is not discriminating", fired, len(xs))
			}
			if !c.discrete && res.RowsRejected == 0 {
				t.Fatal("the constraint rejected no row; it is not evaluating the function")
			}
		})
	}
}

// TestSynthExprMath_FieldNamedLikeABuiltinStillReadsTheField pins the
// collision PRECEDENCE: a bare name is the field, so an existing spec
// with a column named like a built-in keeps generating the same values.
func TestSynthExprMath_FieldNamedLikeABuiltinStillReadsTheField(t *testing.T) {
	for _, name := range []string{"exp", "log", "sqrt", "hill", "sin", "pi", "e", "row", "lag", "now"} {
		t.Run(name, func(t *testing.T) {
			spec := &synth.Spec{
				RowCount: 50,
				Fields: []synth.FieldSpec{
					{Name: name, Type: "f64", Distribution: synth.DistUniform,
						Params: map[string]any{"min": 0.0, "max": 4.0}},
					{Name: "y", Type: "f64", Distribution: synth.DistConstant,
						Params: map[string]any{"value": -1.0}},
				},
				Rules:       []synth.RuleSpec{{When: name + " >= 1", SetExpr: map[string]string{"y": name + " * 2"}}},
				Constraints: []synth.ConstraintSpec{{Expr: name + " < 3.5"}},
			}
			data, _, err := synth.SynthBytes(spec, synth.Options{Seed: 5})
			if err != nil {
				t.Fatalf("SynthBytes: %v", err)
			}
			vs, ys := readF64Field(t, data, name), readF64Field(t, data, "y")
			for i, v := range vs {
				if v >= 3.5 {
					t.Fatalf("row %d: constraint over field %s let %v through", i, name, v)
				}
				want := -1.0
				if v >= 1 {
					want = v * 2
				}
				if ys[i] != want {
					t.Fatalf("row %d: y = %v, want %v (field %s = %v)", i, ys[i], want, name, v)
				}
			}
		})
	}
}

// expectCode parses a spec document and asserts its refusal code and
// that the message contains every fragment.
func expectCode(t *testing.T, doc string, code errors.Code, fragments ...string) *errors.CodedError {
	t.Helper()
	_, err := synth.ParseSpec([]byte(doc))
	if err == nil {
		t.Fatalf("ParseSpec accepted %s", doc)
	}
	ce, ok := err.(*errors.CodedError)
	if !ok {
		t.Fatalf("ParseSpec returned %T (%v), want *errors.CodedError", err, err)
	}
	if ce.Code != code {
		t.Fatalf("code = %s (%s), want %s", ce.Code, ce.Message, code)
	}
	for _, f := range fragments {
		if !strings.Contains(ce.Message, f) {
			t.Errorf("message %q lacks %q", ce.Message, f)
		}
	}
	return ce
}

const collisionFields = `"fields": [
	{"name": "x", "type": "f64", "distribution": "uniform", "params": {"min": 0, "max": 1}},
	{"name": "y", "type": "f64", "distribution": "uniform", "params": {"min": 0, "max": 1}},
	{"name": "log", "type": "f64", "distribution": "uniform", "params": {"min": 0, "max": 1}},
	{"name": "lag", "type": "f64", "distribution": "uniform", "params": {"min": 0, "max": 1}}
]`

func specDoc(tail string) string {
	return `{"row_count": 10, ` + collisionFields + `, ` + tail + `}`
}

// TestSynthExprMath_CallingAShadowedBuiltinIsRefused: the other half of
// the precedence — with a field `log`, log(x) cannot reach the built-in
// and says so, in every slot.
func TestSynthExprMath_CallingAShadowedBuiltinIsRefused(t *testing.T) {
	for _, tail := range []string{
		`"rules": [{"when": "log(x) > 0", "set": {"y": 1}}]`,
		`"rules": [{"set_expr": {"y": "log(x)"}}]`,
		`"rules": [{"set_expr": {"y": "lag(\"x\", 1)"}}]`,
	} {
		ce := expectCode(t, specDoc(tail), errors.PULSE_SYNTH_RULE_EXPR_INVALID, "rename the field")
		if ce.Details["function"] == nil {
			t.Errorf("%s: details lack function: %v", tail, ce.Details)
		}
	}
	expectCode(t, specDoc(`"constraints": [{"expr": "log(x) > 0"}]`), errors.SERVICE_VALIDATION, "rename the field")
}

// TestSynthExprMath_BareBuiltinWithoutFieldIsRefused: expr-lang compiles
// a bare function name to a nil value, so `pi` meaning pi() must be a
// refusal (it was "unknown name" before the built-ins existed), never a
// silently written null-shaped value.
func TestSynthExprMath_BareBuiltinWithoutFieldIsRefused(t *testing.T) {
	expectCode(t, specDoc(`"rules": [{"set_expr": {"y": "pi * x"}}]`), errors.PULSE_SYNTH_RULE_EXPR_INVALID, "bare pi", "pi(...)")
	expectCode(t, specDoc(`"rules": [{"when": "sin > 0", "set": {"y": 1}}]`), errors.PULSE_SYNTH_RULE_EXPR_INVALID, "bare sin")
	expectCode(t, specDoc(`"constraints": [{"expr": "x < e"}]`), errors.SERVICE_VALIDATION, "bare e")
}

// TestSynthExprMath_NowIsRefusedAtSpecValidation: now() is not in the
// row environment, and every slot refuses it with the dedicated code
// before a row exists.
func TestSynthExprMath_NowIsRefusedAtSpecValidation(t *testing.T) {
	cases := []struct {
		tail, slot string
	}{
		{`"rules": [{"when": "now().Year() > 2000", "set": {"y": 1}}]`, "when"},
		{`"rules": [{"set_expr": {"y": "x + now().Unix()"}}]`, "set_expr"},
		{`"constraints": [{"expr": "now().Unix() > 0"}]`, ""},
	}
	for _, c := range cases {
		ce := expectCode(t, specDoc(c.tail), errors.PULSE_SYNTH_EXPR_NONDETERMINISTIC, "now()")
		if ce.Details["function"] != "now" || ce.Details["expr"] == nil {
			t.Errorf("%s: details = %v", c.tail, ce.Details)
		}
		if c.slot != "" && ce.Details[errors.DetailSynthRuleSlot] != c.slot {
			t.Errorf("%s: slot = %v, want %s", c.tail, ce.Details[errors.DetailSynthRuleSlot], c.slot)
		}
	}
	// `now` inside a string literal is text, not a call.
	if _, err := synth.ParseSpec([]byte(specDoc(`"rules": [{"when": "\"now()\" != \"\"", "set": {"y": 1}}]`))); err != nil {
		t.Fatalf("a string literal mentioning now() was refused: %v", err)
	}
}

// TestSynthExprMath_SpecWithoutNewFunctionsIsByteIdentical pins a spec
// that exercises the row environment through rules and constraints with
// ONLY pre-U58 vocabulary (stock built-ins, isnull, arithmetic,
// comparisons) to the bytes it generated before the shared maths was
// added, so the wider environment provably changes nothing it does not
// name.
func TestSynthExprMath_SpecWithoutNewFunctionsIsByteIdentical(t *testing.T) {
	spec := preU58RowEnvSpec()
	data, _, err := synth.SynthBytes(spec, synth.Options{Seed: 4242})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	again, _, err := synth.SynthBytes(spec, synth.Options{Seed: 4242})
	if err != nil {
		t.Fatalf("SynthBytes again: %v", err)
	}
	if !bytes.Equal(data, again) {
		t.Fatal("two runs differ")
	}
	if got := sha256Hex(data); got != preSharedMathSpecHash {
		t.Fatalf("a spec using no new function changed bytes\n got %s\nwant %s", got, preSharedMathSpecHash)
	}
	// The one column E2-S2 moved: `id` is monotonic_from under a
	// rejecting constraint, and now has no gaps.
	for i, id := range readF64Field(t, data, "id") {
		if id != float64(1+i) {
			t.Fatalf("row %d: id = %v, want %d", i, id, 1+i)
		}
	}
}

// preU58RowEnvSpec exercises the row environment through rules and a
// rejecting constraint with ONLY pre-U58 vocabulary. Pinned at seed
// 4242 to preSharedMathSpecHash.
func preU58RowEnvSpec() *synth.Spec {
	spec := ruleFreeSpec()
	// uniform, not normal: like ruleFreeSpec, the pin predates portable
	// variates, and keeping it free of them keeps it comparable.
	spec.Fields = append(spec.Fields, synth.FieldSpec{Name: "score", Type: "f64", Distribution: synth.DistUniform,
		Params: map[string]any{"min": 0.0, "max": 100.0}})
	spec.Rules = []synth.RuleSpec{
		{When: "!isnull(perception)", SetExpr: map[string]string{"perception": "round(perception)"}},
		{When: "nps >= 9 && region == \"east\"", SetExpr: map[string]string{"score": "abs(score - 50) * 2 + max(nps, 3)"}},
	}
	spec.Constraints = []synth.ConstraintSpec{{Expr: "score < 90"}}
	return spec
}

// preSharedMathSpecHash is the sha256 of the spec above at seed 4242.
// E2-S1 took it on the tree BEFORE rowExprOptions gained
// exprenv.MathOptions (530d6ca8…). E2-S2 re-took it once monotonic_from
// began counting ACCEPTED rows: this spec's constraint rejects, so its
// `id` column lost its gaps — and decoding both files showed `id` was
// the ONLY column that differed, so the maths pin still holds for every
// drawn and rule-written value. Like preRuleApplySpecHash, it rides
// no variate or transcendental beyond what that pin already trusts
// across architectures.
const preSharedMathSpecHash = "935c3bb50cac2c031e3cb06ac96ef2d5ad949f4e7ba6c9544791ee77712fa682"

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
