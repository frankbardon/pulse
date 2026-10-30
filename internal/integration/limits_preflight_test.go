package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/mcp"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// limitsCohort imports a 3-numeric-column cohort (a, b, c) plus a
// categorical cat and returns the fs and the cohort path.
func limitsCohort(t *testing.T) (afero.Fs, string) {
	t.Helper()
	fs := afero.NewMemMapFs()
	body := "a,b,c,cat\n"
	for i := range 30 {
		body += fmt.Sprintf("%d,%d,%d,%s\n", i, i*i%7, 30-i, []string{"x", "y", "z"}[i%3])
	}
	if err := afero.WriteFile(fs, "l.csv", []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	res, err := p.ImportFile(context.Background(), pulse.ImportSpec{SourcePath: "l.csv"})
	if err != nil {
		t.Fatalf("ImportFile: %v", err)
	}
	return fs, res.Path
}

func limitsPulse(t *testing.T, fs afero.Fs, l pulse.Limits) *pulse.Pulse {
	t.Helper()
	p, err := pulse.New(pulse.Options{FS: fs, Limits: l})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// matrix3 is a request carrying one 3-member covariance matrix.
func matrix3(cohort string) *types.Request {
	return &types.Request{
		Cohort:       &types.Cohort{Filename: cohort},
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "a", Label: "s"}},
		Matrices:     []types.MatrixSpec{{Type: types.MAT_COVARIANCE, Name: "cov", Fields: []string{"a", "b", "c"}}},
	}
}

// requireLimitExceeded asserts err is PULSE_LIMIT_EXCEEDED naming
// limit, configured and the option, and returns its details.
func requireLimitExceeded(t *testing.T, err error, limit string, configured int64, option string) map[string]any {
	t.Helper()
	ce := requireCode(t, err, errors.PULSE_LIMIT_EXCEEDED)
	d := ce.Details
	if d["limit"] != limit || d["configured"] != configured || d["option"] != option {
		t.Fatalf("details = %v, want limit=%s configured=%d option=%s", d, limit, configured, option)
	}
	return d
}

// TestLimits_MatrixDim_PredictCertainAndProcessRefuses: a 3-member
// matrix under MaxMatrixDim=2 is a certain predict finding (Valid
// false, on Predict, PredictBytes and MCP pulse_predict) and the runtime
// refuses it with the error predict reports — code, message and
// details.
func TestLimits_MatrixDim_PredictCertainAndProcessRefuses(t *testing.T) {
	t.Parallel()

	fs, cohort := limitsCohort(t)
	ctx := context.Background()
	p := limitsPulse(t, fs, pulse.Limits{MaxMatrixDim: 2})
	want := []descriptor.LimitFinding{{Limit: "max_matrix_dim", Configured: 2, Estimated: 3, Grade: descriptor.LimitGradeCertain}}

	res, err := p.Predict(ctx, matrix3(cohort))
	if err != nil {
		t.Fatal(err)
	}
	if res.Valid {
		t.Fatal("Predict: a certain finding must leave Valid false")
	}
	if !reflect.DeepEqual(res.LimitFindings, want) {
		t.Fatalf("Predict LimitFindings = %+v, want %+v", res.LimitFindings, want)
	}

	out, err := mcp.HandlePredict(ctx, p, mcp.PredictIn{Request: *matrix3(cohort)})
	if err != nil {
		t.Fatal(err)
	}
	if out.Valid || !reflect.DeepEqual(out.LimitFindings, want) {
		t.Fatalf("pulse_predict valid=%v findings=%+v, want false %+v", out.Valid, out.LimitFindings, want)
	}

	data, err := afero.ReadFile(fs, cohort)
	if err != nil {
		t.Fatal(err)
	}
	env, err := p.PredictBytes(ctx, data, matrix3(cohort))
	if err != nil {
		t.Fatal(err)
	}
	pr := env.Data.(*descriptor.PredictResult)
	if pr.Valid || !reflect.DeepEqual(pr.LimitFindings, want) {
		t.Fatalf("PredictBytes valid=%v findings=%+v", pr.Valid, pr.LimitFindings)
	}
	// The JSON key the CLI and MCP carry.
	raw, _ := json.Marshal(pr)
	var keyed map[string]any
	_ = json.Unmarshal(raw, &keyed)
	if _, ok := keyed["limit_findings"]; !ok {
		t.Fatalf("limit_findings missing from predict JSON: %s", raw)
	}

	_, perr := p.Process(ctx, matrix3(cohort))
	requireLimitExceeded(t, perr, "max_matrix_dim", 2, "Options.Limits.MaxMatrixDim")
	sameEntry(t, env, perr)
}

// TestLimits_MatrixDim_AtLimitAndDefaults: a matrix at the limit, and
// any matrix under the defaults, produce no finding and run.
func TestLimits_MatrixDim_AtLimitAndDefaults(t *testing.T) {
	t.Parallel()

	fs, cohort := limitsCohort(t)
	ctx := context.Background()
	for name, l := range map[string]pulse.Limits{"at_limit": {MaxMatrixDim: 3}, "defaults": {}, "unlimited": {MaxMatrixDim: pulse.Unlimited}} {
		t.Run(name, func(t *testing.T) {
			p := limitsPulse(t, fs, l)
			res, err := p.Predict(ctx, matrix3(cohort))
			if err != nil {
				t.Fatal(err)
			}
			if !res.Valid || res.LimitFindings != nil {
				t.Fatalf("valid=%v findings=%+v, want valid with none", res.Valid, res.LimitFindings)
			}
			if _, err := p.Process(ctx, matrix3(cohort)); err != nil {
				t.Fatalf("Process: %v", err)
			}
		})
	}
}

func composeOf(cohort string, n int) *types.ComposedRequest {
	c := &types.ComposedRequest{}
	for range n {
		c.Requests = append(c.Requests, &types.Request{
			Cohort:       &types.Cohort{Filename: cohort},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "a", Label: "s"}},
		})
	}
	return c
}

// TestLimits_ComposeSlots: N+1 slots under MaxComposeSlots=N are
// refused whole on the serial and the parallel path (FailFast off and
// on); N slots run.
func TestLimits_ComposeSlots(t *testing.T) {
	t.Parallel()

	fs, cohort := limitsCohort(t)
	ctx := context.Background()
	p := limitsPulse(t, fs, pulse.Limits{MaxComposeSlots: 2})

	if _, err := p.Compose(ctx, composeOf(cohort, 2)); err != nil {
		t.Fatalf("serial at limit: %v", err)
	}
	if _, err := p.ComposeParallel(ctx, composeOf(cohort, 2), pulse.ComposeOptions{}); err != nil {
		t.Fatalf("parallel at limit: %v", err)
	}

	_, err := p.Compose(ctx, composeOf(cohort, 3))
	d := requireLimitExceeded(t, err, "max_compose_slots", 2, "Options.Limits.MaxComposeSlots")
	if d["observed"] != int64(3) {
		t.Fatalf("serial observed = %v, want 3", d["observed"])
	}
	for _, failFast := range []bool{false, true} {
		resp, err := p.ComposeParallel(ctx, composeOf(cohort, 3), pulse.ComposeOptions{FailFast: failFast})
		if resp != nil {
			t.Fatalf("parallel (FailFast=%v) returned a response; the whole call is refused", failFast)
		}
		d := requireLimitExceeded(t, err, "max_compose_slots", 2, "Options.Limits.MaxComposeSlots")
		if d["observed"] != int64(3) {
			t.Fatalf("parallel observed = %v, want 3", d["observed"])
		}
	}
}

func chainOf(cohort string, n int) *types.ChainRequest {
	c := &types.ChainRequest{Cohort: &types.Cohort{Filename: cohort}}
	c.Stages = append(c.Stages, &types.ChainStage{Request: &types.Request{
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "a", Label: "s"}},
		Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}},
	}})
	for i := 1; i < n; i++ {
		c.Stages = append(c.Stages, &types.ChainStage{Request: &types.Request{
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "s", Label: "s"}},
		}})
	}
	return c
}

// TestLimits_ChainStages: N+1 stages under MaxChainStages=N are
// refused before stage 0 runs; N stages run.
func TestLimits_ChainStages(t *testing.T) {
	t.Parallel()

	fs, cohort := limitsCohort(t)
	ctx := context.Background()
	p := limitsPulse(t, fs, pulse.Limits{MaxChainStages: 2})
	if _, err := p.ProcessChain(ctx, chainOf(cohort, 2)); err != nil {
		t.Fatalf("at limit: %v", err)
	}
	resp, err := p.ProcessChain(ctx, chainOf(cohort, 3))
	if resp != nil {
		t.Fatal("an over-limit chain returned a response")
	}
	d := requireLimitExceeded(t, err, "max_chain_stages", 2, "Options.Limits.MaxChainStages")
	if d["observed"] != int64(3) {
		t.Fatalf("observed = %v, want 3", d["observed"])
	}
}

// TestLimits_ChainStages_PredictParity: PredictChain judges the stage
// count right after the empty check, as ProcessChain does — N+1 stages
// under MaxChainStages=N are a certain, unlocated finding beside the
// error the run refuses with (code, message, details); N stages carry
// no finding.
func TestLimits_ChainStages_PredictParity(t *testing.T) {
	t.Parallel()

	fs, cohort := limitsCohort(t)
	ctx := context.Background()
	p := limitsPulse(t, fs, pulse.Limits{MaxChainStages: 2})

	env, err := p.PredictChain(ctx, chainOf(cohort, 2))
	if err != nil {
		t.Fatal(err)
	}
	if res := env.Data.(*pulse.ChainPredictResult); !res.Valid || res.LimitFindings != nil {
		t.Fatalf("at limit: valid=%v findings=%+v errors=%+v", res.Valid, res.LimitFindings, env.Errors)
	}

	env, err = p.PredictChain(ctx, chainOf(cohort, 3))
	if err != nil {
		t.Fatal(err)
	}
	res := env.Data.(*pulse.ChainPredictResult)
	want := []descriptor.LimitFinding{{Limit: "max_chain_stages", Configured: 2, Estimated: 3, Grade: descriptor.LimitGradeCertain}}
	if res.Valid || !reflect.DeepEqual(res.LimitFindings, want) {
		t.Fatalf("over limit: valid=%v findings=%+v, want false %+v", res.Valid, res.LimitFindings, want)
	}
	_, perr := p.ProcessChain(ctx, chainOf(cohort, 3))
	requireLimitExceeded(t, perr, "max_chain_stages", 2, "Options.Limits.MaxChainStages")
	sameEntry(t, env, perr)
}

// TestLimits_PredictChain_StageFindings: stage 0 is judged on the
// cohort's header count, exactly as its Process pre-flight — a certain
// memory finding located details.stage = 0 beside the run's own error;
// a later stage's input rows exist only at runtime, so it never carries
// a certain memory finding.
func TestLimits_PredictChain_StageFindings(t *testing.T) {
	t.Parallel()

	fs, cohort := limitsCohort(t)
	ctx := context.Background()
	p := limitsPulse(t, fs, pulse.Limits{MaxEstimatedMemory: 1})

	env, err := p.PredictChain(ctx, chainOf(cohort, 3))
	if err != nil {
		t.Fatal(err)
	}
	res := env.Data.(*pulse.ChainPredictResult)
	if res.Valid || len(res.LimitFindings) != 1 {
		t.Fatalf("valid=%v findings=%+v, want one stage-0 finding", res.Valid, res.LimitFindings)
	}
	f := res.LimitFindings[0]
	if f.Limit != "max_estimated_memory" || f.Grade != descriptor.LimitGradeCertain || !reflect.DeepEqual(f.Details, map[string]any{"stage": 0}) {
		t.Fatalf("finding %+v, want a certain memory finding at stage 0", f)
	}
	_, perr := p.ProcessChain(ctx, chainOf(cohort, 3))
	d := requireLimitExceeded(t, perr, "max_estimated_memory", 1, "Options.Limits.MaxEstimatedMemory")
	if d["observed"] != f.Estimated {
		t.Fatalf("runtime observed %v, predict estimated %d", d["observed"], f.Estimated)
	}
	sameEntry(t, env, perr)
}

// TestLimits_PredictCompose_SlotFindings: PredictCompose reports each
// slot's Request findings located details.request, graded like
// Process: a possible MaxGroups bound on the grouped slot only (Valid
// stays true, the run trips it), and under a set MaxEstimatedMemory a
// certain finding per slot whose first error is the one the run
// refuses with.
func TestLimits_PredictCompose_SlotFindings(t *testing.T) {
	t.Parallel()

	fs, cohort := limitsCohort(t)
	ctx := context.Background()
	batch := func() *types.ComposedRequest {
		c := composeOf(cohort, 1)
		c.Requests = append(c.Requests, &types.Request{
			Cohort:       &types.Cohort{Filename: cohort},
			Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "a", Label: "s"}},
		})
		return c
	}

	p := limitsPulse(t, fs, pulse.Limits{MaxGroups: 2})
	env, err := p.PredictCompose(ctx, batch())
	if err != nil {
		t.Fatal(err)
	}
	res := env.Data.(*pulse.ComposePredictResult)
	want := []descriptor.LimitFinding{{Limit: "max_groups", Configured: 2, Estimated: 3, Grade: descriptor.LimitGradePossible,
		Details: map[string]any{"request": 1}}}
	if !res.Valid || !reflect.DeepEqual(res.LimitFindings, want) {
		t.Fatalf("valid=%v findings=%+v, want true %+v", res.Valid, res.LimitFindings, want)
	}
	if _, err := p.Compose(ctx, batch()); !errors.HasCode(err, errors.PULSE_LIMIT_EXCEEDED) {
		t.Fatalf("run: %v, want the possible MaxGroups bound to trip", err)
	}

	p = limitsPulse(t, fs, pulse.Limits{MaxEstimatedMemory: 1})
	env, err = p.PredictCompose(ctx, batch())
	if err != nil {
		t.Fatal(err)
	}
	res = env.Data.(*pulse.ComposePredictResult)
	if res.Valid || len(res.LimitFindings) != 2 {
		t.Fatalf("valid=%v findings=%+v, want a certain finding per slot", res.Valid, res.LimitFindings)
	}
	for i, f := range res.LimitFindings {
		if f.Limit != "max_estimated_memory" || f.Grade != descriptor.LimitGradeCertain || !reflect.DeepEqual(f.Details, map[string]any{"request": i}) {
			t.Fatalf("finding %d = %+v", i, f)
		}
	}
	_, perr := p.Compose(ctx, batch())
	d := requireLimitExceeded(t, perr, "max_estimated_memory", 1, "Options.Limits.MaxEstimatedMemory")
	if d["observed"] != res.LimitFindings[0].Estimated {
		t.Fatalf("runtime observed %v, predict estimated %d", d["observed"], res.LimitFindings[0].Estimated)
	}
	sameEntry(t, env, perr)
}

// TestLimits_PredictFacet_Findings: PredictFacet reports a possible
// MaxGroups bound from the faceted fields' dictionary sizes (a numeric
// field mints no group) — Valid stays true, the run trips it — and the
// certain memory finding whose error the run refuses with.
func TestLimits_PredictFacet_Findings(t *testing.T) {
	t.Parallel()

	fs, cohort := limitsCohort(t)
	ctx := context.Background()
	facet := func() *pulse.FacetRequest {
		return &pulse.FacetRequest{Cohort: &types.Cohort{Filename: cohort}, Fields: []string{"cat", "a"}}
	}

	p := limitsPulse(t, fs, pulse.Limits{MaxGroups: 2})
	env, err := p.PredictFacet(ctx, facet())
	if err != nil {
		t.Fatal(err)
	}
	res := env.Data.(*pulse.FacetPredictResult)
	want := []descriptor.LimitFinding{{Limit: "max_groups", Configured: 2, Estimated: 3, Grade: descriptor.LimitGradePossible}}
	if !res.Valid || !reflect.DeepEqual(res.LimitFindings, want) {
		t.Fatalf("valid=%v findings=%+v, want true %+v", res.Valid, res.LimitFindings, want)
	}
	if _, err := p.FacetSchema(ctx, facet()); !errors.HasCode(err, errors.PULSE_LIMIT_EXCEEDED) {
		t.Fatalf("run: %v, want the possible MaxGroups bound to trip", err)
	}
	// At the bound, neither a finding nor a trip.
	p = limitsPulse(t, fs, pulse.Limits{MaxGroups: 3})
	if env, _ = p.PredictFacet(ctx, facet()); env.Data.(*pulse.FacetPredictResult).LimitFindings != nil {
		t.Fatalf("at the bound: findings %+v", env.Data.(*pulse.FacetPredictResult).LimitFindings)
	}
	if _, err := p.FacetSchema(ctx, facet()); err != nil {
		t.Fatalf("at the bound: %v", err)
	}

	p = limitsPulse(t, fs, pulse.Limits{MaxEstimatedMemory: 1})
	env, err = p.PredictFacet(ctx, facet())
	if err != nil {
		t.Fatal(err)
	}
	res = env.Data.(*pulse.FacetPredictResult)
	if res.Valid || len(res.LimitFindings) != 1 || res.LimitFindings[0].Grade != descriptor.LimitGradeCertain {
		t.Fatalf("valid=%v findings=%+v, want one certain memory finding", res.Valid, res.LimitFindings)
	}
	_, perr := p.FacetSchema(ctx, facet())
	d := requireLimitExceeded(t, perr, "max_estimated_memory", 1, "Options.Limits.MaxEstimatedMemory")
	if d["observed"] != res.LimitFindings[0].Estimated {
		t.Fatalf("runtime observed %v, predict estimated %d", d["observed"], res.LimitFindings[0].Estimated)
	}
	sameEntry(t, env, perr)
}
