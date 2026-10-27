package descriptor

import (
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// TestSweepSlotReadsFit: every reference a sweep slot can make to a
// bound fit is a read — a literal coef() / sweep_axis() in a filter, a
// formula or a holdout, and a params.fit on an attribute or a matrix —
// while another fit's name, a computed name or no reference is not.
func TestSweepSlotReadsFit(t *testing.T) {
	cases := []struct {
		name string
		slot *types.Request
		want bool
	}{
		{"filter coef", &types.Request{Filterers: []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: `coef("best", "x") > 0`}}}, true},
		{"formula sweep_axis", &types.Request{Attributes: []*types.Attribute{{Type: types.ATTR_FORMULA, Label: "d", Expression: `sweep_axis("best", "decay")`}}}, true},
		{"holdout coef", &types.Request{Regressions: []*types.RegressionSpec{{Type: types.REG_OLS, Holdout: `coef("best", "x") > 1`}}}, true},
		{"predict params.fit", &types.Request{Attributes: []*types.Attribute{{Type: types.ATTR_REG_PREDICT, Params: json.RawMessage(`{"fit":"best"}`)}}}, true},
		{"matrix params.fit", &types.Request{Matrices: []types.MatrixSpec{{Type: types.MAT_COVARIANCE, Params: json.RawMessage(`{"fit": "best"}`)}}}, true},
		{"other fit", &types.Request{Attributes: []*types.Attribute{{Type: types.ATTR_REG_PREDICT, Params: json.RawMessage(`{"fit":"bestest"}`)}}}, false},
		{"computed name", &types.Request{Filterers: []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: `coef(params.f, "x") > 0`}}}, false},
		{"nothing", &types.Request{Filterers: []*types.Filterer{{Type: types.FILTER_RANGE, Field: "best", Values: []string{"1"}}}}, false},
		{"nil slot", nil, false},
	}
	for _, tc := range cases {
		if got := SweepSlotReadsFit(tc.slot, "best"); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestSweepFitScope: no sweep is the zero scope; a sweep carries its
// rank presence and exactly the expanded sweep slots (explicit slots
// before them, banner slots after them excluded).
func TestSweepFitScope(t *testing.T) {
	if s := SweepFitScope(&types.ComposedRequest{}, nil, 0, 0); s.Present || s.Slots != nil {
		t.Fatalf("sweep-free scope %+v", s)
	}
	reqs := []*types.Request{{Label: "explicit"}, {Label: "s0"}, {Label: "s1"}, {Label: "banner"}}
	written := &types.ComposedRequest{Sweep: &types.SweepSpec{}}
	s := SweepFitScope(written, &types.ComposedRequest{Requests: reqs}, 1, 2)
	if !s.Present || s.Ranked || len(s.Slots) != 2 || s.Slots[0].Label != "s0" || s.Slots[1].Label != "s1" || s.Reads == nil {
		t.Fatalf("scope %+v", s)
	}
	written.Sweep.Rank = &types.SweepRank{By: "regressions.m.r_squared"}
	if !SweepFitScope(written, &types.ComposedRequest{Requests: reqs}, 1, 2).Ranked {
		t.Fatal("rank not seen")
	}
}
