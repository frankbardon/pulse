package pulse

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	perrors "github.com/frankbardon/pulse/errors"
	pio "github.com/frankbardon/pulse/io"
	"github.com/spf13/afero"
)

// dedupFixtureFS writes a synthetic parent/child cohort FLAT to
// "cohort.pulse" on a fresh memfs: groupedTwinFS's columns plus an f64
// parent column, so the parent block is wider than the 4-byte index and
// passes the viability gate's width floor.
func dedupFixtureFS(t *testing.T) afero.Fs {
	t.Helper()
	cols := []string{"id", "region", "parent_code", "parent_weight", "amount", "score", "source", "parent_value"}
	var rows [][]string
	for i := 0; i < 360; i++ {
		p := i / 12
		weight := fmt.Sprintf("%d", 10+p%9)
		if p%5 == 0 {
			weight = ""
		}
		rows = append(rows, []string{
			fmt.Sprint(i + 1),
			[]string{"north", "south", "east", "west"}[p%4],
			fmt.Sprint(1000 + p),
			weight,
			fmt.Sprintf("%d.%02d", 10+i%37, i%100),
			fmt.Sprint((i * 7) % 11),
			"batch-a",
			fmt.Sprintf("%d.5", 100+p),
		})
	}
	fs := afero.NewMemMapFs()
	createTestPulseFile(t, fs, "cohort.pulse", cols, rows)
	return fs
}

var dedupFixtureGroups = []pio.GroupDecl{{Key: []string{"parent_code"}, Members: []string{"region", "parent_weight", "parent_value"}}}

// TestDedup_RoundTrip: retro-dedup a cohort in place, then run every
// facade read path — Process (the same request), streaming, sample,
// facet, profile, export, lookup, filter-to-file, inspect, predict,
// count — against the original and the converted file: the output is
// byte-identical. The converted cohort really is grouped and smaller.
func TestDedup_RoundTrip(t *testing.T) {
	ctx := context.Background()
	orig := dedupFixtureFS(t)
	conv := dedupFixtureFS(t)
	p, err := New(Options{FS: conv})
	if err != nil {
		t.Fatal(err)
	}
	res, err := p.Dedup(ctx, "cohort.pulse", DedupOptions{Groups: dedupFixtureGroups, ElideConstants: true})
	if err != nil {
		t.Fatalf("Dedup: %v", err)
	}
	if !res.Rewritten || !res.InPlace || res.FormatVersionAfter != 2 || res.Records != 360 ||
		res.BytesAfter >= res.BytesBefore || len(res.ElidedConstants) != 1 || res.Groups[0].EntryCount != 30 {
		t.Fatalf("report = %+v", res.DedupReport)
	}
	for _, pr := range formatProbes(ctx) {
		t.Run(pr.name, func(t *testing.T) {
			var out [2]string
			for i, fsys := range []afero.Fs{orig, conv} {
				p, err := New(Options{FS: fsys})
				if err != nil {
					t.Fatal(err)
				}
				got, err := pr.run(p, fsys)
				if err != nil {
					t.Fatalf("%s on cohort %d: %v", pr.name, i+1, err)
				}
				out[i] = mustJSON(t, pr.name, got)
			}
			if out[0] != out[1] {
				t.Fatalf("%s differs between the original and the retro-deduped cohort:\n orig: %s\n dedup: %s", pr.name, out[0], out[1])
			}
		})
	}
}

// TestDedup_ShardArchiveRefused: a shard archive, and an anchored shard
// inside one, are refused with SERVICE_VALIDATION before a byte is
// written — explicit, never a silently wrong archive.
func TestDedup_ShardArchiveRefused(t *testing.T) {
	ctx := context.Background()
	fs := dedupFixtureFS(t)
	raw, _ := afero.ReadFile(fs, "cohort.pulse")
	if err := afero.WriteFile(fs, "s2.pulse", raw, 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := New(Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.CreateShardArchive(ctx, "arch.pulse", []string{"cohort.pulse", "s2.pulse"}); err != nil {
		t.Fatal(err)
	}
	before, _ := afero.ReadFile(fs, "arch.pulse")
	for _, path := range []string{"arch.pulse", "arch.pulse#s2.pulse"} {
		_, err := p.Dedup(ctx, path, DedupOptions{Groups: dedupFixtureGroups})
		if !perrors.HasCode(err, perrors.SERVICE_VALIDATION) {
			t.Fatalf("%s: err = %v, want SERVICE_VALIDATION", path, err)
		}
	}
	after, _ := afero.ReadFile(fs, "arch.pulse")
	if !bytes.Equal(before, after) {
		t.Fatal("refused dedup changed the archive")
	}
	if _, err := p.Dedup(ctx, "missing.pulse", DedupOptions{Groups: dedupFixtureGroups}); !perrors.HasCode(err, perrors.SERVICE_RESOURCE) {
		t.Fatalf("missing cohort: err = %v, want SERVICE_RESOURCE", err)
	}
}

// TestDedup_ReportsInvalidatedSidecars: an in-place dedup names the
// point-lookup index beside the cohort with its rebuild command and
// rebuilds nothing — the index is left stale for the caller. An --out
// dedup leaves the source (and its sidecars) alone and reports none.
func TestDedup_ReportsInvalidatedSidecars(t *testing.T) {
	ctx := context.Background()
	fs := dedupFixtureFS(t)
	p, err := New(Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.BuildIndex(ctx, "cohort.pulse", []string{"id"}); err != nil {
		t.Fatal(err)
	}
	res, err := p.Dedup(ctx, "cohort.pulse", DedupOptions{Groups: dedupFixtureGroups, Out: "copy.pulse"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.InvalidatedSidecars) != 0 {
		t.Fatalf("--out dedup reported sidecars %+v", res.InvalidatedSidecars)
	}
	res, err = p.Dedup(ctx, "cohort.pulse", DedupOptions{Groups: dedupFixtureGroups})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.InvalidatedSidecars) != 1 || res.InvalidatedSidecars[0].Kind != SidecarKindPointLookupIndex ||
		res.InvalidatedSidecars[0].Rebuild == "" || len(res.Warnings()) != 0 {
		t.Fatalf("sidecars = %+v, warnings %v", res.InvalidatedSidecars, res.Warnings())
	}
	v, err := p.VerifyIndex(ctx, "cohort.pulse", []string{"id"})
	if err != nil {
		t.Fatal(err)
	}
	if v.Fresh {
		t.Fatal("the index was rebuilt or still reads fresh after an in-place dedup")
	}
}
