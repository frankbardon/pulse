package descriptor

import (
	"encoding/json"
	"strconv"

	"github.com/frankbardon/pulse/descriptor"
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
	types.WIN_DELTA:      true,
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
	types.WIN_DELTA:       true,
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
	types.WIN_DELTA:       true,
}

// IsOrderableType is the ONE window ORDER BY orderability rule, applied
// on both sides by the shared FieldRefRefusals walk (predict reports it,
// every runtime execution mode refuses with it). It admits exactly the
// types the window comparator (internal/processing/window compareCell,
// shared with Request.Sort) orders by value:
//
//   - every unsigned integer and float type, by numeric value;
//   - date (signed epoch days) and datetime (signed epoch SECONDS) —
//     pre-1970 values are negative and sort before 1970;
//   - packed_bool as 0/1, false before true;
//   - decimal128 by value (Decimal128.Cmp — one column shares one scale);
//   - categorical_* by dictionary LABEL, byte-wise — never by dictionary
//     index, so the order is stable across imports and shards
//     (TestWindowOrderBy_CategoricalOrdersByLabel).
//
// set_* is refused: a set cell decodes to a label slice the comparator
// cannot order, so every row would compare equal
// (TestWindowOrderBy_OrderabilityPredictMatchesRuntime). Nulls sort last
// for every admitted type.
func IsOrderableType(ft encoding.FieldType) bool {
	return !ft.IsSet()
}

// isNumericType reports whether the field type carries a meaningful scalar
// value for window arithmetic (sum, avg, lag, ewma, etc.). Intentionally
// narrower than the canonical encoding.FieldType.IsNumericForAnalytics:
// decimal128 is excluded because the buffered decimal path is unimplemented
// for window operators, and the bit-packed bool encoding is excluded
// because windowing a 0/1 series rarely matches caller intent. Keep this
// helper local; widen it only after the corresponding window engines
// support the broader set.
func isNumericType(ft encoding.FieldType) bool {
	switch ft {
	case encoding.FieldTypeU4,
		encoding.FieldTypeU8, encoding.FieldTypeU16, encoding.FieldTypeU32, encoding.FieldTypeU64,
		encoding.FieldTypeF32, encoding.FieldTypeF64,
		encoding.FieldTypeDate:
		return true
	}
	return false
}

// validateSort checks that every Request.Sort key names a field.
// Whether that field is an available output column is FieldRefRefusals'
// judgement.
func validateSort(env *descriptor.Envelope, req *types.Request, _ *encoding.Schema) {
	if len(req.Sort) == 0 {
		return
	}
	for i, k := range req.Sort {
		idx := strconv.Itoa(i)
		if k.Field == "" {
			env.AddError(
				string(errors.SERVICE_VALIDATION),
				"sort["+idx+"]: field is required",
				map[string]any{"sort_index": i},
			)
		}
	}
}

// validateWindows applies the predict-side validation rules for req.Windows.
// All rejections produce errors via env.AddError. The function does not execute
// any window logic; it inspects only the schema and the request.
func validateWindows(env *descriptor.Envelope, req *types.Request, schema *encoding.Schema, opts *PredictOptions) {
	// Build the set of valid window types once.
	validTypes := make(map[types.WindowType]bool, len(types.AllWindowTypes()))
	for _, t := range types.AllWindowTypes() {
		validTypes[t] = true
	}

	// Label collisions with an available column (aggregation / group
	// output, record column, earlier window) are FieldRefRefusals'
	// shadow refusal on both sides.
	for i, w := range req.Windows {
		idx := strconv.Itoa(i)

		// Unknown type — a type the instance hides included, exactly
		// as a never-registered one.
		if !validTypes[opRoute(opts.instance(), w.Type)] && !isExtensionWindowType(opts, w.Type) {
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

		// OrderBy keys must name a field.
		for j, ok := range w.OrderBy {
			if ok.Field == "" {
				env.AddError(
					string(errors.PULSE_WINDOW_INVALID),
					"window["+idx+"]: order_by["+strconv.Itoa(j)+"] missing field",
					map[string]any{"window_index": i, "order_index": j},
				)
			}
			// Existence and orderability: FieldRefRefusals.
		}

		// PartitionBy existence: FieldRefRefusals.

		// Field required for value-bearing operators; missing field is SERVICE_VALIDATION
		// to share semantics with the existing validators.
		if windowFieldRequired[w.Type] {
			if w.Field == "" {
				env.AddError(
					string(errors.SERVICE_VALIDATION),
					"window["+idx+"] ("+string(w.Type)+"): field is required",
					map[string]any{"window_index": i, "type": string(w.Type)},
				)
			} else if f := schema.Field(w.Field); f != nil && windowNumericFieldRequired[w.Type] && !isNumericType(f.Type) {
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
func validateWindowParams(env *descriptor.Envelope, i int, w *types.Window) {
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

	// WIN_PCT_CHANGE and WIN_DELTA share one params shape (periods >= 1), so they
	// share one case — as WIN_LAG / WIN_LEAD do above. The rendered messages are
	// unchanged for WIN_PCT_CHANGE: string(w.Type) is its own constant.
	case types.WIN_PCT_CHANGE, types.WIN_DELTA:
		if len(w.Params) == 0 {
			return
		}
		var p struct {
			Periods *int `json:"periods"`
		}
		if err := json.Unmarshal(w.Params, &p); err != nil {
			env.AddError(
				string(errors.PULSE_WINDOW_INVALID),
				"window["+idx+"] ("+string(w.Type)+"): malformed params: "+err.Error(),
				map[string]any{"window_index": i},
			)
			return
		}
		if p.Periods != nil && *p.Periods <= 0 {
			env.AddError(
				string(errors.PULSE_WINDOW_INVALID),
				"window["+idx+"] ("+string(w.Type)+"): params.periods must be >= 1",
				map[string]any{"window_index": i, "periods": *p.Periods},
			)
		}
	}
}
