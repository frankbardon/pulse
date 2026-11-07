package integration_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// A FEAT_TARGET_ENCODE params.fit_on is one of the request expressions
// the shared reference / static checks read (vectors.ExprSlots, U67
// E4-S3): a ref() or reserved-built-in fault in it is reported by
// predict — slot features[i].params.fit_on — under the code, message
// and reason Process refuses it with.

// fitOnRefRequest target-encodes region on qty, training on fitOn.
func fitOnRefRequest(fitOn string) *types.Request {
	params, _ := json.Marshal(map[string]any{"target": "qty", "fit_on": fitOn})
	return &types.Request{
		Cohort:       &types.Cohort{Filename: "ref.pulse"},
		Vectors:      []types.VectorSpec{{Name: "v", Fields: []string{"score", "qty"}, Labels: []string{"s", "q"}}},
		Features:     []*types.Feature{{Type: types.FEAT_TARGET_ENCODE, Field: "region", Label: "te", Params: params}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "qty"}},
	}
}

// TestFeatureFitOn_ExprSlotRefusalParity: each fault is one predict
// error at the feature's fit_on slot, the runtime's code, message and
// reason; a well-formed ref() in fit_on is accepted by both.
func TestFeatureFitOn_ExprSlotRefusalParity(t *testing.T) {
	t.Parallel()

	p, data := openReferenceCohort(t, refRegionTables())
	ctx := context.Background()
	// ref marks a reference issue (vectors.ReferenceIssues), which
	// carries predict's details.slot locator; a static built-in issue
	// (vectors.StaticIssues) carries the runtime's details only.
	cases := map[string]struct {
		fitOn string
		ref   bool
	}{
		"unknown table":       {`dot(v, ref("nope", "north")) > 0`, true},
		"unknown literal key": {`dot(v, ref("w", "nowhere")) > 0`, true},
		"labels differ":       {`cosine(v, ref("other", "k")) > 0`, true},
		"vnormalize mode":     {`vsum(vnormalize(v, "l3")) > 0`, false},
		"length mismatch":     {`dot(v, [1, 2, 3]) > 0`, false},
	}
	for name, tc := range cases {
		fitOn := tc.fitOn
		t.Run(name, func(t *testing.T) {
			env, err := p.PredictBytes(ctx, data, fitOnRefRequest(fitOn))
			if err != nil {
				t.Fatal(err)
			}
			_, rerr := p.Process(ctx, fitOnRefRequest(fitOn))
			if rerr == nil {
				t.Fatal("Process accepted the fit_on fault")
			}
			if len(env.Errors) != 1 {
				t.Fatalf("predict errors %+v, want one (runtime %v)", env.Errors, rerr)
			}
			if tc.ref {
				if slot := env.Errors[0].Details["slot"]; slot != "features[0].params.fit_on" {
					t.Errorf("predict slot %v, want features[0].params.fit_on", slot)
				}
				// details.slot is predict's locator; the rest is the runtime's.
				delete(env.Errors[0].Details, "slot")
			}
			sameEntry(t, env, rerr)
		})
	}

	ok := `dot(v, ref("w", "north")) > 0`
	env, err := p.PredictBytes(ctx, data, fitOnRefRequest(ok))
	if err != nil {
		t.Fatal(err)
	}
	if len(env.Errors) != 0 {
		t.Errorf("predict refused a well-formed fit_on ref(): %+v", env.Errors)
	}
	if _, err := p.Process(ctx, fitOnRefRequest(ok)); err != nil {
		t.Errorf("Process refused a well-formed fit_on ref(): %v", err)
	}
}
