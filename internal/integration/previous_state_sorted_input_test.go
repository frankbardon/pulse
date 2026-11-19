package integration_test

import (
	"context"
	"fmt"
	"math/rand"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
)

// TestPreviousState_SortedInputMatchesShuffled: ATTR_PREVIOUS_STATE's
// sorted-input path (each subject's records contiguous, non-decreasing
// in order — prevModeRows as written, and an archive whose shards hold
// consecutive subjects) answers every matrix request with the same whole
// document — data, crosstab, the AMBIGUOUS_TIE warning and Components —
// as the bucket-and-sort path over the same rows shuffled, in a single
// file and in the wave-split archive (prevModeShards).
func TestPreviousState_SortedInputMatchesShuffled(t *testing.T) {
	t.Parallel()

	fs := afero.NewMemMapFs()
	p, _ := prevModeEngine(t, fs, pulse.Options{})
	panelCohort(t, p, "sorted.pulse", prevModeRows)

	shuffled := make([][3]any, len(prevModeRows))
	for i, j := range rand.New(rand.NewSource(485)).Perm(len(prevModeRows)) {
		shuffled[i] = prevModeRows[j]
	}
	panelCohort(t, p, "shuffled.pulse", shuffled)

	archive := func(name string, parts [][][3]any) {
		var shards []string
		for i, rows := range parts {
			shard := fmt.Sprintf("%s_s%d.pulse", name, i)
			panelCohort(t, p, shard, rows)
			shards = append(shards, shard)
		}
		if _, err := p.CreateShardArchive(context.Background(), name+".pulse", shards); err != nil {
			t.Fatal(err)
		}
	}
	// Consecutive subjects per shard: 1-3, 4-6, 7-9 (the rows are grouped
	// by subject in that order).
	var bySubject [3][][3]any
	for _, r := range prevModeRows {
		id := r[0].(uint64)
		bySubject[(id-1)/3] = append(bySubject[(id-1)/3], r)
	}
	archive("sorted_archive", bySubject[:])
	archive("waves_archive", prevModeShards())

	for name := range prevModeRequests("") {
		want := prevModeRun(t, p, prevModeRequests("sorted.pulse")[name](), true)
		for _, cohort := range []string{"shuffled.pulse", "sorted_archive.pulse", "waves_archive.pulse"} {
			if got := prevModeRun(t, p, prevModeRequests(cohort)[name](), true); got != want {
				t.Errorf("%s over %s\n%s\nsorted single file\n%s", name, cohort, got, want)
			}
		}
	}
}
