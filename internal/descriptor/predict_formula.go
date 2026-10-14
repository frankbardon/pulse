package descriptor

import (
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// formulaExpressionMissing is newFormulaAttribute's refusal text
// (internal/processing/attribute.go), repeated here because predict
// cannot import the engine; TestFormulaAttribute_ExpressionSlotPredictParity
// pins the two to the byte.
const formulaExpressionMissing = "formula attribute requires an expression"

// formulaExpressionRefusal re-derives ATTR_FORMULA's constructor
// refusal: the expression is read from the attribute's `expression`
// slot ONLY — an empty slot is PROCESSING_CONFIG, also when the text
// sits in params.expression, which nothing reads. Before, predict
// passed such a request and only the runtime refused it. A slot whose
// type the instance hides routes to "" and is skipped, exactly like a
// never-registered name. Nil when every formula carries an expression.
func formulaExpressionRefusal(req *types.Request, opts *PredictOptions) *errors.CodedError {
	if req == nil {
		return nil
	}
	for _, attr := range req.Attributes {
		if attr != nil && opRoute(opts.Instance, attr.Type) == types.ATTR_FORMULA && attr.Expression == "" {
			return errors.NewCodedError(errors.PROCESSING_CONFIG, formulaExpressionMissing)
		}
	}
	return nil
}

// validateFormulaExpressions is formulaExpressionRefusal on the request
// root (Predict). PredictCompose and PredictChain call the refusal per
// slot / stage, after its field references and weights — the order the
// runtime builds the attribute in.
func validateFormulaExpressions(env *descriptor.Envelope, req *types.Request, opts *PredictOptions) {
	if ce := formulaExpressionRefusal(req, opts); ce != nil {
		addCodedError(env, ce)
	}
}
