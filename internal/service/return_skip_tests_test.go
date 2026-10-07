package service

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// U18 ranks 6–7 (E3-S2): the regressions slot and the tests /
// post_tests slots a `return` selection excludes are never computed —
// no row test is built (processing.WorkStats RowTestFolds), no post
// test runs (PostTestRuns), no regression is fitted (RegressionFits),
// and none of their refusals or warnings is raised — unless a resolved
// multiplicity family claims a test entry, which then still computes so
// every kept p_adjusted is the full run's. The skip happens inside the
// arm: the merge gate still sees the tests on the original request.

var excludeInferential = &types.Return{Exclude: []string{"tests", "post_tests", "regressions"}}

func inferentialWork(t *testing.T, run func()) (folds, posts, fits int64) {
	t.Helper()
	before := processing.WorkStats()
	run()
	d := processing.WorkStats().Sub(before)
	return d.RowTestFolds, d.PostTestRuns, d.RegressionFits
}

func olsSpec(target string, predictors ...string) []*types.RegressionSpec {
	return []*types.RegressionSpec{{Type: types.REG_OLS, Target: target, Predictors: predictors}}
}

// inferentialArm is one serial arm: its request (tests, post-tests and
// regressions) and whether it streams. Row tests and a regression never
// stream together (canStream), so the streaming arm splits in two.
type inferentialArm struct {
	name   string
	stream bool
	mk     func() *types.Request
}

func inferentialArms() []inferentialArm {
	base := func() *types.Request {
		return &types.Request{
			Cohort:       &types.Cohort{Filename: "m.pulse"},
			Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "x", Label: "n"}},
		}
	}
	return []inferentialArm{
		{name: "streaming_tests", stream: true, mk: func() *types.Request { r := base(); r.Tests = multTests(true); return r }},
		{name: "streaming_regression", stream: true, mk: func() *types.Request { r := base(); r.Regressions = olsSpec("y", "x"); return r }},
		{name: "buffered", mk: inferentialRequest},
	}
}

// inferentialRequest: grouped (the buffered arm) with every inferential
// slot — tests, a post-test over the 8 group rows and an OLS fit.
func inferentialRequest() *types.Request {
	return &types.Request{
		Cohort:       &types.Cohort{Filename: "m.pulse"},
		Groups:       []*types.Group{{Type: types.GROUP_RANGE, Field: "id", Interval: 10}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_AVERAGE, Field: "x", Label: "ax"}},
		Tests:        multTests(false),
		PostTests:    []*types.Test{{Type: types.TEST_TREND, Field: "ax", OrderBy: []types.OrderKey{{Field: "id"}}, Label: "trend"}},
		Regressions:  olsSpec("y", "x"),
	}
}

// TestReturnSkipsComputation_TestsAndRegressions: the serial streaming
// and buffered arms. Excluded: zero builds and the slot nil, every
// other part byte-identical; each slot excluded alone skips only
// itself. Absent: every entry computed (the non-vacuous control).
// Compose slots follow their own `return` (serial and parallel); a
// chain stage refuses the slots outright and still does when excluded —
// the refusal reads the original request.
func TestReturnSkipsComputation_TestsAndRegressions(t *testing.T) {
	_, svc := overlayFoldService(t)
	for _, arm := range inferentialArms() {
		t.Run(arm.name, func(t *testing.T) {
			if processing.CanStreamRequest(arm.mk(), multSchema()) != arm.stream {
				t.Fatal("fixture runs the other arm")
			}
			var base *types.Response
			folds, posts, fits := inferentialWork(t, func() { base = mustProcess(t, svc, arm.mk()) })
			if folds != int64(len(base.Tests)) || posts != int64(len(base.PostTests)) || fits != int64(len(base.Regressions)) || folds+posts+fits == 0 {
				t.Fatalf("return absent: folds=%d posts=%d fits=%d; the fixture proves nothing", folds, posts, fits)
			}
			for _, c := range []struct {
				exclude            []string
				folds, posts, fits bool // computed
			}{
				{exclude: []string{"tests", "post_tests", "regressions"}},
				{exclude: []string{"tests"}, posts: true, fits: true},
				{exclude: []string{"post_tests"}, folds: true, fits: true},
				{exclude: []string{"regressions"}, folds: true, posts: true},
			} {
				req := arm.mk()
				req.Return = &types.Return{Exclude: c.exclude}
				var shaped *types.Response
				f, p, r := inferentialWork(t, func() { shaped = mustProcess(t, svc, req) })
				if f != map[bool]int64{true: folds}[c.folds] || p != map[bool]int64{true: posts}[c.posts] || r != map[bool]int64{true: fits}[c.fits] {
					t.Errorf("exclude %v: folds=%d posts=%d fits=%d", c.exclude, f, p, r)
				}
				want := *base
				if !c.folds {
					want.Tests = nil
				}
				if !c.posts {
					want.PostTests = nil
				}
				if !c.fits {
					want.Regressions = nil
				}
				if !bytes.Equal(mustMarshal(t, &want), mustMarshal(t, shaped)) {
					t.Errorf("exclude %v: a kept part moved\n got  %s\n want %s", c.exclude, mustMarshal(t, shaped), mustMarshal(t, &want))
				}
			}
		})
	}

	t.Run("compose", func(t *testing.T) {
		compose := func(ret *types.Return) *types.ComposedRequest {
			c := &types.ComposedRequest{}
			for i := 0; i < 2; i++ {
				r := inferentialRequest()
				r.Label = []string{"a", "b"}[i]
				r.Return = ret
				c.Requests = append(c.Requests, r)
			}
			return c
		}
		for _, parallel := range []bool{false, true} {
			run := func(ret *types.Return) *types.ComposedResponse {
				var (
					out *types.ComposedResponse
					err error
				)
				if parallel {
					out, err = svc.ComposeParallel(context.Background(), compose(ret), ComposeOptions{MaxWorkers: 2})
				} else {
					out, err = svc.Compose(context.Background(), compose(ret))
				}
				if err != nil {
					t.Fatalf("compose: %v", err)
				}
				return out
			}
			var base, shaped *types.ComposedResponse
			folds, posts, fits := inferentialWork(t, func() { base = run(nil) })
			if folds == 0 || posts != 2 || fits != 2 {
				t.Fatalf("parallel=%v return absent: folds=%d posts=%d fits=%d", parallel, folds, posts, fits)
			}
			if folds, posts, fits = inferentialWork(t, func() { shaped = run(excludeInferential) }); folds+posts+fits != 0 {
				t.Errorf("parallel=%v excluded: folds=%d posts=%d fits=%d; want 0", parallel, folds, posts, fits)
			}
			for i := range base.Responses {
				want := *base.Responses[i]
				want.Tests, want.PostTests, want.Regressions = nil, nil, nil
				if !bytes.Equal(mustMarshal(t, &want), mustMarshal(t, shaped.Responses[i])) {
					t.Errorf("parallel=%v slot %d: a kept part moved", parallel, i)
				}
			}
		}
	})

	t.Run("chain_refusal_kept", func(t *testing.T) {
		stage := inferentialRequest()
		stage.Return = excludeInferential
		_, err := svc.ProcessChain(context.Background(), &types.ChainRequest{
			Cohort: &types.Cohort{Filename: "m.pulse"},
			Stages: []*types.ChainStage{{Name: "a", Request: stage}},
		})
		var ce *errors.CodedError
		if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_CHAIN_NOT_MERGEABLE {
			t.Errorf("excluded tests in a chain stage: err %v; want PULSE_CHAIN_NOT_MERGEABLE (the gate reads the original request)", err)
		}
	})
}

// TestReturnSkipsTests_MultiplicityVeto (FR-15): a `request` family
// pools the tests with the post-test, so excluding the tests still
// folds every MEMBER test (the one opting out with method none is
// skipped) and the kept post-test's p_adjusted and m are the full
// run's. Without the block the same selection folds nothing.
func TestReturnSkipsTests_MultiplicityVeto(t *testing.T) {
	_, svc := overlayFoldService(t)
	mk := func(ret *types.Return, block bool) *types.Request {
		r := inferentialRequest()
		r.Regressions = nil
		r.Tests[0].Multiplicity = &types.Multiplicity{Method: types.MultiplicityMethodNone}
		if block {
			r.Multiplicity = &types.Multiplicity{Method: types.MultiplicityMethodHolm, Family: types.MultiplicityFamilyRequest}
		}
		r.Return = ret
		return r
	}
	full := mustProcess(t, svc, mk(nil, true))
	if len(full.PostTests) != 1 || full.PostTests[0].PAdjusted == nil || full.PostTests[0].Multiplicity == nil || full.PostTests[0].Multiplicity.M < 3 {
		t.Fatalf("full run's post-test not corrected over a family: %+v", full.PostTests)
	}
	var shaped *types.Response
	exclTests := &types.Return{Exclude: []string{"tests"}}
	folds, _, _ := inferentialWork(t, func() { shaped = mustProcess(t, svc, mk(exclTests, true)) })
	if want := int64(len(full.Tests) - 1); folds != want {
		t.Errorf("multiplicity veto: %d test folds; want the %d members", folds, want)
	}
	if !bytes.Equal(mustMarshal(t, full.PostTests), mustMarshal(t, shaped.PostTests)) {
		t.Errorf("kept post-test moved under the tests skip\n got  %s\n want %s", mustMarshal(t, shaped.PostTests), mustMarshal(t, full.PostTests))
	}
	if len(shaped.Tests) != len(full.Tests) {
		t.Fatalf("shaped run holds %d test positions; want %d, index-aligned", len(shaped.Tests), len(full.Tests))
	}
	if shaped.Tests[0] != nil {
		t.Errorf("non-member test 0 was folded: %+v", shaped.Tests[0])
	}
	for i := 1; i < len(full.Tests); i++ {
		if !bytes.Equal(mustMarshal(t, full.Tests[i]), mustMarshal(t, shaped.Tests[i])) {
			t.Errorf("member test %d differs from the full run's", i)
		}
	}
	if folds, _, _ := inferentialWork(t, func() { mustProcess(t, svc, mk(exclTests, false)) }); folds != 0 {
		t.Errorf("no multiplicity block: %d test folds; want 0", folds)
	}
}

// TestReturnSkipsTests_ErrorRule pins FR-25 for ranks 6–7: an excluded
// regression or test is not computed, so the refusal it would raise
// (a rank-deficient fit, a non-numeric test field) is not raised and
// predict stays the validator; kept, the same request still refuses.
func TestReturnSkipsTests_ErrorRule(t *testing.T) {
	_, svc := overlayFoldService(t)
	for _, c := range []struct {
		name    string
		mut     func(*types.Request)
		exclude string
		code    errors.Code
	}{
		{name: "regression", exclude: "regressions", code: errors.PROCESSING_REGRESSION_RANK_DEFICIENT,
			mut: func(r *types.Request) { r.Regressions = olsSpec("y", "x", "x") }},
		{name: "test", exclude: "tests", code: errors.PULSE_TEST_FIELD_NOT_NUMERIC,
			mut: func(r *types.Request) {
				r.Tests = []*types.Test{{Type: types.TEST_T, Field: "g", Params: json.RawMessage(`{"mu":1}`)}}
			}},
	} {
		t.Run(c.name, func(t *testing.T) {
			build := func(ret *types.Return) *types.Request {
				r := &types.Request{Cohort: &types.Cohort{Filename: "m.pulse"},
					Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "x", Label: "n"}}, Return: ret}
				c.mut(r)
				return r
			}
			_, err := svc.Process(context.Background(), build(nil))
			var ce *errors.CodedError
			if !stderrors.As(err, &ce) || ce.Code != c.code {
				t.Fatalf("kept: err %v; want %s", err, c.code)
			}
			resp, err := svc.Process(context.Background(), build(&types.Return{Exclude: []string{c.exclude}}))
			if err != nil {
				t.Fatalf("excluded %s still refused: %v", c.exclude, err)
			}
			if resp.Tests != nil || resp.Regressions != nil || len(resp.Warnings) != 0 {
				t.Errorf("excluded %s left a trace: tests=%v regs=%v warnings=%v", c.exclude, resp.Tests, resp.Regressions, resp.Warnings)
			}
		})
	}
}

// TestReturnSkipsTests_MergeGateArmUnchanged: tests and regressions
// refuse shard fan-out (the merge gate), so the request runs the serial
// shard iterator; excluding them skips the fold but must not move the
// request onto the shard reducer. The work profile (inferential
// counters aside) tells the arms apart — the control with the slots
// REMOVED fans out and differs.
func TestReturnSkipsTests_MergeGateArmUnchanged(t *testing.T) {
	cfg := fs.NewMemMap()
	if err := afero.WriteFile(cfg.Fs(), "arch.pulse", multArchive(t, []int{30, 27, 33}), 0o644); err != nil {
		t.Fatal(err)
	}
	svc := New(cfg)
	svc.SetShardWorkers(2)
	mk := func(ret *types.Return, inferential bool) *types.Request {
		r := &types.Request{
			Cohort:       &types.Cohort{Filename: "arch.pulse"},
			Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "x", Label: "s"}},
			Return:       ret,
		}
		if inferential {
			r.Tests = multTests(true)
			r.Regressions = olsSpec("y", "x")
		}
		return r
	}
	cohort, err := svc.Open(context.Background(), "arch.pulse")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := svc.shouldFanOut(mk(nil, false), cohort); !ok {
		t.Fatal("fixture: the slot-free request must fan out")
	}
	if _, ok := svc.shouldFanOut(mk(excludeInferential, true), cohort); ok {
		t.Fatal("fixture: tests must refuse fan-out")
	}
	profile := func(req *types.Request) (*types.Response, processing.WorkStatsSnapshot) {
		before := processing.WorkStats()
		resp := mustProcess(t, svc, req)
		d := processing.WorkStats().Sub(before)
		d.RowTestFolds, d.PostTestRuns, d.RegressionFits = 0, 0, 0
		return resp, d
	}
	base, absent := profile(mk(nil, true))
	shaped, excluded := profile(mk(excludeInferential, true))
	_, fanned := profile(mk(excludeInferential, false))
	if fanned == absent {
		t.Fatal("the fan-out control's work profile equals the serial run's: the arm check proves nothing")
	}
	if excluded != absent {
		t.Errorf("excluding the tests moved the run's arm\n  excluded %+v\n  absent   %+v", excluded, absent)
	}
	want := *base
	want.Tests, want.Regressions = nil, nil
	if !bytes.Equal(mustMarshal(t, &want), mustMarshal(t, shaped)) {
		t.Error("a kept part moved when the tests were skipped")
	}
}

// resolveComputePlan per entry: an excluded tests / post_tests slot
// computes exactly the entries a resolved multiplicity family claims —
// the request's own plan, or a Compose slot's share carried on ctx;
// regressions follow the selection unvetoed.
func TestResolveComputePlan_TestEntries(t *testing.T) {
	svc := &Service{}
	ctx := context.Background()
	req := &types.Request{Tests: make([]*types.Test, 3), PostTests: make([]*types.Test, 2), Regressions: olsSpec("y", "x")}
	ret := mustResolveReturn(t, &types.Request{Return: excludeInferential})
	member := descx.ResolvedMultiplicity{Member: true}
	mult := &descx.MultiplicityPlan{
		Tests:     []descx.ResolvedMultiplicity{{}, member, {}},
		PostTests: []descx.ResolvedMultiplicity{member, member},
	}
	entries := func(p processing.ComputePlan) []bool {
		return []bool{p.ComputesTest(0), p.ComputesTest(1), p.ComputesTest(2), p.ComputesPostTest(0), p.ComputesPostTest(1)}
	}
	if got := svc.resolveComputePlan(ctx, req, nil, mult); !reflect.DeepEqual(entries(got), []bool{true, true, true, true, true}) || !got.Regressions {
		t.Errorf("no return: %+v; want every entry and the fit", got)
	}
	if got := svc.resolveComputePlan(ctx, req, ret, nil); got.Tests || got.PostTests || got.Regressions {
		t.Errorf("excluded, no family: %+v; want every slot off", got)
	}
	want := []bool{false, true, false, true, true}
	if got := svc.resolveComputePlan(ctx, req, ret, mult); !reflect.DeepEqual(entries(got), want) || got.Regressions {
		t.Errorf("excluded, members: %v regressions=%v", entries(got), got.Regressions)
	}
	slotCtx := composeSlotContext(ctx, nil, &descx.ComposeMultiplicityPlan{Requests: []*descx.MultiplicityPlan{nil, mult}}, 1)
	if got := entries(svc.resolveComputePlan(slotCtx, req, ret, nil)); !reflect.DeepEqual(got, want) {
		t.Errorf("Compose slot share: %v", got)
	}
}
