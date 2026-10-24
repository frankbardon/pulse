package synth

import (
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
)

// TestSynthLag_EagerAndCompileRefusalsAgree: a Spec that bypasses
// validateSpec still meets each lag refusal with the SAME code, message
// and details at compileRules / compileConstraints as at validation.
func TestSynthLag_EagerAndCompileRefusalsAgree(t *testing.T) {
	for _, tc := range []struct {
		call string
		code errors.Code
	}{
		{`lag("nope", 1, 0)`, errors.PULSE_SYNTH_LAG_FIELD_INVALID},
		{`lag("score", 2000, 0)`, errors.PULSE_SYNTH_LAG_OFFSET_INVALID},
	} {
		ruled := ruleSpecFixture(RuleSpec{SetExpr: map[string]string{"score": tc.call + " + 1"}})
		validateErr := validateRules(ruled)
		_, wfs, err := buildSchema(ruled)
		if err != nil {
			t.Fatalf("buildSchema: %v", err)
		}
		_, _, compileErr := compileRules(ruled.Rules, wfs)

		constrained := ruleSpecFixture()
		constrained.Constraints = []ConstraintSpec{{Expr: tc.call + " > 0"}}
		specErr := validateSpec(constrained)
		_, cwfs, err := buildSchema(constrained)
		if err != nil {
			t.Fatalf("buildSchema: %v", err)
		}
		_, consErr := compileConstraints(constrained.Constraints, cwfs)

		for _, pair := range [][2]error{{validateErr, compileErr}, {specErr, consErr}} {
			var ces [2]*errors.CodedError
			for i, e := range pair {
				ce, ok := e.(*errors.CodedError)
				if !ok || ce.Code != tc.code {
					t.Fatalf("%s: side %d returned %v, want %s", tc.call, i, e, tc.code)
				}
				ces[i] = ce
			}
			if ces[0].Message != ces[1].Message {
				t.Errorf("%s: messages differ:\n %q\n %q", tc.call, ces[0].Message, ces[1].Message)
			}
			for k, v := range ces[0].Details {
				if ces[1].Details[k] != v {
					t.Errorf("%s: detail %q: %v vs %v", tc.call, k, v, ces[1].Details[k])
				}
			}
		}
	}
}

// TestSynthLag_DetectorsCompileAgainstTheLoaderEnvironment: the
// --suggest-rules detectors only COMPILE candidates against the row
// environment (they never run one over captured records, so lag has no
// cross-record meaning to choose there). A well-formed lag compiles, and
// every shape the loader refuses fails to compile — the bare `lag` is not
// registered, so a call the patcher did not rewrite cannot slip through
// a path that skips rowExprStaticRefusal.
func TestSynthLag_DetectorsCompileAgainstTheLoaderEnvironment(t *testing.T) {
	gate := (&gateDetector{schema: &encoding.Schema{Fields: []encoding.Field{
		{Name: "x", Type: encoding.FieldTypeU4},
	}}}).predicateCompiler()
	dep := (&depDetector{fields: []depField{{name: "x", typ: "u4"}, {name: "y", typ: "u4"}}}).exprChecker()
	if err := gate(`lag("x", 1, 0) > 1`); err != nil {
		t.Fatalf("gate detector refused a valid lag: %v", err)
	}
	if err := dep("y", `lag("x", 2, 0) + 1`); err != nil {
		t.Fatalf("dependency detector refused a valid lag: %v", err)
	}
	for _, bad := range []string{`lag("nope", 1, 0)`, `lag(x, 1, 0)`, `lag("x", 0, 0)`, `lag("x", 1025, 0)`, `lag("x", 1.5, 0)`, `lag("x")`} {
		if err := gate(bad + " > 1"); err == nil {
			t.Errorf("gate detector compiled %s", bad)
		}
		if err := dep("y", bad+" + 1"); err == nil {
			t.Errorf("dependency detector compiled %s", bad)
		}
	}
}

// TestSynthLag_SelfLagClaimsItsTarget: a set_expr computing a field from
// its own PREVIOUS row determines the field — the current draw is never
// read — so it pre-claims it (rules_claim.go) like any unconditional
// value rule, retiring its model and pairs. Reading the CURRENT draw
// beside the lag still transforms it, and does not claim.
func TestSynthLag_SelfLagClaimsItsTarget(t *testing.T) {
	claims := ruleClaims([]RuleSpec{
		{SetExpr: map[string]string{"ad": `spend + 0.6 * lag("ad", 1, 0)`}},
		{SetExpr: map[string]string{"nps": `nps + lag("nps", 1, 0)`}},
	})
	if len(claims) != 1 || claims[0].target != (claimTarget{field: "ad"}) {
		t.Fatalf("claims = %+v, want exactly the self-lagged ad", claims)
	}
}

// TestSynthLag_BufferHoldsOnlyLaggedFieldsAndMaxN: the ring keeps only
// the fields some expression lags, sized to the largest n over rules and
// constraints, and is absent for a spec that never calls lag.
func TestSynthLag_BufferHoldsOnlyLaggedFieldsAndMaxN(t *testing.T) {
	spec := ruleSpecFixture(
		RuleSpec{When: `lag("nps", 3, 0) > 5`, SetNull: []string{"score"}},
		RuleSpec{SetExpr: map[string]string{"score": `lag("score", 2, 0) + lag("nps", 1, 0)`}},
	)
	spec.Constraints = []ConstraintSpec{{Expr: `lag("region", 9, "east") != ""`}}
	_, wfs, err := buildSchema(spec)
	if err != nil {
		t.Fatalf("buildSchema: %v", err)
	}
	b := newLagBuffer(spec, wfs)
	if b == nil || b.size != 9 {
		t.Fatalf("buffer = %+v, want size 9", b)
	}
	if got := len(b.fields); got != 3 || b.fields[0] != "nps" || b.fields[1] != "region" || b.fields[2] != "score" {
		t.Fatalf("lagged fields = %v, want [nps region score]", b.fields)
	}
	if newLagBuffer(ruleSpecFixture(RuleSpec{SetExpr: map[string]string{"score": "score + 1"}}), wfs) != nil {
		t.Fatal("a lag-free spec built a buffer")
	}
}

// TestSynthLag_BufferCopiesSelectionsAndVectors: a committed set
// selection or vector is the row's value AT COMMIT; a later in-place
// change to the row's map or slice does not reach the buffer.
func TestSynthLag_BufferCopiesSelectionsAndVectors(t *testing.T) {
	b := &lagBuffer{size: 1, fields: []string{"s", "v"}, slot: map[string]int{"s": 0, "v": 1},
		class: []lagClass{lagSet, lagVector}, vals: [][]any{make([]any, 1), make([]any, 1)}, nulls: [][]bool{make([]bool, 1), make([]bool, 1)}}
	sel, vec := map[string]bool{"a": true}, []float64{1, 2}
	b.commit(map[string]any{"s": sel, "v": vec}, nil)
	sel["b"] = true
	vec[0] = 9
	gotSel, _, _ := b.read("s", 1)
	gotVec, _, _ := b.read("v", 1)
	if m := gotSel.(map[string]bool); len(m) != 1 || !m["a"] {
		t.Fatalf("set = %v, want the committed {a}", m)
	}
	if v := gotVec.([]float64); v[0] != 1 {
		t.Fatalf("vector = %v, want the committed [1 2]", v)
	}
}
