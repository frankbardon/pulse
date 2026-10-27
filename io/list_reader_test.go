package io_test

import (
	"bytes"
	"context"
	stderrors "errors"
	"math"
	"strconv"
	"strings"
	"testing"

	perrors "github.com/frankbardon/pulse/errors"
	pio "github.com/frankbardon/pulse/io"
	"github.com/spf13/afero"
)

// An embedder's own reader gets the typed list channel the built-in list
// sources use: implementing the public pio.ListAwareReader is all a
// column-less vector declaration needs.

// listRow is one source record: an id and a list cell (nil = null cell;
// a NaN element = null element).
type listRow struct {
	id   int
	list []float64
}

func listFixture() []listRow {
	nan := math.NaN()
	return []listRow{
		{1, []float64{1, 2, 3}},
		{2, []float64{4.5, nan, 6}},
		{3, nil},
		{4, []float64{nan, nan, nan}},
		{5, []float64{7, 8, 9.25}},
	}
}

// listNDJSON is the same fixture as NDJSON with JSON arrays.
const listNDJSON = `{"id":1,"v":[1,2,3]}
{"id":2,"v":[4.5,null,6]}
{"id":3,"v":null}
{"id":4,"v":[null,null,null]}
{"id":5,"v":[7,8,9.25]}
`

// memListReader is a custom (non-built-in) source over listRow: a plain
// pio.ResetReader whose "v" cell text is deliberately junk, so only the
// typed channel can carry the elements.
type memListReader struct {
	rows []listRow
	cur  int
	// omitNulls leaves ListCell.Nulls nil on a list with no null
	// element, as the contract allows.
	omitNulls bool
	// zeroAtNull writes a null element's value as 0 straight into
	// Values (bypassing Append): Nulls alone marks it.
	zeroAtNull bool
	// fill, when set, replaces the RowList body (malformed-cell cases).
	fill func(row int, dst *pio.ListCell)
	// fail, when set, is RowList's error for the row (nil = read it):
	// the source failing to READ a cell, not a cell it cannot hold.
	fail func(row int) error
	// resets counts Reset calls (the dim probe rewinds once).
	resets int
}

func (r *memListReader) ReadHeader() ([]string, error) { return []string{"id", "v"}, nil }
func (r *memListReader) Reset() error                  { r.cur = -1; r.resets++; return nil }
func (r *memListReader) Close() error                  { return nil }

func (r *memListReader) ReadRows(ctx context.Context, fn func([]string) error) error {
	for i := range r.rows {
		r.cur = i
		if err := fn([]string{strconv.Itoa(r.rows[i].id), "not|the|elements"}); err != nil {
			return err
		}
	}
	return nil
}

func (r *memListReader) RowList(col int, dst *pio.ListCell) error {
	if r.fail != nil {
		if err := r.fail(r.cur); err != nil {
			return err
		}
	}
	if col != 1 {
		dst.Reset(pio.ListNotList)
		dst.BadValue = "id"
		return nil
	}
	if r.fill != nil {
		r.fill(r.cur, dst)
		return nil
	}
	l := r.rows[r.cur].list
	if l == nil {
		dst.Reset(pio.ListNull)
		return nil
	}
	dst.Reset(pio.ListValues)
	anyNull := false
	for _, v := range l {
		null := math.IsNaN(v)
		anyNull = anyNull || null
		if r.zeroAtNull && null {
			dst.Values = append(dst.Values, 0)
			dst.Nulls = append(dst.Nulls, true)
			continue
		}
		dst.Append(v, null)
	}
	if r.omitNulls && !anyNull {
		dst.Nulls = nil // documented: nil means no null element
	}
	return nil
}

var _ pio.ListAwareReader = (*memListReader)(nil)
var _ pio.ResetReader = (*memListReader)(nil)

// plainReader hides the list channel: Reader + ResetReader only.
type plainReader struct{ inner *memListReader }

func (p plainReader) ReadHeader() ([]string, error) { return p.inner.ReadHeader() }
func (p plainReader) ReadRows(ctx context.Context, fn func([]string) error) error {
	return p.inner.ReadRows(ctx, fn)
}
func (p plainReader) Close() error { return p.inner.Close() }
func (p plainReader) Reset() error { return p.inner.Reset() }

// importVector imports src with one column-less vector declaration "v"
// and returns the cohort bytes.
func importVector(t *testing.T, src pio.Reader) ([]byte, error) {
	t.Helper()
	return importVectorTo(t, afero.NewMemMapFs(), src)
}

// importVectorTo is importVector onto a caller-held filesystem, so a
// refusal can be checked for having written nothing.
func importVectorTo(t *testing.T, fs afero.Fs, src pio.Reader) ([]byte, error) {
	t.Helper()
	job := pio.NewImportJob(src, "out.pulse")
	job.FS = fs
	job.Vectors = []pio.VectorDecl{{Name: "v"}}
	if _, err := job.Run(context.Background()); err != nil {
		return nil, err
	}
	raw, err := afero.ReadFile(fs, "out.pulse")
	if err != nil {
		t.Fatal(err)
	}
	return raw, nil
}

func vectorReason(t *testing.T, err error) string {
	t.Helper()
	var ce *perrors.CodedError
	if !stderrors.As(err, &ce) {
		t.Fatalf("error %v is not coded", err)
	}
	if ce.Code != perrors.PULSE_IMPORT_VECTOR_INVALID {
		t.Fatalf("code = %s, want PULSE_IMPORT_VECTOR_INVALID (%v)", ce.Code, err)
	}
	r, _ := ce.Details["reason"].(string)
	return r
}

func TestListAwareReader_CustomReaderMatchesNDJSON(t *testing.T) {
	t.Parallel()

	nd, err := pio.NewReaderFromBytes(pio.FormatNDJSON, []byte(listNDJSON), pio.ReaderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	want, err := importVector(t, nd)
	if err != nil {
		t.Fatalf("NDJSON list import: %v", err)
	}
	for name, rd := range map[string]*memListReader{
		"append":       {rows: listFixture()},
		"nil_nulls":    {rows: listFixture(), omitNulls: true},
		"zero_at_null": {rows: listFixture(), zeroAtNull: true},
	} {
		got, err := importVector(t, rd)
		if err != nil {
			t.Fatalf("custom list import (%s): %v", name, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("custom reader cohort (%s, %d bytes) differs from the NDJSON list cohort (%d bytes)", name, len(got), len(want))
		}
	}
}

func TestListAwareReader_CustomReaderWithoutInterfaceRefused(t *testing.T) {
	t.Parallel()

	_, err := importVector(t, plainReader{&memListReader{rows: listFixture()}})
	if got := vectorReason(t, err); got != "no_columns" {
		t.Fatalf("reason = %q, want no_columns", got)
	}
}

func TestListAwareReader_ContractRefusals(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		fill   func(row int, dst *pio.ListCell)
		reason string
	}{
		{"nulls length mismatch", func(row int, dst *pio.ListCell) {
			dst.Reset(pio.ListValues)
			dst.Values = append(dst.Values, 1, 2, 3)
			dst.Nulls = append(dst.Nulls, false)
		}, "malformed_list_cell"},
		{"unknown state", func(row int, dst *pio.ListCell) {
			dst.Reset(pio.ListState(42))
		}, "malformed_list_cell"},
		{"non-numeric element", func(row int, dst *pio.ListCell) {
			dst.Reset(pio.ListValues)
			dst.Append(1, false)
			dst.State, dst.BadIndex, dst.BadValue = pio.ListNonNumeric, 1, `"x"`
		}, "element_not_numeric"},
		{"not a list", func(row int, dst *pio.ListCell) {
			dst.Reset(pio.ListNotList)
			dst.BadValue = "7"
		}, "not_a_list"},
		{"length varies", func(row int, dst *pio.ListCell) {
			dst.Reset(pio.ListValues)
			for e := 0; e < 2+row; e++ {
				dst.Append(float64(e), false)
			}
		}, "list_length_varies"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := importVector(t, &memListReader{rows: listFixture(), fill: c.fill})
			if got := vectorReason(t, err); got != c.reason {
				t.Fatalf("reason = %q, want %q (%v)", got, c.reason, err)
			}
		})
	}
}

// errSourceRead is a source's own failure to read a cell — I/O, decode,
// a corrupt batch — as opposed to a readable cell the vector cannot hold.
var errSourceRead = stderrors.New("batch 2: checksum mismatch")

// failAtRow fails RowList with err on the 0-based source row at.
func failAtRow(at int, err error) func(int) error {
	return func(row int) error {
		if row == at {
			return err
		}
		return nil
	}
}

// importRefused imports src, demands a refusal, and demands that no
// cohort was written.
func importRefused(t *testing.T, src pio.Reader) error {
	t.Helper()
	fs := afero.NewMemMapFs()
	_, err := importVectorTo(t, fs, src)
	if err == nil {
		t.Fatal("import succeeded; a RowList read failure must abort it")
	}
	if ok, _ := afero.Exists(fs, "out.pulse"); ok {
		t.Fatal("a refused import wrote out.pulse")
	}
	return err
}

// A RowList error is the source failing to READ the cell. It aborts the
// import exactly as a ReadRows failure does — never restamped: an uncoded
// error stays uncoded (the CLI's placeholder code, as for ReadRows), the
// cause stays reachable, and the vector, column and row ride the message.
func TestListAwareReader_RowListPlainErrorAbortsImport(t *testing.T) {
	t.Parallel()

	src := &memListReader{rows: listFixture(), fail: failAtRow(2, errSourceRead)}
	err := importRefused(t, src)
	if !stderrors.Is(err, errSourceRead) {
		t.Fatalf("error %v does not wrap the source's own error", err)
	}
	var ce *perrors.CodedError
	if stderrors.As(err, &ce) {
		t.Fatalf("an uncoded source failure was restamped %s: %v", ce.Code, err)
	}
	for _, want := range []string{`vector "v"`, `column "v"`, "row 3"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not name %s", err, want)
		}
	}
	if src.resets == 0 {
		t.Fatal("row 3 failed before the dim probe rewound; want a row-pass failure")
	}

	// Parity: the same failure raised by ReadRows travels the same way.
	_, rrErr := importVector(t, &failingRowsReader{memListReader{rows: listFixture()}})
	if !stderrors.Is(rrErr, errSourceRead) {
		t.Fatalf("ReadRows failure = %v, want errSourceRead", rrErr)
	}
	if stderrors.As(rrErr, &ce) {
		t.Fatalf("a ReadRows failure is coded %s; RowList parity assumes it is not", ce.Code)
	}
}

// A coded RowList error keeps its own code and details; the vector,
// column and row are added beside them.
func TestListAwareReader_RowListCodedErrorKeepsCode(t *testing.T) {
	t.Parallel()

	coded := perrors.NewCodedErrorWithDetails(perrors.DATA_FILE, "batch 2: checksum mismatch", map[string]any{"batch": 2})
	err := importRefused(t, &memListReader{rows: listFixture(), fail: failAtRow(2, coded)})
	var ce *perrors.CodedError
	if !stderrors.As(err, &ce) {
		t.Fatalf("error %v is not coded", err)
	}
	if ce.Code != perrors.DATA_FILE {
		t.Fatalf("code = %s, want the source's own DATA_FILE (%v)", ce.Code, err)
	}
	if !stderrors.Is(err, coded) {
		t.Fatalf("error %v does not wrap the source's own error", err)
	}
	want := map[string]any{"batch": 2, "vector": "v", "column": "v", "row": 3}
	for k, v := range want {
		if ce.Details[k] != v {
			t.Fatalf("details[%q] = %v, want %v (details %v)", k, ce.Details[k], v, ce.Details)
		}
	}
	if len(coded.Details) != 1 {
		t.Fatalf("the source's own error was mutated: details %v", coded.Details)
	}
}

// A RowList error while the dim is being probed aborts the import there,
// before the probe rewinds the source.
func TestListAwareReader_RowListErrorDuringDimProbe(t *testing.T) {
	t.Parallel()

	src := &memListReader{rows: listFixture(), fail: failAtRow(0, errSourceRead)}
	err := importRefused(t, src)
	if !stderrors.Is(err, errSourceRead) {
		t.Fatalf("error %v does not wrap the source's own error", err)
	}
	if !strings.Contains(err.Error(), "row 1") {
		t.Fatalf("error %q does not name row 1", err)
	}
	if src.resets != 0 {
		t.Fatalf("source rewound %d time(s); the probe must abort before rewinding", src.resets)
	}
}

// failingRowsReader fails ReadRows itself with errSourceRead.
type failingRowsReader struct{ memListReader }

func (r *failingRowsReader) ReadRows(ctx context.Context, fn func([]string) error) error {
	return errSourceRead
}
