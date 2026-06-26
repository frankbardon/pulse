//go:build ignore

// TODO(E1-S2/S3/S4): not yet ported to github.com/modelcontextprotocol/go-sdk.
// This file still targets mark3labs/mcp-go and is excluded from the build by
// the constraint above. Handler logic is preserved verbatim; the per-file
// migration stories (tools/strict_decode/import_tools -> E1-S2; resources/
// prompts -> E1-S3; schema_bind -> E1-S4) remove this constraint as they port.

package mcp

import (
	"slices"
	"testing"

	"github.com/spf13/afero"
)

func TestScanPulseFiles_FindsExtensions(t *testing.T) {
	fs := afero.NewMemMapFs()
	for _, name := range []string{"a.pulse", "b.pulse", "c.csv", "sub/d.pulse", "readme.md"} {
		f, err := fs.Create(name)
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		_ = f.Close()
	}

	got := scanPulseFiles(fs)
	slices.Sort(got)
	want := []string{"a.pulse", "b.pulse", "sub/d.pulse"}
	if !slices.Equal(got, want) {
		t.Fatalf("scan mismatch\n got: %v\nwant: %v", got, want)
	}
}

func TestScanPulseFiles_EmptyFs(t *testing.T) {
	fs := afero.NewMemMapFs()
	if got := scanPulseFiles(fs); len(got) != 0 {
		t.Fatalf("expected empty scan, got %v", got)
	}
}

func TestURISchemes_Constant(t *testing.T) {
	if CohortURIScheme != "pulse://" {
		t.Errorf("CohortURIScheme drifted: %q", CohortURIScheme)
	}
	if SkillURIScheme != "pulse-skill://" {
		t.Errorf("SkillURIScheme drifted: %q", SkillURIScheme)
	}
}
