package processing

import (
	"math"
	"strconv"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/weighting"
	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/types"
)

// matrix_inference.go is params.inference: the per-pair p-values and
// Fisher-z confidence intervals MAT_CORRELATION (every method; the rank
// methods' p and interval variance are matrix_rank_inference.go's) and
// MAT_PARTIAL_CORRELATION carry as auxiliary.p, auxiliary.ci_lower and
// auxiliary.ci_upper — square matrices over the output axis in the
// slot's encoding, the primary's twins cell for cell, so a later reader
// (a multiplicity p-site writing auxiliary.p_adjusted) finds the raw p
// under one key whatever the operator. The arithmetic is
// correlationInference (test_pearson.go), the one TEST_PEARSON_R runs.
//
// Inference size (weighting.md "Weighted inference"): N* = the pair's
// row count unweighted, its row floors on the rows carrying mass (a
// weight-0 row is skipped, as the single-pair tests skip it), its Σw under frequency, its Kish n_eff
// PairW²/PairW2 under probability — exact per pair under pairwise
// (CoMoment.PairW / PairW2), the whole fold's under listwise. A plain r
// tests on df = N* − 2 with se = 1/√(N* − 3); a partial r over g
// controls on df = N* − 2 − g with se = 1/√(N* − 3 − g), where N* is
// the whole matrix's (listwise) or the smallest folded pair's
// (pairwise: every partial reads every pair, ppcor's n = min pair n),
// with PULSE_MATRIX_PAIRWISE_N_STAR naming it.
//
// Undefined figures are null (NaN), never collapsed: the diagonal; a
// null r (its cause is in the co-moment warnings); a cell whose pair
// leaves df ≤ 0 or fewer than 3 + g massed rows (p and both bounds); a
// cell whose interval needs more (fewer than 4 + g massed rows or
// N* − 3 − g ≤ 0; the bounds only). A null for size is reported by
// PULSE_MATRIX_INSUFFICIENT_N (scope "inference"); under probability
// weights an effective size below 3 + g adds PULSE_WEIGHT_LOW_NEFF —
// both MatrixResult warnings, never escalated (no matrix warning is).

// inferenceOutputs are the auxiliary keys params.inference adds.
var inferenceOutputs = []string{"p", "ci_lower", "ci_upper"}

// pairInference is one output cell's inference size.
type pairInference struct {
	// n is the rows carrying mass (CoMoment.PairNMass): a weight-0 row
	// counts in the matrix's components n but never in inference's row
	// floors, as TEST_PEARSON_R / TEST_SPEARMAN_R / TEST_KENDALL_TAU
	// skip it from their raw n. Unweighted it is PairN.
	n     int64
	nStar float64 // inference size N*
	nEff  float64 // Kish n_eff (probability weights only; else 0)
}

// inferenceRule is how a cell turns (r, N*) into its p and interval.
// The zero rule is the Pearson / partial one: t on df = N* − 2 − g and
// Fisher's z with se = 1/√(N* − 3 − g) on at least 4 + g rows. A rank
// method (matrix_rank_inference.go) supplies its own p per cell and its
// own Fisher-z variance.
type inferenceRule struct {
	// p, when set, is the cell's two-sided p-value (replacing the t
	// test's); NaN leaves the cell's p null.
	p func(a, b int) float64
	// zDF, when set, is 1/se² of atanh(r) from (r, N*); unset, N* − 3 − g.
	zDF func(r, nStar float64) float64
	// ciRows is how many rows beyond 3 + g the interval needs (0 = 1).
	ciRows int64
	// ciNeed names the interval's floor in the thin-cell warning.
	ciNeed string
}

// inferenceAux builds the three auxiliaries over the output axis for
// the (partial) correlation r — r's coordinates are output positions —
// with g controls, each cell's inference size from size(a, b), plus
// the warnings. pairScoped reports whether size varies per cell (a
// pairwise plain r): the warnings then list the cells; otherwise they
// carry the matrix-wide size once. rule is the cell arithmetic (the
// zero rule: Pearson / partial).
func (in *matrixFinalizeInput) inferenceAux(r func(a, b int) float64, g int, size func(a, b int) pairInference, pairScoped bool, extra []*types.ResponseWarning, rule inferenceRule) (map[string]*types.MatrixValues, []*types.ResponseWarning) {
	workMatrixInferenceBuilds.Add(1)
	plan := in.Plan()
	members, _ := plan.OutputMembers()
	k := len(members)
	alpha := plan.Alpha
	gf := float64(g)
	minRows := int64(3 + g)
	ciRows := rule.ciRows
	if ciRows == 0 {
		ciRows = 1
	}
	ciNeed := rule.ciNeed
	if ciNeed == "" {
		ciNeed = "4 + g rows and N* − 3 − g > 0 for the interval"
	}
	probability := weighting.BasisOf(in.slot.weight) == weighting.Probability

	type cell struct{ p, lo, hi float64 }
	cells := make([][]cell, k)
	var thin, low []map[string]any
	var thinAll, lowAll *pairInference
	for a := 0; a < k; a++ {
		cells[a] = make([]cell, k)
		for b := 0; b < k; b++ {
			cells[a][b] = cell{math.NaN(), math.NaN(), math.NaN()}
		}
	}
	for a := 0; a < k; a++ {
		for b := a + 1; b < k; b++ {
			rv := r(a, b)
			if math.IsNaN(rv) {
				continue
			}
			s := size(a, b)
			if probability && s.nEff < float64(minRows) {
				if pairScoped {
					low = append(low, map[string]any{"row": members[a], "col": members[b], "n_eff": s.nEff})
				} else if lowAll == nil {
					lowAll = &s
				}
			}
			df := s.nStar - 2 - gf
			if s.n < minRows || !(df > 0) {
				if pairScoped {
					thin = append(thin, map[string]any{"row": members[a], "col": members[b], "n": s.n, "n_star": s.nStar, "outputs": inferenceOutputs})
				} else if thinAll == nil {
					thinAll = &s
				}
				continue
			}
			zDF := s.nStar - 3 - gf
			if rule.zDF != nil {
				zDF = rule.zDF(rv, s.nStar)
			}
			ciOK := s.n >= minRows+ciRows && zDF > 0
			inf := correlationInference(rv, df, zDF, ciOK, alpha)
			if rule.p != nil {
				inf.P = rule.p(a, b)
			}
			c := cell{inf.P, inf.Lo, inf.Hi}
			cells[a][b], cells[b][a] = c, c
			if !ciOK && pairScoped {
				thin = append(thin, map[string]any{"row": members[a], "col": members[b], "n": s.n, "n_star": s.nStar, "outputs": inferenceOutputs[1:]})
			} else if !ciOK && thinAll == nil {
				thinAll = &s
			}
		}
	}

	warns := extra
	need := "3 + g rows and N* − 2 − g > 0 for p, " + ciNeed + " (g = " + strconv.Itoa(g) + " controls)"
	if len(thin) > 0 {
		warns = append(warns, matrixWarning(errors.PULSE_MATRIX_INSUFFICIENT_N,
			"matrix "+plan.Name+" has pairs too thin for inference (needs "+need+"); their listed outputs are null",
			map[string]any{"matrix": plan.Name, "scope": "inference", "pairs": thin}))
	}
	if thinAll != nil {
		outputs := inferenceOutputs
		if thinAll.n >= minRows && thinAll.nStar-2-gf > 0 {
			outputs = inferenceOutputs[1:]
		}
		warns = append(warns, matrixWarning(errors.PULSE_MATRIX_INSUFFICIENT_N,
			"matrix "+plan.Name+" is too thin for inference (needs "+need+"; N* is "+strconv.FormatFloat(thinAll.nStar, 'g', 6, 64)+"); its listed outputs are null",
			map[string]any{"matrix": plan.Name, "scope": "inference", "n": thinAll.n, "n_star": thinAll.nStar, "controls": g, "outputs": outputs}))
	}
	if len(low) > 0 {
		warns = append(warns, matrixWarning(errors.PULSE_WEIGHT_LOW_NEFF,
			"matrix "+plan.Name+": pairs whose effective sample size n_eff is below the "+strconv.Itoa(int(minRows))+" its inference needs; their p-values and intervals are unreliable or null",
			map[string]any{"matrix": plan.Name, "type": string(plan.Type), "pairs": low, "min_required": minRows}))
	}
	if lowAll != nil {
		warns = append(warns, matrixWarning(errors.PULSE_WEIGHT_LOW_NEFF,
			"matrix "+plan.Name+": effective sample size n_eff = "+strconv.FormatFloat(lowAll.nEff, 'g', 4, 64)+" is below the "+strconv.Itoa(int(minRows))+" its inference needs; its p-values and intervals are unreliable or null",
			map[string]any{"matrix": plan.Name, "type": string(plan.Type), "n_eff": lowAll.nEff, "min_required": minRows}))
	}

	pick := func(f func(c cell) float64) *types.MatrixValues {
		return in.Square(func(a, b int) float64 { return f(cells[a][b]) })
	}
	return map[string]*types.MatrixValues{
		"p":        pick(func(c cell) float64 { return c.p }),
		"ci_lower": pick(func(c cell) float64 { return c.lo }),
		"ci_upper": pick(func(c cell) float64 { return c.hi }),
	}, warns
}

// pairSize is the inference size of folded columns i and j off cm:
// exact per pair under pairwise (CoMoment.PairNMass / PairW / PairW2;
// the whole fold's under listwise, where they are the global figures).
// Unweighted N* is the row count — every row weighs 1, so PairNMass =
// PairN and the figure is unchanged.
func pairSize(cm *linalg.CoMoment, basis weighting.Basis, i, j int) pairInference {
	s := pairInference{n: cm.PairNMass(i, j)}
	switch basis {
	case weighting.Unweighted:
		s.nStar = float64(s.n)
	default:
		s.nStar = basis.NStar(cm.PairW(i, j), cm.PairW2(i, j))
		if basis == weighting.Probability {
			s.nEff = cm.PairNEff(i, j)
		}
	}
	return s
}

// pearsonInference is MAT_CORRELATION's params.inference over the
// Pearson primary r (axis = every member): each cell on its own pair's
// size.
func (in *matrixFinalizeInput) pearsonInference(r *linalg.Sym) (map[string]*types.MatrixValues, []*types.ResponseWarning) {
	basis := weighting.BasisOf(in.slot.weight)
	return in.inferenceAux(r.At, 0, func(a, b int) pairInference {
		return pairSize(in.CM, basis, a, b)
	}, in.Plan().Pairwise, nil, inferenceRule{})
}

// partialInference is MAT_PARTIAL_CORRELATION's params.inference over
// the partials pc (output coordinates): g = every other folded column
// under control "all", else the control count; one size for the whole
// matrix — the fold's (listwise) or the smallest folded pair's
// (pairwise, with PULSE_MATRIX_PAIRWISE_N_STAR naming it).
func (in *matrixFinalizeInput) partialInference(pc func(a, b int) float64) (map[string]*types.MatrixValues, []*types.ResponseWarning) {
	plan := in.Plan()
	cols := in.Members()
	g := len(plan.Controls)
	if plan.Controls == nil {
		g = len(cols) - 2
	}
	basis := weighting.BasisOf(in.slot.weight)
	size := pairSize(in.CM, basis, 0, 0)
	var extra []*types.ResponseWarning
	if plan.Pairwise {
		bi, bj := 0, 0
		for i := range cols {
			for j := i; j < len(cols); j++ {
				if s := pairSize(in.CM, basis, i, j); s.nStar < size.nStar || (s.nStar == size.nStar && s.n < size.n) {
					size, bi, bj = s, i, j
				}
			}
		}
		extra = append(extra, matrixWarning(errors.PULSE_MATRIX_PAIRWISE_N_STAR,
			"matrix "+plan.Name+": under pairwise deletion each correlation rests on its own rows, so the partial correlations' p-values and intervals use the smallest pair's inference size ("+strconv.FormatFloat(size.nStar, 'g', 6, 64)+
				", pair "+cols[bi]+" × "+cols[bj]+"), the conservative choice; listwise deletion gives one N for every cell",
			map[string]any{"matrix": plan.Name, "outputs": inferenceOutputs, "n_star": size.nStar, "row": cols[bi], "col": cols[bj]}))
	}
	return in.inferenceAux(pc, g, func(int, int) pairInference { return size }, false, extra, inferenceRule{})
}
