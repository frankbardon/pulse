package reprosum

import (
	"math"
	"testing"
)

type goldenCase struct {
	name string
	sum  func() Sum
}

// goldenCases are the frozen inputs of TestReproSum_GoldenBits. The
// generators are FMA-free (math.Ldexp and conversions only), so every
// architecture draws the same inputs; the weighted case goes through
// AddWeighted, whose float64(w*x) is the barrier under test.
func goldenCases() []goldenCase {
	var cs []goldenCase
	for _, c := range corpora() {
		cs = append(cs, goldenCase{c.name, func() Sum { return sumOf(c.xs) }})
	}
	cs = append(cs, goldenCase{"weighted", func() Sum {
		r := newRand(500)
		var s Sum
		for range 4000 {
			w := math.Ldexp(1+r.Float64(), r.IntN(6))
			x := signed(r, math.Ldexp(1+r.Float64(), r.IntN(40)-20))
			s.AddWeighted(w, x)
		}
		return s
	}})
	cs = append(cs, goldenCase{"merge_tree", func() Sum {
		return randomPartitionMerge(newRand(600), mixedCorpus(601, 3000))
	}})
	return cs
}

// goldenBits is the frozen result per case: Float64 bits, then the
// canonical state (3 primaries, 3 carries, pending, flags). It was
// computed on darwin/arm64 and checked identical under linux/amd64
// (docker); CI re-runs it on linux/amd64 and linux/arm64.
var goldenBits = []struct {
	name  string
	sum   uint64
	state [8]uint64
}{
	{"uniform", 0x40a3e19ab4a7a12b, [8]uint64{0x4248000004f866a4, 0x3fc80929e84ac0c4, 0x3d48000000000000, 0x0000000000000000, 0x0000000000000000, 0x0000000000000000, 0, 0}},
	{"signed", 0xc15f39c2da25417e, [8]uint64{0x424bffc18c7a4bb6, 0x3fcbff7d03921f4c, 0x3d48000000000000, 0xbff0000000000000, 0xbff0000000000000, 0x0000000000000000, 0, 0}},
	{"mixed_1e-300_1e300", 0x7e3001433751b13d, [8]uint64{0x7fe80000000007ff, 0x7e4801a19ba8d89e, 0x7bc8008ea81dacba, 0x0000000000000000, 0x0000000000000000, 0x0000000000000000, 0, 0}},
	{"cancellation", 0x405251f680000000, [8]uint64{0x4748000000000000, 0x44c8000000000000, 0x424800000024a3ed, 0x0000000000000000, 0x0000000000000000, 0x0000000000000000, 0, 0}},
	{"subnormal", 0x8062d55b5cc0c000, [8]uint64{0x014bffb4aa928cfd, 0x0148000000000000, 0x0148000000000000, 0xbff0000000000000, 0x0000000000000000, 0x0000000000000000, 0, 0}},
	{"signed_zeros", 0xc0734d9fe08efa67, [8]uint64{0x424bffffff6592f7, 0x3fc809fb882cc898, 0x3d48000000000000, 0xbff0000000000000, 0x0000000000000000, 0x0000000000000000, 0, 0}},
	{"subnormal_and_normal", 0x40f40335dc35e286, [8]uint64{0x42480000a019aee6, 0x3fcbfbaf142e876c, 0x3d48000000000000, 0x0000000000000000, 0xbff0000000000000, 0x0000000000000000, 0, 0}},
	{"top_bin_ties", 0x7edde28c80000000, [8]uint64{0x7fe80000003bc51e, 0x7e4bfb0000000000, 0x7bc8000000000000, 0x0000000000000000, 0xbff0000000000000, 0x0000000000000000, 0, 0}},
	{"near_max", 0x7fe5555555555554, [8]uint64{0x7fe8005555555555, 0x7e48005552000000, 0x7bc8000000000000, 0x0000000000000000, 0x0000000000000000, 0x0000000000000000, 0, 0}},
	{"weighted", 0x4199f70dc3441db6, [8]uint64{0x44c8000000000004, 0x424bff3ee1b86866, 0x3fc81db6b3102417, 0x0000000000000000, 0xbff0000000000000, 0x0000000000000000, 0, 0}},
	{"merge_tree", 0x7e1742cd88b6a539, [8]uint64{0x7fe80000000002e8, 0x7e480059b116d4a8, 0x7bcbff19b13a43ab, 0x0000000000000000, 0x0000000000000000, 0xbff0000000000000, 0, 0}},
}

// TestReproSum_GoldenBits is the cross-architecture gate: the kernel's
// arithmetic is adds, power-of-two scalings and float64()-barriered
// products, so these bits must hold on every GOARCH.
func TestReproSum_GoldenBits(t *testing.T) {
	t.Parallel()

	cases := goldenCases()
	if len(cases) != len(goldenBits) {
		t.Fatalf("%d golden cases, %d frozen results", len(cases), len(goldenBits))
	}
	for i, c := range cases {
		g := goldenBits[i]
		if c.name != g.name {
			t.Fatalf("case %d is %q, frozen %q", i, c.name, g.name)
		}
		s := c.sum()
		if got := math.Float64bits(s.Float64()); got != g.sum {
			t.Errorf("%s: Float64 bits %#016x, golden %#016x", c.name, got, g.sum)
		}
		if got := stateBits(s); got != g.state {
			t.Errorf("%s: state %#016x, golden %#016x", c.name, got, g.state)
		}
	}
}
