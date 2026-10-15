package formulaident

import (
	"math"

	exprparser "github.com/expr-lang/expr/parser"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/paramread"
	"github.com/frankbardon/pulse/internal/totalcall"
	"github.com/frankbardon/pulse/types"
)

// BaselineParam is the Request-host SERIES FORMULA param naming the
// positional baseline row `baseline` reads.
const BaselineParam = "baseline_position"

// RequestSpecRefusal judges the Request-host OVERLAY_FORMULA spec at
// index over a host of shape (MATRIX or SERIES), allowed to call funcs
// (AllowedFuncs). Predict (descriptor.validateFormulaOverlay) and the
// runtime MATRIX and SERIES dispatchers call it before any compile, so
// every refusal below carries one code, message and Details on both
// sides. nil when the spec is in reach; otherwise, first hit wins:
//
//   - no usable params.formula (Params absent or not an object, the
//     key absent, an empty or non-string value):
//     PULSE_OVERLAY_PARAM_MISSING {index, kind, param, reason}
//     (MissingFormula — every FORMULA arm's rule);
//   - a parse failure: PULSE_OVERLAY_FORMULA_PARSE_ERROR {index, kind,
//     formula, parse_error};
//   - a total() call: totalcall.Refusal's code, Details + index, kind;
//   - a vector built-in: BuiltinRefusal;
//   - SERIES only: a params.baseline_position that is not a
//     non-negative integer: PULSE_OVERLAY_LEVEL_OUT_OF_RANGE {index,
//     kind, param, baseline_position};
//   - an unknown variable, function or slot path: RequestRefusal over
//     RequestVars(shape, baseline_position set).
func RequestSpecRefusal(spec *types.OverlaySpec, index int, shape types.OverlayShape, funcs map[string]struct{}) *errors.CodedError {
	if spec == nil {
		return nil
	}
	kind := string(spec.Kind)
	params, cerr := requestParams(spec, index)
	if cerr != nil {
		return cerr
	}
	formula, reason := ComposeFormula(params)
	if reason != "" {
		msg := "overlay " + kind + " requires Params[\"formula\"] (non-empty string)"
		if reason != ReasonMissingFormula {
			msg = "overlay " + kind + " Params[\"formula\"] must be a non-empty string"
		}
		return MissingFormula(kind, msg, index, reason)
	}
	tree, err := exprparser.Parse(formula)
	if err != nil {
		return errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_FORMULA_PARSE_ERROR,
			"overlay "+kind+" formula failed to parse: "+err.Error(),
			map[string]any{"index": index, "kind": kind, "formula": formula, "parse_error": err.Error()})
	}
	if tc := totalcall.Refusal(formula, string(types.OverlayKindFormula)); tc != nil {
		details := map[string]any{"index": index, "kind": kind}
		for k, v := range tc.Details {
			details[k] = v
		}
		return errors.NewCodedErrorWithDetails(tc.Code, tc.Message, details)
	}
	if cerr := BuiltinRefusal(spec.Kind, formula, index); cerr != nil {
		return cerr
	}
	baseline := false
	if shape == types.OverlayShapeSeries {
		var bad bool
		paramread.Read(paramread.Overlay, kind, BaselineParam)
		_, baseline, bad = BaselinePosition(params)
		if bad {
			return errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE,
				"overlay "+kind+" Params[\"baseline_position\"] must be a non-negative integer (the 0-based row of the host series `baseline` reads)",
				map[string]any{"index": index, "kind": kind, "param": BaselineParam, BaselineParam: params[BaselineParam]})
		}
	}
	return RequestRefusal(spec.Kind, formula, &tree.Node, index, shape, RequestVars(shape, baseline), funcs)
}

// requestParams decodes spec.Params as a JSON object: the
// MissingFormula refusal when Params is empty or not an object.
func requestParams(spec *types.OverlaySpec, index int) (map[string]any, *errors.CodedError) {
	kind := string(spec.Kind)
	if len(spec.Params) == 0 {
		return nil, MissingFormula(kind,
			"overlay "+kind+" requires Params[\"formula\"] (non-empty string); Params is missing or empty",
			index, ReasonMissingFormula)
	}
	var m map[string]any
	if err := paramread.Decode(paramread.Overlay, kind, spec.Params, &m); err != nil {
		return nil, MissingFormula(kind, "overlay "+kind+" Params must be a JSON object carrying \"formula\"",
			index, "params is not a JSON object")
	}
	paramread.Read(paramread.Overlay, kind, "formula")
	return m, nil
}

// Reasons a FORMULA spec carries no usable formula (Details.reason of
// MissingFormula).
const (
	ReasonMissingFormula = "missing formula param"
	ReasonEmptyFormula   = "formula param is empty or non-string"
)

// MissingFormula is the refusal of an OVERLAY_FORMULA spec that carries
// no usable formula — Params absent or not an object, no "formula" key,
// an empty or non-string value — on EVERY FORMULA arm (Request host
// MATRIX and SERIES, Compose) and on both sides:
// PULSE_OVERLAY_PARAM_MISSING (the more specific code for "no formula"),
// Details {index, kind, param: "formula", reason}. A formula that is
// present but does not parse, or names an unknown identifier, keeps its
// formula-specific code (PULSE_OVERLAY_FORMULA_PARSE_ERROR /
// _INVALID_IDENT).
func MissingFormula(kind, msg string, index int, reason string) *errors.CodedError {
	return errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_PARAM_MISSING, msg,
		map[string]any{"index": index, "kind": kind, "param": "formula", "reason": reason})
}

// BaselinePosition reads params.baseline_position: (position, true,
// false) for a non-negative integer, (0, false, false) when it is
// absent or null, (0, false, true) for any other value.
func BaselinePosition(params map[string]any) (position int, set, bad bool) {
	raw, present := params[BaselineParam]
	if !present || raw == nil {
		return 0, false, false
	}
	f, ok := raw.(float64)
	if !ok || math.IsNaN(f) || math.IsInf(f, 0) || f < 0 || f != math.Trunc(f) || f > math.MaxInt32 {
		return 0, false, true
	}
	return int(f), true, false
}

// SpecBaselinePosition is BaselinePosition over spec.Params (a
// non-object Params reads as absent).
func SpecBaselinePosition(spec *types.OverlaySpec) (position int, set bool) {
	if spec == nil || len(spec.Params) == 0 {
		return 0, false
	}
	var m map[string]any
	if err := paramread.Decode(paramread.Overlay, string(spec.Kind), spec.Params, &m); err != nil {
		return 0, false
	}
	paramread.Read(paramread.Overlay, string(spec.Kind), BaselineParam)
	position, set, bad := BaselinePosition(m)
	return position, set && !bad
}

// BaselineOutOfRange is the refusal of a SERIES FORMULA whose
// params.baseline_position is at or past the end of a partition of
// seriesLength rows: PULSE_OVERLAY_REF_UNKNOWN {index, kind,
// baseline_position, series_length}. The runtime raises it off the
// materialised partition; predict raises it when the partition length is
// bounded by the schema (every in-partition grouper a GROUP_CATEGORY
// over a categorical field: the product of the dictionary sizes) and
// the position reaches the bound — series_length then carries the bound.
// A data-dependent axis is judged by the runtime only. A negative index
// omits Details.index (the runtime handler, whose dispatcher stamps it).
func BaselineOutOfRange(kind types.OverlayKind, index, position, seriesLength int) *errors.CodedError {
	details := map[string]any{"kind": string(kind), BaselineParam: position, "series_length": seriesLength}
	if index >= 0 {
		details["index"] = index
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_REF_UNKNOWN,
		"overlay "+string(kind)+" params.baseline_position is out of range for the host series", details)
}
