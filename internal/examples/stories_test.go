package examples

import (
	"encoding/json"
	"io/fs"
	"slices"
	"strings"
	"testing"
)

// TestStoryProblems pins each story rule on a synthetic _meta + body:
// the question / interpretation pair, the digit-free interpretation and
// the fixed return.precision.
func TestStoryProblems(t *testing.T) {
	const okBody = `{"return":{"precision":4}}`
	cases := []struct {
		name string
		m    Meta
		body string
		want string // substring of the single problem; "" = none
	}{
		{"not a story", Meta{}, `{}`, ""},
		{"story", Meta{Question: "How?", Interpretation: "Tightly."}, okBody, ""},
		{"question alone", Meta{Question: "How?"}, okBody, "question without _meta.interpretation"},
		{"interpretation alone", Meta{Interpretation: "Tightly."}, okBody, "interpretation without _meta.question"},
		{"blank interpretation", Meta{Question: "How?", Interpretation: "  "}, okBody, "question without _meta.interpretation"},
		{"digit", Meta{Question: "How?", Interpretation: "About 5 units."}, okBody, "carries a digit"},
		{"zero digit", Meta{Question: "How?", Interpretation: "Near 0."}, okBody, "carries a digit"},
		{"no return", Meta{Question: "How?", Interpretation: "Tightly."}, `{}`, "return.precision 4 (got 0)"},
		{"other precision", Meta{Question: "How?", Interpretation: "Tightly."}, `{"return":{"precision":6}}`, "return.precision 4 (got 6)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := StoryProblems(c.m, []byte(c.body))
			if c.want == "" {
				if len(got) != 0 {
					t.Fatalf("problems %v, want none", got)
				}
				return
			}
			if len(got) != 1 || !strings.Contains(got[0], c.want) {
				t.Fatalf("problems %v, want one containing %q", got, c.want)
			}
		})
	}
}

// TestStories_LibraryHoldsTheRules runs StoryProblems over every
// embedded example, and requires at least one describe story.
func TestStories_LibraryHoldsTheRules(t *testing.T) {
	err := fs.WalkDir(content, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".json") {
			return err
		}
		data, err := content.ReadFile(p)
		if err != nil {
			return err
		}
		ex, m, err := Parse(data, false)
		if err != nil {
			return err
		}
		for _, prob := range StoryProblems(m, ex.Body) {
			t.Errorf("%s: %s", p, prob)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	describe := false
	for _, n := range Stories() {
		ex, _ := Get(n)
		describe = describe || slices.Contains(ex.Intents, "describe")
	}
	if !describe {
		t.Errorf("no story example declares the describe intent; stories: %v", Stories())
	}
}

// TestStoryGoldens_MatchStories: every story has an embedded golden
// and every golden belongs to a story, reachable through StoryGolden.
// (Whether a golden is CURRENT is the root package's
// TestExamples_StoryGoldens, which executes the requests.)
func TestStoryGoldens_MatchStories(t *testing.T) {
	stories, goldens := Stories(), StoryGoldenNames()
	if len(stories) == 0 {
		t.Fatal("no story examples")
	}
	if !slices.Equal(stories, goldens) {
		t.Fatalf("stories %v, goldens %v — run go test . -run TestExamples_StoryGoldens -update", stories, goldens)
	}
	for _, n := range stories {
		data, ok := StoryGolden(n)
		if !ok || !json.Valid(data) {
			t.Errorf("StoryGolden(%q): ok=%v, valid JSON=%v", n, ok, json.Valid(data))
		}
	}
	if _, ok := StoryGolden("no_such_story"); ok {
		t.Error("StoryGolden(no_such_story) reported a golden")
	}
}

// TestStoryFields_RideTheWire: a story's question and interpretation
// reach Get, All and Search results.
func TestStoryFields_RideTheWire(t *testing.T) {
	for _, n := range Stories() {
		ex, _ := Get(n)
		if ex.Question == "" || ex.Interpretation == "" {
			t.Errorf("Get(%q) dropped the story fields: %+v", n, ex)
		}
		var hit *ExampleSummary
		for _, s := range Search(n, nil, "") {
			if s.Name == n {
				hit = &s
			}
		}
		if hit == nil || hit.Question != ex.Question || hit.Interpretation != ex.Interpretation {
			t.Errorf("Search(%q) summary = %+v, want the story fields", n, hit)
		}
	}
}
