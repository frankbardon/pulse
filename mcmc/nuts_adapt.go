package mcmc

import (
	"math"

	"github.com/frankbardon/pulse/linalg/detmath"
)

// Dual-averaging constants (Hoffman and Gelman 2014, Stan's defaults).
const (
	daGamma = 0.05
	daT0    = 10
	daKappa = 0.75
)

// dualAveraging is Nesterov dual averaging of log ε towards a mean
// acceptance statistic of delta, as Stan's stepsize_adaptation.
type dualAveraging struct {
	delta, mu           float64
	counter, sBar, xBar float64
}

func newDualAveraging(delta, eps float64) *dualAveraging {
	da := &dualAveraging{delta: delta}
	da.restart(eps)
	return da
}

// restart forgets the averaged history and re-centres μ = log(10·ε).
func (da *dualAveraging) restart(eps float64) {
	da.mu = detmath.Log(float64(10 * eps))
	da.counter, da.sBar, da.xBar = 0, 0, 0
}

// learn folds one transition's acceptance statistic in and returns the
// next step size.
func (da *dualAveraging) learn(accept float64) float64 {
	da.counter++
	if accept > 1 {
		accept = 1
	}
	eta := 1 / (da.counter + daT0)
	da.sBar = float64((1-eta)*da.sBar) + float64(eta*(da.delta-accept))
	x := da.mu - float64(float64(da.sBar*math.Sqrt(da.counter))/daGamma)
	xEta := detmath.Pow(da.counter, -daKappa)
	da.xBar = float64((1-xEta)*da.xBar) + float64(xEta*x)
	return detmath.Exp(x)
}

// final is the step size warmup ends on: exp of the averaged iterate.
func (da *dualAveraging) final() float64 { return detmath.Exp(da.xBar) }

// Windowed metric-adaptation defaults (Stan's, for a 1000-iteration warmup).
const (
	adaptInitBuffer = 75
	adaptTermBuffer = 50
	adaptBaseWindow = 25
)

// varAdaptation is Stan's windowed diagonal metric adaptation: a fast
// initial buffer, slow windows doubling in size whose draws' regularised
// variances become the inverse metric, and a fast terminal buffer.
type varAdaptation struct {
	enabled                      bool
	numWarmup                    int
	initBuffer, termBuffer, base int
	counter, windowSize, nextEnd int
	n                            int
	m, m2                        []float64
}

func newVarAdaptation(warmup, dim int) *varAdaptation {
	va := &varAdaptation{m: make([]float64, dim), m2: make([]float64, dim)}
	if warmup < 20 {
		return va // too short to estimate a metric: it stays the identity
	}
	va.enabled = true
	va.numWarmup = warmup
	if adaptInitBuffer+adaptBaseWindow+adaptTermBuffer > warmup {
		// Stan's short-warmup split: 15% initial, 10% terminal, the rest
		// one slow window. (Stan's own windowed_adaptation skips the
		// restart in this branch; the window is set up here as intended.)
		va.initBuffer = int(float64(0.15 * float64(warmup)))
		va.termBuffer = int(float64(0.1 * float64(warmup)))
		va.base = warmup - (va.initBuffer + va.termBuffer)
	} else {
		va.initBuffer, va.termBuffer, va.base = adaptInitBuffer, adaptTermBuffer, adaptBaseWindow
	}
	va.counter = 0
	va.windowSize = va.base
	va.nextEnd = va.initBuffer + va.windowSize - 1
	return va
}

func (va *varAdaptation) inWindow() bool {
	return va.counter >= va.initBuffer && va.counter < va.numWarmup-va.termBuffer && va.counter != va.numWarmup
}

func (va *varAdaptation) windowEnds() bool {
	return va.counter == va.nextEnd && va.counter != va.numWarmup
}

func (va *varAdaptation) computeNextWindow() {
	last := va.numWarmup - va.termBuffer - 1
	if va.nextEnd == last {
		return
	}
	va.windowSize *= 2
	va.nextEnd = va.counter + va.windowSize
	if va.nextEnd != last && va.nextEnd+2*va.windowSize >= va.numWarmup-va.termBuffer {
		// The following window would not fit: stretch this one to the
		// terminal buffer.
		va.nextEnd = last
	}
}

// learn folds warmup iteration's draw q in. At the end of a slow window
// it overwrites invMetric with the window's regularised variances and
// reports updated; ok is false when that metric is not finite.
func (va *varAdaptation) learn(invMetric, q []float64) (updated, ok bool) {
	if !va.enabled {
		return false, true
	}
	if va.inWindow() {
		va.add(q)
	}
	if !va.windowEnds() {
		va.counter++
		return false, true
	}
	va.computeNextWindow()
	n := float64(va.n)
	if va.n > 1 {
		for i := range invMetric {
			invMetric[i] = va.m2[i] / (n - 1)
		}
	}
	shrink := n / (n + 5)
	floor := float64(1e-3 * (5 / (n + 5)))
	ok = true
	for i, v := range invMetric {
		v = float64(shrink*v) + floor
		invMetric[i] = v
		if math.IsNaN(v) || math.IsInf(v, 0) || !(v > 0) {
			ok = false
		}
	}
	va.n = 0
	clear(va.m)
	clear(va.m2)
	va.counter++
	return true, ok
}

// add is one Welford update of the window's running mean and sum of
// squared deviations.
func (va *varAdaptation) add(q []float64) {
	va.n++
	n := float64(va.n)
	for i, x := range q {
		delta := x - va.m[i]
		va.m[i] += float64(delta / n)
		va.m2[i] += float64((x - va.m[i]) * delta)
	}
}
