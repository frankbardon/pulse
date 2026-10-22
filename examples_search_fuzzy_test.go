package pulse

import (
	"slices"
	"strings"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/internal/examples"
)

// TestExamplesSearchWith_FuzzyTier: the typo tier lives on the shared
// search path, so the facade (and with it MCP and the CLI) answers a
// misspelt word — "regon" finds the region examples an intent keeps —
// while a correctly spelt one keeps its exact answer.
func TestExamplesSearchWith_FuzzyTier(t *testing.T) {
	p, err := New(Options{FS: afero.NewMemMapFs()})
	if err != nil {
		t.Fatal(err)
	}
	got, err := p.ExamplesSearchWith(ExamplesQuery{Query: "regon", Intent: "compare_groups"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 {
		t.Fatal(`"regon" under compare_groups found nothing`)
	}
	for _, s := range got {
		if !slices.Contains(s.Intents, "compare_groups") {
			t.Errorf("%s lacks compare_groups", s.Name)
		}
		var toks []string
		for _, f := range append([]string{s.Name, s.Description}, s.Operators...) {
			toks = append(toks, examples.Tokenize(f)...)
		}
		if !slices.Contains(toks, "region") {
			t.Errorf("%s matched %q without a region token: %s", s.Name, "regon", s.Description)
		}
	}
	// The exact tier still answers a correct spelling, unchanged.
	exact, err := p.ExamplesSearchWith(ExamplesQuery{Query: "region", Intent: "compare_groups"})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range exact {
		if !strings.Contains(strings.ToLower(s.Name+" "+s.Description+" "+strings.Join(s.Operators, " ")), "region") {
			t.Errorf("exact %q hit %s has no region substring", "region", s.Name)
		}
	}
	if len(exact) < len(got) {
		t.Errorf("exact search (%d) found fewer than the fuzzy one (%d)", len(exact), len(got))
	}
}
