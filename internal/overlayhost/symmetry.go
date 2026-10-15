package overlayhost

import (
	"bytes"
	"encoding/json"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/paramread"
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
	var in struct {
		Correct json.RawMessage `json:"correct"`
	}
	paramread.Declare(paramread.Overlay, string(spec.Kind), &in)
	raw := bytes.TrimSpace(spec.Params)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return out, nil
	}
	if err := paramread.DecodeStrict(paramread.Overlay, string(spec.Kind), raw, &in); err != nil {
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
