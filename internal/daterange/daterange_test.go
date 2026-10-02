package daterange

import (
	stderrors "errors"
	"testing"

	"github.com/frankbardon/pulse/errors"
)

func str(s string) *string { return &s }

func TestCompile_MatchAndValidation(t *testing.T) {
	set, err := Compile([]Spec{
		{Label: "Q2", Start: str("2024-04-01"), End: str("2024-06-30")},
		{Label: "Q1", Start: str("2024-01-01"), End: str("2024-03-31")},
	})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if got := set.Labels(); len(got) != 2 || got[0] != "Q1" || got[1] != "Q2" {
		t.Fatalf("Labels = %v, want [Q1 Q2] (lower-bound order)", got)
	}
	// 2024-04-01 is epoch day 19814.
	if label, ok := set.Match(19814); !ok || label != "Q2" {
		t.Fatalf("Match(2024-04-01) = %q, %v; want Q2", label, ok)
	}

	for name, specs := range map[string][]Spec{
		string(errors.PULSE_RANGE_EMPTY):           nil,
		string(errors.PULSE_RANGE_DUPLICATE_LABEL): {{Label: "a"}, {Label: "a"}},
		string(errors.PULSE_RANGE_OVERLAP):         {{Label: "a"}, {Label: "b"}},
		string(errors.PULSE_RANGE_INVALID):         {{Label: "a", Start: str("not-a-date")}},
	} {
		_, err := Compile(specs)
		var ce *errors.CodedError
		if !stderrors.As(err, &ce) || string(ce.Code) != name {
			t.Errorf("Compile(%s case) err = %v, want code %s", name, err, name)
		}
	}
}

// TestCompile_PreEpochBounds pins signed bounds: a pre-1970 start with a
// post-1970 end is a valid range (not start > end), pre-1970 ranges sort
// before post-1970 ones, and Match resolves negative days.
func TestCompile_PreEpochBounds(t *testing.T) {
	set, err := Compile([]Spec{
		{Label: "post", Start: str("1970-01-02")},
		{Label: "span", Start: str("1969-12-01"), End: str("1970-01-01")},
		{Label: "old", Start: str("1900-01-01"), End: str("1969-11-30")},
	})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if got := set.Labels(); len(got) != 3 || got[0] != "old" || got[1] != "span" || got[2] != "post" {
		t.Fatalf("Labels = %v, want [old span post]", got)
	}
	for day, want := range map[int64]string{-25567: "old", -1: "span", 0: "span", 1: "post"} {
		if label, ok := set.Match(day); !ok || label != want {
			t.Errorf("Match(%d) = %q, %v; want %q", day, label, ok, want)
		}
	}
	if _, ok := set.Match(-25568); ok {
		t.Error("Match(1899-12-31) matched; want no range")
	}
}
