package processing

import (
	"encoding/json"
	"math"
	"reflect"
	"strconv"
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
			if got, want := string(coded.Code), string(errors.PULSE_OVERLAY_YOY_FREQUENCY_MISSING); got != want {
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
			if got, want := string(coded.Code), string(errors.PULSE_OVERLAY_YOY_INCOMPATIBLE_FREQUENCY); got != want {
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
	if got, want := string(coded.Code), string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE); got != want {
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
	if got, want := string(coded.Code), string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE); got != want {
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
	if got, want := string(coded.Code), string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE); got != want {
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

	promoted := promoteYoYFrequencyFromGroupParams(req, nil)
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

	promoted := promoteYoYFrequencyFromGroupParams(req, nil)
	freq, ok := readYoYFrequencyFromParams(promoted[0].Params)
	if !ok {
		t.Fatalf("spec.Params missing frequency entry: %s", string(promoted[0].Params))
	}
	if freq != "quarterly" {
		t.Errorf("promoted frequency = %q, want %q (spec override should win)", freq, "quarterly")
	}
}

// TestOverlay_YoY_MultiGrouperHostRejected: a composite host handed to
// the HANDLER directly (a caller-built view) is still refused
// PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE whatever position GROUP_DATE
// holds — defense in depth. The grouped-Process fold never does that: it
// partitions by the non-date keys and hands each partition the date key
// alone (TestOverlay_YoY_GroupedPartitions).
func TestOverlay_YoY_MultiGrouperHostRejected(t *testing.T) {
	keys := []types.AxisKey{{"2020-01", "east"}, {"2021-01", "east"}}
	values := func(i int) (float64, bool) { return 10.0, true }
	for _, kinds := range [][]types.GroupType{
		{types.GROUP_DATE, types.GROUP_CATEGORY},
		{types.GROUP_CATEGORY, types.GROUP_DATE},
	} {
		host := NewSeriesHostViewWithGrouper(keys, values, []string{"order_date", "region"}, kinds)
		_, _, err := ApplyOverlaysSeries([]types.OverlaySpec{newYoYSpec("yoy", "annual")}, host)
		coded, ok := err.(*errors.CodedError)
		if !ok {
			t.Fatalf("%v: err = %v (%T), want a coded refusal", kinds, err, err)
		}
		if coded.Code != errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE || coded.Details["groupers"] != 2 {
			t.Errorf("%v: refusal %s %v, want PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE groupers=2", kinds, coded.Code, coded.Details)
		}
	}
}

// yoyGroupedRun runs spec through the grouped-Process hook over rows
// (each "col=value" keys plus the measure under "v"), grouped by groups.
func yoyGroupedRun(t *testing.T, groups []*types.Group, spec types.OverlaySpec, rows []map[string]any) (*types.Response, error) {
	t.Helper()
	req := &types.Request{Groups: groups, Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "x", Label: "v"}}, Overlays: []types.OverlaySpec{spec}}
	resp := &types.Response{Data: rows}
	err := applyOverlaysSeriesToResponse(req, resp, nil, FullComputePlan())
	return resp, err
}

// TestOverlay_YoY_GroupedPartitions: over [region, day] and [day,
// region] the grouped-Process hook runs YoY once per region, the date
// key alone on each sub-host, so the daily exact-date lookup (which
// reads the key's date element) finds the prior year inside the region
// and never across it, and every entry keeps its full composite key in
// host order. The frequency is promoted from the GROUP_DATE grouper
// wherever it sits.
func TestOverlay_YoY_GroupedPartitions(t *testing.T) {
	// east: 2023-03-01=10, 2024-03-01=15; west: 2023-03-01=40,
	// 2024-03-01=20, 2024-03-02=7 (no 2023-03-02 anywhere).
	type pt struct {
		region, day string
		v           float64
	}
	pts := []pt{
		{"east", "2023-03-01", 10}, {"west", "2023-03-01", 40},
		{"east", "2024-03-01", 15}, {"west", "2024-03-01", 20}, {"west", "2024-03-02", 7},
	}
	want := map[string]string{
		"east|2023-03-01": "NaN", "west|2023-03-01": "NaN",
		"east|2024-03-01": "150", "west|2024-03-01": "50", "west|2024-03-02": "NaN",
	}
	day := func() *types.Group {
		return &types.Group{Type: types.GROUP_DATE, Field: "day", Params: json.RawMessage(`{"frequency": "daily"}`)}
	}
	region := &types.Group{Type: types.GROUP_CATEGORY, Field: "region"}
	spec := types.OverlaySpec{Name: "yoy", Kind: types.OverlayKindYoY, Scope: types.OverlayScopeGroup, Ref: types.OverlayRef{YoY: &types.OverlayYoYRef{}}}
	for name, groups := range map[string][]*types.Group{
		"region×day": {region, day()},
		"day×region": {day(), region},
	} {
		t.Run(name, func(t *testing.T) {
			var rows []map[string]any
			for _, p := range pts {
				rows = append(rows, map[string]any{"region": p.region, "day": p.day, "v": p.v})
			}
			resp, err := yoyGroupedRun(t, groups, spec, rows)
			if err != nil {
				t.Fatalf("hook: %v", err)
			}
			entries := resp.Overlays[0].Payload.Series.Entries
			if len(entries) != len(pts) {
				t.Fatalf("%d entries for %d rows", len(entries), len(pts))
			}
			dateAt := 1
			if groups[0].Type == types.GROUP_DATE {
				dateAt = 0
			}
			for i, e := range entries {
				p := pts[i]
				if len(e.Key) != 2 || e.Key[dateAt] != p.day || e.Key[1-dateAt] != p.region {
					t.Fatalf("entry %d key %v, want the full composite key of row %v", i, e.Key, p)
				}
				got := "absent"
				if st := e.Summary.Statistic; st != nil {
					got = strconv.FormatFloat(*st, 'g', -1, 64)
				}
				if w := want[p.region+"|"+p.day]; got != w {
					t.Errorf("%s %s: yoy %s, want %s", p.region, p.day, got, w)
				}
			}
			if c := resp.Overlays[0].Summary.Count; c == nil || *c != 2 {
				t.Errorf("summary count %v, want 2 (one comparison per region)", c)
			}
		})
	}
}

// TestOverlay_YoY_GroupedRefused: the hook refuses two date groupers
// and a partition_by other than the non-date keys with the
// seriespartition.YoY Details predict echoes; no layer is written.
func TestOverlay_YoY_GroupedRefused(t *testing.T) {
	region := &types.Group{Type: types.GROUP_CATEGORY, Field: "region"}
	day := &types.Group{Type: types.GROUP_DATE, Field: "day"}
	shipped := &types.Group{Type: types.GROUP_DATE, Field: "shipped"}
	rows := func() []map[string]any {
		return []map[string]any{{"region": "east", "day": "2024-03-01", "shipped": "2024-03-02", "v": 1.0}}
	}
	for _, c := range []struct {
		name   string
		groups []*types.Group
		params string
		code   errors.Code
		want   map[string]any
	}{
		{"two date groupers", []*types.Group{day, region, shipped}, `{"frequency": "daily"}`, errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE,
			map[string]any{"index": 0, "kind": "OVERLAY_YOY", "groupers": 3, "date_groupers": 2, "required": "GROUP_DATE"}},
		{"no date grouper", []*types.Group{region, {Type: types.GROUP_CATEGORY, Field: "shipped"}}, `{"frequency": "daily"}`, errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE,
			map[string]any{"index": 0, "kind": "OVERLAY_YOY", "groupers": 2, "date_groupers": 0, "required": "GROUP_DATE"}},
		{"partition_by the date", []*types.Group{region, day}, `{"frequency": "daily", "partition_by": ["day"]}`, errors.PULSE_OVERLAY_PARTITION_INVALID,
			map[string]any{"index": 0, "kind": "OVERLAY_YOY", "partition_by": []string{"day"}, "groups": []string{"region", "day"}, "reason": "not_non_date_keys", "field": "day"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			spec := types.OverlaySpec{Name: "yoy", Kind: types.OverlayKindYoY, Scope: types.OverlayScopeGroup,
				Ref: types.OverlayRef{YoY: &types.OverlayYoYRef{}}, Params: json.RawMessage(c.params)}
			resp, err := yoyGroupedRun(t, c.groups, spec, rows())
			if len(resp.Overlays) > 0 {
				t.Fatalf("a layer survived: %+v", resp.Overlays)
			}
			ce, ok := err.(*errors.CodedError)
			if !ok || ce.Code != c.code {
				t.Fatalf("err = %v, want %s", err, c.code)
			}
			if !reflect.DeepEqual(ce.Details, c.want) {
				t.Errorf("details %#v, want %#v", ce.Details, c.want)
			}
		})
	}
}

// TestPromoteYoYFrequency_FromDateGrouperAnywhere: the frequency
// fallback reads the GROUP_DATE grouper's Params wherever it sits, never
// a non-date groups[0]'s.
func TestPromoteYoYFrequency_FromDateGrouperAnywhere(t *testing.T) {
	spec := types.OverlaySpec{Name: "yoy", Kind: types.OverlayKindYoY, Scope: types.OverlayScopeGroup, Ref: types.OverlayRef{YoY: &types.OverlayYoYRef{}}}
	req := &types.Request{
		Groups: []*types.Group{
			{Type: types.GROUP_CATEGORY, Field: "region", Params: json.RawMessage(`{"frequency": "weekly"}`)},
			{Type: types.GROUP_DATE, Field: "day", Params: json.RawMessage(`{"frequency": "quarterly"}`)},
		},
		Overlays: []types.OverlaySpec{spec},
	}
	if freq, _ := readYoYFrequencyFromParams(promoteYoYFrequencyFromGroupParams(req, nil)[0].Params); freq != "quarterly" {
		t.Errorf("promoted frequency %q, want the date grouper's quarterly", freq)
	}
	req.Groups = req.Groups[:1]
	if _, ok := readYoYFrequencyFromParams(promoteYoYFrequencyFromGroupParams(req, nil)[0].Params); ok {
		t.Error("promoted a non-date grouper's frequency")
	}
}
