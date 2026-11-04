package integration_test

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// for_each and compose sweep axis tokens over expression-capable
// extension params (U79 E1-S5): the runtime (the extension registry's
// ScalarParamsByOperator) and predict (the instance snapshot) hand the
// same visible flagged keys to the expansions, so an axis `{{token}}`
// inside one is PULSE_FOR_EACH_EXPR_SPLICE / PULSE_SWEEP_EXPR_SPLICE —
// slot extension_param — with the built-in slots' details, identically
// on Process, Compose, ComposeParallel and their predicts; an unflagged
// key keeps interpolating, and an extension a feature profile hides
// flags nothing.

// composeRunners are the two Compose runtimes.
func composeRunners(p *pulse.Pulse) map[string]func(*pulse.ComposedRequest) (*pulse.ComposedResponse, error) {
	ctx := context.Background()
	return map[string]func(*pulse.ComposedRequest) (*pulse.ComposedResponse, error){
		"Compose": func(r *pulse.ComposedRequest) (*pulse.ComposedResponse, error) { return p.Compose(ctx, r) },
		"ComposeParallel": func(r *pulse.ComposedRequest) (*pulse.ComposedResponse, error) {
			return p.ComposeParallel(ctx, r, pulse.ComposeOptions{MaxWorkers: 2, FailFast: true})
		},
	}
}

// requireAxisSplice asserts err is code with the splice details at field.
func requireAxisSplice(t *testing.T, what string, err error, code perr.Code, field, placeholder string) *perr.CodedError {
	t.Helper()
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) || ce.Code != code {
		t.Fatalf("%s err = %v, want %s", what, err, code)
	}
	if ce.Details["field"] != field || ce.Details["placeholder"] != placeholder || ce.Details["slot"] != "extension_param" {
		t.Fatalf("%s details = %v, want field %s placeholder %s slot extension_param", what, ce.Details, field, placeholder)
	}
	return ce
}

// requirePredictSame asserts predict's first error is ce, field for field.
func requirePredictSame(t *testing.T, what string, errs []*descriptor.EnvelopeEntry, ce *perr.CodedError) {
	t.Helper()
	if len(errs) == 0 {
		t.Fatalf("%s: predict raised nothing, runtime refused %s", what, ce.Message)
	}
	got := errs[0]
	if got.Code != string(ce.Code) || got.Message != ce.Message || jsonOf(got.Details) != jsonOf(ce.Details) {
		t.Fatalf("%s: predict %s %q %s, runtime %s %q %s", what, got.Code, got.Message, jsonOf(got.Details), ce.Code, ce.Message, jsonOf(ce.Details))
	}
}

// sweepBatch is a compose batch whose sweep over w (1, 2) renders req.
func sweepBatch(t *testing.T, req *types.Request) *pulse.ComposedRequest {
	t.Helper()
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	return &pulse.ComposedRequest{Sweep: &types.SweepSpec{Axes: []types.SweepAxis{{Name: "w", Values: []any{1, 2}}}, Request: raw}}
}

// TestSweepExprSplice_ExtensionParamEveryCategory: per extension
// category, a sweep axis token in the flagged `scale` is refused
// PULSE_SWEEP_EXPR_SPLICE by Compose and ComposeParallel, and
// PredictCompose raises the identical error; the same token in the
// unflagged `tag` predicts clean and runs.
func TestSweepExprSplice_ExtensionParamEveryCategory(t *testing.T) {
	t.Parallel()

	p, cohort := openE2E(t, exprExtensions(&paramCapture{}))
	ctx := context.Background()
	for _, tc := range strictCases() {
		t.Run(tc.category, func(t *testing.T) {
			bad := func() *pulse.ComposedRequest {
				return sweepBatch(t, withReqParams(tc.request(cohort, json.RawMessage(`{"scale":"params.k * {{w}}"}`))))
			}
			field := "sweep.request." + tc.slot + ".params.scale"
			var ce *perr.CodedError
			for name, run := range composeRunners(p) {
				_, err := run(bad())
				ce = requireAxisSplice(t, name, err, perr.PULSE_SWEEP_EXPR_SPLICE, field, "w")
			}
			env, err := p.PredictCompose(ctx, bad())
			if err != nil {
				t.Fatalf("PredictCompose: %v", err)
			}
			requirePredictSame(t, "PredictCompose", env.Errors, ce)

			ok := func() *pulse.ComposedRequest {
				return sweepBatch(t, withReqParams(tc.request(cohort, json.RawMessage(`{"scale":2,"tag":"t_{{w}}"}`))))
			}
			if env, err := p.PredictCompose(ctx, ok()); err != nil || len(env.Errors) > 0 {
				t.Fatalf("PredictCompose (unflagged key) = %v %s", err, jsonOf(env.Errors))
			}
			for name, run := range composeRunners(p) {
				resp, err := run(ok())
				if err != nil || len(resp.Responses) != 2 {
					t.Fatalf("%s (unflagged key) = %v", name, err)
				}
			}
		})
	}
}

// forEachCases are the categories an item for_each reaches, each
// fanning its extension item out over k (2, 3) with a k-varying label
// and nothing downstream reading that label.
func forEachCases() []strictCase {
	fe := &types.ForEach{Axes: []types.SweepAxis{{Name: "k", Values: []any{2, 3}}}}
	count := []*types.Aggregation{{Type: types.AGG_COUNT, Field: "score", Label: "n"}}
	var out []strictCase
	for _, tc := range strictCases() {
		build := tc.request
		switch tc.category {
		case "attribute":
			out = append(out, strictCase{tc.category, tc.slot, func(c *types.Cohort, p json.RawMessage) *types.Request {
				r := build(c, p)
				r.Attributes[0].ForEach, r.Attributes[0].Label, r.Aggregations = fe, "a{{k}}", count
				return r
			}})
		case "feature":
			out = append(out, strictCase{tc.category, tc.slot, func(c *types.Cohort, p json.RawMessage) *types.Request {
				r := build(c, p)
				r.Features[0].ForEach, r.Features[0].Label, r.Aggregations = fe, "f{{k}}", count
				return r
			}})
		case "window":
			out = append(out, strictCase{tc.category, tc.slot, func(c *types.Cohort, p json.RawMessage) *types.Request {
				r := build(c, p)
				r.Windows[0].ForEach, r.Windows[0].Label = fe, "w{{k}}"
				return r
			}})
		}
	}
	return out
}

// TestForEachExprSplice_ExtensionParamEveryCategory: per category an
// item for_each reaches, an axis token in the flagged `scale` is
// refused PULSE_FOR_EACH_EXPR_SPLICE by Process (Predict identical) and,
// as a Compose slot, by Compose and ComposeParallel (PredictCompose
// identical); the same token in the unflagged `tag` predicts valid and
// runs.
func TestForEachExprSplice_ExtensionParamEveryCategory(t *testing.T) {
	t.Parallel()

	p, cohort := openE2E(t, exprExtensions(&paramCapture{}))
	ctx := context.Background()
	cases := forEachCases()
	if len(cases) != 3 {
		t.Fatalf("for_each cases = %d, want attribute, feature, window", len(cases))
	}
	for _, tc := range cases {
		t.Run(tc.category, func(t *testing.T) {
			bad := func() *types.Request {
				return withReqParams(tc.request(cohort, json.RawMessage(`{"scale":"params.k * {{k}}"}`)))
			}
			field := tc.slot + ".params.scale"
			_, err := p.Process(ctx, bad())
			ce := requireAxisSplice(t, "Process", err, perr.PULSE_FOR_EACH_EXPR_SPLICE, field, "k")
			res, err := p.Predict(ctx, bad())
			if err != nil {
				t.Fatalf("Predict: %v", err)
			}
			if res.Valid {
				t.Fatal("Predict valid, Process refused")
			}
			requirePredictSame(t, "Predict", res.Errors, ce)

			batch := func() *pulse.ComposedRequest {
				r := bad()
				r.Label = "fe"
				return &pulse.ComposedRequest{Requests: []*pulse.Request{r}}
			}
			for name, run := range composeRunners(p) {
				_, err := run(batch())
				ce = requireAxisSplice(t, name, err, perr.PULSE_FOR_EACH_EXPR_SPLICE, field, "k")
			}
			env, err := p.PredictCompose(ctx, batch())
			if err != nil {
				t.Fatalf("PredictCompose: %v", err)
			}
			requirePredictSame(t, "PredictCompose", env.Errors, ce)

			ok := func() *types.Request {
				return withReqParams(tc.request(cohort, json.RawMessage(`{"scale":2,"tag":"t_{{k}}"}`)))
			}
			if res, err := p.Predict(ctx, ok()); err != nil || !res.Valid {
				t.Fatalf("Predict (unflagged key) = %v %s", err, jsonOf(res.Errors))
			}
			if _, err := p.Process(ctx, ok()); err != nil {
				t.Fatalf("Process (unflagged key) = %v", err)
			}
		})
	}
}

// TestAxisExprSplice_ExtensionParamHidden: an extension a feature
// profile hides acts never-registered, so its flagged key is not an
// expression slot on that instance: neither expansion refuses the token
// as a splice, and predict and the runtime agree on the refusal that
// remains (the hidden operator itself).
func TestAxisExprSplice_ExtensionParamHidden(t *testing.T) {
	t.Parallel()

	fsys := afero.NewMemMapFs()
	writeParityCohort(t, fsys, "e2e.pulse", paritySchema(t), 0, paritySmallRows)
	fp := &pulse.FeatureProfile{Features: []string{"capability:process", "capability:compose", "capability:compose_sweep",
		"capability:for_each", "AGG_COUNT", "AGG_SUM"}}
	p, err := pulse.New(pulse.Options{FS: fsys, Extensions: exprExtensions(&paramCapture{}), FeatureProfile: fp})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	cohort := &types.Cohort{Filename: "e2e.pulse"}
	ctx := context.Background()
	notSplice := func(what string, err error) *perr.CodedError {
		t.Helper()
		var ce *perr.CodedError
		if !stderrors.As(err, &ce) {
			t.Fatalf("%s err = %v, want a coded refusal of the hidden operator", what, err)
		}
		if ce.Code == perr.PULSE_SWEEP_EXPR_SPLICE || ce.Code == perr.PULSE_FOR_EACH_EXPR_SPLICE {
			t.Fatalf("%s refused %s: a hidden extension flags nothing", what, ce.Code)
		}
		return ce
	}

	agg := strictCases()[0]
	batch := func() *pulse.ComposedRequest {
		return sweepBatch(t, withReqParams(agg.request(cohort, json.RawMessage(`{"scale":"params.k * {{w}}"}`))))
	}
	var ce *perr.CodedError
	for name, run := range composeRunners(p) {
		_, err := run(batch())
		ce = notSplice(name, err)
	}
	env, err := p.PredictCompose(ctx, batch())
	if err != nil {
		t.Fatalf("PredictCompose: %v", err)
	}
	if len(env.Errors) == 0 || env.Errors[0].Code != string(ce.Code) {
		t.Fatalf("PredictCompose errors %s, runtime %s", jsonOf(env.Errors), ce.Code)
	}

	// The feature case by category, not position: the window case's
	// `scale` is also a built-in window_param slot, which would refuse
	// the splice whatever the extension's visibility.
	var feat strictCase
	for _, tc := range forEachCases() {
		if tc.category == "feature" {
			feat = tc
		}
	}
	if feat.request == nil {
		t.Fatal("forEachCases has no feature case")
	}
	item := func() *types.Request {
		return withReqParams(feat.request(cohort, json.RawMessage(`{"scale":"params.k * {{k}}"}`)))
	}
	_, err = p.Process(ctx, item())
	ce = notSplice("Process", err)
	res, err := p.Predict(ctx, item())
	if err != nil {
		t.Fatalf("Predict: %v", err)
	}
	if res.Valid || len(res.Errors) == 0 || res.Errors[0].Code != string(ce.Code) {
		t.Fatalf("Predict errors %s, runtime %s", jsonOf(res.Errors), ce.Code)
	}
}

// TestAxisExprSplice_ExtensionTemplatedType: an operator `type` that is
// itself an axis placeholder names no operator before substitution, so
// both expansions judge the params key against every visible operator:
// an axis token in the flagged `scale` is refused (sweep: Compose,
// ComposeParallel; for_each: Process) with details.type_templated, and
// predict raises the identical error; the unflagged `tag` expands.
func TestAxisExprSplice_ExtensionTemplatedType(t *testing.T) {
	t.Parallel()

	p, cohort := openE2E(t, exprExtensions(&paramCapture{}))
	ctx := context.Background()
	sweepJSON := func(params string) *pulse.ComposedRequest {
		var c pulse.ComposedRequest
		raw := `{"params":{"k":3},"sweep":{"axes":[{"name":"w","values":["AGG_ACME_STRICT"]}],"request":{"cohort":{"filename":"` +
			cohort.Filename + `"},"aggregations":[{"type":"{{w}}","field":"score","label":"v","params":` + params + `}]}}}`
		if err := json.Unmarshal([]byte(raw), &c); err != nil {
			t.Fatal(err)
		}
		return &c
	}
	var ce *perr.CodedError
	for name, run := range composeRunners(p) {
		_, err := run(sweepJSON(`{"scale":"params.k * {{w}}"}`))
		ce = requireAxisSplice(t, name, err, perr.PULSE_SWEEP_EXPR_SPLICE, "sweep.request.aggregations[0].params.scale", "w")
		if ce.Details["type_templated"] != true {
			t.Fatalf("%s details %v, want type_templated", name, ce.Details)
		}
	}
	env, err := p.PredictCompose(ctx, sweepJSON(`{"scale":"params.k * {{w}}"}`))
	if err != nil {
		t.Fatalf("PredictCompose: %v", err)
	}
	requirePredictSame(t, "PredictCompose", env.Errors, ce)
	if env, err := p.PredictCompose(ctx, sweepJSON(`{"scale":2,"tag":"t_{{w}}"}`)); err != nil || len(env.Errors) > 0 {
		t.Fatalf("PredictCompose (unflagged key) = %v %s", err, jsonOf(env.Errors))
	}

	item := func(params string) *types.Request {
		r := withReqParams(&types.Request{Cohort: cohort, Aggregations: []*types.Aggregation{{
			Type: "AGG_ACME_{{x}}", Field: "score", Label: "v{{x}}", Params: json.RawMessage(params),
			ForEach: &types.ForEach{Axes: []types.SweepAxis{{Name: "x", Values: []any{"STRICT"}}}},
		}}})
		return r
	}
	_, err = p.Process(ctx, item(`{"scale":"{{x}}"}`))
	ce = requireAxisSplice(t, "Process", err, perr.PULSE_FOR_EACH_EXPR_SPLICE, "aggregations[0].params.scale", "x")
	if ce.Details["type_templated"] != true {
		t.Fatalf("Process details %v, want type_templated", ce.Details)
	}
	res, err := p.Predict(ctx, item(`{"scale":"{{x}}"}`))
	if err != nil || res.Valid {
		t.Fatalf("Predict = valid %v, %v; Process refused", res.Valid, err)
	}
	requirePredictSame(t, "Predict", res.Errors, ce)
	if res, err := p.Predict(ctx, item(`{"scale":2,"tag":"t_{{x}}"}`)); err != nil || !res.Valid {
		t.Fatalf("Predict (unflagged key) = %v %s", err, jsonOf(res.Errors))
	}
	if _, err := p.Process(ctx, item(`{"scale":2,"tag":"t_{{x}}"}`)); err != nil {
		t.Fatalf("Process (unflagged key) = %v", err)
	}
}

// TestExplain_ExtensionSpliceCaveat: Explain expands for_each items with
// the instance's flagged keys (InstanceSnapshot.SweepOptions), so a
// request a splice ban would refuse carries the caveat naming
// PULSE_FOR_EACH_EXPR_SPLICE — without a cohort (the expansion alone)
// and with one (predict's refusal relayed under Refusals too); a Compose
// sweep token is predict's PULSE_SWEEP_EXPR_SPLICE refusal. The same
// token in the unflagged `tag` raises no such caveat.
func TestExplain_ExtensionSpliceCaveat(t *testing.T) {
	t.Parallel()

	p, cohort := openE2E(t, exprExtensions(&paramCapture{}))
	ctx := context.Background()
	// The feature item: `scale` is no built-in slot there (it is on a
	// window), so only the extension's flag can refuse it.
	var feat strictCase
	for _, tc := range forEachCases() {
		if tc.category == "feature" {
			feat = tc
		}
	}
	item := func(params string) *types.Request {
		return withReqParams(feat.request(cohort, json.RawMessage(params)))
	}
	hasCaveat := func(res *descriptor.ExplainResult, sub string) bool {
		for _, c := range res.Caveats {
			if strings.Contains(c, sub) {
				return true
			}
		}
		return false
	}
	hasRefusal := func(res *descriptor.ExplainResult, code perr.Code) bool {
		for _, r := range res.Refusals {
			if r.Code == string(code) {
				return true
			}
		}
		return false
	}
	const notExpand = "does not expand (" + string(perr.PULSE_FOR_EACH_EXPR_SPLICE) + ")"

	t.Run("no cohort", func(t *testing.T) {
		req := item(`{"scale":"params.k * {{k}}"}`)
		req.Cohort = nil
		res, err := p.Explain(ctx, descriptor.ExplainRequest{Request: req})
		if err != nil {
			t.Fatal(err)
		}
		if !hasCaveat(res, notExpand) {
			t.Fatalf("caveats %q do not name the splice refusal", res.Caveats)
		}
	})
	t.Run("bound", func(t *testing.T) {
		res, err := p.Explain(ctx, descriptor.ExplainRequest{Request: item(`{"scale":"params.k * {{k}}"}`)})
		if err != nil {
			t.Fatal(err)
		}
		if res.Valid == nil || *res.Valid || !hasRefusal(res, perr.PULSE_FOR_EACH_EXPR_SPLICE) || !hasCaveat(res, notExpand) ||
			!hasCaveat(res, "with "+string(perr.PULSE_FOR_EACH_EXPR_SPLICE)) {
			t.Fatalf("valid %v refusals %s caveats %q, want the splice refusal and caveats", res.Valid, jsonOf(res.Refusals), res.Caveats)
		}
	})
	t.Run("compose sweep", func(t *testing.T) {
		agg := strictCases()[0]
		batch := sweepBatch(t, withReqParams(agg.request(cohort, json.RawMessage(`{"scale":"params.k * {{w}}"}`))))
		res, err := p.Explain(ctx, descriptor.ExplainRequest{Composed: batch})
		if err != nil {
			t.Fatal(err)
		}
		if !hasRefusal(res, perr.PULSE_SWEEP_EXPR_SPLICE) || !hasCaveat(res, "with "+string(perr.PULSE_SWEEP_EXPR_SPLICE)) {
			t.Fatalf("refusals %s caveats %q, want PULSE_SWEEP_EXPR_SPLICE", jsonOf(res.Refusals), res.Caveats)
		}
	})
	t.Run("unflagged key", func(t *testing.T) {
		req := item(`{"scale":2,"tag":"t_{{k}}"}`)
		req.Cohort = nil
		res, err := p.Explain(ctx, descriptor.ExplainRequest{Request: req})
		if err != nil {
			t.Fatal(err)
		}
		if hasCaveat(res, "does not expand") {
			t.Fatalf("caveats %q: an unflagged key expands", res.Caveats)
		}
	})
}
