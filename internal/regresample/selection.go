package regresample

import (
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// CheckSelectionCriterion is the rule that a selection names its
// criterion — SERVICE_VALIDATION, details {selection}, when spec sets
// selection without criterion. Predict applies it to every regression
// (internal/descriptor.RegressionSelectionCriterionRefusal); the
// extension adapter and the REG_MULTINOMIAL engine apply the same call
// before their selection driver runs.
func CheckSelectionCriterion(spec *types.RegressionSpec) *errors.CodedError {
	if spec == nil || spec.Selection == "" || spec.Criterion != "" {
		return nil
	}
	return errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
		"regression Selection requires a Criterion (aic | bic)",
		map[string]any{"selection": spec.Selection})
}
