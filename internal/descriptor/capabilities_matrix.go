package descriptor

import (
	"github.com/frankbardon/pulse/descriptor"
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
	return []descriptor.MatrixMeta{
		{
			Name:         string(types.MAT_COVARIANCE),
			Description:  "Covariance matrix of a vector's members (listwise): sample covariance by default, weighted under frequency and probability weights.",
			AcceptsTypes: memberTypes,
			Params: []descriptor.Param{
				{Name: "ddof", Type: "enum", Required: false, Default: "1", EnumValues: []string{"0", "1"}, Description: "Delta degrees of freedom: the denominator is Σw − ddof (n − ddof unweighted)."},
			},
			OutputKeys: descriptor.MatrixOutputKeys{
				Primary: "covariance",
				Scalars: []string{"determinant"},
			},
			Streamable: types.MAT_COVARIANCE.Streamable(),
		},
	}
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
		MissingModes:    []string{"listwise"},
		MergeBlockSize:  linalg.MergeBlockSize,
		Limitations: []string{
			"Matrices are computed over the whole filtered row set; a grouped request still returns one ungrouped matrix per spec.",
			"Matrices run on the serial streaming and buffered paths; a request carrying matrices does not fan out over DecodeWorkers or ShardWorkers.",
			"A matrix result is emitted at finalize: a streamed run carries it at terminal flush only.",
			"A request carrying matrices with joins, or a ProcessChain stage after 0 carrying matrices, is refused with PULSE_MATRIX_UNSUPPORTED_SOURCE; matrices with a crosstab are refused with PULSE_MATRIX_HOST_CONFLICT.",
		},
	}
}
