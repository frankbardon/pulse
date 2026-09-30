package io

import (
	"bytes"
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	perrors "github.com/frankbardon/pulse/errors"
	"github.com/spf13/afero"
)

// flatCohortFS imports rows flat (0x01) to "c.pulse" on a fresh memfs.
func flatCohortFS(t *testing.T, cols []string, rows [][]string, mutate ...func(*ImportJob)) (afero.Fs, []byte) {
	t.Helper()
	fs := afero.NewMemMapFs()
	job := NewImportJob(newMockReader(cols, rows), "c.pulse")
	job.FS = fs
	for _, m := range mutate {
		m(job)
	}
	if _, err := job.Run(context.Background()); err != nil {
		t.Fatalf("flat import: %v", err)
	}
	raw, err := afero.ReadFile(fs, "c.pulse")
	if err != nil {
		t.Fatal(err)
	}
	return fs, raw
}

func runDedup(t *testing.T, fs afero.Fs, mutate func(*DedupJob)) (*DedupReport, error) {
	t.Helper()
	job := &DedupJob{FS: fs, Source: "c.pulse"}
	mutate(job)
	return job.Run(context.Background())
}

// dirListing is every file name in the memfs root, to prove no temp or
// spool file outlives a run.
func dirListing(t *testing.T, fs afero.Fs) []string {
	t.Helper()
	infos, err := afero.ReadDir(fs, ".")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, fi := range infos {
		names = append(names, fi.Name())
	}
	return names
}

// TestDedupJob_InPlaceMatchesImport: retro-dedup of a flat cohort is
// BYTE-IDENTICAL to importing the same source with the same --group
// declarations, and reports the same per-group verdicts — one encode
// path and one gate, not a parallel implementation. The rewrite is in
// place and leaves no staging file behind.
func TestDedupJob_InPlaceMatchesImport(t *testing.T) {
	cols, rows := joinFixture(600)
	fs, flat := flatCohortFS(t, cols, rows)
	importRep, imported, _, err := runGroupImport(t, newMockReader(cols, rows), joinGroups)
	if err != nil {
		t.Fatal(err)
	}

	rep, err := runDedup(t, fs, func(j *DedupJob) { j.Groups = joinGroups })
	if err != nil {
		t.Fatalf("dedup: %v", err)
	}
	got, _ := afero.ReadFile(fs, "c.pulse")
	if !bytes.Equal(got, imported) {
		t.Fatalf("retro-dedup wrote %d bytes that differ from the grouped import's %d", len(got), len(imported))
	}
	if !reflect.DeepEqual(rep.Groups, importRep.Groups) || len(rep.GroupWarnings) != 0 {
		t.Fatalf("groups = %+v (warnings %v)\nimport groups = %+v", rep.Groups, rep.GroupWarnings, importRep.Groups)
	}
	want := DedupReport{
		Source: "c.pulse", Target: "c.pulse", InPlace: true, Rewritten: true, Records: 600,
		FormatVersionBefore: 1, FormatVersionAfter: 2,
		BytesBefore: int64(len(flat)), BytesAfter: int64(len(imported)),
		StrideBefore: importRepFlatStride(t, flat), StrideAfter: importRep.Schema.RecordByteSize(),
		Groups: rep.Groups,
	}
	if !reflect.DeepEqual(*rep, want) {
		t.Fatalf("report = %+v\nwant %+v", *rep, want)
	}
	if names := dirListing(t, fs); !reflect.DeepEqual(names, []string{"c.pulse"}) {
		t.Fatalf("files after dedup = %v, want only c.pulse", names)
	}
}

func importRepFlatStride(t *testing.T, raw []byte) int {
	t.Helper()
	s, _, err := encoding.ReadPreamble(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	return s.RecordByteSize()
}

// TestDedupJob_RegroupsGroupedSource: a 0x02 source re-deduped with a
// DIFFERENT declaration is regrouped from its logical stream — the
// result equals importing with the new declaration alone, and decodes
// to the flat cohort's values.
func TestDedupJob_RegroupsGroupedSource(t *testing.T) {
	cols, rows := joinFixture(600)
	_, custOnly, _, err := runGroupImport(t, newMockReader(cols, rows), joinGroups[:1])
	if err != nil {
		t.Fatal(err)
	}
	_, prodOnly, _, err := runGroupImport(t, newMockReader(cols, rows), joinGroups[1:])
	if err != nil {
		t.Fatal(err)
	}
	fs := afero.NewMemMapFs()
	if err := afero.WriteFile(fs, "c.pulse", custOnly, 0o644); err != nil {
		t.Fatal(err)
	}
	rep, err := runDedup(t, fs, func(j *DedupJob) { j.Groups = joinGroups[1:] })
	if err != nil {
		t.Fatalf("dedup: %v", err)
	}
	got, _ := afero.ReadFile(fs, "c.pulse")
	if !bytes.Equal(got, prodOnly) {
		t.Fatal("regrouped cohort differs from importing with the new declaration")
	}
	if rep.FormatVersionBefore != 2 || rep.FormatVersionAfter != 2 || rep.Records != 600 {
		t.Fatalf("report %+v", rep)
	}
	flatFS, _ := flatCohortFS(t, cols, rows)
	_, want, wantNulls := decodeAll(t, flatFS, "c.pulse")
	_, vals, nulls := decodeAll(t, fs, "c.pulse")
	if !reflect.DeepEqual(vals, want) || !reflect.DeepEqual(nulls, wantNulls) {
		t.Fatal("regrouped cohort does not decode to the flat cohort's values")
	}
}

// TestDedupJob_Out: Target writes a new cohort and leaves the source
// byte-identical; an existing Target is refused before anything runs.
func TestDedupJob_Out(t *testing.T) {
	cols, rows := joinFixture(600)
	fs, flat := flatCohortFS(t, cols, rows)
	_, imported, _, err := runGroupImport(t, newMockReader(cols, rows), joinGroups)
	if err != nil {
		t.Fatal(err)
	}
	rep, err := runDedup(t, fs, func(j *DedupJob) { j.Groups, j.Target = joinGroups, "g.pulse" })
	if err != nil {
		t.Fatal(err)
	}
	src, _ := afero.ReadFile(fs, "c.pulse")
	out, _ := afero.ReadFile(fs, "g.pulse")
	if !bytes.Equal(src, flat) || !bytes.Equal(out, imported) || rep.InPlace || rep.Target != "g.pulse" {
		t.Fatalf("source intact %v, target matches import %v, report %+v", bytes.Equal(src, flat), bytes.Equal(out, imported), rep)
	}
	_, err = runDedup(t, fs, func(j *DedupJob) { j.Groups, j.Target = joinGroups, "g.pulse" })
	if !perrors.HasCode(err, perrors.SERVICE_VALIDATION) {
		t.Fatalf("existing target: err = %v, want SERVICE_VALIDATION", err)
	}
}

// TestDedupJob_FailureLeavesOriginal: every refusal — a member that
// varies within its key (mid-pass), a strict ratio finding (after the
// pass, before the rename), a strict width finding (before the pass), a
// cancelled context — leaves the original byte-identical and no staging
// file behind.
func TestDedupJob_FailureLeavesOriginal(t *testing.T) {
	cols, rows := joinFixture(600)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	cases := []struct {
		name string
		ctx  context.Context
		job  func(*DedupJob)
		code perrors.Code
	}{
		{"member varies within key", context.Background(), func(j *DedupJob) {
			j.Groups = []GroupDecl{{Key: []string{"cust_id"}, Members: []string{"cust_name", "cust_region", "cust_tier", "prod_price", "qty"}}}
		}, perrors.PULSE_GROUP_MEMBER_NOT_CONSTANT},
		{"strict low ratio", context.Background(), func(j *DedupJob) {
			j.Groups = []GroupDecl{{Members: []string{"line_id", "cust_name", "prod_price"}}}
			j.StrictDedup = true
		}, perrors.PULSE_DEDUP_LOW_RATIO},
		{"strict too narrow", context.Background(), func(j *DedupJob) {
			j.Groups = []GroupDecl{{Members: []string{"qty"}}}
			j.StrictDedup = true
		}, perrors.PULSE_GROUP_TOO_NARROW},
		{"unknown field", context.Background(), func(j *DedupJob) {
			j.Groups = []GroupDecl{{Key: []string{"nope"}, Members: []string{"qty"}}}
		}, perrors.PULSE_GROUP_FIELD_UNKNOWN},
		{"nothing to do", context.Background(), func(*DedupJob) {}, perrors.PULSE_GROUP_DECLARATION_INVALID},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fs, flat := flatCohortFS(t, cols, rows)
			job := &DedupJob{FS: fs, Source: "c.pulse"}
			tc.job(job)
			_, err := job.Run(tc.ctx)
			if !perrors.HasCode(err, tc.code) {
				t.Fatalf("err = %v, want %s", err, tc.code)
			}
			assertUntouched(t, fs, flat)
		})
	}
	t.Run("cancelled", func(t *testing.T) {
		fs, flat := flatCohortFS(t, cols, rows)
		job := &DedupJob{FS: fs, Source: "c.pulse", Groups: joinGroups}
		if _, err := job.Run(cancelled); err == nil {
			t.Fatal("cancelled dedup succeeded")
		}
		assertUntouched(t, fs, flat)
	})
}

func assertUntouched(t *testing.T, fs afero.Fs, flat []byte) {
	t.Helper()
	got, _ := afero.ReadFile(fs, "c.pulse")
	if !bytes.Equal(got, flat) {
		t.Fatal("original cohort changed after a refused dedup")
	}
	if names := dirListing(t, fs); !reflect.DeepEqual(names, []string{"c.pulse"}) {
		t.Fatalf("files after refused dedup = %v, want only c.pulse", names)
	}
}

// TestDedupJob_Gate: without strict, the gate's findings are warnings —
// a low-ratio group is written, a too-narrow one is dropped — with the
// same codes and verdicts as import. When every declared group is
// dropped there is nothing to write and the cohort is untouched.
func TestDedupJob_Gate(t *testing.T) {
	cols, rows := joinFixture(600)
	fs, _ := flatCohortFS(t, cols, rows)
	decls := []GroupDecl{{Members: []string{"line_id", "cust_name", "prod_price"}}, {Members: []string{"qty"}}}
	importRep, imported, _, err := runGroupImport(t, newMockReader(cols, rows), decls)
	if err != nil {
		t.Fatal(err)
	}
	rep, err := runDedup(t, fs, func(j *DedupJob) { j.Groups = decls })
	if err != nil {
		t.Fatal(err)
	}
	got, _ := afero.ReadFile(fs, "c.pulse")
	if !bytes.Equal(got, imported) || !reflect.DeepEqual(rep.Groups, importRep.Groups) {
		t.Fatal("gated dedup differs from the gated import")
	}
	if c := codes(rep.GroupWarnings); c != "PULSE_GROUP_TOO_NARROW;PULSE_DEDUP_LOW_RATIO;" {
		t.Fatalf("warnings = %s", c)
	}

	fs2, flat := flatCohortFS(t, cols, rows)
	rep, err = runDedup(t, fs2, func(j *DedupJob) { j.Groups = decls[1:] })
	if err != nil {
		t.Fatal(err)
	}
	if rep.Rewritten || rep.Records != 600 || rep.BytesAfter != rep.BytesBefore || rep.Groups[0].Verdict != encoding.GroupVerdictDroppedTooNarrow {
		t.Fatalf("all-dropped report = %+v", rep)
	}
	assertUntouched(t, fs2, flat)
}

// TestDedupJob_ElideConstants: elision on retro-dedup equals elision on
// import, composed with declared groups.
func TestDedupJob_ElideConstants(t *testing.T) {
	cols, rows := joinFixture(600)
	cols = append(cols, "batch")
	for i := range rows {
		rows[i] = append(rows[i], "batch-7")
	}
	fs, _ := flatCohortFS(t, cols, rows)
	elide := func(j *ImportJob) { j.ElideConstants = true }
	importRep, imported, _, err := runGroupImport(t, newMockReader(cols, rows), joinGroups, elide)
	if err != nil {
		t.Fatal(err)
	}
	rep, err := runDedup(t, fs, func(j *DedupJob) { j.Groups, j.ElideConstants = joinGroups, true })
	if err != nil {
		t.Fatal(err)
	}
	got, _ := afero.ReadFile(fs, "c.pulse")
	if !bytes.Equal(got, imported) {
		t.Fatal("retro-dedup with elision differs from the import with elision")
	}
	if !reflect.DeepEqual(rep.ElidedConstants, importRep.ElidedConstants) || strings.Join(rep.ElidedConstants, ",") != "batch" {
		t.Fatalf("elided %v, import elided %v", rep.ElidedConstants, importRep.ElidedConstants)
	}
}

// TestDedupJob_SuggestGroups: detection over an existing cohort's
// records reports exactly what import predict reports over the source
// it was imported from — the same engine fed decoded rows — and a
// suggestion-only run writes nothing.
func TestDedupJob_SuggestGroups(t *testing.T) {
	cols, rows := orderLinesFixture(14000)
	fs, flat := flatCohortFS(t, cols, rows, orderLinesTypes)
	pred, err := predictJob(t, newMockReader(cols, rows), suggest)
	if err != nil {
		t.Fatal(err)
	}
	rep, err := runDedup(t, fs, func(j *DedupJob) { j.SuggestGroups = true })
	if err != nil {
		t.Fatal(err)
	}
	if rep.Rewritten || rep.Records != 14000 {
		t.Fatalf("suggest-only report = %+v", rep)
	}
	assertUntouched(t, fs, flat)
	if !reflect.DeepEqual(rep.GroupCandidates, pred.GroupCandidates) {
		t.Fatalf("cohort detection = %+v\nimport predict = %+v", rep.GroupCandidates, pred.GroupCandidates)
	}
	if len(rep.GroupCandidates.Suggested) == 0 {
		t.Fatal("no suggestion on a join-shaped cohort")
	}
}

// TestDedupJob_SuggestGroups_NoBitmap: a cohort with no nullable field
// stores no per-row bitmap; its logical rows are padded to the
// detector's full-bitmap layout, and detection still equals import
// predict's over the same source.
func TestDedupJob_SuggestGroups_NoBitmap(t *testing.T) {
	cols, rows := joinFixture(600)
	for _, r := range rows {
		if r[4] == "" {
			r[4] = "9"
		}
	}
	fs, _ := flatCohortFS(t, cols, rows)
	job := NewImportJob(newMockReader(cols, rows), "unused.pulse")
	job.SuggestGroups = true
	pred, err := job.Predict(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if pred.Schema.HasBitmap() {
		t.Fatal("fixture should have no nullable field")
	}
	rep, err := runDedup(t, fs, func(j *DedupJob) { j.SuggestGroups = true })
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(rep.GroupCandidates, pred.GroupCandidates) || len(rep.GroupCandidates.Suggested) != 2 {
		t.Fatalf("cohort detection = %+v\nimport predict = %+v", rep.GroupCandidates, pred.GroupCandidates)
	}
}

// TestDedupJob_TruncatedSource: a cohort whose payload ends mid-record
// is refused, never silently shortened.
func TestDedupJob_TruncatedSource(t *testing.T) {
	cols, rows := joinFixture(60)
	fs, flat := flatCohortFS(t, cols, rows)
	if err := afero.WriteFile(fs, "c.pulse", flat[:len(flat)-3], 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := runDedup(t, fs, func(j *DedupJob) { j.Groups = joinGroups })
	if !perrors.HasCode(err, perrors.ENCODING_INVALID) {
		t.Fatalf("err = %v, want ENCODING_INVALID", err)
	}
	if names := dirListing(t, fs); !reflect.DeepEqual(names, []string{"c.pulse"}) {
		t.Fatalf("files = %v", names)
	}
}
