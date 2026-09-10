package synth

import (
	"math"
	"sort"
)

// This file is the residual-correlation half of the model-recovery
// fidelity work (E5-S2): for every residual correlation generation
// actually applied, it reports the captured rho beside the rho
// recovered from the generated partition.
//
// # Why this section is not `pairwise` under a different name
//
// `pairwise` (PairwiseFidelity) scores Spec.Correlations — a
// correlation between two fields' VALUES, realized by the value-scale
// copula. This section scores Spec.ResidualCorrelations — a correlation
// between two MODELS' residuals, realized by the residual correlator
// (synth/residual_draw.go). The two are different numbers over the same
// pair of fields and neither substitutes for the other: two fields both
// driven by `region` correlate strongly on raw values while their
// residuals may be independent, which is the whole argument
// synth/residual_corr.go's header makes for capturing the residual
// figure separately in the first place. A modelled field is excluded
// from Spec.Correlations by resolveConflicts precisely so the two never
// both fire on one field, so a reader scanning the JSON is looking at
// two disjoint arms, and the section names say which is which:
// `pairwise` for the copula arm, `model_residual_correlations` for the
// model arm.
//
// # What is compared, and on which scale
//
// The captured side is CorrelationSpec.Correlation — the number
// buildResidualCorrelator was asked to hit, itself the Pearson
// correlation between the two models' fitted residuals over the source
// rows (ResidualCorrelationProfile.Pairs). The recovered side is the
// Pearson correlation between the two models' fitted residuals over the
// SYNTHETIC rows, and "fitted" there means fitted BY THE REFIT E5-S1
// already ran on that partition — not evaluated against the captured
// coefficients. That symmetry is the point: capture measured the
// residuals of a model fitted on its own rows, so recovery measures the
// residuals of a model fitted on its own rows too. Scoring against the
// captured coefficients instead would fold every coefficient-recovery
// gap into the correlation and report the SAME finding twice, once in
// `models` where it belongs and once here where it would be
// indistinguishable from a correlator fault.
//
// Residuals are taken on the LATENT scale, for exactly the reason
// fidelity_models.go's header gives for the coefficients: generation
// draws value = Q(Phi(mu + sigma*z)) and correlates the z's, so the
// latent residual sigma*z carries the requested correlation exactly
// while the VALUE-scale residual of a non-normal Q is a monotone but
// non-linear image of it — rank-preserving, Pearson-attenuated. A
// value-scale comparison would therefore report every `--fit-shape`
// target as a badly recovered correlation on a generation path that was
// exactly correct, which is the same trap E5-S1 avoided and it is
// avoided here the same way, through latentFor.
//
// # Why the listing is bounded
//
// This section is QUADRATIC in participants by construction — the
// motivating cohort's 55 applied models declare C(55,2) = 1,485
// correlated pairs — and the report it lands in is already 1.65 MB. An
// unbounded per-pair listing would add a wall nobody reads to a
// document whose entire purpose is to be read, which is the failure
// mode maxThinLevelWarnings and maxThinResidualPairWarnings were both
// introduced to prevent. So the aggregate figures (Compared, Flagged,
// MeanDelta, MaxDelta) cover EVERY compared pair and the per-pair
// listing carries the worst maxResidualRecoveryPairs of them,
// worst-first, with the remainder counted rather than dropped silently.
// Nothing is hidden: a pair not listed is by construction a pair
// recovered better than every pair that is.

// ResidualRecoveryTolerance is the absolute gap between a captured
// residual correlation and the recovered one at which the pair is
// FLAGGED rather than merely reported.
//
// A correlation is already unit-free, so unlike ModelRecoveryTolerance
// this number needs no scale argument to be comparable across fields —
// 0.10 is the same statement about a dollar-denominated spend pair and
// an 11-point NPS pair. The magnitude is chosen to match
// ModelRecoveryTolerance's, so the two halves of the model-recovery
// report flag at the same severity and a reader does not have to hold
// two thresholds in mind: a relationship recovered within a tenth is
// the relationship that was captured, whatever the remaining digits do.
//
// Unlike the coefficient band there is no standard-error term. A
// Pearson correlation over the thousands of co-present rows this
// section measures on has a standard error well under 0.02, so a
// noise-scaled band would collapse onto the floor in every case that
// matters; the pairs where it would not are the thin ones, and those
// are already separated out as unmeasured rather than reported with a
// wide error bar.
const ResidualRecoveryTolerance = 0.10

// maxResidualRecoveryPairs caps the per-pair listing, worst-first. See
// this file's header on why the section must be bounded at all; the
// value matches maxThinLevelWarnings and maxThinResidualPairWarnings,
// the two other places in this package where a per-item listing can
// scale with the schema and is truncated with a counted remainder.
const maxResidualRecoveryPairs = 20

// ResidualUnmeasuredNoModelFit is a RECOVERY-side reason with no
// capture-side counterpart: the pair's endpoints both participated in
// generation's correlated draw, but one of their models could not be
// refitted on the synthetic partition at all (see ModelFidelity.Error),
// so there is no residual vector to correlate.
//
// It is defined here rather than beside the ResidualUnmeasured*
// constants in synth/residual_corr.go on purpose. That vocabulary is
// the closed set a `residual_correlations.unmeasured` entry in a
// PROFILE DOCUMENT can carry, and a capture can never produce this
// reason — there is no refit at capture time. Mixing it in would widen
// a documented file-format vocabulary for a value that will never
// appear in a file.
const ResidualUnmeasuredNoModelFit = "no_model_fit"

// ModelResidualCorrelationFidelity is FidelityReport's
// `model_residual_correlations` section: how well the generated
// partition reproduces the residual correlation structure generation
// was asked to impose.
//
// The aggregate counters describe EVERY compared pair; Pairs carries a
// bounded worst-first sample of them and Omitted counts the rest. See
// this file's header for why the listing is bounded and why nothing is
// lost by it.
type ModelResidualCorrelationFidelity struct {
	// Fields names the participants in COMPONENT order — the order
	// buildResidualCorrelator assigned them, which is drawer order,
	// which is schema field order. It is the generation-side
	// participant set, so a modelled field absent from it took no part
	// in the correlated draw (its model was dropped, or no surviving
	// correlation named it) and drew an independent residual.
	Fields []string `json:"fields"`
	// Compared is the number of applied residual correlations that
	// produced a recovered figure. Flagged counts those whose Delta
	// exceeds ResidualRecoveryTolerance. Both count over every compared
	// pair, not over the bounded Pairs listing.
	Compared int `json:"compared"`
	Flagged  int `json:"flagged,omitempty"`
	// MeanDelta / MaxDelta are the mean and maximum absolute gap over
	// every compared pair — the two figures that make the truncated
	// listing safe to read, since they cannot be improved by what the
	// listing left out.
	MeanDelta float64 `json:"mean_delta"`
	MaxDelta  float64 `json:"max_delta"`
	// Pairs is the worst maxResidualRecoveryPairs compared pairs by
	// absolute Delta, largest first, ties broken by (A, B) so the
	// listing is a function of the numbers alone. Omitted is how many
	// compared pairs it left out; every one of them recovered at least
	// as well as the last entry listed.
	Pairs   []*ModelResidualPairFidelity `json:"pairs,omitempty"`
	Omitted int                          `json:"omitted,omitempty"`
	// Unmeasured holds pairs generation applied that the synthetic
	// partition could not produce a correlation for, each with its
	// reason, capped the same way. It is a separate list rather than an
	// Error field on ModelResidualPairFidelity for the same structural
	// reason ResidualCorrelationProfile splits its two lists: an
	// unmeasured pair has no rho, and a struct with a rho slot in it is
	// how a zero gets written into one.
	Unmeasured        []*ModelResidualUnmeasuredPair `json:"unmeasured,omitempty"`
	UnmeasuredOmitted int                            `json:"unmeasured_omitted,omitempty"`
}

// ModelResidualPairFidelity is one applied residual correlation's
// recovery.
type ModelResidualPairFidelity struct {
	A string `json:"a"`
	B string `json:"b"`
	// CapturedRho is CorrelationSpec.Correlation — the figure
	// buildResidualCorrelator was asked to impose, which is the source
	// residual correlation the profile captured. RecoveredRho is the
	// Pearson correlation between the two models' REFIT residuals over
	// the synthetic partition, on the latent scale. Delta is their
	// absolute difference.
	CapturedRho  float64 `json:"captured_rho"`
	RecoveredRho float64 `json:"recovered_rho"`
	Delta        float64 `json:"delta"`
	// N is the number of synthetic rows carrying BOTH residuals — the
	// realized pair's own co-occurrence count within the refit row cap,
	// not the cohort's row count and not either model's NObs.
	N int `json:"n"`
	// Flagged is true when Delta exceeds ResidualRecoveryTolerance.
	Flagged bool `json:"flagged,omitempty"`
}

// ModelResidualUnmeasuredPair records one applied residual correlation
// the synthetic partition could not be measured for, and why. It
// deliberately carries no recovered-rho slot at all — see
// ResidualUnmeasuredPair, whose reasoning this mirrors exactly.
type ModelResidualUnmeasuredPair struct {
	A string `json:"a"`
	B string `json:"b"`
	// CapturedRho still rides, because the reader needs to know how much
	// structure went unverified: an unmeasured pair captured at 0.8 is a
	// far larger hole in the report than one captured at 0.02.
	CapturedRho float64 `json:"captured_rho"`
	// N is how many synthetic rows DID carry both residuals — zero or a
	// handful for ResidualUnmeasuredNoOverlap, and however many
	// overlapped for ResidualUnmeasuredNoVariance. Zero and meaningless
	// for ResidualUnmeasuredNoModelFit, where one side has no residual
	// vector at all.
	N int `json:"n"`
	// Reason is ResidualUnmeasuredNoOverlap, ResidualUnmeasuredNoVariance
	// or ResidualUnmeasuredNoModelFit.
	Reason string `json:"reason"`
}

// recoveredResiduals is one applied model's per-row latent residuals
// over the synthetic partition, row-aligned with the decoded row slice
// so two of them intersect by index — the same alignment contract
// FieldModel.Residuals / ResidualPresent carry at capture, and the
// reason coResidualSlices serves both.
type recoveredResiduals struct {
	values  []float64
	present []bool
}

// newRecoveredResiduals allocates a residual vector aligned to n
// decoded rows, every row initially absent.
func newRecoveredResiduals(n int) *recoveredResiduals {
	return &recoveredResiduals{values: make([]float64, n), present: make([]bool, n)}
}

// set records one admitted row's latent residual.
func (r *recoveredResiduals) set(row int, v float64) {
	r.values[row] = v
	r.present[row] = true
}

// buildModelResidualFidelity scores every residual correlation
// generation APPLIED against the refit residuals E5-S1's pass produced.
//
// Which correlations were applied is asked of generation's own
// compiler, not inferred from the Spec: buildResidualCorrelator decides
// participation by whether both endpoints reached a compiled
// modelDrawer, and it is a pure function of (correlations, drawers), so
// calling it here against the same drawers reproduces the exact
// participant set and component order the row loop used. A correlation
// naming a field whose model was dropped is therefore absent from this
// section rather than scored — the v0.32.2 rule, applied to the one
// relationship kind ResolveConflicts does not return.
//
// residualsOf is keyed by target field; a nil entry means that model's
// refit did not run, which is an unmeasured pair rather than a missing
// one.
//
// Returns nil when no pair is left to report, so a spec carrying no
// residual correlations — every spec predating `profile create
// --residual-correlations` — leaves the section off the wire entirely.
func buildModelResidualFidelity(spec *Spec, drawers []*modelDrawer, residualsOf map[string]*recoveredResiduals) *ModelResidualCorrelationFidelity {
	rc, _, err := buildResidualCorrelator(spec.ResidualCorrelations, drawers)
	if err != nil || rc == nil {
		return nil
	}

	participates := make(map[string]bool, len(rc.fields))
	for _, f := range rc.fields {
		participates[f] = true
	}

	out := &ModelResidualCorrelationFidelity{Fields: append([]string(nil), rc.fields...)}
	var measured []*ModelResidualPairFidelity
	// Enumeration follows the Spec's own slice order — never a map
	// range — so the aggregate float folds below are a function of the
	// spec alone. Float addition is not associative and Go randomizes
	// map iteration; a mean that moves in its last bits between two runs
	// over identical inputs is exactly the defect E2-S4 fixed at capture
	// (see skills/synthetic-data.md, Determinism).
	for _, c := range spec.ResidualCorrelations {
		if !participates[c.A] || !participates[c.B] {
			continue
		}
		a, b := residualsOf[c.A], residualsOf[c.B]
		if a == nil || b == nil {
			out.Unmeasured = append(out.Unmeasured, &ModelResidualUnmeasuredPair{
				A: c.A, B: c.B, CapturedRho: c.Correlation, Reason: ResidualUnmeasuredNoModelFit,
			})
			continue
		}
		xs, ys := coResidualSlices(a.values, a.present, b.values, b.present)
		if len(xs) < minResidualPairObservations {
			out.Unmeasured = append(out.Unmeasured, &ModelResidualUnmeasuredPair{
				A: c.A, B: c.B, CapturedRho: c.Correlation, N: len(xs),
				Reason: ResidualUnmeasuredNoOverlap,
			})
			continue
		}
		rho := pearson(xs, ys)
		if math.IsNaN(rho) {
			// pearson returns NaN only for n < 2 (excluded above) or a
			// zero-variance side, so this arm is exactly the constant
			// residual case — a model that fits the generated rows
			// perfectly, which a discrete target with few reachable
			// values can genuinely produce.
			out.Unmeasured = append(out.Unmeasured, &ModelResidualUnmeasuredPair{
				A: c.A, B: c.B, CapturedRho: c.Correlation, N: len(xs),
				Reason: ResidualUnmeasuredNoVariance,
			})
			continue
		}
		e := &ModelResidualPairFidelity{
			A: c.A, B: c.B, CapturedRho: c.Correlation, RecoveredRho: rho,
			Delta: math.Abs(rho - c.Correlation), N: len(xs),
		}
		e.Flagged = e.Delta > ResidualRecoveryTolerance
		measured = append(measured, e)
	}

	if len(measured) == 0 && len(out.Unmeasured) == 0 {
		return nil
	}

	out.Compared = len(measured)
	var sum float64
	for _, e := range measured {
		sum += e.Delta
		if e.Flagged {
			out.Flagged++
		}
		if e.Delta > out.MaxDelta {
			out.MaxDelta = e.Delta
		}
	}
	if out.Compared > 0 {
		out.MeanDelta = sum / float64(out.Compared)
	}

	sort.SliceStable(measured, func(i, j int) bool {
		if measured[i].Delta != measured[j].Delta {
			return measured[i].Delta > measured[j].Delta
		}
		if measured[i].A != measured[j].A {
			return measured[i].A < measured[j].A
		}
		return measured[i].B < measured[j].B
	})
	if len(measured) > maxResidualRecoveryPairs {
		out.Omitted = len(measured) - maxResidualRecoveryPairs
		measured = measured[:maxResidualRecoveryPairs]
	}
	out.Pairs = measured

	// The unmeasured listing is capped the same way but ordered by the
	// magnitude of what went unverified rather than by a delta it does
	// not have: a pair captured at 0.8 that could not be checked is a
	// bigger hole than one captured at 0.02.
	sort.SliceStable(out.Unmeasured, func(i, j int) bool {
		ai, aj := math.Abs(out.Unmeasured[i].CapturedRho), math.Abs(out.Unmeasured[j].CapturedRho)
		if ai != aj {
			return ai > aj
		}
		if out.Unmeasured[i].A != out.Unmeasured[j].A {
			return out.Unmeasured[i].A < out.Unmeasured[j].A
		}
		return out.Unmeasured[i].B < out.Unmeasured[j].B
	})
	if len(out.Unmeasured) > maxResidualRecoveryPairs {
		out.UnmeasuredOmitted = len(out.Unmeasured) - maxResidualRecoveryPairs
		out.Unmeasured = out.Unmeasured[:maxResidualRecoveryPairs]
	}
	return out
}
