package types

// FitSpec is one entry of ComposedRequest.Fits: a named fit that runs
// ONCE, before any Compose slot, so every slot — sweep-expanded ones
// included — reads the same fitted figures by Name instead of refitting
// or retyping them.
//
// Request is an ordinary Request with no groups or crosstab holding
// exactly ONE of: a regression (Regressions has one entry — a
// regression fit), or a vector battery (no regression, and exactly one
// Matrices spec or one fitted vector attribute — ATTR_MAHALANOBIS,
// ATTR_PC_SCORE, ATTR_FACTOR_SCORE — a vector fit: the battery's
// listwise mean and co-moment). Its own cohort, filters, features and
// weight decide the rows the fit is taken on, independently of any slot
// that later reads it. The result surfaces as ComposedResponse.Fits[Name]:
// a regression fit's RegressionResult, or a vector fit's
// VectorFitResult (FitResult).
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
	// capability:model_save. Empty writes nothing. A regression fit
	// only: a vector fit with a save is refused (PULSE_FIT_INVALID
	// reason vector_save).
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

// FitKind names the kind of a bound fit. The one model namespace holds
// both kinds; a reference that reads one kind refuses a name bound as
// the other (PULSE_MODEL_KIND_MISMATCH).
type FitKind string

// The fit kinds.
const (
	// FitKindRegression is a regression fit (or a loaded model file):
	// ATTR_REG_PREDICT and coef() read it.
	FitKindRegression FitKind = "regression"
	// FitKindVector is a vector fit — a battery's listwise mean and
	// co-moment: a fitted vector attribute's or MAT_* slot's params.fit
	// reads it.
	FitKindVector FitKind = "vector"
)

// FitResult is one ComposedResponse.Fits entry. Exactly one arm is set:
// a regression fit's result (the embedded *RegressionResult — its
// fields promote, so fits[name].Coefficients reads as before), or a
// vector fit's (Vector).
//
// On the wire a regression entry is the RegressionResult object itself,
// byte-identical to the regression-only shape; a vector entry is
// {"vector": VectorFitResult}. The `vector` key is the discriminator: a
// regression entry never carries it.
type FitResult struct {
	// RegressionResult is a regression fit's result (nil on a vector
	// fit).
	*RegressionResult

	// Vector is a vector fit's summary (nil on a regression fit).
	Vector *VectorFitResult `json:"vector,omitempty"`
}

// MarshalJSON writes the regression arm as its RegressionResult (the
// regression-only wire shape) and the vector arm as {"vector": …},
// every non-finite float as null (MarshalFinite).
func (f FitResult) MarshalJSON() ([]byte, error) {
	switch {
	case f.Vector != nil:
		return MarshalFinite(struct {
			Vector *VectorFitResult `json:"vector"`
		}{f.Vector})
	case f.RegressionResult != nil:
		return MarshalFinite(f.RegressionResult)
	}
	return []byte("null"), nil
}

// VectorFitResult is a vector fit's ComposedResponse.Fits summary: the
// fitted state a consuming slot (a fitted attribute's or MAT_* slot's
// params.fit) reads, at full precision. Like a regression fit's result
// it sits outside the Compose `return` — never rounded.
//
// The covariance is the fit's own: M2*/(N* − 1) on its weight basis (N*
// = Σw unweighted or under frequency weights, Kish's n_eff under
// probability weights) — the matrix ATTR_MAHALANOBIS inverts. Mean and
// covariance cells are null where undefined (no mass; N* ≤ 1).
type VectorFitResult struct {
	// Operator is the battery the fit was taken from: the fit request's
	// MAT_* type or fitted attribute type.
	Operator string `json:"operator"`

	// Labels are the member labels in axis order — the names a
	// consuming slot is aligned to, by label, never by position.
	Labels []string `json:"labels"`

	// Members are the member columns in axis order.
	Members []string `json:"members"`

	// Basis is the weight basis: "unweighted", "frequency" or
	// "probability".
	Basis string `json:"basis"`

	// N counts the listwise-complete fit rows with a valid weight
	// (zero weights included); NWeightInvalid the rows an invalid
	// weight skipped.
	N              int64 `json:"n"`
	NWeightInvalid int64 `json:"n_weight_invalid"`

	// SumWeights is Σw over the rows N counts (N unweighted); NStar is
	// the fit's N*.
	SumWeights float64 `json:"sum_weights"`
	NStar      float64 `json:"n_star"`

	// Mean is the (weighted) member mean, axis order.
	Mean []float64 `json:"mean"`

	// Covariance is the fit's covariance over Labels (square
	// symmetric, full encoding; RowKeys = ColumnKeys = Labels).
	Covariance *MatrixValues `json:"covariance"`
}

// MarshalJSON writes the summary with every non-finite float as null;
// see MarshalFinite.
func (v VectorFitResult) MarshalJSON() ([]byte, error) {
	type alias VectorFitResult
	return MarshalFinite((*alias)(&v))
}
