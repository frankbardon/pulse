package processing

import (
	"math"
	"strconv"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/types"
)

// ATTR_FACTOR_SCORE — each row's score on one common factor of the
// slot's p members, a fitted vector attribute (attribute_fitted.go).
//
// The fit is MAT_FACTOR's on the fit rows (matrix_factor.go): the
// members' correlation R (CoMoment.Corr — listwise, so already PSD and
// never repaired), k from params.factors (an integer, or the Kaiser rule
// on R's eigenvalues), the unrotated loadings from the same faExtractors
// entry and the rotation from the same rotateLoadings call, so the
// loadings L (and the factor correlation Φ, the identity when
// orthogonal) are MAT_FACTOR's on the same rows, column order and sign
// included. With z = D⁻¹(x − μ) the row standardized by the fit's own
// (weighted, ddof = 1) mean and spread, the score on factor c is
//
//	regression (Thurstone, the default)  w_c = R⁻¹ (LΦ)_c
//	bartlett                             w_c = U⁻²L (LᵀU⁻²L)⁻¹ e_c,
//	                                     U² = 1 − diag(LΦLᵀ)
//
// score_c = zᵀw_c — psych::factor.scores(method = "Thurstone" /
// "Bartlett") on scale(x), the weights fixed at fit time. The regression
// score correlates with each member as the structure coefficient (LΦ)_ic
// (up to one positive factor per column), so its sign is MAT_FACTOR's.
//
// Fit failures follow the fitted-attribute rule — a model that cannot
// give a score nulls every score with one fit-scoped warning; a usable
// fit that owes a caveat scores and warns (fitNoter):
//
//   - singular R (rankDeficient): PULSE_MATRIX_SINGULAR, all null;
//   - k not identified ((p − k)² < p + k), or the Kaiser rule keeping
//     fewer factors than params.factor: PULSE_MATRIX_NOT_IDENTIFIED,
//     all null;
//   - a Heywood case (a uniqueness ≤ 0, or ML's ψ held at its floor):
//     PULSE_MATRIX_HEYWOOD, scored as fitted — except Bartlett with a
//     uniqueness ≤ 0, whose weights divide by it: all null;
//   - an extraction or rotation stopped at its cap:
//     PULSE_MATRIX_NOT_CONVERGED, scored from the last iterate.
type factorScoreModel struct {
	cfg     vectors.FactorScoreConfig
	members []string

	// loadings / phi are the fitted (rotated) L and Φ, kept for the
	// MAT_FACTOR parity check.
	loadings [][]float64
	phi      [][]float64

	mean []float64
	// w[i] is member i's raw-unit weight: score = Σ_i w_i (x_i − μ_i).
	w     []float64
	notes []*fitRefusal
}

var _ fitNoter = (*factorScoreModel)(nil)

// newFactorScoreAttribute is ATTR_FACTOR_SCORE's registry factory.
func newFactorScoreAttribute(attr *types.Attribute, schema *encoding.Schema) (AttributeComputer, error) {
	if attr == nil {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG, "ATTR_FACTOR_SCORE requires a non-nil spec")
	}
	at := attributeSlotLabel(attr)
	cfg, cerr := vectors.FactorScoreParams(at, attr.Params)
	if cerr != nil {
		return nil, cerr
	}
	if len(attr.Fields) > 0 {
		if cerr := cfg.CheckMembers(at, len(attr.Fields)); cerr != nil {
			return nil, cerr
		}
	}
	return newFittedVectorAttribute(attr, schema, fittedSource{fitOn: cfg.FitOn, fit: cfg.Fit, labels: cfg.Labels}, &factorScoreModel{cfg: cfg, members: append([]string(nil), attr.Fields...)})
}

// fit runs MAT_FACTOR's extraction and rotation on the fit's
// correlation and fixes the scored factor's row weights.
func (m *factorScoreModel) fit(st fittedState) *fitRefusal {
	cov := st.cov()
	singular := &fitRefusal{code: errors.PULSE_MATRIX_SINGULAR,
		msg: "the fit correlation is singular (a member is constant or a linear combination of others); factor scores need its inverse and every score is null"}
	if rankDeficient(cov) {
		return singular
	}
	p := st.p
	r := st.co.Corr()
	k := m.cfg.Factors.K
	if m.cfg.Factors.Kaiser() {
		eig, err := linalg.SymEigen(r)
		if err != nil {
			return singular
		}
		for _, v := range eig.Values.Slice() {
			if v > 1 {
				k++
			}
		}
		if k < m.cfg.Factor {
			return &fitRefusal{code: errors.PULSE_MATRIX_NOT_IDENTIFIED,
				msg: "the Kaiser rule keeps " + strconv.Itoa(k) + " factor(s) (eigenvalues of the fit correlation above 1), so factor " +
					strconv.Itoa(m.cfg.Factor) + " is not in the model; set params.factors to fit it, and every score is null",
				details: map[string]any{"factors": k, "factor": m.cfg.Factor}}
		}
	}
	if !vectors.FactorIdentified(p, k) {
		return &fitRefusal{code: errors.PULSE_MATRIX_NOT_IDENTIFIED,
			msg: strconv.Itoa(k) + " factors are not identified on " + strconv.Itoa(p) + " members ((p − k)² must be at least p + k, so at most " +
				strconv.Itoa(vectors.MaxIdentifiedFactors(p)) + " here); every score is null",
			details: map[string]any{"factors": k, "max_factors": vectors.MaxIdentifiedFactors(p)}}
	}
	fit, err := faExtractors[m.cfg.Extraction](r, k)
	if err != nil {
		return singular
	}
	if !fit.Converged {
		m.notes = append(m.notes, &fitRefusal{code: errors.PULSE_MATRIX_NOT_CONVERGED,
			msg: "the " + m.cfg.Extraction + " extraction (" + fit.Solver + ") stopped after " + strconv.Itoa(fit.Iterations) +
				" iterations, short of its tolerance; the scores use the last iterate",
			details: map[string]any{"solver": fit.Solver, "iterations": fit.Iterations, "max_iterations": fit.MaxIterations, "tolerance": fit.Tolerance}})
	}
	l, phi := fit.Loadings, identityRows(k)
	if m.cfg.Rotation != "" {
		rot, err := rotateLoadings(fit.Loadings, rotationSpec{Method: m.cfg.Rotation, Kappa: m.cfg.Kappa, Gamma: m.cfg.Gamma, Epsilon: m.cfg.Epsilon})
		if err != nil {
			return &fitRefusal{code: errors.PULSE_MATRIX_SINGULAR,
				msg: "the " + m.cfg.Rotation + " rotation failed (" + err.Error() + "); every score is null"}
		}
		if note := rotationCapNote(m.cfg.Rotation, rot); note != nil {
			m.notes = append(m.notes, note)
		}
		l, phi = rot.Loadings, rot.Phi
	}
	m.loadings, m.phi = l, phi

	// U² = 1 − diag(LΦLᵀ) (psych's h2 on the rotated pattern; the
	// communality is rotation-invariant).
	u2 := make([]float64, p)
	var heywood []string
	var heywoodU2 []float64
	nonPositive := false
	for i := range u2 {
		h := 0.0
		for a := 0; a < k; a++ {
			for b := 0; b < k; b++ {
				h += float64(float64(l[i][a]*phi[a][b]) * l[i][b])
			}
		}
		u2[i] = 1 - h
		if u2[i] <= 0 || (fit.Bounded != nil && fit.Bounded[i]) {
			heywood = append(heywood, m.memberName(i))
			heywoodU2 = append(heywoodU2, u2[i])
		}
		if !(u2[i] > 0) {
			nonPositive = true
		}
	}
	if len(heywood) > 0 {
		details := map[string]any{"heywood_members": heywood, "uniquenesses": heywoodU2}
		if m.cfg.Method == vectors.FactorScoreBartlett && nonPositive {
			return &fitRefusal{code: errors.PULSE_MATRIX_HEYWOOD,
				msg:     "a Heywood case — the fit leaves " + strconv.Itoa(len(heywood)) + " member(s) a uniqueness at or below 0, and Bartlett's weights divide by each uniqueness; every score is null (method \"regression\" still scores)",
				details: details}
		}
		m.notes = append(m.notes, &fitRefusal{code: errors.PULSE_MATRIX_HEYWOOD,
			msg:     "a Heywood case — the fit gives " + strconv.Itoa(len(heywood)) + " member(s) a uniqueness at or below 0 (or at the maximum-likelihood floor); the scores use the loadings as fitted, not repaired",
			details: details})
	}

	c := m.cfg.Factor - 1
	var wc []float64
	var ok bool
	if m.cfg.Method == vectors.FactorScoreBartlett {
		wc, ok = bartlettWeights(l, u2, c)
	} else {
		wc, ok = regressionWeights(r, l, phi, c)
	}
	if !ok {
		return singular
	}
	sd := make([]float64, p)
	m.w = make([]float64, p)
	for i := range sd {
		sd[i] = math.Sqrt(cov[i][i])
		m.w[i] = wc[i] / sd[i]
	}
	m.mean = st.mean()
	return nil
}

// memberName names member i for a warning (its index when unnamed).
func (m *factorScoreModel) memberName(i int) string {
	if i < len(m.members) {
		return m.members[i]
	}
	return strconv.Itoa(i)
}

// regressionWeights is Thurstone's w_c = R⁻¹ S_c, S = LΦ (the
// structure).
func regressionWeights(r *linalg.Sym, l, phi [][]float64, c int) ([]float64, bool) {
	p := r.N()
	k := len(phi)
	s := make([]float64, p)
	rows := make([][]float64, p)
	for i := 0; i < p; i++ {
		for b := 0; b < k; b++ {
			s[i] += float64(l[i][b] * phi[b][c])
		}
		rows[i] = make([]float64, p)
		for j := 0; j < p; j++ {
			rows[i][j] = r.At(i, j)
		}
	}
	return gaussSolve(rows, s)
}

// bartlettWeights is Bartlett's w_c = U⁻²L (LᵀU⁻²L)⁻¹ e_c.
func bartlettWeights(l [][]float64, u2 []float64, c int) ([]float64, bool) {
	p := len(l)
	k := len(l[0])
	mtx := make([][]float64, k)
	for a := range mtx {
		mtx[a] = make([]float64, k)
		for b := range mtx[a] {
			s := 0.0
			for i := 0; i < p; i++ {
				s += float64(l[i][a] * l[i][b] / u2[i])
			}
			mtx[a][b] = s
		}
	}
	e := make([]float64, k)
	e[c] = 1
	coef, ok := gaussSolve(mtx, e)
	if !ok {
		return nil, false
	}
	w := make([]float64, p)
	for i := range w {
		s := 0.0
		for a := 0; a < k; a++ {
			s += float64(l[i][a] * coef[a])
		}
		w[i] = s / u2[i]
	}
	return w, true
}

// fitNotes satisfies fitNoter: a Heywood case, an unconverged
// extraction or rotation.
func (m *factorScoreModel) fitNotes() []*fitRefusal { return m.notes }

// score is the factor score of the complete row x.
func (m *factorScoreModel) score(x []float64) float64 {
	s := 0.0
	for i, w := range m.w {
		s += float64((x[i] - m.mean[i]) * w)
	}
	return s
}
