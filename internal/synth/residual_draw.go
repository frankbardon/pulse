package synth

import (
	"fmt"
	"sort"

	mrand "math/rand/v2"
)

// This file is the generation-time half of the residual correlation
// work: it turns Spec.ResidualCorrelations (written by SpecFromProfile
// from a `profile create --residual-correlations` document) into ONE
// correlated standard-normal vector per row, from which every
// participating modelDrawer takes its own component as the z of the
// composed draw value = Q(Phi(mu(row) + sigma*z)).
//
// # What changed, and why it is not a new stage
//
// Before this landed, the copula was a terminal stage that OVERWROTE
// row[field] for its participants, and a modelled field was excluded
// from it because a model owns its field's value outright — leaving the
// two structures mutually exclusive, so a field could be conditioned on
// categorical predictors or correlated with a sibling numeric but never
// both. The fix is not another stage; it is a change of what the
// correlation is a correlation OF. The Cholesky factor, the completion
// policy for pairs nobody supplied and the ridge report are all the same
// machinery on the same matrix (factorCorrelations, synth/copula.go) —
// only the consumer differs. On the value scale the correlated normal
// goes through Phi and the field's own quantile function and becomes the
// value. Here it goes into the model's linear predictor as the residual
// and comes out through the same Phi and the same Q, so the field is
// simultaneously conditioned and correlated with nothing overwriting
// anything.
//
// # Why the residual and not the value
//
// Once a numeric's systematic variation is explained by its predictors,
// the part still free to move is the residual, and imposing a
// VALUE-scale correlation on it would apply every predictor the two
// targets share a second time — two fields both driven by `region`
// correlate strongly on raw values while their residuals may be
// independent. synth/residual_corr.go's header carries the full
// argument for the capture side; this is the consuming half of the same
// statement.
//
// # What the correlation actually means downstream
//
// The construction correlates the LATENT residuals: component u_i is the
// z of field i, so the latent residual sigma_i*z_i carries the requested
// correlation exactly. For a `normal` target the map from latent to
// value is affine (Q(u) = mean + std*u), so the realized correlation
// between the two fields' VALUE-scale residuals is that same figure —
// which is the case SpecFromProfile always reconstructs, and what
// TestSynthResidual_ReconstructionWithinTolerance measures. For a
// lognormal or a `--fit-shape` mixture target the map is monotone but
// non-linear, so the RANK correlation is preserved exactly and the
// Pearson figure is attenuated — the identical, well-understood property
// the value-scale copula already documents on correlator, not a second
// approximation introduced here.
//
// # Determinism
//
// The component order is the DRAWER order, which buildModelDrawers has
// already fixed to schema field order — never the order the
// `residual_correlations` array happened to be written in, and never a
// map range. Each row consumes exactly one rng.NormFloat64() per
// participant, in that order, before any drawer runs; a drawer that does
// not participate still consumes its own single draw where it always
// did. So the per-row RNG sequence is a function of the Spec alone, and
// "same spec + same seed produces a byte-identical .pulse file" survives
// a stage in which fields now share randomness. See
// skills/synthetic-data.md (Determinism).

// residualCorrelator holds the Cholesky factor of the residual
// correlation matrix over the modelled fields that participate, plus the
// per-row scratch it fills.
//
// The scratch vectors are owned rather than allocated per row for the
// same reason drawRow reuses its row map: the motivating cohort has 105
// modelled fields, and a per-row pair of allocations of that width
// across hundreds of thousands of rows is pure garbage. They are
// per-Spec state on a struct the row loop already holds exclusively, so
// there is no sharing to reason about.
type residualCorrelator struct {
	// fields names the participants in COMPONENT order — which is
	// drawer order, i.e. schema field order. It exists for diagnostics
	// and for the tests that pin the order; the draw itself indexes by
	// the component number stamped onto each modelDrawer.
	fields []string
	chol   [][]float64
	z      []float64
	u      []float64
}

// buildResidualCorrelator compiles the surviving residual correlations
// into the per-row correlated draw, and stamps each participating
// drawer with its component index.
//
// Participation is decided by the DRAWERS, not by the spec list: a
// correlation may only name a field that actually reached a compiled
// modelDrawer, because the component it would otherwise claim has
// nothing to hand it to. A profile-derived spec reaches that case
// whenever a captured model was dropped in translation
// (modelSpecFromProfile) or failed to compile (buildModelDrawers) — both
// of which already warned about the model on its own terms — so the
// warning here names the consequence rather than re-diagnosing the
// cause, and is aggregated into one line however many pairs it covers.
//
// Returns nil (and no error) when fewer than two participants survive: a
// correlation matrix over one field is the scalar 1, and a row of it
// would consume an RNG draw to produce a plain standard normal the
// drawer would have drawn for itself. Keeping the nil case
// indistinguishable from "no residual correlations declared" is what
// makes the byte-identity claim above hold for a spec whose residual
// structure evaporated.
//
// The drawers slice is mutated in place (each participant's residual
// component index is stamped onto it) rather than returning a parallel
// lookup: the index has exactly one consumer, modelDrawer.transform, and
// a side table keyed by field name would be a per-row map lookup on the
// hottest path in the package for no gain in clarity.
func buildResidualCorrelator(correlations []CorrelationSpec, drawers []*modelDrawer) (*residualCorrelator, []string, error) {
	if len(correlations) == 0 {
		return nil, nil, nil
	}
	// A thin or empty drawer set is NOT an early return: the endpoints
	// the correlations name still have to be checked against it, because
	// "every participant lost its model" is exactly the case whose
	// silence this warning exists to break.

	// drawerOf resolves a name to its compiled drawer. Duplicate targets
	// are impossible (validateSpec refuses a second model for a field
	// and SpecFromProfile emits one per field), so the first match is
	// the only match.
	drawerOf := make(map[string]*modelDrawer, len(drawers))
	for _, d := range drawers {
		drawerOf[d.field] = d
	}

	// Participants are collected in DRAWER order rather than in the
	// order the correlation list names them, which is what makes the
	// component assignment a function of the schema instead of a
	// function of how the document was serialized.
	participates := make(map[string]bool, len(drawers))
	dropped := make(map[string]bool)
	for _, c := range correlations {
		aOK, bOK := drawerOf[c.A] != nil, drawerOf[c.B] != nil
		if !aOK {
			dropped[c.A] = true
		}
		if !bOK {
			dropped[c.B] = true
		}
		if !aOK || !bOK {
			continue
		}
		participates[c.A] = true
		participates[c.B] = true
	}

	var warnings []string
	if len(dropped) > 0 {
		warnings = append(warnings, unmodelledResidualWarning(dropped))
	}

	var names []string
	for _, d := range drawers {
		if participates[d.field] {
			names = append(names, d.field)
		}
	}
	if len(names) < 2 {
		return nil, warnings, nil
	}

	idx := make(map[string]int, len(names))
	for i, n := range names {
		idx[n] = i
	}

	// Only the pairs whose BOTH endpoints survived reach the matrix; a
	// pair with a dropped endpoint is not "assumed independent", it is
	// not a pair among participants at all. Every remaining pair among
	// participants that the list does not name IS completed as
	// independent and counted, by exactly the policy and in exactly the
	// words the value-scale matrix uses — see factorCorrelations.
	kept := make([]CorrelationSpec, 0, len(correlations))
	for _, c := range correlations {
		if participates[c.A] && participates[c.B] {
			kept = append(kept, c)
		}
	}

	chol, cholWarnings, err := factorCorrelations("residual correlation", "modelled field", names, idx, kept)
	warnings = append(warnings, cholWarnings...)
	if err != nil {
		return nil, warnings, err
	}

	for i, n := range names {
		drawerOf[n].residual = i
	}
	return &residualCorrelator{
		fields: names,
		chol:   chol,
		z:      make([]float64, len(names)),
		u:      make([]float64, len(names)),
	}, warnings, nil
}

// unmodelledResidualWarning names, once, the fields a residual
// correlation asked for and generation had no model to attach it to.
//
// Sorted and bounded for the same reason every other warning in this
// package that can scale with the schema is: the emitting condition is
// per FIELD, the cohort that motivated this work carries 105 of them,
// and a per-pair line would restate one finding up to 104 times.
func unmodelledResidualWarning(dropped map[string]bool) string {
	names := make([]string, 0, len(dropped))
	for n := range dropped {
		names = append(names, n)
	}
	sort.Strings(names)
	const shownCap = 8
	shown := names
	suffix := ""
	if len(shown) > shownCap {
		shown = shown[:shownCap]
		suffix = fmt.Sprintf(" (+%d more)", len(names)-shownCap)
	}
	return fmt.Sprintf(
		"residual correlation(s) naming %v%s dropped: the field carries no surviving linear model, "+
			"so it has no residual to correlate; the remaining participants still correlate",
		shown, suffix)
}

// draw fills the shared correlated normal vector for one row. Called
// once per row by drawRow, immediately before the model stage, so every
// drawer reading a component reads the SAME row's vector.
func (rc *residualCorrelator) draw(rng *mrand.Rand) {
	if rc == nil {
		return
	}
	correlatedNormals(rng, rc.chol, rc.z, rc.u)
}

// component returns the correlated z for a participating drawer.
func (rc *residualCorrelator) component(i int) float64 {
	return rc.u[i]
}
