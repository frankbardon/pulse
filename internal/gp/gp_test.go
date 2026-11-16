package gp

import (
	"math"
	"slices"
	"strings"
	"sync"
	"testing"
)

// mixedDesign is a small mixed space: a continuous dimension, a
// categorical one with three levels and a second continuous one.
func mixedDesign(t *testing.T) (*Space, [][]float64, []float64) {
	t.Helper()
	sp, err := NewSpace([]Dim{{}, {Levels: 3}, {}})
	if err != nil {
		t.Fatal(err)
	}
	pts := []struct {
		u0, u2 float64
		k      int
	}{
		{0.1, 0.9, 0}, {0.3, 0.2, 1}, {0.55, 0.5, 2}, {0.8, 0.1, 0}, {0.95, 0.7, 1},
		{0.2, 0.35, 2}, {0.45, 0.8, 0}, {0.65, 0.6, 1}, {0.05, 0.05, 1}, {0.75, 0.95, 2},
		{0.35, 0.65, 0}, {0.9, 0.4, 2},
	}
	off := []float64{0, 1.5, -0.7}
	var x [][]float64
	var y []float64
	for _, p := range pts {
		row, err := sp.Encode([]float64{p.u0, 0, p.u2}, []int{0, p.k, 0})
		if err != nil {
			t.Fatal(err)
		}
		x = append(x, row)
		d := p.u0 - 0.4
		y = append(y, float64(3*float64(d*d))+float64(p.u2*0.8)+off[p.k])
	}
	return sp, x, y
}

func TestSpace_EncodeLayout(t *testing.T) {
	t.Parallel()
	sp, err := NewSpace([]Dim{{}, {Levels: 3}, {}, {Levels: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if sp.Dims() != 4 || sp.Cols() != 6 {
		t.Fatalf("Dims %d Cols %d, want 4 and 6", sp.Dims(), sp.Cols())
	}
	row, err := sp.Encode([]float64{0.25, 0, 0.75, 0}, []int{0, 2, 0, 0})
	if err != nil {
		t.Fatal(err)
	}
	if want := []float64{0.25, 0, 0, 1, 0.75, 1}; !slices.Equal(row, want) {
		t.Fatalf("Encode = %v, want %v", row, want)
	}
	for name, c := range map[string]struct {
		u   []float64
		idx []int
	}{
		"short u":     {[]float64{0.1}, []int{0, 0, 0, 0}},
		"short index": {[]float64{0.1, 0, 0.2, 0}, []int{0}},
		"index high":  {[]float64{0.1, 0, 0.2, 0}, []int{0, 3, 0, 0}},
		"index low":   {[]float64{0.1, 0, 0.2, 0}, []int{0, -1, 0, 0}},
		"nan":         {[]float64{math.NaN(), 0, 0.2, 0}, []int{0, 0, 0, 0}},
	} {
		if _, err := sp.Encode(c.u, c.idx); err == nil {
			t.Errorf("%s: Encode accepted", name)
		}
	}
	if _, err := NewSpace(nil); err == nil {
		t.Error("NewSpace accepted no dimensions")
	}
	if _, err := NewSpace([]Dim{{Levels: -1}}); err == nil {
		t.Error("NewSpace accepted negative Levels")
	}
}

// TestGP_GradientMatchesFiniteDifference: the analytic θ-gradient,
// including a categorical dimension's tied length scale, matches a
// central difference of the LML.
func TestGP_GradientMatchesFiniteDifference(t *testing.T) {
	t.Parallel()
	sp, x, y := mixedDesign(t)
	h := Hyper{Amplitude: 1.3, LengthScales: []float64{0.4, 0.9, 0.6}, Noise: 3e-3}
	_, grad, err := LogMarginalLikelihood(sp, x, y, h, DefaultNugget)
	if err != nil {
		t.Fatal(err)
	}
	th := []float64{math.Log(h.Amplitude), math.Log(0.4), math.Log(0.9), math.Log(0.6), math.Log(h.Noise)}
	const step = 1e-6
	for p := range th {
		at := func(delta float64) float64 {
			v := slices.Clone(th)
			v[p] += delta
			l, _, err := LogMarginalLikelihood(sp, x, y, hyperOf(v, 3), DefaultNugget)
			if err != nil {
				t.Fatal(err)
			}
			return l
		}
		fd := (at(step) - at(-step)) / (2 * step)
		if !near(grad[p], fd, 1e-5, 1e-6) {
			t.Errorf("grad[%d] = %.10g, finite difference %.10g", p, grad[p], fd)
		}
	}
}

// TestGP_DuplicatePointsJitter: duplicate observations under a noise too
// small to separate them are accepted — CholeskyRidge jitters the
// diagonal, Ridge reports it — and the posterior stays finite; Fit on
// the same data succeeds without jitter (its noise floor separates them).
func TestGP_DuplicatePointsJitter(t *testing.T) {
	t.Parallel()
	sp, err := NewSpace([]Dim{{}})
	if err != nil {
		t.Fatal(err)
	}
	x := [][]float64{{0.2}, {0.5}, {0.5}, {0.5}, {0.9}}
	y := []float64{1, 2, 2, 2.5, 0}
	m, err := NewModel(sp, x, y, Hyper{Amplitude: 1, LengthScales: []float64{0.3}, Noise: 1e-300}, 1e-300)
	if err != nil {
		t.Fatal(err)
	}
	if m.Ridge() <= 0 {
		t.Fatalf("ridge = %v, want > 0 for duplicate points", m.Ridge())
	}
	mean, variance, err := m.Predict([]float64{0.5})
	if err != nil || !finite(mean) || !finite(variance) || variance < 0 {
		t.Fatalf("Predict = %v, %v, %v", mean, variance, err)
	}
	f, err := Fit(sp, x, y, FitOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if f.N() != 5 {
		t.Fatalf("N = %d", f.N())
	}
	mean, _, err = f.Predict([]float64{0.5})
	if err != nil || mean < 1.9 || mean > 2.4 {
		t.Fatalf("fitted mean at the duplicated point = %v (%v), want near 2.17", mean, err)
	}
}

// TestGP_FitDeterministicConcurrent: concurrent fits of one problem give
// the same bits, and the posterior interpolates the training targets.
func TestGP_FitDeterministicConcurrent(t *testing.T) {
	t.Parallel()
	sp, x, y := mixedDesign(t)
	probe := [][]float64{x[0], {0.5, 0, 1, 0, 0.5}, {0, 0, 0, 1, 1}}
	run := func() []uint64 {
		m, err := Fit(sp, x, y, FitOptions{Seed: 42})
		if err != nil {
			t.Error(err)
			return nil
		}
		h := m.Hyper()
		out := []uint64{math.Float64bits(m.LogMarginalLikelihood()), math.Float64bits(h.Amplitude), math.Float64bits(h.Noise)}
		for _, l := range h.LengthScales {
			out = append(out, math.Float64bits(l))
		}
		for _, p := range probe {
			mu, v, err := m.Predict(p)
			if err != nil {
				t.Error(err)
			}
			out = append(out, math.Float64bits(mu), math.Float64bits(v))
		}
		return out
	}
	want := run()
	if want == nil {
		t.FailNow()
	}
	var wg sync.WaitGroup
	got := make([][]uint64, 6)
	for i := range got {
		wg.Go(func() { got[i] = run() })
	}
	wg.Wait()
	for i, g := range got {
		if !slices.Equal(g, want) {
			t.Fatalf("run %d bits differ from the serial run", i)
		}
	}
	m, err := Fit(sp, x, y, FitOptions{Seed: 42})
	if err != nil {
		t.Fatal(err)
	}
	mu, _, _ := m.Predict(x[0])
	if math.Abs(mu-y[0]) > 0.05 {
		t.Fatalf("posterior mean at a training point = %v, target %v", mu, y[0])
	}
}

// TestGP_FitHonoursFixedBounds: lower == upper fixes a hyperparameter,
// and every fitted value lies in its box.
func TestGP_FitHonoursFixedBounds(t *testing.T) {
	t.Parallel()
	sp, x, y := mixedDesign(t)
	b := DefaultBounds()
	b.Noise = [2]float64{1e-3, 1e-3}
	b.LengthScale = [2]float64{0.2, 5}
	m, err := Fit(sp, x, y, FitOptions{Bounds: &b, Restarts: 3, Seed: 9})
	if err != nil {
		t.Fatal(err)
	}
	h := m.Hyper()
	if !near(h.Noise, 1e-3, 1e-12, 0) {
		t.Errorf("noise = %v, fixed at 1e-3", h.Noise)
	}
	for g, l := range h.LengthScales {
		if l < 0.2*(1-1e-12) || l > 5*(1+1e-12) {
			t.Errorf("length scale %d = %v outside [0.2, 5]", g, l)
		}
	}
}

// TestGP_RestartsCanImproveTheOptimum: the seeded restarts are real
// starts — over a hard start, more restarts never lower the LML.
func TestGP_RestartsCanImproveTheOptimum(t *testing.T) {
	t.Parallel()
	sp, x, y := mixedDesign(t)
	bad := Hyper{Amplitude: 900, LengthScales: []float64{0.011, 90, 0.011}, Noise: 0.9}
	one, err := Fit(sp, x, y, FitOptions{Restarts: 1, Initial: &bad, MaxIter: 3})
	if err != nil {
		t.Fatal(err)
	}
	many, err := Fit(sp, x, y, FitOptions{Restarts: 8, Initial: &bad, MaxIter: 3, Seed: 3})
	if err != nil {
		t.Fatal(err)
	}
	if many.LogMarginalLikelihood() <= one.LogMarginalLikelihood() {
		t.Fatalf("8 starts LML %v not above 1 start %v", many.LogMarginalLikelihood(), one.LogMarginalLikelihood())
	}
}

// TestGP_ConstantTargets: a zero-variance target set standardises by 1
// and predicts the constant.
func TestGP_ConstantTargets(t *testing.T) {
	t.Parallel()
	sp, _ := NewSpace([]Dim{{}})
	x := [][]float64{{0}, {0.5}, {1}}
	m, err := Fit(sp, x, []float64{4, 4, 4}, FitOptions{})
	if err != nil {
		t.Fatal(err)
	}
	mu, v, err := m.Predict([]float64{0.25})
	if err != nil || math.Abs(mu-4) > 1e-9 || v < 0 {
		t.Fatalf("Predict = %v, %v, %v; want 4", mu, v, err)
	}
}

func TestGP_InputRefusals(t *testing.T) {
	t.Parallel()
	sp, x, y := mixedDesign(t)
	good := Hyper{Amplitude: 1, LengthScales: []float64{1, 1, 1}, Noise: 1e-3}
	nanRow := slices.Clone(x)
	nanRow[0] = []float64{math.NaN(), 1, 0, 0, 0}
	badY := slices.Clone(y)
	badY[1] = math.Inf(1)
	cases := []struct {
		name string
		err  error
	}{}
	add := func(name string, err error) {
		cases = append(cases, struct {
			name string
			err  error
		}{name, err})
	}
	_, err := NewModel(nil, x, y, good, DefaultNugget)
	add("nil space", err)
	_, err = NewModel(sp, nil, nil, good, DefaultNugget)
	add("no rows", err)
	_, err = NewModel(sp, x, y[:3], good, DefaultNugget)
	add("target count", err)
	_, err = NewModel(sp, [][]float64{{1, 2}}, []float64{1}, good, DefaultNugget)
	add("row width", err)
	_, err = NewModel(sp, nanRow, y, good, DefaultNugget)
	add("nan row", err)
	_, err = NewModel(sp, x, badY, good, DefaultNugget)
	add("inf target", err)
	_, err = NewModel(sp, x, y, Hyper{Amplitude: 1, LengthScales: []float64{1}, Noise: 1}, DefaultNugget)
	add("length-scale count", err)
	_, err = NewModel(sp, x, y, Hyper{Amplitude: 0, LengthScales: []float64{1, 1, 1}, Noise: 1}, DefaultNugget)
	add("zero amplitude", err)
	_, err = NewModel(sp, x, y, Hyper{Amplitude: 1, LengthScales: []float64{1, 1, 1}, Noise: math.Inf(1)}, DefaultNugget)
	add("inf noise", err)
	_, err = NewModel(sp, x, y, Hyper{Amplitude: 1, LengthScales: []float64{1, -1, 1}, Noise: 1}, DefaultNugget)
	add("negative length scale", err)
	_, err = NewModel(sp, x, y, good, 0)
	add("zero nugget", err)
	_, _, err = LogMarginalLikelihood(sp, x, y, good, -1)
	add("lml negative nugget", err)
	_, _, err = LogMarginalLikelihood(sp, x, y[:2], good, DefaultNugget)
	add("lml target count", err)
	_, _, err = LogMarginalLikelihood(sp, x, y, Hyper{Amplitude: 1, Noise: 1}, DefaultNugget)
	add("lml hyper", err)
	_, err = Fit(sp, x, y, FitOptions{Restarts: -1})
	add("negative restarts", err)
	_, err = Fit(sp, x, y, FitOptions{Nugget: -1})
	add("negative nugget", err)
	_, err = Fit(sp, x, y, FitOptions{MaxIter: -1})
	add("negative maxiter", err)
	_, err = Fit(sp, x, y, FitOptions{Acc: math.NaN()})
	add("nan acc", err)
	_, err = Fit(sp, x, y, FitOptions{Bounds: &Bounds{Amplitude: [2]float64{2, 1}, LengthScale: [2]float64{1, 2}, Noise: [2]float64{1, 2}}})
	add("crossed bounds", err)
	_, err = Fit(sp, x, y, FitOptions{Bounds: &Bounds{Amplitude: [2]float64{1, 2}, LengthScale: [2]float64{0, 2}, Noise: [2]float64{1, 2}}})
	add("zero bound", err)
	_, err = Fit(sp, x, y, FitOptions{Initial: &Hyper{Amplitude: 1}})
	add("initial hyper", err)
	_, err = Fit(sp, nil, nil, FitOptions{})
	add("fit no rows", err)
	m, mErr := NewModel(sp, x, y, good, DefaultNugget)
	if mErr != nil {
		t.Fatal(mErr)
	}
	_, _, err = m.Predict([]float64{1})
	add("predict width", err)
	_, _, err = m.Predict([]float64{math.NaN(), 0, 0, 0, 0})
	add("predict nan", err)
	for _, c := range cases {
		if c.err == nil {
			t.Errorf("%s: accepted", c.name)
		} else if !strings.HasPrefix(c.err.Error(), "gp: ") {
			t.Errorf("%s: error %q lacks the gp: prefix", c.name, c.err)
		}
	}
	h := m.Hyper()
	h.LengthScales[0] = 99
	if m.Hyper().LengthScales[0] != 1 {
		t.Error("Hyper returned an aliased slice")
	}
}
