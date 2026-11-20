package synth

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
)

// Local-clock datetime capture (#594, U76 E3-S2): ProfileOptions
// TimeZone / TimeZoneField and the cohort's declared default zone field
// set the clock a datetime column is captured on.

// eveningHours is open 20:00-23:59 local only: in New York that is
// 01:00-04:59 UTC on the NEXT day, so a UTC capture moves both the hours
// and the day range.
func eveningHours() []any {
	w := make([]any, 24)
	for h := range w {
		w[h] = 0.0
	}
	for h := 20; h < 24; h++ {
		w[h] = 1.0
	}
	return w
}

// zoneCaptureSpec draws ts in each row's zone (site, three zones) on
// weekday evenings, beside a fixed-zone twin field and a second,
// nullable zone column the generation never reads.
func zoneCaptureSpec() *Spec {
	return &Spec{RowCount: 4000, Fields: []FieldSpec{
		{Name: "site", Type: "categorical_u8", Distribution: DistWeightedCategorical,
			Params: map[string]any{"values": []any{"America/New_York", "Asia/Kolkata", "Australia/Sydney"}}},
		{Name: "alt_zone", Type: "categorical_u8", Nullable: true, NullRate: 0.2, Distribution: DistWeightedCategorical,
			Params: map[string]any{"values": []any{"Europe/Berlin"}}},
		{Name: "amount", Type: "f64", Distribution: DistNormal, Params: map[string]any{"mean": 10.0, "std": 2.0}},
		{Name: "ts", Type: "datetime", Distribution: DistUniformDateTime, Params: map[string]any{
			"start": "2026-01-07", "end": "2026-02-24", "time_zone_field": "site",
			"hour_weights": eveningHours(), "weekday_weights": []any{1.0, 1.0, 1.0, 1.0, 1.0, 0.0, 0.0}}},
		{Name: "ny", Type: "datetime", Distribution: DistUniformDateTime, Params: map[string]any{
			"start": "2026-01-07", "end": "2026-02-24", "time_zone": "America/New_York",
			"hour_weights": eveningHours(), "weekday_weights": []any{1.0, 1.0, 1.0, 1.0, 1.0, 0.0, 0.0}}},
	}}
}

// withDefaultZoneField rewrites a single-file cohort so its schema
// declares field the cohort default zone field; records are unchanged.
func withDefaultZoneField(t *testing.T, data []byte, field string) []byte {
	t.Helper()
	r := bytes.NewReader(data)
	s, _, err := encx.ReadPreamble(r)
	if err != nil {
		t.Fatal(err)
	}
	records, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	s.ZoneField = field
	var out bytes.Buffer
	if err := encx.WritePreamble(&out, s); err != nil {
		t.Fatal(err)
	}
	out.Write(records)
	return out.Bytes()
}

func zoneCaptureSource(t *testing.T) []byte {
	t.Helper()
	src, _, err := SynthBytes(zoneCaptureSpec(), Options{Seed: 76})
	if err != nil {
		t.Fatal(err)
	}
	return src
}

func datetimeOf(t *testing.T, p *Profile, name string) *DatetimeProfile {
	t.Helper()
	for i := range p.Fields {
		if p.Fields[i].Name == name {
			if p.Fields[i].Datetime == nil {
				t.Fatalf("%s: no datetime section", name)
			}
			return p.Fields[i].Datetime
		}
	}
	t.Fatalf("%s: not profiled", name)
	return nil
}

// assertEveningWeekdays: every captured reading sits in 20..23 on a
// weekday — exactly the local schedule the source was drawn on.
func assertEveningWeekdays(t *testing.T, label string, dp *DatetimeProfile) {
	t.Helper()
	for h, c := range dp.Hours {
		if (h < 20) != (c == 0) {
			t.Errorf("%s: hour %d count %d; want counts in 20..23 only", label, h, c)
		}
	}
	if dp.Weekdays[5] != 0 || dp.Weekdays[6] != 0 {
		t.Errorf("%s: weekend counts %v; want none", label, dp.Weekdays[5:])
	}
}

// TestProfileZone_FixedZoneReadsTheLocalClock: TimeZone captures the
// hours, weekdays AND the day range on the zone's clock, records the
// zone, and SpecFromProfile rebuilds a zoned uniform_datetime with the
// non-error dst_policy. Without the option the same column reads UTC.
func TestProfileZone_FixedZoneReadsTheLocalClock(t *testing.T) {
	t.Parallel()
	src := zoneCaptureSource(t)

	local, err := ProfileBytes(src, ProfileOptions{TimeZone: "America/New_York"})
	if err != nil {
		t.Fatal(err)
	}
	dp := datetimeOf(t, local, "ny")
	assertEveningWeekdays(t, "local", dp)
	if dp.Start != "2026-01-07" || dp.End != "2026-02-24" {
		t.Errorf("local range %s..%s, want 2026-01-07..2026-02-24", dp.Start, dp.End)
	}
	if dp.TimeZone != "America/New_York" || dp.TimeZoneField != "" {
		t.Errorf("recorded clock %q/%q, want America/New_York", dp.TimeZone, dp.TimeZoneField)
	}

	utc, err := ProfileBytes(src, ProfileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	u := datetimeOf(t, utc, "ny")
	if u.Hours[20] != 0 || u.Hours[1] == 0 || u.End != "2026-02-25" || u.TimeZone != "" {
		t.Errorf("UTC capture hours[20]=%d hours[1]=%d end %s zone %q; want the UTC clock", u.Hours[20], u.Hours[1], u.End, u.TimeZone)
	}

	spec, _ := SpecFromProfile(local, 100)
	for _, f := range spec.Fields {
		if f.Name != "ny" {
			continue
		}
		if f.Params["time_zone"] != "America/New_York" || f.Params["dst_policy"] != profileDSTPolicy {
			t.Errorf("rebuilt params %v; want time_zone + dst_policy %q", f.Params, profileDSTPolicy)
		}
		if _, ok := f.Params["time_zone_field"]; ok {
			t.Errorf("rebuilt params carry time_zone_field: %v", f.Params)
		}
	}
}

// TestProfileZone_CohortDefaultZoneField: a cohort declaring a default
// zone field, profiled with no option, is captured per row on it and
// records time_zone_field — exactly the explicit option's document. An
// explicit option wins over the declared field.
func TestProfileZone_CohortDefaultZoneField(t *testing.T) {
	t.Parallel()
	src := zoneCaptureSource(t)
	declared := withDefaultZoneField(t, src, "site")

	byDefault, err := ProfileBytes(declared, ProfileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	dp := datetimeOf(t, byDefault, "ts")
	assertEveningWeekdays(t, "cohort default", dp)
	if dp.TimeZoneField != "site" || dp.TimeZone != "" {
		t.Errorf("recorded clock %q/%q, want time_zone_field site", dp.TimeZone, dp.TimeZoneField)
	}

	explicit, err := ProfileBytes(src, ProfileOptions{TimeZoneField: "site"})
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(byDefault)
	b, _ := json.Marshal(explicit)
	if !bytes.Equal(a, b) {
		t.Errorf("cohort-default capture differs from the explicit time_zone_field capture")
	}

	// An explicit zone wins over the declared field; UTC is the plain
	// UTC capture, byte-identical to the undeclared cohort's.
	override, err := ProfileBytes(declared, ProfileOptions{TimeZone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	o := datetimeOf(t, override, "ts")
	if o.TimeZone != "" || o.TimeZoneField != "" || o.Hours[20] != 0 || o.Hours[1] == 0 {
		t.Errorf("explicit time_zone did not override the declared field: zone %q/%q hours %v", o.TimeZone, o.TimeZoneField, o.Hours)
	}
	plain, err := ProfileBytes(src, ProfileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	c, _ := json.Marshal(override)
	d, _ := json.Marshal(plain)
	if !bytes.Equal(c, d) {
		t.Errorf("time_zone UTC over a declared cohort differs from the plain UTC capture")
	}
	tokyo, err := ProfileBytes(declared, ProfileOptions{TimeZone: "Asia/Tokyo"})
	if err != nil {
		t.Fatal(err)
	}
	if k := datetimeOf(t, tokyo, "ts"); k.TimeZone != "Asia/Tokyo" || k.TimeZoneField != "" {
		t.Errorf("explicit Asia/Tokyo recorded %q/%q", k.TimeZone, k.TimeZoneField)
	}

	spec, warns := SpecFromProfile(byDefault, 100)
	for _, f := range spec.Fields {
		if f.Name == "ts" && (f.Params["time_zone_field"] != "site" || f.Params["dst_policy"] != profileDSTPolicy) {
			t.Errorf("rebuilt ts params %v; want time_zone_field site + dst_policy", f.Params)
		}
	}
	if _, _, err := SynthBytes(spec, Options{Seed: 1}); err != nil {
		t.Fatalf("rebuilt spec does not generate: %v (warnings %v)", err, warns)
	}
}

// TestProfileZone_NoZoneIsByteIdentical: no option and no declared
// field is the UTC capture, with no zone key in the document.
func TestProfileZone_NoZoneIsByteIdentical(t *testing.T) {
	t.Parallel()
	src := zoneCaptureSource(t)
	a, err := ProfileBytes(src, ProfileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	doc, _ := json.Marshal(a)
	if bytes.Contains(doc, []byte(`"time_zone`)) {
		t.Errorf("unzoned capture carries a zone key")
	}
	spec, _ := SpecFromProfile(a, 10)
	for _, f := range spec.Fields {
		if _, ok := f.Params["dst_policy"]; ok && f.Distribution == DistUniformDateTime {
			t.Errorf("%s: unzoned rebuild emits dst_policy", f.Name)
		}
	}
}

// TestProfileZone_NullZoneCell: a reading whose zone cell is null has no
// local clock: it is left out of the counts with a warning, and the
// rebuilt datetime is nullable so the spec's null-zone rule does not
// refuse the run.
func TestProfileZone_NullZoneCell(t *testing.T) {
	t.Parallel()
	src := zoneCaptureSource(t)
	p, err := ProfileBytes(src, ProfileOptions{TimeZoneField: "alt_zone"})
	if err != nil {
		t.Fatal(err)
	}
	dp := datetimeOf(t, p, "ny")
	sum := 0
	for _, c := range dp.Weekdays {
		sum += c
	}
	if sum == 0 || sum >= 4000 {
		t.Errorf("counted %d readings; want only the zoned ones", sum)
	}
	warned := false
	for _, w := range p.Warnings {
		warned = warned || strings.Contains(w, `"ny"`) && strings.Contains(w, "null zone")
	}
	if !warned {
		t.Errorf("no null-zone warning in %v", p.Warnings)
	}
	spec, _ := SpecFromProfile(p, 200)
	for _, f := range spec.Fields {
		if f.Name == "ny" && !f.Nullable {
			t.Errorf("rebuilt ny is not nullable over a nullable zone field")
		}
	}
	if _, _, err := SynthBytes(spec, Options{Seed: 2}); err != nil {
		t.Fatalf("rebuilt spec does not generate: %v", err)
	}
}

// TestProfileZone_DSTGapSourceRebuilds: a source whose local hours cover
// a spring-forward night rebuilds with dst_policy earlier, so a refilled
// reading in the skipped hour resolves (and warns) instead of aborting.
func TestProfileZone_DSTGapSourceRebuilds(t *testing.T) {
	t.Parallel()
	src, _, err := SynthBytes(&Spec{RowCount: 3000, Fields: []FieldSpec{
		{Name: "ts", Type: "datetime", Distribution: DistUniformDateTime, Params: map[string]any{
			"start": "2026-03-02", "end": "2026-03-14", "time_zone": "America/New_York", "dst_policy": "later"}},
	}}, Options{Seed: 9})
	if err != nil {
		t.Fatal(err)
	}
	p, err := ProfileBytes(src, ProfileOptions{TimeZone: "America/New_York"})
	if err != nil {
		t.Fatal(err)
	}
	spec, _ := SpecFromProfile(p, 3000)
	_, res, err := SynthBytes(spec, Options{Seed: 10})
	if err != nil {
		t.Fatalf("from-profile rebuild aborted on a DST gap: %v", err)
	}
	resolved := false
	for _, w := range res.Warnings {
		resolved = resolved || strings.Contains(w, "DST transition")
	}
	if !resolved {
		t.Errorf("no DST resolution warning; the rebuild never drew the gap (warnings %v)", res.Warnings)
	}
}

// TestProfileZone_Refusals: exclusive options, an unknown zone, a zone
// field that is not a categorical of the cohort, or one holding a
// non-zone name are refused with the zone family's codes.
func TestProfileZone_Refusals(t *testing.T) {
	t.Parallel()
	src := zoneCaptureSource(t)
	cases := []struct {
		name string
		opts ProfileOptions
		code errors.Code
	}{
		{"both", ProfileOptions{TimeZone: "UTC", TimeZoneField: "site"}, errors.SERVICE_VALIDATION},
		{"unknown zone", ProfileOptions{TimeZone: "Mars/Olympus"}, errors.PULSE_TIMEZONE_UNKNOWN},
		{"missing field", ProfileOptions{TimeZoneField: "nope"}, errors.SERVICE_VALIDATION},
		{"numeric field", ProfileOptions{TimeZoneField: "amount"}, errors.SERVICE_VALIDATION},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ProfileBytes(src, tc.opts)
			if code := codeOf(err); code != tc.code {
				t.Fatalf("code %q (%v), want %s", code, err, tc.code)
			}
		})
	}

	bad, _, err := SynthBytes(&Spec{RowCount: 10, Fields: []FieldSpec{
		{Name: "z", Type: "categorical_u8", Distribution: DistWeightedCategorical, Params: map[string]any{"values": []any{"EST"}}},
	}}, Options{Seed: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ProfileBytes(bad, ProfileOptions{TimeZoneField: "z"}); codeOf(err) != errors.PULSE_TIMEZONE_UNKNOWN {
		t.Fatalf("non-zone dictionary entry: %v, want PULSE_TIMEZONE_UNKNOWN", err)
	}
}
