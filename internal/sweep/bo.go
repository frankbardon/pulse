package sweep

import (
	"math"
	"slices"
	"strconv"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/gp"
	"github.com/frankbardon/pulse/linalg/detmath"
	"github.com/frankbardon/pulse/optimize"
	"github.com/frankbardon/pulse/types"
)

// bo.go is mode bo: Bayesian optimisation on the adaptive executor
// (adaptive.go) over the internal/gp surrogate — a zero-mean GP with a
// Matérn-5/2 ARD kernel fitted by ML-II each round — with
// expected-improvement (EI) acquisition (Jones, Schonlau & Welch 1998;
// scikit-optimize's gp_minimize with acq_func "EI").
//
// Every axis is searched on the unit interval: a range axis's
// coordinate is mapped by RangeValue (its integer / log form honoured)
// and enters the surrogate as a continuous dimension; a list axis's
// coordinate u picks the value at index ⌊u·L⌋ clipped into [0, L) and
// enters the surrogate one-hot (gp.Dim{Levels: L}).
//
//   - Round 0 is the initial design: NInit points of a Latin hypercube
//     (init lhs, the lhs draw) or uniform draws (init random, point by
//     point, axis by axis).
//   - Each later round fits the surrogate to every observation so far
//     (x the encoded point, y the Loss — the minimise form, so
//     rank.order is honoured; a penalised point carries the worst
//     observed loss), then proposes min(Batch, budget left) points. Each
//     point maximises EI(x) = (y* − μ)·Φ(z) + σ·φ(z), z = (y* − μ)/σ,
//     with y* the lowest y and μ, σ² the posterior mean and latent
//     variance (no exploration margin, so EI's argmax is invariant to
//     the loss's scale), and EI = 0 where σ² = 0. The maximiser is
//     a seeded multi-start: BOCandidates Latin-hypercube candidates are
//     scored, the BOPolish best (ties: the earlier candidate) are
//     polished by SLSQP (optimize.Minimize) over the continuous
//     coordinates — the list coordinates held — on −EI divided by the
//     population standard deviation of y (1 when it is below 10·ε), with
//     a central-difference gradient, and the best of the
//     candidates and their polishes wins (a polish replaces the incumbent
//     only when strictly better).
//   - Batch > 1 fills by constant liar (cl_min): after each point the
//     point joins the training set at the lowest y and the surrogate is
//     re-conditioned at the round's fitted hyperparameters, so the next
//     point is pushed elsewhere. The candidates are drawn once per round.
//
// Duplicate proposals are accepted (gp factors with CholeskyRidge
// jitter). A surrogate fault (a non-finite loss, a covariance no start
// could factor) is PULSE_SWEEP_INVALID bo_surrogate_failed naming the
// round.
//
// Draws come from one PCG stream over the effective seed (seed,
// seed ^ PCGStream), raw words only: the initial design, then per later
// round one word seeding the GP fit's restarts and the candidate
// hypercube. Every product feeding an addition is an explicit
// float64(a*b) and exp / erfc are linalg/detmath's, so a seed replays
// the search bit-identically on every architecture.

// The defaults and fixed settings of mode bo (types.SweepBO).
const (
	// BOInitPerAxis is the default initial-design size per axis: 2 ×
	// the axis count, capped at the budget.
	BOInitPerAxis = 2
	// BODefaultBatch is the default number of points a later round
	// proposes.
	BODefaultBatch = 1
	// BOCandidates is the number of Latin-hypercube candidates EI is
	// scored at each round.
	BOCandidates = 1000
	// BOPolish is the number of best candidates SLSQP polishes.
	BOPolish = 5
)

// The reasons mode bo's knob rules give. PULSE_SWEEP_INVALID:
const (
	// ReasonBOInitInvalid: `bo.init` is not lhs or random.
	ReasonBOInitInvalid = "bo_init_invalid"
	// ReasonBONInitInvalid: `bo.n_init` is below 1.
	ReasonBONInitInvalid = "bo_n_init_invalid"
	// ReasonBONInitOverBudget: `bo.n_init` exceeds `budget`.
	ReasonBONInitOverBudget = "bo_n_init_over_budget"
	// ReasonBOBatchInvalid: `bo.batch` is below 1.
	ReasonBOBatchInvalid = "bo_batch_invalid"
	// ReasonBOBatchOverBudget: `bo.batch` exceeds `budget`.
	ReasonBOBatchOverBudget = "bo_batch_over_budget"
	// ReasonBOSurrogateFailed: a round's Gaussian-process surrogate
	// could not be fitted to the observations (a run-time refusal).
	ReasonBOSurrogateFailed = "bo_surrogate_failed"
)

// ReasonBOWithoutBO (PULSE_SWEEP_MODE_MISMATCH): `bo` is set under a
// mode other than bo.
const ReasonBOWithoutBO = "bo_without_bo_mode"

func init() {
	RegisterAdaptive(AdaptiveMode{Mode: types.SweepModeBO, Validate: validateBO, New: newBO})
}

// BOSettings are a validated bo sweep's resolved knobs.
type BOSettings struct {
	Init  types.SweepBOInit
	NInit int
	Batch int
}

// ResolveBO resolves spec's bo knobs against their defaults (spec
// validated): init lhs, n_init 2 × axes capped at the budget, batch 1.
func ResolveBO(spec *types.SweepSpec) BOSettings {
	s := BOSettings{Init: types.SweepBOInitLHS, NInit: min(BOInitPerAxis*len(spec.Axes), spec.Budget), Batch: BODefaultBatch}
	if k := spec.BO; k != nil {
		if k.Init != "" {
			s.Init = k.Init
		}
		if k.NInit != nil {
			s.NInit = *k.NInit
		}
		if k.Batch != nil {
			s.Batch = *k.Batch
		}
	}
	return s
}

// validateBO is mode bo's knob rule set (AdaptiveMode.Validate).
func validateBO(spec *types.SweepSpec) *errors.CodedError {
	k := spec.BO
	if k == nil {
		return nil
	}
	if k.Init != "" && !slices.Contains(types.AllSweepBOInits(), k.Init) {
		valid := make([]string, 0, 2)
		for _, v := range types.AllSweepBOInits() {
			valid = append(valid, string(v))
		}
		return invalid("sweep.bo.init", "", ReasonBOInitInvalid,
			"sweep bo.init is "+strconv.Quote(string(k.Init))+"; the initial design is lhs (default) or random",
			map[string]any{"value": string(k.Init), "valid": valid})
	}
	if ce := boCount("n_init", k.NInit, spec.Budget, ReasonBONInitInvalid, ReasonBONInitOverBudget,
		"the initial design evaluates n_init points in round 0"); ce != nil {
		return ce
	}
	return boCount("batch", k.Batch, spec.Budget, ReasonBOBatchInvalid, ReasonBOBatchOverBudget,
		"each later round proposes batch points")
}

// boCount checks one bo count knob: at least 1, at most the budget.
func boCount(key string, v *int, budget int, low, over, why string) *errors.CodedError {
	if v == nil {
		return nil
	}
	field := "sweep.bo." + key
	if *v < 1 {
		return invalid(field, "", low,
			"sweep bo."+key+" is "+strconv.Itoa(*v)+"; "+why+", so set it to at least 1",
			map[string]any{"value": *v, "min": 1})
	}
	if *v > budget {
		return invalid(field, "", over,
			"sweep bo."+key+" is "+strconv.Itoa(*v)+" but budget is "+strconv.Itoa(budget)+
				"; "+why+", so set it to at most the budget (or raise budget)",
			map[string]any{"value": *v, "budget": budget})
	}
	return nil
}

// boStrategy is mode bo's Strategy.
type boStrategy struct {
	dims    []Dim
	set     BOSettings
	draw    *sampler
	space   *gp.Space
	started bool
}

// newBO builds mode bo's strategy (AdaptiveMode.New).
func newBO(cfg StrategyConfig) (Strategy, error) {
	gd := make([]gp.Dim, len(cfg.Dims))
	for i, d := range cfg.Dims {
		if d.Range == nil {
			gd[i].Levels = d.Levels
		}
	}
	space, err := gp.NewSpace(gd)
	if err != nil {
		return nil, err
	}
	return &boStrategy{dims: cfg.Dims, set: ResolveBO(cfg.Spec), draw: newSampler(cfg.Seed), space: space}, nil
}

// Propose implements Strategy.
func (s *boStrategy) Propose(round int, history []Observation, limit int) ([]Point, error) {
	d := len(s.dims)
	if !s.started {
		s.started = true
		n := min(s.set.NInit, limit)
		out := make([]Point, n)
		if s.set.Init == types.SweepBOInitRandom {
			for i := range out {
				u := make([]float64, d)
				for j := range u {
					u[j] = s.draw.unit()
				}
				out[i] = s.point(u)
			}
			return out, nil
		}
		h := s.draw.latinHypercube(n, d)
		for i := range out {
			u := make([]float64, d)
			for j := range u {
				u[j] = h[j][i]
			}
			out[i] = s.point(u)
		}
		return out, nil
	}

	x := make([][]float64, len(history))
	y := make([]float64, len(history))
	for i, o := range history {
		row, err := s.space.Encode(o.Point.U, o.Point.Index)
		if err != nil {
			return nil, surrogateFault(round, err)
		}
		x[i], y[i] = row, o.Loss
	}
	seed := int64(s.draw.src.Uint64())
	model, err := gp.Fit(s.space, x, y, gp.FitOptions{Seed: seed})
	if err != nil {
		return nil, surrogateFault(round, err)
	}
	h := s.draw.latinHypercube(BOCandidates, d)
	cands := make([]Point, BOCandidates)
	for i := range cands {
		u := make([]float64, d)
		for j := range u {
			u[j] = h[j][i]
		}
		cands[i] = s.point(u)
	}

	yBest := slices.Min(y)
	scale := populationSD(y)
	acq := eiScorer{space: s.space, best: yBest, scale: scale}
	n := min(s.set.Batch, limit)
	out := make([]Point, 0, n)
	for k := range n {
		acq.model = model
		p, err := s.maximise(&acq, cands)
		if err != nil {
			return nil, surrogateFault(round, err)
		}
		out = append(out, p)
		if k == n-1 {
			break
		}
		// Constant liar (cl_min): the point joins at the lowest y.
		row, err := s.space.Encode(p.U, p.Index)
		if err != nil {
			return nil, surrogateFault(round, err)
		}
		x, y = append(x, row), append(y, yBest)
		model, err = gp.NewModel(s.space, x, y, model.Hyper(), gp.DefaultNugget)
		if err != nil {
			return nil, surrogateFault(round, err)
		}
	}
	return out, nil
}

// point maps unit coordinates onto an executor Point: a range axis
// keeps its coordinate, a list axis takes the index ⌊u·L⌋ clipped into
// [0, L).
func (s *boStrategy) point(u []float64) Point {
	p := Point{U: make([]float64, len(s.dims)), Index: make([]int, len(s.dims))}
	for j, d := range s.dims {
		if d.Range != nil {
			p.U[j] = min(max(u[j], 0), 1)
			continue
		}
		p.Index[j] = min(int(math.Floor(float64(u[j]*float64(d.Levels)))), d.Levels-1)
	}
	return p
}

// maximise is EI's seeded multi-start maximiser over one round's
// candidates: score every candidate, polish the BOPolish best by SLSQP
// over the continuous coordinates, keep the best (a polish wins only
// when strictly better; candidate ties keep the earlier one).
func (s *boStrategy) maximise(acq *eiScorer, cands []Point) (Point, error) {
	scores := make([]float64, len(cands))
	order := make([]int, len(cands))
	for i, c := range cands {
		v, err := acq.score(c)
		if err != nil {
			return Point{}, err
		}
		scores[i], order[i] = v, i
	}
	slices.SortStableFunc(order, func(a, b int) int {
		switch {
		case scores[a] > scores[b]:
			return -1
		case scores[a] < scores[b]:
			return 1
		}
		return 0
	})
	best, bestScore := cands[order[0]], scores[order[0]]
	var cont []int
	for j, d := range s.dims {
		if d.Range != nil {
			cont = append(cont, j)
		}
	}
	if len(cont) == 0 {
		return best, nil
	}
	for _, i := range order[:min(BOPolish, len(order))] {
		p, v, err := acq.polish(cands[i], cont)
		if err != nil {
			return Point{}, err
		}
		if v > bestScore {
			best, bestScore = p, v
		}
	}
	return best, nil
}

// eiScorer is expected improvement under one surrogate, divided by
// scale (the observed losses' deviation) so its magnitude — and
// SLSQP's absolute accuracy goal — is unit-free.
type eiScorer struct {
	space *gp.Space
	model *gp.Model
	best  float64
	scale float64
}

// score is EI at p over scale.
func (e *eiScorer) score(p Point) (float64, error) {
	row, err := e.space.Encode(p.U, p.Index)
	if err != nil {
		return 0, err
	}
	mu, v, err := e.model.Predict(row)
	if err != nil {
		return 0, err
	}
	return ExpectedImprovement(mu, v, e.best) / e.scale, nil
}

// boPolishStep is the central-difference step of the polish gradient.
const boPolishStep = 1e-6

// polish runs SLSQP from p on −score over the continuous coordinates
// cont (bounds [0, 1], the list indices held) and returns the final
// iterate and its score. A run that stops short keeps its final
// iterate.
func (e *eiScorer) polish(p Point, cont []int) (Point, float64, error) {
	at := func(xc []float64) Point {
		q := Point{U: slices.Clone(p.U), Index: p.Index}
		for k, j := range cont {
			q.U[j] = min(max(xc[k], 0), 1)
		}
		return q
	}
	var fault error
	f := func(xc []float64) float64 {
		v, err := e.score(at(xc))
		if err != nil && fault == nil {
			fault = err
		}
		return -v
	}
	grad := func(xc []float64) []float64 {
		g := make([]float64, len(xc))
		w := slices.Clone(xc)
		for k, v := range xc {
			hi, lo := min(v+boPolishStep, 1), max(v-boPolishStep, 0)
			w[k] = hi
			fh := f(w)
			w[k] = lo
			fl := f(w)
			w[k] = v
			g[k] = (fh - fl) / (hi - lo)
		}
		return g
	}
	x0 := make([]float64, len(cont))
	lower, upper := make([]float64, len(cont)), make([]float64, len(cont))
	for k, j := range cont {
		x0[k], upper[k] = p.U[j], 1
	}
	res, err := optimize.Minimize(f, grad, x0, optimize.MinimizeOptions{Lower: lower, Upper: upper, Acc: 1e-9, MaxIter: 100})
	if fault != nil {
		return Point{}, 0, fault
	}
	if res == nil {
		return Point{}, 0, err
	}
	q := at(res.X)
	v, err := e.score(q)
	if err != nil {
		return Point{}, 0, err
	}
	return q, v, nil
}

// invSqrt2Pi is 1/√(2π), correctly rounded.
const invSqrt2Pi = 0.3989422804014327

// ExpectedImprovement is EI for minimisation at a point with posterior
// mean mu and latent variance v, below the incumbent best:
// (best − mu)·Φ(z) + σ·φ(z), z = (best − mu)/σ, σ = √v; 0 where v is
// not positive. Φ(z) is erfc(−z/√2)/2 and φ is exp(−z²/2)
// /√(2π), both linalg/detmath's, so the value is bit-identical on every
// architecture.
func ExpectedImprovement(mu, v, best float64) float64 {
	if !(v > 0) {
		return 0
	}
	sd := math.Sqrt(v)
	imp := best - mu
	z := imp / sd
	cdf := float64(0.5 * detmath.Erfc(-z/math.Sqrt2))
	pdf := float64(detmath.Exp(float64(float64(-0.5*z)*z)) * invSqrt2Pi)
	return float64(imp*cdf) + float64(sd*pdf)
}

// populationSD is ys' population standard deviation, 1 when it is
// below 10·ε (gp's own standardisation rule).
func populationSD(ys []float64) float64 {
	var sum float64
	for _, v := range ys {
		sum += v
	}
	mean := sum / float64(len(ys))
	var ss float64
	for _, v := range ys {
		dv := v - mean
		ss += float64(dv * dv)
	}
	sd := math.Sqrt(ss / float64(len(ys)))
	if sd < 10*0x1p-52 {
		return 1
	}
	return sd
}

// surrogateFault maps a surrogate error onto the sweep family:
// PULSE_SWEEP_INVALID bo_surrogate_failed, naming the round.
func surrogateFault(round int, err error) *errors.CodedError {
	ce := invalid("sweep.mode", "", ReasonBOSurrogateFailed,
		"sweep mode bo round "+strconv.Itoa(round)+" could not fit its Gaussian-process surrogate to the observations so far ("+
			err.Error()+"); check the rank objective is a finite number on every slot, or search with mode de",
		map[string]any{"mode": string(types.SweepModeBO), "round": round, "cause": err.Error()})
	ce.Cause = err
	return ce
}
