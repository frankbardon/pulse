package linalg_test

import (
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/linalg"
)

// stateWords sums the 8-byte int64 / float64 fields of a struct type —
// the running-state payload words, leaving out the dimension, mode and
// slice headers the predict estimate deliberately does not count.
func stateWords(t *testing.T, typ reflect.Type) int64 {
	t.Helper()
	var words int64
	for i := range typ.NumField() {
		switch typ.Field(i).Type.Kind() {
		case reflect.Int64, reflect.Float64:
			words++
		}
	}
	return words
}

// TestAccumulatorBytesMatchesCoMomentLayout ties predict's per-state
// estimate (vectors.Matrix.AccumulatorBytes, which internal/descriptor
// may import while linalg's internals stay private) to the struct it
// sizes: the header words are CoMoment's int64 / float64 fields and a
// pairwise cell is one pairMoment, read by reflection, so adding a word
// to either fails here rather than drifting the estimate silently (it
// sat at 7 pair words against 9 real ones — TODO #157).
func TestAccumulatorBytesMatchesCoMomentLayout(t *testing.T) {
	co := reflect.TypeFor[linalg.CoMoment]()
	header := 8 * stateWords(t, co)
	pairs, ok := co.FieldByName("pairs")
	if !ok || pairs.Type.Kind() != reflect.Slice {
		t.Fatalf("CoMoment has no pairs slice: %v", pairs.Type)
	}
	pair := int64(pairs.Type.Elem().Size())
	if words := stateWords(t, pairs.Type.Elem()); 8*words != pair {
		t.Fatalf("pairMoment is %d bytes but holds %d 8-byte words", pair, words)
	}
	for _, p := range []int{1, 3, 50, 256} {
		cells := int64(p * (p + 1) / 2)
		m := vectors.Matrix{Members: vectors.Resolved{Members: make([]string, p)}}
		if got, want := m.AccumulatorBytes(), header+8*(int64(p)+cells); got != want {
			t.Errorf("listwise p=%d: AccumulatorBytes %d, CoMoment layout %d", p, got, want)
		}
		m.Pairwise = true
		if got, want := m.AccumulatorBytes(), header+pair*cells; got != want {
			t.Errorf("pairwise p=%d: AccumulatorBytes %d, CoMoment layout %d", p, got, want)
		}
	}
}
