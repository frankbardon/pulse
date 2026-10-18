package descriptor

import (
	"github.com/frankbardon/pulse/internal/fieldaccept"
	"github.com/frankbardon/pulse/types"

	"github.com/frankbardon/pulse/descriptor"
)

// featureCapabilities returns the metadata for every registered
// feature.Computer.
func featureCapabilities() []descriptor.Operator {
	return []descriptor.Operator{
		{
			Name:          string(types.FEAT_LOG),
			Category:      "feature",
			Description:   "Per-row natural log of 1 + Field (log1p); x <= -1 reads null; emits one f64 column.",
			AcceptsTypes:  tableAcceptsTypes(types.FEAT_LOG),
			EmitsType:     "f64",
			EmitsTypeNote: "single column \"<label>\" (default LOG_<field>)",
			Streamable:    true,
		},
		{
			Name:          string(types.FEAT_SQRT),
			Category:      "feature",
			Description:   "Per-row square root of Field; emits one f64 column.",
			AcceptsTypes:  tableAcceptsTypes(types.FEAT_SQRT),
			EmitsType:     "f64",
			EmitsTypeNote: "single column \"<label>\" (default SQRT_<field>)",
			Streamable:    true,
		},
		{
			Name:        string(types.FEAT_BUCKETIZE),
			Category:    "feature",
			Description: "Bin a numeric column into ordered buckets using explicit boundaries or N equal-population quantiles.",
			Params: []descriptor.Param{
				{Name: "boundaries", Type: "list", Required: false, Description: "Sorted ascending list of bucket cutpoints (mutually exclusive with quantiles).", ExactlyOneOf: "cutpoints"},
				{Name: "quantiles", Type: "int", Required: false, Description: "Number of equal-population buckets (mutually exclusive with boundaries).", ExactlyOneOf: "cutpoints"},
			},
			AcceptsTypes:  tableAcceptsTypes(types.FEAT_BUCKETIZE),
			EmitsType:     "u32",
			EmitsTypeNote: "single column \"<label>\" with bucket index per row",
			Streamable:    true,
		},
		{
			Name:          string(types.FEAT_DATE_FEATURES),
			Category:      "feature",
			Description:   "Expand a date or datetime field into year, month, day, day-of-week (dow, ISO: 1 = Monday .. 7 = Sunday) and quarter columns, plus hour over a datetime; a datetime reads the wall clock of its resolved time zone.",
			AcceptsTypes:  []string{"date", "datetime"},
			EmitsTypeNote: "multiple columns: <label>_year, <label>_month, <label>_day, <label>_dow, <label>_quarter; a datetime adds <label>_hour",
			Streamable:    true,
		},
		{
			Name:          string(types.FEAT_ONE_HOT),
			Category:      "feature",
			Description:   "One-hot encode a categorical field as one f32 column per dictionary entry; a packed_bool's levels are \"0\" / \"1\".",
			AcceptsTypes:  fieldaccept.TypeNames(string(types.FEAT_ONE_HOT), fieldaccept.SlotField),
			EmitsTypeNote: "one column per level (dictionary entry, or \"0\" / \"1\" for a packed_bool), prefixed by label",
			Streamable:    true,
		},
		{
			Name:        string(types.FEAT_POLY),
			Category:    "feature",
			Description: "Per-row polynomial expansion of a numeric field; emits Degree-1 derived columns x_2 .. x_Degree. Pair with REG_OLS to fit polynomial regression.",
			Params: []descriptor.Param{
				{Name: "degree", Type: "int", Required: true, Description: "Polynomial degree; must be >= 2 and <= 10. Degree 1 (linear) is the original column."},
			},
			AcceptsTypes:  fieldaccept.TypeNames(string(types.FEAT_POLY), fieldaccept.SlotField),
			EmitsType:     "f64",
			EmitsTypeNote: "columns \"<label>_2\" .. \"<label>_<Degree>\" (default prefix <field>_poly)",
			Streamable:    true,
		},
		{
			Name:          string(types.FEAT_FREQUENCY_ENCODE),
			Category:      "feature",
			Description:   "Replace each categorical value (or packed_bool level \"0\" / \"1\") with its observed relative frequency in the cohort.",
			AcceptsTypes:  fieldaccept.TypeNames(string(types.FEAT_FREQUENCY_ENCODE), fieldaccept.SlotField),
			EmitsType:     "f32",
			EmitsTypeNote: "single column \"<label>\" with frequency per row",
			Streamable:    true,
		},
		{
			Name:        string(types.FEAT_TARGET_ENCODE),
			Category:    "feature",
			Description: "Replace each categorical value (or packed_bool level \"0\" / \"1\") with the smoothed mean of a numeric Target field for that category.",
			Params: []descriptor.Param{
				{Name: "target", Type: "field", Required: true, FieldFilter: "numeric", Description: "Numeric field whose grouped mean is encoded into the output column."},
				{Name: "smoothing", Type: "float", Required: false, Default: 0.0, Description: "Bayesian smoothing weight blending category mean with global mean."},
				{Name: "fit_on", Type: "string", Required: false, Description: "Boolean expression (expression-filter syntax) choosing the training rows, e.g. \"split == 0\"; may read earlier features' columns. Only training rows feed the means; every row is encoded, an unseen category with the training global mean. Silences PULSE_FEAT_TARGET_LEAKAGE_RISK."},
				{Name: "folds", Type: "int", Required: false, Default: 0, Description: "0 (off) or 2..100: encode each training row out-of-fold, from the training rows outside its fold; other rows get the full-training encoding."},
				{Name: "rng_seed", Type: "int", Required: false, Description: "Seed of the fold assignment; only valid with folds."},
			},
			AcceptsTypes:  fieldaccept.TypeNames(string(types.FEAT_TARGET_ENCODE), fieldaccept.SlotField),
			EmitsType:     "f32",
			EmitsTypeNote: "single column \"<label>\"; leaks the holdout's outcomes unless fit_on names the training rows",
			Streamable:    true,
		},
		{
			Name:        string(types.FEAT_TRAIN_TEST_SPLIT),
			Category:    "feature",
			Description: "Emit a deterministic split assignment column (0=train, 1=val, 2=test) for ML workflows.",
			Params: []descriptor.Param{
				{Name: "ratios", Type: "list", Required: true, Description: "Two- or three-element ratio list summing to ~1.0."},
				{Name: "seed", Type: "int", Required: false, Default: 0, Description: "RNG seed; same seed produces byte-identical assignments."},
				{Name: "stratify", Type: "field", Required: false, FieldFilter: "categorical", Description: "Optional categorical or packed_bool field; ratios are applied per level for class balance."},
			},
			AcceptsTypes:  nonVectorFieldTypes,
			EmitsType:     "u8",
			EmitsTypeNote: "single column \"<label>\" with split assignment per row",
			Streamable:    true,
		},
	}
}
