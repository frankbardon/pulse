package vectors

import (
	"slices"
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
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
	missingParams
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
// meanstructure param or a `~ 1` line — else semopy's SLSQP.
func semDefaultSolver(p semParams) sem.Solver {
	if (p.MeanStructure != nil && *p.MeanStructure) || p.Group != nil || (p.Model != nil && sem.HasIntercepts(*p.Model)) {
		return sem.SolverFisher
	}
	return sem.SolverSLSQP
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
				msg += `; a lavaan-convention fit (meanstructure, group, a ~ 1 line) runs Fisher scoring unless params.solver is "slsqp"`
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
	if p.Missing != nil && *p.Missing == MissingPairwise {
		return matrixInvalid(at, "pairwise_unsupported", at+` params.missing "pairwise" is not supported on MAT_SEM: the likelihood is one sample's covariance, so use "listwise"`,
			map[string]any{"param": "missing", "value": *p.Missing, "valid": []string{MissingListwise}})
	}
	m.SEM = plan
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
	model, err := newModel(m.SEM.Desc, sem.ModelOptions{MeanStructure: m.SEM.MeanStructure})
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
