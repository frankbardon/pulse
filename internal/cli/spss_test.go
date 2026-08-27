package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	perrors "github.com/frankbardon/pulse/errors"
	pio "github.com/frankbardon/pulse/io"
	pformat "github.com/frankbardon/pulse/io/format"
	"github.com/frankbardon/pulse/io/spss"
	"github.com/spf13/afero"
	cli "github.com/urfave/cli/v3"
)

// TestFormatFromExt_SPSS covers the extensions the convert leaf detects.
// `pulse convert` and `pulse convert predict` both route through
// formatFromExt, so an unmapped extension makes the whole verb
// unreachable for `.sav` regardless of what the reader registry says.
func TestFormatFromExt_SPSS(t *testing.T) {
	for _, tt := range []struct{ path, want string }{
		{"survey.sav", "spss"},
		{"survey.zsav", "spss"},
		{"SURVEY.SAV", "spss"},
		{"/data/2024/survey.sav", "spss"},
	} {
		if got := formatFromExt(tt.path); got != tt.want {
			t.Errorf("formatFromExt(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

// TestMakeImportReader_SPSS pins the import leaf's reader construction.
// It used to be a SEPARATE dispatch from io/format's — registering a format
// in one place and not the other produced a subcommand that existed and
// immediately failed — and E3-S5 collapsed the duplicate onto
// pformat.NewReader. The assertion is unchanged: what `pulse import spss`
// builds must carry the two optional interfaces the import path keys off.
func TestMakeImportReader_SPSS(t *testing.T) {
	r, err := makeImportReader("spss", afero.NewMemMapFs(), "survey.sav", pformat.ReaderOptions{})
	if err != nil {
		t.Fatalf("makeImportReader(spss): %v", err)
	}
	if r == nil {
		t.Fatal("makeImportReader(spss) returned a nil reader")
	}
	if _, ok := r.(pio.SchemaAwareReader); !ok {
		t.Errorf("the CLI's spss reader does not implement pio.SchemaAwareReader; `pulse import spss` would re-infer types from cell text")
	}
	if _, ok := r.(pio.SourceWarningEmitter); !ok {
		t.Errorf("the CLI's spss reader does not implement pio.SourceWarningEmitter; PULSE_SPSS_* warnings would never be printed")
	}
}

// TestImportCommand_HasSPSSSubcommand asserts the leaf is actually
// mounted. Everything else in this story can be correct and the command
// still not exist.
func TestImportCommand_HasSPSSSubcommand(t *testing.T) {
	for _, c := range ImportCommand().Commands {
		if c.Name == "spss" {
			if c.Action == nil {
				t.Error("`pulse import spss` is mounted with a nil Action")
			}
			return
		}
	}
	t.Error("`pulse import spss` is not mounted on the import command group")
}

// TestNewWriterForFormat_SPSSBuildsTheWriter replaces the refusal this
// arm used to be. `pulse convert data.csv out.sav` is now reachable, so
// the dispatch must hand back a real writer — and one carrying the two
// optional interfaces the export path keys off, because a Writer that
// satisfied only pio.Writer would silently take the rendered-row path
// and encode a `.sav` from resolved label text.
func TestNewWriterForFormat_SPSSBuildsTheWriter(t *testing.T) {
	w, err := newWriterForFormat("spss", afero.NewMemMapFs(), "out.sav", writerOptions{})
	if err != nil {
		t.Fatalf("newWriterForFormat(spss): %v", err)
	}
	if _, ok := w.(pio.SchemaAwareWriter); !ok {
		t.Error("the CLI's spss writer does not implement pio.SchemaAwareWriter; it would never see the source schema")
	}
	if _, ok := w.(pio.CohortWriter); !ok {
		t.Error("the CLI's spss writer does not implement pio.CohortWriter; ExportJob would hand it rendered rows")
	}
	if _, ok := w.(pio.TargetWarningEmitter); !ok {
		t.Error("the CLI's spss writer does not implement pio.TargetWarningEmitter; sidecar and rename warnings would never print")
	}
}

// TestWriterOptionsFrom_MapsEveryFlag pins the one-for-one projection of
// the CLI flags onto spss.WriterOptions. A flag that parsed but did not
// reach the option is the failure this catches: it changes nothing and
// says nothing.
func TestWriterOptionsFrom_MapsEveryFlag(t *testing.T) {
	var got spss.WriterOptions
	cmd := &cli.Command{
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "ignore-sidecar"},
			&cli.BoolFlag{Name: "uncompressed"},
			&cli.StringFlag{Name: "charset"},
			&cli.BoolFlag{Name: "sanitise-names"},
		},
		Action: func(_ context.Context, c *cli.Command) error {
			got = writerOptionsFrom(c).SPSS
			return nil
		},
	}
	args := []string{"x", "--ignore-sidecar", "--uncompressed", "--charset", "cp1252", "--sanitise-names"}
	if err := cmd.Run(context.Background(), args); err != nil {
		t.Fatalf("running: %v", err)
	}
	want := spss.WriterOptions{IgnoreSidecar: true, Uncompressed: true, Charset: "cp1252", SanitiseNames: true}
	if got != want {
		t.Errorf("writerOptionsFrom = %+v, want %+v", got, want)
	}
}

// TestExportSPSS_HasEveryWriteFlag. The knobs have to be MOUNTED, which
// is a different claim from spss.WriterOptions carrying the fields.
func TestExportSPSS_HasEveryWriteFlag(t *testing.T) {
	var spssCmd *cli.Command
	for _, c := range ExportCommand().Commands {
		if c.Name == "spss" {
			spssCmd = c
		}
	}
	if spssCmd == nil {
		t.Fatal("`pulse export spss` is not mounted on the export command group")
	}
	if spssCmd.Action == nil {
		t.Error("`pulse export spss` is mounted with a nil Action")
	}
	for _, name := range []string{"ignore-sidecar", "uncompressed", "charset", "sanitise-names"} {
		assertHasFlag(t, spssCmd, name)
	}
	// The common export flags must survive being extended.
	for _, name := range []string{"input", "output", "include", "labels", "json"} {
		assertHasFlag(t, spssCmd, name)
	}
	// And the shared slice must not have been appended to in place.
	for _, c := range ExportCommand().Commands {
		if c.Name != "csv" {
			continue
		}
		for _, f := range c.Flags {
			for _, n := range f.Names() {
				if n == "ignore-sidecar" || n == "sanitise-names" {
					t.Errorf("`pulse export csv` grew a %q flag; the shared exportFlags slice was mutated in place", n)
				}
			}
		}
	}
}

// TestExportPredict_HasTheTargetFlags. E6-S1 made `pulse export predict`
// target-aware, and a flag that is not mounted cannot be passed: --format
// unmounted leaves predict answering from the source cohort alone, and
// --sanitise-names unmounted leaves it refusing an export that would have
// succeeded, with no way for the caller to say otherwise.
func TestExportPredict_HasTheTargetFlags(t *testing.T) {
	var predict *cli.Command
	for _, c := range ExportCommand().Commands {
		if c.Name == "predict" {
			predict = c
		}
	}
	if predict == nil {
		t.Fatal("`pulse export predict` is not mounted on the export command group")
	}
	for _, name := range []string{"format", "ignore-sidecar", "uncompressed", "charset", "sanitise-names"} {
		assertHasFlag(t, predict, name)
	}
	// It declares no --output, and must not grow one: predict writes nothing.
	for _, f := range predict.Flags {
		for _, n := range f.Names() {
			if n == "output" || n == "o" {
				t.Errorf("`pulse export predict` declares a %q flag; it validates without writing", n)
			}
		}
	}
}

// TestConvertHasTheWriteFlags mirrors the read side's own coverage: a
// knob mounted on `export spss` alone would leave `pulse convert data.csv
// out.sav` — the shortest path to a `.sav` — unable to ask for it.
func TestConvertHasTheWriteFlags(t *testing.T) {
	convert := ConvertCommand()
	for _, name := range []string{"ignore-sidecar", "uncompressed", "sanitise-names"} {
		assertHasFlag(t, convert, name)
		for _, sub := range convert.Commands {
			if sub.Name == "predict" {
				assertHasFlag(t, sub, name)
			}
		}
	}
}

// TestWriteCodedErrorEnvelope_PreservesTheCode is E2-S8's finding closed.
//
// The fatal --json arms on import / convert / export stringified a
// *CodedError into writeErrorEnvelope, so `errors[0].code` read
// "IMPORT_ERROR" and a consumer could not feed it to `pulse errors
// lookup` — while non-fatal warnings on the same paths carried real
// codes all along. This effort built a rich PULSE_SPSS_* error surface
// that was unreachable on the path users hit first.
func TestWriteCodedErrorEnvelope_PreservesTheCode(t *testing.T) {
	t.Run("a coded error keeps its code and details", func(t *testing.T) {
		var buf bytes.Buffer
		err := perrors.NewCodedErrorWithDetails(perrors.PULSE_SPSS_SIDECAR_STALE,
			"the sidecar no longer describes this cohort",
			map[string]any{perrors.DetailSPSSCohort: "out.pulse"})
		if werr := writeCodedErrorEnvelope(&buf, "IMPORT_ERROR", err); werr != nil {
			t.Fatalf("writeCodedErrorEnvelope: %v", werr)
		}
		env := decodeEnvelope(t, buf.Bytes())
		if len(env.Errors) != 1 {
			t.Fatalf("errors = %d, want 1", len(env.Errors))
		}
		if env.Errors[0].Code != string(perrors.PULSE_SPSS_SIDECAR_STALE) {
			t.Errorf("errors[0].code = %q, want %s — the placeholder swallowed the real code",
				env.Errors[0].Code, perrors.PULSE_SPSS_SIDECAR_STALE)
		}
		if env.Errors[0].Details[perrors.DetailSPSSCohort] != "out.pulse" {
			t.Errorf("errors[0].details lost the cohort key: %v", env.Errors[0].Details)
		}
	})

	t.Run("a wrapped coded error still surfaces", func(t *testing.T) {
		var buf bytes.Buffer
		inner := perrors.NewCodedError(perrors.PULSE_SPSS_NAME_INVALID, "bad name")
		if werr := writeCodedErrorEnvelope(&buf, "CONVERT_ERROR", fmt.Errorf("converting: %w", inner)); werr != nil {
			t.Fatalf("writeCodedErrorEnvelope: %v", werr)
		}
		env := decodeEnvelope(t, buf.Bytes())
		if env.Errors[0].Code != string(perrors.PULSE_SPSS_NAME_INVALID) {
			t.Errorf("errors[0].code = %q, want %s", env.Errors[0].Code, perrors.PULSE_SPSS_NAME_INVALID)
		}
	})

	t.Run("an uncoded error keeps the placeholder", func(t *testing.T) {
		var plain, coded bytes.Buffer
		err := fmt.Errorf("reading pulse file: no such file")
		if werr := writeErrorEnvelope(&plain, "EXPORT_ERROR", err.Error()); werr != nil {
			t.Fatalf("writeErrorEnvelope: %v", werr)
		}
		if werr := writeCodedErrorEnvelope(&coded, "EXPORT_ERROR", err); werr != nil {
			t.Fatalf("writeCodedErrorEnvelope: %v", werr)
		}
		if plain.String() != coded.String() {
			t.Errorf("an uncoded error changed shape:\n got %s\nwant %s", coded.String(), plain.String())
		}
	})
}

func decodeEnvelope(t *testing.T, raw []byte) descriptor.Envelope {
	t.Helper()
	var env descriptor.Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	return env
}

// TestNewWriterForFormat_WritableFormatsUnaffected keeps the new arm
// from swallowing anything else.
func TestNewWriterForFormat_WritableFormatsUnaffected(t *testing.T) {
	fs := afero.NewMemMapFs()
	for _, f := range []string{"csv", "tsv", "ndjson", "jsonarray", "parquet", "arrow", "excel"} {
		if _, err := newWriterForFormat(f, fs, "out."+f, writerOptions{}); err != nil {
			t.Errorf("newWriterForFormat(%q): %v", f, err)
		}
	}
}

// TestWriteEnvelopeWithWarnings_LiftsCodes pins where source-parse
// diagnostics land in --json output: the envelope's `warnings` array,
// which is where the Output Format Contract says warnings live. Burying
// them in `data` would leave every generic envelope consumer blind.
func TestWriteEnvelopeWithWarnings_LiftsCodes(t *testing.T) {
	var buf bytes.Buffer
	warns := []*perrors.CodedError{
		perrors.NewCodedErrorWithDetails(
			perrors.PULSE_SPSS_CARDINALITY_HIGH, "too many distinct values",
			map[string]any{"variable": "COMMENT"}),
		perrors.NewCodedError(perrors.PULSE_SPSS_EXTENSION_UNKNOWN, "subtype 42"),
	}
	if err := writeEnvelopeWithWarnings(&buf, map[string]int{"rows": 3}, warns); err != nil {
		t.Fatalf("writeEnvelopeWithWarnings: %v", err)
	}
	var env descriptor.Envelope
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if env.FormatVersion != "1.1" {
		t.Errorf("format_version = %q, want 1.1", env.FormatVersion)
	}
	if len(env.Warnings) != 2 {
		t.Fatalf("warnings = %d, want 2", len(env.Warnings))
	}
	if env.Warnings[0].Code != string(perrors.PULSE_SPSS_CARDINALITY_HIGH) {
		t.Errorf("warnings[0].code = %q, want %s", env.Warnings[0].Code, perrors.PULSE_SPSS_CARDINALITY_HIGH)
	}
	if env.Warnings[0].Details["variable"] != "COMMENT" {
		t.Errorf("warnings[0].details lost the variable key: %v", env.Warnings[0].Details)
	}
	if len(env.Errors) != 0 {
		t.Errorf("errors = %v; warnings must not populate the errors array", env.Errors)
	}
}

// TestWriteEnvelopeWithWarnings_EmptyMatchesPlainEnvelope pins the
// degrade path byte-for-byte: every format other than SPSS emits no
// source warnings, so their --json output must not move.
func TestWriteEnvelopeWithWarnings_EmptyMatchesPlainEnvelope(t *testing.T) {
	data := map[string]int{"rows": 3}
	var withWarn, plain bytes.Buffer
	if err := writeEnvelopeWithWarnings(&withWarn, data, nil); err != nil {
		t.Fatalf("writeEnvelopeWithWarnings: %v", err)
	}
	if err := writeEnvelope(&plain, data); err != nil {
		t.Fatalf("writeEnvelope: %v", err)
	}
	if withWarn.String() != plain.String() {
		t.Errorf("empty-warning envelope diverged from writeEnvelope:\n got %s\nwant %s", withWarn.String(), plain.String())
	}
}

// TestWriteSourceWarnings_TextPath covers the human-readable surface,
// including that a nil slice prints nothing at all.
func TestWriteSourceWarnings_TextPath(t *testing.T) {
	var buf bytes.Buffer
	writeSourceWarnings(&buf, nil)
	if buf.Len() != 0 {
		t.Errorf("nil warnings printed %q, want nothing", buf.String())
	}

	writeSourceWarnings(&buf, []*perrors.CodedError{
		perrors.NewCodedError(perrors.PULSE_SPSS_TEMPORAL_PRECISION, "mapped to raw seconds"),
	})
	out := buf.String()
	if !bytes.Contains([]byte(out), []byte("PULSE_SPSS_TEMPORAL_PRECISION")) {
		t.Errorf("text output %q does not name the code", out)
	}
	if !bytes.Contains([]byte(out), []byte("mapped to raw seconds")) {
		t.Errorf("text output %q does not carry the message", out)
	}
}

// TestImportSPSS_HasCharsetFlag closes the E3-S3 usability trap at the level
// it was open. spss.WithCharset existed but was reachable only from Go: an
// operator holding a legacy `.sav` that declares no encoding and carries any
// 8-bit byte got PULSE_SPSS_CHARSET_INVALID with no recourse from the CLI at
// all. The flag has to be MOUNTED, which is a different claim from the
// option existing.
func TestImportSPSS_HasCharsetFlag(t *testing.T) {
	var spssCmd *cli.Command
	for _, c := range ImportCommand().Commands {
		if c.Name == "spss" {
			spssCmd = c
		}
	}
	if spssCmd == nil {
		t.Fatal("`pulse import spss` is not mounted")
	}
	assertHasFlag(t, spssCmd, "charset")
	assertHasFlag(t, spssCmd, "spss-missing")
	// The common import flags must survive being extended.
	for _, name := range []string{"input", "output", "schema", "sample-rows", "json"} {
		assertHasFlag(t, spssCmd, name)
	}
}

// TestSPSSMissingFlagOnEverySPSSReachableLeaf mirrors the --charset
// check. A knob mounted on `import spss` alone would leave `convert
// survey.sav out.csv` — the shortest path from a `.sav` to something
// readable — unable to ask for the slim schema, and would make the
// default silently unoverridable there.
func TestSPSSMissingFlagOnEverySPSSReachableLeaf(t *testing.T) {
	convert := ConvertCommand()
	assertHasFlag(t, convert, "spss-missing")
	for _, sub := range convert.Commands {
		if sub.Name == "predict" {
			assertHasFlag(t, sub, "spss-missing")
		}
	}
	for _, sub := range ImportCommand().Commands {
		switch sub.Name {
		case "spss", "predict", "schema-template":
			assertHasFlag(t, sub, "spss-missing")
		}
	}
}

// TestMakeImportReader_RejectsUnknownMissingMode checks the refusal
// survives the CLI's own reader construction rather than being swallowed
// into the generic "unsupported import format" message. A typo'd
// --spss-missing must not silently import under the default.
func TestMakeImportReader_RejectsUnknownMissingMode(t *testing.T) {
	_, err := makeImportReader("spss", afero.NewMemMapFs(), "survey.sav",
		pformat.ReaderOptions{SPSSMissing: "nul"})
	if err == nil {
		t.Fatal("makeImportReader accepted an unknown --spss-missing value")
	}
	if !strings.Contains(err.Error(), "nul") {
		t.Errorf("error %q does not name the offending value", err)
	}
}

// TestCharsetFlagOnEverySPSSReachableLeaf covers the other verbs a `.sav`
// can arrive through. A flag on `import spss` alone would leave `convert
// survey.sav out.csv` — the shortest path from an SPSS file to something
// readable — with no way to answer the same question.
func TestCharsetFlagOnEverySPSSReachableLeaf(t *testing.T) {
	convert := ConvertCommand()
	assertHasFlag(t, convert, "charset")
	for _, sub := range convert.Commands {
		if sub.Name == "predict" {
			assertHasFlag(t, sub, "charset")
		}
	}
	for _, sub := range ImportCommand().Commands {
		switch sub.Name {
		case "predict", "schema-template":
			assertHasFlag(t, sub, "charset")
		}
	}
}

// TestImportExcel_SheetFlagStillMounted guards the refactor that replaced
// excel's hand-copied flag slice with withImportFlags. A shared slice that
// got appended to in place would corrupt every other leaf's flags.
func TestImportExcel_SheetFlagStillMounted(t *testing.T) {
	var excelCmd, csvCmd *cli.Command
	for _, c := range ImportCommand().Commands {
		switch c.Name {
		case "excel":
			excelCmd = c
		case "csv":
			csvCmd = c
		}
	}
	if excelCmd == nil || csvCmd == nil {
		t.Fatal("the excel or csv import leaf is not mounted")
	}
	assertHasFlag(t, excelCmd, "sheet")
	for _, f := range csvCmd.Flags {
		for _, n := range f.Names() {
			if n == "sheet" || n == "charset" {
				t.Errorf("`pulse import csv` grew a %q flag; the shared importFlags slice was mutated in place", n)
			}
		}
	}
}

func assertHasFlag(t *testing.T, cmd *cli.Command, name string) {
	t.Helper()
	for _, f := range cmd.Flags {
		for _, n := range f.Names() {
			if n == name {
				return
			}
		}
	}
	t.Errorf("command %q has no --%s flag", cmd.Name, name)
}

// TestWriteEnvelopeWithWarnings_CarriesMissingCategories is E4-S3's
// discoverability criterion at the surface a caller actually reads.
//
// PULSE_SPSS_CATEGORICAL_USER_MISSING is the only SPSS diagnostic whose
// details are NESTED — a field name to flagged-dictionary-entries map
// rather than a flat string — because the prose caps its list and the
// details must not. A marshal that flattened or dropped it would leave
// "which entry do I exclude?" answerable only by opening the sidecar.
func TestWriteEnvelopeWithWarnings_CarriesMissingCategories(t *testing.T) {
	var buf bytes.Buffer
	warns := []*perrors.CodedError{
		perrors.NewCodedErrorWithDetails(
			perrors.PULSE_SPSS_CATEGORICAL_USER_MISSING,
			`spss: 2 categorical columns declare user-missing codes`,
			map[string]any{
				perrors.DetailSPSSMissingCategories: map[string][]string{
					"Q1": {"9"},
					"Q2": {"8", "9"},
				},
				perrors.DetailSPSSDistinct: 2,
			}),
	}
	if err := writeEnvelopeWithWarnings(&buf, map[string]int{"rows": 3}, warns); err != nil {
		t.Fatalf("writeEnvelopeWithWarnings: %v", err)
	}
	var env descriptor.Envelope
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(env.Warnings) != 1 {
		t.Fatalf("warnings = %d, want 1", len(env.Warnings))
	}
	raw, ok := env.Warnings[0].Details[perrors.DetailSPSSMissingCategories].(map[string]any)
	if !ok {
		t.Fatalf("details[%s] = %#v, want a nested object",
			perrors.DetailSPSSMissingCategories, env.Warnings[0].Details[perrors.DetailSPSSMissingCategories])
	}
	q2, ok := raw["Q2"].([]any)
	if !ok || len(q2) != 2 || q2[0] != "8" || q2[1] != "9" {
		t.Errorf("details[%s][\"Q2\"] = %#v, want the two flagged entries in dictionary order",
			perrors.DetailSPSSMissingCategories, raw["Q2"])
	}
}
