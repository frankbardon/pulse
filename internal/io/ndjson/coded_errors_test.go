package ndjson

import (
	"context"
	stderrors "errors"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	pio "github.com/frankbardon/pulse/internal/io"
	"github.com/spf13/afero"
)

func ndjsonImport(t *testing.T, doc string) (*pio.ImportReport, error) {
	t.Helper()
	job := pio.NewImportJob(NewReaderFromBytes([]byte(doc)), "c.pulse")
	job.FS = afero.NewMemMapFs()
	return job.Run(context.Background())
}

func requireCode(t *testing.T, err error, code errors.Code) *errors.CodedError {
	t.Helper()
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != code {
		t.Fatalf("want %s, got %v", code, err)
	}
	return ce
}

// A malformed FIRST line is row 1 — refused with the code every later
// malformed line carries, never a raw decoder error (found by
// FuzzNDJSONImport).
func TestNDJSONImport_MalformedFirstLineIsRowError(t *testing.T) {
	t.Parallel()
	for _, doc := range []string{"{\"a\":\n", "{\"a\":{\"b\":1}}\n", "[1,2,3]\n", "\n\n{oops}\n"} {
		_, err := ndjsonImport(t, doc)
		ce := requireCode(t, err, errors.PULSE_IMPORT_ROW_ERROR)
		if ce.Details["row"] != 1 || ce.Details["line"] == nil {
			t.Fatalf("%q: want details row 1 + line, got %v", doc, ce.Details)
		}
	}
}

// A record line longer than bufio.Scanner's 64 KiB default imports: the
// source is already in memory, so the line cap is the source length.
func TestNDJSONImport_LineLongerThanScannerDefault(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("x", 100<<10)
	rep, err := ndjsonImport(t, "{\"id\":1,\"note\":\""+long+"\"}\n{\"id\":2,\"note\":\"y\"}\n")
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if rep.RowsImported != 2 {
		t.Fatalf("imported %d rows, want 2", rep.RowsImported)
	}
}

// A path source that cannot be read is coded DATA_FILE and keeps the
// filesystem cause on the chain.
func TestNDJSONReader_UnreadablePathIsDataFile(t *testing.T) {
	t.Parallel()
	_, err := NewReader(afero.NewMemMapFs(), "missing.ndjson").ReadHeader()
	ce := requireCode(t, err, errors.DATA_FILE)
	if ce.Cause == nil {
		t.Fatal("want the filesystem cause on the chain")
	}
	_, err = (&Reader{}).ReadHeader()
	requireCode(t, err, errors.DATA_FILE)
}
