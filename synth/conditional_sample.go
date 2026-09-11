package synth

import (
	"fmt"
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
	// bernoulli is true when B's own marginal is a `bernoulli` step, so
	// each cell's Mean is a prevalence rather than a location. DERIVED
	// from B's FieldSpec, never read off the pair — see
	// buildBernoulliConditionals and conditionalNumericDraw.
	bernoulli bool
	// discrete is non-nil when B reconstructed as `discrete`: the cell's
	// moments then locate a draw on B's own staircase rather than on a
	// normal. Derived from B's FieldSpec, never from a wire flag — see
	// discreteConditional.
	discrete *discreteConditional
}

type categoryMoments struct {
	mean, std float64
}

func buildCategoricalNumericPairSamplers(pairs []CategoricalNumericPairSpec, disc map[string]*discreteConditional, bern map[string]bool) []*categoricalNumericPairSampler {
	if len(pairs) == 0 {
		return nil
	}
	out := make([]*categoricalNumericPairSampler, 0, len(pairs))
	for _, p := range pairs {
		if len(p.Categories) == 0 {
			continue
		}
		out = append(out, buildCategoricalNumericPairSampler(p, disc[p.B], bern[p.B]))
	}
	return out
}

func buildCategoricalNumericPairSampler(p CategoricalNumericPairSpec, disc *discreteConditional, bern bool) *categoricalNumericPairSampler {
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
		bernoulli:   bern,
		discrete:    disc,
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
	var v float64
	if c.discrete != nil {
		v = conditionalDiscreteDraw(rng, mom, c.discrete)
	} else {
		v = conditionalNumericDraw(rng, mom, c.bernoulli)
	}
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

// conditionalNumericDraw turns one cell's captured moments into the value
// the pair writes into the row.
//
// The bernoulli arm is not an optimisation of the normal one — it draws a
// DIFFERENT thing. A boolean target's field holds one bit, so a
// continuous conditional draw must be reduced to 0 or 1 downstream, and
// no threshold over a clamped normal reproduces the cell's prevalence
// (that is the defect the field's own bernoulli reconstruction removes;
// see SpecFromProfile). Here the cell's Mean IS the prevalence, so one
// uniform draw against it is exact and mom.std is deliberately unused.
//
// Both arms consume exactly one RNG value, but NOT the same one — a
// Float64 is one uint64 pull where a NormFloat64 may take several. That
// is fine and is the ordinary rule for this package: the per-row draw
// sequence must be a function of the SPEC, which the bernoulli flag is
// part of, not a function of the data. Two specs differing in the flag
// produce different streams by design.
func conditionalNumericDraw(rng *rand.Rand, mom categoryMoments, bernoulli bool) float64 {
	if bernoulli {
		if rng.Float64() < mom.mean {
			return 1
		}
		return 0
	}
	return mom.mean + float64(rng.NormFloat64()*mom.std)
}

// discreteConditional is the target-side half of a conditional pair whose
// numeric target reconstructed as `discrete`: the field's OWN marginal
// (its staircase Q, plus the moments to standardise a cell's location
// against).
//
// It is resolved from the target's FieldSpec at generate() setup time
// rather than carried on the pair spec. That is deliberate and is NOT
// symmetric with SetNumericPairSpec.Bernoulli, which IS a wire flag: a
// flag can disagree with the field's own reconstruction, and a marginal
// disagreeing with the draw that overwrites it is the whole failure class
// this work removes. Deriving it makes the disagreement unrepresentable,
// and it means a HAND-AUTHORED spec that puts `discrete` on a field gets
// the right conditional draw with no extra key to remember.
type discreteConditional struct {
	mean   float64
	invStd float64
	levels discreteLevels
}

// buildDiscreteConditionals resolves one entry per field whose declared
// distribution is `discrete`. Returns nil when the spec declares none, so
// a spec without them allocates nothing and every pair sampler keeps its
// pre-existing draw exactly.
//
// Params are already known-good by the time this runs — buildSampler
// parsed the same declaration at schema-build time — but the error is
// propagated rather than swallowed, because a silently dropped entry
// would put the pair back on the clamped-normal draw with nothing saying so.
// buildBernoulliConditionals returns the set of fields whose own
// marginal is a `bernoulli` step, so a conditional pair overwriting one
// of them draws Bernoulli(cell mean) rather than Normal(cell mean, cell
// std).
//
// It is DERIVED from the compiled field specs, exactly as
// buildDiscreteConditionals is, and that is the whole point rather than
// a tidy-up. The fact used to ride the wire, on
// CategoricalNumericPairSpec.Bernoulli / SetNumericPairSpec.Bernoulli,
// and a wire flag can DISAGREE with the field's own reconstruction — a
// marginal disagreeing with the draw that overwrites it is the failure
// class the boolean and small-integer work exists to remove, and the
// disagreement is invisible: every cell still renders a plausible
// prevalence, merely the wrong one.
//
// It was not hypothetical. SpecFromProfile's SET-numeric arm admitted a
// DistBernoulli target with a comment saying the flag was "carried the
// same way" as the categorical-numeric arm's, and did not set it — so a
// profile-derived set-numeric pair over a boolean target reproduced the
// clamped-normal defect once PER CELL, worst exactly where the signal
// is. Deriving makes the omission unrepresentable; nothing has to
// remember to carry it.
//
// Returns nil when no field reconstructs as bernoulli, so a spec with
// none allocates nothing and every pair sampler keeps its pre-existing
// draw exactly.
func buildBernoulliConditionals(wfs []*writerField) map[string]bool {
	var out map[string]bool
	for _, wf := range wfs {
		if wf.spec.Distribution != DistBernoulli {
			continue
		}
		if out == nil {
			out = make(map[string]bool, 4)
		}
		out[wf.spec.Name] = true
	}
	return out
}

// bernoulliFlagWarnings reports every pair still carrying the retired
// `bernoulli` wire flag over a target whose own marginal is NOT a
// bernoulli step.
//
// The flag is no longer read (see buildBernoulliConditionals), and a
// declaration that silently does nothing is the same class of fault as
// the omission that motivated retiring it — so the one shape where
// ignoring it CHANGES the draw is said out loud. The other shape (flag
// set, target bernoulli) is silent on purpose: the flag and the
// derivation agree, so there is nothing to report and every
// profile-derived spec written before the retirement stays quiet.
//
// Walks the two slices in order and never a map, so the lines are
// deterministic.
func bernoulliFlagWarnings(catNum []CategoricalNumericPairSpec, setNum []SetNumericPairSpec, bern map[string]bool) []string {
	var out []string
	line := func(kind, axis, target string) string {
		return fmt.Sprintf("conditional pair %s (%s -> %s) declares the retired `bernoulli` flag, "+
			"but %q does not reconstruct as a bernoulli marginal; the flag is ignored and the cell draw "+
			"follows the field's own marginal. Declare the field `bernoulli` if that is what it is",
			kind, axis, target, target)
	}
	for _, p := range catNum {
		if p.Bernoulli && !bern[p.B] {
			out = append(out, line("categorical-numeric", p.A, p.B))
		}
	}
	for _, p := range setNum {
		if p.Bernoulli && !bern[p.Numeric] {
			out = append(out, line("set-numeric", p.Set+"."+p.Option, p.Numeric))
		}
	}
	return out
}

func buildDiscreteConditionals(wfs []*writerField) (map[string]*discreteConditional, error) {
	var out map[string]*discreteConditional
	for _, wf := range wfs {
		if wf.spec.Distribution != DistDiscrete {
			continue
		}
		levels, err := parseDiscreteLevels(wf.spec)
		if err != nil {
			return nil, err
		}
		if levels.std <= 0 {
			// A single-level support is a constant column: there is no
			// scale to read a cell's location against, and every cell
			// would produce the one level anyway. Leaving it out means
			// the pair falls back to its ordinary draw, which the clamp
			// then pins to the same single value.
			continue
		}
		if out == nil {
			out = make(map[string]*discreteConditional, 4)
		}
		out[wf.spec.Name] = &discreteConditional{
			mean: levels.mean, invStd: 1 / levels.std, levels: levels,
		}
	}
	return out, nil
}

// conditionalDiscreteDraw turns one cell's captured moments into a level
// of the target's own staircase.
//
// It is the SAME composed construction the model stage uses
// (modelDrawer.transform): standardise the cell's location against the
// field's own marginal moments, add the cell's own spread on that scale,
// and read the result through the field's quantile function —
// value = Q(Phi(u)), u = (cellMean - fieldMean)/fieldStd + (cellStd/fieldStd)*z.
// Reusing it rather than inventing a second conditional construction is
// the point: a modelled target and a paired target then disagree about
// nothing except where their location came from.
//
// Two consequences, both deliberate. (1) The target's own MARGINAL
// survives the pair: every emitted value is a declared level at a
// declared share, which the alternative (draw the cell's normal, let the
// writer round it) destroys per cell — the defect the field-level
// reconstruction removes, reintroduced once for every category. (2) The
// shift is LATENT-scale, so the cell's mean is reproduced in ORDER and
// direction but not exactly in field units; a coefficient-free cell whose
// moments equal the field's reproduces the field's marginal exactly. That
// is the same non-linearity mixture and bernoulli targets carry, and the
// value-scale alternative is what destroys the marginal.
//
// Consumes exactly one NormFloat64 per call — the same draw count as the
// normal arm it replaces, though not the same values. The per-row RNG
// sequence stays a function of the SPEC.
func conditionalDiscreteDraw(rng *rand.Rand, mom categoryMoments, dc *discreteConditional) float64 {
	z := rng.NormFloat64()
	u := float64((mom.mean-dc.mean)*dc.invStd) + float64((mom.std*dc.invStd)*z)
	return dc.levels.quantile(phi(u))
}

// setOptionMap fetches the map[string]bool a setSampler produced for
// set_* field name in row, or nil when absent/wrong-typed — every
// set-option transform below guards on this before mutating a specific
// option's entry in place.
func setOptionMap(row map[string]any, field string) map[string]bool {
	m, _ := row[field].(map[string]bool)
	return m
}

// setCategoricalPairSampler resamples one set_* field's declared Option
// (bit) conditioned on a paired categorical field's already-drawn value,
// reproducing the pair's captured joint selection rate — the set-field
// analogue of categoricalPairSampler, with the target axis fixed to the
// two-valued selected/not_selected Bernoulli domain instead of an
// arbitrary B value. Deliberately NOT built by re-orienting
// categoricalPairSampler (which would require transposing AValue/BValue
// and routing through a synthetic row key) — a small purpose-built
// sampler is simpler to reason about and keeps every accumulation in
// SetCategoricalPairSpec.Cells' own deterministic slice order, never a
// map range, so the same spec + seed still produces byte-identical
// output (see skills/synthetic-data.md's Determinism contract).
type setCategoricalPairSampler struct {
	set, option, categorical string
	// perCategory maps an observed categorical value to P(selected).
	perCategory map[string]float64
	fallback    float64
	hasFallback bool
}

func buildSetCategoricalPairSamplers(pairs []SetCategoricalPairSpec) []*setCategoricalPairSampler {
	if len(pairs) == 0 {
		return nil
	}
	out := make([]*setCategoricalPairSampler, 0, len(pairs))
	for _, p := range pairs {
		if len(p.Cells) == 0 {
			continue
		}
		out = append(out, buildSetCategoricalPairSampler(p))
	}
	return out
}

func buildSetCategoricalPairSampler(p SetCategoricalPairSpec) *setCategoricalPairSampler {
	type agg struct{ selected, total float64 }
	byCat := make(map[string]*agg, len(p.Cells))
	var sumSelected, sumTotal float64
	// Iterate p.Cells (a deterministic slice, fixed at capture time) —
	// never range a map — so floating-point accumulation order, and
	// therefore the fallback probability's exact bits, never depends on
	// Go's randomized map iteration.
	for _, c := range p.Cells {
		if c.Count <= 0 {
			continue
		}
		a := byCat[c.BValue]
		if a == nil {
			a = &agg{}
			byCat[c.BValue] = a
		}
		cnt := float64(c.Count)
		a.total += cnt
		sumTotal += cnt
		if c.AValue == "selected" {
			a.selected += cnt
			sumSelected += cnt
		}
	}
	perCat := make(map[string]float64, len(byCat))
	for k, a := range byCat {
		if a.total > 0 {
			perCat[k] = a.selected / a.total
		}
	}
	s := &setCategoricalPairSampler{set: p.Set, option: p.Option, categorical: p.Categorical, perCategory: perCat}
	if sumTotal > 0 {
		s.fallback = sumSelected / sumTotal
		s.hasFallback = true
	}
	return s
}

// transform overwrites the set field's declared Option entry with a
// fresh Bernoulli(P(selected | row[categorical])) draw. A no-op when the
// set field's own sampler didn't produce a map (defensive; setSampler
// always does).
func (s *setCategoricalPairSampler) transform(rng *rand.Rand, row map[string]any) {
	m := setOptionMap(row, s.set)
	if m == nil {
		return
	}
	catVal, _ := row[s.categorical].(string)
	p, ok := s.perCategory[catVal]
	if !ok {
		if !s.hasFallback {
			return
		}
		p = s.fallback
	}
	m[s.option] = rng.Float64() < p
}

// setNumericPairSampler resamples a numeric field from Normal(mean, std)
// conditioned on whether a paired set_* field's declared Option (bit)
// was independently drawn selected — the set-field analogue of
// categoricalNumericPairSampler, with the conditioning axis fixed to the
// two-valued selected/not_selected domain.
type setNumericPairSampler struct {
	set, option, numeric        string
	selected, notSelected       categoryMoments
	hasSelected, hasNotSelected bool
	fallback                    categoryMoments
	hasFallback                 bool
	hasClamp                    bool
	clampMin, clampMax          float64
	// bernoulli mirrors categoricalNumericPairSampler.bernoulli and is
	// DERIVED the same way, from the numeric target's own FieldSpec.
	bernoulli bool
	// discrete mirrors categoricalNumericPairSampler.discrete.
	discrete *discreteConditional
}

func buildSetNumericPairSamplers(pairs []SetNumericPairSpec, disc map[string]*discreteConditional, bern map[string]bool) []*setNumericPairSampler {
	if len(pairs) == 0 {
		return nil
	}
	out := make([]*setNumericPairSampler, 0, len(pairs))
	for _, p := range pairs {
		if len(p.Categories) == 0 {
			continue
		}
		out = append(out, buildSetNumericPairSampler(p, disc[p.Numeric], bern[p.Numeric]))
	}
	return out
}

func buildSetNumericPairSampler(p SetNumericPairSpec, disc *discreteConditional, bern bool) *setNumericPairSampler {
	s := &setNumericPairSampler{
		set: p.Set, option: p.Option, numeric: p.Numeric,
		hasClamp: p.HasClamp, clampMin: p.Min, clampMax: p.Max,
		bernoulli: bern,
		discrete:  disc,
	}
	// p.Categories is a short (<=2), deterministically ordered slice —
	// accumulate the fallback moments straight from it, never a map.
	var sumMean, sumStd, n float64
	for _, c := range p.Categories {
		std := c.Std
		if std <= 0 {
			std = 1e-9
		}
		mom := categoryMoments{mean: c.Mean, std: std}
		switch c.Category {
		case "selected":
			s.selected, s.hasSelected = mom, true
		case "not_selected":
			s.notSelected, s.hasNotSelected = mom, true
		}
		sumMean += c.Mean
		sumStd += std
		n++
	}
	if n > 0 {
		s.fallback = categoryMoments{mean: sumMean / n, std: sumStd / n}
		s.hasFallback = true
	}
	return s
}

func (s *setNumericPairSampler) transform(rng *rand.Rand, row map[string]any) {
	m := setOptionMap(row, s.set)
	selected := m != nil && m[s.option]
	var mom categoryMoments
	var ok bool
	switch {
	case selected && s.hasSelected:
		mom, ok = s.selected, true
	case !selected && s.hasNotSelected:
		mom, ok = s.notSelected, true
	}
	if !ok {
		if !s.hasFallback {
			return
		}
		mom = s.fallback
	}
	var v float64
	if s.discrete != nil {
		v = conditionalDiscreteDraw(rng, mom, s.discrete)
	} else {
		v = conditionalNumericDraw(rng, mom, s.bernoulli)
	}
	if s.hasClamp {
		if v < s.clampMin {
			v = s.clampMin
		}
		if v > s.clampMax {
			v = s.clampMax
		}
	}
	row[s.numeric] = v
}

// setSetPairSampler resamples set field B's declared OptionB (bit)
// conditioned on set field A's declared OptionA — already independently
// drawn — reproducing the pair's captured 2x2 contingency structure
// between two DIFFERENT set_* fields.
type setSetPairSampler struct {
	setA, optionA, setB, optionB            string
	pGivenASelected, pGivenANotSelected     float64
	hasGivenASelected, hasGivenANotSelected bool
	fallback                                float64
	hasFallback                             bool
}

func buildSetSetPairSamplers(pairs []SetSetPairSpec) []*setSetPairSampler {
	if len(pairs) == 0 {
		return nil
	}
	out := make([]*setSetPairSampler, 0, len(pairs))
	for _, p := range pairs {
		if len(p.Cells) == 0 {
			continue
		}
		out = append(out, buildSetSetPairSampler(p))
	}
	return out
}

func buildSetSetPairSampler(p SetSetPairSpec) *setSetPairSampler {
	var selSel, selTotal, notSel, notTotal, sumSelected, sumTotal float64
	for _, c := range p.Cells {
		if c.Count <= 0 {
			continue
		}
		cnt := float64(c.Count)
		sumTotal += cnt
		bSelected := c.BValue == "selected"
		if bSelected {
			sumSelected += cnt
		}
		if c.AValue == "selected" {
			selTotal += cnt
			if bSelected {
				selSel += cnt
			}
		} else {
			notTotal += cnt
			if bSelected {
				notSel += cnt
			}
		}
	}
	s := &setSetPairSampler{setA: p.SetA, optionA: p.OptionA, setB: p.SetB, optionB: p.OptionB}
	if selTotal > 0 {
		s.pGivenASelected, s.hasGivenASelected = selSel/selTotal, true
	}
	if notTotal > 0 {
		s.pGivenANotSelected, s.hasGivenANotSelected = notSel/notTotal, true
	}
	if sumTotal > 0 {
		s.fallback, s.hasFallback = sumSelected/sumTotal, true
	}
	return s
}

func (s *setSetPairSampler) transform(rng *rand.Rand, row map[string]any) {
	mb := setOptionMap(row, s.setB)
	if mb == nil {
		return
	}
	ma := setOptionMap(row, s.setA)
	aSelected := ma != nil && ma[s.optionA]
	var p float64
	var ok bool
	switch {
	case aSelected && s.hasGivenASelected:
		p, ok = s.pGivenASelected, true
	case !aSelected && s.hasGivenANotSelected:
		p, ok = s.pGivenANotSelected, true
	}
	if !ok {
		if !s.hasFallback {
			return
		}
		p = s.fallback
	}
	mb[s.optionB] = rng.Float64() < p
}
