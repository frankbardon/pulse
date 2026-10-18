// Package windowparams is the one reading of a window slot's
// configuration, shared by the window engine (internal/processing and
// its window factories) and predict (internal/descriptor) so both refuse
// the same request with the same code and the same rule sentence. It
// decides what a slot MEANS and never computes a value.
//
// The rules, in the order Check judges them:
//
//   - order_by needs at least one key — except WIN_SHARE / WIN_LIFT,
//     whose own reading (internal/shareparams) needs it only when
//     cumulative — and every key names a field;
//   - field is required on every value-bearing operator (FieldRequired)
//     and refused on the order-only ones (FieldForbidden: row number,
//     rank, dense rank read no value);
//   - frame is refused on the operators that read none (FrameRejected),
//     required on the ones that do (FrameRequired); a given frame is
//     mode "rows", its bounds are >= 0, and a moving average bounds both
//     ends;
//   - the operator's params (offset, periods, the EWMA smoothing factor,
//     the share / lift slot).
//
// Every refusal is PULSE_WINDOW_INVALID on both sides: predict prefixes
// "window[i] (TYPE): ", the engine "TYPE: ". Predict reports every
// refusal Check returns; the engine raises the first.
//
// Window types this package does not table (an embedder's extension
// window) are judged on the type-neutral rules only: order_by, its keys,
// and a given frame's mode and bounds.
//
// Leaf: stdlib + the public errors / types packages + paramread and the
// two operator-specific leaves (ewmaparams, shareparams).
package windowparams

import (
	"encoding/json"
	"strconv"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/ewmaparams"
	"github.com/frankbardon/pulse/internal/paramread"
	"github.com/frankbardon/pulse/internal/shareparams"
	"github.com/frankbardon/pulse/types"
)

// frameRequired lists window types that REQUIRE a frame specification.
var frameRequired = map[types.WindowType]bool{
	types.WIN_RUNNING_SUM: true,
	types.WIN_RUNNING_AVG: true,
	types.WIN_MOVING_AVG:  true,
	types.WIN_EWMA:        true,
}

// frameRejected lists window types that REJECT a frame specification.
// WIN_SHARE / WIN_LIFT refuse one too, with their own reading's
// sentence (internal/shareparams).
var frameRejected = map[types.WindowType]bool{
	types.WIN_LAG:        true,
	types.WIN_LEAD:       true,
	types.WIN_ROW_NUMBER: true,
	types.WIN_RANK:       true,
	types.WIN_DENSE_RANK: true,
	types.WIN_PCT_CHANGE: true,
	types.WIN_DELTA:      true,
}

// fieldRequired lists the value-bearing operators. ROW_NUMBER / RANK /
// DENSE_RANK have no value field.
var fieldRequired = map[types.WindowType]bool{
	types.WIN_LAG:         true,
	types.WIN_LEAD:        true,
	types.WIN_RUNNING_SUM: true,
	types.WIN_RUNNING_AVG: true,
	types.WIN_MOVING_AVG:  true,
	types.WIN_EWMA:        true,
	types.WIN_PCT_CHANGE:  true,
	types.WIN_DELTA:       true,
	types.WIN_SHARE:       true,
	types.WIN_LIFT:        true,
}

// fieldForbidden lists the order-only operators: they read no value
// field, so a slot naming one is refused rather than silently ignored.
var fieldForbidden = map[types.WindowType]bool{
	types.WIN_ROW_NUMBER: true,
	types.WIN_RANK:       true,
	types.WIN_DENSE_RANK: true,
}

// FieldForbidden reports whether a window of type t refuses a field.
func FieldForbidden(t types.WindowType) bool { return fieldForbidden[t] }

// FrameRequired reports whether a window of type t without a frame is
// refused.
func FrameRequired(t types.WindowType) bool { return frameRequired[t] }

// FrameRejected reports whether a window of type t with a frame is
// refused (WIN_SHARE / WIN_LIFT included: their reading refuses it).
func FrameRejected(t types.WindowType) bool { return frameRejected[t] || shareparams.Handles(t) }

// FieldRequired reports whether a window of type t reads a value field
// (and so refuses a slot without one).
func FieldRequired(t types.WindowType) bool { return fieldRequired[t] }

// Refusal is one violated rule: Message is the operator-neutral sentence
// (callers prefix their own context) and Details the offending values.
type Refusal struct {
	Op      types.WindowType
	Message string
	Details map[string]any
}

// Error renders the refusal for callers that wrap it.
func (r *Refusal) Error() string { return r.Message }

// Coded returns the refusal as the engine raises it:
// PULSE_WINDOW_INVALID, prefixed with the operator name.
func (r *Refusal) Coded() *errors.CodedError {
	if len(r.Details) == 0 {
		return errors.NewCodedError(errors.PULSE_WINDOW_INVALID, string(r.Op)+": "+r.Message)
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_WINDOW_INVALID, string(r.Op)+": "+r.Message, r.Details)
}

// First returns the first refusal Check finds for w, or nil — the one
// the engine raises.
func First(w *types.Window) *Refusal {
	if refs := Check(w); len(refs) > 0 {
		return refs[0]
	}
	return nil
}

// Check returns every rule w breaks, in judging order; nil for a valid
// slot. A nil w is valid (the engine skips it).
func Check(w *types.Window) []*Refusal {
	if w == nil {
		return nil
	}
	var out []*Refusal
	refuse := func(msg string, details map[string]any) {
		out = append(out, &Refusal{Op: w.Type, Message: msg, Details: details})
	}

	if len(w.OrderBy) == 0 && !shareparams.Handles(w.Type) {
		refuse("order_by is required (at least one key)", nil)
	}
	for j, k := range w.OrderBy {
		if k.Field == "" {
			refuse("order_by["+strconv.Itoa(j)+"] missing field", map[string]any{"order_index": j})
		}
	}

	// A missing field on WIN_SHARE / WIN_LIFT is the share reading's
	// refusal, judged with its params below.
	if fieldRequired[w.Type] && w.Field == "" && !shareparams.Handles(w.Type) {
		refuse("field is required", nil)
	}
	if fieldForbidden[w.Type] && w.Field != "" {
		refuse("field is not allowed: this operator reads only its order_by keys", map[string]any{"field": w.Field})
	}

	if w.Frame != nil && frameRejected[w.Type] {
		refuse("frame is not allowed for this operator", nil)
	}
	if w.Frame == nil && frameRequired[w.Type] {
		refuse("frame is required", nil)
	}
	if f := w.Frame; f != nil {
		if f.Mode != "rows" {
			refuse("frame.mode must be \"rows\" (got "+strconv.Quote(f.Mode)+")", map[string]any{"mode": f.Mode})
		}
		if w.Type == types.WIN_MOVING_AVG && (f.Preceding == nil || f.Following == nil) {
			refuse("frame must have bounded preceding AND following", nil)
		}
		if f.Preceding != nil && *f.Preceding < 0 {
			refuse("frame.preceding must be >= 0", map[string]any{"preceding": *f.Preceding})
		}
		if f.Following != nil && *f.Following < 0 {
			refuse("frame.following must be >= 0", map[string]any{"following": *f.Following})
		}
	}

	if ref := paramsRefusal(w); ref != nil {
		out = append(out, ref)
	}
	return out
}

// paramsRefusal is the operator-specific params rule w breaks, or nil.
func paramsRefusal(w *types.Window) *Refusal {
	switch w.Type {
	case types.WIN_SHARE, types.WIN_LIFT:
		if _, ref := shareparams.Resolve(w); ref != nil {
			return &Refusal{Op: w.Type, Message: ref.Message, Details: ref.Details}
		}
	case types.WIN_EWMA:
		if _, ref := ewmaparams.Resolve(w.Params); ref != nil {
			return &Refusal{Op: w.Type, Message: ref.Message, Details: ref.Details}
		}
	case types.WIN_LAG, types.WIN_LEAD:
		if _, ref := Lag(w); ref != nil {
			return ref
		}
	case types.WIN_PCT_CHANGE, types.WIN_DELTA:
		if _, ref := Periods(w); ref != nil {
			return ref
		}
	}
	return nil
}

// LagSpec is the resolved meaning of a WIN_LAG / WIN_LEAD slot's params.
type LagSpec struct {
	// Offset is how many rows back (lag) or forward (lead); default 1.
	Offset int
	// HasDefault reports a non-null params.default; Default is its value,
	// emitted where the offset falls outside the partition.
	HasDefault bool
	Default    any
}

// lagLeadParams is WIN_LAG's / WIN_LEAD's params wire shape.
type lagLeadParams struct {
	Offset  *int            `json:"offset"`
	Default json.RawMessage `json:"default"`
}

// Lag reads a WIN_LAG / WIN_LEAD slot's params.
func Lag(w *types.Window) (LagSpec, *Refusal) {
	spec := LagSpec{Offset: 1}
	paramread.Declare(paramread.Window, string(w.Type), (*lagLeadParams)(nil))
	if len(w.Params) == 0 {
		return spec, nil
	}
	var p lagLeadParams
	if err := paramread.Decode(paramread.Window, string(w.Type), w.Params, &p); err != nil {
		return LagSpec{}, &Refusal{Op: w.Type, Message: "malformed params: " + err.Error()}
	}
	if p.Offset != nil {
		if *p.Offset < 0 {
			return LagSpec{}, &Refusal{Op: w.Type, Message: "params.offset must be >= 0",
				Details: map[string]any{"offset": *p.Offset}}
		}
		spec.Offset = *p.Offset
	}
	if len(p.Default) > 0 && string(p.Default) != "null" {
		var v any
		if err := json.Unmarshal(p.Default, &v); err != nil {
			return LagSpec{}, &Refusal{Op: w.Type, Message: "malformed params.default: " + err.Error()}
		}
		spec.HasDefault = true
		spec.Default = v
	}
	return spec, nil
}

// periodsParams is WIN_PCT_CHANGE's / WIN_DELTA's params wire shape.
type periodsParams struct {
	Periods *int `json:"periods"`
}

// Periods reads a WIN_PCT_CHANGE / WIN_DELTA slot's lookback distance;
// default 1.
func Periods(w *types.Window) (int, *Refusal) {
	paramread.Declare(paramread.Window, string(w.Type), (*periodsParams)(nil))
	if len(w.Params) == 0 {
		return 1, nil
	}
	var p periodsParams
	if err := paramread.Decode(paramread.Window, string(w.Type), w.Params, &p); err != nil {
		return 0, &Refusal{Op: w.Type, Message: "malformed params: " + err.Error()}
	}
	if p.Periods == nil {
		return 1, nil
	}
	if *p.Periods <= 0 {
		return 0, &Refusal{Op: w.Type, Message: "params.periods must be >= 1",
			Details: map[string]any{"periods": *p.Periods}}
	}
	return *p.Periods, nil
}
