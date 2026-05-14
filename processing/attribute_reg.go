package processing

import (
	"fmt"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/processing/regression"
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
	switch attr.Type {
	case types.ATTR_REG_FITTED, types.ATTR_REG_RESIDUAL, types.ATTR_REG_LEVERAGE:
		return string(attr.Type) + "_" + attr.Target
	}
	return string(attr.Type) + "_" + attr.Field
}

// Per-row regression attributes (Phase 7).
//
// Three attribute types — ATTR_REG_FITTED, ATTR_REG_RESIDUAL,
// ATTR_REG_LEVERAGE — emit a single diagnostic column per row from a
// regression fit computed in the pass-1 prepass. Each attribute carries
// its own RegressionSpec-shaped fields (Target, Predictors, Penalty,
// Alpha, L1Ratio) on the types.Attribute spec and refits internally;
// the regression Response.Regressions[] slot is not populated. Option A
// per .planning/regression/README.md.
//
// All three implement TwoPassAttribute:
//   - PrePass buffers the records (Record is a pointer, so this is one
//     pointer slot per row — no value copy);
//   - Finalize calls regression.FitForAttribute and freezes the
//     coefficient bundle;
//   - Row emits ŷᵢ / yᵢ − ŷᵢ / hᵢᵢ for the supplied record.
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
	rows       []regression.Record
}

// PrePassRecord folds one record's (target, predictors) into the
// accumulator. Rows with any null are skipped (listwise deletion,
// matches olsEngine.UpdateRow).
func (w *regressionAccumWrap) PrePassRecord(rec *Record) {
	if _, ok := rec.NumericValue(w.target); !ok {
		return
	}
	values := make(map[string]float64, len(w.predictors)+1)
	y, _ := rec.NumericValue(w.target)
	values[w.target] = y
	for _, name := range w.predictors {
		v, ok := rec.NumericValue(name)
		if !ok {
			return
		}
		values[name] = v
	}
	w.rows = append(w.rows, &mapRecord{values: values})
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
	}
}

// newRegressionAttribute is the shared factory body. The three
// per-type factories below dispatch into this with their kind.
func newRegressionAttribute(attr *types.Attribute, schema *encoding.Schema, kind regressionAttributeKind) (AttributeComputer, error) {
	if attr == nil {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"regression attribute requires a non-nil spec")
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
					errors.PROCESSING_CONFIG,
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
					errors.PROCESSING_CONFIG,
					fmt.Sprintf("%s predictor %q must reference a numeric field, got %s", attr.Type, name, f.Type),
					map[string]any{"attribute": string(attr.Type), "field": name, "field_type": f.Type.String()},
				)
			}
		}
	}

	// ATTR_REG_LEVERAGE is OLS-unpenalized only. Penalized leverage uses
	// a different formula (involving the regularized resolvent) and GLM
	// leverage requires the IRLS weight matrix; both deferred to Phase 9.
	if kind == regAttrLeverage && attr.Penalty != "" {
		return nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_CONFIG,
			"ATTR_REG_LEVERAGE supports only unpenalized OLS — penalized leverage / GLM leverage not implemented",
			map[string]any{"attribute": string(attr.Type), "penalty": attr.Penalty},
		)
	}

	// Validate Penalty / Alpha / L1Ratio coupling through the same
	// validator the engine uses, so the rules stay in sync.
	spec := makeRegressionSpec(attr)
	if err := regression.ValidateRegression(spec); err != nil {
		return nil, err
	}

	return &regressionAttribute{
		kind: kind,
		spec: spec,
		acc: &regressionAccumWrap{
			target:     spec.Target,
			predictors: append([]string(nil), spec.Predictors...),
		},
	}, nil
}

// regressionAcceptsType reports whether the given encoding.FieldType is
// a numeric type acceptable as a regression Target or Predictor. Mirrors
// the AcceptsTypes list on the descriptor entries (no decimal, no geo,
// no categorical).
func regressionAcceptsType(ft encoding.FieldType) bool {
	switch ft {
	case encoding.FieldTypeU8, encoding.FieldTypeU16, encoding.FieldTypeU32, encoding.FieldTypeU64,
		encoding.FieldTypeF32, encoding.FieldTypeF64,
		encoding.FieldTypeNullableU4, encoding.FieldTypeNullableU8, encoding.FieldTypeNullableU16,
		encoding.FieldTypeDate:
		return true
	}
	return false
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

	fit, err := regression.FitForAttribute(a.spec, a.acc.rows, a.kind == regAttrLeverage)
	if err != nil {
		return err
	}
	a.fit = fit
	// Release the buffer; Row reads only fit + the live record.
	a.acc.rows = nil
	return nil
}

// Row emits the per-row scalar for this attribute given a record and
// the finalized fit. Rows with null target / predictors emit 0; the
// downstream label injection treats this as a numeric value (mirrors
// existing ATTR_ZSCORE behaviour for null inputs).
func (a *regressionAttribute) Row(rec *Record, _ string) (float64, error) {
	if !a.finalized || a.fit == nil {
		return 0, errors.NewCodedError(errors.PROCESSING_INTERNAL,
			"regression attribute Row called before Finalize")
	}
	x := make([]float64, len(a.fit.PredictorOrder))
	for i, name := range a.fit.PredictorOrder {
		v, ok := rec.NumericValue(name)
		if !ok {
			return 0, nil
		}
		x[i] = v
	}
	yhat := a.fit.Intercept
	for i, c := range a.fit.Coefficients {
		yhat += c * x[i]
	}

	switch a.kind {
	case regAttrFitted:
		return yhat, nil
	case regAttrResidual:
		y, ok := rec.NumericValue(a.spec.Target)
		if !ok {
			return 0, nil
		}
		return y - yhat, nil
	case regAttrLeverage:
		return regression.LeverageForRow(a.fit, x), nil
	}
	return 0, errors.NewCodedErrorWithDetails(
		errors.PROCESSING_INTERNAL,
		"regression attribute has unknown kind",
		map[string]any{"kind": int(a.kind)},
	)
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
