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
	t.Parallel()

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
		got, err := Resolve(aggs(), spec(params), 0, nil)
		if err != nil || got.Index != want || got.Column != types.AggregationLabelOf(aggs()[want]) {
			t.Errorf("%s: Resolve = (%+v, %v), want %d", params, got, err, want)
		}
	}
}

// An unknown label or a non-string field is refused
// PULSE_OVERLAY_FIELD_UNKNOWN with {index, kind, field, available}.
func TestResolve_Refused(t *testing.T) {
	t.Parallel()

	for params, field := range map[string]any{
		`{"field": "AGG_COUNT_id"}`: "AGG_COUNT_id", // explicit Label wins
		`{"field": "spend"}`:        "spend",        // an input field is not a label
		`{"field": 3}`:              3.0,
	} {
		_, err := Resolve(aggs(), spec(params), 4, nil)
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
	t.Parallel()

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
	t.Parallel()

	spec := &types.ComposeOverlaySpec{Kind: types.OverlayKindIndexVsRef}
	for field, want := range map[any]int{nil: 0, "": 0, "orders": 2, "AGG_AVERAGE_spend": 1} {
		spec.Params = map[string]any{"field": field}
		if got, err := ResolveComposeSlot(aggs(), spec, 0, "a", nil); err != nil || got.Index != want {
			t.Errorf("field %v: (%+v, %v), want %d", field, got, err, want)
		}
	}
	spec.Params = map[string]any{"field": "spend"}
	_, err := ResolveComposeSlot(aggs(), spec, 3, "treated", nil)
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
	t.Parallel()

	spec := &types.ChainOverlaySpec{Kind: types.OverlayKindDeltaVsStage}
	for field, want := range map[any]int{nil: 0, "": 0, "orders": 2, "AGG_AVERAGE_spend": 1} {
		spec.Params = map[string]any{"field": field}
		if got, err := ResolveChainStage(aggs(), spec, 0, "target", 1, nil); err != nil || got.Index != want {
			t.Errorf("field %v: (%+v, %v), want %d", field, got, err, want)
		}
	}
	spec.Params = map[string]any{"field": "spend"}
	_, err := ResolveChainStage(aggs(), spec, 2, "ref", 0, nil)
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

// vecAggs is aggs() with an expanding vector aggregation "m" (columns
// m[a], m[b]) between the scalars, and its column lister: known
// reports whether m's columns are readable (a schema in reach).
func vecAggs(known bool) ([]*types.Aggregation, Columns) {
	as := []*types.Aggregation{
		{Type: types.AGG_SUM, Field: "spend"},
		{Type: types.AGG_VEC_MEAN, Fields: []string{"x", "y"}, Label: "m"},
		{Type: types.AGG_COUNT, Field: "id", Label: "orders"},
	}
	return as, func(i int) ([]string, bool) {
		if i == 1 {
			if !known {
				return nil, false
			}
			return []string{"m[a]", "m[b]"}, true
		}
		return []string{types.AggregationLabelOf(as[i])}, true
	}
}

// An expanded element column resolves to its aggregation with Column
// the element column (Element true); the bare label resolves to the
// aggregation itself (Element false — the array-valued rule judges
// it); with no lister an element name is unknown.
func TestResolve_ElementColumn(t *testing.T) {
	t.Parallel()

	as, cols := vecAggs(true)
	for params, want := range map[string]Scored{
		`{"field": "m[a]"}`:   {Index: 1, Column: "m[a]"},
		`{"field": "m[b]"}`:   {Index: 1, Column: "m[b]"},
		`{"field": "m"}`:      {Index: 1, Column: "m"},
		`{"field": "orders"}`: {Index: 2, Column: "orders"},
		`{}`:                  {Index: 0, Column: "AGG_SUM_spend"},
	} {
		got, err := Resolve(as, spec(params), 0, cols)
		if err != nil || got != want {
			t.Errorf("%s: Resolve = (%+v, %v), want %+v", params, got, err, want)
		}
		if el := got.Element(as); el != (want.Column == "m[a]" || want.Column == "m[b]") {
			t.Errorf("%s: Element = %v", params, el)
		}
	}
	if (Scored{Index: 1}).Element(as) {
		t.Error("a zero Column read as an element column")
	}
	if _, err := Resolve(as, spec(`{"field": "m[a]"}`), 0, nil); err == nil {
		t.Error("an element column resolved with no column lister")
	}
	// Members unknown here (no schema): any m[...] resolves to m, the
	// schema-holding gate judges the element.
	unknownAggs, unknown := vecAggs(false)
	if got, err := Resolve(unknownAggs, spec(`{"field": "m[zz]"}`), 0, unknown); err != nil || got != (Scored{Index: 1, Column: "m[zz]"}) {
		t.Errorf("unknown members: Resolve = (%+v, %v), want m[zz] on 1", got, err)
	}
}

// An element the aggregation does not write, and an empty bracket, are
// PULSE_OVERLAY_FIELD_UNKNOWN; Details.available lists the element
// columns after their aggregation's label, on every host.
func TestResolve_ElementColumnRefused(t *testing.T) {
	t.Parallel()

	as, cols := vecAggs(true)
	wantAvail := []string{"AGG_SUM_spend", "m", "m[a]", "m[b]", "orders"}
	for _, field := range []string{"m[zz]", "m[]", "orders[a]", "m[a"} {
		_, err := Resolve(as, spec(`{"field": "`+field+`"}`), 1, cols)
		var ce *errors.CodedError
		if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_OVERLAY_FIELD_UNKNOWN {
			t.Fatalf("%s: err = %v, want PULSE_OVERLAY_FIELD_UNKNOWN", field, err)
		}
		if !reflect.DeepEqual(ce.Details["available"], wantAvail) {
			t.Errorf("%s: available %v, want %v", field, ce.Details["available"], wantAvail)
		}
	}
	cspec := &types.ComposeOverlaySpec{Kind: types.OverlayKindIndexVsRef, Params: map[string]any{"field": "m[b]"}}
	if got, err := ResolveComposeSlot(as, cspec, 0, "a", cols); err != nil || got != (Scored{Index: 1, Column: "m[b]"}) {
		t.Errorf("ResolveComposeSlot = (%+v, %v)", got, err)
	}
	chspec := &types.ChainOverlaySpec{Kind: types.OverlayKindDeltaVsStage, Params: map[string]any{"field": "m[a]"}}
	if got, err := ResolveChainStage(as, chspec, 0, "target", 0, cols); err != nil || got != (Scored{Index: 1, Column: "m[a]"}) {
		t.Errorf("ResolveChainStage = (%+v, %v)", got, err)
	}
	chspec.Params = map[string]any{"field": "m[c]"}
	_, err := ResolveChainStage(as, chspec, 0, "ref", 0, cols)
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || !reflect.DeepEqual(ce.Details["available"], wantAvail) || ce.Details["which"] != "ref" {
		t.Errorf("ResolveChainStage m[c] = %v", err)
	}
}
