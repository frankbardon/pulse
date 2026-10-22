package pulse_test

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
	res, err := p.Recommend(ctx, descriptor.RecommendRequest{Intent: "measure_construct", Cohort: &types.Cohort{Filename: "battery.pulse"}, Limit: 1000})
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
		label := mat.Request.Attributes[0].Label
		if len(got.DerivedColumns) != 1 || got.DerivedColumns[0] != label {
			t.Errorf("%s: derived columns %v, want [%s]", r.Operator, got.DerivedColumns, label)
		}
		if err := json.Unmarshal(r.Steps[1].Request, &v); err != nil {
			t.Fatal(err)
		}
		raw, _ = json.Marshal(fillSteps(t, v))
		// The fixture has no y: the model reads a source item instead.
		raw = []byte(strings.ReplaceAll(string(raw), `"y"`, `"q1"`))
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
	for _, op := range []string{"ATTR_PC_SCORE", "ATTR_FACTOR_SCORE", "ATTR_SCALE_SCORE"} {
		if !ran[op] {
			t.Errorf("%s: no plan ran", op)
		}
	}
}
