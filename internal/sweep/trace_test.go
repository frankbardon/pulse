package sweep

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/types"
)

func f64p(v float64) *float64 { return &v }

// TestRankTraced_PenalisedAndAllPenalisedRound: rounds of 3, 2 and 2
// slots, asc. Round 0 holds a failed slot (nil response) and a
// null-objective one (NaN): both penalised. Round 1 is all penalised:
// best null, best_so_far carried. Round 2 improves. Every full-ranking
// entry carries its round; Top trims the ranking only, never the trace.
func TestRankTraced_PenalisedAndAllPenalisedRound(t *testing.T) {
	t.Parallel()

	labels := []string{"s0", "s1", "s2", "s3", "s4", "s5", "s6"}
	resps := []*types.Response{rankResp(4), nil, rankResp(math.NaN()), nil, rankResp(math.NaN()), rankResp(5), rankResp(2)}
	rounds := []RoundSpan{{0, 2}, {3, 4}, {5, 6}}
	r := &types.SweepRank{By: "regressions.fit.residual_std_err", Top: intp(1)}
	ranked, _, trace, err := RankTraced(r, nil, labels, nil, resps, rounds)
	if err != nil {
		t.Fatal(err)
	}
	if len(ranked) != 1 || ranked[0].Slot != 6 || ranked[0].Round == nil || *ranked[0].Round != 2 {
		t.Fatalf("ranking %+v", ranked)
	}
	want := []types.SweepTraceEntry{
		{Round: 0, Slots: [2]int{0, 2}, Evaluated: 3, Penalised: 2, Best: f64p(4), BestSoFar: f64p(4), BestLabel: "s0"},
		{Round: 1, Slots: [2]int{3, 4}, Evaluated: 2, Penalised: 2, BestSoFar: f64p(4), BestLabel: "s0"},
		{Round: 2, Slots: [2]int{5, 6}, Evaluated: 2, Penalised: 0, Best: f64p(2), BestSoFar: f64p(2), BestLabel: "s6"},
	}
	if !reflect.DeepEqual(trace, want) {
		t.Fatalf("trace\n%s\nwant\n%s", traceJSON(t, trace), traceJSON(t, want))
	}
	if w := traceJSON(t, trace[1:2]); w != `[{"round":1,"slots":[3,4],"evaluated":2,"penalised":2,"best":null,"best_so_far":4,"best_label":"s0"}]` {
		t.Fatalf("all-penalised round wire %s", w)
	}
}

// TestRankTraced_DescTiesAndNothingRanked: under desc the best is the
// largest; a tie keeps the earlier slot (rank order); a leading round
// with nothing ranked has best and best_so_far null and no best_label.
func TestRankTraced_DescTiesAndNothingRanked(t *testing.T) {
	t.Parallel()

	labels := []string{"a", "b", "c", "d"}
	resps := []*types.Response{nil, rankResp(3), rankResp(7), rankResp(7)}
	rounds := []RoundSpan{{0, 0}, {1, 1}, {2, 3}}
	ranked, _, trace, err := RankTraced(&types.SweepRank{By: "regressions.fit.residual_std_err", Order: types.SweepRankDesc}, nil, labels, nil, resps, rounds)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ranked {
		if e.Round == nil {
			t.Fatalf("entry %+v carries no round", e)
		}
	}
	want := []types.SweepTraceEntry{
		{Round: 0, Slots: [2]int{0, 0}, Evaluated: 1, Penalised: 1},
		{Round: 1, Slots: [2]int{1, 1}, Evaluated: 1, Best: f64p(3), BestSoFar: f64p(3), BestLabel: "b"},
		{Round: 2, Slots: [2]int{2, 3}, Evaluated: 2, Best: f64p(7), BestSoFar: f64p(7), BestLabel: "c"},
	}
	if !reflect.DeepEqual(trace, want) {
		t.Fatalf("trace\n%s\nwant\n%s", traceJSON(t, trace), traceJSON(t, want))
	}
	if w := traceJSON(t, trace[:1]); !strings.Contains(w, `"best":null,"best_so_far":null}`) {
		t.Fatalf("nothing-ranked round wire %s", w)
	}
}

// TestRankTraced_NilRoundsIsRankProfiled: without rounds (every
// non-adaptive mode) no entry carries a round and the trace is nil.
func TestRankTraced_NilRoundsIsRankProfiled(t *testing.T) {
	t.Parallel()

	resps := []*types.Response{rankResp(2), rankResp(1)}
	ranked, _, trace, err := RankTraced(&types.SweepRank{By: "regressions.fit.residual_std_err"}, nil, []string{"a", "b"}, nil, resps, nil)
	if err != nil {
		t.Fatal(err)
	}
	if trace != nil || len(ranked) != 2 || ranked[0].Round != nil || ranked[1].Round != nil {
		t.Fatalf("ranking %+v trace %+v", ranked, trace)
	}
}

func traceJSON(t *testing.T, v []types.SweepTraceEntry) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
