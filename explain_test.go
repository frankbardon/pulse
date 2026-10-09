package pulse

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestExplain_FacadeReadsNoRecord: request-mode Explain over a cohort
// reads its header and schema only — no byte of the record payload —
// for every root that names a cohort, and still names the operators
// smart defaults infer.
func TestExplain_FacadeReadsNoRecord(t *testing.T) {
	spy := &readSpyFs{Fs: afero.NewMemMapFs(), maxRead: map[string]int64{}}
	head := wideRecommendCohort(t, spy.Fs, "wide.pulse", 500)
	p, err := New(Options{FS: spy})
	if err != nil {
		t.Fatal(err)
	}
	cohort := &types.Cohort{Filename: "wide.pulse"}
	slot := func() *types.Request {
		return &types.Request{Cohort: cohort, Groups: []*types.Group{{Field: "c0"}}, Aggregations: []*types.Aggregation{{Field: "m0"}}}
	}
	for name, req := range map[string]descriptor.ExplainRequest{
		"request":  {Request: slot()},
		"composed": {Composed: &types.ComposedRequest{Requests: []*types.Request{slot(), slot()}}},
		"chain":    {Chain: &types.ChainRequest{Cohort: cohort, Stages: []*types.ChainStage{{Request: slot()}}}},
		"facet":    {Facet: &types.FacetRequest{Cohort: cohort, Fields: []string{"c0", "m0"}}},
	} {
		t.Run(name, func(t *testing.T) {
			spy.maxRead = map[string]int64{}
			res, err := p.Explain(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			if got := spy.maxRead["wide.pulse"]; got == 0 || got > head {
				t.Fatalf("Explain read up to byte %d; header + schema end at %d", got, head)
			}
			if res.Valid == nil || !*res.Valid {
				t.Fatalf("valid = %v, refusals %+v", res.Valid, res.Refusals)
			}
			if name != "facet" && !slices.ContainsFunc(res.Steps, func(s descriptor.ExplainStep) bool {
				return s.Defaulted && s.Operator == string(types.AGG_SUM)
			}) {
				t.Errorf("no inferred AGG_SUM step: %+v", res.Steps)
			}
		})
	}
}

// TestExplain_FacadeSidecarAdvisories: a Request root carries the
// sidecar-fed advisories the facade's Predict raises, a Compose slot
// carries them too (the Compose check reads each slot's sidecar), and
// the instance's suppression list drops them.
func TestExplain_FacadeSidecarAdvisories(t *testing.T) {
	codes := func(p *Pulse, req descriptor.ExplainRequest) []string {
		res, err := p.Explain(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, a := range res.Advisories {
			out = append(out, a.Code)
		}
		return out
	}
	roots := map[string]func() descriptor.ExplainRequest{
		"request":  func() descriptor.ExplainRequest { return descriptor.ExplainRequest{Request: measuredRequest(nil)} },
		"composed": func() descriptor.ExplainRequest { return descriptor.ExplainRequest{Composed: &types.ComposedRequest{Requests: []*types.Request{measuredRequest(nil)}}} },
	}
	p := importSav(t, afero.NewMemMapFs(), measuredSavSpec(), Options{})
	sp := importSav(t, afero.NewMemMapFs(), measuredSavSpec(), Options{SuppressAdvisories: []string{advNominal}})
	for name, req := range roots {
		if got := codes(p, req()); !slices.Contains(got, advNominal) {
			t.Errorf("%s: advisories = %v, want %s", name, got, advNominal)
		}
		if got := codes(sp, req()); slices.Contains(got, advNominal) {
			t.Errorf("%s: suppressed %s still attached: %v", name, advNominal, got)
		}
	}
}

func TestExplain_FacadeErrors(t *testing.T) {
	p, err := New(Options{FS: afero.NewMemMapFs()})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	var ce *errors.CodedError
	missing := &types.Cohort{Filename: "x.pulse"}
	_, err = p.Explain(ctx, descriptor.ExplainRequest{Request: &types.Request{Cohort: missing}})
	if !stderrors.As(err, &ce) || ce.Code != errors.DATA_FILE {
		t.Errorf("missing cohort: err = %v, want DATA_FILE", err)
	}
	// The request is checked before any cohort is opened.
	_, err = p.Explain(ctx, descriptor.ExplainRequest{Request: &types.Request{Cohort: missing}, Detail: "chatty"})
	if !stderrors.As(err, &ce) || ce.Code != errors.SERVICE_VALIDATION {
		t.Errorf("bad detail: err = %v, want SERVICE_VALIDATION", err)
	}
	// A root without a cohort is explained, unchecked.
	res, err := p.Explain(ctx, descriptor.ExplainRequest{Sample: &types.SampleRequest{N: 3}})
	if err != nil || res.Valid != nil || res.Root != descriptor.ExplainRootSample {
		t.Errorf("sample: res = %+v, err = %v", res, err)
	}
}

// TestExplain_FacadeProfiled: the facade reads the instance snapshot,
// so a profiled instance never names a hidden operator in the result.
func TestExplain_FacadeProfiled(t *testing.T) {
	const hidden = "AGG_MEDIAN"
	req := descriptor.ExplainRequest{Detail: descriptor.ExplainFull, Request: &types.Request{
		Aggregations: []*types.Aggregation{{Type: types.AGG_MEDIAN, Field: "x"}, {Type: types.AGG_AVERAGE, Field: "x"}},
	}}
	render := func(opts Options) string {
		p, err := New(opts)
		if err != nil {
			t.Fatal(err)
		}
		res, err := p.Explain(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(res)
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	if !strings.Contains(render(Options{FS: afero.NewMemMapFs()}), hidden) {
		t.Fatalf("fixture drift: %s absent on the default instance", hidden)
	}
	got := render(Options{FS: afero.NewMemMapFs(), FeatureProfile: &FeatureProfile{Features: allFeaturesBut(hidden)}})
	if strings.Contains(got, hidden) {
		t.Errorf("profiled instance names hidden %s: %s", hidden, got)
	}
	if !strings.Contains(got, "does not offer") {
		t.Errorf("hidden step not flagged: %s", got)
	}
}
