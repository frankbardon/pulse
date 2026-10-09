package pulse

import (
	"context"
	stderrors "errors"
	"slices"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

func envCodes(env *descriptor.Envelope) []string {
	var out []string
	for _, e := range env.Errors {
		out = append(out, e.Code)
	}
	return out
}

// TestPredictCompose_SlotSidecarAdvisories: a Compose slot over an
// SPSS-imported cohort carries the sidecar-fed advisories (the
// SidecarLoader is wired), attributed to the slot, and the instance's
// suppression list drops them.
func TestPredictCompose_SlotSidecarAdvisories(t *testing.T) {
	ctx := context.Background()
	codes := func(p *Pulse) []string {
		env, err := p.PredictCompose(ctx, &ComposedRequest{Requests: []*Request{measuredRequest(nil)}})
		if err != nil {
			t.Fatal(err)
		}
		res, ok := env.Data.(*ComposePredictResult)
		if !ok {
			t.Fatalf("Data = %T, want *ComposePredictResult", env.Data)
		}
		if !res.Valid || len(env.Errors) != 0 {
			t.Fatalf("valid = %v, errors %v", res.Valid, envCodes(env))
		}
		var out []string
		for _, a := range res.Advisories {
			if a.Details["request"] != 0 {
				t.Errorf("advisory %s not attributed to slot 0: %v", a.Code, a.Details)
			}
			out = append(out, a.Code)
		}
		return out
	}
	p := importSav(t, afero.NewMemMapFs(), measuredSavSpec(), Options{})
	if got, want := codes(p), []string{advNominal, advOrdinal, advWeight}; !slices.Equal(got, want) {
		t.Errorf("advisories = %v, want %v", got, want)
	}
	sp := importSav(t, afero.NewMemMapFs(), measuredSavSpec(), Options{SuppressAdvisories: []string{advNominal}})
	if got := codes(sp); slices.Contains(got, advNominal) {
		t.Errorf("suppressed %s still attached: %v", advNominal, got)
	}
}

// TestPredictRoots_Verdicts: each root predicts valid over a good
// request and reports a coded refusal (Valid false, no returned error)
// over a bad one.
func TestPredictRoots_Verdicts(t *testing.T) {
	ctx := context.Background()
	p, _ := obsFixture(t, Options{})
	cohort := &types.Cohort{Filename: obsCohort}
	bad := func() *Request {
		return &Request{Cohort: cohort, Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "nope"}}}
	}
	type verdict struct {
		env   *descriptor.Envelope
		valid bool
	}
	run := map[string]func(good bool) verdict{
		"compose": func(good bool) verdict {
			r := obsRequest()
			if !good {
				r = bad()
			}
			env, err := p.PredictCompose(ctx, &ComposedRequest{Requests: []*Request{r}})
			if err != nil {
				t.Fatal(err)
			}
			return verdict{env, env.Data.(*ComposePredictResult).Valid}
		},
		"chain": func(good bool) verdict {
			r := obsRequest()
			if !good {
				r = bad()
			}
			env, err := p.PredictChain(ctx, &ChainRequest{Cohort: cohort, Stages: []*ChainStage{{Request: r}}})
			if err != nil {
				t.Fatal(err)
			}
			return verdict{env, env.Data.(*ChainPredictResult).Valid}
		},
		"facet": func(good bool) verdict {
			f := "region"
			if !good {
				f = "nope"
			}
			env, err := p.PredictFacet(ctx, &FacetRequest{Cohort: cohort, Fields: []string{f}})
			if err != nil {
				t.Fatal(err)
			}
			return verdict{env, env.Data.(*FacetPredictResult).Valid}
		},
	}
	for name, fn := range run {
		if v := fn(true); !v.valid || len(v.env.Errors) != 0 {
			t.Errorf("%s good: valid = %v, errors %v", name, v.valid, envCodes(v.env))
		}
		if v := fn(false); v.valid || len(v.env.Errors) == 0 {
			t.Errorf("%s bad: valid = %v, errors %v", name, v.valid, envCodes(v.env))
		}
	}
}

// TestPredictRoots_Errors: the returned error is reserved for a request
// the facade cannot route (nil request or cohort) and an unreadable
// cohort, each coded.
func TestPredictRoots_Errors(t *testing.T) {
	ctx := context.Background()
	p, err := New(Options{FS: afero.NewMemMapFs()})
	if err != nil {
		t.Fatal(err)
	}
	missing := &types.Cohort{Filename: "x.pulse"}
	stage := []*ChainStage{{Request: &Request{Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT}}}}}
	for _, tc := range []struct {
		name string
		call func() (*descriptor.Envelope, error)
		want errors.Code
	}{
		{"facet nil", func() (*descriptor.Envelope, error) { return p.PredictFacet(ctx, nil) }, errors.SERVICE_VALIDATION},
		{"facet no cohort", func() (*descriptor.Envelope, error) { return p.PredictFacet(ctx, &FacetRequest{}) }, errors.SERVICE_VALIDATION},
		{"facet missing", func() (*descriptor.Envelope, error) { return p.PredictFacet(ctx, &FacetRequest{Cohort: missing}) }, errors.DATA_FILE},
		{"chain nil", func() (*descriptor.Envelope, error) { return p.PredictChain(ctx, nil) }, errors.SERVICE_VALIDATION},
		{"chain no cohort", func() (*descriptor.Envelope, error) { return p.PredictChain(ctx, &ChainRequest{Stages: stage}) }, errors.SERVICE_VALIDATION},
		{"chain missing", func() (*descriptor.Envelope, error) {
			return p.PredictChain(ctx, &ChainRequest{Cohort: missing, Stages: stage})
		}, errors.DATA_FILE},
	} {
		env, err := tc.call()
		var ce *errors.CodedError
		if env != nil || !stderrors.As(err, &ce) || ce.Code != tc.want {
			t.Errorf("%s: env = %v, err = %v, want %s", tc.name, env, err, tc.want)
		}
	}
	// A nil Compose batch is a coded envelope refusal: no cohort to open.
	env, err := p.PredictCompose(ctx, nil)
	if err != nil || !slices.Equal(envCodes(env), []string{string(errors.SERVICE_VALIDATION)}) {
		t.Errorf("compose nil: err = %v, errors %v", err, envCodes(env))
	}
	// A cancelled context is returned, never enveloped.
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := p.PredictCompose(cctx, &ComposedRequest{}); !stderrors.Is(err, context.Canceled) {
		t.Errorf("cancelled compose: err = %v", err)
	}
}

// TestPredictRoots_ReadNoRecord: the cohort-bound roots read a
// single-file cohort's header and schema only.
func TestPredictRoots_ReadNoRecord(t *testing.T) {
	spy := &readSpyFs{Fs: afero.NewMemMapFs(), maxRead: map[string]int64{}}
	head := wideRecommendCohort(t, spy.Fs, "wide.pulse", 500)
	p, err := New(Options{FS: spy})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	cohort := &types.Cohort{Filename: "wide.pulse"}
	slot := &types.Request{Cohort: cohort, Groups: []*types.Group{{Field: "c0"}}, Aggregations: []*types.Aggregation{{Field: "m0"}}}
	for name, call := range map[string]func() (*descriptor.Envelope, error){
		"facet": func() (*descriptor.Envelope, error) {
			return p.PredictFacet(ctx, &FacetRequest{Cohort: cohort, Fields: []string{"c0", "m0"}})
		},
		"chain": func() (*descriptor.Envelope, error) {
			return p.PredictChain(ctx, &ChainRequest{Cohort: cohort, Stages: []*ChainStage{{Request: slot}}})
		},
	} {
		spy.maxRead = map[string]int64{}
		env, err := call()
		if err != nil {
			t.Fatal(err)
		}
		if len(env.Errors) != 0 {
			t.Errorf("%s: errors %v", name, envCodes(env))
		}
		if got := spy.maxRead["wide.pulse"]; got == 0 || got > head {
			t.Errorf("%s read up to byte %d; header + schema end at %d", name, got, head)
		}
	}
}
