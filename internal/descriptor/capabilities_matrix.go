package descriptor

import (
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/internal/sem"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/linalg/metric"
	"github.com/frankbardon/pulse/types"
)

// vectorMemberFieldTypes is the accepts_types of every vector-input
// operator (each MAT_*, AGG_VEC_*, the vector attributes,
// GROUP_KMEANS): integer and float fields, plus packed_bool under the
// vector's coerce "binary" — the member rule predict and the runtime
// both run (internal/vectors.MemberTypeAllowed). `date` is NOT a member
// type (PULSE_VECTOR_MEMBER_TYPE). Sorted for golden stability; pinned
// by TestCapabilities_VectorInputAcceptsMemberRule.
var vectorMemberFieldTypes = []string{"f32", "f64", "packed_bool", "u16", "u32", "u4", "u64", "u8"}

// matrixCapabilities returns metadata for every entry in
// types.AllMatrixTypes(). Order is irrelevant; manifest assembly sorts
// by Name.
func matrixCapabilities() []descriptor.MatrixMeta {
	memberTypes := vectorMemberFieldTypes
	// missing: the missing-data knobs every operator takes
	// (internal/vectors.decodeMissing).
	missing := []descriptor.Param{
		{Name: "missing", Type: "enum", Required: false, Default: vectors.MissingListwise, EnumValues: vectors.MissingModes(), Description: "listwise drops a row with any member null; pairwise computes each pair over the rows where both are present and adds auxiliary.n (pairwise N)."},
		{Name: "max_drop_share", Type: "float", Required: false, Description: "Listwise only, no default: warn PULSE_MATRIX_LISTWISE_HEAVY_DROP when the share of rows dropped exceeds it (0 to 1)."},
		// fit: the bound vector fit the slot emits from
		// (internal/vectors resolveFit) — every operator but
		// MAT_DISTANCE, which declares its own list.
		MatrixFitParam(),
	}
	return []descriptor.MatrixMeta{
		{
			Name:         string(types.MAT_CORRELATION),
			Description:  "Correlation matrix of a vector's members (listwise or pairwise): Pearson r by default, or Spearman rho / Kendall tau-b under params.method; a zero-spread member's row and column are null. Pearson is weighted under frequency and probability weights, a rank method under frequency weights only. params.inference adds each pair's two-sided p-value and Fisher-z confidence interval as auxiliary.p / ci_lower / ci_upper: TEST_PEARSON_R's arithmetic on the pair's inference size (pearson), R cor.test's p with a Bonett-Wright / Fieller interval (spearman / kendall).",
			AcceptsTypes: memberTypes,
			Params: append(append([]descriptor.Param{
				{Name: "method", Type: "enum", Required: false, Default: vectors.CorrelationPearson, EnumValues: vectors.CorrelationMethods(), Description: "pearson (r over the co-moments; streamable, mergeable); spearman (rho, r on mid-ranks) or kendall (tau-b): each cell equals TEST_SPEARMAN_R / TEST_KENDALL_TAU over the pair's rows (pairwise re-ranks per pair); buffered and not mergeable, so the request runs serially; frequency weights only (a probability weight is PULSE_WEIGHT_UNSUPPORTED)."},
			}, missing...),
				descriptor.Param{Name: "summary", Type: "object", Required: false, Description: "{\"top_pairs\": k}, k a positive integer: list the k off-diagonal pairs with the largest |r| as vectors.top_pairs [{row, col, r, n}], ties in axis order; undefined pairs are skipped and k past the pair count lists every pair."},
				inferenceParam("a pair's t = r*sqrt((N*-2)/(1-r^2)) on df = N*-2 (TEST_PEARSON_R's arithmetic) and its Fisher-z interval with se = 1/sqrt(N*-3); N* is the pair's own size under pairwise (rows unweighted, sum of weights under frequency, Kish n_eff under probability). Rank methods: R cor.test's default p over the pair's rows (n = N*): without ties exact for kendall below n = 50 and AS 89 for spearman up to n = 1290, otherwise the tie-corrected normal (kendall) or t on n-2 (spearman) approximation; the interval is Fisher z with se = sqrt((1+rho^2/2)/(n-3)) (spearman, Bonett-Wright) or sqrt(0.437/(n-4)) (kendall, Fieller; needs 5 rows)."),
				matrixAlphaParam,
			),
			OutputKeys: descriptor.MatrixOutputKeys{
				Primary:   "correlation",
				Auxiliary: []string{"ci_lower", "ci_upper", "n", "p"},
				Vectors:   []string{"top_pairs"},
				Scalars:   []string{"determinant"},
			},
			Streamable:      types.MAT_CORRELATION.Streamable(),
			Mergeable:       types.MAT_CORRELATION.Mergeable(),
			ComponentSchema: matrixSchema(),
		},
		{
			Name:         string(types.MAT_PARTIAL_CORRELATION),
			Description:  "Partial correlation matrix: each pair's Pearson correlation with other fields held fixed, from the precision matrix of the folded columns' correlation (listwise or pairwise). params.control \"all\" (default) controls each pair for every other member; a field list controls each pair for exactly those fields and drops any member it names from the output. A decomposition operator: a non-PSD (pairwise) input is refused with PULSE_MATRIX_NOT_PSD unless params.repair is \"nearest\", a singular one is PULSE_MATRIX_SINGULAR. Weighted under frequency and probability weights. params.inference adds each pair's two-sided p-value and Fisher-z confidence interval (df = N* - 2 - g, g the controls) as auxiliary.p / ci_lower / ci_upper.",
			AcceptsTypes: memberTypes,
			Params: append([]descriptor.Param{
				{Name: "control", Type: "list", Required: false, Default: vectors.ControlAll, Description: "\"all\" (each pair controls for every other member: -P_ij/sqrt(P_ii*P_jj) of the precision matrix P) or a list of numeric field names: each output pair controls for exactly those fields. A listed member leaves the output axis; a listed field outside the members joins the co-moment and the missing-data mode."},
				{Name: "repair", Type: "enum", Required: false, EnumValues: []string{vectors.RepairNearest}, Description: "nearest: replace a non-PSD input correlation by its nearest correlation matrix (Higham alternating projections with Dykstra's correction, Matrix::nearPD corr = TRUE; at most 100 iterations, convergence 1e-7) and warn PULSE_MATRIX_NOT_PSD with frobenius_adjustment. Absent: a non-PSD input is refused (fatal PULSE_MATRIX_NOT_PSD)."},
				inferenceParam("a pair's t = r*sqrt((N*-2-g)/(1-r^2)) on df = N*-2-g, g the controls (every other folded column under control all, else the control count), as ppcor::pcor, and its Fisher-z interval with se = 1/sqrt(N*-3-g); N* is the whole fold's under listwise, the smallest folded pair's under pairwise (PULSE_MATRIX_PAIRWISE_N_STAR names it)."),
				matrixAlphaParam,
			}, missing...),
			OutputKeys: descriptor.MatrixOutputKeys{
				Primary:   "partial_correlation",
				Auxiliary: []string{"ci_lower", "ci_upper", "n", "p"},
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
			Description:  "Principal component analysis of a vector's members (listwise or pairwise): the eigen-decomposition of their correlation (params.on \"correlation\", the default) or covariance matrix. The primary is the p x k loadings (eigenvector x sqrt(eigenvalue), a rectangular matrix with columns PC1..PCk), auxiliary.eigenvectors the p x k unit eigenvectors; vectors carry every eigenvalue, the explained and cumulative variance shares, the communalities over the kept components and each member's KMO measure of sampling adequacy; scalars the overall KMO, Bartlett's sphericity test (chi-square, df, p) and the number of components kept. Eigenvalues descending, each eigenvector's largest-magnitude entry positive. A decomposition operator: a non-PSD (pairwise) input is refused with PULSE_MATRIX_NOT_PSD unless params.repair is \"nearest\". params.rotation (an orthogonal criterion: varimax, quartimax, geomin_orthogonal or entropy; an oblique one is refused) replaces the primary with the rotated loadings, moves the unrotated loadings to auxiliary.unrotated and adds vectors.rotated_variance; eigenvalues, shares, communalities and eigenvectors stay unrotated. Weighted under frequency and probability weights.",
			AcceptsTypes: memberTypes,
			Params: append([]descriptor.Param{
				{Name: "on", Type: "enum", Required: false, Default: vectors.PCAOnCorrelation, EnumValues: vectors.PCAOnValues(), Description: "correlation: analyse the correlation matrix (every member on one scale); covariance: the covariance matrix (members in their own units, so a large-spread member dominates; params.components is then required)."},
				{Name: "components", Type: "any", Required: false, Default: vectors.PCAComponentsKaiser, Description: "How many components to keep: a positive integer k (at most the member count); \"kaiser\" (every component whose eigenvalue exceeds 1; the default on a correlation, refused on a covariance); or {\"variance\": share}, the fewest leading components whose cumulative explained share reaches share (0 < share <= 1). Required on a covariance. Every eigenvalue is reported either way."},
				{Name: "rotation", Type: "enum", Required: false, Default: vectors.RotationNone, EnumValues: vectors.PCARotations(), Description: "none: the unrotated loadings. Otherwise rotate the kept components (k >= 2) orthogonally: varimax (stats::varimax, Kaiser-normalized), quartimax, geomin_orthogonal (params.epsilon) or entropy (minimum entropy) — GPArotation's quartimax / geominT / entropy, normalize = FALSE. The rotated loadings become the primary, ordered by the variance each accounts for (vectors.rotated_variance), each column signed to sum positive; the unrotated move to auxiliary.unrotated. An oblique criterion (promax, oblimin, geomin) is refused (SERVICE_VALIDATION reason rotation_oblique_unsupported): components are uncorrelated; use MAT_FACTOR."},
				{Name: "epsilon", Type: "float", Required: false, Default: vectors.GeominDefaultEpsilon, Description: "Geomin epsilon, a finite number above 0 (GPArotation delta; default 0.01): larger smooths the criterion. Accepted with rotation geomin_orthogonal only."},
				{Name: "repair", Type: "enum", Required: false, EnumValues: []string{vectors.RepairNearest}, Description: "nearest: decompose the nearest correlation matrix (scaled back to the covariance's variances on a covariance) when the (pairwise) input is not PSD, with a PULSE_MATRIX_NOT_PSD warning carrying frobenius_adjustment. Absent: a non-PSD input is refused (fatal PULSE_MATRIX_NOT_PSD)."},
			}, missing...),
			OutputKeys: descriptor.MatrixOutputKeys{
				Primary:   "loadings",
				Auxiliary: []string{"eigenvectors", "n", "unrotated"},
				Vectors:   []string{"communalities", "cumulative", "eigenvalues", "explained_variance", "kmo_msa", "rotated_variance"},
				Scalars:   []string{"bartlett_chisq", "bartlett_df", "bartlett_p", "components_retained", "kmo"},
			},
			Streamable:      types.MAT_PCA.Streamable(),
			Mergeable:       types.MAT_PCA.Mergeable(),
			ComponentSchema: matrixSchema(),
		},
		{
			Name:         string(types.MAT_COLLINEARITY),
			Description:  "Collinearity check of a vector's members as regression predictors (listwise or pairwise; no response): each member's variance inflation factor VIF = diag(R^-1) and tolerance 1/VIF (car::vif), and Belsley's condition indices and variance-decomposition proportions (perturb::colldiag) on the scaled, uncentered predictors with an intercept (params.center true: the centered predictors). The primary is the predictors' correlation matrix R; auxiliary.variance_decomposition is rectangular (a row per variable, \"(intercept)\" first when uncentered; a column per dimension D1..Dq in condition-index order); scalars carry the largest condition index (condition_number) and the largest VIF (max_vif). A decomposition operator: a non-PSD (pairwise) input is refused with PULSE_MATRIX_NOT_PSD unless params.repair is \"nearest\"; a singular R is PULSE_MATRIX_SINGULAR (dependent_fields). Weighted under frequency and probability weights. No p-values, no banding.",
			AcceptsTypes: memberTypes,
			Params: append([]descriptor.Param{
				{Name: "center", Type: "bool", Required: false, Default: false, Description: "false: Belsley's diagnostics on the scaled, uncentered predictors with an intercept column (Belsley's recommendation; shows collinearity with the intercept), rebuilt from the co-moment as W(Sigma + mu mu^T); true: on the scaled, centered predictors (their correlation), no intercept. VIF and tolerance do not depend on it."},
				{Name: "repair", Type: "enum", Required: false, EnumValues: []string{vectors.RepairNearest}, Description: "nearest: read every figure off the nearest correlation matrix when the (pairwise) correlation is not PSD, with a PULSE_MATRIX_NOT_PSD warning carrying frobenius_adjustment. Absent: a non-PSD input is refused (fatal PULSE_MATRIX_NOT_PSD)."},
			}, missing...),
			OutputKeys: descriptor.MatrixOutputKeys{
				Primary:   "correlation",
				Auxiliary: []string{"n", "variance_decomposition"},
				Vectors:   []string{"condition_indices", "tolerance", "vif"},
				Scalars:   []string{"condition_number", "max_vif"},
			},
			Streamable:      types.MAT_COLLINEARITY.Streamable(),
			Mergeable:       types.MAT_COLLINEARITY.Mergeable(),
			ComponentSchema: matrixSchema(),
		},
		{
			Name:         string(types.MAT_FACTOR),
			Description:  "Exploratory factor analysis of a vector's members (listwise or pairwise) on their correlation matrix: k common factors (params.factors, an integer or the Kaiser rule) extracted by minimum residual (minres, the default; psych fa fm = \"minres\"), iterated principal axes (principal_axis; fm = \"pa\") or maximum likelihood (ml; stats::factanal, fm = \"ml\"), each from the squared multiple correlations, then rotated (params.rotation: none, or an orthogonal — varimax, quartimax, geomin_orthogonal, entropy — or oblique — promax, oblimin, geomin — criterion). The primary is the p x k loadings (a rectangular matrix with columns F1..Fk; rotated when a rotation is set, each column ordered by the variance it accounts for and signed to sum positive); auxiliary.unrotated the unrotated loadings (rotated runs) and auxiliary.factor_correlation the k x k factor correlation (oblique rotations only; square, keyed F1..Fk); vectors carry each member's communality and uniqueness and each factor's variance accounted; scalars the root-mean-square residual (rmsr), the empirical residual chi-square with its df and p (a residual size, not a likelihood test), KMO, Bartlett's sphericity test, the fit's iterations and convergence, and the number of factors; ml adds the Bartlett-corrected likelihood chi-square (likelihood_chisq, on df) and its p (likelihood_p), the Tucker-Lewis index (tli), the RMSEA point estimate (rmsea) with its interval (rmsea_lower / rmsea_upper at params.conf_level, default 90%, psych::fa's RMSEA lower / upper) and BIC. A count the members cannot identify ((p - k)^2 < p + k) nulls the fit with PULSE_MATRIX_NOT_IDENTIFIED; a uniqueness at or below 0 (ml: held at its 0.005 floor) is reported as fitted with PULSE_MATRIX_HEYWOOD; an extraction or rotation at its iteration cap warns PULSE_MATRIX_NOT_CONVERGED. A decomposition operator: a non-PSD (pairwise) input is refused with PULSE_MATRIX_NOT_PSD unless params.repair is \"nearest\". Weighted under frequency and probability weights.",
			AcceptsTypes: memberTypes,
			Params: append([]descriptor.Param{
				{Name: "factors", Type: "any", Required: false, Default: vectors.FactorsKaiser, Description: "How many factors to extract: a positive integer k (at most the member count; a k the members cannot identify, (p - k)^2 < p + k, returns a null fit with PULSE_MATRIX_NOT_IDENTIFIED) or \"kaiser\" (as many as the correlation has eigenvalues above 1)."},
				{Name: "extraction", Type: "enum", Required: false, Default: vectors.FactorExtractionMinres, EnumValues: vectors.FactorExtractions(), Description: "minres: minimise the off-diagonal residuals over the communalities (psych fm = \"minres\"; Pulse solves to the exact optimum, where psych's optimiser stops about 1e-5 short). principal_axis: iterate the communalities on the diagonal until their sum moves by at most 0.001, at most 50 iterations (psych fm = \"pa\"). ml: maximum likelihood over the uniquenesses, each held in [0.005, 1] (stats::factanal, psych fm = \"ml\"; Pulse solves to the exact optimum, where their optimiser stops about 1e-5 short); adds likelihood_chisq, likelihood_p, tli, rmsea and bic, and needs a positive-definite correlation (a singular one nulls the fit with PULSE_MATRIX_SINGULAR). Each starts from the squared multiple correlations; iterations and converged report the run."},
				{Name: "rotation", Type: "enum", Required: false, Default: vectors.RotationNone, EnumValues: vectors.FactorRotations(), Description: "none: the unrotated loadings. Orthogonal: varimax (Kaiser-normalized, stats::varimax), quartimax, geomin_orthogonal (params.epsilon), entropy (minimum entropy). Oblique: promax (varimax then a power target, stats::promax, params.kappa), oblimin (params.gamma), geomin (params.epsilon). Every criterion but varimax / promax is gradient projection with GPArotation's wrapper of that name (geominT / geominQ for the geomin pair), normalize = FALSE. Rotated loadings become the primary; the unrotated move to auxiliary.unrotated; an oblique rotation adds auxiliary.factor_correlation. Communalities do not change."},
				{Name: "kappa", Type: "float", Required: false, Default: vectors.FactorDefaultKappa, Description: "Promax power, a finite number above 1 (stats::promax m). Accepted with rotation promax only."},
				{Name: "gamma", Type: "float", Required: false, Default: 0, Description: "Oblimin gamma, finite: 0 quartimin (the default), 0.5 biquartimin, 1 covarimin. Accepted with rotation oblimin only."},
				{Name: "epsilon", Type: "float", Required: false, Default: vectors.GeominDefaultEpsilon, Description: "Geomin epsilon, a finite number above 0 (GPArotation delta; default 0.01): larger smooths the criterion. Accepted with rotation geomin or geomin_orthogonal only."},
				{Name: "conf_level", Type: "float", Required: false, Default: vectors.FactorDefaultConfLevel, Description: "Coverage of the ml RMSEA interval scalars rmsea_lower / rmsea_upper, 0 < conf_level < 1 (default 0.9, psych::fa's 90%: alpha = 0.1). Accepted with extraction ml only."},
				{Name: "repair", Type: "enum", Required: false, EnumValues: []string{vectors.RepairNearest}, Description: "nearest: factor the nearest correlation matrix when the (pairwise) correlation is not PSD, with a PULSE_MATRIX_NOT_PSD warning carrying frobenius_adjustment. Absent: a non-PSD input is refused (fatal PULSE_MATRIX_NOT_PSD)."},
			}, missing...),
			OutputKeys: descriptor.MatrixOutputKeys{
				Primary:   "loadings",
				Auxiliary: []string{"factor_correlation", "n", "unrotated"},
				Vectors:   []string{"communalities", "uniquenesses", "variance_accounted"},
				Scalars:   []string{"bartlett_chisq", "bartlett_df", "bartlett_p", "bic", "converged", "df", "empirical_chisq", "empirical_p", "factors_retained", "iterations", "kmo", "likelihood_chisq", "likelihood_p", "rmsea", "rmsea_lower", "rmsea_upper", "rmsr", "tli"},
			},
			Streamable:      types.MAT_FACTOR.Streamable(),
			Mergeable:       types.MAT_FACTOR.Mergeable(),
			ComponentSchema: matrixSchema(),
		},
		{
			Name:         string(types.MAT_DISTANCE),
			Description:  "Distances between the centroids of the buckets a grouper cuts the rows into (params.between: any built-in grouper but GROUP_DATE / GROUP_DATE_RANGES, GROUP_KMEANS included). A centroid is the bucket's (weighted) mean of every member. The primary is the k x k distance matrix (square, keyed by the bucket labels in the grouper's emission order, zero diagonal) under params.metric; auxiliary.centroids the k x p centroids (rows the buckets, columns the members), auxiliary.centroids_standardized under standardize and auxiliary.pooled_covariance under mahalanobis; vectors.sizes each bucket's weight mass and vectors.counts its rows. Rows the grouper puts in no bucket (a null key, an include rejection) are excluded and counted (components operator.n_null_key); a row with a null member is dropped (listwise only). Streamable and mergeable when the between grouper is (one co-moment per bucket); GROUP_KMEANS and GROUP_QUANTILE buffer the rows and run serially. Weighted under frequency and probability weights (a GROUP_KMEANS between: frequency only). No p-values.",
			AcceptsTypes: memberTypes,
			Params: []descriptor.Param{
				{Name: "between", Type: "object", Required: true, Description: "The grouper whose buckets are compared: a grouper spec {type, field | vector | fields, params, include, interval} as in groups (GROUP_KMEANS reads vector / fields). Any built-in grouper or a registered extension grouper; it takes the matrix's weight and no weight of its own, and a tz only on a date-family grouper (GROUP_DATE, GROUP_DATE_RANGES), which reads its zone like a groups slot."},
				{Name: "metric", Type: "enum", Required: false, Default: vectors.DistanceDefaultMetric, EnumValues: metric.Accepted(metric.ConsumerMatDistance), Description: "A linalg/metric registry name (capabilities.matrix.metrics, consumer MAT_DISTANCE), in the distance form. Absent over a kind-declared vector, the smart defaults fill the kind's default this operator accepts (capabilities.matrix.vector_kinds: measure euclidean, scale euclidean, composition aitchison — manhattan under standardize — binary cosine; reported in defaults_applied); absent otherwise, euclidean. An explicit metric that fits the vector's kind poorly warns PULSE_VECTOR_METRIC_UNSUITED (an error under strict). The metrics: euclidean (stats::dist), sq_euclidean (its square), manhattan (sum of absolute differences), chebyshev (the largest absolute difference), cosine (1 - cos of the angle between the centroids, clamped to [0, 2]; null for a zero-length one), hellinger and aitchison (compositions: non-negative centroids, refused beside standardize; a negative coordinate or an aitchison zero part nulls the distance with PULSE_MATRIX_METRIC_DOMAIN) or mahalanobis (over the pooled within-bucket covariance, on the raw centroids: scale-free, standardize changes nothing; null on any undefined coordinate). An undefined coordinate (a zero-spread member under standardize) is dropped pairwise and the element sums rescaled by p/p_observed (R stats::dist())."},
				{Name: "min_valid", Type: "int", Required: false, Default: 1, Description: "The fewest complete member pairs (coordinates defined on both centroids) a distance needs, 1 to the member count; below it the distance is null."},
				{Name: "standardize", Type: "bool", Required: false, Default: false, Description: "z-score every member over the bucketed rows (their mean and N* - 1 standard deviation, R scale()) before comparing, so no member dominates by its units."},
				{Name: "missing", Type: "enum", Required: false, Default: vectors.MissingListwise, EnumValues: []string{vectors.MissingListwise}, Description: "listwise only: a row with any member null joins no centroid (pairwise is refused)."},
				missing[1],
			},
			OutputKeys: descriptor.MatrixOutputKeys{
				Primary:   "distance",
				Auxiliary: []string{"centroids", "centroids_standardized", "pooled_covariance"},
				Vectors:   []string{"counts", "sizes"},
			},
			Streamable: types.MAT_DISTANCE.Streamable(),
			Mergeable:  types.MAT_DISTANCE.Mergeable(),
			ComponentSchema: matrixSchema(
				descriptor.ComponentKey{Name: "metric", Type: "string", Description: "The metric the distances use (params.metric)."},
				descriptor.ComponentKey{Name: "standardize", Type: "bool", Description: "Whether the members were z-scored first (params.standardize)."},
				descriptor.ComponentKey{Name: "buckets", Type: "int", Description: "Buckets compared (k)."},
				descriptor.ComponentKey{Name: "n_null_key", Type: "int", Description: "Filter-passing rows the between grouper placed in no bucket (a null key, an include rejection, a GROUP_KMEANS row with a null member); excluded from every figure."},
			),
		},
		{
			Name:         string(types.MAT_SEM),
			Description:  "Structural equation model of a vector's members (listwise only): a lavaan / semopy description in params.model (=~ measurement, ~ regression, ~~ covariance; label* equality and number* fixed multipliers) fitted by maximum likelihood to the members' covariance, whose observed variables are exactly the members (by label). The default setup reproduces semopy (semopy.Model(desc).fit(data), semopy 2.3.11): the Wishart ML objective over the biased (divisor N) covariance minimised by a port of SciPy's SLSQP at ftol 1e-6 — semopy's early stop included — with semopy's specification rules, starting values, expected-information standard errors and calc_stats fit indices. params.solver fisher converges to the optimum instead; params.information rank2 computes the same derivatives more cheaply. The primary is the model-implied covariance Sigma(theta) over the members; auxiliary.estimates is the parameter table (rectangular: a row per semopy inspect row keyed \"lval op rval\" — op ~ for loadings and regressions, ~~ for (co)variances — columns estimate, std_estimate, std_err, z_value, p_value; SE, z and p null on a fixed parameter) and auxiliary.residual_covariance S - Sigma(theta); vectors carry each row's parameter name and whether it is free; scalars the fit indices and the fit's iterations / convergence. A rank-deficient covariance is PULSE_MATRIX_SINGULAR, or under params.singular nearest fitted to semopy's nearest positive-definite substitute with PULSE_MATRIX_NEAREST_PD; a solver stop short of success warns PULSE_MATRIX_NOT_CONVERGED; negative degrees of freedom warn PULSE_MATRIX_NOT_IDENTIFIED. Weighted under frequency weights (exactly the fit on the expanded rows, N = the sum of weights); a probability weight is PULSE_WEIGHT_UNSUPPORTED.",
			AcceptsTypes: memberTypes,
			Params: []descriptor.Param{
				{Name: "model", Type: "string", Required: false, Description: "The model description in semopy / lavaan syntax, one statement per line (# comments): lhs =~ indicators (measurement; the first indicator's loading is fixed to 1), lhs ~ predictors (regression; comma-separated left sides), lhs ~~ rhs (covariance); a term may carry a label (a*x: equal across every use) or a fixed value (0.5*x). Its observed variables must be exactly the members, matched by label (a variable that is not a member, or a member it never names, is SERVICE_VALIDATION bad_params). semopy's operation commands (START, BOUND, DEFINE, ...) are not supported. Absent: one factor F1 over every member."},
				{Name: "solver", Type: "enum", Required: false, Default: string(sem.SolverSLSQP), EnumValues: vectors.SEMSolvers(), Description: "slsqp: SciPy's SLSQP as semopy calls it (a port: the same iterates, the same stop at ftol), so the results are semopy's to rounding — including its early stop short of the optimum at the default ftol. fisher: damped Fisher scoring (Levenberg-Marquardt on the expected information) to the optimum, stopping when every free gradient entry is below grad_tol; a semopy fit at a small ftol lands on the same figures."},
				{Name: "information", Type: "enum", Required: false, Default: string(sem.InformationDense), EnumValues: vectors.SEMInformation(), Description: "How the gradient and the expected information tr(Sigma^-1 dSigma_k Sigma^-1 dSigma_l) are computed: dense forms every dSigma_k as a matrix, as semopy does; rank2 writes each as a sum of a b^T + b a^T terms so every trace is a dot product — the same figures to rounding, much cheaper on large models."},
				{Name: "ftol", Type: "float", Required: false, Default: 1e-6, Description: "SLSQP's ftol (SciPy's and semopy's default 1e-6), a number above 0: the run stops when the objective moves by less. Solver slsqp only. The baseline model behind CFI, TLI, NFI and GFI is always fitted at the default, as semopy fits it."},
				{Name: "max_iter", Type: "int", Required: false, Description: "Iteration cap, a positive integer: default 10000 for slsqp (semopy's setting), 500 for fisher. A run at the cap keeps its last iterate with PULSE_MATRIX_NOT_CONVERGED."},
				{Name: "grad_tol", Type: "float", Required: false, Default: 1e-10, Description: "Fisher scoring's stop, a number above 0: every free gradient entry below it in absolute value. Solver fisher only."},
				{Name: "singular", Type: "enum", Required: false, Default: vectors.SEMSingularRefuse, EnumValues: vectors.SEMSingular(), Description: "A sample covariance that is not positive definite: refuse is PULSE_MATRIX_SINGULAR; nearest fits semopy's substitute, statsmodels' cov_nearest (threshold 0.01), with PULSE_MATRIX_NEAREST_PD (n, p, min_eigenvalue) — a zero-spread member of a standardized vector enters at unit variance, uncorrelated."},
				{Name: "missing", Type: "enum", Required: false, Default: vectors.MissingListwise, EnumValues: []string{vectors.MissingListwise}, Description: "listwise only: a row with any member null joins no figure (pairwise is refused: the likelihood is one sample's covariance)."},
				missing[1],
				MatrixFitParam(),
			},
			OutputKeys: descriptor.MatrixOutputKeys{
				Primary:   "implied_covariance",
				Auxiliary: []string{"estimates", "residual_covariance"},
				Vectors:   []string{"free", "parameter"},
				Scalars:   []string{"agfi", "aic", "bic", "cfi", "chi2", "chi2_baseline", "chi2_p_value", "converged", "dof", "dof_baseline", "gfi", "iterations", "loglik", "n", "n_params", "nfi", "objective", "rmsea", "tli"},
			},
			Streamable:      types.MAT_SEM.Streamable(),
			Mergeable:       types.MAT_SEM.Mergeable(),
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

// metricCapabilities is the shared metric registry (linalg/metric) as
// manifest entries, in canonical order. offered reports whether the
// instance offers a consumer; a consumer it does not offer (hidden, or
// an operator not registered in this build) is left out of every
// entry's consumers.
func metricCapabilities(offered func(metric.Consumer) bool) []descriptor.MetricMeta {
	all := metric.All()
	out := make([]descriptor.MetricMeta, 0, len(all))
	for _, m := range all {
		e := descriptor.MetricMeta{
			Name:          m.Name(),
			Family:        string(m.Family()),
			Description:   m.Description(),
			Direction:     string(m.Direction()),
			Forms:         []string{},
			KindsSuited:   []string{},
			PoorFit:       []string{},
			Fitted:        m.Fitted(),
			FitStat:       string(m.Fit()),
			Domain:        string(m.Domain()),
			ZeroUndefined: m.ZeroUndefined(),
			Scaled:        m.Scaled(),
			Complete:      m.Complete(),
			Consumers:     []string{},
		}
		for _, f := range m.Forms() {
			e.Forms = append(e.Forms, string(f))
		}
		for _, k := range m.KindsSuited() {
			e.KindsSuited = append(e.KindsSuited, string(k))
		}
		for _, k := range m.PoorFit() {
			e.PoorFit = append(e.PoorFit, string(k))
		}
		for _, c := range m.Consumers() {
			if offered(c) {
				e.Consumers = append(e.Consumers, string(c))
			}
		}
		out = append(out, e)
	}
	return out
}

// vectorKindCapabilities is the kind → default metric table
// (metric.Default), in canonical kind order.
func vectorKindCapabilities() []descriptor.VectorKindMeta {
	out := make([]descriptor.VectorKindMeta, 0, len(metric.Kinds()))
	for _, k := range metric.Kinds() {
		e := descriptor.VectorKindMeta{Name: string(k)}
		if m, ok := metric.Default(k, metric.Distance); ok {
			e.DistanceDefault = m.Name()
		}
		if m, ok := metric.Default(k, metric.Similarity); ok {
			e.SimilarityDefault = m.Name()
		}
		out = append(out, e)
	}
	return out
}

// metricConsumerOffered reports, from the instance's registered and
// enabled operator names, whether it offers metric consumer c: "expr"
// through either expression operator, an operator consumer by name.
func metricConsumerOffered(names map[string]bool) func(metric.Consumer) bool {
	return func(c metric.Consumer) bool {
		if c == metric.ConsumerExpr {
			return names["ATTR_FORMULA"] || names["FILTER_EXPRESSION"]
		}
		return names[string(c)]
	}
}

// matrixCapability is the Manifest.Matrix block; offered decides each
// metric's listed consumers (metricCapabilities).
func matrixCapability(offered func(metric.Consumer) bool) descriptor.MatrixCapability {
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
		Metrics:         metricCapabilities(offered),
		VectorKinds:     vectorKindCapabilities(),
		Limitations: []string{
			"Matrices follow Request.Groups: a grouped request returns one result per spec per non-empty bucket (group_key, group_header), spec-major then bucket in the final Data row order (Request.Sort included); a thin bucket is still emitted, with PULSE_MATRIX_INSUFFICIENT_N.",
			"A request carrying matrices fans out over DecodeWorkers and ShardWorkers with bit-identical results, per bucket on a grouped request.",
			"A matrix result is emitted at finalize: a streamed run carries it at terminal flush only.",
			"A rectangular matrix (kind \"rectangular\", MAT_PCA's p x k loadings and eigenvectors, MAT_FACTOR's p x k loadings, MAT_COLLINEARITY's variance decomposition) has the members as rows (MAT_COLLINEARITY's uncentered decomposition leads with an \"(intercept)\" row) and its own column keys, and is always written full whatever the spec's encoding; MAT_DISTANCE's centroids are the one k x p matrix with the bucket labels as rows and the members as columns.",
			"MAT_DISTANCE's primary is square but keyed by the between grouper's bucket labels, not the members.",
			"A request carrying matrices with joins, or a ProcessChain stage after 0 carrying matrices, is refused with PULSE_MATRIX_UNSUPPORTED_SOURCE; matrices with a crosstab are refused with PULSE_MATRIX_HOST_CONFLICT.",
			"Vector members may name a feature, attribute or record-row window label of the request; only MAT_* reads such a vector (else computed_member_unsupported).",
		},
	}
}

// inferenceParam is MAT_CORRELATION / MAT_PARTIAL_CORRELATION's
// params.inference descriptor; how names the operator's arithmetic.
func inferenceParam(how string) descriptor.Param {
	return descriptor.Param{Name: "inference", Type: "bool", Required: false, Default: false,
		Description: "true: add auxiliary.p (two-sided p-value), auxiliary.ci_lower and auxiliary.ci_upper (the 1 - alpha confidence interval), square matrices over the output axis in the primary's encoding, diagonal null: " + how +
			" A cell too thin for its p-value or interval is null with PULSE_MATRIX_INSUFFICIENT_N (scope inference); under probability weights an effective size below its floor adds PULSE_WEIGHT_LOW_NEFF. The auxiliaries ride matrices[*].auxiliary, so a return that drops it (preset standard, the MCP default) skips them. The spec's own multiplicity block implies inference and adds auxiliary.p_adjusted and auxiliary.significant_adjusted (1 below the block's alpha, else 0) over the distinct pairs (family matrix: the upper triangle of one result, m = p(p - 1) / 2; family request: pooled with the request), mirrored, diagonal null, plus a multiplicity echo; the raw p never moves."}
}

// matrixAlphaParam is params.alpha beside params.inference.
var matrixAlphaParam = descriptor.Param{Name: "alpha", Type: "float", Required: false, Default: vectors.InferenceDefaultAlpha,
	Description: "The confidence intervals' two-sided level, strictly between 0 and 1 (coverage 1 - alpha). Applies with inference on only. Under a multiplicity block inference is on even when unset, and significant_adjusted reads the block's own alpha, not this one."}
