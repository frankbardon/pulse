package processing

import (
	"encoding/json"
	"fmt"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// OVERLAY_PAIRWISE_WEIGHTED_TWO_MEANS_Z on the FUSED crosstab path.
//
// The weighted kind exists so a pairwise means test over a weighted cell
// keeps the scan fused (its Welford sibling cannot: AGG_WELFORD is not
// mergeable, see TestCrosstabWelfordCell_StaysBufferedWithCorrectOverlays).
// Nothing in the gate names either the aggregator or the overlay kind,
// so "stays fused" is a property of the absence of an exclusion arm —
// these tests are what turn that absence into a guarded contract.
//
// Float comparison policy. Fused (UpdateRow) and buffered (Aggregate)
// both fold through weightedMeanAggregator.foldOne in the same record
// order, so on one machine they are expected to agree bit-for-bit. The
// assertions still compare every JSON number with a tight relative
// tolerance (wfzRelTol) instead of byte equality: the two call sites are
// separately inlined, and an FMA-fusing backend (arm64 locally, a
// different contraction choice on amd64 CI) is free to contract the m2
// update differently at each site. Strings, keys, presence flags and
// array shapes are still compared exactly, so the tolerance can only
// absorb last-ulp drift, never a structural divergence.

const wfzRelTol = 1e-12

// wfzSchema: row × (wave, aud) so column scope can exercise
// pair_along_dim, and a NULLABLE weight so the fixture can carry
// null-weight rows that the aggregator must skip on both paths.
func wfzSchema(t *testing.T) *encoding.Schema {
	t.Helper()
	dict := func(vals ...string) *encoding.Dictionary {
		d := encoding.NewDictionary()
		for _, v := range vals {
			if _, err := d.Add(v); err != nil {
				t.Fatalf("dict.Add(%q): %v", v, err)
			}
		}
		return d
	}
	return &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "row", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict("r0", "r1")},
			{Name: "wave", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict("w1", "w2")},
			{Name: "aud", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict("all", "owner")},
			{Name: "value", Type: encoding.FieldTypeF64},
			{Name: "weight", Type: encoding.FieldTypeF64, Nullable: true},
		},
	}
}

// wfzRecords lays four weighted observations into each of the eight
// (row, wave, aud) cells. With polluted=true every cell additionally
// carries one NULL-weight row and one ZERO-weight row, both with an
// extreme value (1000): if either path folded them into the weighted
// moments, the weighted mean and every p-value would move by orders of
// magnitude, so the clean/polluted cross-check below is sensitive.
// Order is fixed (slice, interleaved) because the fused path interns
// axis keys first-seen.
func wfzRecords(schema *encoding.Schema, polluted bool) []*Record {
	var out []*Record
	for k := 0; k < 4; k++ {
		for r := 0; r < 2; r++ {
			for w := 0; w < 2; w++ {
				for a := 0; a < 2; a++ {
					value := float64(10 + (r*7+w*3+a*5+k*11)%9 + 2*r + 3*w)
					weight := float64(1 + (k+r+a)%3)
					base := map[string]float64{
						"row": float64(r), "wave": float64(w), "aud": float64(a),
					}
					if polluted && k == 0 {
						// Ahead of the cell's first real observation, so
						// a folded zero weight would hit Σw = 0 (0/0 in
						// the mean update) rather than being an
						// arithmetic no-op on an already-seeded cell.
						out = append(out, NewRecord(schema, wfzWith(base, map[string]float64{"value": 1000, "weight": 0})))
						out = append(out, NewRecordWithNulls(schema, wfzWith(base, map[string]float64{"value": 1000}),
							map[string]bool{"weight": true}))
					}
					out = append(out, NewRecord(schema, wfzWith(base, map[string]float64{"value": value, "weight": weight})))
				}
			}
		}
	}
	return out
}

func wfzWith(base, extra map[string]float64) map[string]float64 {
	out := make(map[string]float64, len(base)+len(extra))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

func wfzRequest(withMarginAgg bool, overlays []types.OverlaySpec) *types.Request {
	req := &types.Request{
		Crosstab: &types.CrosstabSpec{
			Rows: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "row"}},
			Columns: []*types.Group{
				{Type: types.GROUP_CATEGORY, Field: "wave"},
				{Type: types.GROUP_CATEGORY, Field: "aud"},
			},
			// Integer weights as a FREQUENCY cell weight: n_basis
			// "weights" is refused on a probability host (U12 review
			// WS-06), and the weight_field sugar is kind probability.
			Cell: &types.Aggregation{
				Type:   types.AGG_WEIGHTED_MEAN,
				Field:  "value",
				Label:  "wmean_value",
				Weight: types.SlotWeightOf(types.WeightSpec{Field: "weight", Kind: types.WeightKindFrequency}),
			},
			Shape:   types.CrosstabShapeMatrix,
			Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
		},
		Overlays: overlays,
	}
	if withMarginAgg {
		req.Crosstab.MarginAggregations = []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "value", Label: "base"},
			{Type: types.AGG_SUM, Field: "weight", Label: "weighted_base"},
		}
	}
	return req
}

func wfzSpec(t *testing.T, name string, scope types.OverlayScope, nBasis string, pairAlongDim *int) types.OverlaySpec {
	t.Helper()
	return types.OverlaySpec{
		Name:  name,
		Kind:  types.OverlayKindPairwiseWeightedTwoMeansZ,
		Scope: scope,
		Params: mustParams(t, types.PairwiseOverlayParams{
			NBasis:       nBasis,
			PairAlongDim: pairAlongDim,
		}),
	}
}

type wfzOverlayCase struct {
	name     string
	overlays func(t *testing.T) []types.OverlaySpec
}

// wfzOverlayCases is the overlay axis of the matrix: both scopes, both
// on one request, pair_along_dim, each under both n_basis values.
func wfzOverlayCases() []wfzOverlayCase {
	dim := 1
	var cases []wfzOverlayCase
	for _, nb := range []string{types.PairwiseNBasisWeights, types.PairwiseNBasisKish} {
		cases = append(cases,
			wfzOverlayCase{"row_" + nb, func(t *testing.T) []types.OverlaySpec {
				return []types.OverlaySpec{wfzSpec(t, "wz_row", types.OverlayScopeRow, nb, nil)}
			}},
			wfzOverlayCase{"column_" + nb, func(t *testing.T) []types.OverlaySpec {
				return []types.OverlaySpec{wfzSpec(t, "wz_col", types.OverlayScopeColumn, nb, nil)}
			}},
			wfzOverlayCase{"column_pair_along_dim_" + nb, func(t *testing.T) []types.OverlaySpec {
				return []types.OverlaySpec{wfzSpec(t, "wz_col_dim", types.OverlayScopeColumn, nb, &dim)}
			}},
		)
	}
	// Both scopes on one request, and the two n_basis arms side by side,
	// so layer order and per-layer independence are pinned too.
	cases = append(cases, wfzOverlayCase{"row_and_column_mixed_n_basis", func(t *testing.T) []types.OverlaySpec {
		return []types.OverlaySpec{
			wfzSpec(t, "wz_row", types.OverlayScopeRow, types.PairwiseNBasisWeights, nil),
			wfzSpec(t, "wz_col", types.OverlayScopeColumn, types.PairwiseNBasisKish, nil),
		}
	}})
	return cases
}

// TestCanFuseCrosstab_WeightedMeanWithWeightedTwoMeansZ is the gate
// guard: an AGG_WEIGHTED_MEAN cell carrying the weighted pairwise z
// overlay must be admitted by CanFuseCrosstab under every overlay shape
// the parity matrix exercises, with and without auxiliary margin
// aggregations. A future exclusion arm naming either the aggregator or
// the kind fails here, not silently as a perf regression.
func TestCanFuseCrosstab_WeightedMeanWithWeightedTwoMeansZ(t *testing.T) {
	schema := wfzSchema(t)
	for _, oc := range wfzOverlayCases() {
		for _, withMargin := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/margin_aggs_%t", oc.name, withMargin), func(t *testing.T) {
				req := wfzRequest(withMargin, oc.overlays(t))
				ok, reason := CanFuseCrosstab(req, schema, nil)
				if !ok {
					t.Fatalf("CanFuseCrosstab rejected AGG_WEIGHTED_MEAN + %s: %s",
						types.OverlayKindPairwiseWeightedTwoMeansZ, reason)
				}
				if reason != "" {
					t.Fatalf("expected empty reason on success, got %q", reason)
				}
			})
		}
	}
}

// TestFusedCrosstab_WeightedTwoMeansZMatchesBuffered is the parity
// matrix: overlay shape × clean/null-and-zero-weight fixture × margin
// aggregations present/absent. For each row the gate is asserted FIRST
// (a parity check over two buffered runs would be vacuous), then the
// fused orchestrator's response must match the buffered one on the host
// matrix, the full Components block (which carries the four weighted
// moment keys the overlay reads), the overlay layers and the warnings.
func TestFusedCrosstab_WeightedTwoMeansZMatchesBuffered(t *testing.T) {
	schema := wfzSchema(t)
	for _, oc := range wfzOverlayCases() {
		for _, polluted := range []bool{false, true} {
			for _, withMargin := range []bool{false, true} {
				name := fmt.Sprintf("%s/null_zero_weights_%t/margin_aggs_%t", oc.name, polluted, withMargin)
				t.Run(name, func(t *testing.T) {
					recs := wfzRecords(schema, polluted)
					overlays := oc.overlays(t)

					if ok, reason := CanFuseCrosstab(wfzRequest(withMargin, overlays), schema, nil); !ok {
						t.Fatalf("gate rejected the request; parity would be vacuous: %s", reason)
					}

					bufResp, err := runBufferedCrosstabWithComponents(t, schema, wfzRequest(withMargin, overlays), recs, false)
					if err != nil {
						t.Fatalf("buffered RunCrosstab: %v", err)
					}
					fusedResp, err := runFusedCrosstabViaRunner(t, schema, wfzRequest(withMargin, overlays), recs, false)
					if err != nil {
						t.Fatalf("RunCrosstabFused: %v", err)
					}

					wfzAssertNonVacuous(t, "buffered", bufResp, overlays)
					wfzAssertNonVacuous(t, "fused", fusedResp, overlays)
					if withMargin {
						ct := fusedResp.Components.Crosstab
						if len(ct.RowMarginAggregations) == 0 || len(ct.ColumnMarginAggregations) == 0 {
							t.Fatalf("margin_aggregations declared but the fused path surfaced no auxiliary margin figures")
						}
					}

					wfzAssertJSONNear(t, "Crosstab", bufResp.Crosstab, fusedResp.Crosstab)
					wfzAssertJSONNear(t, "Components", bufResp.Components, fusedResp.Components)
					wfzAssertJSONNear(t, "Overlays", bufResp.Overlays, fusedResp.Overlays)
					wfzAssertJSONNear(t, "Warnings", bufResp.Warnings, fusedResp.Warnings)
				})
			}
		}
	}
}

// TestFusedCrosstab_WeightedTwoMeansZIgnoresNullAndZeroWeights pins that
// the null/zero-weight rows in the polluted fixture are really there
// (the floor n counts them) and really excluded from the weighted moments
// on the fused path (every p-value matches the clean fixture). Without
// the first half the null_zero_weights_true rows above could be a no-op.
func TestFusedCrosstab_WeightedTwoMeansZIgnoresNullAndZeroWeights(t *testing.T) {
	schema := wfzSchema(t)
	for _, oc := range wfzOverlayCases() {
		t.Run(oc.name, func(t *testing.T) {
			overlays := oc.overlays(t)
			clean, err := runFusedCrosstabViaRunner(t, schema, wfzRequest(false, overlays), wfzRecords(schema, false), false)
			if err != nil {
				t.Fatalf("fused clean: %v", err)
			}
			dirty, err := runFusedCrosstabViaRunner(t, schema, wfzRequest(false, overlays), wfzRecords(schema, true), false)
			if err != nil {
				t.Fatalf("fused polluted: %v", err)
			}
			cleanN := wfzCellFloat(t, clean, 0, 0, "n")
			dirtyN := wfzCellFloat(t, dirty, 0, 0, "n")
			if dirtyN != cleanN+2 {
				t.Fatalf("polluted floor n = %v, want clean n %v + 2 (null and zero weight rows)", dirtyN, cleanN)
			}
			wfzAssertJSONNear(t, "Overlays", clean.Overlays, dirty.Overlays)
		})
	}
}

func wfzCellFloat(t *testing.T, resp *types.Response, r, c int, key string) float64 {
	t.Helper()
	if resp.Components == nil || resp.Components.Crosstab == nil {
		t.Fatalf("no crosstab components")
	}
	v, ok := resp.Components.Crosstab.CellComponents[r][c][key]
	if !ok {
		t.Fatalf("cell (%d,%d) missing %q", r, c, key)
	}
	f := toFloat64(v)
	if math.IsNaN(f) {
		t.Fatalf("cell (%d,%d) %q is %T, not numeric", r, c, key, v)
	}
	return f
}

// wfzAssertNonVacuous demands real signal on a response: every weighted
// layer present with at least one computed p-value, and every populated
// cell carrying the four weighted-moment keys with non-degenerate
// values. Otherwise parity could hold over two empty layers.
func wfzAssertNonVacuous(t *testing.T, path string, resp *types.Response, overlays []types.OverlaySpec) {
	t.Helper()
	if len(resp.Overlays) != len(overlays) {
		t.Fatalf("%s: %d overlay layers, want %d", path, len(resp.Overlays), len(overlays))
	}
	for i, spec := range overlays {
		layer := resp.Overlays[i]
		if layer.Kind != spec.Kind || layer.Name != spec.Name {
			t.Fatalf("%s: layer[%d] = %s/%s, want %s/%s (order not preserved)",
				path, i, layer.Kind, layer.Name, spec.Kind, spec.Name)
		}
		present := 0
		if mx := layer.Payload.Matrix; mx != nil {
			for _, row := range mx.Cells {
				for _, cell := range row {
					if cell.Present {
						present++
					}
				}
			}
		}
		if present == 0 {
			t.Fatalf("%s: layer %q produced zero present p-values", path, spec.Name)
		}
	}
	if resp.Components == nil || resp.Components.Crosstab == nil {
		t.Fatalf("%s: no crosstab components block", path)
	}
	cells := 0
	for r, row := range resp.Components.Crosstab.CellComponents {
		for c, cell := range row {
			if cell == nil {
				continue
			}
			cells++
			for _, key := range []string{"m2_weighted", "sum_weights_sq", "weighted_variance", "n_eff"} {
				v, ok := cell[key]
				if !ok {
					t.Fatalf("%s: CellComponents[%d][%d] missing %q: %v", path, r, c, key, cell)
				}
				if f := toFloat64(v); !(f > 0) {
					t.Fatalf("%s: CellComponents[%d][%d][%q] = %v, want > 0", path, r, c, key, v)
				}
			}
		}
	}
	if cells != 8 {
		t.Fatalf("%s: %d populated cells, want 8", path, cells)
	}
}

// wfzAssertJSONNear compares two values by their wire form. Numbers are
// compared with relative tolerance wfzRelTol (see the file comment for
// why); everything else — keys, strings, bools, nulls, array lengths —
// exactly.
func wfzAssertJSONNear(t *testing.T, label string, want, got any) {
	t.Helper()
	decode := func(v any) any {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("%s: marshal: %v", label, err)
		}
		var out any
		if err := json.Unmarshal(b, &out); err != nil {
			t.Fatalf("%s: unmarshal: %v", label, err)
		}
		return out
	}
	if diff := jsonNearDiff(label, decode(want), decode(got), wfzRelTol); diff != "" {
		t.Errorf("%s diverges: %s\nbuffered: %s\nfused:    %s", label, diff, jsonOf(t, want), jsonOf(t, got))
	}
}

func jsonNearDiff(path string, want, got any, tol float64) string {
	switch w := want.(type) {
	case float64:
		g, ok := got.(float64)
		if !ok {
			return fmt.Sprintf("%s: want number %v, got %T", path, w, got)
		}
		scale := math.Max(1, math.Max(math.Abs(w), math.Abs(g)))
		if math.Abs(w-g) > tol*scale {
			return fmt.Sprintf("%s: want %.17g, got %.17g", path, w, g)
		}
		return ""
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok || len(g) != len(w) {
			return fmt.Sprintf("%s: object shape differs (want %d keys, got %T)", path, len(w), got)
		}
		for k, wv := range w {
			gv, ok := g[k]
			if !ok {
				return fmt.Sprintf("%s: missing key %q", path, k)
			}
			if d := jsonNearDiff(path+"."+k, wv, gv, tol); d != "" {
				return d
			}
		}
		return ""
	case []any:
		g, ok := got.([]any)
		if !ok || len(g) != len(w) {
			return fmt.Sprintf("%s: array shape differs", path)
		}
		for i := range w {
			if d := jsonNearDiff(fmt.Sprintf("%s[%d]", path, i), w[i], g[i], tol); d != "" {
				return d
			}
		}
		return ""
	default:
		if want != got {
			return fmt.Sprintf("%s: want %v, got %v", path, want, got)
		}
		return ""
	}
}
