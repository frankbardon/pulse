package pulse

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
)

// Embedder examples (Extensions.Examples): validated at pulse.New,
// served through the facade, manifest and ontology like built-ins,
// pruned with their operator. The per-reason validation table lives in
// internal/descriptor (TestLoadExtensionExamples_Refusals); MCP parity in
// mcp/gosdk (TestExtensionExamples_MCP).

const rootExtExampleKept = `{
  "_meta": {
    "name": "acme-kept-revenue",
    "category": "acme",
    "description": "Kept-mean revenue per region.",
    "tags": ["financial"],
    "intents": ["describe"],
    "operators": ["AGG_ACME_KEPT", "GROUP_CATEGORY"]
  },
  "cohort": {"filename": "sales.pulse"},
  "aggregations": [{"type": "AGG_ACME_KEPT", "field": "revenue"}],
  "groups": [{"type": "GROUP_CATEGORY", "field": "region"}]
}`

const rootExtExampleBrand = `{
  "_meta": {
    "name": "acme-brand-share",
    "category": "acme",
    "description": "Brand share across the whole cohort.",
    "tags": ["proportion-analysis"],
    "operators": ["AGG_ACME_BRAND"]
  },
  "cohort": {"filename": "sales.pulse"},
  "aggregations": [{"type": "AGG_ACME_BRAND", "field": "brand"}]
}`

func acmeExampleExtensions() Extensions {
	ext := acmeSkillExtensions()
	ext.Skills = nil
	ext.Examples = fstest.MapFS{
		"kept.json":  {Data: []byte(rootExtExampleKept)},
		"brand.json": {Data: []byte(rootExtExampleBrand)},
	}
	return ext
}

var acmeExampleNames = []string{"acme-brand-share", "acme-kept-revenue"}

func exampleNameSet(sums []ExampleSummary) map[string]bool {
	out := map[string]bool{}
	for _, s := range sums {
		out[s.Name] = true
	}
	return out
}

// TestExtensions_ExamplesServedLikeBuiltins: a profile-free instance
// searches, gets, counts and graphs every embedder example.
func TestExtensions_ExamplesServedLikeBuiltins(t *testing.T) {
	plain, err := New(Options{FS: memFsWith(t, nil)})
	if err != nil {
		t.Fatal(err)
	}
	p, err := New(Options{FS: memFsWith(t, nil), Extensions: acmeExampleExtensions()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	listed := exampleNameSet(p.ExamplesSearch("", nil, "acme"))
	g := p.Ontology()
	for _, name := range acmeExampleNames {
		if !listed[name] {
			t.Errorf("%s: not searchable", name)
		}
		ex, ok := p.ExampleGet(name)
		if !ok || strings.Contains(string(ex.Body), "_meta") {
			t.Errorf("%s: ExampleGet = %v, %v", name, ex, ok)
		}
		if !slices.ContainsFunc(g.Nodes, func(n descriptor.OntologyNode) bool { return n.ID == "example:"+name }) {
			t.Errorf("%s: no ontology node", name)
		}
	}
	if hits := p.ExamplesSearch("AGG_ACME_KEPT", nil, ""); len(hits) != 1 || hits[0].Name != "acme-kept-revenue" {
		t.Errorf("operator query = %v", hits)
	}
	if !slices.Contains(g.Edges, descriptor.OntologyEdge{From: "operator:AGG_ACME_KEPT", To: "example:acme-kept-revenue", Kind: descriptor.OntologyEdgeExemplifiedBy}) {
		t.Error("no exemplified_by edge from the extension operator")
	}
	m, pm := p.Manifest(context.Background()), plain.Manifest(context.Background())
	if m.ExamplesCount != pm.ExamplesCount+2 || !slices.Contains(m.ExampleCategories, "acme") {
		t.Errorf("manifest examples = %d %v, want %d with acme", m.ExamplesCount, m.ExampleCategories, pm.ExamplesCount+2)
	}
}

// TestExtensions_ExamplesKeepBuiltinsByteIdentical: registering embedder
// examples changes no built-in example's listing or body.
func TestExtensions_ExamplesKeepBuiltinsByteIdentical(t *testing.T) {
	plain, err := New(Options{FS: memFsWith(t, nil)})
	if err != nil {
		t.Fatal(err)
	}
	p, err := New(Options{FS: memFsWith(t, nil), Extensions: acmeExampleExtensions()})
	if err != nil {
		t.Fatal(err)
	}
	var got []ExampleSummary
	for _, s := range p.ExamplesSearch("", nil, "") {
		if !slices.Contains(acmeExampleNames, s.Name) {
			got = append(got, s)
		}
	}
	want := plain.ExamplesSearch("", nil, "")
	a, _ := json.Marshal(got)
	b, _ := json.Marshal(want)
	if string(a) != string(b) {
		t.Fatal("built-in example listing differs once embedder examples are registered")
	}
	for _, s := range want {
		x, _ := plain.ExampleGet(s.Name)
		y, _ := p.ExampleGet(s.Name)
		xa, _ := json.Marshal(x)
		ya, _ := json.Marshal(y)
		if string(xa) != string(ya) {
			t.Errorf("%s: example differs once embedder examples are registered", s.Name)
		}
	}
	if q := "Welch"; !slices.EqualFunc(p.ExamplesSearch(q, nil, ""), plain.ExamplesSearch(q, nil, ""), func(a, b ExampleSummary) bool { return a.Name == b.Name }) {
		t.Error("built-in query ranking changed")
	}
}

// TestExtensions_ExamplesPrunedWithOperator: a feature profile hiding
// AGG_ACME_BRAND hides its example — absent from search, manifest count
// and ontology, and an exact get answers like a name that never existed.
func TestExtensions_ExamplesPrunedWithOperator(t *testing.T) {
	p, err := New(Options{FS: memFsWith(t, nil), Extensions: acmeExampleExtensions(),
		FeatureProfile: &FeatureProfile{Features: []string{"capability:process", "AGG_ACME_KEPT", "GROUP_CATEGORY"}}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ex, ok := p.ExampleGet("acme-brand-share")
	never, neverOK := p.ExampleGet("acme-never-registered")
	if ok || ex != never || neverOK {
		t.Errorf("ExampleGet(hidden) = (%v, %v), want the nonexistent answer (%v, %v)", ex, ok, never, neverOK)
	}
	listed := exampleNameSet(p.ExamplesSearch("", nil, ""))
	raw, _ := json.Marshal(p.Ontology())
	if listed["acme-brand-share"] || strings.Contains(string(raw), "example:acme-brand-share") {
		t.Error("hidden example still listed or graphed")
	}
	if !listed["acme-kept-revenue"] {
		t.Error("kept example pruned")
	}
	if got := p.Manifest(context.Background()).ExamplesCount; got != len(listed) {
		t.Errorf("manifest examples_count = %d, search lists %d", got, len(listed))
	}

	// A built-in operator the example uses, hidden: the example goes too.
	p, err = New(Options{FS: memFsWith(t, nil), Extensions: acmeExampleExtensions(),
		FeatureProfile: &FeatureProfile{Features: []string{"capability:process", "AGG_ACME_KEPT", "AGG_ACME_BRAND"}}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, ok := p.ExampleGet("acme-kept-revenue"); ok {
		t.Error("acme-kept-revenue served with GROUP_CATEGORY hidden")
	}
	if _, ok := p.ExampleGet("acme-brand-share"); !ok {
		t.Error("acme-brand-share pruned, want kept")
	}
}

// TestExtensions_ExamplesRefusedAtNew: pulse.New (and CheckFeatureProfile,
// via the shared universe validation) surfaces the validation code.
func TestExtensions_ExamplesRefusedAtNew(t *testing.T) {
	cases := []struct {
		name string
		fs   fstest.MapFS
		code errors.Code
	}{
		{"built-in name", fstest.MapFS{"a.json": {Data: []byte(strings.Replace(rootExtExampleKept, "acme-kept-revenue", "facet_simple_one_field", 1))}}, errors.PULSE_EXTENSION_EXAMPLE_COLLISION},
		{"unregistered operator", fstest.MapFS{"a.json": {Data: []byte(strings.ReplaceAll(rootExtExampleKept, "AGG_ACME_KEPT", "AGG_ACME_NOPE"))}}, errors.PULSE_EXTENSION_EXAMPLE_INVALID},
		{"body typo", fstest.MapFS{"a.json": {Data: []byte(strings.Replace(rootExtExampleKept, `"groups"`, `"grops"`, 1))}}, errors.PULSE_EXTENSION_EXAMPLE_INVALID},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ext := acmeExampleExtensions()
			ext.Examples = tc.fs
			_, err := New(Options{FS: memFsWith(t, nil), Extensions: ext})
			var ce *errors.CodedError
			if !stderrors.As(err, &ce) || ce.Code != tc.code {
				t.Fatalf("New err = %v, want %s", err, tc.code)
			}
			_, err = validateExtensionUniverse(ext)
			if !stderrors.As(err, &ce) || ce.Code != tc.code {
				t.Fatalf("validateExtensionUniverse err = %v, want %s", err, tc.code)
			}
		})
	}
}

// TestExtensions_ExamplesMergedValuesCollide: two merged Extensions
// values shipping the same example name collide; distinct names merge.
func TestExtensions_ExamplesMergedValuesCollide(t *testing.T) {
	base := acmeExampleExtensions()
	a := Extensions{Aggregators: base.Aggregators, Examples: fstest.MapFS{"kept.json": {Data: []byte(rootExtExampleKept)}}}
	b := Extensions{Examples: fstest.MapFS{"other.json": {Data: []byte(rootExtExampleKept)}}}
	_, err := validateExtensionUniverse(mergeExtensions([]Extensions{a, b}))
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_EXTENSION_EXAMPLE_COLLISION || ce.Details["reason"] != descx.ExampleReasonDuplicate {
		t.Fatalf("err = %v, want PULSE_EXTENSION_EXAMPLE_COLLISION duplicate", err)
	}
	b.Examples = fstest.MapFS{"brand.json": {Data: []byte(rootExtExampleBrand)}}
	u, err := validateExtensionUniverse(mergeExtensions([]Extensions{a, b}))
	if err != nil || len(u.examples) != 2 {
		t.Fatalf("distinct names: examples = %d, err = %v; want 2, nil", len(u.examples), err)
	}
}
