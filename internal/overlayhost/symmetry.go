package overlayhost

import (
	"bytes"
	"encoding/json"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// OVERLAY_SYMMETRY params, shared by predict (internal/descriptor) and
// runtime (internal/processing) so both raise the same code, message
// and details. Its square-host rule is OVERLAY_MARKOV's
// MarkovArityRefusal (the same grouper count on both axes); states
// align by MarkovStateName.

// SymmetryParams is a decoded OVERLAY_SYMMETRY params block.
type SymmetryParams struct {
	// Correct applies Edwards' continuity correction to a 2 × 2 table
	// (McNemar): (|n_12 − n_21| − 1)². Default true, R's
	// mcnemar.test(correct = TRUE); inert on a k × k table (Bowker).
	Correct bool
}

// ParseSymmetryParams decodes spec.Params strictly: only `correct` (a
// JSON boolean) is accepted. Anything else is PULSE_OVERLAY_PARAM_MISSING
// with details {kind, param?, ...} plus index when index >= 0.
func ParseSymmetryParams(spec *types.OverlaySpec, index int) (SymmetryParams, *errors.CodedError) {
	out := SymmetryParams{Correct: true}
	raw := bytes.TrimSpace(spec.Params)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return out, nil
	}
	var in struct {
		Correct json.RawMessage `json:"correct"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		return out, markovParamRefusal(spec.Kind, index, "",
			"params must be an object with only correct: "+err.Error(), nil)
	}
	if len(in.Correct) > 0 {
		var b *bool
		if err := json.Unmarshal(in.Correct, &b); err != nil || b == nil {
			return out, markovParamRefusal(spec.Kind, index, "correct",
				"correct must be true or false, got "+string(in.Correct),
				map[string]any{"correct": string(in.Correct)})
		}
		out.Correct = *b
	}
	return out, nil
}

// SymmetrySpecRefusal refuses an OVERLAY_SYMMETRY spec whose Ref is set
// (PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE, details {kind}: the test is
// implicit-margin and reads the whole table) or whose Scope is not
// matrix (PULSE_OVERLAY_SCOPE_UNSUPPORTED, details {kind, scope}), each
// plus index when >= 0. Predict and the runtime handler both call it, so
// the two raise the same code, message and details. nil when the spec
// passes.
func SymmetrySpecRefusal(spec *types.OverlaySpec, index int) *errors.CodedError {
	details := map[string]any{"kind": string(spec.Kind)}
	if index >= 0 {
		details["index"] = index
	}
	if spec.Ref != (types.OverlayRef{}) {
		return errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE,
			"overlay "+string(spec.Kind)+" must leave Ref empty (implicit-margin: the test reads the whole table)", details)
	}
	if spec.Scope != types.OverlayScopeMatrix {
		details["scope"] = string(spec.Scope)
		return errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED,
			"overlay "+string(spec.Kind)+" does not support scope "+string(spec.Scope)+" (supports: matrix)", details)
	}
	return nil
}
