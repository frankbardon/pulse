package descriptor

import (
	"encoding/json"
	"strconv"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// windowFrameRequired lists window types that REQUIRE a frame specification.
var windowFrameRequired = map[types.WindowType]bool{
	types.WIN_RUNNING_SUM: true,
	types.WIN_RUNNING_AVG: true,
	types.WIN_MOVING_AVG:  true,
	types.WIN_EWMA:        true,
}

// windowFrameRejected lists window types that REJECT a frame specification.
var windowFrameRejected = map[types.WindowType]bool{
	types.WIN_LAG:        true,
	types.WIN_LEAD:       true,
	types.WIN_ROW_NUMBER: true,
	types.WIN_RANK:       true,
	types.WIN_DENSE_RANK: true,
	types.WIN_PCT_CHANGE: true,
}

// windowNumericFieldRequired lists window types whose source field MUST be numeric.
// ROW_NUMBER, RANK, DENSE_RANK do not read a value field, so they are not in this map.
var windowNumericFieldRequired = map[types.WindowType]bool{
	types.WIN_LAG:         true,
	types.WIN_LEAD:        true,
	types.WIN_RUNNING_SUM: true,
	types.WIN_RUNNING_AVG: true,
	types.WIN_MOVING_AVG:  true,
	types.WIN_EWMA:        true,
	types.WIN_PCT_CHANGE:  true,
}

// windowFieldRequired reports whether the operator requires a Field at all.
// ROW_NUMBER / RANK / DENSE_RANK have no value field.
var windowFieldRequired = map[types.WindowType]bool{
	types.WIN_LAG:         true,
	types.WIN_LEAD:        true,
	types.WIN_RUNNING_SUM: true,
	types.WIN_RUNNING_AVG: true,
	types.WIN_MOVING_AVG:  true,
	types.WIN_EWMA:        true,
	types.WIN_PCT_CHANGE:  true,
}

// orderableFieldTypes lists encoding field types that can serve as ORDER BY keys.
// Categorical types and bit-packed boolean types are excluded.
func isOrderableType(ft encoding.FieldType) bool {
	switch ft {
	case encoding.FieldTypeU8, encoding.FieldTypeU16, encoding.FieldTypeU32, encoding.FieldTypeU64,
		encoding.FieldTypeF32, encoding.FieldTypeF64,
		encoding.FieldTypeNullableU4, encoding.FieldTypeNullableU8, encoding.FieldTypeNullableU16,
		encoding.FieldTypeDate:
		return true
	}
	return false
}

// isNumericType reports whether the field type carries a meaningful scalar value
// for arithmetic operations (sum, avg, lag, ewma, etc.).
func isNumericType(ft encoding.FieldType) bool {
	switch ft {
	case encoding.FieldTypeU8, encoding.FieldTypeU16, encoding.FieldTypeU32, encoding.FieldTypeU64,
		encoding.FieldTypeF32, encoding.FieldTypeF64,
		encoding.FieldTypeNullableU4, encoding.FieldTypeNullableU8, encoding.FieldTypeNullableU16,
		encoding.FieldTypeDate:
		return true
	}
	return false
}

// availableOutputColumns returns the set of column names that will be
// present on response rows after the pipeline runs. Includes:
//   - every schema field
//   - every aggregation/attribute output label (or default <TYPE>_<field>)
//   - every group-by field name
//   - every window output label
//
// Used by validateSort to confirm that Request.Sort references a real column.
func availableOutputColumns(req *types.Request, schema *encoding.Schema) map[string]bool {
	out := make(map[string]bool, len(schema.Fields)+len(req.Aggregations)+len(req.Attributes)+len(req.Groups)+len(req.Windows))
	for i := range schema.Fields {
		out[schema.Fields[i].Name] = true
	}
	for _, agg := range req.Aggregations {
		label := agg.Label
		if label == "" {
			label = string(agg.Type) + "_" + agg.Field
		}
		out[label] = true
	}
	for _, attr := range req.Attributes {
		label := attr.Label
		if label == "" {
			label = string(attr.Type) + "_" + attr.Field
		}
		out[label] = true
	}
	for _, grp := range req.Groups {
		out[grp.Field] = true
	}
	for _, w := range req.Windows {
		out[windowLabel(w)] = true
	}
	return out
}

// validateSort checks that every Request.Sort key references an available
// output column. Missing-field rejections use SERVICE_VALIDATION (mirrors
// other validators).
func validateSort(env *Envelope, req *types.Request, schema *encoding.Schema) {
	if len(req.Sort) == 0 {
		return
	}
	available := availableOutputColumns(req, schema)
	for i, k := range req.Sort {
		idx := strconv.Itoa(i)
		if k.Field == "" {
			env.AddError(
				string(errors.SERVICE_VALIDATION),
				"sort["+idx+"]: field is required",
				map[string]any{"sort_index": i},
			)
			continue
		}
		if !available[k.Field] {
			env.AddError(
				string(errors.SERVICE_VALIDATION),
				"sort["+idx+"]: field "+k.Field+" is not produced by the pipeline (no schema field, aggregation, attribute, group, or window output matches)",
				map[string]any{"sort_index": i, "field": k.Field},
			)
		}
	}
}

// validateWindows applies the predict-side validation rules for req.Windows.
// All rejections produce errors via env.AddError. The function does not execute
// any window logic; it inspects only the schema and the request.
func validateWindows(env *Envelope, req *types.Request, schema *encoding.Schema, opts *PredictOptions) {
	_ = opts

	// Build the set of valid window types once.
	validTypes := make(map[types.WindowType]bool, len(types.AllWindowTypes()))
	for _, t := range types.AllWindowTypes() {
		validTypes[t] = true
	}

	// Build the set of labels already produced by aggregations / groups / attributes.
	usedLabels := make(map[string]string)
	for _, agg := range req.Aggregations {
		label := agg.Label
		if label == "" {
			label = string(agg.Type) + "_" + agg.Field
		}
		usedLabels[label] = "aggregation"
	}
	for _, attr := range req.Attributes {
		label := attr.Label
		if label == "" {
			label = string(attr.Type) + "_" + attr.Field
		}
		usedLabels[label] = "attribute"
	}
	for _, grp := range req.Groups {
		usedLabels[grp.Field] = "group"
	}

	for i, w := range req.Windows {
		idx := strconv.Itoa(i)

		// Unknown type.
		if !validTypes[w.Type] && !isExtensionWindowType(opts, w.Type) {
			env.AddError(
				string(errors.PULSE_WINDOW_INVALID),
				"window["+idx+"]: unknown window type "+string(w.Type),
				map[string]any{"window_index": i, "type": string(w.Type)},
			)
			continue
		}

		// OrderBy required (≥1).
		if len(w.OrderBy) == 0 {
			env.AddError(
				string(errors.PULSE_WINDOW_INVALID),
				"window["+idx+"] ("+string(w.Type)+"): order_by is required (at least one key)",
				map[string]any{"window_index": i, "type": string(w.Type)},
			)
		}

		// OrderBy fields must exist and be orderable.
		for j, ok := range w.OrderBy {
			if ok.Field == "" {
				env.AddError(
					string(errors.PULSE_WINDOW_INVALID),
					"window["+idx+"]: order_by["+strconv.Itoa(j)+"] missing field",
					map[string]any{"window_index": i, "order_index": j},
				)
				continue
			}
			f := schema.Field(ok.Field)
			if f == nil {
				env.AddError(
					string(errors.PULSE_WINDOW_INVALID),
					"window["+idx+"]: order_by field "+ok.Field+" does not exist in schema",
					map[string]any{"window_index": i, "field": ok.Field},
				)
				continue
			}
			if !isOrderableType(f.Type) {
				env.AddError(
					string(errors.PULSE_WINDOW_INVALID),
					"window["+idx+"]: order_by field "+ok.Field+" is not orderable (type "+f.Type.String()+")",
					map[string]any{"window_index": i, "field": ok.Field, "field_type": f.Type.String()},
				)
			}
		}

		// PartitionBy fields must exist.
		for _, p := range w.PartitionBy {
			if p == "" {
				continue
			}
			if schema.Field(p) == nil {
				env.AddError(
					string(errors.PULSE_WINDOW_INVALID),
					"window["+idx+"]: partition_by field "+p+" does not exist in schema",
					map[string]any{"window_index": i, "field": p},
				)
			}
		}

		// Field required for value-bearing operators; missing field is SERVICE_VALIDATION
		// to share semantics with the existing validators.
		if windowFieldRequired[w.Type] {
			if w.Field == "" {
				env.AddError(
					string(errors.SERVICE_VALIDATION),
					"window["+idx+"] ("+string(w.Type)+"): field is required",
					map[string]any{"window_index": i, "type": string(w.Type)},
				)
			} else if f := schema.Field(w.Field); f == nil {
				// Aggregation / attribute / group labels project into the
				// post-aggregate row set windows operate on; only reject
				// when the name is unknown in both schema and pipeline
				// outputs.
				if _, projected := usedLabels[w.Field]; !projected {
					env.AddError(
						string(errors.SERVICE_VALIDATION),
						"window["+idx+"] ("+string(w.Type)+"): field "+w.Field+" does not exist in schema or upstream pipeline output",
						map[string]any{"window_index": i, "field": w.Field, "type": string(w.Type)},
					)
				}
			} else if windowNumericFieldRequired[w.Type] && !isNumericType(f.Type) {
				env.AddError(
					string(errors.PULSE_WINDOW_INVALID),
					"window["+idx+"] ("+string(w.Type)+"): field "+w.Field+" must be numeric (got "+f.Type.String()+")",
					map[string]any{"window_index": i, "field": w.Field, "field_type": f.Type.String()},
				)
			}
		}

		// Frame matrix.
		if w.Frame != nil && windowFrameRejected[w.Type] {
			env.AddError(
				string(errors.PULSE_WINDOW_INVALID),
				"window["+idx+"] ("+string(w.Type)+"): frame is not allowed for this operator",
				map[string]any{"window_index": i, "type": string(w.Type)},
			)
		}
		if w.Frame == nil && windowFrameRequired[w.Type] {
			env.AddError(
				string(errors.PULSE_WINDOW_INVALID),
				"window["+idx+"] ("+string(w.Type)+"): frame is required",
				map[string]any{"window_index": i, "type": string(w.Type)},
			)
		}
		if w.Frame != nil {
			if w.Frame.Mode != "rows" {
				env.AddError(
					string(errors.PULSE_WINDOW_INVALID),
					"window["+idx+"] ("+string(w.Type)+"): frame.mode must be \"rows\" (got "+strconv.Quote(w.Frame.Mode)+")",
					map[string]any{"window_index": i, "mode": w.Frame.Mode},
				)
			}
			if w.Type == types.WIN_MOVING_AVG {
				if w.Frame.Preceding == nil || w.Frame.Following == nil {
					env.AddError(
						string(errors.PULSE_WINDOW_INVALID),
						"window["+idx+"] (WIN_MOVING_AVG): frame must have bounded preceding AND following",
						map[string]any{"window_index": i},
					)
				}
			}
			if w.Frame.Preceding != nil && *w.Frame.Preceding < 0 {
				env.AddError(
					string(errors.PULSE_WINDOW_INVALID),
					"window["+idx+"]: frame.preceding must be >= 0",
					map[string]any{"window_index": i, "preceding": *w.Frame.Preceding},
				)
			}
			if w.Frame.Following != nil && *w.Frame.Following < 0 {
				env.AddError(
					string(errors.PULSE_WINDOW_INVALID),
					"window["+idx+"]: frame.following must be >= 0",
					map[string]any{"window_index": i, "following": *w.Frame.Following},
				)
			}
		}

		// Operator-specific param validation.
		validateWindowParams(env, i, w)

		// Label collision check (warning-level error per plan: predict warning).
		label := windowLabel(w)
		if other, exists := usedLabels[label]; exists {
			entry := &EnvelopeEntry{
				Code:    string(errors.PULSE_WINDOW_INVALID),
				Message: "window[" + idx + "] label " + label + " collides with " + other + " label",
				Details: map[string]any{"window_index": i, "label": label, "collides_with": other},
			}
			env.Warnings = append(env.Warnings, entry)
		}
		usedLabels[label] = "window"
	}
}

// windowLabel returns the effective output column name for a window.
func windowLabel(w *types.Window) string {
	if w.Label != "" {
		return w.Label
	}
	if w.Field == "" {
		return string(w.Type)
	}
	return string(w.Type) + "_" + w.Field
}

// validateWindowParams checks operator-specific Params bounds.
func validateWindowParams(env *Envelope, i int, w *types.Window) {
	idx := strconv.Itoa(i)
	switch w.Type {
	case types.WIN_EWMA:
		var p struct {
			Alpha *float64 `json:"alpha"`
		}
		if len(w.Params) > 0 {
			if err := json.Unmarshal(w.Params, &p); err != nil {
				env.AddError(
					string(errors.PULSE_WINDOW_INVALID),
					"window["+idx+"] (WIN_EWMA): malformed params: "+err.Error(),
					map[string]any{"window_index": i},
				)
				return
			}
		}
		if p.Alpha == nil {
			env.AddError(
				string(errors.PULSE_WINDOW_INVALID),
				"window["+idx+"] (WIN_EWMA): params.alpha is required",
				map[string]any{"window_index": i},
			)
			return
		}
		if *p.Alpha <= 0 || *p.Alpha > 1 {
			env.AddError(
				string(errors.PULSE_WINDOW_INVALID),
				"window["+idx+"] (WIN_EWMA): params.alpha must be in (0, 1]",
				map[string]any{"window_index": i, "alpha": *p.Alpha},
			)
		}

	case types.WIN_LAG, types.WIN_LEAD:
		if len(w.Params) == 0 {
			return
		}
		var p struct {
			Offset *int `json:"offset"`
		}
		if err := json.Unmarshal(w.Params, &p); err != nil {
			env.AddError(
				string(errors.PULSE_WINDOW_INVALID),
				"window["+idx+"] ("+string(w.Type)+"): malformed params: "+err.Error(),
				map[string]any{"window_index": i},
			)
			return
		}
		if p.Offset != nil && *p.Offset < 0 {
			env.AddError(
				string(errors.PULSE_WINDOW_INVALID),
				"window["+idx+"] ("+string(w.Type)+"): params.offset must be >= 0",
				map[string]any{"window_index": i, "offset": *p.Offset},
			)
		}

	case types.WIN_PCT_CHANGE:
		if len(w.Params) == 0 {
			return
		}
		var p struct {
			Periods *int `json:"periods"`
		}
		if err := json.Unmarshal(w.Params, &p); err != nil {
			env.AddError(
				string(errors.PULSE_WINDOW_INVALID),
				"window["+idx+"] (WIN_PCT_CHANGE): malformed params: "+err.Error(),
				map[string]any{"window_index": i},
			)
			return
		}
		if p.Periods != nil && *p.Periods <= 0 {
			env.AddError(
				string(errors.PULSE_WINDOW_INVALID),
				"window["+idx+"] (WIN_PCT_CHANGE): params.periods must be >= 1",
				map[string]any{"window_index": i, "periods": *p.Periods},
			)
		}
	}
}
