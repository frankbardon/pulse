package processing

import (
	"math"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/processing/regression"
	"github.com/frankbardon/pulse/types"
)

// ATTR_REG_PREDICT — score each row from a BOUND model instead of
// refitting. The model is one of the request's bound fits (a
// ComposedRequest `fits` entry), resolved by params.fit when the
// processor binds its attributes (bindAttribute → bindModels), before
// any record is read; from then on the attribute is row-local: Row reads
// the model's predictor columns off the record and evaluates the shared
// scoring path (model.RowScorer — regression.BuiltinScorer for the
// built-ins, over regression.LinearPredictor, the
// expression ATTR_REG_FITTED and the GLM engine's own η use), so a
// training row's score equals the fit's own fitted value bit for bit.
//
// The scoring rule — params shape, fit name, terms, scale, bound — is
// internal/model.CheckScore, the one the field-reference pass (predict
// and runtime) runs first; bind re-runs it so a processor driven without
// the service refuses the same way. A null in any column the score reads
// emits NaN, which the orchestrator writes as a NULL (NullEmitter).
//
// A failed fit (a Compose `fits` entry that failed on its data —
// model.Set.Failed) has no model: bind passes the same static checks
// (the failed fit keeps its signature) and marks the slot unavailable,
// so every row scores NaN — an all-null column — and the slot raises
// one PULSE_FIT_UNAVAILABLE warning (attributeWarner) naming the fit;
// every other slot of the request is computed normally.

// regPredictAttribute is the ATTR_REG_PREDICT computer. scorer is nil
// until bindModels resolves the fit.
type regPredictAttribute struct {
	attr   *types.Attribute
	schema *encoding.Schema
	scorer model.RowScorer
	// unavailable is the PULSE_FIT_UNAVAILABLE warning of a slot whose
	// fit failed on its data (nil otherwise): no scorer, every row null.
	unavailable *types.ResponseWarning
}

var (
	_ RowLocalAttribute = (*regPredictAttribute)(nil)
	_ NullEmitter       = (*regPredictAttribute)(nil)
	_ modelBinder       = (*regPredictAttribute)(nil)
	_ attributeWarner   = (*regPredictAttribute)(nil)
)

// newRegPredictAttribute judges the params shape only (no model is in
// reach of a factory); bindModels does the rest. A registry probe (nil
// schema) builds the computer the same way.
func newRegPredictAttribute(attr *types.Attribute, schema *encoding.Schema) (AttributeComputer, error) {
	if attr == nil {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG, "ATTR_REG_PREDICT requires a non-nil spec")
	}
	if _, err := model.DecodeScoreParams(attr.Params); err != nil {
		return nil, err
	}
	return &regPredictAttribute{attr: attr, schema: schema}, nil
}

// bindModels resolves params.fit in the request's bound models and
// builds the scorer: model.CheckScore (PULSE_MODEL_UNKNOWN /
// _SCORE_INVALID / _TERM_UNKNOWN), then each scored predictor that is a
// schema field must be numeric (PULSE_MODEL_PREDICTOR_NOT_NUMERIC) — the
// field-reference pass's order.
func (a *regPredictAttribute) bindModels(models *model.Set) error {
	p, sig, cerr := model.CheckScore(a.attr, models)
	if cerr != nil {
		return cerr
	}
	if a.schema != nil {
		for _, col := range sig.Terms {
			if f := a.schema.Field(col); f != nil && !f.Type.IsNumericForAnalytics() {
				return model.PredictorNotNumeric(p.Fit, col, f.Type.String())
			}
		}
	}
	if se, failed := models.Failed(p.Fit); failed {
		a.unavailable = model.FitUnavailable(p.Fit, regPredictLabel(a.attr), se)
		return nil
	}
	b, err := models.Lookup(p.Fit)
	if err != nil {
		return err
	}
	scorer, err := b.Scorer(regression.BuiltinScorer, p)
	if err != nil {
		return err
	}
	a.scorer = scorer
	return nil
}

// EmitsNull satisfies NullEmitter: a NaN from Row / Compute is a null.
func (a *regPredictAttribute) EmitsNull() bool { return true }

// attributeWarnings satisfies attributeWarner: the PULSE_FIT_UNAVAILABLE
// warning of a slot over a failed fit.
func (a *regPredictAttribute) attributeWarnings() []*types.ResponseWarning {
	if a.unavailable == nil {
		return nil
	}
	return []*types.ResponseWarning{a.unavailable}
}

// regPredictLabel is the slot's output label: its own, or the default
// a scoring slot is written under.
func regPredictLabel(attr *types.Attribute) string {
	if attr.Label != "" {
		return attr.Label
	}
	return model.DefaultScoreLabel(attr)
}

// Row is the row's score, NaN (null) when any column it reads is null
// or the slot's fit failed on its data.
func (a *regPredictAttribute) Row(rec *Record, _ string) (float64, error) {
	if a.unavailable != nil {
		return math.NaN(), nil
	}
	if a.scorer == nil {
		return 0, errors.NewCodedError(errors.PROCESSING_INTERNAL,
			"ATTR_REG_PREDICT scored before its model was bound")
	}
	v, ok := a.scorer.Score(rec)
	if !ok {
		return math.NaN(), nil
	}
	return v, nil
}

// Compute is Row over every record (the buffered arm).
func (a *regPredictAttribute) Compute(records []*Record, field string) ([]float64, error) {
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
