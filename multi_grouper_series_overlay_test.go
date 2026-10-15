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
// positional baseline, the sibling and an OVERLAY_FORMULA reading
// value, prior, total and baseline (U35 #432). With two or more
// groupers each runs once per partition (internal/seriespartition).
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
		{Name: "fx", Kind: types.OverlayKindFormula, Scope: types.OverlayScopeGroup,
			Params: json.RawMessage(`{"formula": "(value - prior) / total + baseline", "baseline_position": 0}`)},
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

// yoySpec is an annual OVERLAY_YOY (stride 1: the fixture's two days,
// 2024-01-15 and 2024-02-15, give one comparison per segment), with
// params merged in; frequency "" leaves it to the date grouper.
func yoySpec(t *testing.T, frequency string, params map[string]any) types.OverlaySpec {
	t.Helper()
	m := map[string]any{}
	for k, v := range params {
		m[k] = v
	}
	if frequency != "" {
		m["frequency"] = frequency
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return types.OverlaySpec{Name: "yoy", Kind: types.OverlayKindYoY, Scope: types.OverlayScopeGroup,
		Ref: types.OverlayRef{YoY: &types.OverlayYoYRef{}}, Params: b}
}

// TestMultiGrouper_YoYPerPartition: OVERLAY_YOY finds its GROUP_DATE
// grouper wherever it sits and runs once per combination of every OTHER
// key; each segment's entries equal, bit for bit and in order, the same
// overlay run alone on that segment (a FILTER_INCLUDE per non-date key,
// grouped by the date alone). Shapes put the date first, last and in
// the middle; one case leaves frequency to the date grouper's Params,
// which is not groups[0]; an explicit partition_by naming the non-date
// keys in another order equals the implicit default. Every entry keeps
// its full composite key. Runs on every mode that returns layers.
func TestMultiGrouper_YoYPerPartition(t *testing.T) {
	f := newMGFixture(t, pulse.Options{})
	dayAnnual := func() *types.Group {
		g := mgDay()
		g.Params = json.RawMessage(`{"frequency": "annual"}`)
		return g
	}
	cases := []struct {
		name   string
		groups func() []*types.Group
		date   int
		spec   types.OverlaySpec
		single types.OverlaySpec
	}{
		{"region×day", func() []*types.Group { return []*types.Group{mgRegion(), mgDay()} }, 1,
			yoySpec(t, "annual", nil), yoySpec(t, "annual", nil)},
		{"day×region", func() []*types.Group { return []*types.Group{mgDay(), mgRegion()} }, 0,
			yoySpec(t, "annual", nil), yoySpec(t, "annual", nil)},
		{"region×day×sex frequency from the date grouper", func() []*types.Group { return []*types.Group{mgRegion(), dayAnnual(), mgSex()} }, 1,
			yoySpec(t, "", nil), yoySpec(t, "", nil)},
		{"sex×region×day explicit partition_by", func() []*types.Group { return []*types.Group{mgSex(), mgRegion(), mgDay()} }, 2,
			yoySpec(t, "annual", map[string]any{"partition_by": []string{"region", "sex"}}), yoySpec(t, "annual", map[string]any{"partition_by": []string{}})},
	}
	var modes []mgMode
	for _, m := range mgModes {
		switch m.name {
		case "ProcessStream", "ProcessChain":
			continue
		}
		modes = append(modes, m)
	}
	for _, c := range cases {
		for _, mode := range modes {
			t.Run(c.name+"/"+mode.name, func(t *testing.T) {
				run := func(groups []*types.Group, spec types.OverlaySpec, filters ...*types.Filterer) *types.Response {
					t.Helper()
					req := mgRequest(f, groups, psYSum())
					req.Filterers = filters
					req.Overlays = []types.OverlaySpec{spec}
					if env := predictEnvelope(t, f.p, f.fs, f.cohort, req); len(env.Errors) > 0 {
						t.Fatalf("predict: %+v", env.Errors)
					}
					resp, err := mode.run(t, f, req)
					if err != nil {
						t.Fatalf("run: %v", err)
					}
					if resp == nil || len(resp.Overlays) != 1 || resp.Overlays[0].Payload.Series == nil {
						t.Fatalf("want one series layer, got %+v", resp)
					}
					return resp
				}
				groups := c.groups()
				resp := run(groups, c.spec)
				if n := len(resp.Overlays[0].Payload.Series.Entries); n != len(resp.Data) {
					t.Fatalf("%d entries for %d rows", n, len(resp.Data))
				}
				// segment (the non-date key values) → its entries in order.
				composite := map[string][]string{}
				segVals := map[string][]any{}
				compared := 0
				for _, e := range resp.Overlays[0].Payload.Series.Entries {
					if len(e.Key) != len(groups) {
						t.Fatalf("entry key %v is not the full composite key", e.Key)
					}
					var seg []any
					for i, v := range e.Key {
						if i != c.date {
							seg = append(seg, v)
						}
					}
					id := fmt.Sprint(seg)
					segVals[id] = seg
					composite[id] = append(composite[id], fmt.Sprint(e.Key[c.date])+"="+psStat(e.Summary.Statistic))
					if e.Summary.Statistic != nil && !math.IsNaN(*e.Summary.Statistic) {
						compared++
					}
				}
				if len(composite) < 3 || compared < 3 {
					t.Fatalf("%d segments, %d comparisons; the fixture must split into several", len(composite), compared)
				}
				for id, seg := range segVals {
					var filters []*types.Filterer
					j := 0
					for i, g := range groups {
						if i == c.date {
							continue
						}
						filters = append(filters, &types.Filterer{Type: types.FILTER_INCLUDE, Field: mgKeyColumn(g), Values: []string{fmt.Sprint(seg[j])}})
						j++
					}
					alone := run([]*types.Group{c.groups()[c.date]}, c.single, filters...)
					var want []string
					for _, e := range alone.Overlays[0].Payload.Series.Entries {
						want = append(want, fmt.Sprint(e.Key[0])+"="+psStat(e.Summary.Statistic))
					}
					if !reflect.DeepEqual(composite[id], want) {
						t.Errorf("segment %s:\n got  %v\n want %v (the segment run alone)", id, composite[id], want)
					}
				}
			})
		}
	}
}

// TestMultiGrouper_YoYRefused: no GROUP_DATE grouper, or a
// partition_by other than exactly the non-date keys, is refused by
// predict and by the engine with the same code and Details — no layer
// survives. (Two date groupers need two date columns; the fixture has
// one, so that refusal's parity is pinned in internal/seriespartition,
// internal/descriptor and internal/processing, which share the one
// reading.)
func TestMultiGrouper_YoYRefused(t *testing.T) {
	f := newMGFixture(t, pulse.Options{})
	cases := []struct {
		name   string
		groups []*types.Group
		params map[string]any
		code   perr.Code
		detail map[string]any
	}{
		{"no date grouper", []*types.Group{mgRegion(), mgSex()}, nil, perr.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE,
			map[string]any{"groupers": 2, "date_groupers": 0}},
		{"one non-date grouper", []*types.Group{mgRegion()}, nil, perr.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE,
			map[string]any{"host_grouper": "GROUP_CATEGORY"}},
		{"partition_by misses a key", []*types.Group{mgRegion(), mgDay(), mgSex()}, map[string]any{"partition_by": []string{"region"}}, perr.PULSE_OVERLAY_PARTITION_INVALID,
			map[string]any{"reason": "not_non_date_keys", "field": "sex"}},
		{"partition_by names the date", []*types.Group{mgRegion(), mgDay()}, map[string]any{"partition_by": []string{"region", "day"}}, perr.PULSE_OVERLAY_PARTITION_INVALID,
			map[string]any{"reason": "not_non_date_keys", "field": "day"}},
		{"partition_by none", []*types.Group{mgRegion(), mgDay()}, map[string]any{"partition_by": []string{}}, perr.PULSE_OVERLAY_PARTITION_INVALID,
			map[string]any{"reason": "not_non_date_keys", "field": "region"}},
		{"partition_by unknown", []*types.Group{mgRegion(), mgDay()}, map[string]any{"partition_by": []string{"warehouse"}}, perr.PULSE_OVERLAY_PARTITION_INVALID,
			map[string]any{"reason": "unknown_field", "field": "warehouse"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := mgRequest(f, c.groups, psYSum())
			req.Overlays = []types.OverlaySpec{yoySpec(t, "annual", c.params)}
			env := predictEnvelope(t, f.p, f.fs, f.cohort, req)
			if len(env.Errors) != 1 || env.Errors[0].Code != string(c.code) {
				t.Fatalf("predict errors %+v, want one %s", env.Errors, c.code)
			}
			for k, v := range c.detail {
				if got := env.Errors[0].Details[k]; !reflect.DeepEqual(got, v) {
					t.Errorf("predict details[%s] = %v, want %v", k, got, v)
				}
			}
			resp, err := f.p.Process(context.Background(), req)
			if resp != nil && len(resp.Overlays) > 0 {
				t.Fatalf("a layer survived the refusal: %+v", resp.Overlays)
			}
			ce := requireCodedError(t, err, c.code)
			if !reflect.DeepEqual(any(ce.Details), any(env.Errors[0].Details)) {
				t.Errorf("runtime details %#v, predict %#v", ce.Details, env.Errors[0].Details)
			}
		})
	}
}
