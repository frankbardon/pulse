package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"testing"

	"github.com/frankbardon/pulse"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/mcp/toolmeta"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

const returnDefaultStage = `"aggregations":[{"type":"AGG_SUM","field":"amount","label":"s"}],"groups":[{"type":"GROUP_CATEGORY","field":"name"}]`

// returnDefaultPulse builds an instance over the import fixture with
// opts (FS filled in) and imports data.csv.
func returnDefaultPulse(t *testing.T, opts pulse.Options) *pulse.Pulse {
	t.Helper()
	afs := afero.NewMemMapFs()
	if err := afero.WriteFile(afs, "data.csv", []byte("id,name,amount\n1,Alice,10.5\n2,Bob,20.0\n3,Carol,30.75\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	opts.FS = afs
	p, err := pulse.New(opts)
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	if _, err := p.ImportFile(context.Background(), pulse.ImportSpec{SourcePath: "data.csv"}); err != nil {
		t.Fatalf("ImportFile: %v", err)
	}
	return p
}

// invokeTool runs tool on p through the cfg-baked catalog and returns
// the marshalled output.
func invokeTool(t *testing.T, p *pulse.Pulse, cfg Config, tool, body string) []byte {
	t.Helper()
	for _, d := range Tools(cfg) {
		if d.Name != tool {
			continue
		}
		out, err := d.Invoke(context.Background(), p, json.RawMessage(body))
		if err != nil {
			t.Fatalf("%s: %v", tool, err)
		}
		b, err := json.Marshal(out)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	t.Fatalf("no tool %s", tool)
	return nil
}

func processBody(extra string) string {
	return `{"cohort":{"filename":"imports/data.pulse"},` + returnDefaultStage + extra + `}`
}

// presetOf reports the `returned` preset a single Response carries, ""
// when unshaped.
func presetOf(t *testing.T, b []byte) string {
	t.Helper()
	var r types.Response
	if err := json.Unmarshal(b, &r); err != nil {
		t.Fatalf("decode %s: %v", b, err)
	}
	if r.Returned == nil {
		return ""
	}
	return r.Returned.Preset
}

// TestMCPReturnDefault_StandardByDefault: an MCP request without a
// `return` block is shaped by `standard` (components absent, marker
// stamped) on every request-carrying tool; a request `full` is unshaped.
func TestMCPReturnDefault_StandardByDefault(t *testing.T) {
	p := returnDefaultPulse(t, pulse.Options{})
	cfg := Config{}

	b := invokeTool(t, p, cfg, toolmeta.ToolProcess, processBody(""))
	if got := presetOf(t, b); got != "standard" || bytes.Contains(b, []byte(`"components"`)) {
		t.Errorf("process default: preset %q, output %s", got, b)
	}
	b = invokeTool(t, p, cfg, toolmeta.ToolProcess, processBody(`,"return":{"preset":"full"}`))
	if got := presetOf(t, b); got != "" || !bytes.Contains(b, []byte(`"components"`)) {
		t.Errorf("process full: preset %q, output %s", got, b)
	}

	var pr struct {
		Return *struct {
			Preset string `json:"preset"`
		} `json:"return"`
	}
	if err := json.Unmarshal(invokeTool(t, p, cfg, toolmeta.ToolPredict, processBody("")), &pr); err != nil {
		t.Fatal(err)
	}
	if pr.Return == nil || pr.Return.Preset != "standard" {
		t.Errorf("predict default plan = %+v, want standard", pr.Return)
	}

	req := processBody("")
	compose := invokeTool(t, p, cfg, toolmeta.ToolCompose, `{"requests":[`+req+`,`+processBody(`,"return":{"preset":"full"}`)+`]}`)
	var cr types.ComposedResponse
	if err := json.Unmarshal(compose, &cr); err != nil {
		t.Fatal(err)
	}
	if len(cr.Responses) != 2 || cr.Responses[0].Returned == nil || cr.Responses[0].Returned.Preset != "standard" ||
		cr.Responses[0].Components != nil || cr.Responses[1].Returned != nil || cr.Responses[1].Components == nil {
		t.Errorf("compose slots not defaulted per slot: %s", compose)
	}

	chain := invokeTool(t, p, cfg, toolmeta.ToolProcessChain, `{"cohort":{"filename":"imports/data.pulse"},"stages":[{"name":"a","request":{`+returnDefaultStage+`}}]}`)
	if !bytes.Contains(chain, []byte(`"preset":"standard"`)) || bytes.Contains(chain, []byte(`"components"`)) {
		t.Errorf("chain stage not defaulted: %s", chain)
	}
}

// TestMCPReturnDefault_Precedence: request > config > instance default
// (Options / feature profile) > built-in standard; config `full`
// restores the library's unshaped output byte for byte.
func TestMCPReturnDefault_Precedence(t *testing.T) {
	minimal := &types.Return{Preset: types.ReturnPresetMinimal}
	profile, err := pulse.ExampleFeatureProfile("minimal")
	if err != nil {
		t.Fatal(err)
	}
	profile.Return = minimal
	for _, tc := range []struct {
		name  string
		opts  pulse.Options
		cfg   Config
		extra string
		want  string
	}{
		{"request beats config", pulse.Options{}, Config{DefaultReturn: types.ReturnPresetMinimal}, `,"return":{"preset":"standard"}`, "standard"},
		{"config beats feature profile", pulse.Options{FeatureProfile: profile}, Config{DefaultReturn: types.ReturnPresetStandard}, "", "standard"},
		{"config beats instance default", pulse.Options{DefaultReturn: minimal}, Config{DefaultReturn: types.ReturnPresetStandard}, "", "standard"},
		{"config full beats feature profile", pulse.Options{FeatureProfile: profile}, Config{DefaultReturn: types.ReturnPresetFull}, "", ""},
		{"feature profile beats built-in", pulse.Options{FeatureProfile: profile}, Config{}, "", "minimal"},
		{"instance default beats built-in", pulse.Options{DefaultReturn: minimal}, Config{}, "", "minimal"},
		{"built-in standard", pulse.Options{}, Config{}, "", "standard"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := returnDefaultPulse(t, tc.opts)
			if got := presetOf(t, invokeTool(t, p, tc.cfg, toolmeta.ToolProcess, processBody(tc.extra))); got != tc.want {
				t.Errorf("preset = %q, want %q", got, tc.want)
			}
		})
	}

	t.Run("config full is the library output", func(t *testing.T) {
		p := returnDefaultPulse(t, pulse.Options{})
		var req types.Request
		if err := json.Unmarshal([]byte(processBody("")), &req); err != nil {
			t.Fatal(err)
		}
		res, err := p.Process(context.Background(), &req)
		if err != nil {
			t.Fatal(err)
		}
		want, err := json.Marshal(res)
		if err != nil {
			t.Fatal(err)
		}
		got := invokeTool(t, p, Config{DefaultReturn: types.ReturnPresetFull}, toolmeta.ToolProcess, processBody(""))
		if !bytes.Equal(got, want) {
			t.Errorf("--return full differs from the library:\n got %s\nwant %s", got, want)
		}
		// Nothing is injected, so the request (and predict's plan) is
		// the library's too: no `return` block, no plan.
		if pr := invokeTool(t, p, Config{DefaultReturn: types.ReturnPresetFull}, toolmeta.ToolPredict, processBody("")); bytes.Contains(pr, []byte(`"return"`)) {
			t.Errorf("--return full injected a block: %s", pr)
		}
	})
}

// TestMCPReturnDefault_EngineDisableComponentsSticks: an engine-off
// instance keeps components off under a request `return` (default or
// explicit full); only a request disable_components:false re-opens them.
func TestMCPReturnDefault_EngineDisableComponentsSticks(t *testing.T) {
	p := returnDefaultPulse(t, pulse.Options{DisableComponents: true})
	for _, cfg := range []Config{{}, {DefaultReturn: types.ReturnPresetFull}} {
		if b := invokeTool(t, p, cfg, toolmeta.ToolProcess, processBody(`,"return":{"preset":"full"}`)); bytes.Contains(b, []byte(`"components"`)) {
			t.Errorf("cfg %+v: request full re-opened engine-off components: %s", cfg, b)
		}
	}
	b := invokeTool(t, p, Config{}, toolmeta.ToolProcess, processBody(`,"return":{"preset":"full"},"disable_components":false`))
	if !bytes.Contains(b, []byte(`"components"`)) {
		t.Errorf("disable_components:false did not re-open components: %s", b)
	}
}

// TestMCPReturnDefault_ValidateRefusesUnknownPreset: an unknown config
// preset is PULSE_RETURN_INVALID; empty and every real preset pass.
func TestMCPReturnDefault_ValidateRefusesUnknownPreset(t *testing.T) {
	err := Config{DefaultReturn: "lean"}.Validate(nil)
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_RETURN_INVALID {
		t.Fatalf("Validate(lean) = %v, want PULSE_RETURN_INVALID", err)
	}
	for _, p := range append([]types.ReturnPreset{""}, types.AllReturnPresets()...) {
		if err := (Config{DefaultReturn: p}).Validate(nil); err != nil {
			t.Errorf("Validate(%q) = %v", p, err)
		}
	}
}
