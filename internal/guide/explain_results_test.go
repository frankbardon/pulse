package guide

import (
	"encoding/json"
	stderrors "errors"
	"slices"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/types"
)

func readResult(t *testing.T, inst *descx.InstanceSnapshot, req descriptor.ExplainRequest, root descriptor.ExplainRoot) *descriptor.ExplainResult {
	t.Helper()
	res := explain(t, inst, req, Checks{})
	if res.Mode != descriptor.ExplainModeResponse || res.Root != root {
		t.Fatalf("mode/root = %s/%s, want response/%s", res.Mode, res.Root, root)
	}
	return res
}

func findingAt(res *descriptor.ExplainResult, slot string) (descriptor.ExplainFinding, bool) {
	for _, f := range res.Findings {
		if f.Slot == slot {
			return f, true
		}
	}
	return descriptor.ExplainFinding{}, false
}

func slots(res *descriptor.ExplainResult) []string {
	var out []string
	for _, f := range res.Findings {
		out = append(out, f.Slot)
	}
	return out
}

func hasCaveat(res *descriptor.ExplainResult, sub string) bool {
	return slices.ContainsFunc(res.Caveats, func(c string) bool { return strings.Contains(c, sub) })
}

func hasSentence(res *descriptor.ExplainResult, sub string) bool {
	return slices.ContainsFunc(res.Sentences, func(s string) bool { return strings.Contains(s, sub) })
}

// Each result root takes only its own request as companion.
func TestExplainResults_Validates(t *testing.T) {
	cr, ch, fr := &types.ComposedResponse{}, &types.ChainResponse{}, &types.FacetResult{}
	for name, req := range map[string]descriptor.ExplainRequest{
		"two results":                   {Response: &types.Response{}, ComposedResponse: cr},
		"composed_response and request": {ComposedResponse: cr, Request: &types.Request{}},
		"chain_response and facet":      {ChainResponse: ch, Facet: &types.FacetRequest{}},
		"facet_result and composed":     {FacetResult: fr, Composed: &types.ComposedRequest{}},
		"facet_result and sample":       {FacetResult: fr, Sample: &types.SampleRequest{}},
		"response and chain":            {Response: &types.Response{}, Chain: &types.ChainRequest{}},
	} {
		_, err := Explain(nil, req, Checks{})
		var ce *errors.CodedError
		if !stderrors.As(err, &ce) || ce.Code != errors.SERVICE_VALIDATION {
			t.Errorf("%s: err = %v, want SERVICE_VALIDATION", name, err)
		}
	}
	for name, c := range map[string]struct {
		req  descriptor.ExplainRequest
		root descriptor.ExplainRoot
	}{
		"composed_response + composed": {descriptor.ExplainRequest{ComposedResponse: cr, Composed: &types.ComposedRequest{}}, descriptor.ExplainRootComposedResponse},
		"chain_response alone":         {descriptor.ExplainRequest{ChainResponse: ch}, descriptor.ExplainRootChainResponse},
		"chain_response + chain":       {descriptor.ExplainRequest{ChainResponse: ch, Chain: &types.ChainRequest{}}, descriptor.ExplainRootChainResponse},
		"facet_result + facet":         {descriptor.ExplainRequest{FacetResult: fr, Facet: &types.FacetRequest{}}, descriptor.ExplainRootFacetResult},
	} {
		root, _, err := ValidateExplainRequest(c.req)
		if err != nil || root != c.root {
			t.Errorf("%s: root = %s, err = %v", name, root, err)
		}
	}
}

// composedFixture: two slots, each one uncorrected test (so no slot
// alone has two), the second also an aggregation; one batch overlay
// adjusted over the compose family and carrying a warning.
func composedFixture() (*types.ComposedResponse, *types.ComposedRequest) {
	agg := &types.Response{
		Tests: []*types.TestResult{tTest(0.01, 0.05, 0.6)},
		Data:  []map[string]any{{"total": 12.0}},
		Components: &types.ResponseComponents{
			Aggregations: []types.AggregationComponents{{Label: "total", N: 9}},
			Run:          &types.RunComponents{TotalRecords: 10, FilteredRecords: 9},
		},
	}
	layer := cellLayer(&types.AppliedMultiplicity{Method: types.MultiplicityMethodHolm, Family: types.MultiplicityFamilyCompose, Alpha: 0.05, M: 3})
	layer.Name = "arm cells"
	layer.Warnings = []types.OverlayWarning{{Code: string(errors.PULSE_OVERLAY_REF_ZERO), Message: "zero ref"}}
	resp := &types.ComposedResponse{
		Responses: []*types.Response{{Tests: []*types.TestResult{tTest(0.3, 0.05, 0.1)}}, agg},
		Overlays:  []types.OverlayLayer{layer},
	}
	req := &types.ComposedRequest{Requests: []*types.Request{
		{Tests: []*types.Test{{Type: types.TEST_T, Field: "score", SplitBy: "arm"}}},
		{Tests: []*types.Test{{Type: types.TEST_T, Field: "score", SplitBy: "arm"}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score", Label: "total"}}},
	}}
	return resp, req
}

// Compose is read per slot (paths under responses[i].) then per batch
// overlay layer, which names its correction; the many-tests caveat
// counts across slots.
func TestExplainComposedResponse_PerSlotAndLayers(t *testing.T) {
	resp, req := composedFixture()
	res := readResult(t, nil, descriptor.ExplainRequest{ComposedResponse: resp, Composed: req, Detail: descriptor.ExplainFull}, descriptor.ExplainRootComposedResponse)
	want := []string{"responses[0].tests[0]", "responses[1].tests[0]", "responses[1].aggregations[0]", "overlays[0]"}
	if got := slots(res); !slices.Equal(got, want) {
		t.Fatalf("finding slots = %v, want %v", got, want)
	}
	if f, _ := findingAt(res, "responses[1].tests[0]"); f.Verdict != descriptor.VerdictEvidenceOfDifference || f.Operator != "TEST_T" {
		t.Errorf("slot 2 test = %+v", f)
	}
	if f, _ := findingAt(res, "responses[1].aggregations[0]"); f.Operator != "AGG_SUM" {
		t.Errorf("slot aggregation not named with the batch request: %+v", f)
	}
	layer, _ := findingAt(res, "overlays[0]")
	wantM := descriptor.ExplainMultiplicity{Method: "holm", Family: "compose", M: 3}
	if layer.Multiplicity == nil || *layer.Multiplicity != wantM || layer.Subject != "arm cells" {
		t.Errorf("batch layer = %+v", layer)
	}
	if !manyTestsCaveat(res) || !hasCaveat(res, "2 p-values here carry no adjustment") {
		t.Errorf("many-tests caveat does not count across slots: %v", res.Caveats)
	}
	if !hasCaveat(res, "The batch's overlay 1 (OVERLAY_T_CELL) `arm cells` carries 1 warning (PULSE_OVERLAY_REF_ZERO)") {
		t.Errorf("layer warning not named: %v", res.Caveats)
	}
	for _, s := range []string{"Request 2 reports 1 test and 1 aggregation from 9 of 10 records.", "Request 1's test 1 (TEST_T)",
		"The batch's overlay 1 (OVERLAY_T_CELL)", "after the holm adjustment over its compose family"} {
		if !hasSentence(res, s) {
			t.Errorf("sentences lack %q: %v", s, res.Sentences)
		}
	}
	if !strings.HasPrefix(res.Summary, "This batch reports 2 responses side by side and 1 batch overlay across them; 1 of 3 inferential") {
		t.Errorf("summary = %q", res.Summary)
	}
	if hasCaveat(res, "Partial reading") {
		t.Errorf("partial note with the batch request: %v", res.Caveats)
	}

	// Without the batch request: no operator guessed for the slot's
	// aggregation, and the partial note names the composed request.
	bare := readResult(t, nil, descriptor.ExplainRequest{ComposedResponse: resp}, descriptor.ExplainRootComposedResponse)
	if f, _ := findingAt(bare, "responses[1].aggregations[0]"); f.Operator != "" {
		t.Errorf("guessed an operator: %+v", f)
	}
	if !hasCaveat(bare, "Partial reading: the composed request is absent") {
		t.Errorf("no partial note: %v", bare.Caveats)
	}
	// One slot test alone carries no many-tests caveat.
	one := &types.ComposedResponse{Responses: resp.Responses[:1]}
	if res := readResult(t, nil, descriptor.ExplainRequest{ComposedResponse: one}, descriptor.ExplainRootComposedResponse); manyTestsCaveat(res) {
		t.Errorf("one test carries the many-tests caveat: %v", res.Caveats)
	}
}

// chainFixture: two stages of one aggregation each and a descriptive
// whole-chain overlay; echo puts the normalized request on the response.
func chainFixture(echo bool) (*types.ChainResponse, *types.ChainRequest) {
	stage := func(label string, n int) *types.Response {
		return &types.Response{
			Data: []map[string]any{{"region": "n", label: 1.0}, {"region": "s", label: 2.0}},
			Components: &types.ResponseComponents{
				Aggregations: []types.AggregationComponents{{Label: label, N: n}},
				Groupers:     []types.GrouperComponents{{}},
				Run:          &types.RunComponents{TotalRecords: 100, FilteredRecords: int64(n)},
			},
		}
	}
	req := &types.ChainRequest{
		Cohort: &types.Cohort{Filename: "c.pulse"},
		Stages: []*types.ChainStage{
			{Name: "by_region", Request: &types.Request{
				Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score", Label: "total"}}}},
			{Request: &types.Request{
				Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_AVERAGE, Field: "total", Label: "avg"}}}},
		},
	}
	last := stage("avg", 2)
	resp := &types.ChainResponse{
		Stages:   []*types.Response{stage("total", 90), last},
		Final:    last,
		Overlays: []*types.OverlayLayer{{Name: "growth", Kind: types.OverlayKind("OVERLAY_INDEX_VS_STAGE")}},
	}
	if echo {
		resp.NormalizedRequest = req
	}
	return resp, req
}

// A chain is read stage by stage with the request it echoes, so its
// aggregations are named with no request supplied.
func TestExplainChainResponse_UsesEchoedRequest(t *testing.T) {
	echoed, req := chainFixture(true)
	res := readResult(t, nil, descriptor.ExplainRequest{ChainResponse: echoed, Detail: descriptor.ExplainFull}, descriptor.ExplainRootChainResponse)
	want := []string{"stages[0].aggregations[0]", "stages[1].aggregations[0]", "overlays[0]"}
	if got := slots(res); !slices.Equal(got, want) {
		t.Fatalf("finding slots = %v, want %v", got, want)
	}
	if f, _ := findingAt(res, "stages[1].aggregations[0]"); f.Operator != "AGG_AVERAGE" || f.Subject != "avg" {
		t.Errorf("stage 2 aggregation not named from the echo: %+v", f)
	}
	if hasCaveat(res, "Partial reading") {
		t.Errorf("partial note despite the echo: %v", res.Caveats)
	}
	if !hasSentence(res, "Stage 2's aggregation 1 `avg` runs AGG_AVERAGE") || !hasSentence(res, "normalized_request") {
		t.Errorf("sentences = %v", res.Sentences)
	}
	if !strings.HasPrefix(res.Summary, "This chain reports 2 stages, each on the rows the stage before it returns; its last stage reports 1 aggregation across 2 groups from 2 of 100 records, with 1 whole-chain overlay") {
		t.Errorf("summary = %q", res.Summary)
	}

	// No echo, no companion: partial, nothing guessed.
	bare, _ := chainFixture(false)
	res = readResult(t, nil, descriptor.ExplainRequest{ChainResponse: bare}, descriptor.ExplainRootChainResponse)
	for _, f := range res.Findings {
		if strings.Contains(f.Slot, "aggregations") && f.Operator != "" {
			t.Errorf("guessed an operator: %+v", f)
		}
	}
	if !hasCaveat(res, "Partial reading: the chain request is absent") {
		t.Errorf("no partial note: %v", res.Caveats)
	}
	// No echo, companion supplied: named from the companion.
	res = readResult(t, nil, descriptor.ExplainRequest{ChainResponse: bare, Chain: req}, descriptor.ExplainRootChainResponse)
	if f, _ := findingAt(res, "stages[0].aggregations[0]"); f.Operator != "AGG_SUM" {
		t.Errorf("companion not read: %+v", f)
	}
	// Final alone (no per-stage list) is read as the one stage.
	finalOnly := &types.ChainResponse{Final: echoed.Final}
	res = readResult(t, nil, descriptor.ExplainRequest{ChainResponse: finalOnly}, descriptor.ExplainRootChainResponse)
	if len(res.Findings) != 1 || res.Findings[0].Slot != "stages[0].aggregations[0]" {
		t.Errorf("final-only findings = %+v", res.Findings)
	}
}

func facetParams(field string) json.RawMessage {
	raw, _ := json.Marshal(map[string]string{"field": field})
	return raw
}

// facetFixture: a discrete and a numeric field, each decorated by one
// uncorrected inferential overlay (declared numeric-first so the
// per-field blocks reorder them).
func facetFixture() (*types.FacetResult, *types.FacetRequest) {
	res := &types.FacetResult{
		Fields: map[string]*types.FacetField{
			"region": {Kind: "discrete", TypeName: "categorical_u8", NullCount: 3, Discrete: &types.FacetDiscrete{
				Values: []types.FacetValueCount{{Value: "north", Count: 40}, {Value: "south", Count: 30}}, DistinctCount: 4, TruncatedAt: 2}},
			"score": {Kind: "numeric", TypeName: "f64", NullCount: 1, Numeric: &types.FacetNumeric{
				Min: 1, Max: 99, Mean: 50.2, StdDev: 10.1, Count: 89, Sum: 4467.8, Percentiles: map[string]float64{"p50": 51}}},
		},
		TotalRecords: 100, FilteredRecords: 90,
		Warnings: []string{"region: truncated to top 2"},
		Overlays: []types.OverlayLayer{
			{Name: "score fit", Kind: types.OverlayKind("OVERLAY_KS_VS_POP"), Summary: &types.OverlaySummary{Statistic: fptr(0.2), PValue: fptr(0.004)}},
			{Name: "region fit", Kind: types.OverlayKind("OVERLAY_CHISQ_VS_POP"), Summary: &types.OverlaySummary{Statistic: fptr(3.1), PValue: fptr(0.4)}},
		},
	}
	req := &types.FacetRequest{
		Cohort: &types.Cohort{Filename: "c.pulse"},
		Fields: []string{"region", "score"},
		Overlays: []types.OverlaySpec{
			{Kind: types.OverlayKind("OVERLAY_KS_VS_POP"), Params: facetParams("score")},
			{Kind: types.OverlayKind("OVERLAY_CHISQ_VS_POP"), Params: facetParams("region")},
		},
	}
	return res, req
}

// A facet is read one block per field — the field, then its overlays —
// and the many-tests caveat counts across facet fields.
func TestExplainFacetResult_PerField(t *testing.T) {
	fr, req := facetFixture()
	res := readResult(t, nil, descriptor.ExplainRequest{FacetResult: fr, Facet: req, Detail: descriptor.ExplainFull}, descriptor.ExplainRootFacetResult)
	want := []string{"fields.region", "overlays[1]", "fields.score", "overlays[0]"}
	if got := slots(res); !slices.Equal(got, want) {
		t.Fatalf("finding slots = %v, want %v", got, want)
	}
	region, _ := findingAt(res, "fields.region")
	if region.Verdict != descriptor.VerdictDescriptive || *region.Numbers["distinct_count"] != 4 || *region.Numbers["top_count"] != 40 ||
		*region.Numbers["truncated_at"] != 2 || *region.Numbers["null_count"] != 3 {
		t.Errorf("region finding = %+v", region)
	}
	score, _ := findingAt(res, "fields.score")
	if *score.Numbers["mean"] != 50.2 || *score.Numbers["percentiles.p50"] != 51 || *score.Numbers["count"] != 89 {
		t.Errorf("score finding = %+v", score)
	}
	if f, _ := findingAt(res, "overlays[0]"); f.Verdict != descriptor.VerdictEvidenceOfDifference {
		t.Errorf("KS layer = %+v", f)
	}
	if f, _ := findingAt(res, "overlays[1]"); f.Verdict != descriptor.VerdictNoEvidenceOfDifference {
		t.Errorf("chi-square layer = %+v", f)
	}
	if !manyTestsCaveat(res) {
		t.Errorf("many-tests caveat does not count across facet fields: %v", res.Caveats)
	}
	for _, s := range []string{"Field `score`'s overlay 1 (OVERLAY_KS_VS_POP)", "Field `region` (categorical_u8) has 4 distinct values; the most common is `north`",
		"leaves out 2", "Field `score` (f64) has 89 values from 1 to 99"} {
		if !hasSentence(res, s) {
			t.Errorf("sentences lack %q: %v", s, res.Sentences)
		}
	}
	if !hasCaveat(res, "carries 1 warning") {
		t.Errorf("facet warnings not noted: %v", res.Caveats)
	}
	if res.Summary != "This facet result summarises 2 fields from 90 of 100 records, with 2 overlays; 1 of 2 inferential findings show evidence at their alpha." {
		t.Errorf("summary = %q", res.Summary)
	}

	// One overlay: no many-tests caveat.
	single := *fr
	single.Overlays = fr.Overlays[:1]
	if r := readResult(t, nil, descriptor.ExplainRequest{FacetResult: &single, Facet: req}, descriptor.ExplainRootFacetResult); manyTestsCaveat(r) {
		t.Errorf("one facet overlay carries the many-tests caveat: %v", r.Caveats)
	}
	// Without the request two fields cannot host the overlays: they
	// follow the fields, with a caveat.
	bare := readResult(t, nil, descriptor.ExplainRequest{FacetResult: fr}, descriptor.ExplainRootFacetResult)
	if got := slots(bare); !slices.Equal(got, []string{"fields.region", "fields.score", "overlays[0]", "overlays[1]"}) {
		t.Errorf("bare slots = %v", got)
	}
	if !hasCaveat(bare, "The facet request is absent") {
		t.Errorf("no unattached caveat: %v", bare.Caveats)
	}
	// An empty numeric field is not computable, its mean null.
	empty := &types.FacetResult{Fields: map[string]*types.FacetField{"x": {Kind: "numeric", Numeric: &types.FacetNumeric{}}}}
	f := readResult(t, nil, descriptor.ExplainRequest{FacetResult: empty}, descriptor.ExplainRootFacetResult).Findings[0]
	if f.Verdict != descriptor.VerdictNotComputable || f.Numbers["mean"] != nil {
		t.Errorf("empty numeric = %+v", f)
	}
}

// A hidden operator is never named in a slot of a larger result.
func TestExplainResults_ProfileHidesOperators(t *testing.T) {
	resp, req := composedFixture()
	res := readResult(t, profiled("TEST_T", "AGG_SUM"), descriptor.ExplainRequest{ComposedResponse: resp, Composed: req, Detail: descriptor.ExplainFull},
		descriptor.ExplainRootComposedResponse)
	raw, _ := json.Marshal(res)
	for _, h := range []string{"TEST_T", "AGG_SUM"} {
		if strings.Contains(string(raw), h) {
			t.Errorf("hidden %s named: %s", h, raw)
		}
	}
}

// Every sentence of the three roots, terse and full, with and without
// the companion, passes the prose lint.
func TestExplainResults_ProseLint(t *testing.T) {
	cr, creq := composedFixture()
	ch, chreq := chainFixture(false)
	fr, freq := facetFixture()
	n := 0
	for _, d := range []descriptor.ExplainDetail{descriptor.ExplainTerse, descriptor.ExplainFull} {
		for _, req := range []descriptor.ExplainRequest{
			{ComposedResponse: cr}, {ComposedResponse: cr, Composed: creq},
			{ChainResponse: ch}, {ChainResponse: ch, Chain: chreq},
			{FacetResult: fr}, {FacetResult: fr, Facet: freq},
		} {
			req.Detail = d
			res := explain(t, nil, req, Checks{})
			for _, s := range explainTexts(res) {
				n++
				for _, h := range descx.LintGuidanceText(s) {
					t.Errorf("%s/%s: lint %s on %q in %q", res.Root, d, h.Rule, h.Span, s)
				}
			}
		}
	}
	if n < 60 {
		t.Fatalf("vacuous: %d texts linted", n)
	}
}
