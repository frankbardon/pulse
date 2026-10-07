package limits

import (
	"testing"

	"github.com/frankbardon/pulse/errors"
)

func TestEvaluate(t *testing.T) {
	l := Defaults()
	l.MaxMatrixDim = 2
	if _, ok := Evaluate(l, MaxMatrixDim, 2, Certain); ok {
		t.Fatal("an estimate equal to the limit is not a breach")
	}
	f, ok := Evaluate(l, MaxMatrixDim, 3, Certain)
	if !ok {
		t.Fatal("estimate 3 over limit 2 not reported")
	}
	want := Finding{Limit: MaxMatrixDim, Configured: 2, Estimated: 3, Grade: Certain}
	if f != want {
		t.Fatalf("finding = %+v, want %+v", f, want)
	}
	l.MaxMatrixDim = Unlimited
	if _, ok := Evaluate(l, MaxMatrixDim, 1<<40, Certain); ok {
		t.Fatal("an Unlimited limit never breaches")
	}
	if f, ok := Evaluate(Defaults(), MaxGroups, DefaultMaxGroups+1, Possible); !ok || f.Grade != Possible {
		t.Fatalf("possible finding = %+v, %v", f, ok)
	}
}

func TestChecks(t *testing.T) {
	l := Defaults()
	l.MaxMatrixDim, l.MaxComposeSlots, l.MaxChainStages, l.MaxJoinBuildRows = 2, 3, 4, 999
	for _, c := range []struct {
		name       Name
		over, at   *errors.CodedError
		configured int64
		observed   int64
	}{
		{MaxMatrixDim, CheckMatrixDim(l, 3), CheckMatrixDim(l, 2), 2, 3},
		{MaxComposeSlots, CheckComposeSlots(l, 4), CheckComposeSlots(l, 3), 3, 4},
		{MaxChainStages, CheckChainStages(l, 5), CheckChainStages(l, 4), 4, 5},
		{MaxJoinBuildRows, CheckJoinBuildRows(l, 1000), CheckJoinBuildRows(l, 999), 999, 1000},
	} {
		if c.at != nil {
			t.Errorf("%s: at the limit refused: %v", c.name, c.at)
		}
		if c.over == nil {
			t.Fatalf("%s: over the limit accepted", c.name)
		}
		if c.over.Code != errors.PULSE_LIMIT_EXCEEDED {
			t.Errorf("%s: code = %s", c.name, c.over.Code)
		}
		d := c.over.Details
		if d["limit"] != string(c.name) || d["configured"] != c.configured || d["observed"] != c.observed || d["option"] != Option(c.name) {
			t.Errorf("%s: details = %v", c.name, d)
		}
	}
}

func TestFirstCertain(t *testing.T) {
	possible := Finding{Limit: MaxGroups, Configured: 1, Estimated: 2, Grade: Possible}
	certain := Finding{Limit: MaxMatrixDim, Configured: 2, Estimated: 3, Grade: Certain}
	if err := FirstCertain([]Finding{possible}); err != nil {
		t.Fatalf("a possible finding never refuses: %v", err)
	}
	err := FirstCertain([]Finding{possible, certain})
	if err == nil || err.Details["limit"] != string(MaxMatrixDim) {
		t.Fatalf("FirstCertain = %v, want the matrix finding", err)
	}
}
