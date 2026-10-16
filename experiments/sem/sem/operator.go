package sem

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/types"
	"gonum.org/v1/gonum/mat"
)

// TypeName is the operator's registered name.
const TypeName types.MatrixType = "MAT_LAB_SEM"

// Estimate table columns (Auxiliary["estimates"]).
var estimateColumns = []string{"estimate", "std_estimate", "std_err", "z_value", "p_value"}

var scalarKeys = []string{
	"dof", "dof_baseline", "chi2", "chi2_p_value", "chi2_baseline",
	"cfi", "gfi", "agfi", "nfi", "tli", "rmsea", "aic", "bic", "loglik",
	"objective", "n", "n_params", "iterations", "converged",
}

type params struct {
	Model       string  `json:"model"`
	Solver      string  `json:"solver"`
	Information string  `json:"information"`
	Ftol        float64 `json:"ftol"`
	MaxIter     int     `json:"max_iter"`
	GradTol     float64 `json:"grad_tol"`
}

// Registration returns the MAT_LAB_SEM registration for
// pulse.Options.Extensions.MatrixOps.
func Registration() pulse.MatrixOpRegistration {
	return pulse.MatrixOpRegistration{
		Name:        TypeName,
		Description: "Structural equation model (semopy-compatible ML fit) over the members' covariance: lavaan-style `model` syntax, estimates with SEs, z and p, standardized estimates, and fit indices (chi2, CFI, TLI, RMSEA, GFI, AGFI, NFI, AIC, BIC).",
		Factory:     factory,
		Streamable:  true, // needs only the co-moment (covariance + N)
		Params: []pulse.ParamMeta{
			{Name: "model", JSONType: "string", Description: "Model description in semopy / lavaan syntax (=~ measurement, ~ regression, ~~ covariance; one statement per line). Absent: a one-factor model over every member."},
			{Name: "solver", JSONType: "string", Description: "slsqp: semopy's SciPy SLSQP, reproducing its results including its early stop at ftol; fisher: damped Fisher scoring to the optimum.", Default: "slsqp"},
			{Name: "information", JSONType: "string", Description: "Derivative computation: dense (semopy's) or rank2 (same figures, much cheaper on large models).", Default: "dense"},
			{Name: "ftol", JSONType: "number", Description: "SLSQP ftol.", Default: 1e-6},
			{Name: "max_iter", JSONType: "number", Description: "Iteration cap (10000 for slsqp, as semopy sets it; 500 for fisher)."},
			{Name: "grad_tol", JSONType: "number", Description: "Fisher scoring: stop when every free gradient entry is below it.", Default: 1e-10},
		},
		OutputKeys: descriptor.MatrixOutputKeys{
			Primary:   "implied_covariance",
			Auxiliary: []string{"estimates", "residual_covariance"},
			Vectors:   []string{"parameter", "free"},
			Scalars:   scalarKeys,
		},
	}
}

type operator struct {
	desc string // "" = one factor over the members
	opt  Options
}

func factory(spec *types.MatrixSpec, _ *encoding.Schema) (extend.MatrixOperator, error) {
	var p params
	if len(spec.Params) > 0 && !bytes.Equal(bytes.TrimSpace(spec.Params), []byte("null")) {
		dec := json.NewDecoder(bytes.NewReader(spec.Params))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&p); err != nil {
			return nil, configErr(fmt.Sprintf("%s params: %v", TypeName, err), "params_invalid")
		}
	}
	opt := Options{Solver: Solver(p.Solver), Information: Information(p.Information), Ftol: p.Ftol, MaxIter: p.MaxIter, GradTol: p.GradTol}
	if err := opt.Validate(); err != nil {
		return nil, configErr(fmt.Sprintf("%s: %v", TypeName, err), "params_invalid")
	}
	if strings.TrimSpace(p.Model) != "" {
		if _, err := NewModel(p.Model, false); err != nil {
			return nil, configErr(fmt.Sprintf("%s model: %v", TypeName, err), "model_invalid")
		}
	}
	return &operator{desc: p.Model, opt: opt}, nil
}

func configErr(msg, reason string) error {
	return perr.NewCodedErrorWithDetails(perr.PROCESSING_CONFIG, msg, map[string]any{"type": string(TypeName), "reason": reason})
}

func (o *operator) Finalize(in *extend.MatrixInput) (extend.MatrixOutput, error) {
	if in.CoMoment.Mode() != linalg.Listwise {
		return extend.MatrixOutput{}, configErr(fmt.Sprintf("%s needs one covariance over one row set: use missing \"listwise\"", TypeName), "pairwise_unsupported")
	}
	desc := o.desc
	if strings.TrimSpace(desc) == "" {
		desc = "F1 =~ " + strings.Join(in.Members, " + ")
	}
	m, err := NewModel(desc, false)
	if err != nil {
		return extend.MatrixOutput{}, configErr(err.Error(), "model_invalid")
	}
	memberIdx := indexOf(in.Members)
	var missing []string
	idx := make([]int, len(m.Observed))
	for k, v := range m.Observed {
		i, ok := memberIdx[v]
		if !ok {
			missing = append(missing, v)
		}
		idx[k] = i
	}
	if len(missing) > 0 {
		return extend.MatrixOutput{}, perr.NewCodedErrorWithDetails(perr.PROCESSING_CONFIG,
			fmt.Sprintf("%s: observed model variables are not matrix members: %s", TypeName, strings.Join(missing, ", ")),
			map[string]any{"type": string(TypeName), "reason": "member_missing", "missing": missing})
	}
	// semopy's sample covariance divides by N (numpy bias=True).
	cov := in.CoMoment.Cov(0)
	s := mat.NewSymDense(len(idx), nil)
	for a := range idx {
		for b := a; b < len(idx); b++ {
			s.SetSym(a, b, cov.At(idx[a], idx[b]))
		}
	}
	n := float64(in.CoMoment.N())
	fit, err := FitCov(m, s, n, o.opt)
	if err != nil {
		return extend.MatrixOutput{}, perr.NewCodedErrorWithDetails(perr.PULSE_MATRIX_SINGULAR,
			fmt.Sprintf("%s: %v", TypeName, err), map[string]any{"type": string(TypeName)})
	}
	if err := ComputeStats(fit, desc, o.opt); err != nil {
		return extend.MatrixOutput{}, perr.NewCodedErrorWithDetails(perr.PULSE_MATRIX_SINGULAR,
			fmt.Sprintf("%s baseline: %v", TypeName, err), map[string]any{"type": string(TypeName)})
	}
	out := extend.MatrixOutput{Primary: square(m.Observed, in.Encoding, fit.Implied.At)}
	if !fit.Converged {
		out.Warnings = append(out.Warnings, &types.ResponseWarning{
			Code:    string(perr.PULSE_MATRIX_NOT_CONVERGED),
			Message: fmt.Sprintf("%s: %s after %d iterations; estimates are the last iterate", TypeName, fit.Status, fit.Iterations),
			Details: map[string]any{"solver": string(fit.Options.Solver), "status": fit.Status, "iterations": fit.Iterations},
		})
	}
	if in.WantAuxiliary {
		keys := make([]string, len(fit.Estimates))
		for i, e := range fit.Estimates {
			keys[i] = e.Key()
		}
		est := &types.MatrixValues{
			Kind:       types.MatrixKindRectangular,
			Encoding:   types.MatrixEncodingFull,
			RowKeys:    keys,
			ColumnKeys: append([]string(nil), estimateColumns...),
			Values:     make([][]float64, len(keys)),
		}
		for i, e := range fit.Estimates {
			est.Values[i] = []float64{e.Value, e.StdValue, e.SE, e.Z, e.P}
		}
		out.Auxiliary = map[string]*types.MatrixValues{
			"estimates": est,
			"residual_covariance": square(m.Observed, in.Encoding, func(r, c int) float64 {
				return fit.Sample.At(r, c) - fit.Implied.At(r, c)
			}),
		}
	}
	if in.WantVectors {
		names := make([]string, len(fit.Estimates))
		free := make([]bool, len(fit.Estimates))
		for i, e := range fit.Estimates {
			names[i], free[i] = e.Param, e.Free
		}
		out.Vectors = map[string]any{"parameter": names, "free": free}
	}
	if in.WantScalars {
		st := fit.Stats
		conv := 0.0
		if fit.Converged {
			conv = 1
		}
		out.Scalars = map[string]float64{
			"dof": st.DoF, "dof_baseline": st.DoFBaseline, "chi2": st.Chi2, "chi2_p_value": st.Chi2P,
			"chi2_baseline": st.Chi2Baseline, "cfi": st.CFI, "gfi": st.GFI, "agfi": st.AGFI, "nfi": st.NFI,
			"tli": st.TLI, "rmsea": st.RMSEA, "aic": st.AIC, "bic": st.BIC, "loglik": st.LogLik,
			"objective": fit.Objective, "n": fit.N, "n_params": float64(st.NParams),
			"iterations": float64(fit.Iterations), "converged": conv,
		}
	}
	return out, nil
}

// square renders a symmetric matrix over keys in the requested encoding.
func square(keys []string, enc types.MatrixEncoding, at func(r, c int) float64) *types.MatrixValues {
	if enc == "" {
		enc = types.MatrixEncodingFull
	}
	out := &types.MatrixValues{
		Kind:       types.MatrixKindSquareSymmetric,
		Encoding:   enc,
		RowKeys:    append([]string(nil), keys...),
		ColumnKeys: append([]string(nil), keys...),
		Values:     make([][]float64, len(keys)),
	}
	for r := range keys {
		start := 0
		if enc == types.MatrixEncodingUpper {
			start = r
		}
		row := make([]float64, 0, len(keys)-start)
		for c := start; c < len(keys); c++ {
			v := at(r, c)
			if math.IsInf(v, 0) {
				v = math.NaN()
			}
			row = append(row, v)
		}
		out.Values[r] = row
	}
	return out
}
