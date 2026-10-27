package pulse_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/types"
)

// GROUP_KMEANS labels are a function of the member tuples alone: the
// same rows answer identically on a single file, a shard archive in
// any shard order, ShardWorkers 1 or 3 and DecodeWorkers 1 or 4 (the
// grouper is non-mergeable, so every arm runs it serially over the
// whole cohort — the canonical point set makes row order irrelevant).

func kmReq(cohort string) *types.Request {
	return &types.Request{
		Cohort: &types.Cohort{Filename: cohort},
		Groups: []*types.Group{{Type: types.GROUP_KMEANS, Fields: []string{"score", "qty"},
			Params: json.RawMessage(`{"k":4,"seed":17}`)}},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "score", Label: "n"},
			{Type: types.AGG_SUM, Field: "qty", Label: "q"},
		},
	}
}

// kmAnswer is the order-free answer: the Data rows and the grouper
// components (sums are of integers / exact halves, so any arm's
// summation order reproduces them bit for bit).
func kmAnswer(t *testing.T, p *pulse.Pulse, cohort string) string {
	t.Helper()
	resp, err := p.Process(context.Background(), kmReq(cohort))
	if err != nil {
		t.Fatalf("Process(%s): %v", cohort, err)
	}
	if resp.Components == nil || len(resp.Components.Groupers) != 1 || len(resp.Data) != 4 ||
		resp.Components.Groupers[0].NNull == 0 {
		t.Fatalf("%s: want 4 clusters, grouper components and null-score rows in n_null; got %d rows", cohort, len(resp.Data))
	}
	b, _ := json.Marshal(struct {
		Data    any
		Grouper any
	}{resp.Data, resp.Components.Groupers[0]})
	return string(b)
}

func TestGroupKMeans_InvariantAcrossArmsAndShardOrder(t *testing.T) {
	fsys := afero.NewMemMapFs()
	s := paritySchema(t)
	writeParityCohort(t, fsys, "twin.pulse", s, 0, paritySmallRows)
	shards := []string{"s0.pulse", "s1.pulse", "s2.pulse"}
	for i, path := range shards {
		writeParityCohort(t, fsys, path, s, i*paritySmallRows/3, (i+1)*paritySmallRows/3)
	}
	serial, err := pulse.New(pulse.Options{FS: fsys, ShardWorkers: 1, DecodeWorkers: 1})
	if err != nil {
		t.Fatal(err)
	}
	parallel, err := pulse.New(pulse.Options{FS: fsys, ShardWorkers: 3, DecodeWorkers: 4})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := serial.CreateShardArchive(ctx, "archive.pulse", shards); err != nil {
		t.Fatal(err)
	}
	if _, err := serial.CreateShardArchive(ctx, "reversed.pulse", []string{"s2.pulse", "s0.pulse", "s1.pulse"}); err != nil {
		t.Fatal(err)
	}
	want := kmAnswer(t, serial, "twin.pulse")
	for _, arm := range []struct {
		name   string
		p      *pulse.Pulse
		cohort string
	}{
		{"twin, workers", parallel, "twin.pulse"},
		{"archive serial", serial, "archive.pulse"},
		{"archive shard workers 3", parallel, "archive.pulse"},
		{"reversed shard order", serial, "reversed.pulse"},
		{"reversed shard order, workers", parallel, "reversed.pulse"},
	} {
		if got := kmAnswer(t, arm.p, arm.cohort); got != want {
			t.Errorf("%s answers differently:\n got  %.600s\n want %.600s", arm.name, got, want)
		}
	}
}
