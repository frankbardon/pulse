package regmmm

import (
	"context"
	"encoding/json"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/mcmc"
	"github.com/frankbardon/pulse/types"
)

// stdNormal is a one-dimensional standard normal: the cheapest model
// mcmc.HMC accepts.
type stdNormal struct{}

func (stdNormal) Dim() int { return 1 }
func (stdNormal) LogDensityGrad(theta, grad []float64) float64 {
	grad[0] = -theta[0]
	return -0.5 * theta[0] * theta[0]
}

// TestMMMBayes_HMCStepCapMatchesSampler: the factory's leapfrog cap on
// a fixed step_size is mcmc.HMC's own — at the cap both run, one past
// it the factory refuses (path_too_long) exactly where the sampler
// would refuse mid-run with PULSE_MCMC_INVALID.
func TestMMMBayes_HMCStepCapMatchesSampler(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		path float64
		ok   bool
	}{{maxBayesHMCSteps, true}, {maxBayesHMCSteps + 0.5, false}} {
		params, _ := json.Marshal(map[string]any{"channels": []string{"tv"}, "time": "week", "sampler": "hmc", "path_length": tc.path, "step_size": 1})
		spec := &types.RegressionSpec{Type: BayesName, Name: "bayes", Target: "sales", Predictors: []string{"tv"}, Params: params}
		_, ferr := BayesFactory(spec, nil)
		_, serr := mcmc.HMC{PathLength: tc.path, StepSize: 1}.Sample(context.Background(), stdNormal{}, 1,
			mcmc.Options{Chains: 1, Warmup: 1, Draws: 1, Init: []float64{0}})
		if tc.ok {
			if ferr != nil || serr != nil {
				t.Errorf("path %v at the cap: factory %v, sampler %v; want both to run", tc.path, ferr, serr)
			}
			continue
		}
		if ce, ok := ferr.(*perr.CodedError); !ok || ce.Code != perr.PROCESSING_CONFIG || ce.Details["reason"] != reasonPathTooLong {
			t.Errorf("path %v past the cap: factory %v, want PROCESSING_CONFIG path_too_long", tc.path, ferr)
		}
		if !perr.HasCode(serr, perr.PULSE_MCMC_INVALID) {
			t.Errorf("path %v past the cap: sampler %v, want PULSE_MCMC_INVALID", tc.path, serr)
		}
	}
}
