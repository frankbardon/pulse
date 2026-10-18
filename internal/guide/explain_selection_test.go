package guide

import (
	"slices"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/types"
)

// explain_selection_test.go pins how Explain reads a regression run
// under a selection (forward / backward / stepwise): the model is named
// by the predictors the selection KEPT, its sentence says how many of
// the candidates that is and which it dropped; a request step calls the
// predictors candidates; a ComposedResponse's regression fits are read
// the same way at "fits.<name>".

func selectedResult() *types.RegressionResult {
	return &types.RegressionResult{
		Name: "pd", Type: types.REG_GLM, NObs: 50, Selection: "stepwise", Criterion: "bic",
		SelectedFeatures: []string{"hours"},
		Coefficients:     map[string]float64{"(intercept)": 1, "hours": 0.8},
		StdErrors:        map[string]float64{"(intercept)": 0.2, "hours": 0.1},
		PValues:          map[string]float64{"(intercept)": 0.001, "hours": 0.0001},
	}
}

func selectedSpec() *types.RegressionSpec {
	return &types.RegressionSpec{Type: types.REG_GLM, Name: "pd", Target: "score", Predictors: []string{"hours", "age", "tenure"},
		Selection: "stepwise", Criterion: "bic"}
}

func TestExplainResponse_SelectionNamesKeptPredictors(t *testing.T) {
	req := &types.Request{Regressions: []*types.RegressionSpec{selectedSpec()}}
	res := readResponse(t, nil, &types.Response{Regressions: []*types.RegressionResult{selectedResult()}}, req, descriptor.ExplainFull)
	model, _ := findingAt(res, "regressions[0]")
	if model.Subject != "predicting `score` from `hours`" {
		t.Errorf("model subject = %q, want the kept predictor only", model.Subject)
	}
	if !hasSentence(res, "Stepwise selection on BIC kept 1 of 3 candidate predictors and dropped `age` and `tenure`.") {
		t.Errorf("sentences lack the selection: %v", res.Sentences)
	}
	coef, ok := findingAt(res, "regressions[0].coefficients.hours")
	if !ok || strings.Contains(coef.Subject, "age") {
		t.Errorf("coefficient finding = %+v", coef)
	}

	// Without the request only the kept predictors are known.
	bare := readResponse(t, nil, &types.Response{Regressions: []*types.RegressionResult{selectedResult()}}, nil, descriptor.ExplainFull)
	if !hasSentence(bare, "Stepwise selection on BIC kept `hours`.") {
		t.Errorf("bare sentences lack the kept predictor: %v", bare.Sentences)
	}

	// No selection: the request's predictors name the model, as before.
	plain := selectedResult()
	plain.Selection, plain.Criterion, plain.SelectedFeatures = "", "", nil
	pspec := selectedSpec()
	pspec.Selection, pspec.Criterion = "", ""
	pres := readResponse(t, nil, &types.Response{Regressions: []*types.RegressionResult{plain}}, &types.Request{Regressions: []*types.RegressionSpec{pspec}}, descriptor.ExplainFull)
	if m, _ := findingAt(pres, "regressions[0]"); m.Subject != "predicting `score` from `hours`, `age` and `tenure`" {
		t.Errorf("unselected subject = %q", m.Subject)
	}
	for _, s := range pres.Sentences {
		if strings.Contains(s, "selection") {
			t.Errorf("unselected fit names a selection: %q", s)
		}
	}
}

func TestExplainRequest_SelectionCallsPredictorsCandidates(t *testing.T) {
	res := explain(t, nil, descriptor.ExplainRequest{Request: &types.Request{Regressions: []*types.RegressionSpec{selectedSpec()}}}, Checks{})
	i := slices.IndexFunc(res.Steps, func(s descriptor.ExplainStep) bool { return s.Slot == "regressions[0]" })
	if i < 0 {
		t.Fatalf("no regression step: %+v", res.Steps)
	}
	want := "predicting `score`, choosing among the candidate predictors `hours`, `age` and `tenure` by stepwise selection on BIC"
	if !strings.Contains(res.Steps[i].Text, want) {
		t.Errorf("step = %q, want it to contain %q", res.Steps[i].Text, want)
	}
	for _, h := range descx.LintGuidanceText(res.Steps[i].Text) {
		t.Errorf("lint %s on %q", h.Rule, h.Span)
	}
}

func TestExplainComposedResponse_ReadsRegressionFits(t *testing.T) {
	resp := &types.ComposedResponse{
		Responses: []*types.Response{{Data: []map[string]any{{"n": 3.0}}}},
		Fits: map[string]*types.FitResult{
			"pd":  {RegressionResult: selectedResult()},
			"pop": {Vector: &types.VectorFitResult{}},
		},
	}
	req := &types.ComposedRequest{
		Fits:     []types.FitSpec{{Name: "pd", Request: &types.Request{Regressions: []*types.RegressionSpec{selectedSpec()}}}, {Name: "pop"}},
		Requests: []*types.Request{{}},
	}
	res := readResult(t, nil, descriptor.ExplainRequest{ComposedResponse: resp, Composed: req, Detail: descriptor.ExplainFull}, descriptor.ExplainRootComposedResponse)
	model, ok := findingAt(res, "fits.pd")
	if !ok || model.Subject != "predicting `score` from `hours`" || model.Operator != "REG_GLM" {
		t.Fatalf("fit finding = %+v (found %v)", model, ok)
	}
	if _, ok := findingAt(res, "fits.pd.coefficients.hours"); !ok {
		t.Errorf("no coefficient finding for the fit: %v", slots(res))
	}
	if _, ok := findingAt(res, "fits.pop"); ok {
		t.Errorf("a vector fit got a regression finding")
	}
	if !hasSentence(res, "Fit `pd` (REG_GLM) predicting `score` from `hours` is fitted on 50 rows. Stepwise selection on BIC kept 1 of 3 candidate predictors and dropped `age` and `tenure`.") {
		t.Errorf("sentences lack the fit: %v", res.Sentences)
	}
	if !strings.HasPrefix(res.Summary, "This batch reports 1 response side by side and 1 regression fit") {
		t.Errorf("summary = %q", res.Summary)
	}
	for _, s := range explainTexts(res) {
		for _, h := range descx.LintGuidanceText(s) {
			t.Errorf("lint %s on %q in %q", h.Rule, h.Span, s)
		}
	}
}
