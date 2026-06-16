package skills_test

import (
	"strings"
	"testing"

	"github.com/frankbardon/pulse/internal/mcp/mcptools"
	"github.com/frankbardon/pulse/skills"
)

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
