package regmmm

import (
	"fmt"
	"math"

	"github.com/frankbardon/pulse/mcmc"
	"github.com/frankbardon/pulse/types"
)

// bayes_sampler.go is REG_MMM_BAYES's sampler choice: params.sampler
// (nuts, the default, or hmc), HMC's own knobs path_length and
// step_size, their spec refusals and the bayesSampler hook that builds
// the sampler a fit runs. Absent, every fit is the NUTS one of old, bit
// for bit (the derived seed reads only the params present).

// The params.sampler values.
const (
	samplerNUTS = "nuts"
	samplerHMC  = "hmc"
)

// samplerNames is params.sampler's enum, in manifest order.
var samplerNames = []string{samplerNUTS, samplerHMC}

// maxBayesHMCSteps is mcmc.HMC's leapfrog cap per transition: a fixed
// params.step_size whose ⌈path_length / step_size⌉ exceeds it is the
// factory's refusal (predict raises it), never the sampler's
// PULSE_MCMC_INVALID mid-run. TestMMMBayes_HMCStepCapMatchesSampler
// pins it to the sampler's own.
const maxBayesHMCSteps = 1 << 16

// The params.sampler refusal reasons (details.reason, PROCESSING_CONFIG).
const (
	// reasonSamplerUnknown: params.sampler is not nuts or hmc.
	reasonSamplerUnknown = "sampler_unknown"
	// reasonHMCOnly: path_length or step_size under nuts.
	reasonHMCOnly = "hmc_only"
	// reasonPathLengthRequired: hmc without path_length.
	reasonPathLengthRequired = "path_length_required"
	// reasonHMCKnobInvalid: path_length or step_size not > 0.
	reasonHMCKnobInvalid = "hmc_knob_invalid"
	// reasonPathTooLong: a fixed step_size needs more than
	// maxBayesHMCSteps leapfrog steps to cover path_length.
	reasonPathTooLong = "path_too_long"
	// reasonTargetAcceptUnused: target_accept beside a fixed step_size,
	// which leaves nothing to adapt.
	reasonTargetAcceptUnused = "target_accept_unused"
)

// samplerSettings is the sampler a fit runs: NUTS at targetAccept, or
// with hmc static HMC over pathLength at stepSize (0 adapts it towards
// targetAccept).
type samplerSettings struct {
	hmc                  bool
	targetAccept         float64
	pathLength, stepSize float64
}

// bayesSampler builds the sampler a fit runs from its settings. It is a
// variable only so the package's tests can count and fail sampler runs;
// nothing else sets it.
var bayesSampler = func(s samplerSettings) mcmc.Sampler {
	if s.hmc {
		return mcmc.HMC{PathLength: s.pathLength, StepSize: s.stepSize, TargetAccept: s.targetAccept}
	}
	return mcmc.NUTS{TargetAccept: s.targetAccept}
}

// sampler is cfg's sampler settings.
func (cfg *bayesConfig) sampler() samplerSettings {
	return samplerSettings{hmc: cfg.hmc, targetAccept: cfg.targetAccept, pathLength: cfg.pathLength, stepSize: cfg.stepSize}
}

// samplerConfig validates params.sampler, path_length and step_size
// into cfg: path_length is required under hmc and step_size optional
// there (absent, it adapts), both refused under nuts; each must be > 0,
// a fixed step size must cover the path in at most maxBayesHMCSteps
// leapfrog steps, and target_accept, which only steers step-size
// adaptation, is refused beside a fixed one.
func samplerConfig(spec *types.RegressionSpec, w *bayesParamsWire, cfg *bayesConfig) error {
	name := samplerNUTS
	if w.Sampler != nil {
		name = *w.Sampler
	}
	switch name {
	case samplerNUTS:
		for _, k := range []struct {
			key string
			v   *float64
		}{{"path_length", w.PathLength}, {"step_size", w.StepSize}} {
			if k.v != nil {
				return bayesSlotError(spec, "params."+k.key, fmt.Sprintf("params.%s is static HMC's knob; it needs params.sampler \"hmc\" (NUTS chooses its own path)", k.key),
					map[string]any{"reason": reasonHMCOnly, "sampler": name})
			}
		}
		return nil
	case samplerHMC:
	default:
		return bayesSlotError(spec, "params.sampler", fmt.Sprintf("params.sampler %q is not one of: nuts, hmc", name),
			map[string]any{"reason": reasonSamplerUnknown, "sampler": name, "valid": samplerNames})
	}
	if w.PathLength == nil {
		return bayesSlotError(spec, "params.path_length", "params.sampler \"hmc\" needs params.path_length, the integration time of one trajectory (π/2 ≈ 1.57 is a quarter period of the unit-scale posterior the adapted metric aims at)",
			map[string]any{"reason": reasonPathLengthRequired})
	}
	for _, k := range []struct {
		key string
		v   *float64
	}{{"path_length", w.PathLength}, {"step_size", w.StepSize}} {
		if k.v != nil && !(*k.v > 0 && !math.IsInf(*k.v, 0)) {
			return bayesSlotError(spec, "params."+k.key, fmt.Sprintf("params.%s must be a finite number > 0, got %v", k.key, *k.v),
				map[string]any{"reason": reasonHMCKnobInvalid, "got": *k.v})
		}
	}
	cfg.hmc, cfg.pathLength = true, *w.PathLength
	if w.StepSize == nil {
		return nil
	}
	cfg.stepSize = *w.StepSize
	if !(math.Ceil(cfg.pathLength/cfg.stepSize) <= maxBayesHMCSteps) {
		return bayesSlotError(spec, "params.step_size", fmt.Sprintf("params.path_length %v at params.step_size %v needs more than %d leapfrog steps per transition; raise step_size or shorten path_length",
			cfg.pathLength, cfg.stepSize, maxBayesHMCSteps),
			map[string]any{"reason": reasonPathTooLong, "path_length": cfg.pathLength, "step_size": cfg.stepSize, "max_steps": maxBayesHMCSteps})
	}
	if w.TargetAccept != nil {
		return bayesSlotError(spec, "params.target_accept", "params.target_accept steers step-size adaptation, which a fixed params.step_size turns off; drop one of them",
			map[string]any{"reason": reasonTargetAcceptUnused})
	}
	return nil
}
