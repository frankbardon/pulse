package pulse

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// compose_sweep_trace_test.go pins an adaptive sweep's round trace at
// the facade: every ranking entry's `round` and ComposedResponse
// `sweep_trace` under modes de and bo, serial ≡ parallel, `top` never
// touching the trace, and neither key under a non-adaptive mode.

// wantSpans are the inclusive slot spans of rounds of the given sizes.
func wantSpans(sizes ...int) [][2]int {
	var out [][2]int
	next := 0
	for _, n := range sizes {
		out = append(out, [2]int{next, next + n - 1})
		next += n
	}
	return out
}

// checkTrace asserts resp's round trace against the round spans: one
// entry per round in order, nothing penalised (every slot of the
// fixture ranks), each round's best the first ranked entry of its
// round, best_so_far the first ranked entry of rounds 0..r, and every
// ranking entry's round the span holding its slot.
func checkTrace(t *testing.T, name string, resp *ComposedResponse, spans [][2]int, order types.SweepRankOrder) {
	t.Helper()
	roundOf := func(slot int) int {
		for k, s := range spans {
			if slot >= s[0] && slot <= s[1] {
				return k
			}
		}
		return -1
	}
	for _, e := range resp.Ranking {
		if e.Round == nil || *e.Round != roundOf(e.Slot) {
			t.Fatalf("%s: entry %+v round %v, want %d", name, e, e.Round, roundOf(e.Slot))
		}
	}
	if len(resp.SweepTrace) != len(spans) {
		t.Fatalf("%s: trace %+v, want %d rounds", name, resp.SweepTrace, len(spans))
	}
	better := func(a, b float64) bool {
		if order == types.SweepRankDesc {
			return a > b
		}
		return a < b
	}
	for k, tr := range resp.SweepTrace {
		if tr.Round != k || tr.Slots != spans[k] || tr.Evaluated != spans[k][1]-spans[k][0]+1 || tr.Penalised != 0 {
			t.Fatalf("%s: round %d %+v, want span %v", name, k, tr, spans[k])
		}
		var best, sofar *types.RankEntry
		for i := range resp.Ranking {
			e := &resp.Ranking[i]
			if r := roundOf(e.Slot); r == k && best == nil {
				best = e
			}
			if r := roundOf(e.Slot); r <= k && sofar == nil {
				sofar = e
			}
		}
		if tr.Best == nil || *tr.Best != best.Value || tr.BestSoFar == nil || *tr.BestSoFar != sofar.Value || tr.BestLabel != sofar.Label {
			t.Fatalf("%s: round %d %+v, want best %v so far %+v", name, k, tr, best.Value, sofar)
		}
		if k > 0 && better(*resp.SweepTrace[k-1].BestSoFar, *tr.BestSoFar) {
			t.Fatalf("%s: best_so_far regressed at round %d: %+v", name, k, resp.SweepTrace)
		}
	}
}

// TestComposeSweepTrace_DEAndBO: mode de (generations of 4, 4, 2) and
// mode bo (rounds of 3, 2, 2) set `round` on every ranking entry —
// round 0 on the wire too — and carry one `sweep_trace` entry per
// round; serial and parallel give byte-identical ranking and trace.
func TestComposeSweepTrace_DEAndBO(t *testing.T) {
	t.Parallel()

	p := sweepPulse(t, Options{})
	cases := map[string]struct {
		c     func() *ComposedRequest
		spans [][2]int
	}{
		"de": {func() *ComposedRequest { return deSweep(t, 10, `,"de":{"population":4,"f":0.7,"cr":0.5}`) }, wantSpans(4, 4, 2)},
		"bo": {func() *ComposedRequest {
			return boSweep(t, 7, `,"bo":{"init":"random","n_init":3,"batch":2}`)
		}, wantSpans(3, 2, 2)},
	}
	for mode, tc := range cases {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			var wires []string
			for _, run := range []string{"Compose", "ComposeParallel"} {
				resp, err := composeRuns(p)[run](tc.c())
				if err != nil {
					t.Fatalf("%s: %v", run, err)
				}
				checkTrace(t, run, resp, tc.spans, types.SweepRankDesc)
				raw, err := json.Marshal(resp)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Contains(raw, []byte(`"round":0,`)) || !bytes.Contains(raw, []byte(`"sweep_trace":[{"round":0,"slots":[0,`)) {
					t.Fatalf("%s: wire lacks round 0 / sweep_trace: %s", run, raw)
				}
				w, err := json.Marshal(struct {
					R []types.RankEntry       `json:"r"`
					T []types.SweepTraceEntry `json:"t"`
				}{resp.Ranking, resp.SweepTrace})
				if err != nil {
					t.Fatal(err)
				}
				wires = append(wires, string(w))
			}
			if wires[0] != wires[1] {
				t.Fatalf("serial and parallel differ:\n%s\n%s", wires[0], wires[1])
			}
		})
	}
}

// TestComposeSweepTrace_TopKeepsTrace: rank.top trims the ranking
// (each kept entry still carries its round) but never the trace, which
// is read over the full ranking.
func TestComposeSweepTrace_TopKeepsTrace(t *testing.T) {
	t.Parallel()

	p := sweepPulse(t, Options{})
	// The seed is pinned: a derived one reads the whole spec, top too.
	const knobs = `,"rng_seed":7,"de":{"population":4,"f":0.7,"cr":0.5}`
	full, err := p.Compose(t.Context(), deSweep(t, 10, knobs))
	if err != nil {
		t.Fatal(err)
	}
	c := deSweep(t, 10, knobs)
	top := 2
	c.Sweep.Rank.Top = &top
	trimmed, err := p.Compose(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	if len(trimmed.Ranking) != 2 || !reflect.DeepEqual(trimmed.Ranking, full.Ranking[:2]) {
		t.Fatalf("top ranking %+v, want the full ranking's first two %+v", trimmed.Ranking, full.Ranking[:2])
	}
	if len(full.SweepTrace) != 3 || !reflect.DeepEqual(trimmed.SweepTrace, full.SweepTrace) {
		t.Fatalf("top changed the trace:\n%+v\n%+v", trimmed.SweepTrace, full.SweepTrace)
	}
}

// TestComposeSweepTrace_NonAdaptiveAbsent: grid, zip, random and lhs
// responses carry no `round` on a ranking entry and no `sweep_trace`,
// on both orchestrators.
func TestComposeSweepTrace_NonAdaptiveAbsent(t *testing.T) {
	t.Parallel()

	p := sweepPulse(t, Options{})
	zip := decodeComposedJSON(t, `{"sweep":{"mode":"zip","axes":[{"name":"mu","values":[30,10.5,50]}],
		"label":"mu{{mu}}","request":`+rankSweepBody+`,"rank":{"by":"tests.t.statistic","order":"desc"}}}`)
	cases := map[string]*ComposedRequest{
		"grid":   rankedSweep(t, `{"by":"tests.t.statistic","profile":true}`),
		"zip":    zip,
		"random": sampledSweep(t, "random", 6, `,"rng_seed":11`),
		"lhs":    sampledSweep(t, "lhs", 6, `,"rng_seed":11`),
	}
	for name, c := range cases {
		for run, fn := range composeRuns(p) {
			resp, err := fn(c)
			if err != nil {
				t.Fatalf("%s %s: %v", name, run, err)
			}
			if len(resp.Ranking) == 0 || resp.SweepTrace != nil {
				t.Fatalf("%s %s: ranking %d, trace %+v", name, run, len(resp.Ranking), resp.SweepTrace)
			}
			raw, err := json.Marshal(resp)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(raw), `"round"`) || strings.Contains(string(raw), `"sweep_trace"`) {
				t.Fatalf("%s %s: non-adaptive wire carries the trace: %s", name, run, raw)
			}
		}
	}
}

// TestComposeSweepTrace_StreamRecord: the streamed run's final ranking
// record carries the buffered response's sweep_trace, byte for byte.
func TestComposeSweepTrace_StreamRecord(t *testing.T) {
	t.Parallel()

	p := sweepPulse(t, Options{})
	c := boSweep(t, 7, `,"bo":{"init":"random","n_init":3,"batch":2}`)
	resp, err := p.Compose(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := WriteComposeStream(&buf, c, resp, ComposeStreamOptions{}); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	var last struct {
		Ranking json.RawMessage `json:"ranking"`
		Trace   json.RawMessage `json:"sweep_trace"`
	}
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &last); err != nil {
		t.Fatal(err)
	}
	want, err := json.Marshal(resp.SweepTrace)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.SweepTrace) != 3 || string(last.Trace) != string(want) {
		t.Fatalf("stream trace %s, want %s", last.Trace, want)
	}
}

// TestComposeSweepTrace_OutsideReturn: a Compose-level `return` shapes
// the top-level overlays only — sweep_trace (like ranking) stays on the
// wire at full precision.
func TestComposeSweepTrace_OutsideReturn(t *testing.T) {
	t.Parallel()

	p := sweepPulse(t, Options{})
	c := deSweep(t, 8, `,"de":{"population":4}`)
	plain, err := p.Compose(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	c.Return = &types.Return{Preset: "minimal", Precision: 1}
	shaped, err := p.Compose(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	want, err := json.Marshal(plain.SweepTrace)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(shaped)
	if err != nil {
		t.Fatal(err)
	}
	if shaped.Returned == nil || len(plain.SweepTrace) != 2 || !bytes.Contains(raw, append([]byte(`"sweep_trace":`), want...)) {
		t.Fatalf("shaped wire lacks the full-precision trace %s:\n%s", want, raw)
	}
}
