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
	// M2 / (W − ddof), params.ddof ∈ {0, 1} (default 1). Listwise:
	// a row with any member null is skipped. Weighted under frequency
	// and probability weights (denominator Σw − ddof).
	MAT_COVARIANCE MatrixType = "MAT_COVARIANCE"
)

// AllMatrixTypes returns every built-in matrix operator in alphabetical
// order.
func AllMatrixTypes() []MatrixType {
	return []MatrixType{
		MAT_COVARIANCE,
	}
}

// Streamable reports whether the operator folds row by row: every
// built-in matrix operator does (its state is the per-block co-moment
// set), so the result is emitted at finalize — on the streaming path,
// at terminal flush. An unknown type is not streamable.
func (t MatrixType) Streamable() bool {
	switch t {
	case MAT_COVARIANCE:
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
)

// AllMatrixKinds returns every MatrixKind.
func AllMatrixKinds() []MatrixKind {
	return []MatrixKind{MatrixKindSquareSymmetric}
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
	// Type names the matrix operator (MAT_COVARIANCE).
	Type MatrixType `json:"type"`
	// Vector names the Request.Vectors entry whose members are the
	// matrix's axes.
	Vector string `json:"vector,omitempty"`
	// Fields lists inline members (literal names and / or glob
	// entries), the VectorSpec.Fields rules.
	Fields []string `json:"fields,omitempty"`
	// Params carries the operator's parameters (MAT_COVARIANCE:
	// {"ddof": 0 | 1}).
	Params json.RawMessage `json:"params,omitempty"`
	// Weight is the per-slot weight override: absent inherits
	// Request.Weight (then Options.DefaultWeight); null opts the slot
	// out; a WeightSpec weights it. See .claude/reference/weighting.md.
	Weight SlotWeight `json:"weight,omitzero"`
	// Encoding selects the Values layout of every matrix in the result
	// (MatrixEncodingFull when empty).
	Encoding MatrixEncoding `json:"encoding,omitempty"`
}

// Streamable reports whether the spec's operator folds row by row.
func (s MatrixSpec) Streamable() bool { return s.Type.Streamable() }

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
// order); Labels, when the vector carried display labels, are their
// labels in the same order. Values follows Encoding. An undefined cell
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
// operator's matrix (MAT_COVARIANCE: the covariance); Auxiliary holds
// same-shape companion matrices keyed by name; Vectors per-member or
// per-pair summaries; Scalars whole-matrix figures (determinant: the
// determinant of Primary by the reference Cholesky, null when Primary
// is not positive definite). Warnings are this matrix's diagnostics.
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
