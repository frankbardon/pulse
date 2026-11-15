package processing

import (
	"math"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/processing/regression"
)

// attribute_reg_predict_carry.go is a carried ATTR_REG_PREDICT
// (model.ScoreCarries: params.order set, params.carryover not false)
// over a fit that adstocks its channels in-model. The slot reads the
// scoring frame's RAW spend: its pre-pass folds every scored row's
// spend by params.order's series and period, Finalize checks the
// series (a repeated or non-contiguous period is
// PROCESSING_REGRESSION_ORDER), and Row scores each row with its
// channels read as their adstock at the fit's kernel, the fit's last
// periods (outputs.adstock_state) carried in — regression.Carrier, the
// engine holdout's own computation. It is two-pass by its params alone
// (every routing site and predict read model.ScoreCarries), so the
// parallel reducers and a ProcessChain stage leave it to the serial
// drive.

// regPredictCarryAttribute is the carried ATTR_REG_PREDICT computer.
type regPredictCarryAttribute struct {
	regPredictAttribute
	carrier *regression.Carrier
}

var (
	_ TwoPassAttribute = (*regPredictCarryAttribute)(nil)
	_ modelBinder      = (*regPredictCarryAttribute)(nil)
)

// bindModels binds the fit (regPredictAttribute.bind — the carry-over
// rule included) and builds its carrier over params.order.
func (a *regPredictCarryAttribute) bindModels(models *model.Set) error {
	p, b, err := a.bind(models)
	if err != nil || b == nil {
		return err
	}
	var outs map[string]any
	var coefs map[string]float64
	if b.Result != nil {
		outs, coefs = b.Result.Outputs, b.Result.Coefficients
	}
	st, ok, rerr := model.ReadAdstockState(outs)
	if rerr != nil || !ok {
		return errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
			"ATTR_REG_PREDICT: the bound fit holds no readable outputs.adstock_state", map[string]any{"fit": p.Fit})
	}
	c, err := regression.NewCarrier(st, coefs, p.Order.Field, p.Order.Partition,
		map[string]any{"attribute": string(a.attr.Type), "fit": p.Fit})
	if err != nil {
		return err
	}
	a.carrier = c
	return nil
}

// PrePass satisfies TwoPassAttribute: folds the row's raw spend.
func (a *regPredictCarryAttribute) PrePass(rec *Record, _ string) error {
	if a.carrier != nil {
		a.carrier.Add(rec)
	}
	return nil
}

// Finalize satisfies TwoPassAttribute: checks the scored series.
func (a *regPredictCarryAttribute) Finalize() error {
	if a.carrier == nil {
		return nil
	}
	return a.carrier.Finalize()
}

// Row is the row's carried score; NaN (null) when the fit failed, the
// row cannot be placed or a column it reads is null.
func (a *regPredictCarryAttribute) Row(rec *Record, _ string) (float64, error) {
	if a.unavailable != nil {
		return math.NaN(), nil
	}
	if a.scorer == nil || a.carrier == nil {
		return 0, errors.NewCodedError(errors.PROCESSING_INTERNAL,
			"ATTR_REG_PREDICT scored before its model was bound")
	}
	v, ok := a.scorer.Score(a.carrier.Row(rec))
	if !ok {
		return math.NaN(), nil
	}
	return v, nil
}

// Compute is the two-pass drive over every record (the buffered arm).
func (a *regPredictCarryAttribute) Compute(records []*Record, field string) ([]float64, error) {
	return computeTwoPass(a, records, field)
}

// MergeTally satisfies MergeableAttributeTally for a partner of the same
// kind (the merge gate never admits a carried slot to the parallel
// reducers; a serial drive never merges).
func (a *regPredictCarryAttribute) MergeTally(other RowLocalAttribute) error {
	o, ok := other.(*regPredictCarryAttribute)
	if !ok {
		return tallyPartnerMismatch(a.attr.Type, other)
	}
	model.ScorerGeoTally(a.scorer).Merge(model.ScorerGeoTally(o.scorer))
	return nil
}
