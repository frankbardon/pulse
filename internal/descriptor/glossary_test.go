package descriptor

import (
	"slices"
	"strings"
	"testing"
	"unicode"

	"github.com/frankbardon/pulse/descriptor"
)

// TestGlossaryTermsResolve: the shipped glossary is well formed — unique kebab IDs,
// non-empty Short / WhyCare within their limits, every SeeAlso resolves,
// every jargon term has a Form, and no Form is claimed by two terms.
// The fixture arms prove the validator bites on a dangling SeeAlso and
// a duplicated Form. The "purpose" subtest is the Purpose half: every
// Purpose.Glossary ID resolves and jargon in Plain is linked.
func TestGlossaryTermsResolve(t *testing.T) {
	t.Run("purpose", testPurposeGlossaryLinks)
	for _, p := range glossaryProblems(Glossary()) {
		t.Error(p)
	}

	valid := func() []descriptor.Term {
		return []descriptor.Term{
			{ID: "alpha-term", Short: "a", WhyCare: "b", SeeAlso: []string{"beta-term"}, Jargon: true, Forms: []string{"alpha"}},
			{ID: "beta-term", Short: "a", WhyCare: "b", SeeAlso: []string{"alpha-term"}},
		}
	}
	if p := glossaryProblems(valid()); len(p) != 0 {
		t.Fatalf("valid fixture reported problems: %v", p)
	}
	long := strings.Repeat("x", glossaryShortMax+1)
	longWhy := strings.Repeat("x", glossaryWhyCareMax+1)
	cases := []struct {
		name   string
		mutate func([]descriptor.Term) []descriptor.Term
		want   string
	}{
		{"dangling see-also", func(ts []descriptor.Term) []descriptor.Term {
			ts[0].SeeAlso = []string{"no-such-term"}
			return ts
		}, `SeeAlso "no-such-term" does not resolve`},
		{"self see-also", func(ts []descriptor.Term) []descriptor.Term {
			ts[0].SeeAlso = []string{"alpha-term"}
			return ts
		}, "cites itself"},
		{"repeated see-also", func(ts []descriptor.Term) []descriptor.Term {
			ts[0].SeeAlso = []string{"beta-term", "beta-term"}
			return ts
		}, "listed twice"},
		{"duplicate form", func(ts []descriptor.Term) []descriptor.Term {
			ts[1].Jargon = true
			ts[1].Forms = []string{"alpha"}
			return ts
		}, `Form "alpha" already belongs to term "alpha-term"`},
		{"uppercase form", func(ts []descriptor.Term) []descriptor.Term {
			ts[0].Forms = []string{"Alpha"}
			return ts
		}, "lowercase"},
		{"jargon without forms", func(ts []descriptor.Term) []descriptor.Term {
			ts[0].Forms = nil
			return ts
		}, "no Forms"},
		{"duplicate id", func(ts []descriptor.Term) []descriptor.Term {
			ts[1].ID = "alpha-term"
			return ts
		}, "declared twice"},
		{"non-kebab id", func(ts []descriptor.Term) []descriptor.Term {
			ts[1].ID = "Beta_Term"
			ts[0].SeeAlso = []string{"Beta_Term"}
			return ts
		}, "not kebab-case"},
		{"empty short", func(ts []descriptor.Term) []descriptor.Term {
			ts[0].Short = " "
			return ts
		}, "empty Short"},
		{"empty why-care", func(ts []descriptor.Term) []descriptor.Term {
			ts[0].WhyCare = ""
			return ts
		}, "empty WhyCare"},
		{"short too long", func(ts []descriptor.Term) []descriptor.Term {
			ts[0].Short = long
			return ts
		}, "Short is"},
		{"why-care too long", func(ts []descriptor.Term) []descriptor.Term {
			ts[0].WhyCare = longWhy
			return ts
		}, "WhyCare is"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			problems := glossaryProblems(tc.mutate(valid()))
			for _, p := range problems {
				if strings.Contains(p, tc.want) {
					return
				}
			}
			t.Errorf("want a problem containing %q, got %v", tc.want, problems)
		})
	}
}

// TestGlossary_Size: the glossary stays a ~60-term starter set.
func TestGlossary_Size(t *testing.T) {
	if n := len(Glossary()); n < 55 || n > 70 {
		t.Errorf("glossary has %d terms, want 55..70", n)
	}
}

// TestGlossary_RequiredTerms: every term the guided-analysis design
// names is present.
func TestGlossary_RequiredTerms(t *testing.T) {
	required := []string{
		"p-value", "alpha", "effect-size", "confidence-interval", "degrees-of-freedom",
		"variance", "standard-deviation", "covariance", "correlation", "z-score",
		"percentile", "outlier", "normal-distribution", "skew", "kurtosis",
		"null-hypothesis", "statistical-significance", "multiple-comparisons",
		"weighting", "listwise-deletion", "pairwise-deletion", "eigenvalue",
		"principal-component", "loading", "factor", "reliability", "centroid",
		"distance", "similarity", "stochastic-matrix", "steady-state", "raking",
		"overfitting", "multicollinearity", "residual", "r-squared",
		"regression-coefficient", "standard-error", "odds-ratio", "rank",
		"non-parametric", "independence",
	}
	for _, id := range required {
		if !IsGlossaryTerm(id) {
			t.Errorf("glossary lacks required term %q", id)
		}
	}
}

// effectSizeKeys is every details.effect_size key a statistical test
// emits — the pre-existing cohens_d / eta_squared plus the keys the
// guidance-metadata effort adds. A new effect-size key joins this list
// and the glossary together.
var effectSizeKeys = []string{
	"cohens_d", "cohens_h", "cramers_v", "epsilon_squared", "eta_squared",
	"omega_squared", "partial_eta_squared", "phi", "rank_biserial",
}

// TestGlossary_EffectSizeKeysHaveTerms: every effect-size output key has
// a term whose ID is the key in kebab case, and the term claims the key
// spelling as a Form so prose citing the raw key links to it.
func TestGlossary_EffectSizeKeysHaveTerms(t *testing.T) {
	forms := JargonForms()
	for _, key := range effectSizeKeys {
		id := strings.ReplaceAll(key, "_", "-")
		if !IsGlossaryTerm(id) {
			t.Errorf("effect-size key %q has no glossary term %q", key, id)
			continue
		}
		if forms[key] != id {
			t.Errorf("effect-size key %q is not a Form of term %q (maps to %q)", key, id, forms[key])
		}
	}
}

// TestGlossary_NoBands: conventional cut-offs belong to Interpretation
// bands, never to the definition of an effect size, so the effect-size
// terms' prose carries no numeric threshold other than a scale's own
// endpoints.
func TestGlossary_NoBands(t *testing.T) {
	scaleEnds := strings.NewReplacer("-1", "", "0%", "", "100%", "", "50%", "", "0", "", "1", "")
	byID := map[string]descriptor.Term{}
	for _, term := range Glossary() {
		byID[term.ID] = term
	}
	for _, key := range effectSizeKeys {
		term := byID[strings.ReplaceAll(key, "_", "-")]
		for _, text := range []string{term.Short, term.WhyCare} {
			if strings.IndexFunc(scaleEnds.Replace(text), unicode.IsDigit) >= 0 {
				t.Errorf("term %q prose carries a numeric threshold: %q", term.ID, text)
			}
		}
	}
}

// TestGlossary_Accessors: Glossary hands out deep copies, GlossaryIDs is
// sorted and complete, IsGlossaryTerm rejects unknown IDs, and
// JargonForms covers exactly the jargon terms' forms.
func TestGlossary_Accessors(t *testing.T) {
	g := Glossary()
	g[0].ID = "mutated"
	g[0].SeeAlso[0] = "mutated"
	g[0].Forms[0] = "mutated"
	again := Glossary()
	if again[0].ID == "mutated" || again[0].SeeAlso[0] == "mutated" || again[0].Forms[0] == "mutated" {
		t.Fatal("mutating Glossary()'s result changed the registry")
	}

	ids := GlossaryIDs()
	if !slices.IsSorted(ids) {
		t.Errorf("GlossaryIDs() not sorted: %v", ids)
	}
	if len(ids) != len(again) {
		t.Errorf("GlossaryIDs() has %d IDs, glossary has %d terms", len(ids), len(again))
	}
	for _, id := range ids {
		if !IsGlossaryTerm(id) {
			t.Errorf("IsGlossaryTerm(%q) = false", id)
		}
	}
	if IsGlossaryTerm("not-a-term") {
		t.Error("IsGlossaryTerm accepted an unknown ID")
	}

	forms := JargonForms()
	want := 0
	for _, term := range again {
		if !term.Jargon {
			continue
		}
		for _, f := range term.Forms {
			want++
			if forms[f] != term.ID {
				t.Errorf("JargonForms()[%q] = %q, want %q", f, forms[f], term.ID)
			}
		}
	}
	if len(forms) != want {
		t.Errorf("JargonForms() has %d forms, want %d", len(forms), want)
	}
	forms["p-value"] = "mutated"
	if JargonForms()["p-value"] != "p-value" {
		t.Error("mutating JargonForms()'s result changed the registry")
	}
}
