package processing

import (
	"fmt"
	"math"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/processing/regression"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/types"
)

// defaultAttributeLabel returns the canonical default output label for
// an attribute when its Label field is empty. Regression attributes
// substitute Target for Field so the synthesized label remains
// meaningful when the spec carries no single Field reference. Every
// orchestrator path (streaming two-pass, streaming row-local, buffered
// applyAttributes, the buildRowLocal helper) routes through this so
// the label rule stays single-sourced.
func defaultAttributeLabel(attr *types.Attribute) string {
	return vectors.DefaultAttributeLabel(attr)
}

// DefaultAttributeLabel is defaultAttributeLabel for the service's
// per-shard and parallel-decode reducers, which build their own
// row-local attribute set.
func DefaultAttributeLabel(attr *types.Attribute) string { return defaultAttributeLabel(attr) }

// Per-row regression attributes (Phase 7).
//
// Four attribute types — ATTR_REG_FITTED, ATTR_REG_RESIDUAL,
// ATTR_REG_LEVERAGE, ATTR_REG_INFLUENCE — emit a single diagnostic
// column per row from a
// regression fit computed in the pass-1 prepass. Each attribute carries
// its own RegressionSpec-shaped fields (Target, Predictors, Penalty,
// Alpha, L1Ratio) on the types.Attribute spec and refits internally;
// the regression Response.Regressions[] slot is not populated. Option A
// per .planning/regression/README.md.
//
// All four implement TwoPassAttribute:
//   - PrePass buffers the records (Record is a pointer, so this is one
//     pointer slot per row — no value copy);
//   - Finalize calls regression.FitForAttribute and freezes the
//     coefficient bundle;
//   - Row emits ŷᵢ / yᵢ − ŷᵢ / hᵢᵢ / an influence figure for the
//     supplied record (the standardized / studentized residual kinds
//     and ATTR_REG_INFLUENCE read regression.InfluenceForRow).
//
// Streamability=true: the two-pass orchestrator drives this via
// iter.Reset(), so the whole dataset is never materialized inside the
// attribute (the streaming path holds the records for the duration of
// PrePass→Finalize; pass 2 reads them from the iterator). The buffered
// path (Compute) is also supported for parity with other attributes.

// regressionAttributeKind identifies which per-row output a shared base
// computer emits. Captured in the factory and read inside Row.
type regressionAttributeKind int

const (
	regAttrFitted regressionAttributeKind = iota
	regAttrResidual
	regAttrLeverage
	regAttrInfluence
)

// regressionAttribute is the shared TwoPassAttribute implementation for
// ATTR_REG_FITTED / RESIDUAL / LEVERAGE. The kind field selects the
// per-row formula; all three share PrePass/Finalize semantics.
//
// PrePass extracts (target, predictor) values into the streaming
// accumulator immediately. The two-pass orchestrator drives this with
// a reusable iterator (EnableReuse) — record pointers are NOT safe to
// retain across PrePass calls, so the attribute folds values inline.
type regressionAttribute struct {
	kind regressionAttributeKind
	spec *types.RegressionSpec

	// residualKind is ATTR_REG_RESIDUAL's params.kind on the refit:
	// response / pearson / deviance / working are all y − ŷ (OLS is
	// Gaussian, identity); standardized / studentized read the
	// refit's design (model.ScaledResidualKind). measure is
	// ATTR_REG_INFLUENCE's params.measure (never empty).
	residualKind string
	measure      string

	// acc is the streaming OLS sufficient-statistics accumulator. Folded
	// row-by-row in PrePass; the solver in Finalize consumes its
	// centered Gram + cross-products to produce coefficients (and, for
	// leverage, the centered-Gram inverse).
	acc *regressionAccumWrap

	// fit is populated by Finalize. Row reads from it.
	fit       *regression.AttributeFit
	finalized bool
}

// regressionAccumWrap is a thin adapter around regression.Record so
// the per-row PrePass can drive regression.FitForAttribute via a single
// listwise-deletion sweep. We collect (x, y) pairs in memory (O(n·p)
// space) because the leverage path needs the centered-Gram inverse and
// the per-row x vectors in pass 2; storing the (x, y) pairs gives us
// both. For ATTR_REG_FITTED / RESIDUAL the same storage is unused for
// post-fit work but kept for simplicity (one code path, one solver).
type regressionAccumWrap struct {
	target     string
	predictors []string
	// weight is the refit's stamped weight field ("" unweighted); its
	// value rides each buffered row so the refit judges it.
	weight string
	rows   []regression.Record
}

// PrePassRecord folds one record's (target, predictors) into the
// accumulator. Rows with any null or non-finite input are skipped
// (listwise deletion, regression.InputValue — matches olsEngine.UpdateRow).
func (w *regressionAccumWrap) PrePassRecord(rec *Record) {
	y, ok := regression.InputValue(rec, w.target)
	if !ok {
		return
	}
	values := make(map[string]float64, len(w.predictors)+1)
	values[w.target] = y
	for _, name := range w.predictors {
		v, ok := regression.InputValue(rec, name)
		if !ok {
			return
		}
		values[name] = v
	}
	if w.weight != "" {
		// A null weight stays absent: the refit excludes the row.
		if wv, ok := rec.NumericValue(w.weight); ok {
			values[w.weight] = wv
		}
	}
	w.rows = append(w.rows, &mapRecord{values: values})
}

// weightField is spec's field, "" when spec is nil (unweighted).
func weightField(spec *types.WeightSpec) string {
	if spec == nil {
		return ""
	}
	return spec.Field
}

// mapRecord is a regression.Record adapter backed by a small per-row
// map. Cheap to allocate; owned by regressionAccumWrap.rows.
type mapRecord struct {
	values map[string]float64
}

func (m *mapRecord) NumericValue(name string) (float64, bool) {
	v, ok := m.values[name]
	return v, ok
}

// makeRegressionSpec builds the internal RegressionSpec from the
// attribute spec. The factory has already validated the spec shape;
// this just packages the fields for the regression.FitForAttribute
// entry point.
func makeRegressionSpec(attr *types.Attribute) *types.RegressionSpec {
	return &types.RegressionSpec{
		Type:       types.REG_OLS,
		Target:     attr.Target,
		Predictors: append([]string(nil), attr.Predictors...),
		Penalty:    attr.Penalty,
		Alpha:      attr.Alpha,
		L1Ratio:    attr.L1Ratio,
		// The slot's stamped weight (processing.StampWeightsWith): the
		// refit is weighted with the regression's own kinds.
		Weight: attr.Weight,
	}
}

// newRegressionAttribute is the shared factory body. The three
// per-type factories below dispatch into this with their kind.
func newRegressionAttribute(attr *types.Attribute, schema *encoding.Schema, kind regressionAttributeKind) (AttributeComputer, error) {
	if attr == nil {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"regression attribute requires a non-nil spec")
	}
	if (kind == regAttrFitted || kind == regAttrResidual) && model.FitReferenced(attr.Type, attr) {
		// params.fit scores a bound fit instead of the OLS refit
		// (attribute_reg_fit.go).
		return newRegFitScoreAttribute(attr, schema, kind), nil
	}
	// The refit's params and penalty rule, shared with predict's
	// field-reference pass (model.CheckRefit).
	if cerr := model.CheckRefit(attr.Type, attr); cerr != nil {
		return nil, cerr
	}
	if attr.Target == "" {
		return nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_CONFIG,
			fmt.Sprintf("%s requires Target", attr.Type),
			map[string]any{"attribute": string(attr.Type)},
		)
	}
	if len(attr.Predictors) == 0 {
		return nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_CONFIG,
			fmt.Sprintf("%s requires at least one predictor", attr.Type),
			map[string]any{"attribute": string(attr.Type)},
		)
	}
	if schema != nil {
		if f := schema.Field(attr.Target); f != nil {
			if !regressionAcceptsType(f.Type) {
				return nil, errors.NewCodedErrorWithDetails(
					errors.PULSE_FIELD_TYPE_UNSUPPORTED,
					fmt.Sprintf("%s Target %q must reference a numeric field, got %s", attr.Type, attr.Target, f.Type),
					map[string]any{"attribute": string(attr.Type), "field": attr.Target, "field_type": f.Type.String()},
				)
			}
		}
		for _, name := range attr.Predictors {
			f := schema.Field(name)
			if f == nil {
				continue
			}
			if !regressionAcceptsType(f.Type) {
				return nil, errors.NewCodedErrorWithDetails(
					errors.PULSE_FIELD_TYPE_UNSUPPORTED,
					fmt.Sprintf("%s predictor %q must reference a numeric field, got %s", attr.Type, name, f.Type),
					map[string]any{"attribute": string(attr.Type), "field": name, "field_type": f.Type.String()},
				)
			}
		}
	}

	// A slot reading the refit's design is OLS-unpenalized only
	// (model.CheckRefit refused a penalty above). Penalized leverage uses
	// a different formula (involving the regularized resolvent) and GLM
	// leverage requires the IRLS weight matrix; both deferred.
	var residualKind, measure string
	switch kind {
	case regAttrResidual:
		residualKind = model.RefitResidualKind(attr)
	case regAttrInfluence:
		p, _ := model.DecodeInfluenceParams(attr.Params)
		measure = p.Measure
	}
	if model.PerObservationRefit(attr.Type, attr) {
		// One observation's figure has no probability-weighted form; the
		// resolver refuses it first (PULSE_WEIGHT_UNSUPPORTED,
		// weighting.AttributeClassOf) — this guards a direct factory call.
		if w := attr.Weight.Spec(); w != nil && w.EffectiveKind() == types.WeightKindProbability {
			return nil, errors.NewCodedErrorWithDetails(
				errors.PROCESSING_CONFIG,
				fmt.Sprintf("%s reads each record as w identical observations, which a probability weight does not define; set the slot's weight to null to fit unweighted", attr.Type),
				map[string]any{"attribute": string(attr.Type), "weight": w.Field, "kind": string(w.EffectiveKind())},
			)
		}
	}

	// Validate Penalty / Alpha / L1Ratio coupling through the same
	// validator the engine uses, so the rules stay in sync.
	spec := makeRegressionSpec(attr)
	if err := regression.ValidateRegression(spec); err != nil {
		return nil, err
	}

	return &regressionAttribute{
		kind:         kind,
		spec:         spec,
		residualKind: residualKind,
		measure:      measure,
		acc: &regressionAccumWrap{
			target:     spec.Target,
			predictors: append([]string(nil), spec.Predictors...),
			weight:     weightField(spec.Weight.Spec()),
		},
	}, nil
}

// regressionAcceptsType reports whether the given encoding.FieldType is
// a numeric type acceptable as a regression Target or Predictor for the
// ATTR_REG_* attributes. Mirrors the analytics-numeric set used by
// REG_OLS / REG_GLM / REG_BAYES_LINEAR (descriptor.regressionAcceptsType)
// since the ATTR_REG_* attributes refit OLS under the hood.
func regressionAcceptsType(ft encoding.FieldType) bool {
	return ft.IsNumericForAnalytics()
}

// newRegFittedAttribute / newRegResidualAttribute / newRegLeverageAttribute
// are the registry-facing factories. They thread the kind into the
// shared constructor.
func newRegFittedAttribute(attr *types.Attribute, schema *encoding.Schema) (AttributeComputer, error) {
	return newRegressionAttribute(attr, schema, regAttrFitted)
}

func newRegResidualAttribute(attr *types.Attribute, schema *encoding.Schema) (AttributeComputer, error) {
	return newRegressionAttribute(attr, schema, regAttrResidual)
}

func newRegLeverageAttribute(attr *types.Attribute, schema *encoding.Schema) (AttributeComputer, error) {
	return newRegressionAttribute(attr, schema, regAttrLeverage)
}

func newRegInfluenceAttribute(attr *types.Attribute, schema *encoding.Schema) (AttributeComputer, error) {
	return newRegressionAttribute(attr, schema, regAttrInfluence)
}

// needsDesign reports whether the slot reads the refit's centered-Gram
// inverse: leverage, influence and the scaled residual kinds.
func (a *regressionAttribute) needsDesign() bool {
	return a.kind == regAttrLeverage || a.kind == regAttrInfluence || model.ScaledResidualKind(a.residualKind)
}

// PrePass extracts the per-row (Target, Predictors) values from the
// record and folds them into the accumulator's row buffer. Stores
// scalars only — record pointers are not retained, so iterator reuse
// (EnableReuse) is safe.
//
// Field argument is the attribute's Field (which the regression
// attributes ignore — they use Target / Predictors instead). Kept for
// the TwoPassAttribute signature.
func (a *regressionAttribute) PrePass(rec *Record, _ string) error {
	if a.finalized {
		return errors.NewCodedError(errors.PROCESSING_INTERNAL,
			"regression attribute PrePass after Finalize")
	}
	a.acc.PrePassRecord(rec)
	return nil
}

// Finalize runs the OLS fit over the buffered (x, y) pairs. Leverage
// additionally requests the centered-Gram inverse.
func (a *regressionAttribute) Finalize() error {
	if a.finalized {
		return errors.NewCodedError(errors.PROCESSING_INTERNAL,
			"regression attribute Finalize called twice")
	}
	a.finalized = true

	fit, err := regression.FitForAttribute(a.spec, a.acc.rows, a.needsDesign())
	if err != nil {
		return err
	}
	a.fit = fit
	// Release the buffer; Row reads only fit + the live record.
	a.acc.rows = nil
	return nil
}

// EmitsNull satisfies NullEmitter: a NaN from Row / Compute is a null.
func (a *regressionAttribute) EmitsNull() bool { return true }

var _ NullEmitter = (*regressionAttribute)(nil)

// Row emits the per-row scalar for this attribute given a record and
// the finalized fit. A row with a null or non-finite predictor — or,
// for ATTR_REG_RESIDUAL, target (regression.InputValue) — emits NaN, which the orchestrator
// writes as a NULL (NullEmitter), so a downstream aggregation skips it
// rather than averaging a 0. ATTR_REG_PREDICT shares the rule.
func (a *regressionAttribute) Row(rec *Record, _ string) (float64, error) {
	if !a.finalized || a.fit == nil {
		return 0, errors.NewCodedError(errors.PROCESSING_INTERNAL,
			"regression attribute Row called before Finalize")
	}
	x := make([]float64, len(a.fit.PredictorOrder))
	for i, name := range a.fit.PredictorOrder {
		v, ok := regression.InputValue(rec, name)
		if !ok {
			return math.NaN(), nil
		}
		x[i] = v
	}
	// The shared scoring kernel: ATTR_REG_PREDICT scores a bound fit
	// with the same expression, so the two agree bit for bit.
	yhat := regression.LinearPredictor(a.fit.Intercept, a.fit.Coefficients, x)

	switch a.kind {
	case regAttrFitted:
		return yhat, nil
	case regAttrResidual:
		y, ok := regression.InputValue(rec, a.spec.Target)
		if !ok {
			return math.NaN(), nil
		}
		switch a.residualKind {
		case model.ResidualStandardized:
			return a.influence(rec, x, y-yhat).Standardized, nil
		case model.ResidualStudentized:
			return a.influence(rec, x, y-yhat).Studentized, nil
		}
		// response / pearson / deviance / working: Gaussian identity.
		return y - yhat, nil
	case regAttrLeverage:
		// A row excluded from the weighted refit (invalid or zero
		// weight) has leverage 0 — the w = 0 value of the identity.
		w, ok := a.fit.RowWeight(rec)
		if !ok {
			return 0, nil
		}
		return regression.LeverageForRow(a.fit, x, w), nil
	case regAttrInfluence:
		y, ok := regression.InputValue(rec, a.spec.Target)
		if !ok {
			return math.NaN(), nil
		}
		inf := a.influence(rec, x, y-yhat)
		if a.measure == model.InfluenceDFFITS {
			return inf.DFFITS, nil
		}
		return inf.Cooks, nil
	}
	return 0, errors.NewCodedErrorWithDetails(
		errors.PROCESSING_INTERNAL,
		"regression attribute has unknown kind",
		map[string]any{"kind": int(a.kind)},
	)
}

// influence is one observation's influence figures for rec — every
// figure NaN (null) when rec is outside the weighted refit (an invalid
// or zero weight): it is no observation of the fit.
func (a *regressionAttribute) influence(rec *Record, x []float64, e float64) regression.RowInfluence {
	if _, ok := a.fit.RowWeight(rec); !ok {
		nan := math.NaN()
		return regression.RowInfluence{Leverage: nan, Standardized: nan, Studentized: nan, Cooks: nan, DFFITS: nan}
	}
	return regression.InfluenceForRow(a.fit, x, e)
}

// Compute runs the buffered-path two-pass over the supplied record
// slice. Equivalent to PrePass(every) → Finalize → Row(every).
// Returns a length-len(records) slice with one float per row.
func (a *regressionAttribute) Compute(records []*Record, field string) ([]float64, error) {
	if len(records) == 0 {
		return []float64{}, nil
	}
	for _, r := range records {
		if err := a.PrePass(r, field); err != nil {
			return nil, err
		}
	}
	if err := a.Finalize(); err != nil {
		return nil, err
	}
	out := make([]float64, len(records))
	for i, r := range records {
		v, err := a.Row(r, field)
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return out, nil
}
