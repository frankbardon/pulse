package synth

import (
	"fmt"
	"math"
	"math/rand/v2"
	"regexp/syntax"
	"sort"
	"strings"
	"time"

	"github.com/frankbardon/pulse/errors"
)

// Distribution kind constants.
const (
	DistUniform             = "uniform"
	DistNormal              = "normal"
	DistLogNormal           = "lognormal"
	DistExponential         = "exponential"
	DistPoisson             = "poisson"
	DistPareto              = "pareto"
	DistBernoulli           = "bernoulli"
	DistMonotonicFrom       = "monotonic_from"
	DistWeightedCategorical = "weighted_categorical"
	DistUniformDate         = "uniform_date"
	DistRegex               = "regex"
	DistConstant            = "constant"
)

// AllDistributions returns the registered kind names in sorted order.
// Used by the manifest and tests.
func AllDistributions() []string {
	out := []string{
		DistBernoulli, DistConstant, DistExponential, DistLogNormal,
		DistMonotonicFrom, DistNormal, DistPareto, DistPoisson,
		DistRegex, DistUniform, DistUniformDate, DistWeightedCategorical,
	}
	sort.Strings(out)
	return out
}

// sampler emits the next raw sample for a field. The semantics of the
// returned value depend on the field type:
//
//   - numeric fields: float64 carrying the value (caller clamps/casts).
//   - categorical fields: string carrying the dictionary entry.
//   - date fields: float64 days-since-epoch (epoch = 1970-01-01).
//   - bernoulli/bool: float64 0 or 1.
//
// The boolean second return signals "this row is null" — only set for
// nullable fields with a non-zero null rate. The sampler still consumes
// RNG state so the seeded stream is unaffected by which rows are null.
type sampler interface {
	next(rng *rand.Rand) (any, bool)
}

// buildSampler constructs a sampler for a single FieldSpec. Returns
// PULSE_SYNTH_DISTRIBUTION_UNKNOWN if the distribution kind is not
// recognized, or SERVICE_VALIDATION if its parameters are malformed.
func buildSampler(f FieldSpec) (sampler, error) {
	base, err := buildBaseSampler(f)
	if err != nil {
		return nil, err
	}
	if f.NullRate <= 0 {
		return base, nil
	}
	return &nullableSampler{inner: base, rate: f.NullRate}, nil
}

func buildBaseSampler(f FieldSpec) (sampler, error) {
	switch f.Distribution {
	case DistUniform:
		return newUniformSampler(f)
	case DistNormal:
		return newNormalSampler(f)
	case DistLogNormal:
		return newLogNormalSampler(f)
	case DistExponential:
		return newExponentialSampler(f)
	case DistPoisson:
		return newPoissonSampler(f)
	case DistPareto:
		return newParetoSampler(f)
	case DistBernoulli:
		return newBernoulliSampler(f)
	case DistMonotonicFrom:
		return newMonotonicSampler(f)
	case DistWeightedCategorical:
		return newWeightedCategoricalSampler(f)
	case DistUniformDate:
		return newUniformDateSampler(f)
	case DistRegex:
		return newRegexSampler(f)
	case DistConstant:
		return newConstantSampler(f)
	default:
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_SYNTH_DISTRIBUTION_UNKNOWN,
			fmt.Sprintf("unknown distribution %q for field %q", f.Distribution, f.Name),
			map[string]any{"distribution": f.Distribution, "field": f.Name})
	}
}

type nullableSampler struct {
	inner sampler
	rate  float64
}

func (n *nullableSampler) next(rng *rand.Rand) (any, bool) {
	// Always draw the inner value first so that the seeded stream is
	// independent of the null mask. Then decide null with a separate draw.
	v, _ := n.inner.next(rng)
	if rng.Float64() < n.rate {
		return v, true
	}
	return v, false
}

// --- numeric distributions ---

type uniformSampler struct{ min, max float64 }

func newUniformSampler(f FieldSpec) (sampler, error) {
	min, _, err := paramFloat(f.Name, f.Params, "min", 0)
	if err != nil {
		return nil, err
	}
	max, _, err := paramFloat(f.Name, f.Params, "max", 1)
	if err != nil {
		return nil, err
	}
	if max <= min {
		return nil, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			fmt.Sprintf("field %q: uniform max must be > min", f.Name),
			map[string]any{"min": min, "max": max})
	}
	return &uniformSampler{min: min, max: max}, nil
}

func (u *uniformSampler) next(rng *rand.Rand) (any, bool) {
	return u.min + rng.Float64()*(u.max-u.min), false
}

type normalSampler struct {
	mean, std float64
	min, max  float64
	clamped   bool
}

func newNormalSampler(f FieldSpec) (sampler, error) {
	mean, _, err := paramFloat(f.Name, f.Params, "mean", 0)
	if err != nil {
		return nil, err
	}
	std, _, err := paramFloat(f.Name, f.Params, "std", 1)
	if err != nil {
		return nil, err
	}
	if std <= 0 {
		return nil, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			fmt.Sprintf("field %q: normal std must be > 0", f.Name),
			map[string]any{"std": std})
	}
	min, hasMin, err := paramFloat(f.Name, f.Params, "min", math.Inf(-1))
	if err != nil {
		return nil, err
	}
	max, hasMax, err := paramFloat(f.Name, f.Params, "max", math.Inf(1))
	if err != nil {
		return nil, err
	}
	return &normalSampler{
		mean: mean, std: std,
		min: min, max: max,
		clamped: hasMin || hasMax,
	}, nil
}

func (n *normalSampler) next(rng *rand.Rand) (any, bool) {
	v := n.mean + rng.NormFloat64()*n.std
	if n.clamped {
		if v < n.min {
			v = n.min
		}
		if v > n.max {
			v = n.max
		}
	}
	return v, false
}

type logNormalSampler struct {
	mu, sigma float64
}

func newLogNormalSampler(f FieldSpec) (sampler, error) {
	mu, _, err := paramFloat(f.Name, f.Params, "mu", 0)
	if err != nil {
		return nil, err
	}
	sigma, _, err := paramFloat(f.Name, f.Params, "sigma", 1)
	if err != nil {
		return nil, err
	}
	if sigma <= 0 {
		return nil, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			fmt.Sprintf("field %q: lognormal sigma must be > 0", f.Name), nil)
	}
	return &logNormalSampler{mu: mu, sigma: sigma}, nil
}

func (l *logNormalSampler) next(rng *rand.Rand) (any, bool) {
	return math.Exp(l.mu + rng.NormFloat64()*l.sigma), false
}

type exponentialSampler struct{ lambda float64 }

func newExponentialSampler(f FieldSpec) (sampler, error) {
	lambda, _, err := paramFloat(f.Name, f.Params, "lambda", 1)
	if err != nil {
		return nil, err
	}
	if lambda <= 0 {
		return nil, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			fmt.Sprintf("field %q: exponential lambda must be > 0", f.Name), nil)
	}
	return &exponentialSampler{lambda: lambda}, nil
}

func (e *exponentialSampler) next(rng *rand.Rand) (any, bool) {
	return rng.ExpFloat64() / e.lambda, false
}

type poissonSampler struct{ lambda float64 }

func newPoissonSampler(f FieldSpec) (sampler, error) {
	lambda, _, err := paramFloat(f.Name, f.Params, "lambda", 1)
	if err != nil {
		return nil, err
	}
	if lambda <= 0 {
		return nil, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			fmt.Sprintf("field %q: poisson lambda must be > 0", f.Name), nil)
	}
	return &poissonSampler{lambda: lambda}, nil
}

// next implements Knuth's multiplicative algorithm for small lambda and
// Atkinson's transform for large lambda. We keep the small-lambda branch
// here; for our use cases (synthetic data, lambda typically < 100) it
// is plenty fast.
func (p *poissonSampler) next(rng *rand.Rand) (any, bool) {
	if p.lambda < 30 {
		L := math.Exp(-p.lambda)
		k := 0.0
		prod := 1.0
		for {
			k++
			prod *= rng.Float64()
			if prod <= L {
				return k - 1, false
			}
		}
	}
	// Normal approximation for large lambda. Mean = lambda, variance =
	// lambda. Round to nearest non-negative integer.
	v := p.lambda + rng.NormFloat64()*math.Sqrt(p.lambda)
	if v < 0 {
		v = 0
	}
	return math.Floor(v + 0.5), false
}

type paretoSampler struct {
	xm, alpha float64
}

func newParetoSampler(f FieldSpec) (sampler, error) {
	xm, _, err := paramFloat(f.Name, f.Params, "xm", 1)
	if err != nil {
		return nil, err
	}
	alpha, _, err := paramFloat(f.Name, f.Params, "alpha", 1.5)
	if err != nil {
		return nil, err
	}
	if xm <= 0 || alpha <= 0 {
		return nil, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			fmt.Sprintf("field %q: pareto xm and alpha must be > 0", f.Name), nil)
	}
	return &paretoSampler{xm: xm, alpha: alpha}, nil
}

func (p *paretoSampler) next(rng *rand.Rand) (any, bool) {
	u := 1 - rng.Float64()
	return p.xm / math.Pow(u, 1.0/p.alpha), false
}

type bernoulliSampler struct{ p float64 }

func newBernoulliSampler(f FieldSpec) (sampler, error) {
	p, _, err := paramFloat(f.Name, f.Params, "p", 0.5)
	if err != nil {
		return nil, err
	}
	if p < 0 || p > 1 {
		return nil, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			fmt.Sprintf("field %q: bernoulli p must be in [0, 1]", f.Name), nil)
	}
	return &bernoulliSampler{p: p}, nil
}

func (b *bernoulliSampler) next(rng *rand.Rand) (any, bool) {
	if rng.Float64() < b.p {
		return 1.0, false
	}
	return 0.0, false
}

type monotonicSampler struct {
	cur  int64
	step int64
}

func newMonotonicSampler(f FieldSpec) (sampler, error) {
	start, _, err := paramInt(f.Name, f.Params, "start", 0)
	if err != nil {
		return nil, err
	}
	step, _, err := paramInt(f.Name, f.Params, "step", 1)
	if err != nil {
		return nil, err
	}
	if step == 0 {
		return nil, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			fmt.Sprintf("field %q: monotonic_from step must be non-zero", f.Name), nil)
	}
	return &monotonicSampler{cur: start - step, step: step}, nil
}

func (m *monotonicSampler) next(_ *rand.Rand) (any, bool) {
	m.cur += m.step
	return float64(m.cur), false
}

type constantSampler struct{ val any }

func newConstantSampler(f FieldSpec) (sampler, error) {
	v, ok := f.Params["value"]
	if !ok {
		return nil, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			fmt.Sprintf("field %q: constant requires param value", f.Name), nil)
	}
	return &constantSampler{val: v}, nil
}

func (c *constantSampler) next(_ *rand.Rand) (any, bool) {
	return c.val, false
}

// --- categorical / date / regex distributions ---

type weightedCategoricalSampler struct {
	values []string
	cum    []float64
	total  float64
}

func newWeightedCategoricalSampler(f FieldSpec) (sampler, error) {
	values, ok, err := paramStringSlice(f.Name, f.Params, "values")
	if err != nil {
		return nil, err
	}
	if !ok || len(values) == 0 {
		return nil, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			fmt.Sprintf("field %q: weighted_categorical requires non-empty values", f.Name), nil)
	}
	weights, hasW, err := paramFloatSlice(f.Name, f.Params, "weights")
	if err != nil {
		return nil, err
	}
	if !hasW {
		weights = make([]float64, len(values))
		for i := range weights {
			weights[i] = 1
		}
	}
	if len(weights) != len(values) {
		return nil, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			fmt.Sprintf("field %q: weights length must match values", f.Name),
			map[string]any{"values": len(values), "weights": len(weights)})
	}
	cum := make([]float64, len(weights))
	total := 0.0
	for i, w := range weights {
		if w < 0 {
			return nil, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
				fmt.Sprintf("field %q: negative weight", f.Name),
				map[string]any{"index": i, "value": w})
		}
		total += w
		cum[i] = total
	}
	if total <= 0 {
		return nil, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			fmt.Sprintf("field %q: weights sum must be > 0", f.Name), nil)
	}
	return &weightedCategoricalSampler{values: values, cum: cum, total: total}, nil
}

func (w *weightedCategoricalSampler) next(rng *rand.Rand) (any, bool) {
	x := rng.Float64() * w.total
	idx := sort.SearchFloat64s(w.cum, x)
	if idx >= len(w.values) {
		idx = len(w.values) - 1
	}
	return w.values[idx], false
}

// EpochDate is the epoch used by the .pulse Date type. Days are stored
// as a uint32 days-since-epoch counter.
const dateEpoch = "1970-01-01"

type uniformDateSampler struct {
	startDays, endDays int64
}

func newUniformDateSampler(f FieldSpec) (sampler, error) {
	startStr, ok, err := paramString(f.Name, f.Params, "start", dateEpoch)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			fmt.Sprintf("field %q: uniform_date requires start", f.Name), nil)
	}
	endStr, ok, err := paramString(f.Name, f.Params, "end", "")
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			fmt.Sprintf("field %q: uniform_date requires end", f.Name), nil)
	}
	startT, err := time.Parse("2006-01-02", startStr)
	if err != nil {
		return nil, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			fmt.Sprintf("field %q: invalid start date %q", f.Name, startStr), nil)
	}
	endT, err := time.Parse("2006-01-02", endStr)
	if err != nil {
		return nil, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			fmt.Sprintf("field %q: invalid end date %q", f.Name, endStr), nil)
	}
	if !endT.After(startT) {
		return nil, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			fmt.Sprintf("field %q: uniform_date end must be after start", f.Name), nil)
	}
	const day = int64(86400)
	return &uniformDateSampler{
		startDays: startT.Unix() / day,
		endDays:   endT.Unix() / day,
	}, nil
}

func (u *uniformDateSampler) next(rng *rand.Rand) (any, bool) {
	span := u.endDays - u.startDays
	off := rng.Int64N(span + 1)
	return float64(u.startDays + off), false
}

// regexSampler uses regexp/syntax to walk a parsed regex AST and emit a
// random matching string. The language is restricted to the practical
// shapes we need for synthetic IDs: literals, character classes, fixed
// repetitions, alternation, and bounded `+`, `*`, `{m,n}` repetitions.
type regexSampler struct {
	tree     *syntax.Regexp
	maxRepeat int
}

func newRegexSampler(f FieldSpec) (sampler, error) {
	pattern, ok, err := paramString(f.Name, f.Params, "pattern", "")
	if err != nil {
		return nil, err
	}
	if !ok || pattern == "" {
		return nil, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			fmt.Sprintf("field %q: regex distribution requires pattern", f.Name), nil)
	}
	maxRepeatI, _, err := paramInt(f.Name, f.Params, "max_repeat", 8)
	if err != nil {
		return nil, err
	}
	if maxRepeatI < 1 {
		maxRepeatI = 1
	}
	tree, err := syntax.Parse(pattern, syntax.Perl)
	if err != nil {
		return nil, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			fmt.Sprintf("field %q: invalid regex %q: %v", f.Name, pattern, err), nil)
	}
	return &regexSampler{tree: tree.Simplify(), maxRepeat: int(maxRepeatI)}, nil
}

func (r *regexSampler) next(rng *rand.Rand) (any, bool) {
	var sb strings.Builder
	r.emit(rng, r.tree, &sb)
	return sb.String(), false
}

// emit walks a parsed regex node and writes matching characters.
func (r *regexSampler) emit(rng *rand.Rand, n *syntax.Regexp, sb *strings.Builder) {
	switch n.Op {
	case syntax.OpEmptyMatch, syntax.OpNoMatch, syntax.OpBeginLine,
		syntax.OpEndLine, syntax.OpBeginText, syntax.OpEndText,
		syntax.OpWordBoundary, syntax.OpNoWordBoundary:
		// Anchor / empty: nothing emitted.
	case syntax.OpLiteral:
		for _, rn := range n.Rune {
			sb.WriteRune(rn)
		}
	case syntax.OpCharClass:
		// Sum the size of each [lo,hi] pair, then pick uniformly.
		total := 0
		for i := 0; i < len(n.Rune); i += 2 {
			total += int(n.Rune[i+1]-n.Rune[i]) + 1
		}
		if total == 0 {
			return
		}
		idx := rng.IntN(total)
		for i := 0; i < len(n.Rune); i += 2 {
			width := int(n.Rune[i+1]-n.Rune[i]) + 1
			if idx < width {
				sb.WriteRune(n.Rune[i] + rune(idx))
				return
			}
			idx -= width
		}
	case syntax.OpAnyCharNotNL, syntax.OpAnyChar:
		// Emit a printable ASCII character to keep output stable.
		sb.WriteRune(rune(33 + rng.IntN(94)))
	case syntax.OpCapture:
		for _, sub := range n.Sub {
			r.emit(rng, sub, sb)
		}
	case syntax.OpConcat:
		for _, sub := range n.Sub {
			r.emit(rng, sub, sb)
		}
	case syntax.OpAlternate:
		idx := rng.IntN(len(n.Sub))
		r.emit(rng, n.Sub[idx], sb)
	case syntax.OpStar:
		count := rng.IntN(r.maxRepeat + 1)
		for i := 0; i < count; i++ {
			r.emit(rng, n.Sub[0], sb)
		}
	case syntax.OpPlus:
		count := 1 + rng.IntN(r.maxRepeat)
		for i := 0; i < count; i++ {
			r.emit(rng, n.Sub[0], sb)
		}
	case syntax.OpQuest:
		if rng.IntN(2) == 1 {
			r.emit(rng, n.Sub[0], sb)
		}
	case syntax.OpRepeat:
		min := n.Min
		max := n.Max
		if max < 0 || max > min+r.maxRepeat {
			max = min + r.maxRepeat
		}
		count := min
		if max > min {
			count = min + rng.IntN(max-min+1)
		}
		for i := 0; i < count; i++ {
			r.emit(rng, n.Sub[0], sb)
		}
	}
}
