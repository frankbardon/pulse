package synth_test

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/synth"
	"github.com/spf13/afero"
)

// synthSingleField writes a small single-numeric-field .pulse cohort
// generated from the given distribution/params, mirroring E4-S1's own
// bimodal test fixture shape (synth_test.go's
// TestSynth_MixtureReproducesBimodalShape) so this story's capture-side
// tests exercise the same kind of source data the generation-side test
// already trusts.
func synthSingleField(t *testing.T, rows int, seed int64, dist string, params map[string]any) []byte {
	t.Helper()
	spec := &synth.Spec{
		RowCount: rows,
		Fields: []synth.FieldSpec{
			{Name: "v", Type: "f64", Distribution: dist, Params: params},
		},
	}
	data, _, err := synth.SynthBytes(spec, synth.Options{Seed: seed})
	if err != nil {
		t.Fatalf("synth fixture: %v", err)
	}
	return data
}

// TestProfile_FitShapeCapturesBimodalMixture is (half of) this story's
// own acceptance gate: profiling a clearly bimodal numeric field with
// --fit-shape must capture a mixture representation
// (FieldProfile.Numeric.Shape), with both components landing near the
// source's own well-separated means.
func TestProfile_FitShapeCapturesBimodalMixture(t *testing.T) {
	const (
		mean1, std1 = -10.0, 1.5
		mean2, std2 = 10.0, 1.5
	)
	fs := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	data := synthSingleField(t, 8000, 11, synth.DistMixture, map[string]any{
		"means":   []any{mean1, mean2},
		"stds":    []any{std1, std2},
		"weights": []any{0.5, 0.5},
	})
	if err := afero.WriteFile(fs, "/bimodal.pulse", data, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	prof, err := p.Profile(context.Background(), "/bimodal.pulse", pulse.ProfileOptions{
		FitShape: true,
	})
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	if len(prof.Fields) != 1 || prof.Fields[0].Numeric == nil {
		t.Fatalf("expected one numeric field profile, got %+v", prof.Fields)
	}
	shape := prof.Fields[0].Numeric.Shape
	if shape == nil {
		t.Fatal("expected Numeric.Shape to be populated for a clearly bimodal field")
	}
	if len(shape.Means) != 2 || len(shape.Stds) != 2 || len(shape.Weights) != 2 {
		t.Fatalf("expected 2-component shape, got means=%v stds=%v weights=%v",
			shape.Means, shape.Stds, shape.Weights)
	}
	// shape.Means is sorted ascending by fitTwoComponentEM.
	if math.Abs(shape.Means[0]-mean1) > 1.5 {
		t.Errorf("component 1 mean = %.2f, want near %.1f", shape.Means[0], mean1)
	}
	if math.Abs(shape.Means[1]-mean2) > 1.5 {
		t.Errorf("component 2 mean = %.2f, want near %.1f", shape.Means[1], mean2)
	}
	for i, w := range shape.Weights {
		if math.Abs(w-0.5) > 0.15 {
			t.Errorf("component %d weight = %.3f, want near 0.5", i, w)
		}
	}
	for i, s := range shape.Stds {
		if s <= 0 {
			t.Errorf("component %d std = %.3f, want > 0", i, s)
		}
	}
}

// TestProfile_FitShapeDoesNotForceFitNearNormalField is the regression
// half of this story's fit-selection acceptance criterion: a numeric
// field whose source distribution is genuinely close to normal must NOT
// be force-fit into a needlessly complex mixture — the profile document
// still uses the plain normal capture (Shape stays nil) for that field.
func TestProfile_FitShapeDoesNotForceFitNearNormalField(t *testing.T) {
	fs := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	data := synthSingleField(t, 5000, 21, synth.DistNormal, map[string]any{
		"mean": 50.0, "std": 10.0,
	})
	if err := afero.WriteFile(fs, "/normal.pulse", data, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	prof, err := p.Profile(context.Background(), "/normal.pulse", pulse.ProfileOptions{
		FitShape: true,
	})
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	if len(prof.Fields) != 1 || prof.Fields[0].Numeric == nil {
		t.Fatalf("expected one numeric field profile, got %+v", prof.Fields)
	}
	if shape := prof.Fields[0].Numeric.Shape; shape != nil {
		t.Fatalf("expected no Shape for a near-normal field, got %+v", shape)
	}

	// SpecFromProfile must fall back to the ordinary normal
	// reconstruction for this field, exactly as it does today.
	spec, _ := synth.SpecFromProfile(prof, 1000)
	if len(spec.Fields) != 1 || spec.Fields[0].Distribution != synth.DistNormal {
		t.Fatalf("expected DistNormal reconstruction for a near-normal field, got %+v", spec.Fields)
	}
}

// TestProfile_WithoutFitShape_NumericGeneratesAsToday is the other
// regression required by this story: a profile document captured
// WITHOUT --fit-shape (the default) must generate numeric fields
// exactly as before — no "shape" key at all, and SpecFromProfile still
// reconstructing DistNormal — even for a field whose SOURCE is
// genuinely bimodal. Shape fitting must be opt-in, not automatic.
func TestProfile_WithoutFitShape_NumericGeneratesAsToday(t *testing.T) {
	fs := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	data := synthSingleField(t, 4000, 31, synth.DistMixture, map[string]any{
		"means":   []any{-10.0, 10.0},
		"stds":    []any{1.5, 1.5},
		"weights": []any{0.5, 0.5},
	})
	if err := afero.WriteFile(fs, "/bimodal2.pulse", data, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	prof, err := p.Profile(context.Background(), "/bimodal2.pulse", pulse.ProfileOptions{
		IncludeStats: true,
	})
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	if shape := prof.Fields[0].Numeric.Shape; shape != nil {
		t.Fatalf("expected no Shape when --fit-shape was not requested, got %+v", shape)
	}
	raw, err := json.Marshal(prof)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), `"shape"`) {
		t.Fatal(`expected no "shape" key in profile JSON when --fit-shape is off`)
	}

	spec, _ := synth.SpecFromProfile(prof, 1000)
	if len(spec.Fields) != 1 || spec.Fields[0].Distribution != synth.DistNormal {
		t.Fatalf("expected unchanged DistNormal reconstruction without --fit-shape, got %+v", spec.Fields)
	}
}

// TestProfile_FitShapeThenSynth_ReproducesBimodalShape is this story's
// own non-negotiable fidelity gate: a full profile -> generate
// round-trip on a bimodal fixture (mirroring E4-S1's own bimodal test
// shape, means well separated) must capture a mixture representation
// AND reproduce that bimodality in generated rows — a real statistical
// check on the sampled output, not a smoke test that generation merely
// runs without error. Detection logic mirrors
// TestSynth_MixtureReproducesBimodalShape's two-pass peak-and-valley
// approach exactly, applied here to profile-driven regeneration instead
// of a hand-written mixture spec.
func TestProfile_FitShapeThenSynth_ReproducesBimodalShape(t *testing.T) {
	const (
		mean1, std1 = -10.0, 1.5
		mean2, std2 = 10.0, 1.5
	)
	fs := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	data := synthSingleField(t, 8000, 41, synth.DistMixture, map[string]any{
		"means":   []any{mean1, mean2},
		"stds":    []any{std1, std2},
		"weights": []any{0.5, 0.5},
	})
	if err := afero.WriteFile(fs, "/bimodal3.pulse", data, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	prof, err := p.Profile(context.Background(), "/bimodal3.pulse", pulse.ProfileOptions{
		FitShape: true,
	})
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	if prof.Fields[0].Numeric.Shape == nil {
		t.Fatal("expected a captured Shape to drive regeneration — capture step failed upstream of this test")
	}

	spec, _ := synth.SpecFromProfile(prof, 20000)
	if spec.Fields[0].Distribution != synth.DistMixture {
		t.Fatalf("expected SpecFromProfile to emit DistMixture, got %q", spec.Fields[0].Distribution)
	}
	if _, err := p.Synth(context.Background(), spec, "/regen.pulse", pulse.SynthOptions{Seed: 99}); err != nil {
		t.Fatalf("synth from profile: %v", err)
	}

	regen, err := afero.ReadFile(fs, "/regen.pulse")
	if err != nil {
		t.Fatalf("read regenerated cohort: %v", err)
	}
	values := readF64Field(t, regen, "v")
	assertBimodal(t, values, mean1, std1, mean2, std2)
}

// assertBimodal is the two-pass peak-then-valley bimodality check
// shared by this story's round-trip test and E4-S1's own generation
// test — locate the tallest histogram peak, mask a window around it,
// locate the next-tallest remaining peak, then require the two peaks to
// be well separated with a real valley between them.
func assertBimodal(t *testing.T, values []float64, mean1, std1, mean2, std2 float64) {
	t.Helper()
	const binWidth = 1.0
	lo := mean1 - 6*std1
	hi := mean2 + 6*std2
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
