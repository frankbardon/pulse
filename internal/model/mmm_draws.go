package model

import (
	"fmt"
	"maps"
	"math"
)

// mmm_draws.go reads a REG_MMM_BAYES fit's raw posterior draws
// (outputs.draws, from params.return_draws) and binds one draw as the
// fit's figures — what a Compose optimize block's `posterior` evaluates
// its objective over, one draw at a time, through the very coef() and
// response() it reads the posterior means with.

// MMMDraws is a REG_MMM_BAYES fit's outputs.draws, pooled: each
// coefficient's draws, chains in order then draws (the order the fit's
// summaries pool them in), and the credible level the fit reports at.
type MMMDraws struct {
	// Index maps a coefficient name (the coefficient table's) to its
	// column in Cols.
	Index map[string]int
	// Cols holds one pooled draw list per name, all of one length; a
	// null draw is NaN.
	Cols [][]float64
	// Level is outputs.draws.credible_level.
	Level float64
}

// Len is the number of pooled draws.
func (d *MMMDraws) Len() int {
	if d == nil || len(d.Cols) == 0 {
		return 0
	}
	return len(d.Cols[0])
}

// ReadMMMDraws reads outputs.draws in its JSON form (a fit's normalized
// outputs, or a loaded model file's): names, draws[chain][name][draw]
// and credible_level. A body that does not read as one is an error.
func ReadMMMDraws(raw map[string]any) (*MMMDraws, error) {
	names, ok := raw["names"].([]any)
	if !ok || len(names) == 0 {
		return nil, fmt.Errorf("no names")
	}
	idx := make(map[string]int, len(names))
	for j, n := range names {
		s, ok := n.(string)
		if !ok {
			return nil, fmt.Errorf("name %d is not a string", j)
		}
		idx[s] = j
	}
	chains, ok := raw["draws"].([]any)
	if !ok || len(chains) == 0 {
		return nil, fmt.Errorf("no draws")
	}
	cols := make([][]float64, len(names))
	for c, chain := range chains {
		params, ok := chain.([]any)
		if !ok || len(params) != len(names) {
			return nil, fmt.Errorf("chain %d does not hold one draw list per name", c)
		}
		for j, p := range params {
			ds, ok := p.([]any)
			if !ok {
				return nil, fmt.Errorf("chain %d, %q is not a list", c, names[j])
			}
			for _, v := range ds {
				f, ok := v.(float64)
				if !ok {
					f = math.NaN()
				}
				cols[j] = append(cols[j], f)
			}
		}
	}
	for j := range cols {
		if len(cols[j]) == 0 || len(cols[j]) != len(cols[0]) {
			return nil, fmt.Errorf("ragged draws")
		}
	}
	level, ok := raw["credible_level"].(float64)
	if !ok || !(level > 0 && level < 1) {
		return nil, fmt.Errorf("no credible_level")
	}
	return &MMMDraws{Index: idx, Cols: cols, Level: level}, nil
}

// MMMDrawsOf is the bound REG_MMM_BAYES fit's draws (ReadMMMDraws over
// its outputs.draws in JSON form, NormalizeOutputs); an error when name binds no such fit, or it
// carries no draws that read.
func (s *Set) MMMDrawsOf(name string) (*MMMDraws, error) {
	b, err := s.Lookup(name)
	if err != nil {
		return nil, err
	}
	if b.Result == nil {
		return nil, fmt.Errorf("fit %q has no result", name)
	}
	v, ok := b.Result.Outputs[MMMDrawsKey]
	if !ok {
		return nil, fmt.Errorf("fit %q carries no outputs.%s", name, MMMDrawsKey)
	}
	// A Compose fit's outputs hold the model's own Go values; a loaded
	// file's are already in JSON form. Read both in that form.
	norm, err := NormalizeOutputs(map[string]any{MMMDrawsKey: v})
	if err != nil {
		return nil, err
	}
	raw, ok := norm[MMMDrawsKey].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("fit %q: outputs.%s is not an object", name, MMMDrawsKey)
	}
	return ReadMMMDraws(raw)
}

// WithMMMDraw is s with fit bound at draw k of d instead of its
// posterior means: every coefficient the draws name takes its k-th
// draw — the intercept, each predictor, each curve, kernel and per-geo
// term coef() reads — and each adstock record's kernel parameters take
// the draw's (the record's prefixes on the bare channel; response()
// reads a geo's own through coef()). Every other binding, and every
// other figure of the fit (its intervals, standard errors), is s's. s
// is not modified.
func (s *Set) WithMMMDraw(fit string, d *MMMDraws, k int) (*Set, error) {
	b, err := s.Lookup(fit)
	if err != nil {
		return nil, err
	}
	if b.Result == nil || k < 0 || k >= d.Len() {
		return nil, fmt.Errorf("fit %q: no draw %d", fit, k)
	}
	res := *b.Result
	res.Coefficients = maps.Clone(b.Result.Coefficients)
	for name, j := range d.Index {
		if _, ok := res.Coefficients[name]; ok {
			res.Coefficients[name] = d.Cols[j][k]
		}
	}
	nb := *b
	nb.Result = &res
	if v, ok := res.Coefficients[InterceptKey]; ok {
		nb.Intercept = v
	}
	nb.Coefficients = make([]float64, len(b.Predictors))
	for i, p := range b.Predictors {
		v, ok := res.Coefficients[p]
		if !ok {
			v = b.Coefficients[i]
		}
		nb.Coefficients[i] = v
	}
	if b.Adstocks != nil {
		nb.Adstocks = make(map[string]Adstock, len(b.Adstocks))
		for ch, a := range b.Adstocks {
			if prefixes, ok := MMMBayesAdstockPrefixes(a.Kernel); ok {
				for _, p := range prefixes {
					if v, ok := res.Coefficients[p+ch]; ok {
						a.setParam(p, v)
					}
				}
			}
			nb.Adstocks[ch] = a
		}
	}
	out := *s
	out.byName = maps.Clone(s.byName)
	out.byName[fit] = &nb
	return &out, nil
}

// Quantile7 is the p-quantile of sorted (ascending, non-empty;
// Hyndman–Fan type 7, R's and NumPy's default): the linear
// interpolation between order statistics, the product barriered — the
// quantile REG_MMM_BAYES's credible intervals use.
func Quantile7(sorted []float64, p float64) float64 {
	n := len(sorted)
	if n == 1 {
		return sorted[0]
	}
	h := float64(float64(n-1) * p)
	lo := int(math.Floor(h))
	if lo >= n-1 {
		return sorted[n-1]
	}
	return sorted[lo] + float64((h-float64(lo))*(sorted[lo+1]-sorted[lo]))
}
