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
	for i := 0; i < 10; i++ {
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

	// Check aggregators are sorted.
	for i := 1; i < len(m.Components.Aggregators); i++ {
		if m.Components.Aggregators[i] < m.Components.Aggregators[i-1] {
			t.Errorf("aggregators not sorted: %s before %s",
				m.Components.Aggregators[i-1], m.Components.Aggregators[i])
		}
	}

	// Check windows are sorted.
	for i := 1; i < len(m.Components.Windows); i++ {
		if m.Components.Windows[i] < m.Components.Windows[i-1] {
			t.Errorf("windows not sorted: %s before %s",
				m.Components.Windows[i-1], m.Components.Windows[i])
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

	// Must have 19 field types.
	if len(m.CohortTypes) != 19 {
		t.Errorf("CohortTypes count = %d, want 19", len(m.CohortTypes))
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

func TestRootManifestGolden(t *testing.T) {
	m := BuildManifest()
	env := NewEnvelope(m)
	data, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		t.Fatalf("json.MarshalIndent: %v", err)
	}

	compareGolden(t, "manifest.json", data)
}
