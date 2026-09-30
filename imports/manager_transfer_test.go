package imports

import (
	"context"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/spf13/afero"
)

// TestManager_Open_TransferArtifactRefused: a zstd transfer artifact —
// under its conventional .pulse.zst name, or renamed to .pulse so it
// would otherwise pass straight through — is refused with
// PULSE_COHORT_COMPRESSED, never PULSE_IMPORT_FORMAT_UNKNOWN or a
// passthrough handle every read would then reject.
func TestManager_Open_TransferArtifactRefused(t *testing.T) {
	m, afs, _ := newTestManager(t)
	artifact := append(encoding.ZstdMagic[:], 0x24, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00)
	for _, p := range []string{"c.pulse.zst", "renamed.pulse"} {
		if err := afero.WriteFile(afs, p, artifact, 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := m.Open(context.Background(), Spec{SourcePath: p})
		if !perr.HasCode(err, perr.PULSE_COHORT_COMPRESSED) {
			t.Fatalf("%s: err = %v, want PULSE_COHORT_COMPRESSED", p, err)
		}
	}
	// A plain unknown extension still reads as an unknown format.
	_ = afero.WriteFile(afs, "notes.txt", []byte("hello"), 0o644)
	if _, err := m.Open(context.Background(), Spec{SourcePath: "notes.txt"}); !perr.HasCode(err, perr.PULSE_IMPORT_FORMAT_UNKNOWN) {
		t.Fatalf("notes.txt: err = %v, want PULSE_IMPORT_FORMAT_UNKNOWN", err)
	}
}
