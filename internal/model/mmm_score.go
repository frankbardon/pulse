package model

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"sync"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/paramread"
	"github.com/frankbardon/pulse/types"
)

// mmm_score.go is the static face of scoring a REG_MMM_BAYES fit — the
// rules predict and the runtime share. Its scorer (internal/regmmm)
// averages α + Σγ·z + Σβ·curve(x) over the fit's posterior draws, so a
// fit is scorable only when it carries them (outputs.draws, from
// params.return_draws): a save, an engine holdout or a scoring
// reference over a draws-less fit is refused with reason
// draws_required. A geo panel's score reads the row's geo
// (Signature.ScoreFields); a geo the fit has no parameters for scores
// from the population (μ) and is counted (GeoTally), raising one
// PULSE_MMM_GEO_UNSEEN warning per slot.

// ReasonDrawsRequired refuses saving, holding out or scoring a
// REG_MMM_BAYES fit that does not return its draws.
const ReasonDrawsRequired = "draws_required"

// MMMDrawsKey is REG_MMM_BAYES's outputs key holding its draws.
const MMMDrawsKey = "draws"

// MMMAdstockFixedAtPosteriorMean is a REG_MMM_BAYES adstock record's
// fixed_at: the kernel is fixed at the posterior means of its
// parameters, the decay a score's pre-adstocked inputs were built with.
const MMMAdstockFixedAtPosteriorMean = "posterior_mean"

// MMMBayesReturnsDraws reports whether a REG_MMM_BAYES spec sets
// params.return_draws true (false for any other spec, or params that do
// not decode).
func MMMBayesReturnsDraws(spec *types.RegressionSpec) bool {
	if spec == nil || spec.Type != types.REG_MMM_BAYES || len(spec.Params) == 0 {
		return false
	}
	var p struct {
		ReturnDraws *bool `json:"return_draws"`
	}
	return paramread.Probe(spec.Params, &p) == nil && p.ReturnDraws != nil && *p.ReturnDraws
}

// MMMBayesGeoField is a REG_MMM_BAYES spec's params.geo ("" for none,
// any other type, or params that do not decode).
func MMMBayesGeoField(spec *types.RegressionSpec) string {
	if spec == nil || spec.Type != types.REG_MMM_BAYES || len(spec.Params) == 0 {
		return ""
	}
	var p struct {
		Geo string `json:"geo"`
	}
	if paramread.Probe(spec.Params, &p) != nil {
		return ""
	}
	return p.Geo
}

// mmmOutputsGeoField is a geo panel's field as its outputs.hierarchy
// names it ([geo]), nil without one.
func mmmOutputsGeoField(outs map[string]any) []string {
	h, ok := outs["hierarchy"].(map[string]any)
	if !ok {
		return nil
	}
	if g, ok := h["geo"].(string); ok && g != "" {
		return []string{g}
	}
	return nil
}

// fileMMMScoreFace is a loaded REG_MMM_BAYES file's DrawsMissing and
// ScoreFields (CatalogWithModels), read off its outputs exactly as
// Bound.Signature reads the bound result's.
func fileMMMScoreFace(f *File) (drawsMissing bool, fields []string) {
	if f.Type != types.REG_MMM_BAYES {
		return false, nil
	}
	_, has := f.Outputs[MMMDrawsKey]
	var h struct {
		Geo string `json:"geo"`
	}
	if raw, ok := f.Outputs["hierarchy"]; ok && json.Unmarshal(raw, &h) == nil && h.Geo != "" {
		fields = []string{h.Geo}
	}
	return !has, fields
}

// mmmDrawsRefusal is the PULSE_MODEL_SCORE_INVALID refusal (reason
// draws_required) of a scoring slot of type attr over a REG_MMM_BAYES
// fit without draws; nil otherwise.
func mmmDrawsRefusal(attr types.AttributeType, fit string, sig Signature) *errors.CodedError {
	if sig.Type != types.REG_MMM_BAYES || !sig.DrawsMissing {
		return nil
	}
	return scoreInvalidFor(attr, fit, ReasonDrawsRequired,
		fmt.Sprintf("cannot score %q: a %s fit scores as the mean over its posterior draws, and this fit carries none; fit it with params.return_draws true", fit, sig.Type),
		map[string]any{"type": string(sig.Type)})
}

// MMMBayesDrawsRefusal is the PROCESSING_CONFIG refusal (reason
// draws_required, details {regression, slot[, name]}) of a REG_MMM_BAYES
// spec that is saved or held out (slot "save" / "holdout") without
// params.return_draws true: its saved file and its holdout score read
// the draws. Nil for any other spec or when the draws are returned.
func MMMBayesDrawsRefusal(spec *types.RegressionSpec, slot string) *errors.CodedError {
	if spec == nil || spec.Type != types.REG_MMM_BAYES || MMMBayesReturnsDraws(spec) {
		return nil
	}
	details := map[string]any{"regression": string(spec.Type), "slot": slot, "reason": ReasonDrawsRequired}
	if spec.Name != "" {
		details["name"] = spec.Name
	}
	return errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
		"regression "+string(spec.Type)+" "+slot+" needs the posterior draws (a saved or held-out fit scores as the mean over them); set params.return_draws true",
		details)
}

// GeoTally counts the rows a geo-panel scorer scored from the population
// because their geo is none the fit holds. It is safe for concurrent
// use; partitions fold with Merge (integers, exact).
type GeoTally struct {
	mu    sync.Mutex
	field string
	fit   []string
	rows  map[string]int64
}

// NewGeoTally is an empty tally over field, the fit's geos fitGeos.
func NewGeoTally(field string, fitGeos []string) *GeoTally {
	return &GeoTally{field: field, fit: slices.Clone(fitGeos)}
}

// Add counts one row of geo.
func (t *GeoTally) Add(geo string) {
	t.mu.Lock()
	if t.rows == nil {
		t.rows = map[string]int64{}
	}
	t.rows[geo]++
	t.mu.Unlock()
}

// Merge folds o's counts in.
func (t *GeoTally) Merge(o *GeoTally) {
	if t == nil || o == nil || t == o {
		return
	}
	o.mu.Lock()
	rows := maps.Clone(o.rows)
	o.mu.Unlock()
	t.mu.Lock()
	for g, n := range rows {
		if t.rows == nil {
			t.rows = map[string]int64{}
		}
		t.rows[g] += n
	}
	t.mu.Unlock()
}

// Reset clears the counts.
func (t *GeoTally) Reset() {
	t.mu.Lock()
	t.rows = nil
	t.mu.Unlock()
}

// Warning is the PULSE_MMM_GEO_UNSEEN warning of the counted rows, nil
// when none was counted. who names the scoring site (an attribute's
// label, a regression's holdout); details is merged into the warning's
// details beside field, geos (sorted), rows (aligned) and fit_geos.
func (t *GeoTally) Warning(who string, details map[string]any) *types.ResponseWarning {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	rows := maps.Clone(t.rows)
	t.mu.Unlock()
	if len(rows) == 0 {
		return nil
	}
	geos := slices.Sorted(maps.Keys(rows))
	counts := make([]int64, len(geos))
	var total int64
	for i, g := range geos {
		counts[i] = rows[g]
		total += rows[g]
	}
	d := map[string]any{"field": t.field, "geos": geos, "rows": counts, "fit_geos": slices.Clone(t.fit)}
	maps.Copy(d, details)
	return &types.ResponseWarning{
		Code: string(errors.PULSE_MMM_GEO_UNSEEN),
		Message: who + ": " + strconv.FormatInt(total, 10) + " row(s) hold a value of geo field " + strconv.Quote(t.field) +
			" the fit has no parameters for; they score from the population (μ) parameters and the mean of the fit's geo intercepts",
		Details: d,
	}
}

// GeoTallier is implemented by a RowScorer that keeps a GeoTally (a
// REG_MMM_BAYES geo panel's); GeoTally is nil for one that keeps none.
type GeoTallier interface {
	GeoTally() *GeoTally
}

// ScorerGeoTally is s's GeoTally, nil when it keeps none.
func ScorerGeoTally(s RowScorer) *GeoTally {
	if t, ok := s.(GeoTallier); ok {
		return t.GeoTally()
	}
	return nil
}
