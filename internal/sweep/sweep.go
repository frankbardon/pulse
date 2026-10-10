// Package sweep is the one rule set for a Compose parameter sweep
// (types.SweepSpec, the `sweep` slot of a ComposedRequest).
//
// It is NO-EXECUTE and sits below both callers that must agree on it:
// the runtime (internal/service, Compose / ComposeParallel) and predict
// (internal/descriptor, PredictCompose), which may not import the
// engine (TestPredictNoExecutionImports). It therefore imports only the
// standard library, errors, types and — for expansion — the
// import-fenced internal/template (TestSweep_ImportBoundary).
//
// Validate is the structural check every surface runs before any slot
// is expanded. Its rules (the contract):
//
//   - axes is non-empty;
//   - every axis name is an identifier ([A-Za-z_][A-Za-z0-9_]*), unique
//     within the sweep;
//   - every axis carries a non-empty values list of scalars — a number
//     (json.Number, any integer kind, a finite float), a string or a
//     boolean; null, objects and arrays are refused;
//   - mode is empty, grid or zip; zip needs every axis the same length;
//   - request is a JSON object; overlays, when set, is a JSON array;
//   - rank, when set, names a non-empty `by`; order is empty, asc or
//     desc; top, when set, is at least 1.
//
// Checks run in that order, axes in declared order, and the first fault
// is returned as PULSE_SWEEP_INVALID with details `field` (the sweep-
// rooted path of the offending value), `reason`, `axis` (the axis name)
// where one is involved and, where they apply, `value` / `valid`.
// Whether every axis is referenced and every placeholder names an axis
// needs the substitution scan, so expansion checks it.
package sweep

import (
	"bytes"
	"encoding/json"
	"math"
	"strconv"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// The reason values PULSE_SWEEP_INVALID carries under details.reason.
const (
	ReasonAxesEmpty         = "axes_empty"
	ReasonAxisNameInvalid   = "axis_name_invalid"
	ReasonAxisNameDuplicate = "axis_name_duplicate"
	ReasonValuesEmpty       = "values_empty"
	ReasonValueNotScalar    = "value_not_scalar"
	ReasonModeUnknown       = "mode_unknown"
	ReasonZipLengthMismatch = "zip_length_mismatch"
	ReasonRequestMissing    = "request_missing"
	ReasonRequestNotObject  = "request_not_object"
	ReasonOverlaysNotArray  = "overlays_not_array"
	ReasonRankByEmpty       = "rank_by_empty"
	ReasonRankOrderUnknown  = "rank_order_unknown"
	ReasonRankTopInvalid    = "rank_top_invalid"
)

// Validate reports the first structural fault of spec as a
// PULSE_SWEEP_INVALID *errors.CodedError, or nil. A nil spec is valid
// (no sweep).
func Validate(spec *types.SweepSpec) *errors.CodedError {
	if spec == nil {
		return nil
	}
	if len(spec.Axes) == 0 {
		return invalid("sweep.axes", "", ReasonAxesEmpty,
			"sweep declares no axes; a sweep needs at least one axis", nil)
	}
	seen := make(map[string]int, len(spec.Axes))
	for i, ax := range spec.Axes {
		at := axisPath(i)
		if !isIdentifier(ax.Name) {
			return invalid(at+".name", ax.Name, ReasonAxisNameInvalid,
				"sweep axis "+describe(at, ax.Name)+" needs a name that is an identifier ([A-Za-z_][A-Za-z0-9_]*)",
				map[string]any{"value": ax.Name})
		}
		if first, dup := seen[ax.Name]; dup {
			return invalid(at+".name", ax.Name, ReasonAxisNameDuplicate,
				"sweep axis name "+strconv.Quote(ax.Name)+" is declared twice ("+axisPath(first)+" and "+at+")",
				map[string]any{"first": axisPath(first)})
		}
		seen[ax.Name] = i
		if len(ax.Values) == 0 {
			return invalid(at+".values", ax.Name, ReasonValuesEmpty,
				"sweep axis "+describe(at, ax.Name)+" has no values; list at least one", nil)
		}
		for j, v := range ax.Values {
			if !isScalar(v) {
				vp := at + ".values[" + strconv.Itoa(j) + "]"
				return invalid(vp, ax.Name, ReasonValueNotScalar,
					"sweep axis "+describe(at, ax.Name)+" value "+vp+" is not a scalar; values are numbers, strings or booleans",
					nil)
			}
		}
	}
	switch spec.Mode {
	case "", types.SweepModeGrid, types.SweepModeZip:
	default:
		return invalid("sweep.mode", "", ReasonModeUnknown,
			"sweep mode "+strconv.Quote(string(spec.Mode))+" is not grid or zip",
			map[string]any{"value": string(spec.Mode), "valid": modeNames()})
	}
	if spec.Mode == types.SweepModeZip {
		want := len(spec.Axes[0].Values)
		for i, ax := range spec.Axes[1:] {
			if len(ax.Values) != want {
				at := axisPath(i + 1)
				return invalid(at+".values", ax.Name, ReasonZipLengthMismatch,
					"zip sweep axis "+describe(at, ax.Name)+" has "+strconv.Itoa(len(ax.Values))+
						" values but "+describe(axisPath(0), spec.Axes[0].Name)+" has "+strconv.Itoa(want)+
						"; zip walks the axes in lockstep, so every axis needs the same length",
					map[string]any{"lengths": axisLengths(spec.Axes)})
			}
		}
	}
	body := bytes.TrimSpace(spec.Request)
	if len(body) == 0 || bytes.Equal(body, []byte("null")) {
		return invalid("sweep.request", "", ReasonRequestMissing,
			"sweep has no request body; set `request` to the slot request with axis placeholders", nil)
	}
	if body[0] != '{' || !json.Valid(body) {
		return invalid("sweep.request", "", ReasonRequestNotObject,
			"sweep request must be a JSON object (the Request shape with axis placeholders)", nil)
	}
	if ov := bytes.TrimSpace(spec.Overlays); len(ov) > 0 && !bytes.Equal(ov, []byte("null")) {
		if ov[0] != '[' || !json.Valid(ov) {
			return invalid("sweep.overlays", "", ReasonOverlaysNotArray,
				"sweep overlays must be a JSON array of compose overlay specs", nil)
		}
	}
	if r := spec.Rank; r != nil {
		if r.By == "" {
			return invalid("sweep.rank.by", "", ReasonRankByEmpty,
				"sweep rank needs `by`, the path of the ranked number in each slot's response", nil)
		}
		switch r.Order {
		case "", types.SweepRankAsc, types.SweepRankDesc:
		default:
			return invalid("sweep.rank.order", "", ReasonRankOrderUnknown,
				"sweep rank order "+strconv.Quote(string(r.Order))+" is not asc or desc",
				map[string]any{"value": string(r.Order), "valid": orderNames()})
		}
		if r.Top != nil && *r.Top < 1 {
			return invalid("sweep.rank.top", "", ReasonRankTopInvalid,
				"sweep rank top is "+strconv.Itoa(*r.Top)+"; set it to at least 1, or omit it to rank every slot",
				map[string]any{"value": *r.Top})
		}
	}
	return nil
}

func axisPath(i int) string { return "sweep.axes[" + strconv.Itoa(i) + "]" }

func describe(at, name string) string {
	if name == "" {
		return at
	}
	return strconv.Quote(name) + " (" + at + ")"
}

func invalid(field, axis, reason, msg string, extra map[string]any) *errors.CodedError {
	details := map[string]any{"field": field, "reason": reason}
	if axis != "" {
		details["axis"] = axis
	}
	for k, v := range extra {
		details[k] = v
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_SWEEP_INVALID, msg, details)
}

// isIdentifier reports whether s matches [A-Za-z_][A-Za-z0-9_]*.
func isIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '_', c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z':
		case c >= '0' && c <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}

// isScalar reports whether v is an admissible axis value.
func isScalar(v any) bool {
	switch x := v.(type) {
	case string, bool,
		int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64:
		return true
	case float64:
		return !math.IsNaN(x) && !math.IsInf(x, 0)
	case float32:
		f := float64(x)
		return !math.IsNaN(f) && !math.IsInf(f, 0)
	case json.Number:
		return isNumberText(string(x))
	default:
		return false
	}
}

// isNumberText reports whether s is one JSON number literal.
func isNumberText(s string) bool {
	if s == "" || !(s[0] == '-' || (s[0] >= '0' && s[0] <= '9')) {
		return false
	}
	return json.Valid([]byte(s))
}

func axisLengths(axes []types.SweepAxis) map[string]any {
	out := make(map[string]any, len(axes))
	for _, ax := range axes {
		out[ax.Name] = len(ax.Values)
	}
	return out
}

func modeNames() []string {
	var out []string
	for _, m := range types.AllSweepModes() {
		out = append(out, string(m))
	}
	return out
}

func orderNames() []string {
	var out []string
	for _, o := range types.AllSweepRankOrders() {
		out = append(out, string(o))
	}
	return out
}
