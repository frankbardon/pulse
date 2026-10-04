package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/types"
)

// Share and index overlays on a weighted host (weighting-descriptive
// E3-S3; .claude/reference/weighting.md "Overlays"). Every share / index
// kind is descriptive: it reads the host payload, so a weighted host
// yields a weighted layer with no overlay-side weight logic. The gate is
// frequency expansion — integer weights answer like the cohort whose
// rows are physically duplicated — on every host shape the kinds run
// on: matrix (Process crosstab), series (Process groups) and Compose
// (cross-slot index, panel index). Each case also pins that the
// weighted layer differs from the unweighted one (the weight reached
// it) and that the overlay never mutates the base payload.

// weightedOverlayCase is one host request carrying the overlays under
// test, as JSON with a %COHORT% placeholder.
type weightedOverlayCase struct {
	name string
	// kinds the case must emit, one layer each.
	kinds []types.OverlayKind
	req   string
	// arm the weighted request must take: "fused" / "buffered"
	// (crosstab), "streaming" (series); "" asserts nothing.
	arm string
}

func weightedOverlayCases() []weightedOverlayCase {
	const xt = `"crosstab":{"rows":[{"type":"GROUP_CATEGORY","field":"g"}],"columns":[{"type":"GROUP_RANGE","field":"id","interval":200}],` +
		`"cell":{"type":"AGG_SUM","field":"y","label":"cell"},"margins":{"rows":true,"columns":true,"grand":true}}`
	const series = `"groups":[{"type":"GROUP_RANGE","field":"id","interval":100}],"aggregations":[{"type":"AGG_SUM","field":"y","label":"s"}]`
	matrixKinds := []types.OverlayKind{types.OverlayKindShareOfRow, types.OverlayKindShareOfCol,
		types.OverlayKindShareOfTotal, types.OverlayKindIndexVsMargin}
	const matrixOverlays = `"overlays":[` +
		`{"name":"row_share","kind":"OVERLAY_SHARE_OF_ROW","scope":"cell","ref":{"margin":{"axis":"row"}}},` +
		`{"name":"col_share","kind":"OVERLAY_SHARE_OF_COL","scope":"cell","ref":{"margin":{"axis":"column"}}},` +
		`{"name":"total_share","kind":"OVERLAY_SHARE_OF_TOTAL","scope":"cell","ref":{"margin":{"axis":"grand"}}},` +
		`{"name":"i_row","kind":"OVERLAY_INDEX_VS_MARGIN","scope":"cell","ref":{"margin":{"axis":"row"}}}]`
	return []weightedOverlayCase{
		// A mergeable cell with no filter takes the fused arm; the
		// always-true FILTER_EXPRESSION steers the twin onto the
		// buffered arm (crosstabBufferedSteer).
		{
			name:  "matrix_fused",
			arm:   "fused",
			kinds: matrixKinds,
			req:   `{"cohort":{"filename":"%COHORT%"},` + xt + `,` + matrixOverlays + `}`,
		},
		{
			name:  "matrix_buffered",
			arm:   "buffered",
			kinds: matrixKinds,
			req: `{"cohort":{"filename":"%COHORT%"},"filterers":[{"type":"FILTER_EXPRESSION","expression":"id >= 0"}],` +
				xt + `,` + matrixOverlays + `}`,
		},
		{
			name: "series_streaming",
			arm:  "streaming",
			kinds: []types.OverlayKind{types.OverlayKindShareOfTotal, types.OverlayKindIndexVsTotal,
				types.OverlayKindIndexVsPrior},
			req: `{"cohort":{"filename":"%COHORT%"},` + series + `,"overlays":[` +
				`{"name":"share","kind":"OVERLAY_SHARE_OF_TOTAL","scope":"group"},` +
				`{"name":"i_total","kind":"OVERLAY_INDEX_VS_TOTAL","scope":"group"},` +
				`{"name":"i_prior","kind":"OVERLAY_INDEX_VS_PRIOR","scope":"group"}]}`,
		},
		{
			name: "series",
			kinds: []types.OverlayKind{types.OverlayKindShareOfTotal, types.OverlayKindIndexVsTotal,
				types.OverlayKindIndexVsPrior, types.OverlayKindIndexVsBaseline,
				types.OverlayKindIndexVsRollingMean, types.OverlayKindIndexVsSibling},
			req: `{"cohort":{"filename":"%COHORT%"},` + series + `,"overlays":[` +
				`{"name":"share","kind":"OVERLAY_SHARE_OF_TOTAL","scope":"group"},` +
				`{"name":"i_total","kind":"OVERLAY_INDEX_VS_TOTAL","scope":"group"},` +
				`{"name":"i_prior","kind":"OVERLAY_INDEX_VS_PRIOR","scope":"group"},` +
				`{"name":"i_base","kind":"OVERLAY_INDEX_VS_BASELINE","scope":"group","ref":{"baseline_index":{"position":0}}},` +
				`{"name":"i_roll","kind":"OVERLAY_INDEX_VS_ROLLING_MEAN","scope":"group","ref":{"rolling_mean":{}},"params":{"window":3}},` +
				`{"name":"i_sib","kind":"OVERLAY_INDEX_VS_SIBLING","scope":"group","ref":{"sibling":{"field":"id","value":"200-300"}}}]}`,
		},
	}
}

// weightedComposeCases are the Compose-hosted index kinds: the
// cross-slot cell index and the panel index.
func weightedComposeCases() []weightedOverlayCase {
	const xt = `"crosstab":{"rows":[{"type":"GROUP_CATEGORY","field":"g"}],"columns":[{"type":"GROUP_RANGE","field":"id","interval":200}],` +
		`"cell":{"type":"AGG_SUM","field":"y","label":"cell"},"margins":{"rows":true,"columns":true,"grand":true}}`
	const series = `"groups":[{"type":"GROUP_CATEGORY","field":"g"}],"aggregations":[{"type":"AGG_SUM","field":"y","label":"s"}]`
	sub := func(label, body string) string {
		return `{"label":"` + label + `","cohort":{"filename":"%COHORT%"},` +
			`"filterers":[{"type":"FILTER_EXPRESSION","expression":"id % 2 == 0"}],` + body + `}`
	}
	all := func(label, body string) string {
		return `{"label":"` + label + `","cohort":{"filename":"%COHORT%"},` + body + `}`
	}
	return []weightedOverlayCase{
		{
			name:  "compose_index_vs_ref",
			kinds: []types.OverlayKind{types.OverlayKindIndexVsRef},
			req: `{"requests":[` + all("total", xt) + `,` + sub("audience", xt) + `],"overlays":[` +
				`{"name":"aud_index","kind":"OVERLAY_INDEX_VS_REF","scope":"cell","reference":"total","targets":["audience"]}]}`,
		},
		{
			name:  "compose_panel_index",
			kinds: []types.OverlayKind{types.OverlayKindPanelIndexVsRef},
			req: `{"requests":[` + all("total", series) + `,` + sub("a", series) + `],"overlays":[` +
				`{"name":"panel","kind":"OVERLAY_PANEL_INDEX_VS_REF","scope":"group","reference":"total","targets":["a"]}]}`,
		},
	}
}

func decodeWeightedOverlayRequest[T any](t *testing.T, raw, path string, weighted, stripOverlays bool) *T {
	t.Helper()
	var out T
	if err := json.Unmarshal([]byte(strings.ReplaceAll(raw, "%COHORT%", path)), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	switch r := any(&out).(type) {
	case *types.Request:
		if weighted {
			r.Weight = &types.WeightSpec{Field: "w", Kind: types.WeightKindFrequency}
		}
		if stripOverlays {
			r.Overlays = nil
		}
	case *types.ComposedRequest:
		for _, s := range r.Requests {
			if weighted {
				s.Weight = &types.WeightSpec{Field: "w", Kind: types.WeightKindFrequency}
			}
		}
		if stripOverlays {
			r.Overlays = nil
		}
	}
	return &out
}

// assertWeightedLayers: got (weighted) equals want (expanded,
// unweighted) on every layer, carries one layer per kind, and differs
// from raw (the unweighted host over the weighted cohort).
func assertWeightedLayers(t *testing.T, kinds []types.OverlayKind, got, want, raw []types.OverlayLayer) {
	t.Helper()
	if len(got) != len(kinds) {
		t.Fatalf("got %d layers, want %d (%v)", len(got), len(kinds), kinds)
	}
	for i, k := range kinds {
		if got[i].Kind != k {
			t.Fatalf("layer %d is %s, want %s", i, got[i].Kind, k)
		}
		if len(got[i].Warnings) != 0 {
			t.Errorf("%s: unexpected warnings %v", k, got[i].Warnings)
		}
		if where := overlayMismatch(string(k), reflect.ValueOf(got[i]), reflect.ValueOf(want[i]), true); where != "" {
			t.Errorf("weighted layer differs from the expanded cohort's at %s", where)
		}
		if overlayMismatch(string(k), reflect.ValueOf(got[i].Payload), reflect.ValueOf(raw[i].Payload), false) == "" {
			t.Errorf("%s: the weighted layer equals the unweighted one — the weight never reached it", k)
		}
	}
}

// TestWeightShareIndexOverlaysReadWeightedHost: every share and index
// kind answers on a weighted Process host exactly as on the expanded
// cohort, differs from the unweighted host, and leaves the host payload
// byte-identical to the same weighted request without overlays.
func TestWeightShareIndexOverlaysReadWeightedHost(t *testing.T) {
	weighted := newParityStore(t, "ov_weighted", func(n int) []parityRow { return parityRows(n, freqWeight) })
	expanded := newParityStore(t, "ov_expanded", func(n int) []parityRow { return expandRows(parityRows(n, freqWeight)) })
	run := func(t *testing.T, store *parityStore, req *types.Request) *types.Response {
		t.Helper()
		svc := New(store.mem)
		svc.SetShardWorkers(1)
		svc.SetDecodeWorkers(1)
		resp, err := svc.Process(context.Background(), req)
		if err != nil {
			t.Fatalf("Process: %v", err)
		}
		return resp
	}
	for _, tc := range weightedOverlayCases() {
		t.Run(tc.name, func(t *testing.T) {
			wp, ep := weighted.paths[paritySingleFile], expanded.paths[paritySingleFile]
			wreq := processing.StampWeights(decodeWeightedOverlayRequest[types.Request](t, tc.req, wp, true, false), nil)
			fused, _ := processing.CanFuseCrosstab(wreq, paritySchema(), nil)
			if took := map[string]bool{"fused": fused, "buffered": wreq.Crosstab != nil && !fused,
				"streaming": processing.CanStreamRequest(wreq, paritySchema())}; tc.arm != "" && !took[tc.arm] {
				t.Fatalf("the weighted request does not take the %s arm", tc.arm)
			}
			got := run(t, weighted, decodeWeightedOverlayRequest[types.Request](t, tc.req, wp, true, false))
			want := run(t, expanded, decodeWeightedOverlayRequest[types.Request](t, tc.req, ep, false, false))
			raw := run(t, weighted, decodeWeightedOverlayRequest[types.Request](t, tc.req, wp, false, false))
			assertWeightedLayers(t, tc.kinds, got.Overlays, want.Overlays, raw.Overlays)

			// Never mutate the base payload: the weighted host is the
			// same with and without its overlays.
			bare := run(t, weighted, decodeWeightedOverlayRequest[types.Request](t, tc.req, wp, true, true))
			for name, pair := range map[string][2]any{
				"data": {got.Data, bare.Data}, "crosstab": {got.Crosstab, bare.Crosstab},
				"components": {got.Components, bare.Components},
			} {
				if g, b := mustMarshal(t, pair[0]), mustMarshal(t, pair[1]); !bytes.Equal(g, b) {
					t.Errorf("%s: overlays changed the host payload\nwith    %s\nwithout %s", name, g, b)
				}
			}
		})
	}
}

// TestWeightComposeIndexOverlaysReadWeightedSlots: the Compose-hosted
// index kinds (INDEX_VS_REF, PANEL_INDEX_VS_REF) read the weighted slot
// payloads; a descriptive kind is never refused by a slot weight.
func TestWeightComposeIndexOverlaysReadWeightedSlots(t *testing.T) {
	weighted := newParityStore(t, "cov_weighted", func(n int) []parityRow { return parityRows(n, freqWeight) })
	expanded := newParityStore(t, "cov_expanded", func(n int) []parityRow { return expandRows(parityRows(n, freqWeight)) })
	run := func(t *testing.T, store *parityStore, req *types.ComposedRequest) *types.ComposedResponse {
		t.Helper()
		svc := New(store.mem)
		svc.SetShardWorkers(1)
		svc.SetDecodeWorkers(1)
		resp, err := svc.Compose(context.Background(), req)
		if err != nil {
			t.Fatalf("Compose: %v", err)
		}
		return resp
	}
	for _, tc := range weightedComposeCases() {
		t.Run(tc.name, func(t *testing.T) {
			wp, ep := weighted.paths[paritySingleFile], expanded.paths[paritySingleFile]
			got := run(t, weighted, decodeWeightedOverlayRequest[types.ComposedRequest](t, tc.req, wp, true, false))
			want := run(t, expanded, decodeWeightedOverlayRequest[types.ComposedRequest](t, tc.req, ep, false, false))
			raw := run(t, weighted, decodeWeightedOverlayRequest[types.ComposedRequest](t, tc.req, wp, false, false))
			assertWeightedLayers(t, tc.kinds, got.Overlays, want.Overlays, raw.Overlays)

			bare := run(t, weighted, decodeWeightedOverlayRequest[types.ComposedRequest](t, tc.req, wp, true, true))
			if g, b := mustMarshal(t, got.Responses), mustMarshal(t, bare.Responses); !bytes.Equal(g, b) {
				t.Errorf("overlays changed the slot payloads\nwith    %s\nwithout %s", g, b)
			}
		})
	}
}

// overlayMismatch walks two overlay values and returns the path of the
// first difference, "" when equal. Floats compare within parityClose
// when tolerant, exactly otherwise; NaN equals NaN (an undefined entry —
// INDEX_VS_PRIOR's first, a short rolling window — is NaN on both
// sides). The walk is reflective because an undefined NaN entry does
// not survive encoding/json.
func overlayMismatch(where string, a, b reflect.Value, tolerant bool) string {
	if a.Kind() != b.Kind() {
		return where
	}
	switch a.Kind() {
	case reflect.Pointer, reflect.Interface:
		if a.IsNil() || b.IsNil() {
			if a.IsNil() != b.IsNil() {
				return where
			}
			return ""
		}
		return overlayMismatch(where, a.Elem(), b.Elem(), tolerant)
	case reflect.Struct:
		for i := 0; i < a.NumField(); i++ {
			if w := overlayMismatch(where+"."+a.Type().Field(i).Name, a.Field(i), b.Field(i), tolerant); w != "" {
				return w
			}
		}
	case reflect.Slice, reflect.Array:
		if a.Len() != b.Len() {
			return where + ".len"
		}
		for i := 0; i < a.Len(); i++ {
			if w := overlayMismatch(fmt.Sprintf("%s[%d]", where, i), a.Index(i), b.Index(i), tolerant); w != "" {
				return w
			}
		}
	case reflect.Map:
		if a.Len() != b.Len() {
			return where + ".len"
		}
		for _, k := range a.MapKeys() {
			bv := b.MapIndex(k)
			if !bv.IsValid() {
				return fmt.Sprintf("%s[%v]", where, k)
			}
			if w := overlayMismatch(fmt.Sprintf("%s[%v]", where, k), a.MapIndex(k), bv, tolerant); w != "" {
				return w
			}
		}
	case reflect.Float32, reflect.Float64:
		x, y := a.Float(), b.Float()
		switch {
		case math.IsNaN(x) || math.IsNaN(y):
			if math.IsNaN(x) != math.IsNaN(y) {
				return where
			}
		case tolerant && !parityClose(x, y), !tolerant && x != y:
			return fmt.Sprintf("%s (%v vs %v)", where, x, y)
		}
	default:
		if fmt.Sprint(a.Interface()) != fmt.Sprint(b.Interface()) {
			return fmt.Sprintf("%s (%v vs %v)", where, a.Interface(), b.Interface())
		}
	}
	return ""
}
