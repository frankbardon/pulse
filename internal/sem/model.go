package sem

import (
	"fmt"
	"math"
	"sort"
)

// Matrix identifiers, in semopy's fixed order.
const (
	mBeta = iota
	mLambda
	mPsi
	mTheta
	// mNu (observed intercepts, a column over Observed) and mAlpha
	// (latent means and observed-inner intercepts, a column over Inner)
	// carry the mean structure; a covariance-only model has no
	// parameter in either, so its tables are unchanged.
	mNu
	mAlpha
	nMatrices
)

var matOp = [nMatrices]string{OpRegression, OpRegression, OpCovariance, OpCovariance, OpIntercept, OpIntercept}

type location struct {
	mat  int
	i, j int
	sym  bool
}

// parameter is one semopy Parameter: a free (active) value or a fixed
// one, at one or more matrix locations (a label shares one value).
type parameter struct {
	name     string
	active   bool
	start    float64
	hasStart bool
	lower    float64 // 0 for variances, -Inf otherwise
	locs     []location
}

type stringSet map[string]struct{}

func (s stringSet) has(v string) bool { _, ok := s[v]; return ok }
func (s stringSet) add(v string)      { s[v] = struct{}{} }

func (s stringSet) sorted() []string {
	out := make([]string, 0, len(s))
	for v := range s {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

// Model is a parsed, specified SEM: the variable classification and the
// parameter table, before any data is seen.
type Model struct {
	// Observed is the model's observed variables in semopy's order: the
	// sorted outputs (variables with no outgoing arrow), then the other
	// observed variables sorted. Sample and implied covariances use it.
	Observed []string
	// Inner is the non-output variables: sorted latents, then sorted
	// observed inner variables (Beta and Psi index it).
	Inner []string

	latent, exogenous, endogenous stringSet
	outputs                       stringSet
	obsIndex, innerIndex          map[string]int
	params                        []*parameter
	byName                        map[string]*parameter
	firstManif                    map[string]string
	nReg, nCov, nMean             int
	baseline                      bool

	// MeanStructure is set when the fit models the means too
	// (ModelOptions.MeanStructure, or any `~ 1` statement): observed
	// intercepts (Nu, and Alpha for observed inner variables) and latent
	// means (Alpha) join the parameters, and the objective gains the
	// mean term.
	MeanStructure bool
	// lavaan is set when any lavaan-only feature is in play: the fit
	// then follows lavaan's conventions (Lavaan) instead of semopy's.
	lavaan bool
}

// ModelOptions select the features beyond semopy's default
// covariance-structure model. The zero value is that model.
type ModelOptions struct {
	// MeanStructure models the means: every observed intercept free
	// (an observed exogenous variable's mean fixed to its sample mean,
	// lavaan's fixed.x), every latent mean fixed to 0 unless a `f ~ 1`
	// statement frees it — lavaan's meanstructure = TRUE. A `~ 1`
	// statement in the description sets it too.
	MeanStructure bool
}

// Lavaan reports whether the model follows lavaan's conventions rather
// than semopy's: true whenever a lavaan-only feature (a mean structure)
// is in play. Such a fit converges to the optimum (the default solver
// is Fisher scoring), leaves variances unbounded, and reports lavaan's
// chi-square, degrees of freedom, fit indices, log-likelihood and Wald
// intervals (ComputeStats); a covariance-only model keeps semopy's.
func (m *Model) Lavaan() bool { return m.lavaan }

// NewModel specifies the model a description describes, with semopy's
// defaults (variances of every endogenous and latent variable free,
// observed exogenous (co)variances fixed to the sample, exogenous
// latents free to covary, the first indicator of each latent fixed to
// 1). baseline builds semopy's independence model instead.
func NewModel(desc string, baseline bool) (*Model, error) {
	return newModel(desc, baseline, ModelOptions{})
}

// NewModelWithOptions specifies the model a description describes with
// the features opt selects (ModelOptions); NewModel(desc, false) is
// NewModelWithOptions(desc, ModelOptions{}).
func NewModelWithOptions(desc string, opt ModelOptions) (*Model, error) {
	return newModel(desc, false, opt)
}

func newModel(desc string, baseline bool, opt ModelOptions) (*Model, error) {
	eff, err := Parse(desc)
	if err != nil {
		return nil, err
	}
	m := &Model{byName: map[string]*parameter{}, firstManif: map[string]string{}, baseline: baseline}
	_, icpt := eff.byOp[OpIntercept]
	m.MeanStructure = opt.MeanStructure || icpt
	m.lavaan = m.MeanStructure
	m.classify(eff)
	m.preprocessCovariances(eff)
	for _, op := range eff.ops {
		var err error
		switch op {
		case OpRegression:
			err = m.effectRegression(eff.byOp[op])
		case OpMeasurement:
			err = m.effectMeasurement(eff.byOp[op])
		case OpCovariance:
			err = m.effectCovariance(eff.byOp[op])
		}
		if err != nil {
			return nil, err
		}
	}
	if m.MeanStructure && !baseline {
		if err := m.effectMeans(eff.byOp[OpIntercept]); err != nil {
			return nil, err
		}
	}
	return m, nil
}

// effectMeans adds the mean-structure parameters with lavaan's
// defaults: the `~ 1` statements first, in their order, then every
// other observed variable (Observed order) — free, except an observed
// exogenous one, whose mean is fixed to its sample mean (fixed.x) —
// then every other latent (Inner order), its mean fixed to 0.
func (m *Model) effectMeans(user *lhsMap) error {
	done := stringSet{}
	add := func(v string, mu Mult, active bool, name string) error {
		loc, ok := m.meanLocation(v)
		if !ok {
			return fmt.Errorf("intercept statement %s ~ 1 names a variable the model does not use", v)
		}
		if name == "" {
			m.nMean++
			name = fmt.Sprintf("_m%d", m.nMean)
		}
		m.addParam(name, active, mu, math.Inf(-1), loc)
		done.add(v)
		return nil
	}
	if user != nil {
		for _, v := range user.keys {
			mu := user.m[v].m[interceptTerm]
			name, active := paramMode(mu)
			if err := add(v, mu, active, name); err != nil {
				return err
			}
		}
	}
	for _, v := range m.Observed {
		if done.has(v) {
			continue
		}
		// An observed exogenous mean is the sample's (startRule).
		if err := add(v, Mult{}, !m.exogenous.has(v), ""); err != nil {
			return err
		}
	}
	for _, v := range m.Inner {
		if done.has(v) || !m.latent.has(v) {
			continue
		}
		if err := add(v, Mult{Set: true, Fixed: true, Value: 0}, false, ""); err != nil {
			return err
		}
	}
	return nil
}

// meanLocation is where a variable's intercept / mean lives: Alpha for
// an inner variable (a latent, or an observed variable with outgoing
// arrows, whose Lambda row is the identity), else Nu.
func (m *Model) meanLocation(v string) (location, bool) {
	if i, ok := m.innerIndex[v]; ok {
		return location{mat: mAlpha, i: i}, true
	}
	if i, ok := m.obsIndex[v]; ok {
		return location{mat: mNu, i: i}, true
	}
	return location{}, false
}

func (m *Model) classify(eff *Effects) {
	latents, in, out := stringSet{}, stringSet{}, stringSet{}
	if l, ok := eff.byOp[OpMeasurement]; ok {
		for _, lat := range l.keys {
			latents.add(lat)
			out.add(lat)
			for _, ind := range l.m[lat].keys {
				in.add(ind)
			}
		}
	}
	if l, ok := eff.byOp[OpRegression]; ok {
		for _, lv := range l.keys {
			in.add(lv)
			for _, rv := range l.m[lv].keys {
				out.add(rv)
			}
		}
	}
	all, exo, outputs, endo, obs := stringSet{}, stringSet{}, stringSet{}, stringSet{}, stringSet{}
	for v := range out {
		all.add(v)
	}
	for v := range in {
		all.add(v)
	}
	for v := range all {
		if out.has(v) && !in.has(v) {
			exo.add(v)
		} else {
			endo.add(v)
		}
		if in.has(v) && !out.has(v) {
			outputs.add(v)
		}
		if !latents.has(v) {
			obs.add(v)
		}
	}
	m.latent, m.exogenous, m.endogenous, m.outputs = latents, exo, endo, outputs
	// mimic_lavaan=False: every output is a Lambda row (_output); the
	// rest are inner variables.
	innerLat, innerObs := stringSet{}, stringSet{}
	for v := range all {
		if outputs.has(v) {
			continue
		}
		if latents.has(v) {
			innerLat.add(v)
		} else {
			innerObs.add(v)
		}
	}
	m.Inner = append(innerLat.sorted(), innerObs.sorted()...)
	obsOut, obsRest := stringSet{}, stringSet{}
	for v := range obs {
		if outputs.has(v) {
			obsOut.add(v)
		} else {
			obsRest.add(v)
		}
	}
	m.Observed = append(obsOut.sorted(), obsRest.sorted()...)
	m.obsIndex, m.innerIndex = indexOf(m.Observed), indexOf(m.Inner)
}

func (m *Model) isObsExo(v string) bool {
	_, obs := m.obsIndex[v]
	return obs && m.exogenous.has(v)
}

func indexOf(names []string) map[string]int {
	out := make(map[string]int, len(names))
	for i, n := range names {
		out[n] = i
	}
	return out
}

// startMult marks semopy's internal START multiplier: a fixed value
// taken from the starting-value rule (the sample moment).
var startMult = Mult{Set: true, Label: "\x00START"}

func (mu Mult) isStart() bool { return mu.Label == startMult.Label }

func (m *Model) preprocessCovariances(eff *Effects) {
	cov := eff.lhs(OpCovariance)
	var obsExo []string
	for _, v := range m.Observed {
		if m.exogenous.has(v) {
			obsExo = append(obsExo, v)
		}
	}
	sort.Strings(obsExo)
	for _, v := range obsExo {
		if r := cov.rhs(v); !r.has(v) {
			r.set(v, startMult)
		}
	}
	endoLat := stringSet{}
	for v := range m.endogenous {
		endoLat.add(v)
	}
	for v := range m.latent {
		endoLat.add(v)
	}
	for _, v := range endoLat.sorted() {
		if r := cov.rhs(v); !r.has(v) {
			r.set(v, Mult{})
		}
	}
	for a := 0; a < len(obsExo); a++ {
		for b := a + 1; b < len(obsExo); b++ {
			x, y := obsExo[a], obsExo[b]
			if !cov.rhs(y).has(x) && !cov.rhs(x).has(y) {
				cov.rhs(x).set(y, startMult)
			}
		}
	}
	// cov_diag=False: exogenous latents covary freely. (The output-pair
	// rule is empty under mimic_lavaan=False.)
	var exoLat []string
	for v := range m.exogenous {
		if m.latent.has(v) {
			exoLat = append(exoLat, v)
		}
	}
	sort.Strings(exoLat)
	for a := 0; a < len(exoLat); a++ {
		for b := a + 1; b < len(exoLat); b++ {
			x, y := exoLat[a], exoLat[b]
			if !cov.rhs(y).has(x) && !cov.rhs(x).has(y) {
				cov.rhs(x).set(y, Mult{})
			}
		}
	}
}

func (m *Model) addParam(name string, active bool, mu Mult, lower float64, loc location) {
	if p, ok := m.byName[name]; ok {
		p.locs = append(p.locs, loc)
		return
	}
	p := &parameter{name: name, active: active, lower: lower, locs: []location{loc}}
	if mu.Fixed {
		p.start, p.hasStart = mu.Value, true
	}
	m.params = append(m.params, p)
	m.byName[name] = p
}

// paramMode resolves a multiplier into (name, active) the way semopy's
// effect handlers do; name "" means "auto-name".
func paramMode(mu Mult) (string, bool) {
	switch {
	case !mu.Set:
		return "", true
	case mu.Fixed:
		return "", false
	case mu.isStart():
		return "", false
	default:
		return mu.Label, true
	}
}

func (m *Model) effectRegression(l *lhsMap) error {
	if m.baseline {
		return nil
	}
	for _, lv := range l.keys {
		r := l.m[lv]
		mat, rows, cols := mBeta, m.innerIndex, m.innerIndex
		if m.outputs.has(lv) {
			mat, rows = mLambda, m.obsIndex
		}
		i, ok := rows[lv]
		if !ok {
			return fmt.Errorf("variable %q cannot be regressed here", lv)
		}
		for _, rv := range r.keys {
			j, ok := cols[rv]
			if !ok {
				return fmt.Errorf("variable %q cannot be a predictor here", rv)
			}
			name, active := paramMode(r.m[rv])
			if name == "" {
				m.nReg++
				name = fmt.Sprintf("_b%d", m.nReg)
			}
			m.addParam(name, active, r.m[rv], math.Inf(-1), location{mat: mat, i: i, j: j})
		}
	}
	return nil
}

func (m *Model) effectMeasurement(l *lhsMap) error {
	rev := &lhsMap{m: map[string]*rhsMap{}}
	for _, lat := range l.keys {
		r := l.m[lat]
		inds := append([]string(nil), r.keys...)
		mults := make([]Mult, len(inds))
		first := -1
		for k, ind := range inds {
			mults[k] = r.m[ind]
			if first < 0 && (mults[k].Fixed || mults[k].isStart()) {
				first = k
			}
		}
		if first < 0 {
			for k := range mults {
				if !mults[k].Set {
					first = k
					break
				}
			}
			if first >= 0 {
				mults[first] = Mult{Set: true, Fixed: true, Value: 1}
			}
		}
		for k, ind := range inds {
			rev.rhs(ind).set(lat, mults[k])
		}
		if first >= 0 {
			m.firstManif[lat] = inds[first]
		}
	}
	return m.effectRegression(rev)
}

func (m *Model) effectCovariance(l *lhsMap) error {
	inner := m.innerIndex
	for _, lv := range l.keys {
		r := l.m[lv]
		for _, rv := range r.keys {
			mu := r.m[rv]
			name, active := paramMode(mu)
			if name == "" {
				m.nCov++
				name = fmt.Sprintf("_c%d", m.nCov)
			}
			_, lvInner := inner[lv]
			_, rvInner := inner[rv]
			mat, idx := mTheta, m.obsIndex
			if lvInner && rvInner {
				mat, idx = mPsi, inner
			}
			i, ok1 := idx[lv]
			j, ok2 := idx[rv]
			if !ok1 || !ok2 {
				return fmt.Errorf("covariance %s ~~ %s mixes inner and output variables (unsupported)", lv, rv)
			}
			lower, sym := math.Inf(-1), true
			if i == j {
				if m.baseline && m.latent.has(lv) {
					continue
				}
				// semopy bounds a variance below by 0; lavaan does not.
				lower, sym = 0, false
				if m.lavaan {
					lower = math.Inf(-1)
				}
			} else if m.baseline {
				continue
			}
			m.addParam(name, active, mu, lower, location{mat: mat, i: i, j: j, sym: sym})
		}
	}
	return nil
}
