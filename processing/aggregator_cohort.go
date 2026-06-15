package processing

import (
	"encoding/json"
	"math"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Cohort-analytics aggregators — AGG_WEIGHTED_MEAN, AGG_RATIO,
// AGG_CI_LOWER, AGG_CI_UPPER. These give the orchestrator first-class
// support for stats Prism today computes client-side in compile/inmem/
// after a generic Pulse fold. Each implementation cleanly fits the
// existing Aggregator + OnlineAggregator + MergeableAggregator surface
// — no orchestrator changes required.
//
// Two more cohort aggregators (AGG_LIFT, AGG_SHARE) need access to the
// full record set + per-group buckets at the same time. They will land
// in a follow-up once a `CohortAwareAggregator` interface or a global
// pre-pass hook exists.

// --- AGG_WEIGHTED_MEAN ---------------------------------------------

type weightedMeanParams struct {
	WeightField string `json:"weight_field"`
}

// weightedMeanAggregator accumulates a weighted mean via the
// Chan-Welford streaming recurrence:
//
//	delta   = x − mean
//	wSum   += w
//	mean   += (w / wSum) * delta
//	m2     += w * delta * (x − mean)   // weighted moment (informative
//	                                   // only; weighted mean does not
//	                                   // expose variance to callers).
//
// MergeOnline combines two weighted partials via the same parallel
// formula used by varianceAggregator, weighted by wSum instead of n.
type weightedMeanAggregator struct {
	weightField string
	wSum        float64
	mean        float64
	m2          float64
}

func newWeightedMeanAggregator(agg *types.Aggregation, _ *encoding.Schema) (Aggregator, error) {
	var params weightedMeanParams
	if len(agg.Params) > 0 {
		if err := json.Unmarshal(agg.Params, &params); err != nil {
			return nil, errors.NewCodedError(errors.PROCESSING_CONFIG, "invalid weighted_mean params: "+err.Error())
		}
	}
	if params.WeightField == "" {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"AGG_WEIGHTED_MEAN requires Params.weight_field")
	}
	return &weightedMeanAggregator{weightField: params.WeightField}, nil
}

func (a *weightedMeanAggregator) Aggregate(records []*Record, field string) (float64, error) {
	a.wSum, a.mean, a.m2 = 0, 0, 0
	for _, r := range records {
		v, ok := r.NumericValue(field)
		if !ok {
			continue
		}
		w, ok := r.NumericValue(a.weightField)
		if !ok || w == 0 {
			continue
		}
		a.foldOne(v, w)
	}
	if a.wSum == 0 {
		return 0, nil
	}
	return a.mean, nil
}

func (a *weightedMeanAggregator) UpdateRow(r *Record, field string) error {
	v, ok := r.NumericValue(field)
	if !ok {
		return nil
	}
	w, ok := r.NumericValue(a.weightField)
	if !ok || w == 0 {
		return nil
	}
	a.foldOne(v, w)
	return nil
}

func (a *weightedMeanAggregator) foldOne(x, w float64) {
	a.wSum += w
	delta := x - a.mean
	a.mean += (w / a.wSum) * delta
	a.m2 += w * delta * (x - a.mean)
}

func (a *weightedMeanAggregator) Finalize() (float64, error) {
	if a.wSum == 0 {
		out := 0.0
		a.mean, a.wSum, a.m2 = 0, 0, 0
		return out, nil
	}
	out := a.mean
	a.mean, a.wSum, a.m2 = 0, 0, 0
	return out, nil
}

func (a *weightedMeanAggregator) MergeOnline(other OnlineAggregator) error {
	b, ok := other.(*weightedMeanAggregator)
	if !ok {
		return mergeTypeMismatch("AGG_WEIGHTED_MEAN")
	}
	if b.wSum == 0 {
		return nil
	}
	if a.wSum == 0 {
		a.wSum, a.mean, a.m2 = b.wSum, b.mean, b.m2
		return nil
	}
	total := a.wSum + b.wSum
	delta := b.mean - a.mean
	newMean := a.mean + delta*b.wSum/total
	newM2 := a.m2 + b.m2 + delta*delta*a.wSum*b.wSum/total
	a.wSum = total
	a.mean = newMean
	a.m2 = newM2
	return nil
}

// --- AGG_RATIO ------------------------------------------------------

type ratioParams struct {
	NumeratorField   string `json:"numerator_field"`
	DenominatorField string `json:"denominator_field"`
}

// ratioAggregator emits sum(num) / sum(den) at finalize. Both sums are
// independent + associative, so MergeOnline is trivial. Null on either
// field skips that row's contribution to BOTH sums to keep the ratio
// honest. Denominator-zero at finalize emits NaN (consistent with
// IEEE-754 0/0); callers can detect via math.IsNaN.
type ratioAggregator struct {
	numField, denField string
	num, den           float64
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
	return &ratioAggregator{numField: params.NumeratorField, denField: params.DenominatorField}, nil
}

func (a *ratioAggregator) Aggregate(records []*Record, field string) (float64, error) {
	a.num, a.den = 0, 0
	for _, r := range records {
		n, okN := r.NumericValue(a.numField)
		if !okN {
			continue
		}
		d, okD := r.NumericValue(a.denField)
		if !okD {
			continue
		}
		a.num += n
		a.den += d
	}
	return a.divide()
}

func (a *ratioAggregator) UpdateRow(r *Record, field string) error {
	n, okN := r.NumericValue(a.numField)
	if !okN {
		return nil
	}
	d, okD := r.NumericValue(a.denField)
	if !okD {
		return nil
	}
	a.num += n
	a.den += d
	return nil
}

func (a *ratioAggregator) Finalize() (float64, error) {
	out, err := a.divide()
	a.num, a.den = 0, 0
	return out, err
}

func (a *ratioAggregator) divide() (float64, error) {
	if a.den == 0 {
		return math.NaN(), nil
	}
	return a.num / a.den, nil
}

func (a *ratioAggregator) MergeOnline(other OnlineAggregator) error {
	b, ok := other.(*ratioAggregator)
	if !ok {
		return mergeTypeMismatch("AGG_RATIO")
	}
	a.num += b.num
	a.den += b.den
	return nil
}

// --- AGG_CI_LOWER / AGG_CI_UPPER (normal method) --------------------

type ciParams struct {
	Confidence float64 `json:"confidence"`
	Method     string  `json:"method"`
}

// ciAggregator accumulates Welford (n, mean, M2) and emits the
// requested CI bound at finalize. Sample variance ((M2/(n-1))^0.5 /
// sqrt(n)) is the standard error; multiplied by z gives the half-width.
// Mergeable via the same Chan-Welford reduction as the variance
// aggregator.
//
// frozen{Mean,Stderr,Alpha,TCritical,Bound} mirror the post-finalize
// CI state so Components() can emit
// {mean, stderr, alpha, t_critical, lower|upper} after the Finalize-
// reset wipes the live (n, mean, m2). Aggregate (buffered path) and
// Finalize (streaming path) both stamp the same mirrors via the shared
// bound2 helper.
type ciAggregator struct {
	bound      ciBound
	confidence float64
	method     string
	n          int64
	mean       float64
	m2         float64

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
		return &ciAggregator{bound: bound, confidence: params.Confidence, method: params.Method}, nil
	}
}

func (a *ciAggregator) Aggregate(records []*Record, field string) (float64, error) {
	a.n, a.mean, a.m2 = 0, 0, 0
	for _, r := range records {
		v, ok := r.NumericValue(field)
		if !ok {
			continue
		}
		a.foldOne(v)
	}
	return a.bound2()
}

func (a *ciAggregator) UpdateRow(r *Record, field string) error {
	v, ok := r.NumericValue(field)
	if !ok {
		return nil
	}
	a.foldOne(v)
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
	a.n, a.mean, a.m2 = 0, 0, 0
	return out, err
}

func (a *ciAggregator) bound2() (float64, error) {
	a.frozenAlpha = 1 - a.confidence
	a.frozenHasResult = true
	if a.n < 2 {
		a.frozenMean, a.frozenStderr, a.frozenTCritical, a.frozenBound = 0, 0, 0, math.NaN()
		return math.NaN(), nil
	}
	sampleVar := a.m2 / float64(a.n-1)
	if sampleVar < 0 {
		sampleVar = 0
	}
	stderr := math.Sqrt(sampleVar) / math.Sqrt(float64(a.n))
	z := normalQuantile((1 + a.confidence) / 2)
	half := z * stderr
	a.frozenMean = a.mean
	a.frozenStderr = stderr
	a.frozenTCritical = z
	var out float64
	if a.bound == ciLower {
		out = a.mean - half
	} else {
		out = a.mean + half
	}
	a.frozenBound = out
	return out, nil
}

func (a *ciAggregator) MergeOnline(other OnlineAggregator) error {
	b, ok := other.(*ciAggregator)
	if !ok {
		return mergeTypeMismatch("AGG_CI")
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
// degenerate case without re-deriving from the floor.
//
// t_critical surfaces the normal quantile (z) actually used to scale
// the standard error; the schema name preserves the analyst-facing
// vocabulary while the value reflects the implementation's
// Beasley-Springer-Moro inverse-normal approximation.
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

// normalQuantile returns the inverse CDF of the standard normal at p
// via the Beasley-Springer-Moro approximation. Accurate to ~1e-9 in
// the central range and ~1e-7 in the tails — enough for CI bound math.
// Source: Moro, B. "The full Monte." Risk 8(2), 1995.
func normalQuantile(p float64) float64 {
	if p <= 0 {
		return math.Inf(-1)
	}
	if p >= 1 {
		return math.Inf(1)
	}
	// Coefficients.
	a := [4]float64{
		2.50662823884, -18.61500062529, 41.39119773534, -25.44106049637,
	}
	b := [4]float64{
		-8.47351093090, 23.08336743743, -21.06224101826, 3.13082909833,
	}
	c := [9]float64{
		0.3374754822726147,
		0.9761690190917186,
		0.1607979714918209,
		0.0276438810333863,
		0.0038405729373609,
		0.0003951896511919,
		0.0000321767881768,
		0.0000002888167364,
		0.0000003960315187,
	}

	u := p - 0.5
	if math.Abs(u) < 0.42 {
		r := u * u
		num := ((a[3]*r+a[2])*r+a[1])*r + a[0]
		den := (((b[3]*r+b[2])*r+b[1])*r+b[0])*r + 1
		return u * num / den
	}
	r := p
	if u > 0 {
		r = 1 - p
	}
	r = math.Log(-math.Log(r))
	x := c[0] + r*(c[1]+r*(c[2]+r*(c[3]+r*(c[4]+r*(c[5]+r*(c[6]+r*(c[7]+r*c[8])))))))
	if u < 0 {
		x = -x
	}
	return x
}
