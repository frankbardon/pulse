package pulse

import (
	"archive/zip"
	"bytes"
	"context"
	stderrors "errors"
	"io"
	"reflect"
	"slices"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// Export from a shard archive (cohort-reshape E4-S3): every export
// target reads an archive as its CANONICAL schema over every shard's
// records in archive order, so the output equals the single-file
// cohort's byte for byte — rows, order, types, labels — and the export
// predict reports the same.

// exportFormats is every row and cohort writer the export job drives.
var exportFormats = []pio.Format{
	pio.FormatCSV, pio.FormatTSV, pio.FormatNDJSON, pio.FormatJSONArray,
	pio.FormatParquet, pio.FormatArrow, pio.FormatExcel, pio.FormatSPSS,
}

// archiveExportBytes exports cohort to format f through Pulse.Export and
// returns the written bytes with the report.
func archiveExportBytes(t *testing.T, p *Pulse, fsys afero.Fs, cohort string, f pio.Format, labels ...*types.LabelBinding) ([]byte, *pio.ExportReport) {
	t.Helper()
	out := cohort + "." + string(f)
	w, err := pio.NewWriter(f, fsys, out, pio.WriterOptions{})
	if err != nil {
		t.Fatal(err)
	}
	job := pio.NewExportJob(cohort, w)
	job.FS = fsys
	job.Labels = labels
	rep, err := p.Export(context.Background(), job)
	if err != nil {
		t.Fatalf("export %s to %s: %v", cohort, f, err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close %s: %v", out, err)
	}
	return readFile(t, fsys, out), rep
}

// assertExportsMatch exports arch and flat to every format and requires
// identical bytes and row counts.
func assertExportsMatch(t *testing.T, p *Pulse, fsys afero.Fs, arch, flat string, wantRows int) {
	t.Helper()
	for _, f := range exportFormats {
		a, arep := archiveExportBytes(t, p, fsys, arch, f)
		s, srep := archiveExportBytes(t, p, fsys, flat, f)
		if arep.RowsExported != wantRows || srep.RowsExported != wantRows {
			t.Errorf("%s: rows exported archive %d / single file %d, want %d", f, arep.RowsExported, srep.RowsExported, wantRows)
		}
		if !bytes.Equal(a, s) {
			t.Errorf("%s: archive export (%d bytes) differs from the single-file export (%d bytes)", f, len(a), len(s))
		}
	}
}

// TestExportArchive_EveryFormatMatchesSingleFile: an archive of every
// field type (dictionaries growing shard by shard, so every shard's own
// is a strict prefix of the canonical one) and a grouped (0x02) archive
// export to every format exactly as their single-file twins.
func TestExportArchive_EveryFormatMatchesSingleFile(t *testing.T) {
	t.Parallel()

	t.Run("all field types", func(t *testing.T) {
		t.Parallel()
		p, fsys := memBuilderEngine(t)
		rows := parityRows(t)
		buildCohort(t, p, "arch.pulse", paritySchema(), CohortBuilderOptions{Shards: &ShardSplit{MaxRecords: 1}}, rows)
		buildCohort(t, p, "flat.pulse", paritySchema(), CohortBuilderOptions{}, rows)
		if got := shardSizes(t, p, "arch.pulse"); len(got) != len(rows) {
			t.Fatalf("shard sizes %v, want one row per shard", got)
		}
		assertExportsMatch(t, p, fsys, "arch.pulse", "flat.pulse", len(rows))
	})

	t.Run("grouped", func(t *testing.T) {
		t.Parallel()
		p, fsys := memBuilderEngine(t)
		rows, _ := groupedData()
		groups := []pio.GroupDecl{custGroup}
		buildCohort(t, p, "arch.pulse", groupedSchema(), CohortBuilderOptions{Groups: groups, Shards: &ShardSplit{MaxRecords: 50}}, rows)
		buildCohort(t, p, "flat.pulse", groupedSchema(), CohortBuilderOptions{Groups: groups}, rows)
		c, err := p.Open(context.Background(), "arch.pulse")
		if err != nil {
			t.Fatal(err)
		}
		if !c.Schema().HasGroups() || len(c.Shards()) != 3 {
			t.Fatalf("archive grouped=%v shards=%d, want a grouped 3-shard archive", c.Schema().HasGroups(), len(c.Shards()))
		}
		assertExportsMatch(t, p, fsys, "arch.pulse", "flat.pulse", len(rows))
	})
}

// widenedSetArchive builds sets.pulse: an archive whose set_u8 tags
// field was widened to set_u16 by an appended shard, and flat.pulse,
// the single-file set_u16 cohort of the same rows.
func widenedSetArchive(t *testing.T) (*Pulse, afero.Fs, int) {
	t.Helper()
	label := func(c int) string { return string(rune('a' + c)) }
	var old, more, all []CohortRow
	for i := range 8 {
		old = append(old, CohortRow{uint64(i), []string{label(i)}})
		more = append(more, CohortRow{uint64(100 + i), []string{label(8 + i)}})
	}
	all = append(append(all, old...), more...)
	fsys := afero.NewMemMapFs()
	p, err := New(Options{FS: fsys, Extensions: Extensions{LabelTables: map[string]LabelTable{"codes": {Rows: map[string]string{"1": "one"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	buildCohort(t, p, "sets.pulse", setSchema(), CohortBuilderOptions{Shards: &ShardSplit{MaxRecords: 8}}, old)
	res := buildCohort(t, p, "sets.pulse#more.pulse", setSchema(), CohortBuilderOptions{}, more)
	if got := warningCodes(res.Warnings); len(got) != 1 || got[0] != errors.PULSE_SHARD_SET_WIDENED {
		t.Fatalf("warnings = %v, want one PULSE_SHARD_SET_WIDENED", res.Warnings)
	}
	wide := setSchema()
	wide.Fields[1].Type = encoding.FieldTypeSetU16
	buildCohort(t, p, "flat.pulse", wide, CohortBuilderOptions{}, all)
	return p, fsys, len(all)
}

// TestExportArchive_WidenedSetExportsCanonical: an archive whose set
// field widened across shards exports through the canonical set_u16
// schema — every member of every shard named, as the single-file set_u16
// twin names them — on every format.
func TestExportArchive_WidenedSetExportsCanonical(t *testing.T) {
	t.Parallel()

	p, fsys, n := widenedSetArchive(t)
	assertExportsMatch(t, p, fsys, "sets.pulse", "flat.pulse", n)

	csv, _ := archiveExportBytes(t, p, fsys, "sets.pulse", pio.FormatCSV)
	if !bytes.Contains(csv, []byte("\n0,a\n")) || !bytes.Contains(csv, []byte("\n107,p\n")) {
		t.Errorf("CSV export of the widened archive misses the appended shard's members:\n%s", csv)
	}
}

// TestExportArchive_SavAppliesCarriedSidecar: Materialize writes an
// archive with the source's SPSS sidecar carried beside it,
// fingerprinted over the archive's bytes; its `.sav` export honours that
// sidecar (no PULSE_SPSS_SIDECAR_ABSENT; variable names, value labels
// and the weight round-trip) and equals the single-file output's export
// byte for byte.
func TestExportArchive_SavAppliesCarriedSidecar(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	afs := afero.NewMemMapFs()
	p := importSav(t, afs, constantSavSpec(), Options{})
	if _, err := p.Materialize(ctx, withOutput(absentOutputRequest(), &types.MaterializeOutput{
		Shards: &types.MaterializeShards{MaxRecords: 16},
	}), "derived-arch.pulse", MaterializeOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Materialize(ctx, withOutput(absentOutputRequest(), &types.MaterializeOutput{}),
		"derived-flat.pulse", MaterializeOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := shardSizes(t, p, "derived-arch.pulse"); len(got) != 3 {
		t.Fatalf("shard sizes %v, want 3 shards", got)
	}

	a, arep := archiveExportBytes(t, p, afs, "derived-arch.pulse", pio.FormatSPSS)
	s, srep := archiveExportBytes(t, p, afs, "derived-flat.pulse", pio.FormatSPSS)
	if !bytes.Equal(a, s) {
		t.Errorf(".sav of the archive (%d bytes) differs from the single file's (%d bytes)", len(a), len(s))
	}
	if !slices.Equal(warningCodes(arep.TargetWarnings), warningCodes(srep.TargetWarnings)) {
		t.Errorf("target warnings archive %v, single file %v", arep.TargetWarnings, srep.TargetWarnings)
	}

	names, labels := savRoundTrip(t, p, afs, "derived-arch.pulse")
	want := []string{"ID", "Q1", "WT", "WAVE", "Q1_1", "Q1_2", "rn", "prev_wt"}
	if !slices.Equal(names, want) {
		t.Errorf("exported variables = %v, want %v", names, want)
	}
	if q1 := labels["Q1"]; !reflect.DeepEqual(q1, map[float64]string{1: "Low", 2: "High"}) {
		t.Errorf("Q1 value labels after the archive round trip = %v", q1)
	}
	insp, err := p.Inspect(ctx, "derived-arch.pulse.back.pulse")
	if err != nil {
		t.Fatal(err)
	}
	if insp.SuggestedWeight == nil || insp.SuggestedWeight.Field != "WT" {
		t.Errorf("re-imported archive export suggests %+v, want the carried weight WT", insp.SuggestedWeight)
	}
}

// predictView is the comparable part of an export predict.
type predictView struct {
	Fields   []string
	Types    []encoding.FieldType
	Rows     int
	Columns  []string
	Warnings []errors.Code
}

func predictExportView(t *testing.T, p *Pulse, fsys afero.Fs, cohort string, f pio.Format, labels []*types.LabelBinding) (predictView, error) {
	t.Helper()
	job := pio.NewExportJob(cohort, nil)
	if f != "" {
		w, err := pio.NewWriter(f, afero.NewMemMapFs(), "predict.out", pio.WriterOptions{})
		if err != nil {
			t.Fatal(err)
		}
		job.Target = w
	}
	job.FS = fsys
	job.Labels = labels
	rep, err := p.PredictExport(context.Background(), job)
	if err != nil {
		return predictView{}, err
	}
	v := predictView{Rows: rep.EstimatedRows, Columns: rep.Columns, Warnings: warningCodes(rep.TargetWarnings)}
	for _, fd := range rep.Schema.Fields {
		v.Fields = append(v.Fields, fd.Name)
		v.Types = append(v.Types, fd.Type)
	}
	return v, nil
}

// TestPredictExport_ArchiveMatchesSingleFile: the target-aware export
// predict reports the same schema (the canonical, widened one), row
// estimate, columns and target warnings for an archive as for its
// single-file twin — with no target, a row target and the `.sav`
// validator — and refuses a bad label binding with the same code.
func TestPredictExport_ArchiveMatchesSingleFile(t *testing.T) {
	t.Parallel()

	p, fsys, n := widenedSetArchive(t)
	for _, f := range []pio.Format{"", pio.FormatCSV, pio.FormatArrow, pio.FormatSPSS} {
		a, aerr := predictExportView(t, p, fsys, "sets.pulse", f, nil)
		s, serr := predictExportView(t, p, fsys, "flat.pulse", f, nil)
		if aerr != nil || serr != nil {
			t.Fatalf("%q: predict archive err %v, single file err %v", f, aerr, serr)
		}
		if !reflect.DeepEqual(a, s) {
			t.Errorf("%q: archive predict %+v, single file %+v", f, a, s)
		}
		if a.Rows != n || a.Types[1] != encoding.FieldTypeSetU16 {
			t.Errorf("%q: archive predict rows %d type %v, want %d rows of set_u16", f, a.Rows, a.Types[1], n)
		}
	}

	// A binding on the set field (not categorical) and on a field the
	// cohort lacks: refused by schema facts the archive must report
	// exactly as its single-file twin does.
	for _, bad := range [][]*types.LabelBinding{
		{{Field: "tags", Table: "codes"}},
		{{Field: "missing", Table: "codes"}},
	} {
		for _, f := range []pio.Format{pio.FormatCSV, pio.FormatSPSS} {
			_, aerr := predictExportView(t, p, fsys, "sets.pulse", f, bad)
			_, serr := predictExportView(t, p, fsys, "flat.pulse", f, bad)
			if aerr == nil || serr == nil || codeOf(aerr) != codeOf(serr) {
				t.Errorf("%s %v: binding refused archive %v, single file %v — want the same code", f, bad[0], aerr, serr)
			}
		}
	}
}

// rezipArchive rewrites the archive at path through archive/zip, entry
// by entry in order: replace swaps an entry's payload, deflate stores
// the named entry compressed.
func rezipArchive(t *testing.T, fsys afero.Fs, path string, replace map[string][]byte, deflate string) {
	t.Helper()
	src := readFile(t, fsys, path)
	zr, err := zip.NewReader(bytes.NewReader(src), int64(len(src)))
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range zr.File {
		payload, ok := replace[f.Name]
		if !ok {
			rc, err := f.Open()
			if err != nil {
				t.Fatal(err)
			}
			if payload, err = io.ReadAll(rc); err != nil {
				t.Fatal(err)
			}
			_ = rc.Close()
		}
		m := zip.Store
		if f.Name == deflate {
			m = zip.Deflate
		}
		w, err := zw.CreateHeader(&zip.FileHeader{Name: f.Name, Method: m})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(payload); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := afero.WriteFile(fsys, path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestExportArchive_RefusesUnreadableShards: an export of an archive
// whose shard the canonical schema cannot decode — a dictionary longer
// than the canonical one — is refused with PULSE_SHARD_DICT_DIVERGENCE,
// and one whose shard entry is deflated with PULSE_ARCHIVE_CORRUPT;
// both name the shard on details.entry and export no row.
func TestExportArchive_RefusesUnreadableShards(t *testing.T) {
	t.Parallel()

	rows := []CohortRow{{uint64(1), []string{"a"}}, {uint64(2), []string{"b"}}}
	for _, tc := range []struct {
		name string
		code errors.Code
		bad  func(t *testing.T, p *Pulse, fsys afero.Fs, shard string)
	}{
		{"dictionary divergence", errors.PULSE_SHARD_DICT_DIVERGENCE, func(t *testing.T, p *Pulse, fsys afero.Fs, shard string) {
			// The same layout, a dictionary one label past the canonical.
			buildCohort(t, p, "wide.pulse", setSchema(), CohortBuilderOptions{},
				[]CohortRow{{uint64(1), []string{"a"}}, {uint64(2), []string{"b"}}, {uint64(3), []string{"c"}}})
			rezipArchive(t, fsys, "arch.pulse", map[string][]byte{shard: readFile(t, fsys, "wide.pulse")}, "")
		}},
		{"deflated shard", errors.PULSE_ARCHIVE_CORRUPT, func(t *testing.T, _ *Pulse, fsys afero.Fs, shard string) {
			rezipArchive(t, fsys, "arch.pulse", nil, shard)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p, fsys := memBuilderEngine(t)
			buildCohort(t, p, "arch.pulse", setSchema(), CohortBuilderOptions{Shards: &ShardSplit{MaxRecords: 1}}, rows)
			arts, err := p.ListShards(context.Background(), "arch.pulse")
			if err != nil || len(arts) != 2 {
				t.Fatalf("shards %v (%v), want 2", arts, err)
			}
			shard := arts[1].Filename
			tc.bad(t, p, fsys, shard)

			w, err := pio.NewWriter(pio.FormatCSV, fsys, "out.csv", pio.WriterOptions{})
			if err != nil {
				t.Fatal(err)
			}
			job := pio.NewExportJob("arch.pulse", w)
			job.FS = fsys
			rep, err := p.Export(context.Background(), job)
			_ = w.Close()
			var ce *errors.CodedError
			if !stderrors.As(err, &ce) || ce.Code != tc.code {
				t.Fatalf("export = %v (report %+v), want %s", err, rep, tc.code)
			}
			if ce.Details["entry"] != shard {
				t.Errorf("details.entry = %v, want %q", ce.Details["entry"], shard)
			}
			if rep != nil && rep.RowsExported != 0 {
				t.Errorf("exported %d rows before the refusal", rep.RowsExported)
			}
		})
	}
}
