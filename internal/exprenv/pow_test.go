package exprenv

import (
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/expr-lang/expr"
	exprtypes "github.com/expr-lang/expr/types"
	"github.com/expr-lang/expr/vm"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/detmath"
)

// powTestSchema: two floats (x nullable), a u32, a nullable decimal, a
// categorical and a vector — every operand class `**` can meet.
func powTestSchema() *encoding.Schema {
	dict := encoding.NewDictionary()
	_, _ = dict.Add("a")
	return &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "x", Type: encoding.FieldTypeF64, Nullable: true},
			{Name: "y", Type: encoding.FieldTypeF64},
			{Name: "n", Type: encoding.FieldTypeU32},
			{Name: "d", Type: encoding.FieldTypeDecimal128, Precision: 38, Scale: 2, Nullable: true},
			{Name: "cat", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict},
			{Name: "emb", Type: encoding.FieldTypeVecF64, Nullable: true},
		},
		Vectors: []encoding.VectorField{{FieldIndex: 5, Dim: 3}},
	}
}

// powCompile compiles src typed (the schema prototype) or as the
// null-row twin (every name through Untyped), with or without the pow
// patcher.
func powCompile(s *encoding.Schema, src string, twin, withPow bool) (*vm.Program, error) {
	decl := Prototype(s)
	if twin {
		u := exprtypes.Map{}
		for k, v := range decl {
			u[k] = Untyped(v)
		}
		decl = u
	}
	return expr.Compile(src, append([]expr.Option{expr.Env(decl)}, options(withPow)...)...)
}

func powEnvs(t *testing.T, s *encoding.Schema) map[string]map[string]any {
	t.Helper()
	dec, sc, err := encoding.ParseDecimal128("1.50")
	if err != nil {
		t.Fatal(err)
	}
	if dec, err = dec.Rescale(sc, s.Field("d").Scale); err != nil {
		t.Fatal(err)
	}
	full := map[string]any{"x": 1.7, "y": 2.5, "n": 3.0, "d": Bind(s.Field("d"), dec), "cat": "a", "emb": []float64{1, 2, 3}}
	null := map[string]any{"x": nil, "y": 2.5, "n": 3.0, "d": nil, "cat": "a", "emb": nil}
	return map[string]map[string]any{"full": full, "null": null}
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// TestPow_SemanticsMatchExprLang: with the patcher, every `**` / `^`
// expression compiles (or refuses) with expr-lang's own message, runs to
// a result of the same static and run-time type, and fails a row with the same evaluation
// error text — typed and null-row twin, full and null rows. Only the
// value's last bits may differ (detmath.Pow vs math.Pow).
func TestPow_SemanticsMatchExprLang(t *testing.T) {
	s := powTestSchema()
	envs := powEnvs(t, s)
	for _, src := range []string{
		"x ** 2", "x ^ 2", "y ** x", "n ** 2", "2 ** 3", "2 ^ 3", "(-8) ** (1/3)",
		"2 ** n", "(x ?? 0) ** 2", "(x ?? 4) ^ 0.5", "x ** 2 > 2", "x ** y ** 2",
		"-x ** 2", "(n ** 2) % 4", "x ** 2 + y", "int(n ** 2)",
		"nil ** 2", "x ** nil", "x ** 'a'", "cat ** 2", "d ** 2", "(d ?? 0) ** 2",
		"emb ** 2", "x ** 2 + 'a'", "len('ab') ** len('abc')", "x ** 2 == nil",
	} {
		for _, twin := range []bool{false, true} {
			got, gotErr := powCompile(s, src, twin, true)
			base, baseErr := powCompile(s, src, twin, false)
			if errText(gotErr) != errText(baseErr) {
				t.Errorf("%q twin=%v: compile error\n got: %v\nwant: %v", src, twin, gotErr, baseErr)
				continue
			}
			if gotErr != nil {
				continue
			}
			if gt, bt := got.Node().Type(), base.Node().Type(); gt != bt {
				t.Errorf("%q twin=%v: static result type %v, want %v", src, twin, gt, bt)
			}
			for name, env := range envs {
				g, gErr := expr.Run(got, env)
				b, bErr := expr.Run(base, env)
				if errText(gErr) != errText(bErr) {
					t.Errorf("%q twin=%v %s: run error\n got: %v\nwant: %v", src, twin, name, gErr, bErr)
					continue
				}
				if reflect.TypeOf(g) != reflect.TypeOf(b) {
					t.Errorf("%q twin=%v %s: result type %T, want %T", src, twin, name, g, b)
					continue
				}
				gf, ok1 := g.(float64)
				bf, ok2 := b.(float64)
				if ok1 && ok2 {
					if !(math.IsNaN(gf) && math.IsNaN(bf)) && math.Abs(gf-bf) > 1e-12*math.Max(1, math.Abs(bf)) {
						t.Errorf("%q twin=%v %s: %v, want ~%v", src, twin, name, gf, bf)
					}
				} else if !reflect.DeepEqual(g, b) {
					t.Errorf("%q twin=%v %s: %v, want %v", src, twin, name, g, b)
				}
			}
		}
	}
}

// TestPow_NilOperandErrorText pins the text a null operand raises:
// expr-lang's own conversion failure, at the operator's position, not
// pow()'s coded PROCESSING_RUNTIME argument refusal.
func TestPow_NilOperandErrorText(t *testing.T) {
	s := powTestSchema()
	p, err := powCompile(s, "y + x ** 2", true, true)
	if err != nil {
		t.Fatal(err)
	}
	_, err = expr.Run(p, powEnvs(t, s)["null"])
	const want = "invalid operation: float(<nil>) (1:7)\n | y + x ** 2\n | ......^"
	if err == nil || err.Error() != want {
		t.Fatalf("err %q, want %q", errText(err), want)
	}
}

// TestPow_IntOperandsStayFloat: int ** int is float64, as expr-lang's
// runtime.Exponent always answers, and exact for integer powers.
func TestPow_IntOperandsStayFloat(t *testing.T) {
	for src, want := range map[string]float64{"2 ** 10": 1024, "3 ^ 4": 81, "2 ** -1": 0.5, "len('abc') ** 2": 9, "2 ** 62": 1 << 62} {
		v, err := expr.Eval(src, nil)
		if err != nil {
			t.Fatal(err)
		}
		p, err := expr.Compile(src, Options()...)
		if err != nil {
			t.Fatal(err)
		}
		got, err := expr.Run(p, nil)
		if err != nil {
			t.Fatal(err)
		}
		if reflect.TypeOf(got) != reflect.TypeOf(v) || got != want {
			t.Errorf("%s = %v (%T); want %v (%T)", src, got, got, want, v)
		}
	}
}

// TestPow_OperatorBitEqualsPowBuiltin: `x ** y` and `x ^ y` give
// pow(x, y)'s bits (detmath.Pow) over a deterministic sweep — the
// operator no longer rides math.Pow.
func TestPow_OperatorBitEqualsPowBuiltin(t *testing.T) {
	s := powTestSchema()
	ops := map[string]*vm.Program{}
	for _, src := range []string{"x ** y", "x ^ y", "pow(x, y)", "(x ?? 0) ** y"} {
		for _, twin := range []bool{false, true} {
			p, err := powCompile(s, src, twin, true)
			if err != nil {
				t.Fatal(err)
			}
			key := src
			if twin {
				key += " twin"
			}
			ops[key] = p
		}
	}
	z := uint64(0x9E3779B97F4A7C15)
	next := func() float64 {
		z += 0x9E3779B97F4A7C15
		v := z
		v = (v ^ (v >> 30)) * 0xBF58476D1CE4E5B9
		v = (v ^ (v >> 27)) * 0x94D049BB133111EB
		v ^= v >> 31
		return float64(v>>11) / (1 << 53)
	}
	for i := range 4000 {
		x := float64(next()*200) - 100
		y := float64(next()*20) - 10
		if i%4 == 0 {
			y = math.Round(y) // integer exponents: negative bases stay finite
		}
		env := map[string]any{"x": x, "y": y}
		want := math.Float64bits(detmath.Pow(x, y))
		for key, p := range ops {
			v, err := expr.Run(p, env)
			if err != nil {
				t.Fatalf("%s at (%v, %v): %v", key, x, y, err)
			}
			if got := math.Float64bits(v.(float64)); got != want {
				t.Fatalf("%s at (%v, %v): bits %#x, want detmath.Pow %#x", key, x, y, got, want)
			}
		}
	}
}

// TestPow_BothSpellingsRewritten: the compiled program calls the
// detmath builtin, never expr-lang's OpExponent.
func TestPow_BothSpellingsRewritten(t *testing.T) {
	s := powTestSchema()
	for _, src := range []string{"x ** 2", "x ^ 2", "(x ?? 1) ** y"} {
		for _, twin := range []bool{false, true} {
			p, err := powCompile(s, src, twin, true)
			if err != nil {
				t.Fatal(err)
			}
			if d := p.Disassemble(); strings.Contains(d, "OpExponent") {
				t.Errorf("%q twin=%v still runs OpExponent:\n%s", src, twin, d)
			}
		}
	}
}
