package processing

import (
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/weighting"
	"github.com/frankbardon/pulse/types"
)

// Weighted-host reader for the mean-comparison overlays
// (.claude/reference/weighting.md, Overlays). A host cell is WEIGHTED
// when it carries the weighted floor key sum_weights — whatever the
// weight's source (slot, request, Options.DefaultWeight) — and its kind
// is read off the floor too: n_eff is stamped only under kind
// probability. The Welford triple's own `n` is the raw row count and
// its `variance` is the frequency form m2/(Σw − 1), so on a weighted
// cell neither is read: N* is sum_weights (frequency) or n_eff
// (probability), and the variance is recomputed from m2 and N*
// (weighting.ScaledVariance). An unweighted cell reads {mean, variance,
// n} exactly as before, so an unweighted host is byte-identical.
//
// OVERLAY_T_CELL / OVERLAY_Z_CELL / OVERLAY_T_VS_REF / OVERLAY_Z_VS_REF
// and the Welford-input pairwise kinds read every leg through meanLeg;
// OVERLAY_PAIRWISE_WEIGHTED_TWO_MEANS_Z keeps its own n_basis contract.

// meanLeg is one host cell's (or series row's) two-sample inputs.
type meanLeg struct {
	mean, variance float64
	// nStar is the sample size the test reads: the raw n on an
	// unweighted cell, N* on a weighted one.
	nStar float64
	// basis is Unweighted, Frequency or Probability.
	basis weighting.Basis
	// sumW is Σw (weighted cells only).
	sumW float64
}

// readMeanLeg reads a components map carrying the {mean, variance, n}
// triple; ok=false when any of the three is missing or non-numeric (the
// caller's scalar fallback engages, as before).
func readMeanLeg(m map[string]any) (meanLeg, bool) {
	if m == nil {
		return meanLeg{}, false
	}
	mean, okM := componentsNumeric(m, "mean")
	variance, okV := componentsNumeric(m, "variance")
	n, okN := componentsNumeric(m, "n")
	if !okM || !okV || !okN {
		return meanLeg{}, false
	}
	leg := meanLeg{mean: mean, variance: variance, nStar: n}
	sumW, weighted := componentsNumeric(m, "sum_weights")
	if !weighted {
		return leg, true
	}
	leg.sumW = sumW
	leg.basis = weighting.Frequency
	leg.nStar = sumW
	if nEff, ok := componentsNumeric(m, "n_eff"); ok {
		leg.basis = weighting.Probability
		leg.nStar = nEff
	}
	m2, ok := componentsNumeric(m, "m2")
	if !ok {
		// A map without m2 (an embedder-built triple): the emitted
		// variance is the frequency form m2/(Σw − 1), so m2 is
		// recoverable whenever that form is defined.
		m2 = 0
		if sumW > 1 {
			m2 = variance * (sumW - 1)
		}
	}
	leg.variance = weighting.ScaledVariance(leg.basis, m2, sumW, leg.nStar)
	return leg, true
}

// MeanLeg reads cell (r, c)'s two-sample inputs through the
// weighted-host rule: (mean, variance on w*, N*) on a weighted cell,
// (mean, variance, n) on an unweighted one. ok=false unless the cell
// carries the {mean, variance, n} triple.
func (h *CrosstabHostView) MeanLeg(rowIdx, colIdx int) (meanLeg, bool) {
	if h == nil || h.components == nil {
		return meanLeg{}, false
	}
	cc := h.components.CellComponents
	if rowIdx < 0 || rowIdx >= len(cc) || colIdx < 0 || colIdx >= len(cc[rowIdx]) {
		return meanLeg{}, false
	}
	return readMeanLeg(cc[rowIdx][colIdx])
}

// WeightBasis reports the host's weight basis: the basis of the first
// cell carrying sum_weights (n_eff present ⇒ Probability, else
// Frequency), Unweighted when no cell does. Every cell of one host
// shares its cell slot's weight, so the first weighted cell speaks for
// all of them.
func (h *CrosstabHostView) WeightBasis() weighting.Basis {
	if h == nil || h.components == nil {
		return weighting.Unweighted
	}
	for _, row := range h.components.CellComponents {
		for _, cell := range row {
			if cell == nil {
				continue
			}
			if _, ok := componentsNumeric(cell, "sum_weights"); !ok {
				continue
			}
			if _, ok := componentsNumeric(cell, "n_eff"); ok {
				return weighting.Probability
			}
			return weighting.Frequency
		}
	}
	return weighting.Unweighted
}

// weightedLegTally accumulates the weighted legs a layer read, for the
// layer summary's Parameters: sum_weights = Σw over the legs, and under
// probability n_eff = the Kish effective size of their union,
// (ΣΣw)²/ΣΣw² with each leg's Σw² recovered as Σw²/n_eff. A layer that
// read no weighted leg reports nothing, so an unweighted host's summary
// is unchanged.
type weightedLegTally struct {
	sumW, sumWSq float64
	weighted     bool
	probability  bool
}

func (t *weightedLegTally) add(l meanLeg) {
	if !l.basis.Weighted() {
		return
	}
	t.weighted = true
	t.sumW += l.sumW
	if l.basis == weighting.Probability {
		t.probability = true
		if l.nStar > 0 {
			t.sumWSq += l.sumW * l.sumW / l.nStar
		}
	}
}

// stamp writes the tally onto summary.Parameters (no-op when no leg
// was weighted).
func (t *weightedLegTally) stamp(summary *types.OverlaySummary) {
	if summary == nil || !t.weighted {
		return
	}
	if summary.Parameters == nil {
		summary.Parameters = map[string]float64{}
	}
	summary.Parameters["sum_weights"] = t.sumW
	if t.probability {
		summary.Parameters["n_eff"] = weighting.KishNEff(t.sumW, t.sumWSq)
	}
}

// overlayNSourceWeightedRefusal is the runtime half of the weighted-host
// n_source rule (weighting.NSourceRefusal): PROCESSING_CONFIG naming the
// kind and the source, or nil. The predict validator raises the same
// code with the same reason.
func overlayNSourceWeightedRefusal(kind types.OverlayKind, nSource string, basis weighting.Basis) error {
	reason := weighting.NSourceRefusal(nSource, basis)
	if reason == "" {
		return nil
	}
	return errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
		"overlay "+string(kind)+" n_source "+nSource+": "+reason,
		map[string]any{"kind": string(kind), "param": "n_source", "n_source": nSource})
}
