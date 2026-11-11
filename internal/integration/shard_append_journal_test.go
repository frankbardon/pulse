package integration_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// shard_append_journal_test.go pins the facade side of the append
// journal (U73 E2-S2): with `<archive>.append` beside a shard archive
// (an interrupted in-place `shard add`), every path-based surface —
// Open, Process, Predict, Inspect, CountRecords, Lookup, Export — refuses
// it with the same PULSE_SHARD_APPEND_INCOMPLETE, predict agreeing with
// the runtime; CohortArtifacts lists the journal (it must travel with
// the archive); RepairShardArchive restores the pre-append bytes and
// every surface reads again. PredictBytes / InspectBytes take bytes, not
// a path, so they cannot see a journal — the documented limit.
func TestShardAppendJournal_EverySurfaceRefusesThenRepairs(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fs := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	var shards []string
	for stem, body := range map[string]string{"j_a": "cat,n\na,1\nb,2\n", "j_b": "cat,n\nb,3\nc,4\n"} {
		if err := afero.WriteFile(fs, stem+".csv", []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		res, err := p.ImportFile(ctx, pulse.ImportSpec{SourcePath: stem + ".csv"})
		if err != nil {
			t.Fatalf("ImportFile %s: %v", stem, err)
		}
		shards = append(shards, res.Path)
	}
	const archive = "journal_archive.pulse"
	if _, err := p.CreateShardArchive(ctx, archive, shards); err != nil {
		t.Fatalf("CreateShardArchive: %v", err)
	}
	before, err := afero.ReadFile(fs, archive)
	if err != nil {
		t.Fatal(err)
	}

	// The state a crash mid-append leaves: the journal, then torn bytes.
	j, err := encx.NewAppendJournal(bytes.NewReader(before), int64(len(before)), "j_c.pulse")
	if err != nil {
		t.Fatal(err)
	}
	if err := encx.WriteAppendJournal(fs, archive, j); err != nil {
		t.Fatal(err)
	}
	torn := append(append([]byte{}, before...), bytes.Repeat([]byte{0x50, 0x4b}, 200)...)
	if err := afero.WriteFile(fs, archive, torn, 0o644); err != nil {
		t.Fatal(err)
	}

	req := &types.Request{Cohort: &types.Cohort{Filename: archive}, Aggregations: countAgg()}
	surfaces := map[string]func() error{
		"Open": func() error { _, err := p.Open(ctx, archive); return err },
		"Process": func() error {
			_, err := p.Process(ctx, req)
			return err
		},
		"Predict": func() error { _, err := p.Predict(ctx, req); return err },
		"InspectEnvelope": func() error {
			_, err := p.InspectEnvelope(ctx, archive, nil)
			return err
		},
		"CountRecords": func() error { _, err := p.CountRecords(ctx, archive); return err },
		"Lookup": func() error {
			_, err := p.Lookup(ctx, &pulse.LookupRequest{Cohort: &types.Cohort{Filename: archive}, Field: "cat", Value: "a"})
			return err
		},
		"Export": func() error {
			w, err := pio.NewWriterToBuffer(pio.FormatCSV, pio.WriterOptions{})
			if err != nil {
				return err
			}
			job := pio.NewExportJob(archive, w)
			job.FS = fs
			_, err = p.Export(ctx, job)
			return err
		},
		"VerifyShardArchive": func() error { _, err := p.VerifyShardArchive(ctx, archive); return err },
	}
	for name, call := range surfaces {
		t.Run(name, func(t *testing.T) {
			ce := requireCode(t, call(), errors.PULSE_SHARD_APPEND_INCOMPLETE)
			if ce.Details["journal"] != archive+".append" {
				t.Errorf("details = %v", ce.Details)
			}
		})
	}

	arts, err := p.CohortArtifacts(ctx, archive)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, a := range arts {
		found = found || a == archive+".append"
	}
	if !found {
		t.Errorf("CohortArtifacts = %v, want the journal listed", arts)
	}

	res, err := p.RepairShardArchive(ctx, archive)
	if err != nil {
		t.Fatalf("RepairShardArchive: %v", err)
	}
	if !res.Repaired || res.Shard != "j_c.pulse" {
		t.Errorf("repair = %+v", res)
	}
	after, _ := afero.ReadFile(fs, archive)
	if !bytes.Equal(after, before) {
		t.Fatal("the repaired archive differs from its pre-append bytes")
	}
	if n, err := p.CountRecords(ctx, archive); err != nil || n != 4 {
		t.Errorf("CountRecords after repair = %d, %v; want 4", n, err)
	}
	if pr, err := p.Predict(ctx, req); err != nil || !pr.Valid {
		t.Errorf("Predict after repair = %+v, %v", pr, err)
	}
}
