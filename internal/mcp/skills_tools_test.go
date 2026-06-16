package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/skills"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
)

func TestPulseSkillsList_BackwardCompatFields(t *testing.T) {
	handler := handleSkillsList()
	req := mcpgo.CallToolRequest{}
	req.Params.Name = ToolSkillsList

	out, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("handleSkillsList: %v", err)
	}
	if out.IsError {
		t.Fatalf("handler returned error result: %+v", out.Content)
	}
	if len(out.Content) == 0 {
		t.Fatal("handler returned no content")
	}
	text, ok := out.Content[0].(mcpgo.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", out.Content[0])
	}

	// Decode into a map slice so we can prove omitempty at the JSON level.
	// Decoding straight into []skills.Metadata would hide whether the new
	// fields actually appeared on the wire — Go would zero-fill them either
	// way. The omitempty contract lives in the JSON, not in the struct.
	var raw []map[string]json.RawMessage
	if err := json.Unmarshal([]byte(text.Text), &raw); err != nil {
		t.Fatalf("decode: %v\n%s", err, text.Text)
	}

	if len(raw) == 0 {
		t.Fatal("pulse_skills_list returned 0 entries")
	}

	// Also keep the typed view for content-level checks below.
	var typed []skills.Metadata
	if err := json.Unmarshal([]byte(text.Text), &typed); err != nil {
		t.Fatalf("decode typed: %v", err)
	}
	if len(typed) != len(raw) {
		t.Fatalf("typed/raw length mismatch: %d vs %d", len(typed), len(raw))
	}

	// v0.20.0 compat: response-components.md must appear in the payload.
	var sawResponseComponents bool
	for _, m := range typed {
		if m.Name == "response-components" {
			sawResponseComponents = true
			break
		}
	}
	if !sawResponseComponents {
		names := make([]string, 0, len(typed))
		for _, m := range typed {
			names = append(names, m.Name)
		}
		t.Fatalf("response-components skill missing from pulse_skills_list payload; got: %v", names)
	}

	historical := []string{"name", "description", "type", "applies_to"}
	additive := []string{"kind", "category", "operator", "covers", "examples_tags"}

	for i, entry := range raw {
		entry := entry
		name := string(entry["name"])
		// Use the JSON-encoded name as the subtest label so failures point
		// at the row that broke.
		t.Run(strings.Trim(name, `"`), func(t *testing.T) {
			// Historical four fields must always be present.
			for _, key := range historical {
				if _, ok := entry[key]; !ok {
					t.Errorf("entry %d (%s): missing historical field %q", i, name, key)
				}
			}

			// Validate non-empty name / description on every entry. Loader
			// regressions (e.g. a broken parseMetadata) show up here.
			if typed[i].Name == "" {
				t.Errorf("entry %d: empty name", i)
			}
			if typed[i].Description == "" {
				t.Errorf("entry %d (%s): empty description", i, name)
			}

			// Additive omitempty contract: if the typed struct shows a zero
			// value, the JSON object MUST omit that key. Catches an
			// accidental drop of the `omitempty` tag.
			if typed[i].Kind == "" {
				if _, present := entry["kind"]; present {
					t.Errorf("entry %d (%s): zero-value Kind must be omitted from JSON", i, name)
				}
			}
			if typed[i].Category == "" {
				if _, present := entry["category"]; present {
					t.Errorf("entry %d (%s): zero-value Category must be omitted from JSON", i, name)
				}
			}
			if typed[i].Operator == "" {
				if _, present := entry["operator"]; present {
					t.Errorf("entry %d (%s): zero-value Operator must be omitted from JSON", i, name)
				}
			}
			if len(typed[i].Covers) == 0 {
				if _, present := entry["covers"]; present {
					t.Errorf("entry %d (%s): zero-value Covers must be omitted from JSON", i, name)
				}
			}
			if len(typed[i].ExamplesTags) == 0 {
				if _, present := entry["examples_tags"]; present {
					t.Errorf("entry %d (%s): zero-value ExamplesTags must be omitted from JSON", i, name)
				}
			}

			// Cross-check: any additive key that IS present must be one we
			// know about — catches an accidental rename that would silently
			// drop the old key and add a new one of a different name.
			known := map[string]bool{}
			for _, k := range historical {
				known[k] = true
			}
			for _, k := range additive {
				known[k] = true
			}
			for k := range entry {
				if !known[k] {
					t.Errorf("entry %d (%s): unexpected key %q (additive contract violation)", i, name, k)
				}
			}
		})
	}
}

// TestPulseSkillsGet_BackwardCompatBody drives handleSkillsGet directly and
// asserts the response body is the raw markdown content (text content, not a
// JSON envelope). This locks the historical contract: callers concatenate the
// returned text into LLM context, not a structured payload.
func TestPulseSkillsGet_BackwardCompatBody(t *testing.T) {
	handler := handleSkillsGet()

	list := skills.List()
	if len(list) == 0 {
		t.Fatal("skills.List() returned 0 entries; cannot exercise pulse_skills_get")
	}

	for _, meta := range list {
		meta := meta
		t.Run(meta.Name, func(t *testing.T) {
			req := mcpgo.CallToolRequest{}
			req.Params.Name = ToolSkillsGet
			req.Params.Arguments = map[string]any{"name": meta.Name}

			out, err := handler(context.Background(), req)
			if err != nil {
				t.Fatalf("handler: %v", err)
			}
			if out.IsError {
				t.Fatalf("handler returned error result for %q: %+v", meta.Name, out.Content)
			}
			if len(out.Content) == 0 {
				t.Fatalf("handler returned no content for %q", meta.Name)
			}
			text, ok := out.Content[0].(mcpgo.TextContent)
			if !ok {
				t.Fatalf("expected TextContent, got %T", out.Content[0])
			}
			if text.Text == "" {
				t.Fatalf("empty body for skill %q", meta.Name)
			}
			// Historical contract: bodies start with the YAML frontmatter
			// fence so downstream agents can parse the metadata in-band.
			if !strings.HasPrefix(text.Text, "---\n") {
				t.Errorf("skill %q body does not start with YAML frontmatter fence", meta.Name)
			}
		})
	}
}

func TestPulseSkillsGet_MissingNameError(t *testing.T) {
	handler := handleSkillsGet()

	cases := []struct {
		name string
		args map[string]any
	}{
		{"empty_args", map[string]any{}},
		{"empty_string", map[string]any{"name": ""}},
		{"unknown_skill", map[string]any{"name": "definitely-not-a-real-skill"}},
		{"wrong_type", map[string]any{"name": 42}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			req := mcpgo.CallToolRequest{}
			req.Params.Name = ToolSkillsGet
			req.Params.Arguments = tc.args
			out, err := handler(context.Background(), req)
			if err != nil {
				t.Fatalf("handler: %v", err)
			}
			if !out.IsError {
				t.Fatalf("expected IsError=true for %s, got success", tc.name)
			}
		})
	}
}
