package service

import (
	"context"
	"math"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// TestCoef_ShardArchiveSlotReadsBoundFit: a Compose slot over a shard
// archive (the per-shard reducer builds its own filters and row-local
// attributes) resolves coef() against the slot's bound fits exactly as
// the same slot over the concatenated single file does.
func TestCoef_ShardArchiveSlotReadsBoundFit(t *testing.T) {
	t.Parallel()

	recs := fitsRecords()
	shards := []struct {
		Name    string
		Records [][]uint64
	}{
		{Name: "a.pulse", Records: recs[0:40]},
		{Name: "b.pulse", Records: recs[40:80]},
		{Name: "c.pulse", Records: recs[80:]},
	}
	svc, _ := setupShardArchive(t, "coef_arch.pulse", fitsSchema(), shards, recs)
	slot := func(cohort string) *types.Request {
		return &types.Request{
			Label:      cohort,
			Cohort:     &types.Cohort{Filename: cohort},
			Filterers:  []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: `x > coef("m", "x", "lower")`}},
			Attributes: []*types.Attribute{{Type: types.ATTR_FORMULA, Label: "c", Expression: `coef("m", "x") * x`}},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_COUNT, Field: "c", Label: "n"},
				{Type: types.AGG_SUM, Field: "c", Label: "s"},
			},
		}
	}
	fit := fitRequest("0", "90")
	fit.Cohort = &types.Cohort{Filename: "coef_arch.pulse.concat"}
	out, err := svc.Compose(context.Background(), &types.ComposedRequest{
		Fits:     []types.FitSpec{{Name: "m", Request: fit}},
		Requests: []*types.Request{slot("coef_arch.pulse"), slot("coef_arch.pulse.concat")},
	})
	if err != nil {
		t.Fatal(err)
	}
	arch, single := out.Responses[0].Data[0], out.Responses[1].Data[0]
	// The shard merge sums in another order: the sum agrees to rounding.
	as, ss := arch["s"].(float64), single["s"].(float64)
	if arch["n"] != single["n"] || math.Abs(as-ss) > 1e-12*math.Abs(ss) {
		t.Fatalf("archive %v != single file %v", arch, single)
	}
	lo := out.Fits["m"].CILower["x"]
	want := 0
	for _, r := range recs {
		if x := math.Float64frombits(r[1]); x > lo {
			want++
		}
	}
	if arch["n"] != float64(want) || want == 0 || want == len(recs) {
		t.Fatalf("archive kept %v rows, want %d of %d", arch["n"], want, len(recs))
	}
}
