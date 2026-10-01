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
