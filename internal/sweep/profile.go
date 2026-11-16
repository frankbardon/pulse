package sweep

import (
	"encoding/json"
	"math"
	"sort"

	"github.com/frankbardon/pulse/linalg/detmath"
	"github.com/frankbardon/pulse/types"
)

// Profile is the per-axis profile of a full ranking (rank order, before
// any Top trim): for every axis in spec, one entry per axis value some
// ranked slot ran with. An entry's Best and Label are the FIRST ranked
// slot carrying the value — the best over the other axes in the rank's
// direction, ties keeping expansion order as the ranking does — and N
// counts the ranked slots carrying it. Only ranked slots count (a slot
// that failed, or whose rank value was null or missing, is not an
// evaluated point), so a value no ranked slot ran with has no entry and
// a sampled sweep's profile is sparse.
//
// A list axis (Values) lists its entries in declared value order. A
// range axis (a sampled draw) is binned instead (bins below; bins < 1
// takes types.DefaultProfileBins): one entry per bin some ranked slot
// fell into, in ascending bin order, its Value the first ranked slot's
// draw and Lo / Hi the bin edges. Every spec axis has a key, its list
// empty when nothing ranked.
func Profile(spec []types.SweepAxis, ranked []types.RankEntry, bins int) map[string][]types.RankProfileEntry {
	if bins < 1 {
		bins = types.DefaultProfileBins
	}
	out := make(map[string][]types.RankProfileEntry, len(spec))
	for _, ax := range spec {
		if ax.Range != nil && len(ax.Values) == 0 {
			out[ax.Name] = binnedProfile(ax, ranked, bins)
			continue
		}
		byKey := map[string]int{}
		var list []types.RankProfileEntry
		var keys []string
		for _, e := range ranked {
			v, ok := e.Axes[ax.Name]
			if !ok {
				continue
			}
			k := profileKey(v)
			if i, seen := byKey[k]; seen {
				list[i].N++
				continue
			}
			byKey[k] = len(list)
			list = append(list, types.RankProfileEntry{Value: v, Best: e.Value, Label: e.Label, N: 1})
			keys = append(keys, k)
		}
		order := profileOrder(ax, list, keys)
		entries := make([]types.RankProfileEntry, len(order))
		for i, j := range order {
			entries[i] = list[j]
		}
		out[ax.Name] = entries
	}
	return out
}

// binnedProfile is a range axis's profile: the ranked slots walked in
// rank order, each dropped into its bin (ProfileBin); a bin's entry
// takes the first slot (Best, Label, Value) and counts the rest (N).
// Entries come out in ascending bin order, empty bins absent.
func binnedProfile(ax types.SweepAxis, ranked []types.RankEntry, bins int) []types.RankProfileEntry {
	byBin := map[int]int{}
	var list []types.RankProfileEntry
	var at []int
	for _, e := range ranked {
		v, ok := e.Axes[ax.Name]
		if !ok {
			continue
		}
		k := ProfileBin(ax.Range, bins, profileNumber(v))
		if i, seen := byBin[k]; seen {
			list[i].N++
			continue
		}
		lo, hi := ProfileBinEdge(ax.Range, bins, k), ProfileBinEdge(ax.Range, bins, k+1)
		byBin[k] = len(list)
		list = append(list, types.RankProfileEntry{Value: v, Lo: &lo, Hi: &hi, Best: e.Value, Label: e.Label, N: 1})
		at = append(at, k)
	}
	idx := make([]int, len(list))
	for i := range idx {
		idx[i] = i
	}
	sort.Slice(idx, func(a, b int) bool { return at[idx[a]] < at[idx[b]] })
	entries := make([]types.RankProfileEntry, len(idx))
	for i, j := range idx {
		entries[i] = list[j]
	}
	return entries
}

// ProfileBinEdge is edge k (0..bins) of a validated range cut into bins
// equal-width bins over [min, max] — equal log-width under scale log
// (linalg/detmath, portable): edge 0 is min and edge bins is max
// exactly, edge k in between min + (k/bins)·(max − min) (log: exp of
// the same over log min, log max), the product rounded before its add.
// The edges ignore Integer: bins cut the declared interval.
func ProfileBinEdge(rg *types.SweepRange, bins, k int) float64 {
	lo, hi := *rg.Min, *rg.Max
	switch {
	case k <= 0:
		return lo
	case k >= bins:
		return hi
	}
	f := float64(k) / float64(bins)
	if rg.Scale == types.SweepScaleLog {
		a, b := detmath.Log(lo), detmath.Log(hi)
		return detmath.Exp(a + float64(f*(b-a)))
	}
	return lo + float64(f*(hi-lo))
}

// ProfileBin is the 0-based bin (of bins, ProfileBinEdge's edges) value
// v falls in: the k with edge k <= v < edge k+1, the last bin closed at
// max; a v outside [min, max] lands in the nearest end bin. The bin is
// read off the edges themselves, so a value always sits inside the
// [Lo, Hi] its entry reports.
func ProfileBin(rg *types.SweepRange, bins int, v float64) int {
	if bins <= 1 {
		return 0
	}
	lo, hi := *rg.Min, *rg.Max
	var f float64
	if rg.Scale == types.SweepScaleLog {
		a, b := detmath.Log(lo), detmath.Log(hi)
		if v > 0 {
			f = (detmath.Log(v) - a) / (b - a)
		}
	} else {
		f = (v - lo) / (hi - lo)
	}
	// Clamp before the int conversion: a NaN or out-of-range float's
	// conversion differs across architectures.
	g := math.Floor(float64(f * float64(bins)))
	k := 0
	switch {
	case math.IsNaN(g) || g < 0:
	case g > float64(bins-1):
		k = bins - 1
	default:
		k = int(g)
	}
	// Settle against the reported edges: the estimate may sit one off
	// where the edge computation rounds differently.
	for k > 0 && v < ProfileBinEdge(rg, bins, k) {
		k--
	}
	for k < bins-1 && v >= ProfileBinEdge(rg, bins, k+1) {
		k++
	}
	return k
}

// profileOrder is the permutation of list (keys index-aligned) into the
// axis's entry order: declared value order for a list axis, ascending
// numeric value otherwise (equal numbers by key text).
func profileOrder(ax types.SweepAxis, list []types.RankProfileEntry, keys []string) []int {
	idx := make([]int, len(list))
	for i := range idx {
		idx[i] = i
	}
	if len(ax.Values) > 0 {
		declared := make(map[string]int, len(ax.Values))
		for j := len(ax.Values) - 1; j >= 0; j-- {
			declared[profileKey(ax.Values[j])] = j
		}
		sort.SliceStable(idx, func(a, b int) bool {
			return declared[keys[idx[a]]] < declared[keys[idx[b]]]
		})
		return idx
	}
	sort.SliceStable(idx, func(a, b int) bool {
		fa, fb := profileNumber(list[idx[a]].Value), profileNumber(list[idx[b]].Value)
		if fa != fb {
			return fa < fb
		}
		return keys[idx[a]] < keys[idx[b]]
	})
	return idx
}

// profileKey is v's identity on the profile: its JSON text, so a value
// read off a slot's axes matches the declared value it came from.
func profileKey(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

// profileNumber is a range axis value as a float64 (a draw is a
// json.Number); a non-number is 0.
func profileNumber(v any) float64 {
	switch x := v.(type) {
	case json.Number:
		f, _ := x.Float64()
		return f
	case float64:
		return x
	}
	return 0
}
