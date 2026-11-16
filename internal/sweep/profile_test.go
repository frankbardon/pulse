package sweep

import (
	"encoding/json"
	"math"
	"reflect"
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

// TestRankProfiled_RandomIsSparse: a random sweep's profile lists only
// the drawn (and ranked) values, ascending, each n = 1; the n's sum to
// the ranked count.
func TestRankProfiled_RandomIsSparse(t *testing.T) {
	t.Parallel()

	lo, hi := 0.0, 10.0
	spec := &types.SweepSpec{Mode: types.SweepModeRandom, Samples: 5, RNGSeed: 7, Axes: []types.SweepAxis{
		{Name: "d", Range: &types.SweepRange{Min: &lo, Max: &hi}},
		{Name: "l", Range: &types.SweepRange{Min: &lo, Max: &hi}},
	}}
	var (
		labels []string
		axes   []map[string]any
		resps  []*types.Response
	)
	forEachSample(spec, EffectiveSeed(spec), func(slot int, values map[string]any) bool {
		labels = append(labels, string(rune('p'+slot)))
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
		if len(entries) != 3 {
			t.Fatalf("%s: %d entries, want the 3 ranked draws: %+v", ax.Name, len(entries), entries)
		}
		seen := map[string]bool{}
		for i, e := range entries {
			if e.N != 1 {
				t.Errorf("%s[%d]: n %d, want 1 (one evaluated point per drawn value)", ax.Name, i, e.N)
			}
			if i > 0 && profileNumber(entries[i-1].Value) >= profileNumber(e.Value) {
				t.Errorf("%s: entries not ascending: %+v", ax.Name, entries)
			}
			slot := int(e.Label[0] - 'p')
			if slot == 1 || slot == 3 || axes[slot][ax.Name] != e.Value || e.Best != float64(slot+1) {
				t.Errorf("%s[%d] = %+v does not match ranked slot %d", ax.Name, i, e, slot)
			}
			seen[e.Label] = true
		}
		if len(seen) != 3 {
			t.Errorf("%s: labels %v", ax.Name, seen)
		}
	}
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

// TestEffectiveSeed_ProfileNeverMoves: turning rank.profile on never
// redraws a sampled sweep's points.
func TestEffectiveSeed_ProfileNeverMoves(t *testing.T) {
	t.Parallel()

	lo, hi := 0.0, 1.0
	mk := func(profile bool) *types.SweepSpec {
		return &types.SweepSpec{Mode: types.SweepModeLHS, Samples: 4, Request: json.RawMessage(`{}`),
			Axes: []types.SweepAxis{{Name: "d", Range: &types.SweepRange{Min: &lo, Max: &hi}}},
			Rank: &types.SweepRank{By: "x", Profile: profile}}
	}
	off, on := mk(false), mk(true)
	if EffectiveSeed(off) != EffectiveSeed(on) {
		t.Fatalf("seed moved: %d vs %d", EffectiveSeed(off), EffectiveSeed(on))
	}
	if !on.Rank.Profile {
		t.Fatal("EffectiveSeed mutated the caller's rank")
	}
}
