package pulse

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	perrors "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/io/csv"
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/synth"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// Fixed-stride regression suite, facade half. The parity tests
// (TestGroupedCohort_FacadeParity, TestElidedConstants_FacadeParity)
// compare a deduped cohort with its 0x01 twin — which cannot catch a
// stride consumer that is wrong on BOTH arms the same way. Every test
// here asserts the ABSOLUTE answer (the known record count, the row the
// key names) on the 0x01 cohort and on each deduped twin, so a site that
// strides by the wrong width fails on its own.

// strideTwin is one 0x01 cohort and its deduped twin, plus the source
// row count.
type strideTwin struct {
	name     string
	flat, v2 afero.Fs
	rows     int
}

func strideTwins(t *testing.T) []strideTwin {
	t.Helper()
	g1, g2 := groupedTwinFS(t)
	e1, e2 := elidedTwinFS(t)
	return []strideTwin{
		{name: "grouped", flat: g1, v2: g2, rows: 360},
		{name: "elided", flat: e1, v2: e2, rows: 300},
	}
}

// physicalStride reads the cohort's own schema and returns its on-wire
// stride and whether it is a deduped (grouped) schema.
func physicalStride(t *testing.T, fsys afero.Fs, path string) (int, bool) {
	t.Helper()
	raw, err := afero.ReadFile(fsys, path)
	if err != nil {
		t.Fatal(err)
	}
	s, _, err := encoding.ReadPreamble(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	return s.RecordByteSize(), s.HasGroups()
}

// TestStrideSites_RecordCountEverywhere: every "how many records" answer
// — CountRecords, Cohort.RecordCount, Inspect, export predict's row
// estimate — and an actual full scan agree with the known row count on
// the 0x01 cohort and its deduped twin. Then the last byte is cut off:
// the header-fast arms floor to rows-1 and CountRecords stays SILENT
// (it feeds the parallel-decode gate), while InspectEnvelope raises the
// ENCODING_INVALID truncated-tail warning naming the PHYSICAL stride and
// stride-1 trailing bytes.
func TestStrideSites_RecordCountEverywhere(t *testing.T) {
	ctx := context.Background()
	for _, tw := range strideTwins(t) {
		for i, fsys := range []afero.Fs{tw.flat, tw.v2} {
			t.Run(fmt.Sprintf("%s/%d", tw.name, i), func(t *testing.T) {
				stride, grouped := physicalStride(t, fsys, "cohort.pulse")
				if grouped != (i == 1) {
					t.Fatalf("fixture: grouped=%v on arm %d", grouped, i)
				}
				p, err := New(Options{FS: fsys})
				if err != nil {
					t.Fatal(err)
				}
				counts := func(path string) map[string]int64 {
					out := map[string]int64{}
					n, err := p.CountRecords(ctx, path)
					if err != nil {
						t.Fatalf("CountRecords(%s): %v", path, err)
					}
					out["CountRecords"] = int64(n)
					c, err := p.Open(ctx, path)
					if err != nil {
						t.Fatalf("Open(%s): %v", path, err)
					}
					rc, err := c.RecordCount()
					if err != nil {
						t.Fatalf("RecordCount(%s): %v", path, err)
					}
					out["Cohort.RecordCount"] = int64(rc)
					ins, err := p.Inspect(ctx, path)
					if err != nil {
						t.Fatalf("Inspect(%s): %v", path, err)
					}
					out["Inspect"] = int64(ins.RecordCount)
					job := pio.NewExportJob(path, csv.NewWriterToBuffer())
					job.FS = fsys
					pr, err := job.Predict(ctx)
					if err != nil {
						t.Fatalf("export Predict(%s): %v", path, err)
					}
					out["ExportPredict"] = int64(pr.EstimatedRows)
					return out
				}

				for site, got := range counts("cohort.pulse") {
					if got != int64(tw.rows) {
						t.Errorf("%s = %d, want %d", site, got, tw.rows)
					}
				}
				w := csv.NewWriterToBuffer()
				if _, err := p.Export(ctx, pio.NewExportJob("cohort.pulse", w)); err != nil {
					t.Fatal(err)
				}
				if lines := bytes.Count(w.Bytes(), []byte("\n")); lines != tw.rows+1 {
					t.Errorf("export wrote %d lines, want header + %d rows", lines, tw.rows)
				}
				resp, err := p.Process(ctx, &Request{
					Cohort:       &types.Cohort{Filename: "cohort.pulse"},
					Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "id", Label: "n"}},
				})
				if err != nil {
					t.Fatal(err)
				}
				if got := fmt.Sprint(resp.Data[0]["n"]); got != fmt.Sprint(tw.rows) {
					t.Errorf("full scan counted %s records, want %d", got, tw.rows)
				}

				// Filter-to-file copies whole physical rows by reader
				// position: the output must hold exactly the matching rows.
				wantKept := 0
				for r := 0; r < tw.rows; r++ {
					if (r*7)%11 > 5 {
						wantKept++
					}
				}
				kept, err := p.FilterToFile(ctx, "cohort.pulse", "kept.pulse", "score > 5")
				if err != nil {
					t.Fatal(err)
				}
				keptCount, err := p.CountRecords(ctx, "kept.pulse")
				if err != nil || kept != int64(wantKept) || keptCount != uint64(wantKept) {
					t.Fatalf("FilterToFile kept %d, output counts %d (err %v), want %d", kept, keptCount, err, wantKept)
				}
				fresp, err := p.Process(ctx, &Request{
					Cohort:       &types.Cohort{Filename: "kept.pulse"},
					Filterers:    []*types.Filterer{{Type: types.FILTER_RANGE, Field: "score", Values: []string{"6", "10"}}},
					Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "id", Label: "n"}},
				})
				if err != nil || fmt.Sprint(fresp.Data[0]["n"]) != fmt.Sprint(wantKept) {
					t.Fatalf("filtered output: %v rows with score > 5 (err %v), want every one of %d", fresp, err, wantKept)
				}

				// Truncated tail: drop the last byte.
				raw, err := afero.ReadFile(fsys, "cohort.pulse")
				if err != nil {
					t.Fatal(err)
				}
				if err := afero.WriteFile(fsys, "torn.pulse", raw[:len(raw)-1], 0o644); err != nil {
					t.Fatal(err)
				}
				for site, got := range counts("torn.pulse") {
					if got != int64(tw.rows-1) {
						t.Errorf("truncated tail: %s = %d, want the floor %d", site, got, tw.rows-1)
					}
				}
				env, err := p.InspectEnvelope(ctx, "torn.pulse", nil)
				if err != nil {
					t.Fatal(err)
				}
				if len(env.Warnings) != 1 || env.Warnings[0].Code != string(perrors.ENCODING_INVALID) {
					t.Fatalf("truncated tail: inspect warnings = %s, want one ENCODING_INVALID", mustJSON(t, "warnings", env.Warnings))
				}
				d := env.Warnings[0].Details
				if fmt.Sprint(d["record_stride"]) != fmt.Sprint(stride) || fmt.Sprint(d["trailing_bytes"]) != fmt.Sprint(stride-1) {
					t.Fatalf("truncated-tail warning details = %v, want record_stride %d (physical) and trailing_bytes %d", d, stride, stride-1)
				}
				if ir, ok := env.Data.(*descriptor.InspectResult); !ok || ir.RecordCount != int64(tw.rows-1) {
					t.Fatalf("truncated tail: envelope data %T record count, want %d", env.Data, tw.rows-1)
				}
				// The intact cohort carries no warning.
				env, err = p.InspectEnvelope(ctx, "cohort.pulse", nil)
				if err != nil || len(env.Warnings) != 0 {
					t.Fatalf("intact cohort: inspect err=%v warnings=%s", err, mustJSON(t, "warnings", env.Warnings))
				}
			})
		}
	}
}

// TestStrideSites_LookupLandsOnTheNamedRecord: point Lookup seeks the
// record locator to row_id * stride. On the deduped twin it must land on
// the named record — first, middle and last — keyed by a CHILD field
// (id, never a group member) and, for the parent-grouped cohort, by a
// group MEMBER (parent_code, fanning out to the parent's 12 children).
func TestStrideSites_LookupLandsOnTheNamedRecord(t *testing.T) {
	ctx := context.Background()
	for _, tw := range strideTwins(t) {
		for i, fsys := range []afero.Fs{tw.flat, tw.v2} {
			t.Run(fmt.Sprintf("%s/%d", tw.name, i), func(t *testing.T) {
				p, err := New(Options{FS: fsys})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := p.BuildIndex(ctx, "cohort.pulse", []string{"id"}); err != nil {
					t.Fatal(err)
				}
				for _, id := range []int{1, tw.rows / 2, tw.rows} {
					res, err := p.Lookup(ctx, &LookupRequest{Cohort: &types.Cohort{Filename: "cohort.pulse"}, Field: "id", Value: fmt.Sprint(id)})
					if err != nil {
						t.Fatalf("Lookup(id=%d): %v", id, err)
					}
					if len(res.Rows) != 1 || fmt.Sprint(res.Rows[0]["id"]) != fmt.Sprint(id) {
						t.Fatalf("Lookup(id=%d) = %v, want the one row with that id", id, res.Rows)
					}
					if want := []string{"north", "south", "east", "west"}; tw.name == "elided" && res.Rows[0]["region"] != want[(id-1)%4] {
						t.Fatalf("Lookup(id=%d) region = %v, want %s", id, res.Rows[0]["region"], want[(id-1)%4])
					}
					if tw.name == "grouped" && fmt.Sprint(res.Rows[0]["parent_code"]) != fmt.Sprint(1000+(id-1)/12) {
						t.Fatalf("Lookup(id=%d) parent_code = %v, want %d", id, res.Rows[0]["parent_code"], 1000+(id-1)/12)
					}
				}
				if tw.name != "grouped" {
					return
				}
				if _, err := p.BuildIndex(ctx, "cohort.pulse", []string{"parent_code"}); err != nil {
					t.Fatal(err)
				}
				for _, parent := range []int{0, 15, 29} {
					res, err := p.Lookup(ctx, &LookupRequest{Cohort: &types.Cohort{Filename: "cohort.pulse"}, Field: "parent_code",
						Value: fmt.Sprint(1000 + parent), Multiplicity: LookupMultiplicityAll})
					if err != nil {
						t.Fatalf("Lookup(parent_code=%d): %v", 1000+parent, err)
					}
					if len(res.Rows) != 12 {
						t.Fatalf("Lookup(parent_code=%d) returned %d rows, want 12", 1000+parent, len(res.Rows))
					}
					for k, row := range res.Rows {
						if want := fmt.Sprint(parent*12 + k + 1); fmt.Sprint(row["id"]) != want || fmt.Sprint(row["parent_code"]) != fmt.Sprint(1000+parent) {
							t.Fatalf("Lookup(parent_code=%d) row %d = id %v parent_code %v, want id %s", 1000+parent, k, row["id"], row["parent_code"], want)
						}
					}
				}
			})
		}
	}
}

// TestStrideSites_SynthAugmentFromDedupedSource: synth augmentation
// re-encodes every source row into a new (flat) cohort and the fidelity
// report re-reads the source; both walk the source's records. From a
// deduped source the output cohort, the report and the result must be
// byte-identical to augmenting the 0x01 twin, with every source row
// carried.
func TestStrideSites_SynthAugmentFromDedupedSource(t *testing.T) {
	ctx := context.Background()
	for _, tw := range strideTwins(t) {
		t.Run(tw.name, func(t *testing.T) {
			var out [2][3]string
			for i, fsys := range []afero.Fs{tw.flat, tw.v2} {
				p, err := New(Options{FS: fsys})
				if err != nil {
					t.Fatal(err)
				}
				prof, err := p.Profile(ctx, "cohort.pulse", ProfileOptions{})
				if err != nil {
					t.Fatalf("Profile: %v", err)
				}
				spec, _ := synth.SpecFromProfile(prof, 40)
				res, err := p.Synth(ctx, spec, "augmented.pulse", SynthOptions{Seed: 7, SourceCohort: "cohort.pulse", FidelityReportPath: "fidelity.json"})
				if err != nil {
					t.Fatalf("Synth(augment) on arm %d: %v", i, err)
				}
				n, err := p.CountRecords(ctx, "augmented.pulse")
				if err != nil || n != uint64(tw.rows+40) {
					t.Fatalf("augmented cohort holds %d records (err %v), want %d source + 40 generated", n, err, tw.rows)
				}
				aug, _ := afero.ReadFile(fsys, "augmented.pulse")
				rep, _ := afero.ReadFile(fsys, "fidelity.json")
				out[i] = [3]string{string(aug), string(rep), mustJSON(t, "result", res)}
			}
			for k, what := range []string{"augmented cohort", "fidelity report", "result"} {
				if out[0][k] != out[1][k] {
					t.Fatalf("%s differs between augmenting the 0x01 source and its deduped twin", what)
				}
			}
		})
	}
}

// TestStrideSites_ArchiveRewritersFlattenDeduped: shard admin paths
// that stride through shard payloads by byte offset — create, add —
// accept a deduped cohort into an UNGROUPED archive by storing its
// logical (0x01) twin (E5-S3: the archive's layout wins). The stored
// bytes are exactly those of the flat source, so the archive is
// byte-identical to one built from the flat cohort under the same
// name, and the rewrite is reported by the mandatory
// PULSE_SHARD_GROUPS_REWRITTEN warning.
func TestStrideSites_ArchiveRewritersFlattenDeduped(t *testing.T) {
	ctx := context.Background()
	for _, tw := range strideTwins(t) {
		t.Run(tw.name, func(t *testing.T) {
			fsys := tw.flat
			v2, err := afero.ReadFile(tw.v2, "cohort.pulse")
			if err != nil {
				t.Fatal(err)
			}
			flat, _ := afero.ReadFile(fsys, "cohort.pulse")
			p, err := New(Options{FS: fsys})
			if err != nil {
				t.Fatal(err)
			}
			build := func(arrival []byte) ([]byte, *CreateShardArchiveResult, *AddShardResult) {
				t.Helper()
				for name, b := range map[string][]byte{"a.pulse": flat, "b.pulse": flat, "dedup.pulse": arrival} {
					if err := afero.WriteFile(fsys, name, b, 0o644); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := p.CreateShardArchive(ctx, "arch.pulse", []string{"a.pulse", "b.pulse"}); err != nil {
					t.Fatalf("CreateShardArchive(flat): %v", err)
				}
				add, err := p.AddShard(ctx, "arch.pulse", "dedup.pulse")
				if err != nil {
					t.Fatalf("AddShard: %v", err)
				}
				added, _ := afero.ReadFile(fsys, "arch.pulse")
				created, err := p.CreateShardArchive(ctx, "arch2.pulse", []string{"a.pulse", "dedup.pulse"})
				if err != nil {
					t.Fatalf("CreateShardArchive(flat+arrival): %v", err)
				}
				c2, _ := afero.ReadFile(fsys, "arch2.pulse")
				return append(added, c2...), created, add
			}
			want, _, _ := build(flat)
			got, created, added := build(v2)
			if !bytes.Equal(got, want) {
				t.Fatal("an ungrouped archive holding a deduped arrival differs from one holding its flat twin")
			}
			for _, ws := range [][]encoding.CohesionWarning{created.Warnings, added.Warnings} {
				if len(ws) != 1 || ws[0].Code != string(perrors.PULSE_SHARD_GROUPS_REWRITTEN) || ws[0].Details["reason"] != "incoming_flattened" {
					t.Fatalf("warnings = %+v, want one PULSE_SHARD_GROUPS_REWRITTEN incoming_flattened", ws)
				}
			}
		})
	}
}
