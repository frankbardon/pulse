package synth_test

import (
	"math"
	"sort"
	"testing"

	"github.com/frankbardon/pulse/synth"
)

// A u4/u8/u16/u32/u64 column holds an INTEGER, so every path that writes
// it reduces a drawn number with floor(v+0.5). Before the `discrete`
// reconstruction these tests pin, that reduction happened over a value
// drawn from a clamped normal, and what came out was not approximately
// the source scale — it was a bell where the source had a U, a J or a
// spike. Measured on a 122-field survey cohort: `familiarity` (1-7) has
// 25.26% of its rows at level 1 and generated 13.73%; `nps` (0-10) has
// 2.84% at level 0 and generated 0.20%; `sow` (u16, 0-15) has 64.69% at
// level 0 and generated 38.04%.
//
// Every one of those fields' MEANS came back within a few percent, which
// is how it survived. So every assertion below is on the PER-LEVEL
// distribution, and the three paths that can write such a field — its own
// marginal sampler, a conditional pair, and a linear model — are covered
// separately, because a fix to one leaves the others wrong with no signal.

// npsLevels is the motivating cohort's real 0-10 NPS histogram: left
// skewed, a third of its mass on the top level. Nothing like a normal.
var npsLevels = map[float64]float64{
	0: 1883, 1: 1176, 2: 1435, 3: 1889, 4: 2607, 5: 4695,
	6: 4700, 7: 6968, 8: 9919, 9: 9840, 10: 21231,
}

// discreteParams turns a level->count map into the distribution's params
// plus the normalised shares to assert against.
func discreteParams(levels map[float64]float64) (params map[string]any, want map[float64]float64) {
	keys := make([]float64, 0, len(levels))
	total := 0.0
	for k, w := range levels {
		keys = append(keys, k)
		total += w
	}
	sort.Float64s(keys)
	vals := make([]any, 0, len(keys))
	weights := make([]any, 0, len(keys))
	want = make(map[float64]float64, len(keys))
	for _, k := range keys {
		vals = append(vals, k)
		weights = append(weights, levels[k])
		want[k] = levels[k] / total
	}
	return map[string]any{"values": vals, "weights": weights}, want
}

// levelShares counts each observed value's share of a column.
func levelShares(values []float64) map[float64]float64 {
	hist := map[float64]int{}
	for _, v := range values {
		hist[v]++
	}
	out := make(map[float64]float64, len(hist))
	for k, c := range hist {
		out[k] = float64(c) / float64(len(values))
	}
	return out
}

// assertLevelShares checks a realized per-level distribution against its
// target, with the tolerance stated in standard errors of each level's own
// binomial proportion so the bar scales with n rather than being a
// hand-tuned epsilon. Also fails on any value OUTSIDE the declared
// support.
func assertLevelShares(t *testing.T, label string, values []float64, want map[float64]float64, seMultiple float64) {
	t.Helper()
	got := levelShares(values)
	keys := make([]float64, 0, len(want))
	for k := range want {
		keys = append(keys, k)
	}
	sort.Float64s(keys)
	for _, level := range keys {
		se := math.Sqrt(want[level] * (1 - want[level]) / float64(len(values)))
		tol := seMultiple * se
		if math.Abs(got[level]-want[level]) > tol {
			t.Errorf("%s level %v: share %.4f, want %.4f +/- %.4f (%v SE over n=%d)",
				label, level, got[level], want[level], tol, seMultiple, len(values))
		}
	}
	for level := range got {
		if _, declared := want[level]; !declared {
			t.Errorf("%s emitted level %v, which is not in the declared support", label, level)
		}
	}
}

// TestSpecFromProfile_IntegerReconstructsAsDiscrete pins the translation
// that fixes the marginal at its source, and pins that the weights are
// the document's raw COUNTS — a frequency round trip would be the only
// lossy step in an otherwise exact reconstruction.
func TestSpecFromProfile_IntegerReconstructsAsDiscrete(t *testing.T) {
	for _, typeName := range []string{"u4", "u8", "u16", "u32", "u64"} {
		t.Run(typeName, func(t *testing.T) {
			prof := &synth.Profile{
				RowCount: 40000,
				Fields: []synth.FieldProfile{{
					Name: "score",
					Type: typeName,
					Numeric: &synth.NumericProfile{
						Min: 1, Max: 3, Mean: 1.9, Std: 0.8,
						Discrete: &synth.DiscreteProfile{Levels: []synth.DiscreteLevel{
							{Value: 1, Count: 500, Frequency: 0.25},
							{Value: 2, Count: 1200, Frequency: 0.60},
							{Value: 3, Count: 300, Frequency: 0.15},
						}},
					},
				}},
			}
			spec, warnings := synth.SpecFromProfile(prof, 40000)
			if len(warnings) != 0 {
				t.Fatalf("unexpected warnings: %v", warnings)
			}
			fs := spec.Fields[0]
			if fs.Distribution != synth.DistDiscrete {
				t.Fatalf("distribution %q, want %q — a clamped normal rounded by the writer "+
					"cannot reproduce a coded scale's per-level shares",
					fs.Distribution, synth.DistDiscrete)
			}
			vals, _ := fs.Params["values"].([]any)
			weights, _ := fs.Params["weights"].([]any)
			if len(vals) != 3 || len(weights) != 3 {
				t.Fatalf("params %v do not carry three parallel levels", fs.Params)
			}
			for i, want := range []float64{1, 2, 3} {
				if got, _ := vals[i].(float64); got != want {
					t.Errorf("values[%d] = %v, want %v", i, vals[i], want)
				}
			}
			for i, want := range []float64{500, 1200, 300} {
				if got, _ := weights[i].(float64); got != want {
					t.Errorf("weights[%d] = %v, want the captured count %v", i, weights[i], want)
				}
			}
		})
	}
}

// TestSpecFromProfile_IntegerIgnoresCapturedShape is the ordering half of
// the same claim, mirroring the boolean arm's. fitNumericShape will
// happily prefer a two-component mixture on a 7-level integer column — a
// couple of well-separated low-variance humps beat a single normal on BIC
// — and that mixture reproduces the per-level marginal no better while
// costing a bisection per draw. The discrete arm must sit AHEAD of the
// --fit-shape arm, which a reordering would silently undo.
func TestSpecFromProfile_IntegerIgnoresCapturedShape(t *testing.T) {
	prof := &synth.Profile{
		RowCount: 20000,
		Fields: []synth.FieldProfile{{
			Name: "score",
			Type: "u4",
			Numeric: &synth.NumericProfile{
				Min: 1, Max: 7, Mean: 4.0, Std: 2.3,
				Shape: &synth.ShapeProfile{
					Means:   []float64{1.2, 6.6},
					Stds:    []float64{0.4, 0.6},
					Weights: []float64{0.45, 0.55},
				},
				Discrete: &synth.DiscreteProfile{Levels: []synth.DiscreteLevel{
					{Value: 1, Count: 900, Frequency: 0.45},
					{Value: 7, Count: 1100, Frequency: 0.55},
				}},
			},
		}},
	}
	spec, _ := synth.SpecFromProfile(prof, 20000)
	if got := spec.Fields[0].Distribution; got != synth.DistDiscrete {
		t.Fatalf("distribution %q, want %q — the captured shape must not win over the "+
			"exact histogram of a coded scale", got, synth.DistDiscrete)
	}
}

// TestSpecFromProfile_PreDiscreteDocumentReconstructsAsNormal pins the
// additive contract: a document captured before the `discrete` key existed
// carries no histogram, and its integer fields fall back to the
// clamped-normal reconstruction verbatim. Same silent-fallback shape
// `shape` and `conditional.numeric_pairs` already use.
func TestSpecFromProfile_PreDiscreteDocumentReconstructsAsNormal(t *testing.T) {
	prof := &synth.Profile{
		RowCount: 100,
		Fields: []synth.FieldProfile{{
			Name:    "score",
			Type:    "u4",
			Numeric: &synth.NumericProfile{Min: 1, Max: 7, Mean: 4.0, Std: 1.5},
		}},
	}
	spec, _ := synth.SpecFromProfile(prof, 100)
	if got := spec.Fields[0].Distribution; got != synth.DistNormal {
		t.Fatalf("distribution %q, want %q for a document carrying no histogram",
			got, synth.DistNormal)
	}
}

// TestSpecFromProfile_DiscreteTargetKeepsItsModel pins the THIRD writer's
// admission at the translation boundary.
//
// Refusing a discrete target a model would be invisible in the output:
// the per-target retirement rule drops a modelled field's captured
// conditional pair, so a refused model leaves the field with no
// conditioning at all rather than with a latent-scale one. The motivating
// cohort's most-discussed model target (`nps`, a u4) is exactly this case.
func TestSpecFromProfile_DiscreteTargetKeepsItsModel(t *testing.T) {
	prof := &synth.Profile{
		RowCount: 5000,
		Fields: []synth.FieldProfile{
			{
				Name: "region", Type: "categorical_u8",
				Categorical: &synth.CategoricalProfile{Cardinality: 2, Top: []synth.CategoryHit{
					{Value: "east", Weight: 0.6},
					{Value: "west", Weight: 0.4},
				}},
			},
			{
				Name: "nps", Type: "u4",
				Numeric: &synth.NumericProfile{
					Min: 0, Max: 10, Mean: 7.5, Std: 2.6,
					Discrete: &synth.DiscreteProfile{Levels: []synth.DiscreteLevel{
						{Value: 0, Count: 500, Frequency: 0.1},
						{Value: 7, Count: 1500, Frequency: 0.3},
						{Value: 10, Count: 3000, Frequency: 0.6},
					}},
				},
			},
		},
		Models: []synth.FieldModel{{
			Field: "nps", Intercept: 7.5, ResidualStd: 2.0, NObs: 5000, R2: 0.2,
			Predictors: []synth.ModelPredictor{
				{Kind: synth.ModelPredictorCategoricalLevel, Field: "region", Level: "west", Coefficient: -2.2},
			},
		}},
	}
	spec, warnings := synth.SpecFromProfile(prof, 5000)
	for _, w := range warnings {
		t.Logf("warning: %s", w)
	}
	if len(spec.Models) != 1 || spec.Models[0].Field != "nps" {
		t.Fatalf("the discrete target's model was not applied (%d model(s)); a refusal here is "+
			"silent — the field's conditional pair has already been retired for it",
			len(spec.Models))
	}
}

// TestSpecFromProfile_DiscreteTargetKeepsItsConditionalPair pins the
// SECOND writer's admission at the translation boundary, which is a
// separate decision from the pair sampler's own draw: the "both sides must
// have reconstructed to the expected distribution kind" guard has to name
// DistDiscrete or the captured relationship is dropped, silently, for
// exactly the fields this work is about.
func TestSpecFromProfile_DiscreteTargetKeepsItsConditionalPair(t *testing.T) {
	prof := &synth.Profile{
		RowCount: 5000,
		Fields: []synth.FieldProfile{
			{
				Name: "region", Type: "categorical_u8",
				Categorical: &synth.CategoricalProfile{Cardinality: 2, Top: []synth.CategoryHit{
					{Value: "east", Weight: 0.6}, {Value: "west", Weight: 0.4},
				}},
			},
			{
				Name: "nps", Type: "u4",
				Numeric: &synth.NumericProfile{
					Min: 0, Max: 10, Mean: 7.5, Std: 2.6,
					Discrete: &synth.DiscreteProfile{Levels: []synth.DiscreteLevel{
						{Value: 0, Count: 500, Frequency: 0.1},
						{Value: 7, Count: 1500, Frequency: 0.3},
						{Value: 10, Count: 3000, Frequency: 0.6},
					}},
				},
			},
		},
		Conditional: &synth.ConditionalProfile{
			CategoricalNumericPairs: []synth.CategoricalNumericPairProfile{{
				A: "region", B: "nps", N: 5000,
				Categories: []synth.CategoricalNumericCategoryStat{
					{Category: "east", Mean: 8.6, Std: 2.0, N: 3000},
					{Category: "west", Mean: 5.8, Std: 2.8, N: 2000},
				},
			}},
			SetNumericPairs: []synth.SetNumericPairProfile{{
				Set: "channels", Option: "tv", Numeric: "nps", N: 5000,
				Categories: []synth.CategoricalNumericCategoryStat{
					{Category: "selected", Mean: 8.9, Std: 1.9, N: 2500},
					{Category: "not_selected", Mean: 6.1, Std: 2.7, N: 2500},
				},
			}},
		},
	}
	prof.Fields = append(prof.Fields, synth.FieldProfile{
		Name: "channels", Type: "set_u8",
		Set: &synth.SetProfile{N: 5000, Options: []synth.SetOptionStat{
			{Value: "tv", Count: 2500, Frequency: 0.5},
			{Value: "radio", Count: 1000, Frequency: 0.2},
		}},
	})

	spec, warnings := synth.SpecFromProfile(prof, 5000)
	for _, w := range warnings {
		t.Logf("warning: %s", w)
	}
	// BOTH numeric-target pair arms are asserted here, because the
	// "expected distribution kind" guard is written out separately in each
	// and one of them naming `discrete` says nothing about the other.
	if len(spec.CategoricalNumericPairs) != 1 {
		t.Errorf("the captured region->nps pair was dropped (%d pair(s) on the spec); the "+
			"reconstruction guard must name `discrete` or every conditional relationship on an "+
			"integer scale vanishes in translation", len(spec.CategoricalNumericPairs))
	} else if got := spec.CategoricalNumericPairs[0].B; got != "nps" {
		t.Errorf("categorical-numeric pair target %q, want nps", got)
	}
	if len(spec.SetNumericPairs) != 1 {
		t.Errorf("the captured channels.tv->nps pair was dropped (%d pair(s) on the spec)",
			len(spec.SetNumericPairs))
	} else if got := spec.SetNumericPairs[0].Numeric; got != "nps" {
		t.Errorf("set-numeric pair target %q, want nps", got)
	}
}

// TestSynth_DiscreteMarginalPerLevel covers the FIRST writer: the field's
// own sampler. Asserted per level, never on the mean — a clamped normal
// matched to the same moments reproduces this fixture's mean to three
// digits (see TestSynth_DiscreteBeatsTheClampedNormalItReplaced).
func TestSynth_DiscreteMarginalPerLevel(t *testing.T) {
	params, want := discreteParams(npsLevels)
	spec := &synth.Spec{
		RowCount: 60000,
		Fields: []synth.FieldSpec{{
			Name: "nps", Type: "u4",
			Distribution: synth.DistDiscrete, Params: params,
		}},
	}
	data, _, err := synth.SynthBytes(spec, synth.Options{Seed: 3001})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	assertLevelShares(t, "nps marginal", readF64Field(t, data, "nps"), want, 4)
}

// TestSynth_DiscreteBeatsTheClampedNormalItReplaced is the deliberate
// comparison, and the reason the assertions above are per level. The same
// scale reconstructed the old way — normal(mean, std) clamped to the
// observed range, rounded by the writer — matches the MEAN and misses
// levels by double digits.
func TestSynth_DiscreteBeatsTheClampedNormalItReplaced(t *testing.T) {
	params, want := discreteParams(npsLevels)
	var mean, variance float64
	for level, share := range want {
		mean += float64(share * level)
	}
	for level, share := range want {
		d := level - mean
		variance += float64(share * float64(d*d))
	}
	normalSpec := &synth.Spec{
		RowCount: 60000,
		Fields: []synth.FieldSpec{{
			Name: "nps", Type: "u4", Distribution: synth.DistNormal,
			Params: map[string]any{
				"mean": mean, "std": math.Sqrt(variance), "min": 0.0, "max": 10.0,
			},
		}},
	}
	normalData, _, err := synth.SynthBytes(normalSpec, synth.Options{Seed: 3002})
	if err != nil {
		t.Fatalf("SynthBytes(normal): %v", err)
	}
	discreteSpec := &synth.Spec{
		RowCount: 60000,
		Fields: []synth.FieldSpec{{
			Name: "nps", Type: "u4", Distribution: synth.DistDiscrete, Params: params,
		}},
	}
	discreteData, _, err := synth.SynthBytes(discreteSpec, synth.Options{Seed: 3002})
	if err != nil {
		t.Fatalf("SynthBytes(discrete): %v", err)
	}

	worst := func(values []float64) (float64, float64) {
		got := levelShares(values)
		var e, at float64
		for level, share := range want {
			if d := math.Abs(got[level] - share); d > e {
				e, at = d, level
			}
		}
		return e, at
	}
	normalErr, normalAt := worst(readF64Field(t, normalData, "nps"))
	discreteErr, discreteAt := worst(readF64Field(t, discreteData, "nps"))
	t.Logf("worst per-level error: clamped normal %.4f at level %v | discrete %.4f at level %v",
		normalErr, normalAt, discreteErr, discreteAt)

	// The asymmetry IS the finding, and it is stated in RELATIVE terms
	// because that is the comparison that makes it a finding: the summary
	// statistic everyone checks is off by a few percent while the shares
	// nobody checks are off by a quarter of their own size. On the real
	// cohort the same fields' means came back within ~3%.
	normalMean := columnMean(readF64Field(t, normalData, "nps"))
	meanRel := math.Abs(normalMean-mean) / mean
	levelRel := normalErr / want[normalAt]
	t.Logf("clamped normal: mean %.4f vs %.4f (%.1f%% off) | level %v share off by %.1f%% of itself",
		normalMean, mean, 100*meanRel, normalAt, 100*levelRel)
	if meanRel > 0.05 {
		t.Fatalf("the clamped-normal run's mean is %.1f%% off; the fixture is no longer "+
			"demonstrating that the MEAN broadly survives while the levels do not, which is "+
			"how this defect went unnoticed", 100*meanRel)
	}
	if levelRel < 4*meanRel {
		t.Fatalf("the clamped normal's worst per-level relative error (%.1f%%) is not much worse "+
			"than its mean error (%.1f%%); the fixture no longer demonstrates the asymmetry "+
			"this distribution exists to remove", 100*levelRel, 100*meanRel)
	}
	if normalErr < 0.05 {
		t.Fatalf("the clamped-normal reconstruction is within %.4f per level; the fixture no "+
			"longer demonstrates the defect this distribution removes", normalErr)
	}
	if discreteErr >= normalErr/4 {
		t.Errorf("discrete's worst per-level error is %.4f against the clamped normal's %.4f; "+
			"the exact histogram must be dramatically better, not marginally",
			discreteErr, normalErr)
	}
}

func columnMean(values []float64) float64 {
	var sum float64
	for _, v := range values {
		sum += v
	}
	return sum / float64(len(values))
}

// discretePairSpec builds a categorical -> integer conditional pair whose
// cells shift the scale hard in both directions.
func discretePairSpec(rows int, targetDiscrete bool) *synth.Spec {
	params, _ := discreteParams(npsLevels)
	target := synth.FieldSpec{
		Name: "nps", Type: "u4", Distribution: synth.DistDiscrete, Params: params,
	}
	if !targetDiscrete {
		target = synth.FieldSpec{
			Name: "nps", Type: "u4", Distribution: synth.DistNormal,
			Params: map[string]any{"mean": 7.5, "std": 2.66, "min": 0.0, "max": 10.0},
		}
	}
	return &synth.Spec{
		RowCount: rows,
		Fields: []synth.FieldSpec{
			{
				Name: "region", Type: "categorical_u8",
				Distribution: synth.DistWeightedCategorical,
				Params: map[string]any{
					"values":  []any{"north", "south", "east", "west"},
					"weights": []any{0.25, 0.25, 0.25, 0.25},
				},
			},
			target,
		},
		CategoricalNumericPairs: []synth.CategoricalNumericPairSpec{{
			A: "region", B: "nps",
			Min: 0, Max: 10, HasClamp: true,
			Categories: []synth.CategoricalNumericCategorySpec{
				{Category: "north", Mean: 9.2, Std: 1.2},
				{Category: "south", Mean: 7.6, Std: 2.4},
				{Category: "east", Mean: 5.9, Std: 2.6},
				{Category: "west", Mean: 3.4, Std: 2.5},
			},
		}},
	}
}

// identityCellPairSpec is discretePairSpec with every cell carrying the
// TARGET'S OWN moments. A conditional pair that says nothing must
// reproduce the field's own marginal, and that is the sharpest available
// test of the per-cell draw: under the normal arm this spec is exactly the
// clamped-normal reconstruction the discrete arm replaces, applied once
// per cell.
// npsMoments returns the fixture staircase's own exact mean and std.
func npsMoments() (mean, std float64) {
	_, want := discreteParams(npsLevels)
	var variance float64
	for level, share := range want {
		mean += float64(share * level)
	}
	for level, share := range want {
		d := level - mean
		variance += float64(share * float64(d*d))
	}
	return mean, math.Sqrt(variance)
}

func identityCellPairSpec(rows int, targetDiscrete bool) *synth.Spec {
	spec := discretePairSpec(rows, targetDiscrete)
	mean, std := npsMoments()
	for i := range spec.CategoricalNumericPairs[0].Categories {
		spec.CategoricalNumericPairs[0].Categories[i].Mean = mean
		spec.CategoricalNumericPairs[0].Categories[i].Std = std
	}
	return spec
}

// TestSynth_DiscretePairKeepsTheScaleAndCarriesTheContrast covers the
// SECOND writer, and asserts BOTH halves in one test on purpose: each is
// trivially satisfiable by abandoning the other. A pair that ignores its
// cells keeps the scale perfectly and carries no contrast; a pair that
// draws each cell's normal carries the contrast and destroys the scale
// (which is the defect, once per cell).
func TestSynth_DiscretePairKeepsTheScaleAndCarriesTheContrast(t *testing.T) {
	const rows = 80000
	_, want := discreteParams(npsLevels)

	// HALF ONE — the SCALE. Every cell carries the field's own moments, so
	// the pair is saying nothing and must give back the field's own
	// per-level marginal. This is the half a per-cell continuous draw
	// fails, and failing it is the defect.
	identity, _, err := synth.SynthBytes(identityCellPairSpec(rows, true), synth.Options{Seed: 3100})
	if err != nil {
		t.Fatalf("SynthBytes(identity cells): %v", err)
	}
	assertLevelShares(t, "identity-cell pair", readF64Field(t, identity, "nps"), want, 5)

	data, _, err := synth.SynthBytes(discretePairSpec(rows, true), synth.Options{Seed: 3101})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	scores := readF64Field(t, data, "nps")
	regions := readCategoricalField(t, data, "region")
	for level := range levelShares(scores) {
		if _, declared := want[level]; !declared {
			t.Errorf("the pair emitted level %v, which is not in the target's declared support", level)
		}
	}

	// HALF TWO: the cells order the rows, monotonically in the declared
	// cell means. Latent-scale shifts do not preserve the cell mean in
	// scale points, so ORDER is what is asserted — see
	// conditionalDiscreteDraw.
	byRegion := map[string][]float64{}
	for i := range scores {
		byRegion[regions[i]] = append(byRegion[regions[i]], scores[i])
	}
	order := []string{"west", "east", "south", "north"}
	var last float64 = -1
	for _, region := range order {
		vals, ok := byRegion[region]
		if !ok || len(vals) == 0 {
			t.Fatalf("region %q absent from generated output", region)
		}
		m := columnMean(vals)
		t.Logf("cell %-6s n=%5d mean=%.3f", region, len(vals), m)
		if m <= last {
			t.Errorf("cell %q mean %.3f does not exceed the previous cell's %.3f; the "+
				"conditional shift is not carrying the captured contrast", region, m, last)
		}
		last = m
	}
	if spread := columnMean(byRegion["north"]) - columnMean(byRegion["west"]); spread < 2 {
		t.Errorf("north-west cell spread is only %.3f scale points; the pair is barely "+
			"conditioning at all", spread)
	}
}

// TestSynth_DiscretePairOnANormalTargetFlattensTheScale is the deliberate
// negative. The continuous arm is RETAINED for a target that reconstructed
// as `normal`, and against a coded scale it is wrong rather than merely
// poorer — stated plainly so nobody reads the retained arm as an equally
// valid choice, and so a future attempt to unify the two has to confront
// the number.
func TestSynth_DiscretePairOnANormalTargetFlattensTheScale(t *testing.T) {
	const rows = 80000
	_, want := discreteParams(npsLevels)
	data, _, err := synth.SynthBytes(identityCellPairSpec(rows, false), synth.Options{Seed: 3102})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	got := levelShares(readF64Field(t, data, "nps"))
	var worst, at float64
	for level, share := range want {
		if d := math.Abs(got[level] - share); d > worst {
			worst, at = d, level
		}
	}
	t.Logf("normal-target pair: worst per-level error %.4f at level %v (top level %.4f vs %.4f)",
		worst, at, got[10], want[10])
	if worst < 0.05 {
		t.Errorf("the normal-target pair is within %.4f per level, so this negative control "+
			"proves nothing; the whole point is that a per-cell continuous draw destroys the "+
			"scale the discrete arm preserves", worst)
	}
}

// discreteModelSpec regresses an integer target on a categorical
// predictor through Spec.Models.
func discreteModelSpec(rows int) *synth.Spec {
	params, _ := discreteParams(npsLevels)
	return &synth.Spec{
		RowCount: rows,
		Fields: []synth.FieldSpec{
			{
				Name: "region", Type: "categorical_u8",
				Distribution: synth.DistWeightedCategorical,
				Params: map[string]any{
					"values":  []any{"north", "south", "west"},
					"weights": []any{1.0, 1.0, 1.0},
				},
			},
			{Name: "nps", Type: "u4", Distribution: synth.DistDiscrete, Params: params},
		},
		Models: []synth.FieldModelSpec{{
			Field:       "nps",
			Intercept:   7.5,
			ResidualStd: 2.0,
			Min:         0, Max: 10, HasClamp: true,
			Predictors: []synth.ModelPredictorSpec{
				{Kind: synth.ModelPredictorCategoricalLevel, Field: "region", Level: "north", Coefficient: 2.4},
				{Kind: synth.ModelPredictorCategoricalLevel, Field: "region", Level: "west", Coefficient: -3.1},
			},
		}},
	}
}

// identityModelSpec carries a model that says NOTHING: its intercept is
// the field's own mean, its residual scale the field's own std, and it has
// no predictors. Such a model must give back the field's own per-level
// marginal exactly (u collapses to z, so value = Q(Phi(z))), and that is
// the sharpest available test of the model-path Q — under a normal Q it is
// the clamped-normal reconstruction the discrete arm replaces.
func identityModelSpec(rows int) *synth.Spec {
	spec := discreteModelSpec(rows)
	mean, std := npsMoments()
	spec.Models[0].Intercept = mean
	spec.Models[0].ResidualStd = std
	spec.Models[0].Predictors = nil
	return spec
}

// TestSynthModel_DiscreteTargetKeepsItsLevelsAndOrdersByPredictor covers
// the THIRD writer. Both halves in ONE test, for the same reason the pair
// test does it: a model that says nothing keeps the marginal perfectly and
// orders nothing, and a model that overwrites the value with a continuous
// draw carries the ordering and destroys the marginal.
func TestSynthModel_DiscreteTargetKeepsItsLevelsAndOrdersByPredictor(t *testing.T) {
	const rows = 60000
	_, want := discreteParams(npsLevels)

	// HALF ONE — the MARGINAL, via the identity model.
	identity, _, err := synth.SynthBytes(identityModelSpec(rows), synth.Options{Seed: 3200})
	if err != nil {
		t.Fatalf("SynthBytes(identity model): %v", err)
	}
	assertLevelShares(t, "identity model", readF64Field(t, identity, "nps"), want, 5)

	// HALF TWO — the ORDERING, via the predictor-bearing model.
	data, res, err := synth.SynthBytes(discreteModelSpec(rows), synth.Options{Seed: 3201})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	for _, w := range res.Warnings {
		t.Logf("warning: %s", w)
	}
	scores := readF64Field(t, data, "nps")
	regions := readCategoricalField(t, data, "region")

	for level := range levelShares(scores) {
		if _, declared := want[level]; !declared {
			t.Errorf("the model emitted level %v, which is not in the target's declared support", level)
		}
	}

	byRegion := map[string][]float64{}
	for i := range scores {
		byRegion[regions[i]] = append(byRegion[regions[i]], scores[i])
	}
	west, south, north := columnMean(byRegion["west"]), columnMean(byRegion["south"]), columnMean(byRegion["north"])
	t.Logf("model cell means: west=%.3f south=%.3f north=%.3f", west, south, north)
	if !(west < south && south < north) {
		t.Errorf("cell means %.3f / %.3f / %.3f do not follow the coefficients "+
			"(-3.1 / reference / +2.4); an ordered probit must preserve ordering even though "+
			"it does not preserve magnitude in scale points", west, south, north)
	}
}

// TestSynthModel_DiscreteTargetIsDeterministic pins the byte-identity
// contract on the path that consumes the most RNG state.
func TestSynthModel_DiscreteTargetIsDeterministic(t *testing.T) {
	a, _, err := synth.SynthBytes(discreteModelSpec(5000), synth.Options{Seed: 77})
	if err != nil {
		t.Fatalf("SynthBytes(a): %v", err)
	}
	b, _, err := synth.SynthBytes(discreteModelSpec(5000), synth.Options{Seed: 77})
	if err != nil {
		t.Fatalf("SynthBytes(b): %v", err)
	}
	if string(a) != string(b) {
		t.Error("same spec + same seed produced different bytes")
	}
}

// TestSynthDiscrete_GappedSupportStaysOnItsLevels is the structural
// complement to the three marginal tests: a coded scale need not be
// contiguous (1/3/5/7, or codes with unused middles), and every writer
// must land ON a declared level rather than between two.
//
// A contiguous 0-10 fixture cannot show this — every integer a continuous
// draw rounds to is a declared level there — so the gap is what makes the
// support assertion able to fail at all.
func TestSynthDiscrete_GappedSupportStaysOnItsLevels(t *testing.T) {
	gapped := map[float64]float64{1: 30, 5: 50, 9: 20}
	params, want := discreteParams(gapped)

	base := &synth.Spec{
		RowCount: 40000,
		Fields: []synth.FieldSpec{
			{
				Name: "region", Type: "categorical_u8",
				Distribution: synth.DistWeightedCategorical,
				Params: map[string]any{
					"values":  []any{"north", "west"},
					"weights": []any{1.0, 1.0},
				},
			},
			{Name: "code", Type: "u4", Distribution: synth.DistDiscrete, Params: params},
		},
	}

	marginal := *base
	paired := *base
	paired.CategoricalNumericPairs = []synth.CategoricalNumericPairSpec{{
		A: "region", B: "code", Min: 1, Max: 9, HasClamp: true,
		Categories: []synth.CategoricalNumericCategorySpec{
			{Category: "north", Mean: 8.0, Std: 1.0},
			{Category: "west", Mean: 2.0, Std: 1.0},
		},
	}}
	setPaired := *base
	setPaired.Fields = append([]synth.FieldSpec{}, base.Fields...)
	setPaired.Fields = append(setPaired.Fields, synth.FieldSpec{
		Name: "channels", Type: "set_u8", Distribution: synth.DistSetBernoulli,
		Params: map[string]any{
			"options":     []any{"tv", "radio"},
			"frequencies": []any{0.5, 0.3},
		},
	})
	setPaired.SetNumericPairs = []synth.SetNumericPairSpec{{
		Set: "channels", Option: "tv", Numeric: "code",
		Min: 1, Max: 9, HasClamp: true,
		Categories: []synth.CategoricalNumericCategorySpec{
			{Category: "selected", Mean: 8.0, Std: 1.0},
			{Category: "not_selected", Mean: 2.0, Std: 1.0},
		},
	}}

	modelled := *base
	modelled.Models = []synth.FieldModelSpec{{
		Field: "code", Intercept: 5.0, ResidualStd: 2.0,
		Min: 1, Max: 9, HasClamp: true,
		Predictors: []synth.ModelPredictorSpec{
			{Kind: synth.ModelPredictorCategoricalLevel, Field: "region", Level: "north", Coefficient: 3.0},
		},
	}}

	for _, tc := range []struct {
		writer string
		spec   *synth.Spec
		seed   uint64
	}{
		{"own sampler", &marginal, 41},
		{"categorical-numeric pair", &paired, 42},
		{"set-numeric pair", &setPaired, 44},
		{"linear model", &modelled, 43},
	} {
		t.Run(tc.writer, func(t *testing.T) {
			data, _, err := synth.SynthBytes(tc.spec, synth.Options{Seed: int64(tc.seed)})
			if err != nil {
				t.Fatalf("SynthBytes: %v", err)
			}
			for level, share := range levelShares(readF64Field(t, data, "code")) {
				if _, declared := want[level]; !declared {
					t.Errorf("emitted level %v on %.4f of rows; the declared support is "+
						"{1, 5, 9} and a value between two levels is not a code the scale has",
						level, share)
				}
			}
		})
	}
}
