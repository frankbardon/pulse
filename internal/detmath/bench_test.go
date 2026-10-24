package detmath

import (
	"math"
	"testing"
)

// sink keeps the compiler from discarding a benchmark's result.
var sink float64

// benchInputs is a fixed 1024-input slice per kernel, drawn from the
// sweep stream so detmath and math.* see identical arguments.
func benchInputs(seed uint64, draw func(*sweepRand) float64) []float64 {
	r := &sweepRand{s: seed}
	xs := make([]float64, 1024)
	for i := range xs {
		xs[i] = draw(r)
	}
	return xs
}

func bench1(b *testing.B, f func(float64) float64, xs []float64) {
	b.Helper()
	var s float64
	for i := 0; b.Loop(); i++ {
		s += f(xs[i&1023])
	}
	sink = s
}

func bench2(b *testing.B, f func(float64, float64) float64, xs, ys []float64) {
	b.Helper()
	var s float64
	for i := 0; b.Loop(); i++ {
		s += f(xs[i&1023], ys[i&1023])
	}
	sink = s
}

var (
	expIn   = benchInputs(1, func(r *sweepRand) float64 { return r.scaled(-6, 9) })
	logIn   = benchInputs(2, func(r *sweepRand) float64 { return r.positive(-60, 60) })
	log1pIn = benchInputs(3, func(r *sweepRand) float64 { return r.positive(-30, 10) })
	trigIn  = benchInputs(4, func(r *sweepRand) float64 { return r.scaled(-10, 20) })
	bigIn   = benchInputs(5, func(r *sweepRand) float64 { return r.scaled(30, 200) })
	atanY   = benchInputs(6, func(r *sweepRand) float64 { return r.scaled(-20, 20) })
	atanX   = benchInputs(7, func(r *sweepRand) float64 { return r.scaled(-20, 20) })
	powX    = benchInputs(8, func(r *sweepRand) float64 { return r.positive(-4, 4) })
	powY    = benchInputs(9, func(r *sweepRand) float64 { return r.scaled(-4, 5) })
	expm1In = benchInputs(10, func(r *sweepRand) float64 { return r.scaled(-30, 5) })
	erfIn   = benchInputs(11, func(r *sweepRand) float64 { return r.scaled(-8, 2) })
	erfiIn  = benchInputs(12, func(r *sweepRand) float64 { return r.scaled(-20, -1) })
)

func BenchmarkDetmath_Exp(b *testing.B)     { bench1(b, Exp, expIn) }
func BenchmarkStdlib_Exp(b *testing.B)      { bench1(b, math.Exp, expIn) }
func BenchmarkDetmath_Log(b *testing.B)     { bench1(b, Log, logIn) }
func BenchmarkStdlib_Log(b *testing.B)      { bench1(b, math.Log, logIn) }
func BenchmarkDetmath_Log1p(b *testing.B)   { bench1(b, Log1p, log1pIn) }
func BenchmarkStdlib_Log1p(b *testing.B)    { bench1(b, math.Log1p, log1pIn) }
func BenchmarkDetmath_Sin(b *testing.B)     { bench1(b, Sin, trigIn) }
func BenchmarkStdlib_Sin(b *testing.B)      { bench1(b, math.Sin, trigIn) }
func BenchmarkDetmath_SinHuge(b *testing.B) { bench1(b, Sin, bigIn) }
func BenchmarkStdlib_SinHuge(b *testing.B)  { bench1(b, math.Sin, bigIn) }
func BenchmarkDetmath_Cos(b *testing.B)     { bench1(b, Cos, trigIn) }
func BenchmarkStdlib_Cos(b *testing.B)      { bench1(b, math.Cos, trigIn) }
func BenchmarkDetmath_Tan(b *testing.B)     { bench1(b, Tan, trigIn) }
func BenchmarkStdlib_Tan(b *testing.B)      { bench1(b, math.Tan, trigIn) }
func BenchmarkDetmath_Atan2(b *testing.B)   { bench2(b, Atan2, atanY, atanX) }
func BenchmarkStdlib_Atan2(b *testing.B)    { bench2(b, math.Atan2, atanY, atanX) }
func BenchmarkDetmath_Pow(b *testing.B)     { bench2(b, Pow, powX, powY) }
func BenchmarkStdlib_Pow(b *testing.B)      { bench2(b, math.Pow, powX, powY) }
func BenchmarkDetmath_Expm1(b *testing.B)   { bench1(b, Expm1, expm1In) }
func BenchmarkStdlib_Expm1(b *testing.B)    { bench1(b, math.Expm1, expm1In) }
func BenchmarkDetmath_Erf(b *testing.B)     { bench1(b, Erf, erfIn) }
func BenchmarkStdlib_Erf(b *testing.B)      { bench1(b, math.Erf, erfIn) }
func BenchmarkDetmath_Erfinv(b *testing.B)  { bench1(b, Erfinv, erfiIn) }
func BenchmarkStdlib_Erfinv(b *testing.B)   { bench1(b, math.Erfinv, erfiIn) }
