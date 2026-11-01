package processing

import (
	"math"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/processing/regression"
	"github.com/frankbardon/pulse/types"
)

// ATTR_REG_FITTED / ATTR_REG_RESIDUAL with params.fit (U53 FR-25), and
// ATTR_REG_LEVERAGE / ATTR_REG_INFLUENCE with it (U65 E1-S2):
// score the row through a BOUND fit — a Compose `fits` entry or a
// loaded `models` entry, the namespace ATTR_REG_PREDICT resolves —
// instead of refitting OLS. The fit is resolved when the processor binds
// its attributes (bindModels), before any record is read, so the slot is
// row-local (vectors.FitBound). FITTED is the fit's score through the
// shared scoring path (model.RowScorer — regression.BuiltinScorer for
// the built-ins, the registration's Scorer for an extension regression),
// on the response scale: a REG_GLM fit's fitted mean. RESIDUAL is the
// fit's target minus that score. LEVERAGE, INFLUENCE and the
// standardized / studentized RESIDUAL read the fit's design through
// regression.BoundInfluence (its vcov, dispersion and residual df;
// model.CheckFitDesign admitted the fit). A null in any column read —
// or, for a residual or influence slot, a null target — emits NaN,
// written as a NULL (NullEmitter).
//
// The reference rule is internal/model.CheckFitReference, the one the
// field-reference pass (predict and runtime) runs first; bind re-runs it
// so a processor driven without the service refuses the same way. A
// failed fit (model.Set.Failed) scores an all-null column with one
// PULSE_FIT_UNAVAILABLE warning, as ATTR_REG_PREDICT does.

// regFitScoreAttribute is the params.fit computer of the four
// regression attributes. scorer is nil until bindModels resolves the
// fit.
type regFitScoreAttribute struct {
	attr   *types.Attribute
	kind   regressionAttributeKind
	schema *encoding.Schema
	exts   *ExtensionRegistry
	scorer model.RowScorer
	// target is the bound fit's response column (RESIDUAL reads it).
	target string
	// residualKind / family: RESIDUAL's params.kind and the bound fit's
	// error family and link it reads (regression.ResidualFamily).
	residualKind string
	family       regression.ResidualFamily
	// measure is INFLUENCE's resolved params.measure; design the bound
	// fit's influence design (nil unless the slot reads it:
	// model.FitDesignKind).
	measure string
	design  *regression.BoundInfluence
	// unavailable is the PULSE_FIT_UNAVAILABLE warning of a slot whose
	// fit failed on its data (nil otherwise): no scorer, every row null.
	unavailable *types.ResponseWarning
}

var (
	_ RowLocalAttribute = (*regFitScoreAttribute)(nil)
	_ NullEmitter       = (*regFitScoreAttribute)(nil)
	_ modelBinder       = (*regFitScoreAttribute)(nil)
	_ attributeWarner   = (*regFitScoreAttribute)(nil)
	_ ExtensionAware    = (*regFitScoreAttribute)(nil)
)

func newRegFitScoreAttribute(attr *types.Attribute, schema *encoding.Schema, kind regressionAttributeKind) AttributeComputer {
	return &regFitScoreAttribute{attr: attr, kind: kind, schema: schema}
}

// SetExtensions satisfies ExtensionAware: the registry's scorable types
// and scorer factories, built-ins included.
func (a *regFitScoreAttribute) SetExtensions(r *ExtensionRegistry) { a.exts = r }

// bindModels resolves params.fit and builds the scorer:
// model.CheckFitReference, then each column the score reads that is a
// schema field must be numeric — the field-reference pass's order.
func (a *regFitScoreAttribute) bindModels(models *model.Set) error {
	p, sig, cerr := model.CheckFitReference(a.attr, models, a.exts.ScorableRegression)
	if cerr != nil {
		return cerr
	}
	if a.schema != nil {
		for _, col := range sig.Terms {
			if f := a.schema.Field(col); f != nil && !f.Type.IsNumericForAnalytics() {
				return model.PredictorNotNumericFor(a.attr.Type, p.Fit, col, f.Type.String())
			}
		}
		if model.FitReadsTarget(a.attr.Type) && sig.Target != "" {
			if f := a.schema.Field(sig.Target); f != nil && !f.Type.IsNumericForAnalytics() {
				return model.TargetNotNumeric(a.attr.Type, p.Fit, sig.Target, f.Type.String())
			}
		}
	}
	if se, failed := models.Failed(p.Fit); failed {
		a.unavailable = model.FitUnavailable(p.Fit, a.label(), se)
		return nil
	}
	b, err := models.Lookup(p.Fit)
	if err != nil {
		return err
	}
	scorer, err := b.Scorer(a.exts.RegressionScorer, model.ScoreParams{Fit: p.Fit})
	if err != nil {
		return err
	}
	a.scorer, a.target, a.residualKind, a.measure = scorer, b.Target, p.Kind, p.Measure
	if model.FitDesignKind(a.attr.Type, p) {
		d, err := regression.NewBoundInfluence(b)
		if err != nil {
			return err
		}
		a.design = d
	}
	if p.Kind != "" && p.Kind != model.ResidualResponse {
		// CheckFitReference admitted the kind on a built-in type, so the
		// family / link are the engine's own echo.
		fam, err := regression.NewResidualFamily(b.Family, b.Link)
		if err != nil {
			return err
		}
		a.family = fam
	} else {
		a.family, _ = regression.NewResidualFamily("", "")
	}
	return nil
}

// label is the slot's output label: its own, or the params.fit default.
func (a *regFitScoreAttribute) label() string {
	if a.attr.Label != "" {
		return a.attr.Label
	}
	return model.DefaultFitLabel(a.attr)
}

// EmitsNull satisfies NullEmitter: a NaN from Row / Compute is a null.
func (a *regFitScoreAttribute) EmitsNull() bool { return true }

// attributeWarnings satisfies attributeWarner: the PULSE_FIT_UNAVAILABLE
// warning of a slot over a failed fit.
func (a *regFitScoreAttribute) attributeWarnings() []*types.ResponseWarning {
	if a.unavailable == nil {
		return nil
	}
	return []*types.ResponseWarning{a.unavailable}
}

// Row is the row's fitted value (FITTED), target minus it on the kind's
// scale (RESIDUAL), leverage (LEVERAGE) or influence measure
// (INFLUENCE); NaN (null) when a column read is null or non-finite
// (regression.InputValue), the figure is undefined, or the fit failed
// on its data.
func (a *regFitScoreAttribute) Row(rec *Record, _ string) (float64, error) {
	if a.unavailable != nil {
		return math.NaN(), nil
	}
	if a.scorer == nil {
		return 0, errors.NewCodedError(errors.PROCESSING_INTERNAL,
			string(a.attr.Type)+" params.fit scored before its model was bound")
	}
	if a.kind == regAttrLeverage {
		h, ok := a.design.Leverage(rec)
		if !ok {
			return math.NaN(), nil
		}
		return h, nil
	}
	yhat, ok := a.scorer.Score(rec)
	if !ok {
		return math.NaN(), nil
	}
	if a.kind == regAttrFitted {
		return yhat, nil
	}
	y, ok := regression.InputValue(rec, a.target)
	if !ok {
		return math.NaN(), nil
	}
	if a.design == nil {
		return a.family.Residual(a.residualKind, y, yhat), nil
	}
	inf, ok := a.design.Row(rec, y)
	if !ok {
		return math.NaN(), nil
	}
	switch {
	case a.kind == regAttrInfluence && a.measure == model.InfluenceDFFITS:
		return inf.DFFITS, nil
	case a.kind == regAttrInfluence:
		return inf.Cooks, nil
	case a.residualKind == model.ResidualStudentized:
		return inf.Studentized, nil
	}
	return inf.Standardized, nil
}

// Compute is Row over every record (the buffered arm).
func (a *regFitScoreAttribute) Compute(records []*Record, field string) ([]float64, error) {
	out := make([]float64, len(records))
	for i, r := range records {
		v, err := a.Row(r, field)
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return out, nil
}
