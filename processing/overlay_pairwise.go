package processing

import (
	"fmt"
	"math"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// This file implements the OVERLAY_PAIRWISE_* family: intra-matrix
// axis-pairwise significance overlays that test one host-matrix slot
// against another ALONG a single axis of the same materialised crosstab
// (row-vs-row for each column under ROW scope; column-vs-column for each
// row under COLUMN scope). It is the per-Request counterpart to the
// Compose-host OVERLAY_PROP_Z_PANEL — where the panel pairs across slots,
// this family pairs across the host's own axis indices.
//
// Output: a MATRIX payload whose PAIR axis carries one entry per evaluated
// (i, j) index pair (key = the 2-tuple of the compared legs' display
// labels) and whose OPPOSITE axis echoes the host's other axis. Each cell
// holds the pair's two-sided p-value at that opposite-axis position, or is
// absent when a leg was unreadable or the test degenerate. The family
// emits RAW p-values only; direction, thresholds, and min-n flags are the
// embedder's presentation concern.
//
// All four kinds reuse the shared harness below; only the per-pair Compute
// kernel and the input shape (proportion + n vs Welford triple) differ.

// applyPairwisePropZ / ...ProbitT / ...WelchT / ...TwoMeansZ are the
// dispatch entry points registered in overlayHandlers. Each pins the kind
// and delegates to the shared runner.
func applyPairwisePropZ(spec *types.OverlaySpec, host *CrosstabHostView) (types.OverlayLayer, []types.OverlayWarning, error) {
	return runPairwiseOverlay(spec, host, pairwisePropZKernel, false)
}

func applyPairwiseProbitT(spec *types.OverlaySpec, host *CrosstabHostView) (types.OverlayLayer, []types.OverlayWarning, error) {
	return runPairwiseOverlay(spec, host, pairwiseProbitTKernel, false)
}

func applyPairwiseWelchT(spec *types.OverlaySpec, host *CrosstabHostView) (types.OverlayLayer, []types.OverlayWarning, error) {
	return runPairwiseOverlay(spec, host, pairwiseWelchTKernel, true)
}

func applyPairwiseTwoMeansZ(spec *types.OverlaySpec, host *CrosstabHostView) (types.OverlayLayer, []types.OverlayWarning, error) {
	return runPairwiseOverlay(spec, host, pairwiseTwoMeansZKernel, true)
}

// pwInputs carries one pair's two legs. Proportion kinds populate p1/p2 +
// n1/n2 (m/v are NaN); Welford kinds populate m/v/n (p is NaN).
type pwInputs struct {
	p1, p2 float64
	m1, m2 float64
	v1, v2 float64
	n1, n2 int
}

// pwKernel computes one pair's two-sided p-value. ok=false with a reason
// string signals a degenerate / unreadable pair that stays absent on the
// payload and folds into one aggregated warning per reason.
type pwKernel func(in pwInputs) (pValue float64, reason string, ok bool)

// pairwisePair is one evaluated (i, j) index pair along the pair axis,
// carrying each leg's display label for the output key.
type pairwisePair struct {
	i, j           int
	labelI, labelJ string
}

func runPairwiseOverlay(spec *types.OverlaySpec, host *CrosstabHostView, kernel pwKernel, welford bool) (types.OverlayLayer, []types.OverlayWarning, error) {
	params, err := types.DecodePairwiseParams(spec.Params)
	if err != nil {
		return types.OverlayLayer{}, nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_INTERNAL,
			"overlay "+string(spec.Kind)+" has malformed Params: "+err.Error(),
			map[string]any{"code": string(errors.PULSE_OVERLAY_PARAM_MISSING), "kind": string(spec.Kind)})
	}

	// Components gate — every kind reads per-cell counters / Welford
	// triples / margin counts. Without them there is no sample-size leg.
	if !host.HasComponents() {
		return types.OverlayLayer{}, nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_INTERNAL,
			"overlay "+string(spec.Kind)+" requires Response.Components.Crosstab; the host was built with components disabled",
			map[string]any{"code": string(errors.PULSE_OVERLAY_COMPONENTS_REQUIRED), "kind": string(spec.Kind)})
	}
	// Welford-input kinds need {mean, variance, n} on at least one cell.
	if welford && !host.HasWelfordCells() {
		return types.OverlayLayer{}, nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_INTERNAL,
			"overlay "+string(spec.Kind)+" requires AGG_WELFORD cells (Welford triple on CellComponents); host matrix has none",
			map[string]any{"code": string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE), "kind": string(spec.Kind)})
	}

	rowScope := spec.Scope == types.OverlayScopeRow

	pairs, perr := buildPairwisePairs(spec, host, params, rowScope)
	if perr != nil {
		return types.OverlayLayer{}, nil, perr
	}

	// Opposite-axis extent: ROW scope pairs rows, so the opposite axis is
	// columns; COLUMN scope pairs columns, opposite axis is rows.
	oppCount := host.ColumnCount()
	if !rowScope {
		oppCount = host.RowCount()
	}

	tally := newPairwiseTally()

	// pvals[pairPos][opp] — laid out pair-major; transposed into the
	// output MatrixPayload below per scope.
	pvals := make([][]types.MatrixCell, len(pairs))
	for p := range pairs {
		pvals[p] = make([]types.MatrixCell, oppCount)
		for o := 0; o < oppCount; o++ {
			r1, c1 := pairwiseLegCoord(rowScope, pairs[p].i, o)
			r2, c2 := pairwiseLegCoord(rowScope, pairs[p].j, o)
			in, ok := extractPairInputs(host, params, welford, rowScope, r1, c1, r2, c2)
			if !ok {
				tally.add("ORBIT_STATS_SKIP_EXTRACT_FAILED", o)
				continue
			}
			pv, reason, ok := kernel(in)
			if !ok {
				tally.add(reason, o)
				continue
			}
			pvals[p][o] = types.MatrixCell{Value: pv, Present: true}
		}
	}

	layer := buildPairwiseLayer(spec, host, pairs, pvals, rowScope)
	// Attach the per-reason skip diagnostics to the layer's own carrier
	// (in addition to the returned slice the orchestrator promotes to
	// Response.Warnings) so a per-Request consumer that reads layers
	// directly — e.g. Orbit's stat-test marker pass — can associate each
	// skip with its originating spec.
	warns := tally.warnings(spec)
	layer.Warnings = warns
	return layer, warns, nil
}

// buildPairwisePairs enumerates (i, j) index pairs along the pair axis.
// PairAlongDim=nil yields the full upper triangular; a non-nil value
// buckets indexes by their axis-key tuple minus that dim and pairs only
// within each bucket. Out-of-range pair_along_dim / n_within_depth against
// the actual axis depth fail with PULSE_OVERLAY_PARAM_MISSING.
func buildPairwisePairs(spec *types.OverlaySpec, host *CrosstabHostView, params types.PairwiseOverlayParams, rowScope bool) ([]pairwisePair, error) {
	count := host.ColumnCount()
	depth := host.ColumnAxisDepth()
	keyAt := host.columnKey
	labelAt := func(i int) string { return stringifyOverlayAxisKey(host.columnKey(i)) }
	if rowScope {
		count = host.RowCount()
		depth = host.RowAxisDepth()
		keyAt = host.rowKey
		labelAt = func(i int) string { return stringifyOverlayAxisKey(host.rowKey(i)) }
	}

	if params.NSource == types.PairwiseNSourceNWithin && params.NWithinDepth >= depth {
		return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
			"overlay "+string(spec.Kind)+" n_within_depth exceeds pair-axis dim count",
			map[string]any{"code": string(errors.PULSE_OVERLAY_PARAM_MISSING), "kind": string(spec.Kind),
				"n_within_depth": params.NWithinDepth, "dim_count": depth})
	}

	indexes := make([]int, count)
	for i := range indexes {
		indexes[i] = i
	}

	if params.PairAlongDim == nil {
		out := make([]pairwisePair, 0, count*(count-1)/2)
		for a := 0; a < len(indexes); a++ {
			for b := a + 1; b < len(indexes); b++ {
				i, j := indexes[a], indexes[b]
				out = append(out, pairwisePair{i: i, j: j, labelI: labelAt(i), labelJ: labelAt(j)})
			}
		}
		return out, nil
	}

	dim := *params.PairAlongDim
	if dim >= depth {
		return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
			"overlay "+string(spec.Kind)+" pair_along_dim out of range for pair-axis dim count",
			map[string]any{"code": string(errors.PULSE_OVERLAY_PARAM_MISSING), "kind": string(spec.Kind),
				"pair_along_dim": dim, "dim_count": depth})
	}

	// Bucket by tuple-minus-dim, preserving insertion order so pairs come
	// out in axis-natural order (mirrors the nil-PairAlongDim double loop).
	type bucket struct {
		key     types.AxisKey
		indexes []int
	}
	buckets := make([]*bucket, 0, count)
	for _, i := range indexes {
		ki := keyAt(i)
		if ki == nil || dim >= len(ki) {
			continue
		}
		matched := false
		for _, b := range buckets {
			if axisKeyEqualExceptDim(b.key, ki, dim) {
				b.indexes = append(b.indexes, i)
				matched = true
				break
			}
		}
		if !matched {
			buckets = append(buckets, &bucket{key: ki, indexes: []int{i}})
		}
	}

	out := make([]pairwisePair, 0)
	for _, b := range buckets {
		for a := 0; a < len(b.indexes); a++ {
			for c := a + 1; c < len(b.indexes); c++ {
				i, j := b.indexes[a], b.indexes[c]
				out = append(out, pairwisePair{i: i, j: j, labelI: labelAt(i), labelJ: labelAt(j)})
			}
		}
	}
	return out, nil
}

// pairwiseLegCoord maps a (pairIndex, oppositeIndex) into (row, col). For
// ROW scope the pair axis is rows (pairIndex = row, opp = column); for
// COLUMN scope it is columns (pairIndex = column, opp = row).
func pairwiseLegCoord(rowScope bool, pairIdx, oppIdx int) (row, col int) {
	if rowScope {
		return pairIdx, oppIdx
	}
	return oppIdx, pairIdx
}

// extractPairInputs reads both legs' inputs for a pair at a fixed
// opposite-axis coordinate. Returns ok=false when either leg is
// unreadable (missing cell / components).
func extractPairInputs(host *CrosstabHostView, params types.PairwiseOverlayParams, welford, rowScope bool, r1, c1, r2, c2 int) (pwInputs, bool) {
	if welford {
		m1, v1, n1, ok1 := host.WelfordTriple(r1, c1)
		m2, v2, n2, ok2 := host.WelfordTriple(r2, c2)
		if !ok1 || !ok2 {
			return pwInputs{}, false
		}
		return pwInputs{m1: m1, m2: m2, v1: v1, v2: v2, n1: n1, n2: n2,
			p1: math.NaN(), p2: math.NaN()}, true
	}
	p1, ok1 := pairwiseProportion(host, params, r1, c1)
	p2, ok2 := pairwiseProportion(host, params, r2, c2)
	n1, nok1 := pairwiseSampleSize(host, params, rowScope, r1, c1)
	n2, nok2 := pairwiseSampleSize(host, params, rowScope, r2, c2)
	if !ok1 || !ok2 || !nok1 || !nok2 {
		return pwInputs{}, false
	}
	return pwInputs{p1: p1, p2: p2, n1: n1, n2: n2,
		m1: math.NaN(), m2: math.NaN(), v1: math.NaN(), v2: math.NaN()}, true
}

func pairwiseProportion(host *CrosstabHostView, params types.PairwiseOverlayParams, r, c int) (float64, bool) {
	v, ok := host.CellAt(r, c)
	if !ok {
		return 0, false
	}
	if params.PSource == types.PairwisePSourceCellValue {
		return v, true
	}
	return v / 100.0, true
}

// pairwiseSampleSize reads the n leg per the n_source mode. rowScope names
// the pair axis so n_within sums CellCounts along it.
func pairwiseSampleSize(host *CrosstabHostView, params types.PairwiseOverlayParams, rowScope bool, r, c int) (int, bool) {
	switch params.NSource {
	case types.PairwiseNSourceCellValueWeight:
		v, ok := host.CellAt(r, c)
		if !ok {
			return 0, false
		}
		return int(v), true
	case types.PairwiseNSourceRowMarginN:
		return host.RowMarginN(r)
	case types.PairwiseNSourceColumnMarginN:
		return host.ColumnMarginN(c)
	case types.PairwiseNSourceCellWeightSum:
		f, ok := host.CellWeightSum(r, c)
		if !ok {
			return 0, false
		}
		return int(f), true
	case types.PairwiseNSourceNWithin:
		prefix := params.NWithinDepth + 1
		if rowScope {
			return host.RowSlabN(r, c, prefix)
		}
		return host.ColumnSlabN(r, c, prefix)
	default:
		return host.CellN(r, c)
	}
}

// buildPairwiseLayer assembles the MATRIX-payload OverlayLayer. ROW scope
// emits rows = pairs, columns = host columns; COLUMN scope transposes.
func buildPairwiseLayer(spec *types.OverlaySpec, host *CrosstabHostView, pairs []pairwisePair, pvals [][]types.MatrixCell, rowScope bool) types.OverlayLayer {
	pairHeader := types.AxisHeader{Fields: []string{"pair_a", "pair_b"}, Types: []string{"PAIR", "PAIR"}}
	pairKeys := make([]types.AxisKey, len(pairs))
	for p, pr := range pairs {
		pairKeys[p] = types.AxisKey{pr.labelI, pr.labelJ}
	}

	var mx types.MatrixPayload
	mx.CellLabel = "p_value"
	present := 0

	if rowScope {
		mx.RowHeader = pairHeader
		mx.RowKeys = pairKeys
		mx.ColumnHeader = host.Payload().ColumnHeader
		mx.ColumnKeys = copyAxisKeys(host.Payload().ColumnKeys)
		mx.Cells = pvals // already [pair][col]
		for _, row := range pvals {
			for _, cell := range row {
				if cell.Present {
					present++
				}
			}
		}
	} else {
		mx.RowHeader = host.Payload().RowHeader
		mx.RowKeys = copyAxisKeys(host.Payload().RowKeys)
		mx.ColumnHeader = pairHeader
		mx.ColumnKeys = pairKeys
		// Transpose pvals ([pair][row]) into Cells ([row][pair]).
		rowCount := host.RowCount()
		cells := make([][]types.MatrixCell, rowCount)
		for r := 0; r < rowCount; r++ {
			cells[r] = make([]types.MatrixCell, len(pairs))
			for p := range pairs {
				cell := pvals[p][r]
				cells[r][p] = cell
				if cell.Present {
					present++
				}
			}
		}
		mx.Cells = cells
	}

	count := present
	return types.OverlayLayer{
		Name:  overlayLayerName(spec),
		Kind:  spec.Kind,
		Scope: spec.Scope,
		Ref:   spec.Ref,
		Payload: types.OverlayPayload{
			Shape:  types.OverlayShapeMatrix,
			Matrix: &mx,
		},
		Summary: &types.OverlaySummary{Count: &count},
	}
}

func copyAxisKeys(in []types.AxisKey) []types.AxisKey {
	if in == nil {
		return nil
	}
	out := make([]types.AxisKey, len(in))
	for i, k := range in {
		out[i] = append(types.AxisKey(nil), k...)
	}
	return out
}

// stringifyOverlayAxisKey collapses an AxisKey into a deterministic label
// for the pair-key 2-tuple. Single-element keys produce the bare value;
// multi-element keys join with "|".
func stringifyOverlayAxisKey(k types.AxisKey) string {
	switch len(k) {
	case 0:
		return ""
	case 1:
		return fmt.Sprintf("%v", k[0])
	default:
		parts := make([]string, len(k))
		for i, v := range k {
			parts[i] = fmt.Sprintf("%v", v)
		}
		return joinPipe(parts)
	}
}

func joinPipe(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += "|"
		}
		out += p
	}
	return out
}

// ----- per-pair Compute kernels (ported from Orbit service/stats) -----

// pairwisePropZKernel is the pooled-SE two-proportion z-test. Reuses the
// shared twoProportionZ helper so p-values match OVERLAY_PROP_Z_CELL.
func pairwisePropZKernel(in pwInputs) (float64, string, bool) {
	if in.n1 == 0 || in.n2 == 0 {
		return 0, "ORBIT_STATS_SKIP_N_ZERO", false
	}
	n1f, n2f := float64(in.n1), float64(in.n2)
	p, ok := twoProportionZ(in.p1*n1f, n1f, in.p2*n2f, n2f)
	if !ok {
		return 0, "ORBIT_STATS_SKIP_POOLED_SE_INVALID", false
	}
	return p, "", true
}

// pairwiseProbitTKernel is the probit t-test: Φ⁻¹ transform of each leg's
// proportion, Student-t tail on df = n1 + n2 - 2.
func pairwiseProbitTKernel(in pwInputs) (float64, string, bool) {
	if in.n1 == 0 || in.n2 == 0 {
		return 0, "ORBIT_STATS_SKIP_N_ZERO", false
	}
	dof := float64(in.n1 + in.n2 - 2)
	if dof <= 0 {
		return 0, "ORBIT_STATS_SKIP_DOF_INVALID", false
	}
	const eps = 1e-10
	p1c := clipUnit(in.p1, eps, 1-eps)
	p2c := clipUnit(in.p2, eps, 1-eps)
	denom := math.Sqrt(1.0/float64(in.n1) + 1.0/float64(in.n2))
	if denom == 0 {
		return 0, "ORBIT_STATS_SKIP_DENOM_INVALID", false
	}
	t := (standardNormalPPF(p1c) - standardNormalPPF(p2c)) / denom
	if math.IsNaN(t) || math.IsInf(t, 0) {
		return 0, "ORBIT_STATS_SKIP_T_INVALID", false
	}
	pv := studentTTwoSidedP(t, dof)
	if math.IsNaN(pv) {
		return 0, "ORBIT_STATS_SKIP_PVALUE_NAN", false
	}
	return pv, "", true
}

// pairwiseWelchTKernel is the Welch–Satterthwaite t-test on Welford
// triples.
func pairwiseWelchTKernel(in pwInputs) (float64, string, bool) {
	if in.n1 <= 1 || in.n2 <= 1 {
		return 0, "ORBIT_STATS_SKIP_N_TOO_SMALL", false
	}
	n1f, n2f := float64(in.n1), float64(in.n2)
	a := in.v1 / n1f
	b := in.v2 / n2f
	if a+b <= 0 {
		return 0, "ORBIT_STATS_SKIP_VARIANCE_INVALID", false
	}
	se := math.Sqrt(a + b)
	if se == 0 {
		return 0, "ORBIT_STATS_SKIP_SE_ZERO", false
	}
	t := (in.m1 - in.m2) / se
	dofDenom := (a*a)/(n1f-1) + (b*b)/(n2f-1)
	if dofDenom <= 0 {
		return 0, "ORBIT_STATS_SKIP_DOF_INVALID", false
	}
	dof := (a + b) * (a + b) / dofDenom
	if dof <= 0 || math.IsNaN(dof) || math.IsInf(dof, 0) {
		return 0, "ORBIT_STATS_SKIP_DOF_INVALID", false
	}
	pv := studentTTwoSidedP(t, dof)
	if math.IsNaN(pv) {
		return 0, "ORBIT_STATS_SKIP_PVALUE_NAN", false
	}
	return pv, "", true
}

// pairwiseTwoMeansZKernel is the two-means z-test on Welford triples
// (normal CDF tail, no df adjustment).
func pairwiseTwoMeansZKernel(in pwInputs) (float64, string, bool) {
	if in.n1 <= 1 || in.n2 <= 1 {
		return 0, "ORBIT_STATS_SKIP_N_TOO_SMALL", false
	}
	a := in.v1 / float64(in.n1)
	b := in.v2 / float64(in.n2)
	if a+b <= 0 {
		return 0, "ORBIT_STATS_SKIP_VARIANCE_INVALID", false
	}
	se := math.Sqrt(a + b)
	if se == 0 {
		return 0, "ORBIT_STATS_SKIP_SE_ZERO", false
	}
	z := (in.m1 - in.m2) / se
	pv := 2 * standardNormalCDF(-math.Abs(z))
	return pv, "", true
}

func clipUnit(x, lo, hi float64) float64 {
	if x < lo {
		return lo
	}
	if x > hi {
		return hi
	}
	return x
}

// standardNormalPPF returns Φ⁻¹(p) via the Beasley-Springer-Moro rational
// approximation (~1e-9 across the support). Ported from Orbit's stats
// package so the probit kernel matches byte-for-byte. ±Inf at p∈{0,1};
// the probit kernel clips p before calling.
func standardNormalPPF(p float64) float64 {
	a := [...]float64{
		-3.969683028665376e+01, 2.209460984245205e+02, -2.759285104469687e+02,
		1.383577518672690e+02, -3.066479806614716e+01, 2.506628277459239e+00,
	}
	b := [...]float64{
		-5.447609879822406e+01, 1.615858368580409e+02, -1.556989798598866e+02,
		6.680131188771972e+01, -1.328068155288572e+01,
	}
	c := [...]float64{
		-7.784894002430293e-03, -3.223964580411365e-01, -2.400758277161838e+00,
		-2.549732539343734e+00, 4.374664141464968e+00, 2.938163982698783e+00,
	}
	d := [...]float64{
		7.784695709041462e-03, 3.224671290700398e-01, 2.445134137142996e+00,
		3.754408661907416e+00,
	}
	const pLow = 0.02425
	const pHigh = 1 - pLow
	switch {
	case p <= 0:
		return math.Inf(-1)
	case p >= 1:
		return math.Inf(1)
	case p < pLow:
		q := math.Sqrt(-2 * math.Log(p))
		return (((((c[0]*q+c[1])*q+c[2])*q+c[3])*q+c[4])*q + c[5]) /
			((((d[0]*q+d[1])*q+d[2])*q+d[3])*q + 1)
	case p <= pHigh:
		q := p - 0.5
		r := q * q
		return (((((a[0]*r+a[1])*r+a[2])*r+a[3])*r+a[4])*r + a[5]) * q /
			(((((b[0]*r+b[1])*r+b[2])*r+b[3])*r+b[4])*r + 1)
	default:
		q := math.Sqrt(-2 * math.Log(1-p))
		return -(((((c[0]*q+c[1])*q+c[2])*q+c[3])*q+c[4])*q + c[5]) /
			((((d[0]*q+d[1])*q+d[2])*q+d[3])*q + 1)
	}
}

// ----- skip aggregation -----

// pairwiseTally aggregates per-(pair, opposite) skips by reason code into
// one warning per code carrying the count and a sample opposite-axis
// index, mirroring Orbit's skipTally — avoids one warning per degenerate
// cell on a wide matrix.
type pairwiseTally struct {
	order  []string
	counts map[string]int
	sample map[string]int
}

func newPairwiseTally() *pairwiseTally {
	return &pairwiseTally{counts: map[string]int{}, sample: map[string]int{}}
}

func (t *pairwiseTally) add(reason string, oppIdx int) {
	if reason == "" {
		reason = "ORBIT_STATS_SKIP_EXTRACT_FAILED"
	}
	if _, seen := t.counts[reason]; !seen {
		t.order = append(t.order, reason)
		t.sample[reason] = oppIdx
	}
	t.counts[reason]++
}

func (t *pairwiseTally) warnings(spec *types.OverlaySpec) []types.OverlayWarning {
	if len(t.order) == 0 {
		return nil
	}
	out := make([]types.OverlayWarning, 0, len(t.order))
	for _, reason := range t.order {
		out = append(out, types.OverlayWarning{
			Code:    string(errors.PULSE_OVERLAY_REF_ZERO),
			Message: "overlay " + string(spec.Kind) + " skipped " + fmt.Sprint(t.counts[reason]) + " pair-cell(s): " + reason,
			Details: map[string]any{
				"kind":                string(spec.Kind),
				"reason":              reason,
				"skipped":             t.counts[reason],
				"sample_opposite_idx": t.sample[reason],
			},
		})
	}
	return out
}
