package synth

import (
	"fmt"
	"math"
	"sort"
)

// This file is the profile-time RESIDUAL correlation capture: under
// ProfileOptions.FitResidualCorrelations (`profile create
// --residual-correlations`) it measures the correlation between every
// pair of fitted model residuals and writes the result as the additive
// `residual_correlations` section.
//
// # Why residuals and not raw values
//
// `--include-correlations` (Profile.Pairwise) and `--conditional`
// (Conditional.NumericPairs) both measure the correlation between two
// fields' RAW values, which is the right quantity when each field is
// reconstructed from its own marginal and nothing else. It stops being
// the right quantity the moment a field carries a model: once a
// numeric's systematic variation is explained by its predictors, the
// part still free to move at generation time is the RESIDUAL, and a raw
// correlation double-counts every predictor the two targets share. Two
// fields both driven by `region` correlate strongly on raw values while
// their residuals may be independent — imposing the raw figure on the
// residual draw would then apply `region`'s effect twice.
//
// # Why the full submatrix, and why absence means unmeasured
//
// The existing sections retain the strongest CorrelationTopK pairs,
// because their consumer applies one pair at a time. This section's
// consumer is a joint draw over every participant at once (a Cholesky
// factor of the whole matrix), and a matrix is not a ranked list: a
// pair left out of it is not "weak", it is a hole the factorization has
// to fill with something. So every pair among participants is visited,
// and the ones that could not be measured are written down AS
// unmeasured rather than left to be inferred — see
// ResidualCorrelationProfile for the representation and
// buildCorrelator (synth/copula.go) for what the generator does when it
// meets one.
//
// The distinction this section exists to preserve is between a pair
// MEASURED at rho = 0 (the two residuals genuinely do not move
// together — real, useful structure) and a pair never measured at all
// (no rows carry both residuals — no information whatsoever). Collapsing
// the second into the first is precisely the class of silently
// fabricated structure v0.32.1/v0.32.2 removed elsewhere in this
// package, and it is why an unmeasured pair is NEVER encoded as a rho.

const (
	// minResidualPairObservations is the floor below which a residual
	// pair is recorded as UNMEASURED rather than as a thin measurement.
	//
	// Two points always produce a Pearson coefficient of exactly ±1 —
	// a line through two points is a perfect fit — so a figure computed
	// from a handful of overlapping rows is not a weak measurement, it
	// is an artifact of the arithmetic, and shipping it as a measured
	// rho would put fabricated structure into the very section that
	// exists to keep fabricated structure out.
	//
	// It is deliberately NOT MinPairObservations (30). That constant
	// answers "is this correlation stable enough to trust", and its
	// answer is a WARNING, never a refusal — a thin pair still ships,
	// because a shaky measurement of something real beats no
	// measurement at all. This constant answers a different question:
	// "is there a measurement here at all". Below it there is not, so
	// the honest record is a gap. The two compose — a pair at or above
	// this floor but below MinPairObservations is measured AND warned.
	minResidualPairObservations = 3

	// maxThinResidualPairWarnings caps how many individual thin residual
	// pairs are named on Profile.Warnings before the rest collapse into
	// one counted summary line.
	//
	// This section is quadratic in participants by construction — 105
	// modelled fields is 5,460 pairs — so an unbounded per-pair warning
	// would bury every other line on the slice under a wall that says
	// one thing about the cohort's null structure several thousand
	// times. The cap matches maxThinLevelWarnings for the same reason
	// that one exists, and the list is emitted thinnest-first so the
	// truncated tail is by construction the least severe part of it.
	maxThinResidualPairWarnings = 20
)

// Reasons a residual pair could not be measured. Closed vocabulary: a
// consumer reading `residual_correlations.unmeasured` must be able to
// switch on this exhaustively, and a new reason is a deliberate
// addition to this list rather than a free-text string invented at a
// call site.
const (
	// ResidualUnmeasuredNoOverlap means fewer than
	// minResidualPairObservations rows carried BOTH residuals. Listwise
	// deletion is per model, so two fields with disjoint null patterns
	// can each have thousands of residuals and share almost none.
	ResidualUnmeasuredNoOverlap = "insufficient_overlap"
	// ResidualUnmeasuredNoVariance means the rows DID overlap but at
	// least one side's residual was constant across them, so the
	// correlation is 0/0. A perfectly fitted model on the overlap is the
	// ordinary cause.
	ResidualUnmeasuredNoVariance = "no_variance"
)

// ResidualCorrelation is one MEASURED residual pair.
//
// Every entry in ResidualCorrelationProfile.Pairs is a real measurement
// over N co-present rows, including one whose Rho is 0 — that is the
// measured-zero case this section exists to keep distinguishable from a
// gap, and it is meaningful information (these two residuals were
// watched together and did not move together).
type ResidualCorrelation struct {
	A string `json:"a"`
	B string `json:"b"`
	// Rho is the Pearson correlation between the two fields' fitted
	// residuals over the rows where both exist. Deliberately NOT
	// omitempty: a measured zero must appear on the wire, because
	// omitting it would make it indistinguishable from an unmeasured
	// pair on read-back and would reintroduce exactly the ambiguity
	// this section removes.
	Rho float64 `json:"rho"`
	// N is the number of retained rows carrying both residuals — the
	// pair's true co-occurrence count within the capture's residual
	// reservoir, not the cohort's row count and not either model's
	// NObs.
	N int `json:"n"`
}

// ResidualUnmeasuredPair records one pair among participants that could
// NOT be measured, and why.
//
// It is written down rather than left as an absence from Pairs because
// the whole subject of this section is the difference between "we
// looked and there is no relationship" and "we could not look". A
// reader holding Fields and Pairs can already derive the complement,
// but deriving a fact is not the same as being told it, and the reason
// is not derivable at all.
type ResidualUnmeasuredPair struct {
	A string `json:"a"`
	B string `json:"b"`
	// N is how many rows DID carry both residuals — zero or a handful
	// for ResidualUnmeasuredNoOverlap, and however many overlapped for
	// ResidualUnmeasuredNoVariance. It is the diagnostic that separates
	// "these two fields never co-occur" from "they co-occur constantly
	// and one of them is fitted exactly".
	N int `json:"n"`
	// Reason is one of the ResidualUnmeasured* constants. There is no
	// rho key on this struct at all — an unmeasured pair has no
	// correlation to report, and providing a slot for one is how a zero
	// gets written into it.
	Reason string `json:"reason"`
}

// ResidualCorrelationProfile is the captured correlation structure
// among model residuals: the FULL submatrix over participants, split
// into what was measured and what was not.
//
// The representation is two lists over a declared participant set
// rather than an N x N array of numbers, and that is the load-bearing
// choice. A dense array has one slot per pair and every slot must hold
// a number, so "unmeasured" has nowhere to live except as a sentinel —
// and the sentinel that reads most naturally is 0, which is also a
// perfectly ordinary measurement. Splitting the pairs by whether they
// were measured makes the two states structurally different rather than
// conventionally different, and a JSON round trip preserves a
// structural difference for free.
//
// Fields + Pairs + Unmeasured is exhaustive and non-overlapping: every
// unordered pair among Fields appears in exactly one of the two lists.
type ResidualCorrelationProfile struct {
	// Fields names the model residuals this submatrix covers, sorted.
	// It is the participant set, and it is written down so a reader can
	// tell an absent pair from an absent FIELD — a field with no model
	// has no residual and was never a candidate, which is a different
	// statement from a modelled pair that could not be measured.
	Fields []string `json:"fields"`
	// Pairs holds every MEASURED pair, in (A, B) sorted order.
	Pairs []ResidualCorrelation `json:"pairs,omitempty"`
	// Unmeasured holds every pair among Fields that Pairs does not, each
	// with its reason. Present whenever there is one; a fully measured
	// submatrix omits the key entirely, which is itself the signal that
	// nothing was assumed.
	Unmeasured []ResidualUnmeasuredPair `json:"unmeasured,omitempty"`
}

// computeResidualCorrelations measures the full residual submatrix over
// the models carrying a residual reservoir.
//
// # Who participates
//
// Every model with a non-empty residual vector — INCLUDING a
// zero-predictor model. Selection can legitimately leave a target with
// no admitted predictor (nothing explained enough of its variance), and
// such a target still has a residual: its whole deviation from its own
// mean. Excluding it would drop real structure for a reason that has
// nothing to do with the structure — and would do so asymmetrically,
// since the same field paired against a predictor-carrying target is
// exactly as measurable. Its residual correlation simply coincides with
// its raw correlation, which is the correct answer for a field nothing
// predicts.
//
// A model with a NIL residual vector cannot participate and is not
// listed as a participant. In the capture path that never happens —
// the fitter fills a residual vector for every model it keeps. It is
// reachable only by calling this on a Profile decoded from a document,
// where the reservoir is `json:"-"` and therefore gone; treating those
// as participants would produce a submatrix in which every pair is
// unmeasured, which describes the reader's situation rather than the
// cohort's.
//
// # Determinism
//
// Participants are sorted by name and pairs are enumerated over that
// sorted order, so the emitted section does not depend on the order the
// fitter happened to keep its models in. Every accumulation is over
// slices; no float is ever folded in Go map order (see
// skills/synthetic-data.md, Determinism).
//
// Returns nil when fewer than two fields can participate — a submatrix
// over one field is the scalar 1 and carries nothing.
func computeResidualCorrelations(models []FieldModel, warnings *[]string) *ResidualCorrelationProfile {
	byField := make(map[string]*FieldModel, len(models))
	names := make([]string, 0, len(models))
	for i := range models {
		if len(models[i].Residuals) == 0 {
			continue
		}
		if _, dup := byField[models[i].Field]; dup {
			continue
		}
		byField[models[i].Field] = &models[i]
		names = append(names, models[i].Field)
	}
	if len(names) < 2 {
		return nil
	}
	sort.Strings(names)

	out := &ResidualCorrelationProfile{Fields: names}
	var thin []ResidualCorrelation
	for i := 0; i < len(names); i++ {
		for j := i + 1; j < len(names); j++ {
			a, b := byField[names[i]], byField[names[j]]
			xs, ys := coResiduals(a, b)
			if len(xs) < minResidualPairObservations {
				out.Unmeasured = append(out.Unmeasured, ResidualUnmeasuredPair{
					A: names[i], B: names[j], N: len(xs),
					Reason: ResidualUnmeasuredNoOverlap,
				})
				continue
			}
			rho := pearson(xs, ys)
			if math.IsNaN(rho) {
				// pearson returns NaN only for n < 2 (excluded above) or
				// a zero-variance side, so this arm is exactly the
				// constant-residual case.
				out.Unmeasured = append(out.Unmeasured, ResidualUnmeasuredPair{
					A: names[i], B: names[j], N: len(xs),
					Reason: ResidualUnmeasuredNoVariance,
				})
				continue
			}
			p := ResidualCorrelation{A: names[i], B: names[j], Rho: rho, N: len(xs)}
			out.Pairs = append(out.Pairs, p)
			if p.N < MinPairObservations {
				thin = append(thin, p)
			}
		}
	}
	if warnings != nil {
		*warnings = append(*warnings, residualCaptureWarnings(out, thin)...)
	}
	return out
}

// coResiduals returns the two models' residuals restricted to the rows
// where BOTH are present.
//
// The two vectors are row-aligned by construction — every FieldModel
// from one capture indexes the same retained snapshot rows (see
// FieldModel.Residuals) — so intersecting them is a single walk over
// the shorter length. The length guard is defensive: a caller
// assembling FieldModels from two different captures would otherwise
// silently correlate unrelated rows, and truncating is the only
// reading of that situation that is not simply wrong.
func coResiduals(a, b *FieldModel) (xs, ys []float64) {
	return coResidualSlices(a.Residuals, a.ResidualPresent, b.Residuals, b.ResidualPresent)
}

// coResidualSlices is the row-alignment rule itself, over bare
// slices.
//
// It is factored out of coResiduals so the RECOVERY side of the same
// measurement — the fidelity report's refit residuals over the
// generated partition, which are per-row vectors of exactly this shape
// but carry no FieldModel around them (synth/fidelity_residual.go) —
// intersects rows by the identical walk rather than by a second one
// written to look like it. A capture and its recovery disagreeing about
// which rows a pair shares would show up as a correlation delta and
// read as a generation fault.
func coResidualSlices(ar []float64, ap []bool, br []float64, bp []bool) (xs, ys []float64) {
	n := len(ar)
	if len(br) < n {
		n = len(br)
	}
	if len(ap) < n {
		n = len(ap)
	}
	if len(bp) < n {
		n = len(bp)
	}
	for r := 0; r < n; r++ {
		if !ap[r] || !bp[r] {
			continue
		}
		xs = append(xs, ar[r])
		ys = append(ys, br[r])
	}
	return xs, ys
}

// residualCaptureWarnings turns the captured submatrix into the lines a
// reader needs to see on Profile.Warnings.
//
// Two findings, both bounded. The unmeasured count is ONE line however
// many pairs it covers: the pairs themselves are already in the
// document with their reasons, so the warning's job is to make sure the
// gap is noticed, not to re-list it. Thin measured pairs are named
// individually up to maxThinResidualPairWarnings, thinnest-first, with
// a counted summary for the rest — the same shape
// profile_models_shrink.go uses, for the same reason.
func residualCaptureWarnings(p *ResidualCorrelationProfile, thin []ResidualCorrelation) []string {
	var out []string
	if n := len(p.Unmeasured); n > 0 {
		total := n + len(p.Pairs)
		out = append(out, fmt.Sprintf(
			"residual correlations: %d of %d pair(s) among %d modelled field(s) could not be measured "+
				"and are recorded as unmeasured, not as zero — see residual_correlations.unmeasured for the reason per pair",
			n, total, len(p.Fields)))
	}
	if len(thin) == 0 {
		return out
	}
	sort.Slice(thin, func(i, j int) bool {
		if thin[i].N != thin[j].N {
			return thin[i].N < thin[j].N
		}
		if thin[i].A != thin[j].A {
			return thin[i].A < thin[j].A
		}
		return thin[i].B < thin[j].B
	})
	shown := thin
	if len(shown) > maxThinResidualPairWarnings {
		shown = shown[:maxThinResidualPairWarnings]
	}
	for _, t := range shown {
		if w := thinPairWarning("residual", t.A, t.B, t.N, MinPairObservations); w != "" {
			out = append(out, w)
		}
	}
	if rest := len(thin) - len(shown); rest > 0 {
		out = append(out, fmt.Sprintf(
			"+%d further thin residual pair(s) below %d supporting observation(s); "+
				"they are measurements, not gaps, but rest on few co-present rows",
			rest, MinPairObservations))
	}
	return out
}
