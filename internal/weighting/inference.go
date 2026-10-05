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
	nStar := b.NStar(basis)
	if !(nStar > 1) {
		return 0
	}
	return b.ScaledM2(basis) / (nStar - 1)
}

// ScaledM2 is M2 on w*: c·M2 (M2 itself under Unweighted / Frequency).
func (b *Welford) ScaledM2(basis Basis) float64 {
	if basis != Probability {
		return b.M2
	}
	return basis.Scale(b.SumW, b.SumWSq) * b.M2
}
