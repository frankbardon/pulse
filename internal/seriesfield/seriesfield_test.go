package seriesfield

import (
	"encoding/json"
	stderrors "errors"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

func aggs() []*types.Aggregation {
	return []*types.Aggregation{
		{Type: types.AGG_SUM, Field: "spend"},
		{Type: types.AGG_AVERAGE, Field: "spend"},
		{Type: types.AGG_COUNT, Field: "id", Label: "orders"},
	}
}

func spec(params string) *types.OverlaySpec {
	s := &types.OverlaySpec{Kind: types.OverlayKindDeltaVsPrior}
	if params != "" {
		s.Params = json.RawMessage(params)
	}
	return s
}

// Absent, empty, null and unparseable params score aggregations[0];
// a label resolves by output label, explicit or TYPE_field, so two
// aggregations over one input field are told apart.
func TestResolve(t *testing.T) {
	for params, want := range map[string]int{
		"":                                 0,
		`{}`:                               0,
		`{"window": 3}`:                    0,
		`{"field": ""}`:                    0,
		`{"field": null}`:                  0,
		`[1]`:                              0,
		`{"field": "AGG_SUM_spend"}`:       0,
		`{"field": "AGG_AVERAGE_spend"}`:   1,
		`{"field": "orders", "window": 2}`: 2,
	} {
		got, err := Resolve(aggs(), spec(params), 0)
		if err != nil || got != want {
			t.Errorf("%s: Resolve = (%d, %v), want %d", params, got, err, want)
		}
	}
}

// An unknown label or a non-string field is refused
// PULSE_OVERLAY_FIELD_UNKNOWN with {index, kind, field, available}.
func TestResolve_Refused(t *testing.T) {
	for params, field := range map[string]any{
		`{"field": "AGG_COUNT_id"}`: "AGG_COUNT_id", // explicit Label wins
		`{"field": "spend"}`:        "spend",        // an input field is not a label
		`{"field": 3}`:              3.0,
	} {
		_, err := Resolve(aggs(), spec(params), 4)
		var ce *errors.CodedError
		if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_OVERLAY_FIELD_UNKNOWN {
			t.Fatalf("%s: err = %v, want PULSE_OVERLAY_FIELD_UNKNOWN", params, err)
		}
		want := map[string]any{
			"index":     4,
			"kind":      string(types.OverlayKindDeltaVsPrior),
			"field":     field,
			"available": []string{"AGG_SUM_spend", "AGG_AVERAGE_spend", "orders"},
		}
		if !reflect.DeepEqual(ce.Details, want) {
			t.Errorf("%s: details %#v, want %#v", params, ce.Details, want)
		}
	}
}

func TestKinds(t *testing.T) {
	ks := Kinds()
	if len(ks) != len(kinds) {
		t.Fatalf("Kinds() = %v, want %d kinds", ks, len(kinds))
	}
	for _, k := range ks {
		if !Applies(k) {
			t.Errorf("%s listed but not applied", k)
		}
	}
	if Applies(types.OverlayKindShareOfRow) || IndexOf(aggs(), "nope") != -1 {
		t.Error("a MATRIX-only kind applies, or an unknown label resolved")
	}
}

// ResolveComposeSlot reads the Compose spec's decoded params by the
// Resolve rule and stamps the slot label on a refusal.
func TestResolveComposeSlot(t *testing.T) {
	spec := &types.ComposeOverlaySpec{Kind: types.OverlayKindIndexVsRef}
	for field, want := range map[any]int{nil: 0, "": 0, "orders": 2, "AGG_AVERAGE_spend": 1} {
		spec.Params = map[string]any{"field": field}
		if got, err := ResolveComposeSlot(aggs(), spec, 0, "a"); err != nil || got != want {
			t.Errorf("field %v: (%d, %v), want %d", field, got, err, want)
		}
	}
	spec.Params = map[string]any{"field": "spend"}
	_, err := ResolveComposeSlot(aggs(), spec, 3, "treated")
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_OVERLAY_FIELD_UNKNOWN {
		t.Fatalf("err = %v, want PULSE_OVERLAY_FIELD_UNKNOWN", err)
	}
	want := map[string]any{"index": 3, "kind": string(types.OverlayKindIndexVsRef), "field": "spend",
		"available": []string{"AGG_SUM_spend", "AGG_AVERAGE_spend", "orders"}, "slot": "treated"}
	if !reflect.DeepEqual(ce.Details, want) {
		t.Errorf("details %#v, want %#v", ce.Details, want)
	}
	if !AppliesCompose(types.OverlayKindPanelIndexVsRef) || AppliesCompose(types.OverlayKindRank) || Applies(types.OverlayKindIndexVsRef) {
		t.Error("compose kind set wrong, or a compose kind leaked into the process-host set")
	}
}

// ResolveChainStage reads the chain spec's params by the Resolve rule
// and stamps the arm and stage on a refusal.
func TestResolveChainStage(t *testing.T) {
	spec := &types.ChainOverlaySpec{Kind: types.OverlayKindDeltaVsStage}
	for field, want := range map[any]int{nil: 0, "": 0, "orders": 2, "AGG_AVERAGE_spend": 1} {
		spec.Params = map[string]any{"field": field}
		if got, err := ResolveChainStage(aggs(), spec, 0, "target", 1); err != nil || got != want {
			t.Errorf("field %v: (%d, %v), want %d", field, got, err, want)
		}
	}
	spec.Params = map[string]any{"field": "spend"}
	_, err := ResolveChainStage(aggs(), spec, 2, "ref", 0)
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_OVERLAY_FIELD_UNKNOWN {
		t.Fatalf("err = %v, want PULSE_OVERLAY_FIELD_UNKNOWN", err)
	}
	want := map[string]any{"index": 2, "kind": string(types.OverlayKindDeltaVsStage), "field": "spend",
		"available": []string{"AGG_SUM_spend", "AGG_AVERAGE_spend", "orders"}, "which": "ref", "stage_index": 0}
	if !reflect.DeepEqual(ce.Details, want) {
		t.Errorf("details %#v, want %#v", ce.Details, want)
	}
	if got := ChainKinds(); !reflect.DeepEqual(got, []types.OverlayKind{types.OverlayKindIndexVsStage, types.OverlayKindDeltaVsStage}) &&
		!reflect.DeepEqual(got, []types.OverlayKind{types.OverlayKindDeltaVsStage, types.OverlayKindIndexVsStage}) {
		t.Errorf("ChainKinds = %v", got)
	}
	if AppliesChain(types.OverlayKindIndexVsRef) || Applies(types.OverlayKindIndexVsStage) || AppliesCompose(types.OverlayKindDeltaVsStage) {
		t.Error("a chain kind leaked into another host set, or a compose kind into the chain set")
	}
}
