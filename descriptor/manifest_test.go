package descriptor

import (
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/types"
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

func TestManifestComponentSchemasComplete(t *testing.T) {
	m := BuildManifest()

	// Per-Operator surface: every aggregator capability entry carries
	// a populated ComponentSchema with the universal floor plus the
	// operator-specific keys.
	aggSet := nameSet(m.Components.Aggregators)
	for _, at := range types.AllAggregationTypes() {
		name := string(at)
		op, ok := aggSet[name]
		if !ok {
			t.Fatalf("aggregator %q missing from manifest.components.aggregators", name)
		}
		if len(op.ComponentSchema.Keys) == 0 {
			t.Fatalf("aggregator %q has empty ComponentSchema.Keys (universal floor must be declared)", name)
		}
		if op.ComponentSchema.Mergeability == "" {
			t.Fatalf("aggregator %q has empty ComponentSchema.Mergeability (must be Mergeable, Partial, or None)", name)
		}
	}

	// Top-level projection: ComponentsSchemas.Aggregators mirrors the
	// per-Operator entries.
	if m.ComponentsSchemas.Aggregators == nil {
		t.Fatalf("ComponentsSchemas.Aggregators is nil; manifest projection not populated")
	}
	if got, want := len(m.ComponentsSchemas.Aggregators), len(types.AllAggregationTypes()); got != want {
		t.Errorf("ComponentsSchemas.Aggregators length = %d, want %d", got, want)
	}
	for _, at := range types.AllAggregationTypes() {
		name := string(at)
		sch, ok := m.ComponentsSchemas.Aggregators[name]
		if !ok {
			t.Fatalf("aggregator %q missing from ComponentsSchemas.Aggregators", name)
		}
		if len(sch.Keys) == 0 {
			t.Errorf("aggregator %q ComponentsSchemas entry has empty Keys", name)
		}
		if sch.Mergeability == "" {
			t.Errorf("aggregator %q ComponentsSchemas entry has empty Mergeability", name)
		}
		// Universal floor — every entry begins with {n, n_null}.
		if sch.Keys[0].Name != "n" {
			t.Errorf("aggregator %q ComponentsSchemas entry Keys[0].Name = %q, want \"n\" (universal floor)", name, sch.Keys[0].Name)
		}
		if len(sch.Keys) < 2 || sch.Keys[1].Name != "n_null" {
			t.Errorf("aggregator %q ComponentsSchemas entry Keys[1].Name missing or wrong, want \"n_null\" (universal floor)", name)
		}
	}

	// Per-Operator surface: every grouper capability entry carries a
	// populated ComponentSchema with the universal floor plus the
	// operator-specific keys.
	grpSet := nameSet(m.Components.Groupers)
	for _, gt := range types.AllGroupTypes() {
		name := string(gt)
		op, ok := grpSet[name]
		if !ok {
			t.Fatalf("grouper %q missing from manifest.components.groupers", name)
		}
		if len(op.ComponentSchema.Keys) == 0 {
			t.Fatalf("grouper %q has empty ComponentSchema.Keys (universal floor must be declared)", name)
		}
		if op.ComponentSchema.Mergeability == "" {
			t.Fatalf("grouper %q has empty ComponentSchema.Mergeability (must be Mergeable, Partial, or None)", name)
		}
	}

	// Top-level projection: ComponentsSchemas.Groupers mirrors the
	// per-Operator entries.
	if m.ComponentsSchemas.Groupers == nil {
		t.Fatalf("ComponentsSchemas.Groupers is nil; manifest projection not populated")
	}
	if got, want := len(m.ComponentsSchemas.Groupers), len(types.AllGroupTypes()); got != want {
		t.Errorf("ComponentsSchemas.Groupers length = %d, want %d", got, want)
	}
	for _, gt := range types.AllGroupTypes() {
		name := string(gt)
		sch, ok := m.ComponentsSchemas.Groupers[name]
		if !ok {
			t.Fatalf("grouper %q missing from ComponentsSchemas.Groupers", name)
		}
		if len(sch.Keys) == 0 {
			t.Errorf("grouper %q ComponentsSchemas entry has empty Keys", name)
		}
		if sch.Mergeability == "" {
			t.Errorf("grouper %q ComponentsSchemas entry has empty Mergeability", name)
		}
		// Universal grouper floor — every entry begins with
		// {total_n, n_null}.
		if sch.Keys[0].Name != "total_n" {
			t.Errorf("grouper %q ComponentsSchemas entry Keys[0].Name = %q, want \"total_n\" (universal floor)", name, sch.Keys[0].Name)
		}
		if len(sch.Keys) < 2 || sch.Keys[1].Name != "n_null" {
			t.Errorf("grouper %q ComponentsSchemas entry Keys[1].Name missing or wrong, want \"n_null\" (universal floor)", name)
		}
	}

	// Per-Operator surface: every filterer capability entry carries a
	// populated ComponentSchema with the universal floor plus any
	// operator-specific keys. In v1 every entry is floor-only
	// {n_in, n_out, n_null_input}; future per-filter specifics
	// (e.g. n_below / n_above for FILTER_RANGE) will extend the
	// schema without re-opening this gate.
	filSet := nameSet(m.Components.Filterers)
	for _, ft := range types.AllFiltererTypes() {
		name := string(ft)
		op, ok := filSet[name]
		if !ok {
			t.Fatalf("filterer %q missing from manifest.components.filterers", name)
		}
		if len(op.ComponentSchema.Keys) == 0 {
			t.Fatalf("filterer %q has empty ComponentSchema.Keys (universal floor must be declared)", name)
		}
		if op.ComponentSchema.Mergeability == "" {
			t.Fatalf("filterer %q has empty ComponentSchema.Mergeability (must be Mergeable, Partial, or None)", name)
		}
	}

	// Top-level projection: ComponentsSchemas.Filterers mirrors the
	// per-Operator entries.
	if m.ComponentsSchemas.Filterers == nil {
		t.Fatalf("ComponentsSchemas.Filterers is nil; manifest projection not populated")
	}
	if got, want := len(m.ComponentsSchemas.Filterers), len(types.AllFiltererTypes()); got != want {
		t.Errorf("ComponentsSchemas.Filterers length = %d, want %d", got, want)
	}
	for _, ft := range types.AllFiltererTypes() {
		name := string(ft)
		sch, ok := m.ComponentsSchemas.Filterers[name]
		if !ok {
			t.Fatalf("filterer %q missing from ComponentsSchemas.Filterers", name)
		}
		if len(sch.Keys) == 0 {
			t.Errorf("filterer %q ComponentsSchemas entry has empty Keys", name)
		}
		if sch.Mergeability == "" {
			t.Errorf("filterer %q ComponentsSchemas entry has empty Mergeability", name)
		}
		// Universal filterer floor — every entry begins with
		// {n_in, n_out, n_null_input}.
		if sch.Keys[0].Name != "n_in" {
			t.Errorf("filterer %q ComponentsSchemas entry Keys[0].Name = %q, want \"n_in\" (universal floor)", name, sch.Keys[0].Name)
		}
		if len(sch.Keys) < 2 || sch.Keys[1].Name != "n_out" {
			t.Errorf("filterer %q ComponentsSchemas entry Keys[1].Name missing or wrong, want \"n_out\" (universal floor)", name)
		}
		if len(sch.Keys) < 3 || sch.Keys[2].Name != "n_null_input" {
			t.Errorf("filterer %q ComponentsSchemas entry Keys[2].Name missing or wrong, want \"n_null_input\" (universal floor)", name)
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
