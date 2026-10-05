package parquet

import (
	"bytes"
	"context"
	stderrors "errors"
	"testing"
	"time"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"
	"github.com/apache/arrow-go/v18/arrow/memory"
	"github.com/apache/arrow-go/v18/parquet"
	"github.com/apache/arrow-go/v18/parquet/pqarrow"
	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	pio "github.com/frankbardon/pulse/internal/io"
)

type tsCollector struct{ cells []string }

func (w *tsCollector) WriteHeader([]string) error { return nil }
func (w *tsCollector) WriteRow(v []any) error {
	s, _ := v[0].(string)
	w.cells = append(w.cells, s)
	return nil
}
func (w *tsCollector) Close() error { return nil }

func unitTicks(t time.Time, unit arrow.TimeUnit) arrow.Timestamp {
	ns := t.UnixNano()
	switch unit {
	case arrow.Millisecond:
		return arrow.Timestamp(ns / 1e6)
	case arrow.Microsecond:
		return arrow.Timestamp(ns / 1e3)
	}
	return arrow.Timestamp(ns)
}

// tsParquet writes a one-column Parquet file of the given Arrow timestamp
// type. int96 stores the column as legacy INT96 (nanosecond only).
func tsParquet(t *testing.T, dt *arrow.TimestampType, vals []arrow.Timestamp, int96 bool) []byte {
	t.Helper()
	sc := arrow.NewSchema([]arrow.Field{{Name: "ts", Type: dt, Nullable: true}}, nil)
	b := array.NewRecordBuilder(memory.NewGoAllocator(), sc)
	defer b.Release()
	b.Field(0).(*array.TimestampBuilder).AppendValues(vals, nil)
	rec := b.NewRecordBatch()
	defer rec.Release()

	var buf bytes.Buffer
	arrowProps := pqarrow.NewArrowWriterProperties(pqarrow.WithDeprecatedInt96Timestamps(int96))
	pw, err := pqarrow.NewFileWriter(sc, &buf, parquet.NewWriterProperties(), arrowProps)
	if err != nil {
		t.Fatal(err)
	}
	if err := pw.Write(rec); err != nil {
		t.Fatal(err)
	}
	if err := pw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func tsImport(t *testing.T, data []byte, sourceTZ string, policy pio.DSTPolicy) (*pio.ImportReport, []string, error) {
	t.Helper()
	fs := afero.NewMemMapFs()
	job := pio.NewImportJob(NewReaderFromBytes(data), "ts.pulse")
	job.FS = fs
	job.SourceTZ = sourceTZ
	job.DSTPolicy = policy
	rep, err := job.Run(context.Background())
	if err != nil {
		return nil, nil, err
	}
	if got := rep.Schema.Field("ts").Type; got != encoding.FieldTypeDateTime {
		t.Fatalf("inferred ts type = %s, want datetime", got)
	}
	w := &tsCollector{}
	exp := pio.NewExportJob("ts.pulse", w)
	exp.FS = fs
	if _, err := exp.Run(context.Background()); err != nil {
		t.Fatalf("Export: %v", err)
	}
	return rep, w.cells, nil
}

func truncated(rep *pio.ImportReport) []*perr.CodedError {
	var out []*perr.CodedError
	for _, w := range rep.SourceWarnings {
		if w.Code == perr.PULSE_IMPORT_TIMESTAMP_TRUNCATED {
			out = append(out, w)
		}
	}
	return out
}

// TestParquetTimestamp_AdjustedNaiveAndInt96 covers every Parquet
// timestamp unit, isAdjustedToUTC=true (an instant: the source zone is
// ignored) and false (a wall clock: it applies), plus legacy INT96, which
// pqarrow surfaces as a UTC-zoned type but Pulse reads as naive.
func TestParquetTimestamp_AdjustedNaiveAndInt96(t *testing.T) {
	summer := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	frac := summer.Add(1500 * time.Millisecond)
	cases := []struct {
		name  string
		unit  arrow.TimeUnit
		tz    string
		int96 bool
		naive bool
	}{
		{"ms/adjusted", arrow.Millisecond, "UTC", false, false},
		{"ms/naive", arrow.Millisecond, "", false, true},
		{"us/adjusted", arrow.Microsecond, "UTC", false, false},
		{"us/naive", arrow.Microsecond, "", false, true},
		{"ns/adjusted", arrow.Nanosecond, "UTC", false, false},
		{"ns/naive", arrow.Nanosecond, "", false, true},
		{"ns/int96", arrow.Nanosecond, "UTC", true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := tsParquet(t, &arrow.TimestampType{Unit: tc.unit, TimeZone: tc.tz},
				[]arrow.Timestamp{unitTicks(summer, tc.unit), unitTicks(frac, tc.unit)}, tc.int96)

			rep, got, err := tsImport(t, data, "", "")
			if err != nil {
				t.Fatal(err)
			}
			if got[0] != "2026-07-01T12:00:00Z" || got[1] != "2026-07-01T12:00:01Z" {
				t.Errorf("no source zone = %v, want 12:00:00Z / 12:00:01Z", got)
			}
			if tw := truncated(rep); len(tw) != 1 || tw[0].Details["truncated_n"] != 1 {
				t.Errorf("truncation warnings = %v, want exactly one with truncated_n 1", tw)
			}

			_, got, err = tsImport(t, data, "Europe/Berlin", "")
			if err != nil {
				t.Fatal(err)
			}
			want := "2026-07-01T12:00:00Z"
			if tc.naive {
				want = "2026-07-01T10:00:00Z"
			}
			if got[0] != want {
				t.Errorf("Europe/Berlin row 0 = %q, want %q", got[0], want)
			}
		})
	}
}

// TestParquetTimestamp_WholeSecondsAndPreEpoch: no warning without a
// fraction; a pre-1970 fraction floors toward the past.
func TestParquetTimestamp_WholeSecondsAndPreEpoch(t *testing.T) {
	data := tsParquet(t, &arrow.TimestampType{Unit: arrow.Microsecond, TimeZone: "UTC"},
		[]arrow.Timestamp{0, -1_000_000}, false)
	rep, got, err := tsImport(t, data, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if tw := truncated(rep); len(tw) != 0 {
		t.Errorf("whole seconds warned: %v", tw)
	}
	if got[1] != "1969-12-31T23:59:59Z" {
		t.Errorf("row 1 = %q", got[1])
	}

	data = tsParquet(t, &arrow.TimestampType{Unit: arrow.Microsecond, TimeZone: "UTC"},
		[]arrow.Timestamp{-500_000}, false)
	_, got, err = tsImport(t, data, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if got[0] != "1969-12-31T23:59:59Z" {
		t.Errorf("-0.5 s = %q, want 1969-12-31T23:59:59Z (floor, not toward zero)", got[0])
	}
}

// TestParquetTimestamp_Int96RespectsDSTPolicy: INT96 is a naive wall
// clock, so a Berlin spring-forward gap is refused naming the row.
func TestParquetTimestamp_Int96RespectsDSTPolicy(t *testing.T) {
	gap := time.Date(2026, 3, 29, 2, 30, 0, 0, time.UTC) // Berlin skips 02:30
	data := tsParquet(t, arrow.FixedWidthTypes.Timestamp_ns.(*arrow.TimestampType),
		[]arrow.Timestamp{unitTicks(gap.Add(-2*time.Hour), arrow.Nanosecond), unitTicks(gap, arrow.Nanosecond)}, true)
	_, _, err := tsImport(t, data, "Europe/Berlin", "")
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_IMPORT_DST_NONEXISTENT {
		t.Fatalf("err = %v, want PULSE_IMPORT_DST_NONEXISTENT", err)
	}
	if ce.Details["row"] != 2 {
		t.Errorf("refusal row = %v, want 2", ce.Details["row"])
	}
}
