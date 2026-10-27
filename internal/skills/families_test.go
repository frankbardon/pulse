package skills

import "testing"

// TestBodyBudget_Tiers pins the frozen per-family tiers (U38): the most
// specific stem prefix wins, op-reg-mod-* rides the op-reg- tier, a
// design skill keeps the topical cap and an uncovered stem has none.
// The same function backs TestSkillTokenBudget and the embedder skill
// validation at pulse.New, so an embedder op skill gets its family's tier.
func TestBodyBudget_Tiers(t *testing.T) {
	t.Parallel()

	cases := []struct {
		stem, kind string
		want       int
		ok         bool
	}{
		{"op-agg-sum", "operator", 1800, true},
		{"op-attr-zscore", "operator", 1800, true},
		{"op-mat-pca", "operator", 1800, true},
		{"op-agg-acme-trim", "operator", 1800, true},
		{"op-test-welch", "operator", 2200, true},
		{"op-test-acme-x", "operator", 2200, true},
		{"op-overlay-t-cell", "operator", 2400, true},
		{"op-reg-ols", "operator", 3000, true},
		{"op-reg-mod-resample", "operator", 3000, true},
		{"op-reg-acme-fit", "operator", 3000, true},
		{"tool-predict", "", 2500, true},
		{"type-u8", "", 2000, true},
		{"ext-acme-guide", "design", 6000, true},
		{"aggregation-design", "design", 6000, true},
		{"ext-acme-guide", "", 0, false},
	}
	for _, tc := range cases {
		got, ok := BodyBudget(tc.stem, tc.kind)
		if got != tc.want || ok != tc.ok {
			t.Errorf("BodyBudget(%q, %q) = (%d, %v), want (%d, %v)", tc.stem, tc.kind, got, ok, tc.want, tc.ok)
		}
	}
}

// TestBodyBudgetTiers_MostSpecificFirst: every tier prefix that extends
// another sits before it, so the first match is the most specific one.
func TestBodyBudgetTiers_MostSpecificFirst(t *testing.T) {
	t.Parallel()

	tiers := BodyBudgetTiers()
	for i, a := range tiers {
		for _, b := range tiers[i+1:] {
			if len(b.Prefix) > len(a.Prefix) && b.Prefix[:len(a.Prefix)] == a.Prefix {
				t.Errorf("tier %q sits after the shorter %q and can never match", b.Prefix, a.Prefix)
			}
		}
	}
}
