package pulse

import (
	"bytes"
	"context"
	stderrors "errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/io/spss"
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// Export of an anchored shard (cohort-reshape E6-S1): a source
// `archive.pulse#shard.pulse` exports — every format, run and predict —
// that one shard's records under the archive's CANONICAL schema: the
// same rows the whole-archive export yields for the entry, read the same
// way. Its twin is the archive flattened and filtered to that shard
// (FilterToFileWithOptions{Flatten}), which keeps the canonical schema.

// shardNames lists the archive's shard entries in archive order.
func shardNames(t *testing.T, p *Pulse, archive string) []string {
	t.Helper()
	arts, err := p.ListShards(context.Background(), archive)
	if err != nil {
		t.Fatalf("ListShards %s: %v", archive, err)
	}
	var out []string
	for _, a := range arts {
		out = append(out, a.Filename)
	}
	return out
}

// flattenFiltered writes dst: archive flattened to one file under its
// canonical schema, keeping the rows expr selects.
func flattenFiltered(t *testing.T, p *Pulse, archive, dst, expr string) int64 {
	t.Helper()
	rep, err := p.FilterToFileWithOptions(context.Background(), archive, dst, FilterToFileOptions{Expression: expr, Flatten: true})
	if err != nil {
		t.Fatalf("flatten %s where %s: %v", archive, expr, err)
	}
	if !rep.Flattened {
		t.Fatalf("flatten %s: report %+v, want flattened", archive, rep)
	}
	return rep.Records
}

// TestExportAnchor_EveryFormatMatchesFilteredArchive: an anchored shard
// of a set-widened archive (a shard whose own dictionary is a strict
// prefix of the canonical one, and the appended shard) and of a grouped
// (0x02) archive exports to every format exactly as the archive
// flattened and filtered to that shard; predict reports the same
// schema, rows, columns and target warnings.
func TestExportAnchor_EveryFormatMatchesFilteredArchive(t *testing.T) {
	t.Parallel()

	t.Run("widened set", func(t *testing.T) {
		t.Parallel()
		p, fsys, _ := widenedSetArchive(t)
		names := shardNames(t, p, "sets.pulse")
		if len(names) != 2 || names[1] != "more.pulse" {
			t.Fatalf("shards %v, want [<first> more.pulse]", names)
		}
		for i, expr := range []string{"id < 100", "id >= 100"} {
			flat := "flat-" + names[i]
			if n := flattenFiltered(t, p, "sets.pulse", flat, expr); n != 8 {
				t.Fatalf("filtered twin of %s holds %d rows, want 8", names[i], n)
			}
			anchored := "sets.pulse#" + names[i]
			assertExportsMatch(t, p, fsys, anchored, flat, 8)
			assertPredictsMatch(t, p, fsys, anchored, flat)
			v, err := predictExportView(t, p, fsys, anchored, "", nil)
			if err != nil || v.Types[1] != encoding.FieldTypeSetU16 || v.Rows != 8 {
				t.Errorf("%s predict %+v (%v), want 8 rows under the canonical set_u16", anchored, v, err)
			}
		}
		// The first shard's members only — never the other shard's rows.
		csv, _ := archiveExportBytes(t, p, fsys, "sets.pulse#"+names[0], pio.FormatCSV)
		if !bytes.Contains(csv, []byte("\n0,a\n")) || bytes.Contains(csv, []byte("\n100,")) {
			t.Errorf("CSV export of the anchored first shard:\n%s", csv)
		}
	})

	t.Run("grouped", func(t *testing.T) {
		t.Parallel()
		p, fsys := memBuilderEngine(t)
		rows, _ := groupedData()
		buildCohort(t, p, "arch.pulse", groupedSchema(), CohortBuilderOptions{Groups: []pio.GroupDecl{custGroup}, Shards: &ShardSplit{MaxRecords: 50}}, rows)
		names := shardNames(t, p, "arch.pulse")
		if len(names) != 3 {
			t.Fatalf("shards %v, want 3", names)
		}
		if n := flattenFiltered(t, p, "arch.pulse", "flat.pulse", "order > 50 && order <= 100"); n != 50 {
			t.Fatalf("filtered twin holds %d rows, want 50", n)
		}
		assertExportsMatch(t, p, fsys, "arch.pulse#"+names[1], "flat.pulse", 50)
		assertPredictsMatch(t, p, fsys, "arch.pulse#"+names[1], "flat.pulse")
	})
}

// assertPredictsMatch requires the same export predict for a and b with
// no target, a row target, a list-aware target and the `.sav` validator.
func assertPredictsMatch(t *testing.T, p *Pulse, fsys afero.Fs, a, b string) {
	t.Helper()
	for _, f := range []pio.Format{"", pio.FormatCSV, pio.FormatArrow, pio.FormatSPSS} {
		av, aerr := predictExportView(t, p, fsys, a, f, nil)
		bv, berr := predictExportView(t, p, fsys, b, f, nil)
		if aerr != nil || berr != nil {
			t.Fatalf("%q: predict %s err %v, %s err %v", f, a, aerr, b, berr)
		}
		if !reflect.DeepEqual(av, bv) {
			t.Errorf("%q: predict %s %+v, %s %+v", f, a, av, b, bv)
		}
	}
}

// TestExportAnchor_UnknownAnchorRefusedAsOpen: an anchor naming no
// entry, the reserved canonical entry, or a single-file cohort is
// refused by every export arm — run, predict, row and `.sav` targets —
// with the code AND details pulse.Open gives the same path, never a
// file-not-found.
func TestExportAnchor_UnknownAnchorRefusedAsOpen(t *testing.T) {
	t.Parallel()

	p, fsys, _ := widenedSetArchive(t)
	for _, tc := range []struct {
		path string
		code errors.Code
	}{
		{"sets.pulse#nope.pulse", errors.PULSE_SHARD_MISSING},
		{"sets.pulse#", errors.PULSE_SHARD_MISSING},
		{"sets.pulse#_schema.pulse", errors.PULSE_SHARD_RESERVED_NAME},
		{"flat.pulse#x.pulse", errors.PULSE_ARCHIVE_MAGIC_INVALID},
	} {
		_, oerr := p.Open(context.Background(), tc.path)
		var want *errors.CodedError
		if !stderrors.As(oerr, &want) || want.Code != tc.code {
			t.Fatalf("Open(%q) = %v, want %s", tc.path, oerr, tc.code)
		}
		for _, f := range []pio.Format{pio.FormatCSV, pio.FormatSPSS} {
			w, err := pio.NewWriter(f, fsys, "refused."+string(f), pio.WriterOptions{})
			if err != nil {
				t.Fatal(err)
			}
			job := pio.NewExportJob(tc.path, w)
			job.FS = fsys
			_, rerr := p.Export(context.Background(), job)
			_ = w.Close()
			_, perr := predictExportView(t, p, fsys, tc.path, f, nil)
			for arm, err := range map[string]error{"export": rerr, "predict": perr} {
				var got *errors.CodedError
				if !stderrors.As(err, &got) {
					t.Errorf("%s %s %q = %v, want coded %s", f, arm, tc.path, err, tc.code)
					continue
				}
				if got.Code != want.Code || !reflect.DeepEqual(got.Details, want.Details) {
					t.Errorf("%s %s %q = %s %v, Open gives %s %v", f, arm, tc.path, got.Code, got.Details, want.Code, want.Details)
				}
			}
		}
	}
}

// TestExportAnchor_SavUsesArchiveSidecar: a `.sav` export of an anchored
// shard of an archive whose SPSS sidecar was carried beside it applies
// THAT sidecar (fresh over the archive's bytes): no absent warning, the
// variable names and value labels of a whole-archive export. A
// per-shard sidecar path is never read (a corrupt file there changes
// nothing), and a stale archive sidecar refuses the anchored export
// with the whole-archive export's code.
func TestExportAnchor_SavUsesArchiveSidecar(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	afs := afero.NewMemMapFs()
	p := importSav(t, afs, constantSavSpec(), Options{})
	if _, err := p.Materialize(ctx, withOutput(absentOutputRequest(), &types.MaterializeOutput{
		Shards: &types.MaterializeShards{MaxRecords: 16},
	}), "derived-arch.pulse", MaterializeOptions{}); err != nil {
		t.Fatal(err)
	}
	names := shardNames(t, p, "derived-arch.pulse")
	if len(names) != 3 {
		t.Fatalf("shards %v, want 3", names)
	}
	anchored := "derived-arch.pulse#" + names[1]
	// A per-shard sidecar the anchored export must never consult.
	if err := afero.WriteFile(afs, anchored+".spss.json", []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	sav, rep := archiveExportBytes(t, p, afs, anchored, pio.FormatSPSS)
	for _, w := range rep.TargetWarnings {
		if w.Code == errors.PULSE_SPSS_SIDECAR_ABSENT || w.Code == errors.PULSE_SPSS_SIDECAR_IGNORED {
			t.Fatalf("anchored .sav export did not apply the archive sidecar: %v", w)
		}
	}
	if err := afero.WriteFile(afs, "anchored.sav", sav, 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := pio.NewReader(pio.FormatSPSS, afs, "anchored.sav", pio.ReaderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	imp := pio.NewImportJob(r, "anchored-back.pulse")
	imp.FS = afs
	irep, err := p.Import(ctx, imp)
	if err != nil {
		t.Fatal(err)
	}
	if irep.RowsImported != int(shardSizes(t, p, "derived-arch.pulse")[1]) {
		t.Errorf("re-imported %d rows, want the shard's %d", irep.RowsImported, shardSizes(t, p, "derived-arch.pulse")[1])
	}
	res, err := spss.LoadSidecar(afs, "anchored-back.pulse", spss.WriterOptions{})
	if err != nil || res.Document == nil {
		t.Fatalf("re-imported sidecar: %v", err)
	}
	var vars []string
	var q1 map[float64]string
	for _, v := range res.Document.Payload.Variables {
		vars = append(vars, v.Name)
		if v.Name != "Q1" {
			continue
		}
		q1 = map[float64]string{}
		for _, c := range v.Categories {
			if c.Labelled && c.Code != nil {
				q1[float64(*c.Code)] = c.Label
			}
		}
	}
	if want := []string{"ID", "Q1", "WT", "WAVE", "Q1_1", "Q1_2", "rn", "prev_wt"}; !slices.Equal(vars, want) {
		t.Errorf("anchored .sav variables = %v, want %v", vars, want)
	}
	if !reflect.DeepEqual(q1, map[float64]string{1: "Low", 2: "High"}) {
		t.Errorf("Q1 value labels = %v, want the archive sidecar's", q1)
	}

	// Stale: the archive moved under its sidecar. The anchored export is
	// refused exactly as the whole-archive export is.
	later := time.Now().Add(time.Hour)
	if err := afs.Chtimes("derived-arch.pulse", later, later); err != nil {
		t.Fatal(err)
	}
	refuse := func(path string) *errors.CodedError {
		w, err := pio.NewWriter(pio.FormatSPSS, afs, "stale.sav", pio.WriterOptions{})
		if err != nil {
			t.Fatal(err)
		}
		job := pio.NewExportJob(path, w)
		job.FS = afs
		_, err = p.Export(ctx, job)
		_ = w.Close()
		var ce *errors.CodedError
		if !stderrors.As(err, &ce) {
			t.Fatalf("export of %s over a stale sidecar = %v, want a coded refusal", path, err)
		}
		return ce
	}
	whole, anch := refuse("derived-arch.pulse"), refuse(anchored)
	if whole.Code != anch.Code {
		t.Errorf("stale sidecar: anchored export %s, whole-archive export %s", anch.Code, whole.Code)
	}
}
