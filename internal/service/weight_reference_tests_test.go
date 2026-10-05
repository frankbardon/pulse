package service

import (
	"context"
	"encoding/json"
	"math"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// Weighted reference fixtures for the inferential slots
// (weighting-inferential E1-S4). TestWeightReferenceValues/tests pins
// each weight-aware tier-1 test, per weight configuration, to figures an
// EXTERNAL tool computed offline: kind frequency is stock R on the
// rep()-expanded rows (testdata/weight_reference/test_reference.R —
// t.test, oneway.test, aov, cor.test, chisq.test, prop.test); kind
// probability is the closed form of the one formula rule on w*, its
// moment step cross-checked against statsmodels DescrStatsW /
// CompareMeans / proportions_ztest and scipy chi2_contingency — and the
// same closed form must equal R on the frequency configuration before a
// row is written. All generated into weight_reference_values_test.go by
// gen_weight_reference.py; never hand-edited.
//
// The fixture weightRefTestRows (68 contributing rows, 2 / 3 / 3-level
// categoricals) is large enough for every df and for χ²'s expected-count
// guard under all three configurations (the generator asserts it), and
// carries a null x, zero weights, a negative, a NaN and a non-integer
// frequency weight.
//
// Adding an inferential row (rank tests E2, regressions E4, attributes /
// groupers / CIs E5): extend gen_weight_reference.py so it emits a
// weightRefInferCase — `request` is the JSON slot fragment decoded onto
// a fresh request, `figures` maps a wire path from the response root
// (dot-separated keys; `[i]` an index, `[label]` the position of label in
// the sibling "groups" array) to its reference value — and give the
// surface a coverage call (weight_coverage_test.go). The runner below is
// slot-agnostic.
//
// Tolerances (relative; absolute when the reference is 0), from
// weightRefFigureTol: 1e-12 for statistics, df and estimates — R and
// scipy sum in two passes where Pulse folds Welford recurrences, so they
// sit a few ulps apart (worst seen on arm64: 1e-14, eta² / Σ of squares);
// 1e-10 for p-values and t-based CI bounds — R's pt / qt and Pulse's
// statdist evaluate the incomplete beta by different series, a tail
// amplifies the statistic's error by |t·d ln p/dt|, and a CI bound near
// zero is a difference of two O(1) terms (worst seen: 1.1e-13). Both
// leave room for amd64 FMA contraction. The z / prop-z Wald and Pearson
// Fisher-z CI bounds are not pinned yet: they still use an approximate
// inverse-erf.

type weightRefTestRow struct {
	x, y    float64
	xNull   bool
	h, k, o string
	w, f, p float64
}

// weightRefInferCase is one generated reference row for an inferential
// slot (see the file comment).
type weightRefInferCase struct {
	weight, kind, name, request string
	figures                     map[string]float64
	source                      string
}

var (
	weightRefTestH = []string{"a", "b"}
	weightRefTestK = []string{"p", "q", "r"}
	weightRefTestO = []string{"maybe", "no", "yes"}
)

func weightRefTestSchema() *encoding.Schema {
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "x", Type: encoding.FieldTypeF64, ByteOffset: 0, CsvColumnIdx: 0, Nullable: true},
		{Name: "y", Type: encoding.FieldTypeF64, ByteOffset: 8, CsvColumnIdx: 1},
		{Name: "h", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 16, CsvColumnIdx: 2, Dictionary: parityCatDict(weightRefTestH...)},
		{Name: "k", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 17, CsvColumnIdx: 3, Dictionary: parityCatDict(weightRefTestK...)},
		{Name: "o", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 18, CsvColumnIdx: 4, Dictionary: parityCatDict(weightRefTestO...)},
		{Name: "w", Type: encoding.FieldTypeF64, ByteOffset: 19, CsvColumnIdx: 5},
		{Name: "f", Type: encoding.FieldTypeF64, ByteOffset: 27, CsvColumnIdx: 6},
		{Name: "p", Type: encoding.FieldTypeF64, ByteOffset: 35, CsvColumnIdx: 7},
	}}
}

func weightRefTestService(t *testing.T) *Service {
	t.Helper()
	id := func(labels []string, l string) uint64 {
		i := slices.Index(labels, l)
		if i < 0 {
			t.Fatalf("label %q not in %v", l, labels)
		}
		return uint64(i)
	}
	recs := make([][]uint64, len(weightRefTestRows))
	for i, r := range weightRefTestRows {
		recs[i] = []uint64{math.Float64bits(r.x), math.Float64bits(r.y),
			id(weightRefTestH, r.h), id(weightRefTestK, r.k), id(weightRefTestO, r.o),
			math.Float64bits(r.w), math.Float64bits(r.f), math.Float64bits(r.p)}
	}
	b := writeNullablePulse(t, weightRefTestSchema(), recs, func(r, f int) bool {
		return f == 0 && weightRefTestRows[r].xNull
	})
	cfg := fs.NewMemMap()
	if err := afero.WriteFile(cfg.Fs(), "ref_tests.pulse", b, 0o644); err != nil {
		t.Fatal(err)
	}
	svc := New(cfg)
	svc.SetShardWorkers(1)
	svc.SetDecodeWorkers(1)
	return svc
}

// weightRefInferRun runs one case's slot under its weight, buffered or
// streamed; ok=false when the request cannot take the streaming arm.
func weightRefInferRun(t *testing.T, svc *Service, c weightRefInferCase, buffered bool) (map[string]any, bool) {
	t.Helper()
	req := &types.Request{
		Cohort: &types.Cohort{Filename: "ref_tests.pulse"},
		Weight: &types.WeightSpec{Field: c.weight, Kind: weightRefKind(c.kind)},
	}
	if err := json.Unmarshal([]byte(c.request), req); err != nil {
		t.Fatalf("%s: request fragment: %v", c.name, err)
	}
	req.Aggregations = append(req.Aggregations, testSteerCount())
	if buffered {
		// AGG_MEDIAN never streams; opted out so it stays unweighted.
		req.Aggregations = append(req.Aggregations, &types.Aggregation{
			Type: types.AGG_MEDIAN, Field: "y", Label: paritySteer + "median", Weight: types.NullSlotWeight()})
	} else if !processing.CanStreamRequest(req, weightRefTestSchema()) {
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

// wirePath resolves a figure path (see the file comment) in a wire
// value.
func wirePath(root any, path string) (any, error) {
	cur := root
	for _, seg := range strings.Split(path, ".") {
		name, sel, indexed := strings.Cut(seg, "[")
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil, errPath(path, seg, "not an object")
		}
		cur, ok = obj[name]
		if !ok {
			return nil, errPath(path, seg, "missing")
		}
		if !indexed {
			continue
		}
		sel = strings.TrimSuffix(sel, "]")
		arr, ok := cur.([]any)
		if !ok {
			return nil, errPath(path, seg, "not an array")
		}
		i, err := strconv.Atoi(sel)
		if err != nil {
			groups, _ := obj["groups"].([]any)
			i = slices.Index(groups, any(sel))
			if i < 0 {
				return nil, errPath(path, seg, "label not in the sibling groups")
			}
		}
		if i >= len(arr) {
			return nil, errPath(path, seg, "index out of range")
		}
		cur = arr[i]
	}
	return cur, nil
}

type pathError string

func (e pathError) Error() string { return string(e) }

func errPath(path, seg, why string) error {
	return pathError(path + ": " + seg + ": " + why)
}

// weightRefFigureTol is a figure's relative tolerance (file comment).
func weightRefFigureTol(path string) float64 {
	last := path[strings.LastIndex(path, ".")+1:]
	switch {
	case last == "p_value", strings.HasPrefix(last, "ci_"):
		return 1e-10
	}
	return 1e-12
}

// testWeightRefTests is TestWeightReferenceValues' test-slot half.
func testWeightRefTests(t *testing.T) {
	t.Run("coverage", assertWeightRefTestCoverage)
	if !strings.Contains(weightRefTestProvenance, "R ") || !strings.Contains(weightRefTestProvenance, "statsmodels") {
		t.Fatalf("weightRefTestProvenance %q does not record the generating tool versions", weightRefTestProvenance)
	}
	svc := weightRefTestService(t)
	streamed := 0
	for _, c := range weightRefTestCases {
		t.Run(c.weight+"_"+c.kind+"/"+c.name, func(t *testing.T) {
			if c.source == "" || len(c.figures) == 0 {
				t.Fatal("case has no provenance source or no figures")
			}
			for _, buffered := range []bool{true, false} {
				arm := "streaming"
				if buffered {
					arm = "buffered"
				}
				wire, ok := weightRefInferRun(t, svc, c, buffered)
				if !ok {
					continue
				}
				if !buffered {
					streamed++
				}
				// The fixture clears every guard: a result warning means
				// it no longer exercises the plain path.
				if ws, _ := wirePath(wire, "tests[0].warnings"); ws != nil {
					t.Errorf("%s: unexpected test warnings %v", arm, ws)
				}
				for _, path := range sortedNames(c.figures) {
					want := c.figures[path]
					got, err := wirePath(wire, path)
					if err != nil {
						t.Errorf("%s: %v", arm, err)
						continue
					}
					g, ok := got.(float64)
					if !ok || !relClose(g, want, weightRefFigureTol(path)) {
						t.Errorf("%s %s = %v, reference %.17g (rel %.3g, tol %g)", arm, path, got, want,
							math.Abs(g-want)/math.Abs(want), weightRefFigureTol(path))
					}
				}
			}
		})
	}
	if streamed == 0 {
		t.Fatal("no test case ran on the streaming arm: the streaming half is vacuous")
	}
}

// weightRefCaseType is the operator a case's request slot names.
func weightRefCaseType(t *testing.T, c weightRefInferCase) string {
	t.Helper()
	var frag struct {
		Tests []struct {
			Type string `json:"type"`
		} `json:"tests"`
	}
	if err := json.Unmarshal([]byte(c.request), &frag); err != nil || len(frag.Tests) != 1 {
		t.Fatalf("%s: request %s does not hold one test slot", c.name, c.request)
	}
	return frag.Tests[0].Type
}

// assertWeightRefTestCoverage: every test the manifest marks weighted
// has a reference case under each kind it advertises, and the
// probability kind under BOTH probability configurations (fractional w
// and the integer twin p = c·f).
func assertWeightRefTestCoverage(t *testing.T) {
	t.Helper()
	have := map[string][]types.WeightKind{}
	configs := map[string]map[string]bool{}
	for _, c := range weightRefTestCases {
		op := weightRefCaseType(t, c)
		k := weightRefKind(c.kind)
		if !slices.Contains(have[op], k) {
			have[op] = append(have[op], k)
		}
		if configs[op] == nil {
			configs[op] = map[string]bool{}
		}
		configs[op][c.weight+"_"+c.kind] = true
	}
	assertWeightKindCoverage(t, weightSurfaceTests, have, "TEST_SPECS in testdata/weight_reference/gen_weight_reference.py, then regenerate")
	for op, kinds := range manifestWeightKinds(weightSurfaceTests) {
		if slices.Contains(kinds, types.WeightKindProbability) && !(configs[op]["w_probability"] && configs[op]["p_probability"]) {
			t.Errorf("%s: probability reference cases %v, want both w_probability and p_probability", op, configs[op])
		}
	}
}

// weightRefTestPointEstimates lists, per case, the figures that are
// POINT ESTIMATES: the same number whatever the weight kind (they read
// only Σw ratios, never N*), so the frequency case on f and the
// probability case on p = c·f must agree on them. Statistics, df,
// p-values and standard-error-based figures differ by design.
var weightRefTestPointEstimates = map[string][]string{
	"t_one_sample": {"tests[0].details.mean"},
	"t_split":      {"tests[0].details.mean[a]", "tests[0].details.mean[b]", "tests[0].details.diff"},
	"welch":        {"tests[0].details.mean[a]", "tests[0].details.mean[b]", "tests[0].details.diff"},
	"z":            {"tests[0].details.mean[a]", "tests[0].details.mean[b]", "tests[0].details.diff"},
	"paired":       {"tests[0].details.mean_diff"},
	"anova_f": {"tests[0].details.group_means[p]", "tests[0].details.group_means[q]", "tests[0].details.group_means[r]",
		"tests[0].details.effect_size.eta_squared"},
	"anova_welch": {"tests[0].details.group_means[p]", "tests[0].details.group_means[q]", "tests[0].details.group_means[r]"},
	"pearson":     {"tests[0].statistic", "tests[0].details.mean_x", "tests[0].details.mean_y"},
	"chisq":       {"tests[0].details.effect_size.cramers_v"},
	"prop_z": {"tests[0].details.proportion[a]", "tests[0].details.proportion[b]", "tests[0].details.diff",
		"tests[0].details.effect_size.cohens_h"},
}

// testWeightRefTestsKindsAgree is TestWeightReferenceKindsAgree's
// test-slot half, on Pulse's own answers.
func testWeightRefTestsKindsAgree(t *testing.T) {
	svc := weightRefTestService(t)
	byKey := map[string]weightRefInferCase{}
	for _, c := range weightRefTestCases {
		byKey[c.weight+"|"+c.kind+"|"+c.name] = c
	}
	compared := 0
	for _, fc := range weightRefTestCases {
		if fc.weight != "f" {
			continue
		}
		paths, ok := weightRefTestPointEstimates[fc.name]
		if !ok || len(paths) == 0 {
			t.Errorf("%s: no weightRefTestPointEstimates entry", fc.name)
			continue
		}
		pc, ok := byKey["p|probability|"+fc.name]
		if !ok {
			t.Errorf("%s: no p_probability twin case", fc.name)
			continue
		}
		t.Run(fc.name, func(t *testing.T) {
			fw, _ := weightRefInferRun(t, svc, fc, true)
			pw, _ := weightRefInferRun(t, svc, pc, true)
			for _, path := range paths {
				fv, ferr := wirePath(fw, path)
				pv, perr := wirePath(pw, path)
				if ferr != nil || perr != nil {
					t.Fatalf("%s: %v / %v", path, ferr, perr)
				}
				f, _ := fv.(float64)
				p, _ := pv.(float64)
				if !relClose(p, f, 1e-12) {
					t.Errorf("%s: probability p = %.17g, frequency f = %.17g", path, p, f)
				}
			}
			// The figures that read N* must differ, or the two kinds
			// collapsed onto one formula.
			fs, _ := wirePath(fw, "tests[0].p_value")
			ps, _ := wirePath(pw, "tests[0].p_value")
			if fs == ps {
				t.Errorf("p_value %v identical across kinds: N* is not kind-dependent", fs)
			}
			compared++
		})
	}
	if compared == 0 {
		t.Fatal("no test compared across kinds")
	}
}
