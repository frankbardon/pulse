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

// TestManifestImportCapability verifies the manifest carries the
// read-side peer of Export.Formats, with every slot populated. The
// block exists so an LLM planner can answer "can Pulse read this file,
// and will the cohort's types be the source's or a guess?" without
// crawling io/.
func TestManifestImportCapability(t *testing.T) {
	m := BuildManifest()
	if len(m.Import.Formats) == 0 {
		t.Fatal("Import.Formats is empty; manifest must enumerate supported import formats")
	}
	got := make(map[string]ImportFormatCapability, len(m.Import.Formats))
	for _, f := range m.Import.Formats {
		if f.Name == "" {
			t.Error("Import.Formats entry has empty Name")
		}
		if len(f.Extensions) == 0 {
			t.Errorf("Import.Formats[%q] declares no Extensions", f.Name)
		}
		for _, ext := range f.Extensions {
			if len(ext) == 0 || ext[0] != '.' {
				t.Errorf("Import.Formats[%q] extension %q is not a leading-dot extension", f.Name, ext)
			}
		}
		switch f.SchemaSource {
		case "inferred", "authoritative":
		default:
			t.Errorf("Import.Formats[%q].SchemaSource = %q, not in the closed vocabulary (inferred / authoritative)",
				f.Name, f.SchemaSource)
		}
		got[f.Name] = f
	}

	spss, ok := got["spss"]
	if !ok {
		t.Fatalf("Import.Formats has no spss entry; the format is registered on io/format but invisible to the manifest")
	}
	// The whole point of the SPSS adapter: the .sav dictionary is fact,
	// not a hint the inference pass would overrule.
	if spss.SchemaSource != "authoritative" {
		t.Errorf("Import.Formats[spss].SchemaSource = %q, want authoritative", spss.SchemaSource)
	}
	// Read and write surfaces are NOT symmetric, and this slot is the
	// only honest place to say so.
	if spss.Export {
		t.Error("Import.Formats[spss].Export = true; SPSS export does not exist yet")
	}
}

// TestManifestImportCapability_AlphabetizedByName keeps the golden
// manifest stable.
func TestManifestImportCapability_AlphabetizedByName(t *testing.T) {
	m := BuildManifest()
	for i := 1; i < len(m.Import.Formats); i++ {
		if m.Import.Formats[i].Name < m.Import.Formats[i-1].Name {
			t.Errorf("Import.Formats not sorted: %q before %q",
				m.Import.Formats[i-1].Name, m.Import.Formats[i].Name)
		}
	}
}

// TestManifestImportCapability_ExportFlagMatchesExportBlock pins the
// two capability blocks against each other. Export=true on an import
// entry with no corresponding Export.Formats entry would promise a
// round trip that does not exist.
func TestManifestImportCapability_ExportFlagMatchesExportBlock(t *testing.T) {
	m := BuildManifest()
	writable := make(map[string]bool, len(m.Export.Formats))
	for _, f := range m.Export.Formats {
		writable[f.Name] = true
	}
	for _, f := range m.Import.Formats {
		if f.Export != writable[f.Name] {
			t.Errorf("Import.Formats[%q].Export = %v but Export.Formats %s the format",
				f.Name, f.Export, map[bool]string{true: "carries", false: "omits"}[writable[f.Name]])
		}
	}
}
