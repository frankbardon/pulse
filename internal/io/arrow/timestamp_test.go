package arrow

import (
	"context"
	stderrors "errors"
	"testing"
	"time"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"
	"github.com/apache/arrow-go/v18/arrow/memory"
	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	pio "github.com/frankbardon/pulse/internal/io"
)

// tsCollector keeps the exported datetime column as text.
type tsCollector struct{ cells []string }

func (w *tsCollector) WriteHeader([]string) error { return nil }
func (w *tsCollector) WriteRow(v []any) error {
	s, _ := v[0].(string)
	w.cells = append(w.cells, s)
	return nil
}
func (w *tsCollector) Close() error { return nil }

// ticks converts a UTC instant plus a sub-second offset in nanoseconds to
// a count of unit ticks (the offset must be representable in the unit).
func ticks(t time.Time, unit arrow.TimeUnit) arrow.Timestamp {
	ns := t.UnixNano()
	switch unit {
	case arrow.Second:
		return arrow.Timestamp(ns / 1e9)
	case arrow.Millisecond:
		return arrow.Timestamp(ns / 1e6)
	case arrow.Microsecond:
		return arrow.Timestamp(ns / 1e3)
	}
	return arrow.Timestamp(ns)
}

// tsArrow builds a one-column Arrow IPC file of the given timestamp type.
// A nil entry in vals is a null.
func tsArrow(t *testing.T, dt *arrow.TimestampType, vals []*arrow.Timestamp) []byte {
	t.Helper()
	sc := arrow.NewSchema([]arrow.Field{{Name: "ts", Type: dt, Nullable: true}}, nil)
	b := array.NewRecordBuilder(memory.NewGoAllocator(), sc)
	defer b.Release()
	tb := b.Field(0).(*array.TimestampBuilder)
	for _, v := range vals {
		if v == nil {
			tb.AppendNull()
			continue
		}
		tb.Append(*v)
	}
	rec := b.NewRecordBatch()
	defer rec.Release()
	return helperWriteArrow(t, sc, []arrow.RecordBatch{rec})
}

// tsImport runs an INFERRED import of src and returns the report plus the
// datetime column exported back as canonical UTC literals.
func tsImport(t *testing.T, src pio.Reader, sourceTZ string, policy pio.DSTPolicy) (*pio.ImportReport, []string, error) {
	t.Helper()
	fs := afero.NewMemMapFs()
	job := pio.NewImportJob(src, "ts.pulse")
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

func ptr(v arrow.Timestamp) *arrow.Timestamp { return &v }

func truncationWarnings(rep *pio.ImportReport) []*perr.CodedError {
	var out []*perr.CodedError
	for _, w := range rep.SourceWarnings {
		if w.Code == perr.PULSE_IMPORT_TIMESTAMP_TRUNCATED {
			out = append(out, w)
		}
	}
	return out
}

// TestTimestamp_UnitsZonesAndTruncation is the native-timestamp matrix:
// every unit, zoned (an instant — the source zone is ignored) and naive
// (a wall clock — the source zone applies), each inferred as datetime,
// with the truncation warning firing exactly once iff a fraction existed.
func TestTimestamp_UnitsZonesAndTruncation(t *testing.T) {
	summer := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	frac := summer.Add(250 * time.Millisecond)
	for _, unit := range []arrow.TimeUnit{arrow.Second, arrow.Millisecond, arrow.Microsecond, arrow.Nanosecond} {
		for _, tz := range []string{"", "UTC", "America/New_York"} {
			naive := tz == ""
			t.Run(unit.String()+"/"+tz, func(t *testing.T) {
				dt := &arrow.TimestampType{Unit: unit, TimeZone: tz}
				vals := []*arrow.Timestamp{ptr(ticks(summer, unit)), nil, ptr(ticks(frac, unit))}
				data := tsArrow(t, dt, vals)

				rep, got, err := tsImport(t, NewReaderFromBytes(data), "", "")
				if err != nil {
					t.Fatal(err)
				}
				want := []string{"2026-07-01T12:00:00Z", "", "2026-07-01T12:00:00Z"}
				for i := range want {
					if got[i] != want[i] {
						t.Errorf("no source zone row %d = %q, want %q", i, got[i], want[i])
					}
				}
				tw := truncationWarnings(rep)
				if unit == arrow.Second {
					if len(tw) != 0 {
						t.Errorf("whole-second unit warned: %v", tw)
					}
				} else if len(tw) != 1 || tw[0].Details["truncated_n"] != 1 {
					t.Errorf("truncation warnings = %v, want exactly one with truncated_n 1", tw)
				}

				_, got, err = tsImport(t, NewReaderFromBytes(data), "Europe/Berlin", "")
				if err != nil {
					t.Fatal(err)
				}
				wantBerlin := "2026-07-01T12:00:00Z" // an instant ignores the source zone
				if naive {
					wantBerlin = "2026-07-01T10:00:00Z" // a wall clock reads in CEST
				}
				if got[0] != wantBerlin {
					t.Errorf("Europe/Berlin row 0 = %q, want %q", got[0], wantBerlin)
				}
			})
		}
	}
}

// TestTimestamp_WholeSecondsNeverWarn: a fractional unit holding only
// whole seconds raises no truncation warning.
func TestTimestamp_WholeSecondsNeverWarn(t *testing.T) {
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	data := tsArrow(t, &arrow.TimestampType{Unit: arrow.Nanosecond},
		[]*arrow.Timestamp{ptr(ticks(at, arrow.Nanosecond)), ptr(ticks(at.Add(time.Hour), arrow.Nanosecond))})
	rep, got, err := tsImport(t, NewReaderFromBytes(data), "", "")
	if err != nil {
		t.Fatal(err)
	}
	if tw := truncationWarnings(rep); len(tw) != 0 {
		t.Errorf("whole seconds warned: %v", tw)
	}
	if got[1] != "2026-01-02T04:04:05Z" {
		t.Errorf("row 1 = %q", got[1])
	}
}

// TestTimestamp_PreEpochFloorsTowardThePast: Go's integer division
// truncates toward zero, which would move -1.5 s to -1 s.
func TestTimestamp_PreEpochFloorsTowardThePast(t *testing.T) {
	data := tsArrow(t, &arrow.TimestampType{Unit: arrow.Millisecond, TimeZone: "UTC"},
		[]*arrow.Timestamp{ptr(-1500), ptr(-1000), ptr(-1)})
	rep, got, err := tsImport(t, NewReaderFromBytes(data), "", "")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"1969-12-31T23:59:58Z", "1969-12-31T23:59:59Z", "1969-12-31T23:59:59Z"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row %d = %q, want %q", i, got[i], want[i])
		}
	}
	if tw := truncationWarnings(rep); len(tw) != 1 || tw[0].Details["truncated_n"] != 2 {
		t.Errorf("truncation warnings = %v, want one with truncated_n 2", tw)
	}
}

// TestTimestamp_NaiveRespectsDSTPolicy: a naive native wall clock in a
// DST overlap is refused under the default policy, naming the row, and
// resolved (with the count-bearing warning) under "earlier".
func TestTimestamp_NaiveRespectsDSTPolicy(t *testing.T) {
	ok := time.Date(2026, 10, 25, 1, 0, 0, 0, time.UTC)
	overlap := time.Date(2026, 10, 25, 2, 30, 0, 0, time.UTC) // Berlin shows 02:30 twice
	data := tsArrow(t, &arrow.TimestampType{Unit: arrow.Microsecond},
		[]*arrow.Timestamp{ptr(ticks(ok, arrow.Microsecond)), ptr(ticks(overlap, arrow.Microsecond))})

	_, _, err := tsImport(t, NewReaderFromBytes(data), "Europe/Berlin", "")
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_IMPORT_DST_AMBIGUOUS {
		t.Fatalf("err = %v, want PULSE_IMPORT_DST_AMBIGUOUS", err)
	}
	if ce.Details["row"] != 2 {
		t.Errorf("refusal row = %v, want 2", ce.Details["row"])
	}

	rep, got, err := tsImport(t, NewReaderFromBytes(data), "Europe/Berlin", pio.DSTPolicyEarlier)
	if err != nil {
		t.Fatal(err)
	}
	if got[1] != "2026-10-25T00:30:00Z" { // first occurrence, CEST +02:00
		t.Errorf("earlier-resolved row = %q, want 2026-10-25T00:30:00Z", got[1])
	}
	if len(rep.ZoneWarnings) != 1 || rep.ZoneWarnings[0].Code != perr.PULSE_IMPORT_DST_RESOLVED {
		t.Errorf("ZoneWarnings = %v, want one PULSE_IMPORT_DST_RESOLVED", rep.ZoneWarnings)
	}
}

func TestTypeToPulse_TimestampIsDateTime(t *testing.T) {
	for _, dt := range []arrow.DataType{
		&arrow.TimestampType{Unit: arrow.Second},
		arrow.FixedWidthTypes.Timestamp_ns,
		&arrow.TimestampType{Unit: arrow.Millisecond, TimeZone: "Europe/Berlin"},
	} {
		if got := TypeToPulse(dt); got != encoding.FieldTypeDateTime {
			t.Errorf("TypeToPulse(%v) = %s, want datetime", dt, got)
		}
	}
}
