package overlayhost

import (
	"bytes"
	"encoding/json"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/paramread"
	"github.com/frankbardon/pulse/types"
)

// ChiSqMatrixParams is a decoded OVERLAY_CHISQ_MATRIX params block,
// shared by predict (internal/descriptor) and runtime
// (internal/processing) so both raise the same code, message and
// details (U64 #644).
type ChiSqMatrixParams struct {
	// Correct applies Yates' continuity correction to a 2 × 2 host,
	// TEST_CHISQ params.correct's rule (R's chisq.test(correct = TRUE)).
	// Default false, the historical statistic; inert on any larger
	// table.
	Correct bool
}

// ParseChiSqMatrixParams decodes spec.Params strictly: only `correct`
// (a JSON boolean) is accepted. Anything else is
// PULSE_OVERLAY_PARAM_MISSING with details {kind, param?, ...} plus
// index when index >= 0 (OVERLAY_SYMMETRY's rule).
func ParseChiSqMatrixParams(spec *types.OverlaySpec, index int) (ChiSqMatrixParams, *errors.CodedError) {
	var out ChiSqMatrixParams
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
