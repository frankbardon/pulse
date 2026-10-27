//go:build heavy

package pulse

import (
	"testing"
)

func TestProfileInvisibilityParity(t *testing.T) {
	t.Parallel()

	if NewMCPParitySession == nil {
		t.Fatal("MCP bridge not installed (feature_parity_mcp_bridge_test.go)")
	}
	inline, names := invisibilityProfiles(t)
	t.Run("operators", func(t *testing.T) {
		sessions := &mcpSessions{t: t, by: map[*Pulse]*MCPParitySession{}}
		// mcp-prompts offers no base operator (like the empty fixture,
		// whose vacuity the harness allows by name): surfaces only.
		var hosts []string
		for _, n := range names {
			if n != "mcp-prompts" && n != "mcp-intent-prompts" {
				hosts = append(hosts, n)
			}
		}
		runHiddenParityWith(t, parityFS(t), parityHostConfig{profiles: inline}, hosts, parityCategories, mcpParityEntryPoints(sessions))
	})
	t.Run("surfaces", func(t *testing.T) {
		fsys := parityFS(t)
		full := newParityHostWith(t, fsys, "", parityHostConfig{})
		fullSess := NewMCPParitySession(t, full.p)
		for _, name := range names {
			t.Run(name, func(t *testing.T) {
				h := newParityHostWith(t, fsys, name, parityHostConfig{profiles: inline})
				checkMCPSurfaces(t, h, NewMCPParitySession(t, h.p), fullSess)
			})
		}
	})
}
