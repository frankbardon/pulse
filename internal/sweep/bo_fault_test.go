package sweep

import (
	"math"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestBO_SurrogateFaultIsSweepCoded: a surrogate the round cannot fit
// (here a non-finite loss) stops the run as PULSE_SWEEP_INVALID
// bo_surrogate_failed naming mode and round — the sweep family, which
// the executor's strategyFault passes through — never a bare gp error
// (PROCESSING_INTERNAL).
func TestBO_SurrogateFaultIsSweepCoded(t *testing.T) {
	t.Parallel()
	lo, hi := 0.0, 1.0
	spec := &types.SweepSpec{Mode: types.SweepModeBO, Budget: 4, Axes: []types.SweepAxis{{Name: "x", Range: &types.SweepRange{Min: &lo, Max: &hi}}}}
	st, err := newBO(StrategyConfig{Dims: dimsOf(spec), Budget: 4, Seed: 1, Spec: spec})
	if err != nil {
		t.Fatal(err)
	}
	pts, err := st.Propose(0, nil, 4)
	if err != nil || len(pts) != 2 {
		t.Fatalf("round 0: %d points, %v", len(pts), err)
	}
	history := []Observation{
		{Slot: 0, Point: pts[0], Loss: 1},
		{Slot: 1, Point: pts[1], Loss: math.Inf(1)},
	}
	_, err = st.Propose(1, history, 2)
	ce := strategyFault(types.SweepModeBO, 1, err)
	if ce.Code != errors.PULSE_SWEEP_INVALID || ce.Details["reason"] != ReasonBOSurrogateFailed ||
		ce.Details["round"] != 1 || ce.Details["mode"] != "bo" || !strings.HasPrefix(ce.Details["cause"].(string), "gp: ") {
		t.Fatalf("got %s %v", ce.Code, ce.Details)
	}
}
