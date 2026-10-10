package pulse

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// rankSweepBody is a slot whose one-sample t-test runs against the `mu`
// axis, so its `tests.t.statistic` (a list element selected by label)
// differs per mu.
const rankSweepBody = `{"cohort":{"filename":"` + parityCohort + `"},
	"aggregations":[{"type":"AGG_AVERAGE","field":"age","label":"v"}],
	"tests":[{"type":"TEST_T","field":"age","label":"t","params":{"mu":{"$var":"mu"}}}]}`

// rankedSweep sweeps mu × rep (rep only labels, so each mu ties with
// itself) after one explicit slot, ranked by rank.
func rankedSweep(t *testing.T, rank string) *ComposedRequest {
	t.Helper()
	return decodeComposedJSON(t, `{"requests":[{"label":"base","cohort":{"filename":"`+parityCohort+`"},
		"tests":[{"type":"TEST_T","field":"age","label":"t"}]}],
		"sweep":{"axes":[{"name":"mu","values":[30,10,50]},{"name":"rep","values":[1,2]}],
		"label":"mu{{mu}}_{{rep}}","request":`+rankSweepBody+`,"rank":`+rank+`}}`)
}

// tStat is the slot's t statistic.
func tStat(t *testing.T, r *Response) float64 {
	t.Helper()
	if len(r.Tests) != 1 {
		t.Fatalf("slot carries %d tests", len(r.Tests))
	}
	return r.Tests[0].Statistic
}

func composeRuns(p *Pulse) map[string]func(*ComposedRequest) (*ComposedResponse, error) {
	ctx := context.Background()
	return map[string]func(*ComposedRequest) (*ComposedResponse, error){
		"Compose": func(c *ComposedRequest) (*ComposedResponse, error) { return p.Compose(ctx, c) },
		"ComposeParallel": func(c *ComposedRequest) (*ComposedResponse, error) {
			return p.ComposeParallel(ctx, c, ComposeOptions{MaxWorkers: 4})
		},
	}
}

// TestComposeSweep_RankOrdersSweepSlots: the ranking orders only the
// sweep slots (never the explicit `base`) by the wire value, ties in
// expansion order, 1-based, `top` trimming the ranking but no response.
func TestComposeSweep_RankOrdersSweepSlots(t *testing.T) {
	p := sweepPulse(t, Options{})
	for name, run := range composeRuns(p) {
		t.Run(name, func(t *testing.T) {
			for _, order := range []string{"asc", "desc"} {
				resp, err := run(rankedSweep(t, `{"by":"tests.t.statistic","order":"`+order+`","top":4}`))
				if err != nil {
					t.Fatal(err)
				}
				if len(resp.Responses) != 7 {
					t.Fatalf("top trimmed responses: %d", len(resp.Responses))
				}
				labels := []string{"mu30_1", "mu30_2", "mu10_1", "mu10_2", "mu50_1", "mu50_2"}
				byLabel := map[string]float64{}
				for i, r := range resp.Responses[1:] {
					byLabel[labels[i]] = tStat(t, r)
				}
				// The expected order: stable sort of expansion order.
				want := append([]string(nil), labels...)
				less := func(a, b string) bool {
					if order == "desc" {
						return byLabel[a] > byLabel[b]
					}
					return byLabel[a] < byLabel[b]
				}
				for i := 1; i < len(want); i++ {
					for j := i; j > 0 && less(want[j], want[j-1]); j-- {
						want[j], want[j-1] = want[j-1], want[j]
					}
				}
				want = want[:4]
				if len(resp.Ranking) != 4 {
					t.Fatalf("%s: ranking %+v", order, resp.Ranking)
				}
				for i, e := range resp.Ranking {
					if e.Label != want[i] || e.Rank != i+1 || e.Value != byLabel[want[i]] {
						t.Fatalf("%s: entry %d = %+v, want %s (%v)", order, i, e, want[i], byLabel[want[i]])
					}
					if e.Label == "base" {
						t.Fatal("an explicit slot was ranked")
					}
				}
				// The rep-only twins tie: the _1 slot ranks first.
				if strings.HasSuffix(resp.Ranking[0].Label, "_2") {
					t.Fatalf("%s: tie broke against expansion order: %+v", order, resp.Ranking)
				}
			}
		})
	}
}

// TestComposeSweep_RankPathRefused: a path that resolves in no slot
// fails the batch with PULSE_SWEEP_RANK_PATH naming slot and segment, on
// both entry points; a malformed path is refused before any slot runs.
func TestComposeSweep_RankPathRefused(t *testing.T) {
	p := sweepPulse(t, Options{})
	for name, run := range composeRuns(p) {
		t.Run(name, func(t *testing.T) {
			_, err := run(rankedSweep(t, `{"by":"tests.nowhere.statistic"}`))
			ce := requireCoded(t, err, perr.PULSE_SWEEP_RANK_PATH)
			if ce.Details["label"] != "mu30_1" || ce.Details["segment"] != "nowhere" {
				t.Fatalf("details %+v", ce.Details)
			}
			_, err = run(rankedSweep(t, `{"by":"tests..statistic"}`))
			requireCoded(t, err, perr.PULSE_SWEEP_RANK_PATH)
		})
	}
	env, err := p.PredictCompose(context.Background(), rankedSweep(t, `{"by":"tests..statistic"}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(env.Errors) == 0 || env.Errors[0].Code != string(perr.PULSE_SWEEP_RANK_PATH) {
		t.Fatalf("predict: %+v", env.Errors)
	}
}

// TestComposeSweep_RankReadsUnshapedSlots: a slot `return` that drops
// the ranked part (and so would skip computing it) does not starve the
// rank — the wire still drops it — and a Compose-level `return` keeps
// `ranking` whole and exact.
func TestComposeSweep_RankReadsUnshapedSlots(t *testing.T) {
	p := sweepPulse(t, Options{})
	body := strings.Replace(rankSweepBody, `"cohort"`, `"return":{"include":["data"]},"cohort"`, 1)
	c := decodeComposedJSON(t, `{"return":{"preset":"minimal","precision":1},
		"sweep":{"axes":[{"name":"mu","values":[10.123456,50.654321]}],
		"label":"mu{{mu}}","request":`+body+`,"rank":{"by":"tests.t.statistic"}}}`)
	for name, run := range composeRuns(p) {
		t.Run(name, func(t *testing.T) {
			resp, err := run(c)
			if err != nil {
				t.Fatal(err)
			}
			if len(resp.Ranking) != 2 {
				t.Fatalf("ranking %+v", resp.Ranking)
			}
			for _, r := range resp.Responses {
				if r.Tests != nil {
					t.Fatal("slot return did not drop tests")
				}
			}
			raw, err := json.Marshal(resp)
			if err != nil {
				t.Fatal(err)
			}
			var wire struct {
				Ranking []types.RankEntry `json:"ranking"`
			}
			if err := json.Unmarshal(raw, &wire); err != nil {
				t.Fatal(err)
			}
			if len(wire.Ranking) != 2 || wire.Ranking[0] != resp.Ranking[0] || wire.Ranking[1] != resp.Ranking[1] {
				t.Fatalf("compose return shaped the ranking:\n%s", raw)
			}
		})
	}
}

// TestComposeSweep_RankFreeByteIdentical: no rank, no `ranking` key.
func TestComposeSweep_RankFreeByteIdentical(t *testing.T) {
	p := sweepPulse(t, Options{})
	c := decodeComposedJSON(t, `{"sweep":{"axes":[{"name":"op","values":["AGG_SUM","AGG_MAX"]}],"request":`+sweepSlotBody+`}}`)
	resp, err := p.Compose(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(resp)
	if resp.Ranking != nil || bytes.Contains(raw, []byte(`"ranking"`)) {
		t.Fatalf("rank-free sweep carries a ranking: %s", raw)
	}
}
