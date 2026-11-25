package descriptor

import (
	"sort"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/internal/mcp/toolmeta"
)

// mcpToolCapabilities mirrors toolmeta.Meta() into the manifest payload,
// each description's feature fences rendered against on (total for an
// unscoped instance, so the full manifest carries the fence-free text).
// Sorted by name for deterministic output. We depend on the public leaf
// internal/mcp/toolmeta package (not the SDK adapter itself) to avoid an import
// cycle with the root pulse package.
// TestManifestMCPToolsComplete enforces coverage.
func mcpToolCapabilities(on func(string) bool) []descriptor.MCPTool {
	meta := toolmeta.Meta()
	out := make([]descriptor.MCPTool, len(meta))
	for i, m := range meta {
		out[i] = descriptor.MCPTool{Name: m.Name, Description: RenderProseFences(m.Description, on)}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
