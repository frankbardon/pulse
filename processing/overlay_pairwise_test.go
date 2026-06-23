package processing

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// pairwisePropHost builds a 3-row × 2-col crosstab whose cells are
// percentages (0..100) plus a components block carrying per-cell n. Row
// keys are single-dim category labels.
func pairwisePropHost() *CrosstabHostView {
	mx := &types.MatrixPayload{
		RowHeader:    types.AxisHeader{Fields: []string{"brand"}, Types: []string{"GROUP_CATEGORY"}},
		ColumnHeader: types.AxisHeader{Fields: []string{"aud"}, Types: []string{"GROUP_CATEGORY"}},
		RowKeys:      []types.AxisKey{{"A"}, {"B"}, {"C"}},
		ColumnKeys:   []types.AxisKey{{"x"}, {"y"}},
		Cells: [][]types.MatrixCell{
			{{Value: 50.0, Present: true}, {Value: 20.0, Present: true}},
			{{Value: 30.0, Present: true}, {Value: 20.0, Present: true}},
			{{Value: 10.0, Present: true}, {Value: 20.0, Present: true}},
		},
	}
	comps := &types.CrosstabComponents{
		CellComponents: [][]map[string]any{
			{{"n": 100}, {"n": 100}},
			{{"n": 100}, {"n": 100}},
			{{"n": 100}, {"n": 100}},
		},
	}
	return NewCrosstabHostViewWithComponents(mx, comps)
}

func TestOverlayPairwise_PropZ_RowScope(t *testing.T) {
	host := pairwisePropHost()
	specs := []types.OverlaySpec{{
		Name:  "pz",
		Kind:  types.OverlayKindPairwisePropZ,
		Scope: types.OverlayScopeRow,
	}}
	layers, _, err := ApplyOverlays(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlays: %v", err)
	}
	if len(layers) != 1 {
		t.Fatalf("expected 1 layer, got %d", len(layers))
	}
	mx := layers[0].Payload.Matrix
	if mx == nil {
		t.Fatalf("nil matrix payload")
	}
	// 3 rows -> 3 pairs (A,B),(A,C),(B,C); 2 opposite columns.
	if len(mx.RowKeys) != 3 {
		t.Fatalf("expected 3 pair rows, got %d", len(mx.RowKeys))
	}
	if got := mx.RowKeys[0]; got[0] != "A" || got[1] != "B" {
		t.Fatalf("pair[0] key = %v, want [A B]", got)
	}
	if len(mx.ColumnKeys) != 2 {
		t.Fatalf("expected 2 opposite columns, got %d", len(mx.ColumnKeys))
	}
	// Pair (A,B) at column x: p1=0.50 n1=100, p2=0.30 n2=100. Parity:
	// the cell p-value must equal the shared twoProportionZ helper.
	wantP, ok := twoProportionZ(0.50*100, 100, 0.30*100, 100)
	if !ok {
		t.Fatalf("twoProportionZ unexpectedly undefined")
	}
	got := mx.Cells[0][0]
	if !got.Present {
		t.Fatalf("pair (A,B) col x cell absent")
	}
	gv, _ := got.Value.(float64)
	if math.Abs(gv-wantP) > 1e-12 {
		t.Fatalf("pair (A,B) col x p = %v, want %v", gv, wantP)
	}
}

func TestOverlayPairwise_ColumnScope(t *testing.T) {
	host := pairwisePropHost()
	specs := []types.OverlaySpec{{
		Kind:  types.OverlayKindPairwisePropZ,
		Scope: types.OverlayScopeColumn,
	}}
	layers, _, err := ApplyOverlays(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlays: %v", err)
	}
	mx := layers[0].Payload.Matrix
	// 2 columns -> 1 pair (x,y); rows echo the 3 host rows.
	if len(mx.ColumnKeys) != 1 {
		t.Fatalf("expected 1 pair column, got %d", len(mx.ColumnKeys))
	}
	if len(mx.RowKeys) != 3 {
		t.Fatalf("expected 3 host rows, got %d", len(mx.RowKeys))
	}
}

func TestOverlayPairwise_PairAlongDim(t *testing.T) {
	// Two-dim column axis: (wave, aud). pair_along_dim=1 pairs auds within
	// each wave bucket only — never across waves.
	mx := &types.MatrixPayload{
		RowHeader:    types.AxisHeader{Fields: []string{"brand"}, Types: []string{"GROUP_CATEGORY"}},
		ColumnHeader: types.AxisHeader{Fields: []string{"wave", "aud"}, Types: []string{"GROUP_CATEGORY", "GROUP_CATEGORY"}},
		RowKeys:      []types.AxisKey{{"A"}},
		ColumnKeys:   []types.AxisKey{{"2021", "all"}, {"2021", "owner"}, {"2022", "all"}, {"2022", "owner"}},
		Cells: [][]types.MatrixCell{
			{{Value: 50.0, Present: true}, {Value: 30.0, Present: true}, {Value: 40.0, Present: true}, {Value: 20.0, Present: true}},
		},
	}
	comps := &types.CrosstabComponents{
		CellComponents: [][]map[string]any{
			{{"n": 100}, {"n": 100}, {"n": 100}, {"n": 100}},
		},
	}
	host := NewCrosstabHostViewWithComponents(mx, comps)
	dim := 1
	specs := []types.OverlaySpec{{
		Kind:   types.OverlayKindPairwisePropZ,
		Scope:  types.OverlayScopeColumn,
		Params: mustParams(t, types.PairwiseOverlayParams{PairAlongDim: &dim}),
	}}
	layers, _, err := ApplyOverlays(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlays: %v", err)
	}
	out := layers[0].Payload.Matrix
	// Expect exactly 2 pairs: (2021/all,2021/owner) and (2022/all,2022/owner).
	if len(out.ColumnKeys) != 2 {
		t.Fatalf("expected 2 within-wave pairs, got %d: %v", len(out.ColumnKeys), out.ColumnKeys)
	}
	for _, k := range out.ColumnKeys {
		if k[0] == k[1] {
			t.Fatalf("degenerate self-pair %v", k)
		}
	}
}

func TestOverlayPairwise_WelchT_Welford(t *testing.T) {
	mx := &types.MatrixPayload{
		RowHeader:    types.AxisHeader{Fields: []string{"brand"}, Types: []string{"GROUP_CATEGORY"}},
		ColumnHeader: types.AxisHeader{Fields: []string{"aud"}, Types: []string{"GROUP_CATEGORY"}},
		RowKeys:      []types.AxisKey{{"A"}, {"B"}},
		ColumnKeys:   []types.AxisKey{{"x"}},
		Cells: [][]types.MatrixCell{
			{{Value: 10.0, Present: true}},
			{{Value: 12.0, Present: true}},
		},
	}
	comps := &types.CrosstabComponents{
		CellComponents: [][]map[string]any{
			{{"mean": 10.0, "variance": 4.0, "n": 50}},
			{{"mean": 12.0, "variance": 9.0, "n": 60}},
		},
	}
	host := NewCrosstabHostViewWithComponents(mx, comps)
	specs := []types.OverlaySpec{{Kind: types.OverlayKindPairwiseWelchT, Scope: types.OverlayScopeRow}}
	layers, _, err := ApplyOverlays(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlays: %v", err)
	}
	cell := layers[0].Payload.Matrix.Cells[0][0]
	if !cell.Present {
		t.Fatalf("welch pair cell absent")
	}
	// Closed-form Welch: a=4/50, b=9/60; se=sqrt(a+b); t=(10-12)/se;
	// df via Satterthwaite; p = studentTTwoSidedP(t, df).
	a, b := 4.0/50, 9.0/60
	se := math.Sqrt(a + b)
	tStat := (10.0 - 12.0) / se
	df := (a + b) * (a + b) / ((a*a)/49 + (b*b)/59)
	want := studentTTwoSidedP(tStat, df)
	gv, _ := cell.Value.(float64)
	if math.Abs(gv-want) > 1e-12 {
		t.Fatalf("welch p = %v, want %v", gv, want)
	}
}

func TestOverlayPairwise_ComponentsRequired(t *testing.T) {
	mx := &types.MatrixPayload{
		RowHeader:    types.AxisHeader{Fields: []string{"brand"}, Types: []string{"GROUP_CATEGORY"}},
		ColumnHeader: types.AxisHeader{Fields: []string{"aud"}, Types: []string{"GROUP_CATEGORY"}},
		RowKeys:      []types.AxisKey{{"A"}, {"B"}},
		ColumnKeys:   []types.AxisKey{{"x"}},
		Cells:        [][]types.MatrixCell{{{Value: 50.0, Present: true}}, {{Value: 30.0, Present: true}}},
	}
	// No components.
	host := NewCrosstabHostView(mx)
	specs := []types.OverlaySpec{{Kind: types.OverlayKindPairwisePropZ, Scope: types.OverlayScopeRow}}
	_, _, err := ApplyOverlays(specs, host)
	if err == nil {
		t.Fatalf("expected COMPONENTS_REQUIRED error, got nil")
	}
	if !pairwiseErrHasCode(err, errors.PULSE_OVERLAY_COMPONENTS_REQUIRED) {
		t.Fatalf("error %v does not carry PULSE_OVERLAY_COMPONENTS_REQUIRED", err)
	}
}

func TestOverlayPairwise_NonWelfordShapeMismatch(t *testing.T) {
	// Welch kind against a host with only "n" (no mean/variance) — must
	// fail the Welford-shape gate.
	host := pairwisePropHost()
	specs := []types.OverlaySpec{{Kind: types.OverlayKindPairwiseWelchT, Scope: types.OverlayScopeRow}}
	_, _, err := ApplyOverlays(specs, host)
	if err == nil {
		t.Fatalf("expected shape-mismatch error, got nil")
	}
	if !pairwiseErrHasCode(err, errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE) {
		t.Fatalf("error %v does not carry PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE", err)
	}
}

func TestStandardNormalPPF_RoundTrip(t *testing.T) {
	for _, p := range []float64{0.01, 0.1, 0.25, 0.5, 0.75, 0.9, 0.975, 0.99} {
		z := standardNormalPPF(p)
		back := standardNormalCDF(z)
		if math.Abs(back-p) > 1e-6 {
			t.Fatalf("ppf/cdf round-trip for p=%v: got %v", p, back)
		}
	}
	if z := standardNormalPPF(0.5); math.Abs(z) > 1e-9 {
		t.Fatalf("ppf(0.5) = %v, want ~0", z)
	}
}

func mustParams(t *testing.T, p types.PairwiseOverlayParams) []byte {
	t.Helper()
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	return raw
}

// containsCode reports whether err is a *CodedError carrying code in its
// Details["code"] key (the canonical overlay-code carrier).
func pairwiseErrHasCode(err error, code errors.Code) bool {
	coded, ok := err.(*errors.CodedError)
	if !ok {
		return false
	}
	c, _ := coded.Details["code"].(string)
	return c == string(code)
}
