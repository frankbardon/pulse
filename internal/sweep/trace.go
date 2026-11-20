package sweep

import "github.com/frankbardon/pulse/types"

// RoundSpan is one adaptive round's inclusive [first, last] span of
// 0-based sweep-slot indices (the RankEntry.Slot axis). Adaptive.Run
// renders rounds round-major, so a run's spans are contiguous and
// ascending.
type RoundSpan = [2]int

// RankTraced is RankProfiled for an adaptive sweep: rounds are the
// run's round spans, in round order. Every full-ranking entry is
// stamped with the round its slot ran in (RankEntry.Round) and the
// trace (Trace) is read over the FULL ranking, before r.Top trims it.
// Nil rounds is RankProfiled exactly: no Round stamped, a nil trace.
func RankTraced(r *types.SweepRank, spec []types.SweepAxis, labels []string, axes []map[string]any, responses []*types.Response, rounds []RoundSpan) ([]types.RankEntry, map[string][]types.RankProfileEntry, []types.SweepTraceEntry, error) {
	full, err := rankAll(r, labels, axes, responses)
	if err != nil || full == nil {
		return full, nil, nil, err
	}
	var trace []types.SweepTraceEntry
	if rounds != nil {
		stampRounds(full, rounds)
		trace = Trace(full, rounds)
	}
	var profile map[string][]types.RankProfileEntry
	if r.Profile {
		bins := 0
		if r.ProfileBins != nil {
			bins = *r.ProfileBins
		}
		profile = Profile(spec, full, bins)
	}
	return trimTop(r, full), profile, trace, nil
}

// roundOf is the index of the span holding slot, -1 when none does.
func roundOf(rounds []RoundSpan, slot int) int {
	for k, s := range rounds {
		if slot >= s[0] && slot <= s[1] {
			return k
		}
	}
	return -1
}

// stampRounds sets each entry's Round to the round its slot ran in.
func stampRounds(full []types.RankEntry, rounds []RoundSpan) {
	for i := range full {
		if k := roundOf(rounds, full[i].Slot); k >= 0 {
			round := k
			full[i].Round = &round
		}
	}
}

// Trace is an adaptive sweep's round trace over full — the ranking in
// rank order, before any Top trim — and the run's round spans: one
// entry per span, in round order. Evaluated is the span's width,
// Penalised the span's slots with no ranking entry (a failed slot, or
// one whose ranked value was null or missing). Best is the first entry
// in rank order whose slot lies in the round (null when none does),
// BestSoFar / BestLabel the first in rank order over rounds 0..k — so
// both follow the rank's order and ties keep its expansion order.
func Trace(full []types.RankEntry, rounds []RoundSpan) []types.SweepTraceEntry {
	ranked := make([]int, len(rounds))
	best := make([]int, len(rounds))
	for k := range best {
		best[k] = -1
	}
	for i, e := range full {
		k := roundOf(rounds, e.Slot)
		if k < 0 {
			continue
		}
		ranked[k]++
		if best[k] < 0 {
			best[k] = i
		}
	}
	out := make([]types.SweepTraceEntry, len(rounds))
	sofar := -1
	for k, s := range rounds {
		n := s[1] - s[0] + 1
		t := types.SweepTraceEntry{Round: k, Slots: s, Evaluated: n, Penalised: n - ranked[k]}
		if b := best[k]; b >= 0 {
			v := full[b].Value
			t.Best = &v
			if sofar < 0 || b < sofar {
				sofar = b
			}
		}
		if sofar >= 0 {
			v := full[sofar].Value
			t.BestSoFar = &v
			t.BestLabel = full[sofar].Label
		}
		out[k] = t
	}
	return out
}
