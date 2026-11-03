package jsonarray

import (
	"context"
	stderrors "errors"
	"testing"

	"github.com/frankbardon/pulse/errors"
	pio "github.com/frankbardon/pulse/internal/io"
	"github.com/spf13/afero"
)

func jsonArrayImport(t *testing.T, doc string) (*pio.ImportReport, error) {
	t.Helper()
	job := pio.NewImportJob(NewReaderFromBytes([]byte(doc)), "c.pulse")
	job.FS = afero.NewMemMapFs()
	return job.Run(context.Background())
}

func requireCode(t *testing.T, doc string, err error, code errors.Code) *errors.CodedError {
	t.Helper()
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != code {
		t.Fatalf("%q: want %s, got %v", doc, code, err)
	}
	return ce
}

// A malformed FIRST element is row 1 — refused with the code every
// later malformed element carries (found by FuzzJSONArrayImport).
func TestJSONArrayImport_MalformedFirstElementIsRowError(t *testing.T) {
	t.Parallel()
	for _, doc := range []string{`[{"a":`, `[1,2,3]`, `[{"a":{"b":1}}]`} {
		_, err := jsonArrayImport(t, doc)
		ce := requireCode(t, doc, err, errors.PULSE_IMPORT_ROW_ERROR)
		if ce.Details["row"] != 1 {
			t.Fatalf("%q: want details row 1, got %v", doc, ce.Details)
		}
	}
}

// A document that is not one well-formed top-level array is DATA_FILE:
// not an array, or followed by more data after the closing ']' (which
// was silently ignored: a concatenation of two arrays imported only
// the first).
func TestJSONArrayImport_MalformedDocumentIsDataFile(t *testing.T) {
	t.Parallel()
	for _, doc := range []string{
		`{"a":1}`,
		`"x"`,
		`]`,
		`[{"a":1}] trailing`,
		`[{"a":1}][]`,
		`[] {}`,
	} {
		_, err := jsonArrayImport(t, doc)
		requireCode(t, doc, err, errors.DATA_FILE)
	}
}

// The well-formed edges keep their outcome: an empty source and an
// empty array are both the empty-header refusal they always were (never
// a layout fault), and whitespace around the array is fine.
func TestJSONArrayImport_WellFormedEdgesImport(t *testing.T) {
	t.Parallel()
	for _, doc := range []string{``, "  \n", `[]`, " [ ] \n"} {
		_, err := jsonArrayImport(t, doc)
		requireCode(t, doc, err, errors.PULSE_IMPORT_SCHEMA_AMBIGUOUS)
	}
	doc := "\n[{\"a\":1},{\"a\":2}]\n"
	rep, err := jsonArrayImport(t, doc)
	if err != nil {
		t.Fatalf("%q: %v", doc, err)
	}
	if rep.RowsImported != 2 {
		t.Fatalf("%q: imported %d rows, want 2", doc, rep.RowsImported)
	}
}

// A path source that cannot be read is coded DATA_FILE and keeps the
// filesystem cause on the chain.
func TestJSONArrayReader_UnreadablePathIsDataFile(t *testing.T) {
	t.Parallel()
	_, err := NewReader(afero.NewMemMapFs(), "missing.json").ReadHeader()
	ce := requireCode(t, "missing.json", err, errors.DATA_FILE)
	if ce.Cause == nil {
		t.Fatal("want the filesystem cause on the chain")
	}
	_, err = (&Reader{}).ReadHeader()
	requireCode(t, "", err, errors.DATA_FILE)
}
