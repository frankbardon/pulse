package regresample

import (
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

func TestCheck(t *testing.T) {
	base := func(mut func(*types.RegressionSpec)) *types.RegressionSpec {
		s := &types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: []string{"x"}, Resample: "jackknife"}
		if mut != nil {
			mut(s)
		}
		return s
	}
	cases := []struct {
		name    string
		spec    *types.RegressionSpec
		refused bool
		detail  string
	}{
		{"nil", nil, false, ""},
		{"leave-one-out", base(nil), false, ""},
		{"groups", base(func(s *types.RegressionSpec) { s.JackknifeGroups = 2 }), false, ""},
		{"by", base(func(s *types.RegressionSpec) { s.JackknifeBy = "region" }), false, ""},
		{"both", base(func(s *types.RegressionSpec) { s.JackknifeGroups, s.JackknifeBy = 10, "region" }), true, "jackknife_by"},
		{"groups one", base(func(s *types.RegressionSpec) { s.JackknifeGroups = 1 }), true, "minimum"},
		{"groups negative", base(func(s *types.RegressionSpec) { s.JackknifeGroups = -3 }), true, "minimum"},
		{"groups on bootstrap", base(func(s *types.RegressionSpec) { s.Resample, s.JackknifeGroups = "bootstrap", 10 }), true, "jackknife_groups"},
		{"by without resample", base(func(s *types.RegressionSpec) { s.Resample, s.JackknifeBy = "", "region" }), true, "jackknife_by"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cerr := Check(c.spec)
			if (cerr != nil) != c.refused {
				t.Fatalf("Check refused = %v, want %v (%v)", cerr != nil, c.refused, cerr)
			}
			if cerr == nil {
				return
			}
			if cerr.Code != errors.PROCESSING_CONFIG {
				t.Fatalf("code = %s, want PROCESSING_CONFIG", cerr.Code)
			}
			if _, ok := cerr.Details[c.detail]; !ok {
				t.Fatalf("details %v lack %q", cerr.Details, c.detail)
			}
		})
	}
}

func TestLeaveOneOut(t *testing.T) {
	loo := &types.RegressionSpec{Resample: "jackknife"}
	if !LeaveOneOut(loo) || Grouped(loo) {
		t.Fatal("plain jackknife is leave-one-out")
	}
	g := &types.RegressionSpec{Resample: "jackknife", JackknifeGroups: 5}
	if LeaveOneOut(g) || !Grouped(g) {
		t.Fatal("jackknife_groups is grouped")
	}
	if LeaveOneOut(&types.RegressionSpec{Resample: "bootstrap"}) || LeaveOneOut(nil) {
		t.Fatal("bootstrap / nil are not leave-one-out")
	}
}
