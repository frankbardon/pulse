package pulse

import (
	"context"
	"testing"

	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

func newAskSourceTestPulse(t *testing.T) (*Pulse, afero.Fs) {
	t.Helper()
	afs := afero.NewMemMapFs()
	body := "id,score\n1,10.5\n2,20.0\n3,30.75\n"
	if err := afero.WriteFile(afs, "data.csv", []byte(body), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	p, err := New(Options{FS: afs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p, afs
}

// TestAsk_SourceAutoImport_RunsManagedImport verifies the new
// collapsed entry point: AskRequest.Source triggers an auto-import,
// the resulting handle is used as the cohort, and the import metadata
// surfaces on resp.Import. Predict-only mode avoids needing record
// data on the inferred schema.
func TestAsk_SourceAutoImport_RunsManagedImport(t *testing.T) {
	p, _ := newAskSourceTestPulse(t)

	resp, err := p.Ask(context.Background(), &AskRequest{
		Source:  "data.csv",
		Predict: true,
		Query:   "average score",
	})
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if resp.Import == nil {
		t.Fatal("resp.Import nil; expected populated for auto-import")
	}
	if resp.Import.Handle != "data" || resp.Import.Path != "imports/data.pulse" {
		t.Errorf("resp.Import = %+v, want handle=data path=imports/data.pulse", resp.Import)
	}
	if !resp.Import.Managed {
		t.Errorf("resp.Import.Managed=false; expected managed handle")
	}
	if resp.Predict == nil {
		t.Fatal("resp.Predict nil")
	}
	// Query parser saw the schema and produced a non-empty resolution.
	if resp.QueryResolution == nil {
		t.Error("expected QueryResolution populated by query parser")
	}
}

// TestAsk_SourceIgnoredWhenCohortPresent verifies precedence: when
// Request.Cohort is already set, Source is not consulted — the
// caller-supplied cohort wins.
func TestAsk_SourceIgnoredWhenCohortPresent(t *testing.T) {
	p, _ := newAskSourceTestPulse(t)
	if _, err := p.ImportFile(context.Background(), ImportSpec{SourcePath: "data.csv", Handle: "preimport"}); err != nil {
		t.Fatalf("pre-import: %v", err)
	}

	resp, err := p.Ask(context.Background(), &AskRequest{
		Source:  "data.csv", // would normally collide via auto-import
		Predict: true,
		Query:   "average score",
		Request: &Request{Cohort: &types.Cohort{Filename: "imports/preimport.pulse"}},
	})
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	// Auto-import did NOT run — the explicit cohort path wins.
	if resp.Import != nil {
		t.Errorf("resp.Import populated despite explicit cohort: %+v", resp.Import)
	}
}

// TestAsk_SourceAutoImport_HonoursSourceTTL verifies that SourceTTL is
// parsed and applied to the managed handle's sidecar.
func TestAsk_SourceAutoImport_HonoursSourceTTL(t *testing.T) {
	p, _ := newAskSourceTestPulse(t)

	resp, err := p.Ask(context.Background(), &AskRequest{
		Source:    "data.csv",
		SourceTTL: "24h",
		Predict:   true,
		Query:     "average score",
	})
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if resp.Import == nil || resp.Import.TTLSeconds != 24*3600 {
		t.Errorf("TTLSeconds = %v, want %d", resp.Import, 24*3600)
	}
}
