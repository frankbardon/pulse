package processing

import (
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// E4-S9 gap-fill batch for the six windowed-Process overlay kinds.
//
// E4-S2..S8 each landed extensive inline coverage at the per-kind level;
// this file picks up the cross-kind regressions the per-kind suites
// could not own:
//
//   - TestOverlay_IndexVsBaseline_SpecOrderStable — multi-overlay stack
//     ordering for buffered E4 kinds (INDEX_VS_BASELINE + DELTA_VS_BASELINE
//     on the same Request). Mirrors the E3 streamable-kinds
//     TestOverlay_SeriesSpecOrderPreserved_E2E pattern at the buffered
//     dispatch level — `Response.Overlays[0]` / `Response.Overlays[1]`
//     preserve request-side spec order regardless of which kind
//     finalizes first inside ApplyOverlaysSeries.
//   - TestOverlay_ZScoreVsRolling_WelfordParity — compare the rolling
//     Welford SD output against a naive `sqrt(sum((x-mean)^2)/(n-1))`
//     reference impl over a 20-point series with window=5. Cross-checks
//     the rolling carrier's online Welford-Pébaÿ recurrence against the
//     two-pass closed-form across every window frontier (the per-kind
//     suite pins individual window values but not the full sweep).
//     Tolerance: 1e-12 ULP (the cohort-analytics convention for
//     Welford-vs-naive sweeps).
//   - TestOverlay_YoY_AnnualMonthlyWeekly — single table-driven test
//     covering the 3 lower-resolution YoY frequencies (annual / monthly /
//     weekly) at consolidated regression granularity. Per the story
//     brief: this is the consolidated regression check for the YoY math.
//     The per-frequency tests in overlay_yoy_test.go pin the specific
//     fixture-value outputs (the existing TestOverlay_YoY_AnnualBasic
//     etc.); this test asserts the kind handles every coarse-frequency
//     arm end-to-end through the dispatch in one shot, so a future
//     refactor cannot quietly break one arm while the others pass.
//   - TestOverlay_YoY_HourlyExactArithmetic — explicit 8760-hour offset
//     on an hourly date series, exact-key prior-year lookup. Pins the
//     hourly stride arithmetic (365*24h) the existing
//     TestOverlay_YoY_HourlyBasic test only covers in the 4-key
//     minimal-fixture form. This test walks a multi-day hourly series
//     and asserts every prior-year ordinal lands on the correct host
//     key.
//   - TestOverlay_E4SeriesHandlers_ShapeContract — property assertion
//     across every E4-added SERIES handler. Each handler returns a
//     SERIES-shape OverlayLayer whose `Series.Entries` length equals
//     `host.GroupCount()`, preserves the host's group-key order, and
//     synthesises the documented per-kind default Name. Catches future
//     refactors that accidentally diverge any handler from the
//     canonical SERIES-host contract.
//
// All tests use newStubSeriesHost / newStubYoYHost from the per-kind
// suites — no disk I/O, hermetic by construction. The processing/
// package's overlay_*_test.go family is the canonical home for these
// fixtures; this file picks up the cross-kind shape that none of them
// could own on its own.

// TestOverlay_IndexVsBaseline_SpecOrderStable pins the
// multi-overlay-stack-order contract at the SERIES dispatch level:
// when a Request carries multiple SERIES overlays in matching slot
// order, `Response.Overlays` preserves the request-side ordering
// regardless of which kind finalizes first inside ApplyOverlaysSeries.
// The brief named this test against the INDEX_VS_BASELINE +
// DELTA_VS_BASELINE pair so the assertion covers two buffered E4 kinds
// in one fixture (their per-kind tests cover the cell-value math; this
// covers the cross-kind ordering contract).
//
// Mirrors the E3 streamable-family TestOverlay_SeriesSpecOrderPreserved_E2E
// at the SERIES dispatch level — both kinds in this test are buffered
// per types.OverlayStreamability so the orchestrator's mixed-mode gate
// is not the subject; the spec-order guarantee rides on the
// ApplyOverlaysSeries dispatch loop in processing/overlay_series.go.
func TestOverlay_IndexVsBaseline_SpecOrderStable(t *testing.T) {
	keys := []types.AxisKey{{"jan"}, {"feb"}, {"mar"}, {"apr"}}
	values := []float64{10.0, 20.0, 30.0, 40.0}
	host := newStubSeriesHost(keys, values)

	// Two specs in deliberate order: INDEX_VS_BASELINE first (slot 0),
	// DELTA_VS_BASELINE second (slot 1). Both use Position=0 so the
	// baseline math reads off the same ordinal (decouples the math from
	// the ordering check).
	specs := []types.OverlaySpec{
		newIndexVsBaselineSpec("idx_baseline", 0),
		newDeltaVsBaselineSpec("delta_baseline", 0),
	}

	layers, warnings, err := ApplyOverlaysSeries(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlaysSeries: %v", err)
	}
	assertNoOverlayWarnings(t, warnings)

	if len(layers) != 2 {
		t.Fatalf("len(layers) = %d, want 2", len(layers))
	}

	// Slot 0 must be INDEX_VS_BASELINE; slot 1 must be DELTA_VS_BASELINE.
	if got, want := layers[0].Kind, types.OverlayKindIndexVsBaseline; got != want {
		t.Errorf("layers[0].Kind = %q, want %q (spec-order guarantee)", got, want)
	}
	if got, want := layers[1].Kind, types.OverlayKindDeltaVsBaseline; got != want {
		t.Errorf("layers[1].Kind = %q, want %q (spec-order guarantee)", got, want)
	}
	// Names must echo verbatim — the spec slot's Name was populated, not
	// the synthesised default.
	if got, want := layers[0].Name, "idx_baseline"; got != want {
		t.Errorf("layers[0].Name = %q, want %q", got, want)
	}
	if got, want := layers[1].Name, "delta_baseline"; got != want {
		t.Errorf("layers[1].Name = %q, want %q", got, want)
	}

	// Sanity-check the math at the baseline ordinal so a future refactor
	// that accidentally swaps the per-slot handler dispatch is caught
	// here too: INDEX_VS_BASELINE at Position 0 reads 100 (self-vs-self
	// ratio scaled to 100); DELTA_VS_BASELINE at Position 0 reads 0.0
	// (self-vs-self difference). The two values are structurally
	// distinct so a swap would fail this assertion.
	const tol = 1e-9
	assertSeriesEntryStatisticWithinTol(t, &layers[0], 0, 100.0, tol)
	assertSeriesEntryStatisticWithinTol(t, &layers[1], 0, 0.0, tol)
}

// TestOverlay_ZScoreVsRolling_WelfordParity sweeps a 20-point series
// with window=5 and asserts the rolling-window sample SD output of the
// online Welford-Pébaÿ carrier (rollingCarrier in
// processing/overlay_index_vs_rolling_mean.go) matches a two-pass naive
// reference implementation
// (`sqrt(sum((x-mean)^2)/(n-1))`) within 1e-12 ULP at every window
// frontier.
//
// The per-kind suite (TestOverlay_ZScoreVsRolling_Basic_Window5 and
// TestOverlay_ZScoreVsRolling_SampleSDDocumentedContrast) pins specific
// window-state arithmetic at individual indices; this test sweeps every
// emitted z-score over the full 20-point sequence so a future refactor
// that introduces a Welford-arithmetic bug at any window frontier (not
// just the indices the spot-check tests pin) is caught here. Mirrors
// the cohort-analytics convention of separating arithmetic-spot-pin
// from naive-sweep verification: the spot tests catch hand-rolled
// regressions, the sweep catches accumulator drift.
//
// Reference computation: at each index `i >= 2` walk the most-recent
// up-to-W present priors (the "carrier state" at the divide step),
// compute their two-pass mean and `sum((x-mean)^2) / (n-1)` sample
// variance, and assert the handler's z-score equals
// `(values[i] - reference_mean) / sqrt(reference_var)` within 1e-12 ULP.
// Both the carrier-driven and naive paths feed the same input slice so
// the comparison isolates the arithmetic — any divergence localises to
// the Welford recurrence.
func TestOverlay_ZScoreVsRolling_WelfordParity(t *testing.T) {
	const window = 5
	const n = 20

	// Mixed-magnitude series — not arithmetic so the rolling mean and
	// variance move at every step (a constant or strictly-monotonic
	// series would let an incorrect Welford recurrence pass by accident).
	values := []float64{
		1.0, 4.0, 9.0, 16.0, 25.0,
		36.0, 49.0, 64.0, 81.0, 100.0,
		121.0, 144.0, 169.0, 196.0, 225.0,
		256.0, 289.0, 324.0, 361.0, 400.0,
	}
	keys := make([]types.AxisKey, n)
	for i := 0; i < n; i++ {
		keys[i] = types.AxisKey{i}
	}

	host := newStubSeriesHost(keys, values)
	specs := []types.OverlaySpec{newZScoreVsRollingSpec("zscore_rolling", window)}
	layers, warnings, err := ApplyOverlaysSeries(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlaysSeries: %v", err)
	}
	assertNoOverlayWarnings(t, warnings)

	entries := layers[0].Payload.Series.Entries
	if len(entries) != n {
		t.Fatalf("len(Entries) = %d, want %d", len(entries), n)
	}

	const ulpTol = 1e-12

	for i := 0; i < n; i++ {
		// At divide step `i` the carrier holds the previous
		// `min(i, window)` priors. count < 2 ⇒ NaN.
		var prior []float64
		if i > 0 {
			start := 0
			if i > window {
				start = i - window
			}
			prior = values[start:i]
		}

		gotStat := entries[i].Summary.Statistic
		if gotStat == nil {
			t.Fatalf("entries[%d].Summary.Statistic nil; want populated", i)
		}

		if len(prior) < 2 {
			if !math.IsNaN(*gotStat) {
				t.Fatalf("entries[%d].Summary.Statistic = %v, want NaN (carrier count=%d < 2)",
					i, *gotStat, len(prior))
			}
			continue
		}

		// Two-pass naive reference: mean then sum-of-squared-residuals.
		var sum float64
		for _, v := range prior {
			sum += v
		}
		mean := sum / float64(len(prior))
		var ssr float64
		for _, v := range prior {
			d := v - mean
			ssr += d * d
		}
		variance := ssr / float64(len(prior)-1)
		sd := math.Sqrt(variance)
		if sd == 0 {
			// Constant prior window — the carrier emits NaN +
			// PULSE_OVERLAY_REF_ZERO. This series intentionally avoids
			// constant windows so the path should not fire; assert and
			// move on.
			t.Fatalf("entries[%d]: naive reference sd=0 unexpectedly (prior=%v); test fixture should avoid this branch",
				i, prior)
		}
		wantZ := (values[i] - mean) / sd

		if diff := *gotStat - wantZ; diff > ulpTol || diff < -ulpTol {
			t.Fatalf("entries[%d].Summary.Statistic = %.18g, want %.18g (Welford-vs-naive divergence; |Δ|=%.3g, tol=%.3g)",
				i, *gotStat, wantZ, math.Abs(diff), ulpTol)
		}
	}
}

// TestOverlay_YoY_AnnualMonthlyWeekly is a single table-driven test
// across the three lower-resolution YoY frequencies (annual / monthly /
// weekly). Each row pairs a frequency arm with a synthesised
// multi-period series and the expected per-period output. Per the story
// brief: this is the consolidated regression check for the YoY math —
// the per-frequency tests in overlay_yoy_test.go pin specific
// fixture-value outputs (Test_YoY_AnnualBasic, _MonthlyBasic,
// _WeeklyBasic), this test asserts every coarse-frequency arm
// dispatches end-to-end in one shot so a future refactor cannot quietly
// break one arm while the others pass.
//
// Fixture pattern: each frequency runs a doubling-pattern series where
// every period in the prior year is some constant `p` and every period
// in the current year is `2p`. The prior-year arms (annual/monthly/
// weekly) all use ordinal arithmetic `i - <stride>`, so every
// current-year period reads 200 (doubled vs the same period one year
// prior).
//
//   - annual:  stride=1.   2-year series [10, 20].
//     NaN for year 0; year 1 = 200.
//   - monthly: stride=12. 24-month series [100, 100, ..., 200, 200].
//     NaN for months 0..11; months 12..23 = 200.
//   - weekly:  stride=52. 104-week series [10, 10, ..., 15, 15].
//     NaN for weeks 0..51; weeks 52..103 = 150.
//
// Daily / hourly frequency arms use a separate exact-key arithmetic
// path (TestOverlay_YoY_DailyBasic + TestOverlay_YoY_HourlyExactArithmetic);
// this test covers only the coarse-frequency table.
func TestOverlay_YoY_AnnualMonthlyWeekly(t *testing.T) {
	type freqCase struct {
		freq       string
		buildKeys  func() []types.AxisKey
		buildVals  func() []float64
		nanThrough int     // entries [0..nanThrough) emit NaN
		wantValue  float64 // entries [nanThrough..N) emit this value
	}

	cases := []freqCase{
		{
			freq: "annual",
			buildKeys: func() []types.AxisKey {
				return []types.AxisKey{{"2020"}, {"2021"}}
			},
			buildVals: func() []float64 {
				return []float64{10.0, 20.0}
			},
			nanThrough: 1,
			wantValue:  200.0,
		},
		{
			freq: "monthly",
			buildKeys: func() []types.AxisKey {
				keys := make([]types.AxisKey, 24)
				for i := 0; i < 24; i++ {
					keys[i] = types.AxisKey{i}
				}
				return keys
			},
			buildVals: func() []float64 {
				vals := make([]float64, 24)
				for i := 0; i < 24; i++ {
					if i < 12 {
						vals[i] = 100.0
					} else {
						vals[i] = 200.0
					}
				}
				return vals
			},
			nanThrough: 12,
			wantValue:  200.0,
		},
		{
			freq: "weekly",
			buildKeys: func() []types.AxisKey {
				keys := make([]types.AxisKey, 104)
				for i := 0; i < 104; i++ {
					keys[i] = types.AxisKey{i}
				}
				return keys
			},
			buildVals: func() []float64 {
				vals := make([]float64, 104)
				for i := 0; i < 104; i++ {
					if i < 52 {
						vals[i] = 10.0
					} else {
						vals[i] = 15.0
					}
				}
				return vals
			},
			nanThrough: 52,
			wantValue:  150.0, // 15/10 * 100
		},
	}

	const tol = 1e-9
	for _, tc := range cases {
		t.Run(tc.freq, func(t *testing.T) {
			keys := tc.buildKeys()
			vals := tc.buildVals()
			host := newStubYoYHost(keys, vals)
			specs := []types.OverlaySpec{newYoYSpec("yoy_"+tc.freq, tc.freq)}

			layers, warnings, err := ApplyOverlaysSeries(specs, host)
			if err != nil {
				t.Fatalf("ApplyOverlaysSeries(freq=%s): %v", tc.freq, err)
			}
			assertNoOverlayWarnings(t, warnings)
			if len(layers) != 1 {
				t.Fatalf("expected 1 layer, got %d", len(layers))
			}

			entries := layers[0].Payload.Series.Entries
			if len(entries) != len(keys) {
				t.Fatalf("len(Entries) = %d, want %d (freq=%s)",
					len(entries), len(keys), tc.freq)
			}

			// First nanThrough entries: NaN (no prior-year period).
			for i := 0; i < tc.nanThrough; i++ {
				if entries[i].Summary.Statistic == nil {
					t.Fatalf("entries[%d].Summary.Statistic nil (freq=%s); want NaN",
						i, tc.freq)
				}
				if !math.IsNaN(*entries[i].Summary.Statistic) {
					t.Fatalf("entries[%d].Summary.Statistic = %v (freq=%s); want NaN",
						i, *entries[i].Summary.Statistic, tc.freq)
				}
			}
			// Remaining entries: tc.wantValue.
			for i := tc.nanThrough; i < len(keys); i++ {
				assertSeriesEntryStatisticWithinTol(t, &layers[0], i, tc.wantValue, tol)
			}
		})
	}
}

// TestOverlay_YoY_HourlyExactArithmetic pins the 365*24h hourly stride
// across a multi-day hourly series. Each prior-year hour must resolve
// to its exact-key match in the host; the handler reads the host's
// value at the matching key and emits the YoY ratio.
//
// The per-kind TestOverlay_YoY_HourlyBasic test covers a minimal
// 4-key fixture (2 hours in 2019, 2 hours in 2020). This test walks
// 8760+24 hours covering all of 2019 plus the first 24 hours of 2020,
// so every hour in 2020-01-01 has an exact-key match in 2019-01-01.
// The values are constructed so every 2020 hour is exactly 2x its
// 2019 counterpart — the YoY ratio is always 200 across the entire
// first day of 2020 — and we verify a representative sample of the
// 24 hourly indices (full sweep would be 8784 fp comparisons; we pin
// the boundaries + a few midpoints which catches any off-by-one in
// the exact-key arithmetic).
func TestOverlay_YoY_HourlyExactArithmetic(t *testing.T) {
	// Build 8760 keys for 2019-01-01T00 through 2019-12-31T23, plus 24
	// keys for 2020-01-01T00 through 2020-01-01T23.
	const hours2019 = 8760
	const hours2020 = 24
	total := hours2019 + hours2020

	keys := make([]types.AxisKey, total)
	vals := make([]float64, total)
	// 2019: hour i carries value (i + 1). 2020-01-01: hour j (j in 0..23)
	// carries value 2 * (j + 1) — exactly 2x the 2019-01-01 hour j value.
	// The YoY ratio for every 2020-01-01 hour is therefore 200.
	//
	// Generate timestamps via time.Date — Go's canonical day-of-year
	// constructor normalises month/day overflow so the handler's
	// time.Parse round-trip lands on the same UTC moment.
	const layout = "2006-01-02T15"
	keyFor := func(year, dayOfYear, hourOfDay int) string {
		return time.Date(year, time.January, dayOfYear, hourOfDay, 0, 0, 0, time.UTC).Format(layout)
	}

	idx := 0
	for d := 1; d <= 365; d++ {
		for h := 0; h < 24; h++ {
			keys[idx] = types.AxisKey{keyFor(2019, d, h)}
			vals[idx] = float64(idx + 1)
			idx++
		}
	}
	if idx != hours2019 {
		t.Fatalf("test fixture build error: 2019 idx=%d, want %d", idx, hours2019)
	}
	for h := 0; h < hours2020; h++ {
		keys[idx] = types.AxisKey{keyFor(2020, 1, h)}
		// 2020-01-01 hour h reads exactly 2x the 2019-01-01 hour h value.
		// 2019-01-01 hour h was at fixture index h, with value (h + 1),
		// so the YoY ratio is `2*(h+1) / (h+1) * 100 = 200`.
		vals[idx] = 2.0 * float64(h+1)
		idx++
	}

	host := newStubYoYHost(keys, vals)
	specs := []types.OverlaySpec{newYoYSpec("yoy_hourly", "hourly")}
	layers, warnings, err := ApplyOverlaysSeries(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlaysSeries: %v", err)
	}
	assertNoOverlayWarnings(t, warnings)

	entries := layers[0].Payload.Series.Entries
	if len(entries) != total {
		t.Fatalf("len(Entries) = %d, want %d", len(entries), total)
	}

	// Every 2019 hour (indices 0..hours2019-1) reads NaN — no prior-year
	// match exists in 2018.
	for i := 0; i < hours2019; i++ {
		if entries[i].Summary.Statistic == nil {
			t.Fatalf("entries[%d] (2019 hour) Statistic nil; want NaN", i)
		}
		if !math.IsNaN(*entries[i].Summary.Statistic) {
			t.Fatalf("entries[%d] (2019 hour) = %v; want NaN (no 2018 prior)",
				i, *entries[i].Summary.Statistic)
		}
	}

	// Every 2020-01-01 hour reads 200.0 (2x prior-year value).
	const want = 200.0
	const tol = 1e-9
	// Pin the boundaries (first + last 2020 hour) plus a couple
	// midpoints to catch any off-by-one in the exact-key arithmetic.
	checkIndices := []int{
		hours2019,      // 2020-01-01T00 (first 2020 hour)
		hours2019 + 1,  // 2020-01-01T01
		hours2019 + 11, // 2020-01-01T11 (midday)
		hours2019 + 12, // 2020-01-01T12
		hours2019 + 22, // 2020-01-01T22
		hours2019 + 23, // 2020-01-01T23 (last 2020 hour)
	}
	for _, i := range checkIndices {
		assertSeriesEntryStatisticWithinTol(t, &layers[0], i, want, tol)
	}
}

// TestOverlay_E4SeriesHandlers_ShapeContract is the property assertion
// the brief calls out for the 6 E4 SERIES handlers. Every handler:
//
//  1. Returns a SERIES-shape OverlayLayer (Payload.Shape == OverlayShapeSeries).
//  2. Series.Entries length equals host.GroupCount() (FR-A2: loose
//     alignment — every host ordinal gets exactly one entry slot).
//  3. Preserves the host's group-key order in the entries slice
//     (entries[i].Key == host.GroupKey(i)).
//  4. Synthesises the documented per-kind default Name when Spec.Name
//     is empty.
//
// Catches future refactors that accidentally diverge any of the six
// E4 handlers from the canonical SERIES-host contract. Buffered or
// streamable does not matter here — the property holds at the handler
// emission point regardless of how the orchestrator dispatches.
//
// Per-kind table threads minimal happy-path fixtures (3-key host, no
// gates) so the test stays focused on the shape contract — the
// per-kind suites pin specific cell-value math.
func TestOverlay_E4SeriesHandlers_ShapeContract(t *testing.T) {
	type kindCase struct {
		name      string
		newSpec   func() types.OverlaySpec // spec with Name unset so default-name path fires
		newHost   func() *SeriesHostView   // host shape the handler expects
		wantName  string                   // synthesised default Name
		wantCount int                      // expected entry count == host.GroupCount()
	}

	threeKeys := []types.AxisKey{{"a"}, {"b"}, {"c"}}
	threeValues := []float64{10.0, 20.0, 30.0}

	cases := []kindCase{
		{
			name: "OVERLAY_INDEX_VS_BASELINE",
			newSpec: func() types.OverlaySpec {
				// No Name — exercises the default-name synthesiser.
				return types.OverlaySpec{
					Kind:  types.OverlayKindIndexVsBaseline,
					Scope: types.OverlayScopeGroup,
					Ref: types.OverlayRef{
						BaselineIndex: &types.OverlayBaselineIndexRef{Position: 0},
					},
				}
			},
			newHost:   func() *SeriesHostView { return newStubSeriesHost(threeKeys, threeValues) },
			wantName:  "index_vs_baseline",
			wantCount: 3,
		},
		{
			name: "OVERLAY_DELTA_VS_BASELINE",
			newSpec: func() types.OverlaySpec {
				return types.OverlaySpec{
					Kind:  types.OverlayKindDeltaVsBaseline,
					Scope: types.OverlayScopeGroup,
					Ref: types.OverlayRef{
						BaselineIndex: &types.OverlayBaselineIndexRef{Position: 0},
					},
				}
			},
			newHost:   func() *SeriesHostView { return newStubSeriesHost(threeKeys, threeValues) },
			wantName:  "delta_vs_baseline",
			wantCount: 3,
		},
		{
			name: "OVERLAY_INDEX_VS_PRIOR",
			newSpec: func() types.OverlaySpec {
				return types.OverlaySpec{
					Kind:  types.OverlayKindIndexVsPrior,
					Scope: types.OverlayScopeGroup,
				}
			},
			newHost:   func() *SeriesHostView { return newStubSeriesHost(threeKeys, threeValues) },
			wantName:  "index_vs_prior",
			wantCount: 3,
		},
		{
			name: "OVERLAY_INDEX_VS_ROLLING_MEAN",
			newSpec: func() types.OverlaySpec {
				params, _ := json.Marshal(map[string]any{"window": 2})
				return types.OverlaySpec{
					Kind:  types.OverlayKindIndexVsRollingMean,
					Scope: types.OverlayScopeGroup,
					Ref: types.OverlayRef{
						RollingMean: &types.OverlayRollingMeanRef{},
					},
					Params: params,
				}
			},
			newHost:   func() *SeriesHostView { return newStubSeriesHost(threeKeys, threeValues) },
			wantName:  "index_vs_rolling_mean",
			wantCount: 3,
		},
		{
			name: "OVERLAY_ZSCORE_VS_ROLLING",
			newSpec: func() types.OverlaySpec {
				params, _ := json.Marshal(map[string]any{"window": 2})
				return types.OverlaySpec{
					Kind:  types.OverlayKindZScoreVsRolling,
					Scope: types.OverlayScopeGroup,
					Ref: types.OverlayRef{
						RollingMean: &types.OverlayRollingMeanRef{},
					},
					Params: params,
				}
			},
			newHost:   func() *SeriesHostView { return newStubSeriesHost(threeKeys, threeValues) },
			wantName:  "zscore_vs_rolling",
			wantCount: 3,
		},
		{
			name: "OVERLAY_YOY",
			newSpec: func() types.OverlaySpec {
				params, _ := json.Marshal(map[string]any{"frequency": "annual"})
				return types.OverlaySpec{
					Kind:  types.OverlayKindYoY,
					Scope: types.OverlayScopeGroup,
					Ref: types.OverlayRef{
						YoY: &types.OverlayYoYRef{},
					},
					Params: params,
				}
			},
			// YoY needs a GROUP_DATE-introspecting host — use the YoY
			// stub host helper from the per-kind suite.
			newHost: func() *SeriesHostView {
				yoyKeys := []types.AxisKey{{"2020"}, {"2021"}, {"2022"}}
				yoyValues := []float64{10.0, 20.0, 30.0}
				return newStubYoYHost(yoyKeys, yoyValues)
			},
			wantName:  "yoy",
			wantCount: 3,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec := tc.newSpec()
			host := tc.newHost()
			specs := []types.OverlaySpec{spec}

			layers, _, err := ApplyOverlaysSeries(specs, host)
			if err != nil {
				t.Fatalf("ApplyOverlaysSeries: %v", err)
			}
			if len(layers) != 1 {
				t.Fatalf("len(layers) = %d, want 1", len(layers))
			}
			layer := layers[0]

			// (1) SERIES-shape payload.
			if layer.Payload.Shape != types.OverlayShapeSeries {
				t.Fatalf("layer.Payload.Shape = %q, want %q",
					layer.Payload.Shape, types.OverlayShapeSeries)
			}
			if layer.Payload.Series == nil {
				t.Fatalf("layer.Payload.Series is nil; want non-nil SeriesPayload")
			}

			// (2) Entries length == host.GroupCount().
			entries := layer.Payload.Series.Entries
			if len(entries) != tc.wantCount {
				t.Fatalf("len(entries) = %d, want %d (host.GroupCount mismatch)",
					len(entries), tc.wantCount)
			}

			// (3) Group-key order preserved element-for-element.
			for i := 0; i < tc.wantCount; i++ {
				wantKey, ok := host.GroupKey(i)
				if !ok {
					t.Fatalf("host.GroupKey(%d) returned !ok; fixture inconsistency", i)
				}
				gotKey := entries[i].Key
				if len(gotKey) != len(wantKey) {
					t.Fatalf("entries[%d].Key len = %d, want %d",
						i, len(gotKey), len(wantKey))
				}
				for j := range wantKey {
					if gotKey[j] != wantKey[j] {
						t.Errorf("entries[%d].Key[%d] = %v, want %v",
							i, j, gotKey[j], wantKey[j])
					}
				}
			}

			// (4) Synthesised default Name.
			if got, want := layer.Name, tc.wantName; got != want {
				t.Errorf("layer.Name = %q, want %q (default-name synthesis)",
					got, want)
			}
		})
	}
}

// Note: newDeltaVsBaselineSpec is provided by the per-kind suite in
// overlay_delta_vs_baseline_test.go; reused here as-is for the
// TestOverlay_IndexVsBaseline_SpecOrderStable pair fixture.

// Compile-time defense: bind errors.PULSE_OVERLAY_YOY_FREQUENCY_MISSING
// so the errors package import stays live for future failure-path
// assertions in this file (the gap-fill tests above use NaN-presence
// + warning-code assertions imported from overlay_test_helpers_test.go,
// but a future regression on the missing-frequency arm will land here
// alongside the rest of the E4-S9 batch).
var _ = errors.PULSE_OVERLAY_YOY_FREQUENCY_MISSING
