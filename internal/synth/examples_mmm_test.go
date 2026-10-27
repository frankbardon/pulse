package synth_test

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/frankbardon/pulse/internal/synth"
)

// TestSynthExamples_MMMAdstock generates the shipped MMM example and
// checks its three cross-row claims exactly: week is the accepted-row
// ordinal, true_adstock is spend + 0.6 * its own previous value (0 before
// row 1), and the Fourier controls are the portable sin/cos of the week.
func TestSynthExamples_MMMAdstock(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(filepath.Join("..", "examples", "synth", "07_mmm_adstock.synth.json"))
	if err != nil {
		t.Fatalf("read example: %v", err)
	}
	spec, err := synth.ParseSpec(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	data, _, err := synth.SynthBytes(spec, synth.Options{Seed: 3})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	week := readF64Field(t, data, "week")
	spend := readF64Field(t, data, "spend")
	ad := readF64Field(t, data, "true_adstock")
	sn := readF64Field(t, data, "fourier_sin")
	cs := readF64Field(t, data, "fourier_cos")
	if len(ad) != spec.RowCount {
		t.Fatalf("rows = %d, want %d", len(ad), spec.RowCount)
	}
	prev := 0.0
	varied := false
	for i := range ad {
		if week[i] != float64(i) {
			t.Fatalf("row %d: week = %v, want %d", i, week[i], i)
		}
		want := spend[i] + float64(0.6*prev)
		if math.Float64bits(ad[i]) != math.Float64bits(want) {
			t.Fatalf("row %d: true_adstock = %v, want %v", i, ad[i], want)
		}
		prev = want
		if i > 0 && spend[i] != spend[i-1] {
			varied = true
		}
		if sn[i] < -1 || sn[i] > 1 || cs[i] < -1 || cs[i] > 1 {
			t.Fatalf("row %d: Fourier control out of [-1,1]: %v %v", i, sn[i], cs[i])
		}
	}
	if !varied {
		t.Fatal("spend is constant; the recursion would test nothing")
	}
	// Week 13 is a quarter period: sin = 1, cos ~ 0.
	if math.Abs(sn[13]-1) > 1e-12 || math.Abs(cs[13]) > 1e-12 {
		t.Errorf("week 13: sin %v cos %v, want 1 and 0", sn[13], cs[13])
	}
}
