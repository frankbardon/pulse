// Package collinearity is the variance-inflation arithmetic MAT_COLLINEARITY
// and a regression's collinearity block (types.RegressionSpec.Collinearity,
// U65 E3-S2) share, and the one rule over that block, read by the no-execute
// predict pass (internal/descriptor) and the regression engines
// (internal/processing/regression) so the two refuse the same requests with
// the same code, message and details. It imports neither side.
//
// Arithmetic (FMA-free, every product barriered; InverseSPD and Cholesky
// are linalg's REFERENCE kernels, so the figures are arch-independent):
//
//   - GVIF: per term S (a set of columns of the correlation R), Fox &
//     Monette's (1992) generalized VIF det(R_SS)·det(R_−S,−S)/det(R),
//     formed as det(R_SS)·det((R⁻¹)_SS) (Jacobi's identity: det(R_−S,−S)/
//     det(R) is the determinant of R⁻¹'s S block) so one inverse serves
//     every term. A single column's is (R⁻¹)_jj — the classical VIF, the
//     very value MAT_COLLINEARITY has always reported, bit for bit. car::vif
//     computes the same figure on the coefficient correlation
//     cov2cor(vcov), which Fox & Monette show is equal.
//   - Scaled: GVIF^(1/(2·df)), car's GVIF^(1/(2*Df)) (√ on one column).
//   - Correlation: a cross-product scaled to unit diagonal (cov2cor).
//
// Rule (Check), every refusal PROCESSING_CONFIG with details.slot
// "collinearity" and details.requires naming the fix:
//
//   - shape: basis model | predictors ("basis model or predictors"); each
//     group named ("a group name"), with one or more predictors, each a
//     predictor of the regression in at most one group ("a predictor"), its
//     name unlike every other term's ("a unique term name");
//   - the regression type: REG_OLS or REG_GLM ("type REG_OLS or REG_GLM");
//     an extension or model regression is the shared-slot table's refusal
//     (slot collinearity), never this rule's;
//   - the fit: an intercept ("intercept" — a VIF without one reads the
//     uncentered design, which car warns "may not be sensible"); no
//     selection ("no_selection" — the fitted predictors are the search's);
//     a REG_GLM penalty only under basis predictors ("unpenalized" — the
//     model basis's working weights would be the shrunken fit's);
//   - two or more terms ("two_or_more_terms" — car::vif's own floor).
package collinearity

import (
	"math"
	"slices"
	"strings"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/regsolve"
	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/linalg/detmath"
	"github.com/frankbardon/pulse/types"
)

// Term is one resolved term: its name and its columns, indices into the
// regression's Predictors in ascending order.
type Term struct {
	Name    string
	Columns []int
}

// Terms is spec's resolved term list: every predictor no group names is
// a term of its own, a group is one term placed at its first predictor's
// position. Nil when spec carries no block. It assumes Check passed.
func Terms(spec *types.RegressionSpec) []Term {
	if spec == nil || spec.Collinearity == nil {
		return nil
	}
	groupOf := map[string]int{}
	for g, grp := range spec.Collinearity.Groups {
		for _, p := range grp.Predictors {
			groupOf[p] = g
		}
	}
	var out []Term
	at := map[int]int{} // group → its index in out
	for j, p := range spec.Predictors {
		g, grouped := groupOf[p]
		if !grouped {
			out = append(out, Term{Name: p, Columns: []int{j}})
			continue
		}
		if i, seen := at[g]; seen {
			out[i].Columns = append(out[i].Columns, j)
			continue
		}
		at[g] = len(out)
		out = append(out, Term{Name: spec.Collinearity.Groups[g].Name, Columns: []int{j}})
	}
	return out
}

// Singletons is p one-column terms, 0 … p−1: the classical VIF of each
// column.
func Singletons(p int) [][]int {
	out := make([][]int, p)
	for j := range out {
		out[j] = []int{j}
	}
	return out
}

// Correlation is c scaled to unit diagonal, c_ij / √(c_ii·c_jj), the
// diagonal exactly 1. ok is false when a diagonal entry is not a
// positive finite number (a constant column: no correlation).
func Correlation(c *linalg.Sym) (r *linalg.Sym, ok bool) {
	p := c.N()
	for i := 0; i < p; i++ {
		if d := c.At(i, i); !(d > 0) || math.IsInf(d, 0) {
			return nil, false
		}
	}
	r, _ = linalg.NewSym(p, nil)
	for i := 0; i < p; i++ {
		r.Set(i, i, 1)
		for j := i + 1; j < p; j++ {
			r.Set(i, j, c.At(i, j)/math.Sqrt(float64(c.At(i, i)*c.At(j, j))))
		}
	}
	return r, true
}

// GVIF returns each term's generalized VIF over the correlation r: a
// one-column term's (R⁻¹)_jj, a wider one's det(R_SS)·det((R⁻¹)_SS)
// (see the package comment). err is InverseSPD's (PULSE_MATRIX_SINGULAR
// when r is not positive definite).
func GVIF(r *linalg.Sym, terms [][]int) ([]float64, error) {
	inv, err := linalg.InverseSPD(r)
	if err != nil {
		return nil, err
	}
	out := make([]float64, len(terms))
	for t, cols := range terms {
		if len(cols) == 1 {
			out[t] = inv.At(cols[0], cols[0])
			continue
		}
		out[t] = float64(detSPD(block(r, cols)) * detSPD(block(inv, cols)))
	}
	return out, nil
}

// Scaled is gvif^(1/(2·df)): √gvif on one column, else detmath.Pow.
func Scaled(gvif float64, df int) float64 {
	if df == 1 {
		return math.Sqrt(gvif)
	}
	return detmath.Pow(gvif, 1/float64(2*df))
}

// block is s's principal submatrix on cols.
func block(s *linalg.Sym, cols []int) *linalg.Sym {
	b, _ := linalg.NewSym(len(cols), nil)
	for a, i := range cols {
		for c := a; c < len(cols); c++ {
			b.Set(a, c, s.At(i, cols[c]))
		}
	}
	return b
}

// detSPD is the determinant of the positive-definite s, Π L_ii² over
// its Cholesky factor (diagonal ascending); NaN when s does not factor.
func detSPD(s *linalg.Sym) float64 {
	l, err := linalg.Cholesky(s)
	if err != nil {
		return math.NaN()
	}
	d := 1.0
	for i := 0; i < s.N(); i++ {
		v := l.At(i, i)
		d = float64(d * float64(v*v))
	}
	return d
}

func refuse(spec *types.RegressionSpec, msg, requires string, extra map[string]any) *errors.CodedError {
	details := map[string]any{"type": string(spec.Type), "slot": "collinearity", "requires": requires}
	for k, v := range extra {
		details[k] = v
	}
	if spec.Name != "" {
		details["name"] = spec.Name
	}
	return errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG, string(spec.Type)+" collinearity "+msg, details)
}

// builtin reports whether t is a built-in regression this rule judges
// (an extension or model regression is the shared-slot table's).
func builtin(t types.RegressionType) bool {
	switch t {
	case types.REG_OLS, types.REG_GLM, types.REG_BAYES_LINEAR:
		return true
	}
	return false
}

// Check returns the PROCESSING_CONFIG refusal of spec's collinearity
// block, or nil (no block, a well-formed block on a supported fit, or a
// non-built-in type). The engine (ValidateRegression) and predict
// (validateRegressions) both call it.
func Check(spec *types.RegressionSpec) *errors.CodedError {
	if spec == nil || spec.Collinearity == nil || !builtin(spec.Type) {
		return nil
	}
	c := spec.Collinearity
	if c.Basis != "" && !slices.Contains(types.AllCollinearityBases(), c.Basis) {
		return refuse(spec, "basis must be one of "+strings.Join(types.AllCollinearityBases(), ", ")+" (empty selects model); got "+c.Basis,
			"basis model or predictors", map[string]any{"key": "basis", "value": c.Basis, "allowed": types.AllCollinearityBases()})
	}
	grouped := map[string]string{}
	for _, g := range c.Groups {
		if g.Name == "" {
			return refuse(spec, "groups need a name each (the term's key in the result)",
				"a group name", map[string]any{"key": "groups"})
		}
		if len(g.Predictors) == 0 {
			return refuse(spec, "group "+g.Name+" names no predictor",
				"a predictor", map[string]any{"key": "groups", "group": g.Name})
		}
		for _, p := range g.Predictors {
			if !slices.Contains(spec.Predictors, p) {
				return refuse(spec, "group "+g.Name+" names "+p+", which is not one of the regression's predictors",
					"a predictor", map[string]any{"key": "groups", "group": g.Name, "predictor": p})
			}
			if prev, dup := grouped[p]; dup {
				return refuse(spec, "groups name "+p+" twice ("+prev+" and "+g.Name+"); a predictor joins at most one group",
					"a predictor", map[string]any{"key": "groups", "group": g.Name, "predictor": p})
			}
			grouped[p] = g.Name
		}
	}
	names := map[string]bool{}
	for _, t := range Terms(spec) {
		if names[t.Name] {
			return refuse(spec, "term name "+t.Name+" is used twice (a group's name must differ from every other group's and from every ungrouped predictor)",
				"a unique term name", map[string]any{"key": "groups", "term": t.Name})
		}
		names[t.Name] = true
	}
	if spec.Type != types.REG_OLS && spec.Type != types.REG_GLM {
		return refuse(spec, "is reported by REG_OLS and REG_GLM only (a posterior has no design-based VIF here); drop collinearity, or run MAT_COLLINEARITY over the predictors",
			"type REG_OLS or REG_GLM", nil)
	}
	switch {
	case !regsolve.FitsIntercept(spec):
		return refuse(spec, "needs an intercept: without one the VIF reads the uncentered design, which measures distance from 0, not overlap between predictors; drop intercept: false or collinearity",
			"intercept", nil)
	case spec.Selection != "":
		return refuse(spec, "needs a fixed predictor set: after selection the fitted predictors are the search's; drop selection or collinearity",
			"no_selection", map[string]any{"selection": spec.Selection})
	case spec.Type == types.REG_GLM && spec.Penalty != "" && c.EffectiveBasis() == types.CollinearityBasisModel:
		return refuse(spec, "on basis model reads the fit's working weights, which a penalty shrinks; drop penalty, or set basis predictors for the predictors' own VIF",
			"unpenalized", map[string]any{"penalty": spec.Penalty, "basis": types.CollinearityBasisModel})
	}
	if n := len(Terms(spec)); n < 2 {
		return refuse(spec, "needs two or more terms (a lone term has nothing to overlap with); add a predictor or split a group",
			"two_or_more_terms", map[string]any{"terms": n})
	}
	return nil
}
