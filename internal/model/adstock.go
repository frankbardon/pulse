package model

import (
	"cmp"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"slices"
	"strings"

	"github.com/frankbardon/pulse/internal/distlagparams"
	"github.com/frankbardon/pulse/internal/paramread"
	"github.com/frankbardon/pulse/internal/windowparams"
	"github.com/frankbardon/pulse/types"
)

// adstock.go is a media-mix fit's adstock lineage: per channel, the lag
// kernel its spend went through before the fit read it — the record
// response() reads its steady-state multiplier from, so it never
// guesses a decay or a max_lag. Three sources, never an inference:
//
//   - REG_MMM_HILL: written at fit time when channel <ch> is the output
//     column of exactly one WIN_DISTRIBUTED_LAG window of the fit's
//     request, with that window's RESOLVED params (a decay written as
//     sweep_axis() or params.d records the number it read);
//   - REG_MMM_BAYES: geometric, the decay:<ch> posterior mean, the fit's
//     params.max_lag (default MMMBayesDefaultMaxLag), unnormalised — the
//     kernel the model adstocks raw spend with;
//   - an inline model: declared in its `adstock` section.
//
// A channel built any other way (WIN_LAG columns and a formula, a
// feature, a column the cohort already holds) has no record.

// MMMBayesDefaultMaxLag is REG_MMM_BAYES's adstock window when
// params.max_lag is absent (internal/regmmm reads it as its default).
const MMMBayesDefaultMaxLag = 13

// Adstock is one channel's adstock record: a WIN_DISTRIBUTED_LAG kernel
// (internal/distlagparams) with its resolved params. Decay is set for
// the geometric kernel, Shape and Scale for the Weibull kernels. Its
// JSON form is the model file's `adstock.<channel>` entry, e.g.
// {"kernel": "geometric", "decay": 0.6, "max_lag": 8, "normalize": true}.
type Adstock struct {
	// Kernel is geometric, weibull_cdf or weibull_pdf.
	Kernel string `json:"kernel"`
	// Decay is the geometric kernel's decay in [0, 1).
	Decay *float64 `json:"decay,omitempty"`
	// Shape and Scale are a Weibull kernel's params (each > 0).
	Shape *float64 `json:"shape,omitempty"`
	Scale *float64 `json:"scale,omitempty"`
	// MaxLag is the last lag the kernel weighs (lags 0..MaxLag).
	MaxLag int `json:"max_lag"`
	// Normalize reports whether the weights were divided by their sum
	// (a REG_MMM_BAYES record is never normalised).
	Normalize bool `json:"normalize"`
}

// Params is the record as WIN_DISTRIBUTED_LAG params — what
// distlagparams.Resolve reads.
func (a Adstock) Params() json.RawMessage {
	b, _ := json.Marshal(a)
	return b
}

// Weights are the kernel's weights for lags 0..MaxLag
// (distlagparams.Resolve: the window's own, FMA-free reading), or the
// rule the record breaks.
func (a Adstock) Weights() ([]float64, error) {
	spec, ref := distlagparams.Resolve(a.Params())
	if ref != nil {
		return nil, ref
	}
	return spec.Weights, nil
}

// clone is a deep copy of a.
func (a Adstock) clone() Adstock {
	cp := func(p *float64) *float64 {
		if p == nil {
			return nil
		}
		v := *p
		return &v
	}
	a.Decay, a.Shape, a.Scale = cp(a.Decay), cp(a.Shape), cp(a.Scale)
	return a
}

// cloneAdstocks is a deep copy of m; nil for none.
func cloneAdstocks(m map[string]Adstock) map[string]Adstock {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]Adstock, len(m))
	for k, v := range m {
		out[k] = v.clone()
	}
	return out
}

// Adstock returns channel's adstock record, and whether the fit has
// one. Nil-receiver-safe.
func (b *Bound) Adstock(channel string) (Adstock, bool) {
	if b == nil {
		return Adstock{}, false
	}
	a, ok := b.Adstocks[channel]
	if !ok {
		return Adstock{}, false
	}
	return a.clone(), true
}

// AdstockLineage is the adstock record of regression spec's finished
// fit res, per channel (nil for none): REG_MMM_HILL's from req's
// windows, REG_MMM_BAYES's from its posterior (see the file comment).
// req is the request the fit ran in, its windows' scalar params ALREADY
// resolved to numbers (a window still holding an expression yields no
// record). A failed fit, any other type, or params that do not decode
// give nil.
func AdstockLineage(req *types.Request, spec *types.RegressionSpec, res *types.RegressionResult) map[string]Adstock {
	if res == nil || res.Error != nil {
		return nil
	}
	channels, maxLag := lineageChannels(spec)
	out := map[string]Adstock{}
	for _, ch := range channels {
		var a Adstock
		var ok bool
		switch spec.Type {
		case types.REG_MMM_HILL:
			a, ok = windowAdstock(req, ch)
		case types.REG_MMM_BAYES:
			a, ok = bayesAdstock(res, ch, maxLag)
		}
		if ok {
			out[ch] = a
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// StaticAdstockChannels are the channels (sorted, non-nil) whose
// adstock record a fit of spec run in req will carry, judged from the
// request alone — AdstockLineage's static face, which predict's
// FitCatalog (Signature.Adstocks) and a failed fit's signature read.
// The same channel rule and the same window structure decide; what the
// request cannot show is assumed whole: a window's params that are
// still scalar expressions (they resolve when the run does, and an
// invalid value refuses the window, so the fit fails), and a
// REG_MMM_BAYES channel's decay posterior.
func StaticAdstockChannels(req *types.Request, spec *types.RegressionSpec) []string {
	out := []string{}
	channels, _ := lineageChannels(spec)
	for _, ch := range channels {
		ok := false
		switch spec.Type {
		case types.REG_MMM_HILL:
			ok = staticWindowAdstock(req, ch)
		case types.REG_MMM_BAYES:
			ok = true
		}
		if ok {
			out = append(out, ch)
		}
	}
	slices.Sort(out)
	return out
}

// lineageChannels are spec's media-mix channels that may carry a record
// — its params.channels that are predictors, in params order — and
// params.max_lag; none for any other type or params that do not decode.
func lineageChannels(spec *types.RegressionSpec) ([]string, *int) {
	if spec == nil || (spec.Type != types.REG_MMM_HILL && spec.Type != types.REG_MMM_BAYES) {
		return nil, nil
	}
	var p struct {
		Channels []string `json:"channels"`
		MaxLag   *int     `json:"max_lag"`
	}
	if len(spec.Params) == 0 || paramread.Probe(spec.Params, &p) != nil {
		return nil, nil
	}
	var out []string
	for _, ch := range p.Channels {
		if slices.Contains(spec.Predictors, ch) {
			out = append(out, ch)
		}
	}
	return out, p.MaxLag
}

// windowAdstock is channel ch's record from req's windows: the one
// window adstockWindow finds, its resolved params read as a kernel.
func windowAdstock(req *types.Request, ch string) (Adstock, bool) {
	w := adstockWindow(req, ch)
	if w == nil {
		return Adstock{}, false
	}
	if _, ref := distlagparams.Resolve(w.Params); ref != nil {
		return Adstock{}, false
	}
	var raw distlagparams.Params
	if paramread.Probe(w.Params, &raw) != nil || raw.Kernel == nil || raw.MaxLag == nil {
		return Adstock{}, false
	}
	a := Adstock{Kernel: *raw.Kernel, Decay: raw.Decay, Shape: raw.Shape, Scale: raw.Scale, MaxLag: *raw.MaxLag, Normalize: true}
	if raw.Normalize != nil {
		a.Normalize = *raw.Normalize
	}
	return a, checkAdstock(a) == ""
}

// staticWindowAdstock is windowAdstock judged before the run: the same
// window, its params naming kernel and max_lag; params that read as
// numbers already must read as a kernel, params still holding a scalar
// expression are left to the run.
func staticWindowAdstock(req *types.Request, ch string) bool {
	w := adstockWindow(req, ch)
	if w == nil {
		return false
	}
	var keys map[string]json.RawMessage
	if json.Unmarshal(w.Params, &keys) != nil || !present(keys["kernel"]) || !present(keys["max_lag"]) {
		return false
	}
	var raw distlagparams.Params
	if paramread.Probe(w.Params, &raw) != nil {
		// A param is still an expression: the run resolves it.
		return true
	}
	_, ok := windowAdstock(req, ch)
	return ok
}

// present reports whether a params value is set (not absent, not null).
func present(v json.RawMessage) bool {
	return len(v) > 0 && string(v) != "null"
}

// adstockWindow is the window channel ch's record is read from: ch must
// be the output column of exactly one window, a WIN_DISTRIBUTED_LAG
// whose field no other window writes, and no feature or attribute may
// write ch over it — by its label, a built-in feature's default or
// prefixed output names, an unlabelled attribute's default label
// (featureMayWrite, attributeMayWrite). Nil when there is none.
func adstockWindow(req *types.Request, ch string) *types.Window {
	if req == nil {
		return nil
	}
	var w *types.Window
	labels := map[string]bool{}
	for _, x := range req.Windows {
		if x == nil {
			continue
		}
		l := windowparams.Label(x)
		labels[l] = true
		if l != ch {
			continue
		}
		if w != nil {
			return nil
		}
		w = x
	}
	if w == nil || w.Type != types.WIN_DISTRIBUTED_LAG || labels[w.Field] {
		return nil
	}
	for _, f := range req.Features {
		if f != nil && FeatureMayWrite(f, ch) {
			return nil
		}
	}
	for _, at := range req.Attributes {
		if at != nil && AttributeMayWrite(at, ch) {
			return nil
		}
	}
	return w
}

// singleOutputPrefixes are the built-in single-output features' default
// label prefixes (an unlabelled slot writes prefix + field) — the
// naming internal/vectors.FeatureOutputLabels reads, which
// TestFeatureMayWrite_CoversOutputLabels (internal/vectors) pins this
// guard to.
var singleOutputPrefixes = map[types.FeatureType]string{
	types.FEAT_LOG:              "LOG_",
	types.FEAT_SQRT:             "SQRT_",
	types.FEAT_BUCKETIZE:        "BUCKET_",
	types.FEAT_FREQUENCY_ENCODE: "FREQ_",
	types.FEAT_TARGET_ENCODE:    "TARGET_",
}

// FeatureMayWrite reports whether feature f may write a column named ch,
// judged without the schema (a one-hot's levels and a datetime's hour
// are the schema's): its label; a built-in single-output feature's
// default name (prefix + field, FEAT_TRAIN_TEST_SPLIT's "split"); a
// built-in multi-output feature's any <prefix>_<suffix> output
// (FEAT_DATE_FEATURES / FEAT_ONE_HOT under label or field, FEAT_POLY
// under label or field_poly); any other type's (an extension feature,
// whose outputs are unknown before it runs) label, <label>_ or, when
// unlabelled, <field>_ prefixed outputs. It over-approximates: a guard
// that refuses a record, never one that lets an overwritten column
// keep it.
func FeatureMayWrite(f *types.Feature, ch string) bool {
	if f.Label != "" && f.Label == ch {
		return true
	}
	if p, ok := singleOutputPrefixes[f.Type]; ok {
		return f.Label == "" && ch == p+f.Field
	}
	switch f.Type {
	case types.FEAT_TRAIN_TEST_SPLIT:
		return f.Label == "" && ch == "split"
	case types.FEAT_DATE_FEATURES, types.FEAT_ONE_HOT:
		return strings.HasPrefix(ch, cmp.Or(f.Label, f.Field)+"_")
	case types.FEAT_POLY:
		return strings.HasPrefix(ch, cmp.Or(f.Label, f.Field+"_poly")+"_")
	}
	if f.Label != "" {
		return strings.HasPrefix(ch, f.Label+"_")
	}
	return f.Field != "" && strings.HasPrefix(ch, f.Field+"_")
}

// AttributeMayWrite reports whether attribute a may write a column named
// ch: its label, or — unlabelled — any default label, every one of
// which is the attribute type then "_" (internal/vectors.
// DefaultAttributeLabel; TestAttributeMayWrite_CoversDefaultLabels
// there pins it).
func AttributeMayWrite(a *types.Attribute, ch string) bool {
	if a.Label != "" {
		return a.Label == ch
	}
	return strings.HasPrefix(ch, string(a.Type)+"_")
}

// bayesAdstock is a REG_MMM_BAYES channel's record: geometric at its
// decay:<ch> posterior mean over lags 0..max_lag, unnormalised.
func bayesAdstock(res *types.RegressionResult, ch string, maxLag *int) (Adstock, bool) {
	d, ok := res.Coefficients[MMMDecayPrefix+ch]
	if !ok || math.IsNaN(d) || math.IsInf(d, 0) {
		return Adstock{}, false
	}
	l := MMMBayesDefaultMaxLag
	if maxLag != nil {
		l = *maxLag
	}
	a := Adstock{Kernel: distlagparams.KernelGeometric, Decay: &d, MaxLag: l}
	return a, checkAdstock(a) == ""
}

// checkAdstock is one record's rule ("" when whole): it reads as a
// WIN_DISTRIBUTED_LAG kernel (distlagparams.Resolve — the kernel, the
// params it reads and their ranges, max_lag in [1, 10000], finite
// weights with positive mass).
func checkAdstock(a Adstock) string {
	if _, err := a.Weights(); err != nil {
		return err.Error()
	}
	return ""
}

// adstockRule is the `adstock` section's rule: a media-mix fit's, every
// key a channel — a predictor with a k:<channel> coefficient — and every
// record a valid kernel.
func adstockRule(f *File) string {
	if f.Type != types.REG_MMM_HILL && f.Type != types.REG_MMM_BAYES {
		return fmt.Sprintf("carries an adstock, which only a REG_MMM_HILL or REG_MMM_BAYES fit has, but its type is %s", f.Type)
	}
	if len(f.Adstock) == 0 {
		return "has an empty adstock (a record names at least one channel)"
	}
	for _, ch := range slices.Sorted(maps.Keys(f.Adstock)) {
		if _, ok := f.Coefficients[MMMHalfSaturationPrefix+ch]; !ok || !slices.Contains(f.Predictors, ch) {
			return fmt.Sprintf("has an adstock for %q, which is not a channel (a predictor with a %s%s coefficient)", ch, MMMHalfSaturationPrefix, ch)
		}
		if msg := checkAdstock(f.Adstock[ch]); msg != "" {
			return fmt.Sprintf("has an adstock for %q that is not a valid kernel: %s", ch, msg)
		}
	}
	return ""
}
