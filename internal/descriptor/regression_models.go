package descriptor

import (
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/fieldaccept"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/regmmm"
	"github.com/frankbardon/pulse/types"
)

// regression_models.go is the no-execute declaration of the model
// regressions (model.ModelRegression): built-in REG_* types authored on
// the extension contract. The root package registers each one through
// the extension adapter on the engine's built-in registry, so every
// predict decision a registered extension regression takes from its
// snapshot declaration a model regression takes from this table —
// regressionExtensionFor answers it — and the runtime adapter judges the
// same declaration (the root parity test pins the registration to it).
// The manifest lists it among the built-ins (regressionCapabilities).

// modelRegression is one model regression's static declaration: the
// manifest description and the regression-only facts.
type modelRegression struct {
	Description string
	Decl        ExtensionRegression
	// SpecRefusal is the model factory's spec-only refusal (nil: the
	// factory builds), which predict raises as the runtime does.
	SpecRefusal func(*types.RegressionSpec) *errors.CodedError
}

// modelRegressionTable holds every model regression, keyed by type.
var modelRegressionTable = map[types.RegressionType]modelRegression{
	types.REG_MMM_HILL: {
		SpecRefusal: regmmm.SpecRefusal,
		Description: "Media-mix model: joint nonlinear least squares over Hill-saturated, already-adstocked channels plus linear controls; coefficients k:<channel> / s:<channel> carry each curve.",
		Decl: ExtensionRegression{
			Vcov: true,
			// The model reports no p-value itself; under resample the
			// engine writes the replicate ones.
			PValues:    true,
			Scorer:     true,
			OutputKeys: []string{"k", "s"},
			Params: []descriptor.Param{
				{Name: "channels", Type: "list", Required: true, Description: "Predictors entering through the Hill curve (adstocked spend, at least one); the other predictors are linear controls."},
				{Name: "nonnegative", Type: "bool", Default: true, Description: "Bound every channel β below by 0, so an unsupported channel is exactly 0 (default true)."},
				{Name: "prior", Type: "object", Description: "Gaussian MAP prior {channel, mean, precision} on one channel's β (an experiment-measured effect)."},
			},
			OutputSchema: []descriptor.RegressionOutputMeta{
				{Key: "k", JSONType: "object", Description: "Hill half-saturation per channel (the coefficient k:<channel>)."},
				{Key: "s", JSONType: "object", Description: "Hill shape per channel (the coefficient s:<channel>)."},
			},
		},
	},
}

// ModelRegressionDeclaration returns model regression t's static
// declaration (the root registration's parity anchor) and whether t is
// one.
func ModelRegressionDeclaration(t types.RegressionType) (ExtensionRegression, bool) {
	m, ok := modelRegressionTable[t]
	return m.Decl, ok
}

// ModelRegressionDescription returns model regression t's manifest
// description.
func ModelRegressionDescription(t types.RegressionType) string {
	return modelRegressionTable[t].Description
}

// modelRegressionFor returns model regression t's declaration in the
// snapshot's shape: an OperatorMeta (always buffered, not WeightAware)
// and the regression-only facts.
func modelRegressionFor(t types.RegressionType) (descriptor.OperatorMeta, ExtensionRegression, bool) {
	if !model.ModelRegression(t) {
		return descriptor.OperatorMeta{}, ExtensionRegression{}, false
	}
	m, ok := modelRegressionTable[t]
	if !ok {
		return descriptor.OperatorMeta{}, ExtensionRegression{}, false
	}
	return descriptor.OperatorMeta{Name: string(t), Description: m.Description}, m.Decl, true
}

// modelRegressionMetas are the model regressions' manifest rows, in
// types.AllRegressionTypes order: the extension projection's shape
// (extensionRegressionMetas) — the shared slots the declaration accepts
// marked Slot, then the model's own params keys — under the built-in
// accepted-types table, its result name slot and the buffered form.
func modelRegressionMetas() []descriptor.RegressionMeta {
	var out []descriptor.RegressionMeta
	for _, t := range model.ModelRegressions() {
		m := modelRegressionTable[t]
		d := m.Decl
		var params []descriptor.Param
		for _, p := range extensionRegressionSlotParams(d) {
			params = append(params, p)
			if p.Name == "resample" {
				// The resample knobs the engine's driver reads, which a
				// built-in declares one by one.
				params = append(params, modelResampleKnobs...)
			}
		}
		params = append(params, descriptor.Param{Name: "name", Type: "string", Description: "Result alias; also the saved model's name (default: the save file's stem).", Slot: true})
		out = append(out, descriptor.RegressionMeta{
			Name:           string(t),
			Description:    m.Description,
			AcceptsTypes:   fieldaccept.TypeNames(string(t), fieldaccept.SlotPredictor),
			EmitsTypeNote:  extensionRegressionEmitsNote(d.PValues),
			Streamable:     t.Streamable(),
			StreamableHint: "Always buffered: the model fits over every admitted row.",
			Params:         append(params, d.Params...),
			OutputSchema:   append([]descriptor.RegressionOutputMeta(nil), d.OutputSchema...),
		})
	}
	return out
}

// modelResampleKnobs are the resample knobs a model regression's
// manifest row declares beside resample (the built-ins' wording): the
// engine's jackknife / bootstrap driver reads them for every model.
var modelResampleKnobs = []descriptor.Param{
	{Name: "bootstrap_iters", Type: "int", Default: 1000, Description: "Replicate count when resample == bootstrap, used as given; <= 0 means 1000. Echoed as bootstrap_requested beside bootstrap_succeeded (replicates whose refit succeeded).", Slot: true},
	{Name: "jackknife_groups", Type: "int", Description: "resample jackknife only: delete-a-group jackknife over g groups (2 ≤ g ≤ fitted rows) drawn by rng_seed — g refits instead of one per row. Exclusive with jackknife_by (PROCESSING_CONFIG); absent = leave-one-out over the fitted rows.", Slot: true},
	{Name: "jackknife_by", Type: "field", Description: "resample jackknife only: delete-a-cluster jackknife, one replicate per distinct value of this field (≥ 2); a null value drops the row listwise. Exclusive with jackknife_groups.", Slot: true},
	{Name: "rng_seed", Type: "int", Description: "Seed for the bootstrap RNG and the jackknife_groups assignment; 0 derives a deterministic seed from the request and its rows.", Slot: true},
}

// modelRegressionSpecRefusal is model regression t's spec-only factory
// refusal of reg (nil for any other type, or when the factory builds).
func modelRegressionSpecRefusal(t types.RegressionType, reg *types.RegressionSpec) *errors.CodedError {
	m, ok := modelRegressionTable[t]
	if !ok || m.SpecRefusal == nil {
		return nil
	}
	return m.SpecRefusal(reg)
}
