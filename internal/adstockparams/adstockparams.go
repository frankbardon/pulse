// Package adstockparams is the one reading of WIN_ADSTOCK's params,
// shared by the window factory (internal/processing/window) and predict
// (internal/descriptor, through internal/windowparams) so both refuse the
// same request with the same message. It resolves the params into the
// lag kernel the window convolves with and never reads a row.
//
// The kernel holds one weight per lag l = 0..max_lag (max_lag + 1
// weights; lag 0 is the current row):
//
//   - geometric:   w_l = decay^l, decay in [0, 1);
//   - weibull_cdf: w_l = 1 − F(l; shape, scale) = exp(−(l/scale)^shape),
//     the Weibull survival function, so w_0 = 1;
//   - weibull_pdf: w_l = f(l + 1; shape, scale), the Weibull density one
//     lag on, so a shape below 1 stays finite at lag 0.
//
// With normalize (default true) every weight is divided by their sum, so
// the kernel sums to 1 and the adstocked column keeps the input's scale.
//
// Every rule a request can break is refused here: a missing or unknown
// kernel, max_lag missing, below 1 or above [MaxLagCeiling], decay
// outside [0, 1), shape or scale not above 0, a param the chosen kernel
// does not read (decay on a Weibull kernel, shape / scale on geometric),
// and a kernel whose weights are not finite or sum to no positive mass.
//
// Leaf: stdlib + the public errors / types packages + paramread.
package adstockparams

import (
	"encoding/json"
	"math"
	"strconv"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/paramread"
	"github.com/frankbardon/pulse/types"
)

// The kernel names params.kernel accepts.
const (
	KernelGeometric  = "geometric"
	KernelWeibullCDF = "weibull_cdf"
	KernelWeibullPDF = "weibull_pdf"
)

// Kernels returns the accepted kernel names in manifest order.
func Kernels() []string {
	return []string{KernelGeometric, KernelWeibullCDF, KernelWeibullPDF}
}

// MaxLagCeiling is the largest max_lag accepted: the kernel is
// materialised once per slot, so an unbounded lag would size an
// allocation from a request value. 10000 lags is decades of daily rows.
const MaxLagCeiling = 10000

// Params is the typed wire shape of WIN_ADSTOCK's params.
type Params struct {
	Kernel    *string  `json:"kernel"`
	Decay     *float64 `json:"decay"`
	Shape     *float64 `json:"shape"`
	Scale     *float64 `json:"scale"`
	MaxLag    *int     `json:"max_lag"`
	Normalize *bool    `json:"normalize"`
}

// Spec is a resolved WIN_ADSTOCK slot: the kernel name, whether the
// weights were normalised, and the weights themselves, Weights[l] for
// lag l = 0..max_lag.
type Spec struct {
	Kernel    string
	Normalize bool
	Weights   []float64
}

// Refusal is one violated rule: Message is the operator-neutral sentence
// (callers prefix their own context) and Details the offending values.
type Refusal struct {
	Message string
	Details map[string]any
}

// Error renders the refusal for callers that wrap it.
func (r *Refusal) Error() string { return r.Message }

// Coded returns the refusal as a PULSE_WINDOW_INVALID error with the
// runtime prefix — the factory's refusal, predict's code (the window
// family's one code, internal/windowparams).
func (r *Refusal) Coded() *errors.CodedError {
	msg := string(types.WIN_ADSTOCK) + ": " + r.Message
	if len(r.Details) == 0 {
		return errors.NewCodedError(errors.PULSE_WINDOW_INVALID, msg)
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_WINDOW_INVALID, msg, r.Details)
}

func refuse(msg string, details map[string]any) (Spec, *Refusal) {
	return Spec{}, &Refusal{Message: msg, Details: details}
}

// Resolve reads raw params and returns the resolved kernel, or the first
// rule the params break.
func Resolve(raw json.RawMessage) (Spec, *Refusal) {
	var p Params
	paramread.Declare(paramread.Window, string(types.WIN_ADSTOCK), (*Params)(nil))
	if len(raw) > 0 {
		if err := paramread.Decode(paramread.Window, string(types.WIN_ADSTOCK), raw, &p); err != nil {
			return refuse("malformed params: "+err.Error(), nil)
		}
	}
	if p.Kernel == nil {
		return refuse("params.kernel is required (geometric, weibull_cdf or weibull_pdf)", nil)
	}
	kernel := *p.Kernel
	switch kernel {
	case KernelGeometric, KernelWeibullCDF, KernelWeibullPDF:
	default:
		return refuse("params.kernel must be geometric, weibull_cdf or weibull_pdf (got "+strconv.Quote(kernel)+")",
			map[string]any{"kernel": kernel, "valid": Kernels()})
	}
	if p.MaxLag == nil {
		return refuse("params.max_lag is required (an integer >= 1)", nil)
	}
	maxLag := *p.MaxLag
	if maxLag < 1 || maxLag > MaxLagCeiling {
		return refuse("params.max_lag must be an integer in [1, "+strconv.Itoa(MaxLagCeiling)+"]",
			map[string]any{"max_lag": maxLag})
	}
	normalize := true
	if p.Normalize != nil {
		normalize = *p.Normalize
	}

	w := make([]float64, maxLag+1)
	switch kernel {
	case KernelGeometric:
		if p.Shape != nil || p.Scale != nil {
			return refuse("params.shape and params.scale apply only to the weibull_cdf / weibull_pdf kernels; the geometric kernel reads params.decay",
				map[string]any{"kernel": kernel})
		}
		if p.Decay == nil {
			return refuse("params.decay is required for the geometric kernel (in [0, 1))", map[string]any{"kernel": kernel})
		}
		d := *p.Decay
		if !(d >= 0 && d < 1) {
			return refuse("params.decay must be in [0, 1)", map[string]any{"decay": d})
		}
		for l := range w {
			w[l] = math.Pow(d, float64(l))
		}
	default:
		if p.Decay != nil {
			return refuse("params.decay applies only to the geometric kernel; the "+kernel+" kernel reads params.shape and params.scale",
				map[string]any{"kernel": kernel})
		}
		if p.Shape == nil || p.Scale == nil {
			return refuse("params.shape and params.scale are both required for the "+kernel+" kernel (each > 0)",
				map[string]any{"kernel": kernel})
		}
		k, lambda := *p.Shape, *p.Scale
		if !(k > 0) || math.IsInf(k, 0) {
			return refuse("params.shape must be > 0", map[string]any{"shape": k})
		}
		if !(lambda > 0) || math.IsInf(lambda, 0) {
			return refuse("params.scale must be > 0", map[string]any{"scale": lambda})
		}
		if kernel == KernelWeibullCDF {
			for l := range w {
				w[l] = math.Exp(-math.Pow(float64(l)/lambda, k))
			}
		} else {
			// The density in log space, so a large shape or scale
			// underflows to 0 instead of meeting Inf * 0 = NaN.
			logNorm := math.Log(k) - math.Log(lambda)
			for l := range w {
				z := float64(l+1) / lambda
				w[l] = math.Exp(logNorm + float64((k-1)*math.Log(z)) - math.Pow(z, k))
			}
		}
	}

	sum := 0.0
	for _, x := range w {
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return refuse("the "+kernel+" kernel's weights are not finite for these params", map[string]any{"kernel": kernel})
		}
		sum += x
	}
	if !(sum > 0) || math.IsInf(sum, 0) {
		return refuse("the "+kernel+" kernel puts no finite positive weight on lags 0.."+strconv.Itoa(maxLag)+"; widen max_lag or change shape / scale",
			map[string]any{"kernel": kernel, "max_lag": maxLag})
	}
	if normalize {
		for l := range w {
			w[l] /= sum
		}
	}
	return Spec{Kernel: kernel, Normalize: normalize, Weights: w}, nil
}
