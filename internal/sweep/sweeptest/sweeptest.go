// Package sweeptest registers a deterministic adaptive sweep mode for
// tests: Mode ("test_fixed"), proposing Batch points a round. It exists
// so the adaptive executor (internal/sweep adaptive.go) is verifiable
// without a real search strategy, and it is imported by test files only
// — a blank import registers the mode (its init), so every package's
// tests see the same mode as predict and the runtime.
//
// The strategy also guards the executor's contract: each Propose call
// checks that history is every slot of every earlier round, ordered by
// slot index, round-major, and refuses otherwise — a run whose rounds
// were not barriers fails.
package sweeptest

import (
	"fmt"
	"math/bits"
	"math/rand/v2"

	"github.com/frankbardon/pulse/internal/regresample"
	"github.com/frankbardon/pulse/internal/sweep"
	"github.com/frankbardon/pulse/types"
)

// Mode is the test strategy's SweepMode.
const Mode types.SweepMode = "test_fixed"

// Batch is the number of points a round proposes (fewer when less
// budget is left).
const Batch = 3

func init() {
	sweep.RegisterAdaptive(sweep.AdaptiveMode{Mode: Mode, New: New})
}

// Strategy is the test strategy. Round 0 is Batch seeded uniform points
// (PCG over the config seed, raw words only); every later round steps
// away from the best observation so far (lowest Loss, ties the lowest
// slot): point k shifts each range coordinate by (k+1)/8 (mod 1) and
// each list index by k+1 (mod levels).
type Strategy struct {
	cfg    sweep.StrategyConfig
	rounds []int // points proposed per round so far
}

// New builds the test strategy.
func New(cfg sweep.StrategyConfig) (sweep.Strategy, error) {
	return &Strategy{cfg: cfg}, nil
}

// Propose implements sweep.Strategy.
func (s *Strategy) Propose(round int, history []sweep.Observation, limit int) ([]sweep.Point, error) {
	if err := s.checkHistory(round, history); err != nil {
		return nil, err
	}
	n := min(Batch, limit)
	dims := s.cfg.Dims
	out := make([]sweep.Point, n)
	if round == 0 {
		seed := uint64(s.cfg.Seed)
		src := rand.NewPCG(seed, seed^regresample.PCGStream)
		for k := range out {
			p := sweep.Point{U: make([]float64, len(dims)), Index: make([]int, len(dims))}
			for i, d := range dims {
				if d.Range != nil {
					p.U[i] = float64(src.Uint64()>>11) * 0x1p-53
				} else {
					hi, _ := bits.Mul64(src.Uint64(), uint64(d.Levels))
					p.Index[i] = int(hi)
				}
			}
			out[k] = p
		}
		s.rounds = append(s.rounds, n)
		return out, nil
	}
	best := history[0]
	for _, o := range history[1:] {
		if o.Loss < best.Loss {
			best = o
		}
	}
	for k := range out {
		p := sweep.Point{U: make([]float64, len(dims)), Index: make([]int, len(dims))}
		for i, d := range dims {
			if d.Range != nil {
				u := best.Point.U[i] + float64(float64(k+1)*0.125)
				for u >= 1 {
					u--
				}
				p.U[i] = u
			} else {
				p.Index[i] = (best.Point.Index[i] + k + 1) % d.Levels
			}
		}
		out[k] = p
	}
	s.rounds = append(s.rounds, n)
	return out, nil
}

// checkHistory refuses a history that is not every earlier round's
// slots, in slot order, round-major.
func (s *Strategy) checkHistory(round int, history []sweep.Observation) error {
	if round != len(s.rounds) {
		return fmt.Errorf("sweeptest: round %d proposed after %d rounds", round, len(s.rounds))
	}
	want := 0
	for _, n := range s.rounds {
		want += n
	}
	if len(history) != want {
		return fmt.Errorf("sweeptest: round %d sees %d observations, want %d (a round was not a barrier)", round, len(history), want)
	}
	slot := 0
	for r, n := range s.rounds {
		for range n {
			o := history[slot]
			if o.Slot != slot || o.Round != r {
				return fmt.Errorf("sweeptest: observation %d is slot %d round %d, want slot %d round %d", slot, o.Slot, o.Round, slot, r)
			}
			slot++
		}
	}
	return nil
}

// Evaluated is one slot a Search ran: its 0-based sweep slot, round,
// axis values (as rendered) and objective value.
type Evaluated struct {
	Slot  int
	Round int
	Axes  map[string]any
	Value float64
}

// Search runs an adaptive sweep spec end to end through the real
// executor — sweep.Expand, then Adaptive.Run, every slot rendered and
// strictly decoded — scoring each slot by f over its axis values
// instead of running a request (ok false penalises the slot). It
// returns every evaluated slot in slot order.
func Search(spec *types.SweepSpec, f func(axes map[string]any) (value float64, ok bool)) ([]Evaluated, error) {
	exp, err := sweep.Expand(&types.ComposedRequest{Sweep: spec}, sweep.Options{}, nil)
	if err != nil {
		return nil, err
	}
	if exp.Adaptive == nil {
		return nil, fmt.Errorf("sweeptest: mode %q is not adaptive", spec.Mode)
	}
	var out []Evaluated
	err = exp.Adaptive.Run(func(round int, slots []sweep.AdaptiveSlot) ([]sweep.Outcome, error) {
		outs := make([]sweep.Outcome, len(slots))
		for k, s := range slots {
			v, ok := f(s.Axes)
			outs[k] = sweep.Outcome{Value: v, Penalised: !ok}
			out = append(out, Evaluated{Slot: s.Slot, Round: round, Axes: s.Axes, Value: v})
		}
		return outs, nil
	})
	return out, err
}
