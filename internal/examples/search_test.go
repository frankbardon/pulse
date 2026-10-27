package examples

import (
	"slices"
	"testing"
)

// searchLib is a small synthetic library, sorted by name.
var searchLib = []*Example{
	{Name: "alpha", Category: "tests", Description: "Compare spend across regions", Operators: []string{"TEST_ANOVA_F"}, Intents: []string{"compare_groups"}, Tags: []string{"k-sample"}},
	{Name: "beta", Category: "tests", Description: "Correlate price with sales", Operators: []string{"TEST_PEARSON_R"}, Intents: []string{"relationship"}},
	{Name: "gamma", Category: "aggregations", Description: "Average spend by groups", Operators: []string{"AGG_AVERAGE"}, Intents: []string{"describe"}},
	{Name: "zeta", Category: "tests", Description: "Kruskal comparison", Operators: []string{"TEST_KRUSKAL_WALLIS"}, Intents: []string{"compare_groups"}},
}

var searchOpts = Options{
	Aliases: map[string]string{"kruskal.test": "TEST_KRUSKAL_WALLIS", "chi-square test": "TEST_CHISQ", "aov": "TEST_ANOVA_F", "comparison": "TEST_ANOVA_F"},
	Sounds:  map[string]string{"do x and y move together?": "relationship"},
}

func TestTokenize(t *testing.T) {
	t.Parallel()

	got := Tokenize("Welch's t-test: TEST_T (2-sample)")
	want := []string{"welch", "s", "t", "test", "test", "t", "2", "sample"}
	if !slices.Equal(got, want) {
		t.Errorf("Tokenize = %v, want %v", got, want)
	}
}

func TestSearchQuery_Tiers(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		q     Query
		want  []string
		empty bool
	}{
		// one plain word: legacy substring (spen ⊂ spend), no tokenizing
		{name: "one word substring", q: Query{Query: "spen"}, want: []string{"alpha", "gamma"}},
		// tokens: every content token somewhere; stop words ignored;
		// score = distinct fields (alpha: description+intents = 2)
		{name: "multi-word tokens", q: Query{Query: "compare the groups"}, want: []string{"alpha", "zeta"}},
		// exact tokens only: "group" ≠ "groups", no stemming — the
		// exact tier finds nothing (pinned below), so the fuzzy tier
		// (one edit) answers
		{name: "no stemming, fuzzy fallback", q: Query{Query: "spend group"}, want: []string{"alpha", "gamma"}},
		// synonym fallback only when no literal hit
		{name: "alias one word", q: Query{Query: "aov"}, want: []string{"alpha"}},
		{name: "alias punctuated", q: Query{Query: "run kruskal.test now"}, want: []string{"zeta"}},
		{name: "alias token form", q: Query{Query: "kruskal test please"}, want: []string{"zeta"}},
		{name: "sound phrase", q: Query{Query: "move together"}, want: []string{"beta"}},
		{name: "sound placeholder only", q: Query{Query: "x and y"}, empty: true},
		{name: "intent filter", q: Query{Intent: "compare_groups"}, want: []string{"alpha", "zeta"}},
		{name: "intent + query", q: Query{Query: "kruskal", Intent: "compare_groups"}, want: []string{"zeta"}},
		{name: "category", q: Query{Query: "spend", Category: "aggregations"}, want: []string{"gamma"}},
	}
	for _, c := range cases {
		got := names(SearchQuery(searchLib, c.q, searchOpts))
		if c.empty {
			if len(got) != 0 {
				t.Errorf("%s: got %v, want none", c.name, got)
			}
			continue
		}
		if !slices.Equal(got, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
	// Literal hits win over synonyms: "comparison" matches zeta's text, so
	// its alias (→ alpha's TEST_ANOVA_F) is never consulted.
	if got := names(SearchQuery(searchLib, Query{Query: "comparison"}, searchOpts)); !slices.Equal(got, []string{"zeta"}) {
		t.Errorf("literal: %v", got)
	}
	// The exact token tier itself never stems.
	for _, d := range searchLib {
		doc := &searchDoc{ex: d, intents: d.Intents}
		doc.tokenize()
		if newTokenTier(Tokenize("spend group")).match(doc) != 0 {
			t.Errorf("token tier stemmed %s", d.Name)
		}
	}
	// Zero Options disables the synonym tier.
	if got := SearchQuery(searchLib, Query{Query: "aov"}, Options{}); len(got) != 0 {
		t.Errorf("synonyms without tables: %v", names(got))
	}
}

func TestSearchQuery_ScoreThenName(t *testing.T) {
	t.Parallel()

	lib := []*Example{
		{Name: "a", Description: "spend", Operators: []string{"AGG_SUM"}},
		{Name: "b", Description: "spend", Operators: []string{"AGG_SUM"}, Tags: []string{"spend"}, Intents: []string{"spend"}},
		{Name: "c", Description: "spend"},
	}
	got := names(SearchQuery(lib, Query{Query: "sum spend"}, Options{}))
	if want := []string{"b", "a"}; !slices.Equal(got, want) {
		t.Errorf("ranking = %v, want %v", got, want)
	}
}

func TestSearchQuery_VisibilityBeforeTiers(t *testing.T) {
	t.Parallel()

	opts := searchOpts
	opts.KeepExample = func(n string) bool { return n != "zeta" }
	// zeta alone matches "comparison" literally; hidden, it must not
	// stop the search at the literal tier, so the alias answers.
	if got := names(SearchQuery(searchLib, Query{Query: "comparison"}, opts)); !slices.Equal(got, []string{"alpha"}) {
		t.Errorf("hidden example decided the tier: %v", got)
	}
	opts.KeepIntent = func(id string) bool { return id != "relationship" }
	if got := SearchQuery(searchLib, Query{Query: "move together"}, opts); len(got) != 0 {
		t.Errorf("hidden intent still matched: %v", names(got))
	}
	for _, s := range SearchQuery(searchLib, Query{}, opts) {
		if slices.Contains(s.Intents, "relationship") {
			t.Errorf("%s lists hidden intent", s.Name)
		}
	}
}

// TestSearchQuery_FuzzyTier pins the typo tier: last, content tokens
// only, budget by token length (≤3 never, 4–7 one edit, ≥8 two), and
// never consulted when an earlier tier answers.
func TestSearchQuery_FuzzyTier(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		q     Query
		want  []string
		empty bool
	}{
		{name: "one edit", q: Query{Query: "regons"}, want: []string{"alpha"}},
		{name: "transposition", q: Query{Query: "sepnd"}, want: []string{"alpha", "gamma"}},
		{name: "two edits on a long token", q: Query{Query: "corelatte"}, want: []string{"beta"}},
		{name: "two edits on a short token", q: Query{Query: "corelat"}, empty: true},
		{name: "every token must match", q: Query{Query: "sepnd regons"}, want: []string{"alpha"}},
		{name: "stop words skipped", q: Query{Query: "the sepnd of regons"}, want: []string{"alpha"}},
		{name: "short tokens never fuzz", q: Query{Query: "spedn by grp"}, empty: true},
		// "aggs" is one edit from gamma's "agg", but a short field
		// token is never a fuzzy target either
		{name: "short targets never fuzz", q: Query{Query: "aggs"}, empty: true},
		{name: "intent filter still applies", q: Query{Query: "sepnd", Intent: "describe"}, want: []string{"gamma"}},
	}
	for _, c := range cases {
		got := names(SearchQuery(searchLib, c.q, searchOpts))
		if c.empty {
			if len(got) != 0 {
				t.Errorf("%s: got %v, want none", c.name, got)
			}
			continue
		}
		if !slices.Equal(got, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
	// An exact hit wins: "regions" matches alpha literally, so the
	// fuzzy tier is never consulted.
	if got := names(SearchQuery(searchLib, Query{Query: "regions"}, searchOpts)); !slices.Equal(got, []string{"alpha"}) {
		t.Errorf("exact: %v", got)
	}
}

func TestFuzzyEdits(t *testing.T) {
	t.Parallel()

	for n, want := range map[int]int{1: 0, 3: 0, 4: 1, 7: 1, 8: 2, 20: 2} {
		if got := fuzzyEdits(n); got != want {
			t.Errorf("fuzzyEdits(%d) = %d, want %d", n, got, want)
		}
	}
	for _, c := range []struct {
		a, b string
		k    int
		want bool
	}{
		{"regon", "region", 1, true},
		{"regoin", "region", 1, true},
		{"rgn", "region", 2, false},
		{"abcd", "abcd", 0, true},
		{"abcd", "abce", 0, false},
		{"kitten", "sitting", 2, false},
		{"kitten", "sitting", 3, true},
	} {
		if got := withinEdits(c.a, c.b, c.k); got != c.want {
			t.Errorf("withinEdits(%q, %q, %d) = %v, want %v", c.a, c.b, c.k, got, c.want)
		}
	}
}
