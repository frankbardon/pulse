//go:build heavy

package pulse_test

import (
	"path/filepath"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/spf13/afero"
)

// TestGroupKMeans_DecodeWorkersAgree: above the parallel-decode
// threshold, DecodeWorkers = 4 answers exactly as DecodeWorkers = 1.
func TestGroupKMeans_DecodeWorkersAgree(t *testing.T) {
	dir := t.TempDir()
	writeParityCohort(t, afero.NewOsFs(), filepath.Join(dir, "large.pulse"), paritySchema(t), 0, parityLargeRows)
	one, err := pulse.New(pulse.Options{DataDir: dir, DecodeWorkers: 1})
	if err != nil {
		t.Fatal(err)
	}
	four, err := pulse.New(pulse.Options{DataDir: dir, DecodeWorkers: 4})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := kmAnswer(t, four, "large.pulse"), kmAnswer(t, one, "large.pulse"); got != want {
		t.Errorf("DecodeWorkers=4 answers differently:\n got  %.600s\n want %.600s", got, want)
	}
}
