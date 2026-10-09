package pulse_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/internal/mcp"
	"github.com/frankbardon/pulse/types"
)

func groupByCat(cohort string) *types.Request {
	return &types.Request{
		Cohort:       &types.Cohort{Filename: cohort},
		Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "a", Label: "s"}},
	}
}

// TestLimits_MaxGroups_PredictPossibleAndProcessTrips: grouping on a
// categorical whose dictionary (3) exceeds MaxGroups=2 is a POSSIBLE
// predict finding — Valid stays true on Predict and MCP pulse_predict,
// since a dictionary is an upper bound — and the run, which does mint
// three buckets, trips at runtime with PULSE_LIMIT_EXCEEDED and no
// result. At the limit there is no finding and the run succeeds.
func TestLimits_MaxGroups_PredictPossibleAndProcessTrips(t *testing.T) {
	fs, cohort := limitsCohort(t)
	ctx := context.Background()
	p := limitsPulse(t, fs, pulse.Limits{MaxGroups: 2})
	want := []descriptor.LimitFinding{{Limit: "max_groups", Configured: 2, Estimated: 3, Grade: descriptor.LimitGradePossible}}

	res, err := p.Predict(ctx, groupByCat(cohort))
	if err != nil {
		t.Fatal(err)
	}
	if !res.Valid {
		t.Fatalf("a possible finding left Valid false: %+v", res)
	}
	if !reflect.DeepEqual(res.LimitFindings, want) {
		t.Fatalf("Predict LimitFindings = %+v, want %+v", res.LimitFindings, want)
	}
	out, err := mcp.HandlePredict(ctx, p, mcp.PredictIn{Request: *groupByCat(cohort)})
	if err != nil {
		t.Fatal(err)
	}
	if !out.Valid || !reflect.DeepEqual(out.LimitFindings, want) {
		t.Fatalf("pulse_predict valid=%v findings=%+v, want true %+v", out.Valid, out.LimitFindings, want)
	}

	resp, perr := p.Process(ctx, groupByCat(cohort))
	if resp != nil {
		t.Fatalf("a trip returned a partial result: %+v", resp)
	}
	d := requireLimitExceeded(t, perr, "max_groups", 2, "Options.Limits.MaxGroups")
	if d["observed"] != int64(3) {
		t.Errorf("serial observed = %v, want 3", d["observed"])
	}

	at := limitsPulse(t, fs, pulse.Limits{MaxGroups: 3})
	res, err = at.Predict(ctx, groupByCat(cohort))
	if err != nil || !res.Valid || res.LimitFindings != nil {
		t.Fatalf("at the limit: %+v, %v", res, err)
	}
	if resp, err := at.Process(ctx, groupByCat(cohort)); err != nil || len(resp.Data) != 3 {
		t.Fatalf("at the limit: %v, %v", resp, err)
	}
}
