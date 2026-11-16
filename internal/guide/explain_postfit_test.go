package guide

import (
	"encoding/json"
	"math"
	"slices"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/postfit"
	"github.com/frankbardon/pulse/types"
)

// postFitOp is the fixture's registered post-fit operator.
const postFitOp = "POSTFIT_ACME_MROI"

// postFitInstance is an unscoped instance registering postFitOp with
// declared params channel (required) and spend, and a Purpose.
func postFitInstance() *descx.InstanceSnapshot {
	params := []descriptor.OperatorParamMeta{
		{Name: "channel", JSONType: "string", Required: true},
		{Name: "spend", JSONType: "array"},
	}
	ext := &descx.ExtensionsSnapshot{
		PostFits: []descriptor.OperatorMeta{{Name: postFitOp, Namespace: "ACME", Description: "marginal-ROI grid", Params: params}},
		PostFitDecls: map[types.PostFitType]postfit.Decl{postFitOp: {
			Name: postFitOp, Params: params,
			Outputs: extend.PostFitSchema{Scalars: []string{"beta"}},
		}},
		Purposes: map[string]descriptor.Purpose{postFitOp: {Plain: "Shows what one more unit of spend returns at each level."}},
	}
	return descx.NewInstanceSnapshot(ext, descx.FeatureSet{Enabled: append(descx.FeatureNames(), postFitOp)})
}

// postFitBatch is a Compose batch over the fixture cohort: one counting
// slot `s`, two regression fits `m` and `k`, and the post_fit entries
// raw (a JSON array).
func postFitBatch(t *testing.T, raw string) *types.ComposedRequest {
	t.Helper()
	var c types.ComposedRequest
	fit := `{"cohort":{"filename":"fixture.pulse"},"regressions":[{"type":"REG_OLS","target":"score","predictors":["hours"]}]}`
	body := `{"requests":[{"label":"s","cohort":{"filename":"fixture.pulse"},"aggregations":[{"type":"AGG_COUNT","field":"score","label":"n"}]}],
		"fits":[{"name":"m","request":` + fit + `},{"name":"k","request":` + fit + `}],"post_fit":` + raw + `}`
	if err := json.Unmarshal([]byte(body), &c); err != nil {
		t.Fatal(err)
	}
	return &c
}

// TestExplainPostFit_StepPerEntry: one step per post_fit entry at
// `post_fit[i]`, after the slots, naming the operator (its Purpose),
// the label, the fits it reads, its params and the phase; the summary
// counts the entries.
func TestExplainPostFit_StepPerEntry(t *testing.T) {
	t.Parallel()

	inst := postFitInstance()
	c := postFitBatch(t, `[{"label":"grid","type":"`+postFitOp+`","fits":["m","k"],"params":{"channel":"tv","spend":[10,20,30]}},
		{"label":"bare","type":"`+postFitOp+`","params":{"channel":"radio"}}]`)
	res := explain(t, inst, descriptor.ExplainRequest{Composed: c}, explainChecks(t, inst))
	if res.Valid == nil || !*res.Valid {
		t.Fatalf("batch refused: %+v", res.Refusals)
	}
	slots := stepSlots(res)
	if i := slices.Index(slots, "post_fit[0]"); i < 0 || slots[len(slots)-1] != "post_fit[1]" || i < slices.Index(slots, "requests[0].aggregations[0]") {
		t.Fatalf("post-fit steps not last in order: %v", slots)
	}
	var st descriptor.ExplainStep
	for _, s := range res.Steps {
		if s.Slot == "post_fit[0]" {
			st = s
		}
	}
	if st.Operator != postFitOp {
		t.Errorf("post_fit[0] operator = %q", st.Operator)
	}
	for _, want := range []string{
		"Post-fit entry 1 runs " + postFitOp, "labelled `grid`", "over the fits `m` and `k`",
		"the params `channel` = \"tv\" and `spend` (3 values)",
		"Shows what one more unit of spend returns at each level.",
		"after the fits and optimize blocks", "reads no record", "`post_fit[0]`",
	} {
		if !strings.Contains(st.Text, want) {
			t.Errorf("post_fit[0] text lacks %q:\n%s", want, st.Text)
		}
	}
	if s := stepAt(t, res, "post_fit[1]"); !strings.Contains(s, "reads no fit") || !strings.Contains(s, "`channel` = \"radio\"") {
		t.Errorf("post_fit[1] = %q", s)
	}
	if !strings.Contains(res.Summary, "2 post-fit entries") {
		t.Errorf("summary = %q", res.Summary)
	}
}

// TestExplainPostFit_UnregisteredType: an entry naming an operator the
// instance does not register is said to, with no Operator claimed, and
// predict's PULSE_POSTFIT_INVALID refusal is located at its slot.
func TestExplainPostFit_UnregisteredType(t *testing.T) {
	t.Parallel()

	inst := postFitInstance()
	c := postFitBatch(t, `[{"label":"x","type":"POSTFIT_ACME_NOPE","fits":["m"]}]`)
	res := explain(t, inst, descriptor.ExplainRequest{Composed: c}, explainChecks(t, inst))
	for _, s := range res.Steps {
		if s.Slot == "post_fit[0]" && (s.Operator != "" || !strings.Contains(s.Text, "does not register")) {
			t.Errorf("unregistered step = %+v", s)
		}
	}
	if res.Valid == nil || *res.Valid || len(res.Refusals) != 1 {
		t.Fatalf("refusals %+v", res.Refusals)
	}
	if r := res.Refusals[0]; r.Code != string(errors.PULSE_POSTFIT_INVALID) || r.Details["slot"] != "post_fit[0]" {
		t.Errorf("refusal %+v", r)
	}
}

// TestExplainPostFit_RefusalsLocated: every post-fit refusal predict
// raises — structural and the declared-params check — names the
// entry's `post_fit[i]` slot, with predict's code kept.
func TestExplainPostFit_RefusalsLocated(t *testing.T) {
	t.Parallel()

	ok := `{"label":"a","type":"` + postFitOp + `","fits":["m"],"params":{"channel":"tv"}}`
	cases := []struct {
		name, raw, code, slot string
	}{
		{"unknown fit", `[` + ok + `,{"label":"b","type":"` + postFitOp + `","fits":["zz"],"params":{"channel":"tv"}}]`, string(errors.PULSE_POSTFIT_INVALID), "post_fit[1]"},
		{"duplicate label", `[` + ok + `,` + ok + `]`, string(errors.PULSE_POSTFIT_INVALID), "post_fit[1]"},
		{"unknown param", `[{"label":"a","type":"` + postFitOp + `","params":{"channel":"tv","nope":1}}]`, string(errors.PULSE_REQUEST_UNKNOWN_FIELD), "post_fit[0]"},
		{"missing required param", `[` + ok + `,{"label":"b","type":"` + postFitOp + `"}]`, string(errors.PULSE_REQUEST_PARAM_INVALID), "post_fit[1]"},
	}
	inst := postFitInstance()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := postFitBatch(t, tc.raw)
			env := descx.ValidateComposeWithOptions(c, &descx.PredictOptions{Instance: inst})
			res := explain(t, inst, descriptor.ExplainRequest{Composed: c}, explainChecks(t, inst))
			if res.Valid == nil || *res.Valid || len(res.Refusals) != len(env.Errors) || len(res.Refusals) == 0 {
				t.Fatalf("explain refusals %+v, predict %+v", res.Refusals, env.Errors)
			}
			r := res.Refusals[0]
			if r.Code != tc.code || r.Code != env.Errors[0].Code {
				t.Fatalf("code %s, want %s (predict %s)", r.Code, tc.code, env.Errors[0].Code)
			}
			if r.Details["slot"] != tc.slot {
				t.Errorf("slot %v, want %s (details %v)", r.Details["slot"], tc.slot, r.Details)
			}
			if !strings.Contains(strings.Join(res.Caveats, "\n"), "Predict refuses "+tc.slot+" with "+tc.code) {
				t.Errorf("caveats do not name %s: %v", tc.slot, res.Caveats)
			}
		})
	}
}

// postFitResponse carries a successful entry (scalars, one undefined,
// and a table) and a failed one.
func postFitResponse() *types.ComposedResponse {
	return &types.ComposedResponse{PostFit: []types.PostFitResult{
		{Label: "grid", Type: postFitOp,
			Scalars: map[string]float64{"beta": 8.25, "at_reference": math.NaN()},
			Tables: map[string]*types.PostFitTable{"grid": {Columns: []string{"spend", "marginal_roi"},
				Rows: [][]any{{10.0, 0.5}, {20.0, 0.25}, {30.0, nil}}}}},
		{Label: "bad", Type: postFitOp, Error: &types.SlotError{Code: string(errors.PULSE_POSTFIT_FAILED), Message: "no hill curve"}},
	}}
}

// TestExplainPostFit_Response: one finding per ComposedResponse.PostFit
// entry at `post_fit[i]` — descriptive with its scalars (undefined as
// null) and each table's size, a failed entry not_computable with its
// code in a caveat — and the summary counts them.
func TestExplainPostFit_Response(t *testing.T) {
	t.Parallel()

	res := explain(t, nil, descriptor.ExplainRequest{ComposedResponse: postFitResponse(), Detail: descriptor.ExplainFull}, Checks{})
	if len(res.Findings) != 2 {
		t.Fatalf("findings %+v", res.Findings)
	}
	ok, bad := res.Findings[0], res.Findings[1]
	if ok.Slot != "post_fit[0]" || ok.Subject != "grid" || ok.Verdict != descriptor.VerdictDescriptive {
		t.Errorf("finding 0 = %s %s %s", ok.Slot, ok.Subject, ok.Verdict)
	}
	if bad.Slot != "post_fit[1]" || bad.Verdict != descriptor.VerdictNotComputable {
		t.Errorf("finding 1 = %s %s", bad.Slot, bad.Verdict)
	}
	if v := ok.Numbers["scalars.beta"]; v == nil || *v != 8.25 {
		t.Errorf("beta = %v", v)
	}
	if v, present := ok.Numbers["scalars.at_reference"]; !present || v != nil {
		t.Errorf("undefined scalar not null: present %v", present)
	}
	if v := ok.Numbers["tables.grid.rows"]; v == nil || *v != 3 {
		t.Errorf("grid rows = %v", v)
	}
	all := strings.Join(res.Sentences, "\n")
	for _, s := range []string{"Post-fit entry `grid` (" + postFitOp + ")", "`at_reference` undefined", "`beta` = 8.25",
		"the table `grid` of 3 rows by 2 columns (`spend` and `marginal_roi`)"} {
		if !strings.Contains(all, s) {
			t.Errorf("sentences lack %q:\n%s", s, all)
		}
	}
	if !strings.Contains(strings.Join(res.Caveats, "\n"), "`bad` failed with "+string(errors.PULSE_POSTFIT_FAILED)) {
		t.Errorf("caveats %v", res.Caveats)
	}
	if !strings.Contains(res.Summary, "2 post-fit results") {
		t.Errorf("summary = %q", res.Summary)
	}
}

// TestExplainPostFit_ProseLint: every post-fit sentence passes the
// guidance lint, request and response mode, both details.
func TestExplainPostFit_ProseLint(t *testing.T) {
	t.Parallel()

	inst := postFitInstance()
	reqs := []descriptor.ExplainRequest{
		{Composed: postFitBatch(t, `[{"label":"grid","type":"`+postFitOp+`","fits":["m"],"params":{"channel":"tv","spend":[1,2]}},{"label":"x","type":"POSTFIT_ACME_NOPE"}]`)},
		{ComposedResponse: postFitResponse()},
	}
	n := 0
	for _, req := range reqs {
		for _, d := range []descriptor.ExplainDetail{descriptor.ExplainTerse, descriptor.ExplainFull} {
			req.Detail = d
			res := explain(t, inst, req, explainChecks(t, inst))
			for _, s := range explainTexts(res) {
				n++
				for _, h := range descx.LintGuidanceText(s) {
					t.Errorf("%s/%s: lint %s on %q in %q", res.Root, d, h.Rule, h.Span, s)
				}
			}
		}
	}
	if n == 0 {
		t.Fatal("vacuous: no text linted")
	}
}
