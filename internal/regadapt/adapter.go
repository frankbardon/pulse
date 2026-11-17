package regadapt

import (
	"context"
	"fmt"
	"maps"
	"math"
	"slices"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/internal/processing/regression"
	"github.com/frankbardon/pulse/internal/regmargins"
	"github.com/frankbardon/pulse/internal/regresample"
	"github.com/frankbardon/pulse/internal/weighting"
	"github.com/frankbardon/pulse/types"
)

// adapter.go drives a regression authored on the public
// extend contract (extend.Regression) as an engine regression.Factory —
// the one adapter behind both an embedder's extension REG_* model (the
// root package converts its RegressionRegistration) and the built-in
// model regressions (model.ModelRegression), which this package
// registers on the engine's built-in registry at init
// (models.go), so every engine — and every engine test —
// runs them with no root package in the binary. The model returns a
// narrow extend.RegressionOutput; the adapter (RegressionDriver) owns
// admission (listwise on target and predictors, the valid positive
// weight), the engine figures (name, type, n_obs, sum_weights, n_eff)
// and the mapping onto types.RegressionResult, and holds every output
// to the declaration's contract on every run.

// extCategoryRegression is the regression extension category, as the
// mismatch details name it.
const extCategoryRegression = "regression"

// RegressionAdapter is one extend-contract regression as the engine
// drives it: its name, the regression-only declaration predict judges
// the same slot by (descx.ExtensionRegression), the declared form and
// weight awareness, and the extend factory and optional scorer.
type RegressionAdapter struct {
	Name        types.RegressionType
	Decl        descx.ExtensionRegression
	Streamable  bool
	WeightAware bool
	Factory     extend.RegressionFactory
	Scorer      extend.RegressionScorerFactory
	// BoundScorer, when set (a model regression's only —
	// internal/regmmm.BoundScorers), builds the scorer for an
	// ATTR_REG_PREDICT params.bound instead of Scorer: the whole
	// model's lower / upper bound, which model.CheckScoreFor admits for
	// that type alone.
	BoundScorer func(fit extend.FittedRegression, bound string) (extend.RowScorer, error)
	// ParamScorer, when set (a model regression's only —
	// internal/regordinal.ParamScorers), builds the scorer for the whole
	// ATTR_REG_PREDICT reference instead of Scorer: a categorical-outcome
	// fit's params.level / output / scale column, which
	// model.CheckScoreFor admits for that type alone.
	ParamScorer func(fit extend.FittedRegression, p model.ScoreParams) (extend.RowScorer, error)
	// Dispersion, when set (a model regression's only —
	// internal/regmmm.Dispersions), reads the estimated dispersion φ̂
	// the model's Vcov carries (Vcov = φ̂·(JᵀJ)⁻¹), attached as
	// RegressionResult.Dispersion beside the Vcov; ok=false attaches
	// none.
	Dispersion func(spec *types.RegressionSpec, out extend.RegressionOutput) (float64, bool)
	// Pin, when set (a model regression's only —
	// internal/regordinal.ReplicatePins), pins every modifier refit
	// after the slot's first successful one (regressionModifierEngine):
	// the spec later refits build their model from, the mapping of a
	// pinned refit's refusal, and the coefficients that keep no p-value
	// under resample.
	Pin func(spec *types.RegressionSpec, first *types.RegressionResult) (model.ReplicatePin, error)
}

// regProbeDetails marks a registration probe's weighted pass on a
// refusal's details (details.weighted).
func regProbeDetails(details map[string]any, weighted bool) map[string]any {
	if weighted {
		details["weighted"] = true
	}
	return details
}

// ---- admission and mapping ---------------------------------------------

// regWeightedRecord is the view a WeightAware model receives on a
// weighted slot: the row plus its valid, positive weight.
type regWeightedRecord struct {
	extend.Record
	w float64
}

func (r *regWeightedRecord) Weight() (float64, bool) { return r.w, true }

// regRows is the extend.Rows view over a buffered fit's admitted rows.
type regRows []extend.Record

func (r regRows) Len() int               { return len(r) }
func (r regRows) At(i int) extend.Record { return r[i] }

// RegressionDriver drives one model over one slot's rows: it admits
// each row (listwise on the spec's target and predictors; on a weighted
// WeightAware slot, a valid and positive weight), counts the admitted
// rows and their weights, and maps the model's output onto the result.
type RegressionDriver struct {
	reg   RegressionAdapter
	spec  *types.RegressionSpec
	model extend.Regression
	// weight is the slot's resolved weight, nil unweighted (always nil
	// for a registration that does not declare WeightAware).
	weight       *types.WeightSpec
	n            int
	sumW, sumWSq float64
	// view is the reused single-row weighted view of the streaming arm
	// (a Record is valid only for the call that received it).
	view regWeightedRecord
	// figures, when set (regression.OfferingFigures on a fit a Compose
	// comparison names), receives the fit's comparison figures — read
	// off extend.CompareRegression after a successful fit when the
	// declaration carries Compare.
	figures func(*model.RegressionFigures)
}

// SetFiguresOffer hands the driver the comparison-figures offer
// (regression.FiguresOfferer through its engines; pulse.New's probe).
func (d *RegressionDriver) SetFiguresOffer(offer func(*model.RegressionFigures)) { d.figures = offer }

func NewRegressionDriver(reg RegressionAdapter, spec *types.RegressionSpec, model extend.Regression) *RegressionDriver {
	d := &RegressionDriver{reg: reg, spec: spec, model: model}
	if reg.WeightAware {
		d.weight = spec.Weight.Spec()
	}
	return d
}

// rowAdmitter is a model regression's own listwise rule beyond the
// engine's (internal/model.AdmitToken keeps it internal to the module):
// REG_ORDINAL drops a response params.levels does not list and a null
// covariance cluster, so n_obs counts the rows the model fits.
type rowAdmitter interface {
	AdmitRow(rec extend.Record, _ model.AdmitToken) bool
}

// admit judges rec: false for a row missing its target or a predictor
// (null, absent or non-finite — regression.InputValue, the built-in
// listwise read), one the model's own rule refuses (rowAdmitter), or —
// weighted — whose weight is invalid or zero (built-in regression
// admission, regression.rowWeights.of); else it counts the row and
// returns its weight (0 unweighted).
func (d *RegressionDriver) admit(rec extend.Record) (float64, bool) {
	if _, ok := regression.InputValue(rec, d.spec.Target); !ok {
		return 0, false
	}
	for _, p := range d.spec.Predictors {
		if _, ok := regression.InputValue(rec, p); !ok {
			return 0, false
		}
	}
	if a, ok := d.model.(rowAdmitter); ok && !a.AdmitRow(rec, model.AdmitToken{}) {
		return 0, false
	}
	if d.weight == nil {
		d.n++
		return 0, true
	}
	w, present := rec.NumericValue(d.weight.Field)
	if weighting.Classify(w, present, d.weight.EffectiveKind()) != weighting.Valid || w == 0 {
		return 0, false
	}
	d.n++
	d.sumW += w
	d.sumWSq += float64(w * w)
	return w, true
}

// updateRow admits rec and, when admitted, hands it to the streaming
// model (weighted: through the reused view).
func (d *RegressionDriver) updateRow(s extend.StreamingRegression, rec extend.Record) error {
	w, ok := d.admit(rec)
	if !ok {
		return nil
	}
	if d.weight == nil {
		return s.UpdateRow(rec)
	}
	d.view = regWeightedRecord{Record: rec, w: w}
	return s.UpdateRow(&d.view)
}

// fitRows fits the model over recs under ctx and maps the output
// (result).
func (d *RegressionDriver) fitRows(ctx context.Context, recs []extend.Record) (*types.RegressionResult, error) {
	out, err := d.fitOutput(ctx, recs)
	if err != nil {
		return nil, err
	}
	return d.result(out)
}

// fitOutput fits the model over recs: a streaming model (Streamable
// declared) is fed every admitted row through UpdateRow, in order, then
// finalized — the streaming arm's exact fold; a buffered one fits the
// admitted rows at once, through FitContext under ctx when it
// implements extend.ContextRegression, else through Fit.
func (d *RegressionDriver) fitOutput(ctx context.Context, recs []extend.Record) (extend.RegressionOutput, error) {
	if s, ok := d.streaming(); ok {
		for _, rec := range recs {
			if err := d.updateRow(s, rec); err != nil {
				return extend.RegressionOutput{}, err
			}
		}
		return s.Finalize()
	}
	b, ok := d.model.(extend.BufferedRegression)
	if !ok {
		return extend.RegressionOutput{}, regressionFormMismatch(d.reg, "not_buffered", false)
	}
	rows := make(regRows, 0, len(recs))
	var views []regWeightedRecord
	if d.weight != nil {
		views = make([]regWeightedRecord, 0, len(recs)) // never regrows: the rows below point into it
	}
	for _, rec := range recs {
		w, ok := d.admit(rec)
		if !ok {
			continue
		}
		if d.weight == nil {
			rows = append(rows, rec)
			continue
		}
		views = append(views, regWeightedRecord{Record: rec, w: w})
		rows = append(rows, &views[len(views)-1])
	}
	if c, ok := d.model.(extend.ContextRegression); ok {
		return c.FitContext(ctx, rows)
	}
	return b.Fit(rows)
}

// streaming returns the model as a StreamingRegression when the
// registration declares Streamable.
func (d *RegressionDriver) streaming() (extend.StreamingRegression, bool) {
	if !d.reg.Streamable {
		return nil, false
	}
	s, ok := d.model.(extend.StreamingRegression)
	return s, ok
}

func (d *RegressionDriver) finalize(s extend.StreamingRegression) (*types.RegressionResult, error) {
	out, err := s.Finalize()
	if err != nil {
		return nil, err
	}
	return d.result(out)
}

// result checks out against the registration and maps it onto the
// result: the engine's name, type, n_obs and weighted floor; the
// coefficient table with every figure keyed by every coefficient (an
// absent one NaN — null on the wire); the reported fit statistics; the
// engine's confidence intervals at conf_level
// (regression.AttachExtensionIntervals); the vcov when the spec asked
// for it; the declared outputs.
func (d *RegressionDriver) result(out extend.RegressionOutput) (*types.RegressionResult, error) {
	if err := VerifyRegressionOutput(d.reg, d.spec, out, false); err != nil {
		return nil, err
	}
	q := len(out.Coefficients)
	res := &types.RegressionResult{
		Name:         d.spec.Name,
		Type:         d.spec.Type,
		NObs:         d.n,
		Coefficients: make(map[string]float64, q),
		StdErrors:    make(map[string]float64, q),
		Statistics:   make(map[string]float64, q),
		PValues:      make(map[string]float64, q),
	}
	keys := make([]string, q)
	for i, c := range out.Coefficients {
		keys[i] = c.Name
		res.Coefficients[c.Name] = c.Estimate
		res.StdErrors[c.Name] = figureOrNaN(c.StdErr)
		res.Statistics[c.Name] = figureOrNaN(c.Statistic)
		res.PValues[c.Name] = figureOrNaN(c.PValue)
	}
	f := out.Fit
	for _, s := range []struct {
		key string
		dst *float64
		src *float64
	}{
		{"r2", &res.R2, f.R2}, {"adj_r2", &res.AdjR2, f.AdjR2}, {"residual_std_err", &res.ResidualStdErr, f.ResidualStdErr},
		{"df", &res.DF, f.DF}, {"deviance", &res.Deviance, f.Deviance}, {"null_deviance", &res.NullDeviance, f.NullDeviance},
		{"pseudo_r2", &res.PseudoR2, f.PseudoR2}, {"two_log_lik", &res.TwoLogLik, f.TwoLogLik}, {"aic", &res.AIC, f.AIC},
	} {
		if s.src != nil {
			*s.dst = *s.src
			res.ReportedFigures = append(res.ReportedFigures, s.key)
		}
	}
	res.ConvergedIters = f.Iterations
	// The engine's Wald intervals at conf_level on the reported SE (t on
	// a reported df, else normal); a modifier re-attaches on the
	// replicate SE.
	regression.AttachExtensionIntervals(res, d.spec)
	if d.weight != nil {
		res.SumWeights = d.sumW
		if weighting.BasisOf(d.weight) == weighting.Probability {
			res.NEff = weighting.KishNEff(d.sumW, d.sumWSq)
		}
	}
	if d.spec.Vcov && d.reg.Decl.Vcov {
		v := make([][]float64, q)
		for i, row := range out.Vcov {
			v[i] = slices.Clone(row)
		}
		regression.AttachVcovKeys(res, keys, v)
		if d.reg.Dispersion != nil {
			if phi, ok := d.reg.Dispersion(d.spec, out); ok {
				res.Dispersion = phi
			}
		}
	}
	if len(out.Outputs) > 0 {
		res.Outputs = maps.Clone(out.Outputs)
	}
	res.Warnings = cloneRegressionWarnings(out.Warnings)
	if d.spec.Margins != nil && d.reg.Decl.Margins {
		if err := d.attachMargins(res); err != nil {
			return nil, err
		}
	}
	if d.figures != nil && d.reg.Decl.Compare {
		if err := d.offerFigures(); err != nil {
			return nil, err
		}
	}
	return res, nil
}

// offerFigures asks the fitted model for its nested-model comparison
// figures (extend.CompareRegression, declared by the registration's
// Compare), holds them to the spec (VerifyCompareOutput) and offers them
// to the comparison capture: a likelihood with no dispersion (1) and a
// Wald z table (WaldDF 0). A model error fails the fit as a fit error
// does.
func (d *RegressionDriver) offerFigures() error {
	m, ok := d.model.(extend.CompareRegression)
	if !ok {
		return regMismatch(d.reg, "missing_compare",
			"declares Compare but its model does not implement extend.CompareRegression", nil, false)
	}
	out, err := m.CompareFigures()
	if err != nil {
		return err
	}
	if err := VerifyCompareOutput(d.reg, d.spec, out, false); err != nil {
		return err
	}
	cov := make([][]float64, len(out.Covariance))
	for i, row := range out.Covariance {
		cov[i] = slices.Clone(row)
	}
	d.figures(&model.RegressionFigures{
		Terms:        slices.Clone(out.Terms),
		Coefficients: slices.Clone(out.Coefficients),
		Covariance:   cov,
		Deviance:     out.Deviance,
		DFResidual:   out.DFResidual,
		Dispersion:   1,
	})
	return nil
}

// VerifyCompareOutput holds a model's comparison figures to its spec —
// PULSE_EXTENSION_REGRESSION_OUTPUT_MISMATCH, reason compare_shape: no
// term, an empty or repeated term, a predictor Terms does not name,
// Coefficients not aligned with Terms, or a Covariance that is not
// square over Terms. The probe and every compared run apply it.
func VerifyCompareOutput(reg RegressionAdapter, spec *types.RegressionSpec, out extend.CompareOutput, weighted bool) error {
	shape := func(msg string, extra map[string]any) error {
		return regMismatch(reg, "compare_shape", msg, extra, weighted)
	}
	if len(out.Terms) == 0 {
		return shape("returned comparison figures with no term", nil)
	}
	for i, t := range out.Terms {
		if t == "" || slices.Contains(out.Terms[:i], t) {
			return shape(fmt.Sprintf("returned a comparison term %q that is empty or repeated", t), map[string]any{"term": t})
		}
	}
	for _, p := range spec.Predictors {
		if !slices.Contains(out.Terms, p) {
			return shape(fmt.Sprintf("returned comparison terms %v without the predictor %q", out.Terms, p), map[string]any{"predictor": p})
		}
	}
	q := len(out.Terms)
	if len(out.Coefficients) != q {
		return shape(fmt.Sprintf("returned %d comparison coefficients for %d terms", len(out.Coefficients), q),
			map[string]any{"coefficients": len(out.Coefficients), "terms": q})
	}
	if len(out.Covariance) != q {
		return shape(fmt.Sprintf("returned a comparison covariance of %d rows for %d terms", len(out.Covariance), q),
			map[string]any{"rows": len(out.Covariance), "terms": q})
	}
	for i, row := range out.Covariance {
		if len(row) != q {
			return shape(fmt.Sprintf("returned a comparison covariance row %d of %d columns for %d terms", i, len(row), q),
				map[string]any{"row": i, "columns": len(row), "terms": q})
		}
	}
	return nil
}

// attachMargins asks the fitted model for the slot's marginal effects
// (extend.MarginsRegression, declared by the registration's Margins),
// holds them to the request (VerifyMarginsOutput) and writes
// res.Margins with the engine's inference
// (regression.AttachExtensionMargins). A model error fails the slot as a
// fit error does.
func (d *RegressionDriver) attachMargins(res *types.RegressionResult) error {
	m, ok := d.model.(extend.MarginsRegression)
	if !ok {
		return regMismatch(d.reg, "missing_margins",
			"declares Margins but its model does not implement extend.MarginsRegression", nil, false)
	}
	req := extend.MarginsRequest{
		Type:  d.spec.Margins.EffectiveType(),
		Scale: d.spec.Margins.EffectiveScale(),
		Terms: regmargins.Terms(d.spec),
	}
	out, err := m.Margins(req)
	if err != nil {
		return err
	}
	est, se, err := VerifyMarginsOutput(d.reg, d.spec, req, out, false)
	if err != nil {
		return err
	}
	var at map[string]float64
	if req.Type == types.MarginsMEM {
		at = out.At
	}
	regression.AttachExtensionMargins(res, d.spec, out.Outcomes, est, se, at)
	return nil
}

// regMismatch is a PULSE_EXTENSION_REGRESSION_OUTPUT_MISMATCH of reg
// with details {category, name, reason[, extra]}.
func regMismatch(reg RegressionAdapter, reason, msg string, extra map[string]any, weighted bool) error {
	details := map[string]any{"category": extCategoryRegression, "name": string(reg.Name), "reason": reason}
	maps.Copy(details, extra)
	return errors.NewCodedErrorWithDetails(errors.PULSE_EXTENSION_REGRESSION_OUTPUT_MISMATCH,
		fmt.Sprintf("regression %q %s", reg.Name, msg), regProbeDetails(details, weighted))
}

// VerifyMarginsOutput holds a model's marginal effects to the request —
// PULSE_EXTENSION_REGRESSION_OUTPUT_MISMATCH, reason margins_shape: an
// empty or repeated outcome, an effect on a term req does not list or on
// an outcome out.Outcomes does not (an outcome set without Outcomes), a
// repeated (outcome, term) pair, a missing one, or (type mem) an At key
// that is not a predictor. It returns the estimates and standard errors
// keyed regmargins.Key(outcome, term), a nil StdErr NaN. The probe and
// every run apply it.
func VerifyMarginsOutput(reg RegressionAdapter, spec *types.RegressionSpec, req extend.MarginsRequest, out extend.MarginsOutput, weighted bool) (est, se map[string]float64, err error) {
	shape := func(msg string, extra map[string]any) error {
		return regMismatch(reg, "margins_shape", msg, extra, weighted)
	}
	for i, o := range out.Outcomes {
		if o == "" || slices.Contains(out.Outcomes[:i], o) {
			return nil, nil, shape(fmt.Sprintf("returned an outcome %q that is empty or repeated", o), map[string]any{"outcome": o})
		}
	}
	want := len(req.Terms)
	if len(out.Outcomes) > 0 {
		want *= len(out.Outcomes)
	}
	est = make(map[string]float64, want)
	se = make(map[string]float64, want)
	for _, e := range out.Effects {
		if !slices.Contains(req.Terms, e.Term) {
			return nil, nil, shape(fmt.Sprintf("returned an effect on %q, which is not a requested term %v", e.Term, req.Terms),
				map[string]any{"term": e.Term})
		}
		if (len(out.Outcomes) == 0 && e.Outcome != "") || (len(out.Outcomes) > 0 && !slices.Contains(out.Outcomes, e.Outcome)) {
			return nil, nil, shape(fmt.Sprintf("returned an effect on outcome %q, which its Outcomes %v does not list", e.Outcome, out.Outcomes),
				map[string]any{"outcome": e.Outcome, "term": e.Term})
		}
		key := regmargins.Key(e.Outcome, e.Term)
		if _, dup := est[key]; dup {
			return nil, nil, shape(fmt.Sprintf("returned the effect %q twice", key), map[string]any{"effect": key})
		}
		est[key] = e.Estimate
		se[key] = figureOrNaN(e.StdErr)
	}
	if len(est) != want {
		return nil, nil, shape(fmt.Sprintf("returned %d effects, want %d (one per requested term, per outcome when Outcomes is set)", len(est), want),
			map[string]any{"effects": len(est), "want": want})
	}
	if req.Type == types.MarginsMEM {
		for k := range out.At {
			if !slices.Contains(spec.Predictors, k) {
				return nil, nil, shape(fmt.Sprintf("returned At[%q], which is not a predictor", k), map[string]any{"at": k})
			}
		}
	}
	return est, se, nil
}

// cloneRegressionWarnings copies a model's warnings (each entry and its
// details) so the result never aliases the model's state; nil when
// there are none, so a warning-free result is byte-identical.
func cloneRegressionWarnings(ws []*types.ResponseWarning) []*types.ResponseWarning {
	if len(ws) == 0 {
		return nil
	}
	out := make([]*types.ResponseWarning, len(ws))
	for i, w := range ws {
		c := *w
		c.Details = maps.Clone(w.Details)
		out[i] = &c
	}
	return out
}

func figureOrNaN(v *float64) float64 {
	if v == nil {
		return math.NaN()
	}
	return *v
}

// VerifyRegressionOutput holds a model's output to its registration —
// PULSE_EXTENSION_REGRESSION_OUTPUT_MISMATCH, details.reason:
// missing_coefficients (none), invalid_coefficient (an empty or
// repeated name), undeclared_p_value (a finite p-value from a model
// not declaring PValues), undeclared_output_key (an Outputs key OutputSchema
// does not declare), and — when the spec sets vcov and the
// registration declares Vcov — missing_vcov / vcov_shape (not square
// over the coefficients); invalid_warning (a nil Warnings entry or one
// with an empty Message) and unregistered_warning_code (a code
// errors.ParseCode does not know: an extension warns only in registered
// Pulse codes, so lookup, the manifest and the Strict refusal all know
// it). The probe and every run apply it.
func VerifyRegressionOutput(reg RegressionAdapter, spec *types.RegressionSpec, out extend.RegressionOutput, weighted bool) error {
	mismatch := func(reason, msg string, extra map[string]any) error {
		details := map[string]any{"category": extCategoryRegression, "name": string(reg.Name), "reason": reason}
		maps.Copy(details, extra)
		return errors.NewCodedErrorWithDetails(errors.PULSE_EXTENSION_REGRESSION_OUTPUT_MISMATCH,
			fmt.Sprintf("regression %q %s", reg.Name, msg), regProbeDetails(details, weighted))
	}
	if len(out.Coefficients) == 0 {
		return mismatch("missing_coefficients", "returned no coefficient (every fit reports at least one)", nil)
	}
	seen := make(map[string]bool, len(out.Coefficients))
	for _, c := range out.Coefficients {
		if c.Name == "" || seen[c.Name] {
			return mismatch("invalid_coefficient",
				fmt.Sprintf("returned a coefficient whose name %q is empty or repeated", c.Name),
				map[string]any{"coefficient": c.Name})
		}
		seen[c.Name] = true
	}
	if !reg.Decl.PValues {
		for _, c := range out.Coefficients {
			if c.PValue != nil && !math.IsNaN(*c.PValue) {
				return mismatch("undeclared_p_value",
					fmt.Sprintf("returned a p-value for coefficient %q although the registration does not declare PValues (set PValues: true, or report no p-value)", c.Name),
					map[string]any{"coefficient": c.Name})
			}
		}
	}
	declared := reg.Decl.OutputKeys
	if extras := regUndeclaredKeys(out.Outputs, declared); len(extras) > 0 {
		return mismatch("undeclared_output_key",
			fmt.Sprintf("filled Outputs keys its OutputSchema does not declare: %v (declared: %v)", extras, declared),
			map[string]any{"undeclared": extras, "declared_keys": append([]string{}, declared...)})
	}
	for i, w := range out.Warnings {
		if w == nil || w.Message == "" {
			return mismatch("invalid_warning",
				fmt.Sprintf("returned Warnings[%d] that is nil or has an empty Message", i),
				map[string]any{"warning": i})
		}
		if _, ok := errors.ParseCode(w.Code); !ok {
			return mismatch("unregistered_warning_code",
				fmt.Sprintf("returned Warnings[%d] with code %q, which is not a registered Pulse code (use PROCESSING_REGRESSION_DIAGNOSTIC with a details.reason for the model's own diagnostics)", i, w.Code),
				map[string]any{"warning": i, "code": w.Code})
		}
	}
	if spec != nil && spec.Vcov && reg.Decl.Vcov {
		q := len(out.Coefficients)
		if out.Vcov == nil {
			return mismatch("missing_vcov", "returned no Vcov although the spec set vcov and the registration declares Vcov", nil)
		}
		square := len(out.Vcov) == q
		for _, row := range out.Vcov {
			square = square && len(row) == q
		}
		if !square {
			return mismatch("vcov_shape",
				fmt.Sprintf("returned a Vcov that is not %d×%d (square over its %d coefficients)", q, q, q),
				map[string]any{"coefficients": q})
		}
	}
	return nil
}

// regressionFormMismatch is the PULSE_EXTENSION_STREAMABLE_MISMATCH of a
// model that does not implement its declared form (details.reason
// not_streaming, missing_merge or not_buffered).
func regressionFormMismatch(reg RegressionAdapter, reason string, weighted bool) error {
	var msg string
	switch reason {
	case "missing_merge":
		msg = "declares Streamable=true but its model has UpdateRow and Finalize without Merge, which extend.StreamingRegression requires"
	case "not_streaming":
		msg = "declares Streamable=true but its factory does not return an extend.StreamingRegression"
	default:
		msg = "declares Streamable=false but its factory does not return an extend.BufferedRegression"
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_EXTENSION_STREAMABLE_MISMATCH,
		fmt.Sprintf("regression %q %s", reg.Name, msg),
		regProbeDetails(map[string]any{
			"category":   extCategoryRegression,
			"name":       string(reg.Name),
			"streamable": reg.Streamable,
			"reason":     reason,
		}, weighted))
}

// partialStreamingRegression is a streaming model missing Merge — the
// probe names it (missing_merge) rather than reporting a plain
// not_streaming.
type partialStreamingRegression interface {
	UpdateRow(rec extend.Record) error
	Finalize() (extend.RegressionOutput, error)
}

// CheckRegressionForm reports whether model implements the declared
// form.
func CheckRegressionForm(reg RegressionAdapter, model extend.Regression, weighted bool) error {
	if reg.Streamable {
		if _, ok := model.(extend.StreamingRegression); ok {
			return nil
		}
		if _, ok := model.(partialStreamingRegression); ok {
			return regressionFormMismatch(reg, "missing_merge", weighted)
		}
		return regressionFormMismatch(reg, "not_streaming", weighted)
	}
	if _, ok := model.(extend.BufferedRegression); !ok {
		return regressionFormMismatch(reg, "not_buffered", weighted)
	}
	return nil
}

// ---- scorer adapter ----------------------------------------------------

// FittedRegression is the extend view of a bound fit — the one input
// RegressionRegistration.Scorer reads, built the same way for a fit in
// the request and for the same fit reloaded from its model file: the
// coefficients the result carries and its Outputs in their model-file
// JSON form (model.NormalizeOutputs), so the two score identically.
func FittedRegression(b *model.Bound) (extend.FittedRegression, error) {
	fit := extend.FittedRegression{
		Type:       b.Type,
		Target:     b.Target,
		Predictors: slices.Clone(b.Predictors),
	}
	if b.Result != nil {
		fit.Coefficients = maps.Clone(b.Result.Coefficients)
		outs, err := model.NormalizeOutputs(b.Result.Outputs)
		if err != nil {
			return extend.FittedRegression{}, err
		}
		fit.Outputs = outs
	}
	if fit.Coefficients == nil {
		fit.Coefficients = map[string]float64{}
	}
	return fit, nil
}

// AdaptRegressionScorer wraps the registration's Scorer as the engine's
// model.ScorerFactory. model.CheckScoreFor already refused params.terms
// and — but for a type with a BoundScorer — a lower / upper bound on the
// fit, and params.level / category / output / scale but on a type with a
// ParamScorer, so p carries only the fit name, that bound and those.
func AdaptRegressionScorer(reg RegressionAdapter) model.ScorerFactory {
	return func(b *model.Bound, p model.ScoreParams) (model.RowScorer, error) {
		fit, err := FittedRegression(b)
		if err != nil {
			return nil, err
		}
		if reg.ParamScorer != nil {
			ref := reg
			ref.Scorer = func(fit extend.FittedRegression) (extend.RowScorer, error) {
				return reg.ParamScorer(fit, p)
			}
			return BuildRegressionScorer(ref, fit, false)
		}
		if reg.BoundScorer != nil {
			bound := reg
			bound.Scorer = func(fit extend.FittedRegression) (extend.RowScorer, error) {
				return reg.BoundScorer(fit, p.Bound)
			}
			return BuildRegressionScorer(bound, fit, false)
		}
		return BuildRegressionScorer(reg, fit, false)
	}
}

// BuildRegressionScorer calls the registration's Scorer over fit and
// holds the result to the contract: non-nil, reading only the fit's
// predictors (PULSE_EXTENSION_REGRESSION_OUTPUT_MISMATCH, reason
// nil_scorer / scorer_columns). A Scorer error is returned as is.
func BuildRegressionScorer(reg RegressionAdapter, fit extend.FittedRegression, probe bool) (model.RowScorer, error) {
	rs, err := reg.Scorer(fit)
	if err != nil {
		return nil, err
	}
	mismatch := func(reason, msg string, extra map[string]any) error {
		details := map[string]any{"category": extCategoryRegression, "name": string(reg.Name), "reason": reason}
		maps.Copy(details, extra)
		if probe {
			details["stage"] = "scorer"
		}
		return errors.NewCodedErrorWithDetails(errors.PULSE_EXTENSION_REGRESSION_OUTPUT_MISMATCH,
			fmt.Sprintf("regression %q Scorer %s", reg.Name, msg), details)
	}
	if rs == nil {
		return nil, mismatch("nil_scorer", "returned a nil RowScorer", nil)
	}
	cols := slices.Clone(rs.Columns())
	for _, c := range cols {
		if !slices.Contains(fit.Predictors, c) {
			return nil, mismatch("scorer_columns",
				fmt.Sprintf("reads column %q, which is not one of the fit's predictors %v", c, fit.Predictors),
				map[string]any{"column": c, "predictors": append([]string{}, fit.Predictors...)})
		}
	}
	return regressionRowScorer{rs: rs, cols: cols}, nil
}

// regressionRowScorer is the engine's view of an extension RowScorer;
// the engine row (model.Row) is an extend.ScoreRow as is. A row whose
// declared column holds a NaN or ±Inf scores null without reaching the
// extension — a non-finite input is missing data, as in every built-in
// scorer (regression.InputValue).
type regressionRowScorer struct {
	rs   extend.RowScorer
	cols []string
}

func (s regressionRowScorer) Columns() []string { return slices.Clone(s.cols) }

// GeoTally satisfies model.GeoTallier: the wrapped scorer's unseen-geo
// tally (a REG_MMM_BAYES geo panel's), nil when it keeps none.
func (s regressionRowScorer) GeoTally() *model.GeoTally {
	if t, ok := s.rs.(model.GeoTallier); ok {
		return t.GeoTally()
	}
	return nil
}

func (s regressionRowScorer) Score(row model.Row) (float64, bool) {
	for _, c := range s.cols {
		if v, ok := row.NumericValue(c); ok && (math.IsNaN(v) || math.IsInf(v, 0)) {
			return 0, false
		}
	}
	return s.rs.Score(row)
}

// ---- engine adapter ----------------------------------------------------

// regressionEngine is the engine's buffered view of an extension model
// (regression.Engine + regression.BufferedEngine).
type regressionEngine struct{ d *RegressionDriver }

// Fit fits over no rows (the engine's record-less entry point).
func (e *regressionEngine) Fit() (*types.RegressionResult, error) {
	return e.d.fitRows(context.Background(), nil)
}

// FitBuffered fits over the filtered rows, each wrapped in the extend
// view (extRecord).
func (e *regressionEngine) FitBuffered(records []regression.Record) (*types.RegressionResult, error) {
	return e.FitBufferedContext(context.Background(), records)
}

// FitBufferedContext is FitBuffered under the operation's ctx
// (regression.ContextBufferedEngine, which the engine prefers): a model
// implementing extend.ContextRegression receives it through FitContext,
// so a caller cancel or Limits.RequestTimeout reaches a long fit.
func (e *regressionEngine) FitBufferedContext(ctx context.Context, records []regression.Record) (*types.RegressionResult, error) {
	recs := make([]extend.Record, len(records))
	for i, rec := range records {
		view, err := regressionRecordView(rec)
		if err != nil {
			return nil, err
		}
		recs[i] = view
	}
	return e.d.fitRows(ctx, recs)
}

// SetFiguresOffer hands the driver the comparison-figures offer
// (regression.FiguresOfferer).
func (e *regressionEngine) SetFiguresOffer(offer func(*model.RegressionFigures)) {
	e.d.SetFiguresOffer(offer)
}

// streamingRegressionEngine adds the engine's streaming drive
// (regression.StreamingEngine) for a Streamable model.
type streamingRegressionEngine struct {
	regressionEngine
	s extend.StreamingRegression
}

func (e *streamingRegressionEngine) UpdateRow(rec regression.Record) error {
	view, err := regressionRecordView(rec)
	if err != nil {
		return err
	}
	return e.d.updateRow(e.s, view)
}

func (e *streamingRegressionEngine) Finalize() (*types.RegressionResult, error) {
	return e.d.finalize(e.s)
}

// regressionRecordView is the extend view of an engine regression row:
// both engine drives hand the regression engines *Record (ExtRecord);
// a row that already is an extend.Record passes as is, and any other
// regression.Record (a fit over plain rows, regression.FitBuffered)
// reads through its NumericValue alone.
func regressionRecordView(rec regression.Record) (extend.Record, error) {
	switch r := rec.(type) {
	case *processing.Record:
		return processing.ExtRecord{Record: r}, nil
	case extend.Record:
		return r, nil
	case nil:
		return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL, "extension regression received a nil row", nil)
	}
	return numericRecord{rec}, nil
}

// numericRecord is the extend view of a regression.Record that is no
// engine row: its NumericValue, every other read empty.
type numericRecord struct{ r regression.Record }

func (n numericRecord) Schema() *encoding.Schema { return nil }
func (n numericRecord) IsNull(field string) bool {
	_, ok := n.r.NumericValue(field)
	return !ok
}
func (n numericRecord) NumericValue(field string) (float64, bool) { return n.r.NumericValue(field) }
func (n numericRecord) Uint64Value(field string) (uint64, bool) {
	v, ok := n.r.NumericValue(field)
	if !ok {
		return 0, false
	}
	return processing.EchoToUint64(v), true
}
func (numericRecord) StringValue(string) (string, bool) { return "", false }
func (numericRecord) SetMaskValue(string) (encoding.SetMask, bool) {
	return encoding.SetMask{}, false
}
func (numericRecord) DecimalValue(string) (encoding.Decimal128, bool) {
	return encoding.Decimal128{}, false
}
func (numericRecord) VectorValue(string) ([]float64, bool) { return nil, false }
func (numericRecord) Weight() (float64, bool)              { return 0, false }

// AdaptRegressionFactory wraps the registration's extend factory as the
// engine's regression.Factory: the shared declare-or-refuse rule first
// (descx.ExtensionRegressionSlotRefusal), then the target / predictor
// field types (descx.ExtensionRegressionFieldTypeRefusal — the built-in
// regressions' rule or the declared Accepts) — each predict's own call,
// in predict's order — then the order an order-requiring model needs
// (descx.ExtensionRegressionOrderRefusal), then the registration's
// Validate hook
// (descx.ExtensionRegressionValidateRefusal), then the
// model, held to its declared form.
func AdaptRegressionFactory(reg RegressionAdapter) regression.Factory {
	decl := reg.Decl
	return func(spec *types.RegressionSpec, schema *encoding.Schema) (regression.Engine, error) {
		if cerr := descx.ExtensionRegressionSlotRefusal(spec, decl); cerr != nil {
			return nil, cerr
		}
		if cerr := descx.ExtensionRegressionFieldTypeRefusal(spec, decl, schema); cerr != nil {
			return nil, cerr
		}
		// conf_level in (0, 1) — predict's rule, the built-ins' own
		// (regression.validateConfLevel).
		if err := regression.CheckConfLevel(spec); err != nil {
			return nil, err
		}
		// The modifier rules predict applies to every regression: a
		// selection names its criterion, the jackknife knobs are
		// coherent — then a modifier-bearing slot refits the model
		// through the engine's drivers (regressionModifierEngine).
		if cerr := descx.RegressionSelectionCriterionRefusal(spec); cerr != nil {
			return nil, cerr
		}
		if cerr := regresample.Check(spec); cerr != nil {
			return nil, cerr
		}
		// A model that reads its rows in order needs the spec's order
		// (descx.ExtensionRegressionOrderRefusal — predict's own call).
		if cerr := descx.ExtensionRegressionOrderRefusal(spec, decl); cerr != nil {
			return nil, cerr
		}
		// The registration's own spec-only Validate hook, last before
		// the factory (and before any modifier refit builds one) —
		// predict's own call, the same refusal
		// (descx.ExtensionRegressionValidateRefusal, panic-recovered).
		if cerr := descx.ExtensionRegressionValidateRefusal(spec, decl); cerr != nil {
			return nil, cerr
		}
		if spec.Resample != "" || spec.Selection != "" {
			return &regressionModifierEngine{reg: reg, spec: spec, schema: schema}, nil
		}
		model, err := reg.Factory(spec, schema)
		if err != nil {
			return nil, err
		}
		if model == nil {
			return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
				fmt.Sprintf("extension regression %q factory returned a nil model", reg.Name),
				map[string]any{"type": string(reg.Name)})
		}
		if err := CheckRegressionForm(reg, model, false); err != nil {
			return nil, err
		}
		eng := regressionEngine{d: NewRegressionDriver(reg, spec, model)}
		if s, ok := eng.d.streaming(); ok {
			return &streamingRegressionEngine{regressionEngine: eng, s: s}, nil
		}
		return &eng, nil
	}
}

// regressionModifierEngine is the engine's view of an extension model
// under a Resample or Selection modifier: every refit the drivers ask
// for (regression.RunExtensionModifiers) is a fresh model from the
// registration's factory, built from the slot's spec with the active
// predictors substituted and the modifier keys cleared, and fitted
// over the subset through the same driver as an unmodified slot.
type regressionModifierEngine struct {
	reg    RegressionAdapter
	spec   *types.RegressionSpec
	schema *encoding.Schema
	// figures, when set (regression.FiguresOfferer), is handed to the
	// base fit's driver alone — the first refit of an unselected slot,
	// over every row and predictor (a comparison refuses selection) —
	// never to a replicate's.
	figures func(*model.RegressionFigures)
	offered bool
}

// SetFiguresOffer keeps the comparison-figures offer for the base fit
// (regression.FiguresOfferer).
func (e *regressionModifierEngine) SetFiguresOffer(offer func(*model.RegressionFigures)) {
	e.figures = offer
}

// Fit fits over no rows (the engine's record-less entry point).
func (e *regressionModifierEngine) Fit() (*types.RegressionResult, error) {
	return e.FitBufferedContext(context.Background(), nil)
}

func (e *regressionModifierEngine) FitBuffered(records []regression.Record) (*types.RegressionResult, error) {
	return e.FitBufferedContext(context.Background(), records)
}

// FitBufferedContext runs the modifiers under ctx
// (regression.ContextBufferedEngine): a request deadline stops the
// refit loop between two fits.
//
// A model with a Pin hook (REG_ORDINAL) has every refit after the
// first successful one — the base fit, or under selection the search's
// first candidate — built from the pinned spec (REG_ORDINAL: the first
// fit's levels as params.levels), its refusals mapped by the pin, and
// the pin's NoPValue coefficients keep a NaN p-value under resample.
func (e *regressionModifierEngine) FitBufferedContext(ctx context.Context, records []regression.Record) (*types.RegressionResult, error) {
	var pin *model.ReplicatePin
	refit := func(recs []regression.Record, predictors []string) (*types.RegressionResult, []string, error) {
		spec := e.spec
		if pin != nil {
			spec = pin.Spec
		}
		res, keys, err := e.refit(ctx, spec, recs, predictors)
		if err != nil {
			if pin != nil && pin.Refusal != nil {
				err = pin.Refusal(err)
			}
			return nil, nil, err
		}
		if pin == nil && e.reg.Pin != nil {
			p, pinErr := e.reg.Pin(e.spec, res)
			if pinErr != nil {
				return nil, nil, pinErr
			}
			pin = &p
		}
		return res, keys, nil
	}
	res, err := e.runModifiers(ctx, records, refit)
	if err != nil || pin == nil || res.Resample == "" {
		return res, err
	}
	for _, k := range pin.NoPValue {
		if _, ok := res.PValues[k]; ok {
			res.PValues[k] = math.NaN()
		}
	}
	return res, nil
}

// runModifiers is RunExtensionModifiers over refit with the adapter's
// contract-fault constructors.
func (e *regressionModifierEngine) runModifiers(ctx context.Context, records []regression.Record, refit regression.ExtensionFit) (*types.RegressionResult, error) {
	return regression.RunExtensionModifiers(ctx, e.spec, e.reg.Decl.PValues, records, refit,
		func() error { return RegressionSelectionDevianceMismatch(e.reg, false) },
		func(base, got []string) error {
			return errors.NewCodedErrorWithDetails(errors.PULSE_EXTENSION_REGRESSION_OUTPUT_MISMATCH,
				fmt.Sprintf("regression %q returned the coefficients %v on a resample replicate, not the full fit's %v (every refit reports the same coefficients)", e.reg.Name, got, base),
				map[string]any{"category": extCategoryRegression, "name": string(e.reg.Name), "reason": "replicate_coefficients",
					"coefficients": append([]string{}, base...), "replicate": append([]string{}, got...)})
		})
}

// refit is one fresh fit of the model built from spec over recs with
// predictors active, under the operation's ctx (a ContextRegression
// model's FitContext).
func (e *regressionModifierEngine) refit(ctx context.Context, spec *types.RegressionSpec, recs []regression.Record, predictors []string) (*types.RegressionResult, []string, error) {
	inner := *spec
	inner.Predictors = slices.Clone(predictors)
	inner.Resample, inner.JackknifeGroups, inner.JackknifeBy, inner.BootstrapIters, inner.RNGSeed = "", 0, "", 0, 0
	inner.Selection, inner.Criterion = "", ""
	model, err := e.reg.Factory(&inner, e.schema)
	if err != nil {
		return nil, nil, err
	}
	if model == nil {
		return nil, nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
			fmt.Sprintf("extension regression %q factory returned a nil model", e.reg.Name),
			map[string]any{"type": string(e.reg.Name)})
	}
	if err := CheckRegressionForm(e.reg, model, false); err != nil {
		return nil, nil, err
	}
	views := make([]extend.Record, len(recs))
	for i, rec := range recs {
		if views[i], err = regressionRecordView(rec); err != nil {
			return nil, nil, err
		}
	}
	d := NewRegressionDriver(e.reg, &inner, model)
	if e.figures != nil && !e.offered && e.spec.Selection == "" {
		// The base fit: RunExtensionModifiers fits it first.
		e.offered = true
		d.SetFiguresOffer(e.figures)
	}
	out, err := d.fitOutput(ctx, views)
	if err != nil {
		return nil, nil, err
	}
	res, err := d.result(out)
	if err != nil {
		return nil, nil, err
	}
	keys := make([]string, len(out.Coefficients))
	for i, c := range out.Coefficients {
		keys[i] = c.Name
	}
	return res, keys, nil
}

// RegressionSelectionDevianceMismatch is the refusal of a
// SupportsSelection model whose fit reports no deviance — the figure
// selection's information criterion reads.
func RegressionSelectionDevianceMismatch(reg RegressionAdapter, weighted bool) error {
	return errors.NewCodedErrorWithDetails(errors.PULSE_EXTENSION_REGRESSION_OUTPUT_MISMATCH,
		fmt.Sprintf("regression %q declares SupportsSelection but its fit reports no deviance (RegressionFit.Deviance), which selection's AIC / BIC reads", reg.Name),
		regProbeDetails(map[string]any{"category": extCategoryRegression, "name": string(reg.Name), "reason": "missing_deviance"}, weighted))
}

// FitOutput fits the model over recs under ctx (fitOutput): the
// registration probe's drive.
func (d *RegressionDriver) FitOutput(ctx context.Context, recs []extend.Record) (extend.RegressionOutput, error) {
	return d.fitOutput(ctx, recs)
}

// Result maps out onto the slot's result, held to the declaration
// (result).
func (d *RegressionDriver) Result(out extend.RegressionOutput) (*types.RegressionResult, error) {
	return d.result(out)
}

// Spec is the slot the driver fits.
func (d *RegressionDriver) Spec() *types.RegressionSpec { return d.spec }

// NewRegressionRowScorer is the engine's view of an extension RowScorer
// reading cols (regressionRowScorer).
func NewRegressionRowScorer(rs extend.RowScorer, cols []string) model.RowScorer {
	return regressionRowScorer{rs: rs, cols: slices.Clone(cols)}
}

// regUndeclaredKeys is the keys of outputs (sorted) that declared does
// not list.
func regUndeclaredKeys(outputs map[string]any, declared []string) []string {
	var out []string
	for _, k := range slices.Sorted(maps.Keys(outputs)) {
		if !slices.Contains(declared, k) {
			out = append(out, k)
		}
	}
	return out
}
