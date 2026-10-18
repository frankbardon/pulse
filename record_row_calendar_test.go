package pulse_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// calendarCohort writes three records out of date order: id (u8), d
// (date) and ts (datetime, d's midnight + 03:04:05). id 1 → 2026-01-03,
// id 2 → 2025-12-31, id 3 → 2026-01-01.
func calendarCohort(t *testing.T, fs afero.Fs, path string) {
	t.Helper()
	sch := &encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU8, ByteOffset: 0},
		{Name: "d", Type: encoding.FieldTypeDate, ByteOffset: 1},
		{Name: "ts", Type: encoding.FieldTypeDateTime, ByteOffset: 5},
	}}
	var buf bytes.Buffer
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(encx.WritePreamble(&buf, sch))
	for i, day := range []int64{20456, 20453, 20454} {
		must(encoding.WriteFieldValue(&buf, encoding.FieldTypeU8, uint64(i+1)))
		must(encoding.WriteFieldValue(&buf, encoding.FieldTypeDate, uint64(day)))
		must(encoding.WriteFieldValue(&buf, encoding.FieldTypeDateTime, uint64(day*86400+3*3600+4*60+5)))
		must(encoding.WriteBitmap(&buf, make([]byte, sch.BitmapByteSize())))
	}
	must(afero.WriteFile(fs, path, buf.Bytes(), 0o644))
}

// calendarRequest is an ungrouped windowed request over calendarCohort:
// a WIN_LAG copying d (a date value), a WIN_DELTA and a WIN_RUNNING_SUM
// over d (numbers of days — they read the epoch day, so they would be
// null if a window saw the calendar string), a WIN_ROW_NUMBER ordered by
// ts, and Request.Sort by d.
func calendarRequest() *types.Request {
	byD := []types.OrderKey{{Field: "d"}}
	return &types.Request{Cohort: &types.Cohort{Filename: "cal.pulse"},
		Windows: []*types.Window{
			{Type: types.WIN_LAG, Field: "d", Label: "prev_d", OrderBy: byD},
			{Type: types.WIN_DELTA, Field: "d", Label: "dd", OrderBy: byD},
			{Type: types.WIN_RUNNING_SUM, Field: "d", Label: "rs", OrderBy: byD, Frame: &types.FrameSpec{Mode: "rows"}},
			{Type: types.WIN_ROW_NUMBER, Label: "rn", OrderBy: []types.OrderKey{{Field: "ts", Desc: true}}},
		},
		Sort: byD}
}

// calendarRowsWant is calendarRequest's rows as JSON, chronological.
const calendarRowsWant = `[` +
	`{"d":"2025-12-31","dd":null,"id":2,"prev_d":null,"rn":3,"rs":61363,"ts":"2025-12-31T03:04:05Z"},` +
	`{"d":"2026-01-01","dd":1,"id":3,"prev_d":"2025-12-31","rn":2,"rs":61363,"ts":"2026-01-01T03:04:05Z"},` +
	`{"d":"2026-01-03","dd":2,"id":1,"prev_d":"2026-01-01","rn":1,"rs":61363,"ts":"2026-01-03T03:04:05Z"}]`

// TestProcessRecordRows_RenderCalendarLiterals: Process record rows
// (an ungrouped windowed run) render date as YYYY-MM-DD and datetime as
// RFC 3339 UTC (`…Z`) — as Sample, Lookup and export do — on Process,
// ProcessStream and Compose (a chain stage cannot emit record rows:
// every stage needs an aggregator, PULSE_CHAIN_NOT_MERGEABLE). Request.Sort and every
// window still read the epoch numbers: the rows order chronologically,
// WIN_DELTA / WIN_RUNNING_SUM over the date are day counts, the
// ts-ordered row number follows time, and a WIN_LAG copy of the date
// renders as a date.
func TestProcessRecordRows_RenderCalendarLiterals(t *testing.T) {
	fs := afero.NewMemMapFs()
	calendarCohort(t, fs, "cal.pulse")
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	surfaces := map[string]func() []map[string]any{
		"process": func() []map[string]any {
			resp, err := p.Process(ctx, calendarRequest())
			if err != nil {
				t.Fatal(err)
			}
			return resp.Data
		},
		"process stream": func() []map[string]any {
			it, err := p.ProcessStream(ctx, calendarRequest())
			if err != nil {
				t.Fatal(err)
			}
			defer it.Close()
			var rows []map[string]any
			for {
				row, ok, err := it.Next(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if !ok {
					return rows
				}
				rows = append(rows, row)
			}
		},
		"compose": func() []map[string]any {
			resp, err := p.Compose(ctx, &types.ComposedRequest{Requests: []*types.Request{calendarRequest()}})
			if err != nil {
				t.Fatal(err)
			}
			return resp.Responses[0].Data
		},
	}
	for name, rowsOf := range surfaces {
		t.Run(name, func(t *testing.T) {
			b, err := json.Marshal(rowsOf())
			if err != nil {
				t.Fatal(err)
			}
			if string(b) != calendarRowsWant {
				t.Errorf("rows\n got %s\nwant %s", b, calendarRowsWant)
			}
		})
	}
}

// TestProcessRecordRows_TemporalColumnRules pins which record-row
// columns spell a calendar literal: the schema's date / datetime
// fields and a WIN_LAG / WIN_LEAD copy of one (a lag of a lead too —
// value windows accept date, not datetime); a rank stays a number.
func TestProcessRecordRows_TemporalColumnRules(t *testing.T) {
	fs := afero.NewMemMapFs()
	calendarCohort(t, fs, "cal.pulse")
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	byID := []types.OrderKey{{Field: "id"}}
	resp, err := p.Process(context.Background(), &types.Request{Cohort: &types.Cohort{Filename: "cal.pulse"},
		Windows: []*types.Window{
			{Type: types.WIN_LEAD, Field: "d", Label: "next_d", OrderBy: byID},
			{Type: types.WIN_LAG, Field: "next_d", Label: "back_d", OrderBy: byID},
			{Type: types.WIN_RANK, Label: "rk", OrderBy: []types.OrderKey{{Field: "d"}}},
		},
		Sort: byID})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range resp.Data {
		got = append(got, fmt.Sprintf("%v|%v|%v", r["next_d"], r["back_d"], r["rk"]))
	}
	want := "[2025-12-31|<nil>|3 2026-01-01|2025-12-31|1 <nil>|2026-01-01|2]"
	if fmt.Sprint(got) != want {
		t.Errorf("next_d|back_d|rk = %v\nwant %s", got, want)
	}
}
