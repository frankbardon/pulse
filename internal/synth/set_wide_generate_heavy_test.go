//go:build heavy

package synth_test

import (
	"math"
	"testing"

	"github.com/frankbardon/pulse/internal/synth"
)

// TestSynth_SetBernoulli_WideRungMarginalFrequencies is the story's
// central acceptance bar: per-element selection frequency must match
// the declared Bernoulli probability for members BELOW and ABOVE bit
// 64. A generator that quietly drops the high words still produces a
// valid-looking cohort — every high-bit option would simply read as
// never selected — so the probes straddle the word boundary at 63/64
// and reach the top rung's last bit.
func TestSynth_SetBernoulli_WideRungMarginalFrequencies(t *testing.T) {
	t.Parallel()

	const rowCount = 10000
	const tolerance = 0.03

	cases := []struct {
		typeName string
		options  int
		probe    map[int]float64
	}{
		{
			typeName: "set_u128",
			options:  128,
			probe: map[int]float64{
				0: 0.9, 3: 0.7, 63: 0.25, 64: 0.8, 65: 0.1, 127: 0.6,
			},
		},
		{
			typeName: "set_u256",
			options:  256,
			probe: map[int]float64{
				1: 0.85, 63: 0.2, 64: 0.75, 130: 0.35, 200: 0.65, 255: 0.45,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.typeName, func(t *testing.T) {
			params, names, freqs := wideSetParams(tc.options, 0.5, tc.probe)
			spec := &synth.Spec{
				RowCount: rowCount,
				Fields: []synth.FieldSpec{
					{Name: "features", Type: tc.typeName,
						Distribution: synth.DistSetBernoulli, Params: params},
				},
			}
			data, _, err := synth.SynthBytes(spec, synth.Options{Seed: 7})
			if err != nil {
				t.Fatalf("SynthBytes(%s): %v", tc.typeName, err)
			}

			masks, nullFlags, dict := readWideSetFieldRows(t, data, "features")
			if len(masks) != rowCount {
				t.Fatalf("row count = %d, want %d", len(masks), rowCount)
			}
			if dict.Count() != tc.options {
				t.Fatalf("dictionary size = %d, want %d", dict.Count(), tc.options)
			}

			counts := make([]int, tc.options)
			highBitSeen := false
			for i, m := range masks {
				if nullFlags[i] {
					t.Fatalf("row %d is null in a non-nullable spec", i)
				}
				if hb := m.HighestBit(); hb >= tc.options {
					t.Fatalf("row %d: mask has bit %d beyond declared option count %d",
						i, hb, tc.options)
				}
				if m.HighestBit() >= 64 {
					highBitSeen = true
				}
				for bit, ok := m.NextBit(0); ok; bit, ok = m.NextBit(bit + 1) {
					counts[bit]++
				}
			}
			if !highBitSeen {
				t.Fatalf("%s: no generated row ever set a bit at or above 64 — "+
					"the high words are being dropped", tc.typeName)
			}

			// Every probed index is checked, not just the high ones: an
			// implementation that shifted the whole mask would move the
			// low frequencies too.
			for idx := range tc.probe {
				got := float64(counts[idx]) / float64(rowCount)
				if math.Abs(got-freqs[idx]) > tolerance {
					t.Errorf("option %q (bit %d) frequency = %.4f, want within %.2f of declared %.2f",
						names[idx], idx, got, tolerance, freqs[idx])
				}
			}
			// The unprobed remainder must also sit at its declared base,
			// so a bug confined to "every bit except the ones the test
			// names" cannot pass.
			for idx := range names {
				if _, probed := tc.probe[idx]; probed {
					continue
				}
				got := float64(counts[idx]) / float64(rowCount)
				if math.Abs(got-0.5) > tolerance {
					t.Errorf("option %q (bit %d) frequency = %.4f, want within %.2f of base 0.50",
						names[idx], idx, got, tolerance)
				}
			}
		})
	}
}
