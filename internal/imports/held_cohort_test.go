package imports

import (
	"context"
	stderrors "errors"
	"strings"
	"testing"
	"time"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/spf13/afero"
)

// heldFs refuses to Remove any `.pulse` file, the way Windows refuses
// to delete a cohort another reader still holds open or maps.
type heldFs struct{ afero.Fs }

var errHeld = stderrors.New("the process cannot access the file because it is being used by another process")

func (h heldFs) Remove(name string) error {
	if strings.HasSuffix(name, ".pulse") {
		return errHeld
	}
	return h.Fs.Remove(name)
}

func newHeldManager(t *testing.T) (*Manager, afero.Fs, *fixedClock) {
	t.Helper()
	afs := afero.NewMemMapFs()
	clk := &fixedClock{t: time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)}
	m, err := New(heldFs{afs}, Options{Now: clk.now})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return m, afs, clk
}

// TestManager_Sweep_HeldCohortKeepsSidecar: an expired handle whose
// cohort cannot be deleted (held by another reader) keeps its sidecar
// and is not reported swept, so the next sweep retries it rather than
// leaving an unmanaged orphan.
//
// Falsified by removing the sidecar regardless of the cohort's Remove
// result (the pre-fix order).
func TestManager_Sweep_HeldCohortKeepsSidecar(t *testing.T) {
	t.Parallel()

	m, afs, clk := newHeldManager(t)
	writeCSV(t, afs, "data.csv")
	res, err := m.Open(context.Background(), Spec{SourcePath: "data.csv", TTL: time.Hour})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	clk.advance(2 * time.Hour)

	swept, err := m.Sweep(context.Background())
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if len(swept) != 0 {
		t.Errorf("swept = %v, want none (the cohort is held)", swept)
	}
	if ok, _ := afero.Exists(afs, res.Path); !ok {
		t.Error("held cohort vanished")
	}
	if ok, _ := afero.Exists(afs, res.Path+SidecarSuffix); !ok {
		t.Error("held cohort's sidecar was removed: the handle is orphaned and never swept again")
	}
}

// TestManager_Drop_HeldCohortIsCoded: a Drop whose cohort cannot be
// deleted fails with a coded SERVICE_RESOURCE error, never the raw OS
// error, and leaves the sidecar in place.
//
// Falsified by returning the fmt-wrapped error.
func TestManager_Drop_HeldCohortIsCoded(t *testing.T) {
	t.Parallel()

	m, afs, _ := newHeldManager(t)
	writeCSV(t, afs, "data.csv")
	res, err := m.Open(context.Background(), Spec{SourcePath: "data.csv"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	err = m.Drop(context.Background(), "data")
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perr.SERVICE_RESOURCE {
		t.Fatalf("Drop error = %v, want SERVICE_RESOURCE", err)
	}
	if !stderrors.Is(err, errHeld) {
		t.Errorf("Drop error %v lost its cause", err)
	}
	if ok, _ := afero.Exists(afs, res.Path+SidecarSuffix); !ok {
		t.Error("sidecar removed although the cohort stayed")
	}
}
