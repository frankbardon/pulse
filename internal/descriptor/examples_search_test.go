package descriptor

import (
	stderrors "errors"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/examples"
)

func summaryNames(s []examples.ExampleSummary) []string {
	out := make([]string, len(s))
	for i, h := range s {
		out[i] = h.Name
	}
	return out
}

// legacySearch is the pre-tier matcher, kept as the oracle a one-word
// query is pinned to: a lower-cased substring of name, description or
// any operator, score = fields matched, ties alphabetical.
func legacySearch(q string) []string {
	q = strings.ToLower(strings.TrimSpace(q))
	type hit struct {
		name  string
		score int
	}
	var hits []hit
	for _, ex := range examples.All() {
		s := 0
		if strings.Contains(strings.ToLower(ex.Name), q) {
			s++
		}
		if strings.Contains(strings.ToLower(ex.Description), q) {
			s++
		}
		for _, op := range ex.Operators {
			if strings.Contains(strings.ToLower(op), q) {
				s++
				break
			}
		}
		if s > 0 {
			hits = append(hits, hit{ex.Name, s})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score > hits[j].score
		}
		return hits[i].name < hits[j].name
	})
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.name
	}
	return out
}

// TestExamplesSearch_OneWordPinnedToLegacy pins the back-compat
// contract: with no intent, every one-word query the library's own
// vocabulary yields (plus mixed-case spellings) answers exactly the
// pre-tier result — same set, same order — whenever the legacy matcher
// found anything; the synonym tier is a fallback only.
func TestExamplesSearch_OneWordPinnedToLegacy(t *testing.T) {
	vocab := map[string]struct{}{}
	for _, ex := range examples.All() {
		for _, s := range append([]string{ex.Name, ex.Description}, ex.Operators...) {
			for _, w := range examples.Tokenize(s) {
				vocab[w] = struct{}{}
			}
		}
	}
	for a := range BuiltinKnownAs() {
		for _, w := range examples.Tokenize(a) {
			vocab[w] = struct{}{}
		}
	}
	vocab["Welch"], vocab["ANOVA"], vocab["corr"], vocab["Mean"] = struct{}{}, struct{}{}, struct{}{}, struct{}{}
	snap := hidingSnapshot()
	checked := 0
	for w := range vocab {
		want := legacySearch(w)
		if len(want) == 0 {
			continue
		}
		checked++
		got, err := snap.ExamplesSearch(examples.Query{Query: w})
		if err != nil {
			t.Fatalf("%q: %v", w, err)
		}
		if !slices.Equal(summaryNames(got), want) {
			t.Errorf("one-word %q drifted from the legacy matcher:\n got %v\nwant %v", w, summaryNames(got), want)
		}
		if plain := summaryNames(examples.Search(w, nil, "")); !slices.Equal(plain, want) {
			t.Errorf("examples.Search(%q) drifted from the legacy matcher", w)
		}
	}
	if checked < 100 {
		t.Fatalf("only %d vocabulary words checked; the pin is vacuous", checked)
	}
}

// TestExamplesSearch_Relevance: multi-word queries, aliases and intent
// Sounds reach the examples an analyst means.
func TestExamplesSearch_Relevance(t *testing.T) {
	snap := hidingSnapshot()
	cases := []struct {
		query string
		// every hit must satisfy each, and the list must be non-empty
		wantIntent string
		wantOp     string
	}{
		{query: "compare groups", wantIntent: IntentCompareGroups},
		{query: "move together", wantIntent: IntentRelationship},
		{query: "Do X and Y move together?", wantIntent: IntentRelationship},
		{query: "anova", wantOp: "TEST_ANOVA_F"},
		{query: "aov", wantOp: "TEST_ANOVA_F"},
		{query: "run an aov on spend", wantOp: "TEST_ANOVA_F"},
		{query: "chisq.test", wantOp: "TEST_CHISQ"},
		{query: "cronbach's alpha", wantOp: "MAT_RELIABILITY", wantIntent: IntentMeasureConstruct},
		{query: "is this scale reliable? check internal consistency", wantOp: "MAT_RELIABILITY"},
		{query: "principal components", wantOp: "MAT_PCA"},
		{query: "variance inflation", wantOp: "MAT_COLLINEARITY"},
	}
	for _, c := range cases {
		hits, err := snap.ExamplesSearch(examples.Query{Query: c.query})
		if err != nil {
			t.Fatalf("%q: %v", c.query, err)
		}
		if len(hits) == 0 {
			t.Errorf("%q: no hits", c.query)
			continue
		}
		top := hits[0]
		if c.wantIntent != "" && !slices.Contains(top.Intents, c.wantIntent) {
			t.Errorf("%q: top hit %s intents %v lack %s", c.query, top.Name, top.Intents, c.wantIntent)
		}
		if c.wantOp != "" && !slices.ContainsFunc(hits, func(h examples.ExampleSummary) bool { return slices.Contains(h.Operators, c.wantOp) }) {
			t.Errorf("%q: no hit uses %s: %v", c.query, c.wantOp, summaryNames(hits))
		}
	}
}

// TestExamplesSearch_IntentFilter: the intent filter keeps only
// examples declaring the intent; an unknown or hidden intent is the
// recommend error code.
func TestExamplesSearch_IntentFilter(t *testing.T) {
	snap := hidingSnapshot()
	hits, err := snap.ExamplesSearch(examples.Query{Intent: IntentRelationship})
	if err != nil || len(hits) == 0 {
		t.Fatalf("relationship filter: %v hits, err %v", len(hits), err)
	}
	for _, h := range hits {
		if !slices.Contains(h.Intents, IntentRelationship) {
			t.Errorf("%s lacks relationship: %v", h.Name, h.Intents)
		}
	}
	narrowed, _ := snap.ExamplesSearch(examples.Query{Query: "pearson", Intent: IntentRelationship})
	if len(narrowed) == 0 || len(narrowed) > len(hits) {
		t.Errorf("query+intent = %d hits of %d", len(narrowed), len(hits))
	}

	wantCode := func(t *testing.T, s *InstanceSnapshot, intent string) map[string]any {
		t.Helper()
		_, err := s.ExamplesSearch(examples.Query{Intent: intent})
		var ce *errors.CodedError
		if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_RECOMMEND_INTENT_UNKNOWN {
			t.Fatalf("intent %q: err = %v, want PULSE_RECOMMEND_INTENT_UNKNOWN", intent, err)
		}
		return ce.Details
	}
	if d := wantCode(t, snap, "not_an_intent"); !slices.Contains(d["valid"].([]string), IntentFlows) {
		t.Errorf("valid = %v", d["valid"])
	}

	// Hide every operator serving drivers: the intent is pruned, so the
	// filter refuses it like an unknown one and valid omits it.
	flowOps := BaseOntology().OperatorsServing(IntentDrivers)
	if len(flowOps) == 0 {
		t.Fatal("no operator serves drivers")
	}
	if full, _ := snap.ExamplesSearch(examples.Query{Query: "which factors matter most"}); len(full) == 0 {
		t.Fatal("drivers Sound finds nothing on the full instance")
	}
	hidden := hidingSnapshot(flowOps...)
	if d := wantCode(t, hidden, IntentDrivers); slices.Contains(d["valid"].([]string), IntentDrivers) {
		t.Error("hidden intent listed as valid")
	}
	all, _ := hidden.ExamplesSearch(examples.Query{})
	for _, h := range all {
		if slices.Contains(h.Intents, IntentDrivers) {
			t.Errorf("%s still lists hidden intent drivers", h.Name)
		}
	}
	// A hidden intent's Sounds no longer expand a query.
	if hits, _ := hidden.ExamplesSearch(examples.Query{Query: "which factors matter most"}); len(hits) != 0 {
		t.Errorf("hidden drivers Sound still expands: %v", summaryNames(hits))
	}
}

// TestExamplesSearch_HiddenAliasAbsent: a hidden operator's aliases
// never expand a query and its examples are absent.
func TestExamplesSearch_HiddenAliasAbsent(t *testing.T) {
	full, _ := hidingSnapshot().ExamplesSearch(examples.Query{Query: "aov"})
	if len(full) == 0 {
		t.Fatal("aov finds nothing on the full instance")
	}
	hidden, _ := hidingSnapshot("TEST_ANOVA_F").ExamplesSearch(examples.Query{Query: "aov"})
	for _, h := range hidden {
		if slices.Contains(h.Operators, "TEST_ANOVA_F") {
			t.Errorf("%s uses hidden TEST_ANOVA_F", h.Name)
		}
	}
	if len(hidden) != 0 {
		t.Errorf("aov expands on an instance hiding TEST_ANOVA_F: %v", summaryNames(hidden))
	}
}
