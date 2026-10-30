package integration_test

import (
	"context"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/types"
)

// The grouped-components family's slots, groupers and guards: per-group
// aggregator Components (components.aggregations[*].groups) are keyed
// to the REQUEST, never to the execution arm — the worker-invariance
// gate over every arm is grouped_components_invariance_test.go (the
// shared harness); the stream's terminal-only rule is below.

// gaExtSum is a Streamable + Mergeable extension aggregator, so it
// rides the streaming and both parallel arms (its merges are probed).
const gaExtSum types.AggregationType = "AGG_ACME_GA_SUM"

// gaAggregations: a built-in over a nullable field (non-zero n_null), a
// floor-only built-in, a probability-weighted slot (sum_weights, n_eff,
// n_weight_invalid — qty is 0 on some rows) and the extension
// aggregator. Every operator figure is an exact sum of halves, so the
// archive and its twin agree bit for bit (no float-tolerance needed).
func gaAggregations() []*types.Aggregation {
	return []*types.Aggregation{
		{Type: types.AGG_SUM, Field: "score", Label: "s"},
		{Type: types.AGG_COUNT, Field: "score", Label: "n"},
		{Type: types.AGG_SUM, Field: "score", Label: "ws",
			Weight: types.SlotWeightOf(types.WeightSpec{Field: "qty", Kind: types.WeightKindProbability})},
		{Type: gaExtSum, Field: "score", Label: "x"},
	}
}

func gaGroupers() map[string]*types.Group {
	return map[string]*types.Group{
		"category": {Type: types.GROUP_CATEGORY, Field: "region"},
		// A fan-out grouper: one record feeds several buckets, on every
		// arm (KeysForRow streaming, Group buffered).
		"set_per_element": {Type: types.GROUP_SET_PER_ELEMENT, Field: "tags"},
	}
}

// gaOpen opens a Pulse over fsys with the probed extension registered.
func gaOpen(t *testing.T, opts pulse.Options, probe *parityProbe) *pulse.Pulse {
	t.Helper()
	opts.Extensions = pulse.Extensions{Aggregators: []pulse.AggregatorRegistration{
		paritySumRegistration(probe, gaExtSum, true),
	}}
	p, err := pulse.New(opts)
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	return p
}

// gaAssertGroups fails unless resp carries a non-degenerate groups[] on
// every slot — so arm parity is not parity over an empty block.
func gaAssertGroups(t *testing.T, resp *types.Response) {
	t.Helper()
	aggs := resp.Components.Aggregations
	if len(aggs) != len(gaAggregations()) {
		t.Fatalf("%d aggregation entries, want %d", len(aggs), len(gaAggregations()))
	}
	for _, a := range aggs {
		if len(a.Groups) == 0 || len(a.Groups) != len(resp.Data) {
			t.Fatalf("slot %s: %d groups for %d Data rows", a.Label, len(a.Groups), len(resp.Data))
		}
	}
	nullSeen := false
	for _, g := range aggs[0].Groups {
		nullSeen = nullSeen || g.NNull > 0
	}
	if !nullSeen || aggs[0].Groups[0].Operator == nil {
		t.Errorf("fixture: built-in groups lack n_null / operator: %+v", aggs[0].Groups)
	}
	if g := aggs[2].Groups[0]; g.SumWeights == nil || g.NEff == nil || g.NWeightInvalid == nil {
		t.Errorf("fixture: weighted group lacks the weighted floor: %+v", g)
	}
	if g := aggs[3].Groups[0]; g.Operator["sum"] == nil {
		t.Errorf("fixture: extension group lacks its operator map: %+v", g)
	}
}

// TestGroupedComponents_StreamTerminalOnly: a streamed grouped run's
// chunks carry no per-group state until the terminal chunk, which
// carries the buffered response's groups[] verbatim.
func TestGroupedComponents_StreamTerminalOnly(t *testing.T) {
	t.Parallel()

	fsys := afero.NewMemMapFs()
	writeParityCohort(t, fsys, "twin.pulse", paritySchema(t), 0, paritySmallRows)
	p := gaOpen(t, pulse.Options{FS: fsys}, &parityProbe{})
	req := &types.Request{Cohort: &types.Cohort{Filename: "twin.pulse"},
		Groups: []*types.Group{gaGroupers()["category"]}, Aggregations: gaAggregations()}
	resp := gcProcess(t, p, req)
	gaAssertGroups(t, resp)

	sr, err := p.ProcessStreamResult(context.Background(), req)
	if err != nil {
		t.Fatalf("ProcessStreamResult: %v", err)
	}
	var chunks []pulse.StreamChunk[pulse.Row]
	for c := range sr.Chunks {
		chunks = append(chunks, c)
	}
	if done := <-sr.Done; done.Status != pulse.StreamCompleted {
		t.Fatalf("stream status = %v, err = %v", done.Status, done.Error)
	}
	if len(chunks) < 2 {
		t.Fatalf("%d chunks; need a non-terminal one to prove anything", len(chunks))
	}
	for i, c := range chunks[:len(chunks)-1] {
		if c.Components == nil || len(c.Components.Aggregations) == 0 {
			t.Fatalf("chunk %d: no slot-level aggregation floor", i)
		}
		for _, a := range c.Components.Aggregations {
			if a.Groups != nil {
				t.Errorf("chunk %d slot %s: mid-stream per-group state %s", i, a.Label, mustJSON(t, a.Groups))
			}
		}
	}
	last := chunks[len(chunks)-1]
	if got, want := mustJSON(t, last.Components.Aggregations), mustJSON(t, resp.Components.Aggregations); got != want {
		t.Errorf("terminal chunk groups differ from the buffered response\n got  %s\n want %s", got, want)
	}
}
