package processing

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// pairwiseDistinctHost builds a two-dim row axis (brand, segment) × one
// column, whose cells carry an AGG_DISTINCT_SUM component shape
// {sum, distinct_count} alongside CellCounts. The record counts INFLATE
// against the distinct-key counts — two records per key — which is the
// whole point of the n_within_distinct mode.
//
//	row 0 (acme, urban):   n=4  distinct=2
//	row 1 (acme, rural):   n=2  distinct=1
//	row 2 (zenith, urban): n=4  distinct=2
//	row 3 (zenith, rural): n=0  distinct=0 (nil components slot)
func pairwiseDistinctHost() *CrosstabHostView {
	mx := &types.MatrixPayload{
		RowHeader: types.AxisHeader{
			Fields: []string{"brand", "segment"},
			Types:  []string{"GROUP_SET_PER_ELEMENT", "GROUP_CATEGORY"},
		},
		ColumnHeader: types.AxisHeader{Fields: []string{"wave"}, Types: []string{"GROUP_CATEGORY"}},
		RowKeys: []types.AxisKey{
			{"acme", "urban"}, {"acme", "rural"},
			{"zenith", "urban"}, {"zenith", "rural"},
		},
		ColumnKeys: []types.AxisKey{{"w1"}},
		Cells: [][]types.MatrixCell{
			{{Value: 80.0, Present: true}},
			{{Value: 40.0, Present: true}},
			{{Value: 70.0, Present: true}},
			{{}},
		},
	}
	comps := &types.CrosstabComponents{
		CellCounts: [][]int{{4}, {2}, {4}, {0}},
		CellComponents: [][]map[string]any{
			{{"n": 4, "sum": 80.0, "distinct_count": 2}},
			{{"n": 2, "sum": 40.0, "distinct_count": 1}},
			{{"n": 4, "sum": 70.0, "distinct_count": 2}},
			// Nil slot — a cell no record reached. Contributes ZERO to
			// the slab rather than failing it.
			{nil},
		},
	}
	return newCrosstabHostViewWithComponents(mx, comps)
}

// TestPairwiseCellAggregatorIdentity pins the discriminating key sets
// restated in processing/ from internal/descriptor/capabilities_aggregators.go.
// AGG_FREQUENCY and AGG_MODE must classify as THEMSELVES and never be
// admitted, even though both carry a key spelled "distinct_count".
func TestPairwiseCellAggregatorIdentity(t *testing.T) {
	cases := []struct {
		name     string
		cell     map[string]any
		wantAgg  types.AggregationType
		wantOK   bool
		wantKey  string
		admitted bool
	}{
		{
			name:     "distinct_sum",
			cell:     map[string]any{"n": 4, "sum": 80.0, "distinct_count": 2},
			wantAgg:  types.AGG_DISTINCT_SUM,
			wantOK:   true,
			wantKey:  "distinct_count",
			admitted: true,
		},
		{
			name:     "distinct_count",
			cell:     map[string]any{"n": 4, "cardinality": 3},
			wantAgg:  types.AGG_DISTINCT_COUNT,
			wantOK:   true,
			wantKey:  "cardinality",
			admitted: true,
		},
		{
			name:     "frequency",
			cell:     map[string]any{"n": 4, "distinct_count": 9, "mode_value": "a", "mode_count": 3},
			wantAgg:  types.AGG_FREQUENCY,
			wantOK:   true,
			admitted: false,
		},
		{
			name:     "mode",
			cell:     map[string]any{"n": 4, "value": "a", "count": 3, "distinct_count": 9, "tie_count": 1},
			wantAgg:  types.AGG_MODE,
			wantOK:   true,
			admitted: false,
		},
		{
			name:     "plain sum is unidentified",
			cell:     map[string]any{"n": 4, "sum": 80.0},
			wantOK:   false,
			admitted: false,
		},
		{
			name:     "bare distinct_count is unidentified",
			cell:     map[string]any{"distinct_count": 7},
			wantOK:   false,
			admitted: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mx := &types.MatrixPayload{
				RowKeys:    []types.AxisKey{{"r"}},
				ColumnKeys: []types.AxisKey{{"c"}},
			}
			comps := &types.CrosstabComponents{
				CellComponents: [][]map[string]any{{tc.cell}},
			}
			host := newCrosstabHostViewWithComponents(mx, comps)

			gotAgg, gotOK := host.CellAggregatorIdentity()
			if gotOK != tc.wantOK {
				t.Fatalf("CellAggregatorIdentity ok = %v, want %v (agg %q)", gotOK, tc.wantOK, gotAgg)
			}
			if tc.wantOK && gotAgg != tc.wantAgg {
				t.Fatalf("CellAggregatorIdentity = %q, want %q", gotAgg, tc.wantAgg)
			}

			agg, key, admitted := host.AdmitsDistinctKeyN()
			if admitted != tc.admitted {
				t.Fatalf("AdmitsDistinctKeyN admitted = %v, want %v", admitted, tc.admitted)
			}
			if tc.admitted {
				if key != tc.wantKey {
					t.Fatalf("AdmitsDistinctKeyN key = %q, want %q", key, tc.wantKey)
				}
				if agg != tc.wantAgg {
					t.Fatalf("AdmitsDistinctKeyN agg = %q, want %q", agg, tc.wantAgg)
				}
			}
		})
	}
}

// TestPairwiseRowSlabDistinctN checks the slab walk itself: the same
// prefix match as RowSlabN, distinct-key figures instead of counts, and
// a nil components slot contributing zero rather than failing the slab.
func TestPairwiseRowSlabDistinctN(t *testing.T) {
	host := pairwiseDistinctHost()

	// prefix=1 fixes the fan-out outer dim (brand) and sums across the
	// single-response inner dim (segment).
	gotN, ok := host.RowSlabN(0, 0, 1)
	if !ok {
		t.Fatalf("RowSlabN(0,0,1) not ok")
	}
	if gotN != 6 {
		t.Fatalf("RowSlabN(acme) = %d, want 6 (inflated record count)", gotN)
	}
	gotD, ok := host.RowSlabDistinctN(0, 0, 1)
	if !ok {
		t.Fatalf("RowSlabDistinctN(0,0,1) not ok")
	}
	if gotD != 3 {
		t.Fatalf("RowSlabDistinctN(acme) = %d, want 3 (distinct keys)", gotD)
	}

	// zenith slab: row 3's components slot is nil and must contribute 0.
	gotN, ok = host.RowSlabN(2, 0, 1)
	if !ok || gotN != 4 {
		t.Fatalf("RowSlabN(zenith) = %d ok=%v, want 4", gotN, ok)
	}
	gotD, ok = host.RowSlabDistinctN(2, 0, 1)
	if !ok {
		t.Fatalf("RowSlabDistinctN(zenith) not ok — a nil cell must not fail the slab")
	}
	if gotD != 2 {
		t.Fatalf("RowSlabDistinctN(zenith) = %d, want 2", gotD)
	}

	// prefix=2 narrows to the row itself.
	if got, ok := host.RowSlabDistinctN(0, 0, 2); !ok || got != 2 {
		t.Fatalf("RowSlabDistinctN(acme|urban, prefix 2) = %d ok=%v, want 2", got, ok)
	}
}

// TestPairwiseRowSlabDistinctN_CardinalityKey covers the OTHER admitted
// aggregator: AGG_DISTINCT_COUNT spells its figure "cardinality", so
// the slab accumulator must find it there and not fall back to zero.
func TestPairwiseRowSlabDistinctN_CardinalityKey(t *testing.T) {
	mx := &types.MatrixPayload{
		RowKeys: []types.AxisKey{
			{"acme", "urban"}, {"acme", "rural"}, {"zenith", "urban"},
		},
		ColumnKeys: []types.AxisKey{{"w1"}},
	}
	comps := &types.CrosstabComponents{
		CellCounts: [][]int{{4}, {2}, {4}},
		CellComponents: [][]map[string]any{
			{{"n": 4, "cardinality": 2}},
			{{"n": 2, "cardinality": 1}},
			{{"n": 4, "cardinality": 2}},
		},
	}
	host := newCrosstabHostViewWithComponents(mx, comps)

	if agg, key, ok := host.AdmitsDistinctKeyN(); !ok || agg != types.AGG_DISTINCT_COUNT || key != "cardinality" {
		t.Fatalf("AdmitsDistinctKeyN = (%q, %q, %v), want (AGG_DISTINCT_COUNT, cardinality, true)", agg, key, ok)
	}
	if got, ok := host.RowSlabN(0, 0, 1); !ok || got != 6 {
		t.Fatalf("RowSlabN(acme) = %d ok=%v, want 6", got, ok)
	}
	if got, ok := host.RowSlabDistinctN(0, 0, 1); !ok || got != 3 {
		t.Fatalf("RowSlabDistinctN(acme) = %d ok=%v, want 3 via the cardinality key", got, ok)
	}
}

// TestPairwiseColumnSlabDistinctN mirrors the row assertions on the
// column axis so neither accessor can drift from the other.
func TestPairwiseColumnSlabDistinctN(t *testing.T) {
	mx := &types.MatrixPayload{
		RowKeys: []types.AxisKey{{"r0"}},
		ColumnKeys: []types.AxisKey{
			{"acme", "urban"}, {"acme", "rural"}, {"zenith", "urban"},
		},
	}
	comps := &types.CrosstabComponents{
		CellCounts: [][]int{{4, 2, 4}},
		CellComponents: [][]map[string]any{{
			{"n": 4, "sum": 80.0, "distinct_count": 2},
			nil,
			{"n": 4, "sum": 70.0, "distinct_count": 2},
		}},
	}
	host := newCrosstabHostViewWithComponents(mx, comps)

	if got, ok := host.ColumnSlabN(0, 0, 1); !ok || got != 6 {
		t.Fatalf("ColumnSlabN(acme) = %d ok=%v, want 6", got, ok)
	}
	// The (acme, rural) slot is nil: 2 + 0.
	if got, ok := host.ColumnSlabDistinctN(0, 0, 1); !ok || got != 2 {
		t.Fatalf("ColumnSlabDistinctN(acme) = %d ok=%v, want 2", got, ok)
	}
	if got, ok := host.ColumnSlabDistinctN(0, 2, 1); !ok || got != 2 {
		t.Fatalf("ColumnSlabDistinctN(zenith) = %d ok=%v, want 2", got, ok)
	}
}

// TestOverlayPairwise_NWithinDistinctRefusesFrequencyCells is the
// load-bearing admission test: an AGG_FREQUENCY cell host carries a key
// SPELLED distinct_count whose meaning is distinct VALUES of the measure
// field. A key-presence gate would read it as a sample size. The refusal
// must be up front, coded, and must NAME both the observed aggregator
// and the admitted set.
func TestOverlayPairwise_NWithinDistinctRefusesFrequencyCells(t *testing.T) {
	mx := &types.MatrixPayload{
		RowHeader:    types.AxisHeader{Fields: []string{"brand", "segment"}},
		ColumnHeader: types.AxisHeader{Fields: []string{"wave"}},
		RowKeys:      []types.AxisKey{{"acme", "urban"}, {"acme", "rural"}},
		ColumnKeys:   []types.AxisKey{{"w1"}},
		Cells: [][]types.MatrixCell{
			{{Value: 80.0, Present: true}},
			{{Value: 40.0, Present: true}},
		},
	}
	comps := &types.CrosstabComponents{
		CellCounts: [][]int{{4}, {2}},
		CellComponents: [][]map[string]any{
			{{"n": 4, "distinct_count": 9, "mode_value": "a", "mode_count": 3}},
			{{"n": 2, "distinct_count": 5, "mode_value": "b", "mode_count": 2}},
		},
	}
	host := newCrosstabHostViewWithComponents(mx, comps)

	specs := []types.OverlaySpec{{
		Kind:   types.OverlayKindPairwisePropZ,
		Scope:  types.OverlayScopeRow,
		Params: json.RawMessage(`{"n_source":"n_within_distinct","n_within_depth":0}`),
	}}
	layers, _, err := applyOverlays(specs, host)
	if err == nil {
		t.Fatalf("expected refusal on an AGG_FREQUENCY cell host, got %d layer(s)", len(layers))
	}
	if !pairwiseErrHasCode(err, errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE) {
		t.Fatalf("error %v does not carry PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE", err)
	}
	msg := err.Error()
	for _, want := range []string{
		string(types.AGG_FREQUENCY),
		string(types.AGG_DISTINCT_SUM),
		string(types.AGG_DISTINCT_COUNT),
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("refusal message %q does not name %q", msg, want)
		}
	}

	coded := pairwiseCodedError(t, err)
	if got, _ := coded.Details["observed_cell_aggregator"].(string); got != string(types.AGG_FREQUENCY) {
		t.Fatalf("details observed_cell_aggregator = %q, want %q", got, types.AGG_FREQUENCY)
	}
	adm, _ := coded.Details["admitted_cell_aggregators"].([]string)
	if len(adm) != 2 || adm[0] != string(types.AGG_DISTINCT_SUM) || adm[1] != string(types.AGG_DISTINCT_COUNT) {
		t.Fatalf("details admitted_cell_aggregators = %v, want [AGG_DISTINCT_SUM AGG_DISTINCT_COUNT]", adm)
	}
}

// TestOverlayPairwise_NWithinDistinctRefusesPlainSumCells covers the
// unidentified branch — a cell aggregator that carries no distinct-key
// figure at all is refused just as loudly, never silently skipped.
func TestOverlayPairwise_NWithinDistinctRefusesPlainSumCells(t *testing.T) {
	host := pairwisePropHost() // cells carry {"n": 100} only
	specs := []types.OverlaySpec{{
		Kind:   types.OverlayKindPairwisePropZ,
		Scope:  types.OverlayScopeRow,
		Params: json.RawMessage(`{"n_source":"n_within_distinct"}`),
	}}
	_, _, err := applyOverlays(specs, host)
	if err == nil {
		t.Fatal("expected refusal on a host with no distinct-key figure")
	}
	if !pairwiseErrHasCode(err, errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE) {
		t.Fatalf("error %v does not carry PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE", err)
	}
	if !strings.Contains(err.Error(), "unidentified") {
		t.Fatalf("refusal message %q does not report an unidentified cell aggregator", err.Error())
	}
}

// TestOverlayPairwise_NWithinDistinctAdmittedProducesLayer proves the
// admitted path runs end to end AND that its p-values come from the
// distinct-key slab, not the record-count slab: the two modes must
// disagree on this host.
func TestOverlayPairwise_NWithinDistinctAdmittedProducesLayer(t *testing.T) {
	host := pairwiseDistinctHost()

	run := func(nSource string) float64 {
		t.Helper()
		specs := []types.OverlaySpec{{
			Kind:   types.OverlayKindPairwisePropZ,
			Scope:  types.OverlayScopeRow,
			Params: json.RawMessage(`{"n_source":"` + nSource + `","n_within_depth":0}`),
		}}
		layers, _, err := applyOverlays(specs, host)
		if err != nil {
			t.Fatalf("applyOverlays(%s): %v", nSource, err)
		}
		mx := layers[0].Payload.Matrix
		// Rows are pairs in axis-natural order; pair index 1 is
		// (acme|urban, zenith|urban).
		cell := mx.Cells[1][0]
		if !cell.Present {
			t.Fatalf("pair (acme|urban, zenith|urban) absent under %s", nSource)
		}
		v, _ := cell.Value.(float64)
		return v
	}

	within := run(types.PairwiseNSourceNWithin)
	distinct := run(types.PairwiseNSourceNWithinDistinct)

	// n_within legs are (6, 4); n_within_distinct legs are (3, 2).
	wantWithin, ok := twoProportionZ(0.80*6, 6, 0.70*4, 4)
	if !ok {
		t.Fatal("twoProportionZ(n_within legs) undefined")
	}
	wantDistinct, ok := twoProportionZ(0.80*3, 3, 0.70*2, 2)
	if !ok {
		t.Fatal("twoProportionZ(distinct legs) undefined")
	}
	if math.Abs(within-wantWithin) > 1e-12 {
		t.Fatalf("n_within p = %v, want %v", within, wantWithin)
	}
	if math.Abs(distinct-wantDistinct) > 1e-12 {
		t.Fatalf("n_within_distinct p = %v, want %v", distinct, wantDistinct)
	}
	if within == distinct {
		t.Fatal("n_within and n_within_distinct produced identical p-values; the distinct leg is not reaching the slab")
	}
}

// TestOverlayPairwise_NWithinDistinctDepthGuard proves the existing
// n_within_depth range guard fires for the distinct mode too — it was
// keyed on n_within alone.
func TestOverlayPairwise_NWithinDistinctDepthGuard(t *testing.T) {
	host := pairwiseDistinctHost() // row axis depth = 2
	specs := []types.OverlaySpec{{
		Kind:   types.OverlayKindPairwisePropZ,
		Scope:  types.OverlayScopeRow,
		Params: json.RawMessage(`{"n_source":"n_within_distinct","n_within_depth":2}`),
	}}
	_, _, err := applyOverlays(specs, host)
	if err == nil {
		t.Fatal("expected n_within_depth range refusal, got nil")
	}
	if !pairwiseErrHasCode(err, errors.PULSE_OVERLAY_PARAM_MISSING) {
		t.Fatalf("error %v does not carry PULSE_OVERLAY_PARAM_MISSING", err)
	}
}

// TestOverlayPairwise_DistinctModeInertWhenUnset is the byte-identity
// half: a spec that does not name n_within_distinct must produce the
// exact layer JSON it produced before the mode existed. The literal is
// the pre-change baseline for the canonical 3×2 prop-Z host.
// pairwiseBaselineLayerJSON is the marshalled OVERLAY_PAIRWISE_PROP_Z
// layer for pairwisePropHost() under a params-free spec, captured from
// the pre-n_within_distinct tree (HEAD 9789d05) and verified byte-equal
// after the change. It is the additive-inertness pin: the new mode must
// be unreachable from a spec that does not name it.
const pairwiseBaselineLayerJSON = `[{"name":"pz","kind":"OVERLAY_PAIRWISE_PROP_Z","scope":"row","ref":{},"payload":{"shape":"matrix","matrix":{"row_header":{"fields":["pair_a","pair_b"],"types":["PAIR","PAIR"]},"column_header":{"fields":["aud"],"types":["GROUP_CATEGORY"]},"row_keys":[["A","B"],["A","C"],["B","C"]],"column_keys":[["x"],["y"]],"cells":[[{"value":0.0038924171227785465,"present":true},{"value":1,"present":true}],[{"value":6.737437274750846e-10,"present":true},{"value":1,"present":true}],[{"value":0.00040695201744500586,"present":true},{"value":1,"present":true}]],"grand_total":{"present":false},"cell_label":"p_value","normalize_applied":""}},"summary":{"count":6}}]`

func TestOverlayPairwise_DistinctModeInertWhenUnset(t *testing.T) {
	host := pairwisePropHost()
	specs := []types.OverlaySpec{{
		Name:  "pz",
		Kind:  types.OverlayKindPairwisePropZ,
		Scope: types.OverlayScopeRow,
	}}
	layers, warns, err := applyOverlays(specs, host)
	if err != nil {
		t.Fatalf("applyOverlays: %v", err)
	}
	if len(warns) != 0 {
		t.Fatalf("expected no warnings on the baseline host, got %v", warns)
	}
	got, err := json.Marshal(layers)
	if err != nil {
		t.Fatalf("marshal layers: %v", err)
	}
	if string(got) != pairwiseBaselineLayerJSON {
		t.Fatalf("baseline layer JSON drifted:\n got %s\nwant %s", got, pairwiseBaselineLayerJSON)
	}
}

// pairwiseCodedError unwraps to the typed error so details can be
// asserted key by key. pairwiseErrHasCode (overlay_pairwise_test.go)
// covers the code itself.
func pairwiseCodedError(t *testing.T, err error) *errors.CodedError {
	t.Helper()
	coded, ok := err.(*errors.CodedError)
	if !ok {
		t.Fatalf("error %v is not a *errors.CodedError", err)
	}
	return coded
}
