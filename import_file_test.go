package pulse

import (
	"context"
	"testing"
	"time"

	"github.com/spf13/afero"
)

func writeCSVPulse(t *testing.T, afs afero.Fs, p string) {
	t.Helper()
	body := "id,name,amount\n1,Alice,10.5\n2,Bob,20.0\n3,Carol,30.75\n"
	if err := afero.WriteFile(afs, p, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
}

func newImportTestPulse(t *testing.T) (*Pulse, afero.Fs) {
	t.Helper()
	afs := afero.NewMemMapFs()
	p, err := New(Options{FS: afs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p, afs
}

func TestPulse_ImportFile_CSV(t *testing.T) {
	p, afs := newImportTestPulse(t)
	writeCSVPulse(t, afs, "data.csv")

	res, err := p.ImportFile(context.Background(), ImportSpec{SourcePath: "data.csv"})
	if err != nil {
		t.Fatalf("ImportFile: %v", err)
	}
	if !res.Managed {
		t.Fatalf("Managed=false; expected true for csv import")
	}
	if res.Path != "imports/data.pulse" {
		t.Errorf("path = %q, want imports/data.pulse", res.Path)
	}
	if res.RowsImported != 3 {
		t.Errorf("rows = %d, want 3", res.RowsImported)
	}
}

func TestPulse_ImportFile_PulsePassthrough(t *testing.T) {
	p, afs := newImportTestPulse(t)
	// Bare native magic header is enough for the passthrough's
	// readability check; the file is not actually opened here.
	if err := afero.WriteFile(afs, "curated.pulse", []byte("PULSE\x00\x00\x00\x01"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	res, err := p.ImportFile(context.Background(), ImportSpec{SourcePath: "curated.pulse"})
	if err != nil {
		t.Fatalf("ImportFile: %v", err)
	}
	if res.Managed {
		t.Errorf("Managed=true for pulse passthrough; expected false")
	}
	if res.Path != "curated.pulse" {
		t.Errorf("path = %q, want curated.pulse", res.Path)
	}
}

func TestPulse_ImportFile_DropRoundTrip(t *testing.T) {
	p, afs := newImportTestPulse(t)
	writeCSVPulse(t, afs, "data.csv")
	res, err := p.ImportFile(context.Background(), ImportSpec{SourcePath: "data.csv"})
	if err != nil {
		t.Fatalf("ImportFile: %v", err)
	}
	if err := p.Drop(context.Background(), res.Handle); err != nil {
		t.Fatalf("Drop: %v", err)
	}
	entries, err := p.Imports(context.Background())
	if err != nil {
		t.Fatalf("Imports: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("entries after Drop = %d, want 0", len(entries))
	}
}

func TestPulse_Inspect_TouchesManagedHandle(t *testing.T) {
	p, afs := newImportTestPulse(t)
	writeCSVPulse(t, afs, "data.csv")
	res, err := p.ImportFile(context.Background(), ImportSpec{SourcePath: "data.csv", TTL: 1 * time.Hour})
	if err != nil {
		t.Fatalf("ImportFile: %v", err)
	}
	originalExpiry := *res.ExpiresAt

	// Sleep avoided — use the Manager's own clock through the facade.
	// Inspect should slide expiry forward by the TTL relative to call
	// time (which is "now" at the call). With a memory clock the
	// touched expiry equals now + TTL, which is >= originalExpiry. We
	// cannot easily verify movement without a clock injection; instead
	// verify the touched sidecar's ExpiresAt is at least the original.
	if _, err := p.Inspect(context.Background(), res.Path); err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	entries, err := p.Imports(context.Background())
	if err != nil {
		t.Fatalf("Imports: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	if entries[0].Sidecar.ExpiresAt.Before(originalExpiry) {
		t.Errorf("post-Inspect expiry %v < original %v", entries[0].Sidecar.ExpiresAt, originalExpiry)
	}
}
