package service

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/sweep/sweeptest"
	"github.com/frankbardon/pulse/types"
)

// compose_adaptive_test.go pins the adaptive executor's run-time
// bookkeeping (compose_adaptive.go) with the deterministic test
// strategy: a failed point penalised and listed, fail-fast and strict
// aborts, and the from_sweep binding of ranking[0] across rounds — on
// both orchestrators.

// adaptiveBindBatch is an adaptive test_fixed sweep of budget slots over
// the fits cohort — OLS y ~ x over weeks [0, hi], hi a range axis on
// [20, 103], and a list axis f naming the aggregated field — ranked by
// the fit's residual_std_err; the fit "best" binds from the sweep's
// regression "m".
func adaptiveBindBatch(budget int, fields ...string) *types.ComposedRequest {
	vals := make([]any, len(fields))
	for i, f := range fields {
		vals[i] = f
	}
	lo, hi := 20.0, 103.0
	return &types.ComposedRequest{
		Fits: []types.FitSpec{{Name: "best", FromSweep: &types.FitFromSweep{Regression: "m"}}},
		Sweep: &types.SweepSpec{
			Mode:   sweeptest.Mode,
			Budget: budget,
			Axes: []types.SweepAxis{
				{Name: "hi", Range: &types.SweepRange{Min: &lo, Max: &hi, Integer: true}},
				{Name: "f", Values: vals},
			},
			Request: json.RawMessage(`{"cohort":{"filename":"` + fitsCohort + `"},
				"filterers":[{"type":"FILTER_RANGE","field":"week","values":["0","{{hi}}"]}],
				"aggregations":[{"type":"AGG_SUM","field":{"$var":"f"},"label":"s"}],
				"regressions":[{"type":"REG_OLS","name":"m","target":"y","predictors":["x"]}]}`),
			Rank: &types.SweepRank{By: "regressions.m.residual_std_err"},
		},
	}
}

func runAdaptive(svc *Service, mode string, c *types.ComposedRequest, failFast bool) (*types.ComposedResponse, error) {
	if mode == "serial" {
		return svc.Compose(context.Background(), c)
	}
	return svc.ComposeParallel(context.Background(), c, ComposeOptions{MaxWorkers: 4, FailFast: failFast})
}

// TestComposeAdaptive_FromSweepBindsWinner: the fit bound from an
// adaptive sweep refits ranking[0] — wherever its round — so its
// coefficients are the winning slot's (a later round's here, never a
// placeholder's), serial and parallel alike.
func TestComposeAdaptive_FromSweepBindsWinner(t *testing.T) {
	t.Parallel()
	svc := fitsService(t)
	for _, mode := range []string{"serial", "parallel"} {
		c := adaptiveBindBatch(8, "x", "y")
		c.Sweep.Rank.Order = types.SweepRankDesc // the winner is a later round's
		out, err := runAdaptive(svc, mode, c, true)
		if err != nil {
			t.Fatalf("%s: %v", mode, err)
		}
		if len(out.Responses) != 8 || len(out.Ranking) != 8 || out.Ranking[0].Slot < sweeptest.Batch {
			t.Fatalf("%s: %d responses, ranking %+v — the fixture needs a winner past round 0", mode, len(out.Responses), out.Ranking)
		}
		win := out.Responses[out.Ranking[0].Slot].Regressions[0]
		fit := out.Fits["best"]
		if fit == nil || fit.RegressionResult == nil {
			t.Fatalf("%s: fit %+v", mode, fit)
		}
		if !reflect.DeepEqual(fit.RegressionResult.Coefficients, win.Coefficients) || fit.RegressionResult.ResidualStdErr != out.Ranking[0].Value {
			t.Errorf("%s: fit %+v is not the winner %+v (rank %+v)", mode, fit.RegressionResult.Coefficients, win.Coefficients, out.Ranking[0])
		}
	}
}

// TestComposeAdaptive_PenalisesFailedPoints: a point whose body fails
// (its list value names no field) is penalised and the search goes on:
// the batch succeeds with every budget slot answered, each failed slot a
// stub carrying its label and left out of the ranking, and one
// PULSE_SLOT_FAILED warning lists them (failed_indices, ascending) —
// identically on both orchestrators. Fail-fast aborts at the failing
// round with its located error; Strict fails the batch with it.
func TestComposeAdaptive_PenalisesFailedPoints(t *testing.T) {
	t.Parallel()
	svc := fitsService(t)
	var first []int
	var firstErr any
	for _, mode := range []string{"serial", "parallel"} {
		out, err := runAdaptive(svc, mode, adaptiveBindBatch(9, "x", "nope"), false)
		if err != nil {
			t.Fatalf("%s: %v", mode, err)
		}
		var want []int
		for i, r := range out.Responses {
			if strings.Contains(r.Label, "f=nope") || strings.HasSuffix(r.Label, "_nope") {
				want = append(want, i)
				if len(r.Regressions) != 0 || len(r.Data) != 0 || len(r.Warnings) != 0 {
					t.Errorf("%s: failed slot %d is not a stub: %+v", mode, i, r)
				}
			}
		}
		if len(out.Responses) != 9 || len(want) == 0 || len(want) == 9 {
			t.Fatalf("%s: %d responses, %d failed — the fixture must mix both", mode, len(out.Responses), len(want))
		}
		var warn *types.ResponseWarning
		for _, w := range out.Warnings {
			if w.Code == string(perr.PULSE_SLOT_FAILED) {
				warn = w
			}
		}
		if warn == nil || !reflect.DeepEqual(warn.Details["failed_indices"], want) || warn.Details["slot_kind"] != "sweep" || warn.Details["first_error"] == "" {
			t.Fatalf("%s: warning %+v, want failed_indices %v", mode, warn, want)
		}
		if len(out.Ranking) != 9-len(want) {
			t.Errorf("%s: ranking %d entries, want %d", mode, len(out.Ranking), 9-len(want))
		}
		for _, e := range out.Ranking {
			if slices.Contains(want, e.Slot) {
				t.Errorf("%s: failed slot %d ranked", mode, e.Slot)
			}
		}
		if first == nil {
			first, firstErr = want, warn.Details["first_error"]
		} else if !reflect.DeepEqual(first, want) || firstErr != warn.Details["first_error"] {
			t.Errorf("serial failed %v (%v), parallel %v (%v)", first, firstErr, want, warn.Details["first_error"])
		}
	}
	// Fail-fast: the pool's located error, no response.
	if _, err := runAdaptive(svc, "parallel", adaptiveBindBatch(9, "x", "nope"), true); err == nil || !strings.Contains(err.Error(), "compose parallel: request ") {
		t.Errorf("fail-fast: %v", err)
	}
	// Strict: the failed slot's own located error, on both orchestrators.
	strict := fitsService(t)
	strict.SetStrict(true)
	for _, mode := range []string{"serial", "parallel"} {
		_, err := runAdaptive(strict, mode, adaptiveBindBatch(9, "x", "nope"), false)
		var ce *perr.CodedError
		if err == nil || !stderrors.As(err, &ce) || ce.Details["request"] != first[0] {
			t.Errorf("%s strict: %v, want slot %d's error", mode, err, first[0])
		}
	}
}
