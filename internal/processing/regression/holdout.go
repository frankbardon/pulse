package regression

import (
	"math"

	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/types"
)

// HoldoutFigures scores rows — the records spec's holdout predicate
// held out of the fit — with scorer (the fitted model's RowScorer, on
// the response scale) and folds the out-of-sample figures of
// types.RegressionHoldout; nNull is the count of rows whose predicate
// was null, echoed as NNull.
//
// A row is scored when its target (InputValue) and score are present
// and finite and, on a weighted slot (spec is the STAMPED spec, as the
// fit reads it), its weight is valid and positive — the fit's own row
// rule (rowWeights.of). N counts those rows raw. The losses fold in
// record order exactly as AGG_RMSE / AGG_MAE fold them (Σw·e², Σw·|e|,
// Σw; e = y − ŷ), so the figures equal the ATTR_REG_PREDICT + AGG_*
// route's; R2 is 1 − Σw·e² / Σw·(y − ȳ)², ȳ the held-out rows' own
// weighted mean, NaN (null on the wire) when Σw·(y − ȳ)² is 0. No scored
// row is the slot's data-dependent failure (model.HoldoutEmpty).
func HoldoutFigures(spec *types.RegressionSpec, scorer model.RowScorer, rows []Record, nNull int) (*types.RegressionHoldout, error) {
	rw := newRowWeights(spec)
	var (
		n                         int
		lossSq, lossAbs, sumW, wY float64
		ys, ws                    []float64
	)
	for _, rec := range rows {
		y, ok := InputValue(rec, spec.Target)
		if !ok {
			continue
		}
		yhat, ok := scorer.Score(rec)
		if !ok || math.IsNaN(yhat) || math.IsInf(yhat, 0) {
			continue
		}
		w, ok := rw.of(rec)
		if !ok {
			continue
		}
		d := y - yhat
		sq := d * d
		lossSq += float64(w * sq)
		lossAbs += float64(w * math.Abs(d))
		sumW += w
		wY += float64(w * y)
		ys = append(ys, y)
		ws = append(ws, w)
		n++
	}
	if n == 0 {
		return nil, model.HoldoutEmpty(spec)
	}
	mean := wY / sumW
	var sst float64
	for i, y := range ys {
		c := y - mean
		sst += float64(ws[i] * (c * c))
	}
	r2 := math.NaN()
	if sst > 0 {
		r2 = 1 - lossSq/sst
	}
	return &types.RegressionHoldout{
		N:     n,
		NNull: nNull,
		RMSE:  math.Sqrt(lossSq / sumW),
		MAE:   lossAbs / sumW,
		R2:    r2,
	}, nil
}
