package service

import (
	"testing"

	"github.com/frankbardon/pulse/internal/synth"
	"github.com/spf13/afero"
)

// TestJoinShapeContinuation_ProfilePredictsRunSkip pins the contract of
// `profile create --run-continuation`: its overall figure is the run-skip
// decode's hit rate, byte for byte. It is checked against
// joinShapeContinuation — the same byte-level definition E2-S1's
// measurements use — on both the sorted join-shape fixture and its
// scattered copy, and each field's rate must agree with the overall.
func TestJoinShapeContinuation_ProfilePredictsRunSkip(t *testing.T) {
	fsys, path, schema, rows := loadJoinShapeFixture(t)
	sorted, err := afero.ReadFile(fsys, path)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		data []byte
		high bool
	}{
		{"sorted", sorted, true},
		{"scattered", scatterJoinShapeCohort(sorted, schema, rows), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prof, err := synth.ProfileBytes(tc.data, synth.ProfileOptions{RunContinuation: true})
			if err != nil {
				t.Fatalf("ProfileBytes: %v", err)
			}
			rc := prof.RunContinuation
			want := joinShapeContinuation(tc.data, schema, rows)
			t.Logf("%s: profile overall %.4f, run-skip definition %.4f, %d high fields",
				tc.name, rc.Overall, want, len(rc.HighFields))
			if rc.Overall != want {
				t.Fatalf("overall = %v, run-skip hit rate = %v", rc.Overall, want)
			}
			if rc.Pairs != rows-1 || len(rc.Fields) != len(schema.Fields) {
				t.Fatalf("pairs = %d fields = %d, want %d / %d", rc.Pairs, len(rc.Fields), rows-1, len(schema.Fields))
			}
			sum := 0.0
			for _, f := range rc.Fields {
				sum += f.Rate * float64(rc.Pairs)
			}
			if got := sum / float64(rc.Pairs*len(rc.Fields)); got < want-1e-12 || got > want+1e-12 {
				t.Fatalf("per-field rates average to %v, overall %v", got, want)
			}
			if tc.high {
				// The sorted fixture's parent block is the high block.
				if len(rc.HighFields) < joinShapeParentFields {
					t.Fatalf("sorted: %d high fields, want at least the %d-field parent block", len(rc.HighFields), joinShapeParentFields)
				}
				for _, name := range rc.HighFields[:joinShapeParentFields] {
					if name[:2] != "p_" {
						t.Fatalf("sorted: high block led by %q, want the parent block", name)
					}
				}
			} else if len(rc.HighFields) >= joinShapeParentFields {
				t.Fatalf("scattered: %d high fields — the parent block should not survive a shuffle", len(rc.HighFields))
			}
		})
	}
}

// BenchmarkJoinShapeProfile_RunContinuation measures what
// --run-continuation adds to a profile scan of the join-shape fixture.
func BenchmarkJoinShapeProfile_RunContinuation(b *testing.B) {
	fsys, path, _, _ := loadJoinShapeFixture(b)
	data, err := afero.ReadFile(fsys, path)
	if err != nil {
		b.Fatal(err)
	}
	for _, on := range []bool{false, true} {
		name := "off"
		if on {
			name = "on"
		}
		b.Run(name, func(b *testing.B) {
			for b.Loop() {
				if _, err := synth.ProfileBytes(data, synth.ProfileOptions{RunContinuation: on}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
