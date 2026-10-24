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

	// Save writes the bound fit to a file at this path once the fit has
	// run, under the fit's Name — the RegressionSpec.Save contract
	// (root, atomic write, PULSE_MODEL_OVERWRITTEN warning). A
	// regression fit writes a model file (the path ends
	// .pulse-model.json); a vector fit writes a population file (the
	// path ends .pulse-population.json) that a later request loads with
	// `populations`. Gated by capability:model_save. Empty writes
	// nothing.
	Save string `json:"save,omitempty"`
}

// PopulationRef is one entry of Request.Populations: a population file
// a vector fit's `save` wrote, loaded under Name for this request. The
// name joins the request's bound-model namespace — the one a Compose
// `fits` list and the `models` entries bind — as a vector fit, so a
// fitted attribute's or MAT_* slot's params.fit resolves it exactly
// like a Compose vector fit of the same name; a name bound twice (by a
// `models` entry, another population or a fit) is
// PULSE_MODEL_NAME_COLLISION.
//
// The file is read (through the instance filesystem) and decoded
// before any record is read — in a Compose batch or a chain, before any
// fit or slot runs. Its provenance is informational: a population
// fitted on one cohort scores any cohort holding columns its member
// labels align to (by label, never by position).
type PopulationRef struct {
	// Name binds the population for the request. Required; unique
	// across the request's models, populations and any Compose fit.
	Name string `json:"name"`

	// Path is the population file, relative to the data root, ending
	// .pulse-population.json — the save-path rule.
	Path string `json:"path"`
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
	// FitKindSEM is a structural equation model fit — a fit request
	// holding exactly one MAT_SEM matrix: its fitted parameter matrices
	// per group. ATTR_SEM_SCORE's params.fit reads it.
	FitKindSEM FitKind = "sem"
)

// FitResult is one ComposedResponse.Fits entry. Exactly one arm is set:
// a regression fit's result (the embedded *RegressionResult — its
// fields promote, so fits[name].Coefficients reads as before), a
// vector fit's (Vector) or a SEM fit's (SEM).
//
// On the wire a regression entry is the RegressionResult object itself,
// byte-identical to the regression-only shape; a vector entry is
// {"vector": VectorFitResult} and a SEM entry {"sem": SEMFitResult}.
// The `vector` / `sem` key is the discriminator: a regression entry
// never carries either.
//
// A fit that failed on its data keeps its entry: the arm carries
// `error` (SlotError) beside its identity — {name, type, error} for a
// regression, {"vector": {operator, error}} for a vector fit,
// {"sem": {operator, error}} for a SEM fit — and its name binds no
// model, so a slot scoring from it reads no state.
type FitResult struct {
	// RegressionResult is a regression fit's result (nil on a vector
	// or SEM fit).
	*RegressionResult

	// Vector is a vector fit's summary (nil on any other fit).
	Vector *VectorFitResult `json:"vector,omitempty"`

	// SEM is a SEM fit's fitted parameters (nil on any other fit).
	SEM *SEMFitResult `json:"sem,omitempty"`
}

// MarshalJSON writes the regression arm as its RegressionResult (the
// regression-only wire shape), the vector arm as {"vector": …} and the
// SEM arm as {"sem": …}, every non-finite float as null
// (MarshalFinite).
func (f FitResult) MarshalJSON() ([]byte, error) {
	switch {
	case f.SEM != nil:
		return MarshalFinite(struct {
			SEM *SEMFitResult `json:"sem"`
		}{f.SEM})
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

	// Error is set when the fit failed on its data (its battery's
	// data-dependent refusal — a singular or non-PSD matrix, too few
	// rows): the entry keeps Operator and the error, every other figure
	// absent on the wire, and nothing is bound under the fit's name — a
	// consumer of it reads no state (see SlotError).
	Error *SlotError `json:"error,omitempty"`
}

// slotFailureVectorFit is the wire form of a VectorFitResult carrying
// an Error: its operator and the error, no figure.
type slotFailureVectorFit struct {
	Operator string     `json:"operator"`
	Error    *SlotError `json:"error"`
}

// MarshalJSON writes the summary with every non-finite float as null;
// see MarshalFinite. A failed fit writes {operator, error} only.
func (v VectorFitResult) MarshalJSON() ([]byte, error) {
	if v.Error != nil {
		return MarshalFinite(slotFailureVectorFit{Operator: v.Operator, Error: v.Error})
	}
	type alias VectorFitResult
	return MarshalFinite((*alias)(&v))
}

// SEMFitResult is a SEM fit's ComposedResponse.Fits entry: everything
// a scoring slot (ATTR_SEM_SCORE's params.fit) reads, at full
// precision — the model as fitted, its options, the member labels a
// consuming slot is aligned to (by label, never by position) and each
// group's parameter matrices at the estimate. Like every fit it sits
// outside the Compose `return` — never rounded.
//
// The matrices follow the model's own variable orders: Lambda is
// Observed × Inner, Beta and Psi Inner × Inner, Theta Observed ×
// Observed, Nu and SampleMean over Observed, Alpha over Inner; fixed
// values are included. Inner holds the latents and every observed
// variable a structural (`~`) statement makes an inner variable.
type SEMFitResult struct {
	// Operator is the fit request's battery: MAT_SEM.
	Operator string `json:"operator"`

	// Model is the model description fitted (params.model, or the
	// one-factor default over every member).
	Model string `json:"model"`

	// Labels are the member labels in member order — the names the
	// model's observed variables are — and Members the member columns.
	Labels  []string `json:"labels"`
	Members []string `json:"members"`

	// Observed and Inner are the model's variable orders (the matrices'
	// rows and columns); Latents its latent variables in Inner order.
	Observed []string `json:"observed"`
	Inner    []string `json:"inner"`
	Latents  []string `json:"latents"`

	// Estimator is the fit's estimator ("ml"); Conventions "lavaan" or
	// "semopy" — the fit's convention set.
	Estimator   string `json:"estimator"`
	Conventions string `json:"conventions"`

	// MeanStructure reports params.meanstructure; Group,
	// GroupReference, GroupEqual and GroupPartial are the multi-group
	// knobs (absent on an ungrouped fit). Solver and Information are
	// the solver settings the fit ran under.
	MeanStructure  bool     `json:"meanstructure"`
	Group          string   `json:"group,omitempty"`
	GroupReference string   `json:"group_reference,omitempty"`
	GroupEqual     []string `json:"group_equal,omitempty"`
	GroupPartial   []string `json:"group_partial,omitempty"`
	Solver         string   `json:"solver"`
	Information    string   `json:"information"`

	// Basis is the weight basis: "unweighted" or "frequency".
	Basis string `json:"basis"`

	// N counts the listwise-complete fit rows (every group); SumWeights
	// is Σw over them (N unweighted) — the fit's sample size.
	N          int64   `json:"n"`
	SumWeights float64 `json:"sum_weights"`

	// DoF and NParams are the fit's degrees of freedom and free
	// parameter count; Converged, Status and Iterations its solver's
	// stop. NearestPDMinEigenvalue is set when the fit used the nearest
	// positive-definite substitute (params.singular "nearest").
	DoF                    float64  `json:"dof"`
	NParams                int      `json:"n_params"`
	Converged              bool     `json:"converged"`
	Status                 string   `json:"status"`
	Iterations             int      `json:"iterations"`
	NearestPDMinEigenvalue *float64 `json:"nearest_pd_min_eigenvalue,omitempty"`

	// Groups are the fit's groups in level order: one entry ungrouped
	// (Level absent), one per level with fit rows under params.group.
	Groups []SEMFitGroup `json:"groups"`

	// Error is set when the fit failed on its data (MAT_SEM's
	// data-dependent refusal): the entry keeps Operator and the error,
	// every other figure absent on the wire, and nothing is bound under
	// the fit's name (see SlotError).
	Error *SlotError `json:"error,omitempty"`
}

// SEMFitGroup is one group's parameters in a SEMFitResult.
type SEMFitGroup struct {
	// Level is the group's level label (absent on an ungrouped fit).
	Level string `json:"level,omitempty"`

	// N counts the group's fit rows; SumWeights is Σw over them.
	N          int64   `json:"n"`
	SumWeights float64 `json:"sum_weights"`

	// The parameter matrices at the estimate, fixed values included.
	Lambda [][]float64 `json:"lambda"`
	Beta   [][]float64 `json:"beta"`
	Psi    [][]float64 `json:"psi"`
	Theta  [][]float64 `json:"theta"`
	Nu     []float64   `json:"nu"`
	Alpha  []float64   `json:"alpha"`

	// SampleMean is the group's fit-row mean over Observed — the center
	// of a covariance-only fit's scores.
	SampleMean []float64 `json:"sample_mean"`
}

// slotFailureSEMFit is the wire form of a SEMFitResult carrying an
// Error: its operator and the error, no figure.
type slotFailureSEMFit struct {
	Operator string     `json:"operator"`
	Error    *SlotError `json:"error"`
}

// MarshalJSON writes the fit with every non-finite float as null; see
// MarshalFinite. A failed fit writes {operator, error} only.
func (v SEMFitResult) MarshalJSON() ([]byte, error) {
	if v.Error != nil {
		return MarshalFinite(slotFailureSEMFit{Operator: v.Operator, Error: v.Error})
	}
	type alias SEMFitResult
	return MarshalFinite((*alias)(&v))
}
