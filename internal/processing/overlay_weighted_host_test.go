package processing

import (
	stderrors "errors"
	"math"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/statdist"
	"github.com/frankbardon/pulse/internal/weighting"
	"github.com/frankbardon/pulse/types"
)

// Weighted-host mean overlays (weighting-inferential E3-S1). A host cell
// carrying the weighted floor keys is read through the weighted-host
// rule: N* = sum_weights (frequency) or n_eff (probability), never the
// triple's raw n, and the variance recomputed from m2 on w*. The
// references below are computed from the raw (x, w) samples in closed
// form — w* = w·N*/Σw, variance = Σw*(x − mean)²/(Σw* − 1) — never
// through weighting.Welford.

// wSample is one leg's raw rows.
type wSample struct{ xs, ws []float64 }

var (
	legA = wSample{xs: []float64{3, 7, 4, 9, 12, 5, 8}, ws: []float64{1, 3, 2, 1, 4, 2, 1}}
	legB = wSample{xs: []float64{6, 11, 9, 14, 10, 13}, ws: []float64{2, 1, 3, 2, 1, 2}}
)

// weightedWelfordCell is the CellComponents map a weighted AGG_WELFORD
// cell emits: the operator keys {mean, m2, variance = m2/(Σw − 1),
// stddev} plus the universal floor and its weighted keys (n_eff only
// under kind probability).
func weightedWelfordCell(s wSample, basis weighting.Basis) map[string]any {
	var b weighting.Welford
	for i, x := range s.xs {
		b.Add(x, s.ws[i])
	}
	variance := weightedVariance(b.M2, b.SumW)
	m := map[string]any{
		"mean": b.Mean, "m2": b.M2, "variance": variance, "stddev": math.Sqrt(variance),
		"n": int(b.N), "n_null": 0, "sum_weights": b.SumW, "n_weight_invalid": 0,
	}
	if basis == weighting.Probability {
		m["n_eff"] = b.NEff()
	}
	return m
}

// legRef is a leg's reference (mean, variance on w*, N*, Σw, Σw²).
type legRef struct{ mean, variance, nStar, sumW, sumWSq float64 }

func legClosedForm(s wSample, basis weighting.Basis) legRef {
	var sw, sww, swx float64
	for i, x := range s.xs {
		sw += s.ws[i]
		sww += s.ws[i] * s.ws[i]
		swx += s.ws[i] * x
	}
	mean := swx / sw
	nStar := sw
	if basis == weighting.Probability {
		nStar = sw * sw / sww
	}
	var ss float64
	for i, x := range s.xs {
		wStar := s.ws[i] * nStar / sw
		ss += wStar * (x - mean) * (x - mean)
	}
	return legRef{mean: mean, variance: ss / (nStar - 1), nStar: nStar, sumW: sw, sumWSq: sww}
}

// welchRef is the Welch t (or z) two-sided p on two closed-form legs.
func welchRef(a, b legRef, z bool) float64 {
	va, vb := a.variance/a.nStar, b.variance/b.nStar
	stat := (a.mean - b.mean) / math.Sqrt(va+vb)
	if z {
		return normalTwoSidedP(stat)
	}
	df := (va + vb) * (va + vb) / (va*va/(a.nStar-1) + vb*vb/(b.nStar-1))
	return statdist.StudentTTwoSidedP(stat, df)
}

// rawNP is the pre-E3-S1 answer: the triple's emitted (frequency-form)
// variance over its RAW row count.
func rawNP(a, b map[string]any) float64 {
	p, _ := welchTTest(a["mean"].(float64), a["variance"].(float64), float64(a["n"].(int)),
		b["mean"].(float64), b["variance"].(float64), float64(b["n"].(int)))
	return p
}

func relClose(t *testing.T, where string, got, want, tol float64) {
	t.Helper()
	if math.IsNaN(got) || math.IsNaN(want) || math.Abs(got-want) > tol*math.Max(math.Abs(want), 1e-300) {
		t.Errorf("%s: got %.17g, want %.17g (rel tol %g)", where, got, want, tol)
	}
}

// wantParams is the layer summary's Parameters over both legs: Σw, and
// under probability the Kish n_eff of their union.
func wantParams(basis weighting.Basis, legs ...legRef) map[string]float64 {
	var sw, sww float64
	for _, l := range legs {
		sw += l.sumW
		sww += l.sumWSq
	}
	out := map[string]float64{"sum_weights": sw}
	if basis == weighting.Probability {
		out["n_eff"] = sw * sw / sww
	}
	return out
}

func assertParams(t *testing.T, where string, got *types.OverlaySummary, want map[string]float64) {
	t.Helper()
	if got == nil || len(got.Parameters) != len(want) {
		t.Fatalf("%s: Parameters = %v, want %v", where, got, want)
	}
	for k, v := range want {
		relClose(t, where+" Parameters."+k, got.Parameters[k], v, 1e-12)
	}
}

func TestReadMeanLeg_WeightedHost(t *testing.T) {
	unweighted := map[string]any{"mean": 2.5, "variance": 1.25, "n": 9, "n_null": 1}
	leg, ok := readMeanLeg(unweighted)
	if !ok || leg.mean != 2.5 || leg.variance != 1.25 || leg.nStar != 9 || leg.basis.Weighted() {
		t.Fatalf("unweighted leg = %+v, want the triple verbatim", leg)
	}
	for _, basis := range []weighting.Basis{weighting.Frequency, weighting.Probability} {
		leg, ok := readMeanLeg(weightedWelfordCell(legA, basis))
		if !ok || leg.basis != basis {
			t.Fatalf("basis %v: leg %+v ok %v", basis, leg, ok)
		}
		want := legClosedForm(legA, basis)
		relClose(t, "mean", leg.mean, want.mean, 1e-12)
		relClose(t, "N*", leg.nStar, want.nStar, 1e-12)
		relClose(t, "variance", leg.variance, want.variance, 1e-12)
		if leg.nStar == float64(len(legA.xs)) {
			t.Fatalf("basis %v: N* is the raw row count", basis)
		}
	}
	// A triple without m2 (an embedder-built map) recovers it from the
	// frequency-form variance.
	cell := weightedWelfordCell(legA, weighting.Probability)
	delete(cell, "m2")
	leg, _ = readMeanLeg(cell)
	relClose(t, "variance without m2", leg.variance, legClosedForm(legA, weighting.Probability).variance, 1e-12)
}

// TestMeanOverlays_WeightedHost: every mean overlay reads a weighted
// leg's N* and w*-variance, under both kinds, and reports the legs' Σw
// (and n_eff under probability) on its summary. The raw-n answer the
// pre-E3-S1 code gave on such a host (a slot-only weight never refused
// it) differs.
func TestMeanOverlays_WeightedHost(t *testing.T) {
	for _, basis := range []weighting.Basis{weighting.Frequency, weighting.Probability} {
		a, b := weightedWelfordCell(legA, basis), weightedWelfordCell(legB, basis)
		ca, cb := legClosedForm(legA, basis), legClosedForm(legB, basis)
		params := wantParams(basis, ca, cb)
		name := map[weighting.Basis]string{weighting.Frequency: "frequency", weighting.Probability: "probability"}[basis]
		raw := rawNP(a, b)

		matrix := func(cell map[string]any) *types.Response {
			return &types.Response{
				Crosstab: &types.CrosstabResult{Matrix: &types.MatrixPayload{
					RowKeys: []types.AxisKey{{"r"}}, ColumnKeys: []types.AxisKey{{"c"}},
					Cells: [][]types.MatrixCell{{{Value: cell["mean"], Present: true}}},
				}},
				Components: &types.ResponseComponents{Crosstab: &types.CrosstabComponents{
					CellComponents: [][]map[string]any{{cell}},
				}},
			}
		}
		series := func(cell map[string]any) *types.Response {
			return &types.Response{Data: []map[string]any{{"region": "north", "v": cell}}}
		}
		for _, tc := range []struct {
			kind types.OverlayKind
			z    bool
			run  func() (types.OverlayLayer, error)
			stat func(types.OverlayLayer) float64
		}{
			{types.OverlayKindTCell, false, func() (types.OverlayLayer, error) {
				spec := composeSpecMatrixRef(types.OverlayKindTCell, nil)
				l, _, err := applyTCell(&spec, matrix(b), []*types.Response{matrix(a)}, 0, []int{1})
				return l, err
			}, func(l types.OverlayLayer) float64 { return cellAt(t, l, 0, 0) }},
			{types.OverlayKindZCell, true, func() (types.OverlayLayer, error) {
				spec := composeSpecMatrixRef(types.OverlayKindZCell, nil)
				l, _, err := applyZCell(&spec, matrix(b), []*types.Response{matrix(a)}, 0, []int{1})
				return l, err
			}, func(l types.OverlayLayer) float64 { return cellAt(t, l, 0, 0) }},
			{types.OverlayKindTVsRef, false, func() (types.OverlayLayer, error) {
				spec := composeSpecSeriesRef(types.OverlayKindTVsRef, nil)
				l, _, err := applyTVsRef(&spec, series(b), []*types.Response{series(a)}, 0, []int{1})
				return l, err
			}, func(l types.OverlayLayer) float64 { return entryStat(t, l, 0) }},
			{types.OverlayKindZVsRef, true, func() (types.OverlayLayer, error) {
				spec := composeSpecSeriesRef(types.OverlayKindZVsRef, nil)
				l, _, err := applyZVsRef(&spec, series(b), []*types.Response{series(a)}, 0, []int{1})
				return l, err
			}, func(l types.OverlayLayer) float64 { return entryStat(t, l, 0) }},
			{types.OverlayKindPairwiseWelchT, false, func() (types.OverlayLayer, error) {
				l, _, err := applyPairwiseWelchT(&types.OverlaySpec{Kind: types.OverlayKindPairwiseWelchT, Scope: types.OverlayScopeRow},
					pairwiseHostOf(a, b))
				return l, err
			}, func(l types.OverlayLayer) float64 { return cellAt(t, l, 0, 0) }},
		} {
			where := string(tc.kind) + " " + name
			layer, err := tc.run()
			if err != nil {
				t.Fatalf("%s: %v", where, err)
			}
			got := tc.stat(layer)
			relClose(t, where+" p", got, welchRef(ca, cb, tc.z), 1e-9)
			if !tc.z && math.Abs(got-raw) < 1e-6 {
				t.Errorf("%s: p %.12g equals the raw-n answer — N* never reached the test", where, got)
			}
			assertParams(t, where, layer.Summary, params)
		}
	}
}

// pairwiseHostOf is a 2×1 host whose two rows are the legs; margin
// counts are raw rows.
func pairwiseHostOf(a, b map[string]any) *CrosstabHostView {
	mx := &types.MatrixPayload{
		RowHeader:    types.AxisHeader{Fields: []string{"brand"}, Types: []string{"GROUP_CATEGORY"}},
		ColumnHeader: types.AxisHeader{Fields: []string{"aud"}, Types: []string{"GROUP_CATEGORY"}},
		RowKeys:      []types.AxisKey{{"A"}, {"B"}},
		ColumnKeys:   []types.AxisKey{{"x"}},
		Cells:        [][]types.MatrixCell{{{Value: a["mean"], Present: true}}, {{Value: b["mean"], Present: true}}},
	}
	return newCrosstabHostViewWithComponents(mx, &types.CrosstabComponents{
		CellComponents:     [][]map[string]any{{a}, {b}},
		RowMarginCounts:    []int{a["n"].(int), b["n"].(int)},
		ColumnMarginCounts: []int{a["n"].(int) + b["n"].(int)},
	})
}

// TestMeanOverlays_UnweightedHostReportsNoWeights: an unweighted host's
// summary carries no Parameters (its layer is byte-identical to the
// pre-E3-S1 one; the per-kind byte-identity suites pin the payload).
func TestMeanOverlays_UnweightedHostReportsNoWeights(t *testing.T) {
	a := map[string]any{"mean": 10.0, "variance": 4.0, "n": 50}
	b := map[string]any{"mean": 12.0, "variance": 9.0, "n": 60}
	layer, _, err := applyPairwiseWelchT(&types.OverlaySpec{Kind: types.OverlayKindPairwiseWelchT, Scope: types.OverlayScopeRow}, pairwiseHostOf(a, b))
	if err != nil {
		t.Fatal(err)
	}
	if layer.Summary.Parameters != nil {
		t.Fatalf("unweighted host Parameters = %v, want none", layer.Summary.Parameters)
	}
	p, _ := welchTTest(10, 4, 50, 12, 9, 60)
	if got := cellAt(t, layer, 0, 0); got != p {
		t.Fatalf("unweighted p = %.17g, want %.17g bit for bit", got, p)
	}
}

// TestPairwiseWelchT_WeightedHostNSource: the weighted-host n_source rule
// at runtime — a raw-row-count source is PROCESSING_CONFIG on a weighted
// host under both kinds, the weight sum only under probability; on an
// unweighted host every selector stays inert (E2-S1's predict-only
// decision).
func TestPairwiseWelchT_WeightedHostNSource(t *testing.T) {
	run := func(basis weighting.Basis, nSource string) (float64, error) {
		a, b := weightedWelfordCell(legA, basis), weightedWelfordCell(legB, basis)
		if !basis.Weighted() {
			a = map[string]any{"mean": 10.0, "variance": 4.0, "n": 50}
			b = map[string]any{"mean": 12.0, "variance": 9.0, "n": 60}
		}
		params := mustParams(t, types.PairwiseOverlayParams{NSource: nSource})
		layer, _, err := applyPairwiseWelchT(&types.OverlaySpec{Kind: types.OverlayKindPairwiseWelchT, Scope: types.OverlayScopeRow, Params: params},
			pairwiseHostOf(a, b))
		if err != nil {
			return 0, err
		}
		return cellAt(t, layer, 0, 0), nil
	}
	raw := []string{types.PairwiseNSourceCellNUnweighted, types.PairwiseNSourceRowMarginN, types.PairwiseNSourceColumnMarginN}
	for _, basis := range []weighting.Basis{weighting.Unweighted, weighting.Frequency, weighting.Probability} {
		base, err := run(basis, "")
		if err != nil {
			t.Fatalf("basis %v omitted: %v", basis, err)
		}
		refused := map[string]bool{}
		if basis.Weighted() {
			for _, s := range raw {
				refused[s] = true
			}
		}
		if basis == weighting.Probability {
			refused[types.PairwiseNSourceCellWeightSum] = true
		}
		for _, s := range append(raw, types.PairwiseNSourceCellWeightSum, types.PairwiseNSourceCellValueWeight) {
			p, err := run(basis, s)
			if refused[s] {
				var ce *errors.CodedError
				if !stderrors.As(err, &ce) || ce.Code != errors.PROCESSING_CONFIG || ce.Details["n_source"] != s {
					t.Fatalf("basis %v n_source %s: err %v, want PROCESSING_CONFIG", basis, s, err)
				}
				if ce.Message != "overlay OVERLAY_PAIRWISE_WELCH_T n_source "+s+": "+weighting.NSourceRefusal(s, basis) {
					t.Fatalf("message %q", ce.Message)
				}
				continue
			}
			if err != nil || p != base {
				t.Fatalf("basis %v n_source %s: p %v err %v, want inert %v", basis, s, p, err, base)
			}
		}
	}
}

// TestHostWeightBasis: the host's basis is read off the floor keys.
func TestHostWeightBasis(t *testing.T) {
	for _, basis := range []weighting.Basis{weighting.Frequency, weighting.Probability} {
		h := pairwiseHostOf(weightedWelfordCell(legA, basis), weightedWelfordCell(legB, basis))
		if got := h.WeightBasis(); got != basis {
			t.Fatalf("WeightBasis = %v, want %v", got, basis)
		}
	}
	if got := pairwiseWelfordHost().WeightBasis(); got != weighting.Unweighted {
		t.Fatalf("unweighted host basis = %v", got)
	}
}
