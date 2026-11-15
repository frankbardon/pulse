// Package distlagparams is the one reading of WIN_DISTRIBUTED_LAG's params,
// shared by the window factory (internal/processing/window) and predict
// (internal/descriptor, through internal/windowparams) so both refuse the
// same request with the same message. It resolves the params into the
// lag kernel the window convolves with and never reads a row.
//
// The kernel holds one weight per lag l = 0..max_lag (max_lag + 1
// weights; lag 0 is the current row):
//
//   - geometric:   w_l = decay^l, decay in [0, 1);
//   - delayed:     w_l = decay^((l − peak)²), decay in [0, 1) and peak in
//     [0, max_lag] — the geometric kernel with its peak moved peak lags
//     on (Jin et al. 2017's delayed adstock), so w_peak = 1 when peak is
//     a whole lag;
//   - weibull_cdf: w_l = 1 − F(l; shape, scale) = exp(−(l/scale)^shape),
//     the Weibull survival function, so w_0 = 1;
//   - weibull_pdf: w_l = f(l + 1; shape, scale), the Weibull density one
//     lag on, so a shape below 1 stays finite at lag 0.
//
// With normalize (default true) every weight is divided by their sum, so
// the kernel sums to 1 and the lagged column keeps the input's scale.
//
// Every rule a request can break is refused here: a missing or unknown
// kernel, max_lag missing, below 1 or above [MaxLagCeiling], decay
// outside [0, 1), peak outside [0, max_lag], shape or scale not above 0,
// a param the chosen kernel does not read (decay on a Weibull kernel,
// shape / scale on geometric or delayed, peak on any kernel but
// delayed), and a kernel whose weights are not finite or sum to no
// positive mass.
//
// One weight is one call of [GeometricWeight], [DelayedWeight],
// [WeibullCDFWeight] or [WeibullPDFWeight]: REG_MMM_BAYES's in-model
// adstock calls the same functions, so its weights are this kernel's bit
// for bit.
//
// The weights are computed with linalg/detmath's Exp / Log / Pow, never
// math.*, and every product feeding an add is barriered float64(...), so
// a kernel's bits are the same on every architecture
// (TestDistLag_WeightBitsPinned, TestDistLag_NoFMAContraction).
//
// Leaf: stdlib + the public errors / types / linalg/detmath packages +
// paramread.
package distlagparams

import (
	"encoding/json"
	"math"
	"strconv"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/paramread"
	"github.com/frankbardon/pulse/linalg/detmath"
	"github.com/frankbardon/pulse/types"
)

// The kernel names params.kernel accepts.
const (
	KernelGeometric  = "geometric"
	KernelDelayed    = "delayed"
	KernelWeibullCDF = "weibull_cdf"
	KernelWeibullPDF = "weibull_pdf"
)

// Kernels returns the accepted kernel names in manifest order.
func Kernels() []string {
	return []string{KernelGeometric, KernelDelayed, KernelWeibullCDF, KernelWeibullPDF}
}

// GeometricWeight is the geometric kernel's raw weight at lag l,
// decay^l.
func GeometricWeight(decay float64, l int) float64 {
	return detmath.Pow(decay, float64(l))
}

// DelayedWeight is the delayed kernel's raw weight at lag l,
// decay^((l − peak)²).
func DelayedWeight(decay, peak float64, l int) float64 {
	d := float64(l) - peak
	return detmath.Pow(decay, float64(d*d))
}

// WeibullCDFWeight is the weibull_cdf kernel's raw weight at lag l, the
// Weibull survival exp(−(l/scale)^shape).
func WeibullCDFWeight(shape, scale float64, l int) float64 {
	return detmath.Exp(-detmath.Pow(float64(l)/scale, shape))
}

// WeibullPDFWeight is the weibull_pdf kernel's raw weight at lag l, the
// Weibull density at l + 1, evaluated in log space so a large shape or
// scale underflows to 0 instead of meeting Inf · 0 = NaN.
func WeibullPDFWeight(shape, scale float64, l int) float64 {
	logNorm := detmath.Log(shape) - detmath.Log(scale)
	z := float64(l+1) / scale
	return detmath.Exp(logNorm + float64((shape-1)*detmath.Log(z)) - detmath.Pow(z, shape))
}

// MaxLagCeiling is the largest max_lag accepted: the kernel is
// materialised once per slot, so an unbounded lag would size an
// allocation from a request value. 10000 lags is decades of daily rows.
const MaxLagCeiling = 10000

// Params is the typed wire shape of WIN_DISTRIBUTED_LAG's params.
type Params struct {
	Kernel    *string  `json:"kernel"`
	Decay     *float64 `json:"decay"`
	Peak      *float64 `json:"peak"`
	Shape     *float64 `json:"shape"`
	Scale     *float64 `json:"scale"`
	MaxLag    *int     `json:"max_lag"`
	Normalize *bool    `json:"normalize"`
}

// Spec is a resolved WIN_DISTRIBUTED_LAG slot: the kernel name, whether the
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
	msg := string(types.WIN_DISTRIBUTED_LAG) + ": " + r.Message
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
	return ResolveDeferred(raw, nil)
}

// ResolveDeferred is Resolve with the values of the deferred keys (a
// scalar-expression param predict could not compute: decay, peak,
// shape, scale or max_lag; internal/windowscalar) judged present but not yet
// known: every rule that reads such a value — its own range, and the
// kernel's weights, their finiteness and mass — is left to the runtime,
// which resolves the value and calls Resolve. Every other rule runs as
// Resolve runs it. With any value deferred the Spec carries no weights.
// A nil or empty deferred set is Resolve.
func ResolveDeferred(raw json.RawMessage, deferred map[string]bool) (Spec, *Refusal) {
	var p Params
	paramread.Declare(paramread.Window, string(types.WIN_DISTRIBUTED_LAG), (*Params)(nil))
	if len(raw) > 0 {
		if err := paramread.Decode(paramread.Window, string(types.WIN_DISTRIBUTED_LAG), raw, &p); err != nil {
			return refuse("malformed params: "+err.Error(), nil)
		}
	}
	if p.Kernel == nil {
		return refuse("params.kernel is required (geometric, delayed, weibull_cdf or weibull_pdf)", nil)
	}
	kernel := *p.Kernel
	switch kernel {
	case KernelGeometric, KernelDelayed, KernelWeibullCDF, KernelWeibullPDF:
	default:
		return refuse("params.kernel must be geometric, delayed, weibull_cdf or weibull_pdf (got "+strconv.Quote(kernel)+")",
			map[string]any{"kernel": kernel, "valid": Kernels()})
	}
	if p.MaxLag == nil {
		return refuse("params.max_lag is required (an integer >= 1)", nil)
	}
	maxLag := *p.MaxLag
	if !deferred["max_lag"] && (maxLag < 1 || maxLag > MaxLagCeiling) {
		return refuse("params.max_lag must be an integer in [1, "+strconv.Itoa(MaxLagCeiling)+"]",
			map[string]any{"max_lag": maxLag})
	}
	normalize := true
	if p.Normalize != nil {
		normalize = *p.Normalize
	}

	// A deferred value: the presence rules below still run, every rule
	// reading a value waits for the runtime.
	pending := deferred["max_lag"] || deferred["decay"] || deferred["peak"] || deferred["shape"] || deferred["scale"]
	var w []float64
	if !pending {
		w = make([]float64, maxLag+1)
	}
	if p.Peak != nil && kernel != KernelDelayed {
		return refuse("params.peak applies only to the delayed kernel", map[string]any{"kernel": kernel})
	}
	switch kernel {
	case KernelGeometric, KernelDelayed:
		if p.Shape != nil || p.Scale != nil {
			reads := "params.decay"
			if kernel == KernelDelayed {
				reads = "params.decay and params.peak"
			}
			return refuse("params.shape and params.scale apply only to the weibull_cdf / weibull_pdf kernels; the "+kernel+" kernel reads "+reads,
				map[string]any{"kernel": kernel})
		}
		if p.Decay == nil {
			return refuse("params.decay is required for the "+kernel+" kernel (in [0, 1))", map[string]any{"kernel": kernel})
		}
		d := *p.Decay
		if !deferred["decay"] && !(d >= 0 && d < 1) {
			return refuse("params.decay must be in [0, 1)", map[string]any{"decay": d})
		}
		if kernel == KernelGeometric {
			for l := range w {
				w[l] = GeometricWeight(d, l)
			}
			break
		}
		if p.Peak == nil {
			return refuse("params.peak is required for the delayed kernel (in [0, max_lag])", map[string]any{"kernel": kernel})
		}
		pk := *p.Peak
		if !deferred["peak"] && !(pk >= 0 && (deferred["max_lag"] || pk <= float64(maxLag))) {
			return refuse("params.peak must be in [0, max_lag]", map[string]any{"peak": pk, "max_lag": maxLag})
		}
		for l := range w {
			w[l] = DelayedWeight(d, pk, l)
		}
	default:
		if p.Decay != nil {
			return refuse("params.decay applies only to the geometric and delayed kernels; the "+kernel+" kernel reads params.shape and params.scale",
				map[string]any{"kernel": kernel})
		}
		if p.Shape == nil || p.Scale == nil {
			return refuse("params.shape and params.scale are both required for the "+kernel+" kernel (each > 0)",
				map[string]any{"kernel": kernel})
		}
		k, lambda := *p.Shape, *p.Scale
		if !deferred["shape"] && (!(k > 0) || math.IsInf(k, 0)) {
			return refuse("params.shape must be > 0", map[string]any{"shape": k})
		}
		if !deferred["scale"] && (!(lambda > 0) || math.IsInf(lambda, 0)) {
			return refuse("params.scale must be > 0", map[string]any{"scale": lambda})
		}
		if pending {
			break
		}
		weight := WeibullCDFWeight
		if kernel == KernelWeibullPDF {
			weight = WeibullPDFWeight
		}
		for l := range w {
			w[l] = weight(k, lambda, l)
		}
	}

	if pending {
		return Spec{Kernel: kernel, Normalize: normalize}, nil
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
