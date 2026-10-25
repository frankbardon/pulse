package mcp

import (
	"reflect"
	"testing"

	"github.com/frankbardon/pulse"
	perr "github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/mcp/toolmeta"
)

// sweepSamplingFeatures is an instance offering the compose sweep
// without its sampling modes.
var sweepSamplingFeatures = []string{"capability:compose", "capability:compose_sweep", "AGG_SUM", "AGG_MAX", "GROUP_CATEGORY"}

// TestSweepRandom_MCPSchemaHidesSampling: the bound sweep schema of an
// instance without capability:compose_sweep_sampling offers grid / zip
// only, with no samples, rng_seed or axis range; the full instance
// offers all four modes and the sampling keys.
func TestSweepRandom_MCPSchemaHidesSampling(t *testing.T) {
	_, hidden := instanceFor(t, &pulse.FeatureProfile{Features: sweepSamplingFeatures}, pulse.Extensions{})
	_, enabled := instanceFor(t, &pulse.FeatureProfile{Features: append(append([]string(nil), sweepSamplingFeatures...),
		"capability:compose_sweep_sampling")}, pulse.Extensions{})
	for _, c := range []struct {
		name  string
		inst  *descx.InstanceSnapshot
		modes []string
		keys  bool
	}{
		{"hidden", hidden, []string{"grid", "zip"}, false},
		{"enabled", enabled, []string{"grid", "zip", "random", "lhs"}, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			props := sweepSchema(c.inst)["properties"].(map[string]any)
			if got := props["mode"].(map[string]any)["enum"]; !reflect.DeepEqual(got, c.modes) {
				t.Errorf("mode enum %v, want %v", got, c.modes)
			}
			_, samples := props["samples"]
			_, seed := props["rng_seed"]
			_, rng := props["axes"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)["range"]
			if samples != c.keys || seed != c.keys || rng != c.keys {
				t.Errorf("samples %v rng_seed %v range %v; want all %v", samples, seed, rng, c.keys)
			}
		})
	}
}

// TestSweepRandom_MCPRefusesHiddenSampling: on that instance a sampled
// sweep's `samples` key is refused as PULSE_REQUEST_UNKNOWN_FIELD and a
// bare lhs mode as PULSE_SWEEP_INVALID, by pulse_compose and
// pulse_predict {composed} alike.
func TestSweepRandom_MCPRefusesHiddenSampling(t *testing.T) {
	fp := &pulse.FeatureProfile{Profile: "sweep-no-sampling", WrittenWith: "1.0.0", Features: sweepSamplingFeatures}
	p, _ := instanceFor(t, fp, pulse.Extensions{})
	body := `{"cohort":{"filename":"imports/data.pulse"},"aggregations":[{"type":"AGG_SUM","field":"amount","label":"v{{k}}"}]}`
	for _, tc := range []struct {
		name, args string
		code       perr.Code
	}{
		{"samples", `{"requests":[],"sweep":{"mode":"lhs","samples":3,"axes":[{"name":"k","range":{"min":1,"max":2}}],"request":` + body + `}}`, perr.PULSE_REQUEST_UNKNOWN_FIELD},
		{"mode", `{"requests":[],"sweep":{"mode":"lhs","axes":[{"name":"k","values":[1]}],"request":` + body + `}}`, perr.PULSE_SWEEP_INVALID},
	} {
		t.Run(tc.name+"/compose", func(t *testing.T) {
			_, err := invokeSweepTool(t, p, Config{}, toolmeta.ToolCompose, tc.args)
			if ce := codedOf(t, err); ce.Code != tc.code {
				t.Fatalf("code = %s, want %s (%v)", ce.Code, tc.code, ce)
			}
		})
		t.Run(tc.name+"/predict", func(t *testing.T) {
			out, err := invokeSweepTool(t, p, Config{}, toolmeta.ToolPredict, `{"composed":`+tc.args+`}`)
			if err != nil {
				if ce := codedOf(t, err); ce.Code != tc.code {
					t.Fatalf("code = %s, want %s (%v)", ce.Code, tc.code, ce)
				}
				return
			}
			errs, _ := out["errors"].([]any)
			if len(errs) == 0 || errs[0].(map[string]any)["code"] != string(tc.code) {
				t.Fatalf("errors = %v, want %s first", errs, tc.code)
			}
		})
	}
}
