package processing

import (
	"encoding/json"
	stderrors "errors"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// partitionedSeriesSpecs is every SERIES overlay that runs once per
// partition on a composite-row host (internal/seriespartition.Kinds):
// the eight that read the host as an ordered series (lag-1 prior,
// rolling window, positional baseline, sibling), then the three _TOTAL
// kinds. The sibling names the series key "step" so each partition
// resolves its own sibling row.
func partitionedSeriesSpecs() []types.OverlaySpec {
	window := json.RawMessage(`{"window": 2}`)
	sib := &types.OverlaySiblingRef{Field: "step", Value: "s2"}
	return []types.OverlaySpec{
		{Name: "dp", Kind: types.OverlayKindDeltaVsPrior, Scope: types.OverlayScopeGroup},
		{Name: "ip", Kind: types.OverlayKindIndexVsPrior, Scope: types.OverlayScopeGroup, Ref: types.OverlayRef{Prior: &types.OverlayPriorRef{}}},
		{Name: "irm", Kind: types.OverlayKindIndexVsRollingMean, Scope: types.OverlayScopeGroup, Ref: types.OverlayRef{RollingMean: &types.OverlayRollingMeanRef{}}, Params: window},
		{Name: "zr", Kind: types.OverlayKindZScoreVsRolling, Scope: types.OverlayScopeGroup, Ref: types.OverlayRef{RollingMean: &types.OverlayRollingMeanRef{}}, Params: window},
		{Name: "ib", Kind: types.OverlayKindIndexVsBaseline, Scope: types.OverlayScopeGroup, Ref: types.OverlayRef{BaselineIndex: &types.OverlayBaselineIndexRef{}}},
		{Name: "db", Kind: types.OverlayKindDeltaVsBaseline, Scope: types.OverlayScopeGroup, Ref: types.OverlayRef{BaselineIndex: &types.OverlayBaselineIndexRef{}}},
		{Name: "is", Kind: types.OverlayKindIndexVsSibling, Scope: types.OverlayScopeGroup, Ref: types.OverlayRef{Sibling: sib}},
		{Name: "ds", Kind: types.OverlayKindDeltaVsSibling, Scope: types.OverlayScopeGroup, Ref: types.OverlayRef{Sibling: sib}},
		{Name: "it", Kind: types.OverlayKindIndexVsTotal, Scope: types.OverlayScopeGroup},
		{Name: "st", Kind: types.OverlayKindShareOfTotal, Scope: types.OverlayScopeGroup},
		{Name: "zt", Kind: types.OverlayKindZScoreVsTotal, Scope: types.OverlayScopeGroup},
	}
}

// totalSeriesSpecs is the three _TOTAL kinds of partitionedSeriesSpecs:
// each row's total (or mean and SD) is its partition's.
func totalSeriesSpecs() []types.OverlaySpec { return partitionedSeriesSpecs()[8:] }

// withParam merges key=value into spec.Params.
func withParam(t *testing.T, spec types.OverlaySpec, key string, value any) types.OverlaySpec {
	t.Helper()
	m := map[string]any{}
	if len(spec.Params) > 0 {
		if err := json.Unmarshal(spec.Params, &m); err != nil {
			t.Fatal(err)
		}
	}
	m[key] = value
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	spec.Params = b
	return spec
}

// ptRow is one composite row: the key columns and the measure v.
type ptRow struct {
	keys map[string]string
	v    float64
}

// ptRun runs spec through the grouped-Process hook over rows (in the
// given order) grouped by cols, and renders the one layer entry by
// entry as "key=statistic", with the layer summary and warnings.
func ptRun(t *testing.T, spec types.OverlaySpec, cols []string, rows []ptRow) ([]string, *types.OverlaySummary, []types.OverlayWarning) {
	t.Helper()
	req := &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "x", Label: "v"}}, Overlays: []types.OverlaySpec{spec}}
	for _, c := range cols {
		req.Groups = append(req.Groups, &types.Group{Type: types.GROUP_CATEGORY, Field: c})
	}
	resp := &types.Response{}
	for _, r := range rows {
		row := map[string]any{"v": r.v}
		for _, c := range cols {
			row[c] = r.keys[c]
		}
		resp.Data = append(resp.Data, row)
	}
	if err := applyOverlaysSeriesToResponse(req, resp, nil, FullComputePlan()); err != nil {
		t.Fatalf("hook: %v", err)
	}
	if len(resp.Overlays) != 1 || resp.Overlays[0].Payload.Series == nil {
		t.Fatalf("want one series layer, got %+v", resp.Overlays)
	}
	entries := resp.Overlays[0].Payload.Series.Entries
	if len(entries) != len(rows) {
		t.Fatalf("%d entries for %d rows", len(entries), len(rows))
	}
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = ptKey(e.Key) + "=" + ptStat(e.Summary.Statistic)
	}
	var ws []types.OverlayWarning
	for _, w := range resp.Warnings {
		ws = append(ws, types.OverlayWarning{Code: w.Code, Details: w.Details})
	}
	return out, resp.Overlays[0].Summary, ws
}

func ptKey(k types.AxisKey) string {
	parts := make([]string, len(k))
	for i, v := range k {
		parts[i] = fmt.Sprint(v)
	}
	return strings.Join(parts, "|")
}

// ptStat renders a statistic bit for bit (absent, NaN, or the IEEE bits).
func ptStat(p *float64) string {
	switch {
	case p == nil:
		return "absent"
	case math.IsNaN(*p):
		return "NaN"
	}
	return fmt.Sprintf("%x", math.Float64bits(*p))
}

func ptSummary(s *types.OverlaySummary) string {
	if s == nil {
		return "nil"
	}
	c := "nil"
	if s.Count != nil {
		c = fmt.Sprint(*s.Count)
	}
	return "min=" + ptStat(s.Min) + " max=" + ptStat(s.Max) + " count=" + c + " baseline=" + ptStat(s.Baseline)
}

// ptRows is a three-region series over steps s1..s6 with an absent
// step in "south" and a zero in "west" (so the index kinds raise
// PULSE_OVERLAY_REF_ZERO in one partition only).
func ptRows() []ptRow {
	vals := map[string][]float64{
		"east":  {4, 7, 5, 9, 12, 8},
		"south": {3, 6, 2, -1, 10},
		"west":  {0, 5, 5, 6, 2, 9},
	}
	var rows []ptRow
	for _, region := range []string{"east", "south", "west"} {
		for i, v := range vals[region] {
			rows = append(rows, ptRow{keys: map[string]string{"region": region, "step": fmt.Sprintf("s%d", i+1)}, v: v})
		}
	}
	return rows
}

// interleave orders rows round-robin across regions, as a user sort on
// the step column would: no two rows of one partition are adjacent.
func interleave(rows []ptRow) []ptRow {
	byRegion := map[string][]ptRow{}
	var order []string
	for _, r := range rows {
		if _, ok := byRegion[r.keys["region"]]; !ok {
			order = append(order, r.keys["region"])
		}
		byRegion[r.keys["region"]] = append(byRegion[r.keys["region"]], r)
	}
	var out []ptRow
	for i := 0; ; i++ {
		added := false
		for _, reg := range order {
			if i < len(byRegion[reg]) {
				out = append(out, byRegion[reg][i])
				added = true
			}
		}
		if !added {
			return out
		}
	}
}

// TestOverlaySeries_PartitionedEqualsPerSegment: over [region, step]
// every row-order series kind equals, entry for entry and bit for bit,
// the same overlay over [step] run separately on each region's rows —
// by default (every key but the last) and with partition_by [region],
// in key order and under an interleaving sort. The layer summary is the
// merge of the per-region summaries (min / max widen, count sums).
func TestOverlaySeries_PartitionedEqualsPerSegment(t *testing.T) {
	for _, spec := range partitionedSeriesSpecs() {
		t.Run(string(spec.Kind), func(t *testing.T) {
			want := map[string]string{}
			// The merged summary, by hand: min / max over the regions'
			// present extremes, counts summed, the baseline constant.
			merged := &types.OverlaySummary{Count: new(int)}
			for _, region := range []string{"east", "south", "west"} {
				var rows []ptRow
				for _, r := range ptRows() {
					if r.keys["region"] == region {
						rows = append(rows, r)
					}
				}
				got, summary, _ := ptRun(t, spec, []string{"step"}, rows)
				if summary.Min != nil && (merged.Min == nil || *summary.Min < *merged.Min) {
					merged.Min = summary.Min
				}
				if summary.Max != nil && (merged.Max == nil || *summary.Max > *merged.Max) {
					merged.Max = summary.Max
				}
				*merged.Count += *summary.Count
				merged.Baseline = summary.Baseline
				for _, g := range got {
					k, v, _ := strings.Cut(g, "=")
					want[region+"|"+k] = v
				}
			}
			for name, s := range map[string]types.OverlaySpec{
				"implicit":              spec,
				"partition_by=[region]": withParam(t, spec, "partition_by", []string{"region"}),
			} {
				for order, rows := range map[string][]ptRow{"key order": ptRows(), "interleaved": interleave(ptRows())} {
					got, summary, _ := ptRun(t, s, []string{"region", "step"}, rows)
					for _, g := range got {
						k, v, _ := strings.Cut(g, "=")
						if want[k] != v {
							t.Errorf("%s/%s: %s = %s, want %s (per-region run)", name, order, k, v, want[k])
						}
					}
					if ptSummary(summary) != ptSummary(merged) {
						t.Errorf("%s/%s: summary %s, want %s", name, order, ptSummary(summary), ptSummary(merged))
					}
				}
			}
		})
	}
}

// TestOverlaySeries_PartitionNoneIsTheWholeWalk: partition_by [] runs
// the handler once over every composite row, exactly as the handler
// does on the host directly; the default differs, so the param is live.
func TestOverlaySeries_PartitionNoneIsTheWholeWalk(t *testing.T) {
	spec := partitionedSeriesSpecs()[3] // ZSCORE_VS_ROLLING
	rows := interleave(ptRows())
	none, _, _ := ptRun(t, withParam(t, spec, "partition_by", []string{}), []string{"region", "step"}, rows)
	keys := make([]types.AxisKey, len(rows))
	for i, r := range rows {
		keys[i] = types.AxisKey{r.keys["region"], r.keys["step"]}
	}
	host := NewSeriesHostViewWithGrouper(keys, func(i int) (float64, bool) { return rows[i].v, true },
		[]string{"region", "step"}, []types.GroupType{types.GROUP_CATEGORY, types.GROUP_CATEGORY})
	layers, _, err := ApplyOverlaysSeries([]types.OverlaySpec{spec}, host)
	if err != nil {
		t.Fatal(err)
	}
	for i, e := range layers[0].Payload.Series.Entries {
		if got := ptKey(e.Key) + "=" + ptStat(e.Summary.Statistic); got != none[i] {
			t.Errorf("entry %d: partition_by [] %s, direct walk %s", i, none[i], got)
		}
	}
	if def, _, _ := ptRun(t, spec, []string{"region", "step"}, rows); strings.Join(def, ";") == strings.Join(none, ";") {
		t.Error("the default partition equals the whole walk; the fixture does not tell them apart")
	}
}

// TestOverlaySeries_TotalKindsPartitionNoneIsTheGrandTotal: on the
// _TOTAL kinds partition_by [] is the pre-U46 figure — every composite
// row against the grand total (or grand mean and SD), exactly the
// handler over the whole host — while the default takes each region's
// own total, so the share sums to 1 within every region.
func TestOverlaySeries_TotalKindsPartitionNoneIsTheGrandTotal(t *testing.T) {
	rows := interleave(ptRows())
	keys := make([]types.AxisKey, len(rows))
	for i, r := range rows {
		keys[i] = types.AxisKey{r.keys["region"], r.keys["step"]}
	}
	for _, spec := range totalSeriesSpecs() {
		t.Run(string(spec.Kind), func(t *testing.T) {
			none, noneSummary, _ := ptRun(t, withParam(t, spec, "partition_by", []string{}), []string{"region", "step"}, rows)
			host := NewSeriesHostViewWithGrouper(keys, func(i int) (float64, bool) { return rows[i].v, true },
				[]string{"region", "step"}, []types.GroupType{types.GROUP_CATEGORY, types.GROUP_CATEGORY})
			layers, _, err := ApplyOverlaysSeries([]types.OverlaySpec{spec}, host)
			if err != nil {
				t.Fatal(err)
			}
			for i, e := range layers[0].Payload.Series.Entries {
				if got := ptKey(e.Key) + "=" + ptStat(e.Summary.Statistic); got != none[i] {
					t.Errorf("entry %d: partition_by [] %s, grand-total walk %s", i, none[i], got)
				}
			}
			if ptSummary(noneSummary) != ptSummary(layers[0].Summary) {
				t.Errorf("summary %s, grand-total walk %s", ptSummary(noneSummary), ptSummary(layers[0].Summary))
			}
			def, _, _ := ptRun(t, spec, []string{"region", "step"}, rows)
			if strings.Join(def, ";") == strings.Join(none, ";") {
				t.Error("the default partition equals the grand total; the fixture does not tell them apart")
			}
		})
	}
	// The default share sums to 1 within each region (south's absent
	// step contributes nothing).
	req := &types.Request{
		Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}, {Type: types.GROUP_CATEGORY, Field: "step"}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "x", Label: "v"}},
		Overlays:     []types.OverlaySpec{totalSeriesSpecs()[1]},
	}
	resp := &types.Response{}
	for _, r := range rows {
		resp.Data = append(resp.Data, map[string]any{"region": r.keys["region"], "step": r.keys["step"], "v": r.v})
	}
	if err := applyOverlaysSeriesToResponse(req, resp, nil, FullComputePlan()); err != nil {
		t.Fatal(err)
	}
	sums := map[any]float64{}
	for _, e := range resp.Overlays[0].Payload.Series.Entries {
		sums[e.Key[0]] += *e.Summary.Statistic
	}
	for region, sum := range sums {
		if math.Abs(sum-1) > 1e-12 {
			t.Errorf("%v: shares sum to %v, want 1", region, sum)
		}
	}
	if len(sums) != 3 {
		t.Errorf("shares over %d regions, want 3", len(sums))
	}
}

// TestOverlaySeries_ThreeGroupersPrefixes: over [region, tier, step]
// partition_by [region] runs the series down (tier, step) within each
// region — equal to a [tier, step] host per region with
// partition_by [] — and the default [region, tier] equals a [step] host
// per (region, tier).
func TestOverlaySeries_ThreeGroupersPrefixes(t *testing.T) {
	spec := partitionedSeriesSpecs()[0] // DELTA_VS_PRIOR
	var rows []ptRow
	for i, r := range ptRows() {
		for _, tier := range []string{"gold", "silver"} {
			keys := map[string]string{"region": r.keys["region"], "tier": tier, "step": r.keys["step"]}
			rows = append(rows, ptRow{keys: keys, v: r.v + float64(len(tier)*i)})
		}
	}
	rows = interleave(rows)
	sub := func(match func(ptRow) bool, cols []string, s types.OverlaySpec) map[string]string {
		var part []ptRow
		for _, r := range rows {
			if match(r) {
				part = append(part, r)
			}
		}
		got, _, _ := ptRun(t, s, cols, part)
		out := map[string]string{}
		for i, g := range got {
			out[part[i].keys["region"]+"|"+part[i].keys["tier"]+"|"+part[i].keys["step"]] = g[strings.Index(g, "=")+1:]
		}
		return out
	}
	for name, c := range map[string]struct {
		spec types.OverlaySpec
		want func() map[string]string
	}{
		"partition_by=[region]": {withParam(t, spec, "partition_by", []string{"region"}), func() map[string]string {
			want := map[string]string{}
			for _, reg := range []string{"east", "south", "west"} {
				for k, v := range sub(func(r ptRow) bool { return r.keys["region"] == reg }, []string{"tier", "step"},
					withParam(t, spec, "partition_by", []string{})) {
					want[k] = v
				}
			}
			return want
		}},
		"default [region, tier]": {spec, func() map[string]string {
			want := map[string]string{}
			for _, reg := range []string{"east", "south", "west"} {
				for _, tier := range []string{"gold", "silver"} {
					for k, v := range sub(func(r ptRow) bool { return r.keys["region"] == reg && r.keys["tier"] == tier }, []string{"step"}, spec) {
						want[k] = v
					}
				}
			}
			return want
		}},
	} {
		want := c.want()
		got, _, _ := ptRun(t, c.spec, []string{"region", "tier", "step"}, rows)
		for _, g := range got {
			k, v := g[:strings.Index(g, "=")], g[strings.Index(g, "=")+1:]
			if want[k] != v {
				t.Errorf("%s: %s = %s, want %s", name, k, v, want[k])
			}
		}
	}
}

// TestOverlaySeries_PartitionWarningsOncePerLayer: a zero baseline in
// one partition raises PULSE_OVERLAY_REF_ZERO once for the layer, with
// group_count the whole host's and partitions the raising count. A
// one-row zero "north" partition is a zero total and a zero SD for the
// _TOTAL kinds, so they raise it from that partition alone.
func TestOverlaySeries_PartitionWarningsOncePerLayer(t *testing.T) {
	rows := ptRows()
	rows = append(rows, ptRow{keys: map[string]string{"region": "north", "step": "s1"}, v: 0})
	specs := partitionedSeriesSpecs()
	for _, c := range []struct {
		spec       types.OverlaySpec
		partitions int
	}{
		{specs[4], 2},  // INDEX_VS_BASELINE: west's and north's first values are 0
		{specs[8], 1},  // INDEX_VS_TOTAL
		{specs[9], 1},  // SHARE_OF_TOTAL
		{specs[10], 1}, // ZSCORE_VS_TOTAL
	} {
		_, _, ws := ptRun(t, c.spec, []string{"region", "step"}, interleave(rows))
		if len(ws) != 1 || ws[0].Code != string(errors.PULSE_OVERLAY_REF_ZERO) {
			t.Fatalf("%s: warnings %+v, want one PULSE_OVERLAY_REF_ZERO", c.spec.Kind, ws)
		}
		if ws[0].Details["partitions"] != c.partitions || ws[0].Details["group_count"] != len(rows) {
			t.Errorf("%s: details %v, want partitions %d, group_count %d", c.spec.Kind, ws[0].Details, c.partitions, len(rows))
		}
	}
}

// TestOverlaySeries_PartitionInvalidRefused: a non-prefix or unknown
// partition_by fails the fold PULSE_OVERLAY_PARTITION_INVALID (the
// reading predict shares); a kind outside the partitioned set ignores
// the param.
func TestOverlaySeries_PartitionInvalidRefused(t *testing.T) {
	for _, pb := range [][]string{{"step"}, {"warehouse"}} {
		spec := withParam(t, partitionedSeriesSpecs()[0], "partition_by", pb)
		req := &types.Request{
			Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}, {Type: types.GROUP_CATEGORY, Field: "step"}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "x", Label: "v"}},
			Overlays:     []types.OverlaySpec{spec},
		}
		resp := &types.Response{Data: []map[string]any{{"region": "east", "step": "s1", "v": 1.0}}}
		err := applyOverlaysSeriesToResponse(req, resp, nil, FullComputePlan())
		var ce *errors.CodedError
		if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_OVERLAY_PARTITION_INVALID || ce.Details["index"] != 0 {
			t.Errorf("%v: err = %v, want PULSE_OVERLAY_PARTITION_INVALID at index 0", pb, err)
		}
		if resp.Overlays != nil {
			t.Errorf("%v: a layer survived the refusal", pb)
		}
	}
}

// TestSeriesHost_VectorGrouperKeysOnItsColumn: a vector-input grouper
// with no Field writes its key under <TYPE>_<first field>; the host
// keys each row on that column, not the empty one, so partitions and
// sibling lookups see the real bucket.
func TestSeriesHost_VectorGrouperKeysOnItsColumn(t *testing.T) {
	req := &types.Request{
		Groups: []*types.Group{
			{Type: types.GROUP_KMEANS, Fields: []string{"y", "z"}},
			{Type: types.GROUP_CATEGORY, Field: "step"},
		},
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "x", Label: "v"}},
	}
	resp := &types.Response{Data: []map[string]any{
		{"GROUP_KMEANS_y": "c0", "step": "s1", "v": 1.0},
		{"GROUP_KMEANS_y": "c1", "step": "s1", "v": 2.0},
	}}
	host := buildSeriesHostFromGroupedResponse(req, resp, 0)
	if got := host.GroupFields(); got[0] != "GROUP_KMEANS_y" {
		t.Fatalf("group fields %v", got)
	}
	for i, want := range []string{"c0", "c1"} {
		if k, _ := host.GroupKey(i); k[0] != want {
			t.Errorf("row %d key %v, want %s first", i, k, want)
		}
	}
}
