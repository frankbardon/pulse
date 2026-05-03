package processing

import (
	"fmt"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// decimalAggResult is the structured result returned by decimal-aware
// aggregators. The orchestrator embeds this into the response row map
// in place of a plain float64. Result type carries its own scale so the
// emitter can render the value without referring back to the schema.
type decimalAggResult struct {
	Value     encoding.Decimal128
	Scale     uint8
	Precision uint8
	// FellBack indicates that the aggregation overflowed decimal128(38)
	// and the result was downgraded to f64. Float carries the f64 value
	// when FellBack is true; otherwise it is unset.
	FellBack bool
	Float    float64
}

// MarshalJSON renders the result as a decimal string at the carried
// scale, or as a number when the aggregator fell back to f64.
func (r decimalAggResult) MarshalJSON() ([]byte, error) {
	if r.FellBack {
		return []byte(fmt.Sprintf("%v", r.Float)), nil
	}
	return []byte(fmt.Sprintf("%q", r.Value.String(r.Scale))), nil
}

// String renders the decimal at its scale.
func (r decimalAggResult) String() string {
	if r.FellBack {
		return fmt.Sprintf("%g", r.Float)
	}
	return r.Value.String(r.Scale)
}

// decimalSupportedAgg lists the aggregations that have a defined decimal
// implementation in v1. Aggregations outside this set should be rejected
// at predict time with PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL.
var decimalSupportedAgg = map[types.AggregationType]bool{
	types.AGG_SUM:            true,
	types.AGG_AVERAGE:        true,
	types.AGG_MIN:            true,
	types.AGG_MAX:            true,
	types.AGG_VARIANCE:       true,
	types.AGG_STDDEV:         true,
	types.AGG_COUNT:          true,
	types.AGG_DISTINCT_COUNT: true,
}

// IsDecimalAggregationSupported reports whether agg has a decimal-aware
// implementation.
func IsDecimalAggregationSupported(agg types.AggregationType) bool {
	return decimalSupportedAgg[agg]
}

// AggregateDecimalField runs the decimal-aware aggregator for `agg` over
// the given records, reading the wide map for `field`. Used by the
// orchestrator when the field type is decimal128 or nullable_decimal128.
func AggregateDecimalField(agg types.AggregationType, records []*Record, field string, scale uint8) (decimalAggResult, error) {
	values := make([]encoding.Decimal128, 0, len(records))
	for _, r := range records {
		v, ok := r.WideValue(field)
		if !ok {
			continue
		}
		d, ok := v.(encoding.Decimal128)
		if !ok {
			continue
		}
		values = append(values, d)
	}
	switch agg {
	case types.AGG_COUNT:
		return decimalAggResult{
			Value: encoding.NewDecimal128FromInt(int64(len(values))),
			Scale: 0,
		}, nil
	case types.AGG_DISTINCT_COUNT:
		seen := make(map[string]struct{}, len(values))
		for _, v := range values {
			seen[v.String(scale)] = struct{}{}
		}
		return decimalAggResult{
			Value: encoding.NewDecimal128FromInt(int64(len(seen))),
			Scale: 0,
		}, nil
	case types.AGG_SUM:
		acc := encoding.ZeroDecimal128()
		for _, v := range values {
			next, err := acc.Add(v)
			if err != nil {
				if isOverflow(err) {
					// Plan: AVERAGE may fall back to f64; SUM does not. SUM
					// overflow is a hard error.
					return decimalAggResult{}, err
				}
				return decimalAggResult{}, err
			}
			acc = next
		}
		return decimalAggResult{Value: acc, Scale: scale}, nil
	case types.AGG_MIN, types.AGG_MAX:
		if len(values) == 0 {
			return decimalAggResult{Value: encoding.ZeroDecimal128(), Scale: scale}, nil
		}
		best := values[0]
		for _, v := range values[1:] {
			c := v.Cmp(best)
			if (agg == types.AGG_MIN && c < 0) || (agg == types.AGG_MAX && c > 0) {
				best = v
			}
		}
		return decimalAggResult{Value: best, Scale: scale}, nil
	case types.AGG_AVERAGE:
		if len(values) == 0 {
			return decimalAggResult{Value: encoding.ZeroDecimal128(), Scale: scale}, nil
		}
		acc := encoding.ZeroDecimal128()
		fellBack := false
		fSum := 0.0
		for _, v := range values {
			next, err := acc.Add(v)
			if err != nil {
				if isOverflow(err) {
					fellBack = true
					break
				}
				return decimalAggResult{}, err
			}
			acc = next
		}
		if fellBack {
			for _, v := range values {
				fSum += v.Float64(scale)
			}
			return decimalAggResult{
				FellBack: true,
				Float:    fSum / float64(len(values)),
			}, nil
		}
		// avg = acc / len(values), at scale max(scale, MIN_SCALE).
		divisor := encoding.NewDecimal128FromInt(int64(len(values)))
		// Use Decimal128.Div with operand scales (acc at `scale`, len at 0).
		resScale := scale
		if resScale < encoding.MinDecimalScale {
			resScale = encoding.MinDecimalScale
		}
		out, err := acc.Div(divisor, scale, 0, resScale)
		if err != nil {
			if isOverflow(err) {
				for _, v := range values {
					fSum += v.Float64(scale)
				}
				return decimalAggResult{
					FellBack: true,
					Float:    fSum / float64(len(values)),
				}, nil
			}
			return decimalAggResult{}, err
		}
		return decimalAggResult{Value: out, Scale: resScale}, nil
	case types.AGG_VARIANCE, types.AGG_STDDEV:
		if len(values) == 0 {
			return decimalAggResult{Value: encoding.ZeroDecimal128(), Scale: scale}, nil
		}
		// Two-pass population variance in decimal:
		//   μ = (1/N) Σ x_i  at meanScale = max(scale, MIN_SCALE)
		//   σ² = (1/N) Σ (x_i - μ)²  at varScale = 2 * meanScale
		// STDDEV applies decimal Sqrt to bring the result back to meanScale.
		meanScale := scale
		if meanScale < encoding.MinDecimalScale {
			meanScale = encoding.MinDecimalScale
		}
		// Compute mean. Mirrors AGG_AVERAGE; falls back to f64 on overflow
		// of the running sum.
		sum := encoding.ZeroDecimal128()
		fellBack := false
		for _, v := range values {
			next, err := sum.Add(v)
			if err != nil {
				if isOverflow(err) {
					fellBack = true
					break
				}
				return decimalAggResult{}, err
			}
			sum = next
		}
		if fellBack {
			return decimalVarianceFloat64(values, scale, agg), nil
		}
		divisor := encoding.NewDecimal128FromInt(int64(len(values)))
		mean, err := sum.Div(divisor, scale, 0, meanScale)
		if err != nil {
			if isOverflow(err) {
				return decimalVarianceFloat64(values, scale, agg), nil
			}
			return decimalAggResult{}, err
		}
		// Σ (x_i - μ)² with both sides at meanScale; squared values at
		// varScale = 2 * meanScale.
		varScale := 2 * meanScale
		if varScale > encoding.MaxDecimalPrecision {
			// Result scale alone fills the 38-digit budget — fall back.
			return decimalVarianceFloat64(values, scale, agg), nil
		}
		sumSq := encoding.ZeroDecimal128()
		for _, v := range values {
			vScaled, err := v.Rescale(scale, meanScale)
			if err != nil {
				if isOverflow(err) {
					return decimalVarianceFloat64(values, scale, agg), nil
				}
				return decimalAggResult{}, err
			}
			diff, err := vScaled.Sub(mean)
			if err != nil {
				if isOverflow(err) {
					return decimalVarianceFloat64(values, scale, agg), nil
				}
				return decimalAggResult{}, err
			}
			sq, err := diff.Mul(diff)
			if err != nil {
				if isOverflow(err) {
					return decimalVarianceFloat64(values, scale, agg), nil
				}
				return decimalAggResult{}, err
			}
			next, err := sumSq.Add(sq)
			if err != nil {
				if isOverflow(err) {
					return decimalVarianceFloat64(values, scale, agg), nil
				}
				return decimalAggResult{}, err
			}
			sumSq = next
		}
		variance, err := sumSq.Div(divisor, varScale, 0, varScale)
		if err != nil {
			if isOverflow(err) {
				return decimalVarianceFloat64(values, scale, agg), nil
			}
			return decimalAggResult{}, err
		}
		if agg == types.AGG_VARIANCE {
			return decimalAggResult{Value: variance, Scale: varScale}, nil
		}
		// STDDEV: decimal sqrt to meanScale.
		stddev, err := variance.Sqrt(varScale, meanScale)
		if err != nil {
			if isOverflow(err) {
				return decimalVarianceFloat64(values, scale, agg), nil
			}
			return decimalAggResult{}, err
		}
		return decimalAggResult{Value: stddev, Scale: meanScale}, nil
	default:
		return decimalAggResult{}, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_CONFIG,
			"aggregation has no decimal implementation",
			map[string]any{"aggregation": string(agg)})
	}
}

// isOverflow reports whether err is a PULSE_DECIMAL_OVERFLOW.
func isOverflow(err error) bool {
	return errors.HasCode(err, errors.PULSE_DECIMAL_OVERFLOW)
}

// decimalVarianceFloat64 is the f64 fallback for variance / stddev when
// any intermediate decimal computation would overflow. The result
// reports FellBack=true so the orchestrator can emit a
// PULSE_DECIMAL_PRECISION_LOSS warning.
func decimalVarianceFloat64(values []encoding.Decimal128, scale uint8, agg types.AggregationType) decimalAggResult {
	fs := make([]float64, len(values))
	for i, v := range values {
		fs[i] = v.Float64(scale)
	}
	if agg == types.AGG_STDDEV {
		return decimalAggResult{FellBack: true, Float: populationStdDev(fs)}
	}
	return decimalAggResult{FellBack: true, Float: populationVariance(fs)}
}
