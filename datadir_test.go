package pulse_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/spf13/afero"
)

// relativeDataDirFixture writes a cohort under <tmp>/data, makes <tmp>
// the working directory and returns it.
func relativeDataDirFixture(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	if err := os.Mkdir(filepath.Join(base, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	buildCohortFile(t, afero.NewOsFs(), filepath.Join(base, "data", "c.pulse"))
	t.Chdir(base)
	return base
}

// TestNew_RelativeDataDirSurvivesCwdChange pins #308: a relative
// Options.DataDir is made absolute at New, so the instance keeps reading
// the same directory after the process changes its working directory.
func TestNew_RelativeDataDirSurvivesCwdChange(t *testing.T) {
	relativeDataDirFixture(t)
	p, err := pulse.New(pulse.Options{DataDir: "data"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Chdir(t.TempDir())
	if _, err := p.CountRecords(context.Background(), "c.pulse"); err != nil {
		t.Fatalf("CountRecords after chdir: %v", err)
	}
}

// TestNew_RelativeEnvDataDirSurvivesCwdChange is the PULSE_DATA_DIR twin.
func TestNew_RelativeEnvDataDirSurvivesCwdChange(t *testing.T) {
	relativeDataDirFixture(t)
	t.Setenv("PULSE_DATA_DIR", "data")
	p, err := pulse.New(pulse.Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Chdir(t.TempDir())
	if _, err := p.CountRecords(context.Background(), "c.pulse"); err != nil {
		t.Fatalf("CountRecords after chdir: %v", err)
	}
}
