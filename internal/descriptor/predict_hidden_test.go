package descriptor

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// Predict sees only the instance: every type-keyed predict rule reads
// the operator's route (opRoute), so a name PredictOptions.Instance
// hides predicts byte-identically (after substitution) to a
// never-registered name. Each case also runs the hidden name unscoped
// and requires THAT outcome to differ, so a case whose rule the name
// never reaches fails as vacuous. The root hidden-name parity harness
// drives the public Predict / PredictBytes entry points; these cases
// pin the rules its probe shapes do not reach.

// hiddenPredictSchema carries one column of each kind the cases need.
func hiddenPredictSchema(t *testing.T) *encoding.Schema {
	t.Helper()
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "grade", Type: encoding.FieldTypeCategoricalU8, Description: "Letter grade for the student", Dictionary: makeDictionary(t, "A", "B", "C")},
		{Name: "score", Type: encoding.FieldTypeF64, Description: "Numeric exam score out of one hundred"},
		{Name: "w", Type: encoding.FieldTypeF64, Description: "Sampling weight for the respondent"},
		{Name: "day", Type: encoding.FieldTypeDate, Description: "Calendar day the exam was taken"},
		{Name: "ts", Type: encoding.FieldTypeDateTime, Description: "Instant the exam was submitted"},
		{Name: "amt", Type: encoding.FieldTypeDecimal128, Precision: 18, Scale: 2, Description: "Fee paid for the exam sitting"},
	}}
}

func hiddenPredictOpts(hidden string) *PredictOptions {
	return &PredictOptions{Instance: hideOnly(hidden)}
}

// hideOnly is a scoped instance offering every built-in feature except
// hidden — the profile the hidden-name tests mean, so no request slot
// is hidden along with the name under test.
func hideOnly(hidden ...string) *InstanceSnapshot {
	var enabled []string
	for _, n := range FeatureNames() {
		if !slices.Contains(hidden, n) {
			enabled = append(enabled, n)
		}
	}
	return NewInstanceSnapshot(nil, FeatureSet{Enabled: enabled, Hidden: hidden})
}

// hiddenNameParity runs build(name) through run with the instance
// hiding `hidden`, once with the hidden name and once with `never`,
// and requires identical outcomes after substitution; the unscoped
// control must differ.
func hiddenNameParity(t *testing.T, hidden, never string, run func(name string, opts *PredictOptions) string) {
	t.Helper()
	opts := hiddenPredictOpts(hidden)
	got, want, control := run(hidden, opts), run(never, opts), run(hidden, nil)
	if sub := strings.ReplaceAll(got, hidden, never); sub != want {
		t.Errorf("hidden %s diverges from never-registered %s\nhidden: %s\nnever:  %s", hidden, never, got, want)
	}
	if strings.ReplaceAll(control, hidden, never) == want {
		t.Errorf("vacuous: unscoped %s predicts like a never-registered name\ncontrol: %s", hidden, control)
	}
}

func predictOutcome(t *testing.T, data []byte, req *types.Request, opts *PredictOptions) string {
	t.Helper()
	return hiddenEnvJSON(t, predictFromBytes(data, req, opts))
}

// TestPredict_HiddenOperatorIsNeverRegistered: one case per
// type-keyed predict rule the harness probes do not reach.
func TestPredict_HiddenOperatorIsNeverRegistered(t *testing.T) {
	data := buildTestPulseFile(t, hiddenPredictSchema(t))
	cases := []struct {
		name, hidden, never string
		req                 func(op string) *types.Request
	}{
		{
			// CategoricalAggregationIssues + the categorical suggestion.
			name: "numeric aggregation on a categorical field", hidden: "AGG_SUM", never: "AGG_NEVER_REGISTERED",
			req: func(op string) *types.Request {
				return &types.Request{Aggregations: []*types.Aggregation{{Type: types.AggregationType(op), Field: "grade", Label: "x"}}}
			},
		},
		{
			// The field-reference walk's per-type params table.
			name: "aggregation params naming a field", hidden: "AGG_RATIO", never: "AGG_NEVER_REGISTERED",
			req: func(op string) *types.Request {
				return &types.Request{Aggregations: []*types.Aggregation{{
					Type: types.AggregationType(op), Field: "score", Label: "x",
					Params: json.RawMessage(`{"numerator_field":"nope","denominator_field":"score"}`),
				}}}
			},
		},
		{
			// The crosstab cell's numeric-on-categorical advisory.
			name: "crosstab cell on a categorical field", hidden: "AGG_SUM", never: "AGG_NEVER_REGISTERED",
			req: func(op string) *types.Request {
				return &types.Request{Crosstab: &types.CrosstabSpec{
					Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "grade"}},
					Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "grade"}},
					Cell:    &types.Aggregation{Type: types.AggregationType(op), Field: "grade"},
				}}
			},
		},
		{
			// Zone resolution: a hidden zone-capable grouper is not
			// zone-capable, so it resolves no zone.
			name: "zone-capable grouper", hidden: "GROUP_DATE", never: "GROUP_NEVER_REGISTERED",
			req: func(op string) *types.Request {
				return &types.Request{
					Groups:       []*types.Group{{Type: types.GroupType(op), Field: "ts"}},
					Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "score", Label: "n"}},
				}
			},
		},
		{
			// The regression-attribute Target rule.
			name: "regression attribute without a target", hidden: "ATTR_REG_FITTED", never: "ATTR_NEVER_REGISTERED",
			req: func(op string) *types.Request {
				return &types.Request{
					Attributes:   []*types.Attribute{{Type: types.AttributeType(op), Field: "score", Label: "fit", Predictors: []string{"w"}}},
					Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "score", Label: "n"}},
				}
			},
		},
		{
			// The two-pass attribute streamability gate.
			name: "two-pass attribute with a grouper", hidden: "ATTR_ZSCORE", never: "ATTR_NEVER_REGISTERED",
			req: func(op string) *types.Request {
				return &types.Request{
					Attributes:   []*types.Attribute{{Type: types.AttributeType(op), Field: "score", Label: "z"}},
					Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "grade"}},
					Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "score", Label: "n"}},
				}
			},
		},
		{
			// The crosstab margin aggregations' numeric-on-categorical
			// advisory.
			name: "crosstab margin aggregation on a categorical field", hidden: "AGG_SUM", never: "AGG_NEVER_REGISTERED",
			req: func(op string) *types.Request {
				return &types.Request{Crosstab: &types.CrosstabSpec{
					Rows:               []*types.Group{{Type: types.GROUP_CATEGORY, Field: "grade"}},
					Columns:            []*types.Group{{Type: types.GROUP_CATEGORY, Field: "grade"}},
					Cell:               &types.Aggregation{Type: types.AGG_COUNT, Field: "score"},
					Margins:            types.CrosstabMargins{Rows: true},
					MarginAggregations: []*types.Aggregation{{Type: types.AggregationType(op), Field: "grade", Label: "m"}},
				}}
			},
		},
		{
			// Tier-1 test streamability.
			name: "streamable row test", hidden: "TEST_T", never: "TEST_NEVER_REGISTERED",
			req: func(op string) *types.Request {
				return &types.Request{
					Tests:        []*types.Test{{Type: types.TestType(op), Field: "score", SplitBy: "grade", Label: "t"}},
					Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "score", Label: "n"}},
				}
			},
		},
		{
			// The normalize-on-a-recompute-margin advisory reads the
			// cell's margin class.
			name: "normalized crosstab cell", hidden: "AGG_SUM", never: "AGG_NEVER_REGISTERED",
			req: func(op string) *types.Request {
				return &types.Request{Crosstab: &types.CrosstabSpec{
					Rows:      []*types.Group{{Type: types.GROUP_CATEGORY, Field: "grade"}},
					Columns:   []*types.Group{{Type: types.GROUP_CATEGORY, Field: "grade"}},
					Cell:      &types.Aggregation{Type: types.AggregationType(op), Field: "score"},
					Normalize: types.CrosstabNormalizeRow,
				}}
			},
		},
		{
			// An attribute's default output label (the regression
			// attributes name it after Target): a sort key naming the
			// label resolves only while the type is offered.
			name: "attribute default label", hidden: "ATTR_REG_FITTED", never: "ATTR_NEVER_REGISTERED",
			req: func(op string) *types.Request {
				return &types.Request{
					Attributes: []*types.Attribute{{Type: types.AttributeType(op), Target: "score", Predictors: []string{"w"}}},
					Sort:       []types.OrderKey{{Field: op + "_score"}},
				}
			},
		},
		{
			// The missing-percentile suggestion.
			name: "percentile without its parameter", hidden: "AGG_PERCENTILE", never: "AGG_NEVER_REGISTERED",
			req: func(op string) *types.Request {
				return &types.Request{Aggregations: []*types.Aggregation{{Type: types.AggregationType(op), Field: "score", Label: "p"}}}
			},
		},
		{
			// The date-misuse suggestion keys on GROUP_CATEGORY.
			name: "category grouper on a date field", hidden: "GROUP_CATEGORY", never: "GROUP_NEVER_REGISTERED",
			req: func(op string) *types.Request {
				return &types.Request{
					Groups:       []*types.Group{{Type: types.GroupType(op), Field: "day"}},
					Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "score", Label: "n"}},
				}
			},
		},
		{
			// The streamability suggestion's substitute table.
			name: "non-streamable aggregator with a streamable peer", hidden: "AGG_MEDIAN", never: "AGG_NEVER_REGISTERED",
			req: func(op string) *types.Request {
				return &types.Request{Aggregations: []*types.Aggregation{{Type: types.AggregationType(op), Field: "score", Label: "m"}}}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hiddenNameParity(t, tc.hidden, tc.never, func(name string, opts *PredictOptions) string {
				return predictOutcome(t, data, tc.req(name), opts)
			})
		})
	}
}

// TestPredict_HiddenDefaultsNotApplied: DefaultsApplied never names a
// target the instance hides — on the defaulting path and under
// DisableDefaults (which still reports what WOULD apply).
func TestPredict_HiddenDefaultsNotApplied(t *testing.T) {
	data := buildTestPulseFile(t, hiddenPredictSchema(t))
	req := func() *types.Request {
		return &types.Request{Aggregations: []*types.Aggregation{{Field: "score", Label: "s"}}}
	}
	for _, disable := range []bool{false, true} {
		open := predictFromBytes(data, req(), &PredictOptions{DisableDefaults: disable}).Data.(*descriptor.PredictResult)
		if n := len(open.DefaultsApplied); n != 1 {
			t.Fatalf("disable=%v: unscoped DefaultsApplied = %d entries, want 1", disable, n)
		}
		opts := hiddenPredictOpts("AGG_SUM")
		opts.DisableDefaults = disable
		got := predictFromBytes(data, req(), opts).Data.(*descriptor.PredictResult)
		for _, d := range got.DefaultsApplied {
			if d.Type == "AGG_SUM" {
				t.Errorf("disable=%v: DefaultsApplied names hidden AGG_SUM: %+v", disable, got.DefaultsApplied)
			}
		}
	}
}

// TestPredict_SuggestionsNeverProposeHidden: a suggestion never
// proposes an operator the instance hides.
func TestPredict_SuggestionsNeverProposeHidden(t *testing.T) {
	data := buildTestPulseFile(t, hiddenPredictSchema(t))
	req := &types.Request{
		Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "day"}},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "grade", Label: "x"},
			{Type: types.AGG_MEDIAN, Field: "score", Label: "m"},
		},
	}
	hidden := []string{"GROUP_DATE", "AGG_MODE", "AGG_AVERAGE"}
	proposes := func(opts *PredictOptions) map[string]bool {
		out := map[string]bool{}
		for _, s := range predictFromBytes(data, req, opts).Data.(*descriptor.PredictResult).Suggestions {
			for _, p := range s.Proposed {
				if name, ok := p.(string); ok {
					out[name] = true
				}
			}
		}
		return out
	}
	open := proposes(nil)
	got := proposes(&PredictOptions{Instance: hideOnly(hidden...)})
	for _, h := range hidden {
		if !open[h] {
			t.Errorf("vacuous: unscoped predict never proposes %s (proposals %v)", h, open)
		}
		if got[h] {
			t.Errorf("predict proposes hidden %s (proposals %v)", h, got)
		}
	}
}

// TestValidateFacet_HiddenFilterIsNeverRegistered: the additive-field
// FILTER_EXPRESSION rule judges a hidden FILTER_EXPRESSION as a
// never-registered filterer.
func TestValidateFacet_HiddenFilterIsNeverRegistered(t *testing.T) {
	data := buildTestPulseFile(t, hiddenPredictSchema(t))
	hiddenNameParity(t, "FILTER_EXPRESSION", "FILTER_NEVER_REGISTERED", func(name string, opts *PredictOptions) string {
		req := &types.FacetRequest{
			Fields:         []string{"score"},
			AdditiveFields: []string{"grade"},
			Filterers:      []*types.Filterer{{Type: types.FiltererType(name), Expression: `grade == "A"`}},
		}
		return hiddenEnvJSON(t, ValidateFacetWithOptions(bytes.NewReader(data), req, opts))
	})
}

// TestValidateChain_HiddenIsNeverRegistered: the chain validator's
// merge gate refuses a hidden aggregator as a never-registered one, and
// a hidden chain overlay kind is not in the chain catalog.
func TestValidateChain_HiddenIsNeverRegistered(t *testing.T) {
	data := buildTestPulseFile(t, hiddenPredictSchema(t))
	stage0 := func() *types.Request {
		return &types.Request{
			Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "grade"}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "score", Label: "n"}},
		}
	}
	t.Run("stage aggregator", func(t *testing.T) {
		hiddenNameParity(t, "AGG_MAX", "AGG_NEVER_REGISTERED", func(name string, opts *PredictOptions) string {
			req := &types.ChainRequest{Stages: []*types.ChainStage{
				{Name: "a", Request: stage0()},
				{Name: "b", Request: &types.Request{
					Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "grade"}},
					Aggregations: []*types.Aggregation{{Type: types.AggregationType(name), Field: "n", Label: "m"}},
				}},
			}}
			return hiddenEnvJSON(t, ValidateChainWithOptions(bytes.NewReader(data), req, opts))
		})
	})
	t.Run("chain overlay kind", func(t *testing.T) {
		hiddenNameParity(t, string(types.OverlayKindIndexVsStage), hiddenNeverOverlay, func(name string, opts *PredictOptions) string {
			req := &types.ChainRequest{
				Stages:   []*types.ChainStage{{Name: "a", Request: stage0()}, {Name: "b", Request: stage0()}},
				Overlays: []*types.ChainOverlaySpec{{Kind: types.OverlayKind(name), Ref: types.StageRef{Name: "a"}}},
			}
			return hiddenEnvJSON(t, ValidateChainWithOptions(bytes.NewReader(data), req, opts))
		})
	})
}

// TestPredict_HiddenOverlayHostGrouper: OVERLAY_YOY requires a
// GROUP_DATE first grouper; a GROUP_DATE the instance hides is not one,
// so the overlay is refused naming the authored type. (Substitution
// parity cannot pin this rule: its message names GROUP_DATE as the
// requirement too.)
func TestPredict_HiddenOverlayHostGrouper(t *testing.T) {
	data := buildTestPulseFile(t, hiddenPredictSchema(t))
	req := &types.Request{
		Groups:       []*types.Group{{Type: types.GROUP_DATE, Field: "day", Params: json.RawMessage(`{"component":"month"}`)}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score", Label: "s"}},
		Overlays: []types.OverlaySpec{{
			Kind: types.OverlayKindYoY, Scope: types.OverlayScopeGroup,
			Ref: types.OverlayRef{YoY: &types.OverlayYoYRef{}},
		}},
	}
	hostRefusal := func(opts *PredictOptions) bool {
		for _, e := range predictFromBytes(data, req, opts).Errors {
			if strings.Contains(e.Message, "first grouper to be GROUP_DATE; got GROUP_DATE") {
				return true
			}
		}
		return false
	}
	if hostRefusal(nil) {
		t.Fatal("vacuous: unscoped GROUP_DATE host is refused")
	}
	if !hostRefusal(hiddenPredictOpts(string(types.GROUP_DATE))) {
		t.Error("a hidden GROUP_DATE first grouper satisfies OVERLAY_YOY")
	}
}

// TestPredict_HiddenOverlayHostAggregator: OVERLAY_T_VS_REF reads its
// moments off a map-valued aggregator and otherwise requires Params; an
// AGG_WELFORD the instance hides is not map-valued, so the Params are
// required. (Its message names AGG_WELFORD as the remedy, so
// substitution parity cannot pin it.)
func TestPredict_HiddenOverlayHostAggregator(t *testing.T) {
	data := buildTestPulseFile(t, hiddenPredictSchema(t))
	req := &types.Request{
		Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "grade"}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_WELFORD, Field: "score", Label: "m"}},
		Overlays:     []types.OverlaySpec{{Kind: types.OverlayKindTVsRef, Scope: types.OverlayScopeGroup}},
	}
	paramsRequired := func(opts *PredictOptions) bool {
		for _, e := range predictFromBytes(data, req, opts).Errors {
			if e.Code == "PULSE_OVERLAY_PARAM_MISSING" {
				return true
			}
		}
		return false
	}
	if paramsRequired(nil) {
		t.Fatal("vacuous: unscoped AGG_WELFORD host requires Params")
	}
	if !paramsRequired(hiddenPredictOpts(string(types.AGG_WELFORD))) {
		t.Error("a hidden AGG_WELFORD host makes OVERLAY_T_VS_REF Params optional")
	}
}

// TestScopedFieldRefRefusals_HiddenIsNeverRegistered: the field-
// reference walk (shared by predict and the runtime) decides which
// slots, params and output labels a type reads off the type's route,
// so a hidden operator is walked as a never-registered name.
func TestScopedFieldRefRefusals_HiddenIsNeverRegistered(t *testing.T) {
	schema := hiddenPredictSchema(t)
	refusals := func(req *types.Request, opts *PredictOptions) string {
		var all []string
		for _, ce := range fieldRefRefusals(req, schema, nil, opts.instance()) {
			all = append(all, hiddenEnvJSON(t, ce))
		}
		return strings.Join(all, "\n")
	}
	cases := []struct {
		name, hidden, never string
		req                 func(op string) *types.Request
	}{
		{"feature params", "FEAT_TARGET_ENCODE", "FEAT_NEVER_REGISTERED", func(op string) *types.Request {
			return &types.Request{Features: []*types.Feature{{Type: types.FeatureType(op), Field: "grade", Params: json.RawMessage(`{"target":"nope"}`)}}}
		}},
		{"filterer field requirement", "FILTER_INCLUDE", "FILTER_NEVER_REGISTERED", func(op string) *types.Request {
			return &types.Request{Filterers: []*types.Filterer{{Type: types.FiltererType(op), Values: []string{"A"}}}}
		}},
		{"regression attribute slots", "ATTR_REG_FITTED", "ATTR_NEVER_REGISTERED", func(op string) *types.Request {
			return &types.Request{Attributes: []*types.Attribute{{Type: types.AttributeType(op), Target: "nope", Predictors: []string{"w"}, Label: "fit"}}}
		}},
		{"window field requirement", "WIN_LAG", "WIN_NEVER_REGISTERED", func(op string) *types.Request {
			return &types.Request{Windows: []*types.Window{{Type: types.WindowType(op), Field: "nope", Label: "l", OrderBy: []types.OrderKey{{Field: "score"}}}}}
		}},
		{"test slots", "TEST_CHISQ", "TEST_NEVER_REGISTERED", func(op string) *types.Request {
			return &types.Request{Tests: []*types.Test{{Type: types.TestType(op), Rows: "nope", Cols: "grade", Label: "c"}}}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hiddenNameParity(t, tc.hidden, tc.never, func(name string, opts *PredictOptions) string {
				return refusals(tc.req(name), opts)
			})
		})
	}

	// The exported runtime forms read the same walk.
	req := &types.Request{Features: []*types.Feature{{Type: types.FEAT_TARGET_ENCODE, Field: "grade", Params: json.RawMessage(`{"target":"nope"}`)}}}
	if ScopedFieldRefRefusal(req, schema, nil) == nil {
		t.Fatal("vacuous: unscoped FEAT_TARGET_ENCODE target is not judged")
	}
	if err := ScopedFieldRefRefusal(req, schema, hiddenPredictOpts("FEAT_TARGET_ENCODE").Instance); err != nil {
		t.Errorf("hidden FEAT_TARGET_ENCODE target judged: %v", err)
	}
	facet := &types.FacetRequest{Filterers: []*types.Filterer{{Type: types.FILTER_INCLUDE, Values: []string{"A"}}}}
	if len(ScopedFacetFieldRefRefusals(facet, schema, nil)) == 0 {
		t.Fatal("vacuous: unscoped FILTER_INCLUDE without a field is not refused")
	}
	if got := ScopedFacetFieldRefRefusals(facet, schema, hiddenPredictOpts("FILTER_INCLUDE").Instance); len(got) != 0 {
		t.Errorf("hidden FILTER_INCLUDE requires a field: %v", got)
	}
}

// TestPredict_HiddenDecimalAggregation: the decimal aggregation matrix
// refuses a hidden decimal-capable aggregator as a never-registered
// one. (Its suggestion's prose lists the decimal-capable aggregators,
// so substitution parity cannot pin it.)
func TestPredict_HiddenDecimalAggregation(t *testing.T) {
	data := buildTestPulseFile(t, hiddenPredictSchema(t))
	req := &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "amt", Label: "x"}}}
	refused := func(opts *PredictOptions) bool {
		for _, e := range predictFromBytes(data, req, opts).Warnings {
			if e.Code == "PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL" {
				return true
			}
		}
		return false
	}
	if refused(nil) {
		t.Fatal("vacuous: unscoped AGG_SUM on decimal128 is refused")
	}
	if !refused(hiddenPredictOpts(string(types.AGG_SUM))) {
		t.Error("a hidden AGG_SUM keeps its decimal128 implementation")
	}
}

// TestPredict_HiddenOverlayCellAggregator: OVERLAY_T_CELL reads its
// moments off a map-valued crosstab cell and otherwise requires Params;
// a hidden AGG_WELFORD cell is not map-valued.
func TestPredict_HiddenOverlayCellAggregator(t *testing.T) {
	data := buildTestPulseFile(t, hiddenPredictSchema(t))
	req := &types.Request{
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "grade"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "grade"}},
			Cell:    &types.Aggregation{Type: types.AGG_WELFORD, Field: "score"},
		},
		Overlays: []types.OverlaySpec{{Kind: types.OverlayKindTCell, Scope: types.OverlayScopeCell}},
	}
	paramsRequired := func(opts *PredictOptions) bool {
		for _, e := range predictFromBytes(data, req, opts).Errors {
			if e.Code == "PULSE_OVERLAY_PARAM_MISSING" {
				return true
			}
		}
		return false
	}
	if paramsRequired(nil) {
		t.Fatal("vacuous: unscoped AGG_WELFORD cell requires Params")
	}
	if !paramsRequired(hiddenPredictOpts(string(types.AGG_WELFORD))) {
		t.Error("a hidden AGG_WELFORD cell makes OVERLAY_T_CELL Params optional")
	}
}

// TestValidateChain_HiddenDefaultNotInferred: the chain validator's
// per-stage smart defaults never infer a hidden operator, so an untyped
// slot whose only default the instance hides is judged exactly as with
// defaults disabled — what the runtime executes.
func TestValidateChain_HiddenDefaultNotInferred(t *testing.T) {
	data := buildTestPulseFile(t, hiddenPredictSchema(t))
	req := &types.ChainRequest{Stages: []*types.ChainStage{{Name: "a", Request: &types.Request{
		Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "grade"}},
		Aggregations: []*types.Aggregation{{Field: "score", Label: "s"}},
	}}}}
	run := func(opts *PredictOptions) string {
		return hiddenEnvJSON(t, ValidateChainWithOptions(bytes.NewReader(data), req, opts))
	}
	hidden := hiddenPredictOpts(string(types.AGG_SUM))
	disabled := hiddenPredictOpts(string(types.AGG_SUM))
	disabled.DisableDefaults = true
	if got, want := run(hidden), run(disabled); got != want {
		t.Errorf("hidden default inferred\nhidden:   %s\ndisabled: %s", got, want)
	}
	if run(nil) == run(disabled) {
		t.Error("vacuous: the unscoped default changes nothing")
	}
}

// TestValidateChain_NonScalarNoteNamesOnlyOffered: the chain
// validator admits AGG_MODE_COUNT (a scalar modal count) and refuses
// AGG_MODE with the "(AGG_MODE is excluded)" note, which names
// AGG_MODE only because the instance offers it (the snapshot adapter's
// Hidden) and never names AGG_MODE_COUNT.
func TestValidateChain_NonScalarNoteNamesOnlyOffered(t *testing.T) {
	data := buildTestPulseFile(t, hiddenPredictSchema(t))
	chain := func(agg types.AggregationType) *types.ChainRequest {
		return &types.ChainRequest{Cohort: &types.Cohort{Filename: "c.pulse"}, Stages: []*types.ChainStage{{Name: "a", Request: &types.Request{
			Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "grade"}},
			Aggregations: []*types.Aggregation{{Type: agg, Field: "grade", Label: "f"}},
		}}}}
	}
	for _, opts := range []*PredictOptions{nil, {Instance: hideOnly(string(types.AGG_MODE))}} {
		if env := ValidateChainWithOptions(bytes.NewReader(data), chain(types.AGG_MODE_COUNT), opts); len(env.Errors) != 0 {
			t.Fatalf("validator refused AGG_MODE_COUNT: %s", hiddenEnvJSON(t, env))
		}
	}
	got := hiddenEnvJSON(t, ValidateChainWithOptions(bytes.NewReader(data), chain(types.AGG_MODE), nil))
	if !strings.Contains(got, "(AGG_MODE is excluded)") || strings.Contains(got, "AGG_MODE_COUNT") {
		t.Fatalf("unscoped AGG_MODE refusal = %s", got)
	}
}
