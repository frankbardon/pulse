package descriptor

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/weighting"
	"github.com/frankbardon/pulse/types"
)

// weightGateInst offers every request host and slot the weight-bearing
// slots live in (crosstab, an overlay kind) — everything but
// capability:weighting unless with is true.
func weightGateInst(with bool) *InstanceSnapshot {
	names := []string{featProcess, featCompose, featProcessChain, featFacet, featCrosstab,
		"AGG_SUM", "OVERLAY_SHARE_OF_ROW", "OVERLAY_INDEX_VS_POP"}
	if with {
		names = append(names, featWeighting)
	}
	return scopedOnly(names...)
}

// nestedWeightCases sets one per-slot weight per case, on the slot path
// the refusal must name; sample is that object (its keys are valid_keys).
func nestedWeightCases(w types.SlotWeight) []struct {
	path   string
	sample any
	set    func(r *types.Request)
} {
	ct := func(r *types.Request) *types.CrosstabSpec {
		if r.Crosstab == nil {
			r.Crosstab = &types.CrosstabSpec{}
		}
		return r.Crosstab
	}
	return []struct {
		path   string
		sample any
		set    func(r *types.Request)
	}{
		{"aggregations[1]", &types.Aggregation{}, func(r *types.Request) {
			r.Aggregations = []*types.Aggregation{{Type: types.AGG_SUM}, {Type: types.AGG_SUM, Weight: w}}
		}},
		{"crosstab.cell", &types.Aggregation{}, func(r *types.Request) { ct(r).Cell = &types.Aggregation{Weight: w} }},
		{"crosstab.margin_aggregations[0]", &types.Aggregation{}, func(r *types.Request) {
			ct(r).MarginAggregations = []*types.Aggregation{{Weight: w}}
		}},
		{"tests[0]", &types.Test{}, func(r *types.Request) { r.Tests = []*types.Test{{Weight: w}} }},
		{"post_tests[0]", &types.Test{}, func(r *types.Request) { r.PostTests = []*types.Test{{Weight: w}} }},
		{"regressions[0]", &types.RegressionSpec{}, func(r *types.Request) { r.Regressions = []*types.RegressionSpec{{Weight: w}} }},
		{"attributes[0]", &types.Attribute{}, func(r *types.Request) { r.Attributes = []*types.Attribute{{Weight: w}} }},
		{"overlays[0]", &types.OverlaySpec{}, func(r *types.Request) {
			r.Overlays = []types.OverlaySpec{{Kind: types.OverlayKindShareOfRow, Weight: w}}
		}},
		{"groups[0]", &types.Group{}, func(r *types.Request) { r.Groups = []*types.Group{{Weight: w}} }},
		{"crosstab.rows[0]", &types.Group{}, func(r *types.Request) { ct(r).Rows = []*types.Group{{Weight: w}} }},
		{"crosstab.columns[0]", &types.Group{}, func(r *types.Request) { ct(r).Columns = []*types.Group{{Weight: w}} }},
	}
}

// assertNestedWeightRefusal checks err is the nested unknown-field
// refusal of `weight` at path, valid_keys the object's visible keys.
func assertNestedWeightRefusal(t *testing.T, err error, path string, sample any, inst *InstanceSnapshot) *errors.CodedError {
	t.Helper()
	ce := asCoded(t, err)
	if ce.Code != errors.PULSE_REQUEST_UNKNOWN_FIELD {
		t.Fatalf("code = %s", ce.Code)
	}
	if got := ce.Details["unknown_keys"]; !reflect.DeepEqual(got, []string{"weight"}) {
		t.Errorf("unknown_keys = %v", got)
	}
	if got := ce.Details["path"]; got != path {
		t.Errorf("path = %v, want %s", got, path)
	}
	valid, _ := ce.Details["valid_keys"].([]string)
	want := append([]string(nil), VisibleSlotKeys(sample, inst)...)
	slices.Sort(want)
	if !reflect.DeepEqual(valid, want) || slices.Contains(valid, "weight") {
		t.Errorf("valid_keys = %v, want %v", valid, want)
	}
	if !strings.Contains(ce.Message, "unrecognized key(s) in "+path+`: "weight"`) {
		t.Errorf("message does not locate the key: %s", ce.Message)
	}
	return ce
}

// TestSlotRefusal_WeightAtEveryNestingLevel: with capability:weighting
// hidden, the request-root weight and every per-slot weight — set or
// explicitly null — are PULSE_REQUEST_UNKNOWN_FIELD, located; with it
// enabled none is.
func TestSlotRefusal_WeightAtEveryNestingLevel(t *testing.T) {
	hidden, open := weightGateInst(false), weightGateInst(true)

	root := &types.Request{Weight: &types.WeightSpec{Field: "w"}}
	ce := asCoded(t, SlotRefusal(root, hidden))
	if ce.Code != errors.PULSE_REQUEST_UNKNOWN_FIELD || !reflect.DeepEqual(ce.Details["unknown_keys"], []string{"weight"}) {
		t.Fatalf("root weight: %s %v", ce.Code, ce.Details)
	}
	if _, nested := ce.Details["path"]; nested {
		t.Errorf("root refusal carries a path: %v", ce.Details)
	}
	if want := UnknownFieldError([]string{"weight"}, VisibleSlotKeys(root, hidden)); ce.Message != want.Message {
		t.Errorf("root message diverges from the strict-decode shape:\n%s\n%s", ce.Message, want.Message)
	}
	if err := SlotRefusal(root, open); err != nil {
		t.Errorf("weighting enabled refuses the root weight: %v", err)
	}

	for _, w := range []types.SlotWeight{types.SlotWeightField("w"), types.NullSlotWeight()} {
		for _, c := range nestedWeightCases(w) {
			name := c.path
			if w.IsNull() {
				name += "/null"
			}
			t.Run(name, func(t *testing.T) {
				req := &types.Request{}
				c.set(req)
				if err := SlotRefusal(req, open); err != nil {
					t.Fatalf("weighting enabled refuses %s: %v", c.path, err)
				}
				assertNestedWeightRefusal(t, SlotRefusal(req, hidden), c.path, c.sample, hidden)

				// Inside a composed request and a chain stage: located.
				composed := &types.ComposedRequest{Requests: []*types.Request{{}, req}}
				ce := assertNestedWeightRefusal(t, SlotRefusal(composed, hidden), c.path, c.sample, hidden)
				if ce.Details["request"] != 1 {
					t.Errorf("compose: request = %v", ce.Details["request"])
				}
				chain := &types.ChainRequest{Stages: []*types.ChainStage{{Request: &types.Request{}}, {Request: req}}}
				ce = assertNestedWeightRefusal(t, SlotRefusal(chain, hidden), c.path, c.sample, hidden)
				if ce.Details["stage"] != 1 {
					t.Errorf("chain: stage = %v", ce.Details["stage"])
				}
			})
		}
	}
}

// TestSlotRefusal_FacetOverlayWeight: a FacetRequest's overlays carry
// the per-slot weight too.
func TestSlotRefusal_FacetOverlayWeight(t *testing.T) {
	fr := &types.FacetRequest{Overlays: []types.OverlaySpec{{Kind: types.OverlayKindIndexVsPop, Weight: types.SlotWeightField("w")}}}
	if err := SlotRefusal(fr, weightGateInst(true)); err != nil {
		t.Fatalf("weighting enabled: %v", err)
	}
	assertNestedWeightRefusal(t, SlotRefusal(fr, weightGateInst(false)), "overlays[0]", &types.OverlaySpec{}, weightGateInst(false))
}

// TestSlotRefusal_RootSlotsBeforeNestedWeight: a hidden root slot is
// reported before a nested weight, and an unset weight passes.
func TestSlotRefusal_RootSlotsBeforeNestedWeight(t *testing.T) {
	inst := scopedOnly(featProcess, "AGG_SUM") // crosstab hidden too
	req := &types.Request{
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Weight: types.SlotWeightField("w")}},
		Crosstab:     &types.CrosstabSpec{},
	}
	ce := asCoded(t, SlotRefusal(req, inst))
	if !reflect.DeepEqual(ce.Details["unknown_keys"], []string{"crosstab"}) {
		t.Errorf("unknown_keys = %v, want the root crosstab first", ce.Details["unknown_keys"])
	}
	if err := SlotRefusal(&types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_SUM}}}, inst); err != nil {
		t.Errorf("no weight set, refused: %v", err)
	}
}

// TestPayloadSchema_WeightingHidden: the instance payload schema carries
// no `weight` property anywhere (and so no SlotWeight / WeightSpec def)
// when capability:weighting is hidden; enabling it brings them back.
func TestPayloadSchema_WeightingHidden(t *testing.T) {
	schema := func(inst *InstanceSnapshot) string {
		raw, err := PayloadSchemaForInstance(inst)
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	on := schema(weightGateInst(true))
	for _, want := range []string{`"weight":`, `"SlotWeight":`, `"WeightSpec":`} {
		if !strings.Contains(on, want) {
			t.Fatalf("vacuous: weighting-enabled schema lacks %s", want)
		}
	}
	off := schema(weightGateInst(false))
	for _, gone := range []string{`"weight":`, `"SlotWeight"`, `"WeightSpec"`} {
		if strings.Contains(off, gone) {
			t.Errorf("weighting-hidden schema still carries %s", gone)
		}
	}
}

// TestManifest_WeightingHidden: with capability:weighting hidden the
// instance manifest carries no weight_aware flag — built-in or
// extension — no weight_kinds on any family (aggregator, attribute,
// grouper, test, regression, overlay) and no weighted floor key;
// enabled, all are there.
func TestManifest_WeightingHidden(t *testing.T) {
	const ext = "AGG_ACME_WSUM"
	inst := func(with bool) *InstanceSnapshot {
		names := []string{featProcess, "AGG_SUM", "AGG_WEIGHTED_MEAN", ext, "TEST_MANN_WHITNEY_U", "REG_BAYES_LINEAR", "ATTR_ZSCORE", "GROUP_QUANTILE",
			string(types.OverlayKindPairwiseWeightedTwoMeansZ)}
		if with {
			names = append(names, featWeighting)
		}
		snap := &ExtensionsSnapshot{Aggregators: []descriptor.OperatorMeta{{Name: ext, WeightAware: true}}}
		return NewInstanceSnapshot(snap, FeatureSet{Enabled: names})
	}
	render := func(with bool) string {
		raw, err := json.Marshal(BuildManifestForInstance(inst(with)))
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	// Stand-ins for the non-aggregator families a later change flips.
	for _, op := range []string{"TEST_MANN_WHITNEY_U", "REG_BAYES_LINEAR", "ATTR_ZSCORE", "GROUP_QUANTILE"} {
		defer weighting.OverrideClassForTest(op, weighting.ClassFrequencyOnly)()
	}
	on := render(true)
	if n := strings.Count(on, `"weight_kinds"`); n != 7 {
		t.Fatalf("vacuous: weighting-enabled manifest carries %d weight_kinds, want 7 (2 aggregators, test, regression, attribute, grouper, overlay)", n)
	}
	for _, want := range []string{`"weight_aware":true`, `"n_weight_invalid"`} {
		if !strings.Contains(on, want) {
			t.Fatalf("vacuous: weighting-enabled manifest lacks %s", want)
		}
	}
	m := BuildManifestForInstance(inst(true))
	if len(m.Extensions.Aggregators) != 1 || !m.Extensions.Aggregators[0].WeightAware {
		t.Fatalf("vacuous: extension not weight-aware when enabled: %+v", m.Extensions.Aggregators)
	}
	off := render(false)
	for _, gone := range []string{`"weight_aware"`, `"weight_kinds"`, `"n_weight_invalid"`} {
		if strings.Contains(off, gone) {
			t.Errorf("weighting-hidden manifest still carries %s", gone)
		}
	}
	// The snapshot itself is untouched (the runtime still reads it).
	if !inst(false).Extensions().Aggregators[0].WeightAware {
		t.Error("manifest assembly cleared the snapshot's WeightAware")
	}
}

// TestPredict_WeightingHiddenReportsNoWeights: AGG_WEIGHTED_MEAN's
// params.weight_field stays ungated, but predict's per-slot weights
// report is not offered when capability:weighting is hidden; a slot
// nothing weights names only the remedy the instance offers.
func TestPredict_WeightingHiddenReportsNoWeights(t *testing.T) {
	data := buildTestPulseFile(t, hiddenPredictSchema(t))
	req := &types.Request{Aggregations: []*types.Aggregation{
		{Type: types.AGG_WEIGHTED_MEAN, Field: "score", Label: "m", Params: json.RawMessage(`{"weight_field":"w"}`)},
	}}
	opts := func(with bool) *PredictOptions {
		names := []string{featProcess, "AGG_WEIGHTED_MEAN"}
		if with {
			names = append(names, featWeighting)
		}
		return &PredictOptions{Instance: scopedOnly(names...)}
	}
	result := func(with bool) *descriptor.PredictResult {
		env := predictFromBytes(data, req, opts(with))
		if len(env.Errors) > 0 {
			t.Fatalf("weight_field refused (weighting=%v): %v", with, env.Errors[0])
		}
		return env.Data.(*descriptor.PredictResult)
	}
	if got := result(true).Weights; len(got) != 1 || got[0].Status != descriptor.WeightStatusApplied {
		t.Fatalf("vacuous: enabled weights = %+v", got)
	}
	if got := result(false).Weights; got != nil {
		t.Errorf("hidden weighting reports weights: %+v", got)
	}

	bare := &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_WEIGHTED_MEAN, Field: "score", Label: "m"}}}
	msg := func(with bool) string {
		env := predictFromBytes(data, bare, opts(with))
		if len(env.Errors) == 0 {
			t.Fatalf("AGG_WEIGHTED_MEAN with no weight accepted (weighting=%v)", with)
		}
		return env.Errors[0].Message
	}
	if m := msg(true); !strings.Contains(m, "request weight") {
		t.Fatalf("vacuous: enabled remedy = %s", m)
	}
	if m := msg(false); strings.Contains(m, "slot weight") || strings.Contains(m, "request weight") || !strings.Contains(m, "params.weight_field") {
		t.Errorf("hidden remedy names the hidden surface: %s", m)
	}
}

// TestExampleDetector_Weighting: an example body naming a `weight` at
// any depth requires capability:weighting (so a profile hiding it
// prunes the example); weight_field alone does not.
func TestExampleDetector_Weighting(t *testing.T) {
	detects := func(body string) bool {
		return slices.Contains(detectedExampleCapabilities(ontologyExample{}, decodeExampleBody(json.RawMessage(body))), featWeighting)
	}
	if !detects(`{"weight":{"field":"w"}}`) || !detects(`{"aggregations":[{"type":"AGG_SUM","weight":null}]}`) {
		t.Error("a weight key does not require capability:weighting")
	}
	if detects(`{"aggregations":[{"type":"AGG_WEIGHTED_MEAN","params":{"weight_field":"w"}}]}`) {
		t.Error("weight_field requires capability:weighting")
	}
}
