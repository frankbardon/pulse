package processing

import (
	"encoding/json"
	"math"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/fieldaccept"
	"github.com/frankbardon/pulse/types"
)

// Model-error aggregators — AGG_MAPE, AGG_RMSE, AGG_MAE (fit-scoring
// E2-S1). Each compares an actual column (the slot's Field) with a
// prediction column (params.prediction_field, typically an
// ATTR_REG_PREDICT score in the same slot) row by row and folds one
// per-row loss into two additive sums:
//
//	AGG_MAPE  loss = |y − ŷ| / |y|   figure = Σw·loss / Σw   (a fraction, not ×100)
//	AGG_RMSE  loss = (y − ŷ)²        figure = √(Σw·loss / Σw)
//	AGG_MAE   loss = |y − ŷ|         figure = Σw·loss / Σw
//
// A row contributes only when both sides answer (a null on either side
// skips it; a present actual with a null prediction is counted in
// n_null_prediction) and, on a weighted slot, when its weight is valid
// and > 0. AGG_MAPE further skips a row whose actual is exactly 0 —
// its percentage error is undefined — and counts it in n_zero_actual.
// No contributing row ⇒ NaN (null on the JSON wire). A NaN input
// propagates to a NaN figure: it is a value, not a null.
//
// The state is (Σw·loss, Σw, counters) — every piece additive — so the
// operators are streamable and Mergeable: the merged partial state of
// any partition equals the single pass up to float summation order.
// At w = 1, w·loss is loss and Σw is the contributing-row count, so
// an all-ones weight answers the unweighted figure bit for bit.
//
// The universal floor (n, n_null) keys on the slot's Field — the
// actual — exactly as it does for every aggregator (the floor
// machinery is shared across the buffered, streaming, grouped,
// crosstab and parallel arms); the prediction side's nulls ride the
// operator key n_null_prediction.

// modelErrorKind selects the per-row loss and the finalize transform.
type modelErrorKind int

const (
	modelErrorMAPE modelErrorKind = iota
	modelErrorRMSE
	modelErrorMAE
)

func (k modelErrorKind) aggType() types.AggregationType {
	switch k {
	case modelErrorMAPE:
		return types.AGG_MAPE
	case modelErrorRMSE:
		return types.AGG_RMSE
	}
	return types.AGG_MAE
}

// modelErrorParams is the typed params decode. prediction_field is the
// shared prediction-column param name (reused by later scoring
// aggregators, so keep it stable).
type modelErrorParams struct {
	PredictionField string `json:"prediction_field"`
}

// modelErrorAggregator backs all three operators.
type modelErrorAggregator struct {
	kind       modelErrorKind
	actual     string
	prediction string
	// weight is the applied slot weight; nil on an unweighted slot.
	weight *types.WeightSpec

	lossSum         float64 // Σw·loss
	weightSum       float64 // Σw over contributing rows
	nScored         int     // contributing rows
	nNullPrediction int     // actual present, prediction null
	nZeroActual     int     // AGG_MAPE: both present, actual == 0

	frozen          map[string]any
	frozenHasResult bool
}

func newModelErrorAggregator(kind modelErrorKind) AggregatorFactory {
	return func(agg *types.Aggregation, schema *encoding.Schema) (Aggregator, error) {
		name := string(kind.aggType())
		var params modelErrorParams
		if len(agg.Params) > 0 {
			if err := json.Unmarshal(agg.Params, &params); err != nil {
				return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
					name+": invalid params: "+err.Error(),
					map[string]any{"aggregation": name})
			}
		}
		if err := modelErrorConfigError(kind.aggType(), agg.Field, params.PredictionField, schema); err != nil {
			return nil, err
		}
		a := &modelErrorAggregator{kind: kind, actual: agg.Field, prediction: params.PredictionField}
		if w := slotWeight(agg); w != nil {
			ew := effectiveWeight(w)
			a.weight = &ew
		}
		return a, nil
	}
}

// modelErrorConfigError is the constructor's refusal for a model-error
// slot (AGG_MAPE / AGG_RMSE / AGG_MAE): a missing prediction_field, or
// an actual / prediction column the accepted-types table refuses. A
// name the schema does not carry is a derived column (an attribute
// output, a float) or the field-reference rule's refusal — never this
// one's. Predict raises the same refusals (the prediction_field check
// re-derived, the type checks from the same table);
// TestModelError_PredictMatchesRuntime pins the two together.
func modelErrorConfigError(op types.AggregationType, field, prediction string, schema *encoding.Schema) error {
	name := string(op)
	if prediction == "" {
		return errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
			name+" requires params.prediction_field (the column holding the predicted value)",
			map[string]any{"aggregation": name, "field": field})
	}
	if schema == nil {
		return nil
	}
	// The accepted-types table's refusals, actual then prediction: the
	// integer and float scalars plus packed_bool (a 0/1 outcome against
	// a probability score). Predict raises the same ones.
	if cerr := fieldaccept.Check(name, fieldaccept.SlotField, field, schema); cerr != nil {
		return cerr
	}
	if cerr := fieldaccept.Check(name, fieldaccept.SlotPrediction, prediction, schema); cerr != nil {
		return cerr
	}
	return nil
}

func (a *modelErrorAggregator) reset() {
	a.lossSum, a.weightSum = 0, 0
	a.nScored, a.nNullPrediction, a.nZeroActual = 0, 0, 0
}

func (a *modelErrorAggregator) Aggregate(records []*Record, _ string) (float64, error) {
	a.reset()
	for _, r := range records {
		a.fold(r)
	}
	out := a.figure()
	a.freeze(out)
	return out, nil
}

func (a *modelErrorAggregator) UpdateRow(r *Record, _ string) error {
	a.fold(r)
	return nil
}

func (a *modelErrorAggregator) Finalize() (float64, error) {
	out := a.figure()
	a.freeze(out)
	a.reset()
	return out, nil
}

// fold adds one row's contribution.
func (a *modelErrorAggregator) fold(r *Record) {
	y, okY := r.NumericValue(a.actual)
	if !okY {
		return
	}
	yhat, okP := r.NumericValue(a.prediction)
	if !okP {
		a.nNullPrediction++
		return
	}
	w := 1.0
	if a.weight != nil {
		vw, ok := validRowWeight(r, a.weight)
		if !ok {
			return
		}
		w = vw
	}
	var loss float64
	switch a.kind {
	case modelErrorMAPE:
		if y == 0 {
			a.nZeroActual++
			return
		}
		loss = math.Abs(y-yhat) / math.Abs(y)
	case modelErrorRMSE:
		d := y - yhat
		loss = d * d
	default:
		loss = math.Abs(y - yhat)
	}
	a.lossSum += w * loss
	a.weightSum += w
	a.nScored++
}

// figure resolves the scalar from the live sums; NaN when no row
// contributed.
func (a *modelErrorAggregator) figure() float64 {
	if a.weightSum == 0 {
		return math.NaN()
	}
	mean := a.lossSum / a.weightSum
	if a.kind == modelErrorRMSE {
		return math.Sqrt(mean)
	}
	return mean
}

// freeze stamps the components map from the live state, so
// Components() answers after the streaming Finalize-reset.
func (a *modelErrorAggregator) freeze(scalar float64) {
	a.frozenHasResult = true
	a.frozen = a.componentsFrom(scalar)
}

func (a *modelErrorAggregator) componentsFrom(scalar float64) map[string]any {
	out := map[string]any{
		"loss_sum":          a.lossSum,
		"weight_sum":        a.weightSum,
		"n_scored":          a.nScored,
		"n_null_prediction": a.nNullPrediction,
	}
	switch a.kind {
	case modelErrorMAPE:
		out["n_zero_actual"] = a.nZeroActual
		out["mape"] = scalar
	case modelErrorRMSE:
		mse := math.NaN()
		if a.weightSum != 0 {
			mse = a.lossSum / a.weightSum
		}
		out["mse"] = mse
		out["rmse"] = scalar
	default:
		out["mae"] = scalar
	}
	return out
}

// Components returns the frozen state: loss_sum, weight_sum, n_scored,
// n_null_prediction, plus n_zero_actual + mape (AGG_MAPE), mse + rmse
// (AGG_RMSE) or mae (AGG_MAE). Before any Aggregate / Finalize it
// reports the empty state (zero sums, NaN figure).
func (a *modelErrorAggregator) Components() (map[string]any, error) {
	if !a.frozenHasResult {
		empty := &modelErrorAggregator{kind: a.kind}
		return empty.componentsFrom(math.NaN()), nil
	}
	return a.frozen, nil
}

// MergeOnline adds another partition's live state. Every field is a
// plain sum, so the merge is associative and commutative.
func (a *modelErrorAggregator) MergeOnline(other OnlineAggregator) error {
	b, ok := other.(*modelErrorAggregator)
	if !ok || b.kind != a.kind {
		return mergeTypeMismatch(string(a.kind.aggType()))
	}
	a.lossSum += b.lossSum
	a.weightSum += b.weightSum
	a.nScored += b.nScored
	a.nNullPrediction += b.nNullPrediction
	a.nZeroActual += b.nZeroActual
	return nil
}

// Compile-time interface locks.
var (
	_ MetaAggregator      = (*modelErrorAggregator)(nil)
	_ MergeableAggregator = (*modelErrorAggregator)(nil)
)
