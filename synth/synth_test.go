package synth_test

import (
	"bytes"
	"context"
	"math"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/synth"
	"github.com/spf13/afero"
)

// TestSynth_DeterministicByteIdentical asserts the determinism contract:
// same spec + same seed must produce identical bytes.
func TestSynth_DeterministicByteIdentical(t *testing.T) {
	spec := smallSpec(1000)

	a, _, err := synth.SynthBytes(spec, synth.Options{Seed: 42})
	if err != nil {
		t.Fatalf("first synth: %v", err)
	}
	b, _, err := synth.SynthBytes(spec, synth.Options{Seed: 42})
	if err != nil {
		t.Fatalf("second synth: %v", err)
	}
	if !bytes.Equal(a, b) {
		t.Fatalf("byte mismatch for identical (spec, seed): len(a)=%d len(b)=%d", len(a), len(b))
	}
}

// TestSynth_SeedSensitivity asserts that different seeds change output.
func TestSynth_SeedSensitivity(t *testing.T) {
	spec := smallSpec(500)
	a, _, err := synth.SynthBytes(spec, synth.Options{Seed: 1})
	if err != nil {
		t.Fatalf("seed 1: %v", err)
	}
	b, _, err := synth.SynthBytes(spec, synth.Options{Seed: 2})
	if err != nil {
		t.Fatalf("seed 2: %v", err)
	}
	if bytes.Equal(a, b) {
		t.Fatal("different seeds produced identical output")
	}
}

// TestSynth_MonotonicFromIsDeterministic_AcrossSeeds asserts monotonic_from
// ignores the RNG (the values follow a fixed sequence regardless of seed).
func TestSynth_MonotonicFromIsDeterministic_AcrossSeeds(t *testing.T) {
	spec := &synth.Spec{
		RowCount: 16,
		Fields: []synth.FieldSpec{
			{Name: "id", Type: "u32", Distribution: synth.DistMonotonicFrom,
				Params: map[string]any{"start": 100.0, "step": 1.0}},
		},
	}
	bytesA, _, err := synth.SynthBytes(spec, synth.Options{Seed: 7})
	if err != nil {
		t.Fatalf("seed 7: %v", err)
	}
	bytesB, _, err := synth.SynthBytes(spec, synth.Options{Seed: 99})
	if err != nil {
		t.Fatalf("seed 99: %v", err)
	}
	// Monotonic_from values do not consume RNG, so the output is identical
	// regardless of seed.
	if !bytes.Equal(bytesA, bytesB) {
		t.Fatal("monotonic_from output should be identical across seeds")
	}
}

// TestSynth_NormalDistributionMeanWithinTolerance checks that 1k samples
// from a normal distribution have an empirical mean close to the spec.
func TestSynth_NormalDistributionMeanWithinTolerance(t *testing.T) {
	spec := &synth.Spec{
		RowCount: 5000,
		Fields: []synth.FieldSpec{
			{Name: "v", Type: "f64", Distribution: synth.DistNormal,
				Params: map[string]any{"mean": 100.0, "std": 5.0}},
		},
	}
	data, _, err := synth.SynthBytes(spec, synth.Options{Seed: 42})
	if err != nil {
		t.Fatalf("synth: %v", err)
	}
	values := readF64Field(t, data, "v")
	mean := mean(values)
	if math.Abs(mean-100) > 0.5 {
		t.Errorf("normal mean = %.4f, expected near 100", mean)
	}
}

// TestSynth_BernoulliApproximatesP draws bernoulli(p=0.7) and checks that
// the empirical fraction is close to p.
func TestSynth_BernoulliApproximatesP(t *testing.T) {
	spec := &synth.Spec{
		RowCount: 5000,
		Fields: []synth.FieldSpec{
			{Name: "flag", Type: "u8", Distribution: synth.DistBernoulli,
				Params: map[string]any{"p": 0.7}},
		},
	}
	data, _, err := synth.SynthBytes(spec, synth.Options{Seed: 7})
	if err != nil {
		t.Fatalf("synth: %v", err)
	}
	values := readU8Field(t, data, "flag")
	ones := 0
	for _, v := range values {
		if v == 1 {
			ones++
		}
	}
	frac := float64(ones) / float64(len(values))
	if math.Abs(frac-0.7) > 0.03 {
		t.Errorf("bernoulli fraction = %.3f, expected near 0.7", frac)
	}
}

// TestSynth_WeightedCategoricalRespectsWeights ensures the weighted sampler
// produces frequencies near the requested weights.
func TestSynth_WeightedCategoricalRespectsWeights(t *testing.T) {
	spec := &synth.Spec{
		RowCount: 10000,
		Fields: []synth.FieldSpec{
			{Name: "country", Type: "categorical_u8", Distribution: synth.DistWeightedCategorical,
				Params: map[string]any{
					"values":  []any{"US", "UK", "DE"},
					"weights": []any{0.5, 0.3, 0.2},
				}},
		},
	}
	data, _, err := synth.SynthBytes(spec, synth.Options{Seed: 1})
	if err != nil {
		t.Fatalf("synth: %v", err)
	}
	categories := readCategoricalField(t, data, "country")
	hist := map[string]int{}
	for _, c := range categories {
		hist[c]++
	}
	total := float64(len(categories))
	want := map[string]float64{"US": 0.5, "UK": 0.3, "DE": 0.2}
	for k, v := range want {
		got := float64(hist[k]) / total
		if math.Abs(got-v) > 0.03 {
			t.Errorf("category %s: frac=%.3f want %.2f", k, got, v)
		}
	}
}

// TestSynth_MixtureReproducesBimodalShape is the E4-S1 fidelity gate: a
// mixture with two well-separated components must visibly produce two
// histogram peaks with a valley between them, not collapse into a single
// normal-shaped hump. This is a real statistical check on the sampled
// output, not a smoke test that generation merely runs without error.
func TestSynth_MixtureReproducesBimodalShape(t *testing.T) {
	const (
		mean1, std1 = -10.0, 1.5
		mean2, std2 = 10.0, 1.5
	)
	spec := &synth.Spec{
		RowCount: 8000,
		Fields: []synth.FieldSpec{
			{Name: "v", Type: "f64", Distribution: synth.DistMixture,
				Params: map[string]any{
					"means":   []any{mean1, mean2},
					"stds":    []any{std1, std2},
					"weights": []any{0.5, 0.5},
				}},
		},
	}
	data, _, err := synth.SynthBytes(spec, synth.Options{Seed: 11})
	if err != nil {
		t.Fatalf("synth: %v", err)
	}
	values := readF64Field(t, data, "v")

	const (
		binWidth = 1.0
		lo       = mean1 - 6*std1
		hi       = mean2 + 6*std2
	)
	nBins := int((hi-lo)/binWidth) + 1
	hist := make([]int, nBins)
	binOf := func(x float64) int {
		idx := int((x - lo) / binWidth)
		if idx < 0 {
			idx = 0
		}
		if idx >= nBins {
			idx = nBins - 1
		}
		return idx
	}
	for _, v := range values {
		hist[binOf(v)]++
	}

	// Locate the tallest peak, then mask a window around it (wide enough to
	// cover one whole component) and locate the second-tallest remaining
	// peak — the standard two-pass approach for detecting two modes in a
	// histogram without assuming their exact bin locations.
	maskRadius := int(4 * std1 / binWidth)
	argmax := func(h []int) int {
		best := 0
		for i, c := range h {
			if c > h[best] {
				best = i
			}
		}
		return best
	}
	peak1 := argmax(hist)
	masked := append([]int(nil), hist...)
	for i := peak1 - maskRadius; i <= peak1+maskRadius; i++ {
		if i >= 0 && i < len(masked) {
			masked[i] = 0
		}
	}
	peak2 := argmax(masked)

	if peak1 == peak2 {
		t.Fatalf("only one peak detected — sample collapsed to a single mode (bin %d)", peak1)
	}
	lowIdx, highIdx := peak1, peak2
	if lowIdx > highIdx {
		lowIdx, highIdx = highIdx, lowIdx
	}
	peakX1 := lo + float64(lowIdx)*binWidth
	peakX2 := lo + float64(highIdx)*binWidth
	if peakX2-peakX1 < (mean2-mean1)*0.5 {
		t.Fatalf("detected peaks too close together (%.1f, %.1f) — expected separation near %.1f",
			peakX1, peakX2, mean2-mean1)
	}

	valley := hist[lowIdx]
	for i := lowIdx + 1; i < highIdx; i++ {
		if hist[i] < valley {
			valley = hist[i]
		}
	}
	smallerPeak := hist[lowIdx]
	if hist[highIdx] < smallerPeak {
		smallerPeak = hist[highIdx]
	}
	if float64(valley) >= 0.5*float64(smallerPeak) {
		t.Fatalf("no valley between modes: valley count=%d, peaks=(%d,%d) — shape looks unimodal",
			valley, hist[lowIdx], hist[highIdx])
	}
}

// TestSynth_MixtureValidatesParams asserts malformed mixture params fail
// spec parsing with SERVICE_VALIDATION rather than silently degenerating.
func TestSynth_MixtureValidatesParams(t *testing.T) {
	cases := []struct {
		name   string
		params map[string]any
	}{
		{"single mean", map[string]any{"means": []any{1.0}, "stds": []any{1.0}}},
		{"stds length mismatch", map[string]any{"means": []any{1.0, 2.0}, "stds": []any{1.0}}},
		{"non-positive std", map[string]any{"means": []any{1.0, 2.0}, "stds": []any{1.0, 0.0}}},
		{"weights length mismatch", map[string]any{
			"means": []any{1.0, 2.0}, "stds": []any{1.0, 1.0}, "weights": []any{1.0},
		}},
		{"negative weight", map[string]any{
			"means": []any{1.0, 2.0}, "stds": []any{1.0, 1.0}, "weights": []any{-1.0, 1.0},
		}},
		{"zero-sum weights", map[string]any{
			"means": []any{1.0, 2.0}, "stds": []any{1.0, 1.0}, "weights": []any{0.0, 0.0},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec := &synth.Spec{
				RowCount: 10,
				Fields: []synth.FieldSpec{
					{Name: "v", Type: "f64", Distribution: synth.DistMixture, Params: tc.params},
				},
			}
			if _, _, err := synth.SynthBytes(spec, synth.Options{Seed: 1}); err == nil {
				t.Fatal("expected SERVICE_VALIDATION, got nil")
			}
		})
	}
}

// TestSynth_UniformDateSingleDayRange asserts a degenerate uniform_date
// range (start == end, the shape a single-wave source cohort profiles to)
// is legal and every row lands on that day — not a SERVICE_VALIDATION
// refusal. Regression for the synth-from-sample path over one-wave data.
func TestSynth_UniformDateSingleDayRange(t *testing.T) {
	spec := &synth.Spec{
		RowCount: 50,
		Fields: []synth.FieldSpec{
			{Name: "waveDate", Type: "date", Distribution: synth.DistUniformDate,
				Params: map[string]any{"start": "2023-02-20", "end": "2023-02-20"}},
		},
	}
	data, _, err := synth.SynthBytes(spec, synth.Options{Seed: 7})
	if err != nil {
		t.Fatalf("expected single-day uniform_date range to succeed, got: %v", err)
	}
	got := readF64Field(t, data, "waveDate")
	if len(got) != 50 {
		t.Fatalf("expected 50 rows, got %d", len(got))
	}
	for i, v := range got {
		if v != got[0] {
			t.Fatalf("row %d: expected every row on the same day (%v), got %v", i, got[0], v)
		}
	}
}

// TestSynth_UniformDateEndBeforeStartRejected asserts an inverted range
// (end strictly before start) still refuses — only the equal-bounds case
// was wrongly rejected.
func TestSynth_UniformDateEndBeforeStartRejected(t *testing.T) {
	spec := &synth.Spec{
		RowCount: 10,
		Fields: []synth.FieldSpec{
			{Name: "d", Type: "date", Distribution: synth.DistUniformDate,
				Params: map[string]any{"start": "2023-02-20", "end": "2023-02-19"}},
		},
	}
	if _, _, err := synth.SynthBytes(spec, synth.Options{Seed: 1}); err == nil {
		t.Fatal("expected SERVICE_VALIDATION for end before start, got nil")
	}
}

// TestSynth_ConstraintSatisfiedEveryRow verifies all rows satisfy declared
// constraints when generation succeeds.
func TestSynth_ConstraintSatisfiedEveryRow(t *testing.T) {
	spec := &synth.Spec{
		RowCount: 1000,
		Fields: []synth.FieldSpec{
			{Name: "a", Type: "f64", Distribution: synth.DistUniform,
				Params: map[string]any{"min": 0.0, "max": 100.0}},
			{Name: "b", Type: "f64", Distribution: synth.DistUniform,
				Params: map[string]any{"min": 0.0, "max": 100.0}},
		},
		Constraints:      []synth.ConstraintSpec{{Expr: "a <= b"}},
		MaxRejectionRate: 0.7,
	}
	data, res, err := synth.SynthBytes(spec, synth.Options{Seed: 5})
	if err != nil {
		t.Fatalf("synth: %v", err)
	}
	if res.RowsGenerated != 1000 {
		t.Fatalf("expected 1000 rows, got %d", res.RowsGenerated)
	}
	a := readF64Field(t, data, "a")
	b := readF64Field(t, data, "b")
	for i := range a {
		if a[i] > b[i] {
			t.Fatalf("row %d violates a<=b: a=%v b=%v", i, a[i], b[i])
		}
	}
}

// TestSynth_ConstraintInfeasibleErrorsOut verifies the rejection-rate
// safety valve fires on a contradictory constraint.
func TestSynth_ConstraintInfeasibleErrorsOut(t *testing.T) {
	spec := &synth.Spec{
		RowCount: 100,
		Fields: []synth.FieldSpec{
			{Name: "x", Type: "f64", Distribution: synth.DistUniform,
				Params: map[string]any{"min": 0.0, "max": 1.0}},
		},
		// Practically infeasible: x must be in a tiny slice.
		Constraints: []synth.ConstraintSpec{{Expr: "x > 0.999"}},
	}
	_, _, err := synth.SynthBytes(spec, synth.Options{Seed: 1})
	if err == nil {
		t.Fatal("expected PULSE_SYNTH_CONSTRAINT_INFEASIBLE, got nil")
	}
}

// TestSynth_UnknownDistributionErrors verifies the typed error.
func TestSynth_UnknownDistributionErrors(t *testing.T) {
	spec := &synth.Spec{
		RowCount: 10,
		Fields: []synth.FieldSpec{
			{Name: "x", Type: "f64", Distribution: "magic_distribution"},
		},
	}
	_, _, err := synth.SynthBytes(spec, synth.Options{Seed: 1})
	if err == nil {
		t.Fatal("expected PULSE_SYNTH_DISTRIBUTION_UNKNOWN, got nil")
	}
}

// TestSynth_RoundTripsThroughPulseFacade verifies pulse.New + Synth +
// Inspect round-trips and produces a readable .pulse file.
func TestSynth_RoundTripsThroughPulseFacade(t *testing.T) {
	fs := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	spec := smallSpec(200)
	res, err := p.Synth(context.Background(), spec, "/cohorts/demo.pulse",
		pulse.SynthOptions{Seed: 42})
	if err != nil {
		t.Fatalf("Synth: %v", err)
	}
	if res.RowsGenerated != 200 {
		t.Errorf("expected 200 rows, got %d", res.RowsGenerated)
	}

	insp, err := p.Inspect(context.Background(), "/cohorts/demo.pulse")
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	wantNames := []string{"id", "score", "country"}
	if len(insp.Fields) != len(wantNames) {
		t.Fatalf("inspect fields = %d, want %d", len(insp.Fields), len(wantNames))
	}
	for i, want := range wantNames {
		if insp.Fields[i].Name != want {
			t.Errorf("field %d name = %q, want %q", i, insp.Fields[i].Name, want)
		}
	}
}

// TestSynth_NullableU8RespectsNullRate generates a nullable column and
// verifies the empirical null rate is close to the requested rate. Null
// state is carried by the per-record bitmap, not an inline sentinel.
func TestSynth_NullableU8RespectsNullRate(t *testing.T) {
	spec := &synth.Spec{
		RowCount: 5000,
		Fields: []synth.FieldSpec{
			{Name: "n", Type: "u8", Nullable: true, Distribution: synth.DistUniform,
				Params:   map[string]any{"min": 1.0, "max": 100.0},
				NullRate: 0.25},
		},
	}
	data, _, err := synth.SynthBytes(spec, synth.Options{Seed: 42})
	if err != nil {
		t.Fatalf("synth: %v", err)
	}
	nulls := readNullCountForField(t, data, "n")
	total := readRecordCount(t, data)
	frac := float64(nulls) / float64(total)
	if math.Abs(frac-0.25) > 0.03 {
		t.Errorf("null fraction = %.3f, want near 0.25", frac)
	}
}

// TestProfile_ThenSynth_RoundTripsCategoricalShape generates a synthetic
// cohort, profiles it, then synthesizes from the profile and checks that
// categorical frequencies survive the round trip.
func TestProfile_ThenSynth_RoundTripsCategoricalShape(t *testing.T) {
	fs := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	src := &synth.Spec{
		RowCount: 5000,
		Fields: []synth.FieldSpec{
			{Name: "country", Type: "categorical_u8", Distribution: synth.DistWeightedCategorical,
				Params: map[string]any{
					"values":  []any{"US", "UK", "DE"},
					"weights": []any{0.6, 0.3, 0.1},
				}},
		},
	}
	if _, err := p.Synth(context.Background(), src, "/source.pulse",
		pulse.SynthOptions{Seed: 1}); err != nil {
		t.Fatalf("source synth: %v", err)
	}

	prof, err := p.Profile(context.Background(), "/source.pulse",
		pulse.ProfileOptions{IncludeStats: true})
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	if prof.RowCount != 5000 {
		t.Errorf("profile row count = %d", prof.RowCount)
	}

	specFromProfile, _ := synth.SpecFromProfile(prof, 5000)
	if _, err := p.Synth(context.Background(), specFromProfile, "/synth.pulse",
		pulse.SynthOptions{Seed: 2}); err != nil {
		t.Fatalf("synth from profile: %v", err)
	}

	prof2, err := p.Profile(context.Background(), "/synth.pulse",
		pulse.ProfileOptions{IncludeStats: true})
	if err != nil {
		t.Fatalf("profile2: %v", err)
	}
	if prof2.Fields[0].Categorical == nil {
		t.Fatal("re-profiled categorical missing")
	}
	// Top category should still be US.
	if prof2.Fields[0].Categorical.Top[0].Value != "US" {
		t.Errorf("expected US as top category after round trip, got %s",
			prof2.Fields[0].Categorical.Top[0].Value)
	}
}

func smallSpec(rows int) *synth.Spec {
	return &synth.Spec{
		RowCount: rows,
		Fields: []synth.FieldSpec{
			{Name: "id", Type: "u32", Distribution: synth.DistMonotonicFrom,
				Params: map[string]any{"start": 1.0}},
			{Name: "score", Type: "f64", Distribution: synth.DistNormal,
				Params: map[string]any{"mean": 0.0, "std": 1.0}},
			{Name: "country", Type: "categorical_u8", Distribution: synth.DistWeightedCategorical,
				Params: map[string]any{
					"values":  []any{"US", "UK", "DE"},
					"weights": []any{0.5, 0.3, 0.2},
				}},
		},
	}
}

func readF64Field(t *testing.T, data []byte, name string) []float64 {
	t.Helper()
	return readField(t, data, name)
}

func readU8Field(t *testing.T, data []byte, name string) []uint8 {
	t.Helper()
	values := readField(t, data, name)
	out := make([]uint8, len(values))
	for i, v := range values {
		out[i] = uint8(v)
	}
	return out
}

func readCategoricalField(t *testing.T, data []byte, name string) []string {
	t.Helper()
	r := bytes.NewReader(data)
	if err := encoding.ReadHeader(r); err != nil {
		t.Fatalf("read header: %v", err)
	}
	schema, err := encoding.ReadSchema(r)
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	rr := encoding.NewRecordReader(r, schema)
	var out []string
	values := make(map[string]float64)
	nulls := make(map[string]bool)
	for {
		err := rr.ReadRecord(values, nulls)
		if err != nil {
			break
		}
		f := schema.Field(name)
		if f == nil || f.Dictionary == nil {
			t.Fatalf("not a categorical field: %s", name)
		}
		out = append(out, f.Dictionary.Resolve(uint32(values[name])))
	}
	return out
}

func readField(t *testing.T, data []byte, name string) []float64 {
	t.Helper()
	r := bytes.NewReader(data)
	if err := encoding.ReadHeader(r); err != nil {
		t.Fatalf("read header: %v", err)
	}
	schema, err := encoding.ReadSchema(r)
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	rr := encoding.NewRecordReader(r, schema)
	var out []float64
	values := make(map[string]float64)
	nulls := make(map[string]bool)
	for {
		err := rr.ReadRecord(values, nulls)
		if err != nil {
			break
		}
		out = append(out, values[name])
	}
	return out
}

// readNullCountForField returns the number of records where the given
// field is flagged null (via the per-record bitmap).
func readNullCountForField(t *testing.T, data []byte, name string) int {
	t.Helper()
	r := bytes.NewReader(data)
	if err := encoding.ReadHeader(r); err != nil {
		t.Fatalf("read header: %v", err)
	}
	schema, err := encoding.ReadSchema(r)
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	rr := encoding.NewRecordReader(r, schema)
	values := make(map[string]float64)
	nulls := make(map[string]bool)
	count := 0
	for {
		err := rr.ReadRecord(values, nulls)
		if err != nil {
			break
		}
		if nulls[name] {
			count++
		}
	}
	return count
}

// readRecordCount returns the total number of records in a .pulse byte
// stream by walking the iterator to exhaustion.
func readRecordCount(t *testing.T, data []byte) int {
	t.Helper()
	r := bytes.NewReader(data)
	if err := encoding.ReadHeader(r); err != nil {
		t.Fatalf("read header: %v", err)
	}
	schema, err := encoding.ReadSchema(r)
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	rr := encoding.NewRecordReader(r, schema)
	values := make(map[string]float64)
	nulls := make(map[string]bool)
	n := 0
	for {
		if err := rr.ReadRecord(values, nulls); err != nil {
			break
		}
		n++
	}
	return n
}

func mean(xs []float64) float64 {
	s := 0.0
	for _, x := range xs {
		s += x
	}
	return s / float64(len(xs))
}
