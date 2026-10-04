package descriptor

import (
	"bytes"
	stderrors "errors"
	"reflect"
	"slices"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// scopedOnly is a scoped instance offering exactly enabled.
func scopedOnly(enabled ...string) *InstanceSnapshot {
	return NewInstanceSnapshot(nil, FeatureSet{Enabled: enabled})
}

// allSlotsRequest sets every gated Request slot.
func allSlotsRequest() *types.Request {
	return &types.Request{
		Crosstab: &types.CrosstabSpec{},
		Joins:    []*types.JoinSpec{{}},
		Overlays: []types.OverlaySpec{{Kind: types.OverlayKindShareOfRow, Weight: types.NullSlotWeight()}},
		Weight:   &types.WeightSpec{Field: "w"},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "x", Weight: types.SlotWeightField("w")},
		},
	}
}

func asCoded(t *testing.T, err error) *errors.CodedError {
	t.Helper()
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) {
		t.Fatalf("want a coded error, got %v", err)
	}
	return ce
}

// TestHiddenSlotKeys_Rules pins which slots each instance hides: the
// capability slots follow their capability, an overlay slot needs an
// enabled host that lists an enabled kind.
func TestHiddenSlotKeys_Rules(t *testing.T) {
	cases := []struct {
		name string
		inst *InstanceSnapshot
		root any
		want []string
	}{
		{"nil hides nothing", nil, &types.Request{}, nil},
		{"unscoped hides nothing", UnscopedInstanceSnapshot(&ExtensionsSnapshot{}), &types.Request{}, nil},
		{"process only", scopedOnly(featProcess, "AGG_COUNT"), &types.Request{}, []string{"crosstab", "joins", "overlays", "weight"}},
		{"crosstab with a matrix kind", scopedOnly(featProcess, featCrosstab, "OVERLAY_SHARE_OF_ROW"), types.Request{}, []string{"joins", "weight"}},
		{"joins only", scopedOnly(featProcess, featJoins), &types.Request{}, []string{"crosstab", "overlays", "weight"}},
		{"crosstab host without a kind", scopedOnly(featProcess, featCrosstab), &types.Request{}, []string{"joins", "overlays", "weight"}},
		{"series kind via compose", scopedOnly(featProcess, featCompose, "OVERLAY_DELTA_VS_PRIOR"), &types.Request{}, []string{"crosstab", "joins", "weight"}},
		{"kind enabled but its host hidden", scopedOnly(featProcess, featCompose, "OVERLAY_SHARE_OF_ROW"), &types.Request{}, []string{"crosstab", "joins", "overlays", "weight"}},
		{"weighting enabled", scopedOnly(featProcess, featWeighting), &types.Request{}, []string{"crosstab", "joins", "overlays"}},
		{"nested aggregation weight", scopedOnly(featProcess), &types.Aggregation{}, []string{"weight"}},
		{"nested test weight", scopedOnly(featProcess), &types.Test{}, []string{"weight"}},
		{"nested regression weight", scopedOnly(featProcess), &types.RegressionSpec{}, []string{"weight"}},
		{"nested attribute weight", scopedOnly(featProcess), &types.Attribute{}, []string{"weight"}},
		{"nested overlay weight", scopedOnly(featProcess), &types.OverlaySpec{}, []string{"weight"}},
		{"nested group weight", scopedOnly(featProcess), &types.Group{}, []string{"weight"}},
		{"nested weight with weighting", scopedOnly(featProcess, featWeighting), &types.Group{}, nil},
		{"compose overlays", scopedOnly(featCompose, "OVERLAY_RANK"), &types.ComposedRequest{}, nil},
		{"compose overlays hidden", scopedOnly(featCompose, featCrosstab, "OVERLAY_SHARE_OF_ROW"), &types.ComposedRequest{}, []string{"overlays"}},
		{"chain overlays", scopedOnly(featProcessChain, "OVERLAY_DELTA_VS_STAGE"), &types.ChainRequest{}, nil},
		{"chain overlays hidden", scopedOnly(featProcessChain), &types.ChainRequest{}, []string{"overlays"}},
		{"facet overlays", scopedOnly(featFacet, "OVERLAY_INDEX_VS_POP"), &types.FacetRequest{}, nil},
		{"facet overlays hidden", scopedOnly(featFacet, featCrosstab, "OVERLAY_SHARE_OF_ROW"), &types.FacetRequest{}, []string{"overlays"}},
		{"sample has no gated slot", scopedOnly(), &types.SampleRequest{}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := HiddenSlotKeys(c.root, c.inst); !reflect.DeepEqual(got, c.want) {
				t.Errorf("HiddenSlotKeys = %v, want %v", got, c.want)
			}
			visible := VisibleSlotKeys(c.root, c.inst)
			for _, h := range c.want {
				if slices.Contains(visible, h) {
					t.Errorf("VisibleSlotKeys carries hidden %q: %v", h, visible)
				}
			}
			if len(visible)+len(c.want) != len(JSONObjectKeys(c.root)) {
				t.Errorf("visible %v + hidden %v != all %v", visible, c.want, JSONObjectKeys(c.root))
			}
		})
	}
}

// TestSlotRefusal_DefaultInstanceRefusesNothing: no feature profile, no
// change — every slot set passes on a nil or unscoped instance.
func TestSlotRefusal_DefaultInstanceRefusesNothing(t *testing.T) {
	roots := []any{
		allSlotsRequest(),
		&types.ComposedRequest{Requests: []*types.Request{allSlotsRequest()}, Overlays: []types.ComposeOverlaySpec{{}}},
		&types.ChainRequest{Stages: []*types.ChainStage{{Request: allSlotsRequest()}}, Overlays: []*types.ChainOverlaySpec{{}}},
		&types.FacetRequest{Overlays: []types.OverlaySpec{{}}},
	}
	for _, inst := range []*InstanceSnapshot{nil, UnscopedInstanceSnapshot(&ExtensionsSnapshot{})} {
		for _, r := range roots {
			if err := SlotRefusal(r, inst); err != nil {
				t.Errorf("%T on default instance: %v", r, err)
			}
		}
	}
	// A scoped instance offering every feature refuses nothing either.
	if err := SlotRefusal(allSlotsRequest(), scopedOnly(FeatureNames()...)); err != nil {
		t.Errorf("full-registry instance: %v", err)
	}
}

// TestSlotRefusal_UnknownFieldShape: a set hidden slot is
// PULSE_REQUEST_UNKNOWN_FIELD in exactly the unknown-key shape, with
// valid_keys the instance's visible keys.
func TestSlotRefusal_UnknownFieldShape(t *testing.T) {
	inst := scopedOnly(featProcess, featJoins, "AGG_COUNT")
	req := &types.Request{Crosstab: &types.CrosstabSpec{}, Joins: []*types.JoinSpec{{}}}
	ce := asCoded(t, SlotRefusal(req, inst))
	if ce.Code != errors.PULSE_REQUEST_UNKNOWN_FIELD {
		t.Fatalf("code = %s", ce.Code)
	}
	visible := VisibleSlotKeys(req, inst)
	want := UnknownFieldError([]string{"crosstab"}, visible)
	if ce.Message != want.Message || !reflect.DeepEqual(ce.Details, want.Details) {
		t.Errorf("shape diverges from UnknownFieldError\ngot:  %s %v\nwant: %s %v", ce.Message, ce.Details, want.Message, want.Details)
	}
	valid, _ := ce.Details["valid_keys"].([]string)
	for _, h := range []string{"crosstab", "overlays"} {
		if slices.Contains(valid, h) {
			t.Errorf("valid_keys carries hidden %q", h)
		}
	}
	if !slices.Contains(valid, "joins") || !slices.Contains(valid, "cohort") {
		t.Errorf("valid_keys lost a visible key: %v", valid)
	}
	if got := ce.Details["unknown_keys"]; !reflect.DeepEqual(got, []string{"crosstab"}) {
		t.Errorf("unknown_keys = %v", got)
	}
}

// TestUnknownFieldError_SuggestionsRangeOverCandidates: a near-miss of a
// hidden slot suggests nothing hidden — suggestions are computed over
// the candidate list the caller passes (the visible keys).
func TestUnknownFieldError_SuggestionsRangeOverCandidates(t *testing.T) {
	all := JSONObjectKeys(&types.Request{})
	open := UnknownFieldError([]string{"crosstabs"}, all)
	if got := open.Details["suggestions"].(map[string]any)["crosstabs"]; got != "crosstab" {
		t.Fatalf("vacuous: unscoped suggestion = %v", got)
	}
	inst := scopedOnly(featProcess)
	scoped := UnknownFieldError([]string{"crosstabs"}, VisibleSlotKeys(&types.Request{}, inst))
	for k, v := range scoped.Details["suggestions"].(map[string]any) {
		if s, _ := v.(string); slices.Contains(HiddenSlotKeys(&types.Request{}, inst), s) {
			t.Errorf("suggestion %s → hidden %s", k, s)
		}
	}
	if got := UnknownFieldError(nil, all); got != nil {
		t.Errorf("no unknown keys: %v", got)
	}
}

// TestSlotRefusal_NestedRootsAreLocated: a hidden slot inside a
// composed slot or a chain stage carries details.request / .stage; the
// root's own slot is reported first.
func TestSlotRefusal_NestedRootsAreLocated(t *testing.T) {
	inst := scopedOnly(featProcess, featCompose, featProcessChain, "AGG_COUNT")
	plain := func() *types.Request { return &types.Request{} }

	composed := &types.ComposedRequest{Requests: []*types.Request{plain(), {Crosstab: &types.CrosstabSpec{}}}}
	ce := asCoded(t, SlotRefusal(composed, inst))
	if ce.Details["request"] != 1 || !reflect.DeepEqual(ce.Details["unknown_keys"], []string{"crosstab"}) {
		t.Errorf("composed: %v", ce.Details)
	}

	chain := &types.ChainRequest{Stages: []*types.ChainStage{{Request: plain()}, nil, {Request: &types.Request{Joins: []*types.JoinSpec{{}}}}}}
	ce = asCoded(t, SlotRefusal(chain, inst))
	if ce.Details["stage"] != 2 || !reflect.DeepEqual(ce.Details["unknown_keys"], []string{"joins"}) {
		t.Errorf("chain: %v", ce.Details)
	}

	// Root first: the chain's own overlays (no enabled chain kind)
	// are reported before the stage.
	chain.Overlays = []*types.ChainOverlaySpec{{}}
	ce = asCoded(t, SlotRefusal(chain, inst))
	if _, located := ce.Details["stage"]; located || !reflect.DeepEqual(ce.Details["unknown_keys"], []string{"overlays"}) {
		t.Errorf("chain root: %v", ce.Details)
	}
	if valid, _ := ce.Details["valid_keys"].([]string); !reflect.DeepEqual(valid, []string{"cohort", "stages"}) {
		t.Errorf("chain root valid_keys = %v", valid)
	}

	facet := &types.FacetRequest{Overlays: []types.OverlaySpec{{}}}
	if ce := asCoded(t, SlotRefusal(facet, inst)); !reflect.DeepEqual(ce.Details["unknown_keys"], []string{"overlays"}) {
		t.Errorf("facet: %v", ce.Details)
	}
	composedRoot := &types.ComposedRequest{Requests: []*types.Request{plain()}, Overlays: []types.ComposeOverlaySpec{{}}}
	if ce := asCoded(t, SlotRefusal(composedRoot, inst)); !reflect.DeepEqual(ce.Details["unknown_keys"], []string{"overlays"}) {
		t.Errorf("composed root: %v", ce.Details)
	}
	// Unset slots pass.
	if err := SlotRefusal(&types.ComposedRequest{Requests: []*types.Request{plain(), nil}}, inst); err != nil {
		t.Errorf("unset slots refused: %v", err)
	}
}

// TestPredict_HiddenSlotRefusedFirst: Predict refuses a hidden slot as
// an unknown field before the join-count rule (which it would otherwise
// report) and before reading the cohort.
func TestPredict_HiddenSlotRefusedFirst(t *testing.T) {
	data := buildTestPulseFile(t, hiddenPredictSchema(t))
	req := &types.Request{
		Crosstab: &types.CrosstabSpec{},
		Joins:    []*types.JoinSpec{{}, {}},
	}
	codes := func(opts *PredictOptions) []string {
		var out []string
		for _, e := range predictFromBytes(data, req, opts).Errors {
			out = append(out, e.Code)
		}
		return out
	}
	if open := codes(nil); slices.Contains(open, string(errors.PULSE_REQUEST_UNKNOWN_FIELD)) || len(open) == 0 {
		t.Fatalf("vacuous: unscoped predict errors = %v", open)
	}
	got := codes(&PredictOptions{Instance: scopedOnly(featProcess, "AGG_COUNT")})
	if !reflect.DeepEqual(got, []string{string(errors.PULSE_REQUEST_UNKNOWN_FIELD)}) {
		t.Errorf("errors = %v, want only PULSE_REQUEST_UNKNOWN_FIELD", got)
	}
	// Garbage cohort bytes: the slot is still refused first.
	env := Predict(bytes.NewReader([]byte("not a cohort")), req, &PredictOptions{Instance: scopedOnly(featProcess)})
	if len(env.Errors) != 1 || env.Errors[0].Code != string(errors.PULSE_REQUEST_UNKNOWN_FIELD) {
		t.Errorf("garbage cohort: %v", env.Errors)
	}
}

// TestValidators_HiddenSlotRefused: the compose / chain / facet
// validators refuse a hidden slot as the runtime funnels do.
func TestValidators_HiddenSlotRefused(t *testing.T) {
	data := buildTestPulseFile(t, hiddenPredictSchema(t))
	opts := &PredictOptions{Instance: scopedOnly(featProcess, featCompose, featProcessChain, featFacet, "AGG_COUNT")}
	firstCode := func(errs []*descriptor.EnvelopeEntry) string {
		if len(errs) == 0 {
			return ""
		}
		return errs[0].Code
	}
	cr := &types.ComposedRequest{Requests: []*types.Request{{Crosstab: &types.CrosstabSpec{}}}}
	if got := firstCode(ValidateComposeWithOptions(cr, opts).Errors); got != string(errors.PULSE_REQUEST_UNKNOWN_FIELD) {
		t.Errorf("compose: %s", got)
	}
	ch := &types.ChainRequest{Stages: []*types.ChainStage{{Request: &types.Request{Joins: []*types.JoinSpec{{}}}}}}
	if got := firstCode(ValidateChainWithOptions(bytes.NewReader(data), ch, opts).Errors); got != string(errors.PULSE_REQUEST_UNKNOWN_FIELD) {
		t.Errorf("chain: %s", got)
	}
	fr := &types.FacetRequest{Fields: []string{"grade"}, Overlays: []types.OverlaySpec{{}}}
	if got := firstCode(ValidateFacetWithOptions(bytes.NewReader(data), fr, opts).Errors); got != string(errors.PULSE_REQUEST_UNKNOWN_FIELD) {
		t.Errorf("facet: %s", got)
	}
}
