package regordinal_test

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/regordinal"
	"github.com/frankbardon/pulse/types"
)

// score_e2e_test.go scores a bound REG_ORDINAL fit end to end (U68 #650):
// a Compose `fits` entry fits the golden case, one slot scores every
// row through ATTR_REG_PREDICT — the expected level (default), the
// class, the linear predictor and, through ONE for_each item each, a
// P(y = level) and a P(y ≤ level) column per level — and the per-row
// figures match R's predict(polr, type = "probs") (ordinal.json, see
// oracle_test.go) at e2eRel; the fit saved and reloaded through
// `models` scores the same JSON; the scoring rule's refusals are
// identical at predict and at run.

// scoreSlotJSON is the per-row scoring slot over levels: one group per
// row (id), every column read back by AGG_MIN.
func scoreSlotJSON(cohort string, levels []string, models string) string {
	lv, _ := json.Marshal(levels)
	fe := `"for_each":{"axes":[{"name":"lv","values":` + string(lv) + `}]}`
	m := ""
	if models != "" {
		m = `"models":[{"name":"ord","path":"` + models + `"}],`
	}
	return `{"label":"score","cohort":{"filename":"` + cohort + `"},` + m + `
		"groups":[{"type":"GROUP_RANGE","field":"id","interval":1}],
		"attributes":[
			{"type":"ATTR_REG_PREDICT","label":"expected","params":{"fit":"ord"}},
			{"type":"ATTR_REG_PREDICT","label":"class","params":{"fit":"ord","output":"class"}},
			{"type":"ATTR_REG_PREDICT","label":"eta","params":{"fit":"ord","scale":"link"}},
			{` + fe + `,"type":"ATTR_REG_PREDICT","label":"p_{{lv}}","params":{"fit":"ord","level":{"$var":"lv"}}},
			{` + fe + `,"type":"ATTR_REG_PREDICT","label":"c_{{lv}}","params":{"fit":"ord","level":{"$var":"lv"},"output":"cumulative"}}
		],
		"aggregations":[
			{"type":"AGG_MIN","field":"expected","label":"expected"},
			{"type":"AGG_MIN","field":"class","label":"class"},
			{"type":"AGG_MIN","field":"eta","label":"eta"},
			{` + fe + `,"type":"AGG_MIN","field":"p_{{lv}}","label":"p_{{lv}}"},
			{` + fe + `,"type":"AGG_MIN","field":"c_{{lv}}","label":"c_{{lv}}"}
		]}`
}

func scoreSlot(t testing.TB, cohort string, levels []string, models string) *pulse.Request {
	t.Helper()
	var r pulse.Request
	if err := json.Unmarshal([]byte(scoreSlotJSON(cohort, levels, models)), &r); err != nil {
		t.Fatal(err)
	}
	return &r
}

// ordinalFitRequest is the Compose fit request of case c over cohort.
func ordinalFitRequest(c regordinal.OracleCase, cohort string) *pulse.Request {
	s := caseSpec(c, c.LevelsParam)
	s.Vcov = false
	return &pulse.Request{Cohort: &types.Cohort{Filename: cohort}, Regressions: []*types.RegressionSpec{s}}
}

// scoreCase writes case c's cohort (an id per row) on a fresh instance.
func scoreCase(t *testing.T, c regordinal.OracleCase) (*pulse.Pulse, [][]float64) {
	t.Helper()
	p := newInstance(t, nil)
	cs := cohortSpec{p: len(c.Predictors), id: true}
	var labels []string
	if c.Levels[0] != "1" {
		cs.dict, labels = c.Levels, c.Levels
	}
	rows := slices.Clone(c.Rows)
	if c.LevelsParam != nil {
		// Rows at a value params.levels does not list: dropped by the
		// fit, yet scored (the scorer reads predictors only).
		for i := 0; i < 25; i++ {
			extra := slices.Clone(rows[i])
			extra[cs.p] = 0
			rows = append(rows, extra)
		}
	}
	writeCohort(t, p, "ord.pulse", cs, rows, labels)
	return p, rows
}

func TestRegOrdinal_ScoresMatchPolrEndToEnd(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	covered := map[string]bool{}
	for _, c := range regordinal.LoadOracle(t) {
		if c.RowProbabilities == nil {
			continue
		}
		covered[c.Link+"/"+c.Weight] = true
		t.Run(c.Name, func(t *testing.T) {
			t.Parallel()
			p, rows := scoreCase(t, c)
			save := "models/ord.pulse-model.json"
			out, err := p.Compose(ctx, &pulse.ComposedRequest{
				Fits:     []types.FitSpec{{Name: "ord", Request: ordinalFitRequest(c, "ord.pulse"), Save: save}},
				Requests: []*pulse.Request{scoreSlot(t, "ord.pulse", c.Levels, "")},
			})
			if err != nil {
				t.Fatalf("Compose: %v", err)
			}
			if fit := out.Fits["ord"]; fit == nil || fit.RegressionResult == nil || fit.RegressionResult.Error != nil {
				t.Fatalf("fit = %s", mustJSON(t, out.Fits))
			}
			data := out.Responses[0].Data
			if len(data) != len(rows) {
				t.Fatalf("%d rows, want %d", len(data), len(rows))
			}
			// The for_each items fan out to one column per level.
			for _, lv := range c.Levels {
				for _, col := range []string{"p_" + lv, "c_" + lv} {
					if _, ok := data[0][col]; !ok {
						t.Fatalf("no column %s in %v", col, data[0])
					}
				}
			}
			check := func(what string, id int, got any, want float64) {
				t.Helper()
				g, ok := got.(float64)
				if !ok || rel(g, want) > e2eRel {
					t.Errorf("row %d %s = %v, R %.17g", id, what, got, want)
				}
			}
			for _, row := range data {
				// GROUP_RANGE labels row i "i-(i+1)".
				lo, _, _ := strings.Cut(fmt.Sprint(row["id"]), "-")
				id, err := strconv.Atoi(lo)
				if err != nil {
					t.Fatalf("row id %v: %v", row["id"], err)
				}
				if id >= len(c.Rows) {
					continue // an unlisted-level row: no golden figure
				}
				want := c.RowProbabilities[id]
				check("eta", id, row["eta"], c.RowEta[id])
				expected, cum, best := 0.0, 0.0, 0
				for k, lv := range c.Levels {
					check("P(y = "+lv+")", id, row["p_"+lv], want[k])
					cum += want[k]
					check("P(y <= "+lv+")", id, row["c_"+lv], cum)
					expected += float64(k+1) * want[k]
					if want[k] > want[best] {
						best = k
					}
				}
				check("expected", id, row["expected"], expected)
				if row["class"] != float64(best+1) {
					t.Errorf("row %d class %v, R %d", id, row["class"], best+1)
				}
			}
			// Saved, then reloaded through `models`: the same scores.
			loaded, err := p.Process(ctx, scoreSlot(t, "ord.pulse", c.Levels, save))
			if err != nil {
				t.Fatalf("models reload: %v", err)
			}
			if a, b := mustJSON(t, loaded.Data), mustJSON(t, data); a != b {
				t.Errorf("the reloaded model scores differently:\n%s\n%s", a, b)
			}
		})
	}
	for _, want := range []string{"logit/", "probit/", "cloglog/", "logit/frequency", "logit/probability"} {
		if !covered[want] {
			t.Errorf("no scored golden case for %s", want)
		}
	}
}

// TestRegOrdinal_ScoreRefusalsPredictMatchRuntime: params.level /
// category / output on a fit that does not take them, an unknown level
// (pinned params.levels, or a loaded model file), an outcome-reading
// output without a level and the other outcome-rule faults are refused
// by PredictCompose and by Compose with the same code, message and
// details.
func TestRegOrdinal_ScoreRefusalsPredictMatchRuntime(t *testing.T) {
	t.Parallel()
	c := goldenCase(t, "logit_two")
	p, _ := scoreCase(t, c)
	ctx := context.Background()
	pinned := c
	pinned.LevelsParam = []string{"1", "2", "3"}
	ols := &pulse.Request{Cohort: &types.Cohort{Filename: "ord.pulse"},
		Regressions: []*types.RegressionSpec{{Type: types.REG_OLS, Name: "ord", Target: "x1", Predictors: []string{"x2"}}}}
	// A saved model file for the `models` route.
	save := "models/refusal.pulse-model.json"
	if _, err := p.Compose(ctx, &pulse.ComposedRequest{
		Fits:     []types.FitSpec{{Name: "ord", Request: ordinalFitRequest(c, "ord.pulse"), Save: save}},
		Requests: []*pulse.Request{{Label: "n", Cohort: &types.Cohort{Filename: "ord.pulse"}, Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "id", Label: "n"}}}},
	}); err != nil {
		t.Fatal(err)
	}
	slot := func(params string, models bool) *pulse.Request {
		r := &pulse.Request{Label: "s", Cohort: &types.Cohort{Filename: "ord.pulse"},
			Attributes:   []*types.Attribute{{Type: types.ATTR_REG_PREDICT, Label: "s", Params: json.RawMessage(params)}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_AVERAGE, Field: "s", Label: "s"}}}
		if models {
			r.Models = []types.ModelRef{{Name: "ord", Path: save}}
		}
		return r
	}
	cases := []struct {
		name   string
		fit    *pulse.Request // nil: the `models` route
		params string
		reason string
	}{
		{"level on OLS", ols, `{"fit":"ord","level":"1"}`, "outcome_unsupported"},
		{"output on OLS", ols, `{"fit":"ord","output":"class"}`, "outcome_unsupported"},
		{"category on OLS", ols, `{"fit":"ord","category":"1"}`, "outcome_unsupported"},
		{"category on ordinal", ordinalFitRequest(c, "ord.pulse"), `{"fit":"ord","category":"1"}`, "outcome_unsupported"},
		{"probability without level", ordinalFitRequest(c, "ord.pulse"), `{"fit":"ord","output":"probability"}`, "outcome_required"},
		{"cumulative without level", ordinalFitRequest(c, "ord.pulse"), `{"fit":"ord","output":"cumulative"}`, "outcome_required"},
		{"level beside class", ordinalFitRequest(c, "ord.pulse"), `{"fit":"ord","level":"1","output":"class"}`, "outcome_unused"},
		{"unknown output", ordinalFitRequest(c, "ord.pulse"), `{"fit":"ord","output":"mode"}`, "output_unknown"},
		{"output beside link", ordinalFitRequest(c, "ord.pulse"), `{"fit":"ord","scale":"link","output":"expected"}`, "output_scale"},
		{"unknown pinned level", ordinalFitRequest(pinned, "ord.pulse"), `{"fit":"ord","level":"4"}`, "outcome_unknown"},
		{"unknown level of a loaded model", nil, `{"fit":"ord","level":"4"}`, "outcome_unknown"},
		{"terms on ordinal", ordinalFitRequest(c, "ord.pulse"), `{"fit":"ord","terms":["x1"]}`, "terms_unsupported"},
		{"bound on ordinal", ordinalFitRequest(c, "ord.pulse"), `{"fit":"ord","bound":"lower"}`, "bound_unsupported"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			creq := &pulse.ComposedRequest{Requests: []*pulse.Request{slot(tc.params, tc.fit == nil)}}
			if tc.fit != nil {
				creq.Fits = []types.FitSpec{{Name: "ord", Request: tc.fit}}
			}
			env, err := p.PredictCompose(ctx, creq)
			if err != nil {
				t.Fatal(err)
			}
			if len(env.Errors) != 1 {
				t.Fatalf("predict errors = %+v, want exactly one", env.Errors)
			}
			pe := env.Errors[0]
			if pe.Code != string(perr.PULSE_MODEL_SCORE_INVALID) || pe.Details["reason"] != tc.reason {
				t.Fatalf("predict = %s %v, want %s", pe.Code, pe.Details, tc.reason)
			}
			_, rerr := p.Compose(ctx, creq)
			var ce *perr.CodedError
			if !stderrors.As(rerr, &ce) {
				t.Fatalf("runtime: uncoded %v", rerr)
			}
			if string(ce.Code) != pe.Code || ce.Message != pe.Message || !reflect.DeepEqual(ce.Details, pe.Details) {
				t.Errorf("runtime %s %q %v\n predict %s %q %v", ce.Code, ce.Message, ce.Details, pe.Code, pe.Message, pe.Details)
			}
		})
	}
	// An unpinned level set is the data's: predict cannot judge a level
	// the fit may carry, and the run refuses the unknown one fatally
	// with the rule's own refusal.
	creq := &pulse.ComposedRequest{Fits: []types.FitSpec{{Name: "ord", Request: ordinalFitRequest(c, "ord.pulse")}},
		Requests: []*pulse.Request{slot(`{"fit":"ord","level":"4"}`, false)}}
	if env, err := p.PredictCompose(ctx, creq); err != nil || len(env.Errors) != 0 {
		t.Fatalf("predict over unpinned levels: %v %+v", err, env)
	}
	_, rerr := p.Compose(ctx, creq)
	var ce *perr.CodedError
	if !stderrors.As(rerr, &ce) || ce.Details["reason"] != "outcome_unknown" {
		t.Fatalf("runtime: %v, want outcome_unknown", rerr)
	}
	if got := fmt.Sprint(ce.Details["available"]); got != "[1 2 3]" {
		t.Errorf("available %s", got)
	}
}

// TestRegOrdinal_ScoreWorkerInvariant: the fitted-and-scored response
// is byte-identical across DecodeWorkers / ShardWorkers counts.
func TestRegOrdinal_ScoreWorkerInvariant(t *testing.T) {
	t.Parallel()
	c := goldenCase(t, "logit_four_labeled")
	ctx := context.Background()
	var first string
	for _, w := range []int{1, 3} {
		p := newInstance(t, func(o *pulse.Options) { o.DecodeWorkers, o.ShardWorkers = w, w })
		writeCohort(t, p, "ord.pulse", cohortSpec{p: 3, dict: c.Levels, id: true}, c.Rows, c.Levels)
		out, err := p.Compose(ctx, &pulse.ComposedRequest{
			Fits:     []types.FitSpec{{Name: "ord", Request: ordinalFitRequest(c, "ord.pulse")}},
			Requests: []*pulse.Request{scoreSlot(t, "ord.pulse", c.Levels, "")},
		})
		if err != nil {
			t.Fatal(err)
		}
		got := mustJSON(t, out)
		if first == "" {
			first = got
		} else if got != first {
			t.Errorf("workers %d: response differs", w)
		}
	}
}
