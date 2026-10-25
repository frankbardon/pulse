package sweep

import (
	"encoding/json"
	"math/bits"
	"math/rand/v2"
	"strconv"

	"github.com/frankbardon/pulse/internal/regresample"
	"github.com/frankbardon/pulse/linalg/detmath"
	"github.com/frankbardon/pulse/types"
)

// EffectiveSeed is the seed a sampling sweep (mode random / lhs) draws
// with: spec.RNGSeed when non-zero, else regresample.DeriveSeed — the
// one rng_seed 0 derivation every seeded draw shares — over the spec's
// wire form with rng_seed excluded (never the clock), so the same spec
// draws the same slots on every run and every surface. It reads the
// spec only; predict and the runtime call it on the same spec and
// report the same seed. Meaningless for an enumeration mode.
func EffectiveSeed(spec *types.SweepSpec) int64 {
	if spec == nil {
		return 0
	}
	if spec.RNGSeed != 0 {
		return spec.RNGSeed
	}
	cp := *spec
	cp.RNGSeed = 0
	body, err := json.Marshal(&cp)
	if err != nil {
		body = nil
	}
	return regresample.DeriveSeed(body, 0, nil)
}

// sampler is a sampling sweep's draw stream: PCG seeded with the two
// words every seeded draw in Pulse uses (seed, seed ^ PCGStream). It
// reads raw 64-bit words only and builds each unit draw and bounded
// integer by exact integer / power-of-two arithmetic, so the stream is
// bit-identical on every architecture and Go release (the PCG-DXSM
// algorithm is fixed; no math/rand convenience method is relied on).
type sampler struct {
	src *rand.PCG
}

func newSampler(seed int64) *sampler {
	s := uint64(seed)
	return &sampler{src: rand.NewPCG(s, s^regresample.PCGStream)}
}

// unit is a uniform draw on [0, 1): the top 53 bits of one word, scaled
// by 2^-53 (exact).
func (s *sampler) unit() float64 {
	return float64(s.src.Uint64()>>11) * 0x1p-53
}

// below is a uniform integer on [0, n), n >= 1, by Lemire's
// multiply-and-reject (unbiased).
func (s *sampler) below(n uint64) uint64 {
	hi, lo := bits.Mul64(s.src.Uint64(), n)
	if lo < n {
		thresh := (0 - n) % n
		for lo < thresh {
			hi, lo = bits.Mul64(s.src.Uint64(), n)
		}
	}
	return hi
}

// rangeValue maps a unit draw u in [0, 1) onto a validated range:
// min + u·(max − min) under linear, exp(log min + u·(log max − log min))
// under log (linalg/detmath, portable). Every product is rounded
// before its add (float64(a*b)), so no architecture fuses it; the
// result is clamped into [min, max] against the final rounding.
func rangeValue(rg *types.SweepRange, u float64) float64 {
	lo, hi := *rg.Min, *rg.Max
	var v float64
	if rg.Scale == types.SweepScaleLog {
		a, b := detmath.Log(lo), detmath.Log(hi)
		v = detmath.Exp(a + float64(u*(b-a)))
	} else {
		v = lo + float64(u*(hi-lo))
	}
	if v < lo {
		v = lo
	}
	if v > hi {
		v = hi
	}
	return v
}

// sampledNumber is a drawn value as an axis value: a json.Number in its
// shortest round-trip form, so `{{axis}}` renders it and `$var` splices
// it exactly.
func sampledNumber(v float64) json.Number {
	return json.Number(strconv.FormatFloat(v, 'g', -1, 64))
}

// forEachSample walks a validated sampling spec's spec.Samples slots in
// order, handing each one's 0-based ordinal and axis → value map to
// visit until it returns false. The draw order is part of the contract
// (a seed replays it):
//
//   - random: slot by slot, one unit draw per axis in declared order;
//   - lhs: axis by axis in declared order, first a Fisher–Yates
//     permutation of the n strata (j from n−1 down to 1, swap j with a
//     uniform k in [0, j]), then one jitter draw per slot; slot i takes
//     u = (perm[i] + jitter[i]) / n, so every stratum [k/n, (k+1)/n) of
//     every axis is drawn exactly once.
//
// Each u is mapped by rangeValue.
func forEachSample(spec *types.SweepSpec, seed int64, visit func(slot int, values map[string]any) bool) {
	n := spec.Samples
	rng := newSampler(seed)
	var lhs [][]float64
	if spec.Mode == types.SweepModeLHS {
		lhs = rng.latinHypercube(n, len(spec.Axes))
	}
	for slot := 0; slot < n; slot++ {
		values := make(map[string]any, len(spec.Axes))
		for a, ax := range spec.Axes {
			var u float64
			if lhs != nil {
				u = lhs[a][slot]
			} else {
				u = rng.unit()
			}
			values[ax.Name] = sampledNumber(rangeValue(ax.Range, u))
		}
		if !visit(slot, values) {
			return
		}
	}
}

// latinHypercube draws n points of a dims-dimensional Latin hypercube
// on [0, 1), dimension by dimension (the lhs draw order forEachSample
// documents): out[a][i] is point i's unit coordinate on dimension a.
func (s *sampler) latinHypercube(n, dims int) [][]float64 {
	out := make([][]float64, dims)
	for a := range out {
		perm := make([]int, n)
		for i := range perm {
			perm[i] = i
		}
		for j := n - 1; j > 0; j-- {
			k := int(s.below(uint64(j) + 1))
			perm[j], perm[k] = perm[k], perm[j]
		}
		us := make([]float64, n)
		for i := range us {
			us[i] = (float64(perm[i]) + s.unit()) / float64(n)
		}
		out[a] = us
	}
	return out
}

// LatinHypercube is the lhs sweep's draw — the same PCG stream (seed,
// seed ^ PCGStream) and the same order: dimension by dimension, a
// Fisher–Yates permutation of the n strata, then one jitter draw per
// point — for any seeded multi-point design (a Compose optimize block's
// starts). out[a][i] is point i's coordinate on dimension a, in [0, 1),
// each stratum [k/n, (k+1)/n) of every dimension drawn exactly once.
// Bit-identical on every architecture. n < 1 or dims < 1 draws nothing.
func LatinHypercube(seed int64, n, dims int) [][]float64 {
	if n < 1 || dims < 1 {
		return nil
	}
	return newSampler(seed).latinHypercube(n, dims)
}
