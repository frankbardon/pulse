package guide

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/types"
)

// hidingInstance returns an instance offering every feature but hide.
func hidingInstance(hide ...string) *descx.InstanceSnapshot {
	var set descx.FeatureSet
	for _, n := range descx.FeatureNames() {
		if slices.Contains(hide, n) {
			set.Hidden = append(set.Hidden, n)
		} else {
			set.Enabled = append(set.Enabled, n)
		}
	}
	return descx.NewInstanceSnapshot(nil, set)
}

func explainOp(t *testing.T, inst *descx.InstanceSnapshot, op string, d descriptor.ExplainDetail) *descriptor.ExplainResult {
	t.Helper()
	return explain(t, inst, descriptor.ExplainRequest{Operator: op, Detail: d}, Checks{})
}

// TestExplainOperator_ReturnsTheRegistryView: operator mode returns the
// instance's Validation view of the operator — the record the book
// renders — with mode and root "operator", no steps and an empty
// findings array; full detail adds the operator's glossary terms.
func TestExplainOperator_ReturnsTheRegistryView(t *testing.T) {
	t.Parallel()

	inst := hidingInstance()
	for _, op := range descx.ValidationOperators() {
		res := explainOp(t, inst, op, "")
		want, _ := inst.Validation(op)
		if res.Validation == nil || !reflect.DeepEqual(*res.Validation, want) {
			t.Errorf("%s: validation = %+v, want %+v", op, res.Validation, want)
		}
		if res.Mode != descriptor.ExplainModeOperator || res.Root != descriptor.ExplainRootOperator ||
			res.Findings == nil || len(res.Findings) != 0 || len(res.Steps) != 0 {
			t.Errorf("%s: mode %q root %q findings %v steps %v", op, res.Mode, res.Root, res.Findings, res.Steps)
		}
		if !strings.HasPrefix(res.Summary, op+" ") {
			t.Errorf("%s: summary %q", op, res.Summary)
		}
		if (want.Gap != "") != strings.Contains(strings.Join(res.Caveats, " "), "validation gap") {
			t.Errorf("%s: gap %q but caveats %v", op, want.Gap, res.Caveats)
		}
	}
	full := explainOp(t, inst, "TEST_T", descriptor.ExplainFull)
	if len(full.GlossaryRefs) == 0 || len(full.Sentences) == 0 {
		t.Errorf("full detail: glossary %v sentences %v", full.GlossaryRefs, full.Sentences)
	}
}

// TestExplainOperator_Certification: the NIST StRD rows reach the
// operators they check, each holding only the figures that operator
// produces (the univariate suite certifies the mean for AGG_AVERAGE
// and the sample SD for AGG_STDDEV on one row).
func TestExplainOperator_Certification(t *testing.T) {
	t.Parallel()

	inst := hidingInstance()
	for op, want := range map[string]struct {
		family  string
		sets    int
		figures []string
	}{
		"REG_OLS":      {"linear regression", 11, nil},
		"TEST_ANOVA_F": {"one-way ANOVA", 11, nil},
		"AGG_AVERAGE":  {"univariate summary statistics", 9, []string{"mean"}},
		"AGG_STDDEV":   {"univariate summary statistics", 9, []string{"sd"}},
	} {
		v := explainOp(t, inst, op, "").Validation
		if v == nil || len(v.Certification) != want.sets {
			t.Fatalf("%s: certification %+v, want %d datasets", op, v, want.sets)
		}
		for _, c := range v.Certification {
			if c.Family != want.family || len(c.Digits) == 0 {
				t.Errorf("%s: %+v", op, c)
			}
			least := 16.0
			for k, d := range c.Digits {
				least = min(least, d)
				if want.figures != nil && !slices.Contains(want.figures, k) {
					t.Errorf("%s %s: figure %s is not one it produces", op, c.Dataset, k)
				}
			}
			if c.MinDigits != least {
				t.Errorf("%s %s: min_digits %v, want %v", op, c.Dataset, c.MinDigits, least)
			}
		}
	}
	if v := explainOp(t, inst, "TEST_T", "").Validation; len(v.Certification) != 0 {
		t.Errorf("TEST_T: certification %+v, want none", v.Certification)
	}
}

// TestExplainOperator_WeightingHidden: an instance without weighting
// returns no weighted reference and no weight refusal.
func TestExplainOperator_WeightingHidden(t *testing.T) {
	t.Parallel()

	on, off := hidingInstance(), hidingInstance(descx.FeatureWeighting)
	if v := explainOp(t, on, "TEST_ANOVA_RM", "").Validation; v.WeightRefusal == nil || v.WeightRefusal.Reason == "" ||
		v.WeightRefusal.Code != string(errors.PULSE_WEIGHT_UNSUPPORTED) {
		t.Errorf("weighting offered: weight refusal %+v", v.WeightRefusal)
	}
	if v := explainOp(t, on, "TEST_TUKEY_HSD", "").Validation; v.WeightRefusal == nil || v.WeightRefusal.Reason != descx.PostTestWeightRefusal() {
		t.Errorf("post-test-only: weight refusal %+v, want the shared post-test reason", v.WeightRefusal)
	}
	res := explainOp(t, off, "TEST_ANOVA_RM", "")
	if res.Validation.WeightRefusal != nil || strings.Contains(strings.Join(res.Caveats, " "), "weight") {
		t.Errorf("weighting hidden: refusal %+v, caveats %v", res.Validation.WeightRefusal, res.Caveats)
	}
	for _, r := range explainOp(t, off, "TEST_ANOVA_F", "").Validation.References {
		if r.Weighted {
			t.Errorf("weighting hidden: weighted reference %q", r.Against)
		}
	}
}

// TestExplainOperator_Refusals: an operator the instance does not offer
// (unknown, or hidden by its feature profile) and an operator set beside
// another root are SERVICE_VALIDATION on details.field "operator".
func TestExplainOperator_Refusals(t *testing.T) {
	t.Parallel()

	for name, c := range map[string]struct {
		inst *descx.InstanceSnapshot
		req  descriptor.ExplainRequest
	}{
		"unknown":     {hidingInstance(), descriptor.ExplainRequest{Operator: "TEST_NOPE"}},
		"hidden":      {hidingInstance("TEST_WELCH"), descriptor.ExplainRequest{Operator: "TEST_WELCH"}},
		"beside root": {hidingInstance(), descriptor.ExplainRequest{Operator: "TEST_T", Response: &types.Response{}}},
	} {
		_, err := Explain(c.inst, c.req, Checks{})
		ce, ok := err.(*errors.CodedError)
		if !ok || ce.Code != errors.SERVICE_VALIDATION || ce.Details["field"] != "operator" {
			t.Errorf("%s: err = %v, want SERVICE_VALIDATION on operator", name, err)
		}
	}
}

// TestExplainOperator_Unrecorded: an operator the registry does not
// cover has no validation block, and a caveat says nothing here
// describes how it was checked.
func TestExplainOperator_Unrecorded(t *testing.T) {
	t.Parallel()

	res := explainOp(t, hidingInstance(), "AGG_SUM", "")
	if res.Validation != nil || len(res.Caveats) != 1 || !strings.Contains(res.Summary, "no validation record") {
		t.Errorf("AGG_SUM: validation %+v, summary %q, caveats %v", res.Validation, res.Summary, res.Caveats)
	}
}

// TestExplainOperator_ProseLint: every operator-mode sentence, for
// every recorded operator and one unrecorded, in both details, passes
// the guidance prose lint.
func TestExplainOperator_ProseLint(t *testing.T) {
	t.Parallel()

	inst := hidingInstance()
	n := 0
	for _, op := range append(descx.ValidationOperators(), "AGG_SUM") {
		for _, d := range descriptor.ExplainDetails() {
			res := explainOp(t, inst, op, d)
			texts := append(append([]string{res.Summary}, res.Sentences...), res.Caveats...)
			for _, s := range texts {
				n++
				for _, h := range descx.LintGuidanceText(s) {
					t.Errorf("%s %s: lint %s on %q in %q", op, d, h.Rule, h.Span, s)
				}
			}
		}
	}
	if n < 100 {
		t.Fatalf("vacuous: %d texts", n)
	}
}
