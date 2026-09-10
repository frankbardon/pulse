package synth

import (
	"fmt"
	"sort"
)

// This file is the THIN-LEVEL half of `profile create --fit-models`.
// Selection (profile_models_select.go) decides which candidate FIELDS
// enter a target's design; this decides how much to trust the
// individual LEVELS of a field that was already admitted. The two are
// independent on purpose — a categorical can explain a target
// convincingly overall while three of its retained levels rest on a
// handful of rows each — and neither is allowed to stand in for the
// other.
//
// # What actually goes wrong
//
// A dummy coefficient for a level carried by n_j rows has a standard
// error of roughly σ/sqrt(n_j), where σ is the fit's residual scale.
// At n_j = 4 that is half a residual standard deviation of pure noise
// entering the fitted value as if it were an effect, and generation
// then reproduces it faithfully for every row it draws at that level —
// a level the source cohort barely evidenced comes out of synthesis
// with a confident, invented offset. That is a materially worse failure
// than a noisy conditional mean, which is why this goes further than
// the warn-only stance the pairwise capture takes.
//
// # How much of this problem is left after the top-K collapse
//
// Less than the story that commissioned it assumed, and the reason is
// worth writing down because it changes what this mechanism is FOR.
// Before E2-S1 the design expanded every dictionary in full, so the
// motivating cohort's 210-level `dma` and 1,906-level `brand`
// contributed hundreds of columns each with a handful of rows behind
// them. The top-K collapse (ProfileOptions.TopK, default 32) removed
// that whole class: a retained level is now one of a field's 32 most
// FREQUENT, and everything below folds into one well-populated "other"
// column. Ranking a field's levels by frequency does NOT bound their
// support, though — the 32nd most common `brand` is still rare — and
// LISTWISE deletion cuts it again, hardest on the targets with the most
// nulls. On the motivating cohort the three NPS fields admit 1,736 of
// the 10,000 reservoir rows, and every level in their designs is
// measured against that, not against 381,324.
//
// MEASURED end to end on that cohort (381,324 rows, 105 numeric targets,
// 55 predictor-carrying models at every threshold below):
//
//	threshold   models shrunk   distinct thin levels   mean R²
//	    30            12                 69             0.0533
//	    50            23                 89             0.0527
//	   100           35                104             0.0497
//	   200           43                119             0.0459
//
// Mean R² is IN-SAMPLE and therefore decreasing in the threshold by
// construction — shrinkage always costs in-sample fit — so it cannot
// select the threshold. It is reported because it prices one: at 50 the
// whole mechanism costs about 1% of the fitted models' explanatory power
// relative to the least-shrunk column of the sweep, and at 200 it costs
// 14%, which is the point at which the prior has stopped correcting the
// model and started being it.

const (
	// minLevelObservations is the support below which a fitted design
	// column is treated as thin: warned about, and shrunk toward the
	// baseline rather than fitted freely.
	//
	// It is DELIBERATELY not MinPairObservations (30) and must not be
	// collapsed into it. That constant answers "does this pair have
	// enough co-occurrences for a reconstructed correlation to mean
	// anything"; this one answers "does this level have enough rows for
	// a free coefficient to describe the level rather than the sample".
	// The two happen to be the same ORDER of magnitude because both are
	// small-sample thresholds, which is exactly the coincidence that
	// would make sharing one constant look reasonable and then couple
	// two unrelated tunings forever.
	//
	// 50 is chosen on the standard error, because the sweep in this
	// file's header cannot choose it — in-sample R² falls monotonically
	// as the threshold rises, so every candidate "wins" on the metric
	// that is measurable and the argument has to be made on the
	// statistics instead. A rare level's coefficient has SE ≈ σ/sqrt(n_j)
	// against the fit's residual scale σ, so n_j = 50 resolves the
	// level's effect to about σ/7: at and below that the fitted offset is
	// dominated by sampling noise rather than by the level, and a
	// generated row drawn there carries a visible invented shift. Read as
	// the pseudo-count it doubles as (see planShrinkage), 50 also says a
	// level sitting exactly at the threshold keeps half its free
	// coefficient, which is the honest reading of an estimate whose
	// standard error is a seventh of the spread it is estimating.
	//
	// The sweep does bound it from above: at 200 the penalty costs 14% of
	// the fitted models' in-sample R² and reaches 43 of 55 models, which
	// is a prior that has stopped correcting the fit and started being
	// it.
	//
	// It is a constant, not an option, for the reason minVarianceExplained
	// is: a threshold whose purpose is to be stable across cohorts must
	// not be tunable per run. Re-measure before moving it.
	minLevelObservations = 50

	// maxThinLevelWarnings caps how many individual thin levels are
	// named on Profile.Warnings before the rest are collapsed into one
	// counted summary line.
	//
	// Thinness is a property of the LEVEL, not of the target — a rare
	// `brand` is rare against every numeric field in the cohort — so the
	// natural per-(target, level) emission restates one finding once per
	// fitted model. On the motivating cohort that is 89 distinct thin
	// levels appearing across 23 shrunk models, and the naive form emits
	// 477 lines to say 89 things. Aggregation by (field, level) is
	// therefore the FIRST bound and this cap is the second; the cap
	// exists for the cohort where a large number of DISTINCT levels are
	// thin (few rows, or --top-k raised past what the row count
	// supports).
	//
	// 20 is enough to read the shape of the problem — the list is emitted
	// thinnest-first, so the truncated tail is by construction the least
	// severe part of it — while leaving the profile's other warnings
	// findable. That last clause is the whole point: `--conditional`
	// alone already puts ~2,900 thin-PAIR lines on the same slice, and
	// the predecessor's fidelity report shipped ~4,500. Adding 21 lines
	// to that is a diagnosis; adding 477 is another wall.
	maxThinLevelWarnings = 20
)

// thinLevel records one design column whose support fell below
// minLevelObservations in one fit.
type thinLevel struct {
	// field and level are the (field, level) addressing key the emitted
	// document uses for the coefficient, so a warning names the same
	// thing a reader can look up in `models[].predictors`.
	field string
	level string
	// n is the column's support: the number of rows this fit ADMITTED on
	// which the column was non-zero. Listwise, not pairwise — a level
	// with plenty of rows in the cohort but few surviving the fit's null
	// deletion is thin for this fit, and that is the quantity the
	// coefficient's standard error actually depends on.
	n int
}

// planShrinkage decides this fit's ridge penalty from the support of its
// surviving design columns, and records which of them are thin.
//
// # Why ridge, and why one alpha
//
// The shipped OLS engine implements ridge in closed form on the centered
// Gram (processing/regression/ols_ridge.go: β = (M2_xx + n·λ·I)⁻¹·M2_xy)
// with its own golden and property coverage, and the story's instruction
// is to use it rather than hand-roll shrinkage. λ is a single scalar for
// the whole design — the engine has no per-column penalty and this
// package is not permitted to add one — so the lever available is the
// value of λ and whether it is applied at all.
//
// # The pseudo-count reading, which is what makes one alpha enough
//
// The engine adds n·λ to every diagonal entry, and a 0/1 indicator
// column with support n_j among n admitted rows has centered second
// moment n_j·(1 − n_j/n). Setting
//
//	λ = minLevelObservations / n
//
// therefore adds exactly minLevelObservations to each diagonal, and the
// column's coefficient is retained in the proportion
//
//	n_j·(1 − n_j/n) / (n_j·(1 − n_j/n) + minLevelObservations)
//
// which for a rare level is n_j / (n_j + minLevelObservations). That is
// ridge read as a Gaussian prior worth minLevelObservations
// observations at zero, and it answers the story's open question
// directly: the shrinkage is CONTINUOUS in the level's own support, not
// a hard on/off at the threshold. A level sitting exactly at the
// threshold keeps half its free coefficient; one with a third of the
// threshold keeps a quarter of it; one with ten times the threshold
// keeps 91%. A level with 3 observations and one with 29 are not
// treated alike, which was the property the threshold alone could not
// deliver.
//
// Two honest consequences, both stated because neither is visible in the
// output:
//
//   - Shrinkage is toward the field's REFERENCE level, not toward the
//     grand mean, because that is what a dummy coefficient measures. A
//     thin level collapsing onto the baseline is the conservative
//     answer — "too few rows to say this level differs" — and it is the
//     answer generation should reproduce.
//
//   - Inside a fit that IS penalised, well-supported columns move too,
//     by roughly minLevelObservations/n_j. That is bounded and small by
//     construction (2% at 2,500 rows of support, 9% at 500) but it is
//     not zero, and no single-alpha ridge can make it zero.
//
// # Why it is gated on a thin column actually being present
//
// A design where every column clears the threshold has nothing to
// shrink, and penalising it anyway would move every coefficient in every
// model for no measured reason — including the exactly-linear case,
// where recovering the generating coefficients to 1e-6 is an acceptance
// bar this capture is held to (TestProfileModels_CleanMultiPredictorFit).
// The gate costs a discontinuity at the boundary: a design whose
// thinnest column sits at 51 is fitted by plain OLS, and at 49 its
// well-supported columns move by the bounded amount above. That is the
// deliberate trade — the discontinuity is bounded and attributable to a
// measured condition, whereas always-on shrinkage would be neither.
//
// One further interaction is worth naming because it is invisible: a
// penalised design is no longer rank-deficient, so the solver's refusal
// that E2-S1 uses as its redundancy signal (see maxModelRefitRounds)
// does not fire for a fit that carries a thin level. Two collinear
// predictors are then jointly shrunk rather than one being dropped,
// which is what ridge is for, but it does mean the refit narrowing
// applies to clean designs only.
//
// support must be parallel to columns and contrib the fit's admitted row
// count, both as prune left them.
func (f *fieldFit) planShrinkage() {
	f.alpha = 0
	f.thin = nil
	if len(f.columns) == 0 || f.contrib <= 0 || len(f.support) != len(f.columns) {
		return
	}
	for ci := range f.columns {
		switch f.columns[ci].Kind {
		case dummyCategoricalLevel, dummyCategoricalOther, dummySetOption:
		default:
			// A dummyNumeric column's hit count is "rows where the value
			// was non-zero", which is not a support at all — a scalar
			// predictor is estimated from every admitted row. Counting
			// it as thin would penalise a design for carrying a
			// mostly-zero numeric.
			continue
		}
		if f.support[ci] >= minLevelObservations {
			continue
		}
		f.thin = append(f.thin, thinLevel{
			field: f.columns[ci].Field,
			level: f.columns[ci].Level,
			n:     f.support[ci],
		})
	}
	if len(f.thin) == 0 {
		return
	}
	f.alpha = float64(minLevelObservations) / float64(f.contrib)
}

// thinLevelWarnings renders every shrunk level across the RETAINED fits
// as warning lines, aggregated by (field, level) and bounded.
//
// Aggregation comes first and is the load-bearing bound: a level thin
// for one target is thin for essentially every target, because its
// support is driven by the field's own rarity, so the per-(target,
// level) form is a hundred restatements of one fact on a 105-target
// cohort. Each distinct level is reported once, against the model where
// it was THINNEST — the worst case is the one worth naming — with the
// number of models it was shrunk in carried alongside so the reader can
// see the blast radius without reading a hundred lines to count it.
//
// fits must already be in emission (target schema) order; the aggregate
// is then re-sorted thinnest-first so a truncated list keeps the worst
// offenders and drops the mildest.
func thinLevelWarnings(fits []*fieldFit) []string {
	type agg struct {
		field  string
		level  string
		n      int
		target string
		models int
	}
	at := make(map[[2]string]int)
	var aggs []agg
	for _, fit := range fits {
		for _, tl := range fit.thin {
			key := [2]string{tl.field, tl.level}
			i, seen := at[key]
			if !seen {
				at[key] = len(aggs)
				aggs = append(aggs, agg{field: tl.field, level: tl.level, n: tl.n, target: fit.target, models: 1})
				continue
			}
			aggs[i].models++
			if tl.n < aggs[i].n {
				aggs[i].n = tl.n
				aggs[i].target = fit.target
			}
		}
	}
	if len(aggs) == 0 {
		return nil
	}
	// Thinnest first; ties broken on the (field, level) key so the order
	// is a property of the cohort rather than of map iteration.
	sort.SliceStable(aggs, func(i, j int) bool {
		if aggs[i].n != aggs[j].n {
			return aggs[i].n < aggs[j].n
		}
		if aggs[i].field != aggs[j].field {
			return aggs[i].field < aggs[j].field
		}
		return aggs[i].level < aggs[j].level
	})

	shown := aggs
	if len(shown) > maxThinLevelWarnings {
		shown = shown[:maxThinLevelWarnings]
	}
	out := make([]string, 0, len(shown)+1)
	for _, a := range shown {
		w := thinSupportWarning("model level", a.field+"="+a.level, a.target,
			a.n, minLevelObservations, thinLevelConsequence(a.models))
		if w == "" {
			// Unreachable: an entry only exists below the threshold.
			continue
		}
		out = append(out, w)
	}
	if rest := len(aggs) - len(shown); rest > 0 {
		out = append(out, fmt.Sprintf(
			"+%d further thin model level(s) shrunk toward their reference level (below %d supporting observation(s)); "+
				"listing suppressed to keep the warning list readable — lower --top-k to fold rare levels into %q",
			rest, minLevelObservations, otherCategoryLabel))
	}
	return out
}

// thinLevelConsequence spells out what the shrinkage did, naming the
// model count only when it is more than one so the ordinary single-model
// case reads as a sentence rather than as a report.
func thinLevelConsequence(models int) string {
	if models <= 1 {
		return "coefficient shrunk toward its reference level"
	}
	return fmt.Sprintf("coefficient shrunk toward its reference level in %d model(s)", models)
}
