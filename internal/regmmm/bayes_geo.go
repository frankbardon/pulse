package regmmm

import (
	"cmp"
	"context"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/internal/fieldaccept"
	"github.com/frankbardon/pulse/internal/limits"
	pmodel "github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/paramread"
	"github.com/frankbardon/pulse/mcmc"
	"github.com/frankbardon/pulse/types"
)

// bayes_geo.go is REG_MMM_BAYES's geo panel (params.geo): the spec
// refusals, the panel's per-geo time grid, the MaxMMMGeos limit and the
// sampler run over the hierarchical model (bayes_geo_model.go), and the
// panel's coefficient table and outputs.
//
// params.geo names the market field — a categorical (its labels) or an
// unsigned integer code (its decimal values). The rows are then one
// series per geo: each geo's params.time contiguous with step 1 (ragged
// starts and ends allowed; a repeat within a geo is duplicate_time, an
// interior jump geo_time_gap), and a geo whose value is null is
// geo_missing. The geos are ordered by label (byte order) or by code
// (numerically).
//
// The coefficient table (geoCoefNames): "(intercept)[g]" per geo; per
// channel its population β (term c: β_mode's μ, or in ROI mode the β the
// population ROI and kernel give over the whole panel) then each geo's
// β ("c[g]"); the controls; per kernel parameter and channel the
// population value ("decay:c") then each geo's ("decay:c[g]"); the
// curve parameters — shared ("k:c"), or with params.per_geo_curves the
// population value then each geo's ("k:c[g]"); "sigma"; then each pooling
// scale ("tau:c" for the effect, "tau:decay:c" per kernel parameter,
// "tau:k:c" per pooled curve parameter). outputs.roi / mroi /
// contribution carry per channel the all-geo figure under the channel's
// name — the summed contribution over the summed spend, the
// spend-weighted marginal ROI, the summed contribution — and each geo's
// under "c[g]"; outputs.hierarchy the geo field, the geos and per pooled
// class its population value μ and scale τ.

// TauPrefix names a geo panel's pooling scale: TauPrefix + term.
const TauPrefix = pmodel.MMMTauPrefix

// geoBrackets are the characters a channel or predictor name may not
// carry: they delimit a per-geo term ("tv[NE]").
const geoBrackets = pmodel.MMMGeoOpen + pmodel.MMMGeoClose

// The geo panel's details.reason values.
const (
	reasonBracketReserved = "name_reserved"
	reasonGeoConflict     = "geo_conflict"
	reasonGeoMissing      = "geo_missing"
	reasonGeoInvalid      = "geo_not_code"
	reasonGeoTimeGap      = "geo_time_gap"
	// reasonPerGeoCurvesNoGeo refuses params.per_geo_curves on one
	// market's series.
	reasonPerGeoCurvesNoGeo = "per_geo_curves_without_geo"
)

// geoSpecRefusal is params.geo's spec rule: a field name that is not the
// target, the time field or a predictor.
func geoSpecRefusal(spec *types.RegressionSpec, geo, timeField string) error {
	if geo == "" {
		return bayesSlotError(spec, "params.geo", "params.geo must name the panel's market field (a categorical or an unsigned integer code)", nil)
	}
	if geo == spec.Target || geo == timeField || slices.Contains(spec.Predictors, geo) {
		return bayesSlotError(spec, "params.geo", fmt.Sprintf("params.geo %q is the target, the time field or a predictor; name the panel's market field", geo),
			map[string]any{"field": geo, "reason": reasonGeoConflict})
	}
	return nil
}

// BayesGeoFieldRefusal is the accepted-types table's refusal of spec's
// params.geo field (internal/fieldaccept, slot geo: a categorical or an
// unsigned integer), nil when the field is accepted, unknown to schema
// (the field-reference walk refuses an unknown name), absent, or the
// spec is no REG_MMM_BAYES geo panel. The factory and predict both call
// it, after every spec refusal.
func BayesGeoFieldRefusal(spec *types.RegressionSpec, schema *encoding.Schema) *perr.CodedError {
	if spec == nil || spec.Type != BayesName || schema == nil || len(spec.Params) == 0 {
		return nil
	}
	var p struct {
		Geo string `json:"geo"`
	}
	if paramread.Probe(spec.Params, &p) != nil || p.Geo == "" {
		return nil
	}
	return fieldaccept.Check(string(BayesName), fieldaccept.SlotGeo, p.Geo, schema)
}

// geoKey is one row's geo: its label, or its code.
type geoKey struct {
	label  string
	code   uint64
	isCode bool
}

func (k geoKey) name() string {
	if k.isCode {
		return strconv.FormatUint(k.code, 10)
	}
	return k.label
}

// geoData is the panel in geo order, each geo's rows in time order.
type geoData struct {
	names  []string
	series []*bayesData
}

// readGeoKey reads rec's geo: a categorical's label, else a non-negative
// integer's code; ok false for a null, reason non-empty for a value no
// code reads (a fraction or a negative, which only a field outside the
// accepted types carries).
func (r *bayesRegression) readGeoKey(rec extend.Record) (geoKey, error) {
	field := r.cfg.geo
	if s, ok := rec.StringValue(field); ok {
		return geoKey{label: s}, nil
	}
	v, ok := rec.NumericValue(field)
	if !ok {
		return geoKey{}, r.configError(fmt.Sprintf("params.geo %q is null on a row, or is not a field of the cohort", field),
			map[string]any{"slot": "params.geo", "field": field, "reason": reasonGeoMissing})
	}
	if !(v >= 0) || v != math.Trunc(v) {
		return geoKey{}, r.configError(fmt.Sprintf("params.geo %q must be a categorical or an unsigned integer code, got %v", field, v),
			map[string]any{"slot": "params.geo", "field": field, "reason": reasonGeoInvalid})
	}
	u, _ := rec.Uint64Value(field)
	return geoKey{code: u, isCode: true}, nil
}

// readGeo reads the panel: every row's time, outcome, spend and
// controls (bayesRegression.read's per-row rules), its geo, the
// MaxMMMGeos limit (from ctx; the default when ctx carries none), then
// per geo the time order and its grid.
func (r *bayesRegression) readGeo(ctx context.Context, rows extend.Rows) (*geoData, error) {
	n := rows.Len()
	keys := make([]geoKey, n)
	byGeo := map[geoKey][]int{}
	for i := range n {
		k, err := r.readGeoKey(rows.At(i))
		if err != nil {
			return nil, err
		}
		keys[i] = k
		byGeo[k] = append(byGeo[k], i)
	}
	order := make([]geoKey, 0, len(byGeo))
	for k := range byGeo {
		order = append(order, k)
	}
	slices.SortFunc(order, func(a, b geoKey) int {
		if a.isCode != b.isCode {
			// Never mixed on one field; a fixed order regardless.
			if a.isCode {
				return 1
			}
			return -1
		}
		if a.isCode {
			return cmp.Compare(a.code, b.code)
		}
		return cmp.Compare(a.label, b.label)
	})
	l, ok := limits.FromContext(ctx)
	if !ok {
		l = limits.Defaults()
	}
	if cerr := limits.CheckMMMGeos(l, len(order)); cerr != nil {
		return nil, cerr
	}
	gd := &geoData{names: make([]string, len(order)), series: make([]*bayesData, len(order))}
	for g, k := range order {
		gd.names[g] = k.name()
		d, err := r.readSeries(geoRows{rows: rows, idx: byGeo[k]}, gd.names[g])
		if err != nil {
			return nil, err
		}
		gd.series[g] = d
	}
	return gd, nil
}

// geoRows is one geo's rows of a panel.
type geoRows struct {
	rows extend.Rows
	idx  []int
}

func (g geoRows) Len() int               { return len(g.idx) }
func (g geoRows) At(i int) extend.Record { return g.rows.At(g.idx[i]) }

// readSeries is read over one geo's rows: the same per-row rules, then a
// repeat (duplicate_time) or a jump (geo_time_gap) within the geo
// refused with the geo named.
func (r *bayesRegression) readSeries(rows extend.Rows, geo string) (*bayesData, error) {
	d, err := r.readRows(rows)
	if err != nil {
		return nil, err
	}
	cfg := r.cfg
	for i := 1; i < len(d.time); i++ {
		switch step := d.time[i] - d.time[i-1]; {
		case step == 0:
			return nil, r.configError(fmt.Sprintf("params.time %q repeats %d in geo %s: a geo panel has one row per geo and period", cfg.time, d.time[i], geo),
				map[string]any{"slot": "params.time", "field": cfg.time, "reason": "duplicate_time", "time": d.time[i], "geo": geo})
		case step != 1:
			return nil, r.configError(fmt.Sprintf("params.time %q jumps from %d to %d in geo %s: each geo's periods must be contiguous between its first and last (a ragged start or end is fine; a hole inside is not, the adstock would carry across it)", cfg.time, d.time[i-1], d.time[i], geo),
				map[string]any{"slot": "params.time", "field": cfg.time, "reason": reasonGeoTimeGap, "from": d.time[i-1], "to": d.time[i], "geo": geo})
		}
	}
	return d, nil
}

// geoFitRows is each geo's count of leading (fitted) rows: every row, or
// with params.holdout_from those before it. A cutoff that leaves some geo
// no fitted row, or no geo a held-out row, is PROCESSING_CONFIG
// (holdout_out_of_range; first is the latest geo start, last the latest
// geo end).
func (r *bayesRegression) geoFitRows(gd *geoData) ([]int, error) {
	nf := make([]int, len(gd.series))
	from := r.cfg.holdoutFrom
	first, last := gd.series[0].time[0], gd.series[0].time[len(gd.series[0].time)-1]
	for g, d := range gd.series {
		nf[g] = len(d.time)
		first, last = max(first, d.time[0]), max(last, d.time[len(d.time)-1])
	}
	if from == nil {
		return nf, nil
	}
	if *from <= first || *from > last {
		return nil, r.configError(fmt.Sprintf("params.holdout_from %d is outside the panel: it must leave a fitted row in every geo and a held-out row, in (%d, %d]", *from, first, last),
			map[string]any{"slot": "params.holdout_from", "reason": "holdout_out_of_range", "holdout_from": *from, "first": first, "last": last})
	}
	for g, d := range gd.series {
		nf[g] = min(int(*from-d.time[0]), len(d.time))
	}
	return nf, nil
}

// fitGeo is FitContext on a geo panel.
func (r *bayesRegression) fitGeo(ctx context.Context, rows extend.Rows) (extend.RegressionOutput, error) {
	cfg := r.cfg
	gd, err := r.readGeo(ctx, rows)
	if err != nil {
		return extend.RegressionOutput{}, err
	}
	nf, err := r.geoFitRows(gd)
	if err != nil {
		return extend.RegressionOutput{}, err
	}
	in := geoPanel{geos: gd.names, channels: cfg.channels, perGeoCurves: cfg.perGeoCurves}
	for g, d := range gd.series {
		k := nf[g]
		in.y = append(in.y, d.y[:k])
		sp, ct := make([][]float64, len(d.spend)), make([][]float64, len(d.controls))
		for c, x := range d.spend {
			sp[c] = x[:k]
		}
		for j, z := range d.controls {
			ct[j] = z[:k]
		}
		in.spend = append(in.spend, sp)
		in.controls = append(in.controls, ct)
	}
	pr := cfg.priors
	pr.Kernel, pr.Curve = cfg.kernel, cfg.curve
	m, err := newBayesGeoModel(in, cfg.maxLag, pr)
	if err != nil {
		return extend.RegressionOutput{}, r.slotError(err)
	}
	if err := r.addGeoLiftTests(m, gd, nf); err != nil {
		return extend.RegressionOutput{}, err
	}
	if err := ctx.Err(); err != nil {
		return extend.RegressionOutput{}, err
	}
	post, err := r.sampleGeo(ctx, m, gd, nf)
	if err != nil {
		return extend.RegressionOutput{}, err
	}
	return r.output(post), nil
}

// addGeoLiftTests resolves params.lift_tests onto the panel: each test's
// geo must be one of the panel's (lift_geo_unknown, details.geos the
// panel's), its window inside that geo's fitted periods
// (lift_window_out_of_range, details.geo), its change at or above zero
// spend (lift_spend_negative) — PROCESSING_CONFIG, slot
// params.lift_tests, before any sampling.
func (r *bayesRegression) addGeoLiftTests(m *bayesGeoModel, gd *geoData, nf []int) error {
	for i, l := range r.cfg.lifts {
		g := slices.Index(gd.names, l.geo)
		if g < 0 {
			return r.slotError(liftConfigError(i, reasonLiftGeoUnknown,
				fmt.Sprintf("geo %q is not one of the panel's geos (params.geo %q: %s)", l.geo, r.cfg.geo, strings.Join(gd.names, ", ")),
				map[string]any{"geo": l.geo, "geos": slices.Clone(gd.names)}))
		}
		d := gd.series[g]
		first, last := d.time[0], d.time[nf[g]-1]
		if l.start < first || l.end > last {
			return r.slotError(liftConfigError(i, reasonLiftWindowRange,
				fmt.Sprintf("the window [%d, %d] is outside geo %s's fitted periods [%d, %d]", l.start, l.end, l.geo, first, last),
				map[string]any{"start": l.start, "end": l.end, "first": first, "last": last, "geo": l.geo}))
		}
		lt, err := m.newGeoLiftTest(g, l.channel, int(l.start-first), int(l.end-first), l.spendDelta, l.lift, l.se)
		if err != nil {
			return r.liftResolveError(i, err, map[string]any{"channel": r.cfg.channels[l.channel], "geo": l.geo})
		}
		m.lifts = append(m.lifts, lt)
	}
	return nil
}

// sampleGeo runs MAP → NUTS on the panel's model and maps every draw onto
// the panel's coefficient table, its ROI entries (per channel the
// all-geo figure, then each geo's) and, with a holdout, the held-out
// rows' mean (geo order, then time order).
func (r *bayesRegression) sampleGeo(ctx context.Context, m *bayesGeoModel, gd *geoData, nf []int) (*bayesPosterior, error) {
	cfg := r.cfg
	init, mapOK := m.mapStart(mapMaxIter)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	res, err := bayesSampler(cfg.targetAccept).Sample(ctx, m, uint64(cfg.seed),
		mcmc.Options{Chains: cfg.chains, Warmup: cfg.warmup, Draws: cfg.draws, Init: init})
	if err != nil {
		return nil, r.samplerError(err)
	}
	names := r.geoCoefNames(gd.names)
	effects := r.geoEffectNames(gd.names)
	nch, dim, nd := len(res.Draws), m.Dim(), len(res.Draws[0][0])
	ncoef, nent, nmu := len(names), len(effects), m.nk*m.shape.nc
	post := &bayesPosterior{theta: res, mapOK: mapOK, names: names, effectNames: effects,
		coefs: make([][][]float64, nch), roi: make([][][]float64, nch),
		mroi: make([][][]float64, nch), contrib: make([][][]float64, nch)}
	held := 0
	for g, d := range gd.series {
		held += len(d.time) - nf[g]
	}
	if held > 0 {
		fit := 0
		var y []float64
		for g, d := range gd.series {
			fit += nf[g]
			y = append(y, d.y[nf[g]:]...)
		}
		post.hold = &bayesHoldout{from: *cfg.holdoutFrom, fitRows: fit, y: y, mu: make([][]float64, held),
			sigmaIdx: slices.Index(names, sigmaName)}
		for t := range post.hold.mu {
			post.hold.mu[t] = make([]float64, nch*nd)
		}
	}
	block := func(p int) [][]float64 {
		out := make([][]float64, p)
		for j := range out {
			out[j] = make([]float64, nd)
		}
		return out
	}
	mus := make([][][]float64, nch)
	theta := make([]float64, dim)
	for ch, draws := range res.Draws {
		post.coefs[ch], mus[ch] = block(ncoef), block(nmu)
		post.roi[ch], post.mroi[ch], post.contrib[ch] = block(nent), block(nent), block(nent)
		for d := range nd {
			for j := range dim {
				theta[j] = draws[j][d]
			}
			gdraw := m.draw(theta)
			for j, v := range gdraw.row {
				post.coefs[ch][j][d] = v
			}
			for e := range nent {
				post.roi[ch][e][d], post.mroi[ch][e][d], post.contrib[ch][e][d] = gdraw.roi[e], gdraw.mroi[e], gdraw.contrib[e]
			}
			for k := range m.nk {
				for c := range m.shape.nc {
					mus[ch][k*m.shape.nc+c][d] = gdraw.mu[k][c]
				}
			}
			if post.hold != nil {
				row := 0
				for g, sd := range gd.series {
					if nf[g] == len(sd.time) {
						continue
					}
					for _, v := range m.predictGeo(theta, g, sd.spend, sd.controls, nf[g]) {
						post.hold.mu[row][ch*nd+d] = v
						row++
					}
				}
			}
		}
	}
	post.extra = map[string]any{"hierarchy": r.hierarchy(m, gd, post, mus)}
	return post, nil
}

// hierarchy is outputs.hierarchy: the geo field, the geos, and per pooled
// class (keyed by its population term's name) the parameter it pools
// (roi, beta, the kernel parameter's key or, with per_geo_curves, the
// curve parameter's) with the posterior of its population value μ (the
// effect in its sampled units: ROI, or β on the raw outcome; a curve
// parameter in original units) and of its scale τ (on the parameter's
// unconstrained scale), each a summary at credible_level.
func (r *bayesRegression) hierarchy(m *bayesGeoModel, gd *geoData, post *bayesPosterior, mus [][][]float64) map[string]any {
	cfg := r.cfg
	nc := len(cfg.channels)
	pooledOut := map[string]any{}
	for k := range m.nk {
		for c, ch := range cfg.channels {
			term, param := ch, "roi"
			if k == 0 && m.shape.isBeta[c] {
				param = "beta"
			}
			if np := len(cfg.kernel.Params()); k > np {
				cq := cfg.curve.Params()[k-1-np]
				term, param = cq.prefix+ch, cq.key
			} else if k > 0 {
				kp := cfg.kernel.Params()[k-1]
				term, param = kp.prefix+ch, kp.key
			}
			tau := slices.Index(post.names, TauPrefix+term)
			pooledOut[term] = map[string]any{
				"parameter": param,
				"mu":        summarize(pool(paramChains(mus, k*nc+c)), cfg.credibleLevel),
				"tau":       summarize(pool(paramChains(post.coefs, tau)), cfg.credibleLevel),
			}
		}
	}
	return map[string]any{"geo": cfg.geo, "geos": slices.Clone(gd.names), "pooled": pooledOut}
}

// geoCoefNames is the panel's coefficient table (the file comment's
// order).
func (r *bayesRegression) geoCoefNames(geos []string) []string {
	cfg := r.cfg
	var names []string
	for _, g := range geos {
		names = append(names, pmodel.MMMGeoTerm(interceptName, g))
	}
	perGeo := func(term string) {
		names = append(names, term)
		for _, g := range geos {
			names = append(names, pmodel.MMMGeoTerm(term, g))
		}
	}
	for _, c := range cfg.channels {
		perGeo(c)
	}
	names = append(names, cfg.controls...)
	for _, kp := range cfg.kernel.Params() {
		for _, c := range cfg.channels {
			perGeo(kp.prefix + c)
		}
	}
	for _, cq := range cfg.curve.Params() {
		for _, c := range cfg.channels {
			if cfg.perGeoCurves {
				perGeo(cq.prefix + c)
			} else {
				names = append(names, cq.prefix+c)
			}
		}
	}
	names = append(names, sigmaName)
	for _, c := range cfg.channels {
		names = append(names, TauPrefix+c)
	}
	for _, kp := range cfg.kernel.Params() {
		for _, c := range cfg.channels {
			names = append(names, TauPrefix+kp.prefix+c)
		}
	}
	if cfg.perGeoCurves {
		for _, cq := range cfg.curve.Params() {
			for _, c := range cfg.channels {
				names = append(names, TauPrefix+cq.prefix+c)
			}
		}
	}
	return names
}

// geoEffectNames is the panel's ROI entries: per channel its name (the
// all-geo figure) then each geo's term.
func (r *bayesRegression) geoEffectNames(geos []string) []string {
	var out []string
	for _, c := range r.cfg.channels {
		out = append(out, c)
		for _, g := range geos {
			out = append(out, pmodel.MMMGeoTerm(c, g))
		}
	}
	return out
}
