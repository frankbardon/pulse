package gosdk_test

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/internal/skills"
	"github.com/frankbardon/pulse/mcp/gosdk"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/afero"
)

// TestGuidanceSkills_MCPParity pins the glossary and intents virtual
// skills on every MCP skill surface — pulse_skills_list, pulse_skills_get,
// the exact pulse-skill:// resources and the pulse-skill://{+name}
// template — on a profile-free instance and under every private fixture
// profile. A virtual skill is never pruned; under a profile its body is
// rendered from the instance's pruned ontology, so the three read paths
// must agree with each other (and, profile-free, with skills.Get).
func TestGuidanceSkills_MCPParity(t *testing.T) {
	cfg := gosdk.Config{Version: "9.9.9", DisableCohortScan: true}
	for _, profile := range []string{"", "minimal", "survey-crosstab", "empty"} {
		t.Run("profile="+profile, func(t *testing.T) {
			var fp *pulse.FeatureProfile
			if profile != "" {
				fp = fixtureProfile(t, profile)
			}
			c, cancel := profiledServer(t, afero.NewMemMapFs(), fp, cfg)
			defer cancel()
			p, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(), FeatureProfile: fp})
			if err != nil {
				t.Fatalf("pulse.New: %v", err)
			}
			listed := skillsListed(t, c)
			resources := skillResourcesListed(t, c)
			for _, name := range []string{skills.VirtualGlossary, skills.VirtualIntents} {
				want, ok := skills.Get(name)
				if !ok || !strings.HasPrefix(want, "---\nname: "+name+"\n") {
					t.Fatalf("skills.Get(%s) = %v, body %.40q", name, ok, want)
				}
				if profile != "" {
					// The instance render: the template read is the
					// reference the tool and resource reads must match.
					inst, err := gosdk.ReadSkillViaTemplate(p, gosdk.SkillURIScheme+name)
					if err != nil || !strings.HasPrefix(inst, "---\nname: "+name+"\n") {
						t.Fatalf("template read %s: err=%v, body %.40q", name, err, inst)
					}
					want = inst
				}
				if !slices.Contains(listed, name) {
					t.Errorf("pulse_skills_list lacks %s", name)
				}
				if !slices.Contains(resources, name) {
					t.Errorf("resources/list lacks %s%s", gosdk.SkillURIScheme, name)
				}
				if got := skillGetBody(t, c, name); got != want {
					t.Errorf("pulse_skills_get %s body differs from the rendered skill", name)
				}
				res, err := c.ReadResource(context.Background(), &mcpsdk.ReadResourceParams{URI: gosdk.SkillURIScheme + name})
				if err != nil || len(res.Contents) != 1 || res.Contents[0].Text != want {
					t.Errorf("read %s%s: err=%v, body differs", gosdk.SkillURIScheme, name, err)
				}
				got, err := gosdk.ReadSkillViaTemplate(p, gosdk.SkillURIScheme+name)
				if err != nil || got != want {
					t.Errorf("template read %s%s: err=%v, body differs", gosdk.SkillURIScheme, name, err)
				}
			}
		})
	}
}

func skillGetBody(t *testing.T, c *mcpsdk.ClientSession, name string) string {
	t.Helper()
	var out struct {
		Body string `json:"body"`
	}
	if err := json.Unmarshal([]byte(resultText(t, mustCall(t, c, "pulse_skills_get", map[string]any{"name": name}))), &out); err != nil {
		t.Fatalf("decode pulse_skills_get %s: %v", name, err)
	}
	return out.Body
}

func mustCall(t *testing.T, c *mcpsdk.ClientSession, name string, args map[string]any) *mcpsdk.CallToolResult {
	t.Helper()
	res, err := c.CallTool(context.Background(), &mcpsdk.CallToolParams{Name: name, Arguments: args})
	if err != nil || res.IsError {
		t.Fatalf("%s: err=%v isError=%v", name, err, res != nil && res.IsError)
	}
	return res
}
