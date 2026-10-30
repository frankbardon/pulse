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

// stepValues fills a materialise plan's placeholders: the derived
// cohort's path, the model's other role, and the producers' needs.
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

// TestRecommend_MaterializeStepsRun: bound over a battery cohort, every
// measure_construct draft carrying Steps runs as its plan says — the
// pulse_materialize step through Pulse.Materialize, writing the labelled
// column, then the pulse_process step's model over the written cohort,
// reading that column.
func TestRecommend_MaterializeStepsRun(t *testing.T) {
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
	ran := runPlans(t, p, "measure_construct", "battery.pulse", "q1")
	for _, op := range []string{"ATTR_PC_SCORE", "ATTR_FACTOR_SCORE", "ATTR_SCALE_SCORE"} {
		if !ran[op] {
			t.Errorf("%s: no plan ran", op)
		}
	}
}

// runPlans recommends intent bound to cohort and runs every plan a
// draft carries — the pulse_materialize step through Pulse.Materialize,
// which must write exactly the labelled column, then the pulse_process
// step's model over the written cohort reading that column, with its
// other role filled by the source field other. It returns the
// operators whose plan ran.
func runPlans(t *testing.T, p *pulse.Pulse, intent, cohort, other string) map[string]bool {
	t.Helper()
	ctx := context.Background()
	res, err := p.Recommend(ctx, descriptor.RecommendRequest{Intent: intent, Cohort: &types.Cohort{Filename: cohort}, Limit: 1000})
	if err != nil {
		t.Fatal(err)
	}
	ran := map[string]bool{}
	for _, r := range res.Recommendations {
		if len(r.Steps) == 0 {
			continue
		}
		var mat struct {
			Request types.Request `json:"request"`
			Out     string        `json:"out"`
		}
		var v any
		if err := json.Unmarshal(r.Steps[0].Request, &v); err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(fillSteps(t, v))
		if err := json.Unmarshal(raw, &mat); err != nil {
			t.Fatalf("%s: %v\n%s", r.Operator, err, raw)
		}
		got, err := p.Materialize(ctx, &mat.Request, mat.Out, pulse.MaterializeOptions{Overwrite: true})
		if err != nil {
			t.Fatalf("%s: materialize step: %v\n%s", r.Operator, err, raw)
		}
		label := derivedLabel(mat.Request)
		if len(got.DerivedColumns) != 1 || got.DerivedColumns[0] != label {
			t.Errorf("%s: derived columns %v, want [%s]", r.Operator, got.DerivedColumns, label)
		}
		if err := json.Unmarshal(r.Steps[1].Request, &v); err != nil {
			t.Fatal(err)
		}
		raw, _ = json.Marshal(fillSteps(t, v))
		// The fixture has no y: the model reads a source field instead.
		raw = []byte(strings.ReplaceAll(string(raw), `"y"`, `"`+other+`"`))
		var model types.Request
		if err := json.Unmarshal(raw, &model); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(raw), `"`+label+`"`) {
			t.Errorf("%s: model step does not read %s: %s", r.Operator, label, raw)
		}
		out, err := p.Process(ctx, &model)
		if err != nil {
			t.Fatalf("%s: model step: %v\n%s", r.Operator, err, raw)
		}
		if len(out.Regressions) != 1 || out.Regressions[0].Error != nil {
			t.Errorf("%s: model step regression %+v", r.Operator, out.Regressions)
		}
		ran[r.Operator] = true
	}
	return ran
}

// derivedLabel is the label of a materialize request's one derived
// entry: an attribute's or a feature's.
func derivedLabel(req types.Request) string {
	if len(req.Attributes) == 1 {
		return req.Attributes[0].Label
	}
	if len(req.Features) == 1 {
		return req.Features[0].Label
	}
	return ""
}

// TestRecommend_FeatureMaterializeStepsRun: bound over a cohort with a
// skewed positive field and a category, the feature producers' plans
// run end to end — FEAT_LOG and FEAT_SQRT (distribution_shape) and
// FEAT_FREQUENCY_ENCODE (composition): materialize writes the labelled
// feature column, then REG_OLS on the derived cohort reads it as a
// predictor.
func TestRecommend_FeatureMaterializeStepsRun(t *testing.T) {
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
	ran := runPlans(t, p, "distribution_shape", "households.pulse", "spend")
	for op, ok := range runPlans(t, p, "composition", "households.pulse", "spend") {
		ran[op] = ok
	}
	for _, op := range []string{"FEAT_LOG", "FEAT_SQRT", "FEAT_FREQUENCY_ENCODE"} {
		if !ran[op] {
			t.Errorf("%s: no plan ran", op)
		}
	}
}
