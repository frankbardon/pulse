package synth

import (
	"math"
	"sort"

	"github.com/frankbardon/pulse/encoding"
)

// This file is the PREDICTOR SELECTION stage of `profile create
// --fit-models`. It sits between the capture's retained row sample and
// the design matrices profile_models.go hands the OLS engine, and it
// answers two separate questions in a fixed order.
//
//  1. STRUCTURAL — which LEVELS of a candidate categorical get a design
//     column at all. Answered by the top-K collapse the rest of profile
//     capture already applies (ProfileOptions.TopK, out-of-top-K values
//     folded into a single otherCategoryLabel bucket).
//  2. STATISTICAL — which candidate FIELDS enter a given numeric
//     target's model. Answered by variance explained against an
//     absolute floor (minVarianceExplained).
//
// The order is not cosmetic. Before the collapse existed, a cohort with
// a 1,906-level `brand` beside fifteen other categoricals expanded to
// 2,420 design columns and EVERY numeric target was skipped as too wide
// — the flag was inert on the cohort it was built for. The collapse
// takes the same cohort to under 200 columns on its own, with no
// statistical criterion involved; the floor then narrows that to the
// handful of predictors that carry the field.
//
// # Why variance explained and NOT significance
//
// This is the single most important property in the file and the one
// most likely to be "improved" into a bug. At 381k rows every candidate
// is statistically significant, including candidates explaining a
// thousandth of the target's variance: the standard error shrinks like
// 1/sqrt(n) while the effect does not, so a p-value gate admits
// everything and is "all predictors enter" wearing a lab coat. An
// ABSOLUTE floor on the share of variance a predictor accounts for is
// n-invariant by construction — the same underlying effect admits at 1k
// rows and at 100k rows, and a negligible effect is rejected at both.
// TestModelSelection_AdmissionInvariantToRowCount is the build-failing
// guard on that; a p-value, an F statistic or an n-scaled threshold
// appearing anywhere in this file breaks it.
//
// # Which "variance explained"
//
// MARGINAL, one-way, and ADJUSTED: for each (candidate, target) pair
// independently, the share of the target's total sum of squares lying
// BETWEEN the candidate's groups, corrected for the degrees of freedom
// those groups spend. Two consequences worth stating because neither is
// the obvious reading:
//
//   - Marginal, not incremental. Candidates are scored one at a time
//     against the raw target, never against the residual of an
//     already-admitted predictor. Two collinear candidates (`age` is a
//     banding of `ageExact` on the motivating cohort; `region` is nested
//     inside `dma`) therefore BOTH clear the floor and both enter. That
//     is a deliberate judgement call: incremental scoring would make the
//     admitted set depend on the order candidates are considered in, and
//     the redundancy it exists to remove is exactly what a least-squares
//     fit is equipped to detect.
//
//     Redundancy is resolved by the SOLVER rather than by a second rule
//     here, and the score above is what makes that possible: a design
//     the solver refuses as rank-deficient is refitted with its WEAKEST
//     admitted predictor dropped (see maxModelRefitRounds in
//     profile_models.go), so the candidate that survives a nesting is
//     the one explaining more of the target. Detecting the nesting
//     directly is deliberately not attempted — the dependency that
//     actually bites is LINEAR, not functional (a retained set of exact
//     ages that happens to cover an entire age band makes that band's
//     indicator the sum of theirs), and a functional-dependency probe
//     misses it while claiming to have handled it.
//
//   - Adjusted, not raw. Raw η² is biased UP by roughly (k−1)/(N−1) for
//     a candidate with k groups and no real effect, so a raw floor would
//     admit a 33-group collapsed categorical purely for being wide, and
//     would do it more readily on a smaller sample. The adjustment
//     (identical in form to adjusted R²) removes exactly that term, which
//     is what makes the floor comparable across candidates of different
//     cardinality AND across row counts.

const (
	// minVarianceExplained is the absolute floor a candidate predictor's
	// adjusted variance explained must reach to enter a numeric target's
	// model.
	//
	// 0.01 is Cohen's conventional boundary for a SMALL effect in η²
	// terms (0.01 small / 0.06 medium / 0.14 large), and "explains at
	// least one percent of this field's variance" is the weakest claim
	// that is still a claim: below it a predictor moves the fitted value
	// by less than a tenth of a standard deviation across its whole
	// range, which is invisible in generated data and costs a design
	// column plus its share of an O(p²) accumulator on every retained
	// row.
	//
	// It is deliberately a CONSTANT and not an option. Exposing it would
	// invite per-run tuning of a threshold whose whole purpose is to be
	// stable across cohorts, and the story that introduced it is
	// explicit that selection carries no user-facing override. Tune it
	// HERE, against real cohorts, the way MinPairObservations is tuned.
	//
	// MEASURED on the motivating cohort (381,324 rows, 105 numeric
	// targets, 16 categorical candidates), against the predecessor's own
	// numbers: its `--conditional` capture found 1,575 candidate
	// categorical→numeric pairs and its pick-one generation applied 86.
	//
	//	floor    models  with predictors  skipped  admitted pairs
	//	0.005      93          78            12         124
	//	0.010     103          53             2          76
	//	0.020     105          23             0          30
	//
	// 0.01 is the chosen point: 103 of 105 targets get a model, 76
	// (field, target) relationships are admitted — the same density as
	// the predecessor's 86, now carried by multi-predictor models rather
	// than by one overwrite each — and only two designs stay
	// irreducibly redundant. 0.005 admits half again as many
	// relationships but costs twelve targets their model to
	// rank-deficiency and widens the worst design from 74 columns to 91;
	// 0.02 is plainly too strict, leaving four fifths of the cohort's
	// numerics explained by nothing. Re-measure before moving it.
	minVarianceExplained = 0.01

	// minSelectionObservations is the smallest retained sample on which
	// a variance-explained score is computed at all.
	//
	// Below it the adjusted statistic is dominated by its own correction
	// term rather than by the data — with N barely above the group count
	// k, (N−1)/(N−k) explodes and the score says more about the sample
	// size than about the predictor. Refusing to score is the honest
	// answer and lands the target on the zero-predictor path, which is a
	// complete model rather than a failure. It deliberately matches
	// MinPairObservations, the threshold the pairwise capture already
	// uses for "too thin to say anything".
	minSelectionObservations = MinPairObservations
)

// varCell holds one group's running (count, sum, sum of squares) for one
// numeric target, accumulated with a SHIFT taken from the group's first
// observation.
//
// The shift is what makes a plain sum-of-squares accumulator safe here.
// Naive Σy² − (Σy)²/n loses most of its significant digits when the mean
// is large relative to the spread — a spend column in cents is the
// ordinary case — and Welford's recurrence, which the OLS accumulator
// itself uses, costs a division per observation. Subtracting a constant
// offset changes neither the variance nor the between-group sums of
// squares by a single bit of arithmetic definition, keeps every
// accumulated quantity near zero, and costs one subtraction. The scoring
// walk runs (candidates x targets) times per retained row, so that
// difference is the difference between a free stage and a visible one.
type varCell struct {
	n   int
	off float64
	sum float64
	sq  float64
}

func (c *varCell) add(y float64) {
	if c.n == 0 {
		c.off = y
	}
	d := y - c.off
	c.n++
	c.sum += d
	c.sq += d * d
}

// mean returns the group mean, or 0 for an empty group.
func (c *varCell) mean() float64 {
	if c.n == 0 {
		return 0
	}
	return c.off + c.sum/float64(c.n)
}

// m2 returns the group's centered sum of squares (Σ(y−ȳ)²).
func (c *varCell) m2() float64 {
	if c.n < 2 {
		return 0
	}
	v := c.sq - c.sum*c.sum/float64(c.n)
	if v < 0 {
		// Rounding only; a centered sum of squares is non-negative.
		return 0
	}
	return v
}

// groupScore is the (count, mean, centered sum of squares) triple the
// one-way decomposition below consumes. It is the only shape
// varianceExplained understands, so a categorical group, a collapsed
// "other" bucket and a set option's selected/not-selected split all
// reach it identically.
type groupScore struct {
	n    int
	mean float64
	m2   float64
}

// varianceExplained returns the ADJUSTED share of the target's total sum
// of squares that lies between the supplied groups, or 0 when the sample
// cannot support the question.
//
// The decomposition is the textbook one — SST = SSB + SSW with
// SSB = Σ n_g (ȳ_g − ȳ)² and SSW = Σ M2_g — followed by the same
// degrees-of-freedom correction adjusted R² applies:
//
//	adjusted = 1 − (1 − SSB/SST)·(N − 1)/(N − k)
//
// Empty groups are dropped before k is counted, so a retained level that
// no admitted row happens to carry cannot inflate the correction.
//
// Zero is returned — meaning "does not clear any positive floor" —
// whenever the question is unanswerable rather than answered in the
// negative: fewer than two non-empty groups (a single-level candidate on
// a one-wave cohort has nothing to explain), too few observations, N not
// strictly greater than k, or a target with no variance at all. Every
// one of those is a degenerate candidate the story wants dropped BY THE
// RULE rather than by a special case, and 0 is the rule dropping it.
func varianceExplained(groups []groupScore) float64 {
	n := 0
	k := 0
	for _, g := range groups {
		if g.n == 0 {
			continue
		}
		n += g.n
		k++
	}
	if k < 2 || n < minSelectionObservations || n <= k {
		return 0
	}
	grand := 0.0
	for _, g := range groups {
		if g.n == 0 {
			continue
		}
		grand += float64(g.n) * g.mean
	}
	grand /= float64(n)

	ssb, ssw := 0.0, 0.0
	for _, g := range groups {
		if g.n == 0 {
			continue
		}
		d := g.mean - grand
		ssb += float64(g.n) * d * d
		ssw += g.m2
	}
	sst := ssb + ssw
	if sst <= 0 {
		return 0
	}
	raw := ssb / sst
	adj := 1 - (1-raw)*float64(n-1)/float64(n-k)
	if math.IsNaN(adj) || math.IsInf(adj, 0) || adj < 0 {
		return 0
	}
	return adj
}

// predictorChoice is one candidate field admitted into one numeric
// target's design, together with the LEVELS of it that get a column.
//
// Levels is empty for a set candidate: a set is not a partition, every
// option is an independent indicator, and the top-K collapse below does
// not apply to it (there is no "everything else" bucket that would mean
// anything — a row can select all options or none).
type predictorChoice struct {
	field string
	isSet bool
	// score is the candidate's adjusted variance explained for this
	// target. Retained past the admission decision because it is also
	// the ORDER in which candidates are given up: a design the solver
	// refuses is narrowed by dropping its weakest admitted predictor
	// first, which is the only ranking available that is not arbitrary.
	score float64
	// levels are the dictionary IDs, ascending, that get their own
	// column. Ascending order is load-bearing: the reference level
	// modelColumns drops is the FIRST categorical column for the field,
	// so a stable order is what makes the emitted baseline reproducible.
	levels []uint32
	// other requests the collapsed catch-all column — 1 on a row whose
	// level is outside `levels`. Set only when at least one out-of-top-K
	// level actually occurs among the rows this target admits, so a
	// column that would be identically zero is never created.
	other bool
}

// candidateSurvey is the per-candidate, per-target sufficient statistic
// the selection is computed from, measured over the capture's retained
// row sample.
//
// It is built at finish() rather than during the scan for one structural
// reason: the top-K collapse ranks levels by FREQUENCY, and a frequency
// ranking does not exist until the rows have been seen. Everything the
// selection needs is therefore derived from the same bounded, reservoir-
// sampled rows the fit itself consumes, which also guarantees the two
// cannot disagree — a level admitted into the design is by construction
// a level the fitted rows actually carry.
type candidateSurvey struct {
	targets []string

	fields []surveyField
}

// surveyField is one candidate predictor field's survey state.
type surveyField struct {
	name  string
	isSet bool
	// groups is the number of level/option slots this field expands to:
	// the dictionary count for a categorical, the option count for a set.
	groups int
	// counts is the per-level occurrence count over retained rows,
	// independent of any target's nullity. It is the frequency ranking
	// the top-K collapse reads, and it is deliberately NOT
	// per-target — the collapse is a property of the FIELD, exactly as
	// FieldProfile.Categorical.Top is.
	counts []int
	// labels are the level texts in dictionary-ID order, retained so the
	// top-K tie-break can match topNCategorical's (count desc, value asc)
	// ordering rather than inventing a second one.
	labels []string
	// cells holds the target statistics. Categorical layout is
	// [levelID*len(targets) + targetIdx]; set layout is
	// [(bit*2+sel)*len(targets) + targetIdx] with sel 0 = option not
	// selected, 1 = selected, because a set option's variance explained
	// is a two-group split and both sides are needed.
	cells []varCell
}

func newCandidateSurvey(targets []string, fields []surveyField) *candidateSurvey {
	nt := len(targets)
	for i := range fields {
		width := fields[i].groups
		if fields[i].isSet {
			width *= 2
		}
		fields[i].cells = make([]varCell, width*nt)
	}
	return &candidateSurvey{targets: targets, fields: fields}
}

// observeCategorical folds one retained row's (level, target values) into
// the survey.
func (s *candidateSurvey) observeCategorical(fi int, levelID uint32, targets []float64, present []bool) {
	f := &s.fields[fi]
	if int(levelID) >= f.groups {
		return
	}
	f.counts[levelID]++
	base := int(levelID) * len(s.targets)
	for ti := range s.targets {
		if !present[ti] {
			continue
		}
		f.cells[base+ti].add(targets[ti])
	}
}

// observeSet folds one retained row's set mask into the survey, updating
// the selected arm for every option the row carries and the
// not-selected arm for every option it does not.
func (s *candidateSurvey) observeSet(fi int, mask uint64, targets []float64, present []bool) {
	f := &s.fields[fi]
	nt := len(s.targets)
	for bit := 0; bit < f.groups; bit++ {
		sel := 0
		if mask&(uint64(1)<<uint(bit)) != 0 {
			sel = 1
		}
		f.counts[bit] += sel
		base := (bit*2 + sel) * nt
		for ti := 0; ti < nt; ti++ {
			if !present[ti] {
				continue
			}
			f.cells[base+ti].add(targets[ti])
		}
	}
}

// topLevels returns the field's retained dictionary IDs (ascending) and
// whether a collapsed catch-all is needed, applying exactly the ranking
// topNCategorical applies to FieldProfile.Categorical.Top: count
// descending, level text ascending on ties.
//
// A level whose text IS otherCategoryLabel is deliberately never
// retained on its own when a collapse happens — it folds into the
// catch-all instead. profile.go's otherCategoryLabel comment already
// establishes that a caller reading a profile sees ONE "other" concept,
// and two columns both labelled "other" on the same field would be
// unaddressable by the (field, level) key the emitted document uses.
func (f *surveyField) topLevels(topK int) ([]uint32, bool) {
	type ranked struct {
		id    uint32
		count int
		label string
	}
	present := make([]ranked, 0, f.groups)
	for id := 0; id < f.groups; id++ {
		if f.counts[id] == 0 {
			continue
		}
		present = append(present, ranked{id: uint32(id), count: f.counts[id], label: f.labels[id]})
	}
	if topK <= 0 || len(present) <= topK {
		// Nothing is collapsed, so a level literally named "other" keeps
		// its own column: there is no catch-all for it to collide with,
		// and folding it away would discard a measured level.
		ids := make([]uint32, 0, len(present))
		for _, r := range present {
			ids = append(ids, r.id)
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		return ids, false
	}
	sort.Slice(present, func(i, j int) bool {
		if present[i].count != present[j].count {
			return present[i].count > present[j].count
		}
		return present[i].label < present[j].label
	})
	keep := present[:topK]
	ids := make([]uint32, 0, len(keep))
	for _, r := range keep {
		if r.label == otherCategoryLabel {
			continue
		}
		ids = append(ids, r.id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids, true
}

// selectionFor returns the predictors admitted into target index ti's
// model, in candidate schema order.
//
// Both mechanisms run here and in this order: the collapse decides what
// a candidate's columns WOULD be, then variance explained over exactly
// those collapsed groups decides whether the candidate enters at all.
// Scoring the collapsed groups rather than the raw dictionary is what
// keeps the score honest — it is the variance explained by the predictor
// the model will actually carry, not by a 1,906-column one it will not.
func (s *candidateSurvey) selectionFor(ti, topK int) []predictorChoice {
	var out []predictorChoice
	nt := len(s.targets)
	for fi := range s.fields {
		f := &s.fields[fi]
		if f.isSet {
			// A set field's score is its BEST option: the field is a
			// bundle of independent indicators, so one option carrying
			// the target is reason enough to admit the bundle, and the
			// options that carry nothing cost a near-zero coefficient
			// rather than a wrong one. Narrowing WITHIN an admitted
			// predictor is the neighbouring story's job, not this one's.
			best := 0.0
			for bit := 0; bit < f.groups; bit++ {
				no := &f.cells[(bit*2+0)*nt+ti]
				yes := &f.cells[(bit*2+1)*nt+ti]
				score := varianceExplained([]groupScore{
					{n: no.n, mean: no.mean(), m2: no.m2()},
					{n: yes.n, mean: yes.mean(), m2: yes.m2()},
				})
				if score > best {
					best = score
				}
			}
			if best >= minVarianceExplained {
				out = append(out, predictorChoice{field: f.name, isSet: true, score: best})
			}
			continue
		}

		keep, collapse := f.topLevels(topK)
		retained := make(map[uint32]bool, len(keep))
		for _, id := range keep {
			retained[id] = true
		}
		groups := make([]groupScore, 0, len(keep)+1)
		levels := make([]uint32, 0, len(keep))
		var other varCell
		otherSeen := false
		for id := 0; id < f.groups; id++ {
			c := &f.cells[id*nt+ti]
			if retained[uint32(id)] {
				if c.n == 0 {
					// A retained level no admitted row carries would be
					// an identically-zero column: harmless to the fit's
					// answer but exactly collinear with nothing, and it
					// spends a degree of freedom the solver's rank check
					// then has to forgive. Dropping it here is cheaper
					// than being refused there.
					continue
				}
				levels = append(levels, uint32(id))
				groups = append(groups, groupScore{n: c.n, mean: c.mean(), m2: c.m2()})
				continue
			}
			if c.n == 0 {
				continue
			}
			otherSeen = true
			// Merging two shifted accumulators cannot be done on the
			// packed form, so the catch-all is folded through the same
			// group triple the decomposition consumes: its count, mean
			// and centered sum of squares combine pairwise.
			other = mergeCells(other, *c)
		}
		if !collapse {
			otherSeen = false
		}
		if otherSeen {
			groups = append(groups, groupScore{n: other.n, mean: other.mean(), m2: other.m2()})
		}
		score := varianceExplained(groups)
		if score < minVarianceExplained {
			continue
		}
		out = append(out, predictorChoice{field: f.name, levels: levels, other: otherSeen, score: score})
	}
	return out
}

// mergeCells combines two shifted accumulators into one that reports the
// same count, mean and centered sum of squares as a single accumulator
// fed both inputs.
//
// The count and the shifted first moment add directly once the second
// cell's offset is rebased onto the first's; the second moment needs the
// usual parallel-variance correction term for the difference between the
// two means, which is where a naive concatenation would silently lose
// the between-part of the merged spread.
func mergeCells(a, b varCell) varCell {
	if b.n == 0 {
		return a
	}
	if a.n == 0 {
		return b
	}
	na, nb := float64(a.n), float64(b.n)
	ma, mb := a.mean(), b.mean()
	n := na + nb
	mean := (na*ma + nb*mb) / n
	d := mb - ma
	m2 := a.m2() + b.m2() + d*d*na*nb/n
	// Repack onto a fresh offset so the invariants add()/mean()/m2()
	// rely on continue to hold for a merged cell.
	out := varCell{n: a.n + b.n, off: mean}
	out.sum = 0
	out.sq = m2
	return out
}

// buildSurveyFields describes each candidate predictor field to the
// survey, in the caller's (schema) order.
func buildSurveyFields(schema *encoding.Schema, candidates []string) []surveyField {
	out := make([]surveyField, 0, len(candidates))
	for _, name := range candidates {
		f := schema.Field(name)
		if f == nil || f.Dictionary == nil {
			continue
		}
		n := f.Dictionary.Count()
		if f.Type.IsSet() {
			if max := int(f.Type.MaxSetEntries()); n > max {
				n = max
			}
		}
		labels := make([]string, n)
		for i := 0; i < n; i++ {
			labels[i] = f.Dictionary.Resolve(uint32(i))
		}
		out = append(out, surveyField{
			name:   name,
			isSet:  f.Type.IsSet(),
			groups: n,
			counts: make([]int, n),
			labels: labels,
		})
	}
	return out
}
