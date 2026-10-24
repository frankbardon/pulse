package sem

import (
	"context"
	"fmt"
	"math"
	"math/rand/v2"
	"slices"

	"gonum.org/v1/gonum/mat"
	"gonum.org/v1/gonum/stat/distuv"
)

// bootstrap.go is the non-parametric bootstrap of a fitted model
// (lavaan's se = "bootstrap", boot.ci.type = "perc"): B replicates, each
// drawing every group's rows with replacement WITHIN the group (so each
// replicate keeps every n_g), refitted from the replicate's own moments
// with the full fit's model and options. Over the successful replicates
// each parameter-table row with a free or := value gets lavaan's
// bootstrap SE (the replicates' sample SD, divisor B − 1), z = θ̂ / SE,
// p = 2(1 − Φ(|z|)) and the percentile interval at the level
// (boot:::norm.inter, the interpolation lavaan reads); the point
// estimate and std_estimate stay the full data's.
//
// A replicate fails — and is skipped — when its fit errors (a sample
// covariance that is not positive definite, an undefined fit), stops
// short of convergence, or yields a non-finite value on any row whose
// full-data value is finite. Fewer than BootstrapMinSuccesses successes
// is a *BootstrapError. Determinism: one PCG stream seeded from
// BootstrapOptions.Seed draws every index, replicate by replicate and
// group by group in fit order, so the same rows and seed give the same
// bits.

// BootstrapMinSuccesses is the fewest successful replicates a bootstrap
// reports from.
const BootstrapMinSuccesses = 10

// bootstrapEarlyStop is the replicate count after which a bootstrap
// with no success at all stops (the refit fails systematically).
const bootstrapEarlyStop = 50

// BootstrapSample is one group's rows for resampling: row-major over
// the model's Observed order (len(Rows) a multiple of len(Observed)),
// plus the group's place in a multi-group fit (GroupMoments' Label,
// Level and Reference; ignored on a single-group fit).
type BootstrapSample struct {
	Rows      []float64
	Label     string
	Level     int
	Reference bool
}

// BootstrapOptions configure Bootstrap.
type BootstrapOptions struct {
	// Iters is the replicate count B (positive).
	Iters int
	// Seed seeds the replicate draws.
	Seed uint64
	// Level is the percentile interval's level, in (0, 1).
	Level float64
	// Standardize fits every replicate to its rows z-scored with the
	// population SD — the correlation matrix and zero means — as the
	// full fit of a standardizing vector is.
	Standardize bool
	// Ctx, when non-nil, is read before every replicate: a deadline or a
	// cancel stops the bootstrap between two refits with ctx's own
	// error.
	Ctx context.Context
}

// Bootstrap is a bootstrap's per-row figures, index-aligned with the
// full fit's table rows (BootstrapRows): SE and the percentile bounds,
// NaN on a row Apply leaves alone.
type Bootstrap struct {
	Requested, Succeeded int
	SE, CILower, CIUpper []float64
}

// BootstrapError is a bootstrap with fewer than BootstrapMinSuccesses
// successful replicates: Attempted replicates ran (fewer than Requested
// after the early stop) and Succeeded of them refitted.
type BootstrapError struct {
	Requested, Attempted, Succeeded int
}

func (e *BootstrapError) Error() string {
	return fmt.Sprintf("bootstrap produced %d successful replicates of %d run (%d requested); at least %d are needed",
		e.Succeeded, e.Attempted, e.Requested, BootstrapMinSuccesses)
}

// BootstrapRows is a fit's parameter-table rows in table order: the
// estimates, then the := rows.
func BootstrapRows(fit *Fit) []Estimate {
	return append(append([]Estimate(nil), fit.Estimates...), fit.Defined...)
}

// BootstrapGroupRows is a multi-group fit's table rows in table order:
// every group's estimates in fit order, then the := rows.
func BootstrapGroupRows(fit *GroupFit) []Estimate {
	var rows []Estimate
	for _, g := range fit.Groups {
		rows = append(rows, g.Estimates...)
	}
	return append(rows, fit.Defined...)
}

// bootstrapped reports whether a table row takes bootstrap figures: a
// free parameter or a := row, finite at the full data.
func bootstrapped(e Estimate) bool {
	return (e.Free || e.Op == OpDefined) && !math.IsNaN(e.Value) && !math.IsInf(e.Value, 0)
}

// RunBootstrap bootstraps the fit whose table rows are full (BootstrapRows
// / BootstrapGroupRows): one sample is a single-group fit (FitMoments),
// two or more a multi-group fit (FitGroups) of m, every replicate with
// opt. A replicate's moments are its rows' mean and biased (divisor n)
// covariance, as the full fit's.
func RunBootstrap(m *Model, samples []BootstrapSample, opt Options, bo BootstrapOptions, full []Estimate) (*Bootstrap, error) {
	p := len(m.Observed)
	if bo.Iters < 1 {
		return nil, fmt.Errorf("bootstrap replicates %d: need at least 1", bo.Iters)
	}
	if !(bo.Level > 0 && bo.Level < 1) {
		return nil, fmt.Errorf("bootstrap level %v: need 0 < level < 1", bo.Level)
	}
	if len(samples) == 0 {
		return nil, fmt.Errorf("bootstrap needs one sample at least")
	}
	ns := make([]int, len(samples))
	for g, s := range samples {
		if p == 0 || len(s.Rows)%p != 0 || len(s.Rows) == 0 {
			return nil, fmt.Errorf("bootstrap sample %d: %d values over %d observed variables", g, len(s.Rows), p)
		}
		ns[g] = len(s.Rows) / p
	}
	rng := rand.New(rand.NewPCG(bo.Seed, bo.Seed^0x9E3779B97F4A7C15))
	draw := make([][]float64, len(samples))
	for g := range samples {
		draw[g] = make([]float64, len(samples[g].Rows))
	}
	replicates := make([][]float64, 0, bo.Iters)
	attempted := 0
	for b := 0; b < bo.Iters; b++ {
		if len(replicates) == 0 && b >= bootstrapEarlyStop {
			break
		}
		if bo.Ctx != nil {
			if err := bo.Ctx.Err(); err != nil {
				return nil, err
			}
		}
		attempted++
		drawReplicate(rng, samples, ns, p, draw)
		rows, ok := bootstrapReplicate(m, samples, draw, ns, opt, bo.Standardize)
		if !ok || len(rows) != len(full) {
			continue
		}
		vals := make([]float64, len(full))
		good := true
		for j, e := range rows {
			vals[j] = e.Value
			if bootstrapped(full[j]) && (math.IsNaN(e.Value) || math.IsInf(e.Value, 0)) {
				good = false
				break
			}
		}
		if good {
			replicates = append(replicates, vals)
		}
	}
	if len(replicates) < BootstrapMinSuccesses {
		return nil, &BootstrapError{Requested: bo.Iters, Attempted: attempted, Succeeded: len(replicates)}
	}
	out := &Bootstrap{Requested: bo.Iters, Succeeded: len(replicates),
		SE: make([]float64, len(full)), CILower: make([]float64, len(full)), CIUpper: make([]float64, len(full))}
	alpha := [2]float64{(1 - bo.Level) / 2, (1 + bo.Level) / 2}
	col := make([]float64, len(replicates))
	for j, e := range full {
		if !bootstrapped(e) {
			out.SE[j], out.CILower[j], out.CIUpper[j] = math.NaN(), math.NaN(), math.NaN()
			continue
		}
		for b, r := range replicates {
			col[b] = r[j]
		}
		out.SE[j] = sampleSD(col)
		out.CILower[j] = normInter(col, alpha[0])
		out.CIUpper[j] = normInter(col, alpha[1])
	}
	return out, nil
}

// Apply writes b's figures into rows (the table rows b was run on):
// every bootstrapped row takes its SE, z = value / SE, p and the
// percentile interval; any other row is left as is.
func (b *Bootstrap) Apply(rows []Estimate) {
	for j := range rows {
		e := &rows[j]
		if j >= len(b.SE) || !bootstrapped(*e) {
			continue
		}
		e.SE = b.SE[j]
		e.Z = e.Value / e.SE
		e.P = 2 * (1 - normCDF(math.Abs(e.Z)))
		e.CILower, e.CIUpper = b.CILower[j], b.CIUpper[j]
	}
}

// drawReplicate fills draw[g] with ns[g] rows drawn with replacement
// from group g's own rows — within the group, so every replicate keeps
// each n_g — groups in fit order, rows in draw order.
func drawReplicate(rng *rand.Rand, samples []BootstrapSample, ns []int, p int, draw [][]float64) {
	for g, s := range samples {
		for i := 0; i < ns[g]; i++ {
			r := rng.IntN(ns[g])
			copy(draw[g][i*p:(i+1)*p], s.Rows[r*p:(r+1)*p])
		}
	}
}

// bootstrapReplicate refits one replicate (draw: every group's drawn
// rows) and returns its table rows; ok false when the fit fails or
// does not converge.
func bootstrapReplicate(m *Model, samples []BootstrapSample, draw [][]float64, ns []int, opt Options, standardize bool) ([]Estimate, bool) {
	p := len(m.Observed)
	if len(samples) == 1 {
		mean, s := rowMoments(draw[0], ns[0], p, standardize)
		var mv []float64
		if m.MeanStructure {
			mv = mean
		}
		fit, err := FitMoments(m, s, s, mv, float64(ns[0]), opt)
		if err != nil || !fit.Converged {
			return nil, false
		}
		return BootstrapRows(fit), true
	}
	groups := make([]GroupMoments, len(samples))
	for g, smp := range samples {
		mean, s := rowMoments(draw[g], ns[g], p, standardize)
		groups[g] = GroupMoments{Label: smp.Label, S: s, Raw: s, Mean: mean, N: float64(ns[g]), Level: smp.Level, Reference: smp.Reference}
	}
	fit, err := FitGroups(m, groups, opt)
	if err != nil || !fit.Converged {
		return nil, false
	}
	return BootstrapGroupRows(fit), true
}

// rowMoments are n row-major rows' mean and biased (divisor n)
// covariance, two-pass; standardize z-scores them with the population
// SD (the correlation matrix, zero means).
func rowMoments(rows []float64, n, p int, standardize bool) ([]float64, *mat.SymDense) {
	mean := make([]float64, p)
	for i := 0; i < n; i++ {
		for c := 0; c < p; c++ {
			mean[c] += rows[i*p+c]
		}
	}
	for c := range mean {
		mean[c] /= float64(n)
	}
	s := mat.NewSymDense(p, nil)
	for a := 0; a < p; a++ {
		for b := a; b < p; b++ {
			acc := 0.0
			for i := 0; i < n; i++ {
				acc += float64((rows[i*p+a] - mean[a]) * (rows[i*p+b] - mean[b]))
			}
			s.SetSym(a, b, acc/float64(n))
		}
	}
	if !standardize {
		return mean, s
	}
	sd := make([]float64, p)
	for a := range sd {
		sd[a] = math.Sqrt(s.At(a, a))
	}
	z := mat.NewSymDense(p, nil)
	for a := 0; a < p; a++ {
		for b := a; b < p; b++ {
			z.SetSym(a, b, s.At(a, b)/(sd[a]*sd[b]))
		}
	}
	return make([]float64, p), z
}

// sampleSD is the sample standard deviation (divisor n − 1).
func sampleSD(x []float64) float64 {
	m := 0.0
	for _, v := range x {
		m += v
	}
	m /= float64(len(x))
	ss := 0.0
	for _, v := range x {
		d := v - m
		ss += float64(d * d)
	}
	return math.Sqrt(ss / float64(len(x)-1))
}

// normInter is boot:::norm.inter, the percentile interval's endpoint at
// alpha that lavaan's boot.ci.type = "perc" reads: with R replicates,
// the order statistic at rank (R + 1)·alpha, interpolated on the normal
// quantile scale between the two neighbouring order statistics when the
// rank is fractional, and the extreme order statistic beyond them.
func normInter(t []float64, alpha float64) float64 {
	ts := slices.Clone(t)
	slices.Sort(ts)
	r := len(ts)
	rk := float64(r+1) * alpha
	k := int(math.Trunc(rk))
	switch {
	case k <= 0:
		return ts[0]
	case k >= r:
		return ts[r-1]
	case float64(k) == rk:
		return ts[k-1]
	}
	q := distuv.UnitNormal
	t1 := q.Quantile(alpha)
	t2 := q.Quantile(float64(k) / float64(r+1))
	t3 := q.Quantile(float64(k+1) / float64(r+1))
	tk, tk1 := ts[k-1], ts[k]
	return tk + (t1-t2)/(t3-t2)*(tk1-tk)
}
