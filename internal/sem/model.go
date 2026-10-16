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
	nMatrices
)

var matOp = [nMatrices]string{OpRegression, OpRegression, OpCovariance, OpCovariance}

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
	nReg, nCov                    int
	baseline                      bool
}

// NewModel specifies the model a description describes, with semopy's
// defaults (variances of every endogenous and latent variable free,
// observed exogenous (co)variances fixed to the sample, exogenous
// latents free to covary, the first indicator of each latent fixed to
// 1). baseline builds semopy's independence model instead.
func NewModel(desc string, baseline bool) (*Model, error) {
	eff, err := Parse(desc)
	if err != nil {
		return nil, err
	}
	m := &Model{byName: map[string]*parameter{}, firstManif: map[string]string{}, baseline: baseline}
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
	return m, nil
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
				lower, sym = 0, false
			} else if m.baseline {
				continue
			}
			m.addParam(name, active, mu, lower, location{mat: mat, i: i, j: j, sym: sym})
		}
	}
	return nil
}
