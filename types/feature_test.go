package types_test

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/frankbardon/pulse/types"
)

func TestAllFeatureTypes_AlphabeticalAndComplete(t *testing.T) {
	got := types.AllFeatureTypes()
	want := []types.FeatureType{
		types.FEAT_BUCKETIZE,
		types.FEAT_DATE_FEATURES,
		types.FEAT_FREQUENCY_ENCODE,
		types.FEAT_LOG,
		types.FEAT_ONE_HOT,
		types.FEAT_SQRT,
		types.FEAT_TARGET_ENCODE,
		types.FEAT_TRAIN_TEST_SPLIT,
	}
	if !slices.Equal(got, want) {
		t.Fatalf("AllFeatureTypes mismatch\n got: %v\nwant: %v", got, want)
	}

	for i, ft := range got {
		if string(ft) == "" {
			t.Errorf("FeatureType at index %d is empty", i)
		}
	}
}

func TestRequest_FeaturesRoundTrip(t *testing.T) {
	req := types.Request{
		Cohort: &types.Cohort{Filename: "demo.pulse"},
		Features: []*types.Feature{
			{Type: types.FEAT_LOG, Field: "income", Label: "log_income"},
			{Type: types.FEAT_ONE_HOT, Field: "region"},
			{
				Type:   types.FEAT_TRAIN_TEST_SPLIT,
				Params: json.RawMessage(`{"ratios":[0.7,0.15,0.15],"seed":42}`),
			},
		},
	}

	body, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back types.Request
	if err := json.Unmarshal(body, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(back.Features) != 3 {
		t.Fatalf("want 3 features, got %d", len(back.Features))
	}
	if back.Features[0].Type != types.FEAT_LOG || back.Features[0].Field != "income" {
		t.Errorf("feature[0] mismatch: %+v", back.Features[0])
	}
}

func TestRequest_OmitsEmptyFeatures(t *testing.T) {
	req := types.Request{Cohort: &types.Cohort{Filename: "x"}}
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if got := string(body); got == "" || containsKey(got, "features") {
		t.Errorf("empty Features should be omitted from JSON, got: %s", got)
	}
}

func containsKey(s, key string) bool {
	for i := 0; i+len(key)+1 < len(s); i++ {
		if s[i] == '"' && s[i+1:i+1+len(key)] == key && s[i+1+len(key)] == '"' {
			return true
		}
	}
	return false
}
