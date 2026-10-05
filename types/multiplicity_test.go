package types

import (
	"encoding/json"
	"strings"
	"testing"
)

// multiplicityFreeRequests builds one request per root that reaches a
// `multiplicity` slot (Request, Test, OverlaySpec, ComposeOverlaySpec,
// ComposedRequest, and a FacetRequest's OverlaySpec), none setting one.
func multiplicityFreeRequests() (*Request, *ComposedRequest, *FacetRequest) {
	req := &Request{
		Cohort:       &Cohort{Filename: "a.pulse"},
		Aggregations: []*Aggregation{{Type: AGG_SUM, Field: "x"}},
		Tests:        []*Test{{Type: TEST_T, Field: "x", SplitBy: "g"}},
		PostTests:    []*Test{{Type: TEST_TUKEY_HSD, Field: "x"}},
		Overlays:     []OverlaySpec{{Kind: OverlayKindPairwisePropZ, Scope: OverlayScopeRow}},
	}
	composed := &ComposedRequest{
		Requests: []*Request{req},
		Overlays: []ComposeOverlaySpec{{Kind: OverlayKindPropZPanel, Reference: "request_1", Targets: []string{"request_1"}}},
	}
	facet := &FacetRequest{
		Cohort:   &Cohort{Filename: "a.pulse"},
		Fields:   []string{"g"},
		Overlays: []OverlaySpec{{Kind: OverlayKindIndexVsPop, Scope: OverlayScopeGroup}},
	}
	return req, composed, facet
}

// TestMultiplicityAbsentIsByteIdentical pins the additive contract: a
// request that sets no `multiplicity` anywhere marshals without the key
// and hashes to the baseline captured before the slots existed
// (commit e740a323), on every root that reaches a slot.
func TestMultiplicityAbsentIsByteIdentical(t *testing.T) {
	req, composed, facet := multiplicityFreeRequests()
	cases := []struct {
		name     string
		v        any
		hash     string
		captured string
	}{
		{"request", req, req.Hash(), "aea3f53d8ebda4e4eb576b290e7e7499"},
		{"composed", composed, composed.Hash(), "f7370f13ddeb879b7bb4810fce89075b"},
		{"facet", facet, facet.Hash(), "1621fe88a2914fb4f94c5ea2ea2af739"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b, err := json.Marshal(c.v)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(b), "multiplicity") {
				t.Errorf("absent multiplicity reached the wire: %s", b)
			}
			if c.hash != c.captured {
				t.Errorf("canonical hash drifted from the pre-multiplicity baseline: got %q want %q", c.hash, c.captured)
			}
		})
	}
}

// TestMultiplicityRoundTrip: a set block survives JSON on every slot,
// and an explicit opt-out is distinguishable from absent.
func TestMultiplicityRoundTrip(t *testing.T) {
	req, composed, facet := multiplicityFreeRequests()
	holm := &Multiplicity{Method: MultiplicityMethodHolm, Family: MultiplicityFamilyRow, Alpha: 0.01}
	req.Multiplicity = &Multiplicity{Method: MultiplicityMethodNone}
	req.Tests[0].Multiplicity = &Multiplicity{Family: MultiplicityFamilyRequest}
	req.Overlays[0].Multiplicity = holm
	composed.Multiplicity = &Multiplicity{Family: MultiplicityFamilyCompose}
	composed.Overlays[0].Multiplicity = holm
	facet.Overlays[0].Multiplicity = holm

	for name, v := range map[string]any{"request": req, "composed": composed, "facet": facet} {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{`"multiplicity":{`} {
			if !strings.Contains(string(b), want) {
				t.Errorf("%s: %s missing from %s", name, want, b)
			}
		}
	}
	b, _ := json.Marshal(req)
	var back Request
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if back.Multiplicity == nil || back.Multiplicity.Method != MultiplicityMethodNone {
		t.Errorf("request opt-out lost: %+v", back.Multiplicity)
	}
	if got := back.Overlays[0].Multiplicity; got == nil || *got != *holm {
		t.Errorf("overlay block lost: %+v", got)
	}
	if back.PostTests[0].Multiplicity != nil {
		t.Errorf("absent post-test block decoded as %+v", back.PostTests[0].Multiplicity)
	}
	if req.Hash() == (&Request{Cohort: req.Cohort, Aggregations: req.Aggregations, Tests: req.Tests, PostTests: req.PostTests, Overlays: req.Overlays}).Hash() {
		t.Error("a request-level block does not move the canonical hash")
	}
}

// TestAllMultiplicityVocabularies pins the closed method and family
// sets (R p.adjust names; the five U13 families).
func TestAllMultiplicityVocabularies(t *testing.T) {
	methods := []string{}
	for _, m := range AllMultiplicityMethods() {
		methods = append(methods, string(m))
	}
	if got, want := strings.Join(methods, ","), "none,bonferroni,holm,bh,by"; got != want {
		t.Errorf("methods = %s, want %s", got, want)
	}
	families := []string{}
	for _, f := range AllMultiplicityFamilies() {
		families = append(families, string(f))
	}
	if got, want := strings.Join(families, ","), "layer,row,column,request,compose"; got != want {
		t.Errorf("families = %s, want %s", got, want)
	}
}
