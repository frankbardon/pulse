package mmmfixture

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
	"reflect"
	"testing"
)

// TestJointTruth_DeterministicShape: the joint-truth cohort is one row
// per week in schema field order, generated identically on every call,
// with each stored contribution β·hill(adstock) of its channel and the
// sales equal to the truth's sum within the noise (|residual| ≤ 5σ).
func TestJointTruth_DeterministicShape(t *testing.T) {
	t.Parallel()

	d, again := Joint(), Joint()
	if !reflect.DeepEqual(d, again) {
		t.Fatal("two Joint() calls differ: the generator is not deterministic")
	}
	if len(d.Rows) != JointWeeks {
		t.Fatalf("%d rows, want %d", len(d.Rows), JointWeeks)
	}
	fields := len(JointSchema().Fields)
	for i, r := range d.Rows {
		if len(r) != fields || r[0] != uint64(i+1) {
			t.Fatalf("row %d = %v: want %d values, week %d", i, r, fields, i+1)
		}
		sales := float64(JointIntercept) + JointPriceEffect*r[5].(float64) + JointPromoEffect*r[6].(float64)
		for c, ch := range JointChannels {
			if r[1+c] != d.Spend[ch.Name][i] {
				t.Fatalf("row %d %s spend %v, stored %v", i, ch.Name, r[1+c], d.Spend[ch.Name][i])
			}
			sales += d.Contribution[ch.Name][i]
		}
		if res := r[7].(float64) - sales; math.Abs(res) > 5*JointNoise {
			t.Fatalf("row %d: sales residual %v beyond 5σ", i, res)
		}
	}
	// The stored contribution is the truth's curve on its own adstock.
	tv := JointChannels[0]
	x := d.Spend[tv.Name]
	a := 0.0
	for l := 0; l <= JointMaxLag; l++ {
		a += math.Pow(tv.Decay, float64(l)) * x[20-l]
	}
	if got, want := d.Contribution[tv.Name][20], tv.Beta*JointHill(a, tv.HalfSat, tv.Shape); math.Abs(got-want) > 1e-9*want {
		t.Fatalf("tv contribution week 21 = %v, want %v", got, want)
	}
}

// jointDataHash is the sha256 of a generated cohort's bits: every row
// value (the week as its integer, every float64 as its IEEE-754 word),
// then every stored spend and contribution, channel by channel.
func jointDataHash(d JointData) string {
	h := sha256.New()
	word := func(u uint64) {
		var b [8]byte
		binary.LittleEndian.PutUint64(b[:], u)
		h.Write(b[:])
	}
	for _, r := range d.Rows {
		for _, v := range r {
			switch x := v.(type) {
			case uint64:
				word(x)
			case float64:
				word(math.Float64bits(x))
			default:
				panic(fmt.Sprintf("row value %T", v))
			}
		}
	}
	for _, c := range JointChannels {
		for _, v := range d.Spend[c.Name] {
			word(math.Float64bits(v))
		}
		for _, v := range d.Contribution[c.Name] {
			word(math.Float64bits(v))
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

// jointFrozenHash is jointDataHash(Joint()), frozen when JointWithSeed
// was split out of Joint (bayesian-mmm E4-S1) against the generator
// before the split: Joint's bits — and so every operator test built on
// it — did not move. The generator is FMA-free and detmath-only,
// so the hash holds on every architecture.
const jointFrozenHash = "687a955c1b785f0f8eedcf0d7f856c1c5d2f07b3b124fe40c914a1a33faa3ae2"

// TestJointTruth_FrozenBits: Joint() is byte-identical to the
// pre-JointWithSeed generator and equals JointWithSeed(JointSeed).
func TestJointTruth_FrozenBits(t *testing.T) {
	t.Parallel()

	if got := jointDataHash(Joint()); got != jointFrozenHash {
		t.Errorf("Joint() hash %s, frozen %s: the joint fixture's bits moved", got, jointFrozenHash)
	}
	if !reflect.DeepEqual(Joint(), JointWithSeed(JointSeed)) {
		t.Error("JointWithSeed(JointSeed) differs from Joint()")
	}
}

// TestJointTruth_WithSeed: a replicate seed draws fresh data over the
// same truth — deterministic per seed, distinct across seeds and from
// Joint(), every row the truth's sum within the noise, and every
// channel spending (so a replicate's ROI is defined).
func TestJointTruth_WithSeed(t *testing.T) {
	t.Parallel()

	seen := map[string]uint64{jointDataHash(Joint()): JointSeed}
	for _, seed := range []uint64{1, 2, 1001, 1050} {
		d := JointWithSeed(seed)
		if !reflect.DeepEqual(d, JointWithSeed(seed)) {
			t.Fatalf("seed %d: two calls differ", seed)
		}
		h := jointDataHash(d)
		if prev, dup := seen[h]; dup {
			t.Fatalf("seed %d draws the same data as seed %d", seed, prev)
		}
		seen[h] = seed
		if len(d.Rows) != JointWeeks {
			t.Fatalf("seed %d: %d rows, want %d", seed, len(d.Rows), JointWeeks)
		}
		for i, r := range d.Rows {
			sales := float64(JointIntercept) + float64(JointPriceEffect*r[5].(float64)) + float64(JointPromoEffect*r[6].(float64))
			for c, ch := range JointChannels {
				if r[1+c] != d.Spend[ch.Name][i] {
					t.Fatalf("seed %d row %d %s spend %v, stored %v", seed, i, ch.Name, r[1+c], d.Spend[ch.Name][i])
				}
				sales += d.Contribution[ch.Name][i]
			}
			if r[0] != uint64(i+1) || math.Abs(r[7].(float64)-sales) > 5*JointNoise {
				t.Fatalf("seed %d row %d = %v: wrong week or sales beyond 5σ of the truth", seed, i, r)
			}
		}
		for _, c := range JointChannels {
			if roi := d.TrueROI(c.Name); !(roi > 0) || math.IsInf(roi, 0) {
				t.Errorf("seed %d %s true ROI %v: want a positive finite ROI", seed, c.Name, roi)
			}
		}
	}
}
