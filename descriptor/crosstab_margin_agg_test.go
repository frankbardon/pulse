package descriptor

import (
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// marginAggPredictReq builds a well-formed crosstab over the shared
// predict fixture with all three margins on, then applies the caller's
// mutation. Margins are on by default so a declared auxiliary always has
// somewhere to land — the unobserved warning is tested by turning them
// back off explicitly.
func marginAggPredictReq(mut func(*types.CrosstabSpec)) *types.Request {
	spec := &types.CrosstabSpec{
		Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
		Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "segment"}},
		Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "value", Label: "n"},
		Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
	}
	if mut != nil {
		mut(spec)
	}
	return &types.Request{Crosstab: spec}
}

// TestPredict_Crosstab_MarginAggregationsMalformed pins the predict-side
// half of the request surface. Predict and execution share the DETECTION
// (types.CrosstabSpec.MarginAggregationFaults) and hold their own coded
// rendering, so this asserts the same defects surface here as the
// service tests assert at runtime — the point being that an agent gets
// the refusal before paying for a scan.
func TestPredict_Crosstab_MarginAggregationsMalformed(t *testing.T) {
	schema := crosstabPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	for _, tc := range []struct {
		name string
		mut  func(*types.CrosstabSpec)
		want errors.Code
	}{
		{
			name: "null entry",
			mut:  func(s *types.CrosstabSpec) { s.MarginAggregations = []*types.Aggregation{nil} },
			want: errors.PULSE_CROSSTAB_MARGIN_AGG_INVALID,
		},
		{
			name: "entry with no type",
			mut: func(s *types.CrosstabSpec) {
				s.MarginAggregations = []*types.Aggregation{{Field: "value"}}
			},
			want: errors.PULSE_CROSSTAB_MARGIN_AGG_INVALID,
		},
		{
			name: "duplicate label",
			mut: func(s *types.CrosstabSpec) {
				s.MarginAggregations = []*types.Aggregation{
					{Type: types.AGG_SUM, Field: "value"},
					{Type: types.AGG_SUM, Field: "value"},
				}
			},
			want: errors.PULSE_CROSSTAB_MARGIN_AGG_DUPLICATE_LABEL,
		},
		{
			name: "collides with the cell label",
			mut: func(s *types.CrosstabSpec) {
				s.Cell = &types.Aggregation{Type: types.AGG_COUNT, Field: "value", Label: "base"}
				s.MarginAggregations = []*types.Aggregation{
					{Type: types.AGG_SUM, Field: "value", Label: "base"},
				}
			},
			want: errors.PULSE_CROSSTAB_MARGIN_AGG_DUPLICATE_LABEL,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := PredictFromBytes(data, marginAggPredictReq(tc.mut), nil)
			if !envHasCode(env, tc.want) {
				t.Errorf("expected %s; got errors=%v", tc.want, env.Errors)
			}
		})
	}
}

// TestPredict_Crosstab_MarginAggregationsUnknownField proves the
// auxiliary entries ride the same field-reference walk the cell does. An
// auxiliary naming a field the cohort does not carry would otherwise
// fail deep inside an accumulator with no field name attached.
func TestPredict_Crosstab_MarginAggregationsUnknownField(t *testing.T) {
	schema := crosstabPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	env := PredictFromBytes(data, marginAggPredictReq(func(s *types.CrosstabSpec) {
		s.MarginAggregations = []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "nonexistent", Label: "base"},
		}
	}), nil)

	var found bool
	for _, e := range env.Errors {
		if e.Code == string(errors.SERVICE_VALIDATION) &&
			e.Details != nil && e.Details["field"] == "nonexistent" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected an unknown-field error naming \"nonexistent\"; got errors=%v", env.Errors)
	}
}

// TestPredict_Crosstab_MarginAggregationsUnobserved covers the footgun
// this slot introduces: auxiliary aggregations land in the margin
// accumulators only, so a crosstab that emits no margin computes them
// into nowhere and returns nothing to say so. A warning, never an error
// — the request is structurally legal and runs.
func TestPredict_Crosstab_MarginAggregationsUnobserved(t *testing.T) {
	schema := crosstabPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	aux := func(s *types.CrosstabSpec) {
		s.MarginAggregations = []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "value", Label: "base"},
		}
	}

	// No margin, no normalize: warned.
	env := PredictFromBytes(data, marginAggPredictReq(func(s *types.CrosstabSpec) {
		s.Margins = types.CrosstabMargins{}
		aux(s)
	}), nil)
	if !envHasWarningCode(env, errors.PULSE_CROSSTAB_MARGIN_AGG_UNOBSERVED) {
		t.Errorf("expected PULSE_CROSSTAB_MARGIN_AGG_UNOBSERVED warning; got warnings=%v", env.Warnings)
	}
	if envHasCode(env, errors.PULSE_CROSSTAB_MARGIN_AGG_UNOBSERVED) {
		t.Error("unobserved margin aggregations must be a warning, not an error")
	}

	// A margin is emitted: not warned. Non-vacuity control for the arm
	// above — without it the assertion would pass against a validator
	// that warns unconditionally.
	env = PredictFromBytes(data, marginAggPredictReq(aux), nil)
	if envHasWarningCode(env, errors.PULSE_CROSSTAB_MARGIN_AGG_UNOBSERVED) {
		t.Errorf("margins are on; the unobserved warning must not fire. warnings=%v", env.Warnings)
	}

	// Normalization implies its margin, so it too satisfies the rule.
	env = PredictFromBytes(data, marginAggPredictReq(func(s *types.CrosstabSpec) {
		s.Margins = types.CrosstabMargins{}
		s.Normalize = types.CrosstabNormalizeColumn
		aux(s)
	}), nil)
	if envHasWarningCode(env, errors.PULSE_CROSSTAB_MARGIN_AGG_UNOBSERVED) {
		t.Errorf("normalize=column computes the column margin; the unobserved warning must not fire. warnings=%v", env.Warnings)
	}

	// Slot absent: never warned.
	env = PredictFromBytes(data, marginAggPredictReq(func(s *types.CrosstabSpec) {
		s.Margins = types.CrosstabMargins{}
	}), nil)
	if envHasWarningCode(env, errors.PULSE_CROSSTAB_MARGIN_AGG_UNOBSERVED) {
		t.Errorf("no margin_aggregations declared; the warning must not fire. warnings=%v", env.Warnings)
	}
}

// TestPredict_Crosstab_MarginAggregationsWellFormedClean is the
// non-vacuity control for every refusal above: a well-formed auxiliary
// set raises none of the slot's codes.
func TestPredict_Crosstab_MarginAggregationsWellFormedClean(t *testing.T) {
	schema := crosstabPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	env := PredictFromBytes(data, marginAggPredictReq(func(s *types.CrosstabSpec) {
		s.MarginAggregations = []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "value", Label: "weighted_base"},
			{Type: types.AGG_DISTINCT_COUNT, Field: "region", Label: "unweighted_base"},
		}
	}), nil)

	for _, code := range []errors.Code{
		errors.PULSE_CROSSTAB_MARGIN_AGG_INVALID,
		errors.PULSE_CROSSTAB_MARGIN_AGG_DUPLICATE_LABEL,
		errors.PULSE_CROSSTAB_MARGIN_AGG_UNOBSERVED,
	} {
		if envHasCode(env, code) {
			t.Errorf("well-formed margin_aggregations raised %s; errors=%v", code, env.Errors)
		}
		if envHasWarningCode(env, code) {
			t.Errorf("well-formed margin_aggregations warned %s; warnings=%v", code, env.Warnings)
		}
	}
}

// TestManifest_CrosstabCapabilityMarginAggregations verifies the
// manifest advertises the slot and names the codes that refuse it, so an
// agent can discover the surface without reading Go source.
func TestManifest_CrosstabCapabilityMarginAggregations(t *testing.T) {
	m := BuildManifest()
	if !m.Crosstab.SupportsMarginAggregations {
		t.Error("manifest.Crosstab.SupportsMarginAggregations should be true")
	}
	mustContain := []string{
		"PULSE_CROSSTAB_MARGIN_AGG_INVALID",
		"PULSE_CROSSTAB_MARGIN_AGG_DUPLICATE_LABEL",
		"PULSE_CROSSTAB_MARGIN_AGG_UNOBSERVED",
	}
	if len(m.Crosstab.MarginAggregationRules) != len(mustContain) {
		t.Errorf("MarginAggregationRules len = %d, want %d",
			len(m.Crosstab.MarginAggregationRules), len(mustContain))
	}
	for _, code := range mustContain {
		if !containsSubstring(m.Crosstab.MarginAggregationRules, code) {
			t.Errorf("MarginAggregationRules missing %s; got %v", code, m.Crosstab.MarginAggregationRules)
		}
	}
}

func envHasWarningCode(env *Envelope, code errors.Code) bool {
	for _, w := range env.Warnings {
		if w.Code == string(code) {
			return true
		}
	}
	return false
}
