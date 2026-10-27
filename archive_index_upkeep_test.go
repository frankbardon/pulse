package pulse

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Facade coverage for archive index upkeep (U51 #359, FR-34): the
// anchored-append builder path inherits AddShard's upkeep — an ordinary
// append keeps the archive index in step and warns, a re-encoding one
// reports the invalidated per-shard sidecars on CohortBuildResult — and
// an Overwrite of an indexed archive names every per-shard sidecar.

// indexedSetArchive builds sets.pulse from eight one-label rows (the
// set_u8 rung full) cut into two shards, indexed on id.
func indexedSetArchive(t *testing.T) *Pulse {
	t.Helper()
	label := func(i int) string { return string(rune('a' + i)) }
	var rows []CohortRow
	for i := range 8 {
		rows = append(rows, CohortRow{uint64(i), []string{label(i)}})
	}
	p, _ := memBuilderEngine(t)
	buildCohort(t, p, "sets.pulse", setSchema(), CohortBuilderOptions{Shards: &ShardSplit{MaxRecords: 4}}, rows)
	if _, err := p.BuildIndex(context.Background(), "sets.pulse", []string{"id"}); err != nil {
		t.Fatal(err)
	}
	return p
}

func sidecarJSON(t *testing.T, v []StaleSidecar) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestArchiveIndexUpkeep_AnchoredAppend(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("ordinary append keeps the index", func(t *testing.T) {
		p := indexedSetArchive(t)
		res := buildCohort(t, p, "sets.pulse#more.pulse", setSchema(), CohortBuilderOptions{}, []CohortRow{{uint64(42), []string{"c"}}})
		if got := warningCodes(res.Warnings); len(got) != 1 || got[0] != errors.PULSE_SHARD_INDEXES_UPDATED {
			t.Fatalf("warnings = %v, want PULSE_SHARD_INDEXES_UPDATED", got)
		}
		if len(res.InvalidatedSidecars) != 0 {
			t.Errorf("InvalidatedSidecars = %+v, want none", res.InvalidatedSidecars)
		}
		lr, err := p.Lookup(ctx, &types.LookupRequest{Cohort: &types.Cohort{Filename: "sets.pulse"}, Field: "id", Value: "42"})
		if err != nil || len(lr.Rows) != 1 {
			t.Fatalf("lookup of the appended row = %+v, %v", lr, err)
		}
	})

	t.Run("re-encoding append reports the invalidated sidecars", func(t *testing.T) {
		p := indexedSetArchive(t)
		want, err := p.InvalidatedSidecars(ctx, "sets.pulse")
		if err != nil || len(want) != 2 {
			t.Fatalf("InvalidatedSidecars before = %+v, %v; want the two shard sidecars", want, err)
		}
		res := buildCohort(t, p, "sets.pulse#more.pulse", setSchema(), CohortBuilderOptions{}, []CohortRow{{uint64(42), []string{"z"}}})
		if got := warningCodes(res.Warnings); len(got) != 1 || got[0] != errors.PULSE_SHARD_SET_WIDENED {
			t.Fatalf("warnings = %v, want only PULSE_SHARD_SET_WIDENED", got)
		}
		if sidecarJSON(t, res.InvalidatedSidecars) != sidecarJSON(t, want) {
			t.Errorf("CohortBuildResult.InvalidatedSidecars = %s\nwant %s", sidecarJSON(t, res.InvalidatedSidecars), sidecarJSON(t, want))
		}
		if _, err := p.Lookup(ctx, &types.LookupRequest{Cohort: &types.Cohort{Filename: "sets.pulse"}, Field: "id", Value: "1"}); !errors.HasCode(err, errors.PULSE_INDEX_STALE) {
			t.Errorf("lookup after the re-encoding append = %v, want PULSE_INDEX_STALE", err)
		}
	})
}

// An Overwrite of an indexed archive reports every per-shard sidecar
// the replaced archive's index named.
func TestArchiveIndexUpkeep_OverwriteNamesEveryShardSidecar(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p := indexedSetArchive(t)
	want, err := p.InvalidatedSidecars(ctx, "sets.pulse")
	if err != nil || len(want) != 2 {
		t.Fatalf("InvalidatedSidecars = %+v, %v", want, err)
	}
	res := buildCohort(t, p, "sets.pulse", setSchema(), CohortBuilderOptions{Overwrite: true, Shards: &ShardSplit{MaxRecords: 4}}, []CohortRow{{uint64(1), []string{"a"}}})
	if sidecarJSON(t, res.InvalidatedSidecars) != sidecarJSON(t, want) {
		t.Errorf("InvalidatedSidecars = %s\nwant %s", sidecarJSON(t, res.InvalidatedSidecars), sidecarJSON(t, want))
	}
}
