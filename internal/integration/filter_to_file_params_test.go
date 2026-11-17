package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// filter_to_file_params_test.go — FilterToFileRequest.Params
// (compose-sweep-2 E2-S1, #522): the predicate reads params.<name> as a
// Process filter does — the same values, the same refusals — the values
// key the dedup hash, and capability:params gates the key. Fixture:
// openParams (x = 0..19, wave cycles 1..5).

// ftfParams is a filter-to-file of params.pulse under expr (or, when
// structured, one FILTER_EXPRESSION filterer) with params.
func ftfParams(expr string, structured bool, params types.Params) *pulse.FilterToFileRequest {
	req := &pulse.FilterToFileRequest{SourcePath: "params.pulse", OutputDir: "out", Params: params}
	if structured {
		req.Filterers = []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: expr}}
	} else {
		req.Expression = expr
	}
	return req
}

// TestFilterToFileParams_PredicateReadsParams: both predicate shapes
// keep the rows the literal predicate keeps; other values write another
// output, the same values reuse it.
func TestFilterToFileParams_PredicateReadsParams(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p := openParams(t, pulse.Options{})
	for _, structured := range []bool{false, true} {
		t.Run(fmt.Sprintf("structured=%v", structured), func(t *testing.T) {
			lit, err := p.FilterToFileWithRequest(ctx, ftfParams("x >= 12 && wave in [1, 3]", structured, nil))
			if err != nil {
				t.Fatalf("literal: %v", err)
			}
			req := ftfParams("x >= params.cut && wave in params.waves", structured,
				types.Params{"cut": json.Number("12"), "waves": []any{json.Number("1"), json.Number("3")}})
			got, err := p.FilterToFileWithRequest(ctx, req)
			if err != nil {
				t.Fatalf("params: %v", err)
			}
			if got.RowCount != lit.RowCount || got.RowCount == 0 || got.Reused {
				t.Fatalf("params kept %d (reused %v), literal %d", got.RowCount, got.Reused, lit.RowCount)
			}
			again, err := p.FilterToFileWithRequest(ctx, req)
			if err != nil || !again.Reused || again.OutputPath != got.OutputPath {
				t.Fatalf("same params: %v %+v, want reuse of %s", err, again, got.OutputPath)
			}
			other := ftfParams(req.Expression, structured, types.Params{"cut": json.Number("5"), "waves": req.Params["waves"]})
			if structured {
				other = ftfParams(req.Filterers[0].Expression, structured, types.Params{"cut": json.Number("5"), "waves": req.Params["waves"]})
			}
			moved, err := p.FilterToFileWithRequest(ctx, other)
			if err != nil {
				t.Fatalf("other params: %v", err)
			}
			if moved.Reused || moved.OutputPath == got.OutputPath || moved.RowCount <= got.RowCount {
				t.Fatalf("other params: %+v (first %+v)", moved, got)
			}
		})
	}
}

// TestFilterToFileParams_RefusalsMatchProcess: every params refusal is
// the code and message Process raises for the same predicate and block.
func TestFilterToFileParams_RefusalsMatchProcess(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p := openParams(t, pulse.Options{})
	cases := []struct {
		name   string
		expr   string
		params types.Params
		cohort string
		code   perr.Code
	}{
		{"null value", "wave == params.w", types.Params{"w": nil}, "params.pulse", perr.PULSE_PARAMS_INVALID},
		{"mixed list", "wave in params.w", types.Params{"w": []any{1, "a"}}, "params.pulse", perr.PULSE_PARAMS_INVALID},
		{"undeclared", "wave == params.w", types.Params{"v": 1}, "params.pulse", perr.PULSE_PARAMS_UNDECLARED},
		{"field collision", "wave == params.w", types.Params{"w": 1}, "params_field.pulse", perr.PULSE_PARAMS_FIELD_COLLISION},
	}
	for _, c := range cases {
		for _, structured := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/structured=%v", c.name, structured), func(t *testing.T) {
				_, procErr := p.Process(ctx, &types.Request{
					Cohort: &types.Cohort{Filename: c.cohort}, Params: c.params,
					Filterers:    []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: c.expr}},
					Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "x", Label: "c"}},
				})
				want := requireCodedError(t, procErr, c.code)
				req := ftfParams(c.expr, structured, c.params)
				req.SourcePath = c.cohort
				_, err := p.FilterToFileWithRequest(ctx, req)
				got := requireCodedError(t, err, c.code)
				if got.Message != want.Message {
					t.Fatalf("message %q, Process %q", got.Message, want.Message)
				}
			})
		}
	}
}

// TestFilterToFileParams_FeatureProfileGate: a profile hiding
// capability:params refuses a set params as an unknown field (valid
// keys without it); enabling the feature runs it.
func TestFilterToFileParams_FeatureProfileGate(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fsys := afero.NewMemMapFs()
	openParams(t, pulse.Options{FS: fsys})
	open := func(features ...string) *pulse.Pulse {
		p, err := pulse.New(pulse.Options{FS: fsys, FeatureProfile: &pulse.FeatureProfile{Features: features}})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		return p
	}
	base := []string{"capability:process", "FILTER_EXPRESSION"}
	req := ftfParams("wave == params.w", false, types.Params{"w": 2})
	_, err := open(base...).FilterToFileWithRequest(ctx, req)
	ce := requireCodedError(t, err, perr.PULSE_REQUEST_UNKNOWN_FIELD)
	if fmt.Sprint(ce.Details["unknown_keys"]) != "[params]" || fmt.Sprint(ce.Details["valid_keys"]) == "" ||
		containsKey(ce.Details["valid_keys"], "params") {
		t.Fatalf("details = %v", ce.Details)
	}
	got, err := open(append(append([]string{}, base...), "capability:params")...).FilterToFileWithRequest(ctx, req)
	if err != nil || got.RowCount != paramsRows/5 {
		t.Fatalf("enabled: %v %+v", err, got)
	}
}

func containsKey(v any, key string) bool {
	switch keys := v.(type) {
	case []string:
		for _, k := range keys {
			if k == key {
				return true
			}
		}
	case []any:
		for _, k := range keys {
			if k == key {
				return true
			}
		}
	}
	return false
}

// TestFilterToFileParams_InvalidNeverReuses: an invalid block is refused
// before the dedup check, so an existing output at the named path is
// never handed back for it.
func TestFilterToFileParams_InvalidNeverReuses(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p := openParams(t, pulse.Options{})
	first := ftfParams("wave == params.w", false, types.Params{"w": 2})
	first.OutputName = "named.pulse"
	if _, err := p.FilterToFileWithRequest(ctx, first); err != nil {
		t.Fatalf("first: %v", err)
	}
	bad := ftfParams("wave == params.w", false, types.Params{"w": nil})
	bad.OutputName = "named.pulse"
	res, err := p.FilterToFileWithRequest(ctx, bad)
	if err == nil {
		t.Fatalf("invalid params reused %+v", res)
	}
	requireCodedError(t, err, perr.PULSE_PARAMS_INVALID)
}
