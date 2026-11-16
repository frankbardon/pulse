package regmmm_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/regmmm"
	"github.com/frankbardon/pulse/types"
)

// hill_adstock_oracle_test.go pins REG_MMM_HILL's in-model adstock
// (params.adstock, U71 E2-S3, #551) to R's minpack.lm, one case per
// kernel — geometric, delayed, weibull_cdf, weibull_pdf:
// scripts/reference/gen_mmm_hill_adstock.R (`make
// reference-mmm-hill-adstock`) fits the same joint objective from the
// same start (the operator's five-decay scan) inside the same boxes,
// its kernel weights written independently (closed forms;
// stats::pweibull / dweibull for the Weibull kernels), nlsLM on a
// finite-difference Jacobian and nls.lm on the script's analytic one
// agreeing to 1e-6, the stored point Gauss-Newton polished to the
// stationary point. Each case runs end to end — a cohort, an ordered
// REG_MMM_HILL slot, Process — comparing every coefficient (the kernel
// parameters included), standard error, R², residual SE, df and
// deviance.
//
// Tolerances: hillOracleRel (1e-6, hill_oracle_test.go), the engine's
// Levenberg–Marquardt stop rule against the exact optimum. The measured
// worst disagreement is 2.9e-8 on a coefficient and 3.7e-8 on a
// standard error (2.2e-15 on the fit figures), so 1e-6 is ≥ 25× the
// evidence; a 0.1% error in one Weibull derivative fails it by 1000×
// (falsified). The start and its k scale (hillAdstockStartRel = 1e-10;
// measured 4.2e-13) are a linear least-squares solve and a convolution
// on both sides, the Weibull weights from different formulas (log-space
// density vs dweibull), so a few ulps apart.
const hillAdstockStartRel = 1e-10

// hillAdstockOracleCase is one mmm_hill_adstock.json case.
type hillAdstockOracleCase struct {
	Name           string               `json:"name"`
	Kernel         string               `json:"kernel"`
	MaxLag         int                  `json:"max_lag"`
	N              int                  `json:"n"`
	Columns        map[string][]float64 `json:"columns"`
	Coefficients   []string             `json:"coefficients"`
	KRef           float64              `json:"kref"`
	Start          []float64            `json:"start"`
	Estimates      []float64            `json:"estimates"`
	StdErrors      []float64            `json:"std_errors"`
	Deviance       float64              `json:"deviance"`
	R2             float64              `json:"r2"`
	ResidualStdErr float64              `json:"residual_std_err"`
	DF             float64              `json:"df"`
	// Influence is the fit's tangent-plane figures over its own rows
	// (hill_influence_oracle_test.go).
	Influence map[string][]float64 `json:"influence"`
}

// loadHillAdstockOracle reads the committed golden, checking its hash
// footer, its recorded R and minpack.lm versions and that every kernel
// has its case.
func loadHillAdstockOracle(t *testing.T) []hillAdstockOracleCase {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "mmm_hill_adstock.json"))
	if err != nil {
		t.Fatalf("read mmm_hill_adstock.json: %v (regenerate with `make reference-mmm-hill-adstock`)", err)
	}
	s := string(raw)
	i := strings.LastIndex(s, "\n// golden-hash: ")
	if i < 0 {
		t.Fatal("mmm_hill_adstock.json has no golden-hash footer")
	}
	body := raw[:i]
	sum := sha256.Sum256(body)
	if got := strings.TrimSpace(s[i+len("\n// golden-hash: "):]); got != hex.EncodeToString(sum[:]) {
		t.Fatal("mmm_hill_adstock.json hash mismatch (hand-edited? regenerate with `make reference-mmm-hill-adstock`)")
	}
	var doc struct {
		RVersion string                  `json:"r_version"`
		Packages map[string]string       `json:"packages"`
		Cases    []hillAdstockOracleCase `json:"cases"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.RVersion != "R version 4.6.1 (2026-06-24)" || doc.Packages["minpack.lm"] != "1.2.4" {
		t.Fatalf("mmm_hill_adstock.json records R %q, minpack.lm %q", doc.RVersion, doc.Packages["minpack.lm"])
	}
	seen := map[string]bool{}
	for _, c := range doc.Cases {
		seen[c.Kernel] = true
	}
	for _, k := range []string{"geometric", "delayed", "weibull_cdf", "weibull_pdf"} {
		if !seen[k] {
			t.Fatalf("mmm_hill_adstock.json has no %s case", k)
		}
	}
	return doc.Cases
}

// hillAdstockOracleCohort writes the case as week (u16, the row order),
// y, tv and price (f64) to name.
func hillAdstockOracleCohort(t *testing.T, p *pulse.Pulse, name string, c hillAdstockOracleCase) {
	t.Helper()
	cols := []string{"y", "tv", "price"}
	s := encoding.Schema{Fields: []encoding.Field{{Name: "week", Type: encoding.FieldTypeU16, Description: "Hill adstock oracle row order."}}}
	off := s.FieldWidth(0)
	for i, col := range cols {
		if len(c.Columns[col]) != c.N {
			t.Fatalf("%s: column %s has %d rows, want %d", c.Name, col, len(c.Columns[col]), c.N)
		}
		s.Fields = append(s.Fields, encoding.Field{Name: col, Type: encoding.FieldTypeF64, CsvColumnIdx: i + 1, ByteOffset: off,
			Description: "Hill adstock oracle column " + col + "."})
		off += s.FieldWidth(i + 1)
	}
	b, err := p.NewCohortBuilder(context.Background(), name, s, pulse.CohortBuilderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for r := range c.N {
		row := pulse.CohortRow{uint64(r)}
		for _, col := range cols {
			row = append(row, c.Columns[col][r])
		}
		if err := b.Append(row); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.Close(); err != nil {
		t.Fatal(err)
	}
}

// TestReferenceOracle_MMMHillAdstock: every kernel's case of
// mmm_hill_adstock.json, fitted by REG_MMM_HILL with params.adstock
// through Process, matches minpack.lm's optimum — coefficients (the
// fitted kernel parameters among them) and standard errors.
func TestReferenceOracle_MMMHillAdstock(t *testing.T) {
	t.Parallel()

	cases := loadHillAdstockOracle(t)
	p := instance(t, afero.NewMemMapFs(), nil)
	worstEst, worstSE, worstFit, worstStart := 0.0, 0.0, 0.0, 0.0
	for _, c := range cases {
		file := c.Name + ".pulse"
		hillAdstockOracleCohort(t, p, file, c)

		// The start and k scale: the point R's nlsLM started from.
		x0, kRef, err := regmmm.HillAdstockStart(c.Kernel, c.MaxLag, c.Columns["y"], c.Columns["tv"], [][]float64{c.Columns["price"]})
		if err != nil {
			t.Fatalf("%s: start: %v", c.Name, err)
		}
		if e := relErr(kRef, c.KRef, 0); e > hillAdstockStartRel {
			t.Errorf("%s: kref = %.17g, R %.17g (rel %.2g)", c.Name, kRef, c.KRef, e)
		}
		for i, name := range c.Coefficients {
			e := relErr(x0[i], c.Start[i], 1)
			worstStart = math.Max(worstStart, e)
			if e > hillAdstockStartRel {
				t.Errorf("%s: start %s = %.17g, R %.17g (rel %.2g)", c.Name, name, x0[i], c.Start[i], e)
			}
		}

		params, _ := json.Marshal(map[string]any{"channels": []string{"tv"}, "adstock": c.Kernel, "max_lag": c.MaxLag})
		s := &types.RegressionSpec{Type: regmmm.Name, Name: "mmm", Target: "y", Predictors: []string{"tv", "price"},
			Params: params, Order: &types.RegressionOrder{Field: "week"}}
		resp := process(t, p, &types.Request{Cohort: &types.Cohort{Filename: file}, Regressions: []*types.RegressionSpec{s}})
		if len(resp.Regressions) != 1 || resp.Regressions[0].Error != nil {
			t.Fatalf("%s: regressions = %s", c.Name, mustJSON(t, resp.Regressions))
		}
		res := resp.Regressions[0]
		if len(res.Coefficients) != len(c.Coefficients) {
			t.Fatalf("%s: %d coefficients, R %d", c.Name, len(res.Coefficients), len(c.Coefficients))
		}
		for i, name := range c.Coefficients {
			got, ok := res.Coefficients[name]
			if !ok {
				t.Errorf("%s: no coefficient %s", c.Name, name)
				continue
			}
			e := relErr(got, c.Estimates[i], 1)
			worstEst = math.Max(worstEst, e)
			if e > hillOracleRel {
				t.Errorf("%s: %s = %.17g, R %.17g (rel %.2g)", c.Name, name, got, c.Estimates[i], e)
			}
			e = relErr(res.StdErrors[name], c.StdErrors[i], 0)
			worstSE = math.Max(worstSE, e)
			if e > hillOracleRel {
				t.Errorf("%s: SE(%s) = %.17g, R %.17g (rel %.2g)", c.Name, name, res.StdErrors[name], c.StdErrors[i], e)
			}
		}
		for _, f := range []struct {
			what      string
			got, want float64
		}{
			{"deviance", res.Deviance, c.Deviance},
			{"r2", res.R2, c.R2},
			{"residual_std_err", res.ResidualStdErr, c.ResidualStdErr},
			{"df", res.DF, c.DF},
		} {
			e := relErr(f.got, f.want, 0)
			worstFit = math.Max(worstFit, e)
			if e > hillOracleRel {
				t.Errorf("%s: %s = %.17g, R %.17g (rel %.2g)", c.Name, f.what, f.got, f.want, e)
			}
		}
	}
	t.Logf("worst relative error: start %.2g, estimates %.2g, std errors %.2g, fit %.2g", worstStart, worstEst, worstSE, worstFit)
}
