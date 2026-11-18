package pulse

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/imports"
	"github.com/spf13/afero"
)

const managedZoneCSV = "local,zone\n" +
	"2026-07-01 12:00,America/New_York\n" +
	"2026-07-01 12:00,Asia/Tokyo\n" +
	"2026-07-01 12:00,Europe/Berlin\n"

// sidecarRenameFailFs fails every rename onto a managed-import sidecar
// once armed — the publish step of an imports sidecar update.
type sidecarRenameFailFs struct {
	afero.Fs
	armed bool
}

func (f *sidecarRenameFailFs) Rename(oldname, newname string) error {
	if f.armed && strings.HasSuffix(newname, imports.SidecarSuffix) {
		return &os.LinkError{Op: "rename", Old: oldname, New: newname, Err: os.ErrPermission}
	}
	return f.Fs.Rename(oldname, newname)
}

// managedZoneEngine imports managedZoneCSV as the managed handle
// "rows" and returns its cohort path.
func managedZoneEngine(t *testing.T, fsys afero.Fs) (*Pulse, string) {
	t.Helper()
	if err := afero.WriteFile(fsys, "src.csv", []byte(managedZoneCSV), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := New(Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	res, err := p.ImportFile(context.Background(), ImportSpec{SourcePath: "src.csv", Handle: "rows"})
	if err != nil {
		t.Fatal(err)
	}
	return p, res.Path
}

// managedSidecar is the imports sidecar of handle "rows".
func managedSidecar(t *testing.T, p *Pulse) imports.Sidecar {
	t.Helper()
	entries, err := p.Imports(context.Background())
	if err != nil || len(entries) != 1 {
		t.Fatalf("imports = %v, %v; want the one handle", entries, err)
	}
	return entries[0].Sidecar
}

// reimportFromSidecar re-runs the import a sidecar records — the
// managed refresh — over the same handle.
func reimportFromSidecar(t *testing.T, p *Pulse, sc imports.Sidecar) string {
	t.Helper()
	res, err := p.ImportFile(context.Background(), ImportSpec{
		SourcePath: sc.SourcePath, Format: sc.SourceFormat, Handle: sc.Handle, Overwrite: true,
		ColumnTypeOverrides: sc.ColumnTypeOverrides, SourceTZ: sc.SourceTZ, ColumnSourceTZ: sc.ColumnSourceTZ,
		SourceTZField: sc.SourceTZField, ZoneField: sc.ZoneField, Vectors: sc.Vectors,
	})
	if err != nil {
		t.Fatalf("re-import: %v", err)
	}
	return res.Path
}

// TestCohortZone_ManagedSidecarFollowsSetClear: set on a managed cohort
// records the default on its imports sidecar and clear removes it, so a
// re-import from the sidecar reproduces each state; a refused set leaves
// the sidecar untouched.
func TestCohortZone_ManagedSidecarFollowsSetClear(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fsys := afero.NewMemMapFs()
	p, cohort := managedZoneEngine(t, fsys)
	sidecarPath := cohort + imports.SidecarSuffix

	before := readFile(t, fsys, sidecarPath)
	if _, err := p.SetCohortZoneField(ctx, cohort, "local"); !isZoneOption(err) {
		t.Fatalf("non-categorical set: %v, want SERVICE_VALIDATION option zone_field", err)
	}
	if !bytes.Equal(readFile(t, fsys, sidecarPath), before) {
		t.Fatal("a refused set changed the managed sidecar")
	}

	set, err := p.SetCohortZoneField(ctx, cohort, "zone")
	if err != nil || !set.Changed || len(set.SidecarWarnings) != 0 {
		t.Fatalf("set = %+v, %v", set, err)
	}
	sc := managedSidecar(t, p)
	if sc.ZoneField != "zone" {
		t.Fatalf("sidecar zone_field after set = %q, want zone", sc.ZoneField)
	}
	if got := zoneOfCohort(t, p, reimportFromSidecar(t, p, sc)); got != "zone" {
		t.Fatalf("re-import after set declares %q, want zone", got)
	}

	cleared, err := p.ClearCohortZoneField(ctx, cohort)
	if err != nil || !cleared.Changed || len(cleared.SidecarWarnings) != 0 {
		t.Fatalf("clear = %+v, %v", cleared, err)
	}
	sc = managedSidecar(t, p)
	if sc.ZoneField != "" || bytes.Contains(readFile(t, fsys, sidecarPath), []byte(`"zone_field"`)) {
		t.Fatalf("sidecar after clear still records a default: %s", readFile(t, fsys, sidecarPath))
	}
	if got := zoneOfCohort(t, p, reimportFromSidecar(t, p, sc)); got != "" {
		t.Fatalf("re-import after clear declares %q, want none", got)
	}
}

// TestCohortZone_NonManagedCohortGetsNoImportsSidecar: a cohort outside
// the imports pool is rewritten with no imports sidecar created.
func TestCohortZone_NonManagedCohortGetsNoImportsSidecar(t *testing.T) {
	t.Parallel()

	p, fsys := memBuilderEngine(t)
	buildCohort(t, p, "c.pulse", zoneSweepSchema(""), CohortBuilderOptions{}, zoneSweepRows(0, 6))
	res, err := p.SetCohortZoneField(context.Background(), "c.pulse", "tz")
	if err != nil || !res.Changed || len(res.SidecarWarnings) != 0 {
		t.Fatalf("set = %+v, %v", res, err)
	}
	if made, _ := afero.Exists(fsys, "c.pulse"+imports.SidecarSuffix); made {
		t.Fatal("an imports sidecar was created beside a non-managed cohort")
	}
}

// TestCohortZone_ManagedSidecarFailureIsReported: when the cohort
// rewrite succeeds but its imports sidecar cannot be replaced, the call
// reports a SERVICE_RESOURCE sidecar warning — never a silent
// divergence — and the sidecar keeps its prior bytes.
func TestCohortZone_ManagedSidecarFailureIsReported(t *testing.T) {
	t.Parallel()

	fsys := &sidecarRenameFailFs{Fs: afero.NewMemMapFs()}
	p, cohort := managedZoneEngine(t, fsys)
	fsys.armed = true
	res, err := p.SetCohortZoneField(context.Background(), cohort, "zone")
	if err != nil || !res.Changed {
		t.Fatalf("set = %+v, %v; the cohort rewrite itself succeeds", res, err)
	}
	if got := zoneOfCohort(t, p, cohort); got != "zone" {
		t.Fatalf("cohort declares %q, want zone", got)
	}
	if len(res.SidecarWarnings) != 1 || res.SidecarWarnings[0].Code != errors.SERVICE_RESOURCE ||
		res.SidecarWarnings[0].Details["sidecar"] != cohort+imports.SidecarSuffix {
		t.Fatalf("sidecar warnings = %+v, want one SERVICE_RESOURCE naming the sidecar", res.SidecarWarnings)
	}
	if sc := managedSidecar(t, p); sc.ZoneField != "" {
		t.Fatalf("a failed sidecar update left zone_field %q", sc.ZoneField)
	}
}
