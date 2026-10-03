package descriptor

import (
	"reflect"
	"testing"
)

func TestSortFeatureNames_KindThenCategoryThenTable(t *testing.T) {
	in := []string{
		"mcp_extra:cohort_resources",
		"OVERLAY_SHARE_OF_ROW",
		"io_format:csv",
		"GROUP_CATEGORY",
		"AGG_SUM",
		"capability:facet",
		"AGG_COUNT",
		"capability:process",
		"SYNTH_ACME_DRAW",
		"AGG_ACME_TOTAL",
		"TEST_T",
		"REG_OLS",
	}
	want := []string{
		"capability:process",
		"capability:facet",
		"AGG_COUNT",
		"AGG_SUM",
		"AGG_ACME_TOTAL", // extension: after the built-ins of its category
		"GROUP_CATEGORY",
		"TEST_T",
		"REG_OLS",
		"OVERLAY_SHARE_OF_ROW",
		"SYNTH_ACME_DRAW", // unlisted category: after every listed one
		"io_format:csv",
		"mcp_extra:cohort_resources",
	}
	orig := append([]string(nil), in...)
	got := SortFeatureNames(in)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("SortFeatureNames:\n got %v\nwant %v", got, want)
	}
	if !reflect.DeepEqual(in, orig) {
		t.Fatalf("SortFeatureNames modified its input: %v", in)
	}
}

func TestSortFeatureNames_Idempotent(t *testing.T) {
	once := SortFeatureNames(FeatureNames())
	if twice := SortFeatureNames(once); !reflect.DeepEqual(once, twice) {
		t.Fatal("SortFeatureNames is not idempotent over the feature table")
	}
	if len(once) != len(FeatureNames()) {
		t.Fatalf("SortFeatureNames dropped names: %d != %d", len(once), len(FeatureNames()))
	}
}

func TestFeatureCategoryOf(t *testing.T) {
	cases := map[string]string{
		"AGG_SUM":            "AGG",
		"OVERLAY_T_CELL":     "OVERLAY",
		"capability:process": "",
		"io_format:csv":      "",
		"AGG_ACME_TOTAL":     "AGG",
	}
	for name, want := range cases {
		if got := FeatureCategoryOf(name); got != want {
			t.Errorf("FeatureCategoryOf(%q) = %q, want %q", name, got, want)
		}
	}
}
