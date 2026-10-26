package descriptor

import (
	"sort"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/fieldaccept"
	"github.com/frankbardon/pulse/types"
)

// ExtensionRegression carries one extension REG_* model's
// regression-only facts (pulse.RegressionRegistration): the shared
// spec slots it declares it honours and the Outputs keys it may fill.
// Its OperatorMeta (name, declared Streamable, WeightAware) rides
// ExtensionsSnapshot.Regressions. Never serialised.
type ExtensionRegression struct {
	// Vcov reports that the model returns its coefficient covariance,
	// so a spec's vcov: true is honoured rather than refused.
	Vcov bool
	// PValues reports that the model emits coefficient p-values
	// (RegressionRegistration.PValues): its slot takes a multiplicity
	// block and predict counts its p-values. Undeclared, a block
	// resolving to any method but none is refused and predict counts
	// none.
	PValues bool
	// Penalty reports that the model reads the shared penalty / alpha /
	// l1_ratio slots itself, so setting them is honoured, not refused.
	Penalty bool
	// SupportsSelection reports that the model can be refit over a
	// predictor subset, so the selection driver may wrap it.
	SupportsSelection bool
	// NoResample reports that the model opts out of the engine's
	// resample driver (RegressionRegistration.NoResample): resample and
	// its knobs are refused, not honoured.
	NoResample bool
	// CovarianceTypes are the covariance.type values the model computes
	// itself; a covariance block naming another type is refused.
	CovarianceTypes []types.CovarianceType
	// Accepts are the declared target / predictor field types (empty:
	// the built-in regressions' set; fieldaccept.ExtensionRegressionAccepts).
	Accepts []encoding.FieldType
	// Scorer reports that the registration supplies a row scorer
	// (RegressionRegistration.Scorer): its fits score (ATTR_REG_PREDICT;
	// without one, reason no_scorer) and may be saved as a model file
	// (without one, save is refused).
	Scorer bool
	// OutputKeys are the declared OutputSchema keys, in order.
	OutputKeys []string
	// Params are the model's own params-object keys in the manifest's
	// Param shape (the snapshot's OperatorMeta.Params carries the same
	// declaration as the allowlist reads it).
	Params []descriptor.Param
	// OutputSchema is the declared OutputSchema, in order.
	OutputSchema []descriptor.RegressionOutputMeta
}

// extCategoryRegression is the regression extension category, as
// WeightAwareness and extensionStreamable spell it.
const extCategoryRegression = "regression"

// regression returns the snapshot's entry for the extension REG_*
// model name. Nil-safe.
func (s *ExtensionsSnapshot) regression(name string) (descriptor.OperatorMeta, ExtensionRegression, bool) {
	if s == nil {
		return descriptor.OperatorMeta{}, ExtensionRegression{}, false
	}
	for _, m := range s.Regressions {
		if m.Name == name {
			return m, s.RegressionDetails[name], true
		}
	}
	return descriptor.OperatorMeta{}, ExtensionRegression{}, false
}

// regressionExtensionFor returns t's extension REG_* entry when the
// instance registers t and does not hide it (snap, else the instance's
// own projection). A built-in type, an unknown one and a hidden one all
// report false.
func regressionExtensionFor(inst *InstanceSnapshot, snap *ExtensionsSnapshot, t types.RegressionType) (descriptor.OperatorMeta, ExtensionRegression, bool) {
	if snap == nil {
		snap = inst.Extensions()
	}
	if inst.Hidden(string(t)) {
		return descriptor.OperatorMeta{}, ExtensionRegression{}, false
	}
	return snap.regression(string(t))
}

// isRegisteredRegression reports whether t is an extension REG_* model
// the instance offers (snap, else the instance's own projection).
func isRegisteredRegression(inst *InstanceSnapshot, snap *ExtensionsSnapshot, t types.RegressionType) bool {
	_, _, ok := regressionExtensionFor(inst, snap, t)
	return ok
}

// regressionSpecStreamable is the instance's spec-level streamability of
// a regression spec — the twin of
// processing.ExtensionRegistry.RegressionSpecStreamable: an extension
// model's DECLARED form folded with the spec's modifier downgrade
// (types.RegressionSpec.StreamableAs), else the built-in
// types.RegressionSpec.Streamable. A hidden type is never streamable.
func regressionSpecStreamable(opts *PredictOptions, reg *types.RegressionSpec) bool {
	inst := opts.instance()
	if m, _, ok := regressionExtensionFor(inst, extensionsFromOpts(opts), reg.Type); ok {
		return reg.StreamableAs(m.Streamable)
	}
	return routedRegression(reg, inst).Streamable()
}

// extensionRegressionSlotParams are the shared RegressionSpec fields an
// extension model reads off every slot, listed (marked Slot) ahead of
// its own params keys; the shared slots the registration declares it
// honours (regressionSlotParamsFor, the declare-or-refuse table's
// accepted set) follow target and predictors.
func extensionRegressionSlotParams(decl ExtensionRegression) []descriptor.Param {
	out := []descriptor.Param{
		{Name: "target", Type: "field", Required: true, FieldFilter: "numeric", Description: "Response variable field name; a top-level key of the regression entry.", Slot: true},
		{Name: "predictors", Type: "list", Required: true, Description: "Predictor field names; a top-level key of the regression entry.", Slot: true},
	}
	return append(out, regressionSlotParamsFor(decl)...)
}

// ExtensionRegressionFieldTypeRefusal is the accepted-types refusal of
// an extension REG_* model's target and predictors: the built-in
// regressions' rule, or exactly the declared Accepts
// (fieldaccept.CheckExtensionRegression) — nil when every schema field
// is accepted. Predict and the runtime adapter raise the same error.
func ExtensionRegressionFieldTypeRefusal(spec *types.RegressionSpec, decl ExtensionRegression, schema *encoding.Schema) *errors.CodedError {
	if spec == nil {
		return nil
	}
	op := string(spec.Type)
	if cerr := fieldaccept.CheckExtensionRegression(op, decl.Accepts, fieldaccept.SlotTarget, spec.Target, schema); cerr != nil {
		return cerr
	}
	for _, p := range spec.Predictors {
		if cerr := fieldaccept.CheckExtensionRegression(op, decl.Accepts, fieldaccept.SlotPredictor, p, schema); cerr != nil {
			return cerr
		}
	}
	return nil
}

// extensionRegressionEmitsNote is the manifest row's emits note: the
// built-in shape, with p_values null on every coefficient for a model
// that declares no PValues.
func extensionRegressionEmitsNote(pValues bool) string {
	p := "p_values (null on every coefficient: the model declares no PValues, so a multiplicity block takes only method none)"
	if pValues {
		p = "p_values (null where the model reports none, key kept; a multiplicity block corrects the p-valued ones)"
	}
	return "RegressionResult in the built-in shape: coefficients with std_errors / statistics / " + p + ", the fit statistics it reports, and outputs keyed by output_schema."
}

// extensionRegressionMetas projects the snapshot's extension REG_*
// models onto the manifest's RegressionMeta shape — the built-ins' —
// sorted by name: the declared description, the accepted field types,
// the shared slot keys then the model's own params keys, the declared
// form (streamable; a Resample, Selection or robust covariance on the
// slot still forces the buffered path), the OutputSchema and the
// intents its Purpose declares. weighting reports an instance offering
// capability:weighting: a WeightAware model then carries both weight
// kinds.
func extensionRegressionMetas(s *ExtensionsSnapshot, weighting bool) []descriptor.RegressionMeta {
	if s == nil || len(s.Regressions) == 0 {
		return []descriptor.RegressionMeta{}
	}
	out := make([]descriptor.RegressionMeta, 0, len(s.Regressions))
	for _, m := range sortOperatorMeta(s.Regressions) {
		d := s.RegressionDetails[m.Name]
		hint := "Always buffered: the model fits over every admitted row (extend.BufferedRegression)."
		if m.Streamable {
			hint = "Streams row by row (extend.StreamingRegression); a Resample, Selection or robust covariance on the slot forces the buffered path, where the model is fed the buffered rows one at a time."
		}
		meta := descriptor.RegressionMeta{
			Name:           m.Name,
			Description:    m.Description,
			AcceptsTypes:   fieldaccept.ExtensionRegressionTypeNames(d.Accepts),
			EmitsTypeNote:  extensionRegressionEmitsNote(d.PValues),
			Streamable:     m.Streamable,
			StreamableHint: hint,
			Params:         append(extensionRegressionSlotParams(d), d.Params...),
			OutputSchema:   append([]descriptor.RegressionOutputMeta(nil), d.OutputSchema...),
		}
		if p, ok := s.PurposeOf(m.Name); ok && len(p.Intents) > 0 {
			meta.Intents = append([]string(nil), p.Intents...)
			sort.Strings(meta.Intents)
		}
		if weighting && m.WeightAware {
			meta.WeightKinds = []types.WeightKind{types.WeightKindFrequency, types.WeightKindProbability}
		}
		out = append(out, meta)
	}
	return out
}
