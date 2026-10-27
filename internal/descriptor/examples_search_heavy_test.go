//go:build heavy

package descriptor

import (
	"slices"
	"testing"

	"github.com/frankbardon/pulse/internal/examples"
)

// TestExamplesSearch_OneWordPinnedToLegacy pins the back-compat
// contract: with no intent, every one-word query the library's own
// vocabulary yields (plus mixed-case spellings) answers exactly the
// pre-tier result — same set, same order — whenever the legacy matcher
// found anything; the synonym tier is a fallback only.
func TestExamplesSearch_OneWordPinnedToLegacy(t *testing.T) {
	t.Parallel()

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
