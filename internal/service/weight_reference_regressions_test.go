package service

import (
	"context"
	"encoding/json"
	"math"
	"slices"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// Weighted regression and regression-attribute references
// (weighting-inferential E4-S3). TestWeightReferenceValues/regressions
// pins every weight-aware REG_* and ATTR_REG_* per weight configuration
// to figures an EXTERNAL tool computed offline over the generated
// fixture weightRefRegRows (gen_weight_reference.py, "weighted
// regressions"):
//
//   - kind frequency — stock R 4.6.1 on the rep()-expanded rows
//     (testdata/weight_reference/reg_reference.R): lm for REG_OLS and the
//     attributes (predict(); leverage = f × one copy's hatvalues()), glm
//     with summary(dispersion = 1) for REG_GLM; scikit-learn on the
//     expansion for the penalised β; REG_BAYES_LINEAR (frequency-only)
//     against its conjugate posterior in closed form on the expansion —
//     no stock package fits that prior;
//   - kind probability — the closed form on w* = w·n_eff/Σw (OLS: df
//     n_eff − q, SEs cross-checked with statsmodels GLM(freq_weights =
//     w*); GLM: IRLS on w*, cross-checked likewise), scikit-learn with
//     sample_weight = w for the penalised β, and the scale-free attribute
//     closed form; every closed form must equal R on the frequency
//     configuration before a row is written.
//
// Cases reuse weightRefInferCase: a regression case's figures are wire
// paths under regressions[0]; an attribute case reads its per-row value
// back as an opted-out AGG_SUM grouped by the fixture's id column, keyed
// data[<id>].v.
//
// Tolerances (relative; absolute below 1e-12), weightRefRegTol: Pulse
// folds Welford moments where R solves a QR and numpy an explicit
// inverse — 1e-11 for the closed-form figures (worst seen on arm64:
// 8e-14, an attribute's fitted value), 1e-10 for p-values and credible
// bounds (incomplete-beta series vs scipy; worst 7e-12); IRLS figures
// 1e-9 and coordinate-descent β 1e-8 (two iterative solvers meet only at
// their convergence tolerance; worst seen 2e-15 / exact). All leave room
// for amd64 FMA contraction and stay far below any weighting mistake (w
// vs w* moves a standard error by tens of percent).

type weightRefRegRow struct {
	id            string
	x1, x2        float64
	x2Null        bool
	y, yb, yp, yg float64
	w, f, p       float64
}

func weightRefRegSchema() *encoding.Schema {
	ids := make([]string, len(weightRefRegRows))
	for i, r := range weightRefRegRows {
		ids[i] = r.id
	}
	fields := []encoding.Field{{Name: "id", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 0, CsvColumnIdx: 0, Dictionary: parityCatDict(ids...)}}
	for i, name := range []string{"x1", "x2", "y", "yb", "yp", "yg", "w", "f", "p"} {
		fields = append(fields, encoding.Field{Name: name, Type: encoding.FieldTypeF64, ByteOffset: 1 + 8*i, CsvColumnIdx: i + 1, Nullable: name == "x2"})
	}
	return &encoding.Schema{Fields: fields}
}

const weightRefRegCohort = "ref_regs.pulse"

func weightRefRegService(t *testing.T) *Service {
	t.Helper()
	recs := make([][]uint64, len(weightRefRegRows))
	for i, r := range weightRefRegRows {
		recs[i] = []uint64{uint64(i)}
		for _, v := range []float64{r.x1, r.x2, r.y, r.yb, r.yp, r.yg, r.w, r.f, r.p} {
			recs[i] = append(recs[i], math.Float64bits(v))
		}
	}
	b := writeNullablePulse(t, weightRefRegSchema(), recs, func(r, f int) bool {
		return f == 2 && weightRefRegRows[r].x2Null
	})
	cfg := fs.NewMemMap()
	if err := afero.WriteFile(cfg.Fs(), weightRefRegCohort, b, 0o644); err != nil {
		t.Fatal(err)
	}
	svc := New(cfg)
	svc.SetShardWorkers(1)
	svc.SetDecodeWorkers(1)
	return svc
}

// weightRefRegRun runs one case under its request weight, buffered or
// streamed; ok=false when the request cannot take the streaming arm.
func weightRefRegRun(t *testing.T, svc *Service, c weightRefInferCase, buffered bool) (map[string]any, bool) {
	t.Helper()
	req := &types.Request{
		Cohort: &types.Cohort{Filename: weightRefRegCohort},
		Weight: &types.WeightSpec{Field: c.weight, Kind: weightRefKind(c.kind)},
	}
	if err := json.Unmarshal([]byte(c.request), req); err != nil {
		t.Fatalf("%s: request fragment: %v", c.name, err)
	}
	req.Aggregations = append(req.Aggregations, testSteerCount())
	if buffered {
		req.Aggregations = append(req.Aggregations, &types.Aggregation{
			Type: types.AGG_MEDIAN, Field: "y", Label: paritySteer + "median", Weight: types.NullSlotWeight()})
	} else if !processing.CanStreamRequest(req, weightRefRegSchema()) {
		return nil, false
	}
	resp, err := svc.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("%s: %v", c.name, err)
	}
	stripSteer(resp)
	var wire map[string]any
	if err := json.Unmarshal(mustMarshal(t, resp), &wire); err != nil {
		t.Fatal(err)
	}
	return wire, true
}

// regFigure resolves a regression-case figure path: data[<id>].<col>
// is the data row of that id group, anything else a wirePath.
func regFigure(wire map[string]any, path string) (any, error) {
	rest, ok := strings.CutPrefix(path, "data[")
	if !ok {
		return wirePath(wire, path)
	}
	id, col, _ := strings.Cut(rest, "].")
	rows, _ := wire["data"].([]any)
	for _, r := range rows {
		if m, _ := r.(map[string]any); m != nil && m["id"] == id {
			v, ok := m[col]
			if !ok {
				return nil, errPath(path, col, "missing")
			}
			return v, nil
		}
	}
	return nil, errPath(path, id, "no data row for the id")
}

// weightRefRegTol is a figure's relative tolerance (file comment).
func weightRefRegTol(name, path string) float64 {
	switch {
	case name == "lasso" || name == "elasticnet":
		return 1e-8
	case strings.HasPrefix(name, "glm_"):
		return 1e-9
	case strings.Contains(path, ".p_values.") || strings.Contains(path, ".credible_intervals."):
		return 1e-10
	}
	return 1e-11
}

// weightRefRegCaseSlot is the surface and operator a case's request
// slot names.
func weightRefRegCaseSlot(t *testing.T, c weightRefInferCase) (weightSurface, string) {
	t.Helper()
	var frag struct {
		Regressions []struct {
			Type string `json:"type"`
		} `json:"regressions"`
		Attributes []struct {
			Type string `json:"type"`
		} `json:"attributes"`
	}
	if err := json.Unmarshal([]byte(c.request), &frag); err != nil {
		t.Fatalf("%s: %v", c.name, err)
	}
	switch {
	case len(frag.Regressions) == 1 && len(frag.Attributes) == 0:
		return weightSurfaceRegressions, frag.Regressions[0].Type
	case len(frag.Attributes) == 1 && len(frag.Regressions) == 0:
		return weightSurfaceAttributes, frag.Attributes[0].Type
	}
	t.Fatalf("%s: request %s does not hold exactly one regression or attribute slot", c.name, c.request)
	return "", ""
}

// assertWeightRefRegCoverage: every regression and attribute the
// manifest marks weighted has a reference case under each advertised
// kind, the probability kind under BOTH probability configurations.
func assertWeightRefRegCoverage(t *testing.T) {
	t.Helper()
	have := map[weightSurface]map[string][]types.WeightKind{
		weightSurfaceRegressions: {}, weightSurfaceAttributes: {},
	}
	configs := map[string]map[string]bool{}
	for _, c := range weightRefRegCases {
		s, op := weightRefRegCaseSlot(t, c)
		if k := weightRefKind(c.kind); !slices.Contains(have[s][op], k) {
			have[s][op] = append(have[s][op], k)
		}
		if configs[op] == nil {
			configs[op] = map[string]bool{}
		}
		configs[op][c.weight+"_"+c.kind] = true
	}
	for _, s := range []weightSurface{weightSurfaceRegressions, weightSurfaceAttributes} {
		assertWeightKindCoverage(t, s, have[s], "REG_SPECS / ATTR_SPECS in testdata/weight_reference/gen_weight_reference.py, then regenerate")
		for op, kinds := range manifestWeightKinds(s) {
			if slices.Contains(kinds, types.WeightKindProbability) && !(configs[op]["w_probability"] && configs[op]["p_probability"]) {
				t.Errorf("%s: probability reference cases %v, want both w_probability and p_probability", op, configs[op])
			}
		}
	}
}

// testWeightRefRegs is TestWeightReferenceValues' regression half.
func testWeightRefRegs(t *testing.T) {
	t.Run("coverage", assertWeightRefRegCoverage)
	for _, tool := range []string{"R ", "statsmodels", "scikit-learn"} {
		if !strings.Contains(weightRefRegProvenance, tool) {
			t.Fatalf("weightRefRegProvenance %q does not record %s", weightRefRegProvenance, tool)
		}
	}
	svc := weightRefRegService(t)
	streamed := 0
	for _, c := range weightRefRegCases {
		t.Run(c.weight+"_"+c.kind+"/"+c.name, func(t *testing.T) {
			if c.source == "" || len(c.figures) == 0 {
				t.Fatal("case has no provenance source or no figures")
			}
			for _, buffered := range []bool{true, false} {
				arm := "streaming"
				if buffered {
					arm = "buffered"
				}
				wire, ok := weightRefRegRun(t, svc, c, buffered)
				if !ok {
					continue
				}
				if !buffered {
					streamed++
				}
				for _, path := range sortedNames(c.figures) {
					want := c.figures[path]
					got, err := regFigure(wire, path)
					if err != nil {
						t.Errorf("%s: %v", arm, err)
						continue
					}
					g, ok := got.(float64)
					tol := weightRefRegTol(c.name, path)
					if !ok || !(relClose(g, want, tol) || math.Abs(g-want) <= 1e-12) {
						t.Errorf("%s %s = %v, reference %.17g (rel %.3g, tol %g)", arm, path, got, want,
							math.Abs(g-want)/math.Abs(want), tol)
					}
				}
			}
		})
	}
	if streamed == 0 {
		t.Fatal("no regression case ran on the streaming arm: the streaming half is vacuous")
	}
}

// weightRefRegPointEstimate: the figures that read only Σw ratios, never
// N* — the same number under frequency f and probability p = c·f. Every
// attribute figure is one (β and the hat diagonal are scale-free).
func weightRefRegPointEstimate(path string) bool {
	return strings.HasPrefix(path, "data[") || strings.Contains(path, ".coefficients.") ||
		strings.HasSuffix(path, "].r2") || strings.HasSuffix(path, ".pseudo_r2")
}

// testWeightRefRegsKindsAgree is TestWeightReferenceKindsAgree's
// regression half, on Pulse's own answers: f frequency and p probability
// agree on every point estimate; a fit's standard errors must differ.
func testWeightRefRegsKindsAgree(t *testing.T) {
	svc := weightRefRegService(t)
	byKey := map[string]weightRefInferCase{}
	for _, c := range weightRefRegCases {
		byKey[c.weight+"|"+c.kind+"|"+c.name] = c
	}
	compared := 0
	for _, fc := range weightRefRegCases {
		if fc.weight != "f" {
			continue
		}
		s, op := weightRefRegCaseSlot(t, fc)
		if !slices.Contains(manifestWeightKinds(s)[op], types.WeightKindProbability) {
			continue // frequency-only: no probability twin
		}
		pc, ok := byKey["p|probability|"+fc.name]
		if !ok {
			t.Errorf("%s: no p_probability twin case", fc.name)
			continue
		}
		t.Run(fc.name, func(t *testing.T) {
			fw, _ := weightRefRegRun(t, svc, fc, true)
			pw, _ := weightRefRegRun(t, svc, pc, true)
			points := 0
			for _, path := range sortedNames(fc.figures) {
				if !weightRefRegPointEstimate(path) {
					continue
				}
				fv, ferr := regFigure(fw, path)
				pv, perr := regFigure(pw, path)
				if ferr != nil || perr != nil {
					t.Fatalf("%s: %v / %v", path, ferr, perr)
				}
				f, _ := fv.(float64)
				p, _ := pv.(float64)
				if !(relClose(p, f, weightRefRegTol(fc.name, path)) || math.Abs(p-f) <= 1e-12) {
					t.Errorf("%s: probability p = %.17g, frequency f = %.17g", path, p, f)
				}
				points++
			}
			if points == 0 {
				t.Fatal("no point estimate compared")
			}
			if s == weightSurfaceRegressions {
				fs, _ := wirePath(fw, "regressions[0].std_errors.x1")
				ps, _ := wirePath(pw, "regressions[0].std_errors.x1")
				if fs == nil || fs == ps {
					t.Errorf("std_errors.x1 %v / %v: N* is not kind-dependent", fs, ps)
				}
			}
			compared++
		})
	}
	if compared == 0 {
		t.Fatal("no regression compared across kinds")
	}
}
