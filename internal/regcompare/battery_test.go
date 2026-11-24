package regcompare

import (
	"math"
	"slices"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/statdist"
	"github.com/frankbardon/pulse/types"
)

// vecReq is a vector fit request: a MAT_COVARIANCE battery over fields.
func vecReq(fields ...string) *types.Request {
	return &types.Request{Cohort: &types.Cohort{Filename: "c.pulse"}, Matrices: []types.MatrixSpec{{Type: types.MAT_COVARIANCE, Fields: fields}}}
}

func vecPair(r0, r1 *types.Request) []types.FitSpec {
	return []types.FitSpec{{Name: "m0", Request: r0}, {Name: "m1", Request: r1}}
}

// TestCheckBattery_Refusals: every battery rule refuses with
// PULSE_COMPARISON_INVALID and its reason, located by details.compare.
func TestCheckBattery_Refusals(t *testing.T) {
	t.Parallel()

	prob := types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: types.WeightKindProbability})
	mut := func(r *types.Request, f func(*types.Request)) *types.Request { f(r); return r }
	pattern := mut(vecReq(), func(r *types.Request) {
		r.Matrices[0].Fields, r.Matrices[0].Vector = nil, "v"
		r.Vectors = []types.VectorSpec{{Name: "v", Pattern: "q*"}}
	})
	for _, tc := range []struct {
		name     string
		compare  []types.FitComparisonSpec
		fits     []types.FitSpec
		def      *types.WeightSpec
		reason   string
		requires string
	}{
		{"full a regression", one(""), []types.FitSpec{{Name: "m0", Request: vecReq("a", "b")}, {Name: "m1", Request: fitReq(ols("a"))}}, nil, "fit_kind", ""},
		{"full unknown", []types.FitComparisonSpec{{Name: "c", Restricted: "m0", Full: "mx"}}, vecPair(vecReq("a"), vecReq("a", "b")), nil, "fit_unknown", ""},
		{"full missing", []types.FitComparisonSpec{{Name: "c", Restricted: "m0"}}, vecPair(vecReq("a"), vecReq("a", "b")), nil, "fit_missing", ""},
		{"same fit", []types.FitComparisonSpec{{Name: "c", Restricted: "m0", Full: "m0"}}, vecPair(vecReq("a"), vecReq("a", "b")), nil, "same_fit", ""},
		{"test unknown", one("score"), vecPair(vecReq("a"), vecReq("a", "b")), nil, "test_unknown", ""},
		{"pattern members", one(""), vecPair(pattern, vecReq("a", "b")), nil, ReasonMembersUnlisted, ""},
		{"vec field members", one(""), vecPair(vecReq("a"), mut(vecReq(), func(r *types.Request) { r.Matrices[0].Fields, r.Matrices[0].Vector = nil, "emb" })), nil, ReasonMembersUnlisted, ""},
		{"not nested", one(""), vecPair(vecReq("a", "c"), vecReq("a", "b", "d")), nil, "not_nested", ""},
		{"swapped", one(""), vecPair(vecReq("a", "b", "c"), vecReq("a", "b")), nil, "not_nested", ""},
		{"label on another column", one(""), vecPair(
			mut(vecReq(), func(r *types.Request) {
				r.Matrices[0].Fields, r.Matrices[0].Vector = nil, "v"
				r.Vectors = []types.VectorSpec{{Name: "v", Fields: []string{"a", "z"}, Labels: []string{"a", "b"}}}
			}),
			mut(vecReq(), func(r *types.Request) {
				r.Matrices[0].Fields, r.Matrices[0].Vector = nil, "v"
				r.Vectors = []types.VectorSpec{{Name: "v", Fields: []string{"a", "b", "c"}}}
			})), nil, "not_nested", ""},
		{"no extra member", one(""), vecPair(vecReq("b", "a"), vecReq("a", "b")), nil, "no_extra_terms", ""},
		{"filters differ", one(""), vecPair(vecReq("a"), mut(vecReq("a", "b"), func(r *types.Request) {
			r.Filterers = []*types.Filterer{{Type: types.FILTER_RANGE, Field: "a", Values: []string{"0", "1"}}}
		})), nil, "request_mismatch", ""},
		{"operator differs", one(""), vecPair(vecReq("a"), mut(vecReq("a", "b"), func(r *types.Request) { r.Matrices[0].Type = types.MAT_CORRELATION })), nil, "request_mismatch", ""},
		{"probability slot weight", one(""), vecPair(
			mut(vecReq("a"), func(r *types.Request) { r.Matrices[0].Weight = prob }),
			mut(vecReq("a", "b"), func(r *types.Request) { r.Matrices[0].Weight = prob })), nil, "requires", RequiresNoProbabilityWeight},
		{"default probability weight", one(types.ComparisonTestWald), vecPair(vecReq("a"), vecReq("a", "b")),
			&types.WeightSpec{Field: "w", Kind: types.WeightKindProbability}, "requires", RequiresNoProbabilityWeight},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := Check(tc.compare, tc.fits, tc.def, nil)
			if err == nil {
				t.Fatal("accepted")
			}
			if err.Code != errors.PULSE_COMPARISON_INVALID || err.Details["reason"] != tc.reason || err.Details["compare"] != 0 {
				t.Fatalf("got %s %v, want reason %s", err.Code, err.Details, tc.reason)
			}
			if tc.requires != "" && err.Details["requires"] != tc.requires {
				t.Fatalf("requires %v, want %s", err.Details["requires"], tc.requires)
			}
		})
	}
}

// TestCheckBattery_Plans: the default test is f, lrt reads chi-square,
// a Wald test F; Members are the restricted battery's labels in its
// order, Terms the tested ones in the full battery's; the battery's
// reporting knobs, a request label and an opted-out or frequency weight
// never refuse; a `vectors` entry's labels name the members and an
// attribute battery is a battery like a matrix one.
func TestCheckBattery_Plans(t *testing.T) {
	t.Parallel()

	freq := types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: types.WeightKindFrequency})
	named := func(fields, labels []string) *types.Request {
		r := vecReq()
		r.Matrices[0].Fields, r.Matrices[0].Vector = nil, "v"
		r.Vectors = []types.VectorSpec{{Name: "v", Fields: fields, Labels: labels}}
		return r
	}
	attr := func(fields ...string) *types.Request {
		return &types.Request{Cohort: &types.Cohort{Filename: "c.pulse"}, Attributes: []*types.Attribute{{Type: types.ATTR_MAHALANOBIS, Fields: fields, Label: "d" + fields[0]}}}
	}
	knobs := vecReq("c", "a", "b", "d")
	knobs.Label, knobs.Matrices[0].Name, knobs.Matrices[0].Encoding = "full", "cov", types.MatrixEncoding("full")
	for _, tc := range []struct {
		name    string
		test    types.ComparisonTest
		r0, r1  *types.Request
		def     *types.WeightSpec
		want    types.ComparisonTest
		dist    types.ComparisonDistribution
		members []string
		terms   []string
	}{
		{"default", "", vecReq("b", "a"), knobs, nil, types.ComparisonTestF, types.ComparisonDistributionF, []string{"b", "a"}, []string{"c", "d"}},
		{"lrt", types.ComparisonTestLRT, vecReq("a"), vecReq("a", "b"), nil, types.ComparisonTestLRT, types.ComparisonDistributionChiSquare, []string{"a"}, []string{"b"}},
		{"wald", types.ComparisonTestWald, vecReq("a"), vecReq("a", "b"), nil, types.ComparisonTestWald, types.ComparisonDistributionF, []string{"a"}, []string{"b"}},
		{"labels", "", named([]string{"x1", "x2"}, []string{"one", "two"}), named([]string{"x1", "x2", "x3"}, []string{"one", "two", "three"}), nil,
			types.ComparisonTestF, types.ComparisonDistributionF, []string{"one", "two"}, []string{"three"}},
		{"attribute", "", attr("a", "b"), attr("a", "b", "c"), nil, types.ComparisonTestF, types.ComparisonDistributionF, []string{"a", "b"}, []string{"c"}},
		{"frequency default weight", "", vecReq("a"), vecReq("a", "b"), &types.WeightSpec{Field: "w", Kind: types.WeightKindFrequency},
			types.ComparisonTestF, types.ComparisonDistributionF, []string{"a"}, []string{"b"}},
		{"frequency slot weight", "", func() *types.Request { r := vecReq("a"); r.Matrices[0].Weight = freq; return r }(),
			func() *types.Request { r := vecReq("a", "b"); r.Matrices[0].Weight = freq; return r }(), nil,
			types.ComparisonTestF, types.ComparisonDistributionF, []string{"a"}, []string{"b"}},
		{"opted out of the default probability weight", "", func() *types.Request { r := vecReq("a"); r.Matrices[0].Weight = types.NullSlotWeight(); return r }(),
			func() *types.Request { r := vecReq("a", "b"); r.Matrices[0].Weight = types.NullSlotWeight(); return r }(),
			&types.WeightSpec{Field: "w", Kind: types.WeightKindProbability}, types.ComparisonTestF, types.ComparisonDistributionF, []string{"a"}, []string{"b"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			plans, err := Check(one(tc.test), vecPair(tc.r0, tc.r1), tc.def, nil)
			if err != nil {
				t.Fatal(err)
			}
			p := plans[0]
			if !p.Battery || p.Test != tc.want || p.Distribution != tc.dist || !slices.Equal(p.Members, tc.members) || !slices.Equal(p.Terms, tc.terms) {
				t.Fatalf("plan %+v", p)
			}
		})
	}
}

// coMoment is the battery state of rows (axis order), unweighted.
func coMoment(labels []string, rows [][]float64) *model.VectorFit {
	p := len(labels)
	mean := make([]float64, p)
	for _, r := range rows {
		for k := range r {
			mean[k] += r[k]
		}
	}
	for k := range mean {
		mean[k] /= float64(len(rows))
	}
	m2 := make([][]float64, p)
	for a := range m2 {
		m2[a] = make([]float64, p)
		for b := range m2[a] {
			for _, r := range rows {
				m2[a][b] += float64((r[a] - mean[a]) * (r[b] - mean[b]))
			}
		}
	}
	n := int64(len(rows))
	return &model.VectorFit{Labels: labels, Members: labels, Basis: model.VectorBasisUnweighted, N: n, NMass: n, SumWeights: float64(n), SumWeightsSq: float64(n), Mean: mean, M2: m2}
}

// TestComputeBattery_TwoMembers: restricted {a}, full {a, b} — the row
// for a is lm(a ~ 1) against lm(a ~ b), RSS₁ = M2_aa − M2_ab²/M2_bb, F
// on (1, n − 2); the single-pair figures are NaN (null on the wire).
func TestComputeBattery_TwoMembers(t *testing.T) {
	t.Parallel()

	rows := [][]float64{{1, 2}, {2, 2.5}, {4, 3.9}, {3, 3.1}, {6, 5.2}, {5, 4.1}, {7, 6.6}}
	v := coMoment([]string{"a", "b"}, rows)
	v0 := coMoment([]string{"a"}, [][]float64{{1}, {2}, {4}, {3}, {6}, {5}, {7}})
	for _, test := range []types.ComparisonTest{types.ComparisonTestF, types.ComparisonTestLRT, types.ComparisonTestWald} {
		plans, cerr := Check(one(test), vecPair(vecReq("a"), vecReq("a", "b")), nil, nil)
		if cerr != nil {
			t.Fatal(cerr)
		}
		res, err := ComputeBattery(plans[0], &types.VectorFitResult{}, &types.VectorFitResult{}, v0, v)
		if err != nil || res.Error != nil {
			t.Fatalf("%s: %v %+v", test, err, res.Error)
		}
		rss0 := v.M2[0][0]
		rss1 := rss0 - v.M2[0][1]*v.M2[0][1]/v.M2[1][1]
		df1 := float64(len(rows) - 2)
		f := (rss0 - rss1) / (rss1 / df1)
		row := res.Members[0]
		if len(res.Members) != 1 || row.Member != "a" || math.Abs(row.RestrictedDeviance-rss0) > 1e-12*rss0 || math.Abs(row.FullDeviance-rss1) > 1e-12*rss1 ||
			math.Abs(row.Statistic-f) > 1e-10*f {
			t.Fatalf("%s: row %+v, want rss %v / %v, F %v", test, row, rss0, rss1, f)
		}
		wantP := statdist.FSurvival(f, 1, df1)
		if test == types.ComparisonTestLRT {
			wantP = statdist.ChiSquareSurvival(f, 1)
		}
		if math.Abs(row.PValue-wantP) > 1e-10 || res.DF != 1 || res.FullDFResidual != df1 || res.RestrictedDFResidual != df1+1 || res.NObs != len(rows) {
			t.Fatalf("%s: p %v want %v; %+v", test, row.PValue, wantP, res)
		}
		if (test == types.ComparisonTestWald) != (row.Dispersion == 0) {
			t.Fatalf("%s: dispersion %v", test, row.Dispersion)
		}
		if !math.IsNaN(res.Statistic) || !math.IsNaN(res.PValue) || !math.IsNaN(res.FullDeviance) {
			t.Fatalf("%s: single-pair figures %+v", test, res)
		}
	}
}

// TestComputeBattery_FailsAlone: unequal rows are
// PULSE_COMPARISON_ROWS_DIFFER, a failed fit its own code with
// details.fit, a fitted fit without its state PROCESSING_INTERNAL.
func TestComputeBattery_FailsAlone(t *testing.T) {
	t.Parallel()

	plans, cerr := Check(one(""), vecPair(vecReq("a"), vecReq("a", "b")), nil, nil)
	if cerr != nil {
		t.Fatal(cerr)
	}
	p := plans[0]
	v1 := coMoment([]string{"a", "b"}, [][]float64{{1, 2}, {2, 3}, {4, 1}, {3, 5}})
	v0 := coMoment([]string{"a"}, [][]float64{{1}, {2}, {4}, {3}, {9}})
	ok := &types.VectorFitResult{}
	res, err := ComputeBattery(p, ok, ok, v0, v1)
	if err != nil || res.Error == nil || res.Error.Code != string(errors.PULSE_COMPARISON_ROWS_DIFFER) || res.Members != nil {
		t.Fatalf("rows: %v %+v", err, res)
	}
	failed := &types.VectorFitResult{Error: &types.SlotError{Code: "PROCESSING_MATRIX_SINGULAR", Message: "singular"}}
	res, err = ComputeBattery(p, ok, failed, v0, nil)
	if err != nil || res.Error == nil || res.Error.Code != "PROCESSING_MATRIX_SINGULAR" || res.Error.Details["fit"] != "m1" {
		t.Fatalf("failed fit: %v %+v", err, res.Error)
	}
	if _, err := ComputeBattery(p, ok, ok, v0, nil); err == nil {
		t.Fatal("a fitted fit without its state passed")
	} else if ce, _ := err.(*errors.CodedError); ce == nil || ce.Code != errors.PROCESSING_INTERNAL {
		t.Fatalf("got %v", err)
	}
}
