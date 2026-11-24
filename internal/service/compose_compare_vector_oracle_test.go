package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// compose_compare_vector_oracle_test.go pins a nested-model comparison
// of two vector fits (`compare` over battery fits, U69 #643) to R 4.6.1:
// compare_vector.json (scripts/reference/gen_compare_vector.R) — per
// restricted member j, anova(lm(j ~ restricted others), lm(j ~ full
// others)) (f), its test = "Chisq" form (lrt) and lmtest::waldtest (wald),
// one case under a frequency weight (R expands each row w times). The
// batteries ride a MAT_COVARIANCE slot, a fitted ATTR_MAHALANOBIS and a
// request `vectors` entry, so every battery shape reaches the figures.

type compareVectorMember struct {
	Member             string  `json:"member"`
	Dispersion         float64 `json:"dispersion"`
	Statistic          float64 `json:"statistic"`
	PValue             float64 `json:"p_value"`
	RestrictedDeviance float64 `json:"restricted_deviance"`
	FullDeviance       float64 `json:"full_deviance"`
}

type compareVectorCase struct {
	Name                 string                `json:"name"`
	Test                 string                `json:"test"`
	Weighted             bool                  `json:"weighted"`
	N                    int                   `json:"n"`
	Restricted           []string              `json:"restricted"`
	Full                 []string              `json:"full"`
	Terms                []string              `json:"terms"`
	DF                   float64               `json:"df"`
	Distribution         string                `json:"distribution"`
	RestrictedDFResidual float64               `json:"restricted_df_residual"`
	FullDFResidual       float64               `json:"full_df_residual"`
	Members              []compareVectorMember `json:"members"`
	Rows                 [][]float64           `json:"rows"`
}

func loadCompareVector(t *testing.T) []compareVectorCase {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "processing", "testdata", "reference", "compare_vector.json"))
	if err != nil {
		t.Fatalf("read compare_vector.json: %v (regenerate with scripts/reference/gen_compare_vector.R)", err)
	}
	s := string(raw)
	i := strings.LastIndex(s, "\n// golden-hash: ")
	if i < 0 {
		t.Fatal("compare_vector.json has no golden-hash footer")
	}
	body, stored := s[:i], strings.TrimSpace(s[i+len("\n// golden-hash: "):])
	if sum := sha256.Sum256([]byte(body)); hex.EncodeToString(sum[:]) != stored {
		t.Fatal("compare_vector.json: hash mismatch (hand-edited?)")
	}
	var doc struct {
		Packages map[string]string   `json:"packages"`
		Cases    []compareVectorCase `json:"cases"`
	}
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode compare_vector.json: %v", err)
	}
	if !strings.HasPrefix(doc.Packages["R"], "R version 4.6.1") || doc.Packages["lmtest"] != "0.9.40" || len(doc.Cases) != 4 {
		t.Fatalf("compare_vector.json: R %q, lmtest %q, %d cases", doc.Packages["R"], doc.Packages["lmtest"], len(doc.Cases))
	}
	return doc.Cases
}

const compareVectorCohort = "compare_vector.pulse"

func (c compareVectorCase) service(t *testing.T) *Service {
	t.Helper()
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "a", Type: encoding.FieldTypeF64, ByteOffset: 0},
		{Name: "b", Type: encoding.FieldTypeF64, ByteOffset: 8},
		{Name: "c", Type: encoding.FieldTypeF64, ByteOffset: 16},
		{Name: "d", Type: encoding.FieldTypeF64, ByteOffset: 24},
		{Name: "w", Type: encoding.FieldTypeF64, ByteOffset: 32},
	}}
	rows := make([][]uint64, len(c.Rows))
	for i, r := range c.Rows {
		rows[i] = make([]uint64, len(r))
		for j, v := range r {
			rows[i][j] = math.Float64bits(v)
		}
	}
	return New(setupTestFS(t, compareVectorCohort, schema, rows))
}

// fit is the case's vector fit request over members: the battery shape
// rotates with the case index k (MAT_COVARIANCE, ATTR_MAHALANOBIS, a
// `vectors` entry), the frequency weight on the battery slot.
func (c compareVectorCase) fit(k int, members []string) *types.Request {
	r := &types.Request{Cohort: &types.Cohort{Filename: compareVectorCohort}}
	var w types.SlotWeight
	if c.Weighted {
		w = types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: types.WeightKindFrequency})
	}
	switch k % 3 {
	case 0:
		r.Matrices = []types.MatrixSpec{{Type: types.MAT_COVARIANCE, Fields: members, Weight: w}}
	case 1:
		r.Attributes = []*types.Attribute{{Type: types.ATTR_MAHALANOBIS, Fields: members, Label: "dist", Weight: w}}
	default:
		r.Vectors = []types.VectorSpec{{Name: "battery", Fields: members, Labels: members}}
		r.Matrices = []types.MatrixSpec{{Type: types.MAT_CORRELATION, Vector: "battery", Weight: w}}
	}
	return r
}

// TestReferenceOracle_CompareVectorMatchesR: every member row —
// statistic, p-value, both residual sums of squares, the dispersion —
// and the comparison's df, residual dfs, n_obs, distribution and tested
// members agree with R at 1e-10 relative.
func TestReferenceOracle_CompareVectorMatchesR(t *testing.T) {
	t.Parallel()
	for k, c := range loadCompareVector(t) {
		batch := &types.ComposedRequest{
			Fits: []types.FitSpec{{Name: "m0", Request: c.fit(k, c.Restricted)}, {Name: "m1", Request: c.fit(k, c.Full)}},
			Requests: []*types.Request{{Label: "rows", Cohort: &types.Cohort{Filename: compareVectorCohort},
				Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "a", Label: "n"}}}},
			Compare: []types.FitComparisonSpec{{Name: c.Name, Restricted: "m0", Full: "m1", Test: types.ComparisonTest(c.Test)}},
		}
		resp, err := c.service(t).Compose(context.Background(), batch)
		if err != nil {
			t.Fatalf("%s: Compose: %v", c.Name, err)
		}
		if len(resp.Comparisons) != 1 || resp.Comparisons[0].Error != nil {
			t.Fatalf("%s: comparisons %+v", c.Name, resp.Comparisons)
		}
		got := resp.Comparisons[0]
		if string(got.Test) != c.Test || string(got.Distribution) != c.Distribution || !slices.Equal(got.Terms, c.Terms) || got.NObs != c.N || len(got.Members) != len(c.Members) {
			t.Fatalf("%s: test %s distribution %s terms %v n %d rows %d, want %s %s %v %d %d", c.Name, got.Test, got.Distribution, got.Terms, got.NObs, len(got.Members),
				c.Test, c.Distribution, c.Terms, c.N, len(c.Members))
		}
		check := func(what string, g, w float64) {
			if e := nestedRel(g, w); !(e <= 1e-10) {
				t.Errorf("%s: %s = %.17g, want %.17g (rel %.2g)", c.Name, what, g, w, e)
			}
		}
		check("df", got.DF, c.DF)
		check("restricted_df_residual", got.RestrictedDFResidual, c.RestrictedDFResidual)
		check("full_df_residual", got.FullDFResidual, c.FullDFResidual)
		if c.Distribution == "f" {
			check("df_residual", got.DFResidual, c.FullDFResidual)
		} else if got.DFResidual != 0 {
			t.Errorf("%s: df_residual %g on a chi-square test", c.Name, got.DFResidual)
		}
		for i, want := range c.Members {
			row := got.Members[i]
			if row.Member != want.Member {
				t.Fatalf("%s: row %d is %q, want %q", c.Name, i, row.Member, want.Member)
			}
			check(want.Member+" statistic", row.Statistic, want.Statistic)
			check(want.Member+" p_value", row.PValue, want.PValue)
			check(want.Member+" restricted_deviance", row.RestrictedDeviance, want.RestrictedDeviance)
			check(want.Member+" full_deviance", row.FullDeviance, want.FullDeviance)
			check(want.Member+" dispersion", row.Dispersion, want.Dispersion)
		}
	}
}
