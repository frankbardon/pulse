package descriptor_test

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/jsonfinite"
	"github.com/frankbardon/pulse/types"
)

// explainFamilies are the result families the Explain goldens pin, each
// read with and without its request companion. "request" is request
// mode (a cohort-bound request described before it runs), which has no
// companion axis.
var explainFamilies = []string{"test", "regression", "overlay", "matrix", "descriptive", "compose", "facet", "chain"}

func fp(v float64) *float64 { return &v }

func cellP(rows ...[]any) *types.MatrixPayload {
	out := &types.MatrixPayload{}
	for _, r := range rows {
		var line []types.MatrixCell
		for _, v := range r {
			if v == nil {
				line = append(line, types.MatrixCell{Present: false})
				continue
			}
			line = append(line, types.MatrixCell{Value: v, Present: true})
		}
		out.Cells = append(out.Cells, line)
	}
	return out
}

func gtTest(p, alpha, d float64) *types.TestResult {
	return &types.TestResult{
		Label: "score by plan", Type: types.TEST_T, Statistic: 2.1, DF: 40, PValue: p, Alpha: alpha, RejectNull: p < alpha,
		Details: map[string]any{"effect_size": map[string]any{"cohens_d": d}},
	}
}

func goldenRun(total, kept int64) *types.ResponseComponents {
	return &types.ResponseComponents{Run: &types.RunComponents{TotalRecords: total, FilteredRecords: kept}}
}

// explainFixture returns the family's result root and its request
// companion, as an ExplainRequest with both slots set.
func explainFixture(family string) descriptor.ExplainRequest {
	scoreByPlan := &types.Test{Type: types.TEST_T, Field: "score", SplitBy: "plan"}
	switch family {
	case "test":
		return descriptor.ExplainRequest{
			Response: &types.Response{
				Tests: []*types.TestResult{
					gtTest(0.004, 0.05, 0.62),
					{Label: "region by plan", Type: types.TEST_CHISQ, Statistic: 2.3, DF: 3, PValue: 0.51, Alpha: 0.05,
						Details: map[string]any{"effect_size": map[string]any{"cramers_v": 0.08}}},
					// Undefined: NaN statistic and p-value (null on the wire).
					{Label: "seats by plan", Type: types.TEST_T, Statistic: math.NaN(), PValue: math.NaN(), Alpha: 0.05},
				},
				Components: goldenRun(100, 96),
			},
			Request: &types.Request{
				Cohort: &types.Cohort{Filename: "accounts.pulse"},
				Tests: []*types.Test{scoreByPlan, {Type: types.TEST_CHISQ, Field: "region", SplitBy: "plan"},
					{Type: types.TEST_T, Field: "seats", SplitBy: "plan"}},
			},
		}
	case "regression":
		return descriptor.ExplainRequest{
			Response: &types.Response{
				Regressions: []*types.RegressionResult{{
					Name: "revenue model", Type: types.REG_OLS, R2: 0.31, AdjR2: 0.29, NObs: 96,
					Coefficients: map[string]float64{"(intercept)": 12, "seats": 0.8, "score": 0.05},
					StdErrors:    map[string]float64{"(intercept)": 2, "seats": 0.1, "score": math.NaN()},
					PValues:      map[string]float64{"(intercept)": 0.001, "seats": 0.0001, "score": math.NaN()},
				}},
				Components: goldenRun(100, 96),
			},
			Request: &types.Request{
				Cohort:      &types.Cohort{Filename: "accounts.pulse"},
				Regressions: []*types.RegressionSpec{{Type: types.REG_OLS, Name: "revenue model", Target: "revenue", Predictors: []string{"seats", "score"}}},
			},
		}
	case "overlay":
		return descriptor.ExplainRequest{
			Response: &types.Response{
				Overlays: []types.OverlayLayer{
					{Name: "plan cells", Kind: types.OverlayKind("OVERLAY_T_CELL"), Payload: types.OverlayPayload{
						Shape: types.OverlayShapeMatrix, Matrix: cellP([]any{0.01, 0.3}, []any{math.NaN(), nil}),
					}},
					{Name: "share", Kind: types.OverlayKind("OVERLAY_ZSCORE_VS_TOTAL")},
				},
				Components: goldenRun(100, 96),
			},
			Request: &types.Request{
				Cohort: &types.Cohort{Filename: "accounts.pulse"},
				Crosstab: &types.CrosstabSpec{
					Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
					Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "plan"}},
					Cell:    &types.Aggregation{Type: types.AGG_AVERAGE, Field: "score"},
				},
				Overlays: []types.OverlaySpec{{Name: "plan cells", Kind: types.OverlayKind("OVERLAY_T_CELL")}, {Name: "share", Kind: types.OverlayKind("OVERLAY_ZSCORE_VS_TOTAL")}},
			},
		}
	case "matrix":
		return descriptor.ExplainRequest{
			Response: &types.Response{
				Matrices: []types.MatrixResult{{Name: "corr", Type: types.MAT_CORRELATION, Primary: &types.MatrixValues{
					RowKeys: []string{"revenue", "seats", "score"}, ColumnKeys: []string{"revenue", "seats", "score"},
					Values: [][]float64{{1, 0.62, math.NaN()}, {0.62, 1, 0.1}, {math.NaN(), 0.1, 1}},
				}}},
				Components: goldenRun(100, 96),
			},
			Request: &types.Request{
				Cohort:   &types.Cohort{Filename: "accounts.pulse"},
				Matrices: []types.MatrixSpec{{Name: "corr", Type: types.MAT_CORRELATION, Fields: []string{"revenue", "seats", "score"}}},
			},
		}
	case "descriptive":
		return descriptor.ExplainRequest{
			Response: &types.Response{
				Data: []map[string]any{{"region": "north", "total": 1200.5, "avg_score": 7.1}, {"region": "south", "total": 900.0, "avg_score": nil}},
				Components: &types.ResponseComponents{
					Aggregations: []types.AggregationComponents{{Label: "total", N: 90, NNull: 4}, {Label: "avg_score", N: 88, NNull: 6}},
					Groupers:     []types.GrouperComponents{{TotalN: 96}},
					Run:          &types.RunComponents{TotalRecords: 100, FilteredRecords: 96},
				},
			},
			Request: &types.Request{
				Cohort: &types.Cohort{Filename: "accounts.pulse"},
				Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
				Aggregations: []*types.Aggregation{
					{Type: types.AGG_SUM, Field: "revenue", Label: "total"},
					{Type: types.AGG_AVERAGE, Field: "score", Label: "avg_score"},
				},
			},
		}
	case "compose":
		layer := types.OverlayLayer{Name: "plan cells", Kind: types.OverlayKind("OVERLAY_T_CELL"),
			Multiplicity: &types.AppliedMultiplicity{Method: types.MultiplicityMethodHolm, Family: types.MultiplicityFamilyCompose, Alpha: 0.05, M: 3},
			Payload: types.OverlayPayload{Shape: types.OverlayShapeMatrix, Matrix: cellP([]any{0.01, 0.3}, []any{0.2, nil}),
				SignificantAdjusted: cellP([]any{true, false}, []any{false, nil})}}
		return descriptor.ExplainRequest{
			ComposedResponse: &types.ComposedResponse{
				Responses: []*types.Response{
					{Tests: []*types.TestResult{gtTest(0.3, 0.05, 0.1)}, Components: goldenRun(100, 96)},
					{Tests: []*types.TestResult{gtTest(0.01, 0.05, 0.6)}, Data: []map[string]any{{"total": 12.0}},
						Components: &types.ResponseComponents{
							Aggregations: []types.AggregationComponents{{Label: "total", N: 9}},
							Run:          &types.RunComponents{TotalRecords: 10, FilteredRecords: 9},
						}},
				},
				Overlays: []types.OverlayLayer{layer},
			},
			Composed: &types.ComposedRequest{Requests: []*types.Request{
				{Cohort: &types.Cohort{Filename: "accounts.pulse"}, Tests: []*types.Test{scoreByPlan}},
				{Cohort: &types.Cohort{Filename: "trials.pulse"}, Tests: []*types.Test{scoreByPlan},
					Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "revenue", Label: "total"}}},
			}},
		}
	case "facet":
		params := func(field string) json.RawMessage {
			raw, _ := json.Marshal(map[string]string{"field": field})
			return raw
		}
		return descriptor.ExplainRequest{
			FacetResult: &types.FacetResult{
				Fields: map[string]*types.FacetField{
					"region": {Kind: "discrete", TypeName: "categorical_u8", NullCount: 3, Discrete: &types.FacetDiscrete{
						Values: []types.FacetValueCount{{Value: "north", Count: 40}, {Value: "south", Count: 30}}, DistinctCount: 4, TruncatedAt: 2}},
					"score": {Kind: "numeric", TypeName: "f64", NullCount: 1, Numeric: &types.FacetNumeric{
						Min: 1, Max: 99, Mean: 50.2, StdDev: math.NaN(), Count: 89, Sum: 4467.8, Percentiles: map[string]float64{"p50": 51}}},
				},
				TotalRecords: 100, FilteredRecords: 90,
				Overlays: []types.OverlayLayer{
					{Name: "score fit", Kind: types.OverlayKind("OVERLAY_KS_VS_POP"), Summary: &types.OverlaySummary{Statistic: fp(0.2), PValue: fp(0.004)}},
					{Name: "region fit", Kind: types.OverlayKind("OVERLAY_CHISQ_VS_POP"), Summary: &types.OverlaySummary{Statistic: fp(3.1), PValue: fp(0.4)}},
				},
			},
			Facet: &types.FacetRequest{
				Cohort: &types.Cohort{Filename: "accounts.pulse"},
				Fields: []string{"region", "score"},
				Overlays: []types.OverlaySpec{
					{Kind: types.OverlayKind("OVERLAY_KS_VS_POP"), Params: params("score")},
					{Kind: types.OverlayKind("OVERLAY_CHISQ_VS_POP"), Params: params("region")},
				},
			},
		}
	case "chain":
		stage := func(label string, n int) *types.Response {
			return &types.Response{
				Data: []map[string]any{{"region": "north", label: 1.0}, {"region": "south", label: 2.0}},
				Components: &types.ResponseComponents{
					Aggregations: []types.AggregationComponents{{Label: label, N: n}},
					Groupers:     []types.GrouperComponents{{TotalN: n}},
					Run:          &types.RunComponents{TotalRecords: 100, FilteredRecords: int64(n)},
				},
			}
		}
		last := stage("avg", 2)
		return descriptor.ExplainRequest{
			ChainResponse: &types.ChainResponse{
				Stages:   []*types.Response{stage("total", 90), last},
				Final:    last,
				Overlays: []*types.OverlayLayer{{Name: "growth", Kind: types.OverlayKind("OVERLAY_INDEX_VS_STAGE")}},
			},
			Chain: &types.ChainRequest{
				Cohort: &types.Cohort{Filename: "accounts.pulse"},
				Stages: []*types.ChainStage{
					{Name: "by_region", Request: &types.Request{
						Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
						Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "revenue", Label: "total"}}}},
					{Request: &types.Request{
						Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
						Aggregations: []*types.Aggregation{{Type: types.AGG_AVERAGE, Field: "total", Label: "avg"}}}},
				},
			},
		}
	}
	panic("unknown explain family " + family)
}

// wireRoundTrip sends req through JSON the way pulse explain and
// pulse_explain receive it: results written by the wire rule (an
// undefined figure is null) and read back through jsonfinite, request
// roots through plain encoding/json.
func wireRoundTrip(t *testing.T, req descriptor.ExplainRequest) descriptor.ExplainRequest {
	t.Helper()
	trip := func(src, dst any, result bool) {
		raw, err := json.Marshal(src)
		if err != nil {
			t.Fatal(err)
		}
		if result {
			err = jsonfinite.Unmarshal(raw, dst)
		} else {
			err = json.Unmarshal(raw, dst)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	out := descriptor.ExplainRequest{Detail: req.Detail}
	if req.Request != nil {
		trip(req.Request, &out.Request, false)
	}
	if req.Composed != nil {
		trip(req.Composed, &out.Composed, false)
	}
	if req.Chain != nil {
		trip(req.Chain, &out.Chain, false)
	}
	if req.Facet != nil {
		trip(req.Facet, &out.Facet, false)
	}
	if req.Response != nil {
		trip(req.Response, &out.Response, true)
	}
	if req.ComposedResponse != nil {
		trip(req.ComposedResponse, &out.ComposedResponse, true)
	}
	if req.ChainResponse != nil {
		trip(req.ChainResponse, &out.ChainResponse, true)
	}
	if req.FacetResult != nil {
		trip(req.FacetResult, &out.FacetResult, true)
	}
	return out
}

// explainCase is one golden: its file name and the request it pins.
type explainCase struct {
	name string
	req  descriptor.ExplainRequest
}

// explainCases lists every Explain golden: each result family x
// {terse, full} x {with, without its request companion}, plus request
// mode over the recommend fixture cohort x {terse, full}.
func explainCases(t *testing.T) []explainCase {
	t.Helper()
	var out []explainCase
	for _, d := range []descriptor.ExplainDetail{descriptor.ExplainTerse, descriptor.ExplainFull} {
		for _, fam := range explainFamilies {
			for _, with := range []bool{true, false} {
				req := wireRoundTrip(t, explainFixture(fam))
				req.Detail = d
				suffix := "with_request"
				if !with {
					req.Request, req.Composed, req.Chain, req.Facet = nil, nil, nil, nil
					suffix = "without_request"
				}
				out = append(out, explainCase{"explain." + fam + "." + string(d) + "." + suffix + ".json", req})
			}
		}
		out = append(out, explainCase{"explain.request." + string(d) + ".json", descriptor.ExplainRequest{
			Detail: d,
			Request: &types.Request{
				Cohort:       &types.Cohort{Filename: recommendGoldenCohort},
				Groups:       []*types.Group{{Field: "region"}},
				Aggregations: []*types.Aggregation{{Field: "revenue"}, {Type: types.AGG_AVERAGE, Field: "score"}},
				Tests:        []*types.Test{{Type: types.TEST_T, Field: "score", SplitBy: "region"}},
			},
		}})
	}
	return out
}

// explainJSON is a result as the --json envelope carries it.
func explainJSON(t *testing.T, res *descriptor.ExplainResult) []byte {
	t.Helper()
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(descriptor.NewEnvelope(res)); err != nil {
		t.Fatal(err)
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
}

func runExplain(t *testing.T, p *pulse.Pulse, req descriptor.ExplainRequest) *descriptor.ExplainResult {
	t.Helper()
	res, err := p.Explain(context.Background(), req)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	return res
}

// TestExplainGolden pins Explain per result family x detail x request
// companion (and request mode per detail) as the --json envelope
// carries it. Regenerate with go test ./descriptor/ -run 'Test.*Golden'
// -update.
func TestExplainGolden(t *testing.T) {
	p := recommendGoldenPulse(t)
	for _, c := range explainCases(t) {
		t.Run(c.name, func(t *testing.T) {
			compareGolden(t, c.name, explainJSON(t, runExplain(t, p, c.req)))
		})
	}
}

// TestExplainGolden_NullFigureStaysUndefined: an undefined statistic,
// p-value and coefficient p, written null by the wire and read back
// through the CLI / MCP decode, are reported undefined (null numbers,
// not_computable) — never as a real 0.
func TestExplainGolden_NullFigureStaysUndefined(t *testing.T) {
	p := recommendGoldenPulse(t)
	res := runExplain(t, p, wireRoundTrip(t, explainFixture("test")))
	f := res.Findings[2]
	if f.Verdict != descriptor.VerdictNotComputable || f.Numbers["statistic"] != nil || f.Numbers["p_value"] != nil {
		t.Errorf("undefined test read as %+v (statistic %v)", f, f.Numbers["statistic"])
	}
	res = runExplain(t, p, wireRoundTrip(t, explainFixture("regression")))
	for _, f := range res.Findings {
		if f.Slot == "regressions[0].coefficients.score" {
			if f.Verdict != descriptor.VerdictNotComputable || f.Numbers["p_value"] != nil {
				t.Errorf("undefined coefficient read as %+v", f)
			}
			return
		}
	}
	t.Error("no finding for the score coefficient")
}

// explainProse is every template-built prose string in an Explain
// result: the summary, step texts, follow-up conditions, narrative
// sentences and caveats. Refusals and advisory messages are verbatim
// engine text, not Explain templates, and are not linted here.
func explainProse(res *descriptor.ExplainResult) []string {
	out := []string{res.Summary}
	for _, s := range res.Steps {
		out = append(out, s.Text)
	}
	for _, a := range res.FollowUps {
		out = append(out, a.When)
	}
	out = append(out, res.Sentences...)
	return append(out, res.Caveats...)
}

// lintExplain reports every LintGuidanceText hit in res's prose.
func lintExplain(t *testing.T, where string, res *descriptor.ExplainResult) int {
	t.Helper()
	texts := explainProse(res)
	for _, s := range texts {
		for _, h := range descx.LintGuidanceText(s) {
			t.Errorf("%s: lint %s on %q in %q", where, h.Rule, h.Span, s)
		}
	}
	return len(texts)
}

// TestExplainProseLint is the binding prose-lint gate over Explain: the
// text of every Explain golden passes LintGuidanceText (ASA-NODIFF,
// ASA-PNULL, ASA-PROOF, ASA-IMPORTANT, …), both as generated now — so a
// non-compliant template fails here before any golden moves — and as
// stored on disk. Every case must have its golden.
func TestExplainProseLint(t *testing.T) {
	p := recommendGoldenPulse(t)
	cases := explainCases(t)
	want := map[string]bool{}
	n := 0
	for _, c := range cases {
		want[c.name] = true
		n += lintExplain(t, c.name+" (generated)", runExplain(t, p, c.req))
	}
	stored, _ := filepath.Glob(filepath.Join("testdata", "explain.*.json"))
	for _, path := range stored {
		name := filepath.Base(path)
		if !want[name] {
			t.Errorf("%s: no Explain case generates this golden", name)
			continue
		}
		delete(want, name)
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := splitHash(content)
		var env struct {
			Data descriptor.ExplainResult `json:"data"`
		}
		if err := json.Unmarshal(data, &env); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		n += lintExplain(t, name, &env.Data)
	}
	for name := range want {
		t.Errorf("%s: golden missing (regenerate with -update)", name)
	}
	if len(cases) != len(explainFamilies)*4+2 || n < 10*len(cases) {
		t.Fatalf("vacuous: %d cases, %d texts linted", len(cases), n)
	}
}

// TestExplainProseLint_Falsifier: the lint the gate runs bites on the
// overstatements an Explain template could make.
func TestExplainProseLint_Falsifier(t *testing.T) {
	for _, bad := range []string{
		"Test 1 (TEST_T) shows no difference between the groups.",
		"The p-value is the probability that the null hypothesis is true.",
		"The result proves the effect.",
	} {
		res := &descriptor.ExplainResult{Summary: "ok.", Caveats: []string{bad}}
		hits := 0
		for _, s := range explainProse(res) {
			hits += len(descx.LintGuidanceText(s))
		}
		if hits == 0 {
			t.Errorf("lint passes %q", bad)
		}
	}
	if strings.Contains(strings.Join(explainProse(&descriptor.ExplainResult{Refusals: []descriptor.EnvelopeEntry{{Message: "x"}}}), ""), "x") {
		t.Error("refusal messages are engine text, not Explain prose")
	}
}
