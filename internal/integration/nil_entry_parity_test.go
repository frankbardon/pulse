package integration_test

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestNilEntryRefusedOnEveryArm: a null entry in a top-level operator
// list (`groups: [null]` decodes to a nil *types.Group) is refused by
// the shared null-entry gate SlotRefusal runs first — PROCESSING_CONFIG
// "<slot> contains a null <noun> at index <i>", details {slot, index} —
// with the same code, message and details from predict and the
// runtime, on a request that would otherwise stream and one that would
// buffer, with and without DisablePanicRecovery. Before the gate,
// predict panicked in computeStreamable and the runtime in canStream
// (or, for regressions and joins, predict accepted what the runtime
// panicked on or refused).
func TestNilEntryRefusedOnEveryArm(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	schema := paritySchema(t)
	fsys := afero.NewMemMapFs()
	writeParityCohort(t, fsys, "parity.pulse", schema, 0, 40)
	data, err := afero.ReadFile(fsys, "parity.pulse")
	if err != nil {
		t.Fatal(err)
	}
	// The arm each request would take without the null entry, checked
	// against predict below so the arm label is not a claim.
	arms := []struct {
		name       string
		agg        types.AggregationType
		streamable bool
	}{
		{"streaming", types.AGG_COUNT, true},
		{"buffered", types.AGG_MEDIAN, false},
	}
	slots := []struct {
		slot, want string
		set        func(r *types.Request)
	}{
		{"filterers", "filterers contains a null filterer at index 0", func(r *types.Request) { r.Filterers = []*types.Filterer{nil} }},
		{"aggregations", "aggregations contains a null aggregation at index 1", func(r *types.Request) { r.Aggregations = append(r.Aggregations, nil) }},
		{"attributes", "attributes contains a null attribute at index 0", func(r *types.Request) { r.Attributes = []*types.Attribute{nil} }},
		{"groups", "groups contains a null grouper at index 1", func(r *types.Request) {
			r.Groups = []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}, nil}
		}},
		{"windows", "windows contains a null window at index 0", func(r *types.Request) { r.Windows = []*types.Window{nil} }},
		{"features", "features contains a null feature at index 0", func(r *types.Request) { r.Features = []*types.Feature{nil} }},
		{"tests", "tests contains a null test at index 0", func(r *types.Request) { r.Tests = []*types.Test{nil} }},
		{"post_tests", "post_tests contains a null test at index 0", func(r *types.Request) { r.PostTests = []*types.Test{nil} }},
		{"regressions", "regressions contains a null regression at index 0", func(r *types.Request) { r.Regressions = []*types.RegressionSpec{nil} }},
		{"joins", "joins contains a null join at index 0", func(r *types.Request) { r.Joins = []*types.JoinSpec{nil} }},
	}
	for _, disable := range []bool{false, true} {
		p, err := pulse.New(pulse.Options{FS: fsys, DisablePanicRecovery: disable})
		if err != nil {
			t.Fatal(err)
		}
		recovery := map[bool]string{false: "recover", true: "raw"}[disable]
		for _, arm := range arms {
			base := func() *types.Request {
				return &types.Request{Cohort: &types.Cohort{Filename: "parity.pulse"},
					Aggregations: []*types.Aggregation{{Type: arm.agg, Field: "score"}}}
			}
			clean, err := p.PredictBytes(ctx, data, base())
			if err != nil {
				t.Fatal(err)
			}
			if pr, ok := clean.Data.(*descriptor.PredictResult); !ok || len(clean.Errors) > 0 || pr.Streamable != arm.streamable {
				t.Fatalf("%s: clean request is not on its arm (errors %v)", arm.name, clean.Errors)
			}
			for _, s := range slots {
				t.Run(recovery+"/"+arm.name+"/"+s.slot, func(t *testing.T) {
					req := base()
					s.set(req)
					env, err := p.PredictBytes(ctx, data, req)
					if err != nil {
						t.Fatal(err)
					}
					_, rerr := p.Process(ctx, req)
					ce := requireCode(t, rerr, errors.PROCESSING_CONFIG)
					if ce.Message != s.want {
						t.Errorf("runtime message = %q, want %q", ce.Message, s.want)
					}
					if ce.Details["slot"] != s.slot {
						t.Errorf("runtime details = %v, want slot %q", ce.Details, s.slot)
					}
					sameEntry(t, env, rerr)
				})
			}
		}
	}
}

// TestNilEntryRefusedOnOtherRoots: the same gate on the facet root's
// filterers and the Compose root — a null Requests entry, a null entry
// in a slot's list (details.request), in the base merged into every
// slot, and in a fit request's list (details.fit) — predict and runtime
// identical, no panic.
func TestNilEntryRefusedOnOtherRoots(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	schema := paritySchema(t)
	fsys := afero.NewMemMapFs()
	writeParityCohort(t, fsys, "parity.pulse", schema, 0, 40)
	p, err := pulse.New(pulse.Options{FS: fsys, DisablePanicRecovery: true})
	if err != nil {
		t.Fatal(err)
	}
	cohort := &types.Cohort{Filename: "parity.pulse"}
	count := func() []*types.Aggregation { return []*types.Aggregation{{Type: types.AGG_COUNT, Field: "score"}} }
	slot := func() *types.Request { return &types.Request{Label: "a", Cohort: cohort, Aggregations: count()} }

	t.Run("facet/filterers", func(t *testing.T) {
		fr := &types.FacetRequest{Cohort: cohort, Fields: []string{"region"}, Filterers: []*types.Filterer{nil}}
		env, err := p.PredictFacet(ctx, fr)
		if err != nil {
			t.Fatal(err)
		}
		_, rerr := p.FacetSchema(ctx, fr)
		requireCode(t, rerr, errors.PROCESSING_CONFIG)
		sameEntry(t, env, rerr)
	})

	fit := &types.Request{Cohort: cohort, Filterers: []*types.Filterer{nil},
		Regressions: []*types.RegressionSpec{{Type: types.REG_OLS, Name: "m", Target: "score", Predictors: []string{"qty"}}}}
	compose := []struct {
		name    string
		req     *types.ComposedRequest
		details map[string]any
	}{
		{"requests", &types.ComposedRequest{Requests: []*types.Request{slot(), nil}},
			map[string]any{"slot": "requests", "index": 1}},
		{"slot_groups", &types.ComposedRequest{Requests: []*types.Request{{Label: "a", Cohort: cohort, Aggregations: count(), Groups: []*types.Group{nil}}}},
			map[string]any{"slot": "groups", "index": 0, "request": 0}},
		{"base_groups", &types.ComposedRequest{Base: &types.Request{Groups: []*types.Group{nil}}, Requests: []*types.Request{slot()}},
			map[string]any{"slot": "groups", "index": 0, "request": 0}},
		{"fit_filterers", &types.ComposedRequest{Requests: []*types.Request{slot()}, Fits: []types.FitSpec{{Name: "f", Request: fit}}},
			map[string]any{"slot": "filterers", "index": 0, "fit": 0}},
	}
	for _, c := range compose {
		t.Run("compose/"+c.name, func(t *testing.T) {
			env, err := p.PredictCompose(ctx, c.req)
			if err != nil {
				t.Fatal(err)
			}
			_, rerr := p.Compose(ctx, c.req)
			var ce *errors.CodedError
			if !stderrors.As(rerr, &ce) || ce.Code != errors.PROCESSING_CONFIG {
				t.Fatalf("runtime error = %v, want PROCESSING_CONFIG", rerr)
			}
			want, _ := json.Marshal(c.details)
			have, _ := json.Marshal(ce.Details)
			if string(want) != string(have) {
				t.Errorf("runtime details = %s, want %s", have, want)
			}
			sameEntry(t, env, rerr)
		})
	}
}
