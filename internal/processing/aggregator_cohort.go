package processing

import (
	"encoding/json"
	"math"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/weighting"
	"github.com/frankbardon/pulse/types"
)

// Cohort-analytics aggregators — AGG_WEIGHTED_MEAN, AGG_RATIO,
// AGG_CI_LOWER, AGG_CI_UPPER. These give the orchestrator first-class
// support for stats an embedder would otherwise compute client-side
// after a generic Pulse fold. Each implementation cleanly fits the
// existing Aggregator + OnlineAggregator + MergeableAggregator surface
// — no orchestrator changes required.
//
// Two more cohort aggregators (AGG_LIFT, AGG_SHARE) need access to the
// full record set + per-group buckets at the same time. They will land
// in a follow-up once a `CohortAwareAggregator` interface or a global
// pre-pass hook exists.

type weightedMeanParams struct {
	WeightField string `json:"weight_field"`
}

// newWeightedMeanAggregator builds AGG_WEIGHTED_MEAN — an alias of
// weighted AGG_AVERAGE (weightedAggregator) that keeps its own type
// name and components shape. Its weight is the slot's applied weight
// (StampWeights folds params.weight_field, the slot `weight`, the
// request weight and Options.DefaultWeight into it); a slot that was
// not stamped falls back to params.weight_field (kind probability), so
// a direct factory caller keeps the pre-weighting spelling. No weight
// at all — or an explicit `weight: null` — is PROCESSING_CONFIG: the
// operator is a weighted figure by definition.
func newWeightedMeanAggregator(agg *types.Aggregation, _ *encoding.Schema) (Aggregator, error) {
	var params weightedMeanParams
	if len(agg.Params) > 0 {
		if err := json.Unmarshal(agg.Params, &params); err != nil {
			return nil, errors.NewCodedError(errors.PROCESSING_CONFIG, "invalid weighted_mean params: "+err.Error())
		}
	}
	spec := slotWeight(agg)
	if spec == nil && agg.Weight.IsZero() && params.WeightField != "" {
		spec = &types.WeightSpec{Field: params.WeightField, Kind: types.WeightKindProbability}
	}
	if spec == nil {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"AGG_WEIGHTED_MEAN needs a weight: set params.weight_field, a slot weight or a request weight")
	}
	return newWeightedAggregator(types.AGG_WEIGHTED_MEAN, spec), nil
}

// weightedVariance is the frequency-weights variance m2 / (Σw − 1)
// (statsmodels DescrStatsW.var with ddof=1). Σw ≤ 1 has no positive
// denominator, so it reports 0 — the same zero floor the empty cell
// uses; callers detect it from sum_weights.
func weightedVariance(m2, wSum float64) float64 {
	if wSum <= 1 {
		return 0
	}
	return m2 / (wSum - 1)
}

// kishNEff is Kish's effective sample size (Σw)² / Σw² — the one
// definition weighted inference reads too (weighting.KishNEff). An
// empty cell (Σw² == 0) reports 0.
func kishNEff(wSum, wSumSq float64) float64 { return weighting.KishNEff(wSum, wSumSq) }

type ratioParams struct {
	NumeratorField   string `json:"numerator_field"`
	DenominatorField string `json:"denominator_field"`
}

// ratioAggregator emits sum(num) / sum(den) at finalize. Both sums are
// independent + associative, so MergeOnline is trivial. Null on either
// field skips that row's contribution to BOTH sums to keep the ratio
// honest. Denominator-zero at finalize emits NaN (consistent with
// IEEE-754 0/0); callers can detect via math.IsNaN. On the JSON wire the
// undefined ratio — scalar and components alike — is null
// (types.MarshalFinite), so it never fails the response.
//
// Weighted (a slot weight applied, weighting-descriptive E2-S2): each
// contributing row adds w·num and w·den, so the figure is
// Σw·num / Σw·den; a row whose weight is invalid or zero contributes to
// neither sum. At w = 1, w·x is x and the sums are the unweighted sums
// bit for bit. The components keep their keys (now the weighted sums).
//
// frozen{Num, Den, Ratio, HasResult} mirror the post-Aggregate /
// post-Finalize state so Components() works on both buffered and
// streaming code paths — the streaming Finalize step zeros out
// (num, den) before Components() runs, so the frozen mirrors are the
// source of truth. The frozen ratio preserves NaN under zero-denom
// to match the scalar return path byte-for-byte.
type ratioAggregator struct {
	numField, denField string
	num, den           float64
	// weight is the applied slot weight; nil on an unweighted slot.
	weight *types.WeightSpec

	frozenNum       float64
	frozenDen       float64
	frozenRatio     float64
	frozenHasResult bool
}

func newRatioAggregator(agg *types.Aggregation, _ *encoding.Schema) (Aggregator, error) {
	var params ratioParams
	if len(agg.Params) > 0 {
		if err := json.Unmarshal(agg.Params, &params); err != nil {
			return nil, errors.NewCodedError(errors.PROCESSING_CONFIG, "invalid ratio params: "+err.Error())
		}
	}
	if params.NumeratorField == "" || params.DenominatorField == "" {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"AGG_RATIO requires Params.numerator_field and Params.denominator_field")
	}
	a := &ratioAggregator{numField: params.NumeratorField, denField: params.DenominatorField}
	if w := slotWeight(agg); w != nil {
		ew := effectiveWeight(w)
		a.weight = &ew
	}
	return a, nil
}

// pair returns the row's (numerator, denominator) contribution: both
// fields present (a null on either skips the row), times the row's
// weight on a weighted slot (an invalid or zero weight skips it).
func (a *ratioAggregator) pair(r *Record) (n, d float64, ok bool) {
	n, okN := r.NumericValue(a.numField)
	if !okN {
		return 0, 0, false
	}
	d, okD := r.NumericValue(a.denField)
	if !okD {
		return 0, 0, false
	}
	if a.weight != nil {
		w, ok := validRowWeight(r, a.weight)
		if !ok {
			return 0, 0, false
		}
		return w * n, w * d, true
	}
	return n, d, true
}

func (a *ratioAggregator) Aggregate(records []*Record, field string) (float64, error) {
	a.num, a.den = 0, 0
	for _, r := range records {
		n, d, ok := a.pair(r)
		if !ok {
			continue
		}
		a.num += n
		a.den += d
	}
	out, err := a.divide()
	a.freeze(out)
	return out, err
}

func (a *ratioAggregator) UpdateRow(r *Record, field string) error {
	n, d, ok := a.pair(r)
	if !ok {
		return nil
	}
	a.num += n
	a.den += d
	return nil
}

func (a *ratioAggregator) Finalize() (float64, error) {
	out, err := a.divide()
	a.freeze(out)
	a.num, a.den = 0, 0
	return out, err
}

func (a *ratioAggregator) divide() (float64, error) {
	if a.den == 0 {
		return math.NaN(), nil
	}
	return a.num / a.den, nil
}

// freeze stamps the components mirrors from the live (num, den) state
// and the just-computed scalar. Called from both Aggregate and Finalize
// so Components() returns the same map on either path even after the
// streaming Finalize-reset wipes (num, den). The frozen ratio preserves
// NaN exactly when divide() emitted NaN, so consumers can detect the
// degenerate case via math.IsNaN on the components value without
// re-running the division.
func (a *ratioAggregator) freeze(scalar float64) {
	a.frozenHasResult = true
	a.frozenNum = a.num
	a.frozenDen = a.den
	a.frozenRatio = scalar
}

// Components returns {numerator, denominator, ratio} — the running
// sums of the numerator + denominator fields plus the resolved ratio.
// Reads the frozen mirrors stamped by Aggregate / Finalize so the
// streaming Finalize-reset on (num, den) does not erase the values
// before Components() runs.
//
// Zero-denominator: when sum(den) collapses to zero (every input row
// filtered out, or every row's den == 0), the scalar return is NaN
// (consistent with IEEE-754 0/0) and the frozen ratio mirror preserves
// that NaN (null on the JSON wire, types.MarshalFinite). Numerator and
// denominator surface their raw running sums
// (numerator may be > 0 with denominator == 0 when the den field is
// null on contributing rows — in that case the row is skipped from
// BOTH sums and neither moves, so frozen num + den both end at 0).
func (a *ratioAggregator) Components() (map[string]any, error) {
	if !a.frozenHasResult {
		return map[string]any{
			"numerator":   0.0,
			"denominator": 0.0,
			"ratio":       math.NaN(),
		}, nil
	}
	return map[string]any{
		"numerator":   a.frozenNum,
		"denominator": a.frozenDen,
		"ratio":       a.frozenRatio,
	}, nil
}

// Compile-time interface lock — catches MetaAggregator drift at build
// time for AGG_RATIO.
var _ MetaAggregator = (*ratioAggregator)(nil)

func (a *ratioAggregator) MergeOnline(other OnlineAggregator) error {
	b, ok := other.(*ratioAggregator)
	if !ok {
		return mergeTypeMismatch("AGG_RATIO")
	}
	a.num += b.num
	a.den += b.den
	return nil
}

type ciParams struct {
	Confidence float64 `json:"confidence"`
	Method     string  `json:"method"`
}

// ciAggregator accumulates Welford (n, mean, M2) and emits the
// requested CI bound at finalize: mean ∓ z·stderr with the two-sided
// normal critical value z = Φ⁻¹(1 − α/2) (method "normal") and
// stderr = √(s²/n). Mergeable via the same Chan-Welford reduction as
// the variance aggregator.
//
// Weighted (the slot carries an applied weight, see StampWeights): the
// state is the weighted Welford-West bucket (weighting.Welford) and
// the bound follows the one formula rule (.claude/reference/
// weighting.md, Weighted inference) — the weighted mean, s² on
// w* = w·N*/Σw (weighting.Welford.Variance), stderr = √(s²/N*) with
// N* = Σw under kind frequency (= the unweighted bound on the expanded
// rows) or Kish n_eff under kind probability, and the SAME normal
// critical value (never a t on N* − 1: an all-ones weight must answer
// the unweighted bound bit for bit). Partials merge through the
// weighted Chan merge (mergeWelfordWeighted), so the slot stays
// mergeable. A row whose weight is invalid or zero contributes
// nothing (the floor counts the invalid ones).
//
// frozen{Mean,Stderr,Alpha,TCritical,Bound} mirror the post-finalize
// CI state so Components() can emit
// {mean, stderr, alpha, t_critical, lower|upper} after the Finalize-
// reset wipes the live state. Aggregate (buffered path) and Finalize
// (streaming path) both stamp the same mirrors via the shared bound2
// helper.
type ciAggregator struct {
	bound      ciBound
	confidence float64
	method     string
	n          int64
	mean       float64
	m2         float64

	// weight is the applied slot weight (Kind resolved), nil when the
	// slot is unweighted; basis its inference basis; w the weighted
	// live state.
	weight *types.WeightSpec
	basis  weighting.Basis
	w      weighting.Welford

	frozenMean      float64
	frozenStderr    float64
	frozenAlpha     float64
	frozenTCritical float64
	frozenBound     float64
	frozenHasResult bool
}

type ciBound int

const (
	ciLower ciBound = iota
	ciUpper
)

func newCIAggregator(bound ciBound) AggregatorFactory {
	return func(agg *types.Aggregation, _ *encoding.Schema) (Aggregator, error) {
		params := ciParams{Confidence: 0.95, Method: "normal"}
		if len(agg.Params) > 0 {
			if err := json.Unmarshal(agg.Params, &params); err != nil {
				return nil, errors.NewCodedError(errors.PROCESSING_CONFIG, "invalid ci params: "+err.Error())
			}
			if params.Confidence == 0 {
				params.Confidence = 0.95
			}
			if params.Method == "" {
				params.Method = "normal"
			}
		}
		if !(params.Confidence > 0 && params.Confidence < 1) {
			return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
				"AGG_CI_* confidence must lie in the open interval (0, 1)")
		}
		switch params.Method {
		case "normal":
			// streamable
		case "bootstrap":
			return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
				"AGG_CI_* method=\"bootstrap\" is reserved for a buffered follow-up; use method=\"normal\" today")
		default:
			return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
				"AGG_CI_* method must be \"normal\" or \"bootstrap\"")
		}
		a := &ciAggregator{bound: bound, confidence: params.Confidence, method: params.Method}
		if spec := slotWeight(agg); spec != nil {
			w := *spec
			w.Kind = spec.EffectiveKind()
			a.weight = &w
			a.basis = weighting.BasisOf(&w)
		}
		return a, nil
	}
}

func (a *ciAggregator) reset() {
	a.n, a.mean, a.m2 = 0, 0, 0
	a.w = weighting.Welford{}
}

func (a *ciAggregator) Aggregate(records []*Record, field string) (float64, error) {
	a.reset()
	for _, r := range records {
		if err := a.UpdateRow(r, field); err != nil {
			return 0, err
		}
	}
	return a.bound2()
}

func (a *ciAggregator) UpdateRow(r *Record, field string) error {
	v, ok := r.NumericValue(field)
	if !ok {
		return nil
	}
	if a.weight == nil {
		a.foldOne(v)
		return nil
	}
	w, wok := r.NumericValue(a.weight.Field)
	if weighting.Classify(w, wok, a.weight.Kind) != weighting.Valid || w == 0 {
		return nil
	}
	a.w.Add(v, w)
	return nil
}

func (a *ciAggregator) foldOne(v float64) {
	a.n++
	delta := v - a.mean
	a.mean += delta / float64(a.n)
	a.m2 += delta * (v - a.mean)
}

func (a *ciAggregator) Finalize() (float64, error) {
	out, err := a.bound2()
	a.reset()
	return out, err
}

// moments returns the bound's inputs: the (weighted) mean, the sample
// variance (on w* when weighted) and the sample size it is divided by
// (n, or N*); ok=false when that size is at most 1 (no spread).
func (a *ciAggregator) moments() (mean, sampleVar, size float64, ok bool) {
	if a.weight == nil {
		if a.n < 2 {
			return 0, 0, 0, false
		}
		return a.mean, a.m2 / float64(a.n-1), float64(a.n), true
	}
	nStar := a.w.NStar(a.basis)
	if a.w.N == 0 || !(nStar > 1) {
		return 0, 0, 0, false
	}
	return a.w.Mean, a.w.Variance(a.basis), nStar, true
}

func (a *ciAggregator) bound2() (float64, error) {
	a.frozenAlpha = 1 - a.confidence
	a.frozenHasResult = true
	mean, sampleVar, size, ok := a.moments()
	if !ok {
		a.frozenMean, a.frozenStderr, a.frozenTCritical, a.frozenBound = 0, 0, 0, math.NaN()
		return math.NaN(), nil
	}
	if sampleVar < 0 {
		sampleVar = 0
	}
	stderr := math.Sqrt(sampleVar) / math.Sqrt(size)
	z := normalCriticalTwoSided(1 - a.confidence)
	half := z * stderr
	a.frozenMean = mean
	a.frozenStderr = stderr
	a.frozenTCritical = z
	var out float64
	if a.bound == ciLower {
		out = mean - half
	} else {
		out = mean + half
	}
	a.frozenBound = out
	return out, nil
}

func (a *ciAggregator) MergeOnline(other OnlineAggregator) error {
	b, ok := other.(*ciAggregator)
	if !ok {
		return mergeTypeMismatch("AGG_CI")
	}
	if a.weight != nil {
		mergeWelfordWeighted(&a.w.SumW, &a.w.Mean, &a.w.M2, b.w.SumW, b.w.Mean, b.w.M2)
		a.w.N += b.w.N
		a.w.SumWSq += b.w.SumWSq
		return nil
	}
	mergeWelford(&a.n, &a.mean, &a.m2, b.n, b.mean, b.m2)
	return nil
}

// Components returns {mean, stderr, alpha, t_critical, lower|upper} —
// the inputs to the CI bound calculation plus the resolved bound under
// the chosen key (lower for ciLower, upper for ciUpper).
//
// Reads frozenMean / frozenStderr / frozenAlpha / frozenTCritical /
// frozenBound captured inside bound2() — the shared helper Aggregate
// and Finalize both fall through to — so Components survives the
// streaming Finalize-reset on (n, mean, m2). NaN bound (n < 2) is
// preserved as NaN in the components map so consumers can detect the
// degenerate case without re-deriving from the floor; the JSON wire
// writes it (and the NaN scalar) as null (types.MarshalFinite).
//
// t_critical surfaces the normal quantile (z) actually used to scale
// the standard error (normalCriticalTwoSided, R's qnorm(1 − α/2)); the
// schema name preserves the analyst-facing vocabulary. Weighted, mean
// is the weighted mean and stderr √(s²/N*).
func (a *ciAggregator) Components() (map[string]any, error) {
	if !a.frozenHasResult {
		// No Aggregate / Finalize call yet — emit the configured alpha
		// so consumers can still inspect the request shape, but zero
		// the moments.
		alpha := 1 - a.confidence
		out := map[string]any{
			"mean":       0.0,
			"stderr":     0.0,
			"alpha":      alpha,
			"t_critical": 0.0,
		}
		if a.bound == ciLower {
			out["lower"] = 0.0
		} else {
			out["upper"] = 0.0
		}
		return out, nil
	}
	out := map[string]any{
		"mean":       a.frozenMean,
		"stderr":     a.frozenStderr,
		"alpha":      a.frozenAlpha,
		"t_critical": a.frozenTCritical,
	}
	if a.bound == ciLower {
		out["lower"] = a.frozenBound
	} else {
		out["upper"] = a.frozenBound
	}
	return out, nil
}

// Compile-time interface lock — catches MetaAggregator drift at build
// time for the ciAggregator that backs both AGG_CI_LOWER and
// AGG_CI_UPPER.
var _ MetaAggregator = (*ciAggregator)(nil)
