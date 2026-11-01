package processing

import (
	"fmt"
	"math"
	"strconv"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/ddofspec"
	"github.com/frankbardon/pulse/internal/fieldaccept"
	"github.com/frankbardon/pulse/internal/processing/window"
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
	// Undefined marks a figure with no defined value — the mean,
	// min / max or (co)variance of no contributing row. It never reaches
	// a row: cellValue turns it into NaN, the engine-wide undefined
	// figure (JSON null via types.MarshalFinite), never a "0" string.
	Undefined bool
	// IsCount marks a count (AGG_COUNT / AGG_DISTINCT_COUNT /
	// AGG_NULL_COUNT) over a decimal128 field. A count is never a
	// decimal: cellValue hands the row the float64 count every other
	// field type's count carries, so it marshals as a JSON integer.
	IsCount bool
	Count   int64
}

// decimalCount is the result of a count over a decimal128 field.
func decimalCount(n int) decimalAggResult { return decimalAggResult{IsCount: true, Count: int64(n)} }

// undefinedDecimal is the result of a decimal aggregation over no
// contributing row whose figure is undefined (not a count or a sum).
func undefinedDecimal() decimalAggResult { return decimalAggResult{Undefined: true} }

// cellValue is the value a row / crosstab cell carries: the result
// itself, or NaN for an undefined figure, so a decimal column's empty
// bucket reads exactly as a float column's (null on the wire).
func (r decimalAggResult) cellValue() any {
	if r.Undefined {
		return math.NaN()
	}
	if r.IsCount {
		return float64(r.Count)
	}
	return r
}

// MarshalJSON renders the result as a decimal string at the carried
// scale, or as a number when the aggregator fell back to f64.
func (r decimalAggResult) MarshalJSON() ([]byte, error) {
	if r.Undefined {
		return []byte("null"), nil
	}
	if r.IsCount {
		return []byte(strconv.FormatInt(r.Count, 10)), nil
	}
	if r.FellBack {
		return []byte(fmt.Sprintf("%v", r.Float)), nil
	}
	return []byte(fmt.Sprintf("%q", r.Value.String(r.Scale))), nil
}

// String renders the decimal at its scale.
func (r decimalAggResult) String() string {
	if r.Undefined {
		return ""
	}
	if r.IsCount {
		return strconv.FormatInt(r.Count, 10)
	}
	if r.FellBack {
		return fmt.Sprintf("%g", r.Float)
	}
	return r.Value.String(r.Scale)
}

// IsDecimalAggregationSupported reports whether agg has a decimal-aware
// implementation — the accepted-types table's decimal arm set
// (internal/fieldaccept), which predict reads too.
func IsDecimalAggregationSupported(agg types.AggregationType) bool {
	return fieldaccept.HasDecimalArm(string(agg))
}

// decimalAggregationSupported is IsDecimalAggregationSupported for the
// instance r serves: a built-in the instance hides has no decimal
// implementation here.
func (r *ExtensionRegistry) decimalAggregationSupported(agg types.AggregationType) bool {
	return !r.isHidden(string(agg)) && IsDecimalAggregationSupported(agg)
}

// decimalAggregationRefusal is the accepted-types table's decimal
// refusal (PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL) for a built-in agg
// with no decimal arm over a decimal128 field — nil for a name the
// instance hides or never registered, which the registry lookup then
// refuses as unknown, exactly as on any other field type.
func (r *ExtensionRegistry) decimalAggregationRefusal(agg types.AggregationType, field string, ft encoding.FieldType) *errors.CodedError {
	if r.isHidden(string(agg)) {
		return nil
	}
	return fieldaccept.Refusal(string(agg), fieldaccept.SlotField, field, ft)
}

// decimalSlotRefusal is decimalAggregationRefusal for one aggregation
// slot over schema: nil when the slot's field is not a decimal128 schema
// field, the aggregator is an extension (it owns its decimal semantics)
// or a built-in with a decimal arm. The crosstab margin aggregations
// consult it at resolution, so a margin figure is refused exactly where
// the same aggregator in the cell is.
func (r *ExtensionRegistry) decimalSlotRefusal(agg *types.Aggregation, schema *encoding.Schema) *errors.CodedError {
	if agg == nil || schema == nil || r.isExtensionAggregator(agg.Type) {
		return nil
	}
	f := schema.Field(agg.Field)
	if f == nil || !f.Type.IsDecimal() {
		return nil
	}
	return r.decimalAggregationRefusal(agg.Type, agg.Field, f.Type)
}

// AggregateDecimalSlot runs the decimal-aware aggregator for the slot
// agg over the given records. Used by the orchestrator (ungrouped,
// grouped and crosstab cells) when the field type is decimal128 or
// nullable_decimal128: the decimal dispatch runs before any factory, so
// it resolves AGG_VARIANCE / AGG_STDDEV's params.ddof itself, refusing
// an invalid one with the constructor's own PROCESSING_CONFIG
// (ddofspec.Resolve).
func AggregateDecimalSlot(agg *types.Aggregation, records []*Record, scale uint8) (decimalAggResult, error) {
	dof := 0
	if ddofspec.Applies(agg.Type) {
		d, cerr := ddofspec.Resolve(agg.Type, agg.Params)
		if cerr != nil {
			return decimalAggResult{}, cerr
		}
		dof = d
	}
	return aggregateDecimal(agg.Type, dof, records, agg.Field, scale)
}

// AggregateDecimalField runs the decimal-aware aggregator for `agg` over
// the given records, reading the wide map for `field`, with no params
// (AGG_VARIANCE / AGG_STDDEV at the default ddof 0).
func AggregateDecimalField(agg types.AggregationType, records []*Record, field string, scale uint8) (decimalAggResult, error) {
	return aggregateDecimal(agg, 0, records, field, scale)
}

// aggregateDecimal is the decimal arm table; ddof is the variance
// denominator offset (0 or 1), read by AGG_VARIANCE / AGG_STDDEV only.
func aggregateDecimal(agg types.AggregationType, ddof int, records []*Record, field string, scale uint8) (decimalAggResult, error) {
	if agg == types.AGG_NULL_COUNT {
		// The null bitmap only: no decimal math, so the count is the
		// floor's n_null question (FieldPresent), asked per record.
		nNull := 0
		for _, r := range records {
			if !FieldPresent(r, field) {
				nNull++
			}
		}
		return decimalCount(nNull), nil
	}
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
		return decimalCount(len(values)), nil
	case types.AGG_DISTINCT_COUNT:
		seen := make(map[string]struct{}, len(values))
		for _, v := range values {
			seen[v.String(scale)] = struct{}{}
		}
		return decimalCount(len(seen)), nil
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
			return undefinedDecimal(), nil
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
			return undefinedDecimal(), nil
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
		// No row, or one row under ddof 1: undefined, never 0.
		if len(values) <= ddof {
			return undefinedDecimal(), nil
		}
		// Two-pass variance in decimal:
		//   μ = (1/N) Σ x_i  at meanScale = max(scale, MIN_SCALE)
		//   σ² = (1/(N − ddof)) Σ (x_i - μ)²  at varScale = 2 * meanScale
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
			return decimalVarianceFloat64(values, scale, agg, ddof), nil
		}
		divisor := encoding.NewDecimal128FromInt(int64(len(values)))
		mean, err := sum.Div(divisor, scale, 0, meanScale)
		if err != nil {
			if isOverflow(err) {
				return decimalVarianceFloat64(values, scale, agg, ddof), nil
			}
			return decimalAggResult{}, err
		}
		// Σ (x_i - μ)² with both sides at meanScale; squared values at
		// varScale = 2 * meanScale.
		varScale := 2 * meanScale
		if varScale > encoding.MaxDecimalPrecision {
			// Result scale alone fills the 38-digit budget — fall back.
			return decimalVarianceFloat64(values, scale, agg, ddof), nil
		}
		sumSq := encoding.ZeroDecimal128()
		for _, v := range values {
			vScaled, err := v.Rescale(scale, meanScale)
			if err != nil {
				if isOverflow(err) {
					return decimalVarianceFloat64(values, scale, agg, ddof), nil
				}
				return decimalAggResult{}, err
			}
			diff, err := vScaled.Sub(mean)
			if err != nil {
				if isOverflow(err) {
					return decimalVarianceFloat64(values, scale, agg, ddof), nil
				}
				return decimalAggResult{}, err
			}
			sq, err := diff.Mul(diff)
			if err != nil {
				if isOverflow(err) {
					return decimalVarianceFloat64(values, scale, agg, ddof), nil
				}
				return decimalAggResult{}, err
			}
			next, err := sumSq.Add(sq)
			if err != nil {
				if isOverflow(err) {
					return decimalVarianceFloat64(values, scale, agg, ddof), nil
				}
				return decimalAggResult{}, err
			}
			sumSq = next
		}
		varDivisor := divisor
		if ddof != 0 {
			varDivisor = encoding.NewDecimal128FromInt(int64(len(values) - ddof))
		}
		variance, err := sumSq.Div(varDivisor, varScale, 0, varScale)
		if err != nil {
			if isOverflow(err) {
				return decimalVarianceFloat64(values, scale, agg, ddof), nil
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
				return decimalVarianceFloat64(values, scale, agg, ddof), nil
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
// PULSE_DECIMAL_PRECISION_LOSS warning. ddof 1 divides the two-pass m2
// by n − 1 (undefined below two values), as the float64 aggregator does.
func decimalVarianceFloat64(values []encoding.Decimal128, scale uint8, agg types.AggregationType, ddof int) decimalAggResult {
	fs := make([]float64, len(values))
	for i, v := range values {
		fs[i] = v.Float64(scale)
	}
	if ddof != 0 {
		v := ddofspec.Variance(sumSquaredDeviations(fs, mean(fs)), float64(len(fs)), ddof)
		if agg == types.AGG_STDDEV {
			v = math.Sqrt(v)
		}
		return decimalAggResult{FellBack: true, Float: v}
	}
	if agg == types.AGG_STDDEV {
		return decimalAggResult{FellBack: true, Float: populationStdDev(fs)}
	}
	return decimalAggResult{FellBack: true, Float: populationVariance(fs)}
}

// SortValue orders a decimal aggregate cell by value under window
// ORDER BY and Request.Sort (window.SortValuer). Without it the
// comparator saw an opaque struct and every row compared equal, so a
// sort by a decimal aggregate label was a silent no-op.
func (r decimalAggResult) SortValue() any {
	if r.Undefined {
		return math.NaN()
	}
	if r.IsCount {
		return float64(r.Count)
	}
	if r.FellBack {
		return r.Float
	}
	return window.ScaledDecimal{Value: r.Value, Scale: r.Scale}
}
