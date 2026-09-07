package synth

import (
	"math/rand/v2"
	"sort"
)

// categoricalPairSampler resamples field B conditioned on field A's
// current (independently-drawn) value, reproducing the pair's captured
// joint co-occurrence pattern (Spec.CategoricalPairs) instead of treating
// A and B as independent marginals. This is the categorical-categorical
// analogue of correlator (synth/copula.go): both overwrite a field's
// independently-drawn value in place, as a post-processing step over the
// same row map, rather than inventing a second draw mechanism.
type categoricalPairSampler struct {
	a, b string
	// perA maps an observed A value to a weighted sampler over B values
	// conditioned on that A value.
	perA map[string]*weightedCategoricalSampler
	// fallback is the marginal B distribution pooled across every
	// captured cell for this pair, used when the row's current A value
	// has no entry in perA — e.g. an A value the joint-cell cap
	// (ContingencyCellCap) folded entirely into the ("other","other")
	// catch-all before this pair's cells were grouped by A. Falling back
	// to the pooled marginal is strictly better than skipping the
	// resample outright: it still respects the pair's captured B shape,
	// just without a specific A-conditional row.
	fallback *weightedCategoricalSampler
}

// buildCategoricalPairSamplers constructs one sampler per declared pair,
// skipping any pair with no cells (defensive; validateSpec already
// refuses this shape for spec input, but SpecFromProfile-derived specs
// take this path directly without validation).
func buildCategoricalPairSamplers(pairs []CategoricalPairSpec) []*categoricalPairSampler {
	if len(pairs) == 0 {
		return nil
	}
	out := make([]*categoricalPairSampler, 0, len(pairs))
	for _, p := range pairs {
		if len(p.Cells) == 0 {
			continue
		}
		out = append(out, buildCategoricalPairSampler(p))
	}
	return out
}

func buildCategoricalPairSampler(p CategoricalPairSpec) *categoricalPairSampler {
	byA := make(map[string]map[string]float64)
	marginalB := make(map[string]float64)
	for _, c := range p.Cells {
		if c.Count <= 0 {
			continue
		}
		row := byA[c.AValue]
		if row == nil {
			row = make(map[string]float64)
			byA[c.AValue] = row
		}
		row[c.BValue] += float64(c.Count)
		marginalB[c.BValue] += float64(c.Count)
	}
	perA := make(map[string]*weightedCategoricalSampler, len(byA))
	for aVal, counts := range byA {
		if smp := weightedSamplerFromCounts(counts); smp != nil {
			perA[aVal] = smp
		}
	}
	return &categoricalPairSampler{
		a: p.A, b: p.B,
		perA:     perA,
		fallback: weightedSamplerFromCounts(marginalB),
	}
}

// weightedSamplerFromCounts builds a deterministic weightedCategoricalSampler
// from a value -> count map. Map iteration order in Go is randomized, and
// this feeds the seeded RNG stream (see the Determinism contract in
// skills/synthetic-data.md: same spec + same seed must produce a
// byte-identical .pulse file) — so keys are sorted before the cumulative
// weight table is built. Returns nil when counts is empty or every weight
// is non-positive.
func weightedSamplerFromCounts(counts map[string]float64) *weightedCategoricalSampler {
	if len(counts) == 0 {
		return nil
	}
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	values := make([]string, 0, len(keys))
	cum := make([]float64, 0, len(keys))
	total := 0.0
	for _, k := range keys {
		w := counts[k]
		if w <= 0 {
			continue
		}
		total += w
		values = append(values, k)
		cum = append(cum, total)
	}
	if total <= 0 {
		return nil
	}
	return &weightedCategoricalSampler{values: values, cum: cum, total: total}
}

// transform overwrites row[b] with a value resampled conditioned on
// row[a]'s current value. A field the RNG already drew a base value for
// (via its own independent weighted_categorical sampler) has that value
// replaced outright, exactly as correlator.transform replaces a numeric
// field's independently-drawn value.
func (c *categoricalPairSampler) transform(rng *rand.Rand, row map[string]any) {
	aVal, _ := row[c.a].(string)
	smp := c.perA[aVal]
	if smp == nil {
		smp = c.fallback
	}
	if smp == nil {
		return
	}
	v, _ := smp.next(rng)
	row[c.b] = v
}

// categoricalNumericPairSampler resamples numeric field B from
// Normal(mean, std) conditioned on categorical field A's current value,
// reproducing the pair's captured per-category conditional mean/std
// (Spec.CategoricalNumericPairs) instead of drawing B from its own
// unconditional normal.
type categoricalNumericPairSampler struct {
	a, b        string
	perCategory map[string]categoryMoments
	fallback    categoryMoments
	hasFallback bool
	hasClamp    bool
	clampMin    float64
	clampMax    float64
}

type categoryMoments struct {
	mean, std float64
}

func buildCategoricalNumericPairSamplers(pairs []CategoricalNumericPairSpec) []*categoricalNumericPairSampler {
	if len(pairs) == 0 {
		return nil
	}
	out := make([]*categoricalNumericPairSampler, 0, len(pairs))
	for _, p := range pairs {
		if len(p.Categories) == 0 {
			continue
		}
		out = append(out, buildCategoricalNumericPairSampler(p))
	}
	return out
}

func buildCategoricalNumericPairSampler(p CategoricalNumericPairSpec) *categoricalNumericPairSampler {
	perCat := make(map[string]categoryMoments, len(p.Categories))
	var sumMean, sumWeight float64
	for _, c := range p.Categories {
		std := c.Std
		if std <= 0 {
			std = 1e-9
		}
		perCat[c.Category] = categoryMoments{mean: c.Mean, std: std}
		sumMean += c.Mean
		sumWeight++
	}
	s := &categoricalNumericPairSampler{
		a: p.A, b: p.B,
		perCategory: perCat,
		hasClamp:    p.HasClamp,
		clampMin:    p.Min,
		clampMax:    p.Max,
	}
	if sumWeight > 0 {
		// A plain average of the captured per-category means/stds, used
		// only when the row's drawn A value has no captured category
		// entry (e.g. it was collapsed to "other" upstream of this
		// pair's own capture). Not weighted by each category's N —
		// that figure is capture-time provenance the Spec-facing
		// CategoricalNumericCategorySpec deliberately drops — but this
		// unweighted pool is a reasonable fallback for what should be a
		// rare path, not a load-bearing statistic.
		var sumStd float64
		for _, m := range perCat {
			sumStd += m.std
		}
		s.fallback = categoryMoments{mean: sumMean / sumWeight, std: sumStd / sumWeight}
		s.hasFallback = true
	}
	return s
}

// transform overwrites row[b] with a value drawn from Normal(mean, std)
// conditioned on row[a]'s current value, clamped to [Min, Max] when the
// pair declared a clamp — exactly mirroring how field B's own
// unconditional `normal` reconstruction clamps to its observed range in
// SpecFromProfile.
func (c *categoricalNumericPairSampler) transform(rng *rand.Rand, row map[string]any) {
	aVal, _ := row[c.a].(string)
	mom, ok := c.perCategory[aVal]
	if !ok {
		if !c.hasFallback {
			return
		}
		mom = c.fallback
	}
	v := mom.mean + rng.NormFloat64()*mom.std
	if c.hasClamp {
		if v < c.clampMin {
			v = c.clampMin
		}
		if v > c.clampMax {
			v = c.clampMax
		}
	}
	row[c.b] = v
}
