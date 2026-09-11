package synth

import "testing"

// TestExprReadsIdentifier_ParsesRatherThanMatchesSubstrings is the
// falsifiable half of the self-reference exclusion.
//
// The exclusion has to be exact in BOTH directions. Miss a real
// self-reference and the documented normalisation idiom
// (`{"set_expr": {"nps": "int(nps)"}}`) silently retires the target's
// model; report one that is not there and an ordinary derived field
// keeps a model nothing will use, which is the waste and the false
// fidelity entry this story removes. A substring test fails the second
// direction on the first survey column named `nps_reason`.
func TestExprReadsIdentifier_ParsesRatherThanMatchesSubstrings(t *testing.T) {
	cases := []struct {
		name  string
		src   string
		ident string
		want  bool
	}{
		{"bare self reference", "nps", "nps", true},
		{"self reference in arithmetic", "nps + 1", "nps", true},
		{"self reference inside a call", "int(nps)", "nps", true},
		{"self reference inside a ternary", "isnull(nps) ? 0 : nps", "nps", true},
		{"self reference on the right of a comparison", "aware > nps", "nps", true},
		{"longer field containing the name", "nps_reason > 2", "nps", false},
		{"shorter field contained by the name", "np > 2", "nps", false},
		{"name appearing only in a string literal", "'nps'", "nps", false},
		{"unrelated identifiers", "aware * 2", "nps", false},
		{"no identifiers at all", "9", "nps", false},
		// Unparseable answers TRUE — the conservative direction, since
		// a false claim deletes structure while a missed claim only
		// wastes a stage. validateRules refuses this at spec parse, so
		// it is unreachable from any path that generates.
		{"unparseable is conservative", "nps +", "nps", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := exprReadsIdentifier(tc.src, tc.ident); got != tc.want {
				t.Fatalf("exprReadsIdentifier(%q, %q) = %v, want %v", tc.src, tc.ident, got, tc.want)
			}
		})
	}
}

// TestRuleClaims_KeyingAndOrder pins the claim set itself: which slots
// contribute, which conditionalities do, and the deterministic order.
//
// Order matters even though a second rule claiming the same field is
// not a conflict: the FIRST claim is the one a warning names, so a Go
// map walk here would make the text a reader sees depend on a map seed.
func TestRuleClaims_KeyingAndOrder(t *testing.T) {
	rules := []RuleSpec{
		// Claims both, sorted within the slot and set before set_expr.
		{Set: map[string]any{"b": 1.0, "a": 2.0}, SetExpr: map[string]string{"d": "1", "c": "2"}},
		// Claims nothing: conditional.
		{When: "a == 1", Set: map[string]any{"e": 1.0}, SetExpr: map[string]string{"f": "1"}},
		// Claims nothing: supplies no value.
		{SetNull: []string{"g"}, NullTogether: []string{"h", "i"}},
		// Claims nothing: reads its own target.
		{SetExpr: map[string]string{"j": "j + 1"}},
		// Claims: reads a DIFFERENT field.
		{SetExpr: map[string]string{"k": "j + 1"}},
	}
	got := ruleClaims(rules)
	want := []ruleClaim{
		{claimTarget{field: "a"}, "structural rule 0 (set)"},
		{claimTarget{field: "b"}, "structural rule 0 (set)"},
		{claimTarget{field: "c"}, "structural rule 0 (set_expr)"},
		{claimTarget{field: "d"}, "structural rule 0 (set_expr)"},
		{claimTarget{field: "k"}, "structural rule 4 (set_expr)"},
	}
	if len(got) != len(want) {
		t.Fatalf("ruleClaims returned %d claim(s), want %d\n got %+v\nwant %+v",
			len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("claim %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	if ruleClaims(nil) != nil {
		t.Fatal("a rules-free spec must contribute no claims at all")
	}
}
