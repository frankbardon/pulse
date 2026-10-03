package descriptor

import (
	stderrors "errors"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
)

// extExampleTrim is a valid embedder example using AGG_ACME_TRIM (an
// extension operator) and GROUP_CATEGORY (a built-in).
const extExampleTrim = `{
  "_meta": {
    "name": "acme-trimmed-revenue",
    "category": "acme",
    "description": "Trimmed mean revenue per region.",
    "tags": ["financial"],
    "intents": ["describe"],
    "operators": ["AGG_ACME_TRIM", "GROUP_CATEGORY"]
  },
  "cohort": {"filename": "sales.pulse"},
  "aggregations": [{"type": "AGG_ACME_TRIM", "field": "revenue"}],
  "groups": [{"type": "GROUP_CATEGORY", "field": "region"}]
}`

// extExampleCompose is a valid compose-rooted embedder example using
// only built-ins; its description routes to AGG_ACME_OTHER.
const extExampleCompose = `{
  "_meta": {
    "name": "acme_two_slices",
    "category": "acme",
    "description": "Two sums side by side; unlike AGG_ACME_OTHER it keeps tails.",
    "tags": ["comparison", "cohort-analysis", "compose"],
    "operators": ["AGG_SUM"]
  },
  "requests": [
    {"cohort": {"filename": "sales.pulse"}, "aggregations": [{"type": "AGG_SUM", "field": "revenue"}]}
  ]
}`

func TestLoadExtensionExamples_Valid(t *testing.T) {
	got, err := LoadExtensionExamples(extSkillFS(map[string]string{
		"trim.json": extExampleTrim, "two.json": extExampleCompose,
	}), extSkillSnapshot())
	if err != nil {
		t.Fatalf("LoadExtensionExamples: %v", err)
	}
	if len(got) != 2 || got[0].Example.Name != "acme-trimmed-revenue" || got[1].Example.Name != "acme_two_slices" {
		t.Fatalf("got %d examples, want both sorted by name", len(got))
	}
	trim := got[0]
	if strings.Contains(string(trim.Example.Body), "_meta") || !slices.Equal(trim.Intents, []string{"describe"}) {
		t.Errorf("trim: body keeps _meta or intents lost: %s %v", trim.Example.Body, trim.Intents)
	}
	if !slices.Equal(trim.ExtOperators, []string{"AGG_ACME_TRIM"}) || !slices.Equal(got[1].ExtOperators, []string{"AGG_ACME_OTHER"}) {
		t.Errorf("ExtOperators = %v / %v", trim.ExtOperators, got[1].ExtOperators)
	}
	if none, err := LoadExtensionExamples(nil, extSkillSnapshot()); none != nil || err != nil {
		t.Errorf("nil fs: %v, %v", none, err)
	}
}

func TestLoadExtensionExamples_Refusals(t *testing.T) {
	sub := func(old, new string) string { return strings.Replace(extExampleTrim, old, new, 1) }
	cases := []struct {
		name   string
		files  map[string]string
		code   errors.Code
		reason string
	}{
		{"non-json entry", map[string]string{"notes.md": "x"}, errors.PULSE_EXTENSION_EXAMPLE_INVALID, ExampleReasonLayout},
		{"not an object", map[string]string{"a.json": "[1]"}, errors.PULSE_EXTENSION_EXAMPLE_INVALID, ExampleReasonMeta},
		{"no _meta", map[string]string{"a.json": `{"cohort":{}}`}, errors.PULSE_EXTENSION_EXAMPLE_INVALID, ExampleReasonMeta},
		{"unknown _meta key", map[string]string{"a.json": sub(`"category"`, `"owner": "me", "category"`)}, errors.PULSE_EXTENSION_EXAMPLE_INVALID, ExampleReasonMeta},
		{"bad name", map[string]string{"a.json": sub("acme-trimmed-revenue", "Acme Trimmed")}, errors.PULSE_EXTENSION_EXAMPLE_INVALID, ExampleReasonName},
		{"builtin name", map[string]string{"a.json": sub("acme-trimmed-revenue", "facet_simple_one_field")}, errors.PULSE_EXTENSION_EXAMPLE_COLLISION, ExampleReasonBuiltin},
		{"duplicate name", map[string]string{"a.json": extExampleTrim, "b.json": extExampleTrim}, errors.PULSE_EXTENSION_EXAMPLE_COLLISION, ExampleReasonDuplicate},
		{"bad category", map[string]string{"a.json": sub(`"acme"`, `"Acme Corp"`)}, errors.PULSE_EXTENSION_EXAMPLE_INVALID, ExampleReasonCategory},
		{"no description", map[string]string{"a.json": sub("Trimmed mean revenue per region.", " ")}, errors.PULSE_EXTENSION_EXAMPLE_INVALID, ExampleReasonDescription},
		{"tag not canonical", map[string]string{"a.json": sub(`"financial"`, `"acme-stuff"`)}, errors.PULSE_EXTENSION_EXAMPLE_INVALID, ExampleReasonTags},
		{"intent not in taxonomy", map[string]string{"a.json": sub(`"describe"`, `"astrology"`)}, errors.PULSE_EXTENSION_EXAMPLE_INVALID, ExampleReasonIntents},
		{"capability is an operator", map[string]string{"a.json": sub(`"intents"`, `"capabilities": ["AGG_SUM"], "intents"`)}, errors.PULSE_EXTENSION_EXAMPLE_INVALID, ExampleReasonCapabilities},
		{"unknown body key", map[string]string{"a.json": sub(`"groups"`, `"grups"`)}, errors.PULSE_EXTENSION_EXAMPLE_INVALID, ExampleReasonBody},
		{"compose root, bad slot", map[string]string{"a.json": strings.Replace(extExampleCompose, `"aggregations"`, `"aggs"`, 1)}, errors.PULSE_EXTENSION_EXAMPLE_INVALID, ExampleReasonBody},
		{"operators differ from body", map[string]string{"a.json": sub(`, "GROUP_CATEGORY"]`, `]`)}, errors.PULSE_EXTENSION_EXAMPLE_INVALID, ExampleReasonOperatorsBody},
		{"unregistered operator", map[string]string{"a.json": strings.ReplaceAll(extExampleTrim, "AGG_ACME_TRIM", "AGG_ACME_NOPE")}, errors.PULSE_EXTENSION_EXAMPLE_INVALID, ExampleReasonOperators},
		{"operator named off an edge", map[string]string{"a.json": sub(`"field": "revenue"}`, `"field": "revenue", "label": "TEST_WELCH"}`)}, errors.PULSE_EXTENSION_EXAMPLE_INVALID, ExampleReasonEdgeCoverage},
		{"capability contradicts its detector", map[string]string{"a.json": sub(`"intents"`, `"capabilities": ["capability:crosstab"], "intents"`)}, errors.PULSE_EXTENSION_EXAMPLE_INVALID, ExampleReasonEdgeCoverage},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadExtensionExamples(extSkillFS(tc.files), extSkillSnapshot())
			var ce *errors.CodedError
			if !stderrors.As(err, &ce) || ce.Code != tc.code || ce.Details["reason"] != tc.reason {
				t.Fatalf("err = %v, want %s reason %s", err, tc.code, tc.reason)
			}
			if ce.Details["example"] == nil {
				t.Error("details lack example")
			}
		})
	}
	// A directory entry is a layout refusal too.
	dir := fstest.MapFS{"sub/a.json": &fstest.MapFile{Data: []byte(extExampleTrim)}}
	var ce *errors.CodedError
	if _, err := LoadExtensionExamples(dir, extSkillSnapshot()); !stderrors.As(err, &ce) || ce.Details["reason"] != ExampleReasonLayout {
		t.Errorf("directory: err = %v, want layout", err)
	}
}

// TestInstanceOntology_ExtensionExamples: an embedder example is an
// example node with the edges a built-in gets (operators, intents,
// detector, description mention, a built-in skill's See tags), and is no
// node once an extension operator it names is hidden.
func TestInstanceOntology_ExtensionExamples(t *testing.T) {
	exs, err := LoadExtensionExamples(extSkillFS(map[string]string{
		"trim.json": extExampleTrim, "two.json": extExampleCompose,
	}), extSkillSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	snap := extSkillSnapshot()
	snap.Examples = exs
	g := extendOntology(BaseOntology(), snap)
	trim := OntologyID(descriptor.OntologyNodeExample, "acme-trimmed-revenue")
	two := OntologyID(descriptor.OntologyNodeExample, "acme_two_slices")
	want := []descriptor.OntologyEdge{
		ontoEdge(opID("AGG_ACME_TRIM"), trim, descriptor.OntologyEdgeExemplifiedBy),
		ontoEdge(opID("GROUP_CATEGORY"), trim, descriptor.OntologyEdgeExemplifiedBy),
		ontoEdge(trim, OntologyID(descriptor.OntologyNodeIntent, IntentDescribe), descriptor.OntologyEdgeServesIntent),
		ontoEdge(two, featCompose, descriptor.OntologyEdgeRequiresCapability),
		ontoEdge(two, opID("AGG_ACME_OTHER"), descriptor.OntologyEdgeRoutesTo),
	}
	for _, e := range want {
		if !hasEdge(g, e) {
			t.Errorf("missing edge %+v", e)
		}
	}
	// A built-in atomic skill's `## See` tags=[…] reaches an embedder
	// example carrying the tags.
	if len(g.In(two, descriptor.OntologyEdgeExemplifiedBy)) < 2 {
		t.Errorf("acme_two_slices in-edges = %v; want the AGG_SUM edge plus a See-tags edge", g.In(two, descriptor.OntologyEdgeExemplifiedBy))
	}
	if p := g.Problems(); len(p) != 0 {
		t.Errorf("problems = %v", p)
	}

	// AGG_ACME_TRIM hidden (dropped from the snapshot): its example is no
	// node; AGG_ACME_OTHER hidden: the compose example goes too.
	g = extendOntology(BaseOntology(), &ExtensionsSnapshot{Aggregators: []descriptor.OperatorMeta{{Name: "AGG_ACME_OTHER"}}, Examples: exs})
	if g.Has(trim) || !g.Has(two) {
		t.Errorf("TRIM hidden: has trim = %v (want false), has two = %v (want true)", g.Has(trim), g.Has(two))
	}
	g = extendOntology(BaseOntology(), &ExtensionsSnapshot{Aggregators: []descriptor.OperatorMeta{{Name: "AGG_ACME_TRIM"}}, Examples: exs})
	if !g.Has(trim) || g.Has(two) {
		t.Errorf("OTHER hidden: has trim = %v (want true), has two = %v (want false)", g.Has(trim), g.Has(two))
	}
}

// TestDiscovery_ExtensionExamples: a hide-nothing instance searches,
// serves and counts embedder examples with the built-ins (unchanged);
// a profile hiding a built-in operator an embedder example uses prunes
// it to the nonexistent answer.
func TestDiscovery_ExtensionExamples(t *testing.T) {
	exs, err := LoadExtensionExamples(extSkillFS(map[string]string{
		"trim.json": extExampleTrim, "two.json": extExampleCompose,
	}), extSkillSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	snap := extSkillSnapshot()
	snap.Examples = exs
	d := NewInstanceSnapshot(snap, FeatureSet{Enabled: featureNames()}).Discovery()

	hits := d.ExamplesSearch("trimmed", nil, "")
	if len(hits) != 1 || hits[0].Name != "acme-trimmed-revenue" {
		t.Errorf("search trimmed = %v", hits)
	}
	if cat := d.ExamplesSearch("", nil, "acme"); len(cat) != 2 {
		t.Errorf("category acme = %d hits, want 2", len(cat))
	}
	ex, ok := d.Example("acme_two_slices")
	if !ok || strings.Contains(string(ex.Body), "_meta") || !strings.Contains(string(ex.Body), `"requests"`) {
		t.Errorf("Example = %v, %v", ex, ok)
	}
	full := fullDiscovery.ExamplesSearch("", nil, "")
	all := d.ExamplesSearch("", nil, "")
	if len(all) != len(full)+2 {
		t.Errorf("merged library = %d, want %d", len(all), len(full)+2)
	}
	n, cats, _ := d.ExampleStats()
	fn, _, _ := fullDiscovery.ExampleStats()
	if n != fn+2 || !slices.Contains(cats, "acme") {
		t.Errorf("stats = %d %v, want %d with acme", n, cats, fn+2)
	}
	for _, name := range []string{"facet_simple_one_field", "export-overlays-arrow"} {
		a, _ := d.Example(name)
		b, _ := fullDiscovery.Example(name)
		if string(a.Body) != string(b.Body) {
			t.Errorf("%s: built-in body changed", name)
		}
	}

	// GROUP_CATEGORY hidden: the trim example goes (built-in operator
	// in-edge), the compose example stays.
	var enabled []string
	for _, f := range featureNames() {
		if f != "GROUP_CATEGORY" {
			enabled = append(enabled, f)
		}
	}
	d = NewInstanceSnapshot(snap, FeatureSet{Enabled: enabled, Hidden: []string{"GROUP_CATEGORY"}}).Discovery()
	if ex, ok := d.Example("acme-trimmed-revenue"); ok || ex != nil {
		t.Error("trim example served with GROUP_CATEGORY hidden")
	}
	if len(d.ExamplesSearch("trimmed", nil, "")) != 0 {
		t.Error("trim example searchable with GROUP_CATEGORY hidden")
	}
	if _, ok := d.Example("acme_two_slices"); !ok {
		t.Error("compose example pruned, want kept")
	}
}
