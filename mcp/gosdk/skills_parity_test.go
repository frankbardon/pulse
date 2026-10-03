package gosdk_test

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/mcp/gosdk"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/afero"
)

// TestSkills_FacadeMCPParity: for one instance, the facade (p.Skills,
// p.Skill) and every MCP skill surface mounted on it — pulse_skills_list,
// pulse_skills_get, the pulse-skill:// enumeration and reads — serve the
// identical skill set, metadata and bodies, profile-free and under every
// fixture profile; a skill the profile prunes is absent everywhere and
// reads as not found on both sides.
func TestSkills_FacadeMCPParity(t *testing.T) {
	cfg := gosdk.Config{Version: "9.9.9", DisableCohortScan: true}
	full, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs()})
	if err != nil {
		t.Fatal(err)
	}
	for _, profile := range []string{"", "minimal", "survey-crosstab", "empty"} {
		t.Run("profile="+profile, func(t *testing.T) {
			var fp *pulse.FeatureProfile
			if profile != "" {
				fp = fixtureProfile(t, profile)
			}
			p, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(), FeatureProfile: fp})
			if err != nil {
				t.Fatalf("pulse.New: %v", err)
			}
			srv := newServer()
			if err := gosdk.Register(srv, p, cfg); err != nil {
				t.Fatalf("Register: %v", err)
			}
			c, cancel := connect(t, srv)
			defer cancel()

			// pulse_skills_list == p.Skills(), field for field.
			want := p.Skills()
			var listed struct {
				Skills []pulse.SkillMetadata `json:"skills"`
			}
			if err := json.Unmarshal([]byte(resultText(t, mustCall(t, c, "pulse_skills_list", map[string]any{}))), &listed); err != nil {
				t.Fatalf("decode pulse_skills_list: %v", err)
			}
			if !reflect.DeepEqual(jsonRoundTrip(t, want), jsonRoundTrip(t, listed.Skills)) {
				t.Errorf("pulse_skills_list differs from p.Skills()")
			}

			// pulse-skill:// enumeration == p.Skills() names + descriptions.
			res, err := c.ListResources(context.Background(), nil)
			if err != nil {
				t.Fatalf("ListResources: %v", err)
			}
			got := map[string]string{}
			for _, r := range res.Resources {
				if name, ok := strings.CutPrefix(r.URI, gosdk.SkillURIScheme); ok {
					got[name] = r.Description
				}
			}
			if len(got) != len(want) {
				t.Errorf("resources/list has %d skills, p.Skills() %d", len(got), len(want))
			}
			for _, md := range want {
				if d, ok := got[md.Name]; !ok || d != md.Description {
					t.Errorf("resource %s%s: listed=%v description %q, want %q", gosdk.SkillURIScheme, md.Name, ok, d, md.Description)
				}
			}

			// Bodies: pulse_skills_get == resource read == p.Skill.
			visible := map[string]bool{}
			for _, md := range want {
				visible[md.Name] = true
				body, ok := p.Skill(md.Name)
				if !ok {
					t.Errorf("p.Skill(%s) not found for a listed skill", md.Name)
					continue
				}
				if g := skillGetBody(t, c, md.Name); g != body {
					t.Errorf("pulse_skills_get %s differs from p.Skill", md.Name)
				}
				rr, err := c.ReadResource(context.Background(), &mcpsdk.ReadResourceParams{URI: gosdk.SkillURIScheme + md.Name})
				if err != nil || len(rr.Contents) != 1 || rr.Contents[0].Text != body {
					t.Errorf("read %s%s: err=%v, body differs from p.Skill", gosdk.SkillURIScheme, md.Name, err)
				}
			}

			// Pruned: not found on the facade and on both MCP read paths.
			pruned := 0
			for _, md := range full.Skills() {
				if visible[md.Name] {
					continue
				}
				pruned++
				if _, ok := p.Skill(md.Name); ok {
					t.Errorf("p.Skill(%s) reads a pruned skill", md.Name)
				}
				r, err := c.CallTool(context.Background(), &mcpsdk.CallToolParams{Name: "pulse_skills_get", Arguments: map[string]any{"name": md.Name}})
				if err == nil && !r.IsError {
					t.Errorf("pulse_skills_get %s reads a pruned skill", md.Name)
				}
				if _, err := c.ReadResource(context.Background(), &mcpsdk.ReadResourceParams{URI: gosdk.SkillURIScheme + md.Name}); err == nil {
					t.Errorf("read %s%s reads a pruned skill", gosdk.SkillURIScheme, md.Name)
				}
			}
			if profile != "" && pruned == 0 {
				t.Errorf("vacuous: profile %s prunes no skill", profile)
			}
		})
	}
}

func jsonRoundTrip(t *testing.T, v any) any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}
