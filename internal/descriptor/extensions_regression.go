package descriptor

import (
	"fmt"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
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
