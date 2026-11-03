package descriptor

import (
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestValidateOverlayRollingWindowParam_Forms: predict's rolling-window
// gate refuses a missing or non-object Params and a missing window as
// PULSE_OVERLAY_PARAM_MISSING, and a non-integral, non-numeric or
// non-positive window as PULSE_OVERLAY_LEVEL_OUT_OF_RANGE — the codes
// the runtime's extractRollingParams raises. An integral float (3.0, how
// JSON decodes 3) is a window.
func TestValidateOverlayRollingWindowParam_Forms(t *testing.T) {
	t.Parallel()

	cases := []struct {
		params string
		code   errors.Code
	}{
		{"", errors.PULSE_OVERLAY_PARAM_MISSING},
		{`[3]`, errors.PULSE_OVERLAY_PARAM_MISSING},
		{`{}`, errors.PULSE_OVERLAY_PARAM_MISSING},
		{`{"window": 2.5}`, errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE},
		{`{"window": "3"}`, errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE},
		{`{"window": 0}`, errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE},
		{`{"window": -2}`, errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE},
		{`{"window": 3}`, ""},
		{`{"window": 3.0}`, ""},
	}
	for _, tc := range cases {
		spec := &types.OverlaySpec{Kind: types.OverlayKindIndexVsRollingMean}
		if tc.params != "" {
			spec.Params = json.RawMessage(tc.params)
		}
		env := descriptor.NewEnvelope(nil)
		validateOverlayRollingWindowParam(env, spec, 0)
		switch {
		case tc.code == "" && len(env.Errors) != 0:
			t.Errorf("%s: refused %s", tc.params, env.Errors[0].Code)
		case tc.code != "" && (len(env.Errors) != 1 || env.Errors[0].Code != string(tc.code)):
			t.Errorf("%s: errors = %v, want one %s", tc.params, env.Errors, tc.code)
		}
	}
}

// TestCoerceIntegralOverlayParam: every Go integer width an embedder can
// hand a programmatic Params map is a window, and a float is one only
// when integral — the processing.coerceIntegralNumber rule.
func TestCoerceIntegralOverlayParam(t *testing.T) {
	t.Parallel()

	ok := map[any]int{int(4): 4, int32(5): 5, int64(6): 6, float64(7): 7, float32(8): 8}
	for in, want := range ok {
		if got, okk := coerceIntegralOverlayParam(in); !okk || got != want {
			t.Errorf("coerce(%T %v) = (%d, %v), want (%d, true)", in, in, got, okk, want)
		}
	}
	for _, in := range []any{float64(1.5), float32(2.25), "3", nil, true} {
		if _, okk := coerceIntegralOverlayParam(in); okk {
			t.Errorf("coerce(%T %v) accepted", in, in)
		}
	}
}
