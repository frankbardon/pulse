package skills_test

import (
	"strings"
	"testing"

	"github.com/frankbardon/pulse/internal/mcp/mcptools"
	"github.com/frankbardon/pulse/skills"
)

// TestSkillsCoverAllMCPTools verifies every tool registered via
// mcptools.Meta() has a matching tool-<kebab-name-drop-pulse-prefix>.md
// atomic skill file.
//
// Convention: pulse_skills_list -> tool-skills-list.md.
//
// This is the post-E4 replacement for the legacy "tool name appears in
// mcp-integration.md" check. Atomic-file existence is now the
// load-bearing gate — every MCP tool owns one tool-*.md file.
func TestSkillsCoverAllMCPTools(t *testing.T) {
	// Build the membership set from skills.List() — the loader is the
	// canonical embed surface and avoids re-embedding here.
	skillSet := make(map[string]bool, len(skills.List()))
	for _, m := range skills.List() {
		skillSet[m.Name] = true
	}
	for _, name := range mcptools.Names() {
		stem := "tool-" + strings.ReplaceAll(strings.ToLower(strings.TrimPrefix(name, "pulse_")), "_", "-")
		if !skillSet[stem] {
			t.Errorf("MCP tool %q: missing atomic skill skills/%s.md", name, stem)
		}
	}
}
