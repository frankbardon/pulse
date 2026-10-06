package descriptor

import (
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/types"
)

// matrixCapabilities returns metadata for every entry in
// types.AllMatrixTypes(). Order is irrelevant; manifest assembly sorts
// by Name.
func matrixCapabilities() []descriptor.MatrixMeta {
	// Vector members: integer and float fields, plus packed_bool under
	// the vector's coerce "binary" (internal/vectors.MemberTypeAllowed).
	memberTypes := []string{"f32", "f64", "packed_bool", "u16", "u32", "u4", "u64", "u8"}
	// missing: the missing-data knobs both operators take
	// (internal/vectors.decodeMissing).
	missing := []descriptor.Param{
		{Name: "missing", Type: "enum", Required: false, Default: vectors.MissingListwise, EnumValues: vectors.MissingModes(), Description: "listwise drops a row with any member null; pairwise computes each pair over the rows where both are present and adds auxiliary.n (pairwise N)."},
		{Name: "max_drop_share", Type: "float", Required: false, Description: "Listwise only, no default: warn PULSE_MATRIX_LISTWISE_HEAVY_DROP when the share of rows dropped exceeds it (0 to 1)."},
	}
	return []descriptor.MatrixMeta{
		{
			Name:         string(types.MAT_CORRELATION),
			Description:  "Pearson correlation matrix of a vector's members (listwise or pairwise), r clamped to [-1, 1]; a zero-spread member's row and column are null. Weighted under frequency and probability weights. No p-values.",
			AcceptsTypes: memberTypes,
			Params: append(append([]descriptor.Param(nil), missing...),
				descriptor.Param{Name: "summary", Type: "object", Required: false, Description: "{\"top_pairs\": k}, k a positive integer: list the k off-diagonal pairs with the largest |r| as vectors.top_pairs [{row, col, r, n}], ties in axis order; undefined pairs are skipped and k past the pair count lists every pair."},
			),
			OutputKeys: descriptor.MatrixOutputKeys{
				Primary:   "correlation",
				Auxiliary: []string{"n"},
				Vectors:   []string{"top_pairs"},
				Scalars:   []string{"determinant"},
			},
			Streamable:      types.MAT_CORRELATION.Streamable(),
			Mergeable:       types.MAT_CORRELATION.Mergeable(),
			ComponentSchema: matrixSchema(),
		},
		{
			Name:         string(types.MAT_COVARIANCE),
			Description:  "Covariance matrix of a vector's members (listwise or pairwise): sample covariance by default, weighted under frequency and probability weights.",
			AcceptsTypes: memberTypes,
			Params: append([]descriptor.Param{
				{Name: "ddof", Type: "enum", Required: false, Default: "1", EnumValues: []string{"0", "1"}, Description: "Delta degrees of freedom: the denominator is Σw − ddof (n − ddof unweighted)."},
			}, missing...),
			OutputKeys: descriptor.MatrixOutputKeys{
				Primary:   "covariance",
				Auxiliary: []string{"n"},
				Scalars:   []string{"determinant"},
			},
			Streamable: types.MAT_COVARIANCE.Streamable(),
			Mergeable:  types.MAT_COVARIANCE.Mergeable(),
			ComponentSchema: matrixSchema(
				descriptor.ComponentKey{Name: "ddof", Type: "int", Description: "The delta degrees of freedom the covariance used (params.ddof, default 1)."},
			),
		},
	}
}

// matrixFloorKeys are the Response.Components.Matrices floor every
// matrix entry carries, then the pairwise-only keys
// (types.MatrixComponents).
func matrixFloorKeys() []descriptor.ComponentKey {
	return []descriptor.ComponentKey{
		{Name: "n", Type: "int", Description: "Rows the co-moment counted: listwise the complete rows, pairwise the rows with any member present; weight-0 rows count."},
		{Name: "n_null", Type: "int", Description: "Rows skipped for missing members: listwise any member null, pairwise every member null."},
		{Name: "n_listwise_dropped", Type: "int", Description: "Rows listwise deletion dropped; 0 under pairwise."},
		{Name: "min_pair_n", Type: "int", Optional: true, Description: "Pairwise only: the smallest pair N (the minimum of auxiliary.n)."},
		{Name: "max_pair_n", Type: "int", Optional: true, Description: "Pairwise only: the largest pair N (the maximum of auxiliary.n)."},
	}
}

// matrixSchema composes a matrix operator's ComponentSchema: the floor
// keys, then the operator's own keys. Every matrix operator is
// Mergeable (its counts and co-moments fold through the blocked merge).
// The optional weighted floor keys are spliced in after the floor and
// pairwise keys (the types.MatrixComponents wire order) by
// withMatrixWeightKeys on an instance that offers capability:weighting.
func matrixSchema(extra ...descriptor.ComponentKey) descriptor.ComponentSchema {
	return descriptor.ComponentSchema{
		Keys:         append(matrixFloorKeys(), extra...),
		Mergeability: descriptor.Mergeable,
	}
}

// withMatrixWeightKeys splices the optional weighted floor keys
// (sum_weights, n_eff, n_weight_invalid) in after each matrix
// operator's floor and pairwise keys — every built-in matrix operator
// honours a row weight.
func withMatrixWeightKeys(ms []descriptor.MatrixMeta) []descriptor.MatrixMeta {
	floor := len(matrixFloorKeys())
	for i := range ms {
		keys := ms[i].ComponentSchema.Keys
		out := make([]descriptor.ComponentKey, 0, len(keys)+3)
		out = append(out, keys[:floor]...)
		out = append(out, weightFloorKeys()...)
		out = append(out, keys[floor:]...)
		ms[i].ComponentSchema.Keys = out
	}
	return ms
}

// matrixComponentSchemas is the Manifest.ComponentsSchemas.Matrices
// projection of the (instance-filtered) matrix metas; nil when empty.
func matrixComponentSchemas(ms []descriptor.MatrixMeta) map[string]descriptor.ComponentSchema {
	if len(ms) == 0 {
		return nil
	}
	out := make(map[string]descriptor.ComponentSchema, len(ms))
	for _, m := range ms {
		out[m.Name] = m.ComponentSchema
	}
	return out
}

// sortMatrices returns ms sorted by Name.
func sortMatrices(ms []descriptor.MatrixMeta) []descriptor.MatrixMeta {
	out := append([]descriptor.MatrixMeta(nil), ms...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].Name < out[j-1].Name; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// matrixCapability is the Manifest.Matrix block.
func matrixCapability() descriptor.MatrixCapability {
	encs := make([]string, 0, len(types.AllMatrixEncodings()))
	for _, e := range types.AllMatrixEncodings() {
		encs = append(encs, string(e))
	}
	kinds := make([]string, 0, len(types.AllMatrixKinds()))
	for _, k := range types.AllMatrixKinds() {
		kinds = append(kinds, string(k))
	}
	return descriptor.MatrixCapability{
		Name:            "matrices",
		Encodings:       encs,
		DefaultEncoding: string(types.MatrixEncodingFull),
		Kinds:           kinds,
		MissingModes:    vectors.MissingModes(),
		MergeBlockSize:  linalg.MergeBlockSize,
		Limitations: []string{
			"Matrices are computed over the whole filtered row set; a grouped request still returns one ungrouped matrix per spec.",
			"An ungrouped request carrying matrices fans out over DecodeWorkers and ShardWorkers with bit-identical results; a grouped one runs serially.",
			"A matrix result is emitted at finalize: a streamed run carries it at terminal flush only.",
			"A request carrying matrices with joins, or a ProcessChain stage after 0 carrying matrices, is refused with PULSE_MATRIX_UNSUPPORTED_SOURCE; matrices with a crosstab are refused with PULSE_MATRIX_HOST_CONFLICT.",
		},
	}
}
