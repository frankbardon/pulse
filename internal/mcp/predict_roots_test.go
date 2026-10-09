package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/mcp/toolmeta"
	"github.com/frankbardon/pulse/types"
)

// invokePredict calls pulse_predict through the catalog's Invoke under
// cfg and returns the wire bytes an agent reads.
func invokePredict(t *testing.T, p *pulse.Pulse, cfg Config, args string) ([]byte, error) {
	t.Helper()
	for _, td := range Tools(cfg) {
		if td.Name != toolmeta.ToolPredict {
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
		return body, nil
	}
	t.Fatal("pulse_predict not in the catalog")
	return nil, nil
}

// TestPulsePredict_BareRequestUnchanged: a bare request answers with the
// PredictResult keys at the root, byte-identical to the facade's
// Predict under the same MCP `return` default — no alternative key, no
// errors / warnings slot.
func TestPulsePredict_BareRequestUnchanged(t *testing.T) {
	p, _, path := flatManagedCohort(t)
	req := `{"cohort":{"filename":"` + path + `"},"aggregations":[{"type":"AGG_SUM","field":"amount","label":"total"}]}`
	got, err := invokePredict(t, p, Config{}, req)
	if err != nil {
		t.Fatalf("bare predict: %v", err)
	}

	var direct types.Request
	if err := json.Unmarshal([]byte(req), &direct); err != nil {
		t.Fatal(err)
	}
	fillReturn(&direct, Config{}.returnDefault(instanceOf(p)))
	res, err := p.Predict(context.Background(), &direct)
	if err != nil {
		t.Fatalf("facade predict: %v", err)
	}
	want, err := types.MarshalFinite(res)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("bare pulse_predict drifted from Pulse.Predict:\n got %s\nwant %s", got, want)
	}
}

// TestPulsePredict_AlternativeRoots: each alternative root is predicted
// by its own facade method and answers under the key it was sent under;
// a refused root carries its coded errors beside valid false.
func TestPulsePredict_AlternativeRoots(t *testing.T) {
	p, _, path := flatManagedCohort(t)
	cohort := `{"filename":"` + path + `"}`
	agg := `"aggregations":[{"type":"AGG_SUM","field":"amount","label":"total"}]`
	cases := []struct {
		name, args, key string
		valid           bool
		errCode         string
	}{
		{"composed", `{"composed":{"requests":[{"cohort":` + cohort + `,` + agg + `}]}}`, "composed", true, ""},
		{"facet", `{"facet":{"cohort":` + cohort + `,"fields":["amount"]}}`, "facet", true, ""},
		{"chain", `{"chain":{"cohort":` + cohort + `,"stages":[{"request":{"cohort":` + cohort + `,` + agg + `}}]}}`, "chain", true, ""},
		{"facet refused", `{"facet":{"cohort":` + cohort + `,"fields":["nope"]}}`, "facet", false, "x"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, err := invokePredict(t, p, Config{}, tc.args)
			if err != nil {
				t.Fatalf("invoke: %v", err)
			}
			var out map[string]any
			if err := json.Unmarshal(body, &out); err != nil {
				t.Fatal(err)
			}
			res, ok := out[tc.key].(map[string]any)
			if !ok {
				t.Fatalf("no %q verdict: %s", tc.key, body)
			}
			if len(out) > 3 {
				t.Errorf("unexpected root keys: %s", body)
			}
			if _, bare := out["valid"]; bare {
				t.Errorf("a bare PredictResult key leaked beside %q: %s", tc.key, body)
			}
			if res["valid"] != tc.valid {
				t.Fatalf("valid = %v, want %v: %s", res["valid"], tc.valid, body)
			}
			errs, _ := out["errors"].([]any)
			if tc.valid && len(errs) != 0 {
				t.Errorf("valid verdict carries errors: %s", body)
			}
			if !tc.valid && len(errs) == 0 {
				t.Errorf("valid false without its coded errors: %s", body)
			}
		})
	}
}

// TestPulsePredict_MixingRootsRefused: an alternative beside a bare
// request key or beside another alternative is SERVICE_VALIDATION.
func TestPulsePredict_MixingRootsRefused(t *testing.T) {
	p := defaultPulse(t)
	for _, args := range []string{
		`{"composed":{"requests":[]},"cohort":{"filename":"x.pulse"}}`,
		`{"facet":{"fields":["a"]},"chain":{"stages":[]}}`,
		`{"chain":{"stages":[]},"return":{"preset":"full"}}`,
	} {
		_, err := invokePredict(t, p, Config{}, args)
		ce := codedOf(t, err)
		if ce.Code != perr.SERVICE_VALIDATION {
			t.Errorf("%s: code = %s, want SERVICE_VALIDATION", args, ce.Code)
		}
	}
}

// TestPulsePredict_RootFaultsKeepTheirCodes: a facade error on an
// alternative root (a missing cohort, an unknown key inside a composed
// slot) comes back with its own code.
func TestPulsePredict_RootFaultsKeepTheirCodes(t *testing.T) {
	p := defaultPulse(t)
	for _, tc := range []struct {
		args string
		code perr.Code
	}{
		{`{"facet":{"fields":["a"]}}`, perr.SERVICE_VALIDATION},
		{`{"chain":{"stages":[]}}`, perr.SERVICE_VALIDATION},
		{`{"composed":{"requests":[{"cohort":{"filename":"x.pulse"},"groupers":[]}]}}`, perr.PULSE_REQUEST_UNKNOWN_FIELD},
	} {
		_, err := invokePredict(t, p, Config{}, tc.args)
		if ce := codedOf(t, err); ce.Code != tc.code {
			t.Errorf("%s: code = %s, want %s", tc.args, ce.Code, tc.code)
		}
	}
}

// TestPulsePredict_HiddenRootIsUnknownKey: on an instance whose profile
// hides compose / facet / process_chain, each alternative key is refused
// exactly as a misspelled request slot.
func TestPulsePredict_HiddenRootIsUnknownKey(t *testing.T) {
	p, _ := instanceFor(t, fixtureProfile(t, "minimal"), pulse.Extensions{})
	for _, args := range []string{
		`{"composed":{"requests":[]}}`,
		`{"facet":{"fields":["a"]}}`,
		`{"chain":{"stages":[]}}`,
	} {
		_, err := invokePredict(t, p, Config{}, args)
		if ce := codedOf(t, err); ce.Code != perr.PULSE_REQUEST_UNKNOWN_FIELD {
			t.Errorf("%s: code = %s, want PULSE_REQUEST_UNKNOWN_FIELD", args, ce.Code)
		}
	}
}

// TestPulsePredict_ReturnDefaultReachesAlternativeRoots: the MCP
// `return` default lands on each composed slot and chain stage, as
// pulse_compose / pulse_process_chain apply it.
func TestPulsePredict_ReturnDefaultReachesAlternativeRoots(t *testing.T) {
	def := &types.Return{Preset: types.ReturnPresetStandard}
	in := PredictIn{
		Composed: &types.ComposedRequest{Requests: []*types.Request{{}}},
	}
	fillPredictReturn(&in, def)
	if in.Composed.Requests[0].Return == nil || in.Request.Return != nil {
		t.Errorf("composed: slot return = %v, root return = %v", in.Composed.Requests[0].Return, in.Request.Return)
	}
	in = PredictIn{Chain: &types.ChainRequest{Stages: []*types.ChainStage{{Request: &types.Request{}}}}}
	fillPredictReturn(&in, def)
	if in.Chain.Stages[0].Request.Return == nil || in.Request.Return != nil {
		t.Errorf("chain: stage return = %v, root return = %v", in.Chain.Stages[0].Request.Return, in.Request.Return)
	}
	in = PredictIn{}
	fillPredictReturn(&in, def)
	if in.Request.Return == nil {
		t.Error("bare: root return not filled")
	}
}

// TestBindForInstance_PredictAdvertisesRoots: the bound pulse_predict
// schema is the bound request plus one property per alternative root,
// each carrying its execution tool's bound body; it requires no root
// key, and a profile hiding a root's capability drops that property.
func TestBindForInstance_PredictAdvertisesRoots(t *testing.T) {
	schema := makeBindSchema()
	doc := func(raw json.RawMessage) map[string]any {
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		return m
	}

	bound, err := Bind(schema)
	if err != nil {
		t.Fatal(err)
	}
	pred := doc(bound[toolmeta.ToolPredict])
	if _, ok := pred["required"]; ok {
		t.Errorf("pulse_predict requires a root key: %v", pred["required"])
	}
	props := pred["properties"].(map[string]any)
	for key, tool := range map[string]string{
		"composed": toolmeta.ToolCompose,
		"facet":    toolmeta.ToolFacetSchema,
		"chain":    toolmeta.ToolProcessChain,
	} {
		root, ok := props[key].(map[string]any)
		if !ok {
			t.Fatalf("pulse_predict schema lacks the %q root", key)
		}
		want := doc(bound[tool])
		got, _ := json.Marshal(root["properties"])
		exp, _ := json.Marshal(want["properties"])
		if !bytes.Equal(got, exp) {
			t.Errorf("%q root does not carry %s's bound body", key, tool)
		}
	}
	for k := range doc(bound[toolmeta.ToolProcess])["properties"].(map[string]any) {
		if _, ok := props[k]; !ok {
			t.Errorf("pulse_predict schema lost request slot %q", k)
		}
	}

	_, minimal := instanceFor(t, fixtureProfile(t, "minimal"), pulse.Extensions{})
	bound, err = BindForInstance(schema, minimal)
	if err != nil {
		t.Fatal(err)
	}
	props = doc(bound[toolmeta.ToolPredict])["properties"].(map[string]any)
	for _, key := range []string{"composed", "facet", "chain"} {
		if _, ok := props[key]; ok {
			t.Errorf("minimal: pulse_predict still advertises hidden root %q", key)
		}
	}
}
