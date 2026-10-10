package pulse_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// rankHostCohort imports a small tie-heavy cohort: cat (a / b / c) and
// numeric x, y, z.
func rankHostCohort(t *testing.T) (*pulse.Pulse, string) {
	t.Helper()
	fs := afero.NewMemMapFs()
	body := "cat,x,y,z\n"
	for i := range 90 {
		body += fmt.Sprintf("%s,%d,%d,%d\n", []string{"a", "b", "c"}[i%3], (i*7)%5, (i*i)%6, (i*13)%17)
	}
	if err := afero.WriteFile(fs, "r.csv", []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	res, err := p.ImportFile(context.Background(), pulse.ImportSpec{SourcePath: "r.csv"})
	if err != nil {
		t.Fatalf("ImportFile: %v", err)
	}
	return p, res.Path
}

func rankHostSpecs() []types.MatrixSpec {
	return []types.MatrixSpec{
		{Name: "rho", Type: types.MAT_CORRELATION, Fields: []string{"x", "y", "z"}, Params: json.RawMessage(`{"method": "spearman"}`)},
		{Name: "tau", Type: types.MAT_CORRELATION, Fields: []string{"x", "y", "z"}, Params: json.RawMessage(`{"method": "kendall", "missing": "pairwise"}`)},
	}
}

// TestMatrixRankCorrelation_Hosts: a rank-method matrix follows the
// grouped buckets (each bucket's matrix equals the ungrouped run over
// exactly its rows), and Compose (serial and parallel) slots and a
// ProcessChain stage 0 carry the same results as the plain Process —
// a non-mergeable spec runs serially wherever it lands.
func TestMatrixRankCorrelation_Hosts(t *testing.T) {
	p, cohort := rankHostCohort(t)
	ctx := context.Background()
	grouped := func() *types.Request {
		return &types.Request{
			Cohort:       &types.Cohort{Filename: cohort},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "x", Label: "rows"}},
			Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}},
			Matrices:     rankHostSpecs(),
		}
	}
	alone, err := p.Process(ctx, grouped())
	if err != nil {
		t.Fatal(err)
	}
	if len(alone.Matrices) != 6 {
		t.Fatalf("%d matrices, want 2 specs × 3 buckets", len(alone.Matrices))
	}
	for _, m := range alone.Matrices {
		key := fmt.Sprint(m.GroupKey[0])
		filtered, err := p.Process(ctx, &types.Request{
			Cohort:    &types.Cohort{Filename: cohort},
			Filterers: []*types.Filterer{{Type: types.FILTER_INCLUDE, Field: "cat", Values: []string{key}}},
			Matrices:  rankHostSpecs(),
		})
		if err != nil {
			t.Fatal(err)
		}
		var want types.MatrixResult
		for _, f := range filtered.Matrices {
			if f.Name == m.Name {
				want = f
			}
		}
		want.GroupKey, want.GroupHeader = m.GroupKey, m.GroupHeader
		if !matricesEqual([]types.MatrixResult{m}, []types.MatrixResult{want}) {
			t.Errorf("bucket %s %s = %+v, want the filtered run %+v", key, m.Name, m.Primary, want.Primary)
		}
	}

	composed := &types.ComposedRequest{Requests: []*types.Request{grouped(), grouped()}}
	for _, parallel := range []bool{false, true} {
		var resp *types.ComposedResponse
		if parallel {
			resp, err = p.ComposeParallel(ctx, composed, pulse.ComposeOptions{MaxWorkers: 2})
		} else {
			resp, err = p.Compose(ctx, composed)
		}
		if err != nil {
			t.Fatalf("compose (parallel %v): %v", parallel, err)
		}
		for i, r := range resp.Responses {
			if !matricesEqual(r.Matrices, alone.Matrices) {
				t.Errorf("compose (parallel %v) slot %d matrices differ from Process", parallel, i)
			}
		}
	}

	s0 := grouped()
	s0.Cohort = nil
	chain, err := p.ProcessChain(ctx, &types.ChainRequest{
		Cohort: &types.Cohort{Filename: cohort},
		Stages: []*types.ChainStage{
			{Name: "s0", Request: s0},
			{Name: "s1", Request: &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "rows", Label: "all"}}}},
		},
	})
	if err != nil {
		t.Fatalf("ProcessChain: %v", err)
	}
	if !matricesEqual(chain.Stages[0].Matrices, alone.Matrices) {
		t.Errorf("chain stage 0 matrices differ from Process")
	}
}
