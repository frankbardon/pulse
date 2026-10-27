package pulse_test

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Calendar formulas (U45 E4-S1, TODO #316): weekday / month_index /
// date_trunc over epoch days, to_day over a datetime in the request's
// inherited zone, log1p — through every expression site and execution
// arm, against a stdlib oracle.

const calfCohort = "calendar.pulse"

// calfRows is the calendar cohort's row count.
const calfRows = 60

// calendarDay is row r's `d` (a date, epoch days): every 11th day from
// 2023-12-08, so the rows span two DST cycles, leap day 2024 and two
// year ends.
func calfDay(r int) int64 { return 19699 + int64(r)*11 }

// calendarSecond is row r's `ts` (a datetime): its day at 02:00Z (the
// previous local day in New York all year), 12:00Z (the same day all
// year) or 04:30Z (the same day under EDT, the previous under EST).
func calfSecond(r int) int64 {
	off := [3]int64{2 * 3600, 12 * 3600, 4*3600 + 1800}[r%3]
	return calfDay(r)*86400 + off
}

// calendarDnNull: every 5th row's nullable date dn is null.
func calfDnNull(r int) bool { return r%5 == 0 }

// calfUnits are cat's dictionary labels; row r carries
// calfUnits[r%4] (all four valid date_trunc units).
var calfUnits = []string{"week", "month", "quarter", "year"}

func calfSchema(t testing.TB, units []string) *encoding.Schema {
	t.Helper()
	cat := encoding.NewDictionary()
	for _, u := range units {
		if _, err := cat.Add(u); err != nil {
			t.Fatal(err)
		}
	}
	fields := []encoding.Field{
		{Name: "d", Type: encoding.FieldTypeDate},
		{Name: "ts", Type: encoding.FieldTypeDateTime},
		{Name: "dn", Type: encoding.FieldTypeDate, Nullable: true},
		{Name: "x", Type: encoding.FieldTypeF64},
		{Name: "y", Type: encoding.FieldTypeF64},
		{Name: "cat", Type: encoding.FieldTypeCategoricalU8, Dictionary: cat},
	}
	off := 0
	for i := range fields {
		fields[i].ByteOffset = off
		fields[i].CsvColumnIdx = i
		off += fields[i].Type.ByteSize()
	}
	return &encoding.Schema{Fields: fields}
}

// writeCalendarCohort writes the cohort; units are cat's labels (row r
// carries units[r%len(units)]).
func writeCalfCohort(t testing.TB, fsys afero.Fs, units []string) []byte {
	t.Helper()
	s := calfSchema(t, units)
	var buf []byte
	w := &byteSink{b: &buf}
	if err := encoding.WriteHeader(w); err != nil {
		t.Fatal(err)
	}
	if err := encoding.WriteSchema(w, s); err != nil {
		t.Fatal(err)
	}
	stride, bm := s.RecordByteSize(), s.BitmapByteSize()
	for r := 0; r < calfRows; r++ {
		rec := make([]byte, stride)
		binary.LittleEndian.PutUint32(rec[s.Fields[0].ByteOffset:], uint32(calfDay(r)))
		binary.LittleEndian.PutUint64(rec[s.Fields[1].ByteOffset:], uint64(calfSecond(r)))
		if calfDnNull(r) {
			encoding.BitmapSetNull(rec[stride-bm:], 2)
		} else {
			binary.LittleEndian.PutUint32(rec[s.Fields[2].ByteOffset:], uint32(calfDay(r)+3))
		}
		binary.LittleEndian.PutUint64(rec[s.Fields[3].ByteOffset:], math.Float64bits(float64(r%7)+0.5*float64(r%3)))
		binary.LittleEndian.PutUint64(rec[s.Fields[4].ByteOffset:], math.Float64bits(float64((r*5)%11)-0.25*float64(r%4)))
		rec[s.Fields[5].ByteOffset] = byte(r % len(units))
		buf = append(buf, rec...)
	}
	if err := afero.WriteFile(fsys, calfCohort, buf, 0o644); err != nil {
		t.Fatal(err)
	}
	return buf
}

func calfPulse(t testing.TB, opts pulse.Options) (*pulse.Pulse, []byte) {
	t.Helper()
	fsys := afero.NewMemMapFs()
	data := writeCalfCohort(t, fsys, calfUnits)
	opts.FS = fsys
	p, err := pulse.New(opts)
	if err != nil {
		t.Fatal(err)
	}
	return p, data
}

func calfRequest(t testing.TB, body string) *types.Request {
	t.Helper()
	var req types.Request
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	req.Cohort = &types.Cohort{Filename: calfCohort}
	return &req
}

// The stdlib oracle (test code may hold zone math).
func oracleDay(r int) time.Time { return time.Unix(calfDay(r)*86400, 0).UTC() }

func oracleLocalDay(t testing.TB, r int, zone string) int64 {
	t.Helper()
	loc := time.UTC
	if zone != "" {
		var err error
		if loc, err = time.LoadLocation(zone); err != nil {
			t.Fatal(err)
		}
	}
	y, m, d := time.Unix(calfSecond(r), 0).In(loc).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Unix() / 86400
}

func oracleWeekday(tm time.Time) float64 {
	wd := float64(tm.Weekday())
	if wd == 0 {
		return 7
	}
	return wd
}

func oracleTrunc(tm time.Time, unit string) float64 {
	var start time.Time
	switch unit {
	case "week":
		start = tm.AddDate(0, 0, -int(oracleWeekday(tm)-1))
	case "month":
		start = time.Date(tm.Year(), tm.Month(), 1, 0, 0, 0, 0, time.UTC)
	case "quarter":
		start = time.Date(tm.Year(), (tm.Month()-1)/3*3+1, 1, 0, 0, 0, 0, time.UTC)
	case "year":
		start = time.Date(tm.Year(), 1, 1, 0, 0, 0, 0, time.UTC)
	}
	return float64(start.Unix() / 86400)
}

// calendarSums runs one ATTR_FORMULA per expr, each summed (AGG_SUM),
// and returns the sums by label.
func calfSums(t *testing.T, p *pulse.Pulse, zone string, exprs map[string]string) map[string]float64 {
	t.Helper()
	req := &types.Request{Cohort: &types.Cohort{Filename: calfCohort}, TimeZone: zone}
	for label, src := range exprs {
		req.Attributes = append(req.Attributes, &types.Attribute{Type: types.ATTR_FORMULA, Label: label, Expression: src})
		req.Aggregations = append(req.Aggregations, &types.Aggregation{Type: types.AGG_SUM, Field: label, Label: "sum_" + label})
	}
	resp, err := p.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	out := map[string]float64{}
	for label := range exprs {
		v, ok := resp.Data[0]["sum_"+label].(float64)
		if !ok {
			t.Fatalf("sum_%s = %#v", label, resp.Data[0]["sum_"+label])
		}
		out[label] = v
	}
	return out
}

// TestCalendarFormula_Oracle: every helper, summed over the cohort,
// equals the stdlib oracle's sum — to_day in the request zone, the
// options default zone and UTC — and the answer is the same on every
// decode arm (serial and parallel).
func TestCalendarFormula_Oracle(t *testing.T) {
	t.Parallel()

	exprs := map[string]string{
		"wd":      "weekday(d)",
		"mi":      "month_index(d)",
		"tw":      `date_trunc("week", d)`,
		"tm":      `date_trunc("month", d)`,
		"tq":      `date_trunc("quarter", d)`,
		"ty":      `date_trunc("year", d)`,
		"tc":      `date_trunc(cat, d)`,
		"td":      "to_day(ts)",
		"wdl":     "weekday(to_day(ts))",
		"lp":      "log1p(x)",
		"wdn":     "weekday(dn)",            // null on every 5th row
		"tqn":     `date_trunc("year", dn)`, // null on every 5th row
		"retains": "month_index(dn ?? d) - month_index(d)",
	}
	want := func(zone string) map[string]float64 {
		w := map[string]float64{}
		for r := 0; r < calfRows; r++ {
			tm := oracleDay(r)
			w["wd"] += oracleWeekday(tm)
			w["mi"] += float64(tm.Year()*12 + int(tm.Month()) - 1)
			w["tw"] += oracleTrunc(tm, "week")
			w["tm"] += oracleTrunc(tm, "month")
			w["tq"] += oracleTrunc(tm, "quarter")
			w["ty"] += oracleTrunc(tm, "year")
			w["tc"] += oracleTrunc(tm, calfUnits[r%4])
			local := oracleLocalDay(t, r, zone)
			w["td"] += float64(local)
			w["wdl"] += oracleWeekday(time.Unix(local*86400, 0).UTC())
			w["lp"] += math.Log1p(float64(r%7) + 0.5*float64(r%3))
			tn := oracleDay(r).AddDate(0, 0, 3)
			if !calfDnNull(r) {
				w["wdn"] += oracleWeekday(tn)
				w["tqn"] += oracleTrunc(tn, "year")
				w["retains"] += float64((tn.Year()*12 + int(tn.Month())) - (tm.Year()*12 + int(tm.Month())))
			}
		}
		return w
	}
	for _, c := range []struct {
		name, reqZone, optZone, oracle string
		workers                        int
	}{
		{"utc", "", "", "", 1},
		{"request zone", "America/New_York", "", "America/New_York", 1},
		{"options default zone", "", "America/New_York", "America/New_York", 1},
		{"request zone wins", "Asia/Tokyo", "America/New_York", "Asia/Tokyo", 1},
		{"request zone, parallel decode", "America/New_York", "", "America/New_York", 4},
	} {
		t.Run(c.name, func(t *testing.T) {
			p, _ := calfPulse(t, pulse.Options{DefaultTimeZone: c.optZone, DecodeWorkers: c.workers})
			got := calfSums(t, p, c.reqZone, exprs)
			w := want(c.oracle)
			for label := range exprs {
				if math.Abs(got[label]-w[label]) > 1e-9*math.Max(1, math.Abs(w[label])) {
					t.Errorf("%s (%s): sum %v, want %v", label, exprs[label], got[label], w[label])
				}
			}
		})
	}
	// The zone moves to_day: New York's local days differ from UTC's on
	// this cohort (the oracle is not vacuous).
	var utc, ny float64
	for r := 0; r < calfRows; r++ {
		utc += float64(oracleLocalDay(t, r, ""))
		ny += float64(oracleLocalDay(t, r, "America/New_York"))
	}
	if utc == ny {
		t.Fatal("the cohort does not separate UTC and New York days")
	}
}

// TestCalendarFormula_ZoneReachesEveryExpressionSite: a filter, a facet
// filter, a total() argument and a fitted attribute's params.fit_on all
// read to_day in the request zone. The predicate `to_day(ts) == d`
// keeps every row under UTC and, under New York, exactly the rows whose
// local day is their UTC day.
func TestCalendarFormula_ZoneReachesEveryExpressionSite(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	const zone = "America/New_York"
	sameDay := 0
	for r := 0; r < calfRows; r++ {
		if oracleLocalDay(t, r, zone) == calfDay(r) {
			sameDay++
		}
	}
	if sameDay == 0 || sameDay == calfRows {
		t.Fatalf("sameDay = %d: the cohort does not separate the zones", sameDay)
	}
	p, _ := calfPulse(t, pulse.Options{})

	t.Run("filter", func(t *testing.T) {
		for _, c := range []struct {
			zone string
			want int
		}{{"", calfRows}, {zone, sameDay}} {
			resp, err := p.Process(ctx, calfRequest(t, `{"time_zone":"`+c.zone+`","filterers":[{"type":"FILTER_EXPRESSION","expression":"to_day(ts) == d"}],"aggregations":[{"type":"AGG_COUNT","field":"x","label":"n"}]}`))
			if err != nil {
				t.Fatal(err)
			}
			if got := resp.Data[0]["n"]; got != float64(c.want) && got != c.want && got != int64(c.want) {
				t.Errorf("zone %q: n = %v, want %d", c.zone, got, c.want)
			}
		}
	})
	t.Run("facet", func(t *testing.T) {
		res, err := p.FacetSchema(ctx, &pulse.FacetRequest{Cohort: &types.Cohort{Filename: calfCohort}, TimeZone: zone,
			Fields:    []string{"cat"},
			Filterers: []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: "to_day(ts) == d"}}})
		if err != nil {
			t.Fatal(err)
		}
		if res.FilteredRecords != int64(sameDay) {
			t.Errorf("facet filtered = %d, want %d", res.FilteredRecords, sameDay)
		}
	})
	t.Run("total argument", func(t *testing.T) {
		got := calfSums(t, p, zone, map[string]string{"f": "total(to_day(ts) == d ? 1 : 0) + 0 * x"})
		if got["f"] != float64(sameDay*calfRows) {
			t.Errorf("sum of total() = %v, want %d", got["f"], sameDay*calfRows)
		}
	})
	t.Run("fit_on", func(t *testing.T) {
		maha := func(zone, fitOn string) float64 {
			resp, err := p.Process(ctx, calfRequest(t, `{"time_zone":"`+zone+`","attributes":[{"type":"ATTR_MAHALANOBIS","label":"m","fields":["x","y"],"params":{"fit_on":[{"type":"FILTER_EXPRESSION","expression":"`+fitOn+`"}]}}],"aggregations":[{"type":"AGG_SUM","field":"m","label":"s"}]}`))
			if err != nil {
				t.Fatal(err)
			}
			return resp.Data[0]["s"].(float64)
		}
		zoned := maha(zone, "to_day(ts) == d")
		// The same fit rows spelt without a zone: New York's local day is
		// the UTC day exactly for the 12:00Z rows and the EDT 04:30Z rows.
		var explicit []string
		for r := 0; r < calfRows; r++ {
			if oracleLocalDay(t, r, zone) == calfDay(r) {
				explicit = append(explicit, calfItoa(calfSecond(r)))
			}
		}
		spelt := maha("", "ts in ["+calfJoin(explicit)+"]")
		if zoned != spelt {
			t.Errorf("zoned fit_on sum %v, want the explicit-row fit %v", zoned, spelt)
		}
		if utc := maha("", "to_day(ts) == d"); utc == zoned {
			t.Errorf("fit_on ignored the zone: UTC sum %v == New York sum %v", utc, zoned)
		}
	})
}

func calfItoa(v int64) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func calfJoin(xs []string) string {
	out := ""
	for i, x := range xs {
		if i > 0 {
			out += ", "
		}
		out += x
	}
	return out
}

// TestCalendarFormula_SlotZoneRefused: the expression slots stay
// non-zone-capable — an explicit `tz` is PROCESSING_CONFIG at predict
// and at the run.
func TestCalendarFormula_SlotZoneRefused(t *testing.T) {
	t.Parallel()

	p, data := calfPulse(t, pulse.Options{})
	mk := func() *types.Request {
		return calfRequest(t, `{"attributes":[{"type":"ATTR_FORMULA","label":"f","expression":"to_day(ts)","tz":"America/New_York"}],"aggregations":[{"type":"AGG_COUNT","field":"x","label":"n"}]}`)
	}
	pe := predictCodedError(t, p, data, mk())
	_, err := p.Process(context.Background(), mk())
	if pe == nil || pe.Code != string(perr.PROCESSING_CONFIG) || codeOf(t, err).Code != perr.PROCESSING_CONFIG {
		t.Fatalf("predict %+v, runtime %v; want PROCESSING_CONFIG on both", pe, err)
	}
}

// TestCalendarFormula_RuntimeUnitRefusal: a date_trunc unit read from a
// field that is not one of the four is PROCESSING_CONFIG {function,
// unit, valid} at the run (predict cannot see row values).
func TestCalendarFormula_RuntimeUnitRefusal(t *testing.T) {
	t.Parallel()

	fsys := afero.NewMemMapFs()
	writeCalfCohort(t, fsys, []string{"week", "fortnight"})
	p, err := pulse.New(pulse.Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Process(context.Background(), calfRequest(t, `{"attributes":[{"type":"ATTR_FORMULA","label":"f","expression":"date_trunc(cat, d)"}],"aggregations":[{"type":"AGG_SUM","field":"f","label":"s"}]}`))
	ce := codeOf(t, err)
	if ce.Code != perr.PROCESSING_CONFIG || ce.Details["unit"] != "fortnight" || ce.Details["function"] != "date_trunc" {
		t.Fatalf("got %v (%v), want PROCESSING_CONFIG unit=fortnight", ce, ce.Details)
	}
}
