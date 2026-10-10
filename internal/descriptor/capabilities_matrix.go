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
	// missing: the missing-data knobs every operator takes
	// (internal/vectors.decodeMissing).
	missing := []descriptor.Param{
		{Name: "missing", Type: "enum", Required: false, Default: vectors.MissingListwise, EnumValues: vectors.MissingModes(), Description: "listwise drops a row with any member null; pairwise computes each pair over the rows where both are present and adds auxiliary.n (pairwise N)."},
		{Name: "max_drop_share", Type: "float", Required: false, Description: "Listwise only, no default: warn PULSE_MATRIX_LISTWISE_HEAVY_DROP when the share of rows dropped exceeds it (0 to 1)."},
	}
	return []descriptor.MatrixMeta{
		{
			Name:         string(types.MAT_CORRELATION),
			Description:  "Correlation matrix of a vector's members (listwise or pairwise): Pearson r by default, or Spearman rho / Kendall tau-b under params.method; a zero-spread member's row and column are null. Pearson is weighted under frequency and probability weights, a rank method under frequency weights only. No p-values.",
			AcceptsTypes: memberTypes,
			Params: append(append([]descriptor.Param{
				{Name: "method", Type: "enum", Required: false, Default: vectors.CorrelationPearson, EnumValues: vectors.CorrelationMethods(), Description: "pearson (r over the co-moments; streamable, mergeable); spearman (rho, r on mid-ranks) or kendall (tau-b): each cell equals TEST_SPEARMAN_R / TEST_KENDALL_TAU over the pair's rows (pairwise re-ranks per pair); buffered and not mergeable, so the request runs serially; frequency weights only (a probability weight is PULSE_WEIGHT_UNSUPPORTED)."},
			}, missing...),
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
			Name:         string(types.MAT_PARTIAL_CORRELATION),
			Description:  "Partial correlation matrix: each pair's Pearson correlation with other fields held fixed, from the precision matrix of the folded columns' correlation (listwise or pairwise). params.control \"all\" (default) controls each pair for every other member; a field list controls each pair for exactly those fields and drops any member it names from the output. A decomposition operator: a non-PSD (pairwise) input is refused with PULSE_MATRIX_NOT_PSD unless params.repair is \"nearest\", a singular one is PULSE_MATRIX_SINGULAR. Weighted under frequency and probability weights. No p-values.",
			AcceptsTypes: memberTypes,
			Params: append([]descriptor.Param{
				{Name: "control", Type: "list", Required: false, Default: vectors.ControlAll, Description: "\"all\" (each pair controls for every other member: -P_ij/sqrt(P_ii*P_jj) of the precision matrix P) or a list of numeric field names: each output pair controls for exactly those fields. A listed member leaves the output axis; a listed field outside the members joins the co-moment and the missing-data mode."},
				{Name: "repair", Type: "enum", Required: false, EnumValues: []string{vectors.RepairNearest}, Description: "nearest: replace a non-PSD input correlation by its nearest correlation matrix (Higham alternating projections with Dykstra's correction, Matrix::nearPD corr = TRUE; at most 100 iterations, convergence 1e-7) and warn PULSE_MATRIX_NOT_PSD with frobenius_adjustment. Absent: a non-PSD input is refused (fatal PULSE_MATRIX_NOT_PSD)."},
			}, missing...),
			OutputKeys: descriptor.MatrixOutputKeys{
				Primary:   "partial_correlation",
				Auxiliary: []string{"n"},
			},
			Streamable:      types.MAT_PARTIAL_CORRELATION.Streamable(),
			Mergeable:       types.MAT_PARTIAL_CORRELATION.Mergeable(),
			ComponentSchema: matrixSchema(),
		},
		{
			Name:         string(types.MAT_RELIABILITY),
			Description:  "Scale reliability of a battery of items (listwise or pairwise): Cronbach's alpha and standardized alpha, McDonald's omega from a one-factor minres fit, the mean inter-item correlation, and per item the corrected item-total r, alpha if deleted, mean and sd; the primary is the inter-item correlation matrix. params.reverse flips reverse-keyed items x' = scale_min + scale_max - x before the fold. At least 2 items; omega needs 3 and is null with a warning on 2 items, on a Heywood fit, or on a non-PSD (pairwise) input without params.repair. Weighted under frequency and probability weights. No p-values.",
			AcceptsTypes: memberTypes,
			Params: append([]descriptor.Param{
				{Name: "reverse", Type: "list", Required: false, Description: "Items (members of the matrix) that are reverse-keyed: each value is replaced by scale_min + scale_max - x before the fold. Requires scale_min and scale_max (PROCESSING_CONFIG otherwise)."},
				{Name: "scale_min", Type: "float", Required: false, Description: "The battery's lowest possible response, with scale_max (both or neither; required with reverse). Every item value must lie in [scale_min, scale_max]: one outside is PROCESSING_CONFIG, never clamped, and the range is never inferred from the data."},
				{Name: "scale_max", Type: "float", Required: false, Description: "The battery's highest possible response (see scale_min)."},
				{Name: "repair", Type: "enum", Required: false, EnumValues: []string{vectors.RepairNearest}, Description: "nearest: fit omega on the nearest correlation matrix when the (pairwise) inter-item correlation is not PSD, with a PULSE_MATRIX_NOT_PSD warning carrying frobenius_adjustment. Absent: omega is null with that warning. Alpha never needs it."},
			}, missing...),
			OutputKeys: descriptor.MatrixOutputKeys{
				Primary:   "inter_item_correlation",
				Auxiliary: []string{"n"},
				Vectors:   []string{"alpha_if_deleted", "item_mean", "item_sd", "item_total_r"},
				Scalars:   []string{"alpha", "alpha_standardized", "mean_inter_item_r", "omega"},
			},
			Streamable: types.MAT_RELIABILITY.Streamable(),
			Mergeable:  types.MAT_RELIABILITY.Mergeable(),
			ComponentSchema: matrixSchema(
				descriptor.ComponentKey{Name: "iterations", Type: "int", Optional: true, Description: "Coordinate sweeps the one-factor minres fit behind omega ran (cap 1000); absent when no fit ran (2 items, an undefined or unrepaired non-PSD input)."},
				descriptor.ComponentKey{Name: "converged", Type: "bool", Optional: true, Description: "Whether the minres fit met its tolerance (1e-12) within the cap; false comes with PULSE_MATRIX_NOT_CONVERGED. Absent when no fit ran."},
			),
		},
		{
			Name:         string(types.MAT_PCA),
			Description:  "Principal component analysis of a vector's members (listwise or pairwise): the eigen-decomposition of their correlation (params.on \"correlation\", the default) or covariance matrix. The primary is the p x k loadings (eigenvector x sqrt(eigenvalue), a rectangular matrix with columns PC1..PCk), auxiliary.eigenvectors the p x k unit eigenvectors; vectors carry every eigenvalue, the explained and cumulative variance shares, the communalities over the kept components and each member's KMO measure of sampling adequacy; scalars the overall KMO, Bartlett's sphericity test (chi-square, df, p) and the number of components kept. Eigenvalues descending, each eigenvector's largest-magnitude entry positive. A decomposition operator: a non-PSD (pairwise) input is refused with PULSE_MATRIX_NOT_PSD unless params.repair is \"nearest\". Weighted under frequency and probability weights. No rotation.",
			AcceptsTypes: memberTypes,
			Params: append([]descriptor.Param{
				{Name: "on", Type: "enum", Required: false, Default: vectors.PCAOnCorrelation, EnumValues: vectors.PCAOnValues(), Description: "correlation: analyse the correlation matrix (every member on one scale); covariance: the covariance matrix (members in their own units, so a large-spread member dominates; params.components is then required)."},
				{Name: "components", Type: "any", Required: false, Default: vectors.PCAComponentsKaiser, Description: "How many components to keep: a positive integer k (at most the member count); \"kaiser\" (every component whose eigenvalue exceeds 1; the default on a correlation, refused on a covariance); or {\"variance\": share}, the fewest leading components whose cumulative explained share reaches share (0 < share <= 1). Required on a covariance. Every eigenvalue is reported either way."},
				{Name: "repair", Type: "enum", Required: false, EnumValues: []string{vectors.RepairNearest}, Description: "nearest: decompose the nearest correlation matrix (scaled back to the covariance's variances on a covariance) when the (pairwise) input is not PSD, with a PULSE_MATRIX_NOT_PSD warning carrying frobenius_adjustment. Absent: a non-PSD input is refused (fatal PULSE_MATRIX_NOT_PSD)."},
			}, missing...),
			OutputKeys: descriptor.MatrixOutputKeys{
				Primary:   "loadings",
				Auxiliary: []string{"eigenvectors", "n"},
				Vectors:   []string{"communalities", "cumulative", "eigenvalues", "explained_variance", "kmo_msa"},
				Scalars:   []string{"bartlett_chisq", "bartlett_df", "bartlett_p", "components_retained", "kmo"},
			},
			Streamable:      types.MAT_PCA.Streamable(),
			Mergeable:       types.MAT_PCA.Mergeable(),
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
			"Matrices follow Request.Groups: a grouped request returns one result per spec per non-empty bucket (group_key, group_header), spec-major then bucket in the final Data row order (Request.Sort included); a thin bucket is still emitted, with PULSE_MATRIX_INSUFFICIENT_N.",
			"A request carrying matrices fans out over DecodeWorkers and ShardWorkers with bit-identical results, per bucket on a grouped request.",
			"A matrix result is emitted at finalize: a streamed run carries it at terminal flush only.",
			"A rectangular matrix (kind \"rectangular\", MAT_PCA's p x k loadings and eigenvectors) has the members as rows and its own column keys, and is always written full whatever the spec's encoding.",
			"A request carrying matrices with joins, or a ProcessChain stage after 0 carrying matrices, is refused with PULSE_MATRIX_UNSUPPORTED_SOURCE; matrices with a crosstab are refused with PULSE_MATRIX_HOST_CONFLICT.",
		},
	}
}
