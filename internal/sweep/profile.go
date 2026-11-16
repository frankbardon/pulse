package sweep

import (
	"encoding/json"
	"sort"

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
// Entry order: a list axis (Values) in declared value order; a range
// axis (a sampled draw) ascending by value. Every spec axis has a key,
// its list empty when nothing ranked. A binned range-axis variant adds
// fields to RankProfileEntry; the per-value walk below stays the list
// rule.
func Profile(spec []types.SweepAxis, ranked []types.RankEntry) map[string][]types.RankProfileEntry {
	out := make(map[string][]types.RankProfileEntry, len(spec))
	for _, ax := range spec {
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

// profileOrder is the permutation of list (keys index-aligned) into the
// axis's entry order: declared value order for a list axis, ascending
// numeric value for a range axis (equal numbers by key text).
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
