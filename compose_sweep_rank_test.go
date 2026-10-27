package pulse

import (
	"bytes"
	"context"
	"encoding/json"
	"strconv"
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
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

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
			got, _ := json.Marshal(wire.Ranking)
			want, _ := json.Marshal(resp.Ranking)
			if len(wire.Ranking) != 2 || string(got) != string(want) {
				t.Fatalf("compose return shaped the ranking:\n%s", raw)
			}
		})
	}
}

// TestComposeSweep_RankFreeByteIdentical: no rank, no `ranking` key.
func TestComposeSweep_RankFreeByteIdentical(t *testing.T) {
	t.Parallel()

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

// TestComposeSweep_RankCarriesSlotAndAxes: every ranking entry carries
// its 0-based sweep Slot (its response is responses[explicit+slot]) and
// the typed Axes values the slot ran with — under grid, zip, random and
// lhs, serial and parallel byte-identical, a pinned seed replaying the
// same bytes. On the wire a number axis stays a number, string and
// boolean axes are preserved; nothing is parsed off a label.
func TestComposeSweep_RankCarriesSlotAndAxes(t *testing.T) {
	t.Parallel()

	p := sweepPulse(t, Options{})
	zip := decodeComposedJSON(t, `{"requests":[{"label":"base","cohort":{"filename":"`+parityCohort+`"},
		"tests":[{"type":"TEST_T","field":"age","label":"t"}]}],
		"sweep":{"mode":"zip","axes":[{"name":"mu","values":[30,10.5,50]},{"name":"tag","values":["x","y","z"]},
		{"name":"flag","values":[true,false,true]}],
		"label":"{{tag}}_{{flag}}","request":`+rankSweepBody+`,"rank":{"by":"tests.t.statistic","order":"desc"}}}`)
	cases := map[string]struct {
		c        *ComposedRequest
		explicit int
	}{
		"grid":   {rankedSweep(t, `{"by":"tests.t.statistic"}`), 1},
		"zip":    {zip, 1},
		"random": {sampledSweep(t, "random", 6, `,"rng_seed":11`), 0},
		"lhs":    {sampledSweep(t, "lhs", 6, `,"rng_seed":11`), 0},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var wires []string
			for _, run := range []string{"Compose", "ComposeParallel", "Compose"} {
				resp, err := composeRuns(p)[run](tc.c)
				if err != nil {
					t.Fatalf("%s: %v", run, err)
				}
				if len(resp.Ranking) == 0 {
					t.Fatalf("%s: empty ranking", run)
				}
				seen := map[int]bool{}
				for _, e := range resp.Ranking {
					slot := resp.Responses[tc.explicit+e.Slot]
					if seen[e.Slot] || slot.Label != e.Label || e.Value != tStat(t, slot) {
						t.Fatalf("%s: entry %+v does not point at its slot %q", run, e, slot.Label)
					}
					seen[e.Slot] = true
					mu, ok := e.Axes["mu"].(json.Number)
					if !ok {
						t.Fatalf("%s: mu axis %T %v, want a number", run, e.Axes["mu"], e.Axes["mu"])
					}
					// The label renders mu (grid mu{{mu}}_…, sampled
					// sample=<i>_mu=<value>): the axes name the value the
					// slot ran with, in the same text.
					if name != "zip" && !strings.Contains(slot.Label, "mu"+string(mu)) && !strings.HasSuffix(slot.Label, "mu="+string(mu)) {
						t.Fatalf("%s: slot %q ran with mu %s", run, slot.Label, mu)
					}
					if name == "zip" {
						tag, _ := e.Axes["tag"].(string)
						flag, ok := e.Axes["flag"].(bool)
						if !ok || slot.Label != tag+"_"+strconv.FormatBool(flag) {
							t.Fatalf("%s: zip axes %v for slot %q", run, e.Axes, slot.Label)
						}
					}
				}
				raw, err := json.Marshal(resp.Ranking)
				if err != nil {
					t.Fatal(err)
				}
				wires = append(wires, string(raw))
			}
			if wires[0] != wires[1] || wires[0] != wires[2] {
				t.Fatalf("ranking differs across serial / parallel / rerun:\n%s\n%s\n%s", wires[0], wires[1], wires[2])
			}
			var wire []map[string]any
			if err := json.Unmarshal([]byte(wires[0]), &wire); err != nil {
				t.Fatal(err)
			}
			for _, e := range wire {
				axes, ok := e["axes"].(map[string]any)
				if _, num := axes["mu"].(float64); !ok || !num {
					t.Fatalf("wire entry %v: mu is not a JSON number", e)
				}
				if _, ok := e["slot"].(float64); !ok {
					t.Fatalf("wire entry %v carries no slot", e)
				}
				if name == "zip" {
					if _, ok := axes["tag"].(string); !ok {
						t.Fatalf("wire entry %v: tag not a string", e)
					}
					if _, ok := axes["flag"].(bool); !ok {
						t.Fatalf("wire entry %v: flag not a boolean", e)
					}
				}
			}
		})
	}
}
