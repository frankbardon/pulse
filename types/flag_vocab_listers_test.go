package types

import (
	"slices"
	"testing"
)

func TestAllLookupMultiplicities_OrderFreshAndParsed(t *testing.T) {
	want := []LookupMultiplicity{LookupMultiplicityAssertUnique, LookupMultiplicityFirst, LookupMultiplicityAll}
	got := AllLookupMultiplicities()
	if !slices.Equal(got, want) {
		t.Fatalf("AllLookupMultiplicities() = %v, want %v", got, want)
	}
	got[0] = "mutated"
	if AllLookupMultiplicities()[0] != LookupMultiplicityAssertUnique {
		t.Fatal("AllLookupMultiplicities shares its backing array")
	}
	for _, m := range want {
		if p, ok := ParseLookupMultiplicity(string(m)); !ok || p != m {
			t.Errorf("ParseLookupMultiplicity(%q) = (%q, %v)", m, p, ok)
		}
	}
	if p, ok := ParseLookupMultiplicity("assert-unique"); !ok || p != LookupMultiplicityAssertUnique {
		t.Errorf("hyphen alias = (%q, %v)", p, ok)
	}
	for _, bad := range []string{"", "First", "ALL", "assert unique", "any"} {
		if p, ok := ParseLookupMultiplicity(bad); ok {
			t.Errorf("ParseLookupMultiplicity(%q) accepted as %q", bad, p)
		}
	}
}

func TestAllLabelModes_OrderFreshAndParsed(t *testing.T) {
	want := []LabelMode{LabelModeReplace, LabelModeAugment}
	got := AllLabelModes()
	if !slices.Equal(got, want) {
		t.Fatalf("AllLabelModes() = %v, want %v", got, want)
	}
	got[0] = "mutated"
	if AllLabelModes()[0] != LabelModeReplace {
		t.Fatal("AllLabelModes shares its backing array")
	}
	for _, m := range want {
		if p, ok := ParseLabelMode(string(m)); !ok || p != m {
			t.Errorf("ParseLabelMode(%q) = (%q, %v)", m, p, ok)
		}
	}
	if p, ok := ParseLabelMode(""); !ok || p != LabelModeReplace {
		t.Errorf("empty = (%q, %v), want the default replace", p, ok)
	}
	for _, bad := range []string{"Augment", "REPLACE", "highlight", " replace"} {
		if p, ok := ParseLabelMode(bad); ok {
			t.Errorf("ParseLabelMode(%q) accepted as %q", bad, p)
		}
	}
}
