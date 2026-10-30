package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/types"
)

// TestReturnComposeParallelTimeoutKeepsComponentsVeto (U18 follow-up):
// ComposeParallel with a PerRequestTimeout wraps each slot's context in
// context.WithTimeout. The wrap must derive from the slot context that
// carries the Compose-overlay components veto (composeSlotContext), not
// from the batch's run context — otherwise a slot a Compose overlay
// names drops its components under a `return` that excludes them and
// the overlay reads a skipped block. The vetoed slots still build their
// cell component maps, and the Compose layer plus every kept slot path
// are byte-identical to the unshaped, untimed run.
func TestReturnComposeParallelTimeoutKeepsComponentsVeto(t *testing.T) {
	// serial: reads deltas of process-wide internal/processing work counters, which concurrent tests bump
	_, fs, cohort := acceptanceCohort(t)
	prob := types.WeightSpec{Field: "y", Kind: types.WeightKindProbability}
	standard := &types.Return{Preset: types.ReturnPresetStandard}
	ctx := context.Background()
	fixture := func(ret *types.Return) *types.ComposedRequest {
		slot := func(label, expr string) *types.Request {
			r := floorCrosstab(cohort, &prob, types.SlotWeight{}, nil)
			r.Label = label
			r.Return = ret
			if expr != "" {
				r.Filterers = []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: expr}}
			}
			return r
		}
		return &types.ComposedRequest{
			Requests: []*types.Request{slot("total", ""), slot("sub", "x >= 10"), slot("other", "x < 10")},
			Overlays: []types.ComposeOverlaySpec{{Name: "o", Kind: types.OverlayKindPropZCell, Scope: types.OverlayScopeCell, Reference: "total", Targets: []string{"sub"}}},
		}
	}
	for _, fusionOff := range []bool{false, true} {
		t.Run(map[bool]string{false: "fused", true: "buffered"}[fusionOff], func(t *testing.T) {
			p := newReturnInstance(t, fs, pulse.Options{DisableCrosstabFusion: fusionOff})
			full, _ := composeJSON(t, p, fixture(nil), false)
			if len(full.Overlays) != 1 || full.Overlays[0].Summary == nil {
				t.Fatalf("full overlay layer: %+v", full.Overlays)
			}
			for i, r := range full.Responses {
				if r.Components == nil || r.Components.Crosstab == nil {
					t.Fatalf("full slot %d lacks components.crosstab: the fixture proves nothing", i)
				}
			}
			for _, timeout := range []time.Duration{0, time.Minute} {
				before := processing.WorkStats()
				shaped, err := p.ComposeParallel(ctx, fixture(standard), pulse.ComposeOptions{MaxWorkers: 3, PerRequestTimeout: timeout})
				if err != nil {
					t.Fatalf("timeout=%v: ComposeParallel: %v", timeout, err)
				}
				if got := processing.WorkStats().Sub(before).CrosstabCellComponentMaps; got <= 0 {
					t.Errorf("timeout=%v: the named slots' components.crosstab was not computed (cell maps %d)", timeout, got)
				}
				for i := range full.Responses {
					assertKeptPathsIdentical(t, full.Responses[i], shaped.Responses[i], "components")
				}
				want, _ := json.Marshal(full.Overlays)
				got, _ := json.Marshal(shaped.Overlays)
				if !bytes.Equal(got, want) {
					t.Errorf("timeout=%v: compose overlay layer moved\n got %s\nwant %s", timeout, got, want)
				}
			}
		})
	}
}

// TestReturnComposeFamilyTestVeto (U18 follow-up): a `compose`
// multiplicity family pools the tests and post-tests of every slot, so
// a slot whose `return` excludes them still computes its member entries
// — the family's m and every kept slot's p_adjusted are the full run's
// — while the excluded slot's tests are absent on the wire. Without the
// family the same exclusion skips the slot's tests outright (its
// counter contribution is 0). Serial and parallel Compose.
func TestReturnComposeFamilyTestVeto(t *testing.T) {
	// serial: reads deltas of process-wide internal/processing work counters, which concurrent tests bump
	_, fs, cohort := acceptanceCohort(t)
	p := newReturnInstance(t, fs, pulse.Options{})
	excluded := &types.Return{Exclude: []string{"tests", "post_tests"}}
	fixture := func(mult *types.Multiplicity, bRet *types.Return) *types.ComposedRequest {
		slot := func(label, expr string) *types.Request {
			r := &types.Request{
				Cohort:       &types.Cohort{Filename: cohort},
				Label:        label,
				Groups:       []*types.Group{{Type: types.GROUP_RANGE, Field: "x", Interval: 10}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_AVERAGE, Field: "y", Label: "ay"}},
				Tests: []*types.Test{
					{Type: types.TEST_T, Field: "x", Params: json.RawMessage(`{"mu":20}`), Label: "t"},
					{Type: types.TEST_PEARSON_R, Field: "x", Field2: "y", Label: "r"},
				},
				PostTests: []*types.Test{{Type: types.TEST_TREND, Field: "ay", OrderBy: []types.OrderKey{{Field: "x"}}, Label: "trend"}},
			}
			if expr != "" {
				r.Filterers = []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: expr}}
			}
			if label == "b" {
				r.Return = bRet
			}
			return r
		}
		return &types.ComposedRequest{
			// Slot b starts on a group boundary (x >= 10): every one of its
			// groups is large enough for Pearson r, so it raises no
			// sparse-group warning — which a skipped test would not raise.
			Requests:     []*types.Request{slot("a", ""), slot("b", "x >= 10"), slot("c", "x < 40")},
			Multiplicity: mult,
		}
	}
	run := func(parallel bool, req *types.ComposedRequest) (*types.ComposedResponse, processing.WorkStatsSnapshot) {
		before := processing.WorkStats()
		out, _ := composeJSON(t, p, req, parallel)
		return out, processing.WorkStats().Sub(before)
	}
	family := &types.Multiplicity{Method: types.MultiplicityMethodHolm, Family: types.MultiplicityFamilyCompose}
	const slots, testsPer, postsPer = 3, 2, 1
	for _, parallel := range []bool{false, true} {
		t.Run(map[bool]string{false: "serial", true: "parallel"}[parallel], func(t *testing.T) {
			full, d := run(parallel, fixture(family, nil))
			// The slots are grouped, so every test runs once per group
			// (U46 E3-S1): a slot's tier-1 folds are tests x its groups.
			allFolds, wantM := 0, 0
			for _, r := range full.Responses {
				allFolds += len(r.Tests)
				for _, tr := range append(append([]*types.TestResult{}, r.Tests...), r.PostTests...) {
					if !math.IsNaN(tr.PValue) {
						wantM++
					}
				}
			}
			bFolds := len(full.Responses[1].Tests)
			if d.RowTestFolds != int64(allFolds) || d.PostTestRuns != slots*postsPer {
				t.Fatalf("full run: folds=%d posts=%d; want %d / %d", d.RowTestFolds, d.PostTestRuns, allFolds, slots*postsPer)
			}
			for i, r := range full.Responses {
				if len(r.Data) < 2 || len(r.Tests) != testsPer*len(r.Data) || len(r.PostTests) != postsPer {
					t.Fatalf("full slot %d: %d tests over %d groups, %d post-tests", i, len(r.Tests), len(r.Data), len(r.PostTests))
				}
				for _, tr := range append(append([]*types.TestResult{}, r.Tests...), r.PostTests...) {
					if tr.PAdjusted == nil || tr.Multiplicity == nil || tr.Multiplicity.M != wantM {
						t.Fatalf("full slot %d: %q not pooled in the compose family (m=%v)", i, tr.Label, tr.Multiplicity)
					}
				}
			}

			// The compose family vetoes slot b's skip: its members fold,
			// the kept slots are byte-identical, b's tests are absent.
			shaped, d := run(parallel, fixture(family, excluded))
			if d.RowTestFolds != int64(allFolds) || d.PostTestRuns != slots*postsPer {
				t.Errorf("compose family: folds=%d posts=%d; want every member (%d / %d)",
					d.RowTestFolds, d.PostTestRuns, allFolds, slots*postsPer)
			}
			for _, i := range []int{0, 2} {
				assertKeptPathsIdentical(t, full.Responses[i], shaped.Responses[i])
			}
			assertKeptPathsIdentical(t, full.Responses[1], shaped.Responses[1], "tests", "post_tests")

			// Contrast: no family — the same exclusion skips slot b's tests.
			bare, d := run(parallel, fixture(nil, nil))
			if d.RowTestFolds != int64(allFolds) || d.PostTestRuns != slots*postsPer {
				t.Fatalf("bare full run: folds=%d posts=%d", d.RowTestFolds, d.PostTestRuns)
			}
			skipped, d := run(parallel, fixture(nil, excluded))
			if d.RowTestFolds != int64(allFolds-bFolds) || d.PostTestRuns != (slots-1)*postsPer {
				t.Errorf("no family: folds=%d posts=%d; want slot b's contribution 0 (%d / %d)",
					d.RowTestFolds, d.PostTestRuns, allFolds-bFolds, (slots-1)*postsPer)
			}
			for _, i := range []int{0, 2} {
				assertKeptPathsIdentical(t, bare.Responses[i], skipped.Responses[i])
			}
			assertKeptPathsIdentical(t, bare.Responses[1], skipped.Responses[1], "tests", "post_tests")
		})
	}
}

// TestReturnComposeVetoWeightAware (#221): the Compose components veto
// is weight-basis aware per slot. OVERLAY_PROP_Z_CELL reads a slot's
// crosstab floors only when the slot's cell is weighted, so over
// unweighted slots a `return` that drops components skips them (no cell
// map is built) while over weighted slots components.crosstab is still
// computed. Either way the Compose layer and every kept slot path are
// byte-identical to the unshaped run — the skip changes no number.
// Serial and parallel.
func TestReturnComposeVetoWeightAware(t *testing.T) {
	// serial: reads deltas of process-wide internal/processing work counters, which concurrent tests bump
	_, fs, cohort := acceptanceCohort(t)
	prob := types.WeightSpec{Field: "y", Kind: types.WeightKindProbability}
	freq := types.WeightSpec{Field: "y", Kind: types.WeightKindFrequency}
	standard := &types.Return{Preset: types.ReturnPresetStandard}
	fixture := func(w *types.WeightSpec, ret *types.Return) *types.ComposedRequest {
		slot := func(label, expr string) *types.Request {
			r := floorCrosstab(cohort, w, types.SlotWeight{}, nil)
			r.Label = label
			r.Return = ret
			if expr != "" {
				r.Filterers = []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: expr}}
			}
			return r
		}
		return &types.ComposedRequest{
			Requests: []*types.Request{slot("total", ""), slot("sub", "x >= 10")},
			Overlays: []types.ComposeOverlaySpec{{Name: "o", Kind: types.OverlayKindPropZCell, Scope: types.OverlayScopeCell, Reference: "total", Targets: []string{"sub"}}},
		}
	}
	p := newReturnInstance(t, fs, pulse.Options{})
	for _, tc := range []struct {
		name     string
		w        *types.WeightSpec
		computed bool
	}{
		{name: "unweighted", w: nil, computed: false},
		{name: "frequency", w: &freq, computed: true},
		{name: "probability", w: &prob, computed: true},
	} {
		for _, parallel := range []bool{false, true} {
			t.Run(tc.name+map[bool]string{false: "/serial", true: "/parallel"}[parallel], func(t *testing.T) {
				full, _ := composeJSON(t, p, fixture(tc.w, nil), parallel)
				if len(full.Overlays) != 1 || full.Overlays[0].Summary == nil {
					t.Fatalf("full overlay layer: %+v", full.Overlays)
				}
				before := processing.WorkStats()
				shaped, _ := composeJSON(t, p, fixture(tc.w, standard), parallel)
				maps := processing.WorkStats().Sub(before).CrosstabCellComponentMaps
				if tc.computed && maps <= 0 {
					t.Errorf("weighted slots: components.crosstab was not computed (cell maps %d)", maps)
				}
				if !tc.computed && maps != 0 {
					t.Errorf("unweighted slots: the floor kind still vetoed the skip (cell maps %d)", maps)
				}
				for i := range full.Responses {
					assertKeptPathsIdentical(t, full.Responses[i], shaped.Responses[i], "components")
				}
				want, _ := json.Marshal(full.Overlays)
				got, _ := json.Marshal(shaped.Overlays)
				if !bytes.Equal(got, want) {
					t.Errorf("compose overlay layer moved\n got %s\nwant %s", got, want)
				}
			})
		}
	}
}
