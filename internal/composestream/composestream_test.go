package composestream

import (
	"bytes"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// TestWrite_RequestOnlyRecords: with no response, Write emits only the
// records the request alone decides — a ranked sweep's empty ranking
// (+ an empty profile under rank.profile) and, under TerminalFits, an
// empty fits object last — and nothing for a nil request.
func TestWrite_RequestOnlyRecords(t *testing.T) {
	t.Parallel()

	req := &types.ComposedRequest{
		Sweep: &types.SweepSpec{Rank: &types.SweepRank{By: "n", Profile: true}},
		Fits:  []types.FitSpec{{Name: "m"}},
	}
	for _, tc := range []struct {
		name string
		req  *types.ComposedRequest
		opts Options
		want string
	}{
		{"off", req, Options{}, "{\"ranking\":[],\"ranking_profile\":{}}\n"},
		{"terminal fits", req, Options{TerminalFits: true}, "{\"ranking\":[],\"ranking_profile\":{}}\n{\"fits\":{}}\n"},
		{"nil request", nil, Options{TerminalFits: true}, ""},
	} {
		var buf bytes.Buffer
		if err := Write(&buf, tc.req, nil, tc.opts); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got := buf.String(); got != tc.want {
			t.Errorf("%s: wrote %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestWrite_AdaptiveSweepTrace: under an adaptive mode the final
// ranking record carries `sweep_trace` (an empty list without a
// response, a null best kept); a non-adaptive mode never writes it.
func TestWrite_AdaptiveSweepTrace(t *testing.T) {
	t.Parallel()

	round, best := 0, 2.5
	resp := &types.ComposedResponse{
		Ranking: []types.RankEntry{{Label: "a", Value: best, Rank: 1, Slot: 1, Round: &round}},
		SweepTrace: []types.SweepTraceEntry{
			{Round: 0, Slots: [2]int{0, 1}, Evaluated: 2, Penalised: 1, Best: &best, BestSoFar: &best, BestLabel: "a"},
			{Round: 1, Slots: [2]int{2, 2}, Evaluated: 1, Penalised: 1, BestSoFar: &best, BestLabel: "a"},
		},
	}
	adaptive := &types.ComposedRequest{Sweep: &types.SweepSpec{Mode: types.SweepModeDE, Rank: &types.SweepRank{By: "n"}}}
	grid := &types.ComposedRequest{Sweep: &types.SweepSpec{Rank: &types.SweepRank{By: "n"}}}
	for _, tc := range []struct {
		name string
		req  *types.ComposedRequest
		resp *types.ComposedResponse
		want string
	}{
		{"adaptive", adaptive, resp, `{"ranking":[{"label":"a","value":2.5,"rank":1,"slot":1,"axes":null,"round":0}],` +
			`"sweep_trace":[{"round":0,"slots":[0,1],"evaluated":2,"penalised":1,"best":2.5,"best_so_far":2.5,"best_label":"a"},` +
			`{"round":1,"slots":[2,2],"evaluated":1,"penalised":1,"best":null,"best_so_far":2.5,"best_label":"a"}]}` + "\n"},
		{"adaptive without a response", adaptive, nil, `{"ranking":[],"sweep_trace":[]}` + "\n"},
		{"grid", grid, nil, `{"ranking":[]}` + "\n"},
	} {
		var buf bytes.Buffer
		if err := Write(&buf, tc.req, tc.resp, Options{}); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got := buf.String(); got != tc.want {
			t.Errorf("%s: wrote %s, want %s", tc.name, got, tc.want)
		}
	}
}
