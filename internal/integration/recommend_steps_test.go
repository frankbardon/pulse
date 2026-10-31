package integration_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// stepValues fills a draft's placeholders: the producers' needs.
var stepValues = map[string]any{
	"out":        "derived.pulse",
	"target":     "y",
	"predictors": []string{"y"},
	"component":  1,
	"factor":     1,
}

// fillSteps replaces every "<name>" placeholder (a string, or a
// one-element list of one) in v with stepValues[name].
func fillSteps(t *testing.T, v any) any {
	t.Helper()
	ph := func(s string) (any, bool) {
		if !strings.HasPrefix(s, "<") || !strings.HasSuffix(s, ">") {
			return nil, false
		}
		val, ok := stepValues[strings.Trim(s, "<>")]
		if !ok {
			t.Fatalf("no test value for placeholder %s", s)
		}
		return val, true
	}
	switch x := v.(type) {
	case string:
		if val, ok := ph(x); ok {
			return val
		}
	case []any:
		if len(x) == 1 {
			if s, ok := x[0].(string); ok {
				if val, ok := ph(s); ok {
					return val
				}
			}
		}
		for i := range x {
			x[i] = fillSteps(t, x[i])
		}
	case map[string]any:
		for k := range x {
			x[k] = fillSteps(t, x[k])
		}
	}
	return v
}

// TestRecommend_ScoreDraftsNeedNoPlan: bound over a battery cohort, no
// measure_construct draft carries a materialise plan, and none needs
// one — each score producer's draft runs with a REG_OLS beside it in
// the SAME request, the model reading the score's label.
func TestRecommend_ScoreDraftsNeedNoPlan(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs()})
	if err != nil {
		t.Fatal(err)
	}
	schema := encoding.Schema{Fields: []encoding.Field{
		{Name: "q1", Type: encoding.FieldTypeF64, Description: "First rating item of the battery"},
		{Name: "q2", Type: encoding.FieldTypeF64, Description: "Second rating item of the battery"},
		{Name: "q3", Type: encoding.FieldTypeF64, Description: "Third rating item of the battery"},
	}}
	b, err := p.NewCohortBuilder(ctx, "battery.pulse", schema, pulse.CohortBuilderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for r := range 40 {
		trait := float64(r%9) / 2
		if err := b.Append(pulse.CohortRow{trait + float64(r%3)/4, trait + float64(r%5)/5, trait + float64(r%7)/6}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.Close(); err != nil {
		t.Fatal(err)
	}
	ran := runInRequest(t, p, "measure_construct", "battery.pulse", "q1", map[string]string{
		"ATTR_PC_SCORE": "predictors", "ATTR_FACTOR_SCORE": "predictors", "ATTR_SEM_SCORE": "predictors", "ATTR_SCALE_SCORE": "target",
	})
	for _, op := range []string{"ATTR_PC_SCORE", "ATTR_FACTOR_SCORE", "ATTR_SEM_SCORE", "ATTR_SCALE_SCORE"} {
		if !ran[op] {
			t.Errorf("%s: no draft ran with its model", op)
		}
	}
}

// runInRequest recommends intent bound to cohort, fails on any draft
// carrying steps, and runs each draft of an operator in roles with one
// REG_OLS added to the same request: the model reads the draft's
// derived label (relabelled "derived") in its role, the other role
// filled by the source field other. It returns the operators whose
// draft ran with a fitted model.
func runInRequest(t *testing.T, p *pulse.Pulse, intent, cohort, other string, roles map[string]string) map[string]bool {
	t.Helper()
	ctx := context.Background()
	res, err := p.Recommend(ctx, descriptor.RecommendRequest{Intent: intent, Cohort: &types.Cohort{Filename: cohort}, Limit: 1000})
	if err != nil {
		t.Fatal(err)
	}
	ran := map[string]bool{}
	for _, r := range res.Recommendations {
		if wire, _ := json.Marshal(r); strings.Contains(string(wire), `"steps"`) {
			t.Errorf("%s: draft carries steps: %s", r.Operator, wire)
		}
		role, ok := roles[r.Operator]
		if !ok || ran[r.Operator] {
			continue
		}
		var v any
		if err := json.Unmarshal(r.Request, &v); err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(fillSteps(t, v))
		var req types.Request
		if err := json.Unmarshal(raw, &req); err != nil {
			t.Fatalf("%s: %v\n%s", r.Operator, err, raw)
		}
		const label = "derived"
		switch {
		case len(req.Attributes) == 1 && len(req.Features) == 0:
			req.Attributes[0].Label = label
		case len(req.Features) == 1 && len(req.Attributes) == 0:
			req.Features[0].Label = label
		default:
			t.Fatalf("%s: draft has no single derived entry: %s", r.Operator, raw)
		}
		model := &types.RegressionSpec{Type: "REG_OLS", Target: other, Predictors: []string{label}}
		if role == "target" {
			model.Target, model.Predictors = label, []string{other}
		}
		req.Regressions = append(req.Regressions, model)
		out, err := p.Process(ctx, &req)
		if err != nil {
			t.Fatalf("%s: draft + model: %v\n%s", r.Operator, err, raw)
		}
		if len(out.Regressions) != 1 || out.Regressions[0].Error != nil {
			t.Errorf("%s: model regression %+v", r.Operator, out.Regressions)
			continue
		}
		ran[r.Operator] = true
	}
	return ran
}

// TestRecommend_FeatureDraftsNeedNoPlan: bound over a cohort with a
// skewed positive field and a category, the feature producers — FEAT_LOG
// and FEAT_SQRT (distribution_shape), FEAT_FREQUENCY_ENCODE
// (composition) — carry no materialise plan, and each draft runs with a
// REG_OLS in the same request reading the feature column as a
// predictor.
func TestRecommend_FeatureDraftsNeedNoPlan(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs()})
	if err != nil {
		t.Fatal(err)
	}
	region := encoding.NewDictionary()
	for _, l := range []string{"north", "south", "east"} {
		if _, err := region.Add(l); err != nil {
			t.Fatal(err)
		}
	}
	schema := encoding.Schema{Fields: []encoding.Field{
		{Name: "spend", Type: encoding.FieldTypeF64, Description: "Money the household spent last year"},
		{Name: "region", Type: encoding.FieldTypeCategoricalU8, Description: "Region the household lives in", Dictionary: region},
	}}
	b, err := p.NewCohortBuilder(ctx, "households.pulse", schema, pulse.CohortBuilderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	levels := []string{"north", "south", "south", "east", "east", "east"}
	for r := range 48 {
		if err := b.Append(pulse.CohortRow{float64(1 + (r*r)%37), levels[r%len(levels)]}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.Close(); err != nil {
		t.Fatal(err)
	}
	roles := map[string]string{"FEAT_LOG": "predictors", "FEAT_SQRT": "predictors", "FEAT_FREQUENCY_ENCODE": "predictors"}
	ran := runInRequest(t, p, "distribution_shape", "households.pulse", "spend", roles)
	for op, ok := range runInRequest(t, p, "composition", "households.pulse", "spend", roles) {
		ran[op] = ok
	}
	for _, op := range []string{"FEAT_LOG", "FEAT_SQRT", "FEAT_FREQUENCY_ENCODE"} {
		if !ran[op] {
			t.Errorf("%s: no draft ran with its model", op)
		}
	}
}
