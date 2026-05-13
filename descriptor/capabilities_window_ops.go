package descriptor

import "github.com/frankbardon/pulse/types"

// windowCapabilities returns the metadata for every registered WIN_*
// operator. All window operators run on the post-aggregation row set
// and are buffered; Streamable is always false here.
func windowCapabilities() []Operator {
	return []Operator{
		{
			Name:        string(types.WIN_LAG),
			Category:    "window",
			Description: "Return the value of Field from the row N positions before the current row in the ordered partition.",
			Params: []Param{
				{Name: "offset", Type: "int", Required: false, Default: 1, Description: "Lookback offset (≥ 1)."},
				{Name: "default", Type: "any", Required: false, Description: "Substitute value when the lookback row does not exist."},
			},
			AcceptsTypes:  numericFieldTypesNoDecimal,
			EmitsType:     "f64",
			EmitsTypeNote: "one float per row (Default when offset crosses partition start)",
			Streamable:    false,
		},
		{
			Name:        string(types.WIN_LEAD),
			Category:    "window",
			Description: "Return the value of Field from the row N positions after the current row in the ordered partition.",
			Params: []Param{
				{Name: "offset", Type: "int", Required: false, Default: 1, Description: "Lookahead offset (≥ 1)."},
				{Name: "default", Type: "any", Required: false, Description: "Substitute value when the lookahead row does not exist."},
			},
			AcceptsTypes:  numericFieldTypesNoDecimal,
			EmitsType:     "f64",
			EmitsTypeNote: "one float per row",
			Streamable:    false,
		},
		{
			Name:          string(types.WIN_ROW_NUMBER),
			Category:      "window",
			Description:   "1-based row index within the ordered partition.",
			AcceptsTypes:  allCohortFieldTypes,
			EmitsType:     "u32",
			EmitsTypeNote: "one integer per row (resets per partition)",
			Streamable:    false,
		},
		{
			Name:          string(types.WIN_RANK),
			Category:      "window",
			Description:   "Sparse rank with gaps after ties (1, 2, 2, 4, …) within the ordered partition.",
			AcceptsTypes:  allCohortFieldTypes,
			EmitsType:     "u32",
			EmitsTypeNote: "one integer per row",
			Streamable:    false,
		},
		{
			Name:          string(types.WIN_DENSE_RANK),
			Category:      "window",
			Description:   "Dense rank with no gaps after ties (1, 2, 2, 3, …) within the ordered partition.",
			AcceptsTypes:  allCohortFieldTypes,
			EmitsType:     "u32",
			EmitsTypeNote: "one integer per row",
			Streamable:    false,
		},
		{
			Name:          string(types.WIN_RUNNING_SUM),
			Category:      "window",
			Description:   "Running total of Field over the configured Frame within the ordered partition.",
			AcceptsTypes:  numericFieldTypesNoDecimal,
			EmitsType:     "f64",
			EmitsTypeNote: "one float per row",
			Streamable:    false,
		},
		{
			Name:          string(types.WIN_RUNNING_AVG),
			Category:      "window",
			Description:   "Running average of Field over the configured Frame within the ordered partition.",
			AcceptsTypes:  numericFieldTypesNoDecimal,
			EmitsType:     "f64",
			EmitsTypeNote: "one float per row",
			Streamable:    false,
		},
		{
			Name:          string(types.WIN_MOVING_AVG),
			Category:      "window",
			Description:   "Moving average of Field over the configured Frame; Frame width must be bounded.",
			AcceptsTypes:  numericFieldTypesNoDecimal,
			EmitsType:     "f64",
			EmitsTypeNote: "one float per row (NaN when frame is empty)",
			Streamable:    false,
		},
		{
			Name:        string(types.WIN_EWMA),
			Category:    "window",
			Description: "Exponentially weighted moving average; current = α·value + (1−α)·previous.",
			Params: []Param{
				{Name: "alpha", Type: "float", Required: true, Description: "Smoothing factor in (0, 1]; higher values weight recent observations more."},
			},
			AcceptsTypes:  numericFieldTypesNoDecimal,
			EmitsType:     "f64",
			EmitsTypeNote: "one float per row",
			Streamable:    false,
		},
		{
			Name:        string(types.WIN_PCT_CHANGE),
			Category:    "window",
			Description: "Percent change relative to the row Periods positions earlier in the ordered partition.",
			Params: []Param{
				{Name: "periods", Type: "int", Required: false, Default: 1, Description: "Lookback offset (≥ 1) for the comparison row."},
			},
			AcceptsTypes:  numericFieldTypesNoDecimal,
			EmitsType:     "f64",
			EmitsTypeNote: "one float per row (NaN when previous value is zero or missing)",
			Streamable:    false,
		},
	}
}
