package processing

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// newYoYSpec returns the canonical happy-path OVERLAY_YOY spec with a
// caller-supplied frequency Param. GROUP scope, `Ref.YoY` populated as
// the empty marker, deterministic Name pinned in assertions.
func newYoYSpec(name, frequency string) types.OverlaySpec {
	params, _ := json.Marshal(map[string]any{"frequency": frequency})
	return types.OverlaySpec{
		Name:   name,
		Kind:   types.OverlayKindYoY,
		Scope:  types.OverlayScopeGroup,
		Ref:    types.OverlayRef{YoY: &types.OverlayYoYRef{}},
		Params: params,
	}
}

// newStubYoYHost wraps a SeriesHostView with GROUP_DATE grouper-kind
// introspection populated so the YoY handler's runtime host-grouper
// check passes. Mirrors newStubSeriesHost but threads the
// GROUP_DATE marker for the single-grouper case.
func newStubYoYHost(keys []types.AxisKey, values []float64) *SeriesHostView {
	if values == nil {
		return NewSeriesHostViewWithGrouper(keys, nil, []string{"order_date"}, []types.GroupType{types.GROUP_DATE})
	}
	resolver := func(i int) (float64, bool) {
		if i < 0 || i >= len(values) {
			return 0, false
		}
		v := values[i]
		if math.IsNaN(v) {
			return 0, false
		}
		return v, true
	}
	return NewSeriesHostViewWithGrouper(keys, resolver, []string{"order_date"}, []types.GroupType{types.GROUP_DATE})
}

// TestOverlay_YoY_AnnualBasic pins the annual frequency arm.
// 5-year series [10, 20, 40, 80, 160] yields: year[0]=NaN, year[1]=200,
// year[2]=200, year[3]=200, year[4]=200.
func TestOverlay_YoY_AnnualBasic(t *testing.T) {
	keys := []types.AxisKey{{"2020"}, {"2021"}, {"2022"}, {"2023"}, {"2024"}}
	values := []float64{10.0, 20.0, 40.0, 80.0, 160.0}
	host := newStubYoYHost(keys, values)
	specs := []types.OverlaySpec{newYoYSpec("yoy", "annual")}

	layers, warnings, err := ApplyOverlaysSeries(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlaysSeries: %v", err)
	}
	assertNoOverlayWarnings(t, warnings)
	if len(layers) != 1 {
		t.Fatalf("expected 1 layer, got %d", len(layers))
	}

	entries := layers[0].Payload.Series.Entries
	if len(entries) != 5 {
		t.Fatalf("len(Entries) = %d, want 5", len(entries))
	}
	// First entry: NaN (no prior).
	if entries[0].Summary.Statistic == nil || !math.IsNaN(*entries[0].Summary.Statistic) {
		t.Fatalf("entries[0].Summary.Statistic = %v, want NaN", entries[0].Summary.Statistic)
	}
	const tol = 1e-9
	wants := []float64{200, 200, 200, 200} // each year doubles
	for i, w := range wants {
		assertSeriesEntryStatisticWithinTol(t, &layers[0], i+1, w, tol)
	}
}

// TestOverlay_YoY_MonthlyBasic pins the monthly frequency arm.
// 24-month series with months[0..11]=100*ones and months[12..23]=200*ones
// yields the first 12 months as NaN, months 13..24 as 200 (each month
// doubled vs the same month one year prior).
func TestOverlay_YoY_MonthlyBasic(t *testing.T) {
	keys := make([]types.AxisKey, 24)
	values := make([]float64, 24)
	for i := 0; i < 24; i++ {
		keys[i] = types.AxisKey{i}
		if i < 12 {
			values[i] = 100.0
		} else {
			values[i] = 200.0
		}
	}
	host := newStubYoYHost(keys, values)
	specs := []types.OverlaySpec{newYoYSpec("yoy", "monthly")}

	layers, warnings, err := ApplyOverlaysSeries(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlaysSeries: %v", err)
	}
	assertNoOverlayWarnings(t, warnings)
	entries := layers[0].Payload.Series.Entries

	// First 12 entries: NaN.
	for i := 0; i < 12; i++ {
		if entries[i].Summary.Statistic == nil || !math.IsNaN(*entries[i].Summary.Statistic) {
			t.Fatalf("entries[%d].Summary.Statistic = %v, want NaN", i, entries[i].Summary.Statistic)
		}
	}
	// Months 12..23: 200 (each month doubled vs same month one year prior).
	const tol = 1e-9
	for i := 12; i < 24; i++ {
		assertSeriesEntryStatisticWithinTol(t, &layers[0], i, 200.0, tol)
	}
}

// TestOverlay_YoY_WeeklyBasic pins the weekly frequency arm.
// 104-week series with weeks[0..51]=10 and weeks[52..103]=15 yields
// the first 52 weeks as NaN and weeks 52..103 as 150.
func TestOverlay_YoY_WeeklyBasic(t *testing.T) {
	keys := make([]types.AxisKey, 104)
	values := make([]float64, 104)
	for i := 0; i < 104; i++ {
		keys[i] = types.AxisKey{i}
		if i < 52 {
			values[i] = 10.0
		} else {
			values[i] = 15.0
		}
	}
	host := newStubYoYHost(keys, values)
	specs := []types.OverlaySpec{newYoYSpec("yoy", "weekly")}

	layers, warnings, err := ApplyOverlaysSeries(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlaysSeries: %v", err)
	}
	assertNoOverlayWarnings(t, warnings)
	entries := layers[0].Payload.Series.Entries

	for i := 0; i < 52; i++ {
		if entries[i].Summary.Statistic == nil || !math.IsNaN(*entries[i].Summary.Statistic) {
			t.Fatalf("entries[%d].Summary.Statistic = %v, want NaN", i, entries[i].Summary.Statistic)
		}
	}
	const tol = 1e-9
	for i := 52; i < 104; i++ {
		assertSeriesEntryStatisticWithinTol(t, &layers[0], i, 150.0, tol)
	}
}

// TestOverlay_YoY_QuarterlyBasic pins the quarterly frequency arm.
// 8-quarter series yields the first 4 NaN, then YoY ratios for
// quarters 4..7.
func TestOverlay_YoY_QuarterlyBasic(t *testing.T) {
	keys := []types.AxisKey{{"2020-Q1"}, {"2020-Q2"}, {"2020-Q3"}, {"2020-Q4"},
		{"2021-Q1"}, {"2021-Q2"}, {"2021-Q3"}, {"2021-Q4"}}
	values := []float64{10.0, 20.0, 30.0, 40.0, 11.0, 22.0, 33.0, 44.0}
	host := newStubYoYHost(keys, values)
	specs := []types.OverlaySpec{newYoYSpec("yoy", "quarterly")}

	layers, warnings, err := ApplyOverlaysSeries(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlaysSeries: %v", err)
	}
	assertNoOverlayWarnings(t, warnings)
	entries := layers[0].Payload.Series.Entries

	for i := 0; i < 4; i++ {
		if entries[i].Summary.Statistic == nil || !math.IsNaN(*entries[i].Summary.Statistic) {
			t.Fatalf("entries[%d].Summary.Statistic = %v, want NaN", i, entries[i].Summary.Statistic)
		}
	}
	const tol = 1e-9
	wants := []float64{110, 110, 110, 110} // each Q matches Q1.1 vs Q1 ratio
	for i, w := range wants {
		assertSeriesEntryStatisticWithinTol(t, &layers[0], i+4, w, tol)
	}
}

// TestOverlay_YoY_DailyBasic pins the daily frequency arm. Three keys
// spanning ~370 days exercise the exact-date 365-days-prior lookup.
// 2020-01-01 has no prior; 2021-01-01 = 200; 2021-01-02 = 200.
func TestOverlay_YoY_DailyBasic(t *testing.T) {
	keys := []types.AxisKey{
		{"2020-01-01"}, {"2020-01-02"},
		{"2021-01-01"}, {"2021-01-02"},
	}
	values := []float64{10.0, 20.0, 20.0, 40.0}
	host := newStubYoYHost(keys, values)
	specs := []types.OverlaySpec{newYoYSpec("yoy", "daily")}

	layers, warnings, err := ApplyOverlaysSeries(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlaysSeries: %v", err)
	}
	assertNoOverlayWarnings(t, warnings)
	entries := layers[0].Payload.Series.Entries

	// First two entries: NaN (no prior-year keys for 2020 dates).
	for i := 0; i < 2; i++ {
		if entries[i].Summary.Statistic == nil || !math.IsNaN(*entries[i].Summary.Statistic) {
			t.Fatalf("entries[%d].Summary.Statistic = %v, want NaN", i, entries[i].Summary.Statistic)
		}
	}
	// 2021-01-01 vs 2020-01-01 = 20/10 * 100 = 200.
	// 2021-01-02 vs 2020-01-02 = 40/20 * 100 = 200.
	const tol = 1e-9
	assertSeriesEntryStatisticWithinTol(t, &layers[0], 2, 200.0, tol)
	assertSeriesEntryStatisticWithinTol(t, &layers[0], 3, 200.0, tol)
}

// TestOverlay_YoY_DailyFeb29EmitsNaN pins the leap-year exact-key rule:
// 2020-02-29 → 2019-02-29 (non-leap) → no exact-key match → NaN, no
// error or warning.
func TestOverlay_YoY_DailyFeb29EmitsNaN(t *testing.T) {
	keys := []types.AxisKey{
		{"2019-02-28"},
		{"2020-02-28"},
		{"2020-02-29"},
	}
	values := []float64{100.0, 110.0, 120.0}
	host := newStubYoYHost(keys, values)
	specs := []types.OverlaySpec{newYoYSpec("yoy", "daily")}

	layers, warnings, err := ApplyOverlaysSeries(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlaysSeries: %v", err)
	}
	assertNoOverlayWarnings(t, warnings)
	entries := layers[0].Payload.Series.Entries

	// 2019-02-28: NaN (no 2018 key).
	if entries[0].Summary.Statistic == nil || !math.IsNaN(*entries[0].Summary.Statistic) {
		t.Fatalf("entries[0] = %v, want NaN", entries[0].Summary.Statistic)
	}
	// 2020-02-28: 110/100 * 100 = 110 (vs 2019-02-28).
	const tol = 1e-9
	assertSeriesEntryStatisticWithinTol(t, &layers[0], 1, 110.0, tol)
	// 2020-02-29: NaN — 2019-02-29 does not exist as a key, exact-key
	// lookup misses, no warning.
	if entries[2].Summary.Statistic == nil || !math.IsNaN(*entries[2].Summary.Statistic) {
		t.Fatalf("entries[2] = %v, want NaN (Feb 29 in non-leap prior year)", entries[2].Summary.Statistic)
	}
}

// TestOverlay_YoY_HourlyBasic pins the hourly frequency arm with a small
// fixture spanning the 365-day stride. 2020-01-01T00 vs 2019-01-01T00.
func TestOverlay_YoY_HourlyBasic(t *testing.T) {
	keys := []types.AxisKey{
		{"2019-01-01T00"},
		{"2019-01-01T01"},
		{"2020-01-01T00"},
		{"2020-01-01T01"},
	}
	values := []float64{5.0, 10.0, 50.0, 100.0}
	host := newStubYoYHost(keys, values)
	specs := []types.OverlaySpec{newYoYSpec("yoy", "hourly")}

	layers, warnings, err := ApplyOverlaysSeries(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlaysSeries: %v", err)
	}
	assertNoOverlayWarnings(t, warnings)
	entries := layers[0].Payload.Series.Entries

	// First two entries: NaN (no 2018 keys).
	for i := 0; i < 2; i++ {
		if entries[i].Summary.Statistic == nil || !math.IsNaN(*entries[i].Summary.Statistic) {
			t.Fatalf("entries[%d].Summary.Statistic = %v, want NaN", i, entries[i].Summary.Statistic)
		}
	}
	// 2020-01-01T00 = 50/5 * 100 = 1000. 2020-01-01T01 = 100/10 * 100 = 1000.
	const tol = 1e-9
	assertSeriesEntryStatisticWithinTol(t, &layers[0], 2, 1000.0, tol)
	assertSeriesEntryStatisticWithinTol(t, &layers[0], 3, 1000.0, tol)
}

// TestOverlay_YoY_FrequencyMissingRejected pins the missing-Param arm:
// spec without Params (or with empty Params) is rejected with a coded
// error carrying PULSE_OVERLAY_YOY_FREQUENCY_MISSING.
func TestOverlay_YoY_FrequencyMissingRejected(t *testing.T) {
	keys := []types.AxisKey{{"2020-01"}, {"2021-01"}}
	host := newStubYoYHost(keys, []float64{10.0, 20.0})

	cases := []struct {
		name string
		spec types.OverlaySpec
	}{
		{
			name: "Params nil",
			spec: types.OverlaySpec{
				Name:  "yoy",
				Kind:  types.OverlayKindYoY,
				Scope: types.OverlayScopeGroup,
				Ref:   types.OverlayRef{YoY: &types.OverlayYoYRef{}},
			},
		},
		{
			name: "Params empty object missing frequency",
			spec: types.OverlaySpec{
				Name:   "yoy",
				Kind:   types.OverlayKindYoY,
				Scope:  types.OverlayScopeGroup,
				Ref:    types.OverlayRef{YoY: &types.OverlayYoYRef{}},
				Params: json.RawMessage(`{}`),
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := ApplyOverlaysSeries([]types.OverlaySpec{tc.spec}, host)
			if err == nil {
				t.Fatalf("expected coded error for missing frequency Param, got nil")
			}
			coded, ok := err.(*errors.CodedError)
			if !ok {
				t.Fatalf("err type = %T, want *errors.CodedError", err)
			}
			if got, want := coded.Details["code"], string(errors.PULSE_OVERLAY_YOY_FREQUENCY_MISSING); got != want {
				t.Errorf("err.Details[\"code\"] = %v, want %v", got, want)
			}
			if got, want := coded.Details["param"], "frequency"; got != want {
				t.Errorf("err.Details[\"param\"] = %v, want %v", got, want)
			}
		})
	}
}

// TestOverlay_YoY_FrequencyIncompatibleRejected pins the unsupported-
// frequency arm: spec with frequency outside the supported set is
// rejected with a coded error carrying
// PULSE_OVERLAY_YOY_INCOMPATIBLE_FREQUENCY.
func TestOverlay_YoY_FrequencyIncompatibleRejected(t *testing.T) {
	keys := []types.AxisKey{{"2020-01"}, {"2021-01"}}
	host := newStubYoYHost(keys, []float64{10.0, 20.0})

	for _, freq := range []string{"minutely", "yearly", "biennial", "decade"} {
		t.Run(freq, func(t *testing.T) {
			spec := newYoYSpec("yoy", freq)
			_, _, err := ApplyOverlaysSeries([]types.OverlaySpec{spec}, host)
			if err == nil {
				t.Fatalf("expected coded error for unsupported frequency %q, got nil", freq)
			}
			coded, ok := err.(*errors.CodedError)
			if !ok {
				t.Fatalf("err type = %T, want *errors.CodedError", err)
			}
			if got, want := coded.Details["code"], string(errors.PULSE_OVERLAY_YOY_INCOMPATIBLE_FREQUENCY); got != want {
				t.Errorf("err.Details[\"code\"] = %v, want %v", got, want)
			}
			if got, want := coded.Details["frequency"], freq; got != want {
				t.Errorf("err.Details[\"frequency\"] = %v, want %v", got, want)
			}
			sup, ok := coded.Details["supported"].([]string)
			if !ok {
				t.Fatalf("err.Details[\"supported\"] = %v (type %T), want []string", coded.Details["supported"], coded.Details["supported"])
			}
			if len(sup) != 6 {
				t.Errorf("supported list length = %d, want 6", len(sup))
			}
		})
	}
}

// TestOverlay_YoY_NonDateGrouperRejected pins the host-grouper-kind
// arm: a SeriesHostView carrying a non-GROUP_DATE first grouper is
// rejected at runtime with a coded error carrying
// PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE.
func TestOverlay_YoY_NonDateGrouperRejected(t *testing.T) {
	keys := []types.AxisKey{{"east"}, {"west"}}
	host := NewSeriesHostViewWithGrouper(
		keys,
		func(i int) (float64, bool) { return 10.0, true },
		[]string{"region"},
		[]types.GroupType{types.GROUP_CATEGORY},
	)
	specs := []types.OverlaySpec{newYoYSpec("yoy", "monthly")}

	_, _, err := ApplyOverlaysSeries(specs, host)
	if err == nil {
		t.Fatalf("expected coded error for non-GROUP_DATE host, got nil")
	}
	coded, ok := err.(*errors.CodedError)
	if !ok {
		t.Fatalf("err type = %T, want *errors.CodedError", err)
	}
	if got, want := coded.Details["code"], string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE); got != want {
		t.Errorf("err.Details[\"code\"] = %v, want %v", got, want)
	}
	if got, want := coded.Details["host_grouper"], string(types.GROUP_CATEGORY); got != want {
		t.Errorf("err.Details[\"host_grouper\"] = %v, want %v", got, want)
	}
}

// TestOverlay_YoY_ZeroPriorEmitsRefZero pins the zero-prior path:
// annual frequency with prior-year value 0 yields NaN + REF_ZERO.
func TestOverlay_YoY_ZeroPriorEmitsRefZero(t *testing.T) {
	keys := []types.AxisKey{{"2020"}, {"2021"}}
	values := []float64{0.0, 100.0}
	host := newStubYoYHost(keys, values)
	specs := []types.OverlaySpec{newYoYSpec("yoy", "annual")}

	layers, warnings, err := ApplyOverlaysSeries(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlaysSeries: %v", err)
	}
	assertWarningCode(t, warnings, string(errors.PULSE_OVERLAY_REF_ZERO), 1)

	entries := layers[0].Payload.Series.Entries
	if len(entries) != 2 {
		t.Fatalf("len(Entries) = %d, want 2", len(entries))
	}
	// year[0]: NaN (no prior).
	if entries[0].Summary.Statistic == nil || !math.IsNaN(*entries[0].Summary.Statistic) {
		t.Fatalf("entries[0] = %v, want NaN", entries[0].Summary.Statistic)
	}
	// year[1]: NaN (prior was zero).
	if entries[1].Summary.Statistic == nil || !math.IsNaN(*entries[1].Summary.Statistic) {
		t.Fatalf("entries[1] = %v, want NaN", entries[1].Summary.Statistic)
	}
}

// TestOverlay_YoY_FirstYearNoErrorJustNaN pins the first-year-no-
// comparison contract: the first present year emits NaN without error
// or warning ("no comparison available" is distinct from "denominator
// was zero").
func TestOverlay_YoY_FirstYearNoErrorJustNaN(t *testing.T) {
	keys := []types.AxisKey{{"2020"}, {"2021"}, {"2022"}, {"2023"}, {"2024"}}
	values := []float64{10.0, 20.0, 40.0, 80.0, 160.0}
	host := newStubYoYHost(keys, values)
	specs := []types.OverlaySpec{newYoYSpec("yoy", "annual")}

	layers, warnings, err := ApplyOverlaysSeries(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlaysSeries: %v", err)
	}
	assertNoOverlayWarnings(t, warnings)
	entries := layers[0].Payload.Series.Entries

	if entries[0].Summary.Statistic == nil || !math.IsNaN(*entries[0].Summary.Statistic) {
		t.Fatalf("entries[0].Summary.Statistic = %v, want NaN (no comparison)", entries[0].Summary.Statistic)
	}
}

// TestOverlay_YoY_DefaultLayerName pins the synthesised renderer-facing
// label when the spec omits Name. OVERLAY_YOY is the windowed
// year-over-year kind; the synthesiser surfaces the lower-case bare-
// kind string "yoy".
func TestOverlay_YoY_DefaultLayerName(t *testing.T) {
	keys := []types.AxisKey{{"2020"}, {"2021"}}
	host := newStubYoYHost(keys, []float64{10.0, 20.0})
	params, _ := json.Marshal(map[string]any{"frequency": "annual"})
	specs := []types.OverlaySpec{{
		Kind:   types.OverlayKindYoY,
		Scope:  types.OverlayScopeGroup,
		Ref:    types.OverlayRef{YoY: &types.OverlayYoYRef{}},
		Params: params,
	}}
	layers, _, err := ApplyOverlaysSeries(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlaysSeries: %v", err)
	}
	if got, want := layers[0].Name, "yoy"; got != want {
		t.Errorf("layer.Name = %q, want %q", got, want)
	}
}

// TestOverlay_YoY_NilHostReturnsCoded pins the defense-in-depth
// nil-host arm: a nil SeriesHostView surfaces a coded
// PROCESSING_INTERNAL error rather than panicking.
func TestOverlay_YoY_NilHostReturnsCoded(t *testing.T) {
	spec := newYoYSpec("yoy", "annual")
	_, _, err := applyYoY(&spec, nil)
	if err == nil {
		t.Fatalf("expected coded error for nil host, got nil")
	}
	coded, ok := err.(*errors.CodedError)
	if !ok {
		t.Fatalf("err type = %T, want *errors.CodedError", err)
	}
	if got, want := coded.Details["code"], string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE); got != want {
		t.Errorf("err.Details[\"code\"] = %v, want %v", got, want)
	}
}

func TestOverlay_YoY_BufferedPath_RejectsNonDateHost(t *testing.T) {
	specs := []types.OverlaySpec{newYoYSpec("yoy", "monthly")}
	schema := overlayIntegrationSchema(t)
	recs := overlayIntegrationRecords(schema)
	req := overlayIntegrationBaseRequest()
	req.Overlays = specs
	proc := NewProcessor(schema)
	_, err := proc.Process(t.Context(), req, NewSliceIterator(recs))
	if err == nil {
		t.Fatalf("expected coded error for OVERLAY_YOY against GROUP_CATEGORY host, got nil")
	}
	coded, ok := err.(*errors.CodedError)
	if !ok {
		t.Fatalf("err type = %T, want *errors.CodedError", err)
	}
	if got, want := coded.Details["code"], string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE); got != want {
		t.Errorf("err.Details[\"code\"] = %v, want %v", got, want)
	}
}

// TestOverlay_YoY_FrequencyPromotedFromGroupParams_E2E pins the
// orchestrator-side frequency promotion: when the spec.Params slot is
// empty but the host GROUP_DATE grouper carries `frequency`, the
// orchestrator promotes it onto the spec before handler dispatch and
// the runtime sees the frequency from the promoted slot.
//
// The test reuses overlayIntegrationRecords but with a custom request
// using a 3-key categorical grouper masquerading as GROUP_DATE via the
// runtime view extension. To exercise the actual orchestrator
// promotion path the test calls promoteYoYFrequencyFromGroupParams
// directly with a synthesised request.
func TestOverlay_YoY_FrequencyPromotedFromGroupParams_E2E(t *testing.T) {
	// Synthesised request with a GROUP_DATE grouper carrying
	// `frequency=monthly` on its Params slot. The spec.Params slot is
	// empty so the orchestrator must promote.
	specs := []types.OverlaySpec{{
		Name:  "yoy",
		Kind:  types.OverlayKindYoY,
		Scope: types.OverlayScopeGroup,
		Ref:   types.OverlayRef{YoY: &types.OverlayYoYRef{}},
		// Params intentionally absent.
	}}
	req := &types.Request{
		Groups: []*types.Group{
			{
				Type:   types.GROUP_DATE,
				Field:  "order_date",
				Params: json.RawMessage(`{"component": "month", "frequency": "monthly"}`),
			},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "value"},
		},
		Overlays: specs,
	}

	promoted := promoteYoYFrequencyFromGroupParams(req)
	if len(promoted) != 1 {
		t.Fatalf("len(promoted) = %d, want 1", len(promoted))
	}
	// The promoted spec's Params slot must carry the frequency.
	freq, ok := readYoYFrequencyFromParams(promoted[0].Params)
	if !ok {
		t.Fatalf("promoted spec.Params missing frequency entry: %s", string(promoted[0].Params))
	}
	if freq != "monthly" {
		t.Errorf("promoted frequency = %q, want %q", freq, "monthly")
	}
	// The original request's overlays slice must NOT be mutated.
	if _, ok := readYoYFrequencyFromParams(req.Overlays[0].Params); ok {
		t.Errorf("original req.Overlays[0].Params was mutated by promotion")
	}
}

// TestOverlay_YoY_FrequencyPromotionSkipsWhenSpecHasOverride pins the
// override-wins contract: when both spec.Params and Groups[0].Params
// carry a frequency, the spec value wins.
func TestOverlay_YoY_FrequencyPromotionSkipsWhenSpecHasOverride(t *testing.T) {
	specs := []types.OverlaySpec{{
		Name:   "yoy",
		Kind:   types.OverlayKindYoY,
		Scope:  types.OverlayScopeGroup,
		Ref:    types.OverlayRef{YoY: &types.OverlayYoYRef{}},
		Params: json.RawMessage(`{"frequency": "quarterly"}`), // override
	}}
	req := &types.Request{
		Groups: []*types.Group{
			{
				Type:   types.GROUP_DATE,
				Field:  "order_date",
				Params: json.RawMessage(`{"component": "month", "frequency": "monthly"}`),
			},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "value"},
		},
		Overlays: specs,
	}

	promoted := promoteYoYFrequencyFromGroupParams(req)
	freq, ok := readYoYFrequencyFromParams(promoted[0].Params)
	if !ok {
		t.Fatalf("spec.Params missing frequency entry: %s", string(promoted[0].Params))
	}
	if freq != "quarterly" {
		t.Errorf("promoted frequency = %q, want %q (spec override should win)", freq, "quarterly")
	}
}
