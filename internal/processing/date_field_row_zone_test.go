package processing

import (
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/temporal"
	"github.com/frankbardon/pulse/types"
)

// Per-row zone fields (U50 E1-S2): a slot's `tz_field` names a
// categorical column of IANA zones; each record's instant is read on
// the wall clock of the zone its own cell names.

// rowZoneNames is the zone field's dictionary: code i names zone i.
var rowZoneNames = []string{"Europe/Berlin", "America/New_York", "Asia/Kolkata", "UTC"}

// rowZoneSchema is a `datetime` (ts), a categorical zone field (tz) and
// a non-categorical column (score).
func rowZoneSchema() *encoding.Schema {
	d := encoding.NewDictionary()
	for _, n := range rowZoneNames {
		_, _ = d.Add(n)
	}
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "ts", Type: encoding.FieldTypeDateTime},
		{Name: "tz", Type: encoding.FieldTypeCategoricalU8, Dictionary: d, Nullable: true},
		{Name: "score", Type: encoding.FieldTypeF64},
	}}
}

func rowZoneRecord(ts float64, code int) *Record {
	if code < 0 {
		return NewRecordWithNulls(rowZoneSchema(), map[string]float64{"ts": ts}, map[string]bool{"tz": true})
	}
	return NewRecord(rowZoneSchema(), map[string]float64{"ts": ts, "tz": float64(code)})
}

// TestDateField_PerRowZoneDSTNights: across the spring-forward and
// fall-back nights of Berlin and New York, every instant read in the
// zone its row names lands on the local day and local hour time.In
// gives — the 23-hour and 25-hour days included, the repeated hour
// collapsing into one bucket unit — whatever zone the previous row
// named (the forked lookup caches never leak a zone between rows).
// A null cell, or a code outside the dictionary, has no local value.
func TestDateField_PerRowZoneDSTNights(t *testing.T) {
	t.Parallel()

	df, err := resolveDateField("OP", "ts", "", "tz", rowZoneSchema(), true)
	if err != nil {
		t.Fatal(err)
	}
	if !df.perRow() || df.zone != nil {
		t.Fatalf("per-row arm not taken: %+v", df)
	}
	locs := make([]*time.Location, len(rowZoneNames))
	for i, n := range rowZoneNames {
		z, err := temporal.LoadZone(n)
		if err != nil {
			t.Fatal(err)
		}
		locs[i] = z.Location()
	}
	differs := 0
	for _, night := range []string{"2026-03-08", "2026-03-29", "2026-10-25", "2026-11-01"} {
		d, _ := time.Parse("2006-01-02", night)
		// Interleave zones row by row: code cycles every instant.
		i := 0
		for at := d.Add(-6 * time.Hour); at.Before(d.Add(30 * time.Hour)); at = at.Add(15 * time.Minute) {
			code := i % len(rowZoneNames)
			i++
			r := rowZoneRecord(float64(at.Unix()), code)
			local := at.In(locs[code])
			wantDay := int64(temporal.TimeToDay(time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC)))
			day, ok := df.epochDayAt(r, float64(at.Unix()))
			if !ok || day != wantDay {
				t.Fatalf("%s in %s: day %d ok=%v, want %d", at.Format(time.RFC3339), rowZoneNames[code], day, ok, wantDay)
			}
			if day != int64(temporal.TimeToDay(at)) {
				differs++
			}
			hour, ok := df.epochHourAt(r, float64(at.Unix()))
			if got := temporal.HourToTime(hour).Format("2006-01-02T15"); !ok || got != local.Format("2006-01-02T15") {
				t.Fatalf("%s in %s: hour %s ok=%v, want %s", at.Format(time.RFC3339), rowZoneNames[code], got, ok, local.Format("2006-01-02T15"))
			}
		}
	}
	if differs == 0 {
		t.Fatal("fixture proves nothing: every local day equals the UTC day")
	}
	inst := epochSeconds(2026, time.March, 28, 23, 30, 0)
	for _, r := range []*Record{rowZoneRecord(inst, -1), rowZoneRecord(inst, len(rowZoneNames))} {
		if _, ok := df.epochDayAt(r, inst); ok {
			t.Fatal("a null / out-of-dictionary zone cell yielded a local day")
		}
		if _, ok := df.epochHourAt(r, inst); ok {
			t.Fatal("a null / out-of-dictionary zone cell yielded a local hour")
		}
	}
}

// TestDateField_PerRowZoneConstruction: the table is built only over a
// `datetime` (a `date` column, which no zone ever reaches, stays on the
// zone-free path); a zone field that is absent or not categorical is
// PROCESSING_CONFIG; a dictionary entry that names no zone is
// PULSE_TIMEZONE_UNKNOWN carrying its code.
func TestDateField_PerRowZoneConstruction(t *testing.T) {
	t.Parallel()

	ds := rowZoneSchema()
	ds.Fields[0].Type = encoding.FieldTypeDate
	if df, err := resolveDateField("OP", "ts", "", "tz", ds, true); err != nil || df.perRow() {
		t.Fatalf("date column: perRow=%v err=%v, want the zone-free path", df.perRow(), err)
	}
	for _, field := range []string{"missing", "score"} {
		_, err := resolveDateField("OP", "ts", "", field, rowZoneSchema(), true)
		ce, ok := err.(*errors.CodedError)
		if !ok || ce.Code != errors.PROCESSING_CONFIG {
			t.Fatalf("tz_field %q: %v, want PROCESSING_CONFIG", field, err)
		}
	}
	bad := rowZoneSchema()
	_, _ = bad.Fields[1].Dictionary.Add("Mars/Base")
	_, err := resolveDateField("OP", "ts", "", "tz", bad, true)
	ce, ok := err.(*errors.CodedError)
	if !ok || ce.Code != errors.PULSE_TIMEZONE_UNKNOWN || ce.Details[temporal.DetailDictionaryIndex] != len(rowZoneNames) {
		t.Fatalf("bad dictionary entry: %v", err)
	}
}

// TestPerRowZone_OperatorsAllocationFree: the per-row read on every
// record-reading operator allocates nothing per row — the dictionary
// lookup is a slice index into a table built once per operator, the
// offset lookup the zone's transition table.
func TestPerRowZone_OperatorsAllocationFree(t *testing.T) {
	// serial: testing.AllocsPerRun counts process-wide allocations, which concurrent tests inflate

	s := rowZoneSchema()
	ranges := json.RawMessage(`{"ranges":[{"label":"a","start":"2026-03-01","end":"2026-03-31"}]}`)
	rg, err := newDateRangesGrouper(&types.Group{Type: types.GROUP_DATE_RANGES, Field: "ts", TimeZoneField: "tz", Params: ranges}, s)
	if err != nil {
		t.Fatal(err)
	}
	dg, err := newDateGrouper(&types.Group{Type: types.GROUP_DATE, Field: "ts", TimeZoneField: "tz", Params: json.RawMessage(`{"component":"day"}`)}, s)
	if err != nil {
		t.Fatal(err)
	}
	fb := newDateRangesFilterer()
	fn, err := fb.Build(&types.Filterer{Type: types.FILTER_DATE_RANGES, Field: "ts", TimeZoneField: "tz", Params: ranges}, s)
	if err != nil {
		t.Fatal(err)
	}
	ac, err := newDatePartAttribute(&types.Attribute{Type: types.ATTR_DATE_PART, Field: "ts", TimeZoneField: "tz", Params: json.RawMessage(`{"part":"year_month_day"}`)}, s)
	if err != nil {
		t.Fatal(err)
	}
	attr := ac.(*datePartAttribute)
	recs := make([]*Record, len(rowZoneNames)+1)
	for i := range rowZoneNames {
		recs[i] = rowZoneRecord(epochSeconds(2026, time.March, 28, 23, 30, 0), i)
	}
	recs[len(rowZoneNames)] = rowZoneRecord(epochSeconds(2026, time.March, 28, 23, 30, 0), -1)
	i := 0
	next := func() *Record { r := recs[i%len(recs)]; i++; return r }
	for name, f := range map[string]func(){
		"GROUP_DATE_RANGES.KeyFor": func() { _, _ = rg.(StreamableGrouper).KeyFor(next()) },
		"GROUP_DATE.unit": func() {
			r := next()
			v, _ := r.NumericValue("ts")
			_, _ = dg.(*dateGrouper).unit(r, v)
		},
		"FILTER_DATE_RANGES": func() { _, _ = fn(next()) },
		"ATTR_DATE_PART.Row": func() { _, _ = attr.Row(next(), "ts") },
	} {
		if n := testing.AllocsPerRun(200, f); n != 0 {
			t.Errorf("%s: %v allocations per row, want 0", name, n)
		}
	}
}

// TestNeededFields_ZoneFields: every per-row zone field a request names
// — the request's time_zone_field and each slot's tz_field, crosstab
// axes and a MAT_DISTANCE between included — joins the projection; a
// zone field left out decodes as null on every row and silently empties
// the date operators.
func TestNeededFields_ZoneFields(t *testing.T) {
	t.Parallel()

	fields := []encoding.Field{{Name: "ts", Type: encoding.FieldTypeDateTime}, {Name: "n", Type: encoding.FieldTypeU32}, {Name: "other", Type: encoding.FieldTypeF64}}
	for _, z := range []string{"z_req", "z_filter", "z_feat", "z_attr", "z_group", "z_row", "z_col", "z_between"} {
		fields = append(fields, encoding.Field{Name: z, Type: encoding.FieldTypeCategoricalU8, Dictionary: encoding.NewDictionary()})
	}
	s := &encoding.Schema{Fields: fields}
	day := json.RawMessage(`{"component":"day"}`)
	req := &types.Request{
		TimeZoneField: "z_req",
		Filterers:     []*types.Filterer{{Type: types.FILTER_DATE_RANGES, Field: "ts", TimeZoneField: "z_filter"}},
		Features:      []*types.Feature{{Type: types.FEAT_DATE_FEATURES, Field: "ts", TimeZoneField: "z_feat"}},
		Attributes:    []*types.Attribute{{Type: types.ATTR_DATE_PART, Field: "ts", Label: "p", TimeZoneField: "z_attr"}},
		Groups:        []*types.Group{{Type: types.GROUP_DATE, Field: "ts", Params: day, TimeZoneField: "z_group"}},
		Aggregations:  []*types.Aggregation{{Type: types.AGG_COUNT, Field: "n"}},
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_DATE, Field: "ts", Params: day, TimeZoneField: "z_row"}},
			Columns: []*types.Group{{Type: types.GROUP_DATE, Field: "ts", Params: day, TimeZoneField: "z_col"}},
			Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "n"},
		},
		Matrices: []types.MatrixSpec{{Type: types.MAT_DISTANCE, Name: "d", Fields: []string{"n"},
			Params: json.RawMessage(`{"between":{"type":"GROUP_DATE","field":"ts","tz_field":"z_between","params":{"component":"day"}}}`)}},
	}
	got := NeededFields(req, s, nil)
	if got.IsWide() {
		t.Fatal("projection widened; the zone fields are not proven")
	}
	var missing []string
	for _, f := range fields[3:] {
		if !got.Has(f.Name) {
			missing = append(missing, f.Name)
		}
	}
	slices.Sort(missing)
	if len(missing) > 0 {
		t.Fatalf("zone fields missing from the projection: %v", missing)
	}
	if got.Has("other") {
		t.Fatal("an unreferenced field joined the projection")
	}
}

// TestPrecomputeInputs_ZoneField: a FILTER_DATE_RANGES reading a
// per-row zone field has TWO inputs — its verdict depends on the zone
// cell — so it precomputes only when both sit in one parent group.
func TestPrecomputeInputs_ZoneField(t *testing.T) {
	t.Parallel()

	in, ok := precomputeInputs(&types.Filterer{Type: types.FILTER_DATE_RANGES, Field: "ts", TimeZoneField: "tz"}, nil)
	if !ok || !slices.Equal(in, []string{"ts", "tz"}) {
		t.Fatalf("inputs = %v ok=%v, want [ts tz]", in, ok)
	}
	in, ok = precomputeInputs(&types.Filterer{Type: types.FILTER_DATE_RANGES, Field: "ts"}, nil)
	if !ok || !slices.Equal(in, []string{"ts"}) {
		t.Fatalf("zone-free inputs = %v ok=%v, want [ts]", in, ok)
	}
}
