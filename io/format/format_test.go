package format

import (
	"bytes"
	"testing"

	"github.com/frankbardon/pulse/internal/spsstest"
	pio "github.com/frankbardon/pulse/io"
	"github.com/spf13/afero"
)

// TestFromExt_Matrix pins the extension → format identifier dispatch.
// The `.sav` / `.zsav` rows are the ones this story adds; the rest are
// present so a future edit to the switch cannot quietly drop a mapping
// while adding one.
func TestFromExt_Matrix(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"data.csv", CSV},
		{"data.tsv", TSV},
		{"data.ndjson", NDJSON},
		{"data.jsonl", NDJSON},
		{"data.json", JSONArray},
		{"data.parquet", Parquet},
		{"data.pq", Parquet},
		{"data.arrow", Arrow},
		{"data.feather", Arrow},
		{"data.xlsx", Excel},
		{"data.xls", Excel},
		{"survey.sav", SPSS},
		{"survey.zsav", SPSS},
		// Case folding is the dispatch's own, not the caller's.
		{"SURVEY.SAV", SPSS},
		{"Survey.ZSav", SPSS},
		// A path with directories still resolves on its extension.
		{"/abs/dir/survey.sav", SPSS},
		{"data.pulse", Pulse},
		{"data.unknown", ""},
		{"noext", ""},
	}
	for _, tt := range tests {
		if got := FromExt(tt.path); got != tt.want {
			t.Errorf("FromExt(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

// TestSupportedImport_ContainsSPSS asserts spss is advertised as an
// importable format. SupportedImport is what documentation and CLI help
// enumerate, so a reader registered but not listed here is reachable
// only by accident.
func TestSupportedImport_ContainsSPSS(t *testing.T) {
	for _, f := range SupportedImport {
		if f == SPSS {
			return
		}
	}
	t.Errorf("SupportedImport = %v, missing %q", SupportedImport, SPSS)
}

// TestSupportedImport_EveryEntryConstructs closes the loop the registry
// exists to close: every advertised format must actually yield a reader.
// An entry in SupportedImport with no NewReader case would surface as a
// runtime "unsupported format" from a list the engine itself published.
func TestSupportedImport_EveryEntryConstructs(t *testing.T) {
	fs := afero.NewMemMapFs()
	for _, f := range SupportedImport {
		r, err := NewReader(f, fs, "x", ReaderOptions{})
		if err != nil {
			t.Errorf("NewReader(%q) returned error %v; SupportedImport advertises it", f, err)
			continue
		}
		if r == nil {
			t.Errorf("NewReader(%q) returned a nil reader with no error", f)
		}
	}
}

// TestNewReader_SPSSContracts asserts the reader the dispatch hands back
// for spss satisfies the optional interfaces the shared import path
// keys off. ResetReader is what makes an infer-then-import sequence
// legal at all; SchemaAwareReader is what makes the `.sav` dictionary
// authoritative instead of a hint the inference pass would overrule.
// Asserted through the DISPATCH rather than on spss.Reader directly,
// because a dispatch that returned a wrapper losing an interface would
// pass a package-local assertion and still break import fidelity.
func TestNewReader_SPSSContracts(t *testing.T) {
	r, err := NewReader(SPSS, afero.NewMemMapFs(), "survey.sav", ReaderOptions{})
	if err != nil {
		t.Fatalf("NewReader(spss): %v", err)
	}
	if _, ok := r.(pio.ResetReader); !ok {
		t.Errorf("spss reader does not implement pio.ResetReader")
	}
	if _, ok := r.(pio.SchemaAwareReader); !ok {
		t.Errorf("spss reader does not implement pio.SchemaAwareReader; the .sav dictionary would be re-inferred from cell text")
	}
	if _, ok := r.(pio.SourceWarningEmitter); !ok {
		t.Errorf("spss reader does not implement pio.SourceWarningEmitter; PULSE_SPSS_* warnings would never reach a report")
	}
}

// TestNewReader_Rejections pins the two non-format arms. "pulse" is the
// native format and deliberately has no tabular reader; the empty string
// is the FromExt miss and must not fall through to a default adapter.
func TestNewReader_Rejections(t *testing.T) {
	fs := afero.NewMemMapFs()
	for _, f := range []string{Pulse, "", "sav"} {
		if _, err := NewReader(f, fs, "x", ReaderOptions{}); err == nil {
			t.Errorf("NewReader(%q) = nil error, want an error", f)
		}
	}
}

// TestNewReader_SPSSCharsetOption pins the E3-S5 plumbing: ReaderOptions.
// Charset must actually reach the SPSS reader, because a flag that parses
// and is then dropped looks exactly like a charset that did not help.
//
// The assertion is behavioural rather than structural — a `.sav` carrying an
// 8-bit byte and declaring no encoding fails under the strict UTF-8 default
// and decodes under the override — since checking that an option was stored
// would not prove it was consulted.
func TestNewReader_SPSSCharsetOption(t *testing.T) {
	raw, err := spsstest.Build(spsstest.Spec{
		Vars:  []spsstest.Var{{Name: "A", Label: "cafX"}},
		Cases: [][]spsstest.Value{{spsstest.Num(1)}},
	})
	if err != nil {
		t.Fatalf("spsstest.Build: %v", err)
	}
	at := bytes.Index(raw, []byte("cafX"))
	if at < 0 {
		t.Fatal("the label is not in the emitted bytes")
	}
	raw[at+3] = 0xE9 // "café" in windows-1252; not valid UTF-8 alone

	fs := afero.NewMemMapFs()
	if err := afero.WriteFile(fs, "survey.sav", raw, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	plain, err := NewReader(SPSS, fs, "survey.sav", ReaderOptions{})
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	if _, err := plain.ReadHeader(); err == nil {
		t.Fatal("an undeclared 8-bit file read without an override; the default is strict UTF-8")
	}

	overridden, err := NewReader(SPSS, fs, "survey.sav", ReaderOptions{Charset: "windows-1252"})
	if err != nil {
		t.Fatalf("NewReader with charset: %v", err)
	}
	if _, err := overridden.ReadHeader(); err != nil {
		t.Fatalf("ReaderOptions.Charset did not reach the reader: %v", err)
	}
}

// TestNewReader_CharsetIgnoredElsewhere keeps the new field from changing
// any other format's construction. Every field on ReaderOptions is honoured
// by exactly one format and ignored silently by the rest.
func TestNewReader_CharsetIgnoredElsewhere(t *testing.T) {
	fs := afero.NewMemMapFs()
	for _, f := range []string{CSV, TSV, NDJSON, JSONArray, Parquet, Arrow, Excel} {
		if _, err := NewReader(f, fs, "in."+f, ReaderOptions{Charset: "windows-1252"}); err != nil {
			t.Errorf("NewReader(%q) with a charset: %v", f, err)
		}
	}
}
