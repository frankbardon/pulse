package pulse

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/types"
)

// reg_fit_reference_weight_test.go pins custom-regressions decision #5:
// an ATTR_REG_FITTED / ATTR_REG_RESIDUAL naming params.fit scores a
// bound fit, whose own weight applied when the fit ran, so a weight in
// force on the slot changes nothing and is reported skipped — the
// ATTR_REG_PREDICT rule — by predict and runtime alike (the runtime
// stamps the slot weight null, so its scores are byte-identical to the
// unweighted run). The refit path (no params.fit) stays applied.

func TestRegFitReference_WeightSkipped(t *testing.T) {
	t.Parallel()

	fsys := fitsFacadeFS(t)
	writeCoefModel(t, fsys, facadeModel, 2)
	p, err := New(Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	prob := &types.WeightSpec{Field: "y", Kind: types.WeightKindProbability}
	mk := func(reqW *types.WeightSpec, slotW types.SlotWeight) *Request {
		req := fitRefSlot(fitsFacadeCohort, "week",
			fitRefAttr(types.ATTR_REG_FITTED, "f", `{"fit":"m"}`),
			fitRefAttr(types.ATTR_REG_RESIDUAL, "r", `{"fit":"m"}`),
			fitRefAttr(types.ATTR_REG_PREDICT, "p", `{"fit":"m"}`),
		)
		req.Weight = reqW
		req.Models = []types.ModelRef{ref("m", facadeModel)}
		for _, a := range req.Attributes {
			a.Weight = slotW
		}
		for _, a := range req.Aggregations {
			a.Weight = types.NullSlotWeight()
		}
		return req
	}
	plain, err := p.Process(ctx, mk(nil, types.SlotWeight{}))
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(plain.Data)
	for name, req := range map[string]*Request{
		"request weight": mk(prob, types.SlotWeight{}),
		"slot weight":    mk(nil, types.SlotWeightOf(*prob)),
	} {
		t.Run(name, func(t *testing.T) {
			pr, err := p.Predict(ctx, req)
			if err != nil {
				t.Fatal(err)
			}
			for _, slot := range []string{"attributes[0]", "attributes[1]", "attributes[2]"} {
				if st := fitWeightStatus(pr.Weights, slot); st != descriptor.WeightStatusSkippedNotWeightAware {
					t.Errorf("predict %s status %q, want %s", slot, st, descriptor.WeightStatusSkippedNotWeightAware)
				}
			}
			resp, err := p.Process(ctx, req)
			if err != nil {
				t.Fatalf("runtime refused a skipped weight: %v", err)
			}
			if got, _ := json.Marshal(resp.Data); string(got) != string(want) {
				t.Fatalf("weighted params.fit scores %s, unweighted %s", got, want)
			}
		})
	}

	// The refit path is unchanged: weight-aware, applied.
	refit := fitRefSlot(fitsFacadeCohort, "week",
		&types.Attribute{Type: types.ATTR_REG_FITTED, Label: "f", Target: "y", Predictors: []string{"x"}},
		&types.Attribute{Type: types.ATTR_REG_RESIDUAL, Label: "r", Target: "y", Predictors: []string{"x"}},
	)
	refit.Weight = prob
	for _, a := range refit.Aggregations {
		a.Weight = types.NullSlotWeight()
	}
	pr, err := p.Predict(ctx, refit)
	if err != nil {
		t.Fatal(err)
	}
	for _, slot := range []string{"attributes[0]", "attributes[1]"} {
		if st := fitWeightStatus(pr.Weights, slot); st != descriptor.WeightStatusApplied {
			t.Errorf("refit %s status %q, want %s", slot, st, descriptor.WeightStatusApplied)
		}
	}
	if _, err := p.Process(ctx, refit); err != nil {
		t.Fatalf("weighted refit refused: %v", err)
	}
}

func fitWeightStatus(ws []descriptor.ResolvedWeight, slot string) string {
	for _, w := range ws {
		if w.Slot == slot {
			return w.Status
		}
	}
	return ""
}
