package processing

import (
	"encoding/json"
	"math"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// Weighted shape aggregators (weighting-descriptive E2-S1): AGG_MEDIAN /
// AGG_PERCENTILE (expanded-index type 7), AGG_MODE / AGG_MODE_COUNT
// (max-Σw value / its Σw) and AGG_SKEWNESS / AGG_KURTOSIS (weighted
// population moments).

// wshapeSpec is one shape operator under test; params for percentile.
type wshapeSpec struct {
	name   string
	op     types.AggregationType
	params json.RawMessage
}

var wshapeSpecs = []wshapeSpec{
	{name: "median", op: types.AGG_MEDIAN},
	{name: "p0", op: types.AGG_PERCENTILE, params: json.RawMessage(`{"percentile":0}`)},
	{name: "p37.5", op: types.AGG_PERCENTILE, params: json.RawMessage(`{"percentile":37.5}`)},
	{name: "p95", op: types.AGG_PERCENTILE, params: json.RawMessage(`{"percentile":95}`)},
	{name: "p100", op: types.AGG_PERCENTILE, params: json.RawMessage(`{"percentile":100}`)},
	{name: "mode", op: types.AGG_MODE},
	{name: "mode_count", op: types.AGG_MODE_COUNT},
	{name: "skewness", op: types.AGG_SKEWNESS},
	{name: "kurtosis", op: types.AGG_KURTOSIS},
}

func (s wshapeSpec) agg(w types.SlotWeight) *types.Aggregation {
	return &types.Aggregation{Type: s.op, Field: "value", Label: "x", Params: s.params, Weight: w}
}

func (s wshapeSpec) weighted(kind types.WeightKind) *types.Aggregation {
	return s.agg(types.SlotWeightOf(types.WeightSpec{Field: "weight", Kind: kind}))
}

// wshapeValues: 400 deterministic values. Shape figures are run on a
// fractional set (operation-order sensitive) and a coarse integer set
// (repeats, so the mode and the median ties are real).
func wshapeValues(coarse bool) []float64 {
	xs := make([]float64, 400)
	seed := uint64(0x9e3779b97f4a7c15)
	for i := range xs {
		seed ^= seed << 13
		seed ^= seed >> 7
		seed ^= seed << 17
		if coarse {
			xs[i] = float64(seed % 37)
		} else {
			xs[i] = float64(seed%1_000_003)/997.0 + float64(i%7)*1e-3
		}
	}
	return xs
}

// wshapeRun runs agg buffered, and streamed / merged when the
// operator implements those paths; returns scalar + marshalled
// components per path.
func wshapeRun(t *testing.T, a *types.Aggregation, records []*Record) map[string][2]string {
	t.Helper()
	out := map[string][2]string{}
	pack := func(v float64, agg Aggregator) [2]string {
		c, err := agg.(MetaAggregator).Components()
		if err != nil {
			t.Fatal(err)
		}
		b, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		return [2]string{jsonFloatBits(v), string(b)}
	}
	buf := wcoreNew(t, a)
	v, err := buf.Aggregate(records, "value")
	if err != nil {
		t.Fatal(err)
	}
	out["buffered"] = pack(v, buf)
	if _, ok := wcoreNew(t, a).(OnlineAggregator); ok {
		s := wcoreNew(t, a)
		out["streamed"] = pack(wcoreStream(t, s, records), s)
	}
	if _, ok := wcoreNew(t, a).(MergeableAggregator); ok {
		l := wcoreNew(t, a)
		cuts := []int{0, 3, 41, 42, 157, 230, 311, 377, len(records)}
		for i := 0; i+1 < len(cuts); i++ {
			part := wcoreNew(t, a)
			for _, rec := range records[cuts[i]:cuts[i+1]] {
				_ = part.(OnlineAggregator).UpdateRow(rec, "value")
			}
			if err := l.(MergeableAggregator).MergeOnline(part.(OnlineAggregator)); err != nil {
				t.Fatal(err)
			}
		}
		v, _ := l.(OnlineAggregator).Finalize()
		out["merged"] = pack(v, l)
	}
	return out
}

func jsonFloatBits(v float64) string {
	b, _ := json.Marshal(math.Float64bits(v))
	return string(b)
}

// TestWeightedShape_UnityParity: an all-ones weight reproduces the
// unweighted scalar BIT FOR BIT and the components byte for byte on
// every path the operator runs (buffered; streamed and merged where it
// has them), under both kinds.
func TestWeightedShape_UnityParity(t *testing.T) {
	for _, coarse := range []bool{false, true} {
		records := unitRecords(wshapeValues(coarse))
		for _, s := range wshapeSpecs {
			for _, kind := range []types.WeightKind{types.WeightKindProbability, types.WeightKindFrequency} {
				name := s.name + "/" + string(kind)
				if coarse {
					name += "/coarse"
				}
				t.Run(name, func(t *testing.T) {
					plain := wshapeRun(t, s.agg(types.SlotWeight{}), records)
					got := wshapeRun(t, s.weighted(kind), records)
					if reflect.TypeOf(wcoreNew(t, s.weighted(kind))) == reflect.TypeOf(wcoreNew(t, s.agg(types.SlotWeight{}))) {
						t.Fatal("the weighted factory returned the unweighted type: the parity would be vacuous")
					}
					if len(plain) != len(got) {
						t.Fatalf("paths: unweighted %v, weighted %v", wshapePaths(plain), wshapePaths(got))
					}
					for path, want := range plain {
						if got[path] != want {
							t.Errorf("%s: unweighted %v, unit-weighted %v", path, want, got[path])
						}
					}
				})
			}
		}
	}
}

func wshapePaths(m map[string][2]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestWeightedShape_FrequencyExpansion: integer weights equal the
// physically duplicated rows run unweighted — exactly for median,
// percentile, mode and mode count; 1e-12 relative for the moments.
func TestWeightedShape_FrequencyExpansion(t *testing.T) {
	for _, coarse := range []bool{false, true} {
		xs := wshapeValues(coarse)
		ws := make([]float64, len(xs))
		var ex []float64
		for i, x := range xs {
			ws[i] = float64((i * 5) % 4) // 0..3; zero = no copies
			for k := 0; k < int(ws[i]); k++ {
				ex = append(ex, x)
			}
		}
		weighted, expanded := wvRecords(xs, ws), unitRecords(ex)
		for _, s := range wshapeSpecs {
			t.Run(s.name, func(t *testing.T) {
				want, _ := wcoreNew(t, s.agg(types.SlotWeight{})).Aggregate(expanded, "value")
				for path, got := range map[string]float64{
					"buffered": mustAggregate(t, wcoreNew(t, s.weighted(types.WeightKindFrequency)), weighted),
				} {
					exact := s.op != types.AGG_SKEWNESS && s.op != types.AGG_KURTOSIS
					if exact && got != want || !exact && !wvClose(got, want) {
						t.Errorf("%s = %.17g, expanded %.17g", path, got, want)
					}
				}
				if _, ok := wcoreNew(t, s.agg(types.SlotWeight{})).(OnlineAggregator); ok {
					got := wcoreStream(t, wcoreNew(t, s.weighted(types.WeightKindFrequency)), weighted)
					want := wcoreStream(t, wcoreNew(t, s.agg(types.SlotWeight{})), expanded)
					exact := s.op == types.AGG_MODE || s.op == types.AGG_MODE_COUNT
					if exact && got != want || !exact && !wvClose(got, want) {
						t.Errorf("streamed = %.17g, expanded %.17g", got, want)
					}
				}
			})
		}
	}
}

func mustAggregate(t *testing.T, a Aggregator, records []*Record) float64 {
	t.Helper()
	v, err := a.Aggregate(records, "value")
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// TestWeightedShape_Oracle pins hand-computed weighted figures,
// fractional (non-integer Σw) weights included.
func TestWeightedShape_Oracle(t *testing.T) {
	pct := func(p string) wshapeSpec {
		return wshapeSpec{op: types.AGG_PERCENTILE, params: json.RawMessage(`{"percentile":` + p + `}`)}
	}
	median := wshapeSpec{op: types.AGG_MEDIAN}
	mode := wshapeSpec{op: types.AGG_MODE}
	modeCount := wshapeSpec{op: types.AGG_MODE_COUNT}
	freq, prob := types.WeightKindFrequency, types.WeightKindProbability
	cases := []struct {
		name  string
		spec  wshapeSpec
		kind  types.WeightKind
		xs    []float64
		ws    []float64
		want  float64
		comps map[string]any
	}{
		// Frequency (raw) weights: expanded [10 20 20 20 30 30 40], W = 7
		// (numpy type 7 on np.repeat).
		{"p90 frequency", pct("90"), freq, []float64{40, 10, 30, 20}, []float64{1, 1, 2, 3}, 34,
			map[string]any{"p": 90.0, "position": 5.0, "lower": 30.0, "upper": 40.0, "method": "linear", "value": 34.0}},
		{"p25 frequency", pct("25"), freq, []float64{40, 10, 30, 20}, []float64{1, 1, 2, 3}, 20, nil},
		{"median frequency", median, freq, []float64{40, 10, 30, 20}, []float64{1, 1, 2, 3}, 20,
			map[string]any{"position_low": 3.0, "position_high": 3.0, "median": 20.0}},
		// Even expanded n: [1 2 2 3], h = 1.5 → (2+2)/2.
		{"median even frequency", median, freq, []float64{1, 2, 3}, []float64{1, 2, 1}, 2,
			map[string]any{"position_low": 1.0, "position_high": 2.0, "median": 2.0}},
		// Probability weights rescale to Σw = n = 4: sorted 10 20 30 40,
		// cum 1 4 6 7 → 4/7 16/7 24/7 4. x(k) is the smallest value whose
		// cumulative weight is ≥ k + 1 (Hmisc wtd.quantile normwt = TRUE,
		// which answers 37 and 25 here). p90: h = 0.9·3 = 2.7 →
		// x(2) = 30 (24/7 ≥ 3), x(3) = 40.
		{"p90 probability", pct("90"), prob, []float64{40, 10, 30, 20}, []float64{1, 1, 2, 3}, 30 + (0.9*3-2)*(40-30),
			map[string]any{"p": 90.0, "position": 2.0, "lower": 30.0, "upper": 40.0, "method": "linear", "value": 30 + (0.9*3-2)*(40-30)}},
		// Median h = 1.5 → x(1) = 20 (16/7 ≥ 2), x(2) = 30 → 25.
		{"median probability", median, prob, []float64{40, 10, 30, 20}, []float64{1, 1, 2, 3}, 25,
			map[string]any{"position_low": 1.0, "position_high": 2.0, "median": 25.0}},
		// W = 2.5, n = 3: cum 0.5 1.5 2.5 → 0.6 1.8 3. Median h = 1 →
		// x(1) = 3 (the first cumulative weight ≥ 2); Hmisc answers 3.
		{"median fractional probability", median, prob, []float64{3, 1, 2}, []float64{1, 0.5, 1}, 3,
			map[string]any{"position_low": 1.0, "position_high": 1.0, "median": 3.0}},
		// p100 h = n − 1 = 2 → the largest value.
		{"p100 fractional probability", pct("100"), prob, []float64{3, 1, 2}, []float64{1, 0.5, 1}, 3, nil},
		// p0 is NOT the minimum once the weights are fractional: x(0) is
		// the first value whose cumulative weight reaches 1 — 2, not 1
		// (cum 0.6) — exactly as Hmisc.
		{"p0 fractional probability", pct("0"), prob, []float64{3, 1, 2}, []float64{1, 0.5, 1}, 2, nil},
		// Σw = 0.75 < 1 no longer collapses to the minimum: cum → 1 2 3,
		// h = 0.95·2 = 1.9 → x(1) = 5, x(2) = 9.
		{"p95 W below one", pct("95"), prob, []float64{5, 2, 9}, []float64{0.25, 0.25, 0.25}, 5 + (0.95*2-1)*(9-5),
			map[string]any{"p": 95.0, "position": 1.0, "lower": 5.0, "upper": 9.0, "method": "linear", "value": 5 + (0.95*2-1)*(9-5)}},
		// A zero weight contributes nothing (the 1 vanishes).
		{"median zero weight", median, prob, []float64{1, 5, 7}, []float64{0, 1, 1}, 6, nil},
		{"mode", mode, prob, []float64{1, 2, 2, 3}, []float64{2.5, 1, 1, 0.4}, 1,
			map[string]any{"value": 1.0, "count": 2.5, "distinct_count": 3.0, "tie_count": 1.0}},
		// Weighted tie (1: 2, 2: 1+1) → smallest value, tie_count 2.
		{"mode tie", mode, prob, []float64{2, 1, 2, 3}, []float64{1, 2, 1, 0.5}, 1,
			map[string]any{"value": 1.0, "count": 2.0, "distinct_count": 3.0, "tie_count": 2.0}},
		{"mode_count", modeCount, prob, []float64{1, 2, 2, 3}, []float64{2.5, 1, 1.75, 0.4}, 2.75,
			map[string]any{"distinct_count": 3.0, "mode_value": 2.0, "mode_count": 2.75}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			agg := wcoreNew(t, tc.spec.weighted(tc.kind))
			got := mustAggregate(t, agg, wvRecords(tc.xs, tc.ws))
			if got != tc.want {
				t.Errorf("scalar = %v, want %v", got, tc.want)
			}
			if tc.comps != nil {
				c, _ := agg.(MetaAggregator).Components()
				var gotC map[string]any
				b, _ := json.Marshal(c)
				_ = json.Unmarshal(b, &gotC)
				if string(mustJSON(t, gotC)) != string(mustJSON(t, tc.comps)) {
					t.Errorf("components = %v, want %v", gotC, tc.comps)
				}
			}
			if _, ok := agg.(OnlineAggregator); ok {
				if s := wcoreStream(t, wcoreNew(t, tc.spec.weighted(tc.kind)), wvRecords(tc.xs, tc.ws)); s != tc.want {
					t.Errorf("streamed = %v, want %v", s, tc.want)
				}
			}
		})
	}
}

// wshapeQuantiles: the median / percentile rows of wshapeSpecs.
func wshapeQuantiles() []wshapeSpec {
	var out []wshapeSpec
	for _, s := range wshapeSpecs {
		if s.op == types.AGG_MEDIAN || s.op == types.AGG_PERCENTILE {
			out = append(out, s)
		}
	}
	return out
}

// wshapeFractionalWeights: 400 deterministic weights in (0, 4), so no
// normalized cumulative weight sits on an integer boundary.
func wshapeFractionalWeights() []float64 {
	ws := make([]float64, 400)
	seed := uint64(0x2545f4914f6cdd1d)
	for i := range ws {
		seed ^= seed << 13
		seed ^= seed >> 7
		seed ^= seed << 17
		ws[i] = float64(seed%1_000_000+1) / 250_001.0
	}
	return ws
}

// TestWeightedShape_ProbabilityQuantileScaleInvariant: probability
// weights are rescaled to Σw = n before the quantile math, so scaling
// every weight by any constant — Σw = 1 included — leaves the median and
// every percentile, and their components, unchanged; and the figure is
// NOT the raw expanded-index one (the rescale engaged).
func TestWeightedShape_ProbabilityQuantileScaleInvariant(t *testing.T) {
	xs := wshapeValues(false)
	ws := wshapeFractionalWeights()
	sum := 0.0
	for _, w := range ws {
		sum += w
	}
	scaled := func(c float64) []float64 {
		out := make([]float64, len(ws))
		for i, w := range ws {
			out[i] = w * c
		}
		return out
	}
	scales := map[string]float64{"x1/1024": 1.0 / 1024, "x8": 8, "x1/3": 1.0 / 3, "x1e-6": 1e-6, "x7.3": 7.3, "sum_one": 1 / sum}
	for _, s := range wshapeQuantiles() {
		base := wshapeRun(t, s.weighted(types.WeightKindProbability), wvRecords(xs, ws))
		for name, c := range scales {
			t.Run(s.name+"/"+name, func(t *testing.T) {
				got := wshapeRun(t, s.weighted(types.WeightKindProbability), wvRecords(xs, scaled(c)))
				if !reflect.DeepEqual(got, base) {
					t.Errorf("scaled by %v: %v, unscaled %v", c, got, base)
				}
			})
		}
	}
	// Σw = 1 on a tiny set: the raw rank 0.5·(Σw − 1) clamps to the
	// minimum; normalized (cum 0.75 2.25 3), the median is 20.
	records := wvRecords([]float64{30, 10, 20}, []float64{0.25, 0.25, 0.5})
	if got := mustAggregate(t, wcoreNew(t, wshapeSpec{op: types.AGG_MEDIAN}.weighted(types.WeightKindProbability)), records); got != 20 {
		t.Errorf("Σw = 1 median = %v, want 20 (not the clamped minimum 10)", got)
	}
}

// TestWeightedShape_FrequencyQuantileRaw: frequency weights are NOT
// rescaled — integer weights equal type 7 on the expanded data BIT FOR
// BIT, which a normalized rank would not reproduce on this fixture
// (Σw ≠ n). p95 also pins the interpolation's statement shape: with a
// clamp reassigning the rank (E2-S1's original form) it fuses
// differently from the unweighted twin on arm64 and answers
// 36.99999999999999 instead of 37.
func TestWeightedShape_FrequencyQuantileRaw(t *testing.T) {
	xs := []float64{40, 10, 30, 20}
	ws := []float64{1, 1, 2, 3}
	expanded := unitRecords([]float64{40, 10, 30, 30, 20, 20, 20})
	for _, s := range wshapeQuantiles() {
		t.Run(s.name, func(t *testing.T) {
			want := mustAggregate(t, wcoreNew(t, s.agg(types.SlotWeight{})), expanded)
			got := mustAggregate(t, wcoreNew(t, s.weighted(types.WeightKindFrequency)), wvRecords(xs, ws))
			if got != want {
				t.Errorf("frequency = %v, expanded %v", got, want)
			}
		})
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestWeightedShape_Moments: weighted skewness / kurtosis equal the
// closed-form weighted population moments — g1 = (Σw d³/W)/(Σw d²/W)^1.5,
// g2 = (Σw d⁴/W)/(Σw d²/W)² − 3 — on the buffered and streamed paths.
func TestWeightedShape_Moments(t *testing.T) {
	xs := []float64{2, 4, 4, 5, 7, 9, 13}
	ws := []float64{0.5, 1.5, 1.0, 2.0, 0.8, 1.2, 0.3}
	var W, swx float64
	for i := range xs {
		W += ws[i]
		swx += ws[i] * xs[i]
	}
	mu := swx / W
	var m2, m3, m4 float64
	for i := range xs {
		d := xs[i] - mu
		m2 += ws[i] * d * d
		m3 += ws[i] * d * d * d
		m4 += ws[i] * d * d * d * d
	}
	v := m2 / W
	want := map[types.AggregationType]float64{
		types.AGG_SKEWNESS: (m3 / W) / math.Pow(v, 1.5),
		types.AGG_KURTOSIS: (m4/W)/(v*v) - 3,
	}
	records := wvRecords(xs, ws)
	for op, w := range want {
		s := wshapeSpec{op: op}
		buf := wcoreNew(t, s.weighted(types.WeightKindProbability))
		got := mustAggregate(t, buf, records)
		streamed := wcoreNew(t, s.weighted(types.WeightKindProbability))
		sv := wcoreStream(t, streamed, records)
		for path, g := range map[string]float64{"buffered": got, "streamed": sv} {
			if !wvClose(g, w) {
				t.Errorf("%s %s = %.17g, want %.17g", op, path, g, w)
			}
		}
		for path, a := range map[string]Aggregator{"buffered": buf, "streamed": streamed} {
			c, _ := a.(MetaAggregator).Components()
			key := "skewness"
			if op == types.AGG_KURTOSIS {
				key = "kurtosis"
			}
			if !wvClose(c[key].(float64), w) || !wvClose(c["mean"].(float64), mu) || !wvClose(c["m2"].(float64), m2) || !wvClose(c["m3"].(float64), m3) {
				t.Errorf("%s %s components = %v", op, path, c)
			}
		}
	}
}

// TestWeightedShape_InvalidWeightsExcluded: null, negative and NaN/Inf
// weights drop the row — never coerced — on every shape operator.
func TestWeightedShape_InvalidWeightsExcluded(t *testing.T) {
	clean := wvRecords([]float64{1, 2, 2, 3, 8}, []float64{1, 1, 2, 1, 0.5})
	dirty := append(append([]*Record{}, clean...),
		cohortRec(100, -3, 0, 0, nil),
		cohortRec(100, math.NaN(), 0, 0, nil),
		cohortRec(100, math.Inf(1), 0, 0, nil),
		cohortRec(100, 0, 0, 0, map[string]bool{"weight": true}),
	)
	for _, s := range wshapeSpecs {
		t.Run(s.name, func(t *testing.T) {
			want := mustAggregate(t, wcoreNew(t, s.weighted(types.WeightKindProbability)), clean)
			got := mustAggregate(t, wcoreNew(t, s.weighted(types.WeightKindProbability)), dirty)
			if math.Float64bits(got) != math.Float64bits(want) {
				t.Errorf("invalid weights changed the figure: %v, want %v", got, want)
			}
		})
	}
}

// TestWeightedShape_ExecutionClassUnchanged: a weight does not move a
// shape operator between execution classes — median / percentile stay
// buffered, mode / mode count stay streamable and mergeable, skewness /
// kurtosis stay streamable and non-mergeable.
func TestWeightedShape_ExecutionClassUnchanged(t *testing.T) {
	for _, s := range wshapeSpecs {
		plain := &types.Request{Aggregations: []*types.Aggregation{s.agg(types.SlotWeight{})}}
		weighted := StampWeights(&types.Request{Weight: &types.WeightSpec{Field: "weight"},
			Aggregations: []*types.Aggregation{s.agg(types.SlotWeight{})}}, nil)
		if slotWeight(weighted.Aggregations[0]) == nil {
			t.Fatalf("%s: the weight was not stamped (still pending?)", s.name)
		}
		if CanStreamRequest(weighted, cohortSchema()) != CanStreamRequest(plain, cohortSchema()) ||
			CanMergeRequest(weighted, cohortSchema()) != CanMergeRequest(plain, cohortSchema()) {
			t.Errorf("%s: weighted streamable=%v mergeable=%v, unweighted %v / %v", s.name,
				CanStreamRequest(weighted, cohortSchema()), CanMergeRequest(weighted, cohortSchema()),
				CanStreamRequest(plain, cohortSchema()), CanMergeRequest(plain, cohortSchema()))
		}
		_, wOnline := wcoreNew(t, s.weighted("")).(OnlineAggregator)
		_, pOnline := wcoreNew(t, s.agg(types.SlotWeight{})).(OnlineAggregator)
		_, wMerge := wcoreNew(t, s.weighted("")).(MergeableAggregator)
		_, pMerge := wcoreNew(t, s.agg(types.SlotWeight{})).(MergeableAggregator)
		if wOnline != pOnline || wMerge != pMerge {
			t.Errorf("%s: weighted online=%v merge=%v, unweighted %v / %v", s.name, wOnline, wMerge, pOnline, pMerge)
		}
	}
}

// TestWeightedShape_MedianMeanOfMiddles: an even-count unit-weighted
// median is the unweighted mean of the two middles, (lo + hi) / 2, not
// the interpolation lo + 0.5·(hi − lo) — the two differ in the last
// ULP on this pair.
func TestWeightedShape_MedianMeanOfMiddles(t *testing.T) {
	lo, hi := 0.254458609934608, 10.944941000576247
	if (lo+hi)/2 == lo+0.5*(hi-lo) {
		t.Fatal("fixture no longer separates the two forms")
	}
	records := unitRecords([]float64{hi, -3, lo, 40})
	plain := mustAggregate(t, wcoreNew(t, wshapeSpec{op: types.AGG_MEDIAN}.agg(types.SlotWeight{})), records)
	got := mustAggregate(t, wcoreNew(t, wshapeSpec{op: types.AGG_MEDIAN}.weighted(types.WeightKindProbability)), records)
	if math.Float64bits(got) != math.Float64bits(plain) {
		t.Errorf("unit-weighted median %.17g, unweighted %.17g", got, plain)
	}
}

// TestWeightedShape_HmiscQuantile: probability-weighted median /
// percentile equal Hmisc 5.3.0 wtd.quantile(x, w, probs, normwt = TRUE)
// on the E2-S4 counterexamples, where Hmisc's "smallest value whose
// cumulative weight is ≥ the 1-based rank" and the earlier "exceeds the
// 0-based rank" rule disagree (the old answers are in the comments).
// Pulse interpolates as lo + f·(hi − lo) — the unweighted statement
// shape — while Hmisc writes (1 − f)·lo + f·hi, so the two may differ
// in the last ulps (7.275 vs 7.2749999999999986); 1e-12 relative.
func TestWeightedShape_HmiscQuantile(t *testing.T) {
	// The reference fixture's x with the null and invalid-weight rows
	// dropped; w its fractional probability weights (a zero kept — both
	// drop it from n), p = 0.37·f its integer frequency twin.
	xw := []float64{3.5, 1.25, 7, 3.5, 2, 9.75, 1.25, 3.5, 5, 4, 6.5}
	w := []float64{0.8, 1.7, 0.35, 2.2, 0, 1.15, 0.6, 1.3, 2.7, 0.45, 0.95}
	xp := []float64{3.5, 1.25, 7, 3.5, 2, 9.75, 1.25, 3.5, 5, 4}
	p := []float64{2, 1, 0, 3, 1, 2, 4, 1, 3, 0}
	for i := range p {
		p[i] *= 0.37
	}
	pct := func(v string) wshapeSpec {
		return wshapeSpec{op: types.AGG_PERCENTILE, params: json.RawMessage(`{"percentile":` + v + `}`)}
	}
	median := wshapeSpec{op: types.AGG_MEDIAN}
	cases := []struct {
		name string
		spec wshapeSpec
		xs   []float64
		ws   []float64
		want float64
	}{
		{"w median", median, xw, w, 4.25},               // was 3.5
		{"w p37.5", pct("37.5"), xw, w, 3.5},            // unchanged
		{"w p90", pct("90"), xw, w, 7.2749999999999986}, // was 6.55
		{"p median", median, xp, p, 3.5},                // unchanged
		{"p p37.5", pct("37.5"), xp, p, 3.5},            // was 2.65625
		{"p p90", pct("90"), xp, p, 6.4249999999999954}, // was 5.0
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := mustAggregate(t, wcoreNew(t, tc.spec.weighted(types.WeightKindProbability)), wvRecords(tc.xs, tc.ws))
			if math.Abs(got-tc.want) > 1e-12*math.Abs(tc.want) {
				t.Errorf("got %.17g, Hmisc %.17g", got, tc.want)
			}
		})
	}
}

// TestWeightedShape_QuantileRankSnap: a cumulative weight within 1e-9
// relative of an integer is snapped to it before it is compared with a
// rank, so a cumulative weight that lands one ulp either side of the
// rank (float summation order — e.g. FMA contraction on one
// architecture but not another) answers exactly as the exact integer.
func TestWeightedShape_QuantileRankSnap(t *testing.T) {
	xs := []float64{10, 20, 30, 40}
	quantileOrderStat := func(xs, cum []float64, k int) float64 { return xs[quantileOrderIndex(cum, k)] }
	exact := []float64{1, 2, 3, 4}
	want := quantileOrderStat(xs, exact, 1) // rank 2 (1-based): cum 2 ≥ 2 → 20
	if want != 20 {
		t.Fatalf("exact cumulative weights: x(1) = %v, want 20", want)
	}
	for name, c := range map[string]float64{"below": math.Nextafter(2, 0), "above": math.Nextafter(2, 3), "1e-10 below": 2 - 2e-10} {
		t.Run(name, func(t *testing.T) {
			cum := snapCumWeights([]float64{1, c, 3, 4})
			if got := quantileOrderStat(xs, cum, 1); got != want {
				t.Errorf("cum %.17g: x(1) = %v, want %v", c, got, want)
			}
		})
	}
	// End to end: weights 1.2, 0.3, 0.3 normalize to cumulative 2, 2.5,
	// 3 exactly, but the float rescale lands the first one ulp low
	// (1.9999999999999998). The median's rank is 2, which the first value
	// reaches exactly — 10. Unsnapped the answer would be 20, and so is
	// Hmisc's own float result: the snap answers the exact-arithmetic
	// Hmisc figure, not its last-ulp artifact.
	records := wvRecords([]float64{10, 20, 30}, []float64{1.2, 0.3, 0.3})
	if got := mustAggregate(t, wcoreNew(t, wshapeSpec{op: types.AGG_MEDIAN}.weighted(types.WeightKindProbability)), records); got != 10 {
		t.Errorf("knife-edge median = %v, want 10", got)
	}
	// Outside the tolerance the value is left alone: 2 − 1e-6 does not
	// reach rank 2, so x(1) moves on to 30.
	if got := quantileOrderStat(xs, snapCumWeights([]float64{1, 2 - 1e-6, 3, 4}), 1); got != 30 {
		t.Errorf("2 − 1e-6 snapped: x(1) = %v, want 30", got)
	}
}
