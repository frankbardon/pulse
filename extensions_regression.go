package pulse

import (
	"context"
	stderrors "errors"
	"fmt"
	"maps"
	"math"
	"slices"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/internal/processing/regression"
	"github.com/frankbardon/pulse/internal/regresample"
	"github.com/frankbardon/pulse/internal/weighting"
	"github.com/frankbardon/pulse/types"
)

// extensions_regression.go lifts a RegressionRegistration onto the
// engine (a regression.Factory on processing.ExtensionRegistry.
// Regressions) and the no-execute snapshot
// (descx.ExtensionsSnapshot.Regressions / RegressionDetails), so an
// extension REG_* model runs, streams, weights and corrects like a
// built-in. The model returns a narrow extend.RegressionOutput; the
// adapter (regressionDriver) owns admission (listwise on target and
// predictors, the valid positive weight), the engine figures (name,
// type, n_obs, sum_weights, n_eff) and the mapping onto
// types.RegressionResult, and holds every output to the registration's
// contract on every run — the same check the pulse.New probe applies.

// regressionDeclaration is the registration's regression-only facts
// as the snapshot (and the shared slot refusal) reads them.
func regressionDeclaration(reg RegressionRegistration) descx.ExtensionRegression {
	keys := make([]string, 0, len(reg.OutputSchema))
	for _, o := range reg.OutputSchema {
		keys = append(keys, o.Key)
	}
	return descx.ExtensionRegression{
		Vcov:              reg.Vcov,
		PValues:           reg.PValues,
		Penalty:           reg.Penalty,
		SupportsSelection: reg.SupportsSelection,
		CovarianceTypes:   slices.Clone(reg.CovarianceTypes),
		Scorer:            reg.Scorer != nil,
		Accepts:           slices.Clone(reg.Accepts),
		OutputKeys:        keys,
		Params:            matrixParamSnapshot(reg.Params),
		OutputSchema:      slices.Clone(reg.OutputSchema),
	}
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

// regressionDriver drives one model over one slot's rows: it admits
// each row (listwise on the spec's target and predictors; on a weighted
// WeightAware slot, a valid and positive weight), counts the admitted
// rows and their weights, and maps the model's output onto the result.
type regressionDriver struct {
	reg   RegressionRegistration
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
}

func newRegressionDriver(reg RegressionRegistration, spec *types.RegressionSpec, model extend.Regression) *regressionDriver {
	d := &regressionDriver{reg: reg, spec: spec, model: model}
	if reg.WeightAware {
		d.weight = spec.Weight.Spec()
	}
	return d
}

// admit judges rec: false for a row missing its target or a predictor,
// or — weighted — whose weight is invalid or zero (built-in regression
// admission, regression.rowWeights.of); else it counts the row and
// returns its weight (0 unweighted).
func (d *regressionDriver) admit(rec extend.Record) (float64, bool) {
	if _, ok := rec.NumericValue(d.spec.Target); !ok {
		return 0, false
	}
	for _, p := range d.spec.Predictors {
		if _, ok := rec.NumericValue(p); !ok {
			return 0, false
		}
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
	d.sumWSq += w * w
	return w, true
}

// updateRow admits rec and, when admitted, hands it to the streaming
// model (weighted: through the reused view).
func (d *regressionDriver) updateRow(s extend.StreamingRegression, rec extend.Record) error {
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

// fitRows fits the model over recs and maps the output (result).
func (d *regressionDriver) fitRows(recs []extend.Record) (*types.RegressionResult, error) {
	out, err := d.fitOutput(recs)
	if err != nil {
		return nil, err
	}
	return d.result(out)
}

// fitOutput fits the model over recs: a streaming model (Streamable
// declared) is fed every admitted row through UpdateRow, in order, then
// finalized — the streaming arm's exact fold; a buffered one fits the
// admitted rows at once.
func (d *regressionDriver) fitOutput(recs []extend.Record) (extend.RegressionOutput, error) {
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
	return b.Fit(rows)
}

// streaming returns the model as a StreamingRegression when the
// registration declares Streamable.
func (d *regressionDriver) streaming() (extend.StreamingRegression, bool) {
	if !d.reg.Streamable {
		return nil, false
	}
	s, ok := d.model.(extend.StreamingRegression)
	return s, ok
}

func (d *regressionDriver) finalize(s extend.StreamingRegression) (*types.RegressionResult, error) {
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
func (d *regressionDriver) result(out extend.RegressionOutput) (*types.RegressionResult, error) {
	if err := verifyRegressionOutput(d.reg, d.spec, out, false); err != nil {
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
		{"pseudo_r2", &res.PseudoR2, f.PseudoR2},
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
	if d.spec.Vcov && d.reg.Vcov {
		v := make([][]float64, q)
		for i, row := range out.Vcov {
			v[i] = slices.Clone(row)
		}
		regression.AttachVcovKeys(res, keys, v)
	}
	if len(out.Outputs) > 0 {
		res.Outputs = maps.Clone(out.Outputs)
	}
	return res, nil
}

func figureOrNaN(v *float64) float64 {
	if v == nil {
		return math.NaN()
	}
	return *v
}

// verifyRegressionOutput holds a model's output to its registration —
// PULSE_EXTENSION_REGRESSION_OUTPUT_MISMATCH, details.reason:
// missing_coefficients (none), invalid_coefficient (an empty or
// repeated name), undeclared_p_value (a finite p-value from a model
// not declaring PValues), undeclared_output_key (an Outputs key OutputSchema
// does not declare), and — when the spec sets vcov and the
// registration declares Vcov — missing_vcov / vcov_shape (not square
// over the coefficients). The probe and every run apply it.
func verifyRegressionOutput(reg RegressionRegistration, spec *types.RegressionSpec, out extend.RegressionOutput, weighted bool) error {
	mismatch := func(reason, msg string, extra map[string]any) error {
		details := map[string]any{"category": string(categoryRegression), "name": string(reg.Name), "reason": reason}
		maps.Copy(details, extra)
		return errors.NewCodedErrorWithDetails(errors.PULSE_EXTENSION_REGRESSION_OUTPUT_MISMATCH,
			fmt.Sprintf("regression %q %s", reg.Name, msg), probeDetails(details, weighted))
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
	if !reg.PValues {
		for _, c := range out.Coefficients {
			if c.PValue != nil && !math.IsNaN(*c.PValue) {
				return mismatch("undeclared_p_value",
					fmt.Sprintf("returned a p-value for coefficient %q although the registration does not declare PValues (set PValues: true, or report no p-value)", c.Name),
					map[string]any{"coefficient": c.Name})
			}
		}
	}
	declared := regressionDeclaration(reg).OutputKeys
	if extras := undeclaredKeys(sortedKeys(out.Outputs), declared); len(extras) > 0 {
		return mismatch("undeclared_output_key",
			fmt.Sprintf("filled Outputs keys its OutputSchema does not declare: %v (declared: %v)", extras, declared),
			map[string]any{"undeclared": extras, "declared_keys": append([]string{}, declared...)})
	}
	if spec != nil && spec.Vcov && reg.Vcov {
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
func regressionFormMismatch(reg RegressionRegistration, reason string, weighted bool) error {
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
		probeDetails(map[string]any{
			"category":   string(categoryRegression),
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

// checkRegressionForm reports whether model implements the declared
// form.
func checkRegressionForm(reg RegressionRegistration, model extend.Regression, weighted bool) error {
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

// fittedRegression is the extend view of a bound fit — the one input
// RegressionRegistration.Scorer reads, built the same way for a fit in
// the request and for the same fit reloaded from its model file: the
// coefficients the result carries and its Outputs in their model-file
// JSON form (model.NormalizeOutputs), so the two score identically.
func fittedRegression(b *model.Bound) (extend.FittedRegression, error) {
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

// adaptRegressionScorer wraps the registration's Scorer as the engine's
// model.ScorerFactory. model.CheckScoreFor already refused params.terms
// and a lower / upper bound on the fit, so p carries only the fit name.
func adaptRegressionScorer(reg RegressionRegistration) model.ScorerFactory {
	return func(b *model.Bound, _ model.ScoreParams) (model.RowScorer, error) {
		fit, err := fittedRegression(b)
		if err != nil {
			return nil, err
		}
		return buildRegressionScorer(reg, fit, false)
	}
}

// buildRegressionScorer calls the registration's Scorer over fit and
// holds the result to the contract: non-nil, reading only the fit's
// predictors (PULSE_EXTENSION_REGRESSION_OUTPUT_MISMATCH, reason
// nil_scorer / scorer_columns). A Scorer error is returned as is.
func buildRegressionScorer(reg RegressionRegistration, fit extend.FittedRegression, probe bool) (model.RowScorer, error) {
	rs, err := reg.Scorer(fit)
	if err != nil {
		return nil, err
	}
	mismatch := func(reason, msg string, extra map[string]any) error {
		details := map[string]any{"category": string(categoryRegression), "name": string(reg.Name), "reason": reason}
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
// the engine row (model.Row) is an extend.ScoreRow as is.
type regressionRowScorer struct {
	rs   extend.RowScorer
	cols []string
}

func (s regressionRowScorer) Columns() []string { return slices.Clone(s.cols) }

func (s regressionRowScorer) Score(row model.Row) (float64, bool) { return s.rs.Score(row) }

// ---- engine adapter ----------------------------------------------------

// regressionEngine is the engine's buffered view of an extension model
// (regression.Engine + regression.BufferedEngine).
type regressionEngine struct{ d *regressionDriver }

// Fit fits over no rows (the engine's record-less entry point).
func (e *regressionEngine) Fit() (*types.RegressionResult, error) { return e.d.fitRows(nil) }

// FitBuffered fits over the filtered rows, each wrapped in the extend
// view (extRecord).
func (e *regressionEngine) FitBuffered(records []regression.Record) (*types.RegressionResult, error) {
	recs := make([]extend.Record, len(records))
	for i, rec := range records {
		view, err := regressionRecordView(rec)
		if err != nil {
			return nil, err
		}
		recs[i] = view
	}
	return e.d.fitRows(recs)
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
// both drives hand the regression engines *processing.Record.
func regressionRecordView(rec regression.Record) (extend.Record, error) {
	pr, ok := rec.(*processing.Record)
	if !ok {
		return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
			fmt.Sprintf("extension regression received a %T row, not an engine record", rec), nil)
	}
	return extRecord{pr}, nil
}

// adaptRegressionFactory wraps the registration's extend factory as the
// engine's regression.Factory: the shared declare-or-refuse rule first
// (descx.ExtensionRegressionSlotRefusal), then the target / predictor
// field types (descx.ExtensionRegressionFieldTypeRefusal — the built-in
// regressions' rule or the declared Accepts) — each predict's own call,
// in predict's order — then the
// model, held to its declared form.
func adaptRegressionFactory(reg RegressionRegistration) regression.Factory {
	decl := regressionDeclaration(reg)
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
		if err := checkRegressionForm(reg, model, false); err != nil {
			return nil, err
		}
		eng := regressionEngine{d: newRegressionDriver(reg, spec, model)}
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
	reg    RegressionRegistration
	spec   *types.RegressionSpec
	schema *encoding.Schema
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
func (e *regressionModifierEngine) FitBufferedContext(ctx context.Context, records []regression.Record) (*types.RegressionResult, error) {
	return regression.RunExtensionModifiers(ctx, e.spec, e.reg.PValues, records, e.refit,
		func() error { return regressionSelectionDevianceMismatch(e.reg, false) },
		func(base, got []string) error {
			return errors.NewCodedErrorWithDetails(errors.PULSE_EXTENSION_REGRESSION_OUTPUT_MISMATCH,
				fmt.Sprintf("regression %q returned the coefficients %v on a resample replicate, not the full fit's %v (every refit reports the same coefficients)", e.reg.Name, got, base),
				map[string]any{"category": string(categoryRegression), "name": string(e.reg.Name), "reason": "replicate_coefficients",
					"coefficients": append([]string{}, base...), "replicate": append([]string{}, got...)})
		})
}

// refit is one fresh fit of the model over recs with predictors active.
func (e *regressionModifierEngine) refit(recs []regression.Record, predictors []string) (*types.RegressionResult, []string, error) {
	inner := *e.spec
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
	if err := checkRegressionForm(e.reg, model, false); err != nil {
		return nil, nil, err
	}
	views := make([]extend.Record, len(recs))
	for i, rec := range recs {
		if views[i], err = regressionRecordView(rec); err != nil {
			return nil, nil, err
		}
	}
	d := newRegressionDriver(e.reg, &inner, model)
	out, err := d.fitOutput(views)
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

// regressionSelectionDevianceMismatch is the refusal of a
// SupportsSelection model whose fit reports no deviance — the figure
// selection's information criterion reads.
func regressionSelectionDevianceMismatch(reg RegressionRegistration, weighted bool) error {
	return errors.NewCodedErrorWithDetails(errors.PULSE_EXTENSION_REGRESSION_OUTPUT_MISMATCH,
		fmt.Sprintf("regression %q declares SupportsSelection but its fit reports no deviance (RegressionFit.Deviance), which selection's AIC / BIC reads", reg.Name),
		probeDetails(map[string]any{"category": string(categoryRegression), "name": string(reg.Name), "reason": "missing_deviance"}, weighted))
}

// addRuntimeRegressions records ext.Regressions on the runtime
// registry: the adapted factory, the declared form under
// "regression|<name>", the WeightAware declaration and the FieldInputs
// hook.
func addRuntimeRegressions(r *processing.ExtensionRegistry, regs []RegressionRegistration) {
	if len(regs) == 0 {
		return
	}
	r.Regressions = make(map[types.RegressionType]regression.Factory, len(regs))
	for _, reg := range regs {
		key := processing.StreamabilityKey(string(categoryRegression), string(reg.Name))
		r.Regressions[reg.Name] = adaptRegressionFactory(reg)
		if reg.Scorer != nil {
			if r.RegressionScorers == nil {
				r.RegressionScorers = make(map[types.RegressionType]model.ScorerFactory, len(regs))
			}
			r.RegressionScorers[reg.Name] = adaptRegressionScorer(reg)
		}
		r.Streamable[key] = reg.Streamable
		r.WeightAware[key] = reg.WeightAware
		if reg.FieldInputs != nil {
			r.FieldInputs[key] = processing.FieldInputsFunc(reg.FieldInputs)
		}
	}
}

// addSnapshotRegressions projects ext.Regressions onto the no-execute
// snapshot: an OperatorMeta each (declared form, WeightAware) plus the
// regression-only details.
func addSnapshotRegressions(snap *descx.ExtensionsSnapshot, regs []RegressionRegistration) {
	if len(regs) == 0 {
		return
	}
	snap.RegressionDetails = make(map[string]descx.ExtensionRegression, len(regs))
	for _, r := range regs {
		snap.Regressions = append(snap.Regressions, descriptor.OperatorMeta{
			Name:        string(r.Name),
			Namespace:   parseNamespace(string(r.Name)),
			Description: r.Description,
			Streamable:  r.Streamable,
			WeightAware: r.WeightAware,
			Accepts:     fieldTypeStrings(r.Accepts),
			Params:      paramMetaSnapshot(r.Params),
		})
		snap.RegressionDetails[string(r.Name)] = regressionDeclaration(r)
	}
}

// ---- registration probe ------------------------------------------------

// Synthetic probe data: a target and two predictors over eight
// complete, non-collinear rows (a well-conditioned design, so a
// correctly written model fits), plus the weights the weighted probe
// reads from probeWeight's column.
const (
	probeRegTarget = "probe_y"
	probeRegX1     = "probe_x1"
	probeRegX2     = "probe_x2"
)

var probeRegressionRows = []map[string]float64{
	{probeRegTarget: 3.1, probeRegX1: 1, probeRegX2: 2, probeMatrixWeightField: 1},
	{probeRegTarget: 4.9, probeRegX1: 2, probeRegX2: 1, probeMatrixWeightField: 2},
	{probeRegTarget: 9.2, probeRegX1: 3, probeRegX2: 5, probeMatrixWeightField: 0.5},
	{probeRegTarget: 8.8, probeRegX1: 4, probeRegX2: 3, probeMatrixWeightField: 1.5},
	{probeRegTarget: 13.1, probeRegX1: 5, probeRegX2: 6, probeMatrixWeightField: 1},
	{probeRegTarget: 12.2, probeRegX1: 6, probeRegX2: 4, probeMatrixWeightField: 3},
	{probeRegTarget: 15.9, probeRegX1: 7, probeRegX2: 7, probeMatrixWeightField: 2},
	{probeRegTarget: 13.8, probeRegX1: 8, probeRegX2: 2, probeMatrixWeightField: 1},
}

// probeRegressionSchema is the synthetic schema a regression factory is
// built against: the f64 target, predictors and weight column.
func probeRegressionSchema() *encoding.Schema {
	names := []string{probeRegTarget, probeRegX1, probeRegX2, probeMatrixWeightField}
	fields := make([]encoding.Field, len(names))
	for i, n := range names {
		fields[i] = encoding.Field{Name: n, Type: encoding.FieldTypeF64, ByteOffset: i * 8}
	}
	return &encoding.Schema{Fields: fields}
}

// probeRegressionRecord is one synthetic probe row as an extend.Record:
// every field f64, nothing null.
type probeRegressionRecord struct {
	schema *encoding.Schema
	vals   map[string]float64
}

func (r probeRegressionRecord) Schema() *encoding.Schema { return r.schema }
func (r probeRegressionRecord) IsNull(field string) bool {
	_, ok := r.vals[field]
	return !ok
}
func (r probeRegressionRecord) NumericValue(field string) (float64, bool) {
	v, ok := r.vals[field]
	return v, ok
}
func (r probeRegressionRecord) Uint64Value(field string) (uint64, bool) {
	v, ok := r.vals[field]
	if !ok {
		return 0, false
	}
	return echoToUint64(v), true
}
func (probeRegressionRecord) StringValue(string) (string, bool) { return "", false }
func (probeRegressionRecord) SetMaskValue(string) (encoding.SetMask, bool) {
	return encoding.SetMask{}, false
}
func (probeRegressionRecord) DecimalValue(string) (encoding.Decimal128, bool) {
	return encoding.ZeroDecimal128(), false
}
func (probeRegressionRecord) VectorValue(string) ([]float64, bool) { return nil, false }
func (probeRegressionRecord) Weight() (float64, bool)              { return 0, false }

// probeRegressions builds and fits every regression registration over
// the synthetic rows, each step under recover():
//
//   - a factory panic, error or nil model, and a fit / finalize panic
//     or error — PULSE_EXTENSION_FACTORY_PANIC (details.stage factory /
//     fit); a fit error carrying a data-dependent regression code
//     (PROCESSING_REGRESSION_INSUFFICIENT_DATA, _RANK_DEFICIENT,
//     _NO_CONVERGE, _SEPARATION — a slot failure at run time) is
//     tolerated and skips the output check.
//   - a model that does not implement the declared form —
//     PULSE_EXTENSION_STREAMABLE_MISMATCH (reason not_streaming,
//     missing_merge or not_buffered).
//   - an output the registration does not allow —
//     PULSE_EXTENSION_REGRESSION_OUTPUT_MISMATCH (verifyRegressionOutput).
//
// The probe spec names the synthetic target and predictors and sets
// vcov when the registration declares Vcov. A WeightAware registration
// is probed a second time with probeWeight on its spec (details
// weighted: true on a failure there).
func probeRegressions(regs []RegressionRegistration) error {
	schema := probeRegressionSchema()
	recs := make([]extend.Record, len(probeRegressionRows))
	for i, vals := range probeRegressionRows {
		recs[i] = probeRegressionRecord{schema: schema, vals: vals}
	}
	for _, reg := range regs {
		probes := []bool{false}
		if reg.WeightAware {
			probes = append(probes, true)
		}
		for _, weighted := range probes {
			if err := probeRegression(reg, schema, recs, weighted); err != nil {
				return err
			}
		}
	}
	return nil
}

func probeRegression(reg RegressionRegistration, schema *encoding.Schema, recs []extend.Record, weighted bool) error {
	predictors := []string{probeRegX1, probeRegX2}
	spec := &types.RegressionSpec{Type: reg.Name, Target: probeRegTarget, Predictors: predictors, Vcov: reg.Vcov,
		Params: probeParams(reg.Params, predictors)}
	if weighted {
		spec.Weight = probeWeight
	}
	model, err := safeBuildRegression(reg, spec, schema, weighted)
	if err != nil {
		return err
	}
	if err := checkRegressionForm(reg, model, weighted); err != nil {
		return err
	}
	d := newRegressionDriver(reg, spec, model)
	out, err := safeFitRegression(reg, d, recs, weighted)
	if err != nil {
		var coded *errors.CodedError
		if stderrors.As(err, &coded) && regressionDataRefusal(coded.Code) {
			return nil
		}
		return err
	}
	if err := verifyRegressionOutput(reg, spec, out, weighted); err != nil {
		return err
	}
	if reg.SupportsSelection && out.Fit.Deviance == nil {
		return regressionSelectionDevianceMismatch(reg, weighted)
	}
	if reg.Scorer == nil || weighted {
		return nil
	}
	return probeRegressionScorer(reg, d, out, recs)
}

// probeRegressionScorer builds the registration's scorer over the probe
// fit (bound like a Compose fit) and scores the probe rows, under
// recover(): a Scorer or Score panic, or a Scorer error, is
// PULSE_EXTENSION_FACTORY_PANIC (details.stage scorer); a nil scorer or
// one reading a column outside the predictors is
// PULSE_EXTENSION_REGRESSION_OUTPUT_MISMATCH (buildRegressionScorer).
func probeRegressionScorer(reg RegressionRegistration, d *regressionDriver, out extend.RegressionOutput, recs []extend.Record) (err error) {
	fail := func(what string, extra map[string]any) error {
		details := map[string]any{"category": string(categoryRegression), "name": string(reg.Name), "stage": "scorer"}
		maps.Copy(details, extra)
		return errors.NewCodedErrorWithDetails(errors.PULSE_EXTENSION_FACTORY_PANIC,
			fmt.Sprintf("regression %q Scorer %s during probe-validation", reg.Name, what), details)
	}
	defer func() {
		if r := recover(); r != nil {
			err = fail(fmt.Sprintf("panicked: %v", r), map[string]any{"panic": fmt.Sprintf("%v", r)})
		}
	}()
	res, err := d.result(out)
	if err != nil {
		return err
	}
	b, err := model.Bind("probe", d.spec, res)
	if err != nil {
		return err
	}
	fit, err := fittedRegression(b)
	if err != nil {
		return err
	}
	rs, err := buildRegressionScorer(reg, fit, true)
	if err != nil {
		var coded *errors.CodedError
		if stderrors.As(err, &coded) && coded.Code == errors.PULSE_EXTENSION_REGRESSION_OUTPUT_MISMATCH {
			return err
		}
		return fail(fmt.Sprintf("returned error: %v", err), map[string]any{"scorer_error": err.Error()})
	}
	for _, rec := range recs {
		rs.Score(rec)
	}
	return nil
}

// regressionDataRefusal reports a data-dependent regression refusal —
// the codes that fail a regression slot alone at run time.
func regressionDataRefusal(code errors.Code) bool {
	switch code {
	case errors.PROCESSING_REGRESSION_INSUFFICIENT_DATA, errors.PROCESSING_REGRESSION_RANK_DEFICIENT,
		errors.PROCESSING_REGRESSION_NO_CONVERGE, errors.PROCESSING_REGRESSION_SEPARATION:
		return true
	}
	return false
}

// safeBuildRegression invokes the factory under a deferred recover with
// the same contract as safeBuildMatrixOp.
func safeBuildRegression(reg RegressionRegistration, spec *types.RegressionSpec, schema *encoding.Schema, weighted bool) (model extend.Regression, err error) {
	fail := func(what string, extra map[string]any) error {
		details := map[string]any{"category": string(categoryRegression), "name": string(reg.Name), "stage": "factory"}
		maps.Copy(details, extra)
		return errors.NewCodedErrorWithDetails(errors.PULSE_EXTENSION_FACTORY_PANIC,
			fmt.Sprintf("regression factory %q %s during probe-validation", reg.Name, what),
			probeDetails(details, weighted))
	}
	defer func() {
		if r := recover(); r != nil {
			model, err = nil, fail(fmt.Sprintf("panicked: %v", r), map[string]any{"panic": fmt.Sprintf("%v", r)})
		}
	}()
	model, err = reg.Factory(spec, schema)
	if err != nil {
		return nil, fail(fmt.Sprintf("returned error: %v", err), withProbeParams(map[string]any{"factory_error": err.Error()}, spec.Params))
	}
	if model == nil {
		return nil, fail("returned a nil model", nil)
	}
	return model, nil
}

// safeFitRegression fits the model over the probe rows through the
// run-time driver under a deferred recover. A panic, or an error that
// is not a data-dependent regression refusal, is
// PULSE_EXTENSION_FACTORY_PANIC (details.stage fit); a data-dependent
// refusal is returned as is.
func safeFitRegression(reg RegressionRegistration, d *regressionDriver, recs []extend.Record, weighted bool) (out extend.RegressionOutput, err error) {
	fail := func(what string, extra map[string]any) error {
		details := map[string]any{"category": string(categoryRegression), "name": string(reg.Name), "stage": "fit"}
		maps.Copy(details, extra)
		return errors.NewCodedErrorWithDetails(errors.PULSE_EXTENSION_FACTORY_PANIC,
			fmt.Sprintf("regression %q fit %s during probe-validation", reg.Name, what),
			probeDetails(details, weighted))
	}
	defer func() {
		if r := recover(); r != nil {
			out, err = extend.RegressionOutput{}, fail(fmt.Sprintf("panicked: %v", r), map[string]any{"panic": fmt.Sprintf("%v", r)})
		}
	}()
	out, err = d.fitOutput(recs)
	if err != nil {
		var coded *errors.CodedError
		if stderrors.As(err, &coded) && regressionDataRefusal(coded.Code) {
			return extend.RegressionOutput{}, err
		}
		return extend.RegressionOutput{}, fail(fmt.Sprintf("returned error: %v", err), map[string]any{"fit_error": err.Error()})
	}
	return out, nil
}
