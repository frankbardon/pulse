package gp

import (
	"fmt"
	"math"
	"math/rand/v2"
	"slices"

	"github.com/frankbardon/pulse/internal/regresample"
	"github.com/frankbardon/pulse/linalg/detmath"
	"github.com/frankbardon/pulse/optimize"
)

// Bounds is the box ML-II searches, per hyperparameter kind as
// [lower, upper] (0 < lower ≤ upper, finite); lower == upper fixes the
// hyperparameter. Every length scale shares LengthScale.
type Bounds struct {
	Amplitude   [2]float64
	LengthScale [2]float64
	Noise       [2]float64
}

// DefaultBounds is the box Fit searches when FitOptions.Bounds is nil:
// amplitude [1e-2, 1e3] and length scales [1e-2, 1e2] (scikit-optimize's
// BO defaults, for inputs on the unit interval and standardised
// targets), noise [1e-6, 1].
func DefaultBounds() Bounds {
	return Bounds{
		Amplitude:   [2]float64{1e-2, 1e3},
		LengthScale: [2]float64{1e-2, 1e2},
		Noise:       [2]float64{1e-6, 1},
	}
}

// DefaultInitial is Fit's first start when FitOptions.Initial is nil:
// amplitude 1, every length scale 0.5, noise 1e-4 (each clipped into the
// bounds).
func DefaultInitial(dims int) Hyper {
	ls := make([]float64, dims)
	for d := range ls {
		ls[d] = 0.5
	}
	return Hyper{Amplitude: 1, LengthScales: ls, Noise: 1e-4}
}

// Defaults for FitOptions' zero values.
const (
	DefaultRestarts = 5
	DefaultMaxIter  = 200
	DefaultAcc      = 1e-9
)

// FitOptions configures Fit. The zero value is the default fit.
type FitOptions struct {
	// Restarts is the number of SLSQP starts: Initial, then Restarts−1
	// seeded uniform draws in the log-bounds box. 0 means
	// DefaultRestarts; negative is refused.
	Restarts int
	// Seed seeds the restart draws: PCG (Seed, Seed ^
	// regresample.PCGStream), raw words only.
	Seed int64
	// Nugget is the jitter always added to the training diagonal. 0
	// means DefaultNugget; negative or non-finite is refused.
	Nugget float64
	// Bounds is the ML-II box; nil means DefaultBounds.
	Bounds *Bounds
	// Initial is the first start (clipped into Bounds); nil means
	// DefaultInitial.
	Initial *Hyper
	// MaxIter caps each SLSQP run's major iterations (0 means
	// DefaultMaxIter); Acc is its accuracy goal (0 means DefaultAcc).
	MaxIter int
	Acc     float64
}

// failedObjective stands in for −LML where the training covariance cannot
// be factored even with jitter, so SLSQP backs away from the point.
const failedObjective = 1e300

// Fit conditions the GP on x (encoded rows) and y with hyperparameters
// fitted by ML-II: SLSQP on −LML over θ = log(hyperparameters) within the
// log of the bounds, from each start in order; the highest final LML
// wins, a tie keeping the earlier start. A start whose run stops short
// (PULSE_OPTIMIZE_NOT_CONVERGED) still competes with its final iterate.
func Fit(space *Space, x [][]float64, y []float64, opts FitOptions) (*Model, error) {
	d, err := prepare(space, x, y)
	if err != nil {
		return nil, err
	}
	o, err := resolve(space.Dims(), opts)
	if err != nil {
		return nil, err
	}
	dims := space.Dims()
	p := dims + 2
	lo, hi := make([]float64, p), make([]float64, p)
	setBox := func(i int, b [2]float64) {
		lo[i], hi[i] = detmath.Log(b[0]), detmath.Log(b[1])
	}
	setBox(0, o.bounds.Amplitude)
	for g := range dims {
		setBox(1+g, o.bounds.LengthScale)
	}
	setBox(p-1, o.bounds.Noise)

	starts := make([][]float64, 0, o.restarts)
	first := make([]float64, p)
	first[0] = detmath.Log(o.initial.Amplitude)
	for g, l := range o.initial.LengthScales {
		first[1+g] = detmath.Log(l)
	}
	first[p-1] = detmath.Log(o.initial.Noise)
	for i := range first {
		first[i] = min(max(first[i], lo[i]), hi[i])
	}
	starts = append(starts, first)
	s := uint64(o.seed)
	rng := rand.NewPCG(s, s^regresample.PCGStream)
	for range o.restarts - 1 {
		th := make([]float64, p)
		for i := range th {
			u := float64(rng.Uint64()>>11) * 0x1p-53
			th[i] = lo[i] + float64(u*(hi[i]-lo[i]))
		}
		starts = append(starts, th)
	}

	obj := newObjective(d, dims, o.nugget)
	bestLML := math.Inf(-1)
	var best []float64
	for _, th := range starts {
		res, err := optimize.Minimize(obj.f, obj.grad, th, optimize.MinimizeOptions{
			Lower: lo, Upper: hi, Acc: o.acc, MaxIter: o.maxIter,
		})
		if res == nil {
			return nil, err
		}
		if !finite(res.F) || res.F >= failedObjective {
			continue
		}
		if l := -res.F; l > bestLML {
			bestLML, best = l, slices.Clone(res.X)
		}
	}
	if best == nil {
		return nil, fmt.Errorf("gp: no ML-II start reached a factorable covariance")
	}
	return newModel(d, hyperOf(best, dims), o.nugget)
}

// hyperOf maps θ back onto hyperparameters.
func hyperOf(th []float64, dims int) Hyper {
	h := Hyper{Amplitude: detmath.Exp(th[0]), Noise: detmath.Exp(th[dims+1]), LengthScales: make([]float64, dims)}
	for g := range dims {
		h.LengthScales[g] = detmath.Exp(th[1+g])
	}
	return h
}

// objective is −LML and its gradient over θ for SLSQP, evaluated once per
// distinct θ (SLSQP asks for f and then the gradient at the same point).
type objective struct {
	d      *data
	dims   int
	nugget float64
	at     []float64
	val    float64
	g      []float64
}

func newObjective(d *data, dims int, nugget float64) *objective {
	return &objective{d: d, dims: dims, nugget: nugget}
}

func (o *objective) eval(th []float64) {
	if o.at != nil && slices.Equal(o.at, th) {
		return
	}
	o.at = slices.Clone(th)
	o.g = make([]float64, o.dims+2)
	s, err := evaluate(o.d, hyperOf(th, o.dims), o.nugget, o.g)
	if err != nil || !finite(s.lml) {
		o.val = failedObjective
		clear(o.g)
		return
	}
	o.val = -s.lml
	for i := range o.g {
		o.g[i] = -o.g[i]
	}
}

func (o *objective) f(th []float64) float64 {
	o.eval(th)
	return o.val
}

func (o *objective) grad(th []float64) []float64 {
	o.eval(th)
	return slices.Clone(o.g)
}

// resolved is FitOptions with every default applied.
type resolved struct {
	restarts int
	seed     int64
	nugget   float64
	bounds   Bounds
	initial  Hyper
	maxIter  int
	acc      float64
}

func resolve(dims int, opts FitOptions) (resolved, error) {
	o := resolved{restarts: opts.Restarts, seed: opts.Seed, nugget: opts.Nugget, maxIter: opts.MaxIter, acc: opts.Acc}
	if o.restarts < 0 {
		return o, fmt.Errorf("gp: negative Restarts %d", o.restarts)
	}
	if o.restarts == 0 {
		o.restarts = DefaultRestarts
	}
	if o.nugget == 0 {
		o.nugget = DefaultNugget
	}
	if !positive(o.nugget) {
		return o, fmt.Errorf("gp: nugget %v is not a positive finite number", opts.Nugget)
	}
	if o.maxIter < 0 {
		return o, fmt.Errorf("gp: negative MaxIter %d", o.maxIter)
	}
	if o.maxIter == 0 {
		o.maxIter = DefaultMaxIter
	}
	if o.acc == 0 {
		o.acc = DefaultAcc
	}
	if !positive(o.acc) {
		return o, fmt.Errorf("gp: Acc %v is not a positive finite number", opts.Acc)
	}
	o.bounds = DefaultBounds()
	if opts.Bounds != nil {
		o.bounds = *opts.Bounds
	}
	for _, nb := range []struct {
		name string
		b    [2]float64
	}{{"amplitude", o.bounds.Amplitude}, {"length-scale", o.bounds.LengthScale}, {"noise", o.bounds.Noise}} {
		name, b := nb.name, nb.b
		if !positive(b[0]) || !positive(b[1]) || b[0] > b[1] {
			return o, fmt.Errorf("gp: %s bounds %v must satisfy 0 < lower <= upper < +Inf", name, b)
		}
	}
	o.initial = DefaultInitial(dims)
	if opts.Initial != nil {
		o.initial = opts.Initial.clone()
	}
	if err := o.initial.validate(dims); err != nil {
		return o, err
	}
	return o, nil
}
