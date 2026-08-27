package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	perrors "github.com/frankbardon/pulse/errors"
	pio "github.com/frankbardon/pulse/io"
	pformat "github.com/frankbardon/pulse/io/format"
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

// TestNewWriterForFormat_SPSSIsCodedError is the honest-failure half of
// wiring formatFromExt. Mapping `.sav` makes `pulse convert data.csv
// out.sav` reachable, and the answer must be "Pulse cannot WRITE .sav
// yet" rather than the dispatcher's generic unknown-format message —
// the extension is recognised, which is exactly what makes the generic
// wording misleading.
func TestNewWriterForFormat_SPSSIsCodedError(t *testing.T) {
	_, err := newWriterForFormat("spss", afero.NewMemMapFs(), "out.sav")
	if err == nil {
		t.Fatal("newWriterForFormat(spss) = nil error; SPSS export does not exist yet")
	}
	ce, ok := err.(*perrors.CodedError)
	if !ok {
		t.Fatalf("error is %T, want *errors.CodedError so `pulse errors lookup` can explain it", err)
	}
	if ce.Code != perrors.PULSE_SPSS_EXPORT_UNSUPPORTED {
		t.Errorf("code = %s, want %s", ce.Code, perrors.PULSE_SPSS_EXPORT_UNSUPPORTED)
	}
	if ce.Details["output_path"] != "out.sav" {
		t.Errorf("details[output_path] = %v, want out.sav", ce.Details["output_path"])
	}
}

// TestNewWriterForFormat_WritableFormatsUnaffected keeps the new arm
// from swallowing anything else.
func TestNewWriterForFormat_WritableFormatsUnaffected(t *testing.T) {
	fs := afero.NewMemMapFs()
	for _, f := range []string{"csv", "tsv", "ndjson", "jsonarray", "parquet", "arrow", "excel"} {
		if _, err := newWriterForFormat(f, fs, "out."+f); err != nil {
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
