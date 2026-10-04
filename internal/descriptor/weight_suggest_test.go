package descriptor

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/types"
)

// TestSuggestWeight_Rule: a suggestion only for a weight-capable column
// the schema carries, never with capability:weighting hidden.
func TestSuggestWeight_Rule(t *testing.T) {
	want := descriptor.SuggestedWeight{Field: "w", Source: "spss_sidecar", Kind: "probability"}
	if got := SuggestWeight(weightSchema(), "w", nil); got == nil || *got != want {
		t.Fatalf("SuggestWeight(w) = %+v, want %+v", got, want)
	}
	for _, v := range []string{"", "gone", "cat", "d", "dec"} {
		if got := SuggestWeight(weightSchema(), v, nil); got != nil {
			t.Errorf("SuggestWeight(%q) = %+v, want nil", v, got)
		}
	}
	if got := SuggestWeight(weightSchema(), "w", scopedExcept(FeatureWeighting)); got != nil {
		t.Errorf("hidden weighting: %+v, want nil", got)
	}
}

// TestInspect_SuggestedWeightFromMetadata: the suggestion is judged on
// the schema plus the sidecar fact the caller hands in; plain Inspect
// (no fact) never suggests.
func TestInspect_SuggestedWeightFromMetadata(t *testing.T) {
	data := buildTestPulseFile(t, weightSchema())
	meta := InspectMetadata{WeightVariable: "w"}
	clean := InspectWith(bytes.NewReader(data), nil, meta)
	res := clean.Data.(*descriptor.InspectResult)
	if res.SuggestedWeight == nil || res.SuggestedWeight.Field != "w" {
		t.Fatalf("suggested_weight = %+v", res.SuggestedWeight)
	}
	if plain := Inspect(bytes.NewReader(data), nil).Data.(*descriptor.InspectResult); plain.SuggestedWeight != nil {
		t.Errorf("no sidecar fact: suggested_weight = %+v, want nil", plain.SuggestedWeight)
	}
	if hidden := InspectWith(bytes.NewReader(data), nil, InspectMetadata{WeightVariable: "w", Instance: scopedExcept(FeatureWeighting)}); hidden.Data.(*descriptor.InspectResult).SuggestedWeight != nil {
		t.Errorf("hidden weighting still suggests")
	}
}

// TestPredict_SuggestedWeightIsDataNotWarning: predict echoes the
// suggestion as data under Strict with no warning when no weight
// resolves, and not beside a resolved one or with weighting hidden.
func TestPredict_SuggestedWeightIsDataNotWarning(t *testing.T) {
	data := buildTestPulseFile(t, weightSchema())
	req := &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "x"}}}

	// The fixture's terse descriptions warn on their own; the echo must
	// add nothing to that, strict or not.
	base := predictFromBytes(data, req, &PredictOptions{Strict: true})
	env := predictFromBytes(data, req, &PredictOptions{Strict: true, SuggestedWeightVariable: "w"})
	res := env.Data.(*descriptor.PredictResult)
	if len(env.Errors) != len(base.Errors) || len(env.Warnings) != len(base.Warnings) {
		t.Fatalf("strict predict: the echo added diagnostics: errors %d→%d warnings %d→%d",
			len(base.Errors), len(env.Errors), len(base.Warnings), len(env.Warnings))
	}
	if res.SuggestedWeight == nil || res.SuggestedWeight.Field != "w" {
		t.Fatalf("suggested_weight = %+v", res.SuggestedWeight)
	}
	raw, _ := json.Marshal(env)
	if strings.Contains(string(raw), `"weights"`) {
		t.Errorf("the echo resolved a weight: %s", raw)
	}

	env = predictFromBytes(data, req, &PredictOptions{SuggestedWeightVariable: "w", DefaultWeight: &types.WeightSpec{Field: "wi"}})
	if got := env.Data.(*descriptor.PredictResult).SuggestedWeight; got != nil {
		t.Errorf("beside a resolved default weight: %+v, want nil", got)
	}

	env = predictFromBytes(data, req, &PredictOptions{SuggestedWeightVariable: "w", Instance: scopedExcept(FeatureWeighting)})
	if got := env.Data.(*descriptor.PredictResult).SuggestedWeight; got != nil {
		t.Errorf("hidden weighting: %+v, want nil", got)
	}
}
