package mcp

import (
	"bytes"
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

// TestPulseCompose_SweepRanking: pulse_compose returns the sweep
// `ranking` — read off each slot before the MCP `return` default shapes
// it — ordered, 1-based and trimmed by `top`, every response kept,
// each entry carrying its slot and typed axis values.
func TestPulseCompose_SweepRanking(t *testing.T) {
	p, _ := newImportTestPulse(t)
	if _, err := p.ImportFile(context.Background(), pulse.ImportSpec{SourcePath: "data.csv"}); err != nil {
		t.Fatalf("ImportFile: %v", err)
	}
	body := `{"cohort":{"filename":"imports/data.pulse"},"tests":[{"type":"TEST_T","field":"amount","label":"t","params":{"mu":{"$var":"mu"}}}]}`
	args := `{"sweep":{"axes":[{"name":"mu","values":[40,0,20]}],"label":"mu{{mu}}","request":` + body +
		`,"rank":{"by":"tests.t.statistic","order":"desc","top":2}}}`
	out, err := invokeSweepTool(t, p, Config{}, toolmeta.ToolCompose, args)
	if err != nil {
		t.Fatalf("pulse_compose ranked sweep: %v", err)
	}
	if resps, _ := out["responses"].([]any); len(resps) != 3 {
		t.Fatalf("%d responses, want every slot (3): %v", len(resps), out)
	}
	ranking, _ := out["ranking"].([]any)
	if len(ranking) != 2 {
		t.Fatalf("ranking %v, want the top 2", out["ranking"])
	}
	for i, want := range []struct {
		label string
		slot  float64
		mu    float64
	}{{"mu0", 1, 0}, {"mu20", 2, 20}} {
		e := ranking[i].(map[string]any)
		axes, _ := e["axes"].(map[string]any)
		if e["label"] != want.label || e["rank"] != float64(i+1) || e["slot"] != want.slot || axes["mu"] != want.mu {
			t.Fatalf("ranking[%d] = %v, want %s at rank %d, slot %v, axes mu %v", i, e, want.label, i+1, want.slot, want.mu)
		}
	}
}

// TestPulseCompose_SampledSweepMatchesLibrary: pulse_compose runs a
// seeded lhs sweep to the library's ComposedResponse byte for byte —
// the same drawn slots, the echoed sweep_seed, the ranking — when the
// sweep body carries its own `return` (which the MCP default never
// overrides).
func TestPulseCompose_SampledSweepMatchesLibrary(t *testing.T) {
	p, _ := newImportTestPulse(t)
	if _, err := p.ImportFile(context.Background(), pulse.ImportSpec{SourcePath: "data.csv"}); err != nil {
		t.Fatalf("ImportFile: %v", err)
	}
	body := `{"cohort":{"filename":"imports/data.pulse"},"return":{"preset":"full"},
		"tests":[{"type":"TEST_T","field":"amount","label":"t","params":{"mu":{"$var":"mu"}}}]}`
	args := `{"sweep":{"mode":"lhs","samples":5,"rng_seed":13,"axes":[{"name":"mu","range":{"min":0,"max":40}}],
		"request":` + body + `,"rank":{"by":"tests.t.statistic"}}}`
	out := invokeTool(t, p, Config{}, "pulse_compose", args)
	var req pulse.ComposedRequest
	if err := json.Unmarshal([]byte(args), &req); err != nil {
		t.Fatal(err)
	}
	lib, err := p.Compose(context.Background(), &req)
	if err != nil {
		t.Fatalf("library Compose: %v", err)
	}
	if len(lib.Responses) != 5 || len(lib.Ranking) != 5 || lib.SweepSeed == nil || *lib.SweepSeed != 13 {
		t.Fatalf("library: %d responses, ranking %d, seed %v; want 5, 5 and 13", len(lib.Responses), len(lib.Ranking), lib.SweepSeed)
	}
	want, err := types.MarshalFinite(lib)
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	if err := json.Compact(&got, out); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Bytes(), want) {
		t.Fatalf("MCP result differs from the library response:\n mcp %s\n lib %s", got.Bytes(), want)
	}
}
