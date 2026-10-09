package descriptor

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// The SPSS-sidecar advisories read measure levels and the weighting
// variable the facade hands down from the cohort's SPSS metadata
// sidecar (PredictOptions.SidecarMeasureLevels / SuggestedWeightVariable).
// No sidecar, no advisory: nothing is inferred from names or values.

// sidecarCohort: q a numeric nominal-coded field, o a u8 ordinal one,
// u a u4 ordinal one, s a numeric scale one, c a categorical (a labelled
// SPSS numeric imports so), g a 3-entry split, w a weight-capable f64.
func sidecarCohort(t *testing.T) []byte {
	t.Helper()
	return buildTestPulseFile(t, &encoding.Schema{Fields: []encoding.Field{
		{Name: "q", Type: encoding.FieldTypeF64, Description: "Region code of the respondent"},
		{Name: "o", Type: encoding.FieldTypeU8, Description: "Satisfaction rating from one to five"},
		{Name: "u", Type: encoding.FieldTypeU4, Description: "Agreement rating from one to seven"},
		{Name: "s", Type: encoding.FieldTypeF64, Description: "Household income in dollars"},
		{Name: "c", Type: encoding.FieldTypeCategoricalU8, Description: "Labelled answer to the survey question", Dictionary: makeDictionary(t, "yes", "no")},
		{Name: "g", Type: encoding.FieldTypeCategoricalU8, Description: "Arm the respondent was assigned to", Dictionary: makeDictionary(t, "a", "b", "c")},
		{Name: "w", Type: encoding.FieldTypeF64, Description: "Survey design weight per respondent"},
	}})
}

var sidecarMeasures = map[string]string{
	"q": "nominal", "o": "ordinal", "u": "ordinal", "s": "scale", "c": "nominal", "gone": "nominal",
}

func sidecarAdvisories(t *testing.T, req *types.Request, opts *PredictOptions) []descriptor.Advisory {
	t.Helper()
	if opts == nil {
		opts = &PredictOptions{}
	}
	if opts.SidecarMeasureLevels == nil {
		opts.SidecarMeasureLevels = sidecarMeasures
	}
	return predictFromBytes(sidecarCohort(t), req, opts).Data.(*descriptor.PredictResult).Advisories
}

type wantAdvisory struct {
	code, slot, field string
	suggested         any // nil = absent
}

func checkAdvisories(t *testing.T, got []descriptor.Advisory, want []wantAdvisory) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("advisories = %+v, want %d", got, len(want))
	}
	for i, w := range want {
		a := got[i]
		if a.Code != w.code || a.Details["slot"] != w.slot || a.Details["field"] != w.field {
			t.Errorf("advisory %d = %+v, want %s at %s on %s", i, a, w.code, w.slot, w.field)
		}
		if s, ok := a.Details["suggested"]; w.suggested == nil && ok || w.suggested != nil && s != w.suggested {
			t.Errorf("advisory %d suggested = %v (present %v), want %v", i, s, ok, w.suggested)
		}
		if w.suggested != nil && !strings.Contains(a.Message, w.suggested.(string)) {
			t.Errorf("advisory %d message %q does not name %v", i, a.Message, w.suggested)
		}
	}
}

// TestPredict_AdvisoryCategoricalAsNumeric: a nominal numeric field under
// a mean-family aggregator (any aggregation slot) or a parametric test
// fires; a sum, a scale field, a categorical-typed field, a field the
// schema no longer carries and a cohort without sidecar levels never do.
func TestPredict_AdvisoryCategoricalAsNumeric(t *testing.T) {
	code := string(errors.PULSE_ADVISORY_CATEGORICAL_AS_NUMERIC)
	agg := func(typ types.AggregationType, field string) *types.Aggregation {
		return &types.Aggregation{Type: typ, Field: field, Label: "v"}
	}
	for _, c := range []struct {
		name     string
		req      *types.Request
		measures map[string]string
		want     []wantAdvisory
	}{
		{"average of a nominal field", &types.Request{Aggregations: []*types.Aggregation{agg(types.AGG_AVERAGE, "q")}}, nil,
			[]wantAdvisory{{code, "aggregations[0]", "q", string(types.AGG_FREQUENCY)}}},
		{"stddev in the second slot", &types.Request{Aggregations: []*types.Aggregation{agg(types.AGG_COUNT, "q"), agg(types.AGG_STDDEV, "q")}}, nil,
			[]wantAdvisory{{code, "aggregations[1]", "q", string(types.AGG_FREQUENCY)}}},
		{"crosstab cell", &types.Request{Crosstab: &types.CrosstabSpec{
			Rows: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}}, Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "c"}},
			Cell: agg(types.AGG_AVERAGE, "q"),
		}}, nil, []wantAdvisory{{code, "crosstab.cell", "q", string(types.AGG_FREQUENCY)}}},
		{"parametric test", &types.Request{Tests: []*types.Test{{Type: types.TEST_WELCH, Field: "q", SplitBy: "c"}}}, nil,
			[]wantAdvisory{{code, "tests[0]", "q", string(types.TEST_CHISQ)}}},
		{"sum is not mean-family", &types.Request{Aggregations: []*types.Aggregation{agg(types.AGG_SUM, "q")}}, nil, nil},
		{"scale field", &types.Request{Aggregations: []*types.Aggregation{agg(types.AGG_AVERAGE, "s")}}, nil, nil},
		{"rank-based test", &types.Request{Tests: []*types.Test{{Type: types.TEST_MANN_WHITNEY_U, Field: "q", SplitBy: "c"}}}, nil, nil},
		{"categorical-typed field", &types.Request{Aggregations: []*types.Aggregation{agg(types.AGG_AVERAGE, "c")}}, nil, nil},
		{"field the schema no longer carries", &types.Request{Aggregations: []*types.Aggregation{agg(types.AGG_AVERAGE, "gone")}}, nil, nil},
		{"no sidecar levels", &types.Request{Aggregations: []*types.Aggregation{agg(types.AGG_AVERAGE, "q")}}, map[string]string{}, nil},
		{"sidecar level for another field", &types.Request{Aggregations: []*types.Aggregation{agg(types.AGG_AVERAGE, "q")}}, map[string]string{"s": "nominal"}, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			checkAdvisories(t, sidecarAdvisories(t, c.req, &PredictOptions{SidecarMeasureLevels: c.measures}), c.want)
		})
	}
}

// TestPredict_AdvisoryOrdinalParametric: an ordinal field under a
// parametric test fires, suggesting the first rank-based alternative in
// the test's Purpose.NotFor; Field2 is checked too; a test whose NotFor
// names none fires without a suggestion; aggregators and rank-based
// tests never fire.
func TestPredict_AdvisoryOrdinalParametric(t *testing.T) {
	code := string(errors.PULSE_ADVISORY_ORDINAL_PARAMETRIC)
	for _, c := range []struct {
		name string
		test *types.Test
		want []wantAdvisory
	}{
		{"TEST_T", &types.Test{Type: types.TEST_T, Field: "o", SplitBy: "c"}, []wantAdvisory{{code, "tests[0]", "o", string(types.TEST_MANN_WHITNEY_U)}}},
		{"TEST_ANOVA_F on a u4", &types.Test{Type: types.TEST_ANOVA_F, Field: "u", SplitBy: "g"}, []wantAdvisory{{code, "tests[0]", "u", string(types.TEST_KRUSKAL_WALLIS)}}},
		{"TEST_PAIRED_T field2", &types.Test{Type: types.TEST_PAIRED_T, Field: "s", Field2: "o"}, []wantAdvisory{{code, "tests[0]", "o", string(types.TEST_WILCOXON_SR)}}},
		{"TEST_PEARSON_R both fields", &types.Test{Type: types.TEST_PEARSON_R, Field: "o", Field2: "u"}, []wantAdvisory{
			{code, "tests[0]", "o", string(types.TEST_SPEARMAN_R)}, {code, "tests[0]", "u", string(types.TEST_SPEARMAN_R)}}},
		{"TEST_Z_TWO_SAMPLE has no rank-based NotFor", &types.Test{Type: types.TEST_Z_TWO_SAMPLE, Field: "o", SplitBy: "c"}, []wantAdvisory{{code, "tests[0]", "o", nil}}},
		{"rank-based test", &types.Test{Type: types.TEST_KRUSKAL_WALLIS, Field: "o", SplitBy: "g"}, nil},
		{"scale field", &types.Test{Type: types.TEST_T, Field: "s", SplitBy: "c"}, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			checkAdvisories(t, sidecarAdvisories(t, &types.Request{Tests: []*types.Test{c.test}}, nil), c.want)
		})
	}
	t.Run("aggregator over an ordinal field", func(t *testing.T) {
		got := sidecarAdvisories(t, &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_AVERAGE, Field: "o", Label: "m"}}}, nil)
		checkAdvisories(t, got, nil)
	})
	t.Run("post-test slot", func(t *testing.T) {
		got := sidecarAdvisories(t, &types.Request{PostTests: []*types.Test{{Type: types.TEST_WELCH, Field: "o", SplitBy: "c"}}}, nil)
		checkAdvisories(t, got, []wantAdvisory{{code, "post_tests[0]", "o", string(types.TEST_MANN_WHITNEY_U)}})
	})
}

// TestPredict_SidecarAdvisoriesNeverProposeHidden: a hidden alternative
// is neither suggested nor named; a hidden firing operator routes as
// never-registered and fires nothing.
func TestPredict_SidecarAdvisoriesNeverProposeHidden(t *testing.T) {
	avg := &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_AVERAGE, Field: "q", Label: "m"}}}
	ttest := &types.Request{Tests: []*types.Test{{Type: types.TEST_T, Field: "o", SplitBy: "c"}}}
	welchNominal := &types.Request{Tests: []*types.Test{{Type: types.TEST_WELCH, Field: "q", SplitBy: "c"}}}
	for _, c := range []struct {
		name   string
		req    *types.Request
		hidden string
		fires  bool
	}{
		{"AGG_FREQUENCY hidden", avg, string(types.AGG_FREQUENCY), true},
		{"TEST_MANN_WHITNEY_U hidden", ttest, string(types.TEST_MANN_WHITNEY_U), true},
		{"TEST_CHISQ hidden", welchNominal, string(types.TEST_CHISQ), true},
		{"AGG_AVERAGE hidden", avg, string(types.AGG_AVERAGE), false},
		{"TEST_T hidden", ttest, string(types.TEST_T), false},
	} {
		t.Run(c.name, func(t *testing.T) {
			if len(sidecarAdvisories(t, c.req, nil)) != 1 {
				t.Fatal("vacuous: the unscoped instance does not fire")
			}
			got := sidecarAdvisories(t, c.req, &PredictOptions{Instance: hideOnly(c.hidden)})
			if !c.fires {
				if len(got) != 0 {
					t.Fatalf("advisories = %+v, want none", got)
				}
				return
			}
			if len(got) != 1 {
				t.Fatalf("advisories = %+v, want one", got)
			}
			if _, ok := got[0].Details["suggested"]; ok {
				t.Errorf("suggested = %v with it hidden", got[0].Details["suggested"])
			}
			if body, _ := json.Marshal(got[0]); strings.Contains(string(body), c.hidden) {
				t.Errorf("advisory names hidden %s: %s", c.hidden, body)
			}
		})
	}
}

// TestPredict_AdvisoryWeightAvailableUnused: fires exactly when the
// suggested_weight echo is set — silent once a weight resolves, when
// capability:weighting is hidden, and without a sidecar weight variable.
func TestPredict_AdvisoryWeightAvailableUnused(t *testing.T) {
	code := string(errors.PULSE_ADVISORY_WEIGHT_AVAILABLE_UNUSED)
	count := func(w *types.WeightSpec) *types.Request {
		return &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "s", Label: "t"}}, Weight: w}
	}
	run := func(req *types.Request, opts *PredictOptions) *descriptor.PredictResult {
		t.Helper()
		return predictFromBytes(sidecarCohort(t), req, opts).Data.(*descriptor.PredictResult)
	}

	res := run(count(nil), &PredictOptions{SuggestedWeightVariable: "w"})
	if res.SuggestedWeight == nil || len(res.Advisories) != 1 || res.Advisories[0].Code != code {
		t.Fatalf("suggested_weight=%+v advisories=%+v, want the echo and one %s", res.SuggestedWeight, res.Advisories, code)
	}
	a := res.Advisories[0]
	want := map[string]any{"weight": map[string]any{"field": "w"}}
	if a.Details["field"] != "w" || !reflect.DeepEqual(a.Details["suggested"], want) {
		t.Errorf("details = %+v, want field w and suggested %v", a.Details, want)
	}

	for name, c := range map[string]struct {
		req  *types.Request
		opts *PredictOptions
	}{
		"weight resolves":        {count(&types.WeightSpec{Field: "w"}), &PredictOptions{SuggestedWeightVariable: "w"}},
		"weighting hidden":       {count(nil), &PredictOptions{SuggestedWeightVariable: "w", Instance: scopedExcept(featWeighting)}},
		"no sidecar weight":      {count(nil), &PredictOptions{}},
		"variable not in schema": {count(nil), &PredictOptions{SuggestedWeightVariable: "gone"}},
	} {
		t.Run(name, func(t *testing.T) {
			for _, a := range run(c.req, c.opts).Advisories {
				if a.Code == code {
					t.Errorf("fired %+v", a)
				}
			}
		})
	}
}

// TestSidecarAdvisoryMessagesPassProseLint: the three sidecar messages,
// with and without a suggestion, read as guidance prose.
func TestSidecarAdvisoryMessagesPassProseLint(t *testing.T) {
	reqs := []struct {
		req  *types.Request
		opts *PredictOptions
	}{
		{&types.Request{
			Aggregations: []*types.Aggregation{{Type: types.AGG_AVERAGE, Field: "q", Label: "m"}},
			Tests:        []*types.Test{{Type: types.TEST_T, Field: "o", SplitBy: "c"}, {Type: types.TEST_Z_TWO_SAMPLE, Field: "o", SplitBy: "c"}},
		}, &PredictOptions{SuggestedWeightVariable: "w"}},
		{&types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_AVERAGE, Field: "q", Label: "m"}}},
			&PredictOptions{Instance: hideOnly(string(types.AGG_FREQUENCY))}},
	}
	seen := map[string]bool{}
	for _, r := range reqs {
		for _, a := range sidecarAdvisories(t, r.req, r.opts) {
			seen[a.Code] = true
			if hits := LintGuidanceText(a.Message); len(hits) > 0 {
				t.Errorf("%s message %q: lint hits %+v", a.Code, a.Message, hits)
			}
		}
	}
	if len(seen) != 3 {
		t.Errorf("vacuous: linted codes %v, want all three sidecar codes", seen)
	}
}
