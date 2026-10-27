package pulse

import (
	"context"
	stderrors "errors"
	"fmt"
	"maps"
	"slices"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/internal/processing/regression"
	"github.com/frankbardon/pulse/internal/regadapt"
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
		NoResample:        reg.NoResample,
		CovarianceTypes:   slices.Clone(reg.CovarianceTypes),
		Scorer:            reg.Scorer != nil,
		Accepts:           slices.Clone(reg.Accepts),
		OutputKeys:        keys,
		Params:            matrixParamSnapshot(reg.Params),
		OutputSchema:      slices.Clone(reg.OutputSchema),
	}
}

// ---- the engine adapter (internal/processing) ---------------------

// regressionAdapter is reg as the engine's shared adapter drives it
// (regadapt.RegressionAdapter): the same declaration predict reads.
func regressionAdapter(reg RegressionRegistration) regadapt.RegressionAdapter {
	return regadapt.RegressionAdapter{
		Name:        reg.Name,
		Decl:        regressionDeclaration(reg),
		Streamable:  reg.Streamable,
		WeightAware: reg.WeightAware,
		Factory:     reg.Factory,
		Scorer:      reg.Scorer,
	}
}

// regressionDriver drives one model over one slot's rows
// (regadapt.RegressionDriver).
type regressionDriver = regadapt.RegressionDriver

func newRegressionDriver(reg RegressionRegistration, spec *types.RegressionSpec, m extend.Regression) *regressionDriver {
	return regadapt.NewRegressionDriver(regressionAdapter(reg), spec, m)
}

// adaptRegressionFactory wraps the registration's extend factory as the
// engine's regression.Factory (regadapt.AdaptRegressionFactory).
func adaptRegressionFactory(reg RegressionRegistration) regression.Factory {
	return regadapt.AdaptRegressionFactory(regressionAdapter(reg))
}

// adaptRegressionScorer wraps the registration's Scorer as the engine's
// model.ScorerFactory (regadapt.AdaptRegressionScorer).
func adaptRegressionScorer(reg RegressionRegistration) model.ScorerFactory {
	return regadapt.AdaptRegressionScorer(regressionAdapter(reg))
}

// verifyRegressionOutput holds a model's output to its registration
// (regadapt.VerifyRegressionOutput).
func verifyRegressionOutput(reg RegressionRegistration, spec *types.RegressionSpec, out extend.RegressionOutput, weighted bool) error {
	return regadapt.VerifyRegressionOutput(regressionAdapter(reg), spec, out, weighted)
}

// checkRegressionForm reports whether m implements the declared form
// (regadapt.CheckRegressionForm).
func checkRegressionForm(reg RegressionRegistration, m extend.Regression, weighted bool) error {
	return regadapt.CheckRegressionForm(regressionAdapter(reg), m, weighted)
}

// buildRegressionScorer calls the registration's Scorer over fit, held
// to the contract (regadapt.BuildRegressionScorer).
func buildRegressionScorer(reg RegressionRegistration, fit extend.FittedRegression, probe bool) (model.RowScorer, error) {
	return regadapt.BuildRegressionScorer(regressionAdapter(reg), fit, probe)
}

// regressionSelectionDevianceMismatch is the refusal of a
// SupportsSelection model whose fit reports no deviance.
func regressionSelectionDevianceMismatch(reg RegressionRegistration, weighted bool) error {
	return regadapt.RegressionSelectionDevianceMismatch(regressionAdapter(reg), weighted)
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
	res, err := d.Result(out)
	if err != nil {
		return err
	}
	b, err := model.Bind("probe", d.Spec(), res)
	if err != nil {
		return err
	}
	fit, err := regadapt.FittedRegression(b)
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
	// The probe has no operation: a ContextRegression model's FitContext
	// runs under a background ctx, so it must refuse the synthetic rows
	// cheaply (a coded data refusal) rather than run long.
	out, err = d.FitOutput(context.Background(), recs)
	if err != nil {
		var coded *errors.CodedError
		if stderrors.As(err, &coded) && regressionDataRefusal(coded.Code) {
			return extend.RegressionOutput{}, err
		}
		return extend.RegressionOutput{}, fail(fmt.Sprintf("returned error: %v", err), map[string]any{"fit_error": err.Error()})
	}
	return out, nil
}
