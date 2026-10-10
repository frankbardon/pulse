package types

import "encoding/json"

// MatrixType identifies a matrix operator: a whole-set reducer over a
// battery of numeric columns (a virtual vector, see VectorSpec) whose
// result is a matrix rather than a scalar. Request.Matrices carries the
// specs and Response.Matrices the results, one per spec in request
// order.
//
// Every built-in matrix operator folds its rows into per-merge-block
// linalg.CoMoment partials and combines them through the one fixed
// merge tree at finalize, so its result is a function of the filtered
// rows alone. Per-operator semantics live in the op-mat-* skills; the
// contract is .claude/reference/matrix-and-vectors.md.
type MatrixType string

const (
	// MAT_COVARIANCE is the covariance matrix of the members:
	// M2 / (W − ddof), params.ddof ∈ {0, 1} (default 1).
	// params.missing "listwise" (default: a row with any member null is
	// skipped) or "pairwise" (each pair over its own rows). Weighted
	// under frequency and probability weights (denominator Σw − ddof).
	MAT_COVARIANCE MatrixType = "MAT_COVARIANCE"
	// MAT_CORRELATION is the Pearson correlation matrix of the members:
	// C_ij / √(M2_ii·M2_jj), clamped to [−1, 1], the TEST_PEARSON_R
	// arithmetic. Listwise or pairwise (params.missing). A member with
	// zero spread has no defined correlation: its row and column
	// (diagonal included) are NaN (null on the wire). Weighted under frequency and probability weights
	// (r is scale-free, so the two kinds agree). No p-values.
	MAT_CORRELATION MatrixType = "MAT_CORRELATION"
	// MAT_PARTIAL_CORRELATION is the partial correlation matrix: each
	// pair's correlation with other fields held fixed, read off the
	// precision matrix of the members' Pearson correlations.
	// params.control "all" (default: each pair controls for every other
	// member) or a list of numeric fields (members or not; each output
	// pair controls for exactly those, and the output covers the
	// non-control members). A decomposition operator: a non-PSD input
	// (pairwise) is refused with PULSE_MATRIX_NOT_PSD unless
	// params.repair "nearest"; a singular one is PULSE_MATRIX_SINGULAR.
	// Weighted under frequency and probability weights. No p-values.
	MAT_PARTIAL_CORRELATION MatrixType = "MAT_PARTIAL_CORRELATION"
	// MAT_RELIABILITY is a battery's scale reliability: Cronbach's alpha
	// (raw and standardized), McDonald's omega from a one-factor minres
	// fit, the mean inter-item correlation and per-item diagnostics
	// (corrected item-total r, alpha if deleted, mean, sd), with the
	// inter-item correlation matrix as primary. params.reverse names
	// reverse-keyed items, flipped x' = scale_min + scale_max − x per
	// row before the fold (the range is required with it). At least 2
	// items; omega needs 3 and a positive-semidefinite input (or
	// params.repair "nearest"), and is null with a warning otherwise.
	// Weighted under frequency and probability weights. No p-values.
	MAT_RELIABILITY MatrixType = "MAT_RELIABILITY"
	// MAT_PCA is a principal component analysis of the members'
	// correlation (params.on "correlation", the default) or covariance
	// ("covariance") matrix: the primary is the p × k loadings
	// (eigenvector · √λ, MatrixKindRectangular, one column per retained
	// component), auxiliary.eigenvectors the p × k unit eigenvectors;
	// vectors carry every eigenvalue, the explained and cumulative
	// variance shares, the communalities and per-member KMO (MSA);
	// scalars KMO, Bartlett's sphericity test and the retained count.
	// params.components: an integer k, "kaiser" (λ > 1; the default on
	// a correlation, refused on a covariance) or {"variance": share};
	// required on a covariance. A decomposition operator (the shared
	// PSD guard, params.repair "nearest"). Sign and order follow
	// linalg.SymEigen. Weighted under frequency and probability
	// weights. No rotation.
	MAT_PCA MatrixType = "MAT_PCA"
)

// AllMatrixTypes returns every built-in matrix operator in alphabetical
// order.
func AllMatrixTypes() []MatrixType {
	return []MatrixType{
		MAT_CORRELATION,
		MAT_COVARIANCE,
		MAT_PARTIAL_CORRELATION,
		MAT_PCA,
		MAT_RELIABILITY,
	}
}

// Streamable reports whether the operator folds row by row: every
// built-in matrix operator does (its state is the per-block co-moment
// set), so the result is emitted at finalize — on the streaming path,
// at terminal flush. An unknown type is not streamable. This is the
// TYPE-level answer the manifest reports; routing reads the spec-level
// MatrixSpec.Streamable, which its params may turn off.
func (t MatrixType) Streamable() bool {
	switch t {
	case MAT_CORRELATION, MAT_COVARIANCE, MAT_PARTIAL_CORRELATION, MAT_PCA, MAT_RELIABILITY:
		return true
	}
	return false
}

// Mergeable reports whether the operator's running state combines
// across input partitions, so a request carrying it may fan out over
// DecodeWorkers / ShardWorkers. Every built-in matrix operator merges
// through the blocked merge tree (per-block co-moments keyed by
// absolute record position), so serial and every worker count return
// the same bits. An unknown type is not mergeable. Type-level, like
// Streamable: the merge gate reads MatrixSpec.Mergeable.
func (t MatrixType) Mergeable() bool {
	switch t {
	case MAT_CORRELATION, MAT_COVARIANCE, MAT_PARTIAL_CORRELATION, MAT_PCA, MAT_RELIABILITY:
		return true
	}
	return false
}

// MatrixEncoding selects how MatrixValues.Values lays out a square
// symmetric matrix.
type MatrixEncoding string

const (
	// MatrixEncodingFull emits every row in full: Values[r] has p
	// entries. The default.
	MatrixEncodingFull MatrixEncoding = "full"
	// MatrixEncodingUpper emits the upper triangle, diagonal included:
	// Values[r] has p − r entries, Values[r][k] being cell (r, r + k).
	MatrixEncodingUpper MatrixEncoding = "upper"
)

// AllMatrixEncodings returns every MatrixEncoding in manifest order.
func AllMatrixEncodings() []MatrixEncoding {
	return []MatrixEncoding{MatrixEncodingFull, MatrixEncodingUpper}
}

// MatrixKind names the shape of a MatrixValues.
type MatrixKind string

const (
	// MatrixKindSquareSymmetric is a p × p matrix whose rows and
	// columns are the same members in the same order, with
	// Values[r][c] == Values[c][r].
	MatrixKindSquareSymmetric MatrixKind = "square_symmetric"
	// MatrixKindRectangular is a p × k matrix whose rows are the
	// members (RowKeys) and whose columns are something else
	// (ColumnKeys: MAT_PCA's components "PC1" … "PCk"). It is always
	// written full — Values[r] has k entries — whatever the spec's
	// Encoding asks (upper applies to a symmetric matrix only), and k
	// may be 0 (Values rows empty).
	MatrixKindRectangular MatrixKind = "rectangular"
)

// AllMatrixKinds returns every MatrixKind.
func AllMatrixKinds() []MatrixKind {
	return []MatrixKind{MatrixKindSquareSymmetric, MatrixKindRectangular}
}

// MatrixSpec is one matrix operator entry in Request.Matrices.
//
// The members come from exactly one of Vector (the name of a
// Request.Vectors entry) and Fields (an inline member list resolved by
// the same rules as VectorSpec.Fields). A spec naming both or neither
// is refused (SERVICE_VALIDATION); a Vector no Request.Vectors entry
// defines is PULSE_VECTOR_UNKNOWN. Gated by capability:matrices.
type MatrixSpec struct {
	// Name keys the result (MatrixResult.Name). Optional: defaults to
	// "<TYPE>_<vector>" for a vector spec and to "<TYPE>" for an inline
	// one. Names are unique per request.
	Name string `json:"name,omitempty"`
	// Type names the matrix operator (MAT_COVARIANCE, MAT_CORRELATION).
	Type MatrixType `json:"type"`
	// Vector names the Request.Vectors entry whose members are the
	// matrix's axes.
	Vector string `json:"vector,omitempty"`
	// Fields lists inline members (literal names and / or glob
	// entries), the VectorSpec.Fields rules.
	Fields []string `json:"fields,omitempty"`
	// Params carries the operator's parameters (MAT_COVARIANCE:
	// "ddof": 0 | 1; both: "missing": "listwise" | "pairwise",
	// "max_drop_share": a share in [0, 1], listwise only, no default;
	// MAT_CORRELATION only: "summary": {"top_pairs": k}, k a positive
	// integer, and "method"; MAT_PARTIAL_CORRELATION: "control": "all" |
	// [fields], "repair": "nearest"; MAT_RELIABILITY: "reverse":
	// [fields], "scale_min" / "scale_max": numbers, "repair":
	// "nearest"; MAT_PCA: "on": "correlation" | "covariance",
	// "components": k | "kaiser" | {"variance": share}, "repair":
	// "nearest").
	Params json.RawMessage `json:"params,omitempty"`
	// Weight is the per-slot weight override: absent inherits
	// Request.Weight (then Options.DefaultWeight); null opts the slot
	// out; a WeightSpec weights it. See .claude/reference/weighting.md.
	Weight SlotWeight `json:"weight,omitzero"`
	// Encoding selects the Values layout of every matrix in the result
	// (MatrixEncodingFull when empty).
	Encoding MatrixEncoding `json:"encoding,omitempty"`
}

// Streamable reports whether this concrete spec folds row by row, so a
// request carrying it may run on the streaming path. It folds the
// type-level answer (MatrixType.Streamable) with the spec's params: a
// params choice that needs every row at once (a rank method on
// MAT_CORRELATION — any params.method other than "pearson") makes the
// spec buffered. This, not the type-level method, is what the runtime
// streaming gate, predict and the engine's slot builder read, so they
// cannot disagree. Malformed params read as the type's own answer; the
// field-reference pass refuses them before any run.
func (s MatrixSpec) Streamable() bool { return s.Type.Streamable() && !s.bufferedParams() }

// Mergeable reports whether this concrete spec's running state combines
// across input partitions, so a request carrying it may fan out over
// DecodeWorkers / ShardWorkers. Like Streamable it folds the type-level
// answer (MatrixType.Mergeable) with the spec's params: a buffered spec
// keeps its rows, which do not merge, so it is never mergeable and the
// request runs serially (internal/mergegate.MergeRefusal).
func (s MatrixSpec) Mergeable() bool { return s.Type.Mergeable() && !s.bufferedParams() }

// bufferedParams reports whether the spec's params select a computation
// that needs the whole row set (a rank method). Only params.method on
// MAT_CORRELATION does today: absent or "pearson" folds co-moments;
// anything else ranks.
func (s MatrixSpec) bufferedParams() bool {
	if s.Type != MAT_CORRELATION || len(s.Params) == 0 {
		return false
	}
	var p struct {
		Method *string `json:"method"`
	}
	if err := json.Unmarshal(s.Params, &p); err != nil || p.Method == nil {
		return false
	}
	return *p.Method != "pearson"
}

// EffectiveEncoding returns the spec's Encoding, MatrixEncodingFull
// when empty.
func (s MatrixSpec) EffectiveEncoding() MatrixEncoding {
	if s.Encoding == "" {
		return MatrixEncodingFull
	}
	return s.Encoding
}

// EffectiveName returns the spec's result name: Name when set, else
// "<TYPE>_<vector>" for a vector spec and "<TYPE>" for an inline one.
func (s MatrixSpec) EffectiveName() string {
	switch {
	case s.Name != "":
		return s.Name
	case s.Vector != "":
		return string(s.Type) + "_" + s.Vector
	}
	return string(s.Type)
}

// MatrixValues is one matrix of a MatrixResult. RowKeys and ColumnKeys
// are the member field names in axis order (the vector's resolved
// order) — on a MatrixKindRectangular matrix ColumnKeys name its
// columns instead (MAT_PCA: "PC1" … "PCk"); Labels, when the vector
// carried display labels, are the row members' labels in the same
// order. Values follows Encoding. An undefined cell
// (no mass, zero spread) is NaN in Go and null on the wire.
type MatrixValues struct {
	Kind       MatrixKind     `json:"kind"`
	Encoding   MatrixEncoding `json:"encoding"`
	RowKeys    []string       `json:"row_keys"`
	ColumnKeys []string       `json:"column_keys"`
	Labels     []string       `json:"labels,omitempty"`
	Values     [][]float64    `json:"values"`
}

// MatrixResult is the result of one MatrixSpec. Primary is the
// operator's matrix (MAT_COVARIANCE: the covariance; MAT_CORRELATION:
// Pearson r); Auxiliary holds
// same-shape companion matrices keyed by name ("n": the pairwise N,
// under params.missing "pairwise" only); Vectors per-member or
// per-pair summaries ("top_pairs": a []MatrixPair, MAT_CORRELATION
// under params.summary.top_pairs only); Scalars whole-matrix figures (determinant: the
// determinant of Primary by the reference Cholesky, null when Primary
// is not positive definite). Warnings are this matrix's data-quality
// diagnostics (PULSE_MATRIX_INSUFFICIENT_N, _ZERO_VARIANCE,
// _LISTWISE_HEAVY_DROP, _NOT_PSD).
// GroupKey / GroupHeader identify the group bucket when the request is
// grouped and are omitted otherwise.
type MatrixResult struct {
	Name        string                   `json:"name"`
	Type        MatrixType               `json:"type"`
	GroupKey    AxisKey                  `json:"group_key,omitempty"`
	GroupHeader *AxisHeader              `json:"group_header,omitempty"`
	Primary     *MatrixValues            `json:"primary"`
	Auxiliary   map[string]*MatrixValues `json:"auxiliary,omitempty"`
	Vectors     map[string]any           `json:"vectors,omitempty"`
	Scalars     map[string]float64       `json:"scalars,omitempty"`
	Warnings    []*ResponseWarning       `json:"warnings,omitempty"`
}

// MatrixPair is one entry of a MAT_CORRELATION result's
// vectors.top_pairs: an off-diagonal pair of members (Row before Col in
// axis order), its Pearson r and the rows it was computed over (the
// pair's own N under params.missing "pairwise", the listwise N
// otherwise). top_pairs lists the k pairs with the largest |r|, ties
// broken by (row, col) axis order; the diagonal and pairs with an
// undefined r are never listed.
type MatrixPair struct {
	Row string  `json:"row"`
	Col string  `json:"col"`
	R   float64 `json:"r"`
	N   int     `json:"n"`
}

// MarshalJSON writes the matrix values with every non-finite float as
// null; see MarshalFinite.
func (v MatrixValues) MarshalJSON() ([]byte, error) {
	type alias MatrixValues
	return MarshalFinite((*alias)(&v))
}

// MarshalJSON writes the matrix result with every non-finite float as
// null; see MarshalFinite.
func (v MatrixResult) MarshalJSON() ([]byte, error) {
	type alias MatrixResult
	return MarshalFinite((*alias)(&v))
}

// MatrixComponents is one Response.Components.Matrices entry: the
// constituent counts behind one MatrixResult, keyed back to it by Name
// (and, on a grouped result, GroupKey). The floor — N, NNull,
// NListwiseDropped — is on every entry; MinPairN / MaxPairN only under
// params.missing "pairwise"; the weighted floor (SumWeights, NEff,
// NWeightInvalid) only when a row weight is APPLIED to the slot, NEff
// only under kind probability. Operator carries the operator's own
// declared keys (MAT_COVARIANCE: ddof).
type MatrixComponents struct {
	// Name mirrors MatrixResult.Name.
	Name string `json:"name"`
	// Type mirrors MatrixResult.Type.
	Type MatrixType `json:"type"`
	// GroupKey mirrors MatrixResult.GroupKey on a grouped result;
	// omitted on an ungrouped one.
	GroupKey AxisKey `json:"group_key,omitempty"`

	// N is the rows the matrix's co-moment counted: listwise, the
	// complete rows; pairwise, the rows with at least one member
	// present. A row of weight 0 counts (it adds no mass); a row with
	// an invalid weight does not (NWeightInvalid).
	N int `json:"n"`
	// NNull is the filter-passing rows skipped for missing members:
	// listwise, the rows with any member null (== NListwiseDropped);
	// pairwise, the rows with every member null.
	NNull int `json:"n_null"`
	// NListwiseDropped is the rows listwise deletion dropped; 0 under
	// pairwise.
	NListwiseDropped int `json:"n_listwise_dropped"`

	// MinPairN / MaxPairN are the smallest and largest pair N over the
	// upper triangle, diagonal included (the extremes of
	// auxiliary.n) — set under params.missing "pairwise" only.
	MinPairN *int `json:"min_pair_n,omitempty"`
	MaxPairN *int `json:"max_pair_n,omitempty"`

	// SumWeights is Σw over the rows N counts — set only when a row
	// weight is applied to the slot.
	SumWeights *float64 `json:"sum_weights,omitempty"`
	// NEff is Kish's effective sample size (Σw)² / Σw² over the same
	// rows — set only on a slot weighted with kind probability.
	NEff *float64 `json:"n_eff,omitempty"`
	// NWeightInvalid counts the rows the mode admitted whose weight was
	// invalid (null, negative, NaN / ±Inf, non-integer under kind
	// frequency) and was therefore excluded — set only on a weighted
	// slot.
	NWeightInvalid *int `json:"n_weight_invalid,omitempty"`

	// Operator carries the operator's schema-declared keys
	// (internal/descriptor/capabilities_matrix.go): MAT_COVARIANCE
	// {ddof}; MAT_CORRELATION none (omitted).
	Operator map[string]any `json:"operator,omitempty"`
}

// MarshalJSON writes the matrix components with every non-finite float
// as null; see MarshalFinite.
func (v MatrixComponents) MarshalJSON() ([]byte, error) {
	type alias MatrixComponents
	return MarshalFinite((*alias)(&v))
}
