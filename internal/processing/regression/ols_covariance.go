package regression

import (
	"strconv"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// ols_covariance.go is REG_OLS's robust covariance: the buffered second
// pass over the fitted rows that RegressionSpec.Covariance (hc0–hc3,
// cluster, hac) needs. The first pass folded the sufficient statistics
// and solved; this pass recomputes each listwise-complete row's residual
// and leverage from the solve and hands them to the shared sandwich
// (covariance.go).
//
// Coordinates. The sandwich runs in the centred parameterisation the
// solve already uses, z = (1, x − μ_x) with θ_c = (ȳ, β), where the
// weighted information is block-diagonal: XᵀW*X = diag(N*, c·M2_xx)
// (c = N*/Σw, so c·Σw = N*). Its bread is therefore diag(1/N*,
// M2_xx⁻¹/c) — no new inverse — and a row's leverage is h = 1/N* +
// (x − μ)ᵀ·M2_xx⁻¹·(x − μ)/c. The result maps back to (β₀, β) through
// β₀ = ȳ − μᵀβ: V = J·V_c·Jᵀ with J = [[1, −μᵀ], [0, I]].

// LabeledRecord is the optional extension of Record that a robust
// covariance reads: a categorical cluster field's level label. The
// engine's processing.Record implements it. A Record without it — or a
// field with no label (a numeric cluster id) — is keyed by its
// NumericValue instead, so every Record stays usable.
type LabeledRecord interface {
	Record
	// StringValueChecked returns the field's categorical label and
	// true, or ("", false, nil) when it is null, absent or not
	// categorical; a code its dictionary has no entry for (corrupt
	// record data) is an ENCODING_INVALID error.
	StringValueChecked(name string) (string, bool, error)
}

// levelFields are the fields spec keys rows by through clusterKey: the
// covariance cluster and the jackknife_by delete-group field.
func levelFields(spec *types.RegressionSpec) []string {
	var out []string
	if spec.Covariance != nil && spec.Covariance.Cluster != "" {
		out = append(out, spec.Covariance.Cluster)
	}
	if spec.Resample == "jackknife" && spec.JackknifeBy != "" {
		out = append(out, spec.JackknifeBy)
	}
	return out
}

// LevelFault is the first corrupt categorical code among the fields
// spec keys rows by (covariance cluster, jackknife_by) across records —
// ENCODING_INVALID with the CategoricalIndexFault details — or nil. The
// drivers run it before a fit reads a row (FitEachBufferedWithContext
// over the buffered rows, the streaming guard per row), so the fault
// fails the regression slot alone (slotfail) instead of the row being
// read as a null cluster and dropped, or keyed by its numeric code
// (U51 E5-S10).
func LevelFault(spec *types.RegressionSpec, records ...Record) error {
	fields := levelFields(spec)
	if len(fields) == 0 {
		return nil
	}
	for _, rec := range records {
		lr, ok := rec.(LabeledRecord)
		if !ok {
			return nil
		}
		for _, f := range fields {
			if _, _, err := lr.StringValueChecked(f); err != nil {
				return err
			}
		}
	}
	return nil
}

// clusterKey is rec's cluster identity under field: its categorical
// label, else its numeric value; ok=false when the field is null. A
// corrupt categorical code reads ok=false — never the numeric echo of
// the code — but no row carrying one reaches here: the drivers refuse
// it first (LevelFault).
func clusterKey(rec Record, field string) (string, bool) {
	if lr, ok := rec.(LabeledRecord); ok {
		s, ok, err := lr.StringValueChecked(field)
		if err != nil {
			return "", false
		}
		if ok {
			return "s:" + s, true
		}
	}
	v, ok := InputValue(rec, field)
	if !ok {
		return "", false
	}
	return "n:" + strconv.FormatFloat(v, 'g', -1, 64), true
}

// robustKeys reads the covariance keys a row needs (the cluster under
// type cluster, the order_by value under hac); ok=false drops the row
// listwise, like a null predictor.
func (e *olsEngine) robustKeys(rec Record) (cluster string, order float64, ok bool) {
	return robustRowKeys(e.spec, rec)
}

// robustRowKeys is the covariance-key read every engine's robust path
// shares (REG_OLS, REG_GLM, REG_BAYES_LINEAR): spec's cluster key and
// order_by value of rec, ok=false when either is null.
func robustRowKeys(spec *types.RegressionSpec, rec Record) (cluster string, order float64, ok bool) {
	c := spec.Covariance
	if c.Cluster != "" {
		if cluster, ok = clusterKey(rec, c.Cluster); !ok {
			return "", 0, false
		}
	}
	if c.OrderBy != "" {
		if order, ok = InputValue(rec, c.OrderBy); !ok {
			return "", 0, false
		}
	}
	return cluster, order, true
}

// olsRobust is the robust covariance of an OLS solve, in (β₀, β)
// coordinates.
type olsRobust struct {
	fit       *sandwichFit
	variances []float64 // clamped diag, the SEs are its square roots
	stdErrors []float64
}

// olsSandwich runs the second pass over records and assembles the
// robust covariance of solve (unpenalized: regcov.Check refuses every
// penalty, so slopeCov is M2_xx⁻¹).
func (e *olsEngine) olsSandwich(solve *olsSolveResult, records []Record) (*olsRobust, error) {
	if solve.slopeCov == nil {
		return nil, errors.NewCodedError(errors.PROCESSING_INTERNAL, "olsSandwich: solve carries no slope covariance")
	}
	a := e.acc
	p := a.p
	k := p + 1
	nStar := a.nStar()
	c := a.scale()

	bread := newSquare(k)
	bread[0][0] = 1 / nStar
	for i := 0; i < p; i++ {
		for j := 0; j < p; j++ {
			bread[i+1][j+1] = solve.slopeCov(i, j) / c
		}
	}

	rows := make([]sandwichRow, 0, a.n)
	d := make([]float64, p)
	for _, rec := range records {
		x, y, w, ok := e.fitRow(rec)
		if !ok {
			continue
		}
		cluster, order, _ := e.robustKeys(rec)
		resid := y - a.meanY
		for j := 0; j < p; j++ {
			d[j] = x[j] - a.meanX[j]
			resid -= solve.Coefficients[j] * d[j]
		}
		quad := 0.0
		for i := 0; i < p; i++ {
			row := 0.0
			for j := 0; j < p; j++ {
				row += solve.slopeCov(i, j) * d[j]
			}
			quad += d[i] * row
		}
		score := make([]float64, k)
		score[0] = resid
		for j := 0; j < p; j++ {
			score[j+1] = resid * d[j]
		}
		rows = append(rows, sandwichRow{
			score:   score,
			weight:  c * w,
			lev:     1/nStar + quad/c,
			cluster: cluster,
			order:   order,
		})
	}

	fit, err := sandwich(e.spec, bread, rows, nStar)
	if err != nil {
		return nil, err
	}

	// Back to (β₀, β): V = J·V_c·Jᵀ, J = [[1, −μᵀ], [0, I]].
	vc := fit.v
	v := newSquare(k)
	for i := 1; i < k; i++ {
		for j := i; j < k; j++ {
			v[i][j], v[j][i] = vc[i][j], vc[i][j]
		}
	}
	// Cov(β₀, β_j) = V_c[0][j] − Σ_m μ_m·V_c[m][j].
	for j := 1; j < k; j++ {
		s := vc[0][j]
		for m := 1; m < k; m++ {
			s -= a.meanX[m-1] * vc[m][j]
		}
		v[0][j], v[j][0] = s, s
	}
	// Var(β₀) = V_c[0][0] − 2·Σ_m μ_m·V_c[0][m] + Σ_{m,l} μ_m·μ_l·V_c[m][l].
	s := vc[0][0]
	for m := 1; m < k; m++ {
		s -= 2 * a.meanX[m-1] * vc[0][m]
		for l := 1; l < k; l++ {
			s += a.meanX[m-1] * a.meanX[l-1] * vc[m][l]
		}
	}
	v[0][0] = s

	// The standard errors are √ of the clamped diagonal, and the
	// diagonal IS that clamped variance: √diag(Vcov) == StdErrors bit
	// for bit (sqrt passes NaN through).
	out := &olsRobust{fit: fit, variances: make([]float64, k), stdErrors: make([]float64, k)}
	for j := 0; j < k; j++ {
		vj := v[j][j]
		if vj < 0 {
			vj = 0
		}
		v[j][j] = vj
		out.variances[j] = vj
		out.stdErrors[j] = sqrt(vj)
	}
	fit.v = v
	return out, nil
}
