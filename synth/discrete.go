package synth

import (
	"fmt"
	"math"
	"math/rand/v2"
	"sort"

	"github.com/frankbardon/pulse/errors"
)

// maxDiscreteLevels caps how many DISTINCT observed values a profiled
// integer column may carry and still be reconstructed as `discrete`.
// Above it the capture ABANDONS the histogram entirely and the field
// falls back to the clamped-normal reconstruction.
//
// Abandon rather than truncate. A histogram holding the 64 most common
// of 3,000 levels describes an arbitrary subset of the column and every
// frequency in it is a share of that subset, not of the field — the same
// reason E3's detectors refuse a truncated level map rather than
// reporting figures over one.
//
// It is a package constant tuned in code, deliberately NOT a
// ProfileOptions field, matching minVarianceExplained /
// minLevelObservations / nullRateDivergenceThreshold: a capture flag that
// moved it would make two documents over the same cohort disagree about
// what the field IS, which is precisely the class of silent disagreement
// this arm exists to remove.
//
// Why 64. It admits every coded response scale by a wide margin — a u4's
// ENTIRE domain is 16 values, an NPS 0-10 is 11, a 1-7 Likert is 7, the
// motivating cohort's widest small integer (a 0-15 share-of-wallet band)
// is 16 — and bounds the document addition at 64 levels x 2 numbers per
// field plus one int map of the same size during the scan.
//
// The boundary is PRAGMATIC, not a fidelity cliff, and reading it as one
// would be wrong. The clamped-normal arm's per-level RELATIVE error does
// not shrink as levels are added: for a K-level scale its central levels
// come out roughly 1.4x their true share and its extreme levels roughly
// 0.04/K-vs-1/K, at every K. What DOES shrink is the ABSOLUTE per-level
// error (~1/K), so a field just above this cap is wrong by under ~1.5
// percentage points per level while one just below it can be wrong by 12
// (measured: 0.2526 -> 0.1373 on a 7-level scale). Raising the cap is a
// document-size decision, not a correctness one. Below ~50 rows per
// level a histogram is also reproducing sampling noise exactly, which is
// a true statement about the source and a weaker generalisation than a
// smooth marginal — a second reason not to raise it far.
const maxDiscreteLevels = 64

// isIntegerQuantizedFieldType reports whether a schema type name denotes
// a column whose on-wire value is an INTEGER the writer produces by
// rounding a drawn float.
//
// The list is not a taste judgement about which fields "look discrete":
// it is exactly the set of arms in writeFieldValueForField that apply
// Floor(f+0.5) (`u4` masks the result to a nibble, `u8`/`u16`/`u32`
// clamp it, `u64` takes it whole). Those are the fields where a
// continuous marginal cannot round-trip through the wire, which is the
// same argument isBooleanFieldType makes for its one bit, one type wider.
//
// Deliberately EXCLUDED: `f32`/`f64` (the writer stores the float, so a
// normal reconstruction round-trips and a histogram would be
// overfitting), `date` (integer days, but it has its own uniform_date
// arm and never reaches the numeric accumulator), `decimal128` (exact at
// its own declared scale, not rounded to an integer), and `packed_bool`
// (one bit — isBooleanFieldType's bernoulli arm runs AHEAD of this one
// and is exact with a single parameter).
func isIntegerQuantizedFieldType(typeName string) bool {
	switch typeName {
	case "u4", "u8", "u16", "u32", "u64":
		return true
	}
	return false
}

// discreteLevels is one `discrete` FieldSpec's parsed, validated and
// normalised staircase: the ascending support, the cumulative weight
// after each level, and the EXACT closed-form moments of that
// distribution.
//
// The moments are computed here rather than read from the profile's own
// mean/std so that a hand-authored spec and a profile-derived one cannot
// disagree, and so that fieldMoments' contract ("exact moments only")
// holds by construction for this arm.
type discreteLevels struct {
	// values is strictly ascending and distinct.
	values []float64
	// cum[i] is the cumulative probability through values[i];
	// cum[len-1] is exactly 1.
	cum       []float64
	mean, std float64
}

// parseDiscreteLevels validates a `discrete` FieldSpec's params.
//
// The param shape mirrors weighted_categorical's (`values` + optional
// `weights`, parallel arrays, uniform when weights are absent) as
// closely as a numeric support allows, so an author who knows one knows
// the other. The one added requirement is MONOTONICITY: values must be
// strictly ascending.
//
// Non-ascending values are REFUSED rather than sorted. Sorting would
// pair each weight with a different value than the author wrote, and the
// consequence of getting it wrong is invisible: Q would not be monotone
// in p, which silently breaks the Gaussian copula's rank ordering (a
// correlated pair would come back with the requested magnitude and no
// coherent direction) and the composed model draw's one surviving
// guarantee (that a positive coefficient moves the field UP). Neither
// shows up as an error anywhere.
func parseDiscreteLevels(fs FieldSpec) (discreteLevels, error) {
	var out discreteLevels
	values, ok, err := paramFloatSlice(fs.Name, fs.Params, "values")
	if err != nil {
		return out, err
	}
	if !ok || len(values) == 0 {
		return out, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			fmt.Sprintf("field %q: discrete requires non-empty values", fs.Name),
			map[string]any{"field": fs.Name})
	}
	for i := range values {
		if math.IsNaN(values[i]) || math.IsInf(values[i], 0) {
			return out, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
				fmt.Sprintf("field %q: discrete values[%d] is not finite", fs.Name, i),
				map[string]any{"field": fs.Name, "index": i})
		}
		if i > 0 && values[i] <= values[i-1] {
			return out, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
				fmt.Sprintf("field %q: discrete values must be strictly ascending; values[%d] = %g does not exceed values[%d] = %g",
					fs.Name, i, values[i], i-1, values[i-1]),
				map[string]any{"field": fs.Name, "index": i})
		}
	}
	weights, hasW, err := paramFloatSlice(fs.Name, fs.Params, "weights")
	if err != nil {
		return out, err
	}
	if !hasW {
		weights = make([]float64, len(values))
		for i := range weights {
			weights[i] = 1
		}
	}
	if len(weights) != len(values) {
		return out, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			fmt.Sprintf("field %q: discrete weights length must match values", fs.Name),
			map[string]any{"field": fs.Name, "values": len(values), "weights": len(weights)})
	}
	total := 0.0
	for i, w := range weights {
		if w < 0 || math.IsNaN(w) || math.IsInf(w, 0) {
			return out, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
				fmt.Sprintf("field %q: discrete weights[%d] must be a finite non-negative number", fs.Name, i),
				map[string]any{"field": fs.Name, "index": i, "value": w})
		}
		total += w
	}
	if total <= 0 {
		return out, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			fmt.Sprintf("field %q: discrete weights sum must be > 0", fs.Name),
			map[string]any{"field": fs.Name})
	}

	out.values = values
	out.cum = make([]float64, len(values))
	run := 0.0
	for i, w := range weights {
		run += w
		out.cum[i] = run / total
	}
	// Pin the terminal entry to exactly 1 so quantile(1) cannot fall off
	// the end through a rounding shortfall in the running division.
	out.cum[len(out.cum)-1] = 1

	// Exact closed-form moments. Every product feeding an add is wrapped
	// float64(...) — see synth/moments.go for the FMA-contraction rule
	// this obeys.
	for i := range values {
		p := weights[i] / total
		out.mean += float64(p * values[i])
	}
	variance := 0.0
	for i := range values {
		p := weights[i] / total
		d := values[i] - out.mean
		variance += float64(p * float64(d*d))
	}
	out.std = math.Sqrt(variance)
	return out, nil
}

// quantile returns Q(p) — the level whose cumulative band contains p.
//
// It is a STAIRCASE: not continuous, not pointwise invertible, and that
// is the property every consumer has to respect (see latentInvertible).
func (d discreteLevels) quantile(p float64) float64 {
	idx := sort.SearchFloat64s(d.cum, p)
	if idx >= len(d.values) {
		idx = len(d.values) - 1
	}
	return d.values[idx]
}

// discreteSampler is the field's OWN draw: one uniform against the
// cumulative weights, identical in construction to
// weightedCategoricalSampler (and deliberately so — a numeric support is
// the only difference) and therefore consuming exactly one RNG value per
// row, as the normal arm it replaces did not (NormFloat64 may take
// several). Two specs differing in a field's distribution produce
// different streams by design; the contract is that the stream is a
// function of the SPEC, not of the data.
type discreteSampler struct{ levels discreteLevels }

func newDiscreteSampler(f FieldSpec) (sampler, error) {
	levels, err := parseDiscreteLevels(f)
	if err != nil {
		return nil, err
	}
	return &discreteSampler{levels: levels}, nil
}

func (d *discreteSampler) next(rng *rand.Rand) (any, bool) {
	return d.levels.quantile(rng.Float64()), false
}
