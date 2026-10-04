package processing

import (
	"math"
	"sort"

	"github.com/frankbardon/pulse/internal/weighting"
	"github.com/frankbardon/pulse/types"
)

// Weighted forms of the shape aggregators (weighting-descriptive
// E2-S1; contract .claude/reference/weighting.md "Aggregator
// classification"):
//
//   - AGG_MEDIAN / AGG_PERCENTILE — Hmisc wtd.quantile over a paired
//     (value, weight) collector (type 7 on the expansion for integer
//     cumulative weights), probability weights rescaled to sum to n;
//     buffered only, exactly as unweighted.
//   - AGG_MODE / AGG_MODE_COUNT — the value with the largest Σw (ties to
//     the smallest value) and that Σw; streamable and mergeable, exactly
//     as unweighted.
//   - AGG_SKEWNESS / AGG_KURTOSIS — weighted population moments;
//     streamable, not mergeable, exactly as unweighted.
//
// Like the core weighted aggregators (aggregator_weighted.go) every
// formula reduces to its unweighted twin's operation sequence at w = 1
// (the operation-order rule), so an all-ones weight column reproduces
// the unweighted figure bit for bit, and each factory returns these
// types only when the slot carries an applied weight. None implements
// valueAggregator: the weights live on the records, so the orchestrator
// must hand them the records, never a pre-collected value slice.

// validRowWeight returns the row's weight under spec and whether the row
// contributes: a valid weight (weighting.Classify) greater than zero.
// Invalid weights are excluded here and counted by the orchestrator's
// WeightFloor / WeightRowTally — never coerced.
func validRowWeight(r *Record, spec *types.WeightSpec) (float64, bool) {
	w, ok := r.NumericValue(spec.Field)
	if weighting.Classify(w, ok, spec.Kind) != weighting.Valid || w == 0 {
		return 0, false
	}
	return w, true
}

// effectiveWeight copies spec with its kind spelled out.
func effectiveWeight(spec *types.WeightSpec) types.WeightSpec {
	w := *spec
	w.Kind = spec.EffectiveKind()
	return w
}

// weightedPair is one contributing (value, weight) observation.
type weightedPair struct{ x, w float64 }

// collectWeightedPairs is the paired collector for buffered value
// aggregators: every row whose value is present and whose weight
// contributes, in record order.
func collectWeightedPairs(records []*Record, field string, spec *types.WeightSpec) []weightedPair {
	out := make([]weightedPair, 0, len(records))
	for _, r := range records {
		x, ok := r.NumericValue(field)
		if !ok {
			continue
		}
		if w, ok := validRowWeight(r, spec); ok {
			out = append(out, weightedPair{x, w})
		}
	}
	return out
}

// --- AGG_MEDIAN / AGG_PERCENTILE --------------------------------------

// weightedQuantileAggregator is the weighted median / percentile — Hmisc
// wtd.quantile(type = "quantile") over the expanded index. The pairs are
// sorted by value (sort.Float64s order: NaN first) and W = Σw. The rank
// is h = p·(W − 1) — the unweighted rank p·(n − 1) at w = 1 — and the
// figure interpolates linearly between x₍⌊h⌋₎ and x₍⌈h⌉₎, where the
// 0-based order statistic x₍ₖ₎ is the smallest value whose cumulative
// weight is ≥ the 1-based rank k + 1 (quantileOrderIndex). With integer
// cumulative weights x₍ₖ₎ is the k-th element of the physically
// expanded sorted data, so this is type 7 on the expansion; with
// fractional ones it is Hmisc's choice of order statistic. Before the
// comparison every cumulative weight within 1e-9 relative of an integer
// is snapped to it (snapCumWeights), so a cumulative weight one ulp off
// an integer rank cannot flip the answer between platforms.
//
// Scale rule: kind probability (the default) rescales the cumulative
// weights so W = n, the contributing row count, before the rank is
// taken — Hmisc wtd.quantile(normwt = TRUE) — so multiplying every
// weight by a constant leaves the figure unchanged and h = p·(n − 1)
// always. Kind frequency keeps the raw weights (W is the expanded row
// count; Hmisc normwt = FALSE), so it equals type 7 on the physically
// expanded data. Above, h ≤ W − 1 keeps x₍⌈h⌉₎ inside the data (a
// lookup past the last cumulative weight answers the largest value).
// The interpolation keeps the unweighted lo + f·(hi − lo) form, not
// Hmisc's (1 − f)·lo + f·hi, so w ≡ 1 stays bit-identical to the
// unweighted aggregator; the two agree to the last few ulps.
//
// Components keep the unweighted keys and types: position /
// position_low / position_high are the EXPANDED-space indices ⌊h⌋ / ⌈h⌉
// (integers; equal to the sorted-row indices at w = 1; in the
// normalized space for probability weights).
type weightedQuantileAggregator struct {
	op         types.AggregationType
	weight     types.WeightSpec
	percentile float64

	frozenFinalized bool
	frozenN         int
	frozenLow       int
	frozenHigh      int
	frozenLower     float64
	frozenUpper     float64
	frozenValue     float64
}

func newWeightedQuantileAggregator(op types.AggregationType, spec *types.WeightSpec, percentile float64) *weightedQuantileAggregator {
	return &weightedQuantileAggregator{op: op, weight: effectiveWeight(spec), percentile: percentile}
}

func (a *weightedQuantileAggregator) Aggregate(records []*Record, field string) (float64, error) {
	pairs := collectWeightedPairs(records, field, &a.weight)
	a.frozenFinalized = true
	a.frozenN = len(pairs)
	a.frozenLow, a.frozenHigh = 0, 0
	a.frozenLower, a.frozenUpper, a.frozenValue = 0, 0, 0
	if len(pairs) == 0 {
		return 0, nil
	}
	sort.SliceStable(pairs, func(i, j int) bool {
		x, y := pairs[i].x, pairs[j].x
		return x < y || (math.IsNaN(x) && !math.IsNaN(y))
	})
	cum := make([]float64, len(pairs))
	total := 0.0
	for i, p := range pairs {
		total += p.w
		cum[i] = total
	}
	// Probability weights are rescaled to sum to the contributing row
	// count n (Hmisc wtd.quantile normwt = TRUE), so the figure does not
	// depend on the weights' scale; frequency weights stay raw — they
	// ARE the replication counts. Skipped when Σw is already n (always
	// at w ≡ 1), though (cum·n)/n would be exact there anyway: cum is an
	// integer and cum·n < 2⁵³.
	if n := float64(len(pairs)); a.weight.Kind == types.WeightKindProbability && total != n {
		for i := range cum {
			cum[i] = cum[i] * n / total
		}
		total = n
	}
	snapCumWeights(cum)
	at := func(k int) float64 { return pairs[quantileOrderIndex(cum, k)].x }
	// W ≥ 1 here (W = n ≥ 1 for probability; a sum of positive integers
	// for frequency), so h ≥ 0 needs no clamp.
	if a.op == types.AGG_MEDIAN {
		a.median(total, at)
	} else {
		a.interpolate(total, at)
	}
	return a.frozenValue, nil
}

// cumWeightSnapTol is the relative distance within which a cumulative
// weight is snapped to the nearest integer before it is compared with a
// rank (weighting.md "Weighted percentile / median"). The comparison is
// a knife edge exactly on an integer: a cumulative weight that is
// mathematically the rank but lands one ulp below it — float summation
// order, the probability rescale, FMA contraction on one architecture
// and not another — would pick the next order statistic. Snapping makes
// the answer the exact-arithmetic one, deterministically on every
// platform. Integer cumulative weights (frequency weights, w ≡ 1) are
// fixed points, so the snap never moves them.
const cumWeightSnapTol = 1e-9

// snapCumWeights snaps, in place, every cumulative weight within
// cumWeightSnapTol·max(1, |c|) of an integer to that integer.
func snapCumWeights(cum []float64) []float64 {
	for i, c := range cum {
		if r := math.Round(c); math.Abs(c-r) <= cumWeightSnapTol*math.Max(1, math.Abs(c)) {
			cum[i] = r
		}
	}
	return cum
}

// quantileOrderIndex is the sorted-pair index of the 0-based order
// statistic x₍ₖ₎: the first pair whose cumulative weight reaches the
// 1-based rank k + 1 (Hmisc wtd.quantile: approx(cumsum(w), x, xout =
// rank, method = "constant", f = 1, rule = 2)). Past the last cumulative
// weight it answers the last pair (rule = 2).
func quantileOrderIndex(cum []float64, k int) int {
	i := sort.Search(len(cum), func(i int) bool { return cum[i] >= float64(k+1) })
	if i == len(cum) {
		i = len(cum) - 1
	}
	return i
}

// median is h = 0.5·(W − 1): an integer W gives an integer or
// half-integer h, and the half case is the unweighted median's mean of
// the two middles, (lo + hi) / 2, in its operation order.
func (a *weightedQuantileAggregator) median(total float64, at func(int) float64) {
	rank := 0.5 * (total - 1)
	low := int(math.Floor(rank))
	high := int(math.Ceil(rank))
	a.frozenLow, a.frozenHigh = low, high
	a.frozenLower, a.frozenUpper = at(low), at(high)
	switch {
	case low == high:
		a.frozenValue = a.frozenLower
	case rank-float64(low) == 0.5:
		a.frozenValue = (a.frozenLower + a.frozenUpper) / 2
	default:
		a.frozenValue = a.frozenLower + (rank-float64(low))*(a.frozenUpper-a.frozenLower)
	}
}

// interpolate is the percentile, h = p·(W − 1), written in the
// unweighted percentileAggregator's exact statement shape — in
// particular no clamp reassigning rank: the compiler may fuse the rank
// product into rank − lower (FMA, on arm64), a reassignment blocks that,
// and unity parity needs both forms to fuse identically, bit for bit
// (TestWeightedShape_FrequencyQuantileRaw p95: 37 vs 36.99999999999999).
func (a *weightedQuantileAggregator) interpolate(total float64, at func(int) float64) {
	rank := a.percentile / 100.0 * (total - 1)
	lower := int(math.Floor(rank))
	upper := int(math.Ceil(rank))
	a.frozenLow, a.frozenHigh = lower, upper
	lo, hi := at(lower), at(upper)
	a.frozenLower = lo
	a.frozenUpper = hi
	if lower == upper {
		a.frozenValue = lo
		return
	}
	a.frozenValue = lo + (rank-float64(lower))*(hi-lo)
}

// Components mirrors the unweighted median / percentile maps (empty
// input ⇒ nil, the floor's n is the source of truth).
func (a *weightedQuantileAggregator) Components() (map[string]any, error) {
	if !a.frozenFinalized || a.frozenN == 0 {
		return nil, nil
	}
	if a.op == types.AGG_MEDIAN {
		return map[string]any{
			"position_low":  a.frozenLow,
			"position_high": a.frozenHigh,
			"median":        a.frozenValue,
		}, nil
	}
	return map[string]any{
		"p":        a.percentile,
		"position": a.frozenLow,
		"lower":    a.frozenLower,
		"upper":    a.frozenUpper,
		"method":   percentileMethodLinear,
		"value":    a.frozenValue,
	}, nil
}

// --- AGG_MODE / AGG_MODE_COUNT ----------------------------------------

// weightedModeAggregator keeps Σw per distinct value. AGG_MODE answers
// the value with the largest Σw, AGG_MODE_COUNT that Σw (a float);
// ties go to the smallest value, as unweighted. A zero weight
// contributes nothing, so a value seen only at weight zero is not a
// distinct value.
type weightedModeAggregator struct {
	op     types.AggregationType
	weight types.WeightSpec
	sums   map[float64]float64

	frozenFinalized bool
	frozenValue     float64
	frozenCount     float64
	frozenDistinct  int
	frozenTieCount  int
}

func newWeightedModeAggregator(op types.AggregationType, spec *types.WeightSpec) *weightedModeAggregator {
	return &weightedModeAggregator{op: op, weight: effectiveWeight(spec)}
}

func (a *weightedModeAggregator) UpdateRow(r *Record, field string) error {
	x, ok := r.NumericValue(field)
	if !ok {
		return nil
	}
	w, ok := validRowWeight(r, &a.weight)
	if !ok {
		return nil
	}
	if a.sums == nil {
		a.sums = make(map[float64]float64)
	}
	a.sums[x] += w
	return nil
}

func (a *weightedModeAggregator) Aggregate(records []*Record, field string) (float64, error) {
	a.sums = nil
	for _, r := range records {
		if err := a.UpdateRow(r, field); err != nil {
			return 0, err
		}
	}
	return a.Finalize()
}

func (a *weightedModeAggregator) Finalize() (float64, error) {
	a.frozenFinalized = true
	a.frozenValue, a.frozenCount, a.frozenTieCount = 0, 0, 0
	a.frozenDistinct = len(a.sums)
	for _, s := range a.sums {
		if s > a.frozenCount {
			a.frozenCount = s
		}
	}
	first := true
	for v, s := range a.sums {
		if s == a.frozenCount {
			a.frozenTieCount++
			if first || v < a.frozenValue {
				a.frozenValue = v
				first = false
			}
		}
	}
	a.sums = nil
	if a.op == types.AGG_MODE_COUNT {
		return a.frozenCount, nil
	}
	return a.frozenValue, nil
}

// MergeOnline folds another partial of the same slot: per-value Σw add.
func (a *weightedModeAggregator) MergeOnline(other OnlineAggregator) error {
	b, ok := other.(*weightedModeAggregator)
	if !ok || b.op != a.op {
		return mergeTypeMismatch(string(a.op))
	}
	if len(b.sums) == 0 {
		return nil
	}
	if a.sums == nil {
		a.sums = make(map[float64]float64, len(b.sums))
	}
	for v, s := range b.sums {
		a.sums[v] += s
	}
	return nil
}

// Components mirrors the unweighted maps; count / mode_count carry the
// modal Σw (a float — integral, and so wire-identical, at w = 1).
func (a *weightedModeAggregator) Components() (map[string]any, error) {
	if !a.frozenFinalized || a.frozenDistinct == 0 {
		return nil, nil
	}
	if a.op == types.AGG_MODE_COUNT {
		return map[string]any{
			"distinct_count": a.frozenDistinct,
			"mode_value":     a.frozenValue,
			"mode_count":     a.frozenCount,
		}, nil
	}
	return map[string]any{
		"value":          a.frozenValue,
		"count":          a.frozenCount,
		"distinct_count": a.frozenDistinct,
		"tie_count":      a.frozenTieCount,
	}, nil
}

// --- AGG_SKEWNESS / AGG_KURTOSIS --------------------------------------

// momentState is the weighted central-moment state: rows contributing,
// W = Σw, the weighted mean and Mₖ = Σw(x − mean)ᵏ.
type momentState struct {
	rows       int64
	sumW       float64
	mean       float64
	m2, m3, m4 float64
}

// weightedMomentAggregator is weighted skewness / kurtosis: the
// population moments with W in place of n — g1 = M3 / (W·sd³),
// g2 = M4 / (W·var²) − 3, var = M2 / W.
//
// Streaming folds one observation of weight w into (W, mean, M2..M4) by
// Pébaÿ's pairwise update with n_A = W_old, n_B = w. Written as the
// unweighted recurrence with deltaN = (w·δ)/W and the (n − 1), (n − 2),
// (n² − 3n + 3) coefficients replaced by W_old, (W_old − w)/w and
// (W_old² − W_old·w + w²)/w², it is that recurrence's exact operation
// sequence at w = 1. The buffered path mirrors the unweighted two-pass
// form (mean first, then the weighted deviation powers).
type weightedMomentAggregator struct {
	op     types.AggregationType
	weight types.WeightSpec
	momentState
	frozen momentState
}

func newWeightedMomentAggregator(op types.AggregationType, spec *types.WeightSpec) *weightedMomentAggregator {
	return &weightedMomentAggregator{op: op, weight: effectiveWeight(spec)}
}

func (a *weightedMomentAggregator) kurtosis() bool { return a.op == types.AGG_KURTOSIS }

func (a *weightedMomentAggregator) UpdateRow(r *Record, field string) error {
	x, ok := r.NumericValue(field)
	if !ok {
		return nil
	}
	w, ok := validRowWeight(r, &a.weight)
	if !ok {
		return nil
	}
	a.rows++
	wOld := a.sumW
	a.sumW += w
	delta := x - a.mean
	deltaN := (w * delta) / a.sumW
	term1 := delta * deltaN * wOld
	if a.kurtosis() {
		deltaN2 := deltaN * deltaN
		c4 := (wOld*wOld - wOld*w + w*w) / (w * w)
		a.m4 += term1*deltaN2*c4 + 6*deltaN2*a.m2 - 4*deltaN*a.m3
	}
	c3 := (wOld - w) / w
	a.m3 += term1*deltaN*c3 - 3*deltaN*a.m2
	a.m2 += term1
	a.mean += deltaN
	return nil
}

// Finalize freezes the moments and returns the figure, as the
// unweighted Finalize does over n.
func (a *weightedMomentAggregator) Finalize() (float64, error) {
	a.frozen = a.momentState
	a.momentState = momentState{}
	return a.frozen.shape(a.kurtosis()), nil
}

// shape is the streaming figure over st (0 for ≤ 1 row or no spread).
func (st *momentState) shape(kurtosis bool) float64 {
	if st.rows <= 1 || st.m2 == 0 {
		return 0
	}
	W := st.sumW
	variance := st.m2 / W
	if kurtosis {
		if variance == 0 {
			return 0
		}
		return st.m4/(W*variance*variance) - 3
	}
	sd := math.Sqrt(variance)
	if sd == 0 {
		return 0
	}
	return st.m3 / (W * sd * sd * sd)
}

// Aggregate is the buffered two-pass form: the weighted mean, the
// weighted deviation powers for the components, and the scalar as the
// weighted mean of the standardized powers — each the unweighted
// buffered sequence at w = 1.
func (a *weightedMomentAggregator) Aggregate(records []*Record, field string) (float64, error) {
	pairs := collectWeightedPairs(records, field, &a.weight)
	st := momentState{rows: int64(len(pairs))}
	sumWX := 0.0
	for _, p := range pairs {
		st.sumW += p.w
		sumWX += p.w * p.x
	}
	if st.sumW != 0 {
		st.mean = sumWX / st.sumW
	}
	for _, p := range pairs {
		d := p.x - st.mean
		d2 := d * d
		// (w·d)·d, not w·d2: the unweighted m2 += d2 is fused to
		// FMA(d, d, m2) where FMA is used, and (w·d)·d fuses to the same
		// instruction at w = 1.
		st.m2 += p.w * d * d
		st.m3 += p.w * d2 * d
		if a.kurtosis() {
			st.m4 += p.w * d2 * d2
		}
	}
	a.frozen = st
	a.momentState = momentState{}
	if st.rows <= 1 {
		return 0, nil
	}
	sumSq := 0.0
	for _, p := range pairs {
		d := p.x - st.mean
		sumSq += p.w * d * d
	}
	sd := math.Sqrt(sumSq / st.sumW)
	if sd == 0 {
		return 0, nil
	}
	power := 3.0
	if a.kurtosis() {
		power = 4
	}
	sum := 0.0
	for _, p := range pairs {
		sum += p.w * math.Pow((p.x-st.mean)/sd, power)
	}
	if a.kurtosis() {
		return sum/st.sumW - 3, nil
	}
	return sum / st.sumW, nil
}

// Components mirrors the unweighted {mean, m2, m3[, m4], skewness |
// kurtosis} map from the frozen weighted moments.
func (a *weightedMomentAggregator) Components() (map[string]any, error) {
	st := &a.frozen
	out := map[string]any{"mean": st.mean, "m2": st.m2, "m3": st.m3}
	var figure float64
	if st.rows > 1 && st.m2 > 0 {
		W := st.sumW
		variance := st.m2 / W
		if a.kurtosis() {
			if variance > 0 {
				figure = st.m4/(W*variance*variance) - 3
			}
		} else if sd := math.Sqrt(variance); sd > 0 {
			figure = st.m3 / (W * sd * sd * sd)
		}
	}
	if a.kurtosis() {
		out["m4"] = st.m4
		out["kurtosis"] = figure
	} else {
		out["skewness"] = figure
	}
	if st.rows == 0 {
		for k := range out {
			out[k] = 0.0
		}
	}
	return out, nil
}

// Compile-time interface locks: the weighted twins expose exactly the
// execution interfaces their unweighted twins do.
var (
	_ MetaAggregator      = (*weightedQuantileAggregator)(nil)
	_ MergeableAggregator = (*weightedModeAggregator)(nil)
	_ MetaAggregator      = (*weightedModeAggregator)(nil)
	_ OnlineAggregator    = (*weightedMomentAggregator)(nil)
	_ MetaAggregator      = (*weightedMomentAggregator)(nil)
)
