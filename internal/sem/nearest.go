package sem

import (
	"errors"
	"math"

	"gonum.org/v1/gonum/mat"
)

// nearest.go is semopy's fallback for a sample covariance that is not
// positive definite: Model.load_cov catches the Cholesky failure and
// substitutes statsmodels' cov_nearest(S, threshold=1e-2) — method
// "clipped" (statsmodels.stats.correlation_tools): S is scaled to its
// correlation form C = S / (σσᵀ), every eigenvalue of C below the
// threshold is raised to it (clip_evals), the result is rescaled to a
// unit diagonal (corr_clipped) and back to S's variances (corr2cov).
// A C with no eigenvalue below the threshold is kept as is. Each step
// mirrors the numpy expression it ports, operation for operation; the
// eigendecomposition is gonum's, so the figures match statsmodels to
// rounding, not bit for bit.

// NearestThreshold is the eigenvalue floor semopy passes to cov_nearest.
const NearestThreshold = 1e-2

// Nearest is a cov_nearest result.
type Nearest struct {
	// Cov is the adjusted covariance (S's diagonal, to rounding).
	Cov *mat.SymDense
	// MinEigenvalue is the smallest eigenvalue of S's correlation form,
	// before the clip.
	MinEigenvalue float64
	// Clipped reports whether any eigenvalue was below the threshold
	// (false: Cov is S rescaled through its correlation form unchanged).
	Clipped bool
}

// CovNearest is statsmodels' cov_nearest(s, method="clipped",
// threshold) (above). Every diagonal entry of s must be positive and
// finite: statsmodels' correlation form is undefined otherwise (its
// eigendecomposition of the NaN it produces fails).
func CovNearest(s *mat.SymDense, threshold float64) (*Nearest, error) {
	p := s.SymmetricDim()
	std := make([]float64, p)
	for i := range std {
		d := s.At(i, i)
		if !(d > 0) || math.IsInf(d, 0) {
			return nil, errors.New("cov_nearest needs a positive, finite variance on every variable")
		}
		std[i] = math.Sqrt(d)
	}
	// cov2corr: cov / np.outer(std, std).
	corr := mat.NewSymDense(p, nil)
	for i := 0; i < p; i++ {
		for j := i; j < p; j++ {
			v := s.At(i, j)
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return nil, errors.New("cov_nearest needs a finite covariance")
			}
			corr.SetSym(i, j, v/float64(std[i]*std[j]))
		}
	}
	// clip_evals: evecs · max(evals, t) · evecsᵀ.
	var es mat.EigenSym
	if !es.Factorize(corr, true) {
		return nil, errors.New("cov_nearest: the eigendecomposition did not converge")
	}
	evals := es.Values(nil)
	var vecs mat.Dense
	es.VectorsTo(&vecs)
	out := &Nearest{MinEigenvalue: math.Inf(1)}
	lam := make([]float64, p)
	for k, e := range evals {
		out.MinEigenvalue = math.Min(out.MinEigenvalue, e)
		out.Clipped = out.Clipped || e < threshold
		lam[k] = math.Max(e, threshold)
	}
	adj := corr
	if out.Clipped {
		x := make([][]float64, p)
		for i := range x {
			x[i] = make([]float64, p)
			for j := range x[i] {
				acc := 0.0
				for k := 0; k < p; k++ {
					acc += float64(vecs.At(i, k)*lam[k]) * vecs.At(j, k)
				}
				x[i][j] = acc
			}
		}
		// corr_clipped's cov2corr: x_new / x_std / x_std[:, None].
		xs := make([]float64, p)
		for i := range xs {
			xs[i] = math.Sqrt(x[i][i])
		}
		adj = mat.NewSymDense(p, nil)
		for i := 0; i < p; i++ {
			for j := i; j < p; j++ {
				adj.SetSym(i, j, x[i][j]/xs[j]/xs[i])
			}
		}
	}
	// corr2cov: corr * np.outer(std, std).
	out.Cov = mat.NewSymDense(p, nil)
	for i := 0; i < p; i++ {
		for j := i; j < p; j++ {
			out.Cov.SetSym(i, j, adj.At(i, j)*float64(std[i]*std[j]))
		}
	}
	return out, nil
}

// PositiveDefinite reports whether s factors by Cholesky — the test
// semopy's load_cov applies (LAPACK dpotrf) before falling back to
// CovNearest.
func PositiveDefinite(s *mat.SymDense) bool {
	var ch mat.Cholesky
	return ch.Factorize(s)
}
