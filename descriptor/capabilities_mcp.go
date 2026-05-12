package descriptor

import (
	"sort"

	"github.com/frankbardon/pulse/internal/mcp/mcptools"
)

// mcpToolCapabilities mirrors mcptools.Meta() into the manifest payload.
// Sorted by name for deterministic output. We depend on the leaf
// mcptools sub-package (not internal/mcp itself) to avoid an import
// cycle with the root pulse package.
// TestManifestMCPToolsComplete enforces coverage.
func mcpToolCapabilities() []MCPTool {
	meta := mcptools.Meta()
	out := make([]MCPTool, len(meta))
	for i, m := range meta {
		out[i] = MCPTool{Name: m.Name, Description: m.Description}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
