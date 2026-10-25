package regresample

import (
	"encoding/binary"
	"hash/fnv"
	"math"
)

// PCGStream is the second PCG word every seeded draw in Pulse pairs
// with its seed: rand.NewPCG(seed, seed^PCGStream).
const PCGStream = 0x9E3779B97F4A7C15

// DeriveSeed is the ONE rng_seed 0 derivation every seeded draw shares
// (REG_RESAMPLE's bootstrap and jackknife groups, MAT_SEM's bootstrap,
// the Compose sampling sweep): an FNV-1a hash of spec — the slot's wire
// form with its seed zeroed or dropped — then the row count, then every
// value cells yields, in row order, as its little-endian float64 bits
// (a null, ok false, as a NaN payload no value carries). A pure
// function of the request and the rows: the same request over the same
// data draws the same replicates whatever the worker or shard count, as
// long as the rows arrive in record order (a buffered slot's do). A nil
// cells yields no value.
//
// It lives here, below both the engine and the no-execute callers
// (internal/sweep, predict), so every surface derives the identical
// seed.
func DeriveSeed(spec []byte, rows int, cells func(word func(v float64, ok bool))) int64 {
	h := fnv.New64a()
	_, _ = h.Write(spec)
	var buf [8]byte
	put := func(u uint64) {
		binary.LittleEndian.PutUint64(buf[:], u)
		_, _ = h.Write(buf[:])
	}
	put(uint64(rows))
	if cells != nil {
		cells(func(v float64, ok bool) {
			if !ok {
				put(0x7ff8000000000001) // a null: a NaN payload no value carries
				return
			}
			put(math.Float64bits(v))
		})
	}
	return int64(h.Sum64())
}
