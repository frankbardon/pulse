package types

import (
	"encoding/json"
	"math"
)

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
	// weights. params.rotation: an orthogonal criterion ("varimax",
	// "quartimax", "geomin_orthogonal" with params.epsilon, "entropy");
	// an oblique one is refused.
	MAT_PCA MatrixType = "MAT_PCA"
	// MAT_COLLINEARITY is a collinearity check of the members as
	// regression predictors (no response): the primary is their
	// correlation matrix R; vectors carry each member's variance
	// inflation factor VIF = diag(R⁻¹) and tolerance 1/VIF (car::vif),
	// and Belsley's condition indices; auxiliary.variance_decomposition
	// is Belsley's variance-decomposition proportions
	// (MatrixKindRectangular: a row per variable, a column per
	// dimension); scalars condition_number (the largest condition
	// index) and max_vif (the largest VIF).
	// Belsley runs on the scaled, uncentered predictors with an
	// intercept by default (perturb::colldiag), rebuilt from the
	// co-moment as W(Σ + μμᵀ); params.center true runs it on the
	// centered predictors (R) instead. A decomposition operator (the
	// shared PSD guard, params.repair "nearest"); a singular R is
	// PULSE_MATRIX_SINGULAR. Weighted under frequency and probability
	// weights. No p-values.
	MAT_COLLINEARITY MatrixType = "MAT_COLLINEARITY"
	// MAT_FACTOR is an exploratory factor analysis of the members'
	// correlation matrix: params.factors k (an integer, or "kaiser",
	// the default: the eigenvalues of R above 1) common factors
	// extracted by params.extraction "minres" (the default, the
	// minimum-residual fit, psych fm = "minres"), "principal_axis"
	// (iterated principal axes, psych fm = "pa") or "ml" (maximum
	// likelihood, stats::factanal; adds the likelihood chi-square, TLI,
	// RMSEA with its interval at params.conf_level and BIC scalars),
	// each from the squared multiple
	// correlations, then rotated by params.rotation "none"
	// (the default), orthogonal "varimax", "quartimax", "entropy",
	// "geomin_orthogonal" (params.epsilon, default 0.01) or oblique
	// "promax" (params.kappa, default 4), "oblimin" (params.gamma,
	// default 0), "geomin" (params.epsilon). The primary is the p × k
	// rotated loadings (MatrixKindRectangular, columns "F1" … "Fk");
	// auxiliary.unrotated the unrotated loadings (rotated runs) and
	// auxiliary.factor_correlation the k × k factor correlation
	// (oblique rotations only); vectors carry the communalities,
	// uniquenesses and the variance each factor accounts for; scalars
	// the root-mean-square residual, the empirical residual chi-square
	// (not a likelihood test), KMO, Bartlett's sphericity test and the
	// fit's iterations / convergence. A k the battery cannot identify,
	// (p − k)² < p + k, nulls the fit with PULSE_MATRIX_NOT_IDENTIFIED;
	// a uniqueness at or below 0 is reported as is with
	// PULSE_MATRIX_HEYWOOD. A decomposition operator (the shared PSD
	// guard, params.repair "nearest"). Weighted under frequency and
	// probability weights.
	MAT_FACTOR MatrixType = "MAT_FACTOR"
	// MAT_DISTANCE is the k × k distance matrix between the centroids of
	// the buckets a grouper (params.between: any built-in grouper but
	// the date family, GROUP_KMEANS included) cuts the rows into. A
	// centroid is the bucket's (weighted) mean of every member; the
	// primary is square symmetric, keyed by the bucket labels in the
	// grouper's emission order, with a zero diagonal. params.metric is
	// a linalg/metric registry name the MAT_DISTANCE consumer accepts:
	// "euclidean" (the default), "sq_euclidean", "manhattan",
	// "chebyshev", "cosine", "hellinger", "aitchison" or "mahalanobis"
	// (the pooled within-bucket covariance); params.standardize
	// z-scores the members over the bucketed rows first; params.min_valid
	// (default 1) is the fewest complete member pairs a distance needs.
	// An undefined coordinate is dropped pairwise, the element sums
	// rescaled by p/p_observed. Rows the grouper puts in no bucket (a null
	// key) are excluded and counted. vectors.sizes carries each
	// bucket's mass, auxiliary.centroids the k × p centroids. Listwise
	// only. Streamable and mergeable when the grouper is (one co-moment
	// per bucket); otherwise buffered and serial. Weighted under
	// frequency and probability weights (a GROUP_KMEANS between under
	// frequency weights only).
	MAT_DISTANCE MatrixType = "MAT_DISTANCE"
	// MAT_SEM is a structural equation model fitted by maximum
	// likelihood to the members' covariance: params.model is a lavaan /
	// semopy description (=~ measurement, ~ regression, ~~ covariance,
	// label* equality and number* fixed multipliers; absent: one factor
	// over every member) whose observed variables are exactly the
	// members (by label). The default setup reproduces semopy's
	// semopy.Model(desc).fit(data) — the Wishart ML objective over the
	// biased covariance minimised by SciPy's SLSQP at ftol 1e-6, its
	// early stop included; params.solver "fisher" converges to the
	// optimum instead, params.information "rank2" computes the same
	// derivatives more cheaply. The primary is the model-implied
	// covariance Σ(θ̂); auxiliary.estimates is the parameter table
	// (MatrixKindRectangular: a row per "lval op rval", columns
	// estimate, std_estimate, std_err, z_value, p_value) and
	// auxiliary.residual_covariance S − Σ(θ̂); vectors carry each row's
	// parameter name and whether it is free; scalars the fit indices
	// (chi-square and its p, the baseline chi-square, CFI, GFI, AGFI,
	// NFI, TLI, RMSEA, AIC, BIC, semopy's LogLik) and the fit's
	// iterations / convergence. Listwise only. Weighted under frequency
	// weights (exactly the fit on the expanded rows); a probability
	// weight is refused.
	MAT_SEM MatrixType = "MAT_SEM"
)

// AllMatrixTypes returns every built-in matrix operator in alphabetical
// order.
func AllMatrixTypes() []MatrixType {
	return []MatrixType{
		MAT_COLLINEARITY,
		MAT_CORRELATION,
		MAT_COVARIANCE,
		MAT_DISTANCE,
		MAT_FACTOR,
		MAT_PARTIAL_CORRELATION,
		MAT_PCA,
		MAT_RELIABILITY,
		MAT_SEM,
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
	case MAT_COLLINEARITY, MAT_CORRELATION, MAT_COVARIANCE, MAT_DISTANCE, MAT_FACTOR, MAT_PARTIAL_CORRELATION, MAT_PCA, MAT_RELIABILITY, MAT_SEM:
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
	case MAT_COLLINEARITY, MAT_CORRELATION, MAT_COVARIANCE, MAT_DISTANCE, MAT_FACTOR, MAT_PARTIAL_CORRELATION, MAT_PCA, MAT_RELIABILITY, MAT_SEM:
		return true
	}
	return false
}

// MatrixEncoding selects how a MatrixValues is laid out: Values for a
// square symmetric matrix (full / upper), or the wire as row records.
type MatrixEncoding string

const (
	// MatrixEncodingFull emits every row in full: Values[r] has p
	// entries. The default.
	MatrixEncodingFull MatrixEncoding = "full"
	// MatrixEncodingUpper emits the upper triangle, diagonal included:
	// Values[r] has p − r entries, Values[r][k] being cell (r, r + k).
	MatrixEncodingUpper MatrixEncoding = "upper"
	// MatrixEncodingRecords writes every matrix of the result — primary
	// and each auxiliary, rectangular ones included — as row records:
	// {kind, encoding, columns, rows, labels?}, rows[r] an object
	// {"key": RowKeys[r], <column>: cell, …} in RowKeys order, an
	// undefined cell null, plus the row's RecordFields. In Go, Values
	// keeps the full layout (Records derives the rows); only the wire
	// differs.
	MatrixEncodingRecords MatrixEncoding = "records"
)

// MatrixRecordKey is the record field holding a row's key under
// MatrixEncodingRecords; a column of that name is refused.
const MatrixRecordKey = "key"

// AllMatrixEncodings returns every MatrixEncoding in manifest order.
func AllMatrixEncodings() []MatrixEncoding {
	return []MatrixEncoding{MatrixEncodingFull, MatrixEncodingUpper, MatrixEncodingRecords}
}

// MatrixKind names the shape of a MatrixValues.
type MatrixKind string

const (
	// MatrixKindSquareSymmetric is a p × p matrix whose rows and
	// columns are the same members in the same order, with
	// Values[r][c] == Values[c][r]. Two square matrices are keyed by
	// something else: MAT_FACTOR's auxiliary.factor_correlation (the
	// factors "F1" … "Fk", always written full) and MAT_DISTANCE's
	// primary (the between grouper's bucket labels, in the spec's
	// encoding).
	MatrixKindSquareSymmetric MatrixKind = "square_symmetric"
	// MatrixKindRectangular is a p × k matrix whose rows are the
	// members (RowKeys) and whose columns are something else
	// (ColumnKeys: MAT_PCA's components "PC1" … "PCk"; MAT_FACTOR's
	// factors "F1" … "Fk"; MAT_COLLINEARITY's dimensions "D1" … "Dk", whose uncentered
	// variance decomposition also leads its rows with the intercept,
	// RowKeys "(intercept)" then the members) — or, on MAT_DISTANCE's
	// auxiliary.centroids, a k × p matrix whose rows are the bucket
	// labels and whose columns are the members. It is always
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
	// every operator but MAT_DISTANCE: "fit": a bound vector fit's name
	// (a Compose fits entry) the result is emitted from, folding no row;
	// MAT_CORRELATION only: "summary": {"top_pairs": k}, k a positive
	// integer, and "method"; MAT_PARTIAL_CORRELATION: "control": "all" |
	// [fields], "repair": "nearest"; MAT_RELIABILITY: "reverse":
	// [fields], "scale_min" / "scale_max": numbers, "repair":
	// "nearest"; MAT_PCA: "on": "correlation" | "covariance",
	// "components": k | "kaiser" | {"variance": share}, "rotation"
	// (orthogonal), "epsilon", "repair": "nearest"; MAT_COLLINEARITY:
	// "center": bool, "repair": "nearest"; MAT_FACTOR: "factors": k | "kaiser", "extraction": "minres" |
	// "principal_axis" | "ml", "rotation": "none" | "varimax" | "promax" |
	// "oblimin" | "quartimax" | "geomin" | "geomin_orthogonal" |
	// "entropy", "kappa" (promax), "gamma" (oblimin), "epsilon" (the
	// geomin pair), "conf_level" (ml: the RMSEA interval's coverage,
	// default 0.9), "repair": "nearest";
	// MAT_DISTANCE: "between": a grouper spec, "metric":
	// "euclidean" | "sq_euclidean" | "manhattan" | "chebyshev" | "cosine" |
	// "hellinger" | "aitchison" | "mahalanobis", "standardize": bool,
	// "min_valid": int ≥ 1).
	Params json.RawMessage `json:"params,omitempty"`
	// Weight is the per-slot weight override: absent inherits
	// Request.Weight (then Options.DefaultWeight); null opts the slot
	// out; a WeightSpec weights it. See .claude/reference/weighting.md.
	Weight SlotWeight `json:"weight,omitzero"`
	// Encoding selects the Values layout of every matrix in the result
	// (MatrixEncodingFull when empty).
	Encoding MatrixEncoding `json:"encoding,omitempty"`
	// Multiplicity is the spec's own multiple-comparison block
	// (MAT_CORRELATION / MAT_PARTIAL_CORRELATION only; any other type
	// is refused with PULSE_MULTIPLICITY_INVALID). Setting it implies
	// params.inference (an explicit params.inference false beside it is
	// refused), and the result gains auxiliary.p_adjusted /
	// auxiliary.significant_adjusted beside the raw auxiliary.p plus a
	// MatrixResult.Multiplicity echo. Families: `matrix` (default: the
	// off-diagonal upper triangle of one result, one family per group
	// bucket) and `request` (pooled with the request's other
	// request-family members, every bucket of the spec included). Absent
	// inherits Request.Multiplicity / Options.DefaultMultiplicity field
	// by field — but only a spec already under params.inference takes an
	// inherited correction; nil is byte-identical.
	Multiplicity *Multiplicity `json:"multiplicity,omitempty"`
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
func (s MatrixSpec) Streamable() bool {
	if g, ok := s.betweenType(); ok {
		return s.Type.Streamable() && g.Streamable()
	}
	return s.Type.Streamable() && !s.bufferedParams()
}

// Mergeable reports whether this concrete spec's running state combines
// across input partitions, so a request carrying it may fan out over
// DecodeWorkers / ShardWorkers. Like Streamable it folds the type-level
// answer (MatrixType.Mergeable) with the spec's params: a buffered spec
// keeps its rows, which do not merge, so it is never mergeable and the
// request runs serially (internal/mergegate.MergeRefusal).
func (s MatrixSpec) Mergeable() bool {
	if g, ok := s.betweenType(); ok {
		return s.Type.Mergeable() && g.Streamable() && g.Mergeable()
	}
	return s.Type.Mergeable() && !s.bufferedParams()
}

// betweenType is a MAT_DISTANCE spec's params.between grouper type, ok
// false on any other type or when the params do not name one. The
// spec streams with one co-moment per bucket when the grouper keys a
// row on its own (GroupType.Streamable) and merges when its keys also
// merge (GroupType.Mergeable); otherwise it keeps its rows for the
// grouper's whole-set pass (GROUP_KMEANS, GROUP_QUANTILE) and runs
// serially. Malformed params read as the type's own answer.
func (s MatrixSpec) betweenType() (GroupType, bool) {
	if s.Type != MAT_DISTANCE || len(s.Params) == 0 {
		return "", false
	}
	var p struct {
		Between *struct {
			Type GroupType `json:"type"`
		} `json:"between"`
	}
	if err := json.Unmarshal(s.Params, &p); err != nil || p.Between == nil || p.Between.Type == "" {
		return "", false
	}
	return p.Between.Type, true
}

// bufferedParams reports whether the spec's params select a computation
// that needs the whole row set: a rank method (params.method on
// MAT_CORRELATION: absent or "pearson" folds co-moments, anything else
// ranks) or a bootstrap (a non-empty params.resample on MAT_SEM: every
// replicate redraws the rows).
func (s MatrixSpec) bufferedParams() bool {
	if s.Type == MAT_SEM && len(s.Params) > 0 {
		var p struct {
			Resample *string `json:"resample"`
		}
		return json.Unmarshal(s.Params, &p) == nil && p.Resample != nil && *p.Resample != ""
	}
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
// order. Values follows Encoding (MatrixEncodingRecords: the full
// layout). An undefined cell (no mass, zero spread) is NaN in Go and
// null on the wire.
type MatrixValues struct {
	Kind       MatrixKind     `json:"kind"`
	Encoding   MatrixEncoding `json:"encoding"`
	RowKeys    []string       `json:"row_keys"`
	ColumnKeys []string       `json:"column_keys"`
	Labels     []string       `json:"labels,omitempty"`
	Values     [][]float64    `json:"values"`
	// RecordFields are per-row descriptive fields a records encoding
	// writes beside each row's key and cells, index for index with
	// RowKeys (MAT_SEM's auxiliary.estimates: lval, op, rval,
	// parameter, free); nil elsewhere. Never on the wire otherwise.
	RecordFields []map[string]any `json:"-"`
}

// matrixRecordsWire is the wire form of a MatrixValues under
// MatrixEncodingRecords. It has no marshaller, so the planned Return
// encoder walks it field by field like the structural types.
type matrixRecordsWire struct {
	Kind     MatrixKind       `json:"kind"`
	Encoding MatrixEncoding   `json:"encoding"`
	Columns  []string         `json:"columns"`
	Rows     []map[string]any `json:"rows"`
	Labels   []string         `json:"labels,omitempty"`
}

// Records returns the matrix as row records, in RowKeys order: each an
// object holding MatrixRecordKey (the row key), one entry per column
// (NaN for an undefined cell) and the row's RecordFields. An upper
// square symmetric layout is read through its mirror, so every encoding
// yields the same records.
func (v MatrixValues) Records() []map[string]any {
	upper := v.Encoding == MatrixEncodingUpper && v.Kind == MatrixKindSquareSymmetric
	out := make([]map[string]any, len(v.RowKeys))
	for r, key := range v.RowKeys {
		rec := make(map[string]any, len(v.ColumnKeys)+1)
		if r < len(v.RecordFields) {
			for k, x := range v.RecordFields[r] {
				rec[k] = x
			}
		}
		rec[MatrixRecordKey] = key
		for c, col := range v.ColumnKeys {
			i, j := r, c
			if upper && j < i {
				i, j = j, i
			}
			if upper {
				j -= i
			}
			cell := math.NaN()
			if i < len(v.Values) && j >= 0 && j < len(v.Values[i]) {
				cell = v.Values[i][j]
			}
			rec[col] = cell
		}
		out[r] = rec
	}
	return out
}

func (v MatrixValues) recordsWire() matrixRecordsWire {
	return matrixRecordsWire{
		Kind:     v.Kind,
		Encoding: v.Encoding,
		Columns:  append([]string{}, v.ColumnKeys...),
		Rows:     v.Records(),
		Labels:   v.Labels,
	}
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
	// Multiplicity echoes the correction behind auxiliary.p_adjusted /
	// auxiliary.significant_adjusted (method, family, alpha, m and the
	// excluded null-p cells). Nil when no correction reached the result.
	Multiplicity *AppliedMultiplicity `json:"multiplicity,omitempty"`
	// Error is the data-dependent refusal that failed this slot (or this
	// group bucket of it) alone — PULSE_MATRIX_SINGULAR, _NOT_PSD, … —
	// while the rest of the response survived; see SlotError. A result
	// carrying one has Name / Type / GroupKey / GroupHeader and the
	// error only: Primary and every other figure are absent on the wire.
	Error *SlotError `json:"error,omitempty"`
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
	if v.Encoding == MatrixEncodingRecords {
		w := v.recordsWire()
		return MarshalFinite(&w)
	}
	type alias MatrixValues
	return MarshalFinite((*alias)(&v))
}

// UnmarshalJSON reads every encoding; a records form is read back into
// the full layout (RowKeys from each row's key, ColumnKeys from
// columns, a null cell NaN) with each row's other fields as its
// RecordFields.
func (v *MatrixValues) UnmarshalJSON(b []byte) error {
	var probe struct {
		Encoding MatrixEncoding `json:"encoding"`
	}
	if err := json.Unmarshal(b, &probe); err != nil {
		return err
	}
	type alias MatrixValues
	if probe.Encoding != MatrixEncodingRecords {
		return json.Unmarshal(b, (*alias)(v))
	}
	var w struct {
		Kind     MatrixKind                   `json:"kind"`
		Encoding MatrixEncoding               `json:"encoding"`
		Columns  []string                     `json:"columns"`
		Rows     []map[string]json.RawMessage `json:"rows"`
		Labels   []string                     `json:"labels"`
	}
	if err := json.Unmarshal(b, &w); err != nil {
		return err
	}
	out := MatrixValues{Kind: w.Kind, Encoding: w.Encoding, ColumnKeys: w.Columns, Labels: w.Labels,
		RowKeys: make([]string, len(w.Rows)), Values: make([][]float64, len(w.Rows))}
	if out.ColumnKeys == nil {
		out.ColumnKeys = []string{}
	}
	cols := make(map[string]bool, len(w.Columns))
	for _, c := range w.Columns {
		cols[c] = true
	}
	for r, row := range w.Rows {
		if raw, ok := row[MatrixRecordKey]; ok {
			if err := json.Unmarshal(raw, &out.RowKeys[r]); err != nil {
				return err
			}
		}
		out.Values[r] = make([]float64, len(w.Columns))
		for c, col := range w.Columns {
			var x *float64
			if raw, ok := row[col]; ok {
				if err := json.Unmarshal(raw, &x); err != nil {
					return err
				}
			}
			out.Values[r][c] = math.NaN()
			if x != nil {
				out.Values[r][c] = *x
			}
		}
		var extra map[string]any
		for k, raw := range row {
			if k == MatrixRecordKey || cols[k] {
				continue
			}
			var x any
			if err := json.Unmarshal(raw, &x); err != nil {
				return err
			}
			if extra == nil {
				extra = make(map[string]any)
			}
			extra[k] = x
		}
		if extra != nil {
			if out.RecordFields == nil {
				out.RecordFields = make([]map[string]any, len(w.Rows))
			}
			out.RecordFields[r] = extra
		}
	}
	*v = out
	return nil
}

// MarshalJSON writes the matrix result with every non-finite float as
// null; see MarshalFinite.
func (v MatrixResult) MarshalJSON() ([]byte, error) {
	if v.Error != nil {
		return MarshalFinite(slotFailureMatrix{Name: v.Name, Type: v.Type, GroupKey: v.GroupKey, GroupHeader: v.GroupHeader, Error: v.Error})
	}
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
