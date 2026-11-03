package csv

import (
	"context"
	stdcsv "encoding/csv"
	stderrors "errors"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
)

type cellStringer struct{}

func (cellStringer) String() string { return "custom" }

// TestWriter_CellFormatting pins the text every cell kind exports as: a
// null is an empty cell, integers of every width are decimal, floats are
// the shortest round-tripping form at their own width (a float32 0.1 is
// "0.1", never its float64 widening), bytes are raw, and anything else
// falls back to its fmt form.
func TestWriter_CellFormatting(t *testing.T) {
	t.Parallel()

	cells := []struct {
		in   any
		want string
	}{
		{nil, ""},
		{"text", "text"},
		{true, "true"},
		{int(-1), "-1"},
		{int8(-8), "-8"},
		{int16(-16), "-16"},
		{int32(-32), "-32"},
		{int64(-64), "-64"},
		{uint(1), "1"},
		{uint8(8), "8"},
		{uint16(16), "16"},
		{uint32(32), "32"},
		{uint64(18446744073709551615), "18446744073709551615"},
		{float32(0.1), "0.1"},
		{float64(0.1), "0.1"},
		{float64(1e21), "1e+21"},
		{[]byte("raw"), "raw"},
		{cellStringer{}, "custom"},
	}
	row := make([]any, len(cells))
	want := make([]string, len(cells))
	for i, c := range cells {
		row[i], want[i] = c.in, c.want
	}
	w := NewWriterToBuffer()
	if err := w.WriteRow(row); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	got := strings.TrimRight(w.buf.String(), "\n")
	if exp := strings.Join(want, ","); got != exp {
		t.Errorf("row =\n  %q\nwant\n  %q", got, exp)
	}

	// A separator inside a cell is quoted, so the row reads back whole.
	w = NewWriterToBuffer()
	_ = w.WriteRow([]any{"a,b", 2})
	_ = w.Close()
	if got, exp := strings.TrimRight(w.buf.String(), "\n"), "\"a,b\",2"; got != exp {
		t.Errorf("quoted row = %q, want %q", got, exp)
	}
}

// failingSource fails every read: storage that dies under the parser.
type failingSource struct{}

var errSource = stderrors.New("injected read failure")

func (failingSource) Read([]byte) (int, error) { return 0, errSource }

// TestReader_SourceFailures: a read failure on the header is wrapped
// with its cause; one under a data row is PULSE_IMPORT_ROW_ERROR naming
// the 1-based row it hit; a callback error other than stop passes
// through unchanged.
func TestReader_SourceFailures(t *testing.T) {
	t.Parallel()

	r := NewReaderFromBytes([]byte("h\n"))
	if err := r.init(); err != nil {
		t.Fatal(err)
	}
	r.cr = stdcsv.NewReader(failingSource{})
	if _, err := r.ReadHeader(); err == nil || !stderrors.Is(err, errSource) {
		t.Errorf("header read failure: err = %v, want it wrapped", err)
	}

	r = NewReaderFromBytes([]byte("h\n1\n"))
	if _, err := r.ReadHeader(); err != nil {
		t.Fatal(err)
	}
	var seen int
	err := r.ReadRows(context.Background(), func([]string) error {
		seen++
		r.cr = stdcsv.NewReader(failingSource{}) // the source dies after row 1
		return nil
	})
	if !errors.HasCode(err, errors.PULSE_IMPORT_ROW_ERROR) {
		t.Fatalf("row read failure: err = %v, want PULSE_IMPORT_ROW_ERROR", err)
	}
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Details["row"] != 2 || seen != 1 {
		t.Errorf("row failure details = %v after %d rows, want row 2 after 1", ce.Details, seen)
	}

	sentinel := stderrors.New("callback refused")
	r = NewReaderFromBytes([]byte("h\n1\n2\n"))
	_, _ = r.ReadHeader()
	var calls int
	if err := r.ReadRows(context.Background(), func([]string) error { calls++; return sentinel }); err != sentinel || calls != 1 {
		t.Errorf("callback error = (%v after %d calls), want the sentinel after 1", err, calls)
	}
}
