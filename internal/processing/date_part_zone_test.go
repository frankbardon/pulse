package processing

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/processing/feature"
	"github.com/frankbardon/pulse/internal/temporal"
	"github.com/frankbardon/pulse/types"
)

// zoneSweepInstants: every 20 minutes from 36h before to 36h after both
// 2026 Europe/Berlin DST changes, plus the 2026 New Year turn (year,
// quarter and month all change at local midnight there).
func zoneSweepInstants() []int64 {
	var out []int64
	for _, c := range []string{"2026-03-29T01:00:00Z", "2026-10-25T01:00:00Z", "2026-01-01T00:00:00Z"} {
		at, _ := time.Parse(time.RFC3339, c)
		for t := at.Add(-36 * time.Hour); !t.After(at.Add(36 * time.Hour)); t = t.Add(20 * time.Minute) {
			out = append(out, t.Unix())
		}
	}
	return out
}

func dateTimeSchema() *encoding.Schema {
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "ts", Type: encoding.FieldTypeDateTime},
		{Name: "d", Type: encoding.FieldTypeDate},
		{Name: "score", Type: encoding.FieldTypeF64},
	}}
}

// datePartReference is ATTR_DATE_PART's answer for t, computed with
// time.Time accessors only.
func datePartReference(part string, t time.Time) float64 {
	y, m, d := t.Date()
	switch part {
	case "year":
		return float64(y)
	case "month":
		return float64(m)
	case "day":
		return float64(d)
	case "year_month":
		return float64(y*100 + int(m))
	case "year_month_day":
		return float64(y*10000 + int(m)*100 + d)
	case "month_day":
		return float64(int(m)*100 + d)
	case "hour":
		return float64(t.Hour())
	case "iso_weekday":
		return float64((int(t.Weekday())+6)%7 + 1)
	}
	panic(part)
}

// TestAttribute_DatePart_DateTimeFollowsZone: every part over a
// `datetime` equals the time.In reference in the slot zone (Berlin,
// across both DST changes and New Year) — the zone path through Row and
// Compute alike — and the UTC reference with no zone or a UTC one.
func TestAttribute_DatePart_DateTimeFollowsZone(t *testing.T) {
	berlin, err := temporal.LoadZone("Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}
	schema := dateTimeSchema()
	instants := zoneSweepInstants()
	vals := make([]float64, len(instants))
	for i, s := range instants {
		vals[i] = float64(s)
	}
	records := makeRecords(schema, "ts", vals)
	for _, part := range []string{"year", "month", "day", "year_month", "year_month_day", "month_day", "hour", "iso_weekday"} {
		for _, tc := range []struct {
			tz  string
			loc *time.Location
		}{{"", time.UTC}, {"UTC", time.UTC}, {"Etc/UTC", time.UTC}, {"Europe/Berlin", berlin.Location()}} {
			t.Run(part+"/tz="+tc.tz, func(t *testing.T) {
				attr, err := attributeRegistry[types.ATTR_DATE_PART](&types.Attribute{
					Type: types.ATTR_DATE_PART, Field: "ts", Label: "p", TimeZone: tc.tz,
					Params: json.RawMessage(`{"part":"` + part + `"}`),
				}, schema)
				if err != nil {
					t.Fatal(err)
				}
				got, err := attr.Compute(records, "ts")
				if err != nil {
					t.Fatal(err)
				}
				differs := false
				for i, s := range instants {
					want := datePartReference(part, time.Unix(s, 0).In(tc.loc))
					if got[i] != want {
						t.Fatalf("%s at %s: got %v, want %v", part, time.Unix(s, 0).UTC().Format(time.RFC3339), got[i], want)
					}
					differs = differs || want != datePartReference(part, time.Unix(s, 0).UTC())
				}
				if tc.loc != time.UTC && !differs {
					t.Fatalf("fixture proves nothing: %s reads alike in Berlin and UTC", part)
				}
			})
		}
	}
}

// TestAttribute_DatePart_FieldRefusals: hour needs a datetime; a
// non-temporal field is refused for every part with the accepted-types
// table's PULSE_FIELD_TYPE_UNSUPPORTED (a date under hour too); an absent
// field and a bad part are PROCESSING_CONFIG; each names the field.
func TestAttribute_DatePart_FieldRefusals(t *testing.T) {
	schema := dateTimeSchema()
	unsupported, config := errors.PULSE_FIELD_TYPE_UNSUPPORTED, errors.PROCESSING_CONFIG
	for _, tc := range []struct {
		part, field, want string
		code              errors.Code
	}{
		{"hour", "d", `ATTR_DATE_PART part=hour reads a time of day; d is of type date`, unsupported},
		{"year", "score", `ATTR_DATE_PART field score has type f64; accepted: date, datetime`, unsupported},
		{"hour", "score", `ATTR_DATE_PART field score has type f64; accepted: date, datetime`, unsupported},
		{"month", "derived", `requires a date or datetime field, got "derived"`, config},
		{"minute", "ts", `invalid date part "minute": must be one of year, month, day, year_month, year_month_day, month_day, hour, iso_weekday`, config},
	} {
		t.Run(tc.part+"/"+tc.field, func(t *testing.T) {
			_, err := attributeRegistry[types.ATTR_DATE_PART](&types.Attribute{
				Type: types.ATTR_DATE_PART, Field: tc.field, Label: "p",
				Params: json.RawMessage(`{"part":"` + tc.part + `"}`),
			}, schema)
			ce, ok := err.(*errors.CodedError)
			if !ok || ce.Code != tc.code || !strings.Contains(ce.Message, tc.want) {
				t.Fatalf("err = %v, want %s containing %q", err, tc.code, tc.want)
			}
		})
	}
}

// TestAttribute_DatePart_DateIgnoresZoneNullIsNull: over a `date` the
// zone-free calendar reading is unchanged and a null date is a null
// part (AttributeNull), never 0.
func TestAttribute_DatePart_DateIgnoresZoneNullIsNull(t *testing.T) {
	schema := dateTimeSchema()
	attr, err := attributeRegistry[types.ATTR_DATE_PART](&types.Attribute{
		Type: types.ATTR_DATE_PART, Field: "d", Label: "p", Params: json.RawMessage(`{"part":"year_month_day"}`),
	}, schema)
	if err != nil {
		t.Fatal(err)
	}
	recs := makeRecordsWithNulls(schema, "d", []float64{19797, 0, -1}, []int{1})
	got, err := attr.Compute(recs, "d")
	if err != nil {
		t.Fatal(err)
	}
	if got[0] != 20240315 || !IsAttributeNull(got[1]) || got[2] != 19691231 {
		t.Fatalf("got %v, want [20240315 AttributeNull 19691231]", got)
	}
}

// TestAttribute_DatePart_NullInputIsNullEveryPart: a null `date` or
// `datetime` cell is a null part — AttributeNull, which every arm
// writes as a null and every aggregation counts in n_null — for every
// part, on the buffered (Compute) and row-at-a-time (Row) paths, with
// no zone, a slot zone, and a per-row zone field whose cell is set.
// Never the legacy 0, a real-looking number.
func TestAttribute_DatePart_NullInputIsNullEveryPart(t *testing.T) {
	schema := dateTimeSchema()
	dateParts := []string{"year", "month", "day", "year_month", "year_month_day", "month_day", "iso_weekday"}
	for _, tc := range []struct {
		field, tz string
		parts     []string
		value     float64
	}{
		{"d", "", dateParts, 19797},
		{"ts", "", append(append([]string{}, dateParts...), "hour"), 1773000000},
		{"ts", "Asia/Tokyo", append(append([]string{}, dateParts...), "hour"), 1773000000},
	} {
		for _, part := range tc.parts {
			t.Run(tc.field+"/"+tc.tz+"/"+part, func(t *testing.T) {
				attr, err := attributeRegistry[types.ATTR_DATE_PART](&types.Attribute{
					Type: types.ATTR_DATE_PART, Field: tc.field, Label: "p", TimeZone: tc.tz,
					Params: json.RawMessage(`{"part":"` + part + `"}`),
				}, schema)
				if err != nil {
					t.Fatal(err)
				}
				recs := makeRecordsWithNulls(schema, tc.field, []float64{tc.value, 0}, []int{1})
				got, err := attr.Compute(recs, tc.field)
				if err != nil {
					t.Fatal(err)
				}
				if IsAttributeNull(got[0]) || !IsAttributeNull(got[1]) {
					t.Fatalf("Compute = %v, want [value AttributeNull]", got)
				}
				row, err := attr.(RowLocalAttribute).Row(recs[1], tc.field)
				if err != nil {
					t.Fatal(err)
				}
				if !IsAttributeNull(row) {
					t.Fatalf("Row on a null %s = %v, want AttributeNull", tc.field, row)
				}
			})
		}
	}
}

// TestDateFeatures_DateTimeFollowsZone: over a `datetime`
// FEAT_DATE_FEATURES writes six columns (hour added) equal to the
// time.In reference in the slot zone, on both the buffered (Compute) and
// streaming (EmitRow) paths; dow is ISO (Monday = 1 .. Sunday = 7).
func TestDateFeatures_DateTimeFollowsZone(t *testing.T) {
	berlin, err := temporal.LoadZone("Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}
	schema := dateTimeSchema()
	instants := zoneSweepInstants()
	vals := make([]float64, len(instants))
	for i, s := range instants {
		vals[i] = float64(s)
	}
	recs := makeRecords(schema, "ts", vals)
	view := make([]feature.Record, len(recs))
	for i, r := range recs {
		view[i] = r
	}
	factory, _ := feature.Lookup(types.FEAT_DATE_FEATURES)
	ref := func(t time.Time) map[string]float64 {
		return map[string]float64{
			"df_year": float64(t.Year()), "df_month": float64(t.Month()), "df_day": float64(t.Day()),
			"df_dow": float64((int(t.Weekday())+6)%7 + 1), "df_quarter": float64((int(t.Month())-1)/3 + 1), "df_hour": float64(t.Hour()),
		}
	}
	for _, tc := range []struct {
		tz  string
		loc *time.Location
	}{{"", time.UTC}, {"UTC", time.UTC}, {"Europe/Berlin", berlin.Location()}} {
		t.Run("tz="+tc.tz, func(t *testing.T) {
			comp, err := factory(&types.Feature{Type: types.FEAT_DATE_FEATURES, Field: "ts", Label: "df", TimeZone: tc.tz}, schema)
			if err != nil {
				t.Fatal(err)
			}
			out, err := comp.Compute(view, "ts")
			if err != nil {
				t.Fatal(err)
			}
			if len(out) != 6 {
				t.Fatalf("columns = %d, want 6", len(out))
			}
			stream := comp.(feature.StreamingComputer)
			differs := map[string]bool{}
			for i, s := range instants {
				want := ref(time.Unix(s, 0).In(tc.loc))
				utc := ref(time.Unix(s, 0).UTC())
				row, err := stream.EmitRow(view[i], "ts")
				if err != nil {
					t.Fatal(err)
				}
				for col, w := range want {
					if got := out[col].Values[i]; got != w {
						t.Fatalf("Compute %s at %d: got %v, want %v", col, s, got, w)
					}
					if got := row[col].Values[0]; got != w {
						t.Fatalf("EmitRow %s at %d: got %v, want %v", col, s, got, w)
					}
					if w != utc[col] {
						differs[col] = true
					}
				}
			}
			if tc.loc != time.UTC && len(differs) != 6 {
				t.Fatalf("fixture proves nothing for some column: differing %v", differs)
			}
		})
	}
}

// TestDateFeatures_FieldTypes: a `date` keeps its five columns; a
// non-temporal field is refused naming both accepted types.
func TestDateFeatures_FieldTypes(t *testing.T) {
	schema := dateTimeSchema()
	factory, _ := feature.Lookup(types.FEAT_DATE_FEATURES)
	comp, err := factory(&types.Feature{Type: types.FEAT_DATE_FEATURES, Field: "d", Label: "df"}, schema)
	if err != nil {
		t.Fatal(err)
	}
	recs := makeRecords(schema, "d", []float64{19797})
	out, err := comp.Compute([]feature.Record{recs[0]}, "d")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := out["df_hour"]; ok || len(out) != 5 {
		t.Fatalf("date columns = %v, want the five without hour", out)
	}
	_, err = factory(&types.Feature{Type: types.FEAT_DATE_FEATURES, Field: "score"}, schema)
	if err == nil || !errors.HasCode(err, errors.PULSE_FIELD_TYPE_UNSUPPORTED) || !strings.Contains(err.Error(), "accepted: date, datetime") {
		t.Fatalf("score: err = %v", err)
	}
}

// TestAttribute_DatePart_ISOWeekdayPins: part iso_weekday is ISO 8601
// (Monday = 1 .. Sunday = 7) over a `date` (the calendar, no zone) and
// over a `datetime` (the slot zone's wall clock). Saturday 23:30 in New
// York is Sunday 04:30 UTC: 6 there, 7 in UTC — the local-midnight
// crossing a UTC reading gets wrong.
func TestAttribute_DatePart_ISOWeekdayPins(t *testing.T) {
	schema := dateTimeSchema()
	day := func(s string) float64 {
		tm, _ := time.Parse("2006-01-02", s)
		return float64(temporal.TimeToDay(tm))
	}
	instant := func(s string) float64 {
		tm, _ := time.Parse(time.RFC3339, s)
		return float64(tm.Unix())
	}
	for _, tc := range []struct {
		name, field, tz string
		v, want         float64
	}{
		{"date epoch Thursday", "d", "", 0, 4},
		{"date pre-epoch Wednesday", "d", "", -1, 3},
		{"date Monday", "d", "", day("2024-03-04"), 1},
		{"date Saturday", "d", "", day("2024-03-16"), 6},
		{"date Sunday", "d", "", day("2024-03-17"), 7},
		{"datetime Sat 23:30 New York", "ts", "America/New_York", instant("2026-03-15T03:30:00Z"), 6},
		{"datetime same instant UTC", "ts", "", instant("2026-03-15T03:30:00Z"), 7},
		{"datetime Sun 00:30 New York", "ts", "America/New_York", instant("2026-03-15T04:30:00Z"), 7},
		{"datetime Mon 00:10 Kolkata, Sun UTC", "ts", "Asia/Kolkata", instant("2026-03-15T18:40:00Z"), 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			attr, err := attributeRegistry[types.ATTR_DATE_PART](&types.Attribute{
				Type: types.ATTR_DATE_PART, Field: tc.field, Label: "wd", TimeZone: tc.tz,
				Params: json.RawMessage(`{"part":"iso_weekday"}`),
			}, schema)
			if err != nil {
				t.Fatal(err)
			}
			recs := makeRecords(schema, tc.field, []float64{tc.v})
			got, err := attr.Compute(recs, tc.field)
			if err != nil {
				t.Fatal(err)
			}
			row, err := attr.(interface {
				Row(*Record, string) (float64, error)
			}).Row(recs[0], tc.field)
			if err != nil {
				t.Fatal(err)
			}
			if got[0] != tc.want || row != tc.want {
				t.Fatalf("iso_weekday: Compute %v, Row %v, want %v", got[0], row, tc.want)
			}
		})
	}
}
