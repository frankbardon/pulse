package regmmm

import (
	"context"
	"encoding/json"
	"fmt"
	"math"

	"github.com/frankbardon/pulse/internal/limits"
	"github.com/frankbardon/pulse/types"
)

// bayes_draws.go is REG_MMM_BAYES's raw posterior draws on the wire:
// params.return_draws adds outputs.draws, every params.draws_thin-th
// retained draw of each chain (draw 0, k, 2k, …), as
//
//	{names, draws[chain][coefficient][kept draw], roi{<entry>: [chain][kept draw]}, thin}
//
// names is the coefficient table's (the order of draws' middle index),
// every value on the constrained scale in original units — the draws
// outputs.params summarises; roi is each outputs.roi entry's draws (per
// channel, and on a geo panel each <channel>[<geo>]), so with draws_thin
// 1 a draw set summarised reproduces the reported summary bit for bit.
// A non-finite draw is null on the wire (types.MarshalFinite).
//
// The payload is chains × kept draws × (coefficients + roi entries)
// values, capped by Limits.MaxMMMDrawValues: over it the fit is refused
// (PULSE_LIMIT_EXCEEDED) before any sampling — never truncated. On one
// market the count is the spec's alone, so predict reports it Certain
// (BayesDrawValues) and the pre-flight refuses; on a geo panel it grows
// with the geos, so predict's figure is Possible (the geo field's
// dictionary) and the fit judges the geos it read.
//
// Neither key is part of the derived seed (withoutOutputOnlyParams):
// asking for the draws never moves them.

// reasonDrawsThinWithoutDraws refuses params.draws_thin without
// params.return_draws (it would thin nothing).
const reasonDrawsThinWithoutDraws = "draws_thin_without_return_draws"

// outputOnlyParams are the params keys that shape the output only and so
// never enter the derived seed.
var outputOnlyParams = []string{"return_draws", "draws_thin"}

// drawsConfig validates params.return_draws and params.draws_thin onto
// cfg: draws_thin an integer in [1, maxBayesDraws], and only beside
// return_draws true.
func drawsConfig(spec *types.RegressionSpec, w *bayesParamsWire, cfg *bayesConfig) error {
	if w.ReturnDraws != nil && *w.ReturnDraws {
		cfg.returnDraws, cfg.drawsThin = true, 1
	}
	if w.DrawsThin == nil {
		return nil
	}
	if k := *w.DrawsThin; k < 1 || k > maxBayesDraws {
		return bayesSlotError(spec, "params.draws_thin", fmt.Sprintf("params.draws_thin must be an integer in [1, %d], got %d", maxBayesDraws, k),
			map[string]any{"min": 1, "max": maxBayesDraws, "got": k})
	}
	if !cfg.returnDraws {
		return bayesSlotError(spec, "params.draws_thin", "params.draws_thin thins the draws params.return_draws returns; set return_draws true, or drop draws_thin",
			map[string]any{"reason": reasonDrawsThinWithoutDraws})
	}
	cfg.drawsThin = *w.DrawsThin
	return nil
}

// withoutOutputOnlyParams is raw with the output-only keys removed, or
// raw itself when it carries none (so a spec without them hashes as it
// always has).
func withoutOutputOnlyParams(raw json.RawMessage) json.RawMessage {
	var obj map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &obj) != nil {
		return raw
	}
	found := false
	for _, k := range outputOnlyParams {
		if _, ok := obj[k]; ok {
			delete(obj, k)
			found = true
		}
	}
	if !found {
		return raw
	}
	out, err := json.Marshal(obj)
	if err != nil {
		return raw
	}
	return out
}

// mulSat is a·b, saturating at math.MaxInt64 (both ≥ 0).
func mulSat(a, b int64) int64 {
	if a == 0 || b == 0 {
		return 0
	}
	if a > math.MaxInt64/b {
		return math.MaxInt64
	}
	return a * b
}

// keptDraws is the draws each chain returns: ⌈draws / thin⌉.
func (cfg *bayesConfig) keptDraws() int {
	return (cfg.draws + cfg.drawsThin - 1) / cfg.drawsThin
}

// drawEntries is the coefficient-table length and the roi-entry count of
// a fit over geos geos (0 on one market): coefNames / geoCoefNames and
// the channels / geoEffectNames, counted without building them.
func (cfg *bayesConfig) drawEntries(geos int64) (coefs, roi int64) {
	nc, nz := int64(len(cfg.channels)), int64(len(cfg.controls))
	np, nq := int64(len(cfg.kernel.Params())), int64(len(cfg.curve.Params()))
	if cfg.geo == "" {
		return 2 + nz + nc*(1+np+nq), nc
	}
	g := geos
	perGeo := 1 + g // a pooled term: its population value and each geo's
	curve := nq * nc
	tauCurve := int64(0)
	if cfg.perGeoCurves {
		curve, tauCurve = mulSat(curve, perGeo), nq*nc
	}
	coefs = g + mulSat(nc, perGeo) + nz + mulSat(np*nc, perGeo) + curve + 1 + nc + np*nc + tauCurve
	return coefs, mulSat(nc, perGeo)
}

// drawValues is the draws payload's value count over geos geos (0 when
// draws are not returned).
func (cfg *bayesConfig) drawValues(geos int64) int64 {
	if !cfg.returnDraws {
		return 0
	}
	coefs, roi := cfg.drawEntries(geos)
	return mulSat(int64(cfg.chains)*int64(cfg.keptDraws()), coefs+roi)
}

// checkDrawValues refuses a fit whose draws payload over geos geos
// passes Limits.MaxMMMDrawValues (from ctx; the default when ctx carries
// none). Nil when draws are not returned.
func (r *bayesRegression) checkDrawValues(ctx context.Context, geos int64) error {
	if !r.cfg.returnDraws {
		return nil
	}
	l, ok := limits.FromContext(ctx)
	if !ok {
		l = limits.Defaults()
	}
	if cerr := limits.CheckMMMDrawValues(l, r.cfg.drawValues(geos)); cerr != nil {
		return cerr
	}
	return nil
}

// BayesDrawValues is the draws payload a REG_MMM_BAYES spec returns over
// geos geos (ignored on one market): its value count, whether the spec
// fits a geo panel (the count then depends on the geos the fit reads),
// and false when the spec does not return draws or is refused (predict
// reports the refusal instead). It reads the spec alone.
func BayesDrawValues(spec *types.RegressionSpec, geos int64) (values int64, geo, ok bool) {
	if spec == nil || spec.Type != BayesName {
		return 0, false, false
	}
	reg, err := BayesFactory(spec, nil)
	if err != nil {
		return 0, false, false
	}
	cfg := reg.(*bayesRegression).cfg
	if !cfg.returnDraws {
		return 0, false, false
	}
	return cfg.drawValues(geos), cfg.geo != "", true
}

// drawsOutput is outputs.draws: the coefficient table's names, every
// kept draw per chain and coefficient, each roi entry's kept draws per
// chain, and the thinning step.
func (r *bayesRegression) drawsOutput(post *bayesPosterior, names, effectNames []string) map[string]any {
	k := r.cfg.drawsThin
	thin := func(d []float64) []float64 {
		if k == 1 {
			return d
		}
		out := make([]float64, 0, (len(d)+k-1)/k)
		for i := 0; i < len(d); i += k {
			out = append(out, d[i])
		}
		return out
	}
	draws := make([][][]float64, len(post.coefs))
	for ch, coefs := range post.coefs {
		draws[ch] = make([][]float64, len(coefs))
		for j, d := range coefs {
			draws[ch][j] = thin(d)
		}
	}
	roi := make(map[string]any, len(effectNames))
	for e, name := range effectNames {
		per := make([][]float64, len(post.roi))
		for ch := range post.roi {
			per[ch] = thin(post.roi[ch][e])
		}
		roi[name] = per
	}
	return map[string]any{"names": append([]string(nil), names...), "draws": draws, "roi": roi, "thin": k}
}
