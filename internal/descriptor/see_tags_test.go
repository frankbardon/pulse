package descriptor

import (
	"reflect"
	"slices"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/internal/examples"
	"github.com/frankbardon/pulse/internal/skills"
)

// TestSeeTags pins every `tags=[…]` form the pack writes: plain, quoted,
// `a|b` alternatives and several lists in one span.
func TestSeeTags(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		span string
		want []seeTagSearch
	}{
		{"plain", "pulse_examples_search tags=[p, q]", []seeTagSearch{{{"p"}, {"q"}}}},
		{"bare", "tags=[feature-engineering]", []seeTagSearch{{{"feature-engineering"}}}},
		{"quoted", `pulse_examples_search tags=["time-series"]`, []seeTagSearch{{{"time-series"}}}},
		{"single-quoted", `tags=['a', "b"]`, []seeTagSearch{{{"a"}, {"b"}}}},
		{"alternatives", `pulse_examples_search tags=["cross-tabulation"|"survey"]`, []seeTagSearch{{{"cross-tabulation", "survey"}}}},
		{"alternatives-and", "tags=[a|b, c]", []seeTagSearch{{{"a", "b"}, {"c"}}}},
		{"several-lists", `pulse_examples_search tags=["x"] or tags=[y, z]`, []seeTagSearch{{{"x"}}, {{"y"}, {"z"}}}},
		{"empty-entries", `tags=[ , "", a| ]`, []seeTagSearch{{{"a"}}}},
		{"stem", "op-agg-count", nil},
		{"empty-list", "tags=[]", nil},
		{"unclosed", "tags=[a, b", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := seeTags(c.span)
			if ok != (c.want != nil) || !reflect.DeepEqual(got, c.want) {
				t.Errorf("seeTags(%q) = %q, %v; want %q", c.span, got, ok, c.want)
			}
		})
	}
}

// TestSeeTagsMatch: an entry takes any alternative, entries AND, lists OR.
func TestSeeTagsMatch(t *testing.T) {
	t.Parallel()

	cases := []struct {
		span string
		have []string
		want bool
	}{
		{"tags=[p, q]", []string{"p", "q", "r"}, true},
		{"tags=[p, q]", []string{"p"}, false},
		{`tags=["p"]`, []string{"p"}, true},
		{"tags=[a|b, c]", []string{"b", "c"}, true},
		{"tags=[a|b, c]", []string{"a", "b"}, false},
		{"tags=[x] tags=[y]", []string{"y"}, true},
		{"tags=[x] tags=[y]", []string{"z"}, false},
	}
	for _, c := range cases {
		searches, _ := seeTags(c.span)
		if got := seeTagsMatch(searches, c.have); got != c.want {
			t.Errorf("seeTagsMatch(%q, %q) = %v, want %v", c.span, c.have, got, c.want)
		}
	}
}

// TestOntology_TopicalSeeTagEdges: a shipped topical skill writing each
// once-broken form — quoted tags, `a|b` alternatives — links every
// example its search answers, and a span holding several lists links
// the examples of each.
func TestOntology_TopicalSeeTagEdges(t *testing.T) {
	t.Parallel()

	g := BaseOntology()
	cases := []struct {
		skill string
		form  string
		tags  [][]string // any-of: each inner list is one AND search
	}{
		{"window-design", `tags=["time-series"]`, [][]string{{"time-series"}}},
		{"aggregation-design", `tags=["welford-triple"]`, [][]string{{"welford-triple"}}},
		{"grouper-design", `tags=["cross-tabulation"|"survey"]`, [][]string{{"cross-tabulation"}, {"survey"}}},
	}
	for _, c := range cases {
		body, ok := skills.Raw(c.skill)
		if !ok {
			t.Fatalf("no skill %q", c.skill)
		}
		spans := backtickSpans(seeSection(body))
		if !slices.Contains(spans, "pulse_examples_search "+c.form) && !slices.Contains(spans, c.form) {
			t.Fatalf("skill %q no longer writes %s in its See section; pick another", c.skill, c.form)
		}
		n := 0
		for _, tags := range c.tags {
			for _, s := range examples.Search("", tags, "") {
				n++
				if e := ontoEdge(skillID(c.skill), OntologyID(descriptor.OntologyNodeExample, s.Name), descriptor.OntologyEdgeExemplifiedBy); !hasEdge(g, e) {
					t.Errorf("%s %s: missing %+v", c.skill, c.form, e)
				}
			}
		}
		if n == 0 {
			t.Errorf("%s %s: no example carries the tags (vacuous)", c.skill, c.form)
		}
	}

	sg := buildOntology(ontologySources{
		skills:    []skills.Metadata{{Name: "topic", Kind: "design"}},
		skillBody: func(string) (string, bool) { return "## See\n- `tags=[\"x\"] or tags=[y|z]`\n", true },
		examples: []ontologyExample{
			{Name: "ex-x", Tags: []string{"x"}},
			{Name: "ex-z", Tags: []string{"z"}},
			{Name: "ex-w", Tags: []string{"w"}},
		},
	})
	want := []descriptor.OntologyEdge{
		ontoEdge(skillID("topic"), "example:ex-x", descriptor.OntologyEdgeExemplifiedBy),
		ontoEdge(skillID("topic"), "example:ex-z", descriptor.OntologyEdgeExemplifiedBy),
	}
	if got := sg.Ontology().Edges; !reflect.DeepEqual(got, want) {
		t.Errorf("several-lists span edges = %+v, want %+v", got, want)
	}
}
