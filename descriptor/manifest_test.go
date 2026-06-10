package descriptor

import (
	"encoding/json"
	"testing"
)

func TestManifestFormatVersion(t *testing.T) {
	m := BuildManifest()
	if m.FormatVersion != "1.0" {
		t.Errorf("FormatVersion = %q, want %q", m.FormatVersion, "1.0")
	}
}

func TestManifestJSON_Valid(t *testing.T) {
	m := BuildManifest()
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if !json.Valid(data) {
		t.Fatal("manifest JSON is not valid")
	}
}

func TestManifestDeterministicAcrossRuns(t *testing.T) {
	first, err := json.Marshal(BuildManifest())
	if err != nil {
		t.Fatalf("first marshal: %v", err)
	}
	for i := range 10 {
		next, err := json.Marshal(BuildManifest())
		if err != nil {
			t.Fatalf("marshal iteration %d: %v", i, err)
		}
		if string(first) != string(next) {
			t.Fatalf("manifest output differs on iteration %d", i)
		}
	}
}

func TestManifestIncludesCommands(t *testing.T) {
	m := BuildManifest()
	if len(m.Commands) == 0 {
		t.Fatal("Commands is empty")
	}

	names := make(map[string]bool)
	for _, cmd := range m.Commands {
		names[cmd.Name] = true
	}

	required := []string{"process", "compose", "sample", "facet", "inspect", "predict", "manifest"}
	for _, name := range required {
		if !names[name] {
			t.Errorf("missing command: %s", name)
		}
	}
}

func TestManifestIncludesComponents(t *testing.T) {
	m := BuildManifest()

	if len(m.Components.Aggregators) == 0 {
		t.Error("Aggregators is empty")
	}
	if len(m.Components.Attributes) == 0 {
		t.Error("Attributes is empty")
	}
	if len(m.Components.Filterers) == 0 {
		t.Error("Filterers is empty")
	}
	if len(m.Components.Groupers) == 0 {
		t.Error("Groupers is empty")
	}
	if len(m.Components.Windows) == 0 {
		t.Error("Windows is empty")
	}

	// Check aggregators are sorted by Name.
	for i := 1; i < len(m.Components.Aggregators); i++ {
		if m.Components.Aggregators[i].Name < m.Components.Aggregators[i-1].Name {
			t.Errorf("aggregators not sorted: %s before %s",
				m.Components.Aggregators[i-1].Name, m.Components.Aggregators[i].Name)
		}
	}

	// Check windows are sorted by Name.
	for i := 1; i < len(m.Components.Windows); i++ {
		if m.Components.Windows[i].Name < m.Components.Windows[i-1].Name {
			t.Errorf("windows not sorted: %s before %s",
				m.Components.Windows[i-1].Name, m.Components.Windows[i].Name)
		}
	}
}

// TestManifestWindowsCount verifies all 10 window types are present.
func TestManifestWindowsCount(t *testing.T) {
	m := BuildManifest()
	if len(m.Components.Windows) != 10 {
		t.Errorf("Windows count = %d, want 10", len(m.Components.Windows))
	}
}

func TestManifestIncludesCohortTypes(t *testing.T) {
	m := BuildManifest()
	if len(m.CohortTypes) == 0 {
		t.Fatal("CohortTypes is empty")
	}

	// Must have 17 field types — the original 13 plus set_u8/u16/u32/u64.
	if len(m.CohortTypes) != 17 {
		t.Errorf("CohortTypes count = %d, want 17", len(m.CohortTypes))
	}

	// Check that categorical fields are marked.
	hasCategorical := false
	for _, ft := range m.CohortTypes {
		if ft.Categorical {
			hasCategorical = true
			break
		}
	}
	if !hasCategorical {
		t.Error("no categorical field types found")
	}
}

func TestManifestIncludesSkills(t *testing.T) {
	m := BuildManifest()
	// Skills array must be present (even if empty placeholder).
	if m.Skills == nil {
		t.Fatal("Skills is nil, expected non-nil array")
	}
}

// TestManifest_SkillsNotEmpty verifies the manifest populates Skills from
// the embedded skill pack. The earlier hardcoded empty slice was a bug.
func TestManifest_SkillsNotEmpty(t *testing.T) {
	m := BuildManifest()
	if len(m.Skills) == 0 {
		t.Fatal("Skills slice is empty; manifest must mirror skills.List() output")
	}
	// Every entry must have a name and a description.
	for i, s := range m.Skills {
		if s.Name == "" {
			t.Errorf("Skills[%d] missing Name", i)
		}
		if s.Description == "" {
			t.Errorf("Skills[%d] (%q) missing Description", i, s.Name)
		}
	}
	// Ensure sorted by name (deterministic).
	for i := 1; i < len(m.Skills); i++ {
		if m.Skills[i].Name < m.Skills[i-1].Name {
			t.Errorf("Skills not sorted: %q before %q", m.Skills[i-1].Name, m.Skills[i].Name)
		}
	}
}

// TestManifestExamplesPopulated verifies the embedded example library
// is surfaced in the manifest payload. Non-skippable: missing fields
// silently break LLM-side example discovery.
func TestManifestExamplesPopulated(t *testing.T) {
	m := BuildManifest()
	if m.ExamplesCount == 0 {
		t.Error("ExamplesCount is 0; embedded example library not surfaced")
	}
	if len(m.ExampleCategories) == 0 {
		t.Error("ExampleCategories is empty")
	}
	if len(m.ExampleTags) == 0 {
		t.Error("ExampleTags is empty")
	}
	// Categories must be sorted alphabetically for golden stability.
	for i := 1; i < len(m.ExampleCategories); i++ {
		if m.ExampleCategories[i] < m.ExampleCategories[i-1] {
			t.Errorf("ExampleCategories not sorted: %q before %q", m.ExampleCategories[i-1], m.ExampleCategories[i])
		}
	}
	for i := 1; i < len(m.ExampleTags); i++ {
		if m.ExampleTags[i] < m.ExampleTags[i-1] {
			t.Errorf("ExampleTags not sorted: %q before %q", m.ExampleTags[i-1], m.ExampleTags[i])
		}
	}
}

func TestRootManifestGolden(t *testing.T) {
	m := BuildManifest()
	env := NewEnvelope(m)
	data, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		t.Fatalf("json.MarshalIndent: %v", err)
	}

	compareGolden(t, "manifest.json", data)
}
