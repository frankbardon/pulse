package pulse

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/afero"
)

// TestRootNativeTypes: DateRangeSpec, MemberSet and LoadMemberSetResult
// are declared in the root package itself — not aliases into the
// engine — so the public surface never names an engine type for them.
func TestRootNativeTypes(t *testing.T) {
	const root = "github.com/frankbardon/pulse"
	for _, typ := range []reflect.Type{
		reflect.TypeOf(DateRangeSpec{}),
		reflect.TypeOf((*MemberSet)(nil)).Elem(),
		reflect.TypeOf(LoadMemberSetResult{}),
	} {
		if typ.PkgPath() != root {
			t.Errorf("%s is declared in %q, want the root package %q", typ.Name(), typ.PkgPath(), root)
		}
	}

	// DateRangeSpec keeps its wire spelling.
	start := "2024-01-01"
	got, err := json.Marshal(DateRangeSpec{Label: "Q1", Start: &start})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"label":"Q1","start":"2024-01-01"}`; string(got) != want {
		t.Fatalf("DateRangeSpec JSON = %s, want %s", got, want)
	}
}

// TestLoadMemberSet_FilterToFileBySetAndExpr drives the root-native
// member-set types end to end: the loader's report is carried across
// the engine boundary intact, and the set it returns is accepted by
// FilterToFileBySetAndExpr with the same survivors as the equivalent
// expression filter.
func TestLoadMemberSet_FilterToFileBySetAndExpr(t *testing.T) {
	ctx := context.Background()
	fsys, _ := groupedTwinFS(t)
	p, err := New(Options{FS: fsys})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	schema, err := p.ResolveCanonicalSchema(ctx, "cohort.pulse")
	if err != nil {
		t.Fatalf("ResolveCanonicalSchema: %v", err)
	}
	load, err := LoadMemberSetFromReader(strings.NewReader("north\n\nnowhere\n"), schema, "region")
	if err != nil {
		t.Fatalf("LoadMemberSetFromReader: %v", err)
	}
	if load.Lines != 2 || load.NotInDictionary != 1 || load.Invalid != 0 {
		t.Fatalf("load report = %+v, want Lines 2, NotInDictionary 1, Invalid 0", load)
	}
	if load.Set == nil || load.Set.Kind() != "bitset" || load.Set.Len() != 1 {
		t.Fatalf("load.Set = %#v, want a 1-member bitset", load.Set)
	}

	bySet, err := p.FilterToFileBySetAndExpr(ctx, "cohort.pulse", "by_set.pulse", "region", load.Set, "")
	if err != nil {
		t.Fatalf("FilterToFileBySetAndExpr: %v", err)
	}
	byExpr, err := p.FilterToFile(ctx, "cohort.pulse", "by_expr.pulse", `region == "north"`)
	if err != nil {
		t.Fatalf("FilterToFile: %v", err)
	}
	if bySet != 96 || bySet != byExpr {
		t.Fatalf("set filter wrote %d, expression filter %d; want 96 each", bySet, byExpr)
	}
}

// TestRangeTables_RoundTripNative: a range table registered with root
// DateRangeSpecs is converted into the engine and reported back by
// RangeTables in root form, bounds intact.
func TestRangeTables_RoundTripNative(t *testing.T) {
	s1, e1, s2 := "2024-01-01", "2024-03-31", "2024-04-01"
	in := []DateRangeSpec{{Label: "Q1", Start: &s1, End: &e1}, {Label: "rest", Start: &s2}}
	p, err := New(Options{FS: afero.NewMemMapFs(), Extensions: Extensions{RangeTables: map[string]RangeTable{"fy": {Ranges: in}}}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got := p.RangeTables()
	if len(got) != 1 || got[0].Name != "fy" || got[0].RangeCount != 2 {
		t.Fatalf("RangeTables() = %+v", got)
	}
	if !reflect.DeepEqual(got[0].Ranges, in) {
		t.Fatalf("ranges = %+v, want %+v", got[0].Ranges, in)
	}
}
