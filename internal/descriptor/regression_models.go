package descriptor

import (
	"slices"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/fieldaccept"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/regmmm"
	"github.com/frankbardon/pulse/internal/regordinal"
	"github.com/frankbardon/pulse/types"
)

// regression_models.go is the no-execute declaration of the model
// regressions (model.ModelRegression): built-in REG_* types authored on
// the extension contract. internal/regadapt registers each one
// through the shared extension adapter on the engine's built-in registry
// driven by THIS declaration (regadapt.ModelRegressionAdapter), so
// every predict decision a registered extension regression takes from
// its snapshot declaration a model regression takes from this table —
// regressionExtensionFor answers it — and the runtime adapter judges the
// very same declaration.
// The manifest lists it among the built-ins (regressionCapabilities).

// modelRegression is one model regression's static declaration: the
// manifest description and the regression-only facts.
type modelRegression struct {
	Description string
	Decl        ExtensionRegression
	// SpecRefusal is the model factory's spec-only refusal (nil: the
	// factory builds), which predict raises as the runtime does.
	SpecRefusal func(*types.RegressionSpec) *errors.CodedError
	// WeightAware hands the slot's resolved weight to the model (the
	// adapter's admission and Record.Weight); its class — both kinds or
	// frequency only — is internal/weighting's regressionClasses row,
	// which predict's weight resolver reads. False: a weight in force is
	// refused by that table (no row).
	WeightAware bool
	// Target, when its Name is set, replaces the shared target slot's
	// manifest Param (a target accepting more than the numeric set:
	// REG_ORDINAL's categorical response).
	Target descriptor.Param
}

// modelRegressionTable holds every model regression, keyed by type.
var modelRegressionTable = map[types.RegressionType]modelRegression{
	types.REG_ORDINAL: {
		SpecRefusal: regordinal.SpecRefusal,
		Description: "Ordinal regression (proportional odds; MASS::polr): P(Y ≤ k | x) = F(ζ_k − xᵀβ); thresholds \"<level>|<next level>\" beside the predictors, no intercept.",
		WeightAware: true,
		Target:      descriptor.Param{Name: "target", Type: "field", NamesColumn: true, Required: true, FieldFilter: "any", Description: "Ordered response: a categorical or numeric field, 3 to 100 levels; a top-level key of the regression entry.", Slot: true},
		Decl: ExtensionRegression{
			Vcov:            true,
			PValues:         true,
			NoResample:      true,
			CovarianceTypes: regordinal.CovarianceTypes(),
			OutputKeys:      regordinal.OutputKeys(),
			OutputSchema:    slices.Clone(regordinal.OutputSchema),
			Params:          ordinalParams,
		},
	},
	types.REG_MMM_BAYES: {
		SpecRefusal: regmmm.BayesSpecRefusal,
		Description: "Bayesian media-mix model: in-model geometric adstock and Hill saturation per channel plus linear controls, sampled by NUTS; coefficients are posterior means with posterior SDs.",
		Decl: ExtensionRegression{
			Vcov: true,
			// No scorer (an adstocked prediction needs the history:
			// params.holdout_from is the model's own holdout), no
			// engine resample, no p-values.
			NoResample:   true,
			OutputKeys:   bayesOutputKeys(),
			OutputSchema: slices.Clone(regmmm.BayesOutputSchema),
			Params: []descriptor.Param{
				{Name: "channels", Type: "list", NamesColumn: true, Required: true, Description: "Raw spend predictors (≥ 0) the model adstocks and saturates, at least one; the other predictors are linear controls."},
				{Name: "time", Type: "string", NamesColumn: true, Required: true, Example: "week", Description: "Integer time field (one row per period, contiguous); the rows are sorted by it, and a gap or a repeat is refused."},
				{Name: "max_lag", Type: "float", Default: regmmm.BayesDefaultMaxLag, Description: "Adstock window in periods (an integer ≥ 1; default 13)."},
				{Name: "adstock", Type: "string", Default: "geometric", Description: "Adstock kernel: geometric (the only one)."},
				{Name: "priors", Type: "object", Description: "Per-channel priors keyed by channel: {roi | beta, decay, k, s}, each {family, ...} (normal mu/sigma, halfnormal sigma, lognormal mu/sigma, beta alpha/beta, truncnormal mu/sigma/lower/upper). A beta prior samples β instead of the ROI; k is in peak-week units of the channel's adstock."},
				{Name: "intercept_prior", Type: "object", Description: "Intercept prior on the max-scaled outcome (default normal 0, 2)."},
				{Name: "control_prior", Type: "object", Description: "Prior of every control's coefficient on the centred, scaled control (default normal 0, 1)."},
				{Name: "sigma_prior", Type: "object", Description: "Noise SD prior on the max-scaled outcome (default halfnormal 1)."},
				{Name: "chains", Type: "float", Default: regmmm.BayesDefaultChains, Description: "Sampler chains (1–16; default 4)."},
				{Name: "warmup", Type: "float", Default: regmmm.BayesDefaultWarmup, Description: "Adaptation iterations per chain (1–20000; default 1000)."},
				{Name: "draws", Type: "float", Default: regmmm.BayesDefaultDraws, Description: "Retained draws per chain (1–20000; default 1000)."},
				{Name: "credible_level", Type: "float", Default: regmmm.BayesDefaultCredible, Description: "Posterior interval level in (0, 1) (default 0.9)."},
				{Name: "sampler", Type: "string", Default: "nuts", Description: "Sampler: nuts (the only one)."},
				{Name: "target_accept", Type: "float", Default: regmmm.BayesDefaultTargetAccept, Description: "NUTS step-size adaptation target in (0, 1) (default 0.8); raise it when the fit diverges."},
				{Name: "holdout_from", Type: "float", Example: 131, Description: "Model-owned holdout: the integer time from which rows are held out — the likelihood sees only the rows before it, the adstock runs over the full history, and outputs.holdout scores the held-out rows. Absent: every row is fitted."},
			},
		},
	},
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
				{Name: "channels", Type: "list", NamesColumn: true, Required: true, Description: "Predictors entering through the Hill curve (adstocked spend, at least one); the other predictors are linear controls."},
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

// ordinalParams are REG_ORDINAL's params keys.
var ordinalParams = []descriptor.Param{
	{Name: "link", Type: "enum", EnumValues: regordinal.Links(), Default: regordinal.LinkLogit, Description: "Cumulative link F in P(Y ≤ k | x) = F(ζ_k − xᵀβ) (polr's method): logit (default), probit or cloglog."},
	{Name: "levels", Type: "list", Description: "Level order, lowest first: a categorical's labels or a number's values as written (\"2.5\"); 3 to 100, each once. A row whose response is unlisted is dropped (R's factor NA); a listed level no row carries fails the slot. Absent: dictionary order, or ascending values."},
	{Name: "brant", Type: "object", Description: "{}: adds outputs.brant, the Brant test of proportional odds (omnibus and per predictor; the brant package). Logit only (details.requires link logit)."},
}

// bayesOutputKeys are REG_MMM_BAYES's declared Outputs keys, in order.
func bayesOutputKeys() []string {
	keys := make([]string, 0, len(regmmm.BayesOutputSchema))
	for _, o := range regmmm.BayesOutputSchema {
		keys = append(keys, o.Key)
	}
	return keys
}

// ModelRegressionDeclaration returns model regression t's static
// declaration (the root registration's parity anchor) and whether t is
// one.
func ModelRegressionDeclaration(t types.RegressionType) (ExtensionRegression, bool) {
	m, ok := modelRegressionTable[t]
	return m.Decl, ok
}

// ModelRegressionWeightAware reports whether model regression t reads
// the slot's weight (the adapter hands it the resolved weight).
func ModelRegressionWeightAware(t types.RegressionType) bool {
	return modelRegressionTable[t].WeightAware
}

// ModelRegressionDescription returns model regression t's manifest
// description.
func ModelRegressionDescription(t types.RegressionType) string {
	return modelRegressionTable[t].Description
}

// modelRegressionFor returns model regression t's declaration in the
// snapshot's shape: an OperatorMeta (always buffered; its weight class
// is internal/weighting's table, not the OperatorMeta's WeightAware) and
// the regression-only facts.
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
			if p.Name == "target" && m.Target.Name != "" {
				p = m.Target
			}
			params = append(params, p)
			if p.Name == "resample" {
				// The resample knobs the engine's driver reads, which a
				// built-in declares one by one.
				params = append(params, modelResampleKnobs...)
			}
		}
		if m.WeightAware {
			params = append(params, descriptor.Param{Name: "weight", Type: "object", Description: "{field, kind}: frequency or probability row weight (REG_GLM's prior-weight rule; no two_log_lik / aic under probability); absent inherits the request's, null opts out.", Slot: true})
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
	{Name: "jackknife_by", Type: "field", NamesColumn: true, Description: "resample jackknife only: delete-a-cluster jackknife, one replicate per distinct value of this field (≥ 2); a null value drops the row listwise. Exclusive with jackknife_groups.", Slot: true},
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
