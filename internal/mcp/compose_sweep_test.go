package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/mcp/toolmeta"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// invokeSweepTool calls one catalog tool under cfg and returns its output as
// the wire JSON an agent reads.
func invokeSweepTool(t *testing.T, p *pulse.Pulse, cfg Config, tool, args string) (map[string]any, error) {
	t.Helper()
	for _, td := range Tools(cfg) {
		if td.Name != tool {
			continue
		}
		out, err := td.Invoke(context.Background(), p, json.RawMessage(args))
		if err != nil {
			return nil, err
		}
		body, err := types.MarshalFinite(out)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(body, &m); err != nil {
			t.Fatal(err)
		}
		return m, nil
	}
	t.Fatalf("%s not in the catalog", tool)
	return nil, nil
}

const mcpSweepBody = `{"cohort":{"filename":"imports/data.pulse"},"aggregations":[{"type":"{{op}}","field":"amount","label":"v"}]}`

// TestPulseCompose_RunsSweep: pulse_compose accepts a `sweep` through
// its strict decode and returns the explicit slot plus one response per
// combination, each sweep slot shaped by the MCP `return` default
// exactly as the explicit slot sent without a `return` is.
func TestPulseCompose_RunsSweep(t *testing.T) {
	p, _ := newImportTestPulse(t)
	if _, err := p.ImportFile(context.Background(), pulse.ImportSpec{SourcePath: "data.csv"}); err != nil {
		t.Fatalf("ImportFile: %v", err)
	}
	slot := `{"cohort":{"filename":"imports/data.pulse"},"aggregations":[{"type":"AGG_SUM","field":"amount","label":"v"}]}`
	args := `{"requests":[` + slot + `],"sweep":{"axes":[{"name":"op","values":["AGG_SUM","AGG_MAX"]}],"request":` + mcpSweepBody + `}}`
	out, err := invokeSweepTool(t, p, Config{}, toolmeta.ToolCompose, args)
	if err != nil {
		t.Fatalf("pulse_compose sweep: %v", err)
	}
	resps, _ := out["responses"].([]any)
	if len(resps) != 3 {
		t.Fatalf("%d responses, want 3 (1 explicit + 2 sweep): %v", len(resps), out)
	}
	explicit, _ := json.Marshal(resps[0].(map[string]any)["returned"])
	if string(explicit) == "null" {
		t.Fatalf("vacuous: the explicit slot carries no MCP return default: %v", resps[0])
	}
	for i, r := range resps[1:] {
		got, _ := json.Marshal(r.(map[string]any)["returned"])
		if string(got) != string(explicit) {
			t.Errorf("sweep slot %d returned %s, want the explicit slot's %s", i+1, got, explicit)
		}
	}
}

// TestFillRawReturn: the MCP default lands on a sweep body with no
// `return`, never over an authored one, and leaves a non-object body to
// the sweep's own validation.
func TestFillRawReturn(t *testing.T) {
	def := &types.Return{Preset: types.ReturnPresetStandard}
	got := string(fillRawReturn(json.RawMessage(`{"label_x":"<{{op}}>"}`), def))
	if !strings.Contains(got, `"return":{"preset":"standard"}`) || !strings.Contains(got, `"<{{op}}>"`) {
		t.Errorf("filled body = %s", got)
	}
	for _, body := range []string{`{"return":{"preset":"full"}}`, `[1]`, `null`, `"x"`} {
		if got := string(fillRawReturn(json.RawMessage(body), def)); got != body {
			t.Errorf("%s rewritten to %s", body, got)
		}
	}
	if got := string(fillRawReturn(json.RawMessage(`{}`), nil)); got != `{}` {
		t.Errorf("nil default rewrote the body: %s", got)
	}
}

// TestPulseCompose_SweepFaultsKeepTheirCodes: an invalid sweep comes back
// from pulse_compose and pulse_predict {composed} as PULSE_SWEEP_INVALID;
// on an instance offering Compose but not capability:compose_sweep the
// `sweep` key is refused as PULSE_REQUEST_UNKNOWN_FIELD on both tools.
func TestPulseCompose_SweepFaultsKeepTheirCodes(t *testing.T) {
	sweepArgs := `{"requests":[],"sweep":{"axes":[{"name":"op","values":["AGG_SUM"]}],"request":` + mcpSweepBody + `}}`
	invalid := `{"requests":[],"sweep":{"axes":[],"request":{}}}`

	fp, err := pulse.ParseFeatureProfile([]byte(`{"profile":"compose-no-sweep","written_with":"1.0.0",
		"features":["capability:compose","AGG_SUM","AGG_MAX","GROUP_CATEGORY"]}`))
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	noSweep, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(), FeatureProfile: fp})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}

	for _, tc := range []struct {
		name string
		p    *pulse.Pulse
		args string
		code perr.Code
	}{
		{"invalid", defaultPulse(t), invalid, perr.PULSE_SWEEP_INVALID},
		{"hidden", noSweep, sweepArgs, perr.PULSE_REQUEST_UNKNOWN_FIELD},
	} {
		t.Run(tc.name+"/compose", func(t *testing.T) {
			_, err := invokeSweepTool(t, tc.p, Config{}, toolmeta.ToolCompose, tc.args)
			if ce := codedOf(t, err); ce.Code != tc.code {
				t.Fatalf("code = %s, want %s (%v)", ce.Code, tc.code, ce)
			}
		})
		t.Run(tc.name+"/predict", func(t *testing.T) {
			out, err := invokeSweepTool(t, tc.p, Config{}, toolmeta.ToolPredict, `{"composed":`+tc.args+`}`)
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
