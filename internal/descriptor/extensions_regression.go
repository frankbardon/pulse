package descriptor

import (
	"fmt"
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

// ExtensionRegressionSlotRefusal is the declare-or-refuse rule for an
// extension REG_* model's shared RegressionSpec slots — ONE function
// predict (validateRegressions) and the runtime adapter both call, so
// the two refuse identically. A slot the registration does not declare
// it honours is PROCESSING_CONFIG with details {regression, slot}
// (regression = the spec's type, plus name when the spec carries one):
// today vcov: true without a declared Vcov. Nil when every set slot is
// declared.
func ExtensionRegressionSlotRefusal(spec *types.RegressionSpec, decl ExtensionRegression) *errors.CodedError {
	if spec == nil {
		return nil
	}
	if spec.Vcov && !decl.Vcov {
		details := map[string]any{"regression": string(spec.Type), "slot": "vcov"}
		if spec.Name != "" {
			details["name"] = spec.Name
		}
		return errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
			fmt.Sprintf("regression %s does not declare a coefficient covariance (its registration's Vcov is false); drop vcov: true", spec.Type),
			details)
	}
	return nil
}

// extensionRegressionSlotParams are the shared RegressionSpec fields an
// extension model reads off every slot, listed (marked Slot) ahead of
// its own params keys; vcov joins them when the registration declares
// it honours the covariance.
func extensionRegressionSlotParams(vcov bool) []descriptor.Param {
	out := []descriptor.Param{
		{Name: "target", Type: "field", Required: true, FieldFilter: "numeric", Description: "Response variable field name; a top-level key of the regression entry.", Slot: true},
		{Name: "predictors", Type: "list", Required: true, Description: "Predictor field names; a top-level key of the regression entry.", Slot: true},
	}
	if vcov {
		out = append(out, descriptor.Param{Name: "vcov", Type: "bool", Default: false, Description: "Emit the model's coefficient covariance (vcov) and its correlation; a top-level key of the regression entry.", Slot: true})
	}
	return out
}

// extensionRegressionAcceptsTypes is the scalar field types an
// extension model's target and predictors accept (the accepted-types
// table's rule for an operator it does not table), sorted.
func extensionRegressionAcceptsTypes(name string) []string {
	var out []string
	for b := 0; b < 256; b++ {
		ft := encoding.FieldType(b)
		if ft.IsKnown() && !ft.IsVector() && fieldaccept.Accepts(name, fieldaccept.SlotPredictor, ft) {
			out = append(out, ft.String())
		}
	}
	sort.Strings(out)
	return out
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
			AcceptsTypes:   extensionRegressionAcceptsTypes(m.Name),
			EmitsTypeNote:  "RegressionResult in the built-in shape: coefficients with std_errors / statistics / p_values (null where the model reports none, key kept), the fit statistics it reports, and outputs keyed by output_schema.",
			Streamable:     m.Streamable,
			StreamableHint: hint,
			Params:         append(extensionRegressionSlotParams(d.Vcov), d.Params...),
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
