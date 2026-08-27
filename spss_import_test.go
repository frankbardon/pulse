package pulse

import (
	"bytes"
	"context"
	"testing"

	"github.com/frankbardon/pulse/internal/spsstest"
	pio "github.com/frankbardon/pulse/io"
	pformat "github.com/frankbardon/pulse/io/format"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// seedSav writes a built `.sav` fixture at path on afs and returns the
// bytes, so a caller can assert against the source as well as the
// cohort.
func seedSav(t *testing.T, afs afero.Fs, path string, spec spsstest.Spec) []byte {
	t.Helper()
	raw, err := spsstest.Build(spec)
	if err != nil {
		t.Fatalf("spsstest.Build: %v", err)
	}
	if err := afero.WriteFile(afs, path, raw, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return raw
}

// TestSPSS_ImportProducesQueryableCohort is the story's end-to-end
// criterion, taken through the same facade the CLI leaf calls. It
// deliberately spans every seam the registration touches — extension
// detection, reader construction, the authoritative-schema bypass, the
// .pulse write, and a real query against the result — because each of
// those was individually testable before this story and the file was
// still unreachable from a command.
func TestSPSS_ImportProducesQueryableCohort(t *testing.T) {
	afs := afero.NewMemMapFs()
	seedSav(t, afs, "survey.sav", spsstest.ReferenceSpec())

	p, err := New(Options{FS: afs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := context.Background()

	// No explicit format: detection from the .sav extension is the
	// registration under test.
	res, err := p.ImportFile(ctx, ImportSpec{SourcePath: "survey.sav"})
	if err != nil {
		t.Fatalf("ImportFile: %v", err)
	}
	// Result.Format is the MANAGED handle's format (always pulse); the
	// detected source format lives on the sidecar the pool records.
	entries, err := p.Imports(ctx)
	if err != nil {
		t.Fatalf("Imports: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("managed entries = %d, want 1", len(entries))
	}
	if entries[0].Sidecar.SourceFormat != pformat.SPSS {
		t.Errorf("SourceFormat = %q, want %q — the .sav extension did not resolve through io/format", entries[0].Sidecar.SourceFormat, pformat.SPSS)
	}
	if !res.Managed {
		t.Errorf("Managed = false; a .sav import must produce a managed handle")
	}
	if res.RowsImported != 2 {
		t.Errorf("RowsImported = %d, want 2", res.RowsImported)
	}

	// Inspect proves the cohort is a real .pulse file with the schema
	// the SPSS dictionary declared — not one inference reconstructed.
	info, err := p.Inspect(ctx, res.Path)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	byName := map[string]string{}
	for _, f := range info.Fields {
		byName[f.Name] = f.Type
	}
	// ID is F8 numeric: f64 by the no-integer-narrowing rule, which a
	// range probe over the values {1, 2} would have narrowed to u8.
	if got := byName["ID"]; got != "f64" {
		t.Errorf("ID type = %q, want f64 (inference over {1,2} would say u8)", got)
	}
	// SEX carries value labels, so it maps categorical.
	if got := byName["SEX"]; got != "categorical_u8" {
		t.Errorf("SEX type = %q, want categorical_u8", got)
	}

	// And the cohort answers a real request.
	resp, err := p.Process(ctx, &Request{
		Cohort: &types.Cohort{Filename: res.Path},
		Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "SEX"}},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_COUNT, Label: "n"},
		},
	})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(resp.Data) == 0 {
		t.Fatalf("Process returned no rows for an imported .sav cohort")
	}
}

// TestSPSS_CompressedAndUncompressedProduceIdenticalCohorts is E3-S1's
// acceptance criterion taken all the way to the artefact a user keeps.
//
// The two sources are one spec built twice, so they carry the same logical
// cases through three genuinely different data-section encodings — a flat run
// of doubles, a bytecode command stream, and that same stream deflated into
// zlib blocks behind a ZHEADER / ZTRAILER index. Comparing the resulting `.pulse`
// files BYTE for byte, rather than comparing rendered rows, is what makes the
// claim total: it covers the schema block, the inline categorical
// dictionaries and their entry ORDER, the record data and the null bitmap all
// at once, and no rounding or rendering rule can hide a difference inside a
// cell.
//
// It is the strongest test in the story because the encoder it checks against
// lives in internal/spsstest and shares no code with the decoder — a
// misreading of the specification would have to be made twice, identically,
// in two places, to pass.
func TestSPSS_CompressedAndUncompressedProduceIdenticalCohorts(t *testing.T) {
	specs := []struct {
		name string
		spec spsstest.Spec
	}{
		{"the reference fixture", spsstest.ReferenceSpec()},
		{"the extension fixture", spsstest.ExtensionReferenceSpec()},
		{"a non-conventional compression bias", func() spsstest.Spec {
			s := spsstest.ReferenceSpec()
			s.CompressionBias = 37
			return s
		}()},
	}
	for _, tc := range specs {
		t.Run(tc.name, func(t *testing.T) {
			afs := afero.NewMemMapFs()

			plainSpec := tc.spec
			plainSpec.Compression = spsstest.CompressionNone
			plainSrc := seedSav(t, afs, "plain.sav", plainSpec)

			packedSpec := tc.spec
			packedSpec.Compression = spsstest.CompressionBytecode
			packedSrc := seedSav(t, afs, "packed.sav", packedSpec)

			// The ZSAV arm cuts the stream into small blocks so the
			// index spans several of them: at the conventional
			// 0x3ff000 every fixture here is one block, and a
			// one-block index exercises none of the cumulative
			// offset arithmetic the decoder has to get right.
			zsavSpec := tc.spec
			zsavSpec.Compression = spsstest.CompressionZSAV
			zsavSpec.ZSAVBlockSize = 16
			zsavSrc := seedSav(t, afs, "zipped.zsav", zsavSpec)

			// The premise: the three sources really are different
			// files. Without this the byte-equality below would be
			// trivial.
			if bytes.Equal(plainSrc, packedSrc) {
				t.Fatal("the uncompressed and bytecode .sav sources are byte-identical; the compressed one is not exercising the decoder")
			}
			if bytes.Equal(packedSrc, zsavSrc) || bytes.Equal(plainSrc, zsavSrc) {
				t.Fatal("the .zsav source is byte-identical to a .sav twin; it is not exercising the ZSAV decoder")
			}

			p, err := New(Options{FS: afs})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			ctx := context.Background()

			convert := func(src, dst string) {
				t.Helper()
				reader, err := pformat.NewReader(pformat.SPSS, afs, src, pformat.ReaderOptions{})
				if err != nil {
					t.Fatalf("NewReader(%s): %v", src, err)
				}
				job := pio.NewImportJob(reader, dst)
				if _, err := p.Import(ctx, job); err != nil {
					t.Fatalf("Import(%s): %v", src, err)
				}
			}
			convert("plain.sav", "plain.pulse")
			convert("packed.sav", "packed.pulse")
			convert("zipped.zsav", "zipped.pulse")

			read := func(path string) []byte {
				t.Helper()
				b, err := afero.ReadFile(afs, path)
				if err != nil {
					t.Fatalf("read %s: %v", path, err)
				}
				return b
			}
			plainOut := read("plain.pulse")
			for _, other := range []struct{ name, path string }{
				{"bytecode", "packed.pulse"},
				{"ZSAV", "zipped.pulse"},
			} {
				got := read(other.path)
				if !bytes.Equal(plainOut, got) {
					t.Errorf("the cohorts differ: %d bytes from the uncompressed source, %d from the %s one",
						len(plainOut), len(got), other.name)
				}
			}
		})
	}
}

// TestSPSS_ZsavExtensionImportsThroughTheFacade is the `.zsav` half of the
// end-to-end criterion. The extension is what a user actually types, and it
// resolves to the same reader — so a `.zsav` must import through the plain
// facade call with no format override, no re-save and no special handling.
//
// The E3-S1 seam is what this replaces: until now a `.zsav` reached the
// reader, parsed its dictionary and then failed at the data section.
func TestSPSS_ZsavExtensionImportsThroughTheFacade(t *testing.T) {
	afs := afero.NewMemMapFs()
	spec := spsstest.ReferenceSpec()
	spec.Compression = spsstest.CompressionZSAV
	raw := seedSav(t, afs, "survey.zsav", spec)
	if string(raw[:4]) != "$FL3" {
		t.Fatalf("the fixture opens with %q, not the ZSAV magic $FL3", raw[:4])
	}

	p, err := New(Options{FS: afs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := context.Background()

	res, err := p.ImportFile(ctx, ImportSpec{SourcePath: "survey.zsav"})
	if err != nil {
		t.Fatalf("ImportFile(.zsav): %v", err)
	}
	if res.RowsImported != 2 {
		t.Errorf("RowsImported = %d, want 2", res.RowsImported)
	}

	entries, err := p.Imports(ctx)
	if err != nil {
		t.Fatalf("Imports: %v", err)
	}
	if len(entries) != 1 || entries[0].Sidecar.SourceFormat != pformat.SPSS {
		t.Fatalf("the .zsav extension did not resolve through io/format: %+v", entries)
	}

	// The dictionary still reaches the cohort intact through the extra
	// layer: a ZSAV that inflated to the wrong bytes would show up here
	// as a mistyped or dictionary-less field long before it showed up as
	// a wrong number.
	info, err := p.Inspect(ctx, res.Path)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	byName := map[string]string{}
	for _, f := range info.Fields {
		byName[f.Name] = f.Type
	}
	if got := byName["SEX"]; got != "categorical_u8" {
		t.Errorf("SEX type = %q, want categorical_u8", got)
	}
	if got := byName["ID"]; got != "f64" {
		t.Errorf("ID type = %q, want f64", got)
	}
}

// TestSPSS_ImportKeepsDictionaryCodesNotLabels pins the E2-S6 decision
// at the cohort level, because it is the one an analyst is most likely
// to be surprised by. Two SPSS codes may share a value label, so a
// label-keyed dictionary would collapse them and lose the code; the
// dictionary therefore holds the numeric CODES and labels are an
// output-time LabelTable concern.
func TestSPSS_ImportKeepsDictionaryCodesNotLabels(t *testing.T) {
	afs := afero.NewMemMapFs()
	seedSav(t, afs, "survey.sav", spsstest.ReferenceSpec())

	p, err := New(Options{FS: afs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := context.Background()
	res, err := p.ImportFile(ctx, ImportSpec{SourcePath: "survey.sav"})
	if err != nil {
		t.Fatalf("ImportFile: %v", err)
	}
	info, err := p.Inspect(ctx, res.Path)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}

	var sexValues []string
	for _, f := range info.Fields {
		if f.Name == "SEX" && f.Dictionary != nil {
			sexValues = f.Dictionary.Values
		}
	}
	if len(sexValues) == 0 {
		t.Fatalf("SEX carries no dictionary values; inspect fields = %+v", info.Fields)
	}
	for _, v := range sexValues {
		if v == "Male" || v == "Female" {
			t.Errorf("SEX dictionary contains the LABEL %q; it must hold the SPSS numeric codes so two codes sharing a label cannot collapse", v)
		}
	}
	if sexValues[0] != "1" {
		t.Errorf("SEX dictionary[0] = %q, want %q — entry ORDER is the on-wire encoding and must follow the source", sexValues[0], "1")
	}
}

// TestSPSS_ManagedImportSurfacesSourceWarnings covers the OTHER import
// entry point. `pulse import auto`, `Pulse.ImportFile` and the
// pulse_import MCP tool all go through imports.Manager, which builds
// its own Result rather than returning io.ImportReport — so the
// warnings channel has to be threaded twice or an LLM calling
// pulse_import sees nothing.
func TestSPSS_ManagedImportSurfacesSourceWarnings(t *testing.T) {
	afs := afero.NewMemMapFs()
	spec := spsstest.ReferenceSpec()
	spec.RawExtensions = []spsstest.RawExtension{
		{Subtype: 424242, Size: 1, Payload: []byte("unknowable")},
	}
	seedSav(t, afs, "survey.sav", spec)

	p, err := New(Options{FS: afs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	res, err := p.ImportFile(context.Background(), ImportSpec{SourcePath: "survey.sav"})
	if err != nil {
		t.Fatalf("ImportFile: %v", err)
	}
	if len(res.SourceWarnings) == 0 {
		t.Fatal("ImportResult.SourceWarnings is empty; the managed-import path drops the adapter's diagnostics")
	}
	if res.SourceWarnings[0].Code != "PULSE_SPSS_EXTENSION_UNKNOWN" {
		t.Errorf("code = %s, want PULSE_SPSS_EXTENSION_UNKNOWN", res.SourceWarnings[0].Code)
	}
}

// TestSPSS_ManagedImportCleanFileHasNoWarnings pins the degrade path:
// the slot is omitempty, so a clean import's wire shape is unchanged
// from before the channel existed — and every non-SPSS format reaches
// this same code.
func TestSPSS_ManagedImportCleanFileHasNoWarnings(t *testing.T) {
	afs := afero.NewMemMapFs()
	seedSav(t, afs, "survey.sav", spsstest.ReferenceSpec())

	p, err := New(Options{FS: afs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	res, err := p.ImportFile(context.Background(), ImportSpec{SourcePath: "survey.sav"})
	if err != nil {
		t.Fatalf("ImportFile: %v", err)
	}
	if len(res.SourceWarnings) != 0 {
		t.Errorf("SourceWarnings = %v, want none for a clean fixture", res.SourceWarnings)
	}
}

// TestSPSS_ConvertUsesSourceDictionary is the fidelity gap this story
// closed. `pulse convert survey.sav out.csv` is reachable the moment
// FromExt maps the extension, and before ConvertJob consulted
// SchemaAwareReader it re-inferred every type from the text the reader
// rendered — throwing the dictionary away through a command the
// registration itself created.
func TestSPSS_ConvertUsesSourceDictionary(t *testing.T) {
	afs := afero.NewMemMapFs()
	seedSav(t, afs, "survey.sav", spsstest.ReferenceSpec())

	reader, err := pformat.NewReader(pformat.SPSS, afs, "survey.sav", pformat.ReaderOptions{})
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	job := pio.NewConvertJob(reader, &discardWriter{})
	job.FS = afs

	report, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("ConvertJob.Run: %v", err)
	}
	if report.RowsConverted != 2 {
		t.Errorf("RowsConverted = %d, want 2", report.RowsConverted)
	}
	byName := map[string]string{}
	for _, f := range report.Schema.Fields {
		byName[f.Name] = f.Type.String()
	}
	if got := byName["ID"]; got != "f64" {
		t.Errorf("convert schema ID = %q, want f64; the source dictionary was re-inferred away", got)
	}
	if got := byName["SEX"]; got != "categorical_u8" {
		t.Errorf("convert schema SEX = %q, want categorical_u8", got)
	}
}

// TestSPSS_ConvertPredictUsesSourceDictionary covers the no-execute
// half. A predict that disagreed with its own run would be worse than
// no predict at all.
func TestSPSS_ConvertPredictUsesSourceDictionary(t *testing.T) {
	afs := afero.NewMemMapFs()
	seedSav(t, afs, "survey.sav", spsstest.ReferenceSpec())

	reader, err := pformat.NewReader(pformat.SPSS, afs, "survey.sav", pformat.ReaderOptions{})
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	job := pio.NewConvertJob(reader, &discardWriter{})
	job.FS = afs

	report, err := job.Predict(context.Background())
	if err != nil {
		t.Fatalf("ConvertJob.Predict: %v", err)
	}
	if report.EstimatedRows != 2 {
		t.Errorf("EstimatedRows = %d, want 2", report.EstimatedRows)
	}
	if got := report.Schema.Fields[0].Type.String(); got != "f64" {
		t.Errorf("predicted ID type = %q, want f64", got)
	}
}

// TestSPSS_ImportSurfacesSourceWarnings proves the diagnostics channel
// reaches a report. The fixture carries a deliberately unrecognised
// record type 7 subtype, which the parser tolerates by design — and
// which, before this story, no caller could ever have learned about.
func TestSPSS_ImportSurfacesSourceWarnings(t *testing.T) {
	afs := afero.NewMemMapFs()
	spec := spsstest.ReferenceSpec()
	spec.RawExtensions = []spsstest.RawExtension{
		{Subtype: 424242, Size: 1, Payload: []byte("unknowable")},
	}
	seedSav(t, afs, "survey.sav", spec)

	reader, err := pformat.NewReader(pformat.SPSS, afs, "survey.sav", pformat.ReaderOptions{})
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	job := pio.NewImportJob(reader, "out.pulse")
	job.FS = afs

	report, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("ImportJob.Run: %v", err)
	}
	if len(report.SourceWarnings) == 0 {
		t.Fatal("SourceWarnings is empty; an unrecognised extension subtype must reach the import report")
	}
	found := false
	for _, w := range report.SourceWarnings {
		if w.Code == "PULSE_SPSS_EXTENSION_UNKNOWN" {
			found = true
		}
		if w.Message == "" {
			t.Errorf("warning %s carries an empty message", w.Code)
		}
	}
	if !found {
		t.Errorf("SourceWarnings = %v, want a PULSE_SPSS_EXTENSION_UNKNOWN entry", report.SourceWarnings)
	}
}

// discardWriter is the minimal pio.Writer for convert tests that care
// only about the resolved schema, not the rendered output.
type discardWriter struct{ rows int }

func (w *discardWriter) WriteHeader([]string) error { return nil }
func (w *discardWriter) WriteRow([]any) error       { w.rows++; return nil }
func (w *discardWriter) Close() error               { return nil }

var _ pio.Writer = (*discardWriter)(nil)
