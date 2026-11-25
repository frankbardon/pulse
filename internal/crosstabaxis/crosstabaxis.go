// Package crosstabaxis holds the structural crosstab-axis refusals both
// the request validator at run time (internal/processing,
// validateCrosstabSpec) and predict (internal/descriptor,
// validateCrosstab) raise, so the two refuse the same specs with the
// same code, message and details. It imports only types and errors:
// predict may not import the engine (TestPredictNoExecutionImports), so
// the rule lives in a leaf both reach (TestCrosstabAxis_ImportBoundary).
package crosstabaxis

import (
	"strconv"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// NilGrouperRefusals returns one PROCESSING_CONFIG refusal per null
// grouper on the row axis, then the column axis, in position order
// (`crosstab.rows: [null]` decodes to a nil *types.Group). Every arm
// refuses it up front: the buffered partitioner and the fused keyer both
// read each axis slot, and a null one has no field to key on. Details
// are {axis, index}. Nil for a nil spec or one with no null slot.
func NilGrouperRefusals(spec *types.CrosstabSpec) []*errors.CodedError {
	if spec == nil {
		return nil
	}
	var out []*errors.CodedError
	walk := func(axis string, groups []*types.Group) {
		for i, g := range groups {
			if g == nil {
				out = append(out, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
					"crosstab "+axis+" contains a null grouper at index "+strconv.Itoa(i),
					map[string]any{"axis": axis, "index": i}))
			}
		}
	}
	walk("rows", spec.Rows)
	walk("columns", spec.Columns)
	return out
}
