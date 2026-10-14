package vectors

import (
	"bytes"
	"encoding/json"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/linalg/metric"
	"github.com/frankbardon/pulse/types"
)

// Matrix is one Request.Matrices spec after resolution: its effective
// name, its operator, its members (the vector it names, or its inline
// fields resolved by the vector rules) and its decoded parameters.
type Matrix struct {
	// Index is the spec's position in Request.Matrices.
	Index int
	// Name is the result name (MatrixSpec.EffectiveName).
	Name string
	// Type is the operator.
	Type types.MatrixType
	// Members are the axis members, with their labels and coercion.
	Members Resolved
	// ExplicitLabels reports whether the members carry caller-supplied
	// display labels (otherwise Members.Labels are the member names and
	// the result omits them).
	ExplicitLabels bool
	// Encoding is the effective Values layout.
	Encoding types.MatrixEncoding
	// Streamable and Mergeable are the spec-level answers
	// (types.MatrixSpec.Streamable / Mergeable — the type folded with
	// its params), copied at resolution so the engine, predict and the
	// routing gates read one rule. A non-streamable matrix is BUFFERED:
	// its slot keeps the admitted rows for a finalizer that needs them
	// all (a rank method).
	Streamable bool
	Mergeable  bool
	// DDOF is MAT_COVARIANCE's delta degrees of freedom (default 1).
	DDOF int
	// Pairwise reports params.missing "pairwise": each pair is computed
	// over the rows where both members are present. False is the
	// default "listwise": a row with any member null is dropped.
	Pairwise bool
	// MaxDropShare is params.max_drop_share (listwise only; no
	// default): when the share of filter-passing rows listwise drops
	// exceeds it, the result carries PULSE_MATRIX_LISTWISE_HEAVY_DROP.
	// Nil when unset.
	MaxDropShare *float64
	// Method is MAT_CORRELATION's params.method — CorrelationPearson
	// (the default), CorrelationSpearman or CorrelationKendall; "" on
	// every other type. A rank method is buffered and not mergeable
	// (Streamable / Mergeable false).
	Method string
	// TopPairs is MAT_CORRELATION's params.summary.top_pairs: the
	// number of strongest off-diagonal pairs the result lists under
	// vectors.top_pairs. 0 (the default) emits no summary.
	TopPairs int
	// Controls is MAT_PARTIAL_CORRELATION's params.control named list,
	// in the caller's order: nil is "all" (each pair controls for every
	// other member). A control may be a member (it leaves the output
	// axis) or any other numeric field (it joins the fold: Extra).
	Controls []string
	// Extra are the control fields outside the members, in params
	// order: the slot folds them after the members (Columns), so they
	// join the co-moment and the missing-data mode.
	Extra []string
	// Output are the output axis positions in Columns — the members
	// that are not controls, in axis order. Nil means every member
	// (Columns is then exactly the members).
	Output []int
	// Repair is a decomposition operator's params.repair: "" (refuse a
	// non-PSD input with PULSE_MATRIX_NOT_PSD) or RepairNearest.
	Repair string
	// Reverse are MAT_RELIABILITY's reverse-keyed items as member
	// positions, ascending (params.reverse, de-duplicated by name): the
	// slot folds x' = ScaleMin + ScaleMax − x for each before the
	// co-moment. Nil when nothing is reversed.
	Reverse []int
	// HasScale reports a declared battery-wide params.scale_min /
	// scale_max (both or neither; required with a reverse list) — or,
	// with neither, the kind "scale" vector's declared bounds: every
	// present member value must lie in [ScaleMin, ScaleMax], or the run
	// is refused with PROCESSING_CONFIG — never clamped, never inferred
	// from the data.
	HasScale           bool
	ScaleMin, ScaleMax float64
	// reverseNames carries params.reverse from decode to
	// resolveReliability (members resolve in between).
	reverseNames []string
	// On is MAT_PCA's params.on: PCAOnCorrelation (the default) or
	// PCAOnCovariance; "" on every other type.
	On string
	// Components is MAT_PCA's retention rule (params.components).
	Components PCAComponents
	// Rotation is MAT_PCA's params.rotation: "" (none, the default) or
	// RotationVarimax.
	Rotation string
	// Factors is MAT_FACTOR's params.factors: a fixed K or the Kaiser
	// rule (the default).
	Factors FactorCount
	// Extraction is MAT_FACTOR's params.extraction:
	// FactorExtractionMinres (the default), FactorExtractionPrincipalAxis
	// or FactorExtractionML; "" on every other type.
	Extraction string
	// Kappa is MAT_FACTOR's promax power (params.kappa, default
	// FactorDefaultKappa; promax only) and Gamma its oblimin γ
	// (params.gamma, default 0; oblimin only).
	Kappa, Gamma float64
	// ConfLevel is MAT_FACTOR's params.conf_level: the coverage of the
	// ml RMSEA interval (rmsea_lower / rmsea_upper), FactorDefaultConfLevel
	// unless set; extraction ml only, 0 on every other type.
	ConfLevel float64
	// Center is MAT_COLLINEARITY's params.center: false (the default)
	// runs Belsley's diagnostics on the scaled, UNCENTERED predictors
	// with an intercept column; true on the scaled, centered ones (the
	// predictors' correlation, no intercept).
	Center bool
	// Between is MAT_DISTANCE's resolved params.between grouper: a copy
	// ready for the grouper factory — a GROUP_KMEANS slot's members
	// moved into Fields (a named vector resolved), Vector cleared. Nil
	// on every other type. The engine sets its Weight from the slot's
	// stamped weight (GROUP_KMEANS only: the other groupers' keys do not
	// read a weight).
	Between *types.Group
	// Metric is MAT_DISTANCE's params.metric, a linalg/metric registry
	// name the MAT_DISTANCE consumer accepts (DistanceDefaultMetric by
	// default); "" on every other type.
	Metric string
	// MinValid is MAT_DISTANCE's params.min_valid: the fewest complete
	// member pairs (coordinates defined on both centroids) a distance
	// needs; below it the cell is null. 1 by default; 0 on every other
	// type.
	MinValid int
	// Standardize is MAT_DISTANCE's params.standardize: z-score every
	// member over the bucketed rows before the centroids are compared.
	Standardize bool
	// betweenRaw carries params.between from decode to resolveDistance
	// (members resolve in between).
	betweenRaw json.RawMessage
	// Inference is params.inference on MAT_CORRELATION (every method)
	// and MAT_PARTIAL_CORRELATION: the result carries per-pair
	// auxiliary.p, auxiliary.ci_lower and auxiliary.ci_upper. False
	// (the default) builds none of them.
	Inference bool
	// Alpha is params.alpha, the two-sided level of the inference
	// confidence intervals (coverage 1 − Alpha): InferenceDefaultAlpha
	// under Inference, 0 otherwise.
	Alpha float64
	// Extension reports an embedder-registered MAT_* operator
	// (MatrixResolve.Extension). Its params are opaque to Pulse: the
	// engine-owned missing / max_drop_share are decoded onto Pairwise /
	// MaxDropShare exactly as for a built-in and stripped, and the
	// remaining keys ride ExtParams (nil when none is left) to the
	// operator's factory and Finalize. Streamable / Mergeable are its
	// DECLARED tier (a co-moment operator merges through the blocked
	// merge; a buffered one keeps rows and runs serially).
	Extension bool
	// ExtParams is an extension operator's params object without the
	// engine-owned keys; nil on a built-in.
	ExtParams json.RawMessage
	// ExtAuxiliary is an extension operator's declared auxiliary keys
	// (its registration's OutputKeys.Auxiliary), AuxiliaryKeys' answer.
	ExtAuxiliary []string
	// Fit is params.fit: the bound vector fit (a Compose `fits` entry of
	// kind vector) the slot emits from instead of folding its rows —
	// aligned to Columns' labels (FitLabels) by label, its own weight
	// basis governing. "" folds the filtered rows (the default).
	Fit string
}

// FitLabels are the labels a bound vector fit aligns to, in Columns
// order: each member's display label (its name when it carries none),
// then each control outside the members (MAT_PARTIAL_CORRELATION) by
// name. A vector fit captured from a matrix battery is stored under the
// same labels.
func (m Matrix) FitLabels() []string {
	cols := m.Columns()
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = c
		if i < len(m.Members.Members) && i < len(m.Members.Labels) && m.Members.Labels[i] != "" {
			out[i] = m.Members.Labels[i]
		}
	}
	return out
}

// MatrixExtension is what resolution needs to know about an
// embedder-registered MAT_* operator: its declared tier, the member
// column types it reads (empty: the built-in member types the vector
// rules admit) and its declared auxiliary keys.
type MatrixExtension struct {
	Streamable bool
	Accepts    []encoding.FieldType
	Auxiliary  []string
}

// MatrixResolve parameterises ResolveMatricesWith: Known, Route and
// BetweenExt are ResolveMatricesRouted's known / route / ext; Extension
// reports a registered (and offered) extension MAT_* operator, which
// resolves even when Known refuses it. Nil Extension registers none.
type MatrixResolve struct {
	Known      func(types.MatrixType) bool
	Route      func(types.GroupType) types.GroupType
	BetweenExt func(string) bool
	Extension  func(types.MatrixType) (MatrixExtension, bool)
}

// AuxiliaryKeys lists the matrices[*].auxiliary keys m's finalizer
// emits under its params, in a fixed order — the ONE list the predict
// size model (matrixAuxiliaryNodes) and the runtime's per-spec
// auxiliary compute gate (processing.ComputePlan) read, so predict
// sizes and the computed auxiliaries cannot drift. The multiplicity
// twins (p_adjusted, significant_adjusted) are written later by the
// service fold and are not listed. known is false for an operator this
// list does not model (every built-in is modelled): a caller then
// assumes it may emit any key.
//
//   - n: params.missing pairwise, every operator but MAT_DISTANCE.
//   - p, ci_lower, ci_upper: params.inference (MAT_CORRELATION /
//     MAT_PARTIAL_CORRELATION).
//   - MAT_PCA: eigenvectors; unrotated under a rotation.
//   - MAT_FACTOR: unrotated under a rotation; factor_correlation under
//     an oblique one.
//   - MAT_COLLINEARITY: variance_decomposition.
//   - MAT_DISTANCE: centroids; centroids_standardized under
//     standardize; pooled_covariance under a fitted inverse-covariance
//     metric.
func (m Matrix) AuxiliaryKeys() (keys []string, known bool) {
	if m.Extension {
		// An extension operator's declared auxiliary keys: the probe
		// and the run-time output check hold Finalize to them.
		return append([]string(nil), m.ExtAuxiliary...), true
	}
	if !slices.Contains(types.AllMatrixTypes(), m.Type) {
		return nil, false
	}
	if m.Pairwise && m.Type != types.MAT_DISTANCE {
		keys = append(keys, "n")
	}
	if m.Inference {
		keys = append(keys, "p", "ci_lower", "ci_upper")
	}
	switch m.Type {
	case types.MAT_PCA:
		keys = append(keys, "eigenvectors")
		if m.Rotation != "" {
			keys = append(keys, "unrotated")
		}
	case types.MAT_FACTOR:
		if m.Rotation != "" {
			keys = append(keys, "unrotated")
			if m.Rotation != RotationVarimax {
				keys = append(keys, "factor_correlation")
			}
		}
	case types.MAT_COLLINEARITY:
		keys = append(keys, "variance_decomposition")
	case types.MAT_DISTANCE:
		keys = append(keys, "centroids")
		if m.Standardize {
			keys = append(keys, "centroids_standardized")
		}
		if met, err := metric.ResolveFor(metric.ConsumerMatDistance, m.Metric, metric.Distance); err == nil && met.Fit() == metric.FitInverseCovariance {
			keys = append(keys, "pooled_covariance")
		}
	}
	return keys, true
}

// InferenceDefaultAlpha is params.alpha's default: 95% intervals.
const InferenceDefaultAlpha = 0.05

// InferenceTypes are the matrix operators that take params.inference.
func InferenceTypes() []types.MatrixType {
	return []types.MatrixType{types.MAT_CORRELATION, types.MAT_PARTIAL_CORRELATION}
}

// DistanceDefaultMetric is MAT_DISTANCE's params.metric default. The
// accepted names are the linalg/metric registry's MAT_DISTANCE consumer
// list (metric.Accepted(metric.ConsumerMatDistance)): there is no local
// metric list.
const DistanceDefaultMetric = "euclidean"

// DistanceBetweenTypes returns the built-in groupers MAT_DISTANCE's
// params.between accepts, in AllGroupTypes order: every built-in. The
// date family (GROUP_DATE, GROUP_DATE_RANGES) reads its zone like a
// top-level grouper — the zone pass walks the between as the slot
// "matrices[i].params.between" (internal/descriptor.ResolveZones) and
// ZonedRequest writes an inherited zone into its `tz`. A registered
// extension grouper is accepted too (ResolveMatricesRouted's ext).
func DistanceBetweenTypes() []types.GroupType {
	return types.AllGroupTypes()
}

// IsDateBetween reports whether t is a date-family grouper, the only
// between types that accept a `tz`.
func IsDateBetween(t types.GroupType) bool {
	return t == types.GROUP_DATE || t == types.GROUP_DATE_RANGES
}

// MAT_PCA params.on values.
const (
	PCAOnCorrelation = "correlation"
	PCAOnCovariance  = "covariance"
)

// PCAOnValues returns the params.on values, default first.
func PCAOnValues() []string { return []string{PCAOnCorrelation, PCAOnCovariance} }

// Rotation criteria of the shared rotation engine
// (internal/processing/matrix_rotate.go). RotationNone is the explicit
// "no rotation" spelling of a params.rotation knob.
const (
	RotationNone    = "none"
	RotationVarimax = "varimax"
	RotationPromax  = "promax"
	RotationOblimin = "oblimin"
)

// PCARotations returns MAT_PCA's params.rotation values, default first.
func PCARotations() []string { return []string{RotationNone, RotationVarimax} }

// FactorRotations returns MAT_FACTOR's params.rotation values, default
// first.
func FactorRotations() []string {
	return []string{RotationNone, RotationVarimax, RotationPromax, RotationOblimin}
}

// MAT_FACTOR extraction methods (params.extraction). Each starts from
// the squared multiple correlations; the solvers live in
// internal/processing/matrix_factor.go and matrix_factor_ml.go.
const (
	// FactorExtractionMinres is the minimum-residual fit (psych
	// fm = "minres"), the default.
	FactorExtractionMinres = "minres"
	// FactorExtractionPrincipalAxis is iterated principal axes (psych
	// fm = "pa").
	FactorExtractionPrincipalAxis = "principal_axis"
	// FactorExtractionML is Jöreskog's maximum-likelihood fit over the
	// uniquenesses (stats::factanal, psych fm = "ml"); it adds the
	// likelihood chi-square and the fit indices.
	FactorExtractionML = "ml"
)

// FactorExtractions returns MAT_FACTOR's params.extraction values,
// default first.
func FactorExtractions() []string {
	return []string{FactorExtractionMinres, FactorExtractionPrincipalAxis, FactorExtractionML}
}

// FactorsKaiser is MAT_FACTOR's params.factors "kaiser" (the default):
// as many factors as the correlation has eigenvalues above 1.
const FactorsKaiser = "kaiser"

// FactorDefaultKappa is MAT_FACTOR's default promax power (stats::promax
// m = 4).
const FactorDefaultKappa = 4

// FactorDefaultConfLevel is MAT_FACTOR's default params.conf_level, the
// coverage of the ml RMSEA interval: psych::fa's alpha = 0.1, a 90%
// interval.
const FactorDefaultConfLevel = 0.9

// FactorCount is MAT_FACTOR's resolved params.factors: K > 0 is a fixed
// count; K == 0 is the Kaiser rule, decided on the data.
type FactorCount struct {
	K int
}

// Kaiser reports the data-driven Kaiser rule.
func (f FactorCount) Kaiser() bool { return f.K == 0 }

// FactorIdentified reports whether k common factors are identified on
// p members: the model's degrees of freedom
// p(p − 1)/2 − p·k + k(k − 1)/2 = ((p − k)² − (p + k))/2 are not
// negative.
func FactorIdentified(p, k int) bool { return (p-k)*(p-k)-(p+k) >= 0 }

// MaxIdentifiedFactors is the largest k FactorIdentified admits on p
// members (0 below 3 members, where not even one factor is).
func MaxIdentifiedFactors(p int) int {
	k := 0
	for FactorIdentified(p, k+1) && k+1 <= p {
		k++
	}
	return k
}

// MAT_PCA retention rules (PCAComponents.Rule).
const (
	// PCAComponentsFixed keeps exactly K components (an integer
	// params.components, 1 ≤ K ≤ p).
	PCAComponentsFixed = "fixed"
	// PCAComponentsKaiser keeps the components whose eigenvalue
	// exceeds 1 (params.components "kaiser"; the default on a
	// correlation, refused on a covariance, whose eigenvalues are in
	// the members' units).
	PCAComponentsKaiser = "kaiser"
	// PCAComponentsVariance keeps the fewest leading components whose
	// cumulative explained-variance share reaches Share
	// (params.components {"variance": share}, 0 < share ≤ 1).
	PCAComponentsVariance = "variance"
)

// PCAComponents is MAT_PCA's resolved params.components.
type PCAComponents struct {
	Rule  string
	K     int
	Share float64
}

// RepairNearest is params.repair "nearest": a non-PSD input matrix is
// replaced by its nearest correlation matrix (Higham 2002, alternating
// projections with Dykstra's correction), scaled back to the input's
// diagonal, with a PULSE_MATRIX_NOT_PSD warning carrying the Frobenius
// adjustment.
const RepairNearest = "nearest"

// ControlAll is MAT_PARTIAL_CORRELATION's default params.control.
const ControlAll = "all"

// Columns returns the fields the slot folds, in fold order: the
// members, then any control outside them (Extra). Every co-moment
// index (N, PairN, the warnings' member names) is a Columns index.
func (m Matrix) Columns() []string {
	if len(m.Extra) == 0 {
		return m.Members.Members
	}
	out := make([]string, 0, len(m.Members.Members)+len(m.Extra))
	out = append(out, m.Members.Members...)
	return append(out, m.Extra...)
}

// OutputIndices returns the output axis as Columns positions: Output,
// or every member when Output is nil.
func (m Matrix) OutputIndices() []int {
	if m.Output != nil {
		return m.Output
	}
	out := make([]int, len(m.Members.Members))
	for i := range out {
		out[i] = i
	}
	return out
}

// OutputMembers returns the result's axis keys (OutputIndices'
// fields) and their labels.
func (m Matrix) OutputMembers() (members, labels []string) {
	idx := m.OutputIndices()
	members = make([]string, len(idx))
	labels = make([]string, len(idx))
	for k, i := range idx {
		members[k] = m.Members.Members[i]
		if i < len(m.Members.Labels) {
			labels[k] = m.Members.Labels[i]
		} else {
			labels[k] = members[k]
		}
	}
	return members, labels
}

// Decomposition reports whether the matrix's operator decomposes (or
// inverts) its input matrix, so a non-PSD input is a FATAL
// PULSE_MATRIX_NOT_PSD unless params.repair is "nearest" — the shared
// guard (processing.guardPSD). Every other operator keeps the
// detection-only warning.
func (m Matrix) Decomposition() bool { return IsDecomposition(m.Type) }

// IsDecomposition reports whether t is a decomposition operator (see
// Matrix.Decomposition): MAT_PARTIAL_CORRELATION, MAT_PCA (it
// eigen-decomposes its correlation or covariance), MAT_COLLINEARITY (it
// inverts and eigen-decomposes the predictors' correlation), MAT_FACTOR
// (it factors the correlation), and MAT_RELIABILITY,
// whose McDonald's omega factors the inter-item correlation (there the
// guard's refusal nulls omega with a warning instead of failing the
// matrix: alpha needs no PSD input).
func IsDecomposition(t types.MatrixType) bool {
	switch t {
	case types.MAT_PARTIAL_CORRELATION, types.MAT_RELIABILITY, types.MAT_PCA, types.MAT_COLLINEARITY, types.MAT_FACTOR:
		return true
	}
	return false
}

// Missing-data modes (params.missing).
const (
	MissingListwise = "listwise"
	MissingPairwise = "pairwise"
)

// MissingModes returns the params.missing values, default first.
func MissingModes() []string { return []string{MissingListwise, MissingPairwise} }

// Correlation methods (MAT_CORRELATION params.method).
const (
	CorrelationPearson  = "pearson"
	CorrelationSpearman = "spearman"
	CorrelationKendall  = "kendall"
)

// CorrelationMethods returns the params.method values, default first.
func CorrelationMethods() []string {
	return []string{CorrelationPearson, CorrelationSpearman, CorrelationKendall}
}

// IsRankMethod reports whether method is a rank correlation (spearman
// or kendall): buffered, not mergeable, frequency weights only.
func IsRankMethod(method string) bool {
	return method == CorrelationSpearman || method == CorrelationKendall
}

// SpecMethod is spec's correlation method as the resolver reads it —
// params.method on MAT_CORRELATION (CorrelationPearson when absent),
// "" on every other type. Lenient: a params object that does not
// decode yields the default (the resolver refuses it elsewhere).
func SpecMethod(spec types.MatrixSpec) string {
	if spec.Type != types.MAT_CORRELATION {
		return ""
	}
	var p struct {
		Method *string `json:"method"`
	}
	if len(spec.Params) == 0 || json.Unmarshal(spec.Params, &p) != nil || p.Method == nil {
		return CorrelationPearson
	}
	return *p.Method
}

// missingParams are the engine-owned knobs every matrix operator takes
// (EngineMatrixParams): the missing-data mode, the listwise heavy-drop
// threshold and the bound vector fit the slot emits from.
type missingParams struct {
	Missing      *string  `json:"missing"`
	MaxDropShare *float64 `json:"max_drop_share"`
	Fit          *string  `json:"fit"`
}

// covarianceParams is MAT_COVARIANCE's params object. Summary is
// decoded only to refuse it with a pointed message: top_pairs ranks
// correlations, which a covariance (in the members' own units) is not.
type covarianceParams struct {
	DDOF    *int            `json:"ddof"`
	Summary json.RawMessage `json:"summary"`
	missingParams
}

// correlationParams is MAT_CORRELATION's params object: the method,
// the missing-data knobs and the summary block, so any other key is
// refused (strict decode).
type correlationParams struct {
	Method  *string        `json:"method"`
	Summary *summaryParams `json:"summary"`
	inferenceParams
	missingParams
}

// inferenceParams are the per-pair inference knobs MAT_CORRELATION and
// MAT_PARTIAL_CORRELATION take (decodeInference).
type inferenceParams struct {
	Inference *bool    `json:"inference"`
	Alpha     *float64 `json:"alpha"`
}

// partialParams is MAT_PARTIAL_CORRELATION's params object: the
// control set ("all" or a field list, decoded by decodeControl), the
// repair choice and the missing-data knobs; any other key is refused.
type partialParams struct {
	Control json.RawMessage `json:"control"`
	Repair  *string         `json:"repair"`
	inferenceParams
	missingParams
}

// reliabilityParams is MAT_RELIABILITY's params object: the
// reverse-keyed items, the battery-wide scale range, the repair choice
// and the missing-data knobs; any other key is refused.
type reliabilityParams struct {
	Reverse  []string `json:"reverse"`
	ScaleMin *float64 `json:"scale_min"`
	ScaleMax *float64 `json:"scale_max"`
	Repair   *string  `json:"repair"`
	missingParams
}

// pcaParams is MAT_PCA's params object: the input matrix, the
// retention rule (an integer, "kaiser" or {"variance": share}, decoded
// by decodeComponents), the rotation, the repair choice and the
// missing-data knobs;
// any other key is refused.
type pcaParams struct {
	On         *string         `json:"on"`
	Components json.RawMessage `json:"components"`
	Rotation   *string         `json:"rotation"`
	Repair     *string         `json:"repair"`
	missingParams
}

// factorParams is MAT_FACTOR's params object: the factor count (an
// integer or "kaiser"), the extraction, the rotation with its kappa /
// gamma, the repair choice and the missing-data knobs; any other key is
// refused.
type factorParams struct {
	Factors    json.RawMessage `json:"factors"`
	Extraction *string         `json:"extraction"`
	Rotation   *string         `json:"rotation"`
	Kappa      *float64        `json:"kappa"`
	Gamma      *float64        `json:"gamma"`
	ConfLevel  *float64        `json:"conf_level"`
	Repair     *string         `json:"repair"`
	missingParams
}

// collinearityParams is MAT_COLLINEARITY's params object: the Belsley
// variant (center), the repair choice and the missing-data knobs; any
// other key is refused. The members are the predictors only — there is
// no response key.
type collinearityParams struct {
	Center *bool   `json:"center"`
	Repair *string `json:"repair"`
	missingParams
}

// distanceParams is MAT_DISTANCE's params object: the between grouper
// (resolved by resolveDistance once the members are known), the metric,
// standardize, min_valid and the missing-data knobs (listwise only); any other key
// is refused.
type distanceParams struct {
	Between     json.RawMessage `json:"between"`
	Metric      *string         `json:"metric"`
	Standardize *bool           `json:"standardize"`
	MinValid    *int            `json:"min_valid"`
	missingParams
}

// summaryParams is MAT_CORRELATION's params.summary block.
type summaryParams struct {
	// TopPairs is k: list the k strongest off-diagonal pairs by |r|.
	TopPairs *int `json:"top_pairs"`
}

// ResolveMatrices resolves req.Matrices against schema: first
// req.Vectors (Resolve — its refusal wins), then every matrix spec in
// request order. known reports whether a matrix type is offered (an
// instance-hidden type is unknown); nil admits exactly
// types.AllMatrixTypes(). The first refusal is returned:
//
//   - an unknown type, a spec setting both or neither of `vector` and
//     `fields`, an unknown `encoding`, a result name used twice, or a
//     malformed / out-of-range `params` — SERVICE_VALIDATION naming
//     `matrix` (the slot path) and `reason`;
//   - a `vector` no Request.Vectors entry defines — PULSE_VECTOR_UNKNOWN
//     (`vector`, `slot`, `defined`);
//   - inline `fields` — the vector rules (ResolveSpec at
//     "matrices[i].fields").
//
// The result is index-aligned with req.Matrices. Like Resolve it reads
// only the schema.
func ResolveMatrices(req *types.Request, schema *encoding.Schema, known func(types.MatrixType) bool) ([]Matrix, *errors.CodedError) {
	return ResolveMatricesRouted(req, schema, known, nil, nil)
}

// ResolveMatricesRouted is ResolveMatrices with route mapping a
// MAT_DISTANCE params.between grouper type to the built-in it executes
// as — the instance's view, where a hidden type routes to an unknown
// name (ResolveGroups' route); nil is the identity. ext reports whether
// a routed between type that is not a built-in is a registered
// extension grouper (ResolveGroups' ext: it is then accepted, its
// member set judged); nil means none is.
func ResolveMatricesRouted(req *types.Request, schema *encoding.Schema, known func(types.MatrixType) bool, route func(types.GroupType) types.GroupType, ext func(string) bool) ([]Matrix, *errors.CodedError) {
	return ResolveMatricesWith(req, schema, MatrixResolve{Known: known, Route: route, BetweenExt: ext})
}

// ResolveMatricesWith is ResolveMatricesRouted plus the extension MAT_*
// operators o.Extension reports: such a type resolves like a built-in —
// shape, encoding, name, members by the vector rules, the engine-owned
// missing / max_drop_share — with its other params kept opaque
// (Matrix.ExtParams), its declared tier as Streamable / Mergeable, and
// a member whose column type its non-empty Accepts does not list
// refused with PULSE_VECTOR_MEMBER_TYPE.
func ResolveMatricesWith(req *types.Request, schema *encoding.Schema, o MatrixResolve) ([]Matrix, *errors.CodedError) {
	known, route, ext := o.Known, o.Route, o.BetweenExt
	if route == nil {
		route = func(t types.GroupType) types.GroupType { return t }
	}
	if req == nil {
		return nil, nil
	}
	resolved, verr := Resolve(req.Vectors, schema)
	if verr != nil {
		return nil, verr
	}
	if len(req.Matrices) == 0 {
		return nil, nil
	}
	if known == nil {
		known = builtinMatrixType
	}
	out := make([]Matrix, 0, len(req.Matrices))
	names := make(map[string]int, len(req.Matrices))
	for i := range req.Matrices {
		spec := req.Matrices[i]
		at := matrixPath(i)
		var extOp MatrixExtension
		isExt := false
		if o.Extension != nil {
			extOp, isExt = o.Extension(spec.Type)
		}
		if !isExt && !known(spec.Type) {
			return nil, matrixInvalid(at, "unknown_type",
				at+" references unknown matrix type: "+strconv.Quote(string(spec.Type)),
				map[string]any{"type": string(spec.Type)})
		}
		switch {
		case spec.Vector != "" && len(spec.Fields) > 0:
			return nil, matrixInvalid(at, "vector_and_fields",
				at+" sets both vector and fields; set exactly one", nil)
		case spec.Vector == "" && len(spec.Fields) == 0:
			return nil, matrixInvalid(at, "no_vector_or_fields",
				at+" sets neither vector nor fields; set exactly one", nil)
		}
		enc := spec.EffectiveEncoding()
		if !knownEncoding(enc) {
			return nil, matrixInvalid(at, "unknown_encoding",
				at+" has unknown encoding "+strconv.Quote(string(spec.Encoding)),
				map[string]any{"value": string(spec.Encoding), "valid": encodingNames()})
		}
		name := spec.EffectiveName()
		if first, dup := names[name]; dup {
			return nil, matrixInvalid(at, "duplicate_name",
				"matrix result name "+strconv.Quote(name)+" is used twice (matrices["+strconv.Itoa(first)+"] and "+at+")",
				map[string]any{"name": name, "indices": []int{first, i}})
		}
		names[name] = i

		m := Matrix{Index: i, Name: name, Type: spec.Type, Encoding: enc,
			Streamable: spec.Streamable(), Mergeable: spec.Mergeable()}
		if isExt {
			// The declared tier: the engine's blocked merge carries a
			// co-moment operator across partitions; a buffered one keeps
			// rows, which do not merge.
			m.Extension, m.Streamable, m.Mergeable = true, extOp.Streamable, extOp.Streamable
			m.ExtAuxiliary = append([]string(nil), extOp.Auxiliary...)
			if err := decodeExtensionParams(at, spec, &m); err != nil {
				return nil, err
			}
		} else if err := decodeMatrixParams(at, spec, &m); err != nil {
			return nil, err
		}
		// Controls resolve after the members (below): a control is a
		// member or another numeric field.

		if spec.Vector != "" {
			r, ok := Find(resolved, spec.Vector)
			if !ok {
				defined := make([]string, 0, len(resolved))
				for _, v := range resolved {
					defined = append(defined, v.Name)
				}
				return nil, errors.NewCodedErrorWithDetails(errors.PULSE_VECTOR_UNKNOWN,
					at+" names vector "+strconv.Quote(spec.Vector)+", which no vectors entry defines",
					map[string]any{"vector": spec.Vector, "slot": at + ".vector", "defined": defined})
			}
			m.Members = r
			if r.Native != nil {
				// A native vector's labels are never its member names
				// (schema labels, else the "f_<i>" defaults).
				m.ExplicitLabels = true
			} else {
				// A spec reading native elements carries their labels
				// even when it sets none (the inline rule below).
				m.ExplicitLabels = len(req.Vectors[indexOfVector(req.Vectors, spec.Vector)].Labels) > 0 || r.Refs != nil
			}
		} else {
			r, err := ResolveSpec(at+".fields", types.VectorSpec{Name: name, Fields: spec.Fields}, schema)
			if err != nil {
				return nil, err
			}
			m.Members = r
			// An inline list reading a native vector's elements carries
			// their labels (schema labels, else "f_<k>"), never the
			// "f[k]" member names; scalar members keep their names.
			m.ExplicitLabels = r.Refs != nil
		}
		if isExt {
			if err := acceptsMembers(at, spec.Type, extOp.Accepts, m.Members, schema); err != nil {
				return nil, err
			}
		}
		if err := resolveControls(at, schema, &m); err != nil {
			return nil, err
		}
		if err := resolveReliability(at, spec, &m); err != nil {
			return nil, err
		}
		if err := resolvePCA(at, &m); err != nil {
			return nil, err
		}
		if err := resolveFactor(at, &m); err != nil {
			return nil, err
		}
		if err := resolveDistance(at, req.Vectors, resolved, schema, route, ext, &m); err != nil {
			return nil, err
		}
		if err := resolveFit(at, req, &m); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}

// decodeMatrixParams decodes the operator's params onto m. Unknown keys
// are refused, so a misspelt knob never silently takes its default.
func decodeMatrixParams(at string, spec types.MatrixSpec, m *Matrix) *errors.CodedError {
	raw := bytes.TrimSpace(spec.Params)
	empty := len(raw) == 0 || bytes.Equal(raw, []byte("null"))
	switch spec.Type {
	case types.MAT_COVARIANCE:
		m.DDOF = 1
	case types.MAT_CORRELATION:
		m.Method = CorrelationPearson
	case types.MAT_PCA:
		m.On = PCAOnCorrelation
		m.Components = PCAComponents{Rule: PCAComponentsKaiser}
	case types.MAT_FACTOR:
		m.Extraction = FactorExtractionMinres
	case types.MAT_DISTANCE:
		m.Metric = DistanceDefaultMetric
		m.MinValid = 1
	}
	if empty {
		if spec.Type == types.MAT_DISTANCE {
			return betweenRequired(at)
		}
		if slices.Contains(InferenceTypes(), spec.Type) {
			return decodeInference(at, inferenceParams{}, spec.Multiplicity != nil, m)
		}
		return nil
	}
	var miss missingParams
	switch spec.Type {
	case types.MAT_COVARIANCE:
		var p covarianceParams
		if err := decodeStrict(raw, &p); err != nil {
			return matrixInvalid(at, "bad_params", at+" params do not decode: "+err.Error(), nil)
		}
		if p.DDOF != nil {
			if *p.DDOF != 0 && *p.DDOF != 1 {
				return matrixInvalid(at, "bad_params", at+" params.ddof must be 0 or 1",
					map[string]any{"param": "ddof", "value": *p.DDOF})
			}
			m.DDOF = *p.DDOF
		}
		if s := bytes.TrimSpace(p.Summary); len(s) > 0 && !bytes.Equal(s, []byte("null")) {
			return matrixInvalid(at, "bad_params", at+" params.summary (top_pairs) applies to MAT_CORRELATION only; a covariance is in its members' own units, so pairs do not rank",
				map[string]any{"param": "summary", "type": string(types.MAT_COVARIANCE), "valid_types": []string{string(types.MAT_CORRELATION)}})
		}
		miss = p.missingParams
	case types.MAT_CORRELATION:
		var p correlationParams
		if err := decodeStrict(raw, &p); err != nil {
			return matrixInvalid(at, "bad_params", at+" params do not decode: "+err.Error(), nil)
		}
		if p.Method != nil {
			switch *p.Method {
			case CorrelationPearson, CorrelationSpearman, CorrelationKendall:
				m.Method = *p.Method
			default:
				return matrixInvalid(at, "bad_params", at+" params.method must be \"pearson\", \"spearman\" or \"kendall\"",
					map[string]any{"param": "method", "value": *p.Method, "valid": CorrelationMethods()})
			}
		}
		if p.Summary != nil {
			k := p.Summary.TopPairs
			if k == nil {
				return matrixInvalid(at, "bad_params", at+" params.summary must set top_pairs (a positive integer)",
					map[string]any{"param": "summary.top_pairs"})
			}
			if *k < 1 {
				return matrixInvalid(at, "bad_params", at+" params.summary.top_pairs must be a positive integer",
					map[string]any{"param": "summary.top_pairs", "value": *k})
			}
			m.TopPairs = *k
		}
		if err := decodeInference(at, p.inferenceParams, spec.Multiplicity != nil, m); err != nil {
			return err
		}
		miss = p.missingParams
	case types.MAT_PARTIAL_CORRELATION:
		var p partialParams
		if err := decodeStrict(raw, &p); err != nil {
			return matrixInvalid(at, "bad_params", at+" params do not decode: "+err.Error(), nil)
		}
		controls, err := decodeControl(at, p.Control)
		if err != nil {
			return err
		}
		m.Controls = controls
		if err := decodeRepair(at, p.Repair, m); err != nil {
			return err
		}
		if err := decodeInference(at, p.inferenceParams, spec.Multiplicity != nil, m); err != nil {
			return err
		}
		miss = p.missingParams
	case types.MAT_RELIABILITY:
		var p reliabilityParams
		if err := decodeStrict(raw, &p); err != nil {
			return matrixInvalid(at, "bad_params", at+" params do not decode: "+err.Error(), nil)
		}
		if err := decodeScale(at, p, m); err != nil {
			return err
		}
		if err := decodeRepair(at, p.Repair, m); err != nil {
			return err
		}
		miss = p.missingParams
	case types.MAT_PCA:
		var p pcaParams
		if err := decodeStrict(raw, &p); err != nil {
			return matrixInvalid(at, "bad_params", at+" params do not decode: "+err.Error(), nil)
		}
		if err := decodePCA(at, p, m); err != nil {
			return err
		}
		if err := decodeRepair(at, p.Repair, m); err != nil {
			return err
		}
		miss = p.missingParams
	case types.MAT_FACTOR:
		var p factorParams
		if err := decodeStrict(raw, &p); err != nil {
			return matrixInvalid(at, "bad_params", at+" params do not decode: "+err.Error(), nil)
		}
		if err := decodeFactor(at, p, m); err != nil {
			return err
		}
		if err := decodeRepair(at, p.Repair, m); err != nil {
			return err
		}
		miss = p.missingParams
	case types.MAT_DISTANCE:
		var p distanceParams
		if err := decodeStrict(raw, &p); err != nil {
			return matrixInvalid(at, "bad_params", at+" params do not decode: "+err.Error(), nil)
		}
		if err := decodeDistance(at, p, m); err != nil {
			return err
		}
		miss = p.missingParams
	case types.MAT_COLLINEARITY:
		var p collinearityParams
		if err := decodeStrict(raw, &p); err != nil {
			return matrixInvalid(at, "bad_params", at+" params do not decode: "+err.Error(), nil)
		}
		if p.Center != nil {
			m.Center = *p.Center
		}
		if err := decodeRepair(at, p.Repair, m); err != nil {
			return err
		}
		miss = p.missingParams
	}
	return decodeMissing(at, miss, m)
}

// EngineMatrixParams are the params keys the engine owns on every
// matrix operator, extension ones included: the missing-data mode, the
// listwise heavy-drop threshold and params.fit (the bound vector fit the
// slot emits from — resolveFit). An extension operator never sees
// them (decodeExtensionParams strips them) and may not declare them.
func EngineMatrixParams() []string { return []string{"missing", "max_drop_share", "fit"} }

// decodeExtensionParams decodes an extension operator's params: a JSON
// object (or absent / null) whose engine-owned keys (EngineMatrixParams)
// are judged by the built-in rule (decodeMissing) and removed; the
// remaining keys are kept, unjudged, as m.ExtParams — the operator's
// factory decodes and validates them. A non-object is bad_params.
func decodeExtensionParams(at string, spec types.MatrixSpec, m *Matrix) *errors.CodedError {
	raw := bytes.TrimSpace(spec.Params)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		return matrixInvalid(at, "bad_params", at+" params must be a JSON object", nil)
	}
	var miss missingParams
	if err := json.Unmarshal(raw, &miss); err != nil {
		return matrixInvalid(at, "bad_params", at+" params do not decode: "+err.Error(), nil)
	}
	for _, k := range EngineMatrixParams() {
		delete(obj, k)
	}
	if len(obj) > 0 {
		rest, err := json.Marshal(obj)
		if err != nil {
			return matrixInvalid(at, "bad_params", at+" params do not decode: "+err.Error(), nil)
		}
		m.ExtParams = rest
	}
	return decodeMissing(at, miss, m)
}

// acceptsMembers refuses a member whose column type accepts does not
// list (an element of a native vector reads as its element type, f32
// or f64); an empty accepts admits every member the vector rules did.
func acceptsMembers(at string, op types.MatrixType, accepts []encoding.FieldType, r Resolved, schema *encoding.Schema) *errors.CodedError {
	if len(accepts) == 0 || schema == nil {
		return nil
	}
	for i, name := range r.Members {
		field := name
		if r.Refs != nil {
			field = r.Refs[i].Field
		}
		f := schema.Field(field)
		if f == nil {
			continue // unreachable: members come from the schema
		}
		ft := f.Type
		if ft.IsVector() {
			ft = encoding.FieldTypeF64
			if f.Type.VectorElemSize() == 4 {
				ft = encoding.FieldTypeF32
			}
		}
		if slices.Contains(accepts, ft) {
			continue
		}
		valid := make([]string, len(accepts))
		for k, a := range accepts {
			valid[k] = a.String()
		}
		return errors.NewCodedErrorWithDetails(errors.PULSE_VECTOR_MEMBER_TYPE,
			at+" member "+strconv.Quote(name)+" has type "+ft.String()+", which "+string(op)+
				" does not accept (accepts: "+strings.Join(valid, ", ")+")",
			map[string]any{"matrix": at, "type": string(op), "field": name, "field_type": ft.String(), "accepts": valid})
	}
	return nil
}

// decodePCA applies MAT_PCA's params.rotation ("none", the default, or
// "varimax"), params.on and params.components: on
// "correlation" (default) or "covariance"; components an integer k ≥ 1
// (checked against p in resolvePCA), "kaiser" (the correlation
// default) or {"variance": share} with 0 < share ≤ 1. On a covariance
// components is required and "kaiser" is refused — λ > 1 has no
// meaning in the members' units. Every refusal is bad_params.
func decodePCA(at string, p pcaParams, m *Matrix) *errors.CodedError {
	m.On = PCAOnCorrelation
	if p.Rotation != nil {
		switch *p.Rotation {
		case RotationNone:
		case RotationVarimax:
			m.Rotation = RotationVarimax
		default:
			return matrixInvalid(at, "bad_params", at+" params.rotation must be \"none\" or \"varimax\"",
				map[string]any{"param": "rotation", "value": *p.Rotation, "valid": PCARotations()})
		}
	}
	if p.On != nil {
		switch *p.On {
		case PCAOnCorrelation, PCAOnCovariance:
			m.On = *p.On
		default:
			return matrixInvalid(at, "bad_params", at+" params.on must be \"correlation\" or \"covariance\"",
				map[string]any{"param": "on", "value": *p.On, "valid": PCAOnValues()})
		}
	}
	raw := bytes.TrimSpace(p.Components)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		if m.On == PCAOnCovariance {
			return matrixInvalid(at, "bad_params", at+" params.components is required on a covariance (an integer k or {\"variance\": share}); \"kaiser\" applies to a correlation only",
				map[string]any{"param": "components", "on": m.On})
		}
		m.Components = PCAComponents{Rule: PCAComponentsKaiser}
		return nil
	}
	bad := func(msg string, value any) *errors.CodedError {
		return matrixInvalid(at, "bad_params", at+" params.components "+msg,
			map[string]any{"param": "components", "value": value, "valid": []string{"a positive integer", PCAComponentsKaiser, "{\"variance\": share}"}})
	}
	switch raw[0] {
	case '"':
		var str string
		if err := json.Unmarshal(raw, &str); err != nil || str != PCAComponentsKaiser {
			return bad("must be a positive integer, \"kaiser\" or {\"variance\": share}", string(raw))
		}
		if m.On == PCAOnCovariance {
			return matrixInvalid(at, "bad_params", at+" params.components \"kaiser\" (eigenvalue > 1) applies to a correlation only: a covariance's eigenvalues are in the members' units; set an integer k or {\"variance\": share}",
				map[string]any{"param": "components", "value": PCAComponentsKaiser, "on": m.On})
		}
		m.Components = PCAComponents{Rule: PCAComponentsKaiser}
	case '{':
		var v struct {
			Variance *float64 `json:"variance"`
		}
		if err := decodeStrict(raw, &v); err != nil || v.Variance == nil {
			return bad("object must be {\"variance\": share}", string(raw))
		}
		if s := *v.Variance; !(s > 0 && s <= 1) {
			return bad("variance share must be in (0, 1]", s)
		}
		m.Components = PCAComponents{Rule: PCAComponentsVariance, Share: *v.Variance}
	default:
		var f float64
		if err := json.Unmarshal(raw, &f); err != nil || f != math.Trunc(f) || f < 1 || f > math.MaxInt32 {
			return bad("must be a positive integer, \"kaiser\" or {\"variance\": share}", string(raw))
		}
		m.Components = PCAComponents{Rule: PCAComponentsFixed, K: int(f)}
	}
	return nil
}

// resolvePCA refuses a MAT_PCA integer params.components above the
// member count (bad_params): a p-member battery has p components.
func resolvePCA(at string, m *Matrix) *errors.CodedError {
	if m.Type != types.MAT_PCA || m.Components.Rule != PCAComponentsFixed {
		return nil
	}
	if p := len(m.Members.Members); m.Components.K > p {
		return matrixInvalid(at, "bad_params", at+" params.components "+strconv.Itoa(m.Components.K)+" exceeds the "+strconv.Itoa(p)+" members (a battery has as many components as members)",
			map[string]any{"param": "components", "value": m.Components.K, "members": p})
	}
	return nil
}

// decodeFactor applies MAT_FACTOR's params: factors (a positive integer,
// checked against p in resolveFactor, or "kaiser", the default),
// extraction ("minres", the default, or "principal_axis"), rotation
// ("none", the default, "varimax", "promax" or "oblimin"), kappa (promax
// only, finite and > 1, default 4) and gamma (oblimin only, finite,
// default 0) and conf_level (extraction ml only, 0 < c < 1, default
// 0.9). A kappa without promax, a gamma without oblimin or a conf_level
// without ml is refused rather than ignored. Every refusal is
// bad_params.
func decodeFactor(at string, p factorParams, m *Matrix) *errors.CodedError {
	return decodeFactorWith(at, p, m, matrixInvalid)
}

// invalidFunc builds a slot's SERVICE_VALIDATION refusal (matrixInvalid,
// attributeInvalid): the factor knobs are judged by one rule whichever
// slot carries them.
type invalidFunc func(at, reason, msg string, extra map[string]any) *errors.CodedError

// decodeFactorWith is decodeFactor refusing through invalid
// (ATTR_FACTOR_SCORE's inline fit shares MAT_FACTOR's knobs).
func decodeFactorWith(at string, p factorParams, m *Matrix, matrixInvalid invalidFunc) *errors.CodedError {
	if p.Extraction != nil {
		switch *p.Extraction {
		case FactorExtractionMinres, FactorExtractionPrincipalAxis, FactorExtractionML:
			m.Extraction = *p.Extraction
		default:
			return matrixInvalid(at, "bad_params", at+" params.extraction must be \"minres\", \"principal_axis\" or \"ml\"",
				map[string]any{"param": "extraction", "value": *p.Extraction, "valid": FactorExtractions()})
		}
	}
	if p.Rotation != nil {
		switch *p.Rotation {
		case RotationNone:
		case RotationVarimax, RotationPromax, RotationOblimin:
			m.Rotation = *p.Rotation
		default:
			return matrixInvalid(at, "bad_params", at+" params.rotation must be \"none\", \"varimax\", \"promax\" or \"oblimin\"",
				map[string]any{"param": "rotation", "value": *p.Rotation, "valid": FactorRotations()})
		}
	}
	if m.Rotation == RotationPromax {
		m.Kappa = FactorDefaultKappa
	}
	if p.Kappa != nil {
		if m.Rotation != RotationPromax {
			return matrixInvalid(at, "bad_params", at+" params.kappa applies to rotation \"promax\" only",
				map[string]any{"param": "kappa", "rotation": m.Rotation})
		}
		if k := *p.Kappa; !(k > 1) || math.IsInf(k, 0) {
			return matrixInvalid(at, "bad_params", at+" params.kappa must be a finite number above 1",
				map[string]any{"param": "kappa", "value": k})
		}
		m.Kappa = *p.Kappa
	}
	if p.Gamma != nil {
		if m.Rotation != RotationOblimin {
			return matrixInvalid(at, "bad_params", at+" params.gamma applies to rotation \"oblimin\" only",
				map[string]any{"param": "gamma", "rotation": m.Rotation})
		}
		if g := *p.Gamma; math.IsNaN(g) || math.IsInf(g, 0) {
			return matrixInvalid(at, "bad_params", at+" params.gamma must be a finite number",
				map[string]any{"param": "gamma", "value": g})
		}
		m.Gamma = *p.Gamma
	}
	if m.Extraction == FactorExtractionML {
		m.ConfLevel = FactorDefaultConfLevel
	}
	if p.ConfLevel != nil {
		if m.Extraction != FactorExtractionML {
			return matrixInvalid(at, "bad_params", at+" params.conf_level applies to extraction \"ml\" only",
				map[string]any{"param": "conf_level", "extraction": m.Extraction})
		}
		if c := *p.ConfLevel; !(c > 0 && c < 1) {
			return matrixInvalid(at, "bad_params", at+" params.conf_level must be a number strictly between 0 and 1",
				map[string]any{"param": "conf_level", "value": c})
		}
		m.ConfLevel = *p.ConfLevel
	}
	raw := bytes.TrimSpace(p.Factors)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil
	}
	bad := func(value any) *errors.CodedError {
		return matrixInvalid(at, "bad_params", at+" params.factors must be a positive integer or \"kaiser\"",
			map[string]any{"param": "factors", "value": value, "valid": []string{"a positive integer", FactorsKaiser}})
	}
	if raw[0] == '"' {
		var str string
		if err := json.Unmarshal(raw, &str); err != nil || str != FactorsKaiser {
			return bad(string(raw))
		}
		return nil
	}
	var f float64
	if err := json.Unmarshal(raw, &f); err != nil || f != math.Trunc(f) || f < 1 || f > math.MaxInt32 {
		return bad(string(raw))
	}
	m.Factors = FactorCount{K: int(f)}
	return nil
}

// resolveFactor refuses a MAT_FACTOR integer params.factors above the
// member count (bad_params). A count the battery cannot identify
// (FactorIdentified) is not refused here: the run reports it with
// PULSE_MATRIX_NOT_IDENTIFIED and null fit figures, as for a Kaiser
// count decided on the data.
func resolveFactor(at string, m *Matrix) *errors.CodedError {
	if m.Type != types.MAT_FACTOR || m.Factors.Kaiser() {
		return nil
	}
	if p := len(m.Members.Members); m.Factors.K > p {
		return matrixInvalid(at, "bad_params", at+" params.factors "+strconv.Itoa(m.Factors.K)+" exceeds the "+strconv.Itoa(p)+" members",
			map[string]any{"param": "factors", "value": m.Factors.K, "members": p})
	}
	return nil
}

// decodeDistance applies MAT_DISTANCE's params.metric (default
// "euclidean"), params.standardize (default false) and params.min_valid
// (default 1), and keeps params.between for resolveDistance. A missing
// between is between_required. The metric resolves through the
// linalg/metric registry for the MAT_DISTANCE consumer in the distance
// form: an unknown or unaccepted name is bad_params (details.valid the
// registry's list). A non-negative-domain metric (hellinger, aitchison)
// is refused beside standardize — z-scored centroids are signed — and a
// min_valid below 1 is bad_params (its ceiling, the member count, is
// resolveDistance's).
func decodeDistance(at string, p distanceParams, m *Matrix) *errors.CodedError {
	raw := bytes.TrimSpace(p.Between)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return betweenRequired(at)
	}
	m.betweenRaw = raw
	if p.Metric != nil {
		if _, err := metric.ResolveFor(metric.ConsumerMatDistance, *p.Metric, metric.Distance); err != nil {
			valid := metric.Accepted(metric.ConsumerMatDistance)
			return matrixInvalid(at, "bad_params", at+" params.metric must be one of "+strings.Join(valid, ", "),
				map[string]any{"param": "metric", "value": *p.Metric, "valid": valid})
		}
		m.Metric = *p.Metric
	}
	if p.Standardize != nil {
		m.Standardize = *p.Standardize
	}
	if mm, _ := metric.Lookup(m.Metric); m.Standardize && mm.Domain() == metric.DomainNonNegative {
		return matrixInvalid(at, "bad_params", at+" params.metric "+strconv.Quote(m.Metric)+" reads non-negative values and cannot follow standardize (z-scored centroids are signed); drop standardize or pick another metric",
			map[string]any{"param": "metric", "value": m.Metric, "standardize": true})
	}
	if p.MinValid != nil {
		if *p.MinValid < 1 {
			return matrixInvalid(at, "bad_params", at+" params.min_valid must be at least 1",
				map[string]any{"param": "min_valid", "value": *p.MinValid})
		}
		m.MinValid = *p.MinValid
	}
	return nil
}

// betweenRequired is MAT_DISTANCE's refusal of a spec without
// params.between.
func betweenRequired(at string) *errors.CodedError {
	return matrixInvalid(at, "between_required", at+" MAT_DISTANCE needs params.between, the grouper whose buckets it compares (for example {\"type\": \"GROUP_CATEGORY\", \"field\": \"region\"})",
		map[string]any{"param": "between"})
}

// resolveDistance resolves MAT_DISTANCE's params.between against the
// request's vectors and the schema (route: the instance's view of the
// grouper type; ext: whether a non-built-in routed type is a registered
// extension grouper). The grouper decodes strictly as a types.Group;
// its type must be a built-in (DistanceBetweenTypes) or a registered
// extension (an unknown or hidden type is unknown_between_type); it may
// not carry its own weight (the matrix's weight applies), and only a
// date-family between (IsDateBetween) may carry a `tz` — the zone pass
// (internal/descriptor.ResolveZones, slot "<at>.params.between")
// resolves and refuses it like a top-level grouper's, and writes an
// inherited zone into it (ZonedRequest). A GROUP_KMEANS between
// resolves its vector / fields and params by the grouper rule
// (ResolveGroups' leaf, slot "<at>.params.between") and is rewritten
// for the factory (members into Fields); an extension between's
// vector / fields are judged by the member rules and rewritten the
// same way (its params its own business); every other between, and an
// extension one without a vector input, must name a schema field. The
// missing mode is listwise only (pairwise_unsupported): a centroid
// compares one row set.
func resolveDistance(at string, specs []types.VectorSpec, resolved []Resolved, schema *encoding.Schema, route func(types.GroupType) types.GroupType, ext func(string) bool, m *Matrix) *errors.CodedError {
	if m.Type != types.MAT_DISTANCE {
		return nil
	}
	raw := m.betweenRaw
	m.betweenRaw = nil
	if m.Pairwise {
		return matrixInvalid(at, "pairwise_unsupported", at+" params.missing \"pairwise\" is not supported on MAT_DISTANCE: every centroid must rest on the same rows; use \"listwise\"",
			map[string]any{"param": "missing", "value": MissingPairwise, "valid": []string{MissingListwise}})
	}
	if p := len(m.Members.Members); m.MinValid > p {
		return matrixInvalid(at, "bad_params", at+" params.min_valid "+strconv.Itoa(m.MinValid)+" exceeds the "+strconv.Itoa(p)+" members",
			map[string]any{"param": "min_valid", "value": m.MinValid, "members": p})
	}
	bt := at + ".params.between"
	var g types.Group
	if err := decodeStrict(raw, &g); err != nil {
		return matrixInvalid(at, "bad_params", bt+" does not decode as a grouper: "+err.Error(), map[string]any{"param": "between"})
	}
	typ := route(g.Type)
	builtin := slices.Contains(DistanceBetweenTypes(), typ)
	extension := isExtension(ext, string(typ), builtin)
	switch {
	case g.Type == "":
		return matrixInvalid(at, "bad_params", bt+" needs a grouper type", map[string]any{"param": "between"})
	case !builtin && !extension:
		return matrixInvalid(at, "unknown_between_type", bt+" references unknown grouper type "+strconv.Quote(string(g.Type)),
			map[string]any{"param": "between", "type": string(g.Type), "valid": groupTypeNames(DistanceBetweenTypes())})
	case !g.Weight.IsZero():
		return matrixInvalid(at, "bad_params", bt+".weight is not accepted: the between grouper reads the matrix's own weight",
			map[string]any{"param": "between.weight"})
	case g.TimeZone != "" && !IsDateBetween(typ):
		return matrixInvalid(at, "bad_params", bt+".tz is accepted only on a date-family between (GROUP_DATE, GROUP_DATE_RANGES)",
			map[string]any{"param": "between.tz"})
	}
	var in GroupInput
	if extension {
		mem, err := resolveExtensionMembers(bt, g.Vector, g.Fields, specs, resolved, schema, func(_, reason, msg string, extra map[string]any) *errors.CodedError {
			return matrixInvalid(at, reason, msg, extra)
		})
		if err != nil {
			return err
		}
		in = GroupInput{Slot: bt, Members: mem, Extension: true}
	} else {
		var err *errors.CodedError
		if in, err = resolveGroup(bt, &g, typ, specs, resolved, schema); err != nil {
			return err
		}
	}
	cp := g
	if in.Has() {
		cp.Fields = append([]string(nil), in.Members.Members...)
		cp.Vector = ""
	}
	if !in.Has() || (extension && g.Field != "") {
		if g.Field == "" {
			return matrixInvalid(at, "bad_params", bt+" ("+string(g.Type)+") needs a field", map[string]any{"param": "between.field"})
		}
		if schema != nil && schema.Field(g.Field) == nil {
			return matrixInvalid(at, "unknown_field", bt+" references unknown field: "+g.Field,
				map[string]any{"param": "between.field", "field": g.Field})
		}
	}
	m.Between = &cp
	return nil
}

// groupTypeNames stringifies group types.
func groupTypeNames(ts []types.GroupType) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = string(t)
	}
	return out
}

// decodeRepair applies a decomposition operator's params.repair: absent
// or "nearest".
func decodeRepair(at string, repair *string, m *Matrix) *errors.CodedError {
	if repair == nil {
		return nil
	}
	if *repair != RepairNearest {
		return matrixInvalid(at, "bad_params", at+" params.repair must be \"nearest\"",
			map[string]any{"param": "repair", "value": *repair, "valid": []string{RepairNearest}})
	}
	m.Repair = RepairNearest
	return nil
}

// decodeScale applies MAT_RELIABILITY's params.scale_min / scale_max
// (both or neither, finite, min < max) and keeps the reverse names for
// resolveReliability. Half a range, or a range that is not a finite
// min < max, is PROCESSING_CONFIG. Without a range of its own the
// matrix falls back to its vector's declared scale_min / scale_max
// (resolveReliability); a reverse list with neither is PROCESSING_CONFIG
// there: the reversal x' = min + max − x needs the declared range,
// which is never inferred from the data.
func decodeScale(at string, p reliabilityParams, m *Matrix) *errors.CodedError {
	switch {
	case p.ScaleMin == nil && p.ScaleMax == nil:
		// No range of its own: the vector's declared bounds stand in
		// (resolveReliability, once the members are known), else a
		// reverse list is refused there.
		m.reverseNames = p.Reverse
		return nil
	case p.ScaleMin == nil || p.ScaleMax == nil:
		return scaleConfig(at, at+" params.scale_min and params.scale_max are set together", map[string]any{"param": "scale_min"})
	}
	lo, hi := *p.ScaleMin, *p.ScaleMax
	if math.IsNaN(lo) || math.IsInf(lo, 0) || math.IsNaN(hi) || math.IsInf(hi, 0) || !(lo < hi) {
		return scaleConfig(at, at+" params.scale_min must be below params.scale_max",
			map[string]any{"param": "scale_min", "scale_min": lo, "scale_max": hi})
	}
	m.HasScale, m.ScaleMin, m.ScaleMax = true, lo, hi
	m.reverseNames = p.Reverse
	return nil
}

// ScaleKey is the matrix's declared range and reverse positions as the
// shared ScaleKey (meaningful only when HasScale).
func (m Matrix) ScaleKey() ScaleKey {
	return ScaleKey{Lo: m.ScaleMin, Hi: m.ScaleMax, Reverse: m.Reverse}
}

// scaleConfig is a MAT_RELIABILITY scale-range refusal: PROCESSING_CONFIG
// naming the matrix slot.
func scaleConfig(at, msg string, extra map[string]any) *errors.CodedError {
	details := map[string]any{"matrix": at}
	for k, v := range extra {
		details[k] = v
	}
	return errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG, msg, details)
}

// ReliabilityMinItems is the fewest members MAT_RELIABILITY takes:
// alpha compares at least two items.
const ReliabilityMinItems = 2

// resolveReliability places MAT_RELIABILITY's reverse names on the
// resolved members (each must be a member, listed once) and refuses a
// battery of fewer than ReliabilityMinItems members — both
// SERVICE_VALIDATION bad_params. A matrix without params.scale_min /
// scale_max takes its vector's declared bounds (explicit params win);
// a reverse list with no range from either is PROCESSING_CONFIG.
func resolveReliability(at string, spec types.MatrixSpec, m *Matrix) *errors.CodedError {
	if spec.Type != types.MAT_RELIABILITY {
		return nil
	}
	members := m.Members.Members
	if len(members) < ReliabilityMinItems {
		return matrixInvalid(at, "bad_params", at+" MAT_RELIABILITY needs at least 2 items; alpha compares items with one another",
			map[string]any{"members": append([]string(nil), members...), "min_items": ReliabilityMinItems})
	}
	names := m.reverseNames
	m.reverseNames = nil
	if !m.HasScale && m.Members.HasScale {
		// Explicit params win; otherwise the kind "scale" vector's
		// declared bounds are the battery's range.
		m.HasScale, m.ScaleMin, m.ScaleMax = true, m.Members.ScaleMin, m.Members.ScaleMax
	}
	if len(names) > 0 && !m.HasScale {
		return scaleConfig(at, at+" params.reverse needs the battery's scale range: set params.scale_min and params.scale_max, or scale_min / scale_max on its kind \"scale\" vector (the range is never inferred from the data)",
			map[string]any{"param": "scale_min", "reverse": append([]string(nil), names...)})
	}
	if len(names) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(names))
	for _, r := range names {
		if seen[r] {
			return matrixInvalid(at, "bad_params", at+" params.reverse names "+strconv.Quote(r)+" twice",
				map[string]any{"param": "reverse", "value": r})
		}
		seen[r] = true
		pos := -1
		for i, f := range members {
			if f == r {
				pos = i
				break
			}
		}
		if pos < 0 {
			return matrixInvalid(at, "bad_params", at+" params.reverse names "+strconv.Quote(r)+", which is not an item of the matrix",
				map[string]any{"param": "reverse", "value": r, "members": append([]string(nil), members...)})
		}
		m.Reverse = append(m.Reverse, pos)
	}
	slices.Sort(m.Reverse)
	return nil
}

// decodeControl decodes params.control: absent, null or "all" is nil
// (control for every other member); otherwise a non-empty list of
// distinct, non-empty field names (no glob: a control names one field).
func decodeControl(at string, raw json.RawMessage) ([]string, *errors.CodedError) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, nil
	}
	var all string
	if json.Unmarshal(raw, &all) == nil {
		if all == ControlAll {
			return nil, nil
		}
		return nil, matrixInvalid(at, "bad_params", at+" params.control must be \"all\" or a list of field names",
			map[string]any{"param": "control", "value": all})
	}
	var list []string
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, matrixInvalid(at, "bad_params", at+" params.control must be \"all\" or a list of field names",
			map[string]any{"param": "control"})
	}
	if len(list) == 0 {
		return nil, matrixInvalid(at, "bad_params", at+" params.control lists no field; omit it (or use \"all\") to control for every other member",
			map[string]any{"param": "control"})
	}
	seen := make(map[string]bool, len(list))
	for _, c := range list {
		switch {
		case c == "" || isGlob(c):
			return nil, matrixInvalid(at, "bad_params", at+" params.control entries are field names (no glob)",
				map[string]any{"param": "control", "value": c})
		case seen[c]:
			return nil, matrixInvalid(at, "bad_params", at+" params.control names "+strconv.Quote(c)+" twice",
				map[string]any{"param": "control", "value": c})
		}
		seen[c] = true
	}
	return list, nil
}

// controlSlot rewrites a ResolveSpec refusal's "slot" detail
// ("…params.control.fields[k]", k indexing the outside controls) to the
// caller's own path, "…params.control[j]" with j the entry's position
// in params.control. The error is copied, never mutated.
func controlSlot(err *errors.CodedError, at string, controls, outside []string) *errors.CodedError {
	prefix := at + ".params.control.fields["
	slot, ok := err.Details["slot"].(string)
	if !ok || !strings.HasPrefix(slot, prefix) {
		return err
	}
	k, convErr := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(slot, prefix), "]"))
	if convErr != nil || k < 0 || k >= len(outside) {
		return err
	}
	j := 0
	for i, c := range controls {
		if c == outside[k] {
			j = i
			break
		}
	}
	details := make(map[string]any, len(err.Details))
	for key, v := range err.Details {
		details[key] = v
	}
	details["slot"] = at + ".params.control[" + strconv.Itoa(j) + "]"
	return errors.NewCodedErrorWithDetails(err.Code, err.Message, details)
}

// resolveControls places m.Controls against the resolved members: a
// control that is a member leaves the output axis; any other must be a
// numeric field the vector rules admit (ResolveSpec at
// "matrices[i].params.control": the field-reference and member-type
// refusals) and joins the fold as Extra. At least one member must stay
// on the output axis.
func resolveControls(at string, schema *encoding.Schema, m *Matrix) *errors.CodedError {
	if m.Controls == nil {
		return nil
	}
	members := m.Members.Members
	isControl := make(map[string]bool, len(m.Controls))
	var outside []string
	for _, c := range m.Controls {
		isControl[c] = true
		inside := false
		for _, f := range members {
			if f == c {
				inside = true
				break
			}
		}
		if !inside {
			outside = append(outside, c)
		}
	}
	if len(outside) > 0 {
		r, err := ResolveSpec(at+".params.control", types.VectorSpec{Name: m.Name, Fields: outside}, schema)
		if err != nil {
			return controlSlot(err, at, m.Controls, outside)
		}
		m.Extra = r.Members
	}
	m.Output = []int{}
	for i, f := range members {
		if !isControl[f] {
			m.Output = append(m.Output, i)
		}
	}
	if len(m.Output) == 0 {
		return matrixInvalid(at, "bad_params", at+" params.control names every member; at least one member must stay uncontrolled",
			map[string]any{"param": "control", "members": append([]string(nil), members...)})
	}
	return nil
}

// decodeMissing applies params.missing / params.max_drop_share: missing
// is "listwise" (default) or "pairwise"; max_drop_share is a share in
// [0, 1] and applies to listwise only (pairwise drops no whole row).
func decodeMissing(at string, p missingParams, m *Matrix) *errors.CodedError {
	if p.Missing != nil {
		switch *p.Missing {
		case MissingListwise:
		case MissingPairwise:
			m.Pairwise = true
		default:
			return matrixInvalid(at, "bad_params", at+" params.missing must be \"listwise\" or \"pairwise\"",
				map[string]any{"param": "missing", "value": *p.Missing, "valid": MissingModes()})
		}
	}
	if p.MaxDropShare != nil {
		s := *p.MaxDropShare
		if !(s >= 0 && s <= 1) {
			return matrixInvalid(at, "bad_params", at+" params.max_drop_share must be a share in [0, 1]",
				map[string]any{"param": "max_drop_share", "value": s})
		}
		if m.Pairwise {
			return matrixInvalid(at, "bad_params", at+" params.max_drop_share applies to missing \"listwise\" only; pairwise drops no whole row",
				map[string]any{"param": "max_drop_share", "missing": MissingPairwise})
		}
		v := s
		m.MaxDropShare = &v
	}
	if p.Fit != nil {
		if *p.Fit == "" {
			return matrixInvalid(at, "bad_fit", at+" params.fit must name a bound vector fit",
				map[string]any{"param": "fit"})
		}
		// A bound fit is one listwise state: no row is read, so the
		// missing-data knobs have nothing to act on.
		if p.Missing != nil || p.MaxDropShare != nil {
			param := "missing"
			if p.Missing == nil {
				param = "max_drop_share"
			}
			return matrixInvalid(at, "fit_and_missing", at+" sets params.fit and params."+param+"; a bound vector fit is one listwise state and reads no row — drop "+param,
				map[string]any{"param": param, "fit": *p.Fit})
		}
		m.Fit = *p.Fit
	}
	return nil
}

// resolveFit judges a params.fit slot once its members and the operator
// params resolved: the slot emits its result from the bound state, so
// an operator whose result is not a function of one listwise co-moment
// is refused (fit_unsupported — MAT_DISTANCE's per-group centroids, a
// buffered tier that reads rows: a rank method, a buffered extension;
// MAT_RELIABILITY's declared scale range, which checks and reverse-keys
// every row), and so is a grouped request (fit_grouped: every bucket
// would emit the one bound state).
func resolveFit(at string, req *types.Request, m *Matrix) *errors.CodedError {
	if m.Fit == "" {
		return nil
	}
	unsupported := func(why string) *errors.CodedError {
		return matrixInvalid(at, "fit_unsupported", at+" params.fit cannot bind "+string(m.Type)+": "+why,
			map[string]any{"param": "fit", "fit": m.Fit, "type": string(m.Type)})
	}
	switch {
	case m.Type == types.MAT_DISTANCE:
		return unsupported("its result compares per-group centroids, not one listwise state")
	case !m.Streamable:
		return unsupported("its operator reads the rows themselves (a rank method or a buffered operator), not the co-moment state")
	case m.HasScale:
		return unsupported("its declared scale range checks and reverse-keys every row; a bound state carries no rows")
	}
	if req != nil && (len(req.Groups) > 0 || req.Crosstab != nil) {
		return matrixInvalid(at, "fit_grouped", at+" params.fit on a grouped request would emit the one bound state in every bucket; drop groups or fit",
			map[string]any{"param": "fit", "fit": m.Fit})
	}
	return nil
}

// decodeInference applies params.inference / params.alpha (after
// params.method): inference true turns the per-pair p-values and
// confidence intervals on; alpha is the intervals' two-sided level,
// strictly between 0 and 1 (default InferenceDefaultAlpha), and applies
// with inference on only, on every MAT_CORRELATION method (a rank
// method's p is cor.test's for that method). implied is the spec's own
// `multiplicity` block (U28 E3-S4): a block needs the p-values, so it
// turns inference on when params leave it unset and refuses an
// explicit `inference: false`. Every refusal is bad_params.
func decodeInference(at string, p inferenceParams, implied bool, m *Matrix) *errors.CodedError {
	if implied && p.Inference != nil && !*p.Inference {
		return matrixInvalid(at, "bad_params", at+" params.inference is false, but the spec's multiplicity block corrects its p-values and implies inference; drop one of them",
			map[string]any{"param": "inference", "value": false})
	}
	on := implied || (p.Inference != nil && *p.Inference)
	if p.Alpha != nil {
		a := *p.Alpha
		if !on {
			return matrixInvalid(at, "bad_params", at+" params.alpha applies with params.inference true only",
				map[string]any{"param": "alpha", "value": a})
		}
		if !(a > 0 && a < 1) {
			return matrixInvalid(at, "bad_params", at+" params.alpha must be a number strictly between 0 and 1",
				map[string]any{"param": "alpha", "value": a})
		}
	}
	if !on {
		return nil
	}
	m.Inference = true
	m.Alpha = InferenceDefaultAlpha
	if p.Alpha != nil {
		m.Alpha = *p.Alpha
	}
	return nil
}

// decodeStrict decodes raw into v, refusing unknown keys.
func decodeStrict(raw []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

// MatrixMembers returns the union of every field req's matrix specs
// read beyond Request.Vectors — an inline spec's fields and a
// MAT_PARTIAL_CORRELATION params.control entry (a control outside the
// members joins the fold) — and whether every one resolved (projection
// decodes wide otherwise).
func MatrixMembers(req *types.Request, schema *encoding.Schema) (members []string, ok bool) {
	if req == nil || len(req.Matrices) == 0 {
		return nil, true
	}
	ok = true
	for i, spec := range req.Matrices {
		if spec.Type == types.MAT_PARTIAL_CORRELATION {
			members, ok = appendControlFields(members, ok, matrixPath(i), spec, schema)
		}
		if spec.Type == types.MAT_DISTANCE {
			members, ok = appendBetweenFields(members, ok, matrixPath(i), spec, schema)
		}
		if spec.Vector != "" || len(spec.Fields) == 0 {
			continue // vector members ride Members(req.Vectors)
		}
		r, err := ResolveSpec(matrixPath(i)+".fields", types.VectorSpec{Name: spec.EffectiveName(), Fields: spec.Fields}, schema)
		if err != nil {
			ok = false
			continue
		}
		members = append(members, r.SourceFields()...)
	}
	return members, ok
}

// appendControlFields appends spec's params.control fields that exist
// in the schema (a member repeated here is harmless: projection
// de-duplicates); a malformed control, or one the schema lacks, clears
// ok.
func appendControlFields(members []string, ok bool, at string, spec types.MatrixSpec, schema *encoding.Schema) ([]string, bool) {
	var p struct {
		Control json.RawMessage `json:"control"`
	}
	if len(spec.Params) == 0 || json.Unmarshal(spec.Params, &p) != nil {
		return members, ok
	}
	controls, err := decodeControl(at, p.Control)
	if err != nil {
		return members, false
	}
	for _, c := range controls {
		if schema == nil || schema.Field(c) == nil {
			ok = false
			continue
		}
		members = append(members, c)
	}
	return members, ok
}

// appendBetweenFields appends the columns a MAT_DISTANCE spec's
// params.between reads — its field, and a vector-input between's inline
// fields (GROUP_KMEANS, or an extension grouper, which may set both; a
// named vector's members ride Members(req.Vectors)); an undecodable
// between, or a column the schema lacks, clears ok.
func appendBetweenFields(members []string, ok bool, at string, spec types.MatrixSpec, schema *encoding.Schema) ([]string, bool) {
	g, has := SpecBetween(spec)
	if !has {
		return members, false
	}
	if len(g.Fields) > 0 {
		r, err := ResolveSpec(at+".params.between.fields", types.VectorSpec{Fields: g.Fields}, schema)
		if err != nil {
			return members, false
		}
		members = append(members, r.SourceFields()...)
	}
	if g.Field == "" {
		return members, ok && (g.Vector != "" || len(g.Fields) > 0)
	}
	if schema == nil || schema.Field(g.Field) == nil {
		return members, false
	}
	return append(members, g.Field), ok
}

// SpecBetween decodes a MAT_DISTANCE spec's params.between as written
// (lenient: unknown keys are not judged here); false on any other type
// or when it does not decode.
func SpecBetween(spec types.MatrixSpec) (types.Group, bool) {
	if spec.Type != types.MAT_DISTANCE || len(spec.Params) == 0 {
		return types.Group{}, false
	}
	var p struct {
		Between *types.Group `json:"between"`
	}
	if err := json.Unmarshal(spec.Params, &p); err != nil || p.Between == nil {
		return types.Group{}, false
	}
	return *p.Between, true
}

func builtinMatrixType(t types.MatrixType) bool {
	for _, k := range types.AllMatrixTypes() {
		if k == t {
			return true
		}
	}
	return false
}

func knownEncoding(e types.MatrixEncoding) bool {
	for _, k := range types.AllMatrixEncodings() {
		if k == e {
			return true
		}
	}
	return false
}

func encodingNames() []string {
	all := types.AllMatrixEncodings()
	out := make([]string, len(all))
	for i, e := range all {
		out[i] = string(e)
	}
	return out
}

func indexOfVector(specs []types.VectorSpec, name string) int {
	for i, s := range specs {
		if s.Name == name {
			return i
		}
	}
	return -1
}

func matrixPath(i int) string { return "matrices[" + strconv.Itoa(i) + "]" }

func matrixInvalid(at, reason, msg string, extra map[string]any) *errors.CodedError {
	details := map[string]any{"matrix": at, "reason": reason}
	for k, v := range extra {
		details[k] = v
	}
	return errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION, msg, details)
}
