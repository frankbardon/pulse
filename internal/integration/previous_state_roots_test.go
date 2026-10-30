package integration_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestPreviousState_ProcessChainRefusesAlike: a chain stage carrying
// ATTR_PREVIOUS_STATE is refused PULSE_CHAIN_NOT_MERGEABLE at that
// stage by ProcessChain and PredictChain alike — a chain stage runs
// only the row-local merge rule, and a previous state reads across
// records — whether the stage groups by the output or only carries it,
// with or without an expression filter reading it by state name.
func TestPreviousState_ProcessChainRefusesAlike(t *testing.T) {
	t.Parallel()

	_, p, _ := prevModeBaseline(t)
	reqs := prevModeRequests("panel.pulse")
	for _, name := range []string{"grouped_by_prev", "ungrouped", "expr_filter_grouped"} {
		mk := func() *types.ChainRequest {
			r := reqs[name]()
			r.Cohort = nil
			return &types.ChainRequest{Cohort: &types.Cohort{Filename: "panel.pulse"}, Stages: []*types.ChainStage{{Name: "s0", Request: r}}}
		}
		assertChainParity(t, p, name, mk, errors.PULSE_CHAIN_NOT_MERGEABLE, "stage_index", 0)
	}
}

// TestPreviousState_FacetRootsRefuseAlike: FacetSchema carries no
// attributes, so the previous-state label is no field there — a facet
// of it, or an expression filter reading it, is refused by FacetSchema
// and PredictFacet with one code, message and details.
func TestPreviousState_FacetRootsRefuseAlike(t *testing.T) {
	t.Parallel()

	_, p, _ := prevModeBaseline(t)
	ctx := context.Background()
	for name, mk := range map[string]func() *types.FacetRequest{
		"facet_field": func() *types.FacetRequest {
			return &types.FacetRequest{Cohort: &types.Cohort{Filename: "panel.pulse"}, Fields: []string{"prev"}}
		},
		"expr_filter": func() *types.FacetRequest {
			return &types.FacetRequest{Cohort: &types.Cohort{Filename: "panel.pulse"}, Fields: []string{"plan"},
				Filterers: []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: `prev == "paid"`}}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := p.FacetSchema(ctx, mk())
			if err == nil {
				t.Fatal("facet over the previous-state label ran")
			}
			env, perr := p.PredictFacet(ctx, mk())
			if perr != nil {
				t.Fatal(perr)
			}
			sameEntry(t, env, err)
		})
	}
}

// TestPreviousState_MaterializedCohortFacetsAndSamplesLikeProcess: over
// the cohort materialize writes, FacetSchema, Facet and Sample read the
// previous-state column as Process reads the attribute in-request —
// the same state names with the same counts (FacetSchema: Process's
// GROUP_CATEGORY counts; null count = Process's AGG_NULL_COUNT; Facet:
// the observed names; Sample: names, never dictionary codes).
func TestPreviousState_MaterializedCohortFacetsAndSamplesLikeProcess(t *testing.T) {
	t.Parallel()

	_, p, _ := prevModeBaseline(t)
	ctx := context.Background()
	reqs := prevModeRequests("panel.pulse")
	if _, err := p.Materialize(ctx, &types.Request{Cohort: &types.Cohort{Filename: "panel.pulse"}, Attributes: prevStateAttr()},
		"derived.pulse", pulse.MaterializeOptions{}); err != nil {
		t.Fatal(err)
	}

	// In-request: count per previous state, and the null count.
	want := map[string]int64{}
	grouped, err := p.Process(ctx, reqs["grouped_by_prev"]())
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range grouped.Data {
		want[row["prev"].(string)] = int64(row["n"].(float64))
	}
	ungrouped, err := p.Process(ctx, reqs["ungrouped"]())
	if err != nil {
		t.Fatal(err)
	}
	wantNull := int64(ungrouped.Data[0]["null_prev"].(float64))
	if len(want) == 0 || wantNull == 0 {
		t.Fatalf("baseline counts %v nulls %d: the panel should carry both", want, wantNull)
	}

	fr, err := p.FacetSchema(ctx, &types.FacetRequest{Cohort: &types.Cohort{Filename: "derived.pulse"}, Fields: []string{"prev"}})
	if err != nil {
		t.Fatal(err)
	}
	f := fr.Fields["prev"]
	if f == nil || f.Discrete == nil {
		t.Fatalf("facet prev %+v, want a discrete summary", f)
	}
	got := map[string]int64{}
	for _, v := range f.Discrete.Values {
		got[v.Value] = v.Count
	}
	if a, b := flowsJSON(t, got), flowsJSON(t, want); a != b {
		t.Errorf("facet counts %s, process counts %s", a, b)
	}
	if f.NullCount != wantNull {
		t.Errorf("facet null count %d, process null count %d", f.NullCount, wantNull)
	}

	values, err := p.Facet(ctx, "derived.pulse", "prev")
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range values {
		if _, ok := want[v]; !ok {
			t.Errorf("facet value %q is no observed previous state (%v)", v, want)
		}
	}

	rows, err := p.Sample(ctx, "derived.pulse", 1000)
	if err != nil {
		t.Fatal(err)
	}
	tally, nulls := map[string]int64{}, int64(0)
	for _, r := range rows {
		switch v := r["prev"].(type) {
		case nil:
			nulls++
		case string:
			tally[v]++
		default:
			raw, _ := json.Marshal(r)
			t.Fatalf("sampled prev %T %v, want a state name or null: %s", v, v, raw)
		}
	}
	if a, b := flowsJSON(t, tally), flowsJSON(t, want); a != b || nulls != wantNull {
		t.Errorf("sample tally %s (%d null), process %s (%d null)", a, nulls, b, wantNull)
	}
}
