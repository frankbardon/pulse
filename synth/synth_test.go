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
// verifies the empirical null rate is close to the requested rate.
func TestSynth_NullableU8RespectsNullRate(t *testing.T) {
	spec := &synth.Spec{
		RowCount: 5000,
		Fields: []synth.FieldSpec{
			{Name: "n", Type: "nullable_u8", Distribution: synth.DistUniform,
				Params:   map[string]any{"min": 1.0, "max": 100.0},
				NullRate: 0.25},
		},
	}
	data, _, err := synth.SynthBytes(spec, synth.Options{Seed: 42})
	if err != nil {
		t.Fatalf("synth: %v", err)
	}
	values := readU8Field(t, data, "n")
	nulls := 0
	for _, v := range values {
		if v == 0xFF {
			nulls++
		}
	}
	frac := float64(nulls) / float64(len(values))
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

	specFromProfile := synth.SpecFromProfile(prof, 5000)
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

// --- helpers ---

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

func mean(xs []float64) float64 {
	s := 0.0
	for _, x := range xs {
		s += x
	}
	return s / float64(len(xs))
}
