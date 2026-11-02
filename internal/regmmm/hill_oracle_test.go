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

// hill_oracle_test.go pins REG_MMM_HILL to R's minpack.lm (U64 E4-S1,
// #619): scripts/reference/gen_mmm_hill.R fits the same objective
// (½·Σr², plus the prior's pseudo-residual), from the same start, inside
// the same bounds, with nlsLM and nls.lm (analytic Jacobian) agreeing
// to 1e-7 and the stored point Gauss-Newton polished to the stationary
// point. The test runs each case through the operator end to end — a
// cohort, a REG_MMM_HILL slot, Process — and compares every
// coefficient, standard error, R², residual SE, df and deviance.
//
// Tolerance (hillOracleRel = 1e-6, the optimize oracles' tolerance):
// the reference is the exact stationary point (gradient 0 to 1e-9 of
// |J_j|·|r| in R), while Pulse's Levenberg–Marquardt stops on its own
// rules (relative cost change 1e-13, relative step 1e-10,
// optimize.LeastSquaresOptions' defaults), so its x sits a stopping
// distance from that point, magnified along the fit's ill-conditioned
// directions (a channel's β, k and s trade off along a valley). The
// measured worst disagreement is 4.3e-8 on a coefficient and 9.2e-8 on
// a standard error (held_channel), 2e-9 on the fit figures; with the
// stop rules tightened to 1e-16 / 1e-14 it falls to 3.7e-9 / 9.7e-9, so
// it is the stop rule, not the objective. 1e-6 is ≥ 10× the evidence
// and far below what a wrong fit moves: a σ² df off by one, the prior's
// precision 1% off or a 0.1% error in the s Jacobian each fail it by
// 300× or more (falsified). The start is compared tighter (hillStartRel):
// a linear least-squares solve on both sides. A held β is compared
// exactly (both sides place it on 0).
const (
	hillOracleRel = 1e-6
	hillStartRel  = 1e-11
)

// hillOracleCase is one mmm_hill.json case.
type hillOracleCase struct {
	Name           string               `json:"name"`
	N              int                  `json:"n"`
	Channels       []string             `json:"channels"`
	Controls       []string             `json:"controls"`
	Columns        map[string][]float64 `json:"columns"`
	Coefficients   []string             `json:"coefficients"`
	Start          []float64            `json:"start"`
	Estimates      []float64            `json:"estimates"`
	Free           []string             `json:"free"`
	StdErrors      []float64            `json:"std_errors"`
	Held           []string             `json:"held"`
	Deviance       float64              `json:"deviance"`
	R2             float64              `json:"r2"`
	ResidualStdErr float64              `json:"residual_std_err"`
	DF             float64              `json:"df"`
	Prior          *struct {
		Channel   string  `json:"channel"`
		Mean      float64 `json:"mean"`
		Precision float64 `json:"precision"`
	} `json:"prior"`
}

// loadHillOracle reads the committed golden, checking its hash footer
// and its recorded R and minpack.lm versions.
func loadHillOracle(t *testing.T) []hillOracleCase {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "processing", "testdata", "reference", "mmm_hill.json"))
	if err != nil {
		t.Fatalf("read mmm_hill.json: %v (regenerate with `make reference`)", err)
	}
	s := string(raw)
	i := strings.LastIndex(s, "\n// golden-hash: ")
	if i < 0 {
		t.Fatal("mmm_hill.json has no golden-hash footer")
	}
	body := raw[:i]
	sum := sha256.Sum256(body)
	if got := strings.TrimSpace(s[i+len("\n// golden-hash: "):]); got != hex.EncodeToString(sum[:]) {
		t.Fatal("mmm_hill.json hash mismatch (hand-edited? regenerate with `make reference`)")
	}
	var doc struct {
		RVersion string            `json:"r_version"`
		Packages map[string]string `json:"packages"`
		Cases    []hillOracleCase  `json:"cases"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.RVersion != "R version 4.6.1 (2026-06-24)" || doc.Packages["minpack.lm"] != "1.2.4" {
		t.Fatalf("mmm_hill.json records R %q, minpack.lm %q", doc.RVersion, doc.Packages["minpack.lm"])
	}
	if len(doc.Cases) < 4 {
		t.Fatalf("mmm_hill.json has %d cases", len(doc.Cases))
	}
	return doc.Cases
}

// hillOracleCohort writes the case's columns (y, channels, controls) as
// f64 fields to name.
func hillOracleCohort(t *testing.T, p *pulse.Pulse, name string, c hillOracleCase) {
	t.Helper()
	cols := append(append([]string{"y"}, c.Channels...), c.Controls...)
	s := encoding.Schema{}
	off := 0
	for i, col := range cols {
		if len(c.Columns[col]) != c.N {
			t.Fatalf("%s: column %s has %d rows, want %d", c.Name, col, len(c.Columns[col]), c.N)
		}
		s.Fields = append(s.Fields, encoding.Field{Name: col, Type: encoding.FieldTypeF64, CsvColumnIdx: i, ByteOffset: off,
			Description: "Hill oracle column " + col + "."})
		off += s.FieldWidth(i)
	}
	b, err := p.NewCohortBuilder(context.Background(), name, s, pulse.CohortBuilderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for r := range c.N {
		row := make(pulse.CohortRow, len(cols))
		for i, col := range cols {
			row[i] = c.Columns[col][r]
		}
		if err := b.Append(row); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.Close(); err != nil {
		t.Fatal(err)
	}
}

// relErr is |got − want| / max(|want|, floor).
func relErr(got, want, floor float64) float64 {
	return math.Abs(got-want) / math.Max(math.Abs(want), floor)
}

// TestReferenceOracle_MMMHill: every case of mmm_hill.json, fitted by
// REG_MMM_HILL through Process, matches minpack.lm's optimum.
func TestReferenceOracle_MMMHill(t *testing.T) {
	t.Parallel()

	cases := loadHillOracle(t)
	p := instance(t, afero.NewMemMapFs(), nil)
	worstEst, worstSE, worstFit, worstStart := 0.0, 0.0, 0.0, 0.0
	for _, c := range cases {
		file := c.Name + ".pulse"
		hillOracleCohort(t, p, file, c)

		// The start: the same point R's nlsLM started from.
		a := make([][]float64, len(c.Channels))
		for i, ch := range c.Channels {
			a[i] = c.Columns[ch]
		}
		z := make([][]float64, len(c.Controls))
		for j, ct := range c.Controls {
			z[j] = c.Columns[ct]
		}
		priorIdx, pm, pp := -1, 0.0, 0.0
		params := map[string]any{"channels": c.Channels}
		if c.Prior != nil {
			for i, ch := range c.Channels {
				if ch == c.Prior.Channel {
					priorIdx = i
				}
			}
			pm, pp = c.Prior.Mean, c.Prior.Precision
			params["prior"] = map[string]any{"channel": c.Prior.Channel, "mean": pm, "precision": pp}
		}
		x0, err := regmmm.HillStart(c.Columns["y"], a, z, priorIdx, pm, pp)
		if err != nil {
			t.Fatalf("%s: start: %v", c.Name, err)
		}
		for i, name := range c.Coefficients {
			e := relErr(x0[i], c.Start[i], 1)
			worstStart = math.Max(worstStart, e)
			if e > hillStartRel {
				t.Errorf("%s: start %s = %.17g, R %.17g (rel %.2g)", c.Name, name, x0[i], c.Start[i], e)
			}
		}

		raw, _ := json.Marshal(params)
		s := &types.RegressionSpec{Type: regmmm.Name, Name: "mmm", Target: "y",
			Predictors: append(append([]string{}, c.Channels...), c.Controls...), Params: raw}
		resp := process(t, p, &types.Request{Cohort: &types.Cohort{Filename: file}, Regressions: []*types.RegressionSpec{s}})
		if len(resp.Regressions) != 1 || resp.Regressions[0].Error != nil {
			t.Fatalf("%s: regressions = %s", c.Name, mustJSON(t, resp.Regressions))
		}
		res := resp.Regressions[0]

		held := map[string]bool{}
		for _, h := range c.Held {
			held[h] = true
			held[regmmm.HalfSaturationPrefix+h] = true
			held[regmmm.ShapePrefix+h] = true
		}
		for i, name := range c.Coefficients {
			got, ok := res.Coefficients[name]
			if !ok {
				t.Errorf("%s: no coefficient %s", c.Name, name)
				continue
			}
			switch {
			case held[name] && !strings.Contains(name, ":"):
				if got != 0 {
					t.Errorf("%s: held %s = %v, want exactly 0", c.Name, name, got)
				}
			case held[name]:
				// A held channel's k and s are not identified: not pinned.
			default:
				e := relErr(got, c.Estimates[i], 1)
				worstEst = math.Max(worstEst, e)
				if e > hillOracleRel {
					t.Errorf("%s: %s = %.17g, R %.17g (rel %.2g)", c.Name, name, got, c.Estimates[i], e)
				}
			}
			if held[name] && !math.IsNaN(res.StdErrors[name]) {
				t.Errorf("%s: held %s has std error %v, want NaN (null)", c.Name, name, res.StdErrors[name])
			}
		}
		for i, name := range c.Free {
			e := relErr(res.StdErrors[name], c.StdErrors[i], 0)
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
