package guide

import (
	"encoding/json"
	stderrors "errors"
	"math"
	"slices"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/types"
)

func fptr(v float64) *float64 { return &v }
func bptr(v bool) *bool       { return &v }

// tTest is a two-sample TEST_T result at alpha with p and Cohen's d.
func tTest(p, alpha, d float64) *types.TestResult {
	return &types.TestResult{
		Label: "score by arm", Type: types.TEST_T, Statistic: 2.1, DF: 40, PValue: p, Alpha: alpha, RejectNull: p < alpha,
		Details: map[string]any{"effect_size": map[string]any{"cohens_d": d}},
	}
}

func readResponse(t *testing.T, inst *descx.InstanceSnapshot, resp *types.Response, req *types.Request, d descriptor.ExplainDetail) *descriptor.ExplainResult {
	t.Helper()
	res := explain(t, inst, descriptor.ExplainRequest{Response: resp, Request: req, Detail: d}, Checks{})
	if res.Mode != descriptor.ExplainModeResponse || res.Root != descriptor.ExplainRootResponse {
		t.Fatalf("mode/root = %s/%s", res.Mode, res.Root)
	}
	return res
}

func TestExplainResponse_Validates(t *testing.T) {
	resp := &types.Response{}
	for name, req := range map[string]descriptor.ExplainRequest{
		"response and composed": {Response: resp, Composed: &types.ComposedRequest{}},
		"response and sample":   {Response: resp, Sample: &types.SampleRequest{}},
		"response bad detail":   {Response: resp, Detail: "chatty"},
	} {
		_, err := Explain(nil, req, Checks{})
		var ce *errors.CodedError
		if !stderrors.As(err, &ce) || ce.Code != errors.SERVICE_VALIDATION {
			t.Errorf("%s: err = %v, want SERVICE_VALIDATION", name, err)
		}
	}
	if _, err := Explain(nil, descriptor.ExplainRequest{Response: resp, Request: &types.Request{}}, Checks{}); err != nil {
		t.Errorf("response with request companion: %v", err)
	}
}

// A non-significant result reads "no evidence of", never "no
// difference"; a significant one "evidence of".
func TestExplainResponse_NoEvidenceNeverNoDifference(t *testing.T) {
	res := readResponse(t, nil, &types.Response{Tests: []*types.TestResult{tTest(0.21, 0.05, 0.1)}}, nil, descriptor.ExplainFull)
	f := res.Findings[0]
	if f.Verdict != descriptor.VerdictNoEvidenceOfDifference || f.Operator != "TEST_T" || f.Subject != "score by arm" {
		t.Fatalf("finding = %+v", f)
	}
	joined := strings.Join(explainTexts(res), "\n")
	if !strings.Contains(joined, "no evidence of a difference") {
		t.Errorf("sentences lack the no-evidence reading: %s", joined)
	}
	if strings.Contains(strings.ToLower(joined), "no difference") {
		t.Errorf("sentences say no difference: %s", joined)
	}
	res = readResponse(t, nil, &types.Response{Tests: []*types.TestResult{tTest(0.001, 0.05, 0.1)}}, nil, descriptor.ExplainTerse)
	if v := res.Findings[0].Verdict; v != descriptor.VerdictEvidenceOfDifference {
		t.Errorf("significant verdict = %s", v)
	}
	if !strings.Contains(res.Summary, "1 of 1 inferential finding shows evidence") {
		t.Errorf("summary = %q", res.Summary)
	}
}

// Alpha comes from the result, never an assumed 0.05.
func TestExplainResponse_AlphaFromResult(t *testing.T) {
	at05 := readResponse(t, nil, &types.Response{Tests: []*types.TestResult{tTest(0.03, 0.05, 0.1)}}, nil, descriptor.ExplainFull)
	at01 := readResponse(t, nil, &types.Response{Tests: []*types.TestResult{tTest(0.03, 0.01, 0.1)}}, nil, descriptor.ExplainFull)
	if at05.Findings[0].Verdict != descriptor.VerdictEvidenceOfDifference || at01.Findings[0].Verdict != descriptor.VerdictNoEvidenceOfDifference {
		t.Fatalf("verdicts = %s / %s", at05.Findings[0].Verdict, at01.Findings[0].Verdict)
	}
	if got := *at01.Findings[0].Numbers["alpha"]; got != 0.01 {
		t.Errorf("alpha number = %v", got)
	}
	if !slices.ContainsFunc(at01.Sentences, func(s string) bool { return strings.Contains(s, "at alpha 0.01") }) {
		t.Errorf("sentences do not name alpha 0.01: %v", at01.Sentences)
	}
}

// An adjusted verdict reads significant_adjusted, which can differ
// from the raw one, and names the correction.
func TestExplainResponse_AdjustedVerdict(t *testing.T) {
	adj := tTest(0.03, 0.05, 0.1)
	adj.PAdjusted, adj.SignificantAdjusted = fptr(0.09), bptr(false)
	adj.Multiplicity = &types.AppliedMultiplicity{Method: types.MultiplicityMethodHolm, Family: types.MultiplicityFamilyRequest, Alpha: 0.05, M: 3}
	res := readResponse(t, nil, &types.Response{Tests: []*types.TestResult{adj}}, nil, descriptor.ExplainFull)
	f := res.Findings[0]
	if f.Verdict != descriptor.VerdictNoEvidenceOfDifference {
		t.Errorf("adjusted verdict = %s, want no evidence (raw p is below alpha)", f.Verdict)
	}
	want := descriptor.ExplainMultiplicity{Method: "holm", Family: "request", M: 3}
	if f.Multiplicity == nil || *f.Multiplicity != want {
		t.Errorf("multiplicity = %+v", f.Multiplicity)
	}
	if *f.Numbers["p_adjusted"] != 0.09 {
		t.Errorf("p_adjusted = %v", f.Numbers["p_adjusted"])
	}
	adj.SignificantAdjusted = bptr(true)
	if v := readResponse(t, nil, &types.Response{Tests: []*types.TestResult{adj}}, nil, descriptor.ExplainTerse).Findings[0].Verdict; v != descriptor.VerdictEvidenceOfDifference {
		t.Errorf("adjusted-significant verdict = %s", v)
	}
}

func manyTestsCaveat(res *descriptor.ExplainResult) bool {
	return slices.ContainsFunc(res.Caveats, func(c string) bool { return strings.Contains(c, "PULSE_ADVISORY_MANY_TESTS") })
}

// Two or more uncorrected p-values carry the many-tests caveat, in
// terse output too; corrected ones and a single test do not.
func TestExplainResponse_ManyTestsCaveat(t *testing.T) {
	two := &types.Response{Tests: []*types.TestResult{tTest(0.2, 0.05, 0.1), tTest(0.01, 0.05, 0.1)}}
	if res := readResponse(t, nil, two, nil, descriptor.ExplainTerse); !manyTestsCaveat(res) {
		t.Errorf("two uncorrected tests: caveats = %v", res.Caveats)
	}
	if res := readResponse(t, nil, &types.Response{Tests: two.Tests[:1]}, nil, descriptor.ExplainTerse); manyTestsCaveat(res) {
		t.Errorf("one test carries the many-tests caveat: %v", res.Caveats)
	}
	m := &types.AppliedMultiplicity{Method: types.MultiplicityMethodHolm, Family: types.MultiplicityFamilyRequest, Alpha: 0.05, M: 2}
	var corrected []*types.TestResult
	for _, tr := range two.Tests {
		c := *tr
		c.Multiplicity, c.PAdjusted, c.SignificantAdjusted = m, fptr(0.4), bptr(false)
		corrected = append(corrected, &c)
	}
	if res := readResponse(t, nil, &types.Response{Tests: corrected}, nil, descriptor.ExplainTerse); manyTestsCaveat(res) {
		t.Errorf("corrected tests carry the many-tests caveat: %v", res.Caveats)
	}
	// An uncorrected per-cell overlay counts every cell.
	cells := &types.Response{Overlays: []types.OverlayLayer{cellLayer(nil)}}
	if res := readResponse(t, nil, cells, nil, descriptor.ExplainTerse); !manyTestsCaveat(res) {
		t.Errorf("per-cell overlay: caveats = %v", res.Caveats)
	}
}

// cellLayer is an OVERLAY_T_CELL layer with three cell p-values, one
// undefined; m adjusts it.
func cellLayer(m *types.AppliedMultiplicity) types.OverlayLayer {
	l := types.OverlayLayer{Name: "t cells", Kind: types.OverlayKind("OVERLAY_T_CELL"), Payload: types.OverlayPayload{
		Shape: types.OverlayShapeMatrix,
		Matrix: &types.MatrixPayload{Cells: [][]types.MatrixCell{
			{{Value: 0.01, Present: true}, {Value: 0.3, Present: true}},
			{{Value: math.NaN(), Present: true}, {Present: false}},
		}},
	}}
	if m != nil {
		l.Multiplicity = m
		l.Payload.SignificantAdjusted = &types.MatrixPayload{Cells: [][]types.MatrixCell{
			{{Value: false, Present: true}, {Value: false, Present: true}},
			{{Value: false, Present: true}, {Present: false}},
		}}
	}
	return l
}

func TestExplainResponse_Overlays(t *testing.T) {
	res := readResponse(t, nil, &types.Response{Overlays: []types.OverlayLayer{cellLayer(nil)}}, nil, descriptor.ExplainFull)
	f := res.Findings[0]
	if f.Verdict != descriptor.VerdictEvidenceOfDifference || *f.Numbers["tests"] != 2 || *f.Numbers["below_alpha"] != 1 {
		t.Errorf("uncorrected cells = %+v", f)
	}
	if !slices.ContainsFunc(res.Caveats, func(c string) bool { return strings.Contains(c, "OVERLAY_T_CELL carries no alpha") }) {
		t.Errorf("caveats lack the overlay alpha note: %v", res.Caveats)
	}
	m := &types.AppliedMultiplicity{Method: types.MultiplicityMethodBH, Family: types.MultiplicityFamilyLayer, Alpha: 0.1, M: 2}
	res = readResponse(t, nil, &types.Response{Overlays: []types.OverlayLayer{cellLayer(m)}}, nil, descriptor.ExplainTerse)
	f = res.Findings[0]
	if f.Verdict != descriptor.VerdictNoEvidenceOfDifference || f.Multiplicity == nil || *f.Numbers["alpha"] != 0.1 {
		t.Errorf("adjusted cells = %+v", f)
	}
	// A single-p kind reads its summary p-value.
	scalar := types.OverlayLayer{Kind: types.OverlayKind("OVERLAY_KS_VS_POP"), Summary: &types.OverlaySummary{Statistic: fptr(0.2), PValue: fptr(0.004)}}
	res = readResponse(t, nil, &types.Response{Overlays: []types.OverlayLayer{scalar}}, nil, descriptor.ExplainTerse)
	if f := res.Findings[0]; f.Verdict != descriptor.VerdictEvidenceOfDifference || *f.Numbers["summary.p_value"] != 0.004 {
		t.Errorf("scalar layer = %+v", f)
	}
	// A descriptive kind carries no verdict of evidence.
	desc := types.OverlayLayer{Kind: types.OverlayKind("OVERLAY_ZSCORE_VS_TOTAL")}
	if f := readResponse(t, nil, &types.Response{Overlays: []types.OverlayLayer{desc}}, nil, descriptor.ExplainTerse).Findings[0]; f.Verdict != descriptor.VerdictDescriptive {
		t.Errorf("descriptive layer = %+v", f)
	}
}

// Every band names its convention; an unbanded effect size gets its
// number only.
func TestExplainResponse_BandsNameConvention(t *testing.T) {
	chisq := &types.TestResult{Type: types.TEST_CHISQ, Statistic: 9, PValue: 0.01, Alpha: 0.05, RejectNull: true,
		Details: map[string]any{"effect_size": map[string]any{"cramers_v": 0.21}}}
	resp := &types.Response{
		Tests:       []*types.TestResult{tTest(0.01, 0.05, 0.62), chisq},
		Regressions: []*types.RegressionResult{olsResult()},
		Matrices:    []types.MatrixResult{corrMatrix()},
	}
	res := readResponse(t, nil, resp, nil, descriptor.ExplainFull)
	banded := 0
	for _, f := range res.Findings {
		if (f.StrengthBand == "") != (f.Convention == "") {
			t.Errorf("band without convention (or the reverse): %+v", f)
		}
		if f.StrengthBand != "" {
			banded++
		}
	}
	if banded < 3 {
		t.Errorf("fixture drift: %d banded findings, want the t-test, the OLS fit and the correlation matrix", banded)
	}
	if f := res.Findings[0]; f.StrengthBand != "medium" || f.Convention != "Cohen (1988)" {
		t.Errorf("t-test band = %q / %q", f.StrengthBand, f.Convention)
	}
	cf := res.Findings[1]
	if cf.Verdict != descriptor.VerdictEvidenceOfAssociation || cf.StrengthBand != "" || cf.Convention != "" {
		t.Errorf("chi-square finding = %+v", cf)
	}
	if v := cf.Numbers["details.effect_size.cramers_v"]; v == nil || *v != 0.21 {
		t.Errorf("cramers_v number = %v", v)
	}
	if !slices.ContainsFunc(res.Sentences, func(s string) bool { return strings.Contains(s, "`cramers_v` is 0.21.") }) {
		t.Errorf("unbanded effect size sentence missing: %v", res.Sentences)
	}
}

func olsResult() *types.RegressionResult {
	return &types.RegressionResult{
		Name: "fit", Type: types.REG_OLS, R2: 0.2, AdjR2: 0.18, NObs: 50,
		Coefficients: map[string]float64{"(intercept)": 1, "hours": 0.8, "age": 0.01},
		StdErrors:    map[string]float64{"(intercept)": 0.2, "hours": 0.1, "age": 0.05},
		PValues:      map[string]float64{"(intercept)": 0.001, "hours": 0.0001, "age": 0.6},
	}
}

func corrMatrix() types.MatrixResult {
	return types.MatrixResult{Name: "corr", Type: types.MAT_CORRELATION, Primary: &types.MatrixValues{
		RowKeys: []string{"a", "b"}, ColumnKeys: []string{"a", "b"}, Values: [][]float64{{1, -0.45}, {-0.45, 1}},
	}, Scalars: map[string]float64{"determinant": 0.8}}
}

func TestExplainResponse_RegressionsAndMatrices(t *testing.T) {
	req := &types.Request{Regressions: []*types.RegressionSpec{{Type: types.REG_OLS, Target: "score", Predictors: []string{"hours", "age"}}}}
	res := readResponse(t, nil, &types.Response{Regressions: []*types.RegressionResult{olsResult()}, Matrices: []types.MatrixResult{corrMatrix()},
		Components: &types.ResponseComponents{Matrices: []types.MatrixComponents{{Name: "corr", N: 40, NListwiseDropped: 3}}}}, req, descriptor.ExplainFull)
	byVerdict := map[descriptor.Verdict]int{}
	for _, f := range res.Findings {
		byVerdict[f.Verdict]++
		if strings.Contains(f.Subject, "(intercept)") {
			t.Errorf("intercept got a finding: %+v", f)
		}
	}
	if byVerdict[descriptor.VerdictEvidenceOfEffect] != 1 || byVerdict[descriptor.VerdictNoEvidenceOfEffect] != 1 {
		t.Errorf("coefficient verdicts = %v", byVerdict)
	}
	model := res.Findings[0]
	if model.Subject != "predicting `score` from `hours` and `age`" || model.StrengthBand != "medium" || !strings.HasPrefix(model.Convention, "Cohen (1988)") {
		t.Errorf("model finding = %+v", model)
	}
	if !slices.ContainsFunc(res.Caveats, func(c string) bool { return strings.HasPrefix(c, "Regression p-values are raw") }) {
		t.Errorf("caveats lack the raw regression note: %v", res.Caveats)
	}
	mf := res.Findings[len(res.Findings)-1]
	if mf.Operator != "MAT_CORRELATION" || mf.StrengthBand != "medium" || *mf.Numbers["primary.values.strongest"] != -0.45 ||
		*mf.Numbers["n_listwise_dropped"] != 3 || *mf.Numbers["scalars.determinant"] != 0.8 {
		t.Errorf("matrix finding = %+v", mf)
	}
}

// An undefined p-value is not_computable with a null figure — in Go
// (NaN) and after a JSON round trip (null decodes to 0).
func TestExplainResponse_NotComputable(t *testing.T) {
	nan := tTest(math.NaN(), 0.05, 0.1)
	nan.Statistic = math.NaN()
	res := readResponse(t, nil, &types.Response{Tests: []*types.TestResult{nan}}, nil, descriptor.ExplainFull)
	f := res.Findings[0]
	if f.Verdict != descriptor.VerdictNotComputable || f.Numbers["p_value"] != nil {
		t.Fatalf("NaN finding = %+v", f)
	}
	raw, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(raw), `"p_value":null`) {
		t.Errorf("p_value not null on the wire: %s", raw)
	}
	wire, err := types.MarshalFinite(&types.Response{Tests: []*types.TestResult{nan}})
	if err != nil {
		t.Fatal(err)
	}
	var back types.Response
	if err := json.Unmarshal(wire, &back); err != nil {
		t.Fatal(err)
	}
	if v := readResponse(t, nil, &back, nil, descriptor.ExplainTerse).Findings[0].Verdict; v != descriptor.VerdictNotComputable {
		t.Errorf("round-tripped null p verdict = %s", v)
	}
}

// With the request, aggregations are named by operator and weight;
// without it, by count only, with the partial note — never a guessed
// operator.
func TestExplainResponse_AggregationsWithAndWithoutRequest(t *testing.T) {
	resp := &types.Response{
		Data:     []map[string]any{{"region": "n", "total": 10.0}, {"region": "s", "total": 12.0}},
		Metadata: &types.ResponseMetadata{TotalRows: 100, FilteredRows: 90},
		Components: &types.ResponseComponents{
			Aggregations: []types.AggregationComponents{{Label: "total", N: 90, NNull: 4, SumWeights: fptr(80)}},
			Groupers:     []types.GrouperComponents{{}},
			Run:          &types.RunComponents{TotalRecords: 100, FilteredRecords: 90, NullRecords: 4},
		},
	}
	req := &types.Request{
		Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score", Label: "total"}},
		Weight:       &types.WeightSpec{Field: "wt", Kind: types.WeightKindProbability},
	}
	with := readResponse(t, nil, resp, req, descriptor.ExplainFull)
	f := with.Findings[0]
	if f.Operator != "AGG_SUM" || f.Subject != "total" || *f.Numbers["n_null"] != 4 || *f.Numbers["sum_weights"] != 80 {
		t.Errorf("with request = %+v", f)
	}
	if !slices.ContainsFunc(with.Sentences, func(s string) bool { return strings.Contains(s, "weighted by `wt`") }) {
		t.Errorf("weight not named: %v", with.Sentences)
	}
	if slices.ContainsFunc(with.Caveats, func(c string) bool { return strings.HasPrefix(c, "Partial reading") }) {
		t.Errorf("partial note with the request: %v", with.Caveats)
	}
	if !strings.Contains(with.Summary, "1 aggregation across 2 groups from 90 of 100 records") {
		t.Errorf("summary = %q", with.Summary)
	}

	without := readResponse(t, nil, resp, nil, descriptor.ExplainFull)
	for _, f := range without.Findings {
		if f.Operator != "" {
			t.Errorf("guessed an operator without the request: %+v", f)
		}
	}
	if !slices.ContainsFunc(without.Caveats, func(c string) bool { return strings.HasPrefix(c, "Partial reading") }) {
		t.Errorf("no partial note: %v", without.Caveats)
	}
	if !strings.Contains(without.Summary, "aggregated output of 90 rows across 2 groups") {
		t.Errorf("summary = %q", without.Summary)
	}
	if !slices.ContainsFunc(without.Sentences, func(s string) bool { return strings.Contains(s, "does not name it") }) {
		t.Errorf("unnamed weight not flagged: %v", without.Sentences)
	}
	// Ungrouped with the request reads the value.
	one := &types.Response{Data: []map[string]any{{"total": 42.0}}, Components: &types.ResponseComponents{
		Aggregations: []types.AggregationComponents{{Label: "total", N: 9}}}}
	r1 := &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score", Label: "total", Weight: types.NullSlotWeight()}}, Weight: req.Weight}
	g := readResponse(t, nil, one, r1, descriptor.ExplainFull)
	if v := g.Findings[0].Numbers["value"]; v == nil || *v != 42 {
		t.Errorf("ungrouped value = %v", v)
	}
	if slices.ContainsFunc(g.Sentences, func(s string) bool { return strings.Contains(s, "weighted by") }) {
		t.Errorf("a null slot weight is named: %v", g.Sentences)
	}
}

// Absent Components, a return-shaped response and nil test positions
// are read without error, each saying what it could not read.
func TestExplainResponse_ToleratesStrippedParts(t *testing.T) {
	resp := &types.Response{
		Data:     []map[string]any{{"x": 1.0}},
		Tests:    []*types.TestResult{nil, tTest(0.5, 0.05, 0.1)},
		Returned: &types.ReturnedMarker{Preset: "minimal"},
	}
	res := readResponse(t, nil, resp, nil, descriptor.ExplainTerse)
	if len(res.Findings) != 1 || !strings.Contains(res.Summary, "aggregated output across 1 group") {
		t.Fatalf("summary = %q, findings = %+v", res.Summary, res.Findings)
	}
	for _, want := range []string{"carries no components", "shaped by a return block"} {
		if !slices.ContainsFunc(res.Caveats, func(c string) bool { return strings.Contains(c, want) }) {
			t.Errorf("caveats lack %q: %v", want, res.Caveats)
		}
	}
	empty := readResponse(t, nil, &types.Response{}, nil, descriptor.ExplainFull)
	if empty.Summary != "This response reports no analysis result." || len(empty.Findings) != 0 || empty.Findings == nil {
		t.Errorf("empty response = %+v", empty)
	}
}

// A hidden operator is never named, in findings or prose.
func TestExplainResponse_ProfileHidesOperators(t *testing.T) {
	inst := profiled("TEST_T", "AGG_MEDIAN")
	resp := &types.Response{Tests: []*types.TestResult{tTest(0.01, 0.05, 0.6)},
		Components: &types.ResponseComponents{Aggregations: []types.AggregationComponents{{Label: "mid", N: 3}}}}
	req := &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_MEDIAN, Field: "score", Label: "mid"}}}
	res := readResponse(t, inst, resp, req, descriptor.ExplainFull)
	raw, _ := json.Marshal(res)
	for _, h := range []string{"TEST_T", "AGG_MEDIAN"} {
		if strings.Contains(string(raw), h) {
			t.Errorf("hidden %s named: %s", h, raw)
		}
	}
	if res.Findings[0].Verdict != descriptor.VerdictEvidenceOfDifference {
		t.Errorf("hidden-operator verdict = %s", res.Findings[0].Verdict)
	}
	def := readResponse(t, nil, resp, req, descriptor.ExplainFull)
	raw, _ = json.Marshal(def)
	for _, h := range []string{"TEST_T", "AGG_MEDIAN"} {
		if !strings.Contains(string(raw), h) {
			t.Errorf("fixture drift: the default instance does not name %s", h)
		}
	}
}

// Every response-mode sentence, terse and full, passes the prose lint.
func TestExplainResponse_ProseLint(t *testing.T) {
	m := &types.AppliedMultiplicity{Method: types.MultiplicityMethodHolm, Family: types.MultiplicityFamilyRequest, Alpha: 0.05, M: 2}
	adj := tTest(0.03, 0.05, 0.3)
	adj.Multiplicity, adj.PAdjusted, adj.SignificantAdjusted = m, fptr(0.06), bptr(false)
	full := &types.Response{
		Data:        []map[string]any{{"total": 3.0}},
		Tests:       []*types.TestResult{tTest(0.2, 0.05, 0.9), tTest(0.001, 0.01, 0.05), tTest(math.NaN(), 0.05, 0.1), adj},
		PostTests:   []*types.TestResult{{Type: types.TEST_TUKEY_HSD, PValue: 0.04, Alpha: 0.05, RejectNull: true}},
		Regressions: []*types.RegressionResult{olsResult()},
		Matrices:    []types.MatrixResult{corrMatrix()},
		Overlays:    []types.OverlayLayer{cellLayer(nil), cellLayer(m), {Kind: types.OverlayKind("OVERLAY_ZSCORE_VS_TOTAL")}},
		Components: &types.ResponseComponents{
			Aggregations: []types.AggregationComponents{{Label: "total", N: 9, NNull: 1}},
			Run:          &types.RunComponents{TotalRecords: 10, FilteredRecords: 9, ShardCount: 2, PartialCohortReason: "x"},
		},
		Returned: &types.ReturnedMarker{Preset: "standard"},
	}
	req := &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score", Label: "total"}}, Weight: &types.WeightSpec{Field: "wt"}}
	n := 0
	for _, r := range []*types.Request{nil, req} {
		for _, d := range []descriptor.ExplainDetail{descriptor.ExplainTerse, descriptor.ExplainFull} {
			res := readResponse(t, nil, full, r, d)
			for _, s := range explainTexts(res) {
				n++
				for _, h := range descx.LintGuidanceText(s) {
					t.Errorf("%s: lint %s on %q in %q", d, h.Rule, h.Span, s)
				}
			}
		}
	}
	if n < 20 {
		t.Fatalf("vacuous: %d texts linted", n)
	}
}

// Every built-in test family and inferential overlay kind is classed,
// and every class entry names a real one.
func TestExplainResponse_ClassTablesComplete(t *testing.T) {
	for _, tt := range types.AllTestTypes() {
		if _, ok := testClass[string(tt)]; !ok {
			t.Errorf("testClass misses %s", tt)
		}
	}
	if len(testClass) != len(types.AllTestTypes()) {
		t.Errorf("testClass has %d entries, %d test types", len(testClass), len(types.AllTestTypes()))
	}
	inferential := map[string]bool{}
	for _, k := range types.AllOverlayKinds() {
		ins, _ := descx.InterpretationsOf(string(k))
		if slices.ContainsFunc(ins, func(in descriptor.Interpretation) bool { return in.Shared == descx.SharedPValue }) {
			inferential[string(k)] = true
			if _, ok := overlayClass[string(k)]; !ok {
				t.Errorf("overlayClass misses %s", k)
			}
		}
	}
	for k := range overlayClass {
		if !inferential[k] {
			t.Errorf("overlayClass names %s, which reads no p-value", k)
		}
	}
}
