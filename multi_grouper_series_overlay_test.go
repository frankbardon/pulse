package pulse_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/observe"
	"github.com/frankbardon/pulse/types"
)

// mgSeriesOverlaySpecs is every SERIES overlay that reads the host as
// an ordered series: the lag-1 prior, the rolling window, the
// positional baseline and the sibling. With two or more groupers each
// runs once per partition (internal/seriespartition).
func mgSeriesOverlaySpecs() []types.OverlaySpec {
	window := json.RawMessage(`{"window": 2}`)
	sib := &types.OverlaySiblingRef{Field: "region", Value: "north"}
	return []types.OverlaySpec{
		{Name: "dp", Kind: types.OverlayKindDeltaVsPrior, Scope: types.OverlayScopeGroup},
		{Name: "ip", Kind: types.OverlayKindIndexVsPrior, Scope: types.OverlayScopeGroup, Ref: types.OverlayRef{Prior: &types.OverlayPriorRef{}}},
		{Name: "irm", Kind: types.OverlayKindIndexVsRollingMean, Scope: types.OverlayScopeGroup, Ref: types.OverlayRef{RollingMean: &types.OverlayRollingMeanRef{}}, Params: window},
		{Name: "zr", Kind: types.OverlayKindZScoreVsRolling, Scope: types.OverlayScopeGroup, Ref: types.OverlayRef{RollingMean: &types.OverlayRollingMeanRef{}}, Params: window},
		{Name: "ib", Kind: types.OverlayKindIndexVsBaseline, Scope: types.OverlayScopeGroup, Ref: types.OverlayRef{BaselineIndex: &types.OverlayBaselineIndexRef{}}},
		{Name: "db", Kind: types.OverlayKindDeltaVsBaseline, Scope: types.OverlayScopeGroup, Ref: types.OverlayRef{BaselineIndex: &types.OverlayBaselineIndexRef{}}},
		{Name: "is", Kind: types.OverlayKindIndexVsSibling, Scope: types.OverlayScopeGroup, Ref: types.OverlayRef{Sibling: sib}},
		{Name: "ds", Kind: types.OverlayKindDeltaVsSibling, Scope: types.OverlayScopeGroup, Ref: types.OverlayRef{Sibling: sib}},
	}
}

// psSpecs is the eleven partitioned kinds over a series key: the eight
// ordered kinds, then the three _TOTAL kinds (psTotalSpecs). The sibling
// pair names sibling (the series key column and a value every partition
// carries); without one the sibling pair is left out.
func psSpecs(sibField, sibValue string) []types.OverlaySpec {
	var out []types.OverlaySpec
	for _, s := range mgSeriesOverlaySpecs() {
		if s.Ref.Sibling != nil {
			if sibField == "" {
				continue
			}
			s.Ref.Sibling = &types.OverlaySiblingRef{Field: sibField, Value: sibValue}
		}
		out = append(out, s)
	}
	return append(out, psTotalSpecs()...)
}

// psTotalSpecs is the three _TOTAL kinds: each row against its
// partition's total (or mean and SD).
func psTotalSpecs() []types.OverlaySpec {
	return []types.OverlaySpec{
		{Name: "it", Kind: types.OverlayKindIndexVsTotal, Scope: types.OverlayScopeGroup},
		{Name: "st", Kind: types.OverlayKindShareOfTotal, Scope: types.OverlayScopeGroup},
		{Name: "zt", Kind: types.OverlayKindZScoreVsTotal, Scope: types.OverlayScopeGroup},
	}
}

// psWithPartition sets params.partition_by on every spec.
func psWithPartition(t *testing.T, specs []types.OverlaySpec, pb []string) []types.OverlaySpec {
	t.Helper()
	out := make([]types.OverlaySpec, len(specs))
	for i, s := range specs {
		m := map[string]any{}
		if len(s.Params) > 0 {
			if err := json.Unmarshal(s.Params, &m); err != nil {
				t.Fatal(err)
			}
		}
		m["partition_by"] = pb
		b, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		s.Params = b
		out[i] = s
	}
	return out
}

// psCase is one partitioned shape: the composite groups, how many lead
// the partition, the composite request's specs and the specs each
// per-partition run takes over the remaining groups.
type psCase struct {
	name      string
	groups    func() []*types.Group
	depth     int
	specs     []types.OverlaySpec
	perSingle []types.OverlaySpec
}

// psScoreBy2 buckets score (= id) in pairs, so every region carries a
// series of four to seven points.
func psScoreBy2() *types.Group {
	return &types.Group{Type: types.GROUP_RANGE, Field: "score", Interval: 2}
}

// psYSum is the scored measure: Σy over the bucket varies point to
// point (y = id mod 3, +100 past id 18), so the rolling SD is non-zero.
func psYSum() *types.Aggregation {
	return &types.Aggregation{Type: types.AGG_SUM, Field: "y", Label: "ys"}
}

// psStreamable reports whether every grouper and overlay kind of req
// streams, so the streaming mode must take the streaming arm.
func psStreamable(req *types.Request) bool {
	for _, g := range req.Groups {
		if !g.Type.Streamable() {
			return false
		}
	}
	for _, o := range req.Overlays {
		if !types.OverlayStreamability[o.Kind] {
			return false
		}
	}
	return true
}

// psStat renders a statistic bit for bit.
func psStat(p *float64) string {
	switch {
	case p == nil:
		return "absent"
	case math.IsNaN(*p):
		return "NaN"
	}
	return fmt.Sprintf("%x", math.Float64bits(*p))
}

// psRender splits each layer of resp by the first depth key elements:
// layer → partition prefix → the partition's entries in order, each as
// "suffix-key=statistic".
func psRender(t *testing.T, resp *types.Response, nSpecs, depth int) []map[string][]string {
	t.Helper()
	if resp == nil || len(resp.Overlays) != nSpecs {
		t.Fatalf("want %d layers, got %+v", nSpecs, resp)
	}
	out := make([]map[string][]string, nSpecs)
	for i, layer := range resp.Overlays {
		if layer.Payload.Series == nil {
			t.Fatalf("layer %d carries no series payload", i)
		}
		out[i] = map[string][]string{}
		for _, e := range layer.Payload.Series.Entries {
			prefix := fmt.Sprint([]any(e.Key[:depth]))
			out[i][prefix] = append(out[i][prefix], fmt.Sprint([]any(e.Key[depth:]))+"="+psStat(e.Summary.Statistic))
		}
	}
	return out
}

// TestMultiGrouper_SeriesOverlaysPartitioned: with two or more
// groupers every prior / rolling / baseline / sibling overlay runs once
// per partition — every key but the last by default, or an explicit
// leading prefix — and equals, entry for entry and bit for bit, the
// same overlay run separately on each partition's rows (a FILTER_INCLUDE
// on the partition keys, grouped by the remaining keys). It holds on
// the buffered, streaming and both parallel arms (shard archive and
// parallel decode), under a sort on the keys, under a sort on the
// aggregate that interleaves partitions, through Compose and a `return`
// allowlist. Shapes: [region, score/2], [region, sex] (the sibling pair
// names sex = F), the story's segment × day [region, day], and three
// groupers [region, sex, score/2] partitioned by both prefixes
// (partition_by [region] against per-region runs with partition_by []).
// The prior pair alone, and the three _TOTAL kinds alone, over
// streamable groupers must ride the streaming arm; each _TOTAL row is
// scored against its partition's total (or mean and SD).
func TestMultiGrouper_SeriesOverlaysPartitioned(t *testing.T) {
	f := newMGFixture(t, pulse.Options{})
	cases := []psCase{
		{name: "region×score", groups: func() []*types.Group { return []*types.Group{mgRegion(), psScoreBy2()} }, depth: 1,
			specs: psSpecs("", ""), perSingle: psSpecs("", "")},
		{name: "region×sex", groups: func() []*types.Group { return []*types.Group{mgRegion(), mgSex()} }, depth: 1,
			specs: psSpecs("sex", "F"), perSingle: psSpecs("sex", "F")},
		{name: "region×day", groups: func() []*types.Group { return []*types.Group{mgRegion(), mgDay()} }, depth: 1,
			specs: psSpecs("", ""), perSingle: psSpecs("", "")},
		{name: "region×score streamable kinds", groups: func() []*types.Group { return []*types.Group{mgRegion(), psScoreBy2()} }, depth: 1,
			specs: psSpecs("", "")[:2], perSingle: psSpecs("", "")[:2]},
		{name: "region×score total kinds", groups: func() []*types.Group { return []*types.Group{mgRegion(), psScoreBy2()} }, depth: 1,
			specs: psTotalSpecs(), perSingle: psTotalSpecs()},
		{name: "region×sex×score default", groups: func() []*types.Group { return []*types.Group{mgRegion(), mgSex(), psScoreBy2()} }, depth: 2,
			specs: psSpecs("", ""), perSingle: psSpecs("", "")},
		{name: "region×sex×score partition_by [region]", groups: func() []*types.Group { return []*types.Group{mgRegion(), mgSex(), psScoreBy2()} }, depth: 1,
			specs: psWithPartition(t, psSpecs("", ""), []string{"region"}), perSingle: psWithPartition(t, psSpecs("", ""), []string{})},
	}
	var modes []mgMode
	for _, m := range mgModes {
		switch m.name {
		case "ProcessStream", "ProcessChain":
			// ProcessStream yields rows, not layers; a chain stage's
			// own overlays are the chain-host contract.
			continue
		}
		modes = append(modes, m)
	}
	for _, c := range cases {
		for _, mode := range modes {
			t.Run(c.name+"/"+mode.name, func(t *testing.T) {
				run := func(groups []*types.Group, specs []types.OverlaySpec, filters ...*types.Filterer) *types.Response {
					t.Helper()
					req := mgRequest(f, groups, psYSum())
					req.Filterers = filters
					req.Overlays = specs
					if env := predictEnvelope(t, f.p, f.fs, f.cohort, req); len(env.Errors) > 0 {
						t.Fatalf("predict: %+v", env.Errors)
					}
					resp, err := mode.run(t, f, req)
					if err != nil {
						t.Fatalf("run: %v", err)
					}
					if mode.name == "Streaming" && psStreamable(req) && f.lastArm() != observe.ArmStreaming {
						t.Fatalf("streamable request ran on arm %q, want streaming", f.lastArm())
					}
					return resp
				}
				groups := c.groups()
				composite := psRender(t, run(groups, c.specs), len(c.specs), c.depth)
				cols := make([]string, c.depth)
				for i := range cols {
					cols[i] = mgKeyColumn(groups[i])
				}
				// One run per partition the composite layer carries.
				seen := 0
				for prefix := range composite[0] {
					vals := strings.Fields(strings.Trim(prefix, "[]"))
					if len(vals) != c.depth {
						t.Fatalf("prefix %q does not split into %d keys", prefix, c.depth)
					}
					filters := make([]*types.Filterer, c.depth)
					for i := range filters {
						filters[i] = &types.Filterer{Type: types.FILTER_INCLUDE, Field: cols[i], Values: []string{vals[i]}}
					}
					single := psRender(t, run(c.groups()[c.depth:], c.perSingle, filters...), len(c.perSingle), 0)
					for i := range c.specs {
						if got, want := composite[i][prefix], single[i]["[]"]; !reflect.DeepEqual(got, want) {
							t.Errorf("%s partition %s:\n got  %v\n want %v (the partition run alone)", c.specs[i].Kind, prefix, got, want)
						}
					}
					seen++
				}
				if seen < 3 {
					t.Fatalf("only %d partitions; the fixture must split into several", seen)
				}
			})
		}
	}
}

// TestMultiGrouper_TotalKindsPartitionNoneIsGrandTotal: with
// partition_by [] a _TOTAL overlay over [region, score/2] scores every
// composite row against the GRAND total — share = ys / Σys over every
// row, the pre-U46 figure — while the default shares sum to 1 within
// each region.
func TestMultiGrouper_TotalKindsPartitionNoneIsGrandTotal(t *testing.T) {
	f := newMGFixture(t, pulse.Options{})
	share := psTotalSpecs()[1:2]
	run := func(specs []types.OverlaySpec) *types.Response {
		req := mgRequest(f, []*types.Group{mgRegion(), psScoreBy2()}, psYSum())
		req.Overlays = specs
		resp, err := f.p.Process(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	resp := run(psWithPartition(t, share, []string{}))
	grand := 0.0
	for _, row := range resp.Data {
		grand += row["ys"].(float64)
	}
	entries := resp.Overlays[0].Payload.Series.Entries
	if len(entries) != len(resp.Data) || len(entries) < 6 {
		t.Fatalf("%d entries for %d rows", len(entries), len(resp.Data))
	}
	for i, e := range entries {
		if want := resp.Data[i]["ys"].(float64) / grand; *e.Summary.Statistic != want {
			t.Errorf("row %d: share %v, want %v (grand total %v)", i, *e.Summary.Statistic, want, grand)
		}
	}
	sums := map[any]float64{}
	for _, e := range run(share).Overlays[0].Payload.Series.Entries {
		sums[e.Key[0]] += *e.Summary.Statistic
	}
	if len(sums) < 3 {
		t.Fatalf("shares over %d regions; the fixture must split into several", len(sums))
	}
	for region, sum := range sums {
		if math.Abs(sum-1) > 1e-12 {
			t.Errorf("region %v: default shares sum to %v, want 1", region, sum)
		}
	}
}

// TestMultiGrouper_SeriesOverlayPartitionRefused: a non-prefix or
// unknown partition_by is refused PULSE_OVERLAY_PARTITION_INVALID by
// predict and by the engine with the same Details.
func TestMultiGrouper_SeriesOverlayPartitionRefused(t *testing.T) {
	f := newMGFixture(t, pulse.Options{})
	for name, pb := range map[string][]string{"not a prefix": {"sex"}, "unknown": {"warehouse"}} {
		t.Run(name, func(t *testing.T) {
			req := mgRequest(f, []*types.Group{mgRegion(), mgSex()}, psYSum())
			req.Overlays = psWithPartition(t, psSpecs("sex", "F")[3:4], pb)
			env := predictEnvelope(t, f.p, f.fs, f.cohort, req)
			if len(env.Errors) != 1 || env.Errors[0].Code != string(perr.PULSE_OVERLAY_PARTITION_INVALID) {
				t.Fatalf("predict errors %+v, want PULSE_OVERLAY_PARTITION_INVALID", env.Errors)
			}
			resp, err := f.p.Process(context.Background(), req)
			if resp != nil && len(resp.Overlays) > 0 {
				t.Fatalf("a layer survived the refusal: %+v", resp.Overlays)
			}
			ce := requireCodedError(t, err, perr.PULSE_OVERLAY_PARTITION_INVALID)
			if !reflect.DeepEqual(any(ce.Details), any(env.Errors[0].Details)) {
				t.Errorf("runtime details %#v, predict %#v", ce.Details, env.Errors[0].Details)
			}
		})
	}
}
