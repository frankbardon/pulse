package service

import (
	"encoding/json"
	"fmt"
	"slices"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// Grouper parity harness (weighting-inferential E5-S2): GROUP_QUANTILE
// cuts its buckets at the weighted order statistics (the U11 Hmisc
// wtd.quantile rule; probability weights rescaled to the row count) on
// groups[i] and on both crosstab axes, under every kind the manifest's
// weight_kinds advertises. The read-back slots are opted out of the
// weight (a raw AGG_COUNT and AGG_SUM per bucket), so the grouper is the
// only weighted slot and every figure is raw:
//
//   - Unity (TestWeightUnityParity/groupers): an all-1.0 weight answers
//     BYTE-identically to no weight — buckets, counts and components.
//   - Expansion (TestWeightFrequencyExpansionParity/groupers): kind
//     frequency — every bucket opens at the value the expanded rows'
//     bucket of the same key opens at (the cut is that order statistic;
//     a heavy row spanning a cut lands whole in the higher bucket, so
//     counts and highs are not the expansion's); kind probability —
//     scale invariance, weights × testScaleC answer byte-identically.
//   - Scale (TestWeightProbabilityQuantileScaleInvariance/groupers):
//     fractional probability weights × several constants answer
//     byte-identically.
//
// A crosstab axis row also checks its row-margin counts per bucket equal
// the groups row's bucket counts under the same weight: the axis cuts
// exactly where the grouper does. GROUP_QUANTILE never streams or merges,
// so only the buffered arm applies; the grouped-streaming arm replaces
// the request's groups and is skipped.

type groupParityOp struct {
	name string
	// axis is "" (groups[0]), "rows" or "columns" (the crosstab axis the
	// quantile grouper sits on).
	axis string
}

var groupParityOps = []groupParityOp{
	{name: "groups"},
	{name: "crosstab_rows", axis: "rows"},
	{name: "crosstab_columns", axis: "columns"},
}

const groupParityBuckets = 4

func groupParityQuantile() *types.Group {
	return &types.Group{Type: types.GROUP_QUANTILE, Field: "y", Interval: groupParityBuckets}
}

func (o groupParityOp) kinds() []types.WeightKind {
	return manifestWeightKinds(weightSurfaceGroupers)[string(types.GROUP_QUANTILE)]
}

func (o groupParityOp) build(path string) func() *types.Request {
	return func() *types.Request {
		req := &types.Request{Cohort: &types.Cohort{Filename: path}}
		if o.axis == "" {
			req.Groups = []*types.Group{groupParityQuantile()}
			req.Aggregations = []*types.Aggregation{
				{Type: types.AGG_COUNT, Field: "y", Label: "n", Weight: types.NullSlotWeight()},
				{Type: types.AGG_SUM, Field: "x", Label: "sx", Weight: types.NullSlotWeight()},
			}
			return req
		}
		other := &types.Group{Type: types.GROUP_CATEGORY, Field: "h"}
		ct := &types.CrosstabSpec{
			Rows: []*types.Group{groupParityQuantile()}, Columns: []*types.Group{other},
			Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "y", Weight: types.NullSlotWeight()},
			Margins: types.CrosstabMargins{Rows: true, Columns: true},
		}
		if o.axis == "columns" {
			ct.Rows, ct.Columns = ct.Columns, ct.Rows
		}
		req.Crosstab = ct
		return req
	}
}

// groupParityModes are the arms a row can take: the shared ones minus
// grouped streaming (it replaces req.Groups); a crosstab row runs only
// buffered, unshaped (a crosstab carries no top-level aggregation for
// the buffered arm's steering median, and a quantile axis never fuses).
func groupParityModes(row groupParityOp) []parityMode {
	var out []parityMode
	for _, m := range parityModes() {
		switch {
		case m.name == "grouped_streaming":
		case row.axis != "" && m.name == "buffered":
			m.shape = func(*types.Request) {}
			m.engaged = func(*testing.T, *Service, *types.Request, *Cohort) bool { return true }
			out = append(out, m)
		case row.axis != "":
		default:
			out = append(out, m)
		}
	}
	return out
}

// groupAnswer is the part of a response a grouper row compares: the
// data, the components and the crosstab payload (not the metadata,
// which names the cohort file).
func groupAnswer(t *testing.T, resp *types.Response) string {
	t.Helper()
	return string(mustMarshal(t, map[string]any{"data": resp.Data, "components": resp.Components, "crosstab": resp.Crosstab}))
}

func assertGroupParityCoverage(t *testing.T) {
	t.Helper()
	have := map[string][]types.WeightKind{}
	for _, r := range groupParityOps {
		for _, k := range r.kinds() {
			if !slices.Contains(have[string(types.GROUP_QUANTILE)], k) {
				have[string(types.GROUP_QUANTILE)] = append(have[string(types.GROUP_QUANTILE)], k)
			}
		}
	}
	assertWeightKindCoverage(t, weightSurfaceGroupers, have, "groupParityOps, weight_groupers_parity_test.go")
}

// groupUnityParity is TestWeightUnityParity's grouper half.
func groupUnityParity(t *testing.T, store *parityStore) {
	t.Run("coverage", assertGroupParityCoverage)
	ran := 0
	for _, row := range groupParityOps {
		for _, mode := range groupParityModes(row) {
			t.Run(mode.name+"/"+row.name, func(t *testing.T) {
				path := store.paths[mode.cohort]
				base := runArmRequest(t, store, mode, path, row.build(path), nil)
				if base == nil {
					t.Logf("%s does not run on the %s arm; not applicable", row.name, mode.name)
					return
				}
				ran++
				stripSteer(base)
				want := mustMarshal(t, base)
				for _, src := range sourcesFor(row.kinds()) {
					t.Run(src.name, func(t *testing.T) {
						got := runArmRequest(t, store, mode, path, row.build(path), &src)
						stripSteer(got)
						if g := mustMarshal(t, got); string(g) != string(want) {
							t.Errorf("unity weight changed the answer:\n weighted   %s\n unweighted %s", g, want)
						}
					})
				}
			})
		}
	}
	if ran == 0 {
		t.Fatal("no arm ran a quantile grouper: the gate is vacuous")
	}
}

// quantileBuckets reads the groups[0] quantile grouper's components
// buckets as wire maps, keyed by bucket key.
func quantileBuckets(t *testing.T, resp *types.Response) map[string]map[string]any {
	t.Helper()
	if resp.Components == nil || len(resp.Components.Groupers) == 0 {
		t.Fatal("no grouper components")
	}
	var op struct {
		Buckets []map[string]any `json:"buckets"`
	}
	if err := json.Unmarshal(mustMarshal(t, resp.Components.Groupers[0].Operator), &op); err != nil {
		t.Fatal(err)
	}
	out := map[string]map[string]any{}
	for _, b := range op.Buckets {
		out[b["key"].(string)] = b
	}
	if len(out) == 0 {
		t.Fatal("quantile grouper emitted no buckets")
	}
	return out
}

// axisCounts reads a crosstab's quantile-axis margin counts per key.
func axisCounts(t *testing.T, resp *types.Response, axis string) map[string]float64 {
	t.Helper()
	m := resp.Crosstab
	if m == nil {
		t.Fatal("no crosstab payload")
	}
	keys, margins := m.Matrix.RowKeys, m.Matrix.RowMargins
	if axis == "columns" {
		keys, margins = m.Matrix.ColumnKeys, m.Matrix.ColumnMargins
	}
	var wire struct {
		Keys    [][]any                   `json:"keys"`
		Margins []struct{ Value float64 } `json:"margins"`
	}
	b, err := json.Marshal(map[string]any{"keys": keys, "margins": margins})
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &wire); err != nil {
		t.Fatal(err)
	}
	if len(wire.Keys) != len(wire.Margins) || len(wire.Keys) == 0 {
		t.Fatalf("%s axis: %d keys, %d margins", axis, len(wire.Keys), len(wire.Margins))
	}
	out := map[string]float64{}
	for i, k := range wire.Keys {
		out[fmt.Sprint(k[0])] = wire.Margins[i].Value
	}
	return out
}

// assertAxisMatchesGroups: the crosstab axis buckets the rows exactly
// as the groups[0] grouper does under the same weight.
func assertAxisMatchesGroups(t *testing.T, store *parityStore, mode parityMode, row groupParityOp, src *paritySource) {
	t.Helper()
	path := store.paths[mode.cohort]
	groups := runArmRequest(t, store, mode, path, groupParityOps[0].build(path), src)
	axis := runArmRequest(t, store, mode, path, row.build(path), src)
	want := quantileBuckets(t, groups)
	got := axisCounts(t, axis, row.axis)
	if len(got) != len(want) {
		t.Fatalf("%s axis keys %v, groups buckets %v", row.axis, got, want)
	}
	for k, b := range want {
		if got[k] != b["count"].(float64) {
			t.Errorf("%s axis bucket %s: %v rows, groups bucket %v", row.axis, k, got[k], b["count"])
		}
	}
}

// groupExpansionParity is TestWeightFrequencyExpansionParity's grouper
// half (shared stores; scaled holds every weight × testScaleC).
func groupExpansionParity(t *testing.T, weighted, expanded, scaled *parityStore) {
	t.Run("coverage", assertGroupParityCoverage)
	src := map[types.WeightKind]paritySource{}
	for _, s := range paritySources()[:2] { // request_probability, request_frequency
		src[s.kind] = s
	}
	ran := 0
	for _, row := range groupParityOps {
		for _, mode := range groupParityModes(row) {
			t.Run(mode.name+"/"+row.name, func(t *testing.T) {
				if runArmRequest(t, weighted, mode, weighted.paths[mode.cohort], row.build(weighted.paths[mode.cohort]), nil) == nil {
					t.Skipf("%s does not run on the %s arm", row.name, mode.name)
				}
				ran++
				for _, kind := range row.kinds() {
					t.Run(string(kind), func(t *testing.T) {
						s := src[kind]
						if row.axis != "" {
							assertAxisMatchesGroups(t, weighted, mode, row, &s)
						}
						switch kind {
						case types.WeightKindFrequency:
							if row.axis != "" {
								return // the axis = groups check above; groups expand
							}
							base := runArmRequest(t, expanded, mode, expanded.paths[mode.cohort], row.build(expanded.paths[mode.cohort]), nil)
							got := runArmRequest(t, weighted, mode, weighted.paths[mode.cohort], row.build(weighted.paths[mode.cohort]), &s)
							unweighted := runArmRequest(t, weighted, mode, weighted.paths[mode.cohort], row.build(weighted.paths[mode.cohort]), nil)
							gb, eb := quantileBuckets(t, got), quantileBuckets(t, base)
							for k, b := range gb {
								e, ok := eb[k]
								if !ok {
									t.Errorf("bucket %s: absent from the expansion", k)
									continue
								}
								if b["low"] != e["low"] {
									t.Errorf("bucket %s opens at %v, expansion at %v", k, b["low"], e["low"])
								}
							}
							if string(mustMarshal(t, gb)) == string(mustMarshal(t, quantileBuckets(t, unweighted))) {
								t.Error("the frequency weight did not move a cut")
							}
							// The floor stays raw: total_n is the row count.
							if g, u := got.Components.Groupers[0].TotalN, unweighted.Components.Groupers[0].TotalN; g != u {
								t.Errorf("grouper total_n %d, unweighted %d: must stay raw", g, u)
							}
						case types.WeightKindProbability:
							base := runArmRequest(t, weighted, mode, weighted.paths[mode.cohort], row.build(weighted.paths[mode.cohort]), &s)
							stripSteer(base)
							got := runArmRequest(t, scaled, mode, scaled.paths[mode.cohort], row.build(scaled.paths[mode.cohort]), &s)
							stripSteer(got)
							if g, w := groupAnswer(t, got), groupAnswer(t, base); g != w {
								t.Errorf("weights × %v changed the answer:\n scaled   %s\n unscaled %s", testScaleC, g, w)
							}
						default:
							t.Fatalf("no parity arm for weight kind %q", kind)
						}
					})
				}
			})
		}
	}
	if ran == 0 {
		t.Fatal("no arm ran a quantile grouper: the gate is vacuous")
	}
}

// groupScaleInvariance is TestWeightProbabilityQuantileScaleInvariance's
// grouper half: fractional probability weights × each constant answer
// byte-identically on every row.
func groupScaleInvariance(t *testing.T, base *parityStore, stores map[string]*parityStore) {
	src := paritySources()[0] // request_probability
	ran := 0
	for _, row := range groupParityOps {
		for _, mode := range groupParityModes(row) {
			t.Run(mode.name+"/"+row.name, func(t *testing.T) {
				if runArmRequest(t, base, mode, base.paths[mode.cohort], row.build(base.paths[mode.cohort]), nil) == nil {
					t.Skipf("%s does not run on the %s arm", row.name, mode.name)
				}
				ran++
				want := runArmRequest(t, base, mode, base.paths[mode.cohort], row.build(base.paths[mode.cohort]), &src)
				stripSteer(want)
				unweighted := runArmRequest(t, base, mode, base.paths[mode.cohort], row.build(base.paths[mode.cohort]), nil)
				stripSteer(unweighted)
				if groupAnswer(t, want) == groupAnswer(t, unweighted) {
					t.Fatal("the probability weight did not move a cut: the check is vacuous")
				}
				for name, store := range stores {
					got := runArmRequest(t, store, mode, store.paths[mode.cohort], row.build(store.paths[mode.cohort]), &src)
					stripSteer(got)
					if g, w := groupAnswer(t, got), groupAnswer(t, want); g != w {
						t.Errorf("%s: answer\n %s\n unscaled %s", name, g, w)
					}
				}
			})
		}
	}
	if ran == 0 {
		t.Fatal("no arm ran a quantile grouper: the gate is vacuous")
	}
}
