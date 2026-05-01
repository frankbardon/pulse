package skills_test

import (
	"strings"
	"testing"

	"github.com/frankbardon/pulse/internal/mcp"
	"github.com/frankbardon/pulse/skills"
)

// TestSkillsCoverAllMCPTools verifies that every tool registered with the
// MCP server appears in mcp-integration.md. Adding or removing a tool
// without updating the skill is the trigger this gate catches.
func TestSkillsCoverAllMCPTools(t *testing.T) {
	content, ok := skills.Get("mcp-integration")
	if !ok {
		t.Fatal("mcp-integration.md not found")
	}
	for _, tool := range mcp.RegisteredTools() {
		if !strings.Contains(content, tool) {
			t.Errorf("mcp-integration.md does not mention MCP tool %q", tool)
		}
	}
}
