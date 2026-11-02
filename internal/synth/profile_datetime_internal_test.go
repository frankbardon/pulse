package synth

import (
	"bytes"
	"encoding/json"
	"math"
	"testing"
	"time"
)

// datetimeSourceSpec: a zoned, nullable datetime with a business-hours
// and weekday profile, beside a numeric and a categorical the capture
// arms (correlations, conditional pairs, models) could pair it with.
func datetimeSourceSpec() *Spec {
	return &Spec{RowCount: 6000, Fields: []FieldSpec{
		{Name: "site", Type: "categorical_u8", Distribution: DistWeightedCategorical,
			Params: map[string]any{"values": []any{"a", "b"}, "weights": []any{0.7, 0.3}}},
		{Name: "amount", Type: "f64", Distribution: DistNormal, Params: map[string]any{"mean": 10.0, "std": 2.0}},
		{Name: "ts", Type: "datetime", Nullable: true, NullRate: 0.1, Distribution: DistUniformDateTime, Params: map[string]any{
			"start": "2026-01-07", "end": "2026-03-15", "time_zone": "America/New_York", "dst_policy": "earlier",
			"hour_weights":    toAny(businessHours()),
			"weekday_weights": []any{1.0, 1.0, 1.0, 1.0, 1.0, 0.5, 0.0}}},
	}, Rules: []RuleSpec{
		// Site b runs 12 days late, so a numeric model of ts on site would
		// be admitted if the capture offered ts as a model target.
		{When: `site == "b" && !isnull(ts)`, SetExpr: map[string]string{"ts": "ts + 12 * 24 * 3600"}},
	}}
}

// utcShares returns the non-null datetime's UTC hour and weekday shares
// and its first / last UTC day.
func utcShares(t *testing.T, data []byte) (hours [24]float64, wd [7]float64, first, last string, nulls int) {
	t.Helper()
	rows, nm := datetimeRows(t, data)
	n := 0.0
	first, last = "9999", "0000"
	for i, r := range rows {
		if nm[i]["ts"] {
			nulls++
			continue
		}
		u := time.Unix(int64(r["ts"].(float64)), 0).UTC()
		hours[u.Hour()]++
		wd[(int(u.Weekday())+6)%7]++
		d := u.Format("2006-01-02")
		first, last = min(first, d), max(last, d)
		n++
	}
	for i := range hours {
		hours[i] /= n
	}
	for i := range wd {
		wd[i] /= n
	}
	return hours, wd, first, last, nulls
}

// TestProfileDatetime_RoundTripKeepsTheMarginal: profile create →
// SpecFromProfile → generate keeps a datetime a `datetime` field drawn by
// uniform_datetime, with the source's UTC day range, weekday and hour
// marginals and null rate — and with every capture arm on, the column
// joins no correlation, pair or model a uniform_datetime cannot ride.
func TestProfileDatetime_RoundTripKeepsTheMarginal(t *testing.T) {
	t.Parallel()

	src, _, err := SynthBytes(datetimeSourceSpec(), Options{Seed: 31})
	if err != nil {
		t.Fatal(err)
	}
	prof, err := ProfileBytes(src, ProfileOptions{IncludeStats: true, IncludeCorrelations: true, IncludeConditional: true, FitModels: true})
	if err != nil {
		t.Fatal(err)
	}
	var fp *FieldProfile
	for i := range prof.Fields {
		if prof.Fields[i].Name == "ts" {
			fp = &prof.Fields[i]
		}
	}
	if fp == nil || fp.Datetime == nil || fp.Numeric != nil {
		t.Fatalf("ts profile = %+v, want a datetime section and no numeric one", fp)
	}
	for _, m := range prof.Models {
		if m.Field == "ts" {
			t.Fatalf("ts was captured as a model target (intercept %v)", m.Intercept)
		}
	}
	spec, _ := SpecFromProfile(prof, 6000)
	for _, f := range spec.Fields {
		if f.Name == "ts" && (f.Type != "datetime" || f.Distribution != DistUniformDateTime || !f.Nullable) {
			t.Fatalf("rebuilt ts = %+v, want a nullable uniform_datetime datetime", f)
		}
	}
	for _, c := range spec.Correlations {
		if c.A == "ts" || c.B == "ts" {
			t.Fatalf("ts joined a correlation: %+v", c)
		}
	}
	out, _, err := SynthBytes(spec, Options{Seed: 32})
	if err != nil {
		t.Fatalf("generating from the profile: %v", err)
	}
	sh, sw, sf, sl, sn := utcShares(t, src)
	gh, gw, gf, gl, gn := utcShares(t, out)
	if sf != gf || sl != gl {
		t.Errorf("UTC day range %s..%s, want the source's %s..%s", gf, gl, sf, sl)
	}
	for h := range sh {
		if math.Abs(sh[h]-gh[h]) > 0.02 || (sh[h] == 0) != (gh[h] == 0) {
			t.Errorf("UTC hour %02d share %.4f, source %.4f", h, gh[h], sh[h])
		}
	}
	for d := range sw {
		if math.Abs(sw[d]-gw[d]) > 0.02 || (sw[d] == 0) != (gw[d] == 0) {
			t.Errorf("UTC weekday %d share %.4f, source %.4f", d+1, gw[d], sw[d])
		}
	}
	if math.Abs(float64(gn-sn)) > 150 {
		t.Errorf("nulls %d, source %d", gn, sn)
	}

	// The emitted spec round-trips: a re-parsed spec regenerates the
	// same bytes.
	raw, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	again, err := ParseSpec(raw)
	if err != nil {
		t.Fatalf("re-parsing the emitted spec: %v", err)
	}
	out2, _, err := SynthBytes(again, Options{Seed: 32})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, out2) {
		t.Fatal("the emitted spec does not regenerate byte-identically")
	}
}

// TestProfileDatetime_WeekdayWeightsArePerDay: over a range that is not
// whole weeks the captured weekday COUNTS over-represent the weekdays the
// range repeats; the rebuilt weights divide by each weekday's days in
// range, so a flat source rebuilds flat.
func TestProfileDatetime_WeekdayWeightsArePerDay(t *testing.T) {
	t.Parallel()

	// 2026-01-05 (Mon) .. 2026-01-14 (Wed): Mon-Wed twice, Thu-Sun once.
	p := datetimeParamsFromProfile(&DatetimeProfile{
		Start: "2026-01-05", End: "2026-01-14",
		Weekdays: [7]int{200, 200, 200, 100, 100, 100, 100},
	})
	w := p["weekday_weights"].([]any)
	for i, x := range w {
		if x.(float64) != 100 {
			t.Fatalf("weekday %d weight %v, want 100 (count / days in range)", i+1, x)
		}
	}
}

// TestProfileDatetime_PreCaptureDocumentStillRebuilds: a document
// captured before the datetime section existed carries the column's
// numeric summary and still rebuilds from it.
func TestProfileDatetime_PreCaptureDocumentStillRebuilds(t *testing.T) {
	t.Parallel()

	prof := &Profile{RowCount: 10, Fields: []FieldProfile{{Name: "ts", Type: "datetime",
		Numeric: &NumericProfile{Min: 1.7e9, Max: 1.71e9, Mean: 1.705e9, Std: 1e6}}}}
	spec, _ := SpecFromProfile(prof, 50)
	if spec.Fields[0].Distribution != DistNormal {
		t.Fatalf("distribution %q, want the numeric summary's normal", spec.Fields[0].Distribution)
	}
	if _, _, err := SynthBytes(spec, Options{Seed: 1}); err != nil {
		t.Fatal(err)
	}
}
