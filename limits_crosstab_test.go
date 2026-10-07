package pulse_test

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/internal/mcp"
	"github.com/frankbardon/pulse/types"
)

// crosstabLimitsCohort imports a 30-row cohort whose seg (2 keys) x
// tier (3 keys) grid is fully populated: 6 cells.
func crosstabLimitsCohort(t *testing.T) (afero.Fs, string) {
	t.Helper()
	fs := afero.NewMemMapFs()
	var b strings.Builder
	b.WriteString("seg,tier,v\n")
	for i := range 30 {
		fmt.Fprintf(&b, "%s,%s,%d\n", []string{"x", "y"}[i%2], []string{"gold", "silver", "bronze"}[i%3], i)
	}
	if err := afero.WriteFile(fs, "xt.csv", []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	res, err := p.ImportFile(context.Background(), pulse.ImportSpec{SourcePath: "xt.csv"})
	if err != nil {
		t.Fatalf("ImportFile: %v", err)
	}
	return fs, res.Path
}

func segByTier(cohort string) *types.Request {
	return &types.Request{
		Cohort: &types.Cohort{Filename: cohort},
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "seg"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "tier"}},
			Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "v", Label: "c"},
			Shape:   types.CrosstabShapeMatrix,
		},
	}
}

func crosstabLimitsPulse(t *testing.T, fs afero.Fs, cells int64, disableFusion bool) *pulse.Pulse {
	t.Helper()
	p, err := pulse.New(pulse.Options{FS: fs, Limits: pulse.Limits{MaxCrosstabCells: cells}, DisableCrosstabFusion: disableFusion})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// TestLimits_MaxCrosstabCells_BothArms: a 2 x 3 grid under
// MaxCrosstabCells=5 is a POSSIBLE predict finding (the dictionary
// product is an upper bound, so Valid stays true on Predict and MCP
// pulse_predict) and trips at runtime with the same {code, limit,
// configured} on the fused arm and on the buffered arm forced through
// DisableCrosstabFusion — predict's CrosstabFusable names the arm. At 6
// there is no finding and both arms run.
func TestLimits_MaxCrosstabCells_BothArms(t *testing.T) {
	fs, cohort := crosstabLimitsCohort(t)
	ctx := context.Background()
	want := []descriptor.LimitFinding{{Limit: "max_crosstab_cells", Configured: 5, Estimated: 6, Grade: descriptor.LimitGradePossible}}
	trips := map[string]map[string]any{}
	for _, arm := range []struct {
		name          string
		disableFusion bool
	}{{"fused", false}, {"buffered", true}} {
		t.Run(arm.name, func(t *testing.T) {
			p := crosstabLimitsPulse(t, fs, 5, arm.disableFusion)
			res, err := p.Predict(ctx, segByTier(cohort))
			if err != nil {
				t.Fatal(err)
			}
			if res.CrosstabFusable == nil || *res.CrosstabFusable == arm.disableFusion {
				t.Fatalf("CrosstabFusable = %v, want %v", res.CrosstabFusable, !arm.disableFusion)
			}
			if !res.Valid || !reflect.DeepEqual(res.LimitFindings, want) {
				t.Fatalf("Predict valid=%v findings=%+v, want true %+v", res.Valid, res.LimitFindings, want)
			}
			out, err := mcp.HandlePredict(ctx, p, *segByTier(cohort))
			if err != nil {
				t.Fatal(err)
			}
			if !out.Valid || !reflect.DeepEqual(out.LimitFindings, want) {
				t.Fatalf("pulse_predict valid=%v findings=%+v", out.Valid, out.LimitFindings)
			}

			resp, perr := p.Process(ctx, segByTier(cohort))
			if resp != nil {
				t.Fatalf("a trip returned a partial result: %+v", resp)
			}
			trips[arm.name] = requireLimitExceeded(t, perr, "max_crosstab_cells", 5, "Options.Limits.MaxCrosstabCells")

			at := crosstabLimitsPulse(t, fs, 6, arm.disableFusion)
			res, err = at.Predict(ctx, segByTier(cohort))
			if err != nil || !res.Valid || res.LimitFindings != nil {
				t.Fatalf("at the limit: %+v, %v", res, err)
			}
			resp, err = at.Process(ctx, segByTier(cohort))
			if err != nil {
				t.Fatalf("at the limit: %v", err)
			}
			if m := resp.Crosstab.Matrix; len(m.RowKeys) != 2 || len(m.ColumnKeys) != 3 {
				t.Fatalf("at the limit: grid %d x %d, want 2 x 3", len(m.RowKeys), len(m.ColumnKeys))
			}
		})
	}
	// The buffered arm checks the observed grid (6) after partitioning;
	// the fused arm refuses the first interned key that would grow it
	// past the limit, so only {code, limit, configured} are compared.
	if trips["buffered"]["observed"] != int64(6) {
		t.Errorf("buffered observed = %v, want 6", trips["buffered"]["observed"])
	}
	if obs, _ := trips["fused"]["observed"].(int64); obs <= 5 {
		t.Errorf("fused observed = %v, want > 5", trips["fused"]["observed"])
	}
}

// TestLimits_MaxCrosstabCells_JoinedCrosstab: the joined crosstab
// (always buffered) grades the grid over the JOINED schema — seg (2) x
// the right side's r_tier (3) — as a possible finding and trips at
// runtime.
func TestLimits_MaxCrosstabCells_JoinedCrosstab(t *testing.T) {
	fs, left, right := joinLimitCohorts(t)
	ctx := context.Background()
	p := crosstabLimitsPulse(t, fs, 5, false)
	want := []descriptor.LimitFinding{{Limit: "max_crosstab_cells", Configured: 5, Estimated: 6, Grade: descriptor.LimitGradePossible}}

	res, err := p.Predict(ctx, joinedCrosstab(left, right))
	if err != nil {
		t.Fatal(err)
	}
	if !res.Valid || !reflect.DeepEqual(res.LimitFindings, want) {
		t.Fatalf("Predict valid=%v findings=%+v, want true %+v", res.Valid, res.LimitFindings, want)
	}
	if res.CrosstabFusable == nil || *res.CrosstabFusable {
		t.Fatalf("CrosstabFusable = %v, want false (joined crosstabs are buffered)", res.CrosstabFusable)
	}
	resp, perr := p.Process(ctx, joinedCrosstab(left, right))
	if resp != nil {
		t.Fatalf("a trip returned a partial result: %+v", resp)
	}
	d := requireLimitExceeded(t, perr, "max_crosstab_cells", 5, "Options.Limits.MaxCrosstabCells")
	if d["observed"] != int64(6) {
		t.Errorf("observed = %v, want 6", d["observed"])
	}
	if _, err := crosstabLimitsPulse(t, fs, 6, false).Process(ctx, joinedCrosstab(left, right)); err != nil {
		t.Fatalf("at the limit: %v", err)
	}
}
