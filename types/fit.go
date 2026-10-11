package types

// FitSpec is one entry of ComposedRequest.Fits: a named regression fit
// that runs ONCE, before any Compose slot, so every slot — sweep-expanded
// ones included — reads the same fitted coefficients by Name instead of
// refitting or retyping them.
//
// Request is an ordinary Request holding exactly one regression
// (Regressions has one entry) and no groups or crosstab; its own
// cohort, filters, features and weight decide the rows the model is fit
// on, independently of any slot that later reads it. The fit's
// RegressionResult surfaces as ComposedResponse.Fits[Name].
//
// It rides as the optional `fits` slot on ComposedRequest, gated by
// capability:compose_fits. A nil / empty list is absent: the slot is
// `omitempty`, so a fit-free request is byte-identical on the wire and
// under CanonicalHash.
type FitSpec struct {
	// Name binds the fit for the batch. Required and unique among the
	// fits (one namespace with any loaded model).
	Name string `json:"name"`

	// Request is the request the fit runs: exactly one regression, no
	// groups, no crosstab.
	Request *Request `json:"request"`

	// Save writes the bound model as a model file at this path once the
	// fit has run, under the fit's Name — the RegressionSpec.Save
	// contract (suffix, root, atomic write, overwrite warning). Gated by
	// capability:model_save. Empty writes nothing.
	Save string `json:"save,omitempty"`
}

// ModelRef is one entry of Request.Models (and FacetRequest.Models): a
// model file a `save` wrote, loaded under Name for this request. The
// name joins the request's bound-model namespace — the one a Compose
// `fits` list binds — so ATTR_REG_PREDICT `fit` and coef() resolve it
// exactly like a fit of the same name; a name bound twice (by another
// entry or a fit) is PULSE_MODEL_NAME_COLLISION.
//
// The file is read (through the instance filesystem) and decoded before
// any record is read — in a Compose batch or a chain, before any fit
// or slot runs, so a load always sees the file as it was before the
// batch. Its provenance is informational: a model trained on one cohort
// scores any cohort holding its predictor columns.
type ModelRef struct {
	// Name binds the model for the request. Required; unique across
	// the request's models and any Compose fit.
	Name string `json:"name"`

	// Path is the model file, relative to the data root, ending
	// .pulse-model.json — the save-path rule.
	Path string `json:"path"`
}
