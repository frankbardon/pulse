package vectors

import (
	"encoding/json"
	stderrors "errors"
	"slices"
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/paramread"
	"github.com/frankbardon/pulse/internal/sem"
	"github.com/frankbardon/pulse/types"
)

// SEMPlan is MAT_SEM's resolved params: the model description, the
// parsed model and the fit options. Matrix.SEM is nil on every other
// type.
type SEMPlan struct {
	// Desc is params.model, or — absent — the one-factor default over
	// every member (SEMDefaultModel).
	Desc string
	// Model is Desc parsed and specified (sem.NewModel).
	Model *sem.Model
	// Order maps each of Model.Observed (semopy's variable order) to its
	// member position in Columns.
	Order []int
	// Options are the solver settings (the zero value is semopy's
	// default setup).
	Options sem.Options
	// Singular is params.singular: SEMSingularNearest (default, semopy's
	// default fit) or SEMSingularRefuse.
	Singular string
	// MeanStructure is params.meanstructure: model the means too
	// (sem.ModelOptions.MeanStructure). A `~ 1` line in the model sets
	// Model.MeanStructure whatever this says.
	MeanStructure bool
	// Group is params.group: the field whose levels split the rows into
	// the groups of one joint multi-group fit (sem.FitGroups); "" is an
	// ungrouped fit. A grouped fit models the means (lavaan's default)
	// and follows lavaan's conventions.
	Group string
	// GroupReference is params.group_reference: the level whose implied
	// covariance is the primary ("" — the first level).
	GroupReference string
	// GroupType is the group field's type; GroupLevels its levels in
	// order when the schema knows them — a categorical dictionary in
	// dictionary order, packed_bool's "0" / "1" — nil on an integer
	// field, whose levels are its observed values, ascending. A level
	// without rows after the filters is no group of the fit.
	GroupType   encoding.FieldType
	GroupLevels []string
	// GroupEqual and GroupPartial are params.group_equal /
	// group_partial: lavaan's group.equal classes held equal across
	// the groups and the group.partial rows freed from them
	// (sem.ModelOptions).
	GroupEqual, GroupPartial []string
	// Resample is params.resample: SEMResampleBootstrap (lavaan's se =
	// "bootstrap": the bootstrap's SE, z, p and percentile interval
	// replace the analytic ones; the slot buffers its rows) or "" (none).
	// BootstrapIters and RNGSeed are its replicate count (default
	// SEMBootstrapItersDefault) and seed (0 — absent or explicit —
	// derives one from the request and the slot's rows, REG_RESAMPLE's
	// rule: regression.DeriveSeed).
	Resample       string
	BootstrapIters int
	RNGSeed        int64
	// CILevel is params.ci_level (default SEMCILevelDefault): the level
	// of the percentile interval and — lavaan's level — of every
	// lavaan-convention fit's Wald interval (Options.Level); CILevelSet
	// reports it was given (refused on a semopy-convention fit, which
	// reports no interval).
	CILevel    float64
	CILevelSet bool
	// Estimator is params.estimator: sem.EstimatorML (default) or one of
	// lavaan's ordinal estimators, sem.EstimatorDWLS / EstimatorWLSMV
	// (sem.FitOrdinal: two-step polychoric / polyserial statistics, the
	// slot buffers its rows). Ordered is params.ordered — the ordered
	// members by label, required (non-empty) by an ordinal estimator and
	// refused without one.
	Estimator sem.Estimator
	Ordered   []string
	// OrderedLevels is each Ordered member's category-count bound as the
	// schema knows it — packed_bool's 2, u4's 16 — 0 when unknown (its categories are its observed
	// values): predict's estimate of the threshold rows.
	OrderedLevels []int
}

// Ordinal reports whether the plan fits by an ordinal estimator (DWLS /
// WLSMV).
func (p *SEMPlan) Ordinal() bool { return p != nil && p.Estimator.Ordinal() }

// SEMEstimators are the accepted params.estimator values, default first.
func SEMEstimators() []string {
	return []string{string(sem.EstimatorML), string(sem.EstimatorDWLS), string(sem.EstimatorWLSMV)}
}

// SpecSEMEstimator is a MAT_SEM spec's params.estimator as written ("" on
// any other type, when absent, or when the params do not decode).
func SpecSEMEstimator(spec types.MatrixSpec) string {
	if spec.Type != types.MAT_SEM || len(spec.Params) == 0 {
		return ""
	}
	var p struct {
		Estimator *string `json:"estimator"`
	}
	if paramread.Probe(spec.Params, &p) != nil || p.Estimator == nil {
		return ""
	}
	return *p.Estimator
}

// SEMOrdinalScalarKeys are the scalars a WLSMV fit adds: the
// scaled-shifted test (its statistic, p, scaling factor and shift), the
// scaled baseline and the scaled indices.
func SEMOrdinalScalarKeys() []string {
	return []string{"chi2_scaled", "chi2_scaled_p_value", "scaling_factor", "shift_parameter",
		"chi2_baseline_scaled", "cfi_scaled", "tli_scaled", "rmsea_scaled"}
}

// SEMThresholdRowsDefault is predict's estimate of an ordered member's
// threshold rows when the schema does not bound its categories: a 1-5
// rating's four.
const SEMThresholdRowsDefault = 4

// ThresholdRowsEstimate is predict's estimate of an ordinal fit's
// threshold rows (`x | tk`): per ordered member its schema bound less 1
// (OrderedLevels), else SEMThresholdRowsDefault. The runtime's count is
// the data's: each member's observed categories less 1.
func (p *SEMPlan) ThresholdRowsEstimate() int {
	if !p.Ordinal() {
		return 0
	}
	n := 0
	for i := range p.Ordered {
		l := 0
		if i < len(p.OrderedLevels) {
			l = p.OrderedLevels[i]
		}
		if l > 1 {
			n += l - 1
		} else {
			n += SEMThresholdRowsDefault
		}
	}
	return n
}

// ThresholdRowsBound is the threshold-row count p-value predict files:
// per ordered member its schema bound less 1 when known — an upper
// bound, a category no row holds is no threshold — else 1, the fewest
// a fitting member has (a constant one fails the slot). known reports
// that every member's bound was the schema's.
func (p *SEMPlan) ThresholdRowsBound() (n int, known bool) {
	if !p.Ordinal() {
		return 0, true
	}
	known = true
	for i := range p.Ordered {
		if i < len(p.OrderedLevels) && p.OrderedLevels[i] > 1 {
			n += p.OrderedLevels[i] - 1
			continue
		}
		n++
		known = false
	}
	return n, known
}

// Bootstrap reports whether the plan bootstraps its fit.
func (p *SEMPlan) Bootstrap() bool { return p != nil && p.Resample == SEMResampleBootstrap }

// MAT_SEM's params.resample value and the bootstrap's defaults.
const (
	SEMResampleBootstrap     = "bootstrap"
	SEMBootstrapItersDefault = 1000
	SEMCILevelDefault        = 0.95
)

// SpecSEMResample is a MAT_SEM spec's params.resample as written ("" on
// any other type, when absent, or when the params do not decode) — the
// weight resolver's input (a bootstrap refuses every weight), read
// before the params are judged.
func SpecSEMResample(spec types.MatrixSpec) string {
	if spec.Type != types.MAT_SEM || len(spec.Params) == 0 {
		return ""
	}
	var p struct {
		Resample *string `json:"resample"`
	}
	if paramread.Probe(spec.Params, &p) != nil || p.Resample == nil {
		return ""
	}
	return *p.Resample
}

// SEMResample are the accepted non-empty params.resample values.
func SEMResample() []string { return []string{SEMResampleBootstrap} }

// SEMBootstrapScalarKeys are the scalars a bootstrapped fit adds: the
// replicates run and those that refitted (the SE / p / interval read
// these only).
func SEMBootstrapScalarKeys() []string {
	return []string{"bootstrap_requested", "bootstrap_succeeded"}
}

// Grouped reports whether the plan is a joint multi-group fit.
func (p *SEMPlan) Grouped() bool { return p != nil && p.Group != "" }

// SEMGroupFieldTypes are the field types params.group accepts: the
// categorical types, packed_bool and the small integers u4 / u8.
func SEMGroupFieldTypes() []encoding.FieldType {
	return []encoding.FieldType{encoding.FieldTypeCategoricalU8, encoding.FieldTypeCategoricalU16,
		encoding.FieldTypeCategoricalU32, encoding.FieldTypePackedBool, encoding.FieldTypeU4, encoding.FieldTypeU8}
}

// SEMGroupAuxiliaryKeys are a grouped fit's per-level auxiliary keys
// for level, in emission order.
func SEMGroupAuxiliaryKeys(level string) []string {
	return []string{"implied_covariance." + level, "residual_covariance." + level,
		"implied_means." + level, "observed_means." + level}
}

// SEMGroupChi2Key and SEMGroupNKey are a grouped fit's per-level
// scalars: the group's chi-square n_g·F_g and its n (Σw under a
// frequency weight).
func SEMGroupChi2Key(level string) string { return "chi2_group_" + level }

func SEMGroupNKey(level string) string { return "n_group_" + level }

// GroupLevelsKnown is a grouped plan's levels as the schema knows them
// (an upper bound: a level without rows after the filters is no group)
// and their basis — BucketBasisDictionary (a categorical),
// BucketBasisBoolean (packed_bool) or, on an integer field whose levels
// are its observed values, nil and BucketBasisUnknown. A fresh slice.
func (p *SEMPlan) GroupLevelsKnown() ([]string, string) {
	switch {
	case p.GroupType.IsCategorical():
		return append([]string(nil), p.GroupLevels...), BucketBasisDictionary
	case p.GroupType == encoding.FieldTypePackedBool:
		return append([]string(nil), p.GroupLevels...), BucketBasisBoolean
	}
	return nil, BucketBasisUnknown
}

// groupStateBound is the per-level co-moment states a grouped plan
// holds beside its base state, at most: one per known level, else the
// integer type's value range (u4 16, u8 256).
func (p *SEMPlan) groupStateBound() int64 {
	switch {
	case p.GroupLevels != nil:
		return int64(len(p.GroupLevels))
	case p.GroupType == encoding.FieldTypeU4:
		return 16
	}
	return 256
}

// Lavaan reports whether the resolved fit follows lavaan's conventions
// (sem.Model.Lavaan: a mean structure); false before resolution.
func (p *SEMPlan) Lavaan() bool { return p != nil && p.Model != nil && p.Model.Lavaan() }

// HasMeans reports whether the resolved fit models the means
// (auxiliary implied_means / observed_means, `~1` estimate rows).
func (p *SEMPlan) HasMeans() bool { return p != nil && p.Model != nil && p.Model.MeanStructure }

// MAT_SEM's params.singular values: fit a rank-deficient covariance as
// semopy's default fit does — raw while it factors, else statsmodels'
// cov_nearest substitute (PULSE_MATRIX_NEAREST_PD) — or refuse it
// (PULSE_MATRIX_SINGULAR).
const (
	SEMSingularRefuse  = "refuse"
	SEMSingularNearest = "nearest"
)

// SEMSingular are the accepted params.singular values, default first.
func SEMSingular() []string { return []string{SEMSingularNearest, SEMSingularRefuse} }

// semParams is MAT_SEM's params object: the model description, the
// solver settings and the missing-data knobs (listwise only); any
// other key is refused.
type semParams struct {
	Model       *string  `json:"model"`
	Solver      *string  `json:"solver"`
	Information *string  `json:"information"`
	Ftol        *float64 `json:"ftol"`
	MaxIter     *int     `json:"max_iter"`
	GradTol     *float64 `json:"grad_tol"`
	Singular    *string  `json:"singular"`
	// MeanStructure is lavaan's meanstructure: observed intercepts and
	// latent means join the model (lavaan conventions).
	MeanStructure *bool `json:"meanstructure"`
	// Group is lavaan's group: the field whose levels are the groups of
	// a joint multi-group fit; GroupReference the primary's level.
	Group          *string `json:"group"`
	GroupReference *string `json:"group_reference"`
	// GroupEqual / GroupPartial are lavaan's group.equal /
	// group.partial: a list, or one comma-separated string (a compose
	// sweep axis value).
	GroupEqual   *semList `json:"group_equal"`
	GroupPartial *semList `json:"group_partial"`
	// Resample is lavaan's se = "bootstrap" (REG_RESAMPLE's naming):
	// BootstrapIters replicates seeded by RNGSeed, percentile intervals
	// at CILevel.
	Resample       *string  `json:"resample"`
	BootstrapIters *int     `json:"bootstrap_iters"`
	RNGSeed        *int64   `json:"rng_seed"`
	CILevel        *float64 `json:"ci_level"`
	// Estimator is lavaan's estimator (ml | dwls | wlsmv); Ordered
	// lavaan's ordered = (the ordered members by label: a list, or one
	// comma-separated string).
	Estimator *string  `json:"estimator"`
	Ordered   *semList `json:"ordered"`
	missingParams
}

// semList is a list param that also reads as one comma-separated
// string ("" is the empty list): a compose sweep axis value is a
// scalar, so a sweep over group_equal writes "loadings,intercepts".
type semList []string

func (l *semList) UnmarshalJSON(b []byte) error {
	var one string
	if err := json.Unmarshal(b, &one); err == nil {
		*l = nil
		for _, v := range strings.Split(one, ",") {
			if v = strings.TrimSpace(v); v != "" {
				*l = append(*l, v)
			}
		}
		return nil
	}
	var many []string
	if err := json.Unmarshal(b, &many); err != nil {
		return err
	}
	*l = many
	return nil
}

// SEMSolvers and SEMInformation are the accepted params.solver and
// params.information values, default first.
func SEMSolvers() []string {
	return []string{string(sem.SolverSLSQP), string(sem.SolverFisher)}
}

func SEMInformation() []string {
	return []string{string(sem.InformationDense), string(sem.InformationRank2)}
}

// SEMEstimateColumns are the columns of a semopy-convention MAT_SEM
// fit's auxiliary.estimates, in order.
func SEMEstimateColumns() []string {
	return []string{"estimate", "std_estimate", "std_err", "z_value", "p_value"}
}

// SEMEstimateColumnsOf are the columns of plan's auxiliary.estimates,
// in order — the ONE list the runtime renders and predict sizes: a
// lavaan-convention fit (SEMPlan.Lavaan) adds the 95% Wald interval
// ci_lower / ci_upper; a semopy one is SEMEstimateColumns.
func SEMEstimateColumnsOf(plan *SEMPlan) []string {
	cols := SEMEstimateColumns()
	if plan.Lavaan() {
		cols = append(cols, "ci_lower", "ci_upper")
	}
	return cols
}

// semDefaultSolver is the solver a fit without params.solver runs:
// Fisher scoring (to the optimum) under lavaan conventions — a
// meanstructure param, a group, a bootstrap, a `~ 1` line or a `:=`
// line — else semopy's SLSQP.
func semDefaultSolver(p semParams) sem.Solver {
	if (p.MeanStructure != nil && *p.MeanStructure) || p.Group != nil || (p.Resample != nil && *p.Resample != "") ||
		(p.Estimator != nil && sem.Estimator(*p.Estimator).Ordinal()) ||
		(p.Model != nil && (sem.HasIntercepts(*p.Model) || sem.HasDefined(*p.Model))) {
		return sem.SolverFisher
	}
	return sem.SolverSLSQP
}

// MAT_SEM's params surfaces a feature profile can hide: the mean
// structure, the joint multi-group fit, the `:=` defined parameters and
// the bootstrap (the capability rows internal/descriptor declares under
// these names).
// Hidden, a surface resolves as never existing — its params keys leave
// the instance's params allowlist (internal/descriptor.ParamsAllowlist:
// refused as unknown keys), its model syntax is refused
// (semHiddenSyntaxRefusal) — and the manifest drops its params and
// output keys (SEMFeatureParams, SEMFeatureOutputs). The defined
// parameters own model syntax only: no params key, no output key (their
// rows ride auxiliary.estimates).
const (
	SEMFeatureMeanStructure = "capability:sem_meanstructure"
	SEMFeatureGroups        = "capability:sem_groups"
	SEMFeatureDefined       = "capability:sem_defined"
	SEMFeatureBootstrap     = "capability:sem_bootstrap"
	SEMFeatureOrdinal       = "capability:sem_ordinal"
)

// SEMFeatureParams returns the MAT_SEM params keys each hideable
// surface owns, in capability-declaration order (a fresh map).
func SEMFeatureParams() map[string][]string {
	return map[string][]string{
		SEMFeatureMeanStructure: {"meanstructure"},
		SEMFeatureGroups:        {"group", "group_reference", "group_equal", "group_partial"},
		SEMFeatureDefined:       {},
		SEMFeatureBootstrap:     {"resample", "bootstrap_iters", "rng_seed", "ci_level"},
		SEMFeatureOrdinal:       {"estimator", "ordered"},
	}
}

// SEMOutputKeyLevel is the placeholder a patterned MAT_SEM output key
// carries where a grouped fit writes a level's label: the manifest
// declares "implied_means.<level>" for the runtime's "implied_means.2"
// (MatrixOutputKeyMatches).
const SEMOutputKeyLevel = "<level>"

// SEMFeatureOutputs returns the MAT_SEM output keys each hideable
// surface owns, by MatrixResult part ("auxiliary", "vectors",
// "scalars"; a fresh map): what only a mean structure, or only a
// grouped fit, emits.
func SEMFeatureOutputs() map[string]map[string][]string {
	group := map[string][]string{
		"auxiliary": SEMGroupAuxiliaryKeys(SEMOutputKeyLevel),
		"vectors":   {"group", "label"},
		"scalars":   {SEMGroupChi2Key(SEMOutputKeyLevel), SEMGroupNKey(SEMOutputKeyLevel)},
	}
	return map[string]map[string][]string{
		SEMFeatureMeanStructure: {"auxiliary": {"implied_means", "observed_means"}},
		SEMFeatureGroups:        group,
		SEMFeatureBootstrap:     {"scalars": SEMBootstrapScalarKeys()},
		SEMFeatureOrdinal:       {"scalars": SEMOrdinalScalarKeys()},
	}
}

// MatrixOutputKeyMatches reports whether a result key matches a
// declared output key: equal, or — a declared key holding
// SEMOutputKeyLevel — the key with a non-empty level in its place.
func MatrixOutputKeyMatches(declared, key string) bool {
	pre, post, ok := strings.Cut(declared, SEMOutputKeyLevel)
	if !ok {
		return declared == key
	}
	return len(key) > len(pre)+len(post) && strings.HasPrefix(key, pre) && strings.HasSuffix(key, post)
}

// semHiddenSyntaxRefusal refuses the model syntax a hidden surface
// owns: a `~ 1` line without the mean structure, a per-group modifier
// c(...) without the multi-group fit, a `:=` line without the defined
// parameters. The message names no surface.
func semHiddenSyntaxRefusal(at string, m *Matrix, hidden func(string) bool) *errors.CodedError {
	if m.Type != types.MAT_SEM || hidden == nil || m.SEM == nil || m.SEM.Desc == "" {
		return nil
	}
	if hidden(SEMFeatureMeanStructure) && sem.HasIntercepts(m.SEM.Desc) {
		return matrixInvalid(at, "bad_params", at+" params.model: an intercept term 1 (x ~ 1) is not supported; drop the line",
			map[string]any{"param": "model"})
	}
	if hidden(SEMFeatureGroups) && sem.HasGroupModifiers(m.SEM.Desc) {
		return matrixInvalid(at, "bad_params", at+" params.model: a c(...) modifier is not supported; use a label (a*x) or a number (0.5*x)",
			map[string]any{"param": "model"})
	}
	if hidden(SEMFeatureDefined) && sem.HasDefined(m.SEM.Desc) {
		return matrixInvalid(at, "bad_params", at+" params.model: a name := expr line is not supported; drop the line",
			map[string]any{"param": "model"})
	}
	return nil
}

// SEMDefaultModel is MAT_SEM's model when params.model is absent: one
// factor "F1" over every member, by label.
func SEMDefaultModel(labels []string) string {
	return "F1 =~ " + strings.Join(labels, " + ")
}

// decodeSEM decodes MAT_SEM's solver settings and keeps the model
// description for resolveSEM, which needs the members.
func decodeSEM(at string, p semParams, m *Matrix) *errors.CodedError {
	plan := &SEMPlan{Singular: SEMSingularNearest}
	if p.MeanStructure != nil {
		plan.MeanStructure = *p.MeanStructure
	}
	solver := semDefaultSolver(p)
	if p.Group != nil {
		if *p.Group == "" {
			return matrixInvalid(at, "bad_params", at+" params.group is empty; name the field whose levels are the groups, or omit it",
				map[string]any{"param": "group"})
		}
		if p.MeanStructure != nil && !*p.MeanStructure {
			return matrixInvalid(at, "bad_params", at+" params.meanstructure is false, but a multi-group fit (params.group) always models the means; drop meanstructure",
				map[string]any{"param": "meanstructure", "value": false})
		}
		plan.Group = *p.Group
	}
	for _, lp := range []struct {
		name string
		v    *semList
		out  *[]string
	}{{"group_equal", p.GroupEqual, &plan.GroupEqual}, {"group_partial", p.GroupPartial, &plan.GroupPartial}} {
		if lp.v == nil {
			continue
		}
		if p.Group == nil {
			return matrixInvalid(at, "bad_params", at+" params."+lp.name+" applies with params.group only",
				map[string]any{"param": lp.name, "value": []string(*lp.v)})
		}
		*lp.out = []string(*lp.v)
	}
	for _, v := range plan.GroupEqual {
		if !slices.Contains(sem.GroupEqualValues(), v) {
			msg := at + " params.group_equal " + strconv.Quote(v) + " is not a lavaan group.equal value"
			if v == "thresholds" {
				msg = at + ` params.group_equal "thresholds" needs an ordinal estimator; the ML fit has no thresholds`
			}
			return matrixInvalid(at, "bad_params", msg,
				map[string]any{"param": "group_equal", "value": v, "valid": sem.GroupEqualValues()})
		}
	}
	if p.GroupReference != nil {
		if p.Group == nil {
			return matrixInvalid(at, "bad_params", at+" params.group_reference applies with params.group only",
				map[string]any{"param": "group_reference", "value": *p.GroupReference})
		}
		plan.GroupReference = *p.GroupReference
	}
	if p.Singular != nil {
		if !slices.Contains(SEMSingular(), *p.Singular) {
			return matrixInvalid(at, "bad_params", at+` params.singular must be "nearest" or "refuse"`,
				map[string]any{"param": "singular", "value": *p.Singular, "valid": SEMSingular()})
		}
		plan.Singular = *p.Singular
	}
	if p.Model != nil {
		if strings.TrimSpace(*p.Model) == "" {
			return matrixInvalid(at, "bad_params", at+" params.model is empty; omit it for a one-factor model over every member",
				map[string]any{"param": "model"})
		}
		plan.Desc = *p.Model
	}
	if p.Solver != nil {
		plan.Options.Solver = sem.Solver(*p.Solver)
		solver = plan.Options.Solver
		if !slices.Contains(SEMSolvers(), *p.Solver) {
			return matrixInvalid(at, "bad_params", at+` params.solver must be "slsqp" or "fisher"`,
				map[string]any{"param": "solver", "value": *p.Solver, "valid": SEMSolvers()})
		}
	}
	if p.Information != nil {
		plan.Options.Information = sem.Information(*p.Information)
		if !slices.Contains(SEMInformation(), *p.Information) {
			return matrixInvalid(at, "bad_params", at+` params.information must be "dense" or "rank2"`,
				map[string]any{"param": "information", "value": *p.Information, "valid": SEMInformation()})
		}
	}
	if p.Ftol != nil {
		if !(*p.Ftol > 0) {
			return matrixInvalid(at, "bad_params", at+" params.ftol must be positive",
				map[string]any{"param": "ftol", "value": *p.Ftol})
		}
		if solver == sem.SolverFisher {
			msg := at + ` params.ftol applies to solver "slsqp" only; Fisher scoring stops on grad_tol`
			if p.Solver == nil {
				msg += `; a lavaan-convention fit (meanstructure, group, resample, a ~ 1 or := line) runs Fisher scoring unless params.solver is "slsqp"`
			}
			return matrixInvalid(at, "bad_params", msg,
				map[string]any{"param": "ftol", "solver": string(sem.SolverFisher)})
		}
		plan.Options.Ftol = *p.Ftol
	}
	if p.MaxIter != nil {
		if *p.MaxIter < 1 {
			return matrixInvalid(at, "bad_params", at+" params.max_iter must be a positive integer",
				map[string]any{"param": "max_iter", "value": *p.MaxIter})
		}
		plan.Options.MaxIter = *p.MaxIter
	}
	if p.GradTol != nil {
		if !(*p.GradTol > 0) {
			return matrixInvalid(at, "bad_params", at+" params.grad_tol must be positive",
				map[string]any{"param": "grad_tol", "value": *p.GradTol})
		}
		if solver != sem.SolverFisher {
			return matrixInvalid(at, "bad_params", at+` params.grad_tol applies to solver "fisher" only; SLSQP stops on ftol`,
				map[string]any{"param": "grad_tol", "solver": string(sem.SolverSLSQP)})
		}
		plan.Options.GradTol = *p.GradTol
	}
	if err := decodeSEMBootstrap(at, p, plan); err != nil {
		return err
	}
	if err := decodeSEMOrdinal(at, p, plan); err != nil {
		return err
	}
	if p.Missing != nil && *p.Missing == MissingPairwise {
		return matrixInvalid(at, "pairwise_unsupported", at+` params.missing "pairwise" is not supported on MAT_SEM: the likelihood is one sample's covariance, so use "listwise"`,
			map[string]any{"param": "missing", "value": *p.Missing, "valid": []string{MissingListwise}})
	}
	m.SEM = plan
	return nil
}

// decodeSEMOrdinal decodes params.estimator and params.ordered: an
// estimator outside SEMEstimators, an ordinal estimator without ordered
// members, ordered members under ML, a duplicate ordered member, and
// the knobs an ordinal fit has no use for — solver "slsqp" (it runs
// Fisher scoring), information, singular (the two-step statistics are
// no covariance to substitute), meanstructure false (it always models
// the thresholds), a multi-group fit or a bootstrap (not yet) — are
// bad_params, never ignored.
func decodeSEMOrdinal(at string, p semParams, plan *SEMPlan) *errors.CodedError {
	plan.Estimator = sem.EstimatorML
	if p.Estimator != nil {
		if !slices.Contains(SEMEstimators(), *p.Estimator) {
			return matrixInvalid(at, "bad_params", at+` params.estimator must be "ml", "dwls" or "wlsmv"`,
				map[string]any{"param": "estimator", "value": *p.Estimator, "valid": SEMEstimators()})
		}
		plan.Estimator = sem.Estimator(*p.Estimator)
	}
	if p.Ordered != nil {
		plan.Ordered = []string(*p.Ordered)
	}
	if !plan.Ordinal() {
		if p.Ordered != nil {
			return matrixInvalid(at, "bad_params", at+` params.ordered names ordered members, but the ML fit treats every member as continuous; add estimator "wlsmv" (or "dwls"), or drop ordered`,
				map[string]any{"param": "ordered", "estimator": string(plan.Estimator), "value": plan.Ordered})
		}
		return nil
	}
	if len(plan.Ordered) == 0 {
		return matrixInvalid(at, "bad_params", at+" params.estimator "+strconv.Quote(string(plan.Estimator))+" fits ordered members; name them in params.ordered (the members' labels)",
			map[string]any{"param": "ordered", "estimator": string(plan.Estimator)})
	}
	seen := map[string]bool{}
	for _, v := range plan.Ordered {
		if seen[v] {
			return matrixInvalid(at, "bad_params", at+" params.ordered lists "+strconv.Quote(v)+" twice",
				map[string]any{"param": "ordered", "value": v})
		}
		seen[v] = true
	}
	refuse := func(param string, value any, why string) *errors.CodedError {
		return matrixInvalid(at, "bad_params", at+" params."+param+" "+why,
			map[string]any{"param": param, "value": value, "estimator": string(plan.Estimator)})
	}
	switch {
	case p.Solver != nil && sem.Solver(*p.Solver) != sem.SolverFisher:
		return refuse("solver", *p.Solver, `is not available with an ordinal estimator, which runs Fisher scoring; drop solver or set "fisher"`)
	case p.Information != nil:
		return refuse("information", *p.Information, "applies to the ML fit only; an ordinal estimator weighs the two-step statistics by their asymptotic variances; drop information")
	case p.Singular != nil:
		return refuse("singular", *p.Singular, "applies to the ML fit's covariance only; drop singular")
	case p.MeanStructure != nil && !*p.MeanStructure:
		return refuse("meanstructure", false, "is false, but an ordinal fit always models the thresholds and means; drop meanstructure")
	case p.Group != nil:
		return refuse("group", *p.Group, "with an ordinal estimator is not supported yet; fit each group alone or use the ML estimator")
	case plan.Bootstrap():
		return refuse("resample", plan.Resample, "with an ordinal estimator is not supported yet; drop resample (the robust standard errors are analytic)")
	}
	return nil
}

// resolveSEMOrdinal judges an ordinal plan once its members and the
// schema are known (nil schema: the bounds stay unknown): a vector
// standardizing its members is refused (an ordered member's categories
// are its raw values), and each ordered member's category-count bound
// is recorded (OrderedLevels).
func resolveSEMOrdinal(at string, schema *encoding.Schema, m *Matrix) *errors.CodedError {
	if m.Type != types.MAT_SEM || !m.SEM.Ordinal() {
		return nil
	}
	if m.Members.Standardize != "" {
		return matrixInvalid(at, "bad_params", at+" params.estimator "+strconv.Quote(string(m.SEM.Estimator))+" reads each ordered member's raw categories, but the members' vector standardizes them; drop standardize",
			map[string]any{"param": "estimator", "standardize": m.Members.Standardize})
	}
	labels := m.FitLabels()
	m.SEM.OrderedLevels = make([]int, len(m.SEM.Ordered))
	if schema == nil || m.Members.Refs != nil {
		return nil
	}
	for i, v := range m.SEM.Ordered {
		k := slices.Index(labels, v)
		if k < 0 || k >= len(m.Members.Members) {
			continue
		}
		f := schema.Field(m.Members.Members[k])
		if f == nil {
			continue
		}
		switch f.Type {
		case encoding.FieldTypePackedBool:
			m.SEM.OrderedLevels[i] = 2
		case encoding.FieldTypeU4:
			m.SEM.OrderedLevels[i] = 16
		}
	}
	return nil
}

// decodeSEMBootstrap decodes params.resample and its knobs:
// bootstrap_iters a positive integer, rng_seed any integer — either
// without resample "bootstrap" (or resample "" — none) is bad_params,
// never ignored — and ci_level strictly between 0 and 1 (any
// lavaan-convention fit's Wald interval too; resolveSEM refuses it on
// a semopy-convention one).
func decodeSEMBootstrap(at string, p semParams, plan *SEMPlan) *errors.CodedError {
	on := false
	plan.CILevel = SEMCILevelDefault
	if p.Resample != nil && *p.Resample != "" {
		if !slices.Contains(SEMResample(), *p.Resample) {
			return matrixInvalid(at, "bad_params", at+` params.resample must be "bootstrap" (non-parametric, lavaan's se = "bootstrap") or omitted`,
				map[string]any{"param": "resample", "value": *p.Resample, "valid": SEMResample()})
		}
		on = true
		plan.Resample, plan.BootstrapIters = *p.Resample, SEMBootstrapItersDefault
	}
	if p.CILevel != nil {
		if !(*p.CILevel > 0 && *p.CILevel < 1) {
			return matrixInvalid(at, "bad_params", at+" params.ci_level must lie strictly between 0 and 1",
				map[string]any{"param": "ci_level", "value": *p.CILevel})
		}
		plan.CILevel, plan.CILevelSet = *p.CILevel, true
		plan.Options.Level = *p.CILevel
	}
	for _, k := range []struct {
		name string
		set  bool
	}{{"bootstrap_iters", p.BootstrapIters != nil}, {"rng_seed", p.RNGSeed != nil}} {
		if k.set && !on {
			return matrixInvalid(at, "bad_params", at+" params."+k.name+` applies with params.resample "bootstrap" only`,
				map[string]any{"param": k.name})
		}
	}
	if !on {
		return nil
	}
	if p.BootstrapIters != nil {
		if *p.BootstrapIters < 1 {
			return matrixInvalid(at, "bad_params", at+" params.bootstrap_iters must be a positive integer",
				map[string]any{"param": "bootstrap_iters", "value": *p.BootstrapIters})
		}
		plan.BootstrapIters = *p.BootstrapIters
	}
	if p.RNGSeed != nil {
		plan.RNGSeed = *p.RNGSeed
	}
	return nil
}

// resolveSEM parses MAT_SEM's model once the members are known and
// matches its observed variables to them by label (FitLabels: the
// display label, else the member name): every observed variable must be
// a member and every member an observed variable — a member the model
// never names is refused rather than silently left out of the fit.
func resolveSEM(at string, m *Matrix) *errors.CodedError {
	if m.Type != types.MAT_SEM {
		return nil
	}
	if m.SEM == nil {
		m.SEM = &SEMPlan{Singular: SEMSingularNearest}
	}
	labels := m.FitLabels()
	if m.SEM.Desc == "" {
		m.SEM.Desc = SEMDefaultModel(labels)
	}
	newModel := sem.NewModelWithOptions
	if m.SEM.Grouped() {
		newModel = sem.NewGroupModel
	}
	model, err := newModel(m.SEM.Desc, sem.ModelOptions{MeanStructure: m.SEM.MeanStructure,
		GroupEqual: m.SEM.GroupEqual, GroupPartial: m.SEM.GroupPartial, Lavaan: m.SEM.Bootstrap(), Ordered: m.SEM.Ordered})
	var oe *sem.OrdinalSpecError
	if stderrors.As(err, &oe) {
		return matrixInvalid(at, "bad_params", at+" params."+oe.Param+": "+oe.Error(),
			map[string]any{"param": oe.Param, "variable": oe.Var, "problem": oe.Reason})
	}
	if ge, ok := err.(*sem.GroupSpecError); ok {
		return matrixInvalid(at, "bad_params", at+" params."+ge.Error(),
			map[string]any{"param": ge.Param, "value": ge.Value})
	}
	var de *sem.DefinedError
	if stderrors.As(err, &de) {
		extra := map[string]any{"param": "model", "defined": de.Name, "problem": de.Reason, "error": err.Error()}
		if de.Ref != "" {
			extra["reference"] = de.Ref
		}
		return matrixInvalid(at, "defined_invalid", at+" params.model: "+err.Error(), extra)
	}
	var ce *sem.ConstraintError
	if stderrors.As(err, &ce) {
		return matrixInvalid(at, "constraint_unsupported", at+" params.model: "+err.Error(),
			map[string]any{"param": "model", "constraint": ce.Op, "error": err.Error()})
	}
	if err != nil {
		return matrixInvalid(at, "bad_params", at+" params.model: "+err.Error(),
			map[string]any{"param": "model", "error": err.Error()})
	}
	pos := make(map[string]int, len(labels))
	for i, l := range labels {
		pos[l] = i
	}
	order := make([]int, len(model.Observed))
	var missing []string
	used := make(map[int]bool, len(order))
	for k, v := range model.Observed {
		i, ok := pos[v]
		if !ok {
			missing = append(missing, v)
			continue
		}
		order[k] = i
		used[i] = true
	}
	if len(missing) > 0 {
		return matrixInvalid(at, "bad_params", at+" params.model names observed variables that are not members: "+strings.Join(missing, ", "),
			map[string]any{"param": "model", "not_members": missing, "members": labels})
	}
	var unused []string
	for i, l := range labels {
		if !used[i] {
			unused = append(unused, l)
		}
	}
	if len(unused) > 0 {
		return matrixInvalid(at, "bad_params", at+" members the model never names: "+strings.Join(unused, ", ")+"; drop them from the battery or add them to params.model",
			map[string]any{"param": "model", "unused_members": unused})
	}
	if m.SEM.CILevelSet && !model.Lavaan() {
		return matrixInvalid(at, "bad_params", at+" params.ci_level sets the Wald interval (ci_lower / ci_upper) of a lavaan-convention fit, but this fit follows semopy's conventions and reports none; add meanstructure, a := line or resample \"bootstrap\", or drop ci_level",
			map[string]any{"param": "ci_level", "value": m.SEM.CILevel})
	}
	m.SEM.Model, m.SEM.Order = model, order
	return nil
}

// resolveSEMGroup checks a grouped MAT_SEM's params.group against the
// request and the schema (nil skips the field checks): the request is
// not grouped itself (each bucket of a grouped request is an
// ungrouped fit — group_with_groups), the field exists, is no member
// and has a SEMGroupFieldTypes type (group_field_type), a categorical
// dictionary holds two levels at least (group_one_level), no params.fit
// binds the slot, and a group_reference names a level of the field.
func resolveSEMGroup(at string, req *types.Request, schema *encoding.Schema, m *Matrix) *errors.CodedError {
	if m.Type != types.MAT_SEM || !m.SEM.Grouped() {
		return nil
	}
	plan := m.SEM
	if req != nil && (len(req.Groups) > 0 || req.Crosstab != nil) {
		return matrixInvalid(at, "group_with_groups", at+" params.group fits one joint multi-group model, but the request is grouped too (each bucket is then its own fit); drop the request groups / crosstab or params.group",
			map[string]any{"param": "group", "group": plan.Group})
	}
	if m.Members.Standardize != "" {
		return matrixInvalid(at, "bad_params", at+" params.group fits each group's own moments, but the members' vector standardizes them over the pooled rows; drop standardize or params.group",
			map[string]any{"param": "group", "standardize": m.Members.Standardize})
	}
	if m.Fit != "" {
		return matrixInvalid(at, "bad_params", at+" params.group splits the slot's rows, but params.fit binds a fitted state that folds none; drop one of them",
			map[string]any{"param": "group", "fit": m.Fit})
	}
	for _, f := range m.Members.SourceFields() {
		if f == plan.Group {
			return matrixInvalid(at, "bad_params", at+" params.group "+strconv.Quote(plan.Group)+" is a member of the model; the group field splits the rows and is never an observed variable",
				map[string]any{"param": "group", "group": plan.Group})
		}
	}
	if schema == nil {
		return nil
	}
	f := schema.Field(plan.Group)
	if f == nil {
		return matrixInvalid(at, "bad_params", at+" params.group names field "+strconv.Quote(plan.Group)+", which the cohort does not have",
			map[string]any{"param": "group", "group": plan.Group})
	}
	if !slices.Contains(SEMGroupFieldTypes(), f.Type) {
		valid := make([]string, 0, len(SEMGroupFieldTypes()))
		for _, t := range SEMGroupFieldTypes() {
			valid = append(valid, t.String())
		}
		return matrixInvalid(at, "group_field_type", at+" params.group field "+strconv.Quote(plan.Group)+" is "+f.Type.String()+"; a group field is categorical, packed_bool or a small integer (u4, u8)",
			map[string]any{"param": "group", "group": plan.Group, "field_type": f.Type.String(), "valid": valid})
	}
	plan.GroupType = f.Type
	switch {
	case f.Type.IsCategorical():
		if f.Dictionary != nil {
			// GROUP_CATEGORY's key: the label, else (an empty label)
			// the index.
			for i, l := range f.Dictionary.Values() {
				if l == "" {
					l = strconv.Itoa(i)
				}
				plan.GroupLevels = append(plan.GroupLevels, l)
			}
		}
		if len(plan.GroupLevels) < 2 {
			return matrixInvalid(at, "group_one_level", at+" params.group field "+strconv.Quote(plan.Group)+" has fewer than two levels; a multi-group fit needs two groups at least",
				map[string]any{"param": "group", "group": plan.Group, "levels": plan.GroupLevels})
		}
	case f.Type == encoding.FieldTypePackedBool:
		plan.GroupLevels = []string{"0", "1"}
	}
	if k := plan.Model.Arity(); k > 0 {
		if plan.GroupLevels == nil {
			return matrixInvalid(at, "bad_params", at+" params.model's per-group modifiers c(...) read one value per level, but the levels of "+f.Type.String()+" field "+strconv.Quote(plan.Group)+" are its observed values; group by a categorical or packed_bool field",
				map[string]any{"param": "model", "group": plan.Group, "arity": k, "field_type": f.Type.String()})
		}
		if err := plan.Model.CheckGroupCount(len(plan.GroupLevels)); err != nil {
			return matrixInvalid(at, "bad_params", at+" params.model: "+err.Error()+" (the levels of "+strconv.Quote(plan.Group)+", in order: "+strings.Join(plan.GroupLevels, ", ")+")",
				map[string]any{"param": "model", "group": plan.Group, "arity": k, "groups": len(plan.GroupLevels), "levels": plan.GroupLevels})
		}
	}
	if plan.GroupReference == "" {
		return nil
	}
	ok := false
	if plan.GroupLevels != nil {
		ok = slices.Contains(plan.GroupLevels, plan.GroupReference)
	} else if v, err := strconv.ParseUint(plan.GroupReference, 10, 8); err == nil {
		ok = strconv.FormatUint(v, 10) == plan.GroupReference && (f.Type != encoding.FieldTypeU4 || v < 16)
	}
	if !ok {
		return matrixInvalid(at, "bad_params", at+" params.group_reference "+strconv.Quote(plan.GroupReference)+" is not a level of field "+strconv.Quote(plan.Group),
			map[string]any{"param": "group_reference", "value": plan.GroupReference, "levels": plan.GroupLevels})
	}
	return nil
}
