package processing

import (
	"math"
	"strconv"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/weighting"
	"github.com/frankbardon/pulse/types"
)

// makeMatrixFromRows is makeMatrixFromValues over any rectangular table
// (NaN = an absent cell).
func makeMatrixFromRows(values [][]float64) *types.Response {
	rowKeys := make([]types.AxisKey, len(values))
	var colKeys []types.AxisKey
	cells := make([][]types.MatrixCell, len(values))
	for i, row := range values {
		rowKeys[i] = types.AxisKey{"r" + strconv.Itoa(i)}
		cells[i] = make([]types.MatrixCell, len(row))
		for j, v := range row {
			if i == 0 {
				colKeys = append(colKeys, types.AxisKey{"c" + strconv.Itoa(j)})
			}
			if math.IsNaN(v) {
				continue
			}
			cells[i][j] = types.MatrixCell{Value: v, Present: true}
		}
	}
	return &types.Response{Crosstab: &types.CrosstabResult{Matrix: &types.MatrixPayload{
		RowHeader:    types.AxisHeader{Types: []string{"GROUP_CATEGORY"}},
		ColumnHeader: types.AxisHeader{Types: []string{"GROUP_CATEGORY"}},
		RowKeys:      rowKeys,
		ColumnKeys:   colKeys,
		Cells:        cells,
	}}}
}

func chiSqSampleSpec() *types.ComposeOverlaySpec {
	return &types.ComposeOverlaySpec{Name: "cv", Kind: types.OverlayKindChiSqVsRef, Scope: types.OverlayScopeMatrix,
		Reference: "control", Targets: []string{"variant"}, Params: map[string]any{"reference_kind": "sample"}}
}

// TestReferenceOracle_OverlayChiSqVsRefSample: params.reference_kind
// "sample" (#791) is the 2 x |C| homogeneity test, R's
// chisq.test(rbind(target[C], ref[C])) with C the cells whose target +
// reference count is positive — including the experiment.pulse region
// x segment case (variant against control: 14.57 where goodness of fit
// gives 37.29).
func TestReferenceOracle_OverlayChiSqVsRefSample(t *testing.T) {
	t.Parallel()

	doc := loadReference[chiSqVsRefOracle](t, "overlay_chisq_vs_ref_sample")
	c := overlayOracleCheck(t, "OVERLAY_CHISQ_VS_REF reference_kind sample")
	spec := chiSqSampleSpec()
	for _, k := range doc.Cases {
		layer, warnings, err := applyChiSqVsRef(spec, makeMatrixFromRows(k.Ref), []*types.Response{makeMatrixFromRows(k.Target)}, 0, []int{1})
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
		if codes["PULSE_OVERLAY_REF_ZERO"] != 0 {
			t.Errorf("%s: %d PULSE_OVERLAY_REF_ZERO, want none (a one-sided empty cell is an observation)", k.Name, codes["PULSE_OVERLAY_REF_ZERO"])
		}
		if low := k.ExpectedMin < 5; (codes["PULSE_OVERLAY_EXPECTED_LOW"] > 0) != low {
			t.Errorf("%s: PULSE_OVERLAY_EXPECTED_LOW %v, R expected_min %v", k.Name, codes["PULSE_OVERLAY_EXPECTED_LOW"] > 0, k.ExpectedMin)
		}
	}
	c.done(len(doc.Cases))
}

// TestChiSqVsRef_DefaultIsGoodnessOfFit: an absent reference_kind and an
// explicit "fixed" are the goodness of fit, bit-identical (the
// experiment.pulse case's 37.29), never the homogeneity figure.
func TestChiSqVsRef_DefaultIsGoodnessOfFit(t *testing.T) {
	t.Parallel()

	variant := [][]float64{{27, 16, 13}, {17, 19, 17}, {16, 16, 18}, {16, 19, 13}}
	control := [][]float64{{13, 18, 13}, {14, 11, 21}, {20, 24, 8}, {21, 15, 15}}
	run := func(params map[string]any) float64 {
		spec := chiSqSampleSpec()
		spec.Params = params
		layer, _, err := applyChiSqVsRef(spec, makeMatrixFromRows(control), []*types.Response{makeMatrixFromRows(variant)}, 0, []int{1})
		if err != nil {
			t.Fatal(err)
		}
		return *layer.Summary.Statistic
	}
	def, fixed, sample := run(nil), run(map[string]any{"reference_kind": "fixed"}), run(map[string]any{"reference_kind": "sample"})
	if math.Float64bits(def) != math.Float64bits(fixed) {
		t.Fatalf("fixed %v != default %v", fixed, def)
	}
	if math.Abs(def-37.29190862814051) > 1e-9 {
		t.Fatalf("goodness of fit = %v, want 37.2919 (R chisq.test(v, p = c, rescale.p = TRUE))", def)
	}
	if math.Abs(sample-14.569923104609343) > 1e-9 {
		t.Fatalf("homogeneity = %v, want 14.5699 (R chisq.test(rbind(v, c)))", sample)
	}
}

// TestChiSqVsRef_ReferenceKindRefused: a reference_kind other than
// "fixed" / "sample" is PULSE_OVERLAY_PARAM_MISSING naming the param.
func TestChiSqVsRef_ReferenceKindRefused(t *testing.T) {
	t.Parallel()

	for _, v := range []any{"samples", 1.0, true} {
		spec := chiSqSampleSpec()
		spec.Params = map[string]any{"reference_kind": v}
		m := makeMatrixFromRows([][]float64{{1, 2}, {3, 4}})
		_, _, err := applyChiSqVsRef(spec, m, []*types.Response{m}, 0, []int{1})
		if !errors.HasCode(err, errors.PULSE_OVERLAY_PARAM_MISSING) {
			t.Fatalf("reference_kind %v: err = %v, want PULSE_OVERLAY_PARAM_MISSING", v, err)
		}
	}
}

// TestChiSqHomogeneity_WeightedSlots: under probability each slot's Σw
// table is scaled to its own Kish n_eff (the statistic equals the
// unweighted kernel on the two scaled tables); frequency reads Σw as
// is; summary.parameters carries the union of both slots' floors.
func TestChiSqHomogeneity_WeightedSlots(t *testing.T) {
	t.Parallel()

	spec := chiSqSampleSpec()
	tc, rc := tallyCT(ctFixture, 2, 3), tallyCT(ctRefFixture, 2, 3)
	stat := func(basis weighting.Basis) *types.OverlaySummary {
		layer, _, err := applyChiSqVsRef(spec, composeCountResponse(ctRefFixture, basis), []*types.Response{composeCountResponse(ctFixture, basis)}, 0, []int{1})
		if err != nil {
			t.Fatal(err)
		}
		return layer.Summary
	}
	scaled := func(tab ctTables, c float64) [][]float64 {
		out := make([][]float64, tab.rows)
		for i := range out {
			out[i] = make([]float64, tab.cols)
			for j := range out[i] {
				out[i][j] = tab.cell[i][j].sw * c
			}
		}
		return out
	}
	kernel := func(target, ref [][]float64) float64 {
		layer, _, err := applyChiSqVsRef(spec, makeMatrixFromRows(ref), []*types.Response{makeMatrixFromRows(target)}, 0, []int{1})
		if err != nil {
			t.Fatal(err)
		}
		return *layer.Summary.Statistic
	}
	freq, prob := stat(weighting.Frequency), stat(weighting.Probability)
	relClose(t, "frequency χ²", *freq.Statistic, kernel(scaled(tc, 1), scaled(rc, 1)), 1e-12)
	cT, cR := tc.table.sw/tc.table.sww, rc.table.sw/rc.table.sww
	relClose(t, "probability χ²", *prob.Statistic, kernel(scaled(tc, cT), scaled(rc, cR)), 1e-12)
	if *prob.Statistic == *freq.Statistic {
		t.Fatal("probability slots read as frequency: the reference's own Kish scale was not applied")
	}
	sw, sww := tc.table.sw+rc.table.sw, tc.table.sww+rc.table.sww
	assertParams(t, "probability", prob, map[string]float64{"df": 5, "sum_weights": sw, "n_eff": sw * sw / sww})
	assertParams(t, "frequency", freq, map[string]float64{"df": 5, "sum_weights": sw})
}
