package descriptor

import (
	stderrors "errors"
	"testing"

	"github.com/frankbardon/pulse/internal/limits"
	"github.com/frankbardon/pulse/types"
)

// TestJoinBuildLimitFinding: the predict-time MaxJoinBuildRows rule
// reads the right side's count through the counter alone, grades a
// breach certain, and yields nothing without a single join, a counter,
// a readable count or a bound.
func TestJoinBuildLimitFinding(t *testing.T) {
	l := limits.Defaults()
	l.MaxJoinBuildRows = 999
	join := &types.JoinSpec{Right: "right.pulse"}
	req := &types.Request{Joins: []*types.JoinSpec{join}}
	var asked []string
	count := func(n int64) func(string) (int64, error) {
		return func(p string) (int64, error) { asked = append(asked, p); return n, nil }
	}

	f, ok := joinBuildLimitFinding(req, l, count(1000))
	if !ok || f != (limits.Finding{Limit: limits.MaxJoinBuildRows, Configured: 999, Estimated: 1000, Grade: limits.Certain}) {
		t.Fatalf("finding = %+v, %v", f, ok)
	}
	if len(asked) != 1 || asked[0] != "right.pulse" {
		t.Fatalf("counter asked for %v, want the right cohort", asked)
	}
	if _, ok := joinBuildLimitFinding(req, l, count(999)); ok {
		t.Fatal("at the limit: a finding")
	}
	if _, ok := joinBuildLimitFinding(req, l, nil); ok {
		t.Fatal("nil counter: a finding")
	}
	if _, ok := joinBuildLimitFinding(req, l, func(string) (int64, error) { return 0, stderrors.New("x") }); ok {
		t.Fatal("count error: a finding")
	}
	two := &types.Request{Joins: []*types.JoinSpec{join, join}}
	if _, ok := joinBuildLimitFinding(two, l, count(1000)); ok {
		t.Fatal("two joins (refused by the join-count rule): a finding")
	}
	asked = nil
	free := limits.Defaults()
	free.MaxJoinBuildRows = limits.Unlimited
	if _, ok := joinBuildLimitFinding(req, free, count(1000)); ok || len(asked) != 0 {
		t.Fatalf("unlimited: finding=%v, counted %v", ok, asked)
	}
}
