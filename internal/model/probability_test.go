package model

import (
	"encoding/json"
	"math"
	"slices"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// probability_test.go pins the categorical-outcome scoring rule
// (checkOutcome inside CheckScoreFor) and the generic outcome column
// (OutcomeScorer) over a probability vector.

// outcomeCatalog binds "ord" (REG_ORDINAL, levels pinned lo/mid/hi),
// "ord_open" (REG_ORDINAL, levels left to the data), "ols" and "glm".
func outcomeCatalog() FitCatalog {
	return CatalogFromFits([]types.FitSpec{
		{Name: "ord", Request: olsReq(&types.RegressionSpec{Type: types.REG_ORDINAL, Target: "y", Predictors: []string{"x1"}, Params: json.RawMessage(`{"levels":["lo","mid","hi"]}`)})},
		{Name: "ord_open", Request: olsReq(&types.RegressionSpec{Type: types.REG_ORDINAL, Target: "y", Predictors: []string{"x1"}})},
		{Name: "ols", Request: olsReq(&types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: []string{"x1"}})},
		{Name: "glm", Request: olsReq(&types.RegressionSpec{Type: types.REG_GLM, Target: "y", Predictors: []string{"x1"}})},
	})
}

// ordinalScorable is the instance view with REG_ORDINAL scorable (the
// model-regression wiring supplies its scorer).
func ordinalScorable(t types.RegressionType) bool { return ScorableType(t) || t == types.REG_ORDINAL }

func TestCheckScore_Outcome(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, params string
		reason       string
		slot         string
	}{
		{"ordinal default (expected)", `{"fit":"ord"}`, "", ""},
		{"ordinal level (probability)", `{"fit":"ord","level":"mid"}`, "", ""},
		{"ordinal cumulative", `{"fit":"ord","level":"hi","output":"cumulative"}`, "", ""},
		{"ordinal expected", `{"fit":"ord","output":"expected"}`, "", ""},
		{"ordinal class", `{"fit":"ord","output":"class"}`, "", ""},
		{"ordinal link", `{"fit":"ord","scale":"link"}`, "", ""},
		{"ordinal response", `{"fit":"ord","scale":"response","level":"lo"}`, "", ""},
		{"open levels: the run judges", `{"fit":"ord_open","level":"anything"}`, "", ""},
		{"unknown level", `{"fit":"ord","level":"top"}`, ReasonOutcomeUnknown, "params.level"},
		{"probability without level", `{"fit":"ord","output":"probability"}`, ReasonOutcomeRequired, ""},
		{"cumulative without level", `{"fit":"ord","output":"cumulative"}`, ReasonOutcomeRequired, ""},
		{"level beside expected", `{"fit":"ord","level":"lo","output":"expected"}`, ReasonOutcomeUnused, "params.level"},
		{"level beside class", `{"fit":"ord","level":"lo","output":"class"}`, ReasonOutcomeUnused, "params.level"},
		{"unknown output", `{"fit":"ord","output":"median"}`, ReasonOutputUnknown, ""},
		{"output beside link", `{"fit":"ord","scale":"link","output":"expected"}`, ReasonOutputScale, ""},
		{"level beside link", `{"fit":"ord","scale":"link","level":"lo"}`, ReasonOutputScale, ""},
		{"category on ordinal", `{"fit":"ord","category":"lo"}`, ReasonOutcomeUnsupported, "params.category"},
		{"level on OLS", `{"fit":"ols","level":"lo"}`, ReasonOutcomeUnsupported, "params.level"},
		{"output on GLM", `{"fit":"glm","output":"probability"}`, ReasonOutcomeUnsupported, "params.output"},
		{"category on GLM", `{"fit":"glm","category":"a"}`, ReasonOutcomeUnsupported, "params.category"},
	}
	cat := outcomeCatalog()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := CheckScoreFor(scoreAttr(tc.params), cat, ordinalScorable)
			if tc.reason == "" {
				if err != nil {
					t.Fatalf("refused: %v", err)
				}
				return
			}
			if err == nil || err.Code != errors.PULSE_MODEL_SCORE_INVALID || err.Details["reason"] != tc.reason {
				t.Fatalf("err = %v, want %s", err, tc.reason)
			}
			if tc.slot != "" && err.Details["slot"] != tc.slot {
				t.Fatalf("details.slot %v, want %s (%v)", err.Details["slot"], tc.slot, err.Details)
			}
		})
	}
	// The requires vocabulary: level / output name REG_ORDINAL; no
	// scorable type takes category yet, so its refusal names none.
	_, _, err := CheckScoreFor(scoreAttr(`{"fit":"ols","output":"class"}`), cat, ordinalScorable)
	if err.Details["requires"] != "type REG_ORDINAL" {
		t.Errorf("output on OLS requires %v", err.Details["requires"])
	}
	_, _, err = CheckScoreFor(scoreAttr(`{"fit":"ord","category":"lo"}`), cat, ordinalScorable)
	if _, has := err.Details["requires"]; has {
		t.Errorf("category requires %v, want none", err.Details["requires"])
	}
	// An unknown level lists the fit's levels.
	_, _, err = CheckScoreFor(scoreAttr(`{"fit":"ord","level":"top"}`), cat, ordinalScorable)
	if got, _ := err.Details["available"].([]string); !slices.Equal(got, []string{"lo", "mid", "hi"}) {
		t.Errorf("available %v", err.Details["available"])
	}
	// Without the instance's scorer the ordinal fit is no_scorer, first.
	if _, _, err := CheckScore(scoreAttr(`{"fit":"ord","level":"lo"}`), cat); err == nil || err.Details["reason"] != ReasonNoScorer {
		t.Errorf("built-in view: %v, want no_scorer", err)
	}
}

// TestSignature_OutcomesRuntimeMatchesPredict: a bound REG_ORDINAL fit
// knows its levels from outputs.levels — the run's []string and a model
// file's []any alike — as predict's catalog knows pinned params.levels.
func TestSignature_OutcomesRuntimeMatchesPredict(t *testing.T) {
	t.Parallel()
	for _, levels := range []any{[]string{"lo", "mid", "hi"}, []any{"lo", "mid", "hi"}} {
		b := &Bound{Name: "ord", Type: types.REG_ORDINAL, Result: &types.RegressionResult{Outputs: map[string]any{"levels": levels}}}
		sig := b.Signature()
		if !sig.OutcomesKnown || !slices.Equal(sig.Outcomes, []string{"lo", "mid", "hi"}) {
			t.Errorf("%T levels: signature outcomes %v (known %v)", levels, sig.Outcomes, sig.OutcomesKnown)
		}
	}
	cat := outcomeCatalog()
	if s, _ := cat.Signature("ord"); !s.OutcomesKnown || !slices.Equal(s.Outcomes, []string{"lo", "mid", "hi"}) {
		t.Errorf("pinned levels: %v (known %v)", s.Outcomes, s.OutcomesKnown)
	}
	if s, _ := cat.Signature("ord_open"); s.OutcomesKnown {
		t.Errorf("open levels known statically: %v", s.Outcomes)
	}
	if s, _ := cat.Signature("ols"); s.OutcomesKnown || s.Outcomes != nil {
		t.Errorf("OLS has outcomes %v", s.Outcomes)
	}
}

// fixedVector is a ProbabilityScorer returning one vector; column "x"
// null makes it null.
type fixedVector []float64

func (v fixedVector) Columns() []string  { return []string{"x"} }
func (v fixedVector) Outcomes() []string { return []string{"a", "b", "c", "d"}[:len(v)] }
func (v fixedVector) Probabilities(row Row, out []float64) bool {
	if _, ok := row.NumericValue("x"); !ok {
		return false
	}
	copy(out, v)
	return true
}

type oneRow map[string]float64

func (r oneRow) NumericValue(n string) (float64, bool) { v, ok := r[n]; return v, ok }

func TestOutcomeScorer(t *testing.T) {
	t.Parallel()
	v := fixedVector{0.1, 0.5, 0.15, 0.25}
	row := oneRow{"x": 1}
	for _, tc := range []struct {
		output  string
		outcome int
		want    float64
	}{
		{OutputProbability, 1, 0.5},
		{OutputProbability, 3, 0.25},
		{OutputCumulative, 0, 0.1},
		{OutputCumulative, 2, 0.1 + 0.5 + 0.15},
		{OutputExpected, -1, 1*0.1 + 2*0.5 + 3*0.15 + 4*0.25},
		{OutputClass, -1, 2},
	} {
		rs, err := OutcomeScorer(v, tc.output, tc.outcome)
		if err != nil {
			t.Fatal(err)
		}
		if got, ok := rs.Score(row); !ok || math.Abs(got-tc.want) > 1e-15 {
			t.Errorf("%s(%d) = %v %v, want %v", tc.output, tc.outcome, got, ok, tc.want)
		}
		if _, ok := rs.Score(oneRow{}); ok {
			t.Errorf("%s: a null column scored", tc.output)
		}
	}
	// A tie is the first most probable outcome.
	rs, _ := OutcomeScorer(fixedVector{0.4, 0.4, 0.2}, OutputClass, -1)
	if got, _ := rs.Score(row); got != 1 {
		t.Errorf("tie class %v, want 1", got)
	}
	// More outcomes than the stack buffer.
	long := make(fixedVectorLong, 40)
	long[39] = 1
	rs, _ = OutcomeScorer(long, OutputExpected, -1)
	if got, _ := rs.Score(row); got != 40 {
		t.Errorf("long expected %v, want 40", got)
	}
	for _, bad := range []struct {
		output  string
		outcome int
	}{{OutputProbability, -1}, {OutputCumulative, 4}, {"median", 0}} {
		if _, err := OutcomeScorer(v, bad.output, bad.outcome); err == nil {
			t.Errorf("%s(%d) built", bad.output, bad.outcome)
		}
	}
}

type fixedVectorLong []float64

func (v fixedVectorLong) Columns() []string { return []string{"x"} }
func (v fixedVectorLong) Outcomes() []string {
	out := make([]string, len(v))
	for i := range out {
		out[i] = string(rune('A' + i))
	}
	return out
}
func (v fixedVectorLong) Probabilities(_ Row, out []float64) bool { copy(out, v); return true }
