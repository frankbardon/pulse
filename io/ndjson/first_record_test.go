package ndjson

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/io/csv"
	"github.com/spf13/afero"
)

// NDJSON has no header line, so Reader.ReadHeader derives the column
// names by decoding the FIRST JSON object. It used to leave that object
// consumed: ReadRows resumed after it and the record was lost — a
// 60-line file imported 59 rows and a 1-line file imported none, with no
// error and no warning. The tests below pin the first record into the
// row pass on every route a caller can take through the Reader.
//
// Scope note: the first object's keys still determine the column set.
// These tests fix a dropped ROW, not the column-union semantics.

// ndjsonDoc renders n objects, each carrying its own ordinal so a
// dropped record is visible in the VALUES and not only in the count.
func ndjsonDoc(n int) []byte {
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteString(`{"n":` + strconv.Itoa(i) + `,"tag":"r` + strconv.Itoa(i) + `"}` + "\n")
	}
	return []byte(b.String())
}

// ordinalBase offsets the import fixture's values away from {0,1},
// which inference reads as a packed_bool column. The offset keeps this
// test about the dropped record instead of about type inference.
const ordinalBase = 2

// ndjsonOrdinalDoc is the single-column variant the IMPORT tests use.
// The two-column document's "tag" column is deliberately all-distinct,
// which inference rejects as unbounded cardinality — a different
// complaint from the one under test here.
func ndjsonOrdinalDoc(n int) []byte {
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteString(`{"n":` + strconv.Itoa(i+ordinalBase) + `}` + "\n")
	}
	return []byte(b.String())
}

// collect drains a reader through ReadHeader + ReadRows.
func collect(t *testing.T, r *Reader) ([]string, [][]string) {
	t.Helper()
	header, err := r.ReadHeader()
	if err != nil {
		t.Fatalf("ReadHeader: %v", err)
	}
	var rows [][]string
	if err := r.ReadRows(context.Background(), func(row []string) error {
		rows = append(rows, append([]string(nil), row...))
		return nil
	}); err != nil {
		t.Fatalf("ReadRows: %v", err)
	}
	return header, rows
}

// TestNdjsonReader_FirstRecordReachesTheRowPass is the reader-level
// statement of the bug: every object in the document must be yielded by
// ReadRows, including the one ReadHeader looked at.
func TestNdjsonReader_FirstRecordReachesTheRowPass(t *testing.T) {
	for _, n := range []int{1, 2, 60} {
		t.Run(strconv.Itoa(n)+"-records", func(t *testing.T) {
			r := NewReaderFromBytes(ndjsonDoc(n))
			defer r.Close()

			header, rows := collect(t, r)
			if len(header) != 2 || header[0] != "n" || header[1] != "tag" {
				t.Fatalf("header = %v, want [n tag]", header)
			}
			if len(rows) != n {
				t.Fatalf("got %d rows, want %d — the first record was dropped", len(rows), n)
			}
			for i, row := range rows {
				if row[0] != strconv.Itoa(i) || row[1] != "r"+strconv.Itoa(i) {
					t.Errorf("row %d = %v, want [%d r%d]", i, row, i, i)
				}
			}
		})
	}
}

// TestNdjsonReader_ReadRowsWithoutHeaderYieldsEveryRecord takes the
// other route: ReadRows auto-calls ReadHeader, and the object it
// consumed there must still be emitted.
func TestNdjsonReader_ReadRowsWithoutHeaderYieldsEveryRecord(t *testing.T) {
	r := NewReaderFromBytes(ndjsonDoc(3))
	defer r.Close()

	var rows [][]string
	if err := r.ReadRows(context.Background(), func(row []string) error {
		rows = append(rows, append([]string(nil), row...))
		return nil
	}); err != nil {
		t.Fatalf("ReadRows: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3", len(rows))
	}
	if rows[0][0] != "0" {
		t.Errorf("rows[0] = %v, want the first record", rows[0])
	}
}

// TestNdjsonReader_ResetReplaysTheFirstRecord pins the rewind: a reader
// drained once and Reset must yield the same record set again, first
// record included. This is the route an INFERRED import takes — infer,
// Reset, ReadHeader, ReadRows.
func TestNdjsonReader_ResetReplaysTheFirstRecord(t *testing.T) {
	r := NewReaderFromBytes(ndjsonDoc(4))
	defer r.Close()

	_, first := collect(t, r)
	if err := r.Reset(); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	_, second := collect(t, r)

	if len(first) != 4 || len(second) != 4 {
		t.Fatalf("pass one %d rows, pass two %d rows, want 4 and 4", len(first), len(second))
	}
	for i := range first {
		if first[i][0] != second[i][0] || first[i][1] != second[i][1] {
			t.Errorf("row %d: pass one %v, pass two %v", i, first[i], second[i])
		}
	}
}

// TestNdjsonImport_InferredPathImportsEveryRecord is the end-to-end
// claim on the inferred route, checked on the VALUES: the cohort
// exported back out must start at the first source record.
func TestNdjsonImport_InferredPathImportsEveryRecord(t *testing.T) {
	for _, n := range []int{1, 60} {
		t.Run(strconv.Itoa(n)+"-records", func(t *testing.T) {
			fs := afero.NewMemMapFs()
			job := pio.NewImportJob(NewReaderFromBytes(ndjsonOrdinalDoc(n)), "inferred.pulse")
			job.FS = fs

			rep, err := job.Run(context.Background())
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if rep.RowsImported != n {
				t.Fatalf("RowsImported = %d, want %d (errors %v)", rep.RowsImported, n, rep.RowErrors)
			}
			assertExportedOrdinals(t, fs, "inferred.pulse", n)
		})
	}
}

// TestNdjsonImport_ExplicitSchemaImportsEveryRecord is the same claim on
// the explicit-schema route, which never calls Reset and reaches
// ReadHeader only through ReadRows' auto-init.
func TestNdjsonImport_ExplicitSchemaImportsEveryRecord(t *testing.T) {
	for _, n := range []int{1, 60} {
		t.Run(strconv.Itoa(n)+"-records", func(t *testing.T) {
			fs := afero.NewMemMapFs()
			job := pio.NewImportJob(NewReaderFromBytes(ndjsonOrdinalDoc(n)), "explicit.pulse")
			job.FS = fs
			job.Schema = &encoding.Schema{Fields: []encoding.Field{
				{Name: "n", Type: encoding.FieldTypeU8, CsvColumnIdx: 0},
			}}

			rep, err := job.Run(context.Background())
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if rep.RowsImported != n {
				t.Fatalf("RowsImported = %d, want %d (errors %v)", rep.RowsImported, n, rep.RowErrors)
			}
			assertExportedOrdinals(t, fs, "explicit.pulse", n)
		})
	}
}

// assertExportedOrdinals exports the cohort to CSV and checks the "n"
// column runs 0..n-1 in order. Counting rows alone would pass on a
// cohort that dropped record 0 and gained a duplicate somewhere else.
func assertExportedOrdinals(t *testing.T, fs afero.Fs, path string, n int) {
	t.Helper()
	w := csv.NewWriterToBuffer()
	job := pio.NewExportJob(path, w)
	job.FS = fs
	if _, err := job.Run(context.Background()); err != nil {
		t.Fatalf("Export: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("writer Close: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(w.Bytes())), "\n")
	if len(lines) != n+1 {
		t.Fatalf("exported %d lines, want %d (header + %d records)", len(lines), n+1, n)
	}
	col := -1
	for i, name := range strings.Split(lines[0], ",") {
		if name == "n" {
			col = i
		}
	}
	if col < 0 {
		t.Fatalf("no n column in exported header %q", lines[0])
	}
	for i := 1; i < len(lines); i++ {
		got := strings.Split(lines[i], ",")[col]
		if want := strconv.Itoa(i - 1 + ordinalBase); got != want {
			t.Errorf("exported record %d has n = %q, want %s", i-1, got, want)
		}
	}
}

// TestNdjsonReader_HeterogeneousKeysUnchanged is the scope boundary.
// Retaining the first record must not change which COLUMNS exist: the
// first object's keys are still the whole column set, and a key that
// appears only later is still dropped. Changing that is a separate
// design question.
func TestNdjsonReader_HeterogeneousKeysUnchanged(t *testing.T) {
	data := `{"name":"alice","age":30}
{"name":"bob","score":88.0}
{"name":"charlie","age":35}
`
	r := NewReaderFromBytes([]byte(data))
	defer r.Close()

	header, rows := collect(t, r)
	if len(header) != 2 || header[0] != "name" || header[1] != "age" {
		t.Fatalf("header = %v, want [name age] from the first object alone", header)
	}
	want := [][]string{{"alice", "30"}, {"bob", ""}, {"charlie", "35"}}
	if len(rows) != len(want) {
		t.Fatalf("got %d rows, want %d", len(rows), len(want))
	}
	for i := range want {
		if rows[i][0] != want[i][0] || rows[i][1] != want[i][1] {
			t.Errorf("row %d = %v, want %v", i, rows[i], want[i])
		}
	}
}

// TestNdjsonReader_MalformedFirstLineStillErrors keeps the parse-error
// route honest: the buffered first record is decoded once, in
// ReadHeader, and a malformed first line must not reach the row pass as
// a second, differently-coded failure.
func TestNdjsonReader_MalformedFirstLineStillErrors(t *testing.T) {
	r := NewReaderFromBytes([]byte("{bad json}\n{\"a\":1}\n"))
	defer r.Close()

	if _, err := r.ReadHeader(); err == nil {
		t.Fatal("expected a header parse error on a malformed first line")
	}
}

// TestNdjsonReader_LeadingBlankLinesSkipped confirms the buffered record
// is the first NON-EMPTY object, and that blank padding before it does
// not turn into a phantom row.
func TestNdjsonReader_LeadingBlankLinesSkipped(t *testing.T) {
	r := NewReaderFromBytes([]byte("\n\n{\"a\":1}\n{\"a\":2}\n"))
	defer r.Close()

	header, rows := collect(t, r)
	if len(header) != 1 || header[0] != "a" {
		t.Fatalf("header = %v, want [a]", header)
	}
	if len(rows) != 2 || rows[0][0] != "1" || rows[1][0] != "2" {
		t.Fatalf("rows = %v, want [[1] [2]]", rows)
	}
}

// TestNdjsonReader_CancellationBeforeTheFirstRecord pins the context
// check that guards the REPLAYED record. The scan loop's own check
// cannot cover it: on a one-record document there is no second
// iteration to reach, so without a check of its own a cancelled context
// would still deliver a row.
func TestNdjsonReader_CancellationBeforeTheFirstRecord(t *testing.T) {
	r := NewReaderFromBytes(ndjsonDoc(1))
	defer r.Close()

	if _, err := r.ReadHeader(); err != nil {
		t.Fatalf("ReadHeader: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var rows int
	err := r.ReadRows(ctx, func(row []string) error {
		rows++
		return nil
	})
	if err == nil {
		t.Fatal("expected the cancelled context to be reported")
	}
	if rows != 0 {
		t.Errorf("delivered %d rows under a cancelled context, want 0", rows)
	}
}
