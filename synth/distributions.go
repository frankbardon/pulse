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
	DistMixture             = "mixture"
	DistSetBernoulli        = "set_bernoulli"
)

// AllDistributions returns the registered kind names in sorted order.
// Used by the manifest and tests.
func AllDistributions() []string {
	out := []string{
		DistBernoulli, DistConstant, DistExponential, DistLogNormal,
		DistMixture, DistMonotonicFrom, DistNormal, DistPareto, DistPoisson,
		DistRegex, DistSetBernoulli, DistUniform, DistUniformDate, DistWeightedCategorical,
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
//   - set_* fields: map[string]bool keyed by every declared dictionary
//     option, true when that option's bit is selected — see
//     setSampler / writeFieldValueForField's set case.
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
	case DistMixture:
		return newMixtureSampler(f)
	case DistSetBernoulli:
		return newSetSampler(f)
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
	return u.min + float64(rng.Float64()*(u.max-u.min)), false
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
	v := n.mean + float64(rng.NormFloat64()*n.std)
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
	return math.Exp(l.mu + float64(rng.NormFloat64()*l.sigma)), false
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
	v := p.lambda + float64(rng.NormFloat64()*math.Sqrt(p.lambda))
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
	// float64(...) around an already-float64 value is NOT redundant: it
	// is the FMA barrier (synth/moments.go). rng.Float64() inlines to a
	// scaled integer, and without the conversion `1 - scale*n` contracts
	// into a single fused multiply-subtract on arm64 but not on amd64.
	u := 1 - float64(rng.Float64())
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

// newConstantSampler is the one sampler whose row value comes straight
// from the spec document rather than from an arithmetic draw, so it is
// also the one that can put the WRONG GO TYPE into the row. Every other
// sampler for a given field type emits one shape — float64 for every
// scalar (including packed_bool, which bernoulliSampler emits as 1.0 /
// 0.0), string for a categorical, map[string]bool for a set — and
// sentinelFor types the expression environment on exactly that
// assumption. A JSON `true` on a packed_bool field therefore put a Go
// bool where the env promised a float64, and any constraint (and, from
// the rule layer on, any `when` or `set_expr`) touching that field
// failed at RUN time with the inverse of issue #258's type mismatch.
//
// The value is normalised to the field's row shape HERE, once, at spec
// compile time, rather than being coerced at every read: the row map has
// no type discipline of its own, so the only place the invariant can
// hold is where the value enters it.
//
// Byte-identity is preserved for every spec that worked before. A bool
// on a numeric target already encoded as 1/0 (toFloat64/toBool both map
// it), and a JSON number is already float64. What changes is the shapes
// that were BROKEN: a string on a non-decimal scalar encoded as a silent
// 0 (toFloat64's string arm) and is now refused; a non-string on a
// categorical raised ENCODING_TYPE_MISMATCH at row time and is now
// refused at parse; an array on a set_* raised the same at row time and
// now works, as the option-name list an author would write.
func newConstantSampler(f FieldSpec) (sampler, error) {
	v, ok := f.Params["value"]
	if !ok {
		return nil, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			fmt.Sprintf("field %q: constant requires param value", f.Name), nil)
	}
	norm, err := constantRowValue(f, v)
	if err != nil {
		return nil, err
	}
	return &constantSampler{val: norm}, nil
}

// literalFloat reads a JSON-decoded numeric literal. json.Unmarshal into
// an `any` produces float64, but a programmatically-built Spec may carry
// a Go int, so both are accepted.
func literalFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case uint64:
		return float64(x), true
	}
	return 0, false
}

// constantRowValue coerces a declared `constant` value to the Go type the
// row holds for f's field type — the three classes sentinelFor derives.
// A field whose declared type is not a name fieldTypeFromName knows is
// passed through untouched: buildSchema refuses that field on its own
// terms and a coercion error here would name the wrong cause.
func constantRowValue(f FieldSpec, v any) (any, error) {
	ft, ok := fieldTypeFromName(f.Type)
	if !ok {
		return v, nil
	}
	bad := func(want string) error {
		return errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			fmt.Sprintf("field %q: constant value for %s must be %s, got %T", f.Name, f.Type, want, v),
			map[string]any{"field": f.Name, "type": f.Type})
	}
	switch {
	case ft.IsCategorical():
		if _, isStr := v.(string); !isStr {
			return nil, bad("a string")
		}
		return v, nil
	case ft.IsSet():
		// map[string]bool is what a programmatically-built Spec may
		// already carry; from JSON the natural form is the list of
		// selected option names, which is also what params.options
		// declares.
		if m, isMap := v.(map[string]bool); isMap {
			return m, nil
		}
		// map[string]any is the SAME value after a JSON round trip —
		// json.Marshal turns map[string]bool into an object and
		// json.Unmarshal into `any` hands it back generically. This
		// arm is what makes `--emit-spec` honest for an all-null
		// set_* column: SpecFromProfile's unsummarisable fallback
		// (sentinelFor) puts an empty map[string]bool here, so
		// without it the emitted spec parses and then refuses at
		// generation — the one shape in the whole Spec that does not
		// decode back to the Go type it was marshalled from.
		if m, isMap := v.(map[string]any); isMap {
			sel := make(map[string]bool, len(m))
			for k, e := range m {
				b, isBool := e.(bool)
				if !isBool {
					return nil, bad("an object of option -> bool, or an array of option strings")
				}
				if b {
					sel[k] = true
				}
			}
			return sel, nil
		}
		arr, isArr := v.([]any)
		if !isArr {
			return nil, bad("an array of option strings")
		}
		sel := make(map[string]bool, len(arr))
		for _, e := range arr {
			s, isStr := e.(string)
			if !isStr {
				return nil, bad("an array of option strings")
			}
			sel[s] = true
		}
		return sel, nil
	default:
		// Scalar class. A bool means 1/0, the same reduction every other
		// boolean path applies.
		if b, isBool := v.(bool); isBool {
			if b {
				return float64(1), nil
			}
			return float64(0), nil
		}
		if n, isNum := literalFloat(v); isNum {
			return n, nil
		}
		// decimal128 is the one scalar whose string form is exact
		// (encoding.ParseDecimal128, no float round trip), so it is kept
		// verbatim for writeFieldValueForField's own string arm.
		if _, isStr := v.(string); isStr && ft.IsDecimal() {
			return v, nil
		}
		return nil, bad("a number or a bool")
	}
}

func (c *constantSampler) next(_ *rand.Rand) (any, bool) {
	return c.val, false
}

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

// mixtureSampler draws from a mixture of Gaussian components: pick a
// component index by weight (identical cumulative-weight scheme to
// weightedCategoricalSampler), then draw N(means[i], stds[i]^2). This is
// the shape-fitting technique chosen for E4-S1 over KDE because its
// per-component parameters (mean, std, weight) are the same closed-form
// shape SpecFromProfile already reconstructs for a single normal — a
// captured bimodal/skewed profile can hand this sampler two or more
// component summaries directly, with no kernel or bandwidth machinery.
// Component count is a schema-mode declaration in v1 (len(means)); an
// automatic count-selection heuristic (e.g. BIC-driven EM) is deferred to
// the profile-capture story that fits mixtures from real data.
type mixtureSampler struct {
	means, stds []float64
	cum         []float64
	total       float64
}

// mixtureComponents is one DistMixture field's parsed, validated
// parameter set. Weights are carried EXACTLY as declared (never
// pre-normalised) alongside their running total, because
// newMixtureSampler's component pick compares a scaled uniform draw
// against a cumulative-weight table built by accumulating those same
// raw numbers: dividing each weight by the total first would produce a
// mathematically identical table and a floating-point different one,
// which at a knife-edge draw is a different component, a different
// value, and a different byte in a file this package promises is
// reproducible. The copula-side consumers (fieldMoments / quantileFor,
// synth/copula.go) do their own normalisation because a CDF genuinely
// needs weights summing to one; the sampler must not.
type mixtureComponents struct {
	means, stds, weights []float64
	weightTotal          float64
}

// parseMixtureComponents validates a DistMixture FieldSpec's params in
// the exact order newMixtureSampler has always validated them, so the
// same malformed spec still fails on the same clause with the same
// message (see TestSynth_MixtureValidatesParams). It exists because
// three call sites now need the same numbers: the independent sampler
// below, and — since a shape-fitted field can carry a linear model
// (E4-S1) — the mixture's analytic moments and its numerically
// inverted quantile function in synth/copula.go.
func parseMixtureComponents(f FieldSpec) (mixtureComponents, error) {
	var mc mixtureComponents
	means, ok, err := paramFloatSlice(f.Name, f.Params, "means")
	if err != nil {
		return mc, err
	}
	if !ok || len(means) < 2 {
		return mc, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			fmt.Sprintf("field %q: mixture requires at least 2 means", f.Name), nil)
	}
	stds, ok, err := paramFloatSlice(f.Name, f.Params, "stds")
	if err != nil {
		return mc, err
	}
	if !ok || len(stds) != len(means) {
		return mc, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			fmt.Sprintf("field %q: mixture stds length must match means", f.Name),
			map[string]any{"means": len(means), "stds": len(stds)})
	}
	for i, s := range stds {
		if s <= 0 {
			return mc, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
				fmt.Sprintf("field %q: mixture std must be > 0", f.Name),
				map[string]any{"index": i, "value": s})
		}
	}
	weights, hasW, err := paramFloatSlice(f.Name, f.Params, "weights")
	if err != nil {
		return mc, err
	}
	if !hasW {
		weights = make([]float64, len(means))
		for i := range weights {
			weights[i] = 1
		}
	}
	if len(weights) != len(means) {
		return mc, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			fmt.Sprintf("field %q: mixture weights length must match means", f.Name),
			map[string]any{"means": len(means), "weights": len(weights)})
	}
	total := 0.0
	for i, w := range weights {
		if w < 0 {
			return mc, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
				fmt.Sprintf("field %q: negative mixture weight", f.Name),
				map[string]any{"index": i, "value": w})
		}
		total += w
	}
	if total <= 0 {
		return mc, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			fmt.Sprintf("field %q: mixture weights sum must be > 0", f.Name), nil)
	}
	return mixtureComponents{means: means, stds: stds, weights: weights, weightTotal: total}, nil
}

func newMixtureSampler(f FieldSpec) (sampler, error) {
	mc, err := parseMixtureComponents(f)
	if err != nil {
		return nil, err
	}
	// The cumulative table is accumulated here, from the raw weights, in
	// declaration order — the identical arithmetic this constructor has
	// always performed. See mixtureComponents on why the weights are not
	// normalised on the way in.
	cum := make([]float64, len(mc.weights))
	total := 0.0
	for i, w := range mc.weights {
		total += w
		cum[i] = total
	}
	return &mixtureSampler{means: mc.means, stds: mc.stds, cum: cum, total: total}, nil
}

func (m *mixtureSampler) next(rng *rand.Rand) (any, bool) {
	x := rng.Float64() * m.total
	idx := sort.SearchFloat64s(m.cum, x)
	if idx >= len(m.means) {
		idx = len(m.means) - 1
	}
	return m.means[idx] + float64(rng.NormFloat64()*m.stds[idx]), false
}

// setSampler draws a set_* field's own independent marginal: one
// Bernoulli(freqs[i]) draw per declared option, in FIXED declaration
// order (never Go map iteration order — see buildSchema's dictionary
// pre-registration, which relies on this same fixed order for
// deterministic bit assignment). Returns a map[string]bool covering
// EVERY declared option so a later set-option conditional transform
// (synth/conditional_sample.go) can flip one option's state in place
// without needing to know the others.
type setSampler struct {
	options []string
	freqs   []float64
}

func newSetSampler(f FieldSpec) (sampler, error) {
	options, ok, err := paramStringSlice(f.Name, f.Params, "options")
	if err != nil {
		return nil, err
	}
	if !ok || len(options) == 0 {
		return nil, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			fmt.Sprintf("field %q: set_bernoulli requires non-empty options", f.Name), nil)
	}
	freqs, hasF, err := paramFloatSlice(f.Name, f.Params, "frequencies")
	if err != nil {
		return nil, err
	}
	if !hasF {
		freqs = make([]float64, len(options))
		for i := range freqs {
			freqs[i] = 0.5
		}
	}
	if len(freqs) != len(options) {
		return nil, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			fmt.Sprintf("field %q: frequencies length must match options", f.Name),
			map[string]any{"options": len(options), "frequencies": len(freqs)})
	}
	for i, p := range freqs {
		if p < 0 || p > 1 {
			return nil, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
				fmt.Sprintf("field %q: set_bernoulli frequency must be in [0, 1]", f.Name),
				map[string]any{"index": i, "value": p})
		}
	}
	return &setSampler{options: options, freqs: freqs}, nil
}

func (s *setSampler) next(rng *rand.Rand) (any, bool) {
	m := make(map[string]bool, len(s.options))
	for i, opt := range s.options {
		m[opt] = rng.Float64() < s.freqs[i]
	}
	return m, false
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
	if endT.Before(startT) {
		return nil, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			fmt.Sprintf("field %q: uniform_date end must not be before start", f.Name), nil)
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
	tree      *syntax.Regexp
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
