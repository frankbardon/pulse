package service

import (
	"context"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/types"
)

// TestReturnSkipsComputation_Crosstab extends TestReturnSkipsComputation
// to the crosstab arms (U18 ranks 2-3): the per-cell / per-margin
// component maps (buildCellComponentMap) and the auxiliary
// margin_aggregations accumulation are never built when the plan drops
// components.crosstab (or only its *_margin_aggregations figures), on
// the buffered, fused and join arms alike — and, closing the pre-U18
// leak, never under the engine DisableComponents gate either. The
// `absent` run proves each zero non-vacuous.

// skipCrosstabRequest: region x segment (or x r_tier on the join arm),
// every margin displayed, one auxiliary margin aggregation.
func skipCrosstabRequest(join bool) *types.Request {
	req := &types.Request{
		Cohort: &types.Cohort{Filename: "left.pulse"},
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "segment"}},
			Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "value", Label: "total"},
			Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
			MarginAggregations: []*types.Aggregation{
				{Type: types.AGG_COUNT, Field: "value", Label: "base"},
			},
			Shape: types.CrosstabShapeMatrix,
		},
	}
	if join {
		req.Joins = crosstabJoinSpec()
		req.Crosstab.Columns = []*types.Group{{Type: types.GROUP_CATEGORY, Field: "r_tier"}}
	}
	return req
}

type crosstabSkipArm struct {
	name          string
	join          bool
	disableFusion bool
	wantFused     bool
}

var crosstabSkipArms = []crosstabSkipArm{
	{name: "buffered", disableFusion: true},
	{name: "fused", wantFused: true},
	{name: "join", join: true},
}

func TestReturnSkipsComputation_Crosstab(t *testing.T) {
	ex := func(paths ...string) *types.Return { return &types.Return{Exclude: paths} }
	both := map[string]bool{"maps": true, "aux": true}
	auxOnly := map[string]bool{"aux": true}
	selections := []skipSelection{
		{name: "absent"},
		{name: "keeps_components", ret: ex("metadata")},
		{name: "exclude_components", ret: ex("components"), zero: both},
		{name: "exclude_crosstab", ret: ex("components.crosstab"), zero: both},
		{name: "standard", ret: &types.Return{Preset: types.ReturnPresetStandard}, zero: both},
		{name: "minimal", ret: &types.Return{Preset: types.ReturnPresetMinimal}, zero: both},
		{name: "exclude_aux_margins", ret: ex(
			"components.crosstab.row_margin_aggregations",
			"components.crosstab.column_margin_aggregations",
			"components.crosstab.grand_total_aggregations",
		), zero: auxOnly},
	}
	for _, arm := range crosstabSkipArms {
		t.Run(arm.name, func(t *testing.T) {
			svc := New(crosstabJoinFixture(t))
			svc.SetDisableCrosstabFusion(arm.disableFusion)
			assertCrosstabArm(t, svc, arm)

			var base *types.CrosstabComponents
			for _, sel := range selections {
				t.Run(sel.name, func(t *testing.T) {
					req := skipCrosstabRequest(arm.join)
					req.Return = sel.ret
					resp, delta := runCrosstabSkip(t, svc, req)
					assertCrosstabCounters(t, delta, sel.zero)
					if sel.ret == nil {
						if resp.Components == nil || resp.Components.Crosstab == nil {
							t.Fatal("return-absent run carried no Components.Crosstab")
						}
						base = resp.Components.Crosstab
						if len(base.RowMarginAggregations) == 0 || base.GrandTotalAggregations == nil {
							t.Fatal("return-absent run carried no auxiliary margin figures: the fixture proves nothing")
						}
						return
					}
					assertKeptCrosstabEqual(t, base, resp, sel.zero)
				})
			}

			// Engine DisableComponents (no `return`): the gate closes
			// the map builds and the accumulation too, not only their
			// emission — the leak this story closes.
			t.Run("engine_disable_components", func(t *testing.T) {
				gated := New(crosstabJoinFixture(t))
				gated.SetDisableCrosstabFusion(arm.disableFusion)
				gated.SetDisableComponents(true)
				resp, delta := runCrosstabSkip(t, gated, skipCrosstabRequest(arm.join))
				assertCrosstabCounters(t, delta, both)
				if resp.Components != nil {
					t.Errorf("DisableComponents: Components present: %+v", resp.Components)
				}
			})
		})
	}
}

// runCrosstabSkip runs req and returns the response and the work-counter
// delta it caused.
func runCrosstabSkip(t *testing.T, svc *Service, req *types.Request) (*types.Response, processing.WorkStatsSnapshot) {
	t.Helper()
	before := processing.WorkStats()
	resp, err := svc.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	delta := processing.WorkStats().Sub(before)
	if resp.Crosstab == nil || resp.Crosstab.Matrix == nil || len(resp.Crosstab.Matrix.RowKeys) == 0 {
		t.Fatal("no crosstab matrix: the fixture proves nothing")
	}
	return resp, delta
}

func assertCrosstabCounters(t *testing.T, delta processing.WorkStatsSnapshot, zero map[string]bool) {
	t.Helper()
	for _, c := range []struct {
		name string
		got  int64
	}{
		{"maps", delta.CrosstabCellComponentMaps},
		{"aux", delta.AuxMarginAccumulators},
	} {
		if zero[c.name] && c.got != 0 {
			t.Errorf("%s: excluded, yet built %d time(s)", c.name, c.got)
		}
		if !zero[c.name] && c.got <= 0 {
			t.Errorf("%s: kept, yet never built (delta %d) — the zero assertions would be vacuous", c.name, c.got)
		}
	}
}

// assertCrosstabArm proves the arm under test is the one named: the
// fused arm must pass the fusion gate, the others must not reach it.
func assertCrosstabArm(t *testing.T, svc *Service, arm crosstabSkipArm) {
	t.Helper()
	if arm.join {
		return // processCrosstab routes every joined request buffered
	}
	cohort, err := svc.Open(context.Background(), "left.pulse")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ok, why := processing.CanFuseCrosstab(skipCrosstabRequest(false), cohort.Schema(), svc.extensions)
	if arm.wantFused && !ok {
		t.Fatalf("fusion gate declined (%s): this arm would silently run buffered", why)
	}
}

// assertKeptCrosstabEqual: a skipped crosstab block is absent; skipping
// only the auxiliary figures leaves every other crosstab component
// equal to the return-absent run's.
func assertKeptCrosstabEqual(t *testing.T, base *types.CrosstabComponents, resp *types.Response, zero map[string]bool) {
	t.Helper()
	var got *types.CrosstabComponents
	if resp.Components != nil {
		got = resp.Components.Crosstab
	}
	if zero["maps"] {
		if got != nil {
			t.Errorf("components.crosstab skipped, yet present: %+v", got)
		}
		return
	}
	if got == nil {
		t.Fatal("components.crosstab kept, yet absent")
	}
	want := *base
	if zero["aux"] {
		if got.RowMarginAggregations != nil || got.ColumnMarginAggregations != nil || got.GrandTotalAggregations != nil {
			t.Errorf("auxiliary figures skipped, yet present")
		}
		want.RowMarginAggregations, want.ColumnMarginAggregations, want.GrandTotalAggregations = nil, nil, nil
	}
	if !reflect.DeepEqual(&want, got) {
		t.Errorf("kept crosstab components moved\n  got:  %+v\n  want: %+v", got, &want)
	}
}

// An auxiliary naming no registered operator is refused whatever the
// plan computes: the registry resolution sits AHEAD of the
// AuxMargins gate (TestCrosstab_BufferedAuxMarginUnresolvableTypeRefused
// pins the ungated half).
func TestReturnSkipsComputation_CrosstabUnknownAuxStillRefused(t *testing.T) {
	for _, tc := range []struct {
		name   string
		ret    *types.Return
		gated  bool
		joined bool
	}{
		{name: "exclude_components", ret: &types.Return{Exclude: []string{"components"}}},
		{name: "exclude_aux_margins", ret: &types.Return{Exclude: []string{
			"components.crosstab.row_margin_aggregations",
			"components.crosstab.column_margin_aggregations",
			"components.crosstab.grand_total_aggregations",
		}}},
		{name: "minimal", ret: &types.Return{Preset: types.ReturnPresetMinimal}},
		{name: "engine_disable_components", gated: true},
		{name: "join_exclude_components", ret: &types.Return{Exclude: []string{"components"}}, joined: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := New(crosstabJoinFixture(t))
			svc.SetDisableComponents(tc.gated)
			req := skipCrosstabRequest(tc.joined)
			req.Return = tc.ret
			req.Crosstab.MarginAggregations = []*types.Aggregation{
				{Type: types.AggregationType("AGG_NOT_A_REAL_OPERATOR"), Field: "value", Label: "bogus"},
			}
			_, err := svc.Process(context.Background(), req)
			if err == nil {
				t.Fatal("unknown margin aggregation accepted once its figures were excluded")
			}
			if !errors.HasCode(err, errors.PROCESSING_CONFIG) {
				t.Errorf("want %s, got %v", errors.PROCESSING_CONFIG, err)
			}
		})
	}
}
