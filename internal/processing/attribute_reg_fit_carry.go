package processing

import (
	"math"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/processing/regression"
)

// attribute_reg_fit_carry.go is a carried params.fit ATTR_REG_FITTED /
// ATTR_REG_RESIDUAL (model.FitCarries: params.order set,
// params.carryover not false) over a fit that adstocks its channels
// in-model — the carried ATTR_REG_PREDICT's contract
// (attribute_reg_predict_carry.go) on the fitted value and the
// residual. The slot reads the scoring frame's RAW spend: its pre-pass
// folds every scored row's spend by params.order's series and period,
// Finalize checks the series (a repeated or non-contiguous period is
// PROCESSING_REGRESSION_ORDER), and Row reads each row's channels as
// their adstock at the fit's kernel, the fit's last periods
// (outputs.adstock_state) carried in — regression.Carrier — before the
// fitted value (and the residual y − ŷ, y read off the row) is taken.
// On the fit's own rows that is the fit's own in-model adstock, so the
// carried fitted value and residual are the fit's. It is two-pass by
// its params alone (vectors.FitBound is false for it, so every routing
// site and predict agree), so the parallel reducers and a ProcessChain
// stage leave it to the serial drive.

// regFitCarryAttribute is the carried params.fit fitted / residual
// computer.
type regFitCarryAttribute struct {
	regFitScoreAttribute
	carrier *regression.Carrier
}

var (
	_ TwoPassAttribute = (*regFitCarryAttribute)(nil)
	_ modelBinder      = (*regFitCarryAttribute)(nil)
)

// bindModels binds the fit (regFitScoreAttribute.bind — the carry-over
// rule included) and builds its carrier over params.order.
func (a *regFitCarryAttribute) bindModels(models *model.Set) error {
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
	if rerr != nil || !ok || p.Order == nil {
		return errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
			string(a.attr.Type)+": the bound fit holds no readable outputs.adstock_state", map[string]any{"fit": p.Fit})
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
func (a *regFitCarryAttribute) PrePass(rec *Record, _ string) error {
	if a.carrier != nil {
		a.carrier.Add(rec)
	}
	return nil
}

// Finalize satisfies TwoPassAttribute: checks the scored series.
func (a *regFitCarryAttribute) Finalize() error {
	if a.carrier == nil {
		return nil
	}
	return a.carrier.Finalize()
}

// Row is the row's carried fitted value or residual; NaN (null) when
// the fit failed, the row cannot be placed or a column it reads is
// null.
func (a *regFitCarryAttribute) Row(rec *Record, _ string) (float64, error) {
	if a.unavailable != nil {
		return math.NaN(), nil
	}
	if a.scorer == nil || a.carrier == nil {
		return 0, errors.NewCodedError(errors.PROCESSING_INTERNAL,
			string(a.attr.Type)+" params.fit scored before its model was bound")
	}
	return a.rowOf(a.carrier.Row(rec))
}

// Compute is the two-pass drive over every record (the buffered arm).
func (a *regFitCarryAttribute) Compute(records []*Record, field string) ([]float64, error) {
	return computeTwoPass(a, records, field)
}

// MergeTally satisfies MergeableAttributeTally for a partner of the same
// kind (the merge gate never admits a carried slot to the parallel
// reducers; a serial drive never merges).
func (a *regFitCarryAttribute) MergeTally(other RowLocalAttribute) error {
	o, ok := other.(*regFitCarryAttribute)
	if !ok {
		return tallyPartnerMismatch(a.attr.Type, other)
	}
	model.ScorerGeoTally(a.scorer).Merge(model.ScorerGeoTally(o.scorer))
	return nil
}
