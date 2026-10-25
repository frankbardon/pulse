package model

import (
	"fmt"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// UnknownRegressionType is the refusal of a regression slot whose type no
// registry on the instance resolves — never registered, hidden by a
// feature profile, or a pack model whose extension is not registered.
// index is the slot's position in Request.Regressions. Predict
// (internal/descriptor) and the engine (internal/processing/regression)
// both raise exactly this error: one code, one sentence, one details map.
func UnknownRegressionType(index int, typ types.RegressionType) *errors.CodedError {
	return errors.NewCodedErrorWithDetails(errors.PULSE_REGRESSION_INVALID,
		fmt.Sprintf("regression[%d]: unknown regression type %s", index, typ),
		map[string]any{"regression_index": index, "type": string(typ)})
}
