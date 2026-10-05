package service

import (
	"bytes"
	"context"
	"math"
	"reflect"
	"slices"
	"testing"

	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/internal/processing/multiplicity"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// TestOverlayPSites_CoverEveryInferentialKind: the p-site table has an
// entry for exactly the overlay kinds the catalog flags Inferential —
// a new inferential kind without an entry (or a descriptive kind with
// one) fails here — and each entry reads a payload shape the kind
// really emits.
func TestOverlayPSites_CoverEveryInferentialKind(t *testing.T) {
	wantShape := map[overlayPSite]types.OverlayShape{
		pSiteSummaryPValue:   types.OverlayShapeScalar,
		pSiteSeriesPValue:    types.OverlayShapeSeries,
		pSiteSeriesStatistic: types.OverlayShapeSeries,
		pSiteMatrixCell:      types.OverlayShapeMatrix,
		pSiteMatrixPanel:     types.OverlayShapeMatrix,
	}
	caps := descx.OverlayCapabilities()
	if len(caps) != len(types.AllOverlayKinds()) {
		t.Fatalf("capabilities cover %d kinds, catalog has %d", len(caps), len(types.AllOverlayKinds()))
	}
	inferential := 0
	for _, c := range caps {
		site, ok := overlayPSites[c.Kind]
		if c.Inferential != ok {
			t.Errorf("%s: inferential=%v but p-site entry present=%v", c.Kind, c.Inferential, ok)
			continue
		}
		if !ok {
			continue
		}
		inferential++
		if !slices.Contains(c.Shapes, wantShape[site]) {
			t.Errorf("%s: p-site %d reads a %s payload, kind emits %v", c.Kind, site, wantShape[site], c.Shapes)
		}
	}
	if inferential != len(overlayPSites) {
		t.Errorf("table has %d entries, catalog flags %d inferential kinds", len(overlayPSites), inferential)
	}
}

func overlayMember(slot string, kind types.OverlayKind, method types.MultiplicityMethod, family types.MultiplicityFamily, alpha float64) descx.ResolvedMultiplicity {
	return descx.ResolvedMultiplicity{Slot: slot, Operator: string(kind), Method: method, Family: family, Alpha: alpha, Member: true}
}

func scalarLayer(kind types.OverlayKind, stat, p float64) types.OverlayLayer {
	return types.OverlayLayer{Kind: kind, Payload: types.OverlayPayload{Shape: types.OverlayShapeScalar, Scalar: f(stat)},
		Summary: &types.OverlaySummary{Statistic: f(stat), PValue: f(p)}}
}

func seriesLayer(kind types.OverlayKind, inStatistic bool, ps ...*float64) types.OverlayLayer {
	entries := make([]types.SeriesEntry, len(ps))
	for i, p := range ps {
		entries[i].Key = types.AxisKey{i}
		if inStatistic {
			entries[i].Summary.Statistic = p
		} else {
			entries[i].Summary.Statistic = f(9)
			entries[i].Summary.PValue = p
		}
	}
	return types.OverlayLayer{Kind: kind, Payload: types.OverlayPayload{Shape: types.OverlayShapeSeries, Series: &types.SeriesPayload{Entries: entries}}}
}

func matrixLayer(kind types.OverlayKind, cells [][]types.MatrixCell) types.OverlayLayer {
	return types.OverlayLayer{Kind: kind, Payload: types.OverlayPayload{Shape: types.OverlayShapeMatrix, Matrix: &types.MatrixPayload{
		RowHeader: types.AxisHeader{Fields: []string{"r"}}, ColumnHeader: types.AxisHeader{Fields: []string{"c"}},
		RowKeys: []types.AxisKey{{"a"}, {"b"}}, ColumnKeys: []types.AxisKey{{"u"}, {"v"}},
		Cells: cells, CellLabel: "p_value",
	}}}
}

func cell(v any) types.MatrixCell { return types.MatrixCell{Value: v, Present: true} }

// TestFoldOverlayMultiplicity_PSites: every p-site shape is read, the
// adjusted figures land beside the raw ones (summary, series entry,
// parallel matrices on identical coordinates, panel vectors element for
// element), the base payload is byte-identical, the flag reads the
// resolved alpha and the layer echo carries {method, family, alpha, m}.
func TestFoldOverlayMultiplicity_PSites(t *testing.T) {
	bonf, holm := types.MultiplicityMethodBonferroni, types.MultiplicityMethodHolm
	layer := types.MultiplicityFamilyLayer
	nan := math.NaN()
	cases := []struct {
		name  string
		kind  types.OverlayKind
		build func() types.OverlayLayer
		alpha float64
		// read returns the adjusted p-values and flags (nil = undefined)
		// in site order.
		read  func(t *testing.T, l *types.OverlayLayer) ([]float64, []*bool)
		raw   []float64
		wantM int
	}{
		{
			name: "summary p_value (CHISQ_MATRIX)", kind: types.OverlayKindChiSqMatrix, alpha: 0.05,
			build: func() types.OverlayLayer { return scalarLayer(types.OverlayKindChiSqMatrix, 7.5, 0.02) },
			read: func(t *testing.T, l *types.OverlayLayer) ([]float64, []*bool) {
				return []float64{*l.Summary.PAdjusted}, []*bool{l.Summary.SignificantAdjusted}
			},
			raw: []float64{0.02}, wantM: 1,
		},
		{
			name: "series p_value (CHISQ_ROW)", kind: types.OverlayKindChiSqRow, alpha: 0.05,
			build: func() types.OverlayLayer {
				return seriesLayer(types.OverlayKindChiSqRow, false, f(0.01), f(0.04), nil, f(nan))
			},
			read: func(t *testing.T, l *types.OverlayLayer) ([]float64, []*bool) {
				var ps []float64
				var sig []*bool
				for i, e := range l.Payload.Series.Entries {
					if i == 2 {
						if e.Summary.PAdjusted != nil {
							t.Error("an entry without a p gained an adjusted one")
						}
						continue
					}
					ps, sig = append(ps, *e.Summary.PAdjusted), append(sig, e.Summary.SignificantAdjusted)
				}
				return ps, sig
			},
			raw: []float64{0.01, 0.04, nan}, wantM: 2,
		},
		{
			name: "series statistic (T_VS_REF), alpha 0.1", kind: types.OverlayKindTVsRef, alpha: 0.1,
			build: func() types.OverlayLayer { return seriesLayer(types.OverlayKindTVsRef, true, f(0.03), f(0.045)) },
			read: func(t *testing.T, l *types.OverlayLayer) ([]float64, []*bool) {
				var ps []float64
				var sig []*bool
				for _, e := range l.Payload.Series.Entries {
					ps, sig = append(ps, *e.Summary.PAdjusted), append(sig, e.Summary.SignificantAdjusted)
				}
				return ps, sig
			},
			raw: []float64{0.03, 0.045}, wantM: 2,
		},
		{
			name: "matrix cells (FISHER_EXACT_CELL)", kind: types.OverlayKindFisherExactCell, alpha: 0.05,
			build: func() types.OverlayLayer {
				return matrixLayer(types.OverlayKindFisherExactCell, [][]types.MatrixCell{
					{cell(0.01), {Value: 0.9, Present: false}}, // an absent cell never joins, whatever it holds
					{cell(0.02), cell(nan)},
				})
			},
			read: func(t *testing.T, l *types.OverlayLayer) ([]float64, []*bool) {
				pa, sa := l.Payload.PAdjusted, l.Payload.SignificantAdjusted
				if pa == nil || sa == nil {
					t.Fatal("parallel matrices missing")
				}
				base := l.Payload.Matrix
				for _, m := range []*types.MatrixPayload{pa, sa} {
					if !reflect.DeepEqual(m.RowKeys, base.RowKeys) || !reflect.DeepEqual(m.ColumnKeys, base.ColumnKeys) ||
						!reflect.DeepEqual(m.RowHeader, base.RowHeader) || !reflect.DeepEqual(m.ColumnHeader, base.ColumnHeader) ||
						len(m.Cells) != len(base.Cells) || len(m.Cells[0]) != len(base.Cells[0]) {
						t.Fatalf("parallel matrix %q off the base coordinates", m.CellLabel)
					}
				}
				if pa.Cells[0][1].Present || sa.Cells[0][1].Present {
					t.Error("an absent base cell is present in a parallel matrix")
				}
				if !pa.Cells[1][1].Present || sa.Cells[1][1].Present {
					t.Error("an undefined p: p_adjusted cell must be present (null), significant cell absent")
				}
				var ps []float64
				var sig []*bool
				for _, rc := range [][2]int{{0, 0}, {1, 0}, {1, 1}} {
					ps = append(ps, pa.Cells[rc[0]][rc[1]].Value.(float64))
					if s := sa.Cells[rc[0]][rc[1]]; s.Present {
						b := s.Value.(bool)
						sig = append(sig, &b)
					} else {
						sig = append(sig, nil)
					}
				}
				return ps, sig
			},
			raw: []float64{0.01, 0.02, nan}, wantM: 2,
		},
		{
			name: "panel vectors (PROP_Z_PANEL)", kind: types.OverlayKindPropZPanel, alpha: 0.05,
			build: func() types.OverlayLayer {
				return matrixLayer(types.OverlayKindPropZPanel, [][]types.MatrixCell{
					{cell([]float64{0.001, 0.2, nan}), {Value: []float64{0.9}, Present: false}},
					{{Present: false}, cell([]float64{0.01, 0.03, 0.04})},
				})
			},
			read: func(t *testing.T, l *types.OverlayLayer) ([]float64, []*bool) {
				pa, sa := l.Payload.PAdjusted, l.Payload.SignificantAdjusted
				var ps []float64
				var sig []*bool
				for _, rc := range [][2]int{{0, 0}, {1, 1}} {
					ps = append(ps, pa.Cells[rc[0]][rc[1]].Value.([]float64)...)
					sig = append(sig, sa.Cells[rc[0]][rc[1]].Value.([]*bool)...)
				}
				if pa.Cells[0][1].Present || sa.Cells[1][0].Present {
					t.Error("an absent panel cell is present in a parallel matrix")
				}
				return ps, sig
			},
			raw: []float64{0.001, 0.2, nan, 0.01, 0.03, 0.04}, wantM: 5,
		},
	}
	for _, method := range []types.MultiplicityMethod{bonf, holm} {
		for _, c := range cases {
			t.Run(string(method)+"/"+c.name, func(t *testing.T) {
				l := c.build()
				before := mustMarshal(t, l)
				resp := &types.Response{Overlays: []types.OverlayLayer{l}}
				plan := &descx.MultiplicityPlan{Overlays: []descx.ResolvedMultiplicity{overlayMember("overlays[0]", c.kind, method, layer, c.alpha)}}
				if err := foldRequestMultiplicity(plan, resp); err != nil {
					t.Fatal(err)
				}
				got := &resp.Overlays[0]
				// The base payload and summary are untouched.
				stripped := *got
				stripped.Multiplicity = nil
				stripped.Payload.PAdjusted, stripped.Payload.SignificantAdjusted = nil, nil
				if stripped.Summary != nil {
					s := *stripped.Summary
					s.PAdjusted, s.SignificantAdjusted = nil, nil
					stripped.Summary = &s
				}
				if stripped.Payload.Series != nil {
					entries := slices.Clone(stripped.Payload.Series.Entries)
					for i := range entries {
						entries[i].Summary.PAdjusted, entries[i].Summary.SignificantAdjusted = nil, nil
					}
					stripped.Payload.Series = &types.SeriesPayload{Entries: entries}
				}
				if after := mustMarshal(t, stripped); !bytes.Equal(before, after) {
					t.Errorf("base layer changed:\n got %s\nwant %s", after, before)
				}
				want, err := multiplicity.Adjust(multiplicity.Method(method), c.raw)
				if err != nil {
					t.Fatal(err)
				}
				ps, sig := c.read(t, got)
				if len(ps) != len(want) {
					t.Fatalf("read %d adjusted p-values, want %d", len(ps), len(want))
				}
				moved := false
				for i := range want {
					if !sameFloat(ps[i], want[i]) {
						t.Errorf("site %d p_adjusted %v, want %v", i, ps[i], want[i])
					}
					moved = moved || !sameFloat(ps[i], c.raw[i])
					switch {
					case math.IsNaN(want[i]):
						if sig[i] != nil {
							t.Errorf("site %d: undefined p carries a significance flag", i)
						}
					case sig[i] == nil || *sig[i] != (want[i] < c.alpha):
						t.Errorf("site %d significant_adjusted %v, want %v (alpha %v)", i, sig[i], want[i] < c.alpha, c.alpha)
					}
				}
				if !moved && c.wantM > 1 {
					t.Error("no adjusted p moved; the case proves nothing")
				}
				wantEcho := &types.AppliedMultiplicity{Method: method, Family: layer, Alpha: c.alpha, M: c.wantM}
				if !reflect.DeepEqual(got.Multiplicity, wantEcho) {
					t.Errorf("echo %+v, want %+v", got.Multiplicity, wantEcho)
				}
			})
		}
	}
}

// TestFoldOverlayMultiplicity_Families: `layer` never mixes layers;
// `request` pools request-family layers with the tests; a descriptive
// or non-member layer is never touched.
func TestFoldOverlayMultiplicity_Families(t *testing.T) {
	bonf := types.MultiplicityMethodBonferroni
	chi, fisher := types.OverlayKindChiSqMatrix, types.OverlayKindFisherExactCell
	build := func() *types.Response {
		return &types.Response{
			Tests: []*types.TestResult{result(types.TEST_T, 0.01)},
			Overlays: []types.OverlayLayer{
				scalarLayer(chi, 5, 0.02),
				matrixLayer(fisher, [][]types.MatrixCell{{cell(0.01), cell(0.03)}, {cell(0.2), cell(0.4)}}),
				scalarLayer(chi, 5, 0.03),
			},
		}
	}
	cases := []struct {
		name string
		plan *descx.MultiplicityPlan
		// wantM per overlay layer (0 = no echo) and for the test.
		wantM    [3]int
		wantTest int
	}{
		{
			name: "layer families stay inside each layer",
			plan: &descx.MultiplicityPlan{Overlays: []descx.ResolvedMultiplicity{
				overlayMember("overlays[0]", chi, bonf, types.MultiplicityFamilyLayer, 0.05),
				overlayMember("overlays[1]", fisher, bonf, types.MultiplicityFamilyLayer, 0.05),
				overlayMember("overlays[2]", chi, bonf, types.MultiplicityFamilyLayer, 0.05),
			}},
			wantM: [3]int{1, 4, 1},
		},
		{
			name: "request family pools tests and request-family layers",
			plan: &descx.MultiplicityPlan{
				Tests: []descx.ResolvedMultiplicity{member("tests[0]", bonf)},
				Overlays: []descx.ResolvedMultiplicity{
					overlayMember("overlays[0]", chi, bonf, types.MultiplicityFamilyRequest, 0.05),
					overlayMember("overlays[1]", fisher, bonf, types.MultiplicityFamilyLayer, 0.05),
					overlayMember("overlays[2]", chi, bonf, types.MultiplicityFamilyRequest, 0.05),
				},
			},
			wantM: [3]int{3, 4, 3}, wantTest: 3,
		},
		{
			name: "a non-member layer is untouched",
			plan: &descx.MultiplicityPlan{Overlays: []descx.ResolvedMultiplicity{
				{Slot: "overlays[0]", Operator: string(chi), Method: types.MultiplicityMethodNone, Family: types.MultiplicityFamilyLayer},
				overlayMember("overlays[1]", fisher, bonf, types.MultiplicityFamilyLayer, 0.05),
			}},
			wantM: [3]int{0, 4, 0},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp := build()
			if err := foldRequestMultiplicity(c.plan, resp); err != nil {
				t.Fatal(err)
			}
			for i, want := range c.wantM {
				l := resp.Overlays[i]
				if want == 0 {
					if l.Multiplicity != nil || (l.Summary != nil && l.Summary.PAdjusted != nil) || l.Payload.PAdjusted != nil {
						t.Errorf("layer %d corrected without membership", i)
					}
					continue
				}
				if l.Multiplicity == nil || l.Multiplicity.M != want {
					t.Errorf("layer %d echo %+v, want m=%d", i, l.Multiplicity, want)
				}
				if l.Summary != nil && l.Summary.PValue != nil {
					if w := math.Min(1, *l.Summary.PValue*float64(want)); !sameFloat(*l.Summary.PAdjusted, w) {
						t.Errorf("layer %d p_adjusted %v, want %v", i, *l.Summary.PAdjusted, w)
					}
				}
			}
			tr := resp.Tests[0]
			if c.wantTest == 0 {
				if tr.PAdjusted != nil {
					t.Error("test corrected without membership")
				}
			} else if tr.Multiplicity == nil || tr.Multiplicity.M != c.wantTest {
				t.Errorf("test echo %+v, want m=%d", tr.Multiplicity, c.wantTest)
			}
		})
	}
}

// overlayCrosstabRequest is a g×h crosstab carrying the three request-
// host inferential p-site shapes (SCALAR summary, SERIES entries,
// MATRIX cells), plus optional tests.
func overlayCrosstabRequest(tests bool) *types.Request {
	r := &types.Request{
		Cohort: &types.Cohort{Filename: "m.pulse"},
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "h"}},
			Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "x", Label: "n"},
			Shape:   types.CrosstabShapeMatrix,
			Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
		},
		Overlays: []types.OverlaySpec{
			{Name: "m", Kind: types.OverlayKindChiSqMatrix, Scope: types.OverlayScopeMatrix},
			{Name: "r", Kind: types.OverlayKindChiSqRow, Scope: types.OverlayScopeRow},
			{Name: "f", Kind: types.OverlayKindFisherExactCell, Scope: types.OverlayScopeCell},
			{Name: "d", Kind: types.OverlayKindShareOfRow, Scope: types.OverlayScopeRow, Ref: types.OverlayRef{Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow}}},
		},
	}
	if tests {
		r.Tests = multTests(false)
	}
	return r
}

func overlayFoldService(t *testing.T) (*Service, *Service) {
	t.Helper()
	cfg := fs.NewMemMap()
	if err := afero.WriteFile(cfg.Fs(), "m.pulse", writeNullablePulse(t, multSchema(), multRows(80, 0), nil), 0o644); err != nil {
		t.Fatal(err)
	}
	fused := New(cfg)
	buffered := New(cfg)
	buffered.SetDisableCrosstabFusion(true)
	return fused, buffered
}

// TestMultiplicityFold_CrosstabOverlays runs the post-hook over a real
// crosstab: layer and request families against the core applied to the
// baseline's raw p-values, the base response byte-identical apart from
// the additive slots, and the fused and buffered arms answering
// identically.
func TestMultiplicityFold_CrosstabOverlays(t *testing.T) {
	fused, buffered := overlayFoldService(t)
	ctx := context.Background()
	if ok, reason := processing.CanFuseCrosstab(overlayCrosstabRequest(false), multSchema(), fused.Extensions()); !ok {
		t.Fatalf("fixture does not fuse (%s); the fused arm would not be exercised", reason)
	}
	holm := types.MultiplicityMethodHolm
	for _, c := range []struct {
		name   string
		tests  bool
		block  *types.Multiplicity
		family types.MultiplicityFamily
	}{
		{"layer", false, &types.Multiplicity{Method: holm}, types.MultiplicityFamilyLayer},
		{"request pools tests", true, &types.Multiplicity{Method: holm, Family: types.MultiplicityFamilyRequest, Alpha: 0.1}, types.MultiplicityFamilyRequest},
	} {
		t.Run(c.name, func(t *testing.T) {
			base, err := buffered.Process(ctx, overlayCrosstabRequest(c.tests))
			if err != nil {
				t.Fatal(err)
			}
			req := overlayCrosstabRequest(c.tests)
			req.Multiplicity = c.block
			got, err := buffered.Process(ctx, req)
			if err != nil {
				t.Fatal(err)
			}
			// Collect the baseline's raw p-values per layer, in site order.
			layerPs := make([][]float64, 3)
			layerPs[0] = []float64{*base.Overlays[0].Summary.PValue}
			for _, e := range base.Overlays[1].Payload.Series.Entries {
				layerPs[1] = append(layerPs[1], *e.Summary.PValue)
			}
			for _, row := range base.Overlays[2].Payload.Matrix.Cells {
				for _, cl := range row {
					layerPs[2] = append(layerPs[2], cl.Value.(float64))
				}
			}
			gotPs := make([][]float64, 3)
			gotPs[0] = []float64{*got.Overlays[0].Summary.PAdjusted}
			for _, e := range got.Overlays[1].Payload.Series.Entries {
				gotPs[1] = append(gotPs[1], *e.Summary.PAdjusted)
			}
			for _, row := range got.Overlays[2].Payload.PAdjusted.Cells {
				for _, cl := range row {
					gotPs[2] = append(gotPs[2], cl.Value.(float64))
				}
			}
			if got.Overlays[3].Multiplicity != nil || got.Overlays[3].Payload.PAdjusted != nil {
				t.Error("descriptive SHARE_OF_ROW layer was corrected")
			}
			var wantPs [][]float64
			var wantM []int
			if c.family == types.MultiplicityFamilyLayer {
				for _, ps := range layerPs {
					adj, _ := multiplicity.Adjust(multiplicity.MethodHolm, ps)
					wantPs = append(wantPs, adj)
					wantM = append(wantM, multiplicity.FamilySize(ps))
				}
			} else {
				// Tests first, then post-tests, then overlay layers in slot order.
				var pool []float64
				for _, r := range base.Tests {
					pool = append(pool, r.PValue)
				}
				nTests := len(pool)
				for _, ps := range layerPs {
					pool = append(pool, ps...)
				}
				adj, _ := multiplicity.Adjust(multiplicity.MethodHolm, pool)
				m := multiplicity.FamilySize(pool)
				off := nTests
				for _, ps := range layerPs {
					wantPs = append(wantPs, adj[off:off+len(ps)])
					wantM = append(wantM, m)
					off += len(ps)
				}
				for i, r := range got.Tests {
					if r.PAdjusted == nil || !sameFloat(*r.PAdjusted, adj[i]) || r.Multiplicity.M != m {
						t.Errorf("test %d not pooled with the overlays: %v / %+v, want %v m=%d", i, r.PAdjusted, r.Multiplicity, adj[i], m)
					}
				}
			}
			alpha := c.block.Alpha
			if alpha == 0 {
				alpha = types.DefaultMultiplicityAlpha
			}
			for i := range wantPs {
				if len(gotPs[i]) != len(wantPs[i]) {
					t.Fatalf("layer %d: %d adjusted p-values, want %d", i, len(gotPs[i]), len(wantPs[i]))
				}
				for k := range wantPs[i] {
					if !sameFloat(gotPs[i][k], wantPs[i][k]) {
						t.Errorf("layer %d site %d p_adjusted %v, want %v", i, k, gotPs[i][k], wantPs[i][k])
					}
				}
				want := &types.AppliedMultiplicity{Method: holm, Family: c.family, Alpha: alpha, M: wantM[i]}
				if !reflect.DeepEqual(got.Overlays[i].Multiplicity, want) {
					t.Errorf("layer %d echo %+v, want %+v", i, got.Overlays[i].Multiplicity, want)
				}
			}
			// The base matrix, crosstab and data are byte-identical.
			if a, b := mustMarshal(t, base.Crosstab), mustMarshal(t, got.Crosstab); !bytes.Equal(a, b) {
				t.Error("host crosstab changed under the correction")
			}
			for i := range base.Overlays {
				if a, b := mustMarshal(t, base.Overlays[i].Payload.Matrix), mustMarshal(t, got.Overlays[i].Payload.Matrix); !bytes.Equal(a, b) {
					t.Errorf("layer %d base matrix changed", i)
				}
			}
			// Fused and buffered arms answer identically.
			fr := overlayCrosstabRequest(c.tests)
			fr.Multiplicity = c.block
			fgot, err := fused.Process(ctx, fr)
			if err != nil {
				t.Fatal(err)
			}
			if a, b := mustMarshal(t, fgot.Overlays), mustMarshal(t, got.Overlays); !bytes.Equal(a, b) {
				t.Errorf("fused and buffered overlays differ:\n fused    %s\n buffered %s", a, b)
			}
		})
	}
}
