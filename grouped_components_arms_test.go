package pulse_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/types"
)

// Per-group aggregator Components (components.aggregations[*].groups)
// are keyed to the REQUEST, never to the execution arm: the buffered
// grouped arm, the streaming-grouped terminal flush, the serial and
// parallel shard-archive arms and the parallel-decode arm all report
// the same groups[] — and a multi-shard archive the same as its
// single-file twin.

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

// gaAnswer is the arm-invariant document: Data plus
// Components.Aggregations (Run differs by design — shard_count).
func gaAnswer(t *testing.T, resp *types.Response) string {
	t.Helper()
	if resp.Components == nil {
		t.Fatal("no Components")
	}
	return mustJSON(t, resp.Data) + mustJSON(t, resp.Components.Aggregations)
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

// TestGroupedComponents_ArmsAgree: buffered ≡ streaming-grouped ≡
// archive serial ≡ archive ShardWorkers=3, the archive answering like
// its single-file twin; the extension aggregator is merged on the
// parallel shard arm.
func TestGroupedComponents_ArmsAgree(t *testing.T) {
	fsys := afero.NewMemMapFs()
	s := paritySchema(t)
	writeParityCohort(t, fsys, "twin.pulse", s, 0, paritySmallRows)
	shards := []string{"s0.pulse", "s1.pulse", "s2.pulse"}
	for i, path := range shards {
		writeParityCohort(t, fsys, path, s, i*paritySmallRows/3, (i+1)*paritySmallRows/3)
	}
	probe := &parityProbe{}
	serial := gaOpen(t, pulse.Options{FS: fsys, ShardWorkers: 1, DecodeWorkers: 1}, probe)
	parallel := gaOpen(t, pulse.Options{FS: fsys, ShardWorkers: 3}, probe)
	if _, err := serial.CreateShardArchive(context.Background(), "archive.pulse", shards); err != nil {
		t.Fatalf("CreateShardArchive: %v", err)
	}
	exts := pulse.ServiceForTest(serial).Extensions()

	for name, grp := range gaGroupers() {
		t.Run(name, func(t *testing.T) {
			req := func(cohort string) *types.Request {
				return &types.Request{Cohort: &types.Cohort{Filename: cohort},
					Groups: []*types.Group{grp}, Aggregations: gaAggregations()}
			}
			// ATTR_PERCENTILE is non-streamable and adds no output
			// column: it forces the buffered grouped arm.
			buffered := req("twin.pulse")
			buffered.Attributes = []*types.Attribute{{Type: types.ATTR_PERCENTILE, Field: "qty", Label: "qty_pct"}}
			if processing.CanStreamRequestWithExtensions(buffered, s, exts) {
				t.Fatal("fixture: the buffered arm streams")
			}
			if !processing.CanStreamRequestWithExtensions(req("twin.pulse"), s, exts) ||
				!processing.CanMergeRequestWithExtensions(req("archive.pulse"), s, exts) {
				t.Fatal("fixture: the request neither streams nor merges; the arms would collapse")
			}

			base := gcProcess(t, serial, buffered)
			gaAssertGroups(t, base)
			want := gaAnswer(t, base)

			arms := []struct {
				name string
				p    *pulse.Pulse
				req  *types.Request
			}{
				{"streaming", serial, req("twin.pulse")},
				{"archive_serial", serial, req("archive.pulse")},
				{"archive_shard_workers_3", parallel, req("archive.pulse")},
			}
			for _, arm := range arms {
				probe.reset()
				if got := gaAnswer(t, gcProcess(t, arm.p, arm.req)); got != want {
					t.Errorf("%s answers differently from the buffered single-file arm\n got  %s\n want %s", arm.name, got, want)
				}
				if arm.name == "archive_shard_workers_3" && probe.merges.Load() == 0 {
					t.Error("the extension aggregator was never merged; the parallel shard arm did not run")
				}
			}
		})
	}
}

// TestGroupedComponents_DecodeWorkersAgree: serial ≡ DecodeWorkers=4
// over a single file above the parallel-decode threshold, extension
// slot included.
func TestGroupedComponents_DecodeWorkersAgree(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the large-cohort decode-worker arm in -short mode")
	}
	dir := t.TempDir()
	writeParityCohort(t, afero.NewOsFs(), filepath.Join(dir, "large.pulse"), paritySchema(t), 0, parityLargeRows)
	probe := &parityProbe{}
	serial := gaOpen(t, pulse.Options{DataDir: dir, DecodeWorkers: 1}, probe)
	parallel := gaOpen(t, pulse.Options{DataDir: dir, DecodeWorkers: 4}, probe)
	for name, grp := range gaGroupers() {
		t.Run(name, func(t *testing.T) {
			req := &types.Request{Cohort: &types.Cohort{Filename: "large.pulse"},
				Groups: []*types.Group{grp}, Aggregations: gaAggregations()}
			base := gcProcess(t, serial, req)
			gaAssertGroups(t, base)
			probe.reset()
			if got, want := gaAnswer(t, gcProcess(t, parallel, req)), gaAnswer(t, base); got != want {
				t.Errorf("DecodeWorkers=4 answers differently from DecodeWorkers=1\n got  %.800s\n want %.800s", got, want)
			}
			if probe.merges.Load() == 0 {
				t.Error("the extension aggregator was never merged; the parallel decode arm did not run")
			}
		})
	}
}

// TestGroupedComponents_StreamTerminalOnly: a streamed grouped run's
// chunks carry no per-group state until the terminal chunk, which
// carries the buffered response's groups[] verbatim.
func TestGroupedComponents_StreamTerminalOnly(t *testing.T) {
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
