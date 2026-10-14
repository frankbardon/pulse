package processing

import (
	"math"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/types"
)

// ATTR_PC_SCORE — each row's score on one principal component of the
// slot's p members, a fitted vector attribute (attribute_fitted.go),
// standardized to unit variance on the fit population.
//
// The fit is MAT_PCA's decomposition on the fit rows: the analysed
// matrix M is the members' correlation (params.on "correlation", the
// default; CoMoment.Corr) or their ddof = 1 covariance on w*
// (fittedState.cov — MAT_PCA's listwise reliabilityCov, the kind's
// denominator; a fit is listwise, so its one n_eff is every entry's),
// decomposed by linalg.SymEigen, so the order (eigenvalues descending)
// and the sign (each eigenvector's largest-magnitude component
// positive) are MAT_PCA's and a score correlates with each member with
// the sign of MAT_PCA's loading on the same rows. With S the covariance,
// D = diag(√S_ii) (the identity on a covariance), v_c the eigenvector
// and λ_c the eigenvalue of component c:
//
//	score_c = v_cᵀ D⁻¹ (x − μ) / √λ_c
//
// — prcomp's x / sdev, and psych::principal's regression scores — so
// the scores have mean 0 and variance 1 under the fit's own (weighted,
// ddof = 1) moments.
//
// params.rotation (an orthogonal criterion, vectors.PCARotations)
// rotates the unrotated loadings of the
// params.components = k leading components with the shared rotation
// engine (rotateLoadings, the call MAT_PCA's pcaRotate makes — same
// order and sign rule), giving L = A·T, and scores
//
//	score_c = Σ_j s_j T_jc     (s the k unrotated standardized scores)
//
// — psych::principal(rotate = "varimax")'s scores R⁻¹L, unit variance
// because T is orthogonal (why an oblique criterion is refused). A
// rotation stopped at its iteration cap keeps the last iterate with a
// PULSE_MATRIX_NOT_CONVERGED warning (fit scope; details carry the
// solver).
//
// The singular DECISION is the fitted-attribute rule (rankDeficient,
// shared with ATTR_MAHALANOBIS): a constant member or a member that is a
// linear combination of others nulls every score with
// PULSE_MATRIX_SINGULAR — a zero eigenvalue has no standardized score.
// The per-row VALUE is one FMA-free dot product with weights fixed at
// fit time, summed in member order.
type pcScoreModel struct {
	cfg vectors.PCScoreConfig

	mean []float64
	// w[i] is member i's weight: score = Σ_i w_i (x_i − μ_i).
	w     []float64
	notes []*fitRefusal
}

var _ fitNoter = (*pcScoreModel)(nil)

// newPCScoreAttribute is ATTR_PC_SCORE's registry factory.
func newPCScoreAttribute(attr *types.Attribute, schema *encoding.Schema) (AttributeComputer, error) {
	if attr == nil {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG, "ATTR_PC_SCORE requires a non-nil spec")
	}
	at := attributeSlotLabel(attr)
	cfg, cerr := vectors.PCScoreParams(at, attr.Params)
	if cerr != nil {
		return nil, cerr
	}
	if len(attr.Fields) > 0 {
		if cerr := cfg.CheckMembers(at, len(attr.Fields)); cerr != nil {
			return nil, cerr
		}
	}
	return newFittedVectorAttribute(attr, schema, fittedSource{fitOn: cfg.FitOn, fit: cfg.Fit, labels: cfg.Labels}, &pcScoreModel{cfg: cfg})
}

// fit decomposes the fit's correlation (or covariance) and fixes the
// row weights of the scored component.
func (m *pcScoreModel) fit(st fittedState) *fitRefusal {
	cov := st.cov()
	singular := &fitRefusal{code: errors.PULSE_MATRIX_SINGULAR,
		msg: "the fit covariance is singular (a member is constant or a linear combination of others); a zero-variance component has no standardized score and every score is null"}
	if rankDeficient(cov) {
		return singular
	}
	p := st.p
	sd := make([]float64, p)
	for i := range sd {
		sd[i] = 1
	}
	var mat *linalg.Sym
	if m.cfg.On == vectors.PCAOnCovariance {
		s, err := linalg.NewSymFromRows(cov)
		if err != nil {
			return singular
		}
		mat = s
	} else {
		mat = st.co.Corr()
		for i := range sd {
			sd[i] = math.Sqrt(cov[i][i])
		}
	}
	eig, err := linalg.SymEigen(mat)
	if err != nil {
		return singular
	}
	lam := eig.Values.Slice()
	// unrotated(i, c) is member i's weight on standardized component c.
	unrotated := func(i, c int) float64 {
		return eig.Vectors.At(i, c) / (sd[i] * math.Sqrt(lam[c]))
	}
	c := m.cfg.Component - 1
	m.w = make([]float64, p)
	if m.cfg.Rotation == "" {
		for i := range m.w {
			m.w[i] = unrotated(i, c)
		}
	} else {
		k := m.cfg.Components
		a := make([][]float64, p)
		for i := range a {
			a[i] = make([]float64, k)
			for j := range a[i] {
				a[i][j] = float64(eig.Vectors.At(i, j) * math.Sqrt(math.Max(lam[j], 0)))
			}
		}
		rot, err := rotateLoadings(a, rotationSpec{Method: m.cfg.Rotation, Epsilon: m.cfg.Epsilon})
		if err != nil {
			return &fitRefusal{code: errors.PULSE_MATRIX_SINGULAR,
				msg: "the " + m.cfg.Rotation + " rotation failed (" + err.Error() + "); every score is null"}
		}
		if note := rotationCapNote(m.cfg.Rotation, rot); note != nil {
			m.notes = append(m.notes, note)
		}
		for i := range m.w {
			sum := 0.0
			for j := 0; j < k; j++ {
				sum += float64(unrotated(i, j) * rot.Rotation[j][c])
			}
			m.w[i] = sum
		}
	}
	m.mean = st.mean()
	return nil
}

// fitNotes satisfies fitNoter: the rotation's non-convergence.
func (m *pcScoreModel) fitNotes() []*fitRefusal { return m.notes }

// score is the standardized component score of the complete row x.
func (m *pcScoreModel) score(x []float64) float64 {
	s := 0.0
	for i, w := range m.w {
		s += float64((x[i] - m.mean[i]) * w)
	}
	return s
}
