package descriptor

import (
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// validateCrosstab walks a CrosstabSpec against the cohort schema and
// adds structural errors / streamability reasons / warnings to the
// envelope. Called from Predict; predict's existing field-reference
// walk (validateRequestFields) sees req.Crosstab.Rows / Columns / Cell
// fields lifted up via predictCrosstabProjection so unknown-field
// errors fire there. This helper handles the crosstab-specific
// structural and normalization invariants.
func validateCrosstab(env *Envelope, req *types.Request, schema *encoding.Schema, opts *PredictOptions) {
	spec := req.Crosstab
	if spec == nil {
		return
	}

	if len(spec.Rows) == 0 {
		env.AddError(string(errors.PULSE_CROSSTAB_EMPTY_ROWS),
			"crosstab requires at least one row-axis grouper",
			nil)
	}
	if len(spec.Columns) == 0 {
		env.AddError(string(errors.PULSE_CROSSTAB_EMPTY_COLUMNS),
			"crosstab requires at least one column-axis grouper",
			nil)
	}
	if spec.Cell == nil {
		env.AddError(string(errors.PULSE_CROSSTAB_MISSING_CELL),
			"crosstab requires a Cell aggregation",
			nil)
	}
	if len(req.Groups) > 0 || len(req.Aggregations) > 0 {
		env.AddError(string(errors.PULSE_CROSSTAB_CONFLICTS_WITH_GROUPS),
			"crosstab cannot coexist with top-level groups or aggregations on the same request",
			map[string]any{
				"groups":       len(req.Groups),
				"aggregations": len(req.Aggregations),
			})
	}
	if !types.IsValidNormalize(spec.Normalize) {
		env.AddError(string(errors.SERVICE_VALIDATION),
			"crosstab normalize must be one of: none, row, column, total",
			map[string]any{"normalize": string(spec.Normalize)})
	}
	if !types.IsValidShape(spec.Shape) {
		env.AddError(string(errors.SERVICE_VALIDATION),
			"crosstab shape must be one of: matrix, long",
			map[string]any{"shape": string(spec.Shape)})
	}
	if spec.NormalizeLevel != nil {
		mode := spec.NormalizeOrDefault()
		switch mode {
		case types.CrosstabNormalizeNone:
			env.AddError(string(errors.PULSE_CROSSTAB_NORMALIZE_LEVEL_WITHOUT_NESTED_AXIS),
				"crosstab normalize_level requires Normalize to be row or column",
				map[string]any{"normalize": string(mode), "normalize_level": *spec.NormalizeLevel})
		case types.CrosstabNormalizeTotal:
			env.AddError(string(errors.PULSE_CROSSTAB_NORMALIZE_LEVEL_INCOMPATIBLE),
				"crosstab normalize_level cannot be combined with Normalize=total",
				map[string]any{"normalize": string(mode), "normalize_level": *spec.NormalizeLevel})
		case types.CrosstabNormalizeRow:
			if axisLen := len(spec.Rows); axisLen > 0 {
				if lvl := *spec.NormalizeLevel; lvl < 0 || lvl >= axisLen {
					env.AddError(string(errors.PULSE_CROSSTAB_NORMALIZE_LEVEL_OUT_OF_RANGE),
						"crosstab normalize_level is out of range for the row axis",
						map[string]any{"normalize_level": lvl, "axis": "rows", "axis_len": axisLen})
				}
			}
		case types.CrosstabNormalizeColumn:
			if axisLen := len(spec.Columns); axisLen > 0 {
				if lvl := *spec.NormalizeLevel; lvl < 0 || lvl >= axisLen {
					env.AddError(string(errors.PULSE_CROSSTAB_NORMALIZE_LEVEL_OUT_OF_RANGE),
						"crosstab normalize_level is out of range for the column axis",
						map[string]any{"normalize_level": lvl, "axis": "columns", "axis_len": axisLen})
				}
			}
		}
	}

	// Walk axis field references; emit unknown-field errors with the
	// same surface predict uses for the top-level Group slot. The
	// projected column set augments the schema with feature outputs
	// so derived columns are addressable on either axis.
	projected := map[string]bool{}
	for _, feat := range req.Features {
		if feat != nil && feat.Label != "" {
			projected[feat.Label] = true
		}
	}
	checkField := func(field, role string) {
		if field == "" {
			return
		}
		if schema.Field(field) != nil || projected[field] {
			return
		}
		env.AddError(string(errors.SERVICE_VALIDATION),
			"crosstab "+role+" references unknown field: "+field,
			map[string]any{"field": field, "role": role})
	}
	for _, g := range spec.Rows {
		if g != nil {
			checkField(g.Field, "row grouper")
		}
	}
	for _, g := range spec.Columns {
		if g != nil {
			checkField(g.Field, "column grouper")
		}
	}
	if spec.Cell != nil {
		checkField(spec.Cell.Field, "cell aggregation")
		// Numeric aggregation on categorical field — promote per
		// strict (predict honours opts.Strict here too).
		if f := schema.Field(spec.Cell.Field); f != nil && f.Type.IsCategorical() && numericAggregations[spec.Cell.Type] {
			entry := &EnvelopeEntry{
				Code:    string(errors.PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL),
				Message: "numeric aggregation " + string(spec.Cell.Type) + " is not meaningful for categorical field " + spec.Cell.Field,
				Details: map[string]any{"field": spec.Cell.Field, "aggregation": string(spec.Cell.Type)},
			}
			if opts.Strict {
				env.Errors = append(env.Errors, entry)
			} else {
				env.Warnings = append(env.Warnings, entry)
			}
		}
		// Warn when normalization is requested on a recompute aggregator
		// without the implied margin: harmless under v1's always-
		// recompute path, but flag so the warning trail is informative.
		mode := spec.NormalizeOrDefault()
		if mode != types.CrosstabNormalizeNone && spec.Cell.Type.MarginReducibility() == types.MarginRecompute {
			env.AddWarning(string(errors.PULSE_CROSSTAB_NORMALIZE_UNSATISFIABLE),
				"normalize="+string(mode)+" on a recompute-margin aggregator ("+string(spec.Cell.Type)+") requires recomputing the margin over raw rows; v1 does this automatically but cost is non-trivial",
				map[string]any{"aggregation": string(spec.Cell.Type), "normalize": string(mode)})
		}
		// Reject normalize × map-valued aggregator. Map cells
		// (AGG_SET_FREQUENCY today) cannot be divided by a margin —
		// the operation is undefined.
		if mode != types.CrosstabNormalizeNone && spec.Cell.Type.MapValued() {
			env.AddError(string(errors.PULSE_CROSSTAB_NORMALIZE_MAP_VALUED),
				"crosstab normalize="+string(mode)+" is incompatible with map-valued cell aggregator "+string(spec.Cell.Type)+
					" — drop normalize or pick a scalar aggregator",
				map[string]any{"aggregation": string(spec.Cell.Type), "normalize": string(mode)})
		}
	}
}

// crosstabStreamableReasons reports why a Crosstab request cannot stream.
// Mirrors the buffered gate inside types.CrosstabSpec.IsBuffered and
// adds reasons individually so the envelope's StreamableReasons slice
// stays informative.
func crosstabStreamableReasons(spec *types.CrosstabSpec) []string {
	if spec == nil {
		return nil
	}
	var reasons []string
	if spec.ShapeOrDefault() == types.CrosstabShapeMatrix {
		reasons = append(reasons, "crosstab shape=matrix requires buffered reshape")
	}
	if spec.Margins.Rows || spec.Margins.Columns || spec.Margins.Grand {
		reasons = append(reasons, "crosstab margins require buffered recompute")
	}
	if spec.NormalizeOrDefault() != types.CrosstabNormalizeNone {
		reasons = append(reasons, "crosstab normalize requires buffered margin computation")
	}
	if len(spec.Rows) > 1 || len(spec.Columns) > 1 {
		reasons = append(reasons, "crosstab with nested axes requires buffered recursive partitioning")
	}
	return reasons
}
