package gosdk_test

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/mcp/gosdk"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/afero"
)

// callRaw calls a tool and returns its whole result as JSON (error
// results included), or the protocol error's text.
func callRaw(t *testing.T, c *mcpsdk.ClientSession, name string, args map[string]any) string {
	t.Helper()
	res, err := c.CallTool(context.Background(), &mcpsdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return "protocol error: " + err.Error()
	}
	raw, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("marshal %s result: %v", name, err)
	}
	return string(raw)
}

// skillsListed returns the skill names pulse_skills_list reports.
func skillsListed(t *testing.T, c *mcpsdk.ClientSession) []string {
	t.Helper()
	res, err := c.CallTool(context.Background(), &mcpsdk.CallToolParams{Name: "pulse_skills_list", Arguments: map[string]any{}})
	if err != nil || res.IsError {
		t.Fatalf("pulse_skills_list: %v %+v", err, res)
	}
	var out struct {
		Skills []struct {
			Name string `json:"name"`
		} `json:"skills"`
	}
	if err := json.Unmarshal([]byte(resultText(t, res)), &out); err != nil {
		t.Fatalf("decode skills list: %v", err)
	}
	names := make([]string, len(out.Skills))
	for i, s := range out.Skills {
		names[i] = s.Name
	}
	return names
}

// examplesListed returns the example names an unfiltered
// pulse_examples_search reports.
func examplesListed(t *testing.T, c *mcpsdk.ClientSession) []string {
	t.Helper()
	res, err := c.CallTool(context.Background(), &mcpsdk.CallToolParams{Name: "pulse_examples_search", Arguments: map[string]any{}})
	if err != nil || res.IsError {
		t.Fatalf("pulse_examples_search: %v %+v", err, res)
	}
	var out struct {
		Results []struct {
			Name string `json:"name"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(resultText(t, res)), &out); err != nil {
		t.Fatalf("decode examples search: %v", err)
	}
	names := make([]string, len(out.Results))
	for i, r := range out.Results {
		names[i] = r.Name
	}
	return names
}

// resultText is the text content a tool result carries.
func resultText(t *testing.T, res *mcpsdk.CallToolResult) string {
	t.Helper()
	if len(res.Content) == 0 {
		t.Fatalf("tool result has no content")
	}
	tc, ok := res.Content[0].(*mcpsdk.TextContent)
	if !ok {
		t.Fatalf("tool result content is %T, want text", res.Content[0])
	}
	return tc.Text
}

func skillResourcesListed(t *testing.T, c *mcpsdk.ClientSession) []string {
	t.Helper()
	res, err := c.ListResources(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	var out []string
	for _, r := range res.Resources {
		if name, ok := strings.CutPrefix(r.URI, gosdk.SkillURIScheme); ok {
			out = append(out, name)
		}
	}
	return out
}

func readResourceRaw(c *mcpsdk.ClientSession, uri string) string {
	res, err := c.ReadResource(context.Background(), &mcpsdk.ReadResourceParams{URI: uri})
	if err != nil {
		return "error: " + err.Error()
	}
	raw, _ := json.Marshal(res)
	return string(raw)
}

// TestRegister_DiscoveryHonoursFeatureProfile pins the skill / example
// prune on every MCP discovery path: a skill or example for a hidden
// surface is absent from pulse_skills_list, pulse_examples_search and
// the pulse-skill:// enumeration, and an exact-name read of it is
// byte-identical (after name substitution) to a name that never existed.
// The manifest's skills list and examples count agree with the tools.
func TestRegister_DiscoveryHonoursFeatureProfile(t *testing.T) {
	cfg := gosdk.Config{Version: "9.9.9", DisableCohortScan: true}
	full, cancelFull := profiledServer(t, afero.NewMemMapFs(), nil, cfg)
	defer cancelFull()
	c, cancel := profiledServer(t, afero.NewMemMapFs(), fixtureProfile(t, "minimal"), cfg)
	defer cancel()

	// Skills: hidden operator skill, hidden tool skill; kept type,
	// topical and core-tool skills.
	listed := skillsListed(t, c)
	for _, gone := range []string{"op-reg-glm", "op-agg-average", "op-overlay-yoy", "op-synth-normal", "tool-lookup", "tool-compose"} {
		if slices.Contains(listed, gone) {
			t.Errorf("pulse_skills_list lists hidden skill %s", gone)
		}
		if !slices.Contains(skillsListed(t, full), gone) {
			t.Fatalf("profile-free pulse_skills_list lacks %s (test premise)", gone)
		}
	}
	for _, kept := range []string{"op-agg-sum", "type-u8", "crosstab-guide", "tool-inspect", "tool-process"} {
		if !slices.Contains(listed, kept) {
			t.Errorf("pulse_skills_list dropped visible skill %s", kept)
		}
	}
	resources := skillResourcesListed(t, c)
	if !slices.Equal(resources, listed) {
		t.Errorf("pulse-skill:// enumeration disagrees with pulse_skills_list:\n res:  %v\n list: %v", resources, listed)
	}

	const hiddenSkill, bogusSkill = "op-reg-glm", "op-never-existed"
	got := strings.ReplaceAll(callRaw(t, c, "pulse_skills_get", map[string]any{"name": hiddenSkill}), hiddenSkill, bogusSkill)
	if want := callRaw(t, c, "pulse_skills_get", map[string]any{"name": bogusSkill}); got != want {
		t.Errorf("pulse_skills_get hidden != nonexistent:\n hidden: %s\n bogus:  %s", got, want)
	}
	got = strings.ReplaceAll(readResourceRaw(c, gosdk.SkillURIScheme+hiddenSkill), hiddenSkill, bogusSkill)
	if want := readResourceRaw(c, gosdk.SkillURIScheme+bogusSkill); got != want {
		t.Errorf("pulse-skill:// read hidden != nonexistent:\n hidden: %s\n bogus:  %s", got, want)
	}
	// Topical bodies are served whole (unrendered) on a profiled instance.
	if a, b := callRaw(t, c, "pulse_skills_get", map[string]any{"name": "crosstab-guide"}), callRaw(t, full, "pulse_skills_get", map[string]any{"name": "crosstab-guide"}); a != b {
		t.Errorf("topical skill body changed under a feature profile")
	}

	// Examples.
	fullEx := examplesListed(t, full)
	ex := examplesListed(t, c)
	if len(ex) >= len(fullEx) {
		t.Fatalf("profiled examples (%d) not pruned from %d", len(ex), len(fullEx))
	}
	var hiddenEx string
	for _, n := range fullEx {
		if !slices.Contains(ex, n) {
			hiddenEx = n
			break
		}
	}
	const bogusEx = "example-never-existed"
	got = strings.ReplaceAll(callRaw(t, c, "pulse_examples_get", map[string]any{"name": hiddenEx}), hiddenEx, bogusEx)
	if want := callRaw(t, c, "pulse_examples_get", map[string]any{"name": bogusEx}); got != want {
		t.Errorf("pulse_examples_get hidden != nonexistent:\n hidden: %s\n bogus:  %s", got, want)
	}
	if hits := callRaw(t, c, "pulse_examples_search", map[string]any{"query": hiddenEx}); strings.Contains(hits, `"`+hiddenEx+`"`) {
		t.Errorf("pulse_examples_search by name returns hidden example %s", hiddenEx)
	}

	// The manifest agrees with the tools.
	p, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(), FeatureProfile: fixtureProfile(t, "minimal")})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	m := p.Manifest(context.Background())
	if m.ExamplesCount != len(ex) {
		t.Errorf("manifest examples_count = %d, pulse_examples_search returns %d", m.ExamplesCount, len(ex))
	}
	var mSkills []string
	for _, s := range m.Skills {
		mSkills = append(mSkills, s.Name)
	}
	if !slices.Equal(mSkills, listed) {
		t.Errorf("manifest skills disagree with pulse_skills_list:\n manifest: %v\n tool:     %v", mSkills, listed)
	}
}

// TestRegister_DiscoveryProfileFreeUnchanged pins that a profile-free
// instance and a profile that hides nothing serve the full skill pack and
// example library.
func TestRegister_DiscoveryProfileFreeUnchanged(t *testing.T) {
	cfg := gosdk.Config{Version: "9.9.9", DisableCohortScan: true}
	full, cancel := profiledServer(t, afero.NewMemMapFs(), nil, cfg)
	defer cancel()
	p, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs()})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	m := p.Manifest(context.Background())
	if got := len(skillsListed(t, full)); got != len(m.Skills) || got == 0 {
		t.Errorf("profile-free skills listed %d, manifest %d", got, len(m.Skills))
	}
	if got := len(examplesListed(t, full)); got != m.ExamplesCount || got == 0 {
		t.Errorf("profile-free examples listed %d, manifest %d", got, m.ExamplesCount)
	}
	if got, want := len(skillResourcesListed(t, full)), len(m.Skills); got != want {
		t.Errorf("profile-free pulse-skill:// enumerated %d, want %d", got, want)
	}
}
