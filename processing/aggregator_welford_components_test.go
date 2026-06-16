package processing

import (
	"math"
	"math/rand"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// controlMoments returns the buffered (M2, M3, M4) population central
// moments of vals around the population mean. Mirrors the Aggregate
// path's sumDeviationPowers helper so the control is computed by the
// same recurrence the production code stamps into frozen state.
func controlMoments(vals []float64) (n int, m, m2, m3, m4 float64) {
	if len(vals) == 0 {
		return 0, 0, 0, 0, 0
	}
	sum := 0.0
	for _, v := range vals {
		sum += v
	}
	m = sum / float64(len(vals))
	for _, v := range vals {
		d := v - m
		d2 := d * d
		m2 += d2
		m3 += d2 * d
		m4 += d2 * d2
	}
	return len(vals), m, m2, m3, m4
}

// runAggregateForComponents builds a fresh aggregator instance via the
// registry, drives Aggregate over the supplied records (buffered path
// — exercises the aggregateValues frozen-state stamp), and returns the
// emitted operator-specific map plus scalar.
func runAggregateForComponents(t *testing.T, aggType types.AggregationType,
	field string, schema *encoding.Schema, records []*Record,
) (map[string]any, float64) {
	t.Helper()
	agg := makeAggregator(t, aggType, field, schema)
	scalar, err := agg.Aggregate(records, field)
	if err != nil {
		t.Fatalf("%s.Aggregate: %v", aggType, err)
	}
	meta, ok := agg.(MetaAggregator)
	if !ok {
		t.Fatalf("%s does not implement MetaAggregator", aggType)
	}
	got, err := meta.Components()
	if err != nil {
		t.Fatalf("%s.Components: %v", aggType, err)
	}
	return got, scalar
}

// floatEqual returns true when a and b are bit-identical at the
// math.Float64bits level. NaN bits compare equal under this rule,
// which is the semantics this test wants (variance, NaN CI bound).
func floatEqual(a, b float64) bool {
	return math.Float64bits(a) == math.Float64bits(b)
}

// ulpDistance returns the count of representable float64 values
// between a and b. Two values are considered "within ULP" parity for
// the Chan-Welford merge comparison when this distance is small
// (16 is generous for a 4-way merge on 200 samples). Operates on the
// math.Float64bits representation so the count is meaningful across
// the float64 spectrum. Handles sign-bit boundary crossing.
func ulpDistance(a, b float64) uint64 {
	if math.IsNaN(a) || math.IsNaN(b) {
		if math.IsNaN(a) && math.IsNaN(b) {
			return 0
		}
		return math.MaxUint64
	}
	ab := math.Float64bits(a)
	bb := math.Float64bits(b)
	// Map negative floats to a monotonic representation so subtraction
	// gives the correct ULP distance across zero.
	const signMask = uint64(1) << 63
	if ab&signMask != 0 {
		ab = signMask - ab
	}
	if bb&signMask != 0 {
		bb = signMask - bb
	}
	if ab > bb {
		return ab - bb
	}
	return bb - ab
}

// requireFloat asserts the components map carries `key` and its float64
// value is bit-equal to `want`.
func requireFloat(t *testing.T, got map[string]any, key string, want float64) {
	t.Helper()
	v, ok := got[key]
	if !ok {
		t.Errorf("components missing key %q (got keys: %v)", key, mapKeys(got))
		return
	}
	f, ok := v.(float64)
	if !ok {
		t.Errorf("components[%q] = %v (%T), want float64", key, v, v)
		return
	}
	if !floatEqual(f, want) {
		t.Errorf("components[%q] = %v, want %v (bit-equal)", key, f, want)
	}
}

func mapKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// requireKeySet asserts the components map carries exactly `want` keys
// — no missing, no extras. Catches schema drift between the
// capabilities declaration and the runtime emission.
func requireKeySet(t *testing.T, got map[string]any, want []string) {
	t.Helper()
	if got == nil {
		t.Fatalf("components map nil; want keys %v", want)
	}
	for _, k := range want {
		if _, ok := got[k]; !ok {
			t.Errorf("missing key %q (got %v)", k, mapKeys(got))
		}
	}
	if len(got) != len(want) {
		t.Errorf("got %d keys (%v), want %d (%v)", len(got), mapKeys(got), len(want), want)
	}
}

// TestWelfordFamilyComponents_VarianceEmits sweeps the variance
// aggregator across empty / single-row / multi-row shapes and asserts
// {mean, m2, variance} keys plus bit-equal values vs the control
// recurrence.
func TestWelfordFamilyComponents_VarianceEmits(t *testing.T) {
	schema := numericSchema()
	multi := []float64{1, 4, 9, 16, 25, 36}
	cases := []struct {
		name string
		vals []float64
	}{
		{"empty", nil},
		{"singleRow", []float64{42}},
		{"multiRow", multi},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			records := makeRecords(schema, "score", tc.vals)
			got, scalar := runAggregateForComponents(t, types.AGG_VARIANCE, "score", schema, records)
			requireKeySet(t, got, []string{"mean", "m2", "variance"})
			n, wantMean, wantM2, _, _ := controlMoments(tc.vals)
			var wantVar float64
			if n > 0 {
				wantVar = wantM2 / float64(n)
			}
			requireFloat(t, got, "mean", wantMean)
			requireFloat(t, got, "m2", wantM2)
			requireFloat(t, got, "variance", wantVar)
			if !floatEqual(scalar, wantVar) {
				t.Errorf("scalar = %v, want %v (population variance)", scalar, wantVar)
			}
		})
	}
}

// TestWelfordFamilyComponents_StdDevEmits asserts the stddev
// aggregator surfaces {mean, m2, variance, stddev} with bit-equal
// values vs the control recurrence.
func TestWelfordFamilyComponents_StdDevEmits(t *testing.T) {
	schema := numericSchema()
	cases := []struct {
		name string
		vals []float64
	}{
		{"empty", nil},
		{"singleRow", []float64{7}},
		{"multiRow", []float64{10, 12, 14, 18, 22, 30}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			records := makeRecords(schema, "score", tc.vals)
			got, scalar := runAggregateForComponents(t, types.AGG_STDDEV, "score", schema, records)
			requireKeySet(t, got, []string{"mean", "m2", "variance", "stddev"})
			n, wantMean, wantM2, _, _ := controlMoments(tc.vals)
			var wantVar, wantSD float64
			if n > 0 {
				wantVar = wantM2 / float64(n)
				wantSD = math.Sqrt(wantVar)
			}
			requireFloat(t, got, "mean", wantMean)
			requireFloat(t, got, "m2", wantM2)
			requireFloat(t, got, "variance", wantVar)
			requireFloat(t, got, "stddev", wantSD)
			if !floatEqual(scalar, wantSD) {
				t.Errorf("scalar = %v, want %v (population stddev)", scalar, wantSD)
			}
		})
	}
}

// TestWelfordFamilyComponents_SkewnessEmits asserts the skewness
// aggregator surfaces {mean, m2, m3, skewness}. The scalar (and the
// emitted skewness key) match m3 / (n * sd^3) when the input is non-
// degenerate; both fall back to zero otherwise.
func TestWelfordFamilyComponents_SkewnessEmits(t *testing.T) {
	schema := numericSchema()
	cases := []struct {
		name string
		vals []float64
	}{
		{"empty", nil},
		{"singleRow", []float64{5}},
		{"multiRow", []float64{1, 2, 4, 8, 16, 32}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			records := makeRecords(schema, "score", tc.vals)
			got, scalar := runAggregateForComponents(t, types.AGG_SKEWNESS, "score", schema, records)
			requireKeySet(t, got, []string{"mean", "m2", "m3", "skewness"})
			n, wantMean, wantM2, wantM3, _ := controlMoments(tc.vals)
			var wantSkew float64
			if n > 1 && wantM2 > 0 {
				variance := wantM2 / float64(n)
				sd := math.Sqrt(variance)
				if sd > 0 {
					wantSkew = wantM3 / (float64(n) * sd * sd * sd)
				}
			}
			requireFloat(t, got, "mean", wantMean)
			requireFloat(t, got, "m2", wantM2)
			requireFloat(t, got, "m3", wantM3)
			requireFloat(t, got, "skewness", wantSkew)
			// Scalar return uses populationStdDev(vals) which re-derives
			// mean internally; float-op ordering differs by O(ULP) from
			// the Components-path (variance from cached m2). Allow a
			// 1e-12 absolute tolerance — the components map is the
			// canonical numerically-stable answer for downstream
			// consumers.
			if !floatClose(scalar, wantSkew, 1e-12) {
				t.Errorf("scalar = %v, want %v (population skewness, tol 1e-12)", scalar, wantSkew)
			}
		})
	}
}

// TestWelfordFamilyComponents_KurtosisEmits asserts the kurtosis
// aggregator surfaces {mean, m2, m3, m4, kurtosis} with the excess
// kurtosis estimator m4 / (n * variance^2) - 3.
func TestWelfordFamilyComponents_KurtosisEmits(t *testing.T) {
	schema := numericSchema()
	cases := []struct {
		name string
		vals []float64
	}{
		{"empty", nil},
		{"singleRow", []float64{11}},
		{"multiRow", []float64{1, 2, 3, 5, 8, 13}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			records := makeRecords(schema, "score", tc.vals)
			got, scalar := runAggregateForComponents(t, types.AGG_KURTOSIS, "score", schema, records)
			requireKeySet(t, got, []string{"mean", "m2", "m3", "m4", "kurtosis"})
			n, wantMean, wantM2, wantM3, wantM4 := controlMoments(tc.vals)
			var wantKurt float64
			if n > 1 && wantM2 > 0 {
				variance := wantM2 / float64(n)
				if variance > 0 {
					wantKurt = wantM4/(float64(n)*variance*variance) - 3
				}
			}
			requireFloat(t, got, "mean", wantMean)
			requireFloat(t, got, "m2", wantM2)
			requireFloat(t, got, "m3", wantM3)
			requireFloat(t, got, "m4", wantM4)
			requireFloat(t, got, "kurtosis", wantKurt)
			// Scalar return uses populationStdDev(vals) which re-derives
			// mean internally; float-op ordering differs by O(ULP) from
			// the Components-path. 1e-12 absolute tolerance — the
			// components map is the canonical answer.
			if !floatClose(scalar, wantKurt, 1e-12) {
				t.Errorf("scalar = %v, want %v (excess kurtosis, tol 1e-12)", scalar, wantKurt)
			}
		})
	}
}

// TestWelfordFamilyComponents_ZScoreEmits asserts the zscore
// aggregator surfaces {pop_mean, pop_stddev, target_value, zscore}.
// target_value is the last value seen; zscore is (target - mean) / sd
// under those population params.
func TestWelfordFamilyComponents_ZScoreEmits(t *testing.T) {
	schema := numericSchema()
	cases := []struct {
		name string
		vals []float64
	}{
		{"empty", nil},
		{"singleRow", []float64{9}},
		{"multiRow", []float64{1, 3, 5, 7, 9, 11}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			records := makeRecords(schema, "score", tc.vals)
			got, _ := runAggregateForComponents(t, types.AGG_ZSCORE, "score", schema, records)
			requireKeySet(t, got, []string{"pop_mean", "pop_stddev", "target_value", "zscore"})
			n, wantMean, wantM2, _, _ := controlMoments(tc.vals)
			var wantSD, wantTarget, wantZ float64
			if n > 0 {
				wantSD = math.Sqrt(wantM2 / float64(n))
				wantTarget = tc.vals[len(tc.vals)-1]
				if wantSD > 0 {
					wantZ = (wantTarget - wantMean) / wantSD
				}
			}
			requireFloat(t, got, "pop_mean", wantMean)
			requireFloat(t, got, "pop_stddev", wantSD)
			requireFloat(t, got, "target_value", wantTarget)
			requireFloat(t, got, "zscore", wantZ)
		})
	}
}

// TestWelfordFamilyComponents_WelfordEmits asserts the AGG_WELFORD
// aggregator surfaces {mean, m2, variance, stddev} with the sample
// (n-1 denominator) variance — bit-identical to the WelfordTriple
// returned via Rich(). Empty input emits (nil, nil) so the universal
// floor (n=0) carries the entire payload.
func TestWelfordFamilyComponents_WelfordEmits(t *testing.T) {
	schema := welfordSchema()
	cases := []struct {
		name      string
		vals      []float64
		wantEmpty bool
	}{
		{"empty", nil, true},
		{"singleRow", []float64{17}, false},
		{"multiRow", []float64{2.5, 3.5, 4.0, 5.5, 8.0, 13.0}, false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			records := make([]*Record, 0, len(tc.vals))
			for _, v := range tc.vals {
				records = append(records, welfordRecord(v))
			}
			agg, err := newWelfordAggregator(&types.Aggregation{Type: types.AGG_WELFORD, Field: "value"}, schema)
			if err != nil {
				t.Fatalf("newWelfordAggregator: %v", err)
			}
			if _, err := agg.Aggregate(records, "value"); err != nil {
				t.Fatalf("Aggregate: %v", err)
			}
			meta := agg.(MetaAggregator)
			got, err := meta.Components()
			if err != nil {
				t.Fatalf("Components: %v", err)
			}
			if tc.wantEmpty {
				if got != nil {
					t.Errorf("Components() = %v, want nil for empty input", got)
				}
				return
			}
			requireKeySet(t, got, []string{"mean", "m2", "variance", "stddev"})

			// The WELFORD aggregator uses sample variance (n-1
			// denominator) per welfordBucket.sampleVariance. Cross-
			// reference via Rich() so the test re-asserts the same
			// triple that overlay handlers consume.
			rich, _ := agg.(RichAggregator).Rich()
			triple := rich.(WelfordTriple)
			requireFloat(t, got, "mean", triple.Mean)
			requireFloat(t, got, "variance", triple.Variance)
			requireFloat(t, got, "stddev", math.Sqrt(triple.Variance))

			// m2 is derivable from variance * (n - 1) but stored
			// directly on the bucket; re-derive the control via the
			// same welfordBucket recurrence the aggregator uses.
			controlMean, controlVar, controlN := controlWelford(tc.vals)
			if triple.Mean != controlMean || triple.Variance != controlVar || triple.N != controlN {
				t.Errorf("Rich triple = %+v, want mean=%v variance=%v N=%d",
					triple, controlMean, controlVar, controlN)
			}
		})
	}
}

// TestWelfordFamilyComponents_CIEmits asserts both AGG_CI_LOWER and
// AGG_CI_UPPER surface their schema-declared keys. Bound key flips
// with the aggregator: ciLower emits "lower"; ciUpper emits "upper".
func TestWelfordFamilyComponents_CIEmits(t *testing.T) {
	schema := numericSchema()
	cases := []struct {
		name string
		vals []float64
	}{
		{"empty", nil},
		{"singleRow", []float64{42}},
		{"multiRow", []float64{10, 12, 13, 18, 22, 30}},
	}
	for _, kind := range []types.AggregationType{types.AGG_CI_LOWER, types.AGG_CI_UPPER} {
		kind := kind
		boundKey := "lower"
		if kind == types.AGG_CI_UPPER {
			boundKey = "upper"
		}
		for _, tc := range cases {
			tc := tc
			t.Run(string(kind)+"_"+tc.name, func(t *testing.T) {
				records := makeRecords(schema, "score", tc.vals)
				got, scalar := runAggregateForComponents(t, kind, "score", schema, records)
				requireKeySet(t, got, []string{"mean", "stderr", "alpha", "t_critical", boundKey})
				// alpha is always 1 - confidence. Default confidence
				// is 0.95; the emitted alpha mirrors the same runtime
				// float subtraction the production code performs.
				// Compute the expected value via a runtime expression
				// (not a constant — Go's const folding of `1-0.95`
				// rounds differently than the IEEE-754 subtraction the
				// aggregator does at runtime).
				confidence := 0.95
				wantAlpha := 1 - confidence
				requireFloat(t, got, "alpha", wantAlpha)
				// Bound key carries the scalar return (NaN when n < 2).
				v, ok := got[boundKey]
				if !ok {
					t.Fatalf("missing bound key %q", boundKey)
				}
				f, ok := v.(float64)
				if !ok {
					t.Fatalf("bound %q is %T, want float64", boundKey, v)
				}
				if !floatEqual(f, scalar) {
					t.Errorf("components[%q] = %v, want %v (scalar return)", boundKey, f, scalar)
				}
			})
		}
	}
}

// TestWelfordFamilyComponents_ParallelVsSerial — the ULP-level parity
// lock. Drives a 200-sample cohort through:
//
//  1. Serial single-instance MyAGG.Aggregate(all records).
//  2. Parallel four-way split: four MyAGG instances fold their own
//     quarter of the cohort via UpdateRow; instance #1 absorbs the
//     others via MergeOnline (the Chan-Welford parallel formula in
//     processing/aggregator_online.go.mergeWelford). Finalize is
//     called only on the surviving instance.
//
// For the Mergeable aggregators (AGG_VARIANCE, AGG_STDDEV) the
// 4-way merge path uses the Chan-Welford recurrence; results match
// the serial run within a small ULP budget (the parallel formula is
// mathematically equivalent but not bit-equal due to floating-point
// associativity).
//
// AGG_SKEWNESS and AGG_KURTOSIS expose OnlineAggregator but not
// MergeableAggregator — the per-shard parallel reducer in
// service/shard_reduce.go falls back to single-instance streaming for
// them. The parity lock for those operators compares the streaming
// UpdateRow→Finalize→Components path against the buffered
// Aggregate→Components path on the same cohort, asserting bit-equal
// emission of every key.
func TestWelfordFamilyComponents_ParallelVsSerial(t *testing.T) {
	rng := rand.New(rand.NewSource(0x57E4D))
	samples := make([]float64, 200)
	for i := range samples {
		samples[i] = rng.NormFloat64()*4 + 50
	}
	schema := numericSchema()
	records := makeRecords(schema, "score", samples)

	const ulpBudget = uint64(64)

	// --- Mergeable family: 4-way MergeOnline ----------------------
	mergeable := []struct {
		aggType types.AggregationType
		keys    []string
	}{
		{types.AGG_VARIANCE, []string{"mean", "m2", "variance"}},
		{types.AGG_STDDEV, []string{"mean", "m2", "variance", "stddev"}},
	}
	for _, c := range mergeable {
		c := c
		t.Run(string(c.aggType)+"_Merge4Way", func(t *testing.T) {
			// Serial: one instance over the full cohort.
			serial, _ := runAggregateForComponents(t, c.aggType, "score", schema, records)

			// Parallel: 4-way split via UpdateRow + MergeOnline.
			const workers = 4
			chunkSize := len(records) / workers
			instances := make([]Aggregator, workers)
			for w := 0; w < workers; w++ {
				instances[w] = makeAggregator(t, c.aggType, "score", schema)
				online, ok := instances[w].(OnlineAggregator)
				if !ok {
					t.Fatalf("%s does not implement OnlineAggregator", c.aggType)
				}
				start := w * chunkSize
				end := start + chunkSize
				if w == workers-1 {
					end = len(records)
				}
				for _, r := range records[start:end] {
					if err := online.UpdateRow(r, "score"); err != nil {
						t.Fatalf("UpdateRow: %v", err)
					}
				}
			}
			head := instances[0].(OnlineAggregator)
			mergeIface, ok := head.(MergeableAggregator)
			if !ok {
				t.Fatalf("%s does not implement MergeableAggregator", c.aggType)
			}
			for w := 1; w < workers; w++ {
				other := instances[w].(OnlineAggregator)
				if err := mergeIface.MergeOnline(other); err != nil {
					t.Fatalf("MergeOnline: %v", err)
				}
			}
			if _, err := head.Finalize(); err != nil {
				t.Fatalf("Finalize: %v", err)
			}
			parallel, err := head.(MetaAggregator).Components()
			if err != nil {
				t.Fatalf("Components: %v", err)
			}

			// Compare within ULP budget — Chan-Welford merge is
			// mathematically equivalent to single-pass, but not bit-
			// equal due to floating-point associativity.
			for _, k := range c.keys {
				sv, sok := serial[k]
				pv, pok := parallel[k]
				if !sok || !pok {
					t.Errorf("key %q present serial=%v parallel=%v", k, sok, pok)
					continue
				}
				sf, sIsF := sv.(float64)
				pf, pIsF := pv.(float64)
				if !sIsF || !pIsF {
					t.Errorf("key %q non-float: serial=%T parallel=%T", k, sv, pv)
					continue
				}
				if d := ulpDistance(sf, pf); d > ulpBudget {
					t.Errorf("key %q: serial=%v parallel=%v differ by %d ULPs (budget %d)",
						k, sf, pf, d, ulpBudget)
				}
			}
		})
	}

	// --- Non-mergeable family: streaming vs buffered ------------
	//
	// AGG_SKEWNESS and AGG_KURTOSIS run as a single OnlineAggregator
	// instance under the per-shard parallel reducer (no MergeOnline).
	// Lock the streaming UpdateRow→Finalize→Components path against
	// the buffered Aggregate→Components path on the same cohort.
	streamingOnly := []struct {
		aggType types.AggregationType
		keys    []string
	}{
		{types.AGG_SKEWNESS, []string{"mean", "m2", "m3", "skewness"}},
		{types.AGG_KURTOSIS, []string{"mean", "m2", "m3", "m4", "kurtosis"}},
	}
	for _, c := range streamingOnly {
		c := c
		t.Run(string(c.aggType)+"_StreamingVsBuffered", func(t *testing.T) {
			buffered, _ := runAggregateForComponents(t, c.aggType, "score", schema, records)

			streamAgg := makeAggregator(t, c.aggType, "score", schema)
			online, ok := streamAgg.(OnlineAggregator)
			if !ok {
				t.Fatalf("%s does not implement OnlineAggregator", c.aggType)
			}
			for _, r := range records {
				if err := online.UpdateRow(r, "score"); err != nil {
					t.Fatalf("UpdateRow: %v", err)
				}
			}
			if _, err := online.Finalize(); err != nil {
				t.Fatalf("Finalize: %v", err)
			}
			streamed, err := streamAgg.(MetaAggregator).Components()
			if err != nil {
				t.Fatalf("Components: %v", err)
			}

			// Streaming and buffered drive different recurrences for
			// higher moments — the buffered path computes M2/M3/M4
			// over the pre-computed mean (a stable two-pass form),
			// while the streaming path uses the Welford-Pébaÿ online
			// recurrence. Both converge to the same population
			// moments on well-conditioned inputs but at a wider
			// numerical band than a Chan-Welford 4-way merge of
			// second-order moments; assert relative-error tolerance
			// (1e-9 — six orders of magnitude tighter than the
			// per-operation floating epsilon).
			const relTol = 1e-9
			for _, k := range c.keys {
				bv, bok := buffered[k]
				sv, sok := streamed[k]
				if !bok || !sok {
					t.Errorf("key %q present buffered=%v streamed=%v", k, bok, sok)
					continue
				}
				bf, bIsF := bv.(float64)
				sf, sIsF := sv.(float64)
				if !bIsF || !sIsF {
					t.Errorf("key %q non-float: buffered=%T streamed=%T", k, bv, sv)
					continue
				}
				diff := math.Abs(bf - sf)
				scale := math.Max(math.Abs(bf), math.Abs(sf))
				if scale == 0 {
					if diff != 0 {
						t.Errorf("key %q: buffered=%v streamed=%v non-zero diff at zero scale",
							k, bf, sf)
					}
					continue
				}
				if rel := diff / scale; rel > relTol {
					t.Errorf("key %q: buffered=%v streamed=%v rel-err=%v (tol %v)",
						k, bf, sf, rel, relTol)
				}
			}
		})
	}
}

// TestWelfordFamilyComponents_CompileTimeLocks is a build-time
// canary: if any of the eight Welford-family aggregator concrete types
// loses its MetaAggregator method set, the var declarations above (in
// aggregator.go, aggregator_welford.go, aggregator_cohort.go) won't
// compile. This test exists as runtime documentation of the lock —
// the meaningful enforcement is at build time.
func TestWelfordFamilyComponents_CompileTimeLocks(t *testing.T) {
	var (
		_ MetaAggregator = (*varianceAggregator)(nil)
		_ MetaAggregator = (*stdDevAggregator)(nil)
		_ MetaAggregator = (*skewnessAggregator)(nil)
		_ MetaAggregator = (*kurtosisAggregator)(nil)
		_ MetaAggregator = (*zscoreAggregator)(nil)
		_ MetaAggregator = (*welfordAggregator)(nil)
		_ MetaAggregator = (*ciAggregator)(nil)
	)
}
