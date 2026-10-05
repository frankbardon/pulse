package service

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/internal/processing/multiplicity"
	"github.com/frankbardon/pulse/types"
)

// multiplicity_chain_test.go covers the ProcessChain hook (U13, E3-S2):
// every stage corrects its own `request` family — stage 0 inside
// Service.Process, every later stage in runChainStage — and no family
// spans stages.
//
// The v1 chain gate (mergegate.ChainRefusal) refuses Tests and
// PostTests on every stage, a crosstab cannot carry the aggregator a
// stage needs, and the grouped-SERIES request overlays and the
// chain-host kinds are all descriptive — so a chain that passes the
// gate reaches no p-site today. The stage hook is therefore driven
// directly through runChainStage (below the gate) with real tests over
// materialised chain rows, and the whole-chain path is pinned by
// byte-identity.

// chainStageRows is a prior stage's output: 24 rows keyed k ∈ {a, b},
// s shifted by k so the split t-test and the one-sample test both have
// small p-values.
func chainStageRows() []map[string]any {
	rows := make([]map[string]any, 24)
	for i := range rows {
		k := "a"
		shift := 0.0
		if i%2 == 1 {
			k, shift = "b", 0.9
		}
		rows[i] = map[string]any{"k": k, "s": 5 + shift + 0.3*float64(i%5) - 0.1*float64(i%3)}
	}
	return rows
}

// chainStageRequest is a later stage carrying n of three tests over the
// materialised rows, with an optional request-level block.
func chainStageRequest(n int, m *types.Multiplicity) *types.Request {
	all := []*types.Test{
		{Type: types.TEST_T, Field: "s", Params: json.RawMessage(`{"mu":5.1}`), Label: "one"},
		{Type: types.TEST_T, Field: "s", SplitBy: "k", Label: "two"},
		{Type: types.TEST_T, Field: "s", Params: json.RawMessage(`{"mu":5.6}`), Label: "three"},
	}
	return &types.Request{
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "s", Label: "t"}},
		Tests:        all[:n],
		Multiplicity: m,
	}
}

// runStage resolves req on svc (its own block, then the instance
// default) and runs it as a later chain stage.
func runStage(t *testing.T, svc *Service, req *types.Request) *types.Response {
	t.Helper()
	prior := &types.Request{
		Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "k"}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "v", Label: "s"}},
	}
	schema, err := processing.ChainOutputSchema(prior)
	if err != nil {
		t.Fatal(err)
	}
	recs, err := processing.RecordsFromChainRows(chainStageRows(), schema)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := svc.resolveMultiplicity(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := svc.runChainStage(context.Background(), req, plan, schema, recs)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// TestMultiplicityChain_StageHook: a later stage's tests are corrected
// as one `request` family of that stage alone — from the stage's own
// block or from Options.DefaultMultiplicity — against the core applied
// to the uncorrected run's raw p-values; with no block anywhere the
// stage answers byte-identically.
func TestMultiplicityChain_StageHook(t *testing.T) {
	holm := &types.Multiplicity{Method: types.MultiplicityMethodHolm}
	bh := &types.Multiplicity{Method: types.MultiplicityMethodBH}
	cases := []struct {
		name   string
		def    *types.Multiplicity
		block  *types.Multiplicity
		n      int
		method multiplicity.Method // "" = uncorrected
	}{
		{"stage block, three tests", nil, holm, 3, multiplicity.MethodHolm},
		{"stage block, two tests", nil, bh, 2, multiplicity.MethodBH},
		{"instance default reaches the stage", bh, nil, 3, multiplicity.MethodBH},
		{"stage block wins over the default", bh, holm, 3, multiplicity.MethodHolm},
		{"no block anywhere", nil, nil, 3, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			base := runStage(t, New(nil), chainStageRequest(c.n, nil))
			svc := New(nil)
			svc.SetDefaultMultiplicity(c.def)
			got := runStage(t, svc, chainStageRequest(c.n, c.block))
			if c.method == "" {
				a, _ := json.Marshal(base)
				b, _ := json.Marshal(got)
				if !bytes.Equal(a, b) {
					t.Fatalf("uncorrected stage changed:\n%s\n%s", a, b)
				}
				return
			}
			if len(got.Tests) != c.n || len(base.Tests) != c.n {
				t.Fatalf("stage yields %d tests (baseline %d), want %d", len(got.Tests), len(base.Tests), c.n)
			}
			assertRawUntouched(t, got.Tests, snapshotRaw(base.Tests))
			ps := make([]float64, c.n)
			for i, r := range base.Tests {
				ps[i] = r.PValue
			}
			want, err := multiplicity.Adjust(c.method, ps)
			if err != nil {
				t.Fatal(err)
			}
			moved := false
			for i, r := range got.Tests {
				if r.PAdjusted == nil || r.Multiplicity == nil {
					t.Fatalf("stage test %d uncorrected", i)
				}
				if !sameFloat(*r.PAdjusted, want[i]) {
					t.Errorf("stage test %d p_adjusted %v, want %v", i, *r.PAdjusted, want[i])
				}
				if r.Multiplicity.M != c.n || string(r.Multiplicity.Method) != string(c.method) ||
					r.Multiplicity.Family != types.MultiplicityFamilyRequest {
					t.Errorf("stage test %d echo %+v, want %s request m=%d", i, *r.Multiplicity, c.method, c.n)
				}
				moved = moved || !sameFloat(*r.PAdjusted, r.PValue)
			}
			if !moved {
				t.Fatal("no adjusted p differs from its raw one; the fixture proves nothing")
			}
		})
	}
}

// TestMultiplicityChain_NoFamilySpansStages: two stages run one after
// the other on one instance each correct only their own members — a
// family of 2 and a family of 3, never one of 5 — exactly as each
// stage request run alone.
func TestMultiplicityChain_NoFamilySpansStages(t *testing.T) {
	holm := &types.Multiplicity{Method: types.MultiplicityMethodHolm}
	svc := New(nil)
	first := runStage(t, svc, chainStageRequest(2, holm))
	second := runStage(t, svc, chainStageRequest(3, holm))
	for _, c := range []struct {
		name string
		resp *types.Response
		m    int
	}{{"stage 1", first, 2}, {"stage 2", second, 3}} {
		alone := runStage(t, New(nil), chainStageRequest(c.m, holm))
		a, _ := json.Marshal(alone)
		b, _ := json.Marshal(c.resp)
		if !bytes.Equal(a, b) {
			t.Errorf("%s differs from the stage request run alone:\n%s\n%s", c.name, b, a)
		}
		for i, r := range c.resp.Tests {
			if r.Multiplicity == nil || r.Multiplicity.M != c.m {
				t.Errorf("%s test %d echo %+v, want m=%d", c.name, i, r.Multiplicity, c.m)
			}
		}
	}
}

// TestMultiplicityChain_WholeChain: through ProcessChain every stage
// resolves its own block (and the instance default) — and, since no
// stage that passes the v1 chain gate carries a p-site, a chain whose
// stages and instance name a block answers byte-identically to the
// block-free chain, and stage 0 byte-identically to a standalone
// Process of its request.
func TestMultiplicityChain_WholeChain(t *testing.T) {
	_, buffered := overlayFoldService(t)
	cfg := buffered.fs
	ctx := context.Background()
	mk := func(m *types.Multiplicity) *types.ChainRequest {
		return &types.ChainRequest{
			Cohort: &types.Cohort{Filename: "m.pulse"},
			Stages: []*types.ChainStage{
				{Name: "by_gh", Request: &types.Request{
					Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}, {Type: types.GROUP_CATEGORY, Field: "h"}},
					Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "x", Label: "sx"}},
					Multiplicity: m,
				}},
				{Name: "by_g", Request: &types.Request{
					Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}},
					Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "sx", Label: "total"}},
					Multiplicity: m,
				}},
			},
		}
	}
	base, err := New(cfg).ProcessChain(ctx, mk(nil))
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(base)
	holm := &types.Multiplicity{Method: types.MultiplicityMethodHolm}
	for _, c := range []struct {
		name  string
		def   *types.Multiplicity
		block *types.Multiplicity
	}{
		{"stage blocks", nil, holm},
		{"instance default", holm, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			svc := New(cfg)
			svc.SetDefaultMultiplicity(c.def)
			got, err := svc.ProcessChain(ctx, mk(c.block))
			if err != nil {
				t.Fatal(err)
			}
			if b, _ := json.Marshal(got); !bytes.Equal(b, want) {
				t.Errorf("chain changed under a block:\n%s\n%s", b, want)
			}
			stage0 := mk(c.block).Stages[0].Request
			stage0.Cohort = &types.Cohort{Filename: "m.pulse"}
			alone, err := svc.Process(ctx, stage0)
			if err != nil {
				t.Fatal(err)
			}
			a, _ := json.Marshal(alone)
			s0, _ := json.Marshal(got.Stages[0])
			if !bytes.Equal(a, s0) {
				t.Errorf("stage 0 differs from a standalone Process:\n%s\n%s", s0, a)
			}
		})
	}
}
