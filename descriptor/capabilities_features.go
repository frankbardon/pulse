package descriptor

import "github.com/frankbardon/pulse/types"

var categoricalFieldTypes = []string{
	"categorical_u16",
	"categorical_u32",
	"categorical_u8",
}

// featureCapabilities returns the metadata for every registered
// feature.Computer.
func featureCapabilities() []Operator {
	return []Operator{
		{
			Name:          string(types.FEAT_LOG),
			Category:      "feature",
			Description:   "Per-row natural log of Field; emits one f64 column.",
			AcceptsTypes:  numericFieldTypesNoDecimal,
			EmitsType:     "f64",
			EmitsTypeNote: "single column \"<label>\" (default LOG_<field>)",
			Streamable:    true,
		},
		{
			Name:          string(types.FEAT_SQRT),
			Category:      "feature",
			Description:   "Per-row square root of Field; emits one f64 column.",
			AcceptsTypes:  numericFieldTypesNoDecimal,
			EmitsType:     "f64",
			EmitsTypeNote: "single column \"<label>\" (default SQRT_<field>)",
			Streamable:    true,
		},
		{
			Name:        string(types.FEAT_BUCKETIZE),
			Category:    "feature",
			Description: "Bin a numeric column into ordered buckets using explicit boundaries or N equal-population quantiles.",
			Params: []Param{
				{Name: "boundaries", Type: "list", Required: false, Description: "Sorted ascending list of bucket cutpoints (mutually exclusive with quantiles)."},
				{Name: "quantiles", Type: "int", Required: false, Description: "Number of equal-population buckets (mutually exclusive with boundaries)."},
			},
			AcceptsTypes:  numericFieldTypesNoDecimal,
			EmitsType:     "u32",
			EmitsTypeNote: "single column \"<label>\" with bucket index per row",
			Streamable:    true,
		},
		{
			Name:          string(types.FEAT_DATE_FEATURES),
			Category:      "feature",
			Description:   "Expand a date field into year, month, day, day_of_week, and is_weekend columns.",
			AcceptsTypes:  []string{"date"},
			EmitsTypeNote: "multiple columns: <label>_year, <label>_month, <label>_day, <label>_day_of_week, <label>_is_weekend",
			Streamable:    true,
		},
		{
			Name:          string(types.FEAT_ONE_HOT),
			Category:      "feature",
			Description:   "One-hot encode a categorical field as one f32 column per dictionary entry.",
			AcceptsTypes:  categoricalFieldTypes,
			EmitsTypeNote: "one column per dictionary entry, prefixed by label",
			Streamable:    true,
		},
		{
			Name:        string(types.FEAT_POLY),
			Category:    "feature",
			Description: "Per-row polynomial expansion of a numeric field; emits Degree-1 derived columns x_2 .. x_Degree. Pair with REG_OLS to fit polynomial regression.",
			Params: []Param{
				{Name: "degree", Type: "int", Required: true, Description: "Polynomial degree; must be >= 2 and <= 10. Degree 1 (linear) is the original column."},
			},
			AcceptsTypes:  numericFieldTypesNoDecimal,
			EmitsType:     "f64",
			EmitsTypeNote: "columns \"<label>_2\" .. \"<label>_<Degree>\" (default prefix <field>_poly)",
			Streamable:    true,
		},
		{
			Name:          string(types.FEAT_FREQUENCY_ENCODE),
			Category:      "feature",
			Description:   "Replace each categorical value with its observed relative frequency in the cohort.",
			AcceptsTypes:  categoricalFieldTypes,
			EmitsType:     "f32",
			EmitsTypeNote: "single column \"<label>\" with frequency per row",
			Streamable:    true,
		},
		{
			Name:        string(types.FEAT_TARGET_ENCODE),
			Category:    "feature",
			Description: "Replace each categorical value with the smoothed mean of a numeric Target field for that category.",
			Params: []Param{
				{Name: "target", Type: "field", Required: true, FieldFilter: "numeric", Description: "Numeric field whose grouped mean is encoded into the output column."},
				{Name: "smoothing", Type: "float", Required: false, Default: 0.0, Description: "Bayesian smoothing weight blending category mean with global mean."},
			},
			AcceptsTypes:  categoricalFieldTypes,
			EmitsType:     "f32",
			EmitsTypeNote: "single column \"<label>\"; risk of target leakage on non-CV pipelines",
			Streamable:    true,
		},
		{
			Name:        string(types.FEAT_TRAIN_TEST_SPLIT),
			Category:    "feature",
			Description: "Emit a deterministic split assignment column (0=train, 1=val, 2=test) for ML workflows.",
			Params: []Param{
				{Name: "ratios", Type: "list", Required: true, Description: "Two- or three-element ratio list summing to ~1.0."},
				{Name: "seed", Type: "int", Required: false, Default: 0, Description: "RNG seed; same seed produces byte-identical assignments."},
				{Name: "stratify", Type: "field", Required: false, FieldFilter: "categorical", Description: "Optional categorical field; ratios are applied per category for class balance."},
			},
			AcceptsTypes:  allCohortFieldTypes,
			EmitsType:     "u8",
			EmitsTypeNote: "single column \"<label>\" with split assignment per row",
			Streamable:    true,
		},
	}
}
