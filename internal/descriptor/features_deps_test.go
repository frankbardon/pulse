package descriptor_test

import (
	"reflect"
	"testing"

	descx "github.com/frankbardon/pulse/internal/descriptor"
)

// TestFeatureDependenciesResolve pins the shape of the dependency graph
// that the descriptor layer can check on its own: every target is a
// feature row (no dangling edge), no group is empty or repeats a name, no
// feature depends on itself, the graph is acyclic, and the named hard
// edges hold. The host-completeness and handler-map halves need the
// engine and live in internal/processing (TestProfileDependenciesComplete).
func TestFeatureDependenciesResolve(t *testing.T) {
	names := map[string]bool{}
	for _, f := range descx.Features() {
		names[f.Name] = true
	}
	for _, f := range descx.Features() {
		for gi, g := range f.DependsOn {
			if len(g) == 0 {
				t.Errorf("%s: dependency group %d is empty", f.Name, gi)
			}
			seen := map[string]bool{}
			for _, target := range g {
				if !names[target] {
					t.Errorf("%s: dependency group %d names %q, which is not a feature row (dangling edge)", f.Name, gi, target)
				}
				if target == f.Name {
					t.Errorf("%s: depends on itself", f.Name)
				}
				if seen[target] {
					t.Errorf("%s: dependency group %d repeats %q", f.Name, gi, target)
				}
				seen[target] = true
			}
		}
	}

	// Acyclic: a cycle would make a feature unsatisfiable on its own.
	const (
		white = iota
		grey
		black
	)
	color := map[string]int{}
	var visit func(string, []string)
	visit = func(n string, path []string) {
		switch color[n] {
		case grey:
			t.Errorf("dependency cycle: %v -> %s", path, n)
			return
		case black:
			return
		}
		color[n] = grey
		deps, _ := descx.FeatureDependencies(n)
		for _, g := range deps {
			for _, target := range g {
				visit(target, append(path, n))
			}
		}
		color[n] = black
	}
	for _, n := range descx.FeatureNames() {
		visit(n, nil)
	}

	requestHosts := []string{"capability:process", "capability:compose", "capability:process_chain"}
	expect := map[string][][]string{
		"AGG_SUM":                           {requestHosts},
		"TEST_TUKEY_HSD":                    {requestHosts}, // no TUKEY → ANOVA edge: the pairing is advice
		"capability:joins":                  {requestHosts},
		"capability:crosstab":               {requestHosts},
		"capability:weighting":              {requestHosts},
		"capability:for_each":               {requestHosts}, // an item fan-out rides any request host
		"capability:multiplicity":           nil,            // every block is a request slot, but Facet overlays carry one too: no host group
		"capability:stream":                 {{"capability:process"}},
		"capability:watch":                  {{"capability:process"}},
		"capability:filter_to_file":         {{"capability:process"}, {"FILTER_EXPRESSION"}},
		"capability:materialize":            {{"capability:process"}},       // runs Process's pre-flight and record pipeline
		"capability:compose_sweep":          {{"capability:compose"}},       // a sweep expands into Compose slots
		"capability:compose_sweep_sampling": {{"capability:compose_sweep"}}, // a sampling mode is a sweep mode
		"capability:compose_optimize":       {{"capability:compose"}},       // an optimize block is a Compose slot
		"capability:crosstab_cells":         {{"capability:crosstab"}},      // the measures are a crosstab's cells
		// fits run before Compose slots, and a fit is one regression
		"capability:compose_fits": {{"capability:compose"}, {"ATTR_FACTOR_SCORE", "ATTR_MAHALANOBIS", "ATTR_PC_SCORE", "REG_BAYES_LINEAR", "REG_GLM", "REG_MMM_HILL", "REG_OLS", "capability:matrices"}},
		// a save writes a fitted regression, from any request host
		"capability:model_save": {requestHosts, {"ATTR_FACTOR_SCORE", "ATTR_MAHALANOBIS", "ATTR_PC_SCORE", "REG_BAYES_LINEAR", "REG_GLM", "REG_MMM_HILL", "REG_OLS", "capability:matrices"}},
		// a holdout scores a regression slot's fit, from any request host
		"capability:regression_holdout": {requestHosts, {"REG_BAYES_LINEAR", "REG_GLM", "REG_MMM_HILL", "REG_OLS"}},
		// parameters are read only by an expression, from any request host
		"capability:params": {requestHosts, {"ATTR_FORMULA", "FILTER_EXPRESSION", "OVERLAY_FORMULA", "capability:regression_holdout", "capability:compose_optimize"}},
		// a load feeds a scoring reference, from any request host or a facet
		"capability:models":            {{"capability:process", "capability:compose", "capability:process_chain", "capability:facet"}, {"ATTR_FACTOR_SCORE", "ATTR_FORMULA", "ATTR_MAHALANOBIS", "ATTR_PC_SCORE", "ATTR_REG_FITTED", "ATTR_REG_PREDICT", "ATTR_REG_RESIDUAL", "ATTR_SEM_SCORE", "ATTR_VEC_DISTANCE", "FILTER_EXPRESSION", "capability:matrices", "capability:compose_optimize"}},
		"ATTR_REG_FITTED":              {requestHosts, {"REG_OLS"}},
		"ATTR_REG_LEVERAGE":            {requestHosts, {"REG_OLS"}},
		"ATTR_REG_RESIDUAL":            {requestHosts, {"REG_OLS"}},
		"OVERLAY_T_CELL":               {{"capability:compose"}, {"AGG_WELFORD"}},
		"OVERLAY_Z_CELL":               {{"capability:compose"}, {"AGG_WELFORD"}},
		"OVERLAY_T_VS_REF":             {{"capability:compose"}, {"AGG_WELFORD"}},
		"OVERLAY_Z_VS_REF":             {{"capability:compose"}, {"AGG_WELFORD"}},
		"OVERLAY_PAIRWISE_WELCH_T":     {{"capability:crosstab"}, {"AGG_WELFORD"}},
		"OVERLAY_PAIRWISE_TWO_MEANS_Z": {{"capability:crosstab"}, {"AGG_WELFORD"}},
		// Any-of edge (FR-18): a weighted AGG_AVERAGE cell is a host too.
		"OVERLAY_PAIRWISE_WEIGHTED_TWO_MEANS_Z": {{"capability:crosstab"}, {"AGG_AVERAGE", "AGG_WEIGHTED_MEAN"}},
		"OVERLAY_SHARE_OF_TOTAL":                {{"capability:compose", "capability:crosstab"}},
		// The prior-period tests (U47 #328): a series or no-column crosstab
		// host, and the measure family whose components they read.
		"OVERLAY_T_VS_PRIOR":      {{"capability:compose", "capability:crosstab"}, {"AGG_STDDEV", "AGG_VARIANCE", "AGG_WELFORD"}},
		"OVERLAY_Z_VS_PRIOR":      {{"capability:compose", "capability:crosstab"}, {"AGG_STDDEV", "AGG_VARIANCE", "AGG_WELFORD"}},
		"OVERLAY_PROP_Z_VS_PRIOR": {{"capability:compose", "capability:crosstab"}, {"AGG_AVERAGE"}},
		"OVERLAY_YOY":             {{"capability:compose"}, {"GROUP_DATE"}},
		"capability:process":      nil,
		"io_format:csv":           nil,
		// SEM params surfaces (U59): the mean structure rides an inline SEM
		// fit (MAT_SEM or ATTR_SEM_SCORE),
		// a multi-group fit always models the means too.
		"capability:sem_meanstructure": {{"ATTR_SEM_SCORE", "MAT_SEM"}},
		"capability:sem_groups":        {{"ATTR_SEM_SCORE", "MAT_SEM"}, {"capability:sem_meanstructure"}},
		"capability:sem_defined":       {{"ATTR_SEM_SCORE", "MAT_SEM"}},
		"capability:sem_bootstrap":     {{"MAT_SEM"}},
		"capability:sem_ordinal":       {{"ATTR_SEM_SCORE", "MAT_SEM"}},
	}
	for name, want := range expect {
		got, ok := descx.FeatureDependencies(name)
		if !ok {
			t.Errorf("%s: not a feature row", name)
			continue
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: DependsOn = %v, want %v", name, got, want)
		}
	}

	// The accessors hand out copies: mutating one must not reach the table.
	deps, _ := descx.FeatureDependencies("ATTR_REG_FITTED")
	deps[1][0] = "MUTATED"
	if again, _ := descx.FeatureDependencies("ATTR_REG_FITTED"); again[1][0] != "REG_OLS" {
		t.Errorf("FeatureDependencies leaked the table's backing slice")
	}
	rows := descx.Features()
	for i := range rows {
		if len(rows[i].DependsOn) > 0 {
			rows[i].DependsOn[0][0] = "MUTATED"
		}
	}
	if again, _ := descx.FeatureDependencies("AGG_SUM"); again[0][0] != "capability:process" {
		t.Errorf("Features leaked the table's backing slice")
	}
}
