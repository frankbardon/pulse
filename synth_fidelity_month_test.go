package pulse

import (
	"testing"

	"github.com/frankbardon/pulse/internal/synth"
)

// monthVolumeSource is a datetime whose volume trends up across two
// years with a summer peak — the month series month_weights carry.
func monthVolumeSource() *synth.Spec {
	season := []float64{0.5, 0.6, 0.8, 1.0, 1.3, 1.6, 1.7, 1.5, 1.1, 0.9, 0.7, 0.5}
	months := make([]any, 24)
	for m := range months {
		months[m] = float64((1 + float64(m)/24) * season[m%12])
	}
	return &synth.Spec{RowCount: 5000, Fields: []synth.FieldSpec{
		{Name: "ts", Type: "datetime", Distribution: synth.DistUniformDateTime, Params: map[string]any{
			"start": "2024-01-10", "end": "2025-12-20", "month_weights": months}},
	}}
}

// TestSynthFidelity_MonthTVDReadsTheVolumeSeries (#594, FR-17): a twin
// rebuilt from the source's profile regenerates its month-by-month
// volume, so the datetime shape's month_tvd is small; the same twin
// with its month weights stripped regenerates flat, and month_tvd says
// so while hour_tvd and weekday_tvd stay small.
func TestSynthFidelity_MonthTVDReadsTheVolumeSeries(t *testing.T) {
	t.Parallel()

	src, _, err := synth.SynthBytes(monthVolumeSource(), synth.Options{Seed: 21})
	if err != nil {
		t.Fatal(err)
	}
	prof, err := synth.ProfileBytes(src, synth.ProfileOptions{IncludeStats: true})
	if err != nil {
		t.Fatal(err)
	}
	twin, _ := synth.SpecFromProfile(prof, 5000)
	if twin.Fields[0].Params["month_weights"] == nil {
		t.Fatalf("rebuilt ts carries no month_weights: %v", twin.Fields[0].Params)
	}
	report, _, err := writeUtilityReport(t, monthVolumeSource(), twin, SynthOptions{FidelityReportPath: "/report.json"})
	if err != nil {
		t.Fatal(err)
	}
	e := fieldEntry(report, "ts")
	if e == nil || e.Shape == nil {
		t.Fatal("ts: no shape")
	}
	if e.Shape.MonthTVD > 0.05 {
		t.Errorf("faithful twin month_tvd = %.4f, want ≤ 0.05", e.Shape.MonthTVD)
	}

	delete(twin.Fields[0].Params, "month_weights")
	flat, _, err := writeUtilityReport(t, monthVolumeSource(), twin, SynthOptions{FidelityReportPath: "/report.json"})
	if err != nil {
		t.Fatal(err)
	}
	f := fieldEntry(flat, "ts")
	if f == nil || f.Shape == nil {
		t.Fatal("flat ts: no shape")
	}
	if f.Shape.MonthTVD < 0.12 || f.Shape.HourTVD > 0.05 || f.Shape.WeekdayTVD > 0.05 {
		t.Errorf("flat twin shape = %+v, want month_tvd ≥ 0.12 with hour / weekday ≤ 0.05", f.Shape)
	}
}
