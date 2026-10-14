package overlayhost

import (
	"bytes"
	"encoding/json"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// OVERLAY_RETENTION params and spec refusals, shared by predict
// (internal/descriptor) and runtime (internal/processing) so both raise
// the same code, message and details. Its host rules are OVERLAY_MARKOV's:
// MarkovArityRefusal (the same grouper count on both axes), states
// aligned by MarkovStateName, and MarkovStateRefusal for an absorbing
// name the host lacks (runtime only: predict reads no record).

// RetentionParams is a decoded OVERLAY_RETENTION params block.
type RetentionParams struct {
	// Absorbing lists the named absorbing states a move into counts as
	// churn (nil when omitted: no churn split, switch = 1 − stay).
	Absorbing []string
}

// ParseRetentionParams decodes spec.Params strictly: only `absorbing`
// (a non-empty array of distinct non-empty state names, OVERLAY_MARKOV's
// form) is accepted. Anything else is PULSE_OVERLAY_PARAM_MISSING with
// details {kind, param?, ...} plus index when index >= 0.
func ParseRetentionParams(spec *types.OverlaySpec, index int) (RetentionParams, *errors.CodedError) {
	var out RetentionParams
	raw := bytes.TrimSpace(spec.Params)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return out, nil
	}
	var in struct {
		Absorbing json.RawMessage `json:"absorbing"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		return out, markovParamRefusal(spec.Kind, index, "",
			"params must be an object with only absorbing: "+err.Error(), nil)
	}
	if len(in.Absorbing) > 0 {
		names, cerr := parseAbsorbing(spec.Kind, in.Absorbing, index)
		if cerr != nil {
			return out, cerr
		}
		out.Absorbing = names
	}
	return out, nil
}

// RetentionSpecRefusal refuses an OVERLAY_RETENTION spec whose Ref is
// set (PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE, details {kind}: the
// rates read the whole table) or whose Scope is not row
// (PULSE_OVERLAY_SCOPE_UNSUPPORTED, details {kind, scope}), each plus
// index when >= 0. Predict and the runtime handler both call it, so the
// two raise the same code, message and details. nil when the spec
// passes.
func RetentionSpecRefusal(spec *types.OverlaySpec, index int) *errors.CodedError {
	return implicitSpecRefusal(spec, index, types.OverlayScopeRow, "the rates read the whole table")
}

// implicitSpecRefusal is the shared Ref / Scope refusal of an
// implicit-margin kind with one supported scope (SymmetrySpecRefusal,
// RetentionSpecRefusal): a set Ref first, then a scope other than
// scope. why completes the Ref message's parenthesis.
func implicitSpecRefusal(spec *types.OverlaySpec, index int, scope types.OverlayScope, why string) *errors.CodedError {
	details := map[string]any{"kind": string(spec.Kind)}
	if index >= 0 {
		details["index"] = index
	}
	if spec.Ref != (types.OverlayRef{}) {
		return errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE,
			"overlay "+string(spec.Kind)+" must leave Ref empty (implicit-margin: "+why+")", details)
	}
	if spec.Scope != scope {
		details["scope"] = string(spec.Scope)
		return errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED,
			"overlay "+string(spec.Kind)+" does not support scope "+string(spec.Scope)+" (supports: "+string(scope)+")", details)
	}
	return nil
}
