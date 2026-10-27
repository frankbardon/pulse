package descriptor

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/optblock"
	"github.com/frankbardon/pulse/types"
)

// optimizeBatch is a batch with one regression fit (demand: units on
// price) and one optimize block maximising revenue over the fitted
// curve; edit adjusts the block.
func optimizeBatch(edit func(*types.OptimizeSpec)) *types.ComposedRequest {
	spec := types.OptimizeSpec{
		Name: "price",
		Variables: []types.OptimizeVariable{
			{Name: "p", Lower: types.BoundParam("p_min"), Upper: types.BoundValue(50)},
		},
		Objective: `p * (coef("demand", "(intercept)") + coef("demand", "price") * p)`,
		Direction: types.OptimizeMaximize,
	}
	if edit != nil {
		edit(&spec)
	}
	return &types.ComposedRequest{
		Requests: []*types.Request{{Cohort: &types.Cohort{Filename: "sales.pulse"}, Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT}}}},
		Params:   types.Params{"p_min": 1},
		Fits: []types.FitSpec{{Name: "demand", Request: &types.Request{
			Cohort:      &types.Cohort{Filename: "sales.pulse"},
			Regressions: []*types.RegressionSpec{{Type: types.REG_OLS, Target: "units", Predictors: []string{"price"}}},
		}}},
		Optimize: []types.OptimizeSpec{spec},
	}
}

// TestOptimize_PredictRefusals: predict refuses each malformed block
// with the block's code, located details.optimize, and judges nothing
// past it; a valid block passes and is summarised.
func TestOptimize_PredictRefusals(t *testing.T) {
	t.Parallel()

	two := 2
	zero := 0
	cases := []struct {
		name string
		edit func(*types.OptimizeSpec)
		code errors.Code
	}{
		{"unknown identifier", func(s *types.OptimizeSpec) { s.Objective = "p * units" }, errors.PULSE_OPTIMIZE_EXPRESSION_INVALID},
		{"unknown fit", func(s *types.OptimizeSpec) { s.Objective = `coef("supply", "price") * p` }, errors.PULSE_MODEL_UNKNOWN},
		{"unknown term", func(s *types.OptimizeSpec) { s.Objective = `coef("demand", "cost") * p` }, errors.PULSE_MODEL_TERM_UNKNOWN},
		{"duplicate variable", func(s *types.OptimizeSpec) { s.Variables = append(s.Variables, s.Variables[0]) }, errors.PULSE_OPTIMIZE_VARIABLE_INVALID},
		{"lower above upper", func(s *types.OptimizeSpec) { s.Variables[0].Upper = types.BoundValue(0.5) }, errors.PULSE_OPTIMIZE_VARIABLE_INVALID},
		{"empty objective", func(s *types.OptimizeSpec) { s.Objective = "" }, errors.PULSE_OPTIMIZE_SPEC_INVALID},
		{"bad constraint type", func(s *types.OptimizeSpec) {
			s.Constraints = []types.OptimizeConstraint{{Expr: "p - 2", Type: "lte"}}
		}, errors.PULSE_OPTIMIZE_SPEC_INVALID},
		{"starts below one", func(s *types.OptimizeSpec) { s.Starts = &zero }, errors.PULSE_OPTIMIZE_SPEC_INVALID},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			env := ValidateCompose(optimizeBatch(c.edit))
			res := env.Data.(*ComposeValidationResult)
			if res.Valid || len(env.Errors) != 1 || env.Errors[0].Code != string(c.code) {
				t.Fatalf("valid %v errors %+v, want one %s", res.Valid, env.Errors, c.code)
			}
			if env.Errors[0].Details["optimize"] != 0 {
				t.Errorf("details %v carry no optimize index", env.Errors[0].Details)
			}
			if res.Optimize != nil {
				t.Errorf("a refused block is summarised: %+v", res.Optimize)
			}
		})
	}

	env := ValidateCompose(optimizeBatch(func(s *types.OptimizeSpec) {
		s.Starts = &two
		s.Constraints = []types.OptimizeConstraint{{Expr: "40 - p", Type: types.OptimizeConstraintIneq}}
	}))
	for _, e := range env.Errors {
		if e.Code == string(errors.PULSE_OPTIMIZE_SPEC_INVALID) || e.Code == string(errors.PULSE_OPTIMIZE_EXPRESSION_INVALID) ||
			e.Code == string(errors.PULSE_OPTIMIZE_VARIABLE_INVALID) || e.Code == string(errors.PULSE_MODEL_UNKNOWN) {
			t.Fatalf("valid block refused: %+v", e)
		}
	}
	res := env.Data.(*ComposeValidationResult)
	spec := optimizeBatch(nil).Optimize[0]
	spec.Starts = &two
	spec.Constraints = []types.OptimizeConstraint{{Expr: "40 - p", Type: types.OptimizeConstraintIneq}}
	want := []descriptor.OptimizeSummary{{
		Name: "price", Direction: types.OptimizeMaximize, Variables: []string{"p"},
		Constraints: 1, Starts: 2, RNGSeed: optblock.EffectiveSeed(&spec),
	}}
	if !reflect.DeepEqual(res.Optimize, want) {
		t.Fatalf("summary %+v, want %+v", res.Optimize, want)
	}
	if want[0].RNGSeed == 0 {
		t.Fatal("no derived seed")
	}
	// The default starts are 8.
	res = ValidateCompose(optimizeBatch(nil)).Data.(*ComposeValidationResult)
	if len(res.Optimize) != 1 || res.Optimize[0].Starts != types.OptimizeDefaultStarts {
		t.Fatalf("default starts summary %+v", res.Optimize)
	}
}

// TestOptimize_HiddenSlotRefusal: an instance hiding
// capability:compose_optimize refuses a set `optimize` as an unknown
// key whose valid_keys never name it; the enabled instance refuses
// nothing and an optimize-free batch is never refused.
func TestOptimize_HiddenSlotRefusal(t *testing.T) {
	t.Parallel()

	req := optimizeBatch(nil)
	req.Fits, req.Params = nil, nil
	ce := asCoded(t, SlotRefusal(req, scopedOnly(featCompose)))
	if ce.Code != errors.PULSE_REQUEST_UNKNOWN_FIELD || !reflect.DeepEqual(ce.Details["unknown_keys"], []string{"optimize"}) {
		t.Fatalf("got %s %v", ce.Code, ce.Details)
	}
	for _, k := range ce.Details["valid_keys"].([]string) {
		if k == "optimize" {
			t.Errorf("valid_keys names the hidden slot: %v", ce.Details["valid_keys"])
		}
	}
	if err := SlotRefusal(req, scopedOnly(featCompose, featComposeOpt)); err != nil {
		t.Errorf("enabled instance refuses: %v", err)
	}
	req.Optimize = nil
	if err := SlotRefusal(req, scopedOnly(featCompose)); err != nil {
		t.Errorf("optimize-free batch refused: %v", err)
	}
}

// TestOptimize_HiddenSchema: the payload schema of an instance hiding
// capability:compose_optimize names no `optimize` / `optimizations`
// slot and no Optimize* def; the full registry and the enabled
// instance carry them, the bound a number-or-params union and the
// enums closed.
func TestOptimize_HiddenSchema(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		name string
		inst *InstanceSnapshot
		want bool
	}{
		{"full registry", nil, true},
		{"enabled", scopedOnly(featCompose, featComposeOpt), true},
		{"hidden", scopedOnly(featCompose), false},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			b, err := PayloadSchemaForInstance(c.inst)
			if err != nil {
				t.Fatal(err)
			}
			var doc struct {
				Defs map[string]struct {
					Properties map[string]any `json:"properties"`
					Enum       []string       `json:"enum"`
					OneOf      []any          `json:"oneOf"`
				} `json:"$defs"`
			}
			if err := json.Unmarshal(b, &doc); err != nil {
				t.Fatal(err)
			}
			_, slot := doc.Defs["ComposedRequest"].Properties["optimize"]
			_, result := doc.Defs["ComposedResponse"].Properties["optimizations"]
			_, spec := doc.Defs["OptimizeSpec"]
			_, res := doc.Defs["OptimizeResult"]
			if slot != c.want || result != c.want || spec != c.want || res != c.want {
				t.Fatalf("optimize %v optimizations %v OptimizeSpec %v OptimizeResult %v; want all %v", slot, result, spec, res, c.want)
			}
			if !c.want {
				return
			}
			if got := doc.Defs["OptimizeDirection"].Enum; !reflect.DeepEqual(got, []string{"maximize", "minimize"}) {
				t.Errorf("OptimizeDirection enum %v", got)
			}
			if got := doc.Defs["OptimizeConstraintType"].Enum; !reflect.DeepEqual(got, []string{"eq", "ineq"}) {
				t.Errorf("OptimizeConstraintType enum %v", got)
			}
			if got := len(doc.Defs["OptimizeBound"].OneOf); got != 2 {
				t.Errorf("OptimizeBound oneOf has %d arms, want 2", got)
			}
		})
	}
}

// TestErrorOwners_OptimizeCodes: the block's own codes ride
// capability:compose_optimize, and the shared expression codes it can
// raise list it among their owners.
func TestErrorOwners_OptimizeCodes(t *testing.T) {
	t.Parallel()

	for _, c := range []errors.Code{errors.PULSE_OPTIMIZE_SPEC_INVALID, errors.PULSE_OPTIMIZE_VARIABLE_INVALID, errors.PULSE_OPTIMIZE_EXPRESSION_INVALID} {
		if !reflect.DeepEqual(errorOwners[c], []string{featComposeOpt}) {
			t.Errorf("%s owners %v", c, errorOwners[c])
		}
	}
	for _, c := range []errors.Code{errors.PULSE_MODEL_UNKNOWN, errors.PULSE_MODEL_TERM_UNKNOWN, errors.PULSE_MODEL_STAT_UNKNOWN, errors.PULSE_PARAMS_UNDECLARED} {
		found := false
		for _, o := range errorOwners[c] {
			found = found || o == featComposeOpt
		}
		if !found {
			t.Errorf("%s owners %v do not list %s", c, errorOwners[c], featComposeOpt)
		}
	}
}
