package formulaident

import (
	"encoding/json"
	"math"

	exprparser "github.com/expr-lang/expr/parser"

	"github.com/frankbardon/pulse/errors"
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
//   - no usable params.formula: PULSE_OVERLAY_FORMULA_INVALID_IDENT
//     {index, kind, reason};
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
		if reason != "missing formula param" {
			msg = "overlay " + kind + " Params[\"formula\"] must be a non-empty string"
		}
		return errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_FORMULA_INVALID_IDENT, msg,
			map[string]any{"index": index, "kind": kind, "reason": reason})
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
// PULSE_OVERLAY_FORMULA_INVALID_IDENT {index, kind, reason} refusal
// when Params is empty or not an object.
func requestParams(spec *types.OverlaySpec, index int) (map[string]any, *errors.CodedError) {
	kind := string(spec.Kind)
	if len(spec.Params) == 0 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_FORMULA_INVALID_IDENT,
			"overlay "+kind+" requires Params[\"formula\"] (non-empty string); Params is missing or empty",
			map[string]any{"index": index, "kind": kind, "reason": "missing formula param"})
	}
	var m map[string]any
	if err := json.Unmarshal(spec.Params, &m); err != nil {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_FORMULA_INVALID_IDENT,
			"overlay "+kind+" Params must be a JSON object carrying \"formula\"",
			map[string]any{"index": index, "kind": kind, "reason": "params is not a JSON object"})
	}
	return m, nil
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
	if err := json.Unmarshal(spec.Params, &m); err != nil {
		return 0, false
	}
	position, set, bad := BaselinePosition(m)
	return position, set && !bad
}
