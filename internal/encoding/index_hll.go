package encoding

import (
	"encoding/base64"
	"math"
	"math/bits"
)

// index_hll.go is the per-shard distinct-key sketch of an archive point
// lookup index (U73 #498): a HyperLogLog over the composite keys one
// shard's sidecar holds, recorded on that shard's manifest record
// (IndexManifestShard.HLL, "hll", omitempty) and merged across the
// covered shards into the index's union distinct-key ESTIMATE. The
// per-shard DistinctKeys stay exact and their entry-level sum stays a
// sum (a key in k shards counts k times); the sketch is the only figure
// that does not double-count.
//
// Precision p = IndexHLLPrecision = 10: m = 1024 registers, a relative
// standard error of 1.04/sqrt(m) ≈ 3.25% at every cardinality (the
// estimator below has no small- or mid-range bias to correct). Each
// register is 6 bits (its value is at most 64-p+1 = 55), packed four to
// three bytes, so a sketch is 1 + 768 bytes and its base64 manifest
// string 1028 characters whatever the shard's size.
//
// Integer-only up to the estimate, so the registers are bit-identical
// on every architecture: x = splitmix64(HashKey(key)) (the FNV-1a 64
// bucket hash, re-mixed as the Bloom's h1 is); register = the top p
// bits of x; value = 1 + the leading zeros of the remaining 64-p bits,
// capped at 64-p+1. Merging two sketches takes each register's max, so
// the merged sketch is exactly the sketch of the union of their keys.
// The estimate (Ertl 2017, "New cardinality estimation algorithms for
// HyperLogLog sketches", the improved raw estimator) uses only IEEE
// add, multiply, divide and sqrt, each product written behind a
// float64() barrier so no architecture fuses it — the same register
// state yields the same estimate on arm64 and amd64.
//
// Wire form ("hll"): standard base64 of [precision byte][768 packed
// bytes], registers big-endian bit-packed in register order. Anything
// else — another precision, another length, a register above 55, bad
// base64 — decodes as "no sketch", and no sketch means the estimate is
// unknown, never zero.

// IndexHLLPrecision is the per-shard sketch's precision p (m = 2^p
// registers).
const IndexHLLPrecision = 10

const (
	indexHLLRegisters = 1 << IndexHLLPrecision
	indexHLLMaxRank   = 64 - IndexHLLPrecision + 1
	indexHLLPacked    = indexHLLRegisters * 6 / 8
)

// IndexHLL is one HyperLogLog sketch at IndexHLLPrecision.
type IndexHLL struct {
	reg [indexHLLRegisters]uint8
}

// NewIndexHLL returns an empty sketch (estimate 0).
func NewIndexHLL() *IndexHLL { return &IndexHLL{} }

// Add records one composite on-wire key.
func (h *IndexHLL) Add(key []byte) {
	x := splitmix64(HashKey(key))
	j := x >> (64 - IndexHLLPrecision)
	rank := bits.LeadingZeros64(x<<IndexHLLPrecision) + 1
	if rank > indexHLLMaxRank {
		rank = indexHLLMaxRank
	}
	if uint8(rank) > h.reg[j] {
		h.reg[j] = uint8(rank)
	}
}

// Merge folds o into h: register-wise max, the sketch of the union.
func (h *IndexHLL) Merge(o *IndexHLL) {
	for i, v := range o.reg {
		if v > h.reg[i] {
			h.reg[i] = v
		}
	}
}

// IndexHLLFromBuckets sketches every key one shard's sidecar buckets
// hold — the set its DistinctKeys counts.
func IndexHLLFromBuckets(buckets []IndexBucket) *IndexHLL {
	h := NewIndexHLL()
	for _, b := range buckets {
		for _, en := range b.Entries {
			h.Add(en.Key)
		}
	}
	return h
}

// Encode returns the sketch's manifest string.
func (h *IndexHLL) Encode() string {
	raw := make([]byte, 1, 1+indexHLLPacked)
	raw[0] = IndexHLLPrecision
	for i := 0; i < indexHLLRegisters; i += 4 {
		a, b, c, d := h.reg[i], h.reg[i+1], h.reg[i+2], h.reg[i+3]
		raw = append(raw, a<<2|b>>4, b<<4|c>>2, c<<6|d)
	}
	return base64.StdEncoding.EncodeToString(raw)
}

// DecodeIndexHLL parses a manifest sketch string; ok is false for an
// empty or malformed one (absent: the estimate is unknown).
func DecodeIndexHLL(s string) (h *IndexHLL, ok bool) {
	if s == "" {
		return nil, false
	}
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil || len(raw) != 1+indexHLLPacked || raw[0] != IndexHLLPrecision {
		return nil, false
	}
	h = NewIndexHLL()
	p := raw[1:]
	for i, k := 0, 0; i < indexHLLRegisters; i, k = i+4, k+3 {
		x, y, z := p[k], p[k+1], p[k+2]
		h.reg[i] = x >> 2
		h.reg[i+1] = (x&0x03)<<4 | y>>4
		h.reg[i+2] = (y&0x0f)<<2 | z>>6
		h.reg[i+3] = z & 0x3f
	}
	for _, v := range h.reg {
		if v > indexHLLMaxRank {
			return nil, false
		}
	}
	return h, true
}

// Estimate is the sketch's distinct-count estimate (Ertl's improved
// raw estimator), rounded to the nearest integer.
func (h *IndexHLL) Estimate() int {
	var c [indexHLLMaxRank + 1]int
	for _, v := range h.reg {
		c[v]++
	}
	const m = float64(indexHLLRegisters)
	const q = indexHLLMaxRank - 1
	// float64() barriers: m is a power of two, so the compiler may turn
	// the division into a multiply it could otherwise fuse into tau's 1-x.
	z := m * hllTau(float64(float64(indexHLLRegisters-c[q+1])/m))
	for k := q; k >= 1; k-- {
		z = 0.5 * (z + float64(c[k]))
	}
	z += float64(m * hllSigma(float64(float64(c[0])/m)))
	// alpha_inf = 1 / (2 ln 2); the constant folds exactly at compile time.
	const num = m * m / (2 * math.Ln2)
	return int(math.Round(num / z))
}

// hllSigma is Ertl's sigma(x) = x + sum_{k>=1} x^(2^k) 2^(k-1).
func hllSigma(x float64) float64 {
	if x == 1 {
		return math.Inf(1)
	}
	y, z := 1.0, x
	for {
		x = float64(x * x)
		prev := z
		z += float64(x * y)
		y += y
		if z == prev {
			return z
		}
	}
}

// hllTau is Ertl's tau(x) = (1/3)(1 - x - sum_{k>=1} (1 - x^(2^-k))^2 2^-k).
func hllTau(x float64) float64 {
	if x == 0 || x == 1 {
		return 0
	}
	y, z := 1.0, 1-x
	for {
		x = math.Sqrt(x)
		prev := z
		y *= 0.5
		d := 1 - x
		z -= float64(float64(d*d) * y)
		if z == prev {
			return z / 3
		}
	}
}

// IndexDistinctKeysEstimate merges the sketches of every shard record
// an archive index entry covers into the union distinct-key estimate.
// ok is false when the entry covers no shard or ANY record carries no
// valid sketch (an index built before the sketch existed, or a manifest
// an older binary rewrote): an unknown estimate, never 0. The estimate
// is clamped to [the largest per-shard DistinctKeys, their sum] — the
// union can be neither smaller than any one shard's exact count nor
// larger than the sum — so a one-shard index reports its exact count.
func IndexDistinctKeysEstimate(shards []IndexManifestShard) (int, bool) {
	if len(shards) == 0 {
		return 0, false
	}
	u := NewIndexHLL()
	lo, hi := 0, 0
	for _, sh := range shards {
		h, ok := DecodeIndexHLL(sh.HLL)
		if !ok {
			return 0, false
		}
		u.Merge(h)
		hi += sh.DistinctKeys
		if sh.DistinctKeys > lo {
			lo = sh.DistinctKeys
		}
	}
	est := u.Estimate()
	if est < lo {
		est = lo
	}
	if est > hi {
		est = hi
	}
	return est, true
}
