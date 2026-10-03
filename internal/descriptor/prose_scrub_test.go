package descriptor

import (
	"encoding/json"
	"testing"
)

func proseScrubFor(hidden ...string) ProseScrub {
	return NewProseScrub(NewInstanceSnapshot(nil, FeatureSet{
		Enabled: []string{"capability:process", "AGG_COUNT"},
		Hidden:  hidden,
	}))
}

// TestProseScrub_UnscopedIsIdentity: a nil instance's scrub (and the
// zero value) changes nothing, so profile-free MCP prose is
// byte-identical.
func TestProseScrub_UnscopedIsIdentity(t *testing.T) {
	for _, s := range []ProseScrub{{}, NewProseScrub(nil)} {
		if s.Active() {
			t.Fatal("unscoped scrub is active")
		}
		in := "Uses AGG_SUM. Calls pulse_compose."
		if got := s.Text(in); got != in {
			t.Errorf("Text changed unscoped prose: %q", got)
		}
		raw := json.RawMessage(`{"description":"AGG_SUM here."}`)
		got, err := s.SchemaDescriptions(raw)
		if err != nil || string(got) != string(raw) {
			t.Errorf("SchemaDescriptions changed unscoped schema: %s %v", got, err)
		}
	}
}

// TestProseScrub_TextDropsHiddenSentences: the manifest's sentence drop
// applied line by line — hidden operators and the tools a hidden
// capability owns are removed, layout survives, a list item reduced to
// its marker disappears.
func TestProseScrub_TextDropsHiddenSentences(t *testing.T) {
	s := proseScrubFor("AGG_SUM", "capability:compose")
	for _, tc := range []struct{ in, want string }{
		{"Count rows with AGG_COUNT. Sum with AGG_SUM. Done.", "Count rows with AGG_COUNT. Done."},
		{"First para.\n\nUse AGG_SUM here.\n\nLast para.", "First para.\n\nLast para."},
		{"1. Call pulse_manifest.\n2. Then pulse_compose.\n3. Done.\n", "1. Call pulse_manifest.\n2. Done.\n"},
		{"1. A.\n2. pulse_compose.\n3. B.\n\n1. C.\n2. D.", "1. A.\n2. B.\n\n1. C.\n2. D."},
		{"4. Validate first. Then run pulse_compose.", "4. Validate first."},
		{"FILTER_SET_* and AGG_SUMMARY stay.", "FILTER_SET_* and AGG_SUMMARY stay."},
		{"Only AGG_SUM.", ""},
	} {
		if got := s.Text(tc.in); got != tc.want {
			t.Errorf("Text(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestProseScrub_SchemaDescriptions: every string "description" keyword
// at any depth is scrubbed; a property NAMED description (object value)
// is walked, not replaced; untouched schemas keep their bytes.
func TestProseScrub_SchemaDescriptions(t *testing.T) {
	s := proseScrubFor("AGG_SUM")
	raw := json.RawMessage(`{"type":"object","description":"Top. AGG_SUM top.","properties":{"description":{"type":"string","description":"Nested AGG_SUM. Kept."},"n":{"type":"integer","maximum":1.50}}}`)
	got, err := s.SchemaDescriptions(raw)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(got, &doc); err != nil {
		t.Fatal(err)
	}
	if doc["description"] != "Top." {
		t.Errorf("top description = %q", doc["description"])
	}
	nested := doc["properties"].(map[string]any)["description"].(map[string]any)
	if nested["description"] != "Kept." {
		t.Errorf("nested description = %q", nested["description"])
	}
	clean := json.RawMessage(`{"description":"AGG_COUNT only.","enum":["AGG_SUM"]}`)
	if got, _ := s.SchemaDescriptions(clean); string(got) != string(clean) {
		t.Errorf("schema with no hidden description re-encoded: %s", got)
	}
}
