package descriptor

import (
	"testing"
)

// TestManifestExportCapability verifies the manifest carries the
// canonical ExportCapability entry with one ExportFormatCapability
// per format the export dispatcher supports. The canonical formats
// MUST be present with their OverlaySupport label so LLM planners can
// route Response.Overlays without inspecting io/.
func TestManifestExportCapability(t *testing.T) {
	m := BuildManifest()
	if len(m.Export.Formats) == 0 {
		t.Fatal("Export.Formats is empty; manifest must enumerate supported export formats")
	}

	// Build a name → OverlaySupport lookup for the assertion table.
	got := make(map[string]string, len(m.Export.Formats))
	for _, f := range m.Export.Formats {
		if f.Name == "" {
			t.Errorf("Export.Formats entry has empty Name")
		}
		if f.OverlaySupport == "" {
			t.Errorf("Export.Formats[%q] has empty OverlaySupport", f.Name)
		}
		got[f.Name] = f.OverlaySupport
	}

	// Canonical per-format overlay-embedding strategies.
	want := map[string]string{
		"arrow":   "sidecar",
		"parquet": "sidecar",
		"excel":   "sheets",
		"ndjson":  "trailing_block",
		"csv":     "warn_and_skip",
	}
	for name, label := range want {
		if got[name] != label {
			t.Errorf("Export.Formats[%q].OverlaySupport = %q, want %q", name, got[name], label)
		}
	}
}

// TestManifestExportCapability_AlphabetizedByName verifies the Formats
// slice is sorted alphabetically by Name so the golden manifest stays
// stable.
func TestManifestExportCapability_AlphabetizedByName(t *testing.T) {
	m := BuildManifest()
	for i := 1; i < len(m.Export.Formats); i++ {
		if m.Export.Formats[i].Name < m.Export.Formats[i-1].Name {
			t.Errorf("Export.Formats not sorted: %q before %q",
				m.Export.Formats[i-1].Name, m.Export.Formats[i].Name)
		}
	}
}

// TestManifestExportCapability_OverlaySupportVocabulary verifies that
// every OverlaySupport label belongs to the canonical four-value
// vocabulary declared by ExportFormatCapability's godoc. Catches
// typos and unintentional new labels.
func TestManifestExportCapability_OverlaySupportVocabulary(t *testing.T) {
	m := BuildManifest()
	allowed := map[string]bool{
		"sidecar":        true,
		"sheets":         true,
		"trailing_block": true,
		"warn_and_skip":  true,
	}
	for _, f := range m.Export.Formats {
		if !allowed[f.OverlaySupport] {
			t.Errorf("Export.Formats[%q].OverlaySupport = %q, not in canonical vocabulary (sidecar / sheets / trailing_block / warn_and_skip)",
				f.Name, f.OverlaySupport)
		}
	}
}
