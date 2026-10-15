package descriptor

import (
	"github.com/frankbardon/pulse/internal/fieldaccept"
	"github.com/frankbardon/pulse/types"

	"github.com/frankbardon/pulse/descriptor"
)

// tableAcceptsTypes is a tabled operator's accepts_types: the
// accepted-types table's (internal/fieldaccept) field slot, which
// predict and the runtime both enforce, so the declaration cannot drift
// from the rule.
func tableAcceptsTypes[T ~string](t T) []string {
	return fieldaccept.TypeNames(string(t), fieldaccept.SlotField)
}

// windowCapabilities returns the metadata for every registered WIN_*
// operator. All window operators run on the post-aggregation row set
// and are buffered; Streamable is always false here.
func windowCapabilities() []descriptor.Operator {
	return []descriptor.Operator{
		{
			Name:        string(types.WIN_LAG),
			Category:    "window",
			Description: "Return the value of Field from the row N positions before the current row in the ordered partition.",
			Params: []descriptor.Param{
				{Name: "offset", Type: "int", Required: false, Default: 1, Description: "Lookback offset (≥ 1)."},
				{Name: "default", Type: "any", Required: false, Description: "Substitute value when the lookback row does not exist."},
			},
			AcceptsTypes:  tableAcceptsTypes(types.WIN_LAG),
			EmitsType:     "f64",
			EmitsTypeNote: "one float per row (Default when offset crosses partition start)",
			Streamable:    false,
		},
		{
			Name:        string(types.WIN_LEAD),
			Category:    "window",
			Description: "Return the value of Field from the row N positions after the current row in the ordered partition.",
			Params: []descriptor.Param{
				{Name: "offset", Type: "int", Required: false, Default: 1, Description: "Lookahead offset (≥ 1)."},
				{Name: "default", Type: "any", Required: false, Description: "Substitute value when the lookahead row does not exist."},
			},
			AcceptsTypes:  tableAcceptsTypes(types.WIN_LEAD),
			EmitsType:     "f64",
			EmitsTypeNote: "one float per row",
			Streamable:    false,
		},
		{
			Name:          string(types.WIN_ROW_NUMBER),
			Category:      "window",
			Description:   "1-based row index within the ordered partition.",
			AcceptsTypes:  nonVectorFieldTypes,
			EmitsType:     "u32",
			EmitsTypeNote: "one integer per row (resets per partition)",
			Streamable:    false,
		},
		{
			Name:          string(types.WIN_RANK),
			Category:      "window",
			Description:   "Sparse rank with gaps after ties (1, 2, 2, 4, …) within the ordered partition.",
			AcceptsTypes:  nonVectorFieldTypes,
			EmitsType:     "u32",
			EmitsTypeNote: "one integer per row",
			Streamable:    false,
		},
		{
			Name:          string(types.WIN_DENSE_RANK),
			Category:      "window",
			Description:   "Dense rank with no gaps after ties (1, 2, 2, 3, …) within the ordered partition.",
			AcceptsTypes:  nonVectorFieldTypes,
			EmitsType:     "u32",
			EmitsTypeNote: "one integer per row",
			Streamable:    false,
		},
		{
			Name:          string(types.WIN_RUNNING_SUM),
			Category:      "window",
			Description:   "Running total of Field over the configured Frame within the ordered partition.",
			AcceptsTypes:  tableAcceptsTypes(types.WIN_RUNNING_SUM),
			EmitsType:     "f64",
			EmitsTypeNote: "one float per row",
			Streamable:    false,
		},
		{
			Name:          string(types.WIN_RUNNING_AVG),
			Category:      "window",
			Description:   "Running average of Field over the configured Frame within the ordered partition.",
			AcceptsTypes:  tableAcceptsTypes(types.WIN_RUNNING_AVG),
			EmitsType:     "f64",
			EmitsTypeNote: "one float per row",
			Streamable:    false,
		},
		{
			Name:          string(types.WIN_MOVING_AVG),
			Category:      "window",
			Description:   "Moving average of Field over the configured Frame; Frame width must be bounded.",
			AcceptsTypes:  tableAcceptsTypes(types.WIN_MOVING_AVG),
			EmitsType:     "f64",
			EmitsTypeNote: "one float per row (null when every value in the frame is missing)",
			Streamable:    false,
		},
		{
			Name:        string(types.WIN_EWMA),
			Category:    "window",
			Description: "Exponentially weighted moving average; current = α·value + (1−α)·previous. Give exactly one of alpha or decay; alpha = 1 − decay.",
			Params: []descriptor.Param{
				{Name: "alpha", Type: "float", Required: false, Description: "Smoothing factor in (0, 1]; higher values weight recent observations more. Exactly one of alpha or decay; alpha = 1 − decay.", ExactlyOneOf: "smoothing"},
				{Name: "decay", Type: "float", Required: false, Description: "Carry-over share in [0, 1) (adstock decay); alpha = 1 − decay, taken from the exact decimal. Exactly one of alpha or decay.", ExactlyOneOf: "smoothing"},
			},
			AcceptsTypes:  tableAcceptsTypes(types.WIN_EWMA),
			EmitsType:     "f64",
			EmitsTypeNote: "one float per row",
			// Cumulative over the ordered partition: the required frame
			// is never read.
			IgnoresFrame: true,
			Streamable:   false,
		},
		{
			Name:        string(types.WIN_PCT_CHANGE),
			Category:    "window",
			Description: "Fractional change, (current − previous) / previous, against the row Periods positions earlier in the ordered partition: 0.05 is a 5% rise, never multiplied by 100.",
			Params: []descriptor.Param{
				{Name: "periods", Type: "int", Required: false, Default: 1, Description: "Lookback offset (≥ 1) for the comparison row."},
			},
			AcceptsTypes:  tableAcceptsTypes(types.WIN_PCT_CHANGE),
			EmitsType:     "f64",
			EmitsTypeNote: "one float per row; null (not NaN) when the previous value is zero or missing",
			Streamable:    false,
		},
		{
			Name:        string(types.WIN_DELTA),
			Category:    "window",
			Description: "Point DIFFERENCE (current minus the row Periods positions earlier in the ordered partition), in the field's own units. The subtraction counterpart of WIN_PCT_CHANGE, which emits a RATIO instead; a zero previous value is a legitimate delta here and is never nulled.",
			Params: []descriptor.Param{
				{Name: "periods", Type: "int", Required: false, Default: 1, Description: "Lookback offset (≥ 1) for the comparison row."},
			},
			AcceptsTypes:  tableAcceptsTypes(types.WIN_DELTA),
			EmitsType:     "f64",
			EmitsTypeNote: "one float per row; null (not NaN) when no comparison row exists — the first Periods rows of each partition, or a missing value on either side",
			Streamable:    false,
		},
	}
}
