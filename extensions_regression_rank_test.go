package pulse_test

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/types"
)

// Sweep ranking on custom fits (U53 E2-S4): a compose sweep ranks on a
// custom model's declared outputs (regressions.<name>.outputs.<key> — an
// exact 0 included, since a present map key stays on the wire) and on a
// fixed-set fit statistic the model reports; ranking on one it does not
// report is refused with PULSE_SWEEP_RANK_PATH.

const regRankLoss types.RegressionType = "REG_RANK_LOSS"

// lossModel fits the target's mean and reports outputs.loss =
// (shift − 2)², its one param; it reports no fit statistic.
type lossModel struct{ shift float64 }

func (m lossModel) Fit(rows extend.Rows) (extend.RegressionOutput, error) {
	sum := 0.0
	for i := 0; i < rows.Len(); i++ {
		v, _ := rows.At(i).NumericValue("score")
		sum += v
	}
	d := m.shift - 2
	return extend.RegressionOutput{
		Coefficients: []extend.RegressionCoefficient{{Name: "(intercept)", Estimate: sum / float64(max(rows.Len(), 1))}},
		Outputs:      map[string]any{"loss": d * d},
	}, nil
}

func rankRegistrations() []pulse.RegressionRegistration {
	return append(regressionParityRegistrations(), pulse.RegressionRegistration{
		Name: regRankLoss, Description: "Test-only mean fit reporting a shift-dependent loss.",
		Params:       []pulse.ParamMeta{{Name: "shift", JSONType: "number", Description: "The loss's offset.", Required: true}},
		OutputSchema: []pulse.RegressionOutputMeta{{Key: "loss", JSONType: "number", Description: "(shift − 2)²."}},
		Factory: func(spec *types.RegressionSpec, _ *encoding.Schema) (extend.Regression, error) {
			var params struct {
				Shift float64 `json:"shift"`
			}
			if len(spec.Params) > 0 {
				if err := json.Unmarshal(spec.Params, &params); err != nil {
					return nil, err
				}
			}
			return lossModel{shift: params.Shift}, nil
		},
	})
}

func rankCompose(t *testing.T, p *pulse.Pulse, body string) (*pulse.ComposedResponse, error) {
	t.Helper()
	var c pulse.ComposedRequest
	if err := json.Unmarshal([]byte(body), &c); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return p.Compose(context.Background(), &c)
}

func TestExtensions_RegressionSweepRank(t *testing.T) {
	p, cohort := openE2E(t, pulse.Extensions{Regressions: rankRegistrations()})
	file := cohort.Filename

	t.Run("outputs_key", func(t *testing.T) {
		resp, err := rankCompose(t, p, `{"sweep":{"axes":[{"name":"s","values":[0,3,2,1]}],"label":"s{{s}}",
			"request":{"cohort":{"filename":"`+file+`"},"regressions":[{"type":"REG_RANK_LOSS","name":"fit","target":"score","predictors":["qty"],
			"params":{"shift":{"$var":"s"}}}]},"rank":{"by":"regressions.fit.outputs.loss"}}}`)
		if err != nil {
			t.Fatal(err)
		}
		ax := func(s string) map[string]any { return map[string]any{"s": json.Number(s)} }
		want := []types.RankEntry{{Label: "s2", Value: 0, Rank: 1, Slot: 2, Axes: ax("2")}, {Label: "s3", Value: 1, Rank: 2, Slot: 1, Axes: ax("3")},
			{Label: "s1", Value: 1, Rank: 3, Slot: 3, Axes: ax("1")}, {Label: "s0", Value: 4, Rank: 4, Slot: 0, Axes: ax("0")}}
		if len(resp.Ranking) != len(want) {
			t.Fatalf("ranking = %+v, want %+v", resp.Ranking, want)
		}
		for i := range want {
			if !reflect.DeepEqual(resp.Ranking[i], want[i]) {
				t.Errorf("ranking[%d] = %+v, want %+v", i, resp.Ranking[i], want[i])
			}
		}
	})

	t.Run("reported_statistic", func(t *testing.T) {
		resp, err := rankCompose(t, p, `{"sweep":{"axes":[{"name":"x","values":["qty","day"]}],"label":"{{x}}",
			"request":{"cohort":{"filename":"`+file+`"},"regressions":[{"type":"REG_PARITY_OLS","name":"fit","target":"score","predictors":[{"$var":"x"}]}]},
			"rank":{"by":"regressions.fit.residual_std_err"}}}`)
		if err != nil {
			t.Fatal(err)
		}
		if len(resp.Ranking) != 2 {
			t.Fatalf("ranking = %+v", resp.Ranking)
		}
		rse := map[string]float64{}
		for _, x := range []string{"qty", "day"} {
			r := fitOne(t, p, cohort, &types.RegressionSpec{Type: regParityOLS, Name: "fit", Target: "score", Predictors: []string{x}})
			rse[x] = r.ResidualStdErr
		}
		first, second := "qty", "day"
		if rse["day"] < rse["qty"] {
			first, second = "day", "qty"
		}
		if resp.Ranking[0].Label != first || resp.Ranking[0].Value != rse[first] || resp.Ranking[1].Label != second || resp.Ranking[1].Value != rse[second] {
			t.Errorf("ranking = %+v, want %s (%v) then %s (%v)", resp.Ranking, first, rse[first], second, rse[second])
		}
	})

	t.Run("unreported_statistic", func(t *testing.T) {
		_, err := rankCompose(t, p, `{"sweep":{"axes":[{"name":"s","values":[0,1]}],
			"request":{"cohort":{"filename":"`+file+`"},"regressions":[{"type":"REG_RANK_LOSS","name":"fit","target":"score","predictors":["qty"],
			"params":{"shift":{"$var":"s"}}}]},"rank":{"by":"regressions.fit.residual_std_err"}}}`)
		ce := requireCodedError(t, err, perr.PULSE_SWEEP_RANK_PATH)
		if ce.Details["reason"] != "missing" || ce.Details["segment"] != "residual_std_err" {
			t.Errorf("details = %v, want reason missing at residual_std_err", ce.Details)
		}
	})
}
