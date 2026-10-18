// Package shareparams is the one reading of WIN_SHARE's and WIN_LIFT's
// slot, shared by the window factory (internal/processing/window) and
// predict (internal/descriptor) so both refuse the same request with the
// same code and the same rule sentence. It decides what a request MEANS
// and never divides a value.
//
// The rules, in the order they are judged:
//
//   - params decode (WIN_SHARE: cumulative; WIN_LIFT: cumulative, base);
//   - field is required (the column whose share is taken);
//   - WIN_LIFT: params.base is required (the column it is compared with);
//   - frame is refused: a share is always of the whole partition's total,
//     and the cumulative running sum always runs from the partition's
//     first row to the current one;
//   - cumulative true needs order_by (the running sum needs an order);
//     cumulative false (the default) needs none.
//
// Every refusal is PULSE_WINDOW_INVALID on both sides: predict prefixes
// "window[i] (TYPE): ", the factory "TYPE: ".
//
// Leaf: stdlib + the public errors / types packages + paramread.
package shareparams

import (
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/paramread"
	"github.com/frankbardon/pulse/types"
)

// shareParams is WIN_SHARE's params wire shape.
type shareParams struct {
	Cumulative *bool `json:"cumulative"`
}

// liftParams is WIN_LIFT's params wire shape.
type liftParams struct {
	Cumulative *bool  `json:"cumulative"`
	Base       string `json:"base"`
}

// Spec is the resolved meaning of one WIN_SHARE / WIN_LIFT slot.
type Spec struct {
	// Cumulative selects the running share (unbounded preceding to the
	// current row, over the partition's total) instead of the row's own.
	Cumulative bool
	// Base is WIN_LIFT's comparison column; "" on WIN_SHARE.
	Base string
}

// Refusal is one violated rule: Message is the operator-neutral sentence
// (callers prefix their own context) and Details the offending values.
type Refusal struct {
	Op      types.WindowType
	Message string
	Details map[string]any
}

// Error renders the refusal for callers that wrap it.
func (r *Refusal) Error() string { return r.Message }

// Coded returns the refusal as the factory raises it:
// PULSE_WINDOW_INVALID, prefixed with the operator name.
func (r *Refusal) Coded() *errors.CodedError {
	if len(r.Details) == 0 {
		return errors.NewCodedError(errors.PULSE_WINDOW_INVALID, string(r.Op)+": "+r.Message)
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_WINDOW_INVALID, string(r.Op)+": "+r.Message, r.Details)
}

// Handles reports whether t is one of the two operators this package
// reads.
func Handles(t types.WindowType) bool {
	return t == types.WIN_SHARE || t == types.WIN_LIFT
}

// Resolve reads w and returns its Spec, or the first rule it breaks.
// w.Type must satisfy Handles.
func Resolve(w *types.Window) (Spec, *Refusal) {
	refuse := func(msg string, details map[string]any) (Spec, *Refusal) {
		return Spec{}, &Refusal{Op: w.Type, Message: msg, Details: details}
	}
	var spec Spec
	switch w.Type {
	case types.WIN_LIFT:
		var p liftParams
		paramread.Declare(paramread.Window, string(w.Type), (*liftParams)(nil))
		if len(w.Params) > 0 {
			if err := paramread.Decode(paramread.Window, string(w.Type), w.Params, &p); err != nil {
				return refuse("malformed params: "+err.Error(), nil)
			}
		}
		spec.Cumulative = p.Cumulative != nil && *p.Cumulative
		spec.Base = p.Base
	default:
		var p shareParams
		paramread.Declare(paramread.Window, string(w.Type), (*shareParams)(nil))
		if len(w.Params) > 0 {
			if err := paramread.Decode(paramread.Window, string(w.Type), w.Params, &p); err != nil {
				return refuse("malformed params: "+err.Error(), nil)
			}
		}
		spec.Cumulative = p.Cumulative != nil && *p.Cumulative
	}
	if w.Field == "" {
		return refuse("field is required (the column whose share is taken)", nil)
	}
	if w.Type == types.WIN_LIFT && spec.Base == "" {
		return refuse("params.base is required (the column the field's share is compared with)", nil)
	}
	if w.Frame != nil {
		return refuse("frame is not allowed: the share is always of the whole partition's total, and params.cumulative runs from the partition's first row to the current one", nil)
	}
	if spec.Cumulative && len(w.OrderBy) == 0 {
		return refuse("order_by is required when params.cumulative is true (the running sum needs an order)",
			map[string]any{"cumulative": true})
	}
	return spec, nil
}
