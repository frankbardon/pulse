package synth

import (
	"math"
	"math/rand"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/detmath"
)

// TestSynthExprMath_EagerAndCompileRefusalsAgree: a Spec that bypasses
// validateSpec (the SpecFromProfile shape) still meets the now() refusal
// with the SAME code and details at compileRules / compileConstraints as
// at validation — the synth twin of predict/runtime parity.
func TestSynthExprMath_EagerAndCompileRefusalsAgree(t *testing.T) {
	ruled := ruleSpecFixture(RuleSpec{When: "now().Unix() > 0", SetNull: []string{"score"}})
	validateErr := validateRules(ruled)
	_, wfs, err := buildSchema(ruled)
	if err != nil {
		t.Fatalf("buildSchema: %v", err)
	}
	_, _, compileErr := compileRules(ruled.Rules, wfs)

	constrained := ruleSpecFixture()
	constrained.Constraints = []ConstraintSpec{{Expr: "score < now().Unix()"}}
	specErr := validateSpec(constrained)
	_, cwfs, err := buildSchema(constrained)
	if err != nil {
		t.Fatalf("buildSchema: %v", err)
	}
	_, consErr := compileConstraints(constrained.Constraints, cwfs)

	for _, pair := range [][2]struct {
		who string
		err error
	}{
		{{"validateRules", validateErr}, {"compileRules", compileErr}},
		{{"validateSpec", specErr}, {"compileConstraints", consErr}},
	} {
		var codes [2]*errors.CodedError
		for i, side := range pair {
			ce, ok := side.err.(*errors.CodedError)
			if !ok {
				t.Fatalf("%s returned %T (%v), want a *errors.CodedError", side.who, side.err, side.err)
			}
			if ce.Code != errors.PULSE_SYNTH_EXPR_NONDETERMINISTIC {
				t.Fatalf("%s: code %s, want PULSE_SYNTH_EXPR_NONDETERMINISTIC", side.who, ce.Code)
			}
			codes[i] = ce
		}
		if codes[0].Message != codes[1].Message {
			t.Errorf("%s / %s messages differ:\n %q\n %q", pair[0].who, pair[1].who, codes[0].Message, codes[1].Message)
		}
		for k, v := range codes[0].Details {
			if codes[1].Details[k] != v {
				t.Errorf("%s / %s detail %q: %v vs %v", pair[0].who, pair[1].who, k, v, codes[1].Details[k])
			}
		}
	}
}

// TestShapeFit_RunsOnDetmath: the --fit-shape density and likelihood
// evaluate exp / log through internal/detmath, whose bits are the same
// on every architecture, not through math.Exp / math.Log, whose are not.
// Checked over enough inputs that the stdlib kernels disagree with
// detmath on some of them on this machine (arm64: ~5e-4 of Exp inputs,
// ~2e-3 of Log inputs), so a revert to math.* fails here.
func TestShapeFit_RunsOnDetmath(t *testing.T) {
	r := rand.New(rand.NewSource(9))
	for i := 0; i < 200000; i++ {
		x := float64(r.Float64()*40) - 20
		mean := float64(r.Float64()*10) - 5
		std := 0.2 + float64(r.Float64()*5)
		variance := float64(std * std)
		want := detmath.Exp(-float64((x-mean)*(x-mean))/(2*variance)) / math.Sqrt(2*math.Pi*variance)
		if got := normalPDF(x, mean, std); math.Float64bits(got) != math.Float64bits(want) {
			t.Fatalf("normalPDF(%v, %v, %v) = %x, want detmath's %x", x, mean, std, math.Float64bits(got), math.Float64bits(want))
		}
		wantLL := float64(-0.5*detmath.Log(2*math.Pi*variance)) - (float64((x-mean)*(x-mean)))/(2*variance)
		if got := normalLogLikelihood([]float64{x}, mean, std); math.Float64bits(got) != math.Float64bits(0+wantLL) {
			t.Fatalf("normalLogLikelihood(%v, %v, %v) = %x, want detmath's %x", x, mean, std, math.Float64bits(got), math.Float64bits(wantLL))
		}
	}
}

// TestSynthRow_DetectorsCompileAgainstTheLoaderEnvironment pins the
// --suggest-rules decision for row(): both detectors only COMPILE a
// candidate, to check the loader will accept it, and never run one over
// captured records — so row() compiles there exactly as it does in a
// spec (no record-index meaning to choose), and bad arity is refused the
// same way.
func TestSynthRow_DetectorsCompileAgainstTheLoaderEnvironment(t *testing.T) {
	gate := (&gateDetector{schema: &encoding.Schema{Fields: []encoding.Field{
		{Name: "x", Type: encoding.FieldTypeU4},
	}}}).predicateCompiler()
	if err := gate("row() % 52 == 0 && x > 1"); err != nil {
		t.Fatalf("gate detector refused row(): %v", err)
	}
	if err := gate("row(1) > 0"); err == nil {
		t.Fatal("gate detector compiled row(1)")
	}
	dep := (&depDetector{fields: []depField{{name: "x", typ: "u4"}, {name: "y", typ: "u4"}}}).exprChecker()
	if err := dep("y", "row() % 2"); err != nil {
		t.Fatalf("dependency detector refused row(): %v", err)
	}
	if err := dep("y", "row(1)"); err == nil {
		t.Fatal("dependency detector compiled row(1)")
	}
}
