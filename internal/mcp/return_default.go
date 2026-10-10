package mcp

import (
	"bytes"
	"encoding/json"

	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/types"
)

// return_default.go is the MCP surface's `return` default: an MCP agent
// pays tokens for every key it is sent, so a request WITHOUT its own
// `return` block is shaped by the `standard` preset unless the host
// chose otherwise. The library default stays `full` (identity); only
// the MCP tools apply this layer. Precedence, highest first:
//
//  1. the request's own `return` block (a Compose slot's, a chain
//     stage's) — never touched here;
//  2. Config.DefaultReturn (gosdk.Config.DefaultReturn, the mcpserve
//     option, `pulse mcp --return`);
//  3. the instance default (pulse.Options.DefaultReturn, else the
//     feature profile's `return`) — resolved by the engine itself, so
//     nothing is injected;
//  4. the built-in `standard` preset.
//
// The default is injected as the request's own block, so the engine's
// DisableComponents gate still wins over it (descx.EffectiveReturn): an
// engine-off instance keeps components off and only a request
// `disable_components: false` re-opens them.

// DefaultReturnPreset is the built-in MCP default.
const DefaultReturnPreset = types.ReturnPresetStandard

// Validate refuses a Config whose DefaultReturn is not a known preset
// (PULSE_RETURN_INVALID, the resolver's own error). Empty is valid.
func (c Config) Validate(inst *descx.InstanceSnapshot) error {
	if c.DefaultReturn == "" {
		return nil
	}
	return descx.ValidateDefaultReturn(&types.Return{Preset: c.DefaultReturn}, inst)
}

// returnDefault is the block an MCP request without its own `return`
// receives on inst, nil when nothing is injected: the instance's own
// default applies (no config preset), or the resolved preset is `full`
// on an instance with no default — identity, so the request stays
// byte-identical to a library call.
func (c Config) returnDefault(inst *descx.InstanceSnapshot) *types.Return {
	instDefault := inst.DefaultReturn() != nil
	preset := c.DefaultReturn
	switch {
	case preset != "":
	case instDefault:
		return nil
	default:
		preset = DefaultReturnPreset
	}
	if preset == types.ReturnPresetFull && !instDefault {
		return nil
	}
	return &types.Return{Preset: preset}
}

// fillReturn sets req.Return to a fresh copy of def when the request
// carries none.
func fillReturn(req *types.Request, def *types.Return) {
	if req != nil && req.Return == nil && def != nil {
		r := *def
		req.Return = &r
	}
}

// withReturnDefault wraps a decode so every request slot the decoded
// input carries without a `return` receives cfg's MCP default.
func withReturnDefault[In any](cfg Config, decode decodeFunc[In], fill func(*In, *types.Return)) decodeFunc[In] {
	return func(raw json.RawMessage, inst *descx.InstanceSnapshot) (In, error) {
		in, err := decode(raw, inst)
		if err != nil {
			return in, err
		}
		fill(&in, cfg.returnDefault(inst))
		return in, nil
	}
}

func fillRequestReturn(in *types.Request, def *types.Return) { fillReturn(in, def) }

// fillComposeReturn fills each slot; the Compose-level `return` shapes
// only the top-level overlays and is left to the caller. A sweep's body
// is a slot too: it receives the same default, so an expanded slot is
// shaped exactly as an explicit one sent without a `return`.
func fillComposeReturn(in *types.ComposedRequest, def *types.Return) {
	for _, slot := range in.Requests {
		fillReturn(slot, def)
	}
	if in.Sweep != nil {
		in.Sweep.Request = fillRawReturn(in.Sweep.Request, def)
	}
}

// fillRawReturn adds def as the `return` key of a raw request body that
// carries none (a sweep body, still holding its axis placeholders). A
// body that is not a JSON object is returned untouched — the sweep's
// own validation refuses it with its coded error.
func fillRawReturn(body json.RawMessage, def *types.Return) json.RawMessage {
	if def == nil {
		return body
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(body, &obj) != nil || obj == nil {
		return body
	}
	if _, set := obj["return"]; set {
		return body
	}
	ret, err := json.Marshal(def)
	if err != nil {
		return body
	}
	obj["return"] = ret
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if enc.Encode(obj) != nil {
		return body
	}
	return json.RawMessage(bytes.TrimRight(buf.Bytes(), "\n"))
}

func fillChainReturn(in *types.ChainRequest, def *types.Return) {
	for _, st := range in.Stages {
		if st != nil {
			fillReturn(st.Request, def)
		}
	}
}
