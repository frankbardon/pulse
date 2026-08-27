package spss

import (
	"bytes"
	"context"
	"math"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/spsstest"
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// ---------------------------------------------------------------------------
// The interfaces the dispatch keys off
// ---------------------------------------------------------------------------

// TestWriter_SatisfiesTheOptionalInterfaces is the mounting claim. Every
// one of these is a dispatch decision made by a type assertion somewhere
// in io/, so a Writer that quietly stopped satisfying one would not fail
// to compile — it would take a different, wrong path.
func TestWriter_SatisfiesTheOptionalInterfaces(t *testing.T) {
	var w any = NewWriter(afero.NewMemMapFs(), "out.sav", WriterOptions{})
	for name, ok := range map[string]bool{
		"pio.Writer":               func() bool { _, ok := w.(pio.Writer); return ok }(),
		"pio.SchemaAwareWriter":    func() bool { _, ok := w.(pio.SchemaAwareWriter); return ok }(),
		"pio.CohortWriter":         func() bool { _, ok := w.(pio.CohortWriter); return ok }(),
		"pio.TargetWarningEmitter": func() bool { _, ok := w.(pio.TargetWarningEmitter); return ok }(),
		"pio.OverlayAwareWriter":   func() bool { _, ok := w.(pio.OverlayAwareWriter); return ok }(),
	} {
		if !ok {
			t.Errorf("the .sav Writer does not satisfy %s", name)
		}
	}
}

// ---------------------------------------------------------------------------
// The cohort path, end to end through ExportJob
// ---------------------------------------------------------------------------

// TestExportJob_SPSS_RoundTripsThroughTheWholeAdapterPath is the story's
// central claim at the seam it is mounted on: a real ExportJob against a
// real cohort produces a `.sav` that reads back with the same values.
//
// It goes through pio.ExportJob rather than calling the encoder directly
// because the encoder was already tested in E5-S3. What is new here is the
// DISPATCH — that ExportJob hands the writer the cohort, skips its own row
// loop, and that Close lands the bytes.
func TestExportJob_SPSS_RoundTripsThroughTheWholeAdapterPath(t *testing.T) {
	fs, cohort, _ := importFixture(t, richSpec())

	w := NewWriter(fs, "out.sav", WriterOptions{})
	job := pio.NewExportJob(cohort, w)
	job.FS = fs

	report, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("ExportJob.Run: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if report.RowsExported == 0 {
		t.Fatal("RowsExported = 0; the writer reported no cases")
	}

	// The emitted file must be the same bytes the tested encoder path
	// produces for the same cohort and options. Anything else means the
	// adapter is doing its own thing on the way through.
	want := exportCohort(t, fs, cohort, WriterOptions{})
	got, err := afero.ReadFile(fs, "out.sav")
	if err != nil {
		t.Fatalf("reading emitted file: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("the adapter emitted %d bytes, the encoder path emits %d; they must be the same file",
			len(got), len(want))
	}
	if len(w.Bytes()) != len(got) {
		t.Errorf("Bytes() = %d bytes, the written file is %d", len(w.Bytes()), len(got))
	}
}

// TestExportJob_SPSS_NeverCallsWriteRow pins the control-flow half of
// pio.CohortWriter. A dispatcher that called both would decode the cohort
// twice and — worse — hand the writer rendered text it must not encode
// from.
func TestExportJob_SPSS_NeverCallsWriteRow(t *testing.T) {
	fs, cohort, _ := importFixture(t, richSpec())

	w := &countingWriter{Writer: NewWriter(fs, "out.sav", WriterOptions{})}
	job := pio.NewExportJob(cohort, w)
	job.FS = fs
	if _, err := job.Run(context.Background()); err != nil {
		t.Fatalf("ExportJob.Run: %v", err)
	}
	if w.rows != 0 {
		t.Errorf("WriteRow was called %d time(s); a pio.CohortWriter must take the place of the row loop, not run beside it", w.rows)
	}
	if w.headers != 1 {
		t.Errorf("WriteHeader called %d time(s), want exactly 1", w.headers)
	}
	if w.schemas != 1 {
		t.Errorf("SetPulseSchema called %d time(s), want exactly 1 — before WriteHeader", w.schemas)
	}
	if w.schemaAfterHeader {
		t.Error("SetPulseSchema arrived AFTER WriteHeader; the contract is before")
	}
}

// countingWriter records the dispatch calls a real writer receives. It
// embeds the real Writer so the export still succeeds and the assertions
// are about a working path rather than a stub.
type countingWriter struct {
	*Writer
	rows              int
	headers           int
	schemas           int
	schemaAfterHeader bool
}

func (c *countingWriter) SetPulseSchema(s *encoding.Schema) {
	c.schemas++
	if c.headers > 0 {
		c.schemaAfterHeader = true
	}
	c.Writer.SetPulseSchema(s)
}

func (c *countingWriter) WriteHeader(cols []string) error {
	c.headers++
	return c.Writer.WriteHeader(cols)
}

func (c *countingWriter) WriteRow(v []any) error {
	c.rows++
	return c.Writer.WriteRow(v)
}

// TestExportJob_SPSS_SidecarAbsentWarns covers the diagnostic that has to
// reach a caller, through the slot it reaches them by. A cohort with no
// SPSS provenance exports fine — that is the whole point of the absent /
// stale split — but the file it produces is a SYNTHESIS, and a user who
// cannot tell that apart from a faithful re-emission has been misled.
func TestExportJob_SPSS_SidecarAbsentWarns(t *testing.T) {
	fs, cohort := plainCohort(t)

	w := NewWriter(fs, "out.sav", WriterOptions{})
	job := pio.NewExportJob(cohort, w)
	job.FS = fs
	report, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("ExportJob.Run: %v", err)
	}
	if !hasCode(report.TargetWarnings, perr.PULSE_SPSS_SIDECAR_ABSENT) {
		t.Errorf("ExportReport.TargetWarnings = %v, want a PULSE_SPSS_SIDECAR_ABSENT", codes(report.TargetWarnings))
	}
}

// TestExportJob_SPSS_IgnoreSidecarWarns is the other half: the flag is an
// escape hatch, not a silencer.
func TestExportJob_SPSS_IgnoreSidecarWarns(t *testing.T) {
	fs, cohort, _ := importFixture(t, plainSpec())

	w := NewWriter(fs, "out.sav", WriterOptions{IgnoreSidecar: true})
	job := pio.NewExportJob(cohort, w)
	job.FS = fs
	if _, err := job.Run(context.Background()); err != nil {
		t.Fatalf("ExportJob.Run: %v", err)
	}
	if !hasCode(w.Warnings(), perr.PULSE_SPSS_SIDECAR_IGNORED) {
		t.Errorf("Warnings() = %v, want a PULSE_SPSS_SIDECAR_IGNORED", codes(w.Warnings()))
	}
}

// TestExportJob_SPSS_IgnoreSidecarCannotRoundTripADerivedSet pins the
// first of the two landmines --ignore-sidecar carries, so it stays a
// documented refusal rather than becoming a documented silence.
//
// A multiple-dichotomy import leaves a DERIVED `set_*` convenience column
// beside its constituents, and that column's dictionary entries ARE the
// constituents' names. With the sidecar suppressed there is no derived
// registry to fold it back, so synthesis mints indicator variables MD1 and
// MD2 from the set column beside the real MD1 and MD2 — four variables,
// two names. Before E5-S5 that emitted a file with two variables of each
// name and said nothing.
//
// The fix is not to make this work: it is to export WITHOUT the flag, so
// the sidecar's derived registry consumes the column.
func TestExportJob_SPSS_IgnoreSidecarCannotRoundTripADerivedSet(t *testing.T) {
	fs, cohort, _ := importFixture(t, richSpec())

	w := NewWriter(fs, "out.sav", WriterOptions{IgnoreSidecar: true})
	job := pio.NewExportJob(cohort, w)
	job.FS = fs
	_, err := job.Run(context.Background())
	ce := codedErr(t, err)
	if ce.Code != perr.PULSE_SPSS_NAME_COLLISION {
		t.Errorf("code = %s, want %s", ce.Code, perr.PULSE_SPSS_NAME_COLLISION)
	}

	// Without the flag the same cohort exports cleanly — which is what
	// makes the refusal a property of the flag rather than of the cohort.
	clean := NewWriter(fs, "clean.sav", WriterOptions{})
	cleanJob := pio.NewExportJob(cohort, clean)
	cleanJob.FS = fs
	if _, err := cleanJob.Run(context.Background()); err != nil {
		t.Fatalf("the same cohort must export without --ignore-sidecar: %v", err)
	}
}

// TestExportJob_SPSS_RefusesProjectionAndLabels is the honesty check on
// the two ExportJob slots a cohort writer cannot honour. Emitting every
// column in answer to --include would be a silent wrong answer.
func TestExportJob_SPSS_RefusesProjectionAndLabels(t *testing.T) {
	fs, cohort, _ := importFixture(t, richSpec())

	t.Run("include", func(t *testing.T) {
		w := NewWriter(fs, "out.sav", WriterOptions{})
		job := pio.NewExportJob(cohort, w)
		job.FS = fs
		job.Includes = []string{"REGION"}
		_, err := job.Run(context.Background())
		ce := codedErr(t, err)
		if ce.Code != perr.PULSE_SPSS_EXPORT_UNSUPPORTED {
			t.Errorf("code = %s, want %s", ce.Code, perr.PULSE_SPSS_EXPORT_UNSUPPORTED)
		}
		if !strings.Contains(ce.Message, "--include") {
			t.Errorf("message %q does not name the option that was refused", ce.Message)
		}
	})

	t.Run("labels", func(t *testing.T) {
		w := NewWriter(fs, "out.sav", WriterOptions{})
		job := pio.NewExportJob(cohort, w)
		job.FS = fs
		job.LabelResolver = nopResolver{}
		_, err := job.Run(context.Background())
		ce := codedErr(t, err)
		if ce.Code != perr.PULSE_SPSS_EXPORT_UNSUPPORTED {
			t.Errorf("code = %s, want %s", ce.Code, perr.PULSE_SPSS_EXPORT_UNSUPPORTED)
		}
	})
}

// nopResolver is a LabelResolver that binds nothing; its presence alone is
// what the writer refuses on, because ExportJob.Run applies it to the row
// stream the cohort path never produces.
type nopResolver struct{}

func (nopResolver) Has(string) bool                             { return false }
func (nopResolver) Mode(string) types.LabelMode                 { return "" }
func (nopResolver) Apply(string, string) (string, string, bool) { return "", "", false }
func (nopResolver) FieldsWithAugment() []string                 { return nil }
func (nopResolver) Warnings() []pio.LabelWarning                { return nil }

// TestWriter_Uncompressed pins that the flag reaches the encoder rather
// than merely existing on the struct.
func TestWriter_Uncompressed(t *testing.T) {
	fs, cohort, _ := importFixture(t, richSpec())

	for _, tt := range []struct {
		name string
		opts WriterOptions
	}{
		{"bytecode", WriterOptions{}},
		{"uncompressed", WriterOptions{Uncompressed: true}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w := NewWriterToBuffer(tt.opts)
			if _, err := w.WriteCohort(context.Background(), pio.CohortSource{FS: fs, Path: cohort}); err != nil {
				t.Fatalf("WriteCohort: %v", err)
			}
			d, err := parseDictionary(w.Bytes())
			if err != nil {
				t.Fatalf("re-parsing the emitted file: %v", err)
			}
			want := tt.opts.Compression()
			if d.header.compression != want {
				t.Errorf("emitted compression flag = %d, want %d", d.header.compression, want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// The row path
// ---------------------------------------------------------------------------

// TestWriter_RowPathBuildsACohort is `pulse convert data.csv out.sav` at
// the writer level: header, rows, Close, and a readable `.sav` out the
// other end. There is no cohort behind it, so the writer must build one.
func TestWriter_RowPathBuildsACohort(t *testing.T) {
	fs := afero.NewMemMapFs()
	w := NewWriter(fs, "out.sav", WriterOptions{})
	if err := w.WriteHeader([]string{"AGE", "REGION"}); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	for _, row := range [][]any{{"31", "north"}, {"44", "south"}, {"29", "north"}} {
		if err := w.WriteRow(row); err != nil {
			t.Fatalf("WriteRow: %v", err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	d, err := parseDictionary(w.Bytes())
	if err != nil {
		t.Fatalf("the emitted file does not parse: %v", err)
	}
	if len(d.vars) != 2 {
		t.Fatalf("emitted %d variables, want 2", len(d.vars))
	}
	// A CSV has no sidecar and never had one; the dictionary is a
	// synthesis and must say so.
	if !hasCode(w.Warnings(), perr.PULSE_SPSS_SIDECAR_ABSENT) {
		t.Errorf("Warnings() = %v, want a PULSE_SPSS_SIDECAR_ABSENT — the row path synthesises", codes(w.Warnings()))
	}
}

// TestWriter_RowPathWithNoRowsRefuses. A zero-row source gives inference
// nothing, so the schema would be invented outright; a `.sav` full of
// invented variables is worse than no file.
func TestWriter_RowPathWithNoRowsRefuses(t *testing.T) {
	w := NewWriterToBuffer(WriterOptions{})
	if err := w.WriteHeader([]string{"A"}); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	ce := codedErr(t, w.Close())
	if ce.Code != perr.PULSE_SPSS_EXPORT_UNSUPPORTED {
		t.Errorf("code = %s, want %s", ce.Code, perr.PULSE_SPSS_EXPORT_UNSUPPORTED)
	}
}

// TestWriter_CloseIsIdempotent. The CLI calls Close after the job, and a
// second Close must not re-encode a buffered row set into a second file.
func TestWriter_CloseIsIdempotent(t *testing.T) {
	fs, cohort, _ := importFixture(t, richSpec())
	w := NewWriter(fs, "out.sav", WriterOptions{})
	if _, err := w.WriteCohort(context.Background(), pio.CohortSource{FS: fs, Path: cohort}); err != nil {
		t.Fatalf("WriteCohort: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	first, _ := afero.ReadFile(fs, "out.sav")
	if err := w.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	second, _ := afero.ReadFile(fs, "out.sav")
	if string(first) != string(second) {
		t.Error("a second Close changed the emitted file")
	}
}

// ---------------------------------------------------------------------------
// Overlays
// ---------------------------------------------------------------------------

// TestWriter_OverlaysWarnAndSkip pins the manifest's claim. The capability
// block says "warn_and_skip" for spss; that must be true of the adapter.
func TestWriter_OverlaysWarnAndSkip(t *testing.T) {
	w := NewWriterToBuffer(WriterOptions{})
	if got := w.OverlayWarnings(); got != nil {
		t.Errorf("OverlayWarnings() = %v with no layers recorded, want nil", codes(got))
	}
	w.SetOverlays(overlayPair())
	warns := w.OverlayWarnings()
	if len(warns) != 1 {
		t.Fatalf("OverlayWarnings() = %d, want exactly 1", len(warns))
	}
	if warns[0].Code != perr.PULSE_OVERLAY_EXPORT_CSV_UNSUPPORTED {
		t.Errorf("code = %s, want %s", warns[0].Code, perr.PULSE_OVERLAY_EXPORT_CSV_UNSUPPORTED)
	}
	if warns[0].Details["layer_count"] != 2 {
		t.Errorf("details[layer_count] = %v, want 2", warns[0].Details["layer_count"])
	}
}

// ---------------------------------------------------------------------------
// Shared helpers
// ---------------------------------------------------------------------------

// plainCohort writes a cohort that never went through SPSS, which is the
// case the absent-sidecar warning exists for.
func plainCohort(t *testing.T) (afero.Fs, string) {
	t.Helper()
	fs := afero.NewMemMapFs()
	s := &encoding.Schema{Fields: []encoding.Field{
		{Name: "ID", Type: encoding.FieldTypeU32},
		{Name: "SCORE", Type: encoding.FieldTypeF64},
	}}
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := encoding.WriteSchema(&buf, s); err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}
	for _, rec := range [][]uint64{{1, math.Float64bits(1.5)}, {2, math.Float64bits(2.5)}} {
		for i, v := range rec {
			if err := encoding.WriteFieldValue(&buf, s.Fields[i].Type, v); err != nil {
				t.Fatalf("WriteFieldValue: %v", err)
			}
		}
	}
	if err := afero.WriteFile(fs, "plain.pulse", buf.Bytes(), 0644); err != nil {
		t.Fatalf("writing cohort: %v", err)
	}
	return fs, "plain.pulse"
}

// plainSpec is a `.sav` with no multiple-response sets and no derived
// columns — the shape that isolates a question from the MD fold.
func plainSpec() spsstest.Spec {
	return spsstest.Spec{
		Vars: []spsstest.Var{
			{Name: "AGE", Print: spsstest.Format{Type: spsstest.FormatF, Width: 3}},
			{Name: "REGION", Width: 6, Measure: spsstest.MeasureNominal},
		},
		Cases: [][]spsstest.Value{
			{spsstest.Num(31), spsstest.Text("North")},
			{spsstest.Num(44), spsstest.Text("South")},
		},
		DisplayParams:     true,
		CharacterEncoding: "UTF-8",
	}
}

// overlayPair is two minimal overlay layers — enough for the warn-and-skip
// surface, whose only question is "were any handed over".
func overlayPair() []*types.OverlayLayer {
	return []*types.OverlayLayer{
		{Name: "a", Kind: types.OverlayKindFormula},
		{Name: "b", Kind: types.OverlayKindFormula},
	}
}

func codes(warns []*perr.CodedError) []string {
	out := make([]string, 0, len(warns))
	for _, w := range warns {
		if w != nil {
			out = append(out, string(w.Code))
		}
	}
	return out
}
