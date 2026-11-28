package examples

import (
	"slices"
	"testing"
)

// preEditMissingDescription is agg_missing_by_channel's description
// before docs-validation E2-S2 reworded it: the text the eval agent's
// plain-word query "null missing rate" found nothing against (#803).
const preEditMissingDescription = "Null check on intake.pulse: AGG_COUNT (present values) beside AGG_NULL_COUNT on the nullable income and age fields, per intake channel via GROUP_CATEGORY. The null counts read the per-record null bitmap; income is blank on far more paper forms than web forms, while age gaps are small and even."

// TestSearchQuery_WordSynonymTier pins the plain-word synonym tier on
// the eval miss behind #803 and its ordering against the other tiers.
func TestSearchQuery_WordSynonymTier(t *testing.T) {
	t.Parallel()

	lib := []*Example{
		{Name: "agg_missing_by_channel", Description: preEditMissingDescription, Operators: []string{"AGG_COUNT", "AGG_NULL_COUNT", "GROUP_CATEGORY"}, Intents: []string{"data_quality"}},
		{Name: "region_share", Description: "Each region's share of the total", Operators: []string{"OVERLAY_SHARE_OF_TOTAL"}},
		{Name: "spend_mean", Description: "Mean spend per region", Operators: []string{"AGG_AVERAGE"}},
	}
	cases := []struct {
		name  string
		q     string
		want  []string
		empty bool
	}{
		// the eval miss: "rate" reaches the text's "counts" (null and
		// missing are literal); the exact tier misses on "rate"
		{name: "eval miss", q: "null missing rate", want: []string{"agg_missing_by_channel"}},
		{name: "one word", q: "blanks", want: []string{"agg_missing_by_channel"}},
		{name: "avg for average / mean", q: "avg spend", want: []string{"spend_mean"}},
		{name: "proportion for share", q: "region proportion", want: []string{"region_share"}},
		// every token, or a synonym, must match: "revenue" has neither
		{name: "every token", q: "missing revenue", empty: true},
		// every token literal, but in no single example
		{name: "no single example", q: "region intake", empty: true},
	}
	for _, c := range cases {
		got := names(SearchQuery(lib, Query{Query: c.q}, Options{}))
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
	// An exact hit wins: "null" is in the text, so the word tier never
	// widens it.
	if got := names(SearchQuery(lib, Query{Query: "null"}, Options{})); !slices.Equal(got, []string{"agg_missing_by_channel"}) {
		t.Errorf("exact: %v", got)
	}
	// An alias answers before a word synonym.
	opts := Options{Aliases: map[string]string{"nonresponse": "OVERLAY_SHARE_OF_TOTAL"}}
	if got := names(SearchQuery(lib, Query{Query: "missing nonresponse"}, opts)); !slices.Equal(got, []string{"region_share"}) {
		t.Errorf("alias tier first: %v", got)
	}
	// A typo still reaches the fuzzy tier after the word tier misses.
	if got := names(SearchQuery(lib, Query{Query: "regoin"}, Options{})); !slices.Equal(got, []string{"region_share", "spend_mean"}) {
		t.Errorf("fuzzy after words: %v", got)
	}
}

// TestSearchLibrary_WordSynonymEvalMiss replays the #803 eval miss on the
// embedded library: with agg_missing_by_channel's description put back
// to its pre-edit text, "null missing rate" ranks it first.
func TestSearchLibrary_WordSynonymEvalMiss(t *testing.T) {
	t.Parallel()

	idx := loadIndex()
	var lib []*Example
	for _, n := range idx.all {
		ex := idx.byName[n]
		if n == "agg_missing_by_channel" {
			c := *ex
			c.Description = preEditMissingDescription
			ex = &c
		}
		lib = append(lib, ex)
	}
	got := names(SearchQuery(lib, Query{Query: "null missing rate"}, Options{}))
	if len(got) == 0 || got[0] != "agg_missing_by_channel" {
		t.Fatalf("null missing rate = %v, want agg_missing_by_channel first", got)
	}
}

// TestWordSynonyms_Table: every word is a single lower-case Tokenize
// token, in exactly one group of two or more.
func TestWordSynonyms_Table(t *testing.T) {
	t.Parallel()

	seen := map[string]int{}
	for i, g := range wordSynonyms {
		if len(g) < 2 {
			t.Errorf("group %d %v has one word", i, g)
		}
		for _, w := range g {
			if toks := Tokenize(w); len(toks) != 1 || toks[0] != w || !isContent(w) {
				t.Errorf("%q is no single content token", w)
			}
			if j, dup := seen[w]; dup {
				t.Errorf("%q in groups %d and %d", w, j, i)
			}
			seen[w] = i
		}
	}
}
