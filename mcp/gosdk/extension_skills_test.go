package gosdk_test

import (
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/mcp/gosdk"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

type extSkillStubAgg struct{}

func (extSkillStubAgg) Aggregate(extend.Rows, string) (float64, error) { return 0, nil }

func extSkillStubFactory(*types.Aggregation, *encoding.Schema) (extend.Aggregator, error) {
	return extSkillStubAgg{}, nil
}

func extSkillDoc(name, op string) string {
	return "---\nname: " + name + "\ndescription: An Acme aggregate.\nkind: operator\ncategory: AGG\noperator: " + op +
		"\ntype: reference\napplies_to: process\n---\n\n# " + op +
		"\n\n## Params\nNone.\n\n## Inputs\nA numeric field.\n\n## Output\nA float64.\n\n## Components\nThe floor.\n\n## Gotchas\nNone.\n\n## See\n`aggregation-design`\n"
}

func extSkillPulse(t *testing.T, fp *pulse.FeatureProfile) *pulse.Pulse {
	t.Helper()
	p, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(), FeatureProfile: fp, Extensions: pulse.Extensions{
		Aggregators: []pulse.AggregatorRegistration{
			{Name: "AGG_ACME_KEPT", Description: "Stub.", Factory: extSkillStubFactory},
			{Name: "AGG_ACME_BRAND", Description: "Stub.", Factory: extSkillStubFactory},
		},
		Skills: fstest.MapFS{
			"op-agg-acme-kept.md":  {Data: []byte(extSkillDoc("op-agg-acme-kept", "AGG_ACME_KEPT"))},
			"op-agg-acme-brand.md": {Data: []byte(extSkillDoc("op-agg-acme-brand", "AGG_ACME_BRAND"))},
		},
	}})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	return p
}

// TestExtensionSkills_MCP: an embedder skill is served over MCP exactly
// like a built-in — pulse_skills_list, pulse_skills_get, the
// pulse-skill:// enumeration and reads — and one whose operator a
// feature profile hides reads like a name that never existed on every
// one of those paths.
func TestExtensionSkills_MCP(t *testing.T) {
	cfg := gosdk.Config{Version: "9.9.9", DisableCohortScan: true}
	for _, tc := range []struct {
		name    string
		profile *pulse.FeatureProfile
		visible []string
		hidden  []string
	}{
		{"profile-free", nil, []string{"op-agg-acme-kept", "op-agg-acme-brand"}, nil},
		{"brand hidden", &pulse.FeatureProfile{Features: []string{"capability:process", "AGG_ACME_KEPT"}}, []string{"op-agg-acme-kept"}, []string{"op-agg-acme-brand"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := extSkillPulse(t, tc.profile)
			srv := newServer()
			if err := gosdk.Register(srv, p, cfg); err != nil {
				t.Fatalf("Register: %v", err)
			}
			c, cancel := connect(t, srv)
			defer cancel()

			listed, resources := skillsListed(t, c), skillResourcesListed(t, c)
			for _, name := range tc.visible {
				want, ok := p.Skill(name)
				if !ok {
					t.Fatalf("p.Skill(%s) not found", name)
				}
				if !slices.Contains(listed, name) || !slices.Contains(resources, name) {
					t.Errorf("%s: in pulse_skills_list=%v resources=%v, want both", name, slices.Contains(listed, name), slices.Contains(resources, name))
				}
				if got := skillGetBody(t, c, name); got != want {
					t.Errorf("%s: pulse_skills_get body differs from p.Skill", name)
				}
				if got := templateReadRaw(p, gosdk.SkillURIScheme+name); got != "ok: "+want {
					t.Errorf("%s: template read differs from p.Skill", name)
				}
			}
			neverGet := callRaw(t, c, "pulse_skills_get", map[string]any{"name": neverSkill})
			neverRead := readResourceRaw(c, gosdk.SkillURIScheme+neverSkill)
			neverTmpl := templateReadRaw(p, gosdk.SkillURIScheme+neverSkill)
			for _, name := range tc.hidden {
				if slices.Contains(listed, name) || slices.Contains(resources, name) {
					t.Errorf("%s: hidden skill listed", name)
				}
				substEqual(t, "pulse_skills_get", name, neverSkill, callRaw(t, c, "pulse_skills_get", map[string]any{"name": name}), neverGet)
				substEqual(t, "resource read", name, neverSkill, readResourceRaw(c, gosdk.SkillURIScheme+name), neverRead)
				substEqual(t, "template read", name, neverSkill, templateReadRaw(p, gosdk.SkillURIScheme+name), neverTmpl)
			}
		})
	}
}

func extExampleDoc(name, op string) string {
	return `{"_meta": {"name": "` + name + `", "category": "acme", "description": "An Acme aggregate over revenue.",
  "tags": ["financial"], "operators": ["` + op + `"]},
  "cohort": {"filename": "sales.pulse"}, "aggregations": [{"type": "` + op + `", "field": "revenue"}]}`
}

// TestExtensionExamples_MCP: an embedder example is served over MCP
// exactly like a built-in — pulse_examples_search and pulse_examples_get
// agree with the facade — and one whose operator a feature profile hides
// reads like a name that never existed.
func TestExtensionExamples_MCP(t *testing.T) {
	cfg := gosdk.Config{Version: "9.9.9", DisableCohortScan: true}
	const never = "acme-never-registered"
	for _, tc := range []struct {
		name    string
		profile *pulse.FeatureProfile
		visible []string
		hidden  []string
	}{
		{"profile-free", nil, []string{"acme-kept", "acme-brand"}, nil},
		{"brand hidden", &pulse.FeatureProfile{Features: []string{"capability:process", "AGG_ACME_KEPT"}}, []string{"acme-kept"}, []string{"acme-brand"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(), FeatureProfile: tc.profile, Extensions: pulse.Extensions{
				Aggregators: []pulse.AggregatorRegistration{
					{Name: "AGG_ACME_KEPT", Description: "Stub.", Factory: extSkillStubFactory},
					{Name: "AGG_ACME_BRAND", Description: "Stub.", Factory: extSkillStubFactory},
				},
				Examples: fstest.MapFS{
					"kept.json":  {Data: []byte(extExampleDoc("acme-kept", "AGG_ACME_KEPT"))},
					"brand.json": {Data: []byte(extExampleDoc("acme-brand", "AGG_ACME_BRAND"))},
				},
			}})
			if err != nil {
				t.Fatalf("pulse.New: %v", err)
			}
			srv := newServer()
			if err := gosdk.Register(srv, p, cfg); err != nil {
				t.Fatalf("Register: %v", err)
			}
			c, cancel := connect(t, srv)
			defer cancel()

			listed := examplesListed(t, c)
			for _, name := range tc.visible {
				ex, ok := p.ExampleGet(name)
				if !ok {
					t.Fatalf("p.ExampleGet(%s) not found", name)
				}
				if !slices.Contains(listed, name) {
					t.Errorf("%s: not in pulse_examples_search", name)
				}
				if got := callRaw(t, c, "pulse_examples_get", map[string]any{"name": name}); !strings.Contains(got, ex.Description) || strings.Contains(got, "isError\":true") {
					t.Errorf("%s: pulse_examples_get = %s", name, got)
				}
			}
			neverGet := callRaw(t, c, "pulse_examples_get", map[string]any{"name": never})
			for _, name := range tc.hidden {
				if slices.Contains(listed, name) {
					t.Errorf("%s: hidden example listed", name)
				}
				substEqual(t, "pulse_examples_get", name, never, callRaw(t, c, "pulse_examples_get", map[string]any{"name": name}), neverGet)
			}
		})
	}
}
