package synth_test

import (
	"bytes"
	"testing"

	isynth "github.com/frankbardon/pulse/internal/synth"
	"github.com/frankbardon/pulse/synth"
	"github.com/spf13/afero"
)

const minimalSpec = `{
  "row_count": 64,
  "fields": [
    {"name": "id", "type": "u32", "distribution": "monotonic_from", "params": {"start": 1}},
    {"name": "value", "type": "f64", "distribution": "uniform", "params": {"min": 0, "max": 1}}
  ]
}`

// TestFacade_ForwardsAreByteIdentical pins that every forward in the
// public facade reaches the internal generator unchanged: the same spec
// and seed through the facade and through internal/synth yield the same
// bytes, and the file / profile / spec round trip stays on that path.
func TestFacade_ForwardsAreByteIdentical(t *testing.T) {
	spec, err := synth.ParseSpec([]byte(minimalSpec))
	if err != nil {
		t.Fatalf("ParseSpec: %v", err)
	}
	opts := synth.Options{Seed: 42}

	pub, res, err := synth.SynthBytes(spec, opts)
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	if res.RowsGenerated != 64 {
		t.Fatalf("RowsGenerated = %d, want 64", res.RowsGenerated)
	}
	internal, _, err := isynth.SynthBytes(spec, opts)
	if err != nil {
		t.Fatalf("internal SynthBytes: %v", err)
	}
	if !bytes.Equal(pub, internal) {
		t.Fatal("facade SynthBytes diverged from internal/synth")
	}

	fs := afero.NewMemMapFs()
	if _, err := synth.Synth(fs, spec, "out.pulse", opts); err != nil {
		t.Fatalf("Synth: %v", err)
	}
	onDisk, err := afero.ReadFile(fs, "out.pulse")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(onDisk, pub) {
		t.Fatal("Synth file differs from SynthBytes output")
	}

	prof, err := synth.ProfileFile(fs, "out.pulse", synth.ProfileOptions{})
	if err != nil {
		t.Fatalf("ProfileFile: %v", err)
	}
	profB, err := synth.ProfileBytes(pub, synth.ProfileOptions{})
	if err != nil {
		t.Fatalf("ProfileBytes: %v", err)
	}
	if len(prof.Fields) != 2 || len(profB.Fields) != 2 {
		t.Fatalf("profile fields = %d / %d, want 2", len(prof.Fields), len(profB.Fields))
	}

	derived, _ := synth.SpecFromProfile(prof, 10)
	if derived == nil || derived.RowCount != 10 {
		t.Fatalf("SpecFromProfile row count = %+v", derived)
	}
	if err := synth.WriteSpec(fs, derived, "spec.json"); err != nil {
		t.Fatalf("WriteSpec: %v", err)
	}
	raw, err := afero.ReadFile(fs, "spec.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := synth.ParseSpec(raw); err != nil {
		t.Fatalf("ParseSpec(WriteSpec output): %v", err)
	}

	// The vocabulary constants stay usable as their kept field values.
	fs2 := synth.FieldSpec{Distribution: synth.DistNormal}
	if fs2.Distribution != isynth.DistNormal {
		t.Fatal("DistNormal re-declaration diverged")
	}
	var _ *synth.FidelityReport = (*isynth.FidelityReport)(nil)
}
