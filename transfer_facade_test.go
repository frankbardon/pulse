package pulse

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	perrors "github.com/frankbardon/pulse/errors"
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// transferFixtures builds every cohort layout transport compression must
// carry on one memfs: a flat 0x01 cohort, a grouped 0x02 cohort, a
// grouped cohort with an elided constant, and shard archives of 0x01
// and of 0x02 shards. Returns the paths with the layout each must
// report.
func transferFixtures(t *testing.T) (afero.Fs, map[string]string) {
	t.Helper()
	ctx := context.Background()
	fs := dedupFixtureFS(t) // cohort.pulse, flat 0x01
	p, err := New(Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Dedup(ctx, "cohort.pulse", DedupOptions{Groups: dedupFixtureGroups, Out: "grouped.pulse"}); err != nil {
		t.Fatalf("grouped fixture: %v", err)
	}
	if _, err := p.Dedup(ctx, "cohort.pulse", DedupOptions{Groups: dedupFixtureGroups, ElideConstants: true, Out: "elided.pulse"}); err != nil {
		t.Fatalf("elided fixture: %v", err)
	}
	copyFile := func(src, dst string) {
		raw, err := afero.ReadFile(fs, src)
		if err != nil {
			t.Fatal(err)
		}
		if err := afero.WriteFile(fs, dst, raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	copyFile("cohort.pulse", "flat2.pulse")
	copyFile("grouped.pulse", "grouped2.pulse")
	if _, err := p.CreateShardArchive(ctx, "arch_v1.pulse", []string{"cohort.pulse", "flat2.pulse"}); err != nil {
		t.Fatalf("0x01 archive: %v", err)
	}
	if _, err := p.CreateShardArchive(ctx, "arch_v2.pulse", []string{"grouped.pulse", "grouped2.pulse"}); err != nil {
		t.Fatalf("0x02 archive: %v", err)
	}
	for _, grouped := range []string{"grouped.pulse", "elided.pulse"} {
		raw, _ := afero.ReadFile(fs, grouped)
		if raw[8] != 0x02 {
			t.Fatalf("%s header version = %#x, want 0x02", grouped, raw[8])
		}
	}
	return fs, map[string]string{
		"cohort.pulse":  pio.TransferLayoutSingleFile,
		"grouped.pulse": pio.TransferLayoutSingleFile,
		"elided.pulse":  pio.TransferLayoutSingleFile,
		"arch_v1.pulse": pio.TransferLayoutShardArchive,
		"arch_v2.pulse": pio.TransferLayoutShardArchive,
	}
}

func sha256Hex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// TestTransfer_RoundTripByteIdentical: every layout — 0x01, 0x02
// grouped, 0x02 elided, and shard archives of each — compresses and
// decompresses back to a file whose SHA-256 equals the original's, and
// both reports carry that same digest.
func TestTransfer_RoundTripByteIdentical(t *testing.T) {
	ctx := context.Background()
	fs, fixtures := transferFixtures(t)
	p, err := New(Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	for path, layout := range fixtures {
		t.Run(path, func(t *testing.T) {
			orig, _ := afero.ReadFile(fs, path)
			want := sha256Hex(orig)
			zst := path + ".zst"
			out, err := p.ExportTransfer(ctx, &pio.TransferExportJob{Source: path, Output: zst})
			if err != nil {
				t.Fatalf("ExportTransfer: %v", err)
			}
			if out.SHA256 != want || out.Layout != layout || out.CohortBytes != int64(len(orig)) || out.Level != pio.DefaultTransferLevel {
				t.Fatalf("export report = %+v, want sha %s layout %s bytes %d", out, want, layout, len(orig))
			}
			if out.Ratio <= 1 {
				t.Fatalf("ratio %.2f: a repetitive cohort must compress", out.Ratio)
			}
			in, err := p.ImportTransfer(ctx, &pio.TransferImportJob{Source: zst, Output: "rt_" + path})
			if err != nil {
				t.Fatalf("ImportTransfer: %v", err)
			}
			got, _ := afero.ReadFile(fs, "rt_"+path)
			if sha256Hex(got) != want || in.SHA256 != want || in.Layout != layout || in.CompressedBytes != out.CompressedBytes {
				t.Fatalf("round trip: sha %s (report %s), want %s; report %+v", sha256Hex(got), in.SHA256, want, in)
			}
		})
	}
}

// TestTransfer_CompressedCohortRefusedEverywhere: a transfer artifact
// sitting at a cohort path is refused by EVERY facade read path with
// PULSE_COHORT_COMPRESSED somewhere in the chain — never a garbled
// ENCODING_INVALID alone — and by Open, CountRecords and Inspect as the
// top-level code, so `pulse errors lookup` names the fix.
func TestTransfer_CompressedCohortRefusedEverywhere(t *testing.T) {
	ctx := context.Background()
	fs := dedupFixtureFS(t)
	p, err := New(Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.ExportTransfer(ctx, &pio.TransferExportJob{Source: "cohort.pulse", Output: "a.zst"}); err != nil {
		t.Fatal(err)
	}
	raw, _ := afero.ReadFile(fs, "a.zst")
	if err := afero.WriteFile(fs, "cohort.pulse", raw, 0o644); err != nil {
		t.Fatal(err)
	}
	// Inspect / Predict report through an envelope rather than a Go
	// error; their envelope codes are asserted below.
	envelopeProbes := map[string]bool{"Inspect": true, "InspectEnvelope": true, "Predict": true}
	for _, pr := range formatProbes(ctx) {
		if envelopeProbes[pr.name] {
			continue
		}
		t.Run(pr.name, func(t *testing.T) {
			_, err := pr.run(p, fs)
			if !perrors.HasCode(err, perrors.PULSE_COHORT_COMPRESSED) {
				t.Fatalf("%s: err = %v, want PULSE_COHORT_COMPRESSED in the chain", pr.name, err)
			}
		})
	}
	topLevel := func(name string, err error) {
		t.Helper()
		ce, ok := err.(*perrors.CodedError)
		if !ok || ce.Code != perrors.PULSE_COHORT_COMPRESSED {
			t.Fatalf("%s: top-level err = %v, want PULSE_COHORT_COMPRESSED", name, err)
		}
	}
	_, err = p.Open(ctx, "cohort.pulse")
	topLevel("Open", err)
	_, err = p.CountRecords(ctx, "cohort.pulse")
	topLevel("CountRecords", err)
	env, err := p.InspectEnvelope(ctx, "cohort.pulse", &descriptor.InspectOptions{})
	if err != nil || len(env.Errors) == 0 || env.Errors[0].Code != string(perrors.PULSE_COHORT_COMPRESSED) {
		t.Fatalf("InspectEnvelope: err %v errors %+v, want PULSE_COHORT_COMPRESSED", err, env)
	}
	penv := descriptor.PredictFromBytes(raw, &Request{Cohort: &types.Cohort{Filename: "cohort.pulse"}}, nil)
	if len(penv.Errors) == 0 || penv.Errors[0].Code != string(perrors.PULSE_COHORT_COMPRESSED) {
		t.Fatalf("PredictFromBytes errors = %+v, want PULSE_COHORT_COMPRESSED", penv.Errors)
	}
}

// TestTransfer_UncompressedPathUnchanged: compression is opt-in — the
// source cohort is left byte-identical by an export, and a decompressed
// cohort serves every read path exactly as the original does.
func TestTransfer_UncompressedPathUnchanged(t *testing.T) {
	ctx := context.Background()
	fs := dedupFixtureFS(t)
	before, _ := afero.ReadFile(fs, "cohort.pulse")
	p, err := New(Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.ExportTransfer(ctx, &pio.TransferExportJob{Source: "cohort.pulse", Output: "c.zst", Level: 19}); err != nil {
		t.Fatal(err)
	}
	after, _ := afero.ReadFile(fs, "cohort.pulse")
	if !bytes.Equal(before, after) {
		t.Fatal("export transfer modified its source cohort")
	}
	recv := afero.NewMemMapFs()
	zraw, _ := afero.ReadFile(fs, "c.zst")
	_ = afero.WriteFile(recv, "c.zst", zraw, 0o644)
	rp, err := New(Options{FS: recv})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rp.ImportTransfer(ctx, &pio.TransferImportJob{Source: "c.zst", Output: "cohort.pulse"}); err != nil {
		t.Fatal(err)
	}
	for _, pr := range formatProbes(ctx) {
		t.Run(pr.name, func(t *testing.T) {
			var out [2]string
			for i, fsys := range []afero.Fs{fs, recv} {
				pp, err := New(Options{FS: fsys})
				if err != nil {
					t.Fatal(err)
				}
				got, err := pr.run(pp, fsys)
				if err != nil {
					t.Fatalf("%s on side %d: %v", pr.name, i, err)
				}
				out[i] = mustJSON(t, pr.name, got)
			}
			if out[0] != out[1] {
				t.Fatalf("%s differs between sender and receiver", pr.name)
			}
		})
	}
}
