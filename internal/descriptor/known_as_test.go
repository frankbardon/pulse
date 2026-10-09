package descriptor

import (
	"slices"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/internal/buildinfo"
)

func TestFoldAlias(t *testing.T) {
	for in, want := range map[string]string{
		"ANOVA":             "anova",
		"  Chi   Square ":   "chi square",
		"chi-square\ttest":  "chi-square test",
		"":                  "",
		"   ":               "",
		"Welch's  T\nTest ": "welch's t test",
	} {
		if got := FoldAlias(in); got != want {
			t.Errorf("FoldAlias(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestPurposeKnownAsUnique is the alias gate: every built-in KnownAs
// alias passes the per-Purpose rules (non-empty, ≤ KnownAsMax, listed
// once, no intent Sound) and no two built-in operators declare the same
// alias once folded. The backfill must also be non-trivial — the
// textbook names the guidance promises are declared.
func TestPurposeKnownAsUnique(t *testing.T) {
	for name, p := range builtinPurposes {
		for _, v := range knownAsViolations(name, p.KnownAs) {
			t.Error(v)
		}
	}
	for _, v := range KnownAsCollisions(builtinPurposes) {
		t.Error(v)
	}
	idx := BuiltinKnownAs()
	for alias, op := range map[string]string{
		"anova":              "TEST_ANOVA_F",
		"chi square":         "TEST_CHISQ",
		"t test":             "TEST_T",
		"pearson":            "TEST_PEARSON_R",
		"kruskal.test":       "TEST_KRUSKAL_WALLIS",
		"lm":                 "REG_OLS",
		"correlation matrix": "MAT_CORRELATION",
	} {
		if idx[alias] != op {
			t.Errorf("built-in alias %q → %q, want %q", alias, idx[alias], op)
		}
	}
}

// TestPurposeKnownAsUnique_Falsifiers proves each alias rule bites.
func TestPurposeKnownAsUnique_Falsifiers(t *testing.T) {
	sound := intentRegistry[0].Sounds[0]
	for _, tc := range []struct {
		name    string
		aliases []string
		want    string
	}{
		{"empty", []string{"  "}, "is empty"},
		{"too long", []string{strings.Repeat("x", KnownAsMax+1)}, "limit"},
		{"duplicate folded", []string{"ANOVA", "anova "}, "listed twice"},
		{"sound collision", []string{strings.ToUpper(sound)}, "collides with a Sound"},
	} {
		vs := knownAsViolations("AGG_X", tc.aliases)
		if len(vs) == 0 || vs[0].Rule != PurposeRuleKnownAs || !strings.Contains(vs[0].Detail, tc.want) {
			t.Errorf("%s: got %v, want a %s violation containing %q", tc.name, vs, PurposeRuleKnownAs, tc.want)
		}
	}
	if vs := knownAsViolations("AGG_X", []string{"fine alias", "another"}); len(vs) != 0 {
		t.Errorf("clean aliases flagged: %v", vs)
	}

	// ValidatePurpose carries the rule (the extension path shares it).
	p := purposeTestAnovaF
	p.KnownAs = []string{"anova", "Anova"}
	if !slices.ContainsFunc(ValidatePurpose("TEST_ANOVA_F", p, BuiltinPurposeResolver()), func(v PurposeViolation) bool {
		return v.Rule == PurposeRuleKnownAs
	}) {
		t.Error("ValidatePurpose ignores a duplicate alias")
	}

	// Cross-operator collision, named on the alphabetically later operator.
	reg := map[string]descriptor.Purpose{
		"AGG_A": {KnownAs: []string{"Shared Name", "a only"}},
		"AGG_B": {KnownAs: []string{"shared  name"}},
	}
	vs := KnownAsCollisions(reg)
	if len(vs) != 1 || vs[0].Name != "AGG_B" || !strings.Contains(vs[0].Detail, "AGG_A") {
		t.Errorf("collision: got %v", vs)
	}
}

// TestInstanceKnownAs: the instance alias index carries built-in and
// extension aliases, and an operator the feature profile hides drops
// its aliases.
func TestInstanceKnownAs(t *testing.T) {
	full := hidingSnapshot().KnownAs()
	if full["anova"] != "TEST_ANOVA_F" {
		t.Fatalf("full instance lacks anova: %v", full["anova"])
	}
	var nilSnap *InstanceSnapshot
	if got := nilSnap.KnownAs(); got["anova"] != "TEST_ANOVA_F" || len(got) != len(BuiltinKnownAs()) {
		t.Error("nil snapshot does not answer the full built-in index")
	}

	hidden := hidingSnapshot("TEST_ANOVA_F").KnownAs()
	for _, a := range builtinPurposes["TEST_ANOVA_F"].KnownAs {
		if op, ok := hidden[FoldAlias(a)]; ok {
			t.Errorf("hidden TEST_ANOVA_F alias %q still maps to %s", a, op)
		}
	}
	if hidden["chi square"] != "TEST_CHISQ" {
		t.Error("hiding TEST_ANOVA_F dropped an unrelated alias")
	}

	ext := &ExtensionsSnapshot{
		Aggregators: []descriptor.OperatorMeta{{Name: "AGG_ACME_GINI"}},
		Purposes: map[string]descriptor.Purpose{
			"AGG_ACME_GINI": {Intents: []string{IntentDescribe}, KnownAs: []string{"Gini Coefficient"}},
		},
	}
	if got := UnscopedInstanceSnapshot(ext).KnownAs()["gini coefficient"]; got != "AGG_ACME_GINI" {
		t.Errorf("extension alias → %q, want AGG_ACME_GINI", got)
	}
	scoped := NewInstanceSnapshot(ext, FeatureSet{Enabled: ReachedFeatureNames(buildinfo.Version()), Hidden: []string{"AGG_ACME_GINI"}})
	if got, ok := scoped.KnownAs()["gini coefficient"]; ok {
		t.Errorf("hidden extension alias still maps to %s", got)
	}
}
