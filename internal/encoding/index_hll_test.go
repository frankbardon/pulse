package encoding

import (
	"encoding/base64"
	"encoding/binary"
	"hash/crc32"
	"testing"
)

// index_hll_test.go pins the per-shard distinct-key sketch (U73 #498):
// estimates within the documented error, merge = union, the wire form's
// round trip and its refusals, and frozen estimates so a cross-arch
// drift in the registers or the estimator fails here.

// hllKey is the 8-byte on-wire key of a u64 id.
func hllKey(id uint64) []byte {
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], id)
	return b[:]
}

func hllOf(lo, hi uint64) *IndexHLL {
	h := NewIndexHLL()
	for id := lo; id < hi; id++ {
		h.Add(hllKey(id))
	}
	return h
}

// hllTolerance is three standard errors of the documented 1.04/sqrt(m)
// relative error at p = 10.
const hllTolerance = 3 * 0.0325

func TestIndexHLL_EstimateWithinDocumentedError(t *testing.T) {
	t.Parallel()
	for _, n := range []uint64{1, 10, 100, 1000, 3000, 10000, 50000, 200000} {
		got := hllOf(0, n).Estimate()
		rel := (float64(got) - float64(n)) / float64(n)
		if rel < -hllTolerance || rel > hllTolerance {
			t.Errorf("n=%d: estimate %d, relative error %.4f outside ±%.4f", n, got, rel, hllTolerance)
		}
	}
	if got := NewIndexHLL().Estimate(); got != 0 {
		t.Errorf("empty sketch estimate = %d, want 0", got)
	}
}

// TestIndexHLL_FrozenEstimates pins exact estimates: the registers are
// integer-only and the estimator FMA-free, so these are identical on
// arm64 and amd64 — a drift is a determinism regression.
func TestIndexHLL_FrozenEstimates(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		n    uint64
		want int
	}{{100, frozenHLL100}, {10000, frozenHLL10000}, {200000, frozenHLL200000}} {
		if got := hllOf(0, c.n).Estimate(); got != c.want {
			t.Errorf("n=%d: estimate %d, frozen %d", c.n, got, c.want)
		}
	}
}

const (
	frozenHLL100    = 99
	frozenHLL10000  = 9979
	frozenHLL200000 = 202128
)

func TestIndexHLL_MergeIsUnion(t *testing.T) {
	t.Parallel()
	a, b := hllOf(0, 3000), hllOf(1500, 4500)
	a.Merge(b)
	if want := hllOf(0, 4500); a.reg != want.reg {
		t.Fatal("merged sketch differs from the sketch of the union")
	}
}

func TestIndexHLL_EncodeRoundTrip(t *testing.T) {
	t.Parallel()
	h := hllOf(0, 5000)
	h.reg[0], h.reg[indexHLLRegisters-1] = indexHLLMaxRank, 1
	s := h.Encode()
	if len(s) != 1028 {
		t.Fatalf("encoded length %d, want 1028", len(s))
	}
	got, ok := DecodeIndexHLL(s)
	if !ok || got.reg != h.reg {
		t.Fatal("round trip changed the registers")
	}
}

func TestIndexHLL_DecodeRefusesMalformed(t *testing.T) {
	t.Parallel()
	good, _ := base64.StdEncoding.DecodeString(hllOf(0, 10).Encode())
	enc := func(b []byte) string { return base64.StdEncoding.EncodeToString(b) }
	wrongP := append([]byte{}, good...)
	wrongP[0] = 12
	highReg := append([]byte{}, good...)
	highReg[1] = 0xff // register 0 = 63 > 55
	for name, s := range map[string]string{
		"empty":           "",
		"not base64":      "!!!",
		"short":           enc(good[:len(good)-1]),
		"other precision": enc(wrongP),
		"register > 55":   enc(highReg),
	} {
		if _, ok := DecodeIndexHLL(s); ok {
			t.Errorf("%s: decoded, want refused", name)
		}
	}
}

func TestIndexDistinctKeysEstimate_MergesAndClamps(t *testing.T) {
	t.Parallel()
	rec := func(lo, hi uint64) IndexManifestShard {
		return IndexManifestShard{DistinctKeys: int(hi - lo), HLL: hllOf(lo, hi).Encode()}
	}
	shards := []IndexManifestShard{rec(0, 3000), rec(1500, 4500), rec(3000, 6000)}
	got, ok := IndexDistinctKeysEstimate(shards)
	if !ok {
		t.Fatal("estimate unknown")
	}
	if rel := (float64(got) - 6000) / 6000; rel < -hllTolerance || rel > hllTolerance {
		t.Errorf("union estimate %d, true 6000 (sum 9000)", got)
	}

	// One shard: the clamp to [max, sum] makes it exact.
	if got, ok := IndexDistinctKeysEstimate(shards[:1]); !ok || got != 3000 {
		t.Errorf("one shard: estimate %d (ok %v), want exact 3000", got, ok)
	}
	// The clamp bounds: never above the per-shard sum, never below the
	// largest per-shard count (records whose counts disagree with their
	// sketches expose each bound).
	over := []IndexManifestShard{
		{DistinctKeys: 10, HLL: hllOf(0, 1000).Encode()},
		{DistinctKeys: 10, HLL: hllOf(1000, 2000).Encode()},
	}
	if got, _ := IndexDistinctKeysEstimate(over); got != 20 {
		t.Errorf("upper clamp: estimate %d, want the sum 20", got)
	}
	under := []IndexManifestShard{
		{DistinctKeys: 5000, HLL: hllOf(0, 10).Encode()},
		{DistinctKeys: 7, HLL: hllOf(0, 7).Encode()},
	}
	if got, _ := IndexDistinctKeysEstimate(under); got != 5000 {
		t.Errorf("lower clamp: estimate %d, want the largest shard 5000", got)
	}
	// An empty shard records a sketch, so the estimate stays known.
	empty := IndexManifestShard{HLL: NewIndexHLL().Encode()}
	if got, ok := IndexDistinctKeysEstimate([]IndexManifestShard{empty}); !ok || got != 0 {
		t.Errorf("empty shard: estimate %d (ok %v), want known 0", got, ok)
	}
}

func TestIndexDistinctKeysEstimate_AbsentSketchIsUnknown(t *testing.T) {
	t.Parallel()
	known := IndexManifestShard{DistinctKeys: 10, HLL: hllOf(0, 10).Encode()}
	for name, shards := range map[string][]IndexManifestShard{
		"no shards":         nil,
		"one lacks":         {known, {DistinctKeys: 5}},
		"one malformed":     {known, {DistinctKeys: 5, HLL: "AAAA"}},
		"all lack (legacy)": {{DistinctKeys: 10}},
	} {
		if got, ok := IndexDistinctKeysEstimate(shards); ok {
			t.Errorf("%s: estimate %d known, want unknown", name, got)
		}
	}
}

// TestIndexHLL_FrozenWireForm pins the manifest string's bytes (the
// CRC32 of the encoding of ids 0..999): the hash, the register layout
// and the packing are a persisted format.
func TestIndexHLL_FrozenWireForm(t *testing.T) {
	t.Parallel()
	if got := crc32.ChecksumIEEE([]byte(hllOf(0, 1000).Encode())); got != frozenHLLWireCRC {
		t.Errorf("wire form CRC32 %#x, frozen %#x", got, frozenHLLWireCRC)
	}
}

const frozenHLLWireCRC = 0xc5e170f4
