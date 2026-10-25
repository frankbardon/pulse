package mcp

import (
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/mcp/toolmeta"
)

// optimizeFeatures is an instance offering Compose without its optimize
// blocks.
var optimizeFeatures = []string{"capability:compose", "AGG_SUM", "GROUP_CATEGORY"}

// TestOptimize_MCPSchemaHidesOptimize: the bound compose schema of an
// instance without capability:compose_optimize has no `optimize` key;
// the instance offering it describes the block (name, variables,
// objective, direction required).
func TestOptimize_MCPSchemaHidesOptimize(t *testing.T) {
	_, hidden := instanceFor(t, &pulse.FeatureProfile{Features: optimizeFeatures}, pulse.Extensions{})
	_, enabled := instanceFor(t, &pulse.FeatureProfile{Features: append(append([]string(nil), optimizeFeatures...), "capability:compose_optimize")}, pulse.Extensions{})
	props := func(t *testing.T, raw json.RawMessage) map[string]any {
		var doc map[string]any
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatal(err)
		}
		return doc["properties"].(map[string]any)
	}
	raw, err := buildComposeSchemaWithExtensions(fieldClassification{}, hidden)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := props(t, raw)["optimize"]; ok {
		t.Fatal("the hiding instance's compose schema describes `optimize`")
	}
	raw, err = buildComposeSchemaWithExtensions(fieldClassification{}, enabled)
	if err != nil {
		t.Fatal(err)
	}
	opt, ok := props(t, raw)["optimize"].(map[string]any)
	if !ok {
		t.Fatal("the enabled instance's compose schema lacks `optimize`")
	}
	req, _ := json.Marshal(opt["items"].(map[string]any)["required"])
	if string(req) != `["name","variables","objective","direction"]` {
		t.Fatalf("required %s", req)
	}
}

// TestOptimize_MCPRefusesHiddenOptimize: on that instance a set
// `optimize` is refused as PULSE_REQUEST_UNKNOWN_FIELD by pulse_compose
// and pulse_predict {composed} alike.
func TestOptimize_MCPRefusesHiddenOptimize(t *testing.T) {
	fp := &pulse.FeatureProfile{Profile: "no-optimize", WrittenWith: "1.0.0", Features: optimizeFeatures}
	p, _ := instanceFor(t, fp, pulse.Extensions{})
	args := `{"requests":[{"cohort":{"filename":"imports/data.pulse"},"aggregations":[{"type":"AGG_SUM","field":"amount","label":"v"}]}],
		"optimize":[{"name":"o","direction":"minimize","variables":[{"name":"x","lower":0,"upper":1}],"objective":"x * x"}]}`
	_, err := invokeSweepTool(t, p, Config{}, toolmeta.ToolCompose, args)
	if ce := codedOf(t, err); ce.Code != perr.PULSE_REQUEST_UNKNOWN_FIELD {
		t.Fatalf("compose code = %s (%v)", ce.Code, ce)
	}
	out, err := invokeSweepTool(t, p, Config{}, toolmeta.ToolPredict, `{"composed":`+args+`}`)
	if err != nil {
		if ce := codedOf(t, err); ce.Code != perr.PULSE_REQUEST_UNKNOWN_FIELD {
			t.Fatalf("predict code = %s (%v)", ce.Code, ce)
		}
		return
	}
	errs, _ := out["errors"].([]any)
	if len(errs) == 0 || errs[0].(map[string]any)["code"] != string(perr.PULSE_REQUEST_UNKNOWN_FIELD) {
		t.Fatalf("predict errors = %v", errs)
	}
}
