package processing

import (
	"math"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// Overlay R oracles (U36 E3-S1). Every expected figure below is read
// from a golden scripts/reference/gen_overlays.R wrote (`make
// reference`): R computed it from the overlay's inputs, so no test here
// derives its expectation from the helper the overlay calls
// (chiSquareSurvival, ksTwoSampleP, fisherExactTwoSided,
// statdist.StudentTTwoSidedP, ...). Relative 1e-10 throughout
// (oracleRelTol), the far-tail floor only at the representable edge.

// overlayOracleCheck is newOracleCheck at the shared overlay tolerance.
func overlayOracleCheck(t *testing.T, name string) *oracleCheck {
	return newOracleCheck(t, name, oracleRelTol, oracleProbAbsFloor)
}

// --------------------------------------------------- OVERLAY_CHISQ_VS_POP

type chiSqVsPopOracle struct {
	Name         string    `json:"name"`
	SubsetValues []string  `json:"subset_values"`
	SubsetCounts []float64 `json:"subset_counts"`
	PopValues    []string  `json:"pop_values"`
	PopCounts    []float64 `json:"pop_counts"`
	Compared     []string  `json:"compared"`
	Statistic    float64   `json:"statistic"`
	DF           float64   `json:"df"`
	PValue       float64   `json:"p_value"`
	ExpectedMin  float64   `json:"expected_min"`
}

func facetCounts(values []string, counts []float64) []types.FacetValueCount {
	out := make([]types.FacetValueCount, len(values))
	for i, v := range values {
		out[i] = types.FacetValueCount{Value: v, Count: int64(counts[i])}
	}
	return out
}

func facetTotal(counts []float64) int64 {
	var n int64
	for _, c := range counts {
		n += int64(c)
	}
	return n
}

// chiSqVsPopOracleLayer runs OVERLAY_CHISQ_VS_POP on one golden case.
func chiSqVsPopOracleLayer(t *testing.T, k chiSqVsPopOracle) (types.OverlayLayer, []types.OverlayWarning) {
	t.Helper()
	pop := newFacetResultDiscrete("category", facetCounts(k.PopValues, k.PopCounts),
		int64(len(k.PopValues)), facetTotal(k.PopCounts), facetTotal(k.PopCounts), 0)
	host := newFacetResultDiscrete("category", facetCounts(k.SubsetValues, k.SubsetCounts),
		int64(len(k.SubsetValues)), facetTotal(k.SubsetCounts), facetTotal(k.SubsetCounts), 0)
	view, err := ResolveFacetPopulation(pop, "category")
	if err != nil {
		t.Fatalf("%s: ResolveFacetPopulation: %v", k.Name, err)
	}
	layer, warnings, err := applyChiSqVsPop(chiSqVsPopSpec(), host.Fields["category"], view)
	if err != nil {
		t.Fatalf("%s: applyChiSqVsPop: %v", k.Name, err)
	}
	return layer, warnings
}

// chiSqVsPopOracleCase returns the named overlay_chisq_vs_pop.json case.
func chiSqVsPopOracleCase(t *testing.T, name string) chiSqVsPopOracle {
	t.Helper()
	for _, k := range loadReference[chiSqVsPopOracle](t, "overlay_chisq_vs_pop").Cases {
		if k.Name == name {
			return k
		}
	}
	t.Fatalf("overlay_chisq_vs_pop.json has no case %q", name)
	return chiSqVsPopOracle{}
}

// TestReferenceOracle_OverlayChiSqVsPop: statistic, df and p against R's
// chisq.test(x[C], p = pop[C], rescale.p = TRUE); the scalar payload is
// the statistic; a subset value outside C warns PULSE_OVERLAY_REF_ZERO
// and an expected count below 5 PULSE_OVERLAY_EXPECTED_LOW.
func TestReferenceOracle_OverlayChiSqVsPop(t *testing.T) {
	doc := loadReference[chiSqVsPopOracle](t, "overlay_chisq_vs_pop")
	c := overlayOracleCheck(t, "OVERLAY_CHISQ_VS_POP")
	for _, k := range doc.Cases {
		layer, warnings := chiSqVsPopOracleLayer(t, k)
		s := layer.Summary
		if s == nil || s.Statistic == nil || s.PValue == nil || layer.Payload.Scalar == nil {
			t.Fatalf("%s: layer %+v lacks the statistic / p", k.Name, layer)
		}
		c.check(*s.Statistic, k.Statistic, "%s statistic", k.Name)
		c.check(*layer.Payload.Scalar, k.Statistic, "%s scalar", k.Name)
		c.check(*s.PValue, k.PValue, "%s p", k.Name)
		if s.Parameters["df"] != k.DF {
			t.Errorf("%s: df = %v, R %v", k.Name, s.Parameters["df"], k.DF)
		}
		codes := map[string]int{}
		for _, w := range warnings {
			codes[w.Code]++
		}
		if want := len(k.SubsetValues) - len(k.Compared); codes["PULSE_OVERLAY_REF_ZERO"] != want {
			t.Errorf("%s: %d PULSE_OVERLAY_REF_ZERO warnings, want %d (subset values outside C)", k.Name, codes["PULSE_OVERLAY_REF_ZERO"], want)
		}
		if low := k.ExpectedMin < 5; (codes["PULSE_OVERLAY_EXPECTED_LOW"] > 0) != low {
			t.Errorf("%s: PULSE_OVERLAY_EXPECTED_LOW %v, R expected_min %v", k.Name, codes["PULSE_OVERLAY_EXPECTED_LOW"] > 0, k.ExpectedMin)
		}
	}
	c.done(len(doc.Cases))
}

// --------------------------------------------------- OVERLAY_CHISQ_VS_REF

type chiSqVsRefOracle struct {
	Name          string      `json:"name"`
	Target        [][]float64 `json:"target"`
	Ref           [][]float64 `json:"ref"`
	Statistic     float64     `json:"statistic"`
	DF            float64     `json:"df"`
	PValue        float64     `json:"p_value"`
	ExpectedMin   float64     `json:"expected_min"`
	ExcludedCells float64     `json:"excluded_cells"`
}

func matrix3(m [][]float64) [3][3]float64 {
	var out [3][3]float64
	for i := range out {
		copy(out[i][:], m[i])
	}
	return out
}

// TestReferenceOracle_OverlayChiSqVsRef: the whole-matrix goodness of
// fit against R's chisq.test(target[C], p = ref[C], rescale.p = TRUE),
// C the cells with a positive reference count. A zero reference cell
// is left out of the statistic, the df AND the target total (the
// target renormalised over C, OVERLAY_CHISQ_VS_POP's rule), and warns
// PULSE_OVERLAY_REF_ZERO when it holds target counts.
func TestReferenceOracle_OverlayChiSqVsRef(t *testing.T) {
	doc := loadReference[chiSqVsRefOracle](t, "overlay_chisq_vs_ref")
	c := overlayOracleCheck(t, "OVERLAY_CHISQ_VS_REF")
	spec := types.ComposeOverlaySpec{Name: "cv", Kind: types.OverlayKindChiSqVsRef, Scope: types.OverlayScopeMatrix,
		Reference: "baseline", Targets: []string{"treatment"}}
	for _, k := range doc.Cases {
		layer, warnings, err := applyChiSqVsRef(&spec, makeMatrixFromValues(matrix3(k.Ref)),
			[]*types.Response{makeMatrixFromValues(matrix3(k.Target))}, 0, []int{1})
		if err != nil {
			t.Fatalf("%s: %v", k.Name, err)
		}
		s := layer.Summary
		if s == nil || s.Statistic == nil || s.PValue == nil || layer.Payload.Scalar == nil {
			t.Fatalf("%s: layer %+v lacks the statistic / p", k.Name, layer)
		}
		c.check(*s.Statistic, k.Statistic, "%s statistic", k.Name)
		c.check(*s.PValue, k.PValue, "%s p", k.Name)
		c.check(*layer.Payload.Scalar, k.PValue, "%s scalar (the p-value)", k.Name)
		if s.Parameters["df"] != k.DF {
			t.Errorf("%s: df = %v, R %v", k.Name, s.Parameters["df"], k.DF)
		}
		codes := map[string]int{}
		for _, w := range warnings {
			codes[w.Code]++
		}
		stranded := 0
		for i := range k.Ref {
			for j := range k.Ref[i] {
				if k.Ref[i][j] == 0 && k.Target[i][j] > 0 {
					stranded++
				}
			}
		}
		if codes["PULSE_OVERLAY_REF_ZERO"] != stranded {
			t.Errorf("%s: %d PULSE_OVERLAY_REF_ZERO warnings, want %d (target counts in zero-reference cells)", k.Name, codes["PULSE_OVERLAY_REF_ZERO"], stranded)
		}
		if low := k.ExpectedMin < 5; (codes["PULSE_OVERLAY_EXPECTED_LOW"] > 0) != low {
			t.Errorf("%s: PULSE_OVERLAY_EXPECTED_LOW %v, R expected_min %v", k.Name, codes["PULSE_OVERLAY_EXPECTED_LOW"] > 0, k.ExpectedMin)
		}
	}
	c.done(len(doc.Cases))
}

// ------------------------------------------------------ OVERLAY_KS_VS_POP

type ksVsPopOracle struct {
	Name               string             `json:"name"`
	Path               string             `json:"path"`
	Min                float64            `json:"min"`
	Max                float64            `json:"max"`
	HostBins           []int64            `json:"host_bins"`
	PopBins            []int64            `json:"pop_bins"`
	HostPercentiles    map[string]float64 `json:"host_percentiles"`
	PopPercentiles     map[string]float64 `json:"pop_percentiles"`
	NSubset            int64              `json:"n_subset"`
	NPop               int64              `json:"n_pop"`
	Statistic          float64            `json:"statistic"`
	PValue             float64            `json:"p_value"`
	Exact              bool               `json:"exact"`
	RKSTestP           *float64           `json:"r_ks_test_p"`
	RKSTestMethod      string             `json:"r_ks_test_method"`
	RKSTestPAsymptotic *float64           `json:"r_ks_test_p_asymptotic"`
	RStephensP         *float64           `json:"r_stephens_p"`
}

// ksVsPopOracleLayer runs OVERLAY_KS_VS_POP on one golden case.
func ksVsPopOracleLayer(t *testing.T, k ksVsPopOracle) types.OverlayLayer {
	t.Helper()
	arm := func(n int64, bins []int64, pct map[string]float64) *types.FacetNumeric {
		num := &types.FacetNumeric{Count: n, Min: k.Min, Max: k.Max, Percentiles: pct}
		if k.Path == "histogram" {
			num.Histogram = &types.FacetHistogram{Min: k.Min, Max: k.Max, Bins: bins}
		}
		return num
	}
	host := newFacetResultNumeric("price", arm(k.NSubset, k.HostBins, k.HostPercentiles), k.NSubset, k.NSubset, 0)
	pop := newFacetResultNumeric("price", arm(k.NPop, k.PopBins, k.PopPercentiles), k.NPop, k.NPop, 0)
	view, err := ResolveFacetPopulation(pop, "price")
	if err != nil {
		t.Fatalf("%s: ResolveFacetPopulation: %v", k.Name, err)
	}
	layer, warnings, err := applyKSVsPop(ksVsPopSpec(), host.Fields["price"], view)
	if err != nil || len(warnings) != 0 {
		t.Fatalf("%s: applyKSVsPop err %v, warnings %+v", k.Name, err, warnings)
	}
	return layer
}

// ksVsPopOracleCase returns the named overlay_ks_vs_pop.json case.
func ksVsPopOracleCase(t *testing.T, name string) ksVsPopOracle {
	t.Helper()
	for _, k := range loadReference[ksVsPopOracle](t, "overlay_ks_vs_pop").Cases {
		if k.Name == name {
			return k
		}
	}
	t.Fatalf("overlay_ks_vs_pop.json has no case %q", name)
	return ksVsPopOracle{}
}

// TestReferenceOracle_OverlayKSVsPop: D against R — the bucket-edge gap
// (= ks.test's statistic on the bucketised samples) on the histogram
// path, the approx(rule = 2) gap on the percentile path — and the p
// against ks.test (U36 E3-S4): ks.test(x, y)$p.value on the bucketised
// samples (exact, conditional on the bucket ties, when m·n < 10000 —
// the branch must match the one ks.test ran), and ks.test's
// asymptotic psmirnov(D, c(m, n), exact = FALSE) on the percentile
// path. disjoint_halves pins psmirnov at D = 1, the generator's written
// reason (ks.test's own D rounds an ulp past 1 and reports 0). The
// golden's r_stephens_p is the pre-U36 figure, history only.
func TestReferenceOracle_OverlayKSVsPop(t *testing.T) {
	doc := loadReference[ksVsPopOracle](t, "overlay_ks_vs_pop")
	c := overlayOracleCheck(t, "OVERLAY_KS_VS_POP")
	paths := map[string]int{}
	exact := map[bool]int{}
	for _, k := range doc.Cases {
		paths[k.Path]++
		if k.Path == "histogram" {
			exact[k.Exact]++
			var m, n float64
			for i := range k.HostBins {
				m += float64(k.HostBins[i])
				n += float64(k.PopBins[i])
			}
			if got := ksUsesExact(m, n); got != k.Exact {
				t.Errorf("%s: exact branch = %v, ks.test ran exact = %v", k.Name, got, k.Exact)
			}
		}
		layer := ksVsPopOracleLayer(t, k)
		s := layer.Summary
		if s == nil || s.Statistic == nil || s.PValue == nil || layer.Payload.Scalar == nil {
			t.Fatalf("%s: layer %+v lacks D / p", k.Name, layer)
		}
		c.check(*s.Statistic, k.Statistic, "%s D", k.Name)
		c.check(*layer.Payload.Scalar, k.Statistic, "%s scalar", k.Name)
		c.check(*s.PValue, k.PValue, "%s p (ks.test)", k.Name)
		if s.Parameters["n_subset"] != float64(k.NSubset) || s.Parameters["n_pop"] != float64(k.NPop) {
			t.Errorf("%s: parameters %v, want n_subset %d n_pop %d", k.Name, s.Parameters, k.NSubset, k.NPop)
		}
	}
	if paths["histogram"] == 0 || paths["percentile"] == 0 {
		t.Fatalf("golden covers paths %v, want both", paths)
	}
	if exact[true] == 0 || exact[false] == 0 {
		t.Fatalf("golden's histogram cases cover exact branches %v, want both", exact)
	}
	c.done(len(doc.Cases))
}

// TestOverlayKSVsPop_HistogramPIsTestKS: the histogram-path overlay and
// TEST_KS share one port (ksTwoSampleP) — run on the bucketised samples
// expanded into rows (bucket index i repeated bins[i] times per arm),
// TEST_KS reports the overlay's D and p, bit for bit, on both the exact
// and the limiting branch.
func TestOverlayKSVsPop_HistogramPIsTestKS(t *testing.T) {
	for _, name := range []string{"shifted_small", "moderate", "shifted_large", "disjoint_halves"} {
		k := ksVsPopOracleCase(t, name)
		layer := ksVsPopOracleLayer(t, k)
		schema := twoSampleFixtureSchema()
		rt, err := newKSRow(&types.Test{Type: types.TEST_KS, Field: "revenue", SplitBy: "treatment"}, schema)
		if err != nil {
			t.Fatalf("%s: newKSRow: %v", name, err)
		}
		for _, side := range []struct {
			label string
			bins  []int64
		}{{"a", k.HostBins}, {"b", k.PopBins}} {
			for i, c := range side.bins {
				for r := int64(0); r < c; r++ {
					if err := rt.UpdateRow(newTestRecord(t, schema, map[string]float64{"revenue": float64(i)}, "treatment", side.label)); err != nil {
						t.Fatalf("%s: UpdateRow: %v", name, err)
					}
				}
			}
		}
		res, err := rt.Finalize()
		if err != nil {
			t.Fatalf("%s: Finalize: %v", name, err)
		}
		if *layer.Summary.Statistic != res.Statistic {
			t.Errorf("%s: overlay D %v, TEST_KS D %v", name, *layer.Summary.Statistic, res.Statistic)
		}
		if *layer.Summary.PValue != res.PValue {
			t.Errorf("%s: overlay p %v, TEST_KS p %v", name, *layer.Summary.PValue, res.PValue)
		}
	}
}

// ------------------------------------------ OVERLAY_PAIRWISE_PROBIT_T

type probitOracle struct {
	Name  string    `json:"name"`
	Pct   []float64 `json:"pct"`
	N     []float64 `json:"n"`
	Pairs []struct {
		T      float64 `json:"t"`
		DF     float64 `json:"df"`
		PValue float64 `json:"p_value"`
	} `json:"pairs"`
}

// TestReferenceOracle_OverlayPairwiseProbitT: the probit t's documented
// design formula — t = (qnorm(p1) - qnorm(p2)) / sqrt(1/n1 + 1/n2) on
// shares clipped to [1e-10, 1 - 1e-10], Student t on n1 + n2 - 2 — as R
// computes it, through the pairwise overlay on a three-row percentage
// host (pairs (A,B), (A,C), (B,C)); the cell's direction is sign(t).
func TestReferenceOracle_OverlayPairwiseProbitT(t *testing.T) {
	doc := loadReference[probitOracle](t, "overlay_pairwise_probit_t")
	c := overlayOracleCheck(t, "OVERLAY_PAIRWISE_PROBIT_T")
	for _, k := range doc.Cases {
		mx := &types.MatrixPayload{
			RowHeader:    types.AxisHeader{Fields: []string{"brand"}, Types: []string{"GROUP_CATEGORY"}},
			ColumnHeader: types.AxisHeader{Fields: []string{"aud"}, Types: []string{"GROUP_CATEGORY"}},
			RowKeys:      []types.AxisKey{{"A"}, {"B"}, {"C"}},
			ColumnKeys:   []types.AxisKey{{"x"}},
		}
		comps := &types.CrosstabComponents{}
		for i := range k.Pct {
			mx.Cells = append(mx.Cells, []types.MatrixCell{{Value: k.Pct[i], Present: true}})
			comps.CellComponents = append(comps.CellComponents, []map[string]any{{"n": int(k.N[i])}})
		}
		layers, _, err := applyOverlays([]types.OverlaySpec{{Kind: types.OverlayKindPairwiseProbitT, Scope: types.OverlayScopeRow}},
			newCrosstabHostViewWithComponents(mx, comps))
		if err != nil {
			t.Fatalf("%s: %v", k.Name, err)
		}
		out := layers[0].Payload.Matrix
		if out == nil || len(out.Cells) != len(k.Pairs) {
			t.Fatalf("%s: payload %+v, want %d pair rows", k.Name, out, len(k.Pairs))
		}
		for i, want := range k.Pairs {
			cell := out.Cells[i][0]
			got, ok := cell.Value.(float64)
			if !cell.Present || !ok {
				t.Fatalf("%s pair %v: cell %+v, want a present p-value", k.Name, out.RowKeys[i], cell)
			}
			c.check(got, want.PValue, "%s pair %v", k.Name, out.RowKeys[i])
		}
	}
	c.done(len(doc.Cases))
}

// ------------------------------------------ OVERLAY_FISHER_EXACT_CELL

type fisherCellOracle struct {
	Name  string      `json:"name"`
	Table [][]float64 `json:"table"`
	P     [][]float64 `json:"p"`
}

// fisherCellOracleCase returns the named overlay_fisher_exact_cell.json
// case.
func fisherCellOracleCase(t *testing.T, name string) fisherCellOracle {
	t.Helper()
	for _, k := range loadReference[fisherCellOracle](t, "overlay_fisher_exact_cell").Cases {
		if k.Name == name {
			return k
		}
	}
	t.Fatalf("overlay_fisher_exact_cell.json has no case %q", name)
	return fisherCellOracle{}
}

// TestReferenceOracle_OverlayFisherExactCell: every cell's two-sided p
// against R's fisher.test on that cell's 2 x 2 (the cell against the
// rest of its row and column), relErr tie rule included.
func TestReferenceOracle_OverlayFisherExactCell(t *testing.T) {
	doc := loadReference[fisherCellOracle](t, "overlay_fisher_exact_cell")
	c := overlayOracleCheck(t, "OVERLAY_FISHER_EXACT_CELL")
	for _, k := range doc.Cases {
		layers, _, err := applyOverlays([]types.OverlaySpec{{Kind: types.OverlayKindFisherExactCell, Scope: types.OverlayScopeCell}},
			countHost(k.Table))
		if err != nil {
			t.Fatalf("%s: %v", k.Name, err)
		}
		mx := layers[0].Payload.Matrix
		for i := range k.P {
			for j, want := range k.P[i] {
				cell := mx.Cells[i][j]
				got, ok := cell.Value.(float64)
				if !cell.Present || !ok {
					t.Fatalf("%s [%d][%d]: cell %+v, want a present p-value", k.Name, i, j, cell)
				}
				c.check(got, want, "%s [%d][%d]", k.Name, i, j)
			}
		}
	}
	c.done(len(doc.Cases))
}

// ------------------------------------------------ two-sample summary legs

type twoSampleOracle struct {
	Name   string    `json:"name"`
	Kind   string    `json:"kind"`
	MeanA  float64   `json:"mean_a"`
	VarA   float64   `json:"var_a"`
	NA     float64   `json:"n_a"`
	MeanB  float64   `json:"mean_b"`
	VarB   float64   `json:"var_b"`
	NB     float64   `json:"n_b"`
	WelchP float64   `json:"welch_p"`
	ZP     float64   `json:"z_p"`
	X      []float64 `json:"x"`
	N      []float64 `json:"n"`
	PropP  float64   `json:"prop_p"`
}

// twoSampleOracleCase returns the named overlay_two_sample.json case.
func twoSampleOracleCase(t *testing.T, name string) twoSampleOracle {
	t.Helper()
	for _, k := range loadReference[twoSampleOracle](t, "overlay_two_sample").Cases {
		if k.Name == name {
			return k
		}
	}
	t.Fatalf("overlay_two_sample.json has no case %q", name)
	return twoSampleOracle{}
}

// TestReferenceOracle_OverlayTwoSampleLegs: the shared two-sample
// kernels — the Welch t and two-means z of the mean overlays
// (welchTTest / welchZTest) and the pooled two-proportion z of the
// proportion overlays (twoProportionZ) — against R on every leg pair.
// Pinned here once, they are what the routing tests (which legs a host,
// an n_source or a panel reads) may run on their expected legs: those
// tests verify the routing, this one the kernel.
func TestReferenceOracle_OverlayTwoSampleLegs(t *testing.T) {
	doc := loadReference[twoSampleOracle](t, "overlay_two_sample")
	c := overlayOracleCheck(t, "overlay two-sample legs")
	n := 0
	for _, k := range doc.Cases {
		n++
		if k.Kind == "proportions" {
			p, ok := twoProportionZ(k.X[0], k.N[0], k.X[1], k.N[1])
			if !ok {
				t.Fatalf("%s: twoProportionZ undefined", k.Name)
			}
			c.check(p, k.PropP, "%s pooled two-proportion z", k.Name)
			continue
		}
		p, ok := welchTTest(k.MeanA, k.VarA, k.NA, k.MeanB, k.VarB, k.NB)
		if !ok {
			t.Fatalf("%s: welchTTest undefined", k.Name)
		}
		c.check(p, k.WelchP, "%s Welch t", k.Name)
		z, ok := welchZTest(k.MeanA, k.VarA, k.NA, k.MeanB, k.VarB, k.NB)
		if !ok {
			t.Fatalf("%s: welchZTest undefined", k.Name)
		}
		c.check(z, k.ZP, "%s two-means z", k.Name)
	}
	c.done(n)
}

// ---------------------------------------------- weighted unit fixtures

type weightedUnitOracle struct {
	Name      string  `json:"name"`
	Kind      string  `json:"kind"`
	Statistic float64 `json:"statistic"`
	DF        float64 `json:"df"`
	PValue    float64 `json:"p_value"`
}

// weightedUnitOracleCase returns the overlay_weighted_unit.json case
// (name, kind).
func weightedUnitOracleCase(t *testing.T, name, kind string) weightedUnitOracle {
	t.Helper()
	for _, k := range loadReference[weightedUnitOracle](t, "overlay_weighted_unit").Cases {
		if k.Name == name && k.Kind == kind {
			return k
		}
	}
	t.Fatalf("overlay_weighted_unit.json has no case %s/%s", name, kind)
	return weightedUnitOracle{}
}

// oracleRelClose is relClose at oracleRelTol, failing on NaN.
func oracleRelClose(t *testing.T, where string, got, want float64) {
	t.Helper()
	if math.IsNaN(got) || math.Abs(got-want) > oracleRelTol*math.Max(math.Abs(want), 1e-300) {
		t.Errorf("%s: got %.17g, R %.17g (rel tol %g)", where, got, want, oracleRelTol)
	}
}
