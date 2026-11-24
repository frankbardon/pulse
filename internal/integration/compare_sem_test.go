package integration_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"os"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// compare_sem_test.go pins the nested comparison of two SEM fits
// (`compare` over two MAT_SEM `fits`, U69 #643) to lavaan 0.7.3:
// sem_compare.json (scripts/reference/gen_sem_compare.R) — the plain
// chi-square difference of lavTestLRT under ML (a dropped path, the
// metric-vs-configural and scalar-vs-metric invariance steps across the
// waves) and DWLS, and the Satorra–Bentler (2001) scaled difference
// under WLSMV (a fixed factor covariance; threshold + loading
// invariance against the configural fit).

type semCompareSide struct {
	Model      string   `json:"model"`
	GroupEqual []string `json:"group_equal"`
}

type semCompareCase struct {
	Name            string         `json:"name"`
	Estimator       string         `json:"estimator"`
	Group           string         `json:"group"`
	MeanStructure   bool           `json:"meanstructure"`
	Ordered         []string       `json:"ordered"`
	Restricted      semCompareSide `json:"restricted"`
	Full            semCompareSide `json:"full"`
	N               float64        `json:"n"`
	RestrictedChisq float64        `json:"restricted_chisq"`
	FullChisq       float64        `json:"full_chisq"`
	RestrictedDF    float64        `json:"restricted_df"`
	FullDF          float64        `json:"full_df"`
	DF              float64        `json:"df"`
	ScalingFactor   *float64       `json:"scaling_factor"`
	Statistic       float64        `json:"statistic"`
	PValue          float64        `json:"p_value"`
}

func loadSEMCompare(t *testing.T) []semCompareCase {
	t.Helper()
	raw, err := os.ReadFile("internal/processing/testdata/reference/sem_compare.json")
	if err != nil {
		t.Fatalf("read sem_compare.json: %v (regenerate with scripts/reference/gen_sem_compare.R)", err)
	}
	s := string(raw)
	i := strings.LastIndex(s, "\n// golden-hash: ")
	if i < 0 {
		t.Fatal("sem_compare.json has no golden-hash footer")
	}
	body, stored := s[:i], strings.TrimSpace(s[i+len("\n// golden-hash: "):])
	if sum := sha256.Sum256([]byte(body)); hex.EncodeToString(sum[:]) != stored {
		t.Fatal("sem_compare.json: hash mismatch (hand-edited?)")
	}
	var doc struct {
		Packages map[string]string `json:"packages"`
		Cases    []semCompareCase  `json:"cases"`
	}
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode sem_compare.json: %v", err)
	}
	if !strings.HasPrefix(doc.Packages["R"], "R version 4.6.1") || doc.Packages["lavaan"] != "0.7.3" || len(doc.Cases) != 6 {
		t.Fatalf("sem_compare.json: R %q, lavaan %q, %d cases", doc.Packages["R"], doc.Packages["lavaan"], len(doc.Cases))
	}
	return doc.Cases
}

// semCompareFit is one side's SEM fit request.
func (c semCompareCase) fit(side semCompareSide) *types.Request {
	params := map[string]any{"model": side.Model}
	fields := brandMembers
	if c.Estimator != "ml" {
		params["estimator"], params["ordered"] = c.Estimator, c.Ordered
		fields = brandOrdered
	}
	if c.MeanStructure {
		params["meanstructure"] = true
	}
	if c.Group != "" {
		params["group"] = c.Group
	}
	if len(side.GroupEqual) > 0 {
		params["group_equal"] = side.GroupEqual
	}
	raw, _ := json.Marshal(params)
	return &types.Request{
		Cohort:   &types.Cohort{Filename: "brand.pulse"},
		Matrices: []types.MatrixSpec{{Name: "sem", Type: types.MAT_SEM, Fields: fields, Params: raw}},
	}
}

// batch is the case as a Compose batch: the two fits and one
// comparison (default test), beside a trivial slot.
func (c semCompareCase) batch() *pulse.ComposedRequest {
	return &pulse.ComposedRequest{
		Fits: []types.FitSpec{{Name: "m0", Request: c.fit(c.Restricted)}, {Name: "m1", Request: c.fit(c.Full)}},
		Requests: []*types.Request{{Label: "rows", Cohort: &types.Cohort{Filename: "brand.pulse"},
			Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "q1", Label: "n"}}}},
		Compare: []types.FitComparisonSpec{{Name: "c", Restricted: "m0", Full: "m1"}},
	}
}

func semRel(got, want float64) float64 {
	return math.Abs(got-want) / math.Max(math.Abs(want), 1e-300)
}

// TestReferenceOracle_CompareSEMMatchesR: every case's comparison
// matches lavTestLRT — each fit's chi-square and df, the df difference,
// the (scaled) statistic, its scaling factor and p — and the statistic
// is exactly the result's own closed form over its reported figures.
func TestReferenceOracle_CompareSEMMatchesR(t *testing.T) {
	t.Parallel()

	p, _ := brandGroupCohort{csv: "brand", wave: encoding.FieldTypeCategoricalU8}.build(t, pulse.Options{})
	for _, c := range loadSEMCompare(t) {
		t.Run(c.Name, func(t *testing.T) {
			out, err := p.Compose(context.Background(), c.batch())
			if err != nil {
				t.Fatal(err)
			}
			if len(out.Comparisons) != 1 {
				t.Fatalf("%d comparisons", len(out.Comparisons))
			}
			r := out.Comparisons[0]
			if r.Error != nil {
				t.Fatalf("comparison failed: %s %s %v", r.Error.Code, r.Error.Message, r.Error.Details)
			}
			if r.Test != types.ComparisonTestLRT || r.Distribution != types.ComparisonDistributionChiSquare || r.Terms == nil || len(r.Terms) != 0 {
				t.Fatalf("test %q distribution %q terms %v", r.Test, r.Distribution, r.Terms)
			}
			if r.DF != c.DF || r.RestrictedDFResidual != c.RestrictedDF || r.FullDFResidual != c.FullDF || float64(r.NObs) != c.N {
				t.Fatalf("df %v (%v, %v) n %d, want %v (%v, %v) n %v", r.DF, r.RestrictedDFResidual, r.FullDFResidual, r.NObs, c.DF, c.RestrictedDF, c.FullDF, c.N)
			}
			// The fits are lavaan's to its optimizer's stop: ML within
			// 1e-8 (measured ≤ 4e-9), the ordinal two-step within 1e-6
			// (measured ≤ 1.1e-7).
			tol := 1e-8
			if c.Estimator != "ml" {
				tol = 1e-6
			}
			for _, f := range []struct {
				name      string
				got, want float64
			}{
				{"restricted chisq", r.RestrictedDeviance, c.RestrictedChisq},
				{"full chisq", r.FullDeviance, c.FullChisq},
				{"statistic", r.Statistic, c.Statistic},
				{"p_value", r.PValue, c.PValue},
			} {
				w := tol
				if f.name == "p_value" {
					// p moves with the statistic times its density
					// slope, so its relative error grows in the far tail.
					w = math.Max(tol, tol*c.Statistic)
				}
				if d := semRel(f.got, f.want); !(d <= w) {
					t.Errorf("%s = %.12g, lavaan %.12g (rel %.2g > %.2g)", f.name, f.got, f.want, d, w)
				}
			}
			closed := r.RestrictedDeviance - r.FullDeviance
			if c.ScalingFactor != nil {
				if d := semRel(r.ScalingFactor, *c.ScalingFactor); !(d <= tol) {
					t.Errorf("scaling_factor = %.12g, lavaan %.12g (rel %.2g)", r.ScalingFactor, *c.ScalingFactor, d)
				}
				closed /= r.ScalingFactor
			} else if r.ScalingFactor != 0 {
				t.Errorf("unscaled comparison carries scaling_factor %v", r.ScalingFactor)
			}
			if r.Statistic != closed {
				t.Errorf("statistic %v is not the closed form %v", r.Statistic, closed)
			}
		})
	}
}
