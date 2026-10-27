package service

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/internal/processing/multiplicity"
	"github.com/frankbardon/pulse/types"
)

// matrix_multiplicity_test.go is the matrix half of the multiplicity
// fold end to end (U28 E3-S4): a MatrixSpec `multiplicity` block adds
// auxiliary.p_adjusted / significant_adjusted and the MatrixResult echo
// over the off-diagonal UPPER triangle (m = k(k - 1) / 2), against R
// p.adjust (matrix_multiplicity.json); the `request` family pooled
// across buckets, specs and tests; null cells excluded; the upper
// encoding; Compose slots and chain stage 0; the `return` veto; and the
// multiplicity-free byte identity.

// matrixMultOracle is one matrix_multiplicity.json case.
type matrixMultOracle struct {
	Name    string                   `json:"name"`
	Fixture string                   `json:"fixture"`
	Family  types.MultiplicityFamily `json:"family"`
	Group   *string                  `json:"group"`
	Specs   []struct {
		Type    types.MatrixType `json:"type"`
		Method  string           `json:"method"`
		Members []string         `json:"members"`
		Weight  string           `json:"weight"`
	} `json:"specs"`
	Results []struct {
		Spec   int          `json:"spec"`
		Bucket *float64     `json:"bucket"`
		M      int          `json:"m"`
		P      [][]*float64 `json:"p"`
	} `json:"results"`
	Adjusted map[string][][][]*float64 `json:"adjusted"`
}

// oracleRequest builds the case's request under method: every spec
// carries its own block (no params.inference — the block implies it).
func (c matrixMultOracle) request(dir string, method types.MultiplicityMethod) *types.Request {
	req := &types.Request{Cohort: &types.Cohort{Filename: filepath.Join(dir, c.Fixture+".pulse")}}
	for i, s := range c.Specs {
		params := map[string]any{}
		if s.Method != "pearson" && s.Type == types.MAT_CORRELATION {
			params["method"] = s.Method
		}
		spec := inferenceSpec(s.Type, fmt.Sprintf("m%d", i), s.Members, params, s.Weight)
		if len(params) == 0 {
			spec.Params = nil
		}
		spec.Multiplicity = &types.Multiplicity{Method: method, Family: c.Family}
		req.Matrices = append(req.Matrices, spec)
	}
	if c.Group != nil {
		req.Groups = []*types.Group{{Type: types.GROUP_RANGE, Field: *c.Group, Interval: 1}}
		req.Aggregations = []*types.Aggregation{{Type: types.AGG_COUNT, Field: *c.Group, Label: "n"}}
	}
	return req
}

// oracleBucket is the GROUP_RANGE interval-1 label of an integer value.
func oracleBucket(v float64) string {
	return fmt.Sprintf("%g-%g", v, v+1)
}

// TestMatrixMultiplicity_MatchesROracle pins every matrix_multiplicity.json
// case under bonferroni, holm, bh and by: the raw auxiliary.p is
// cor.test's / ppcor's, auxiliary.p_adjusted is p.adjust over the
// distinct pairs (per result for `matrix`, pooled over buckets and
// specs for `request`), mirrored with a null diagonal;
// significant_adjusted is p_adjusted < 0.05; the echo carries the
// family size.
func TestMatrixMultiplicity_MatchesROracle(t *testing.T) {
	t.Parallel()

	var golden struct {
		Cases []matrixMultOracle `json:"cases"`
	}
	dir := loadMV(t, "matrix_multiplicity.json", &golden)
	cfg := fixtureFS(t, dir)
	if len(golden.Cases) < 8 {
		t.Fatalf("oracle has %d cases", len(golden.Cases))
	}
	methods := map[string]types.MultiplicityMethod{"bonferroni": types.MultiplicityMethodBonferroni,
		"holm": types.MultiplicityMethodHolm, "bh": types.MultiplicityMethodBH, "by": types.MultiplicityMethodBY}
	for _, c := range golden.Cases {
		for key, method := range methods {
			t.Run(c.Name+"/"+key, func(t *testing.T) {
				resp := processMatricesN(t, cfg, c.request(dir, method), len(c.Results))
				for i, want := range c.Results {
					res := findOracleResult(t, resp, fmt.Sprintf("m%d", want.Spec), want.Bucket)
					checkMatrixCells(t, "auxiliary.p", res.Auxiliary["p"].Values, want.P, true)
					adj := res.Auxiliary["p_adjusted"]
					if adj == nil {
						t.Fatalf("auxiliary.p_adjusted missing (keys %v)", auxKeys(*res))
					}
					checkMatrixCells(t, "auxiliary.p_adjusted", adj.Values, c.Adjusted[key][i], true)
					sig := res.Auxiliary["significant_adjusted"]
					for r := range adj.Values {
						for col, v := range adj.Values[r] {
							s := sig.Values[r][col]
							switch {
							case math.IsNaN(v):
								if !math.IsNaN(s) {
									t.Errorf("significant_adjusted [%d][%d] = %v beside a null p_adjusted", r, col, s)
								}
							case (s == 1) != (v < 0.05) || (s != 0 && s != 1):
								t.Errorf("significant_adjusted [%d][%d] = %v for p_adjusted %v", r, col, s, v)
							}
						}
					}
					wantEcho := &types.AppliedMultiplicity{Method: method, Family: c.Family, Alpha: 0.05, M: want.M}
					if !reflect.DeepEqual(res.Multiplicity, wantEcho) {
						t.Errorf("echo = %+v, want %+v", res.Multiplicity, wantEcho)
					}
				}
			})
		}
	}
}

func processMatricesN(t *testing.T, cfg *fs.Config, req *types.Request, n int) *types.Response {
	t.Helper()
	resp, err := New(cfg).Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(resp.Matrices) != n {
		t.Fatalf("Process: %d matrices, want %d", len(resp.Matrices), n)
	}
	return resp
}

func findOracleResult(t *testing.T, resp *types.Response, name string, bucket *float64) *types.MatrixResult {
	t.Helper()
	for i := range resp.Matrices {
		m := &resp.Matrices[i]
		if m.Name != name {
			continue
		}
		if bucket == nil {
			return m
		}
		if len(m.GroupKey) == 1 && fmt.Sprint(m.GroupKey[0]) == oracleBucket(*bucket) {
			return m
		}
	}
	keys := []string{}
	for _, m := range resp.Matrices {
		keys = append(keys, m.Name+fmt.Sprint(m.GroupKey))
	}
	t.Fatalf("no result %s bucket %v among %v", name, bucket, keys)
	return nil
}

// TestMatrixMultiplicity_FamilySizeIsUpperTriangle: the falsifier for
// the family: a 5-member matrix files m = 10 hypotheses, not 25 (the
// full square) nor 20 (both triangles) — Bonferroni multiplies by
// exactly 10 — and the adjusted figure is mirrored.
func TestMatrixMultiplicity_FamilySizeIsUpperTriangle(t *testing.T) {
	t.Parallel()

	cfg := writeMatrixCohort(t, "m.pulse", matrixRows(60))
	spec := missingSpec(types.MAT_CORRELATION, []string{"x1", "x2", "x3"}, "", "")
	spec.Multiplicity = &types.Multiplicity{Method: types.MultiplicityMethodBonferroni}
	res := processMatrices(t, cfg, &types.Request{Cohort: &types.Cohort{Filename: "m.pulse"}, Matrices: []types.MatrixSpec{spec}}).Matrices[0]
	if res.Multiplicity == nil || res.Multiplicity.M != 3 || res.Multiplicity.Family != types.MultiplicityFamilyMatrix {
		t.Fatalf("echo = %+v, want m = 3 (3 members), family matrix", res.Multiplicity)
	}
	p, adj := res.Auxiliary["p"].Values, res.Auxiliary["p_adjusted"].Values
	for r := 0; r < 3; r++ {
		for c := 0; c < 3; c++ {
			if r == c {
				if !math.IsNaN(adj[r][c]) {
					t.Errorf("diagonal [%d][%d] = %v, want null", r, c, adj[r][c])
				}
				continue
			}
			if want := math.Min(1, 3*p[r][c]); adj[r][c] != want {
				t.Errorf("[%d][%d] = %v, want min(1, 3 x %v) = %v", r, c, adj[r][c], p[r][c], want)
			}
		}
	}
}

// TestMatrixMultiplicity_UpperEncoding: under `upper` the twins are
// upper-shaped (row r holds k - r cells) and carry exactly the full
// encoding's upper triangle.
func TestMatrixMultiplicity_UpperEncoding(t *testing.T) {
	t.Parallel()

	cfg := writeMatrixCohort(t, "m.pulse", matrixRows(60))
	run := func(enc types.MatrixEncoding) types.MatrixResult {
		spec := missingSpec(types.MAT_CORRELATION, []string{"x1", "x2", "x3"}, "", `{"method": "spearman"}`)
		spec.Encoding = enc
		spec.Multiplicity = &types.Multiplicity{Method: types.MultiplicityMethodHolm}
		return processMatrices(t, cfg, &types.Request{Cohort: &types.Cohort{Filename: "m.pulse"}, Matrices: []types.MatrixSpec{spec}}).Matrices[0]
	}
	full, upper := run(types.MatrixEncodingFull), run(types.MatrixEncodingUpper)
	for _, key := range []string{"p_adjusted", "significant_adjusted"} {
		u := upper.Auxiliary[key]
		if u.Encoding != types.MatrixEncodingUpper || u.Kind != types.MatrixKindSquareSymmetric {
			t.Fatalf("%s shape = %s %s", key, u.Kind, u.Encoding)
		}
		for r := range u.Values {
			if len(u.Values[r]) != 3-r {
				t.Fatalf("%s row %d has %d cells, want %d", key, r, len(u.Values[r]), 3-r)
			}
			for k, v := range u.Values[r] {
				f := full.Auxiliary[key].Values[r][r+k]
				if !(v == f || math.IsNaN(v) && math.IsNaN(f)) {
					t.Errorf("%s (%d, %d) upper %v != full %v", key, r, r+k, v, f)
				}
			}
		}
	}
	if !reflect.DeepEqual(full.Multiplicity, upper.Multiplicity) {
		t.Errorf("echo differs: full %+v, upper %+v", full.Multiplicity, upper.Multiplicity)
	}
}

// TestMatrixMultiplicity_NullCellsExcluded: a pair too thin for a p
// (2 rows) is excluded from m, counted in the echo's excluded and left
// null in p_adjusted / significant_adjusted.
func TestMatrixMultiplicity_NullCellsExcluded(t *testing.T) {
	t.Parallel()

	nan := math.NaN()
	rows := [][5]float64{{1, 2, 4, 1, 1}, {2, 1, 5, 1, 1}, {3, nan, 3, 1, 1}, {4, nan, 9, 1, 1}, {5, nan, 1, 1, 1}, {6, nan, 7, 1, 1}}
	spec := missingSpec(types.MAT_CORRELATION, []string{"x1", "x2", "x3"}, "", `{"missing": "pairwise"}`)
	spec.Multiplicity = &types.Multiplicity{Method: types.MultiplicityMethodBH}
	res := runOneMatrix(t, rows, spec)
	p, adj, sig := res.Auxiliary["p"].Values, res.Auxiliary["p_adjusted"].Values, res.Auxiliary["significant_adjusted"].Values
	if !math.IsNaN(p[0][1]) || !math.IsNaN(p[1][2]) || math.IsNaN(p[0][2]) {
		t.Fatalf("fixture: p = %v, want (x1, x2) and (x2, x3) null, (x1, x3) defined", p)
	}
	want := &types.AppliedMultiplicity{Method: types.MultiplicityMethodBH, Family: types.MultiplicityFamilyMatrix, Alpha: 0.05, M: 1, Excluded: 2}
	if !reflect.DeepEqual(res.Multiplicity, want) {
		t.Errorf("echo = %+v, want %+v", res.Multiplicity, want)
	}
	for _, c := range [][2]int{{0, 1}, {1, 0}, {1, 2}, {2, 1}} {
		if !math.IsNaN(adj[c[0]][c[1]]) || !math.IsNaN(sig[c[0]][c[1]]) {
			t.Errorf("excluded cell %v: p_adjusted %v, significant %v; want null", c, adj[c[0]][c[1]], sig[c[0]][c[1]])
		}
	}
	if adj[0][2] != p[0][2] || adj[2][0] != p[0][2] {
		t.Errorf("m = 1: p_adjusted (x1, x3) = %v / %v, want the raw %v", adj[0][2], adj[2][0], p[0][2])
	}
}

// TestMatrixMultiplicity_RequestFamilyPoolsWithTests: a `request`
// family spec pools with the request's tests (and across its buckets
// when grouped) — every member equals multiplicity.Adjust over the
// hand-assembled family, tests first, then the matrix's upper
// triangles bucket by bucket; the `matrix` family on the same request
// leaves the tests' family alone.
func TestMatrixMultiplicity_RequestFamilyPoolsWithTests(t *testing.T) {
	t.Parallel()

	cfg := writeMatrixCohort(t, "m.pulse", matrixRows(80))
	for _, grouped := range []bool{false, true} {
		t.Run(fmt.Sprintf("grouped=%v", grouped), func(t *testing.T) {
			spec := missingSpec(types.MAT_CORRELATION, []string{"x1", "x2", "x3"}, "", "")
			spec.Multiplicity = &types.Multiplicity{Method: types.MultiplicityMethodHolm, Family: types.MultiplicityFamilyRequest}
			req := &types.Request{
				Cohort:   &types.Cohort{Filename: "m.pulse"},
				Tests:    []*types.Test{{Type: types.TEST_PEARSON_R, Field: "x1", Field2: "x3", Multiplicity: &types.Multiplicity{Method: types.MultiplicityMethodHolm}}},
				Matrices: []types.MatrixSpec{spec},
			}
			if grouped {
				req.Groups = []*types.Group{{Type: types.GROUP_RANGE, Field: "f", Interval: 2}}
				req.Aggregations = []*types.Aggregation{{Type: types.AGG_COUNT, Field: "x1", Label: "n"}}
			}
			resp, err := New(cfg).Process(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			if grouped && len(resp.Matrices) < 2 {
				t.Fatalf("fixture: %d buckets, want ≥ 2", len(resp.Matrices))
			}
			var ps []float64
			for _, tr := range resp.Tests {
				ps = append(ps, tr.PValue)
			}
			for _, m := range resp.Matrices {
				for _, c := range matrixPCells(m.Auxiliary["p"]) {
					ps = append(ps, c.p)
				}
			}
			want, _ := multiplicity.Adjust(multiplicity.Method(types.MultiplicityMethodHolm), ps)
			at := 0
			for _, tr := range resp.Tests {
				if tr.PAdjusted == nil || *tr.PAdjusted != want[at] || tr.Multiplicity.M != len(ps) {
					t.Errorf("test p_adjusted = %v (m %+v), want %v (m %d)", tr.PAdjusted, tr.Multiplicity, want[at], len(ps))
				}
				at++
			}
			for _, m := range resp.Matrices {
				for _, c := range matrixPCells(m.Auxiliary["p"]) {
					if got := m.Auxiliary["p_adjusted"].Values[c.r][c.c]; got != want[at] {
						t.Errorf("%v (%d, %d) p_adjusted = %v, want %v", m.GroupKey, c.r, c.c, got, want[at])
					}
					at++
				}
				if m.Multiplicity == nil || m.Multiplicity.M != len(ps) || m.Multiplicity.Family != types.MultiplicityFamilyRequest {
					t.Errorf("%v echo = %+v, want request family m = %d", m.GroupKey, m.Multiplicity, len(ps))
				}
			}

			// The `matrix` family: the tests correct alone (one per
			// group when grouped), each bucket on its own three pairs.
			req.Matrices[0].Multiplicity = &types.Multiplicity{Method: types.MultiplicityMethodHolm}
			resp, err = New(cfg).Process(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			if m := resp.Tests[0].Multiplicity.M; m != len(resp.Tests) {
				t.Errorf("matrix family leaked into the tests' family: m = %d", m)
			}
			for _, m := range resp.Matrices {
				if m.Multiplicity.M != 3 {
					t.Errorf("%v: matrix family m = %d, want 3 per bucket", m.GroupKey, m.Multiplicity.M)
				}
			}
		})
	}
}

// TestMatrixMultiplicity_RawPNeverMoves: a block adds exactly the two
// twins and the echo — auxiliary.p, the intervals, the primary and the
// rest of the result keep their bytes against params.inference alone.
func TestMatrixMultiplicity_RawPNeverMoves(t *testing.T) {
	t.Parallel()

	cfg := writeMatrixCohort(t, "m.pulse", matrixRows(60))
	run := func(params string, m *types.Multiplicity) types.MatrixResult {
		spec := missingSpec(types.MAT_PARTIAL_CORRELATION, []string{"x1", "x2", "x3"}, "", params)
		spec.Multiplicity = m
		return processMatrices(t, cfg, &types.Request{Cohort: &types.Cohort{Filename: "m.pulse"}, Matrices: []types.MatrixSpec{spec}}).Matrices[0]
	}
	plain := run(`{"inference": true}`, nil)
	corrected := run("", &types.Multiplicity{Method: types.MultiplicityMethodBY})
	if corrected.Multiplicity == nil || corrected.Auxiliary["p_adjusted"] == nil || corrected.Auxiliary["significant_adjusted"] == nil {
		t.Fatalf("no correction: %+v", corrected.Multiplicity)
	}
	delete(corrected.Auxiliary, "p_adjusted")
	delete(corrected.Auxiliary, "significant_adjusted")
	corrected.Multiplicity = nil
	a, _ := json.Marshal(plain)
	b, _ := json.Marshal(corrected)
	if string(a) != string(b) {
		t.Errorf("a block moved more than its twins:\n%s\n%s", a, b)
	}
}

// TestMatrixMultiplicity_AbsentIsByteIdentical: no block reaching an
// inferential spec — none at all, an inherited block on a spec without
// params.inference, an own `none` beside params.inference, a block
// whose method resolves to none — leaves the response byte-identical
// to the multiplicity-free run, and a spec without a block marshals
// without the key.
func TestMatrixMultiplicity_AbsentIsByteIdentical(t *testing.T) {
	t.Parallel()

	cfg := writeMatrixCohort(t, "m.pulse", matrixRows(60))
	marshal := func(t *testing.T, params string, own, reqBlock *types.Multiplicity) string {
		t.Helper()
		spec := missingSpec(types.MAT_CORRELATION, []string{"x1", "x2", "x3"}, "", params)
		spec.Multiplicity = own
		resp, err := New(cfg).Process(context.Background(), &types.Request{Cohort: &types.Cohort{Filename: "m.pulse"},
			Matrices: []types.MatrixSpec{spec}, Multiplicity: reqBlock})
		if err != nil {
			t.Fatal(err)
		}
		b, err := json.Marshal(resp.Matrices)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	holm := &types.Multiplicity{Method: types.MultiplicityMethodHolm}
	none := &types.Multiplicity{Method: types.MultiplicityMethodNone}
	off := marshal(t, "", nil, nil)
	if got := marshal(t, "", nil, holm); got != off {
		t.Errorf("an inherited block reached a spec without params.inference:\n%s\n%s", got, off)
	}
	on := marshal(t, `{"inference": true}`, nil, nil)
	for name, got := range map[string]string{
		"own none":       marshal(t, "", none, nil),
		"request none":   marshal(t, `{"inference": true}`, nil, none),
		"own none + req": marshal(t, `{"inference": true}`, none, holm),
	} {
		if got != on {
			t.Errorf("%s moved the response:\n%s\n%s", name, got, on)
		}
	}
	if strings.Contains(off, "multiplicity") || strings.Contains(on, "p_adjusted") {
		t.Errorf("multiplicity reached a multiplicity-free response: %s", on)
	}
	b, _ := json.Marshal(missingSpec(types.MAT_CORRELATION, []string{"x1"}, "", ""))
	if strings.Contains(string(b), "multiplicity") {
		t.Errorf("an absent block reached the wire: %s", b)
	}
}

// TestMatrixMultiplicity_ImpliedInferenceRefusals: an explicit
// params.inference false beside a block is refused (bad_params); alpha
// is accepted under the implied inference.
func TestMatrixMultiplicity_ImpliedInferenceRefusals(t *testing.T) {
	t.Parallel()

	cfg := writeMatrixCohort(t, "m.pulse", matrixRows(30))
	for _, c := range []struct {
		params string
		ok     bool
	}{
		{`{"inference": false}`, false},
		{`{"alpha": 0.1}`, true},
		{`{"inference": true, "alpha": 0.1}`, true},
	} {
		spec := missingSpec(types.MAT_CORRELATION, []string{"x1", "x2", "x3"}, "", c.params)
		spec.Multiplicity = &types.Multiplicity{Method: types.MultiplicityMethodHolm}
		_, err := New(cfg).Process(context.Background(), &types.Request{Cohort: &types.Cohort{Filename: "m.pulse"}, Matrices: []types.MatrixSpec{spec}})
		if (err == nil) != c.ok {
			t.Errorf("params %s: err = %v, want ok %v", c.params, err, c.ok)
		}
		if err != nil && !strings.Contains(err.Error(), "params.inference is false") {
			t.Errorf("params %s: refusal %v", c.params, err)
		}
	}
}

// TestMatrixMultiplicity_ReturnVetoKeepsFamily: a `return` that drops
// the matrices (or their auxiliaries) still computes a member spec's
// p-values, so a pooled test's p_adjusted and m equal the full run's.
func TestMatrixMultiplicity_ReturnVetoKeepsFamily(t *testing.T) {
	t.Parallel()

	cfg := writeMatrixCohort(t, "m.pulse", matrixRows(80))
	run := func(ret *types.Return) *types.TestResult {
		spec := missingSpec(types.MAT_CORRELATION, []string{"x1", "x2", "x3"}, "", "")
		spec.Multiplicity = &types.Multiplicity{Method: types.MultiplicityMethodBH, Family: types.MultiplicityFamilyRequest}
		resp, err := New(cfg).Process(context.Background(), &types.Request{
			Cohort:   &types.Cohort{Filename: "m.pulse"},
			Tests:    []*types.Test{{Type: types.TEST_PEARSON_R, Field: "x1", Field2: "x3", Multiplicity: &types.Multiplicity{Method: types.MultiplicityMethodBH}}},
			Matrices: []types.MatrixSpec{spec},
			Return:   ret,
		})
		if err != nil {
			t.Fatal(err)
		}
		return resp.Tests[0]
	}
	full := run(nil)
	if full.Multiplicity.M != 4 {
		t.Fatalf("full run m = %d, want 4 (1 test + 3 pairs)", full.Multiplicity.M)
	}
	for _, ret := range []*types.Return{
		{Exclude: []string{"matrices[*].auxiliary"}},
		{Exclude: []string{"matrices"}},
		{Preset: types.ReturnPresetStandard},
	} {
		got := run(ret)
		if got.Multiplicity.M != full.Multiplicity.M || *got.PAdjusted != *full.PAdjusted {
			t.Errorf("return %+v: test m = %d p_adjusted %v, want %d %v", ret, got.Multiplicity.M, *got.PAdjusted, full.Multiplicity.M, *full.PAdjusted)
		}
	}
}

// TestMatrixMultiplicity_ComposeAndChain: a Compose slot (serial and
// parallel) folds its matrices in the slot's own families, equal to the
// slot run alone; a ProcessChain stage 0 carrying a corrected matrix
// equals the plain Process.
func TestMatrixMultiplicity_ComposeAndChain(t *testing.T) {
	t.Parallel()

	cfg := writeMatrixCohort(t, "m.pulse", matrixRows(80))
	slot := func() *types.Request {
		spec := missingSpec(types.MAT_CORRELATION, []string{"x1", "x2", "x3"}, "", `{"method": "kendall"}`)
		spec.Multiplicity = &types.Multiplicity{Method: types.MultiplicityMethodHolm, Family: types.MultiplicityFamilyRequest}
		return &types.Request{Cohort: &types.Cohort{Filename: "m.pulse"},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "x3", Label: "total"}},
			Matrices:     []types.MatrixSpec{spec}}
	}
	alone, err := New(cfg).Process(context.Background(), slot())
	if err != nil {
		t.Fatal(err)
	}
	if alone.Matrices[0].Multiplicity == nil {
		t.Fatal("standalone run carries no correction")
	}
	want, _ := json.Marshal(alone.Matrices)
	check := func(t *testing.T, name string, got []types.MatrixResult) {
		t.Helper()
		b, _ := json.Marshal(got)
		if string(b) != string(want) {
			t.Errorf("%s matrices differ from the standalone run:\n%s\n%s", name, b, want)
		}
	}
	composed := func() *types.ComposedRequest {
		return &types.ComposedRequest{Requests: []*types.Request{slot(), slot()},
			Multiplicity: &types.Multiplicity{Family: types.MultiplicityFamilyCompose}}
	}
	resp, err := New(cfg).Compose(context.Background(), composed())
	if err != nil {
		t.Fatal(err)
	}
	for i, r := range resp.Responses {
		check(t, fmt.Sprintf("compose slot %d", i), r.Matrices)
	}
	resp, err = New(cfg).ComposeParallel(context.Background(), composed(), ComposeOptions{MaxWorkers: 2})
	if err != nil {
		t.Fatal(err)
	}
	for i, r := range resp.Responses {
		check(t, fmt.Sprintf("compose parallel slot %d", i), r.Matrices)
	}
	s0 := slot()
	s0.Cohort = nil
	chain, err := New(cfg).ProcessChain(context.Background(), &types.ChainRequest{
		Cohort: &types.Cohort{Filename: "m.pulse"},
		Stages: []*types.ChainStage{
			{Name: "s0", Request: s0},
			{Name: "s1", Request: &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "total", Label: "grand"}}}},
		},
	})
	if err != nil {
		t.Fatalf("ProcessChain: %v", err)
	}
	check(t, "chain stage 0", chain.Stages[0].Matrices)
}
