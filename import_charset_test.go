package pulse

import (
	"bytes"
	"context"
	stderrors "errors"
	"testing"

	perrors "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/spsstest"
	"github.com/spf13/afero"
)

// undeclaredCharsetSav returns the bytes of a `.sav` that declares NO
// character encoding — no record 7/20 name, no record 7/3 code — and whose
// single text datum carries the windows-1252 byte for "ü".
//
// A file shaped like this is the one case the charset override exists for:
// the reader's default is strict UTF-8 (deliberately, so a pre-Unicode file
// fails loudly instead of importing mojibake), the 0xFC byte is not valid
// UTF-8, and the file has no further evidence to offer. Only the caller can
// say what the byte means.
func undeclaredCharsetSav(t *testing.T) []byte {
	t.Helper()
	raw, err := spsstest.Build(spsstest.Spec{
		Vars:  []spsstest.Var{{Name: "CITY", Width: 6}},
		Cases: [][]spsstest.Value{{spsstest.Text("Zurich")}},
	})
	if err != nil {
		t.Fatalf("spsstest.Build: %v", err)
	}
	at := bytes.Index(raw, []byte("Zurich"))
	if at < 0 {
		t.Fatal("the fixture does not hold the datum")
	}
	raw[at+1] = 0xFC // windows-1252 'ü'
	return raw
}

func codedImportErr(t *testing.T, err error) *perrors.CodedError {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	var ce *perrors.CodedError
	if !stderrors.As(err, &ce) {
		t.Fatalf("error is not coded: %v", err)
	}
	return ce
}

// TestImportSpec_CharsetRescuesUndeclaredSav is the E6-S3 criterion at the
// facade both `pulse import auto` and the pulse_import MCP tool call. Before
// the Charset slot existed on ImportSpec the managed pool had no recourse at
// all for this file: the reader-level spss.WithCharset was reachable only by
// an embedder constructing the reader itself, which neither of those two
// surfaces does.
func TestImportSpec_CharsetRescuesUndeclaredSav(t *testing.T) {
	afs := afero.NewMemMapFs()
	if err := afero.WriteFile(afs, "legacy.sav", undeclaredCharsetSav(t), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	p, err := New(Options{FS: afs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := context.Background()

	// Without the override the import must fail, and fail with the code
	// that names the problem — otherwise the test would pass against a
	// reader that had simply started substituting U+FFFD.
	_, err = p.ImportFile(ctx, ImportSpec{SourcePath: "legacy.sav"})
	if ce := codedImportErr(t, err); ce.Code != perrors.PULSE_SPSS_CHARSET_INVALID {
		t.Fatalf("code = %s, want %s", ce.Code, perrors.PULSE_SPSS_CHARSET_INVALID)
	}

	// With it, the same file imports and the datum decodes.
	res, err := p.ImportFile(ctx, ImportSpec{SourcePath: "legacy.sav", Charset: "windows-1252"})
	if err != nil {
		t.Fatalf("ImportFile with Charset: %v", err)
	}
	if res.RowsImported != 1 {
		t.Fatalf("RowsImported = %d, want 1", res.RowsImported)
	}
	rows, err := p.Sample(ctx, res.Path, 1)
	if err != nil {
		t.Fatalf("Sample: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("sampled %d rows, want 1", len(rows))
	}
	if got := rows[0]["CITY"]; got != "Zürich" {
		t.Errorf("CITY = %v, want %q — the override changed decoding, not just the verdict", got, "Zürich")
	}
}

// TestImportSpec_CharsetIsInertForNonSPSS pins the other half of the
// contract. Charset rides the shared format.ReaderOptions struct exactly as
// Sheet does, so it must be silently ignored by every format that has no
// opinion about codepages — a CSV import must be byte-identical with and
// without it, not merely successful.
func TestImportSpec_CharsetIsInertForNonSPSS(t *testing.T) {
	afs := afero.NewMemMapFs()
	body := "id,city\n1,Zurich\n2,Bern\n"
	if err := afero.WriteFile(afs, "data.csv", []byte(body), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	p, err := New(Options{FS: afs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := context.Background()

	plain, err := p.ImportFile(ctx, ImportSpec{SourcePath: "data.csv", Handle: "plain"})
	if err != nil {
		t.Fatalf("ImportFile plain: %v", err)
	}
	withCharset, err := p.ImportFile(ctx, ImportSpec{
		SourcePath: "data.csv", Handle: "withcharset", Charset: "windows-1252",
	})
	if err != nil {
		t.Fatalf("ImportFile with Charset: %v — the option must be inert for CSV, not rejected", err)
	}
	if withCharset.RowsImported != plain.RowsImported {
		t.Errorf("RowsImported = %d with Charset, %d without", withCharset.RowsImported, plain.RowsImported)
	}

	a, err := afero.ReadFile(afs, plain.Path)
	if err != nil {
		t.Fatalf("read %s: %v", plain.Path, err)
	}
	b, err := afero.ReadFile(afs, withCharset.Path)
	if err != nil {
		t.Fatalf("read %s: %v", withCharset.Path, err)
	}
	if !bytes.Equal(a, b) {
		t.Error("the CSV cohort differs with Charset set; the option is not inert for non-SPSS formats")
	}
}
