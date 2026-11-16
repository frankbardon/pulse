package sweep

import (
	"encoding/json"
	"math"
	"reflect"
	"strconv"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// profileGrid is a 2 × 3 grid over a (strings) and b (numbers declared
// out of order), slot values by (a, b). Expansion order (a slowest):
//
//	slot  a  b  value
//	0     x  3  5
//	1     x  1  2
//	2     x  2  7
//	3     y  3  4
//	4     y  1  9
//	5     y  2  1
func profileGrid() ([]types.SweepAxis, []string, []map[string]any, []*types.Response) {
	spec := []types.SweepAxis{
		{Name: "a", Values: []any{"x", "y"}},
		{Name: "b", Values: []any{json.Number("3"), json.Number("1"), json.Number("2")}},
	}
	val := map[string]float64{"x3": 5, "x1": 2, "x2": 7, "y3": 4, "y1": 9, "y2": 1}
	var (
		labels []string
		axes   []map[string]any
		resps  []*types.Response
	)
	forEachCombination(spec, types.SweepModeGrid, func(_ int, values map[string]any) bool {
		l := values["a"].(string) + string(values["b"].(json.Number))
		labels = append(labels, l)
		axes = append(axes, values)
		resps = append(resps, rankResp(val[l]))
		return true
	})
	return spec, labels, axes, resps
}

func pe(v any, best float64, label string, n int) types.RankProfileEntry {
	return types.RankProfileEntry{Value: v, Best: best, Label: label, N: n}
}

// TestRankProfiled_GridMatchesHandTable: on a 2-axis grid each axis
// value's entry is the min (asc) / max (desc) over the other axis — the
// hand-computed table — with the achieving slot's label and n = the
// slots behind it, in declared value order; Top trims the ranking, never
// the profile.
func TestRankProfiled_GridMatchesHandTable(t *testing.T) {
	t.Parallel()

	n1, n2, n3 := json.Number("1"), json.Number("2"), json.Number("3")
	cases := []struct {
		name string
		r    types.SweepRank
		want map[string][]types.RankProfileEntry
	}{
		{"asc", types.SweepRank{By: "regressions.fit.residual_std_err", Profile: true}, map[string][]types.RankProfileEntry{
			"a": {pe("x", 2, "x1", 3), pe("y", 1, "y2", 3)},
			"b": {pe(n3, 4, "y3", 2), pe(n1, 2, "x1", 2), pe(n2, 1, "y2", 2)},
		}},
		{"desc, top 1", types.SweepRank{By: "regressions.fit.residual_std_err", Order: types.SweepRankDesc, Top: intp(1), Profile: true}, map[string][]types.RankProfileEntry{
			"a": {pe("x", 7, "x2", 3), pe("y", 9, "y1", 3)},
			"b": {pe(n3, 5, "x3", 2), pe(n1, 9, "y1", 2), pe(n2, 7, "x2", 2)},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec, labels, axes, resps := profileGrid()
			ranking, profile, err := RankProfiled(&tc.r, spec, labels, axes, resps)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(profile, tc.want) {
				t.Fatalf("profile %+v\nwant %+v", profile, tc.want)
			}
			plain, err := Rank(&tc.r, labels, axes, resps)
			if err != nil || !reflect.DeepEqual(ranking, plain) {
				t.Fatalf("RankProfiled ranking %+v differs from Rank %+v (%v)", ranking, plain, err)
			}
		})
	}
}

// TestRankProfiled_OnlyRankedSlotsCount: a null-valued slot and a
// failed (nil) slot are not evaluated points — n drops, and a value only
// they carried has no entry.
func TestRankProfiled_OnlyRankedSlotsCount(t *testing.T) {
	t.Parallel()

	spec, labels, axes, resps := profileGrid()
	resps[5] = rankResp(math.NaN()) // y2: null
	resps[2] = nil                  // x2: failed
	_, profile, err := RankProfiled(&types.SweepRank{By: "regressions.fit.residual_std_err", Profile: true}, spec, labels, axes, resps)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]types.RankProfileEntry{
		"a": {pe("x", 2, "x1", 2), pe("y", 4, "y3", 2)},
		"b": {pe(json.Number("3"), 4, "y3", 2), pe(json.Number("1"), 2, "x1", 2)},
	}
	if !reflect.DeepEqual(profile, want) {
		t.Fatalf("profile %+v\nwant %+v", profile, want)
	}
}

// TestRankProfiled_RandomIsBinnedAndSparse: a random sweep's profile
// bins each range axis (default 10 bins): only bins a ranked draw fell
// into have an entry, ascending, each holding its best slot's draw
// inside [lo, hi]; the n's sum to the ranked count.
func TestRankProfiled_RandomIsBinnedAndSparse(t *testing.T) {
	t.Parallel()

	lo, hi := 0.0, 10.0
	spec := &types.SweepSpec{Mode: types.SweepModeRandom, Samples: 40, RNGSeed: 7, Axes: []types.SweepAxis{
		{Name: "d", Range: &types.SweepRange{Min: &lo, Max: &hi}},
		{Name: "l", Range: &types.SweepRange{Min: fp(0.01), Max: &hi, Scale: types.SweepScaleLog}},
	}}
	var (
		labels []string
		axes   []map[string]any
		resps  []*types.Response
	)
	forEachSample(spec, EffectiveSeed(spec), func(slot int, values map[string]any) bool {
		labels = append(labels, "s"+strconv.Itoa(slot))
		axes = append(axes, values)
		resps = append(resps, rankResp(float64(slot+1)))
		return true
	})
	resps[1] = rankResp(math.NaN())
	resps[3] = nil
	_, profile, err := RankProfiled(&types.SweepRank{By: "regressions.fit.residual_std_err", Profile: true}, spec.Axes, labels, axes, resps)
	if err != nil {
		t.Fatal(err)
	}
	for _, ax := range spec.Axes {
		entries := profile[ax.Name]
		if len(entries) == 0 || len(entries) > types.DefaultProfileBins {
			t.Fatalf("%s: %d entries, want 1..%d bins", ax.Name, len(entries), types.DefaultProfileBins)
		}
		total := 0
		for i, e := range entries {
			total += e.N
			if e.Lo == nil || e.Hi == nil || !(*e.Lo < *e.Hi) {
				t.Fatalf("%s[%d]: edges %v %v", ax.Name, i, e.Lo, e.Hi)
			}
			if i > 0 && *entries[i-1].Hi > *e.Lo {
				t.Errorf("%s: bins not ascending: %+v", ax.Name, entries)
			}
			v := profileNumber(e.Value)
			if v < *e.Lo || v > *e.Hi {
				t.Errorf("%s[%d]: value %v outside [%v, %v]", ax.Name, i, v, *e.Lo, *e.Hi)
			}
			slot, _ := strconv.Atoi(e.Label[1:])
			if slot == 1 || slot == 3 || axes[slot][ax.Name] != e.Value || e.Best != float64(slot+1) {
				t.Errorf("%s[%d] = %+v does not match ranked slot %d", ax.Name, i, e, slot)
			}
		}
		if total != 38 {
			t.Errorf("%s: n sums to %d, want the 38 ranked slots", ax.Name, total)
		}
	}
}

// binEntry is a binned profile entry.
func binEntry(v any, lo, hi, best float64, label string, n int) types.RankProfileEntry {
	return types.RankProfileEntry{Value: v, Lo: &lo, Hi: &hi, Best: best, Label: label, N: n}
}

// TestRankProfiled_RangeAxisBinsHandTable: a linear axis on [0, 10] in
// 5 bins and a log axis on [1e-3, 1e3] in 6 (one per decade) against
// the hand table — the rank order walked, the first slot per bin its
// best, bins ascending whatever the rank order, empty bins absent, the
// range max in the last bin. A list axis beside them is never binned.
func TestRankProfiled_RangeAxisBinsHandTable(t *testing.T) {
	t.Parallel()

	spec := []types.SweepAxis{
		{Name: "d", Range: &types.SweepRange{Min: fp(0), Max: fp(10)}},
		{Name: "l", Range: &types.SweepRange{Min: fp(1e-3), Max: fp(1e3), Scale: types.SweepScaleLog}},
		{Name: "k", Values: []any{"a", "b"}},
	}
	// Rank order (best first): slot label, d, l, k.
	rows := []struct {
		label string
		d, l  string
		k     string
	}{
		{"e", "10", "1000", "a"},
		{"a", "1", "0.002", "b"},
		{"c", "7.2", "0.7", "a"},
		{"b", "1.5", "0.5", "b"},
		{"d", "9.9", "50", "a"},
	}
	var ranked []types.RankEntry
	for i, r := range rows {
		ranked = append(ranked, types.RankEntry{Label: r.label, Value: float64(i + 1), Rank: i + 1, Slot: i,
			Axes: map[string]any{"d": json.Number(r.d), "l": json.Number(r.l), "k": r.k}})
	}
	bins := map[string]int{"d": 5, "l": 6}
	for _, ax := range spec[:2] {
		got := Profile(spec, ranked, bins[ax.Name])[ax.Name]
		var want []types.RankProfileEntry
		switch ax.Name {
		case "d":
			want = []types.RankProfileEntry{
				binEntry(json.Number("1"), 0, 2, 2, "a", 2),
				binEntry(json.Number("7.2"), 6, 8, 3, "c", 1),
				binEntry(json.Number("10"), 8, 10, 1, "e", 2),
			}
		case "l":
			want = []types.RankProfileEntry{
				binEntry(json.Number("0.002"), 1e-3, 1e-2, 2, "a", 1),
				binEntry(json.Number("0.7"), 0.1, 1, 3, "c", 2),
				binEntry(json.Number("50"), 10, 100, 5, "d", 1),
				binEntry(json.Number("1000"), 100, 1000, 1, "e", 1),
			}
		}
		if len(got) != len(want) {
			t.Fatalf("%s: %+v\nwant %+v", ax.Name, got, want)
		}
		for i := range want {
			g, w := got[i], want[i]
			if g.Value != w.Value || g.Best != w.Best || g.Label != w.Label || g.N != w.N ||
				g.Lo == nil || g.Hi == nil || !near(*g.Lo, *w.Lo) || !near(*g.Hi, *w.Hi) {
				t.Fatalf("%s[%d] = %+v (lo %v hi %v)\nwant %+v (lo %v hi %v)", ax.Name, i, g, edgeOf(g.Lo), edgeOf(g.Hi), w, *w.Lo, *w.Hi)
			}
		}
		// The end edges are the declared bounds exactly.
		if *got[len(got)-1].Hi != *ax.Range.Max || ProfileBinEdge(ax.Range, bins[ax.Name], 0) != *ax.Range.Min {
			t.Fatalf("%s: end edges %v / %v", ax.Name, ProfileBinEdge(ax.Range, bins[ax.Name], 0), *got[len(got)-1].Hi)
		}
	}
	list := Profile(spec, ranked, 5)["k"]
	if len(list) != 2 || list[0].Lo != nil || list[0].Hi != nil || list[0].Value != "a" || list[0].N != 3 {
		t.Fatalf("list axis binned or reordered: %+v", list)
	}
}

// TestProfileBin_EdgesAgree: every value sits in the bin whose reported
// edges hold it — across a dense walk of a linear and a log range, the
// bin index never decreases and each value is in [edge k, edge k+1).
func TestProfileBin_EdgesAgree(t *testing.T) {
	t.Parallel()

	for _, rg := range []*types.SweepRange{
		{Min: fp(-3), Max: fp(0.7)},
		{Min: fp(1e-4), Max: fp(37), Scale: types.SweepScaleLog},
	} {
		for _, bins := range []int{1, 3, 10, 17} {
			prev := 0
			for i := 0; i <= 4000; i++ {
				v := RangeValue(rg, float64(i)/4000)
				k := ProfileBin(rg, bins, v)
				lo, hi := ProfileBinEdge(rg, bins, k), ProfileBinEdge(rg, bins, k+1)
				if k < prev || v < lo || v > hi || (v == hi && k != bins-1) {
					t.Fatalf("%+v bins %d: v=%v in bin %d [%v, %v) (prev %d)", *rg, bins, v, k, lo, hi, prev)
				}
				prev = k
			}
		}
	}
}

// TestRankProfiled_DefaultAndSetBins: profile_bins nil cuts a range axis
// into types.DefaultProfileBins; an explicit count is used as given.
func TestRankProfiled_DefaultAndSetBins(t *testing.T) {
	t.Parallel()

	spec := []types.SweepAxis{{Name: "d", Range: &types.SweepRange{Min: fp(0), Max: fp(1)}}}
	var (
		labels []string
		axes   []map[string]any
		resps  []*types.Response
	)
	for i := 0; i < 100; i++ {
		labels = append(labels, strconv.Itoa(i))
		axes = append(axes, map[string]any{"d": RangeNumber(spec[0].Range, (float64(i)+0.5)/100)})
		resps = append(resps, rankResp(float64(i+1)))
	}
	for _, tc := range []struct {
		bins *int
		want int
	}{{nil, types.DefaultProfileBins}, {intp(4), 4}, {intp(1), 1}} {
		_, profile, err := RankProfiled(&types.SweepRank{By: "regressions.fit.residual_std_err", Profile: true, ProfileBins: tc.bins}, spec, labels, axes, resps)
		if err != nil {
			t.Fatal(err)
		}
		if got := len(profile["d"]); got != tc.want {
			t.Fatalf("bins %v: %d entries, want %d", tc.bins, got, tc.want)
		}
		for _, e := range profile["d"] {
			if e.N != 100/tc.want {
				t.Fatalf("bins %v: entry %+v, want n %d", tc.bins, e, 100/tc.want)
			}
		}
	}
}

func near(a, b float64) bool { return math.Abs(a-b) <= 1e-12*math.Max(1, math.Abs(b)) }

func edgeOf(p *float64) any {
	if p == nil {
		return nil
	}
	return *p
}

// TestRankProfiled_OffIsNil: without profile the profile is nil and the
// ranking equals Rank's.
func TestRankProfiled_OffIsNil(t *testing.T) {
	t.Parallel()

	spec, labels, axes, resps := profileGrid()
	r := &types.SweepRank{By: "regressions.fit.residual_std_err", Top: intp(2)}
	ranking, profile, err := RankProfiled(r, spec, labels, axes, resps)
	if err != nil || profile != nil {
		t.Fatalf("profile %+v, err %v", profile, err)
	}
	plain, _ := Rank(r, labels, axes, resps)
	if !reflect.DeepEqual(ranking, plain) || len(ranking) != 2 {
		t.Fatalf("ranking %+v vs %+v", ranking, plain)
	}
	if _, p, err := RankProfiled(nil, spec, labels, axes, resps); p != nil || err != nil {
		t.Fatalf("nil rank: %+v %v", p, err)
	}
}

// TestEffectiveSeed_ProfileNeverMoves: turning rank.profile on, or
// setting rank.profile_bins, never redraws a sampled sweep's points.
func TestEffectiveSeed_ProfileNeverMoves(t *testing.T) {
	t.Parallel()

	lo, hi := 0.0, 1.0
	mk := func(profile bool, bins *int) *types.SweepSpec {
		return &types.SweepSpec{Mode: types.SweepModeLHS, Samples: 4, Request: json.RawMessage(`{}`),
			Axes: []types.SweepAxis{{Name: "d", Range: &types.SweepRange{Min: &lo, Max: &hi}}},
			Rank: &types.SweepRank{By: "x", Profile: profile, ProfileBins: bins}}
	}
	off, on, binned := mk(false, nil), mk(true, nil), mk(true, intp(4))
	for _, s := range []*types.SweepSpec{on, binned} {
		if EffectiveSeed(off) != EffectiveSeed(s) {
			t.Fatalf("seed moved: %d vs %d (%+v)", EffectiveSeed(off), EffectiveSeed(s), *s.Rank)
		}
	}
	if !on.Rank.Profile || binned.Rank.ProfileBins == nil {
		t.Fatal("EffectiveSeed mutated the caller's rank")
	}
}
