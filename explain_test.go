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
		"request": func() descriptor.ExplainRequest { return descriptor.ExplainRequest{Request: measuredRequest(nil)} },
		"composed": func() descriptor.ExplainRequest {
			return descriptor.ExplainRequest{Composed: &types.ComposedRequest{Requests: []*types.Request{measuredRequest(nil)}}}
		},
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

// TestExplain_FacadeResponseMode: a response Process returned reads
// into findings — named by operator with its request beside it, by
// count only with the partial note without — and response mode opens
// no cohort.
func TestExplain_FacadeResponseMode(t *testing.T) {
	fsys := afero.NewMemMapFs()
	wideRecommendCohort(t, fsys, "wide.pulse", 20)
	p, err := New(Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	req := &types.Request{Cohort: &types.Cohort{Filename: "wide.pulse"}, Groups: []*types.Group{{Field: "c0"}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "m0", Label: "total"}}}
	resp, err := p.Process(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	with, err := p.Explain(context.Background(), descriptor.ExplainRequest{Response: resp, Request: req})
	if err != nil {
		t.Fatal(err)
	}
	if with.Mode != descriptor.ExplainModeResponse || len(with.Findings) != 1 || with.Findings[0].Operator != string(types.AGG_SUM) {
		t.Fatalf("with request = %+v", with)
	}
	if n := with.Findings[0].Numbers["n"]; n == nil || *n != 20 {
		t.Errorf("components floor n = %v", n)
	}
	if !strings.Contains(with.Summary, "from 20 of 20 records") {
		t.Errorf("summary = %q", with.Summary)
	}
	without, err := p.Explain(context.Background(), descriptor.ExplainRequest{Response: resp})
	if err != nil {
		t.Fatal(err)
	}
	if without.Findings[0].Operator != "" || !slices.ContainsFunc(without.Caveats, func(c string) bool { return strings.HasPrefix(c, "Partial reading") }) {
		t.Errorf("without request = %+v", without)
	}
	// Response mode reads no cohort: the file can be gone.
	if err := fsys.Remove("wide.pulse"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Explain(context.Background(), descriptor.ExplainRequest{Response: resp, Request: req}); err != nil {
		t.Errorf("response mode touched the cohort: %v", err)
	}
}

// TestExplain_FacadeResultRoots: the results Compose, ProcessChain and
// FacetSchema return read into findings — a chain run with its request
// echoed names its stages' operators with no request supplied.
func TestExplain_FacadeResultRoots(t *testing.T) {
	fsys := afero.NewMemMapFs()
	wideRecommendCohort(t, fsys, "wide.pulse", 20)
	ctx := context.Background()
	cohort := &types.Cohort{Filename: "wide.pulse"}
	sum := func(field, label string) []*types.Aggregation {
		return []*types.Aggregation{{Type: types.AGG_SUM, Field: field, Label: label}}
	}

	p, err := New(Options{FS: fsys, EchoRequest: true})
	if err != nil {
		t.Fatal(err)
	}
	creq := &types.ComposedRequest{Requests: []*types.Request{
		{Cohort: cohort, Aggregations: sum("m0", "a")},
		{Cohort: cohort, Groups: []*types.Group{{Field: "c0"}}, Aggregations: sum("m0", "b")},
	}}
	cresp, err := p.Compose(ctx, creq)
	if err != nil {
		t.Fatal(err)
	}
	res, err := p.Explain(ctx, descriptor.ExplainRequest{ComposedResponse: cresp, Composed: creq})
	if err != nil {
		t.Fatal(err)
	}
	if res.Root != descriptor.ExplainRootComposedResponse || len(res.Findings) != 2 ||
		res.Findings[1].Slot != "responses[1].aggregations[0]" || res.Findings[1].Operator != string(types.AGG_SUM) {
		t.Errorf("compose = %+v", res)
	}

	chreq := &types.ChainRequest{Cohort: cohort, Stages: []*types.ChainStage{
		{Request: &types.Request{Groups: []*types.Group{{Field: "c0"}}, Aggregations: sum("m0", "total")}},
		{Request: &types.Request{Aggregations: sum("total", "grand")}},
	}}
	chresp, err := p.ProcessChain(ctx, chreq)
	if err != nil {
		t.Fatal(err)
	}
	if chresp.NormalizedRequest == nil {
		t.Fatal("fixture drift: EchoRequest did not echo the chain request")
	}
	res, err = p.Explain(ctx, descriptor.ExplainRequest{ChainResponse: chresp})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Findings) != 2 || res.Findings[1].Slot != "stages[1].aggregations[0]" || res.Findings[1].Operator != string(types.AGG_SUM) ||
		slices.ContainsFunc(res.Caveats, func(c string) bool { return strings.HasPrefix(c, "Partial reading") }) {
		t.Errorf("chain = %+v", res)
	}

	freq := &types.FacetRequest{Cohort: cohort, Fields: []string{"c0", "m0"}}
	fresp, err := p.FacetSchema(ctx, freq)
	if err != nil {
		t.Fatal(err)
	}
	res, err = p.Explain(ctx, descriptor.ExplainRequest{FacetResult: fresp, Facet: freq})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Findings) != 2 || res.Findings[0].Slot != "fields.c0" || res.Findings[1].Slot != "fields.m0" ||
		!strings.Contains(res.Summary, "summarises 2 fields from 20 of 20 records") {
		t.Errorf("facet = %+v", res)
	}
}

// TestExplain_HiddenFeatureSurfaces: an instance hiding
// capability:explain describes no explain surface — the manifest lists
// no `explain` command and the payload schema has no ExplainRequest /
// ExplainResult root — while one enabling it keeps both. The facade
// itself stays callable (facade methods are not gated). An instance
// offering explain but hiding compose drops the composed roots from
// ExplainRequest with the ComposedRequest / ComposedResponse defs.
func TestExplain_HiddenFeatureSurfaces(t *testing.T) {
	const feat = "capability:explain"
	for _, tc := range []struct {
		name         string
		features     []string
		want         bool
		wantComposed bool
	}{
		{"hidden", allFeaturesBut(feat), false, true},
		{"enabled", allFeaturesBut("capability:synth"), true, true},
		{"compose hidden", allFeaturesBut("capability:compose"), true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := New(Options{FS: afero.NewMemMapFs(), FeatureProfile: &FeatureProfile{Features: tc.features}})
			if err != nil {
				t.Fatal(err)
			}
			m := p.Manifest(context.Background())
			if listed := slices.ContainsFunc(m.Commands, func(c descriptor.Command) bool { return c.Name == "explain" }); listed != tc.want {
				t.Errorf("manifest lists explain = %v, want %v", listed, tc.want)
			}
			raw, err := p.PayloadSchema()
			if err != nil {
				t.Fatal(err)
			}
			var doc struct {
				Defs map[string]struct {
					Properties map[string]json.RawMessage `json:"properties"`
				} `json:"$defs"`
			}
			if err := json.Unmarshal(raw, &doc); err != nil {
				t.Fatal(err)
			}
			for _, root := range []string{"ExplainRequest", "ExplainResult"} {
				if _, ok := doc.Defs[root]; ok != tc.want {
					t.Errorf("payload schema has %s = %v, want %v", root, ok, tc.want)
				}
			}
			if !tc.want {
				if _, err := p.Explain(context.Background(), descriptor.ExplainRequest{Sample: &types.SampleRequest{N: 1}}); err != nil {
					t.Errorf("facade gated: %v", err)
				}
				return
			}
			props := doc.Defs["ExplainRequest"].Properties
			for _, k := range []string{"composed", "composed_response"} {
				if _, ok := props[k]; ok != tc.wantComposed {
					t.Errorf("ExplainRequest.%s present = %v, want %v", k, ok, tc.wantComposed)
				}
			}
			if _, ok := doc.Defs["ComposedResponse"]; ok != tc.wantComposed {
				t.Errorf("ComposedResponse def present = %v, want %v", ok, tc.wantComposed)
			}
		})
	}
}
