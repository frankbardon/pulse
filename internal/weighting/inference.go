package weighting

import "github.com/frankbardon/pulse/types"

// Weighted inference — the one formula rule every weighted test,
// confidence bound, regression and overlay builds on
// (.claude/reference/weighting.md, Weighted inference):
//
//	N* = Σw                  under kind frequency (w = 3 is three identical rows)
//	N* = n_eff = (Σw)²/Σw²   under kind probability (Kish)
//	w* = w·N*/Σw             so Σw* = N*
//
// and every weighted formula is the FREQUENCY formula evaluated on w*.
// Point estimates (means, r) are scale-free, hence identical across the
// kinds; spreads, standard errors and degrees of freedom read N*.
// Unweighted input is the special case w ≡ 1, where N* = Σw = n.

// Basis names how a sample's Σw becomes the sample size its inference
// uses. The zero value is Unweighted.
type Basis int

const (
	// Unweighted: every row weighs 1, so N* = n.
	Unweighted Basis = iota
	// Frequency: N* = Σw.
	Frequency
	// Probability: N* = Kish n_eff.
	Probability
)

// BasisOf returns the basis of a resolved weight spec: Unweighted for
// nil, else its EFFECTIVE kind (an empty kind is probability).
func BasisOf(spec *types.WeightSpec) Basis {
	switch {
	case spec == nil:
		return Unweighted
	case spec.EffectiveKind() == types.WeightKindFrequency:
		return Frequency
	}
	return Probability
}

// Weighted reports whether the basis carries a weight.
func (b Basis) Weighted() bool { return b != Unweighted }

// KishNEff is Kish's effective sample size (Σw)²/Σw²; 0 for an empty
// sample (Σw² == 0).
func KishNEff(sumW, sumWSq float64) float64 {
	if sumWSq == 0 {
		return 0
	}
	return sumW * sumW / sumWSq
}

// NStar is the sample size inference uses under b: Σw (Unweighted,
// Frequency) or Kish n_eff (Probability).
func (b Basis) NStar(sumW, sumWSq float64) float64 {
	if b == Probability {
		return KishNEff(sumW, sumWSq)
	}
	return sumW
}

// Scale is the factor c = N*/Σw that maps the weights w onto w*. It is
// exactly 1 under Unweighted and Frequency (no multiply happens, so the
// unweighted operation order is untouched); 0 for an empty sample.
func (b Basis) Scale(sumW, sumWSq float64) float64 {
	if b != Probability {
		return 1
	}
	if sumW == 0 {
		return 0
	}
	return KishNEff(sumW, sumWSq) / sumW
}

// Welford is the weighted Welford–West bucket: the running (Σw, Σw²,
// weighted mean, M2_w = Σw·(x − mean)²) of one sample, plus its raw row
// count. Add folds one row in the operation-order exactness form —
// (w·δ)/Σw and (w·δ)·δ' — so w ≡ 1 reproduces the unweighted Welford
// recurrence bit for bit (Σw is then the exact integer n). The zero
// value is an empty bucket.
type Welford struct {
	// N is the number of rows folded (the raw `n` a test reports).
	N int64
	// SumW is Σw, SumWSq Σw².
	SumW, SumWSq float64
	// Mean is the weighted mean Σw·x/Σw.
	Mean float64
	// M2 is Σw·(x − Mean)².
	M2 float64
}

// Add folds value x with weight w (> 0; the caller has already excluded
// invalid and zero weights, which contribute nothing).
func (b *Welford) Add(x, w float64) {
	b.N++
	b.SumW += w
	b.SumWSq += w * w
	delta := x - b.Mean
	wd := w * delta
	b.Mean += wd / b.SumW
	b.M2 += wd * (x - b.Mean)
}

// NStar is the bucket's N* under basis.
func (b *Welford) NStar(basis Basis) float64 { return basis.NStar(b.SumW, b.SumWSq) }

// NEff is the bucket's Kish n_eff (reported under kind probability).
func (b *Welford) NEff() float64 { return KishNEff(b.SumW, b.SumWSq) }

// Variance is the sample variance on w*: c·M2/(N* − 1) — m2/(Σw − 1)
// under Frequency (the unbiased m2/(n − 1) under Unweighted) and
// M2·n_eff/(Σw·(n_eff − 1)) under Probability. 0 when N* ≤ 1, so the
// caller detects degeneracy itself.
func (b *Welford) Variance(basis Basis) float64 {
	return ScaledVariance(basis, b.M2, b.SumW, b.NStar(basis))
}

// ScaledVariance is the sample variance on w* of a sample summarised
// by its M2 = Σw·(x − mean)², Σw and N* (its NStar under basis):
// c·M2/(N* − 1) with c = N*/Σw under Probability and 1 otherwise —
// Welford.Variance's exact operation order, for a reader that holds the
// moments rather than the bucket (an overlay reading a host cell's m2,
// sum_weights and n_eff). 0 when N* ≤ 1.
func ScaledVariance(basis Basis, m2, sumW, nStar float64) float64 {
	if !(nStar > 1) {
		return 0
	}
	if basis == Probability {
		c := 0.0
		if sumW != 0 {
			c = nStar / sumW
		}
		m2 = c * m2
	}
	return m2 / (nStar - 1)
}

// ScaledM2 is M2 on w*: c·M2 (M2 itself under Unweighted / Frequency).
func (b *Welford) ScaledM2(basis Basis) float64 {
	if basis != Probability {
		return b.M2
	}
	return basis.Scale(b.SumW, b.SumWSq) * b.M2
}

// Weighted-host n_source rule (.claude/reference/weighting.md, Overlays).
// An overlay on a weighted host — a host cell carrying the weighted floor
// keys, whatever the weight's source — takes its sample size from N*
// (sum_weights under frequency, n_eff under probability). An explicit
// n_source selector is judged against that: an UNWEIGHTED count — the
// raw row counts (cell, margin, n_within slab) and the distinct-key
// counts — is never a valid sample size there, and a weight-sum source
// (cell_weight_sum, or cell_value_weighted reading a Σw cell) is one
// only under frequency. The payload-margin modes of the Compose panel
// (row_margin_value, row_margin_value_within) are NOT refused: they
// read the base's Σw, which the overlay maps onto N* itself.

// WeightSumNotSampleSize is why Σw is never a sample size under kind
// probability (U12 review WS-07): probability weights are defined only
// up to scale, so their sum can sit on either side of the effective
// size. NSourceRefusal and NBasisRefusal share it.
const WeightSumNotSampleSize = "the weight sum is arbitrary in scale under weight kind probability (it can overstate or understate the sample size)"

// NSourceRefusal is why an explicit overlay n_source cannot stand on a
// host weighted under basis, "" when it can (an omitted source reads
// the kind-driven N*; a source the rule does not name keeps its own
// contract). The predict validator and the overlay runtime both raise
// it as PROCESSING_CONFIG, so the two arms refuse identically. The
// spellings are the pairwise family's (types.PairwiseNSource*) plus
// the Compose panel's (types.PanelNSource*); the two share
// cell_n_unweighted.
func NSourceRefusal(nSource string, basis Basis) string {
	if !basis.Weighted() {
		return ""
	}
	switch nSource {
	case types.PairwiseNSourceCellNUnweighted, types.PairwiseNSourceRowMarginN,
		types.PairwiseNSourceColumnMarginN, types.PairwiseNSourceNWithin:
		return "raw row count is not a valid sample size on a weighted host; omit n_source to read the weighted sample size N* (sum_weights under kind frequency, n_eff under kind probability)"
	case types.PairwiseNSourceNWithinDistinct, types.PairwiseNSourceRowMarginDistinct,
		types.PairwiseNSourceColumnMarginDistinct, types.PanelNSourceRowMarginDistinctWithin:
		return "a distinct-key count is unweighted, so it is not a valid sample size on a weighted host; omit n_source to read the weighted sample size N* (sum_weights under kind frequency, n_eff under kind probability)"
	case types.PairwiseNSourceCellWeightSum, types.PairwiseNSourceCellValueWeight:
		if basis == Probability {
			return WeightSumNotSampleSize + "; omit n_source to read Kish n_eff"
		}
	}
	return ""
}

// FrequencyOnlyHostRefusal is the PULSE_WEIGHT_UNSUPPORTED refusal of a
// frequency-only overlay (OVERLAY_FISHER_EXACT_CELL) whose HOST crosstab
// cell is weighted under kind probability — the case the slot-level
// class check cannot see because no weight reaches the overlay slot
// itself (the cell carries its own slot weight, or the overlay opted
// out with `weight: null` while the cell kept the request weight).
// slot is the overlay's slot ("overlays[i]"). The predict validator and
// the overlay runtime both raise it, so the two arms refuse with one
// message and one details map.
func FrequencyOnlyHostRefusal(slot, operator string) (string, map[string]any) {
	msg := slot + ": " + operator + " has a weighted form only under weight kind \"" + string(types.WeightKindFrequency) +
		"\", and its host crosstab cell is weighted under kind \"" + string(types.WeightKindProbability) +
		"\"; use a frequency weight (integer replication counts) on the crosstab cell or set \"weight\": null on it to run it unweighted"
	return msg, map[string]any{"slot": slot, "operator": operator, "host": "crosstab.cell",
		"kind": string(types.WeightKindProbability), "supported_kinds": []string{string(types.WeightKindFrequency)}}
}

// ScalesByHostFloor reports whether an overlay kind reads its host's
// payload Σw (a cell, a margin, a table) as counts and, under weight
// kind probability, scales them onto Kish n_eff with the n_eff it reads
// off the host's weighted floor (Response.Components): the χ² kinds and
// the Compose proportion kinds. Under kind frequency Σw already IS N*,
// so the floor is not needed there.
func ScalesByHostFloor(kind types.OverlayKind) bool {
	switch kind {
	case types.OverlayKindChiSqRow, types.OverlayKindChiSqCol, types.OverlayKindChiSqMatrix,
		types.OverlayKindChiSqVsRef, types.OverlayKindPropZCell, types.OverlayKindPropZPanel:
		return true
	}
	return false
}

// HiddenFloorRefusal is the PROCESSING_CONFIG refusal of a
// ScalesByHostFloor kind whose host is weighted under kind probability
// but built with components disabled (Options.DisableComponents or the
// request's disable_components): the floor that carries n_eff is
// absent, so the overlay would read Σw as a sample size — silently
// anti-conservative. slot is the overlay's slot ("overlays[i]"), host
// the host slot ("crosstab.cell", or "requests[j]" on Compose). The
// predict validators and the runtime raise it with one message and one
// details map.
func HiddenFloorRefusal(slot string, kind types.OverlayKind, host string) (string, map[string]any) {
	msg := slot + ": " + string(kind) + " on a host weighted under kind \"" + string(types.WeightKindProbability) +
		"\" scales its weight sums to Kish n_eff read from the host's components, and " + host +
		" was built with components disabled, so the weight sums would be read as the sample size; " +
		"enable components on that host (drop disable_components / Options.DisableComponents) or use a frequency weight"
	return msg, map[string]any{"slot": slot, "operator": string(kind), "host": host,
		"kind": string(types.WeightKindProbability), "reason": "components_disabled"}
}
