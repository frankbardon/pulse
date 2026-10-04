package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse"
	perr "github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/mcp/toolmeta"
	"github.com/frankbardon/pulse/types"
)

// weightingFeatures is a profile with every request host and the
// weight-bearing slots, without capability:weighting.
var weightingFeatures = []string{
	"capability:process", "capability:compose", "capability:process_chain", "capability:crosstab",
	"AGG_SUM", "AGG_COUNT", "GROUP_CATEGORY", "OVERLAY_SHARE_OF_ROW",
}

func weightingInstance(t *testing.T, with bool) (*pulse.Pulse, *descx.InstanceSnapshot) {
	t.Helper()
	features := append([]string(nil), weightingFeatures...)
	if with {
		features = append(features, descx.FeatureWeighting)
	}
	return instanceFor(t, &pulse.FeatureProfile{Features: features}, pulse.Extensions{})
}

// TestBindForInstance_WeightingHidden: with capability:weighting hidden
// no bound tool schema carries a `weight` property — request root or
// nested (aggregations, crosstab, groups, overlays, tests …), in the
// process / predict / compose / chain tools alike.
func TestBindForInstance_WeightingHidden(t *testing.T) {
	schema := makeBindSchema()
	_, on := weightingInstance(t, true)
	_, off := weightingInstance(t, false)
	enabled, err := BindForInstance(schema, on)
	if err != nil {
		t.Fatal(err)
	}
	hidden, err := BindForInstance(schema, off)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{toolmeta.ToolProcess, toolmeta.ToolPredict, toolmeta.ToolCompose, toolmeta.ToolProcessChain} {
		if n := bytes.Count(enabled[tool], []byte(`"weight":`)); n < 2 {
			t.Fatalf("vacuous: %s with weighting carries %d weight properties", tool, n)
		}
	}
	for tool, body := range hidden {
		if bytes.Contains(body, []byte(`"weight":`)) {
			t.Errorf("%s: weighting hidden, bound schema still carries a weight property", tool)
		}
	}
}

// TestCheckUnknownKeys_HiddenWeightRefusedLikeLibrary: a request-root
// weight sent over MCP is refused exactly as the library refuses it.
func TestCheckUnknownKeys_HiddenWeightRefusedLikeLibrary(t *testing.T) {
	_, off := weightingInstance(t, false)
	sameRefusal(t, "root weight",
		checkUnknownRequestKeys([]byte(`{"weight":{"field":"score"}}`), off),
		codedOf(t, descx.SlotRefusal(&types.Request{Weight: &types.WeightSpec{Field: "score"}}, off)))
	_, on := weightingInstance(t, true)
	if ce := checkUnknownRequestKeys([]byte(`{"weight":{"field":"score"}}`), on); ce != nil {
		t.Errorf("weighting enabled: root weight refused: %v", ce)
	}
}

// TestTools_InvokeRefusesHiddenNestedWeight: a nested per-slot weight —
// an explicit null included — reaches the facade's request-slot gate
// through the MCP decode and is refused located, as the library refuses
// the same typed request.
func TestTools_InvokeRefusesHiddenNestedWeight(t *testing.T) {
	p, off := weightingInstance(t, false)
	invoke := map[string]InvokeFunc{}
	for _, d := range Tools(Config{}) {
		invoke[d.Name] = d.Invoke
	}
	cases := []struct {
		tool string
		body string
		want any
	}{
		{toolmeta.ToolProcess,
			`{"cohort":{"filename":"x.pulse"},"aggregations":[{"type":"AGG_SUM","field":"score","weight":null}]}`,
			&types.Request{Cohort: &types.Cohort{Filename: "x.pulse"}, Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score", Weight: types.NullSlotWeight()}}}},
		{toolmeta.ToolCompose,
			`{"requests":[{"cohort":{"filename":"x.pulse"}},{"cohort":{"filename":"x.pulse"},"groups":[{"type":"GROUP_CATEGORY","field":"category","weight":"score"}]}]}`,
			&types.ComposedRequest{Requests: []*types.Request{
				{Cohort: &types.Cohort{Filename: "x.pulse"}},
				{Cohort: &types.Cohort{Filename: "x.pulse"}, Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "category", Weight: types.SlotWeightField("score")}}},
			}}},
	}
	for _, c := range cases {
		t.Run(c.tool, func(t *testing.T) {
			_, err := invoke[c.tool](context.Background(), p, json.RawMessage(c.body))
			got := codedOf(t, err)
			if got.Code != perr.PULSE_REQUEST_UNKNOWN_FIELD {
				t.Fatalf("code = %s (%s), want PULSE_REQUEST_UNKNOWN_FIELD", got.Code, got.Message)
			}
			sameRefusal(t, c.tool, got, codedOf(t, descx.SlotRefusal(c.want, off)))
		})
	}
}
