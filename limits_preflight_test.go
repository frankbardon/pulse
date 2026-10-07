package pulse_test

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

	out, err := mcp.HandlePredict(ctx, p, *matrix3(cohort))
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
