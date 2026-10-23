package descriptor

import (
	"maps"
	"strconv"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/fieldaccept"
	"github.com/frankbardon/pulse/internal/shareparams"
	"github.com/frankbardon/pulse/internal/windowparams"
	"github.com/frankbardon/pulse/types"
)

// WindowFrameRequired reports whether a window of type t that carries
// no frame is refused — the rule Recommend's drafts ask the caller to
// satisfy. The table is the shared window reading's
// (internal/windowparams), so predict and the engine agree.
func WindowFrameRequired(t types.WindowType) bool { return windowparams.FrameRequired(t) }

// WindowFieldForbidden reports whether predict refuses a window of type
// t that names a field (the order-only operators) — Recommend's drafts
// and skeletons omit the slot for them.
func WindowFieldForbidden(t types.WindowType) bool { return windowparams.FieldForbidden(t) }

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
// for every admitted type. vec_f32 / vec_f64 are refused too: a vector
// has no single value to order by.
func IsOrderableType(ft encoding.FieldType) bool {
	return !ft.IsSet() && !ft.IsVector()
}

// isNumericType reports whether the field type is a value-window
// input — the accepted-types table's (internal/fieldaccept) value-window
// set, which validateWindows enforces. Kept for the missing-field
// suggestions (numericFallbackFields).
func isNumericType(ft encoding.FieldType) bool {
	return !ft.IsVector() && fieldaccept.Accepts(string(types.WIN_LAG), fieldaccept.SlotField, ft)
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

		// The slot's configuration: one reading with the engine
		// (internal/windowparams), the same code and rule sentence on
		// both sides — predict reports every rule broken, the engine
		// the first. OrderBy / PartitionBy existence and orderability:
		// FieldRefRefusals.
		for _, ref := range windowparams.Check(w) {
			details := map[string]any{"window_index": i, "type": string(w.Type)}
			maps.Copy(details, ref.Details)
			env.AddError(
				string(errors.PULSE_WINDOW_INVALID),
				"window["+idx+"] ("+string(w.Type)+"): "+ref.Message,
				details,
			)
		}

		// The accepted-types table's refusal, the runtime's (processing
		// applyWindows) word for word.
		if windowparams.FieldRequired(w.Type) && w.Field != "" {
			if cerr := fieldaccept.Check(string(w.Type), fieldaccept.SlotField, w.Field, schema); cerr != nil {
				env.AddError(string(cerr.Code), cerr.Message, cerr.Details)
			}
		}

		// WIN_LIFT's base column: the accepted-types table's refusal,
		// the runtime's (processing applyWindows) word for word.
		if w.Type == types.WIN_LIFT {
			if spec, ref := shareparams.Resolve(w); ref == nil {
				if cerr := fieldaccept.Check(string(w.Type), fieldaccept.SlotBase, spec.Base, schema); cerr != nil {
					env.AddError(string(cerr.Code), cerr.Message, cerr.Details)
				}
			}
		}

	}
}

// windowLabel returns the effective output column name for a window:
// windowparams.Label, the one derivation the engine reads too.
func windowLabel(w *types.Window) string { return windowparams.Label(w) }
