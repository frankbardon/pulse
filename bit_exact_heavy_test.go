//go:build heavy

package pulse_test

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/observe"
	"github.com/frankbardon/pulse/types"
)

// TestBitExact_Crosstab: an AGG_SUM / weighted AGG_AVERAGE crosstab
// cell over region × a fixed-width range of y answers identical cells,
// margins and components on the fused crosstab arm (serial, and with
// DecodeWorkers 7), the buffered crosstab arm (fusion disabled, serial
// and with a parallel record decode) and the archive.
func TestBitExact_Crosstab(t *testing.T) {
	t.Parallel()

	dir := bitExactCohorts(t)
	arms := []bitExactArm{
		{name: "flat/fused", cohort: "flat.pulse", opts: pulse.Options{DecodeWorkers: 1}, arm: observe.ArmFusedCrosstab},
		{name: "flat/fused/decode=7", cohort: "flat.pulse", opts: pulse.Options{DecodeWorkers: 7}, arm: observe.ArmFusedCrosstab},
		{name: "flat/buffered", cohort: "flat.pulse", opts: pulse.Options{DecodeWorkers: 1, DisableCrosstabFusion: true}, arm: observe.ArmBuffered},
		{name: "flat/buffered/decode=7", cohort: "flat.pulse", opts: pulse.Options{DecodeWorkers: 7, DisableCrosstabFusion: true}, arm: observe.ArmParallelDecode},
		{name: "archive/shards=1", cohort: "arch.pulse", opts: pulse.Options{ShardWorkers: 1}},
		{name: "archive/shards=8", cohort: "arch.pulse", opts: pulse.Options{ShardWorkers: 8}},
		{name: "archive/buffered", cohort: "arch.pulse", opts: pulse.Options{ShardWorkers: 8, DisableCrosstabFusion: true}},
	}
	for _, cell := range []*types.Aggregation{
		{Type: types.AGG_SUM, Field: "x", Label: "s"},
		{Type: types.AGG_AVERAGE, Field: "x", Label: "wa", Weight: bitExactPW()},
	} {
		t.Run(cell.Label, func(t *testing.T) {
			resps := bitExactRun(t, dir, arms, func(cohort string) *types.Request {
				c := *cell
				return &types.Request{Cohort: &types.Cohort{Filename: cohort},
					Crosstab: &types.CrosstabSpec{
						Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
						Columns: []*types.Group{{Type: types.GROUP_RANGE, Field: "y", Interval: 25}},
						Cell:    &c,
					}}
			})
			if resps[0].Crosstab == nil {
				t.Fatal("no crosstab: the case proves nothing")
			}
			bitExactCompare(t, arms, resps)
		})
	}
}

// TestBitExact_FormulaTotal: total(x) — and its frequency-weighted form,
// and a filtered share formula whose total() reads a row-local label —
// reads the same cohort total on every arm. A one-layer, ungrouped
// total() runs two-phase on the parallel reducers (U51 E3-S1, #399): a
// parallel pre-pass merges binned {sum, count} partials and the reduce
// binds the published totals, so DecodeWorkers 2 / 3 / 7 and
// ShardWorkers 2 / 3 / 8 must really fan out AND answer the serial
// bits — the binned sum does not care about the split.
func TestBitExact_FormulaTotal(t *testing.T) {
	t.Parallel()

	dir := bitExactCohorts(t)
	arms := bitExactArms(true)
	cases := []struct {
		name string
		mk   func(cohort string) *types.Request
		key  string // a Data figure that must be non-integral
	}{
		{name: "plain", key: "max", mk: func(cohort string) *types.Request {
			return &types.Request{Cohort: &types.Cohort{Filename: cohort},
				Attributes:   []*types.Attribute{{Type: types.ATTR_FORMULA, Expression: "total(x)", Label: "tot"}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_MAX, Field: "tot", Label: "max"}}}
		}},
		{name: "weighted", key: "max", mk: func(cohort string) *types.Request {
			attr := &types.Attribute{Type: types.ATTR_FORMULA, Expression: "total(x)", Label: "tot",
				Weight: types.SlotWeightOf(types.WeightSpec{Field: "fw", Kind: types.WeightKindFrequency})}
			return &types.Request{Cohort: &types.Cohort{Filename: cohort},
				Attributes:   []*types.Attribute{attr},
				Aggregations: []*types.Aggregation{{Type: types.AGG_MAX, Field: "tot", Label: "max"}}}
		}},
		{name: "filtered-share-over-label", key: "s", mk: func(cohort string) *types.Request {
			return &types.Request{Cohort: &types.Cohort{Filename: cohort},
				Filterers: []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: "y > 20"}},
				Attributes: []*types.Attribute{
					{Type: types.ATTR_FORMULA, Expression: "x == nil ? nil : x * y", Label: "xy"},
					{Type: types.ATTR_FORMULA, Expression: "xy == nil ? nil : xy / total(xy) + total(y)", Label: "share"},
				},
				Aggregations: []*types.Aggregation{
					{Type: types.AGG_SUM, Field: "share", Label: "s"},
					{Type: types.AGG_AVERAGE, Field: "share", Label: "a"},
					{Type: types.AGG_MAX, Field: "share", Label: "m"},
					{Type: types.AGG_COUNT, Field: "xy", Label: "n"},
				},
				// A matrix beside the total() formula rides phase 2's
				// blocked merge (the serial arm runs it buffered).
				Matrices: []types.MatrixSpec{{Type: types.MAT_COVARIANCE, Name: "c", Fields: []string{"x", "y"}}}}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resps := bitExactRun(t, dir, arms, c.mk)
			if v, _ := resps[0].Data[0][c.key].(float64); v == 0 || v == math.Trunc(v) {
				t.Fatalf("%s = %v is zero or integral: the fixture is not mixed-magnitude", c.key, v)
			}
			bitExactCompare(t, arms, resps)
			// Matrices fold by merge-block position, so they match within
			// a cohort (the archive scatters the rows into another order).
			first := map[string]*types.Response{}
			for i, r := range resps {
				ref, ok := first[arms[i].cohort]
				if !ok {
					first[arms[i].cohort] = r
					continue
				}
				if !reflect.DeepEqual(r.Matrices, ref.Matrices) {
					t.Errorf("%s: Matrices differ\n got %s\nwant %s", arms[i].name, mustBitExactJSON(t, r.Matrices), mustBitExactJSON(t, ref.Matrices))
				}
			}
		})
	}
}

// TestBitExact_FormulaTotalOutsideFirstCut: a GROUPED total() and a
// MULTI-LAYER total() (an argument reading an earlier total) stay off
// the parallel reducers (mergegate.ParallelTotals) — every knob runs
// the sequential arm — and still answer the same bits.
func TestBitExact_FormulaTotalOutsideFirstCut(t *testing.T) {
	t.Parallel()

	dir := bitExactCohorts(t)
	for _, c := range []struct {
		name string
		arm  observe.Arm
		mk   func(cohort string) *types.Request
	}{
		{name: "grouped", arm: observe.ArmBuffered, mk: func(cohort string) *types.Request {
			return &types.Request{Cohort: &types.Cohort{Filename: cohort},
				Attributes:   []*types.Attribute{{Type: types.ATTR_FORMULA, Expression: "(x ?? 0) / total(x)", Label: "sh"}},
				Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "sh", Label: "s"}}}
		}},
		{name: "multi-layer", arm: observe.ArmStreaming, mk: func(cohort string) *types.Request {
			return &types.Request{Cohort: &types.Cohort{Filename: cohort},
				Attributes: []*types.Attribute{
					{Type: types.ATTR_FORMULA, Expression: "total(x)", Label: "t1"},
					{Type: types.ATTR_FORMULA, Expression: "(x ?? 0) / total(x - t1 / 7)", Label: "t2"},
				},
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "t2", Label: "s"}}}
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			var arms []bitExactArm
			for _, a := range bitExactArms(false) {
				a.arm = c.arm
				arms = append(arms, a)
			}
			resps := bitExactRun(t, dir, arms, c.mk)
			bitExactCompare(t, arms, resps)
		})
	}
}

// TestBitExact_CountsAndDistinctSums: the weighted counting aggregators
// (AGG_FREQUENCY's Σw match_count / share, AGG_SET_FREQUENCY's Σw per
// member, AGG_SET_CARDINALITY_SUM / _AVG's Σw·card and Σw) and
// AGG_DISTINCT_SUM (each respondent's one value summed once) answer
// identical bits, ungrouped and grouped, on every worker count and
// shard split — their sums are binned too.
func TestBitExact_CountsAndDistinctSums(t *testing.T) {
	t.Parallel()

	dir := bitExactCohorts(t)
	arms := bitExactArms(true)
	freq, _ := json.Marshal(map[string]string{"value": "east"})
	distinct, _ := json.Marshal(map[string]string{"distinct_by": "rid"})
	for _, grouped := range []bool{false, true} {
		t.Run(fmt.Sprintf("grouped=%v", grouped), func(t *testing.T) {
			resps := bitExactRun(t, dir, arms, func(cohort string) *types.Request {
				req := &types.Request{Cohort: &types.Cohort{Filename: cohort}, Aggregations: []*types.Aggregation{
					{Type: types.AGG_FREQUENCY, Field: "region", Label: "f", Params: freq, Weight: bitExactPW()},
					{Type: types.AGG_SET_FREQUENCY, Field: "tags", Label: "sf", Weight: bitExactPW()},
					{Type: types.AGG_SET_CARDINALITY_SUM, Field: "tags", Label: "cs", Weight: bitExactPW()},
					{Type: types.AGG_SET_CARDINALITY_AVG, Field: "tags", Label: "ca", Weight: bitExactPW()},
					{Type: types.AGG_DISTINCT_SUM, Field: "rv", Label: "ds", Params: distinct},
				}}
				if grouped {
					req.Groups = []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}}
				}
				return req
			})
			for _, k := range []string{"cs", "ds"} {
				if v, _ := resps[0].Data[0][k].(float64); v == 0 || v == math.Trunc(v) {
					t.Fatalf("%s = %v is zero or integral: the fixture is not mixed-magnitude", k, v)
				}
			}
			bitExactCompare(t, arms, resps)
		})
	}
}

// TestBitExact_GroupRounded (U51 E3-S3): GROUP_ROUNDED merges
// (types.GroupType.Mergeable, roundedGrouper.MergeGrouperState), so a
// request grouped by it — alone, over the nullable mixed-magnitude x,
// and beside a categorical grouper — fans out on both reducers and
// answers the serial single file's Data and Components bit for bit,
// grouper components (per-bucket counts, low / high, edges) included.
func TestBitExact_GroupRounded(t *testing.T) {
	t.Parallel()

	dir := bitExactCohorts(t)
	arms := bitExactArms(true)
	cases := map[string][]*types.Group{
		"y":        {{Type: types.GROUP_ROUNDED, Field: "y", Interval: 2.5}},
		"x":        {{Type: types.GROUP_ROUNDED, Field: "x", Interval: 1000}},
		"region*y": {{Type: types.GROUP_CATEGORY, Field: "region"}, {Type: types.GROUP_ROUNDED, Field: "y", Interval: 7.3}},
	}
	for name, groups := range cases {
		t.Run(name, func(t *testing.T) {
			resps := bitExactRun(t, dir, arms, func(cohort string) *types.Request {
				gs := make([]*types.Group, len(groups))
				for i, g := range groups {
					c := *g
					gs[i] = &c
				}
				aggs := bitExactAggs()
				if name == "x" {
					// Width-1000 buckets of values up to 1e6 are
					// ill-conditioned for the Welford moments (their
					// mean dwarfs their spread), which merge within
					// ULP-scaled cancellation, not 1e-12: keep the
					// binned sums only.
					kept := aggs[:0]
					for _, a := range aggs {
						if a.Label != "wa" && a.Label != "wm" {
							kept = append(kept, a)
						}
					}
					aggs = kept
				}
				return &types.Request{Cohort: &types.Cohort{Filename: cohort}, Groups: gs, Aggregations: aggs}
			})
			ref := resps[0]
			if len(ref.Data) < 10 {
				t.Fatalf("%d rows: the fixture is degenerate", len(ref.Data))
			}
			var rounded map[string]any
			for _, g := range ref.Components.Groupers {
				if _, ok := g.Operator["precision"]; ok { // GROUP_ROUNDED's own key
					rounded = g.Operator
				}
			}
			if b, _ := rounded["buckets"].([]map[string]any); len(b) < 10 {
				t.Fatalf("GROUP_ROUNDED components carry %d buckets: %v", len(b), rounded)
			}
			bitExactCompare(t, arms, resps)
		})
	}
}

// TestBitExact_ScalarSums: the reproducible-sum slots, ungrouped and
// grouped, answer identical bits on every worker count and shard split.
func TestBitExact_ScalarSums(t *testing.T) {
	t.Parallel()

	dir := bitExactCohorts(t)
	arms := bitExactArms(true)
	for _, grouped := range []bool{false, true} {
		t.Run(fmt.Sprintf("grouped=%v", grouped), func(t *testing.T) {
			resps := bitExactRun(t, dir, arms, func(cohort string) *types.Request {
				req := &types.Request{Cohort: &types.Cohort{Filename: cohort}, Aggregations: bitExactAggs()}
				if grouped {
					req.Groups = []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}}
				}
				return req
			})
			// The figures are real: a non-dyadic, non-integral sum, and
			// the probability floor's sum_weights / n_eff present.
			if s, _ := resps[0].Data[0]["s"].(float64); s == 0 || s == math.Trunc(s) {
				t.Fatalf("sum %v is zero or integral: the fixture is not mixed-magnitude", s)
			}
			for _, e := range resps[0].Components.Aggregations {
				if e.Label == "ws" && (e.SumWeights == nil || e.NEff == nil) {
					t.Fatalf("weighted slot %s carries no sum_weights / n_eff floor", e.Label)
				}
			}
			bitExactCompare(t, arms, resps)
		})
	}
}
