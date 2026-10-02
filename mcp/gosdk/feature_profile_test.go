package gosdk_test

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/internal/mcp/toolmeta"
	"github.com/frankbardon/pulse/mcp/gosdk"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/afero"
)

// coreTools are the MCP tools bound to a core surface: every instance,
// whatever its feature profile, mounts them.
var coreTools = []string{
	"pulse_errors_lookup",
	"pulse_examples_get",
	"pulse_examples_search",
	"pulse_inspect",
	"pulse_manifest",
	"pulse_predict",
	"pulse_skills_get",
	"pulse_skills_list",
}

// fixtureProfile loads one of the private feature-profile fixtures.
func fixtureProfile(t *testing.T, name string) *pulse.FeatureProfile {
	t.Helper()
	data, err := os.ReadFile("../../descriptor/testdata/profiles/" + name + ".json")
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	fp, err := pulse.ParseFeatureProfile(data)
	if err != nil {
		t.Fatalf("parse fixture %s: %v", name, err)
	}
	return fp
}

// profiledServer builds an instance over fs with fp, mounts it on a
// fresh server and connects an in-memory client.
func profiledServer(t *testing.T, fs afero.Fs, fp *pulse.FeatureProfile, cfg gosdk.Config) (*mcpsdk.ClientSession, func()) {
	t.Helper()
	p, err := pulse.New(pulse.Options{FS: fs, FeatureProfile: fp})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	srv := newServer()
	if err := gosdk.Register(srv, p, cfg); err != nil {
		t.Fatalf("Register: %v", err)
	}
	return connect(t, srv)
}

func toolNames(t *testing.T, c *mcpsdk.ClientSession) []string {
	t.Helper()
	out, err := c.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	var names []string
	for _, tool := range out.Tools {
		names = append(names, tool.Name)
	}
	slices.Sort(names)
	return names
}

// promptNames lists the mounted prompts. A server with none registered
// does not advertise the prompts capability at all — exactly a server
// that never registered one.
func promptNames(t *testing.T, c *mcpsdk.ClientSession) []string {
	t.Helper()
	if c.InitializeResult().Capabilities.Prompts == nil {
		return nil
	}
	out, err := c.ListPrompts(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListPrompts: %v", err)
	}
	var names []string
	for _, p := range out.Prompts {
		names = append(names, p.Name)
	}
	slices.Sort(names)
	return names
}

func sortedWith(extra ...string) []string {
	out := append(append([]string(nil), coreTools...), extra...)
	slices.Sort(out)
	return out
}

// TestRegister_FeatureProfileScopesTools pins that a profiled server
// mounts exactly the core tools plus those whose feature the profile
// enables, over the three private fixtures and the profile-free arm.
func TestRegister_FeatureProfileScopesTools(t *testing.T) {
	all := toolmeta.Names()
	slices.Sort(all)
	for _, tc := range []struct {
		fixture string
		want    []string
	}{
		{fixture: "", want: all},
		{fixture: "empty", want: sortedWith()},
		{fixture: "minimal", want: sortedWith("pulse_process")},
		{fixture: "survey-crosstab", want: sortedWith("pulse_process", "pulse_facet", "pulse_facet_schema")},
	} {
		t.Run("fixture="+tc.fixture, func(t *testing.T) {
			var fp *pulse.FeatureProfile
			if tc.fixture != "" {
				fp = fixtureProfile(t, tc.fixture)
			}
			c, cancel := profiledServer(t, afero.NewMemMapFs(), fp, gosdk.Config{Version: "9.9.9", DisableCohortScan: true})
			defer cancel()
			if got := toolNames(t, c); !slices.Equal(got, tc.want) {
				t.Errorf("tools = %v\nwant    %v", got, tc.want)
			}
		})
	}
}

// TestRegister_HiddenToolCallMatchesNonexistent pins the parity rule:
// calling a hidden tool fails byte-identically (after name
// substitution) to calling a name that was never registered.
func TestRegister_HiddenToolCallMatchesNonexistent(t *testing.T) {
	c, cancel := profiledServer(t, afero.NewMemMapFs(), fixtureProfile(t, "minimal"), gosdk.Config{Version: "9.9.9", DisableCohortScan: true})
	defer cancel()
	ctx := context.Background()

	const hidden, bogus = "pulse_compose", "pulse_never_registered"
	_, hiddenErr := c.CallTool(ctx, &mcpsdk.CallToolParams{Name: hidden, Arguments: map[string]any{}})
	_, bogusErr := c.CallTool(ctx, &mcpsdk.CallToolParams{Name: bogus, Arguments: map[string]any{}})
	if hiddenErr == nil || bogusErr == nil {
		t.Fatalf("expected both calls to fail: hidden=%v bogus=%v", hiddenErr, bogusErr)
	}
	if got, want := strings.ReplaceAll(hiddenErr.Error(), hidden, bogus), bogusErr.Error(); got != want {
		t.Errorf("hidden tool error differs from a nonexistent one:\n hidden: %s\n bogus:  %s", got, want)
	}
}

// TestRegister_FeatureProfileScopesPrompts pins that each prompt mounts
// iff its mcp_extra:prompt_* feature is enabled.
func TestRegister_FeatureProfileScopesPrompts(t *testing.T) {
	for _, tc := range []struct {
		name     string
		features []string
		want     []string
	}{
		{name: "none", features: []string{}, want: nil},
		{name: "bootstrap only", features: []string{"mcp_extra:prompt_bootstrap"}, want: []string{gosdk.PromptBootstrap}},
		{name: "author only", features: []string{"mcp_extra:prompt_author_request"}, want: []string{gosdk.PromptAuthorRequest}},
		{name: "both", features: []string{"mcp_extra:prompt_bootstrap", "mcp_extra:prompt_author_request"}, want: []string{gosdk.PromptAuthorRequest, gosdk.PromptBootstrap}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, cancel := profiledServer(t, afero.NewMemMapFs(), &pulse.FeatureProfile{Features: tc.features}, gosdk.Config{Version: "9.9.9", DisableCohortScan: true})
			defer cancel()
			if got := promptNames(t, c); !slices.Equal(got, tc.want) {
				t.Errorf("prompts = %v, want %v", got, tc.want)
			}
		})
	}
	for _, fixture := range []string{"empty", "minimal", "survey-crosstab"} {
		t.Run("fixture="+fixture, func(t *testing.T) {
			c, cancel := profiledServer(t, afero.NewMemMapFs(), fixtureProfile(t, fixture), gosdk.Config{Version: "9.9.9", DisableCohortScan: true})
			defer cancel()
			if got := promptNames(t, c); len(got) != 0 {
				t.Errorf("fixture %s enables no prompt, got %v", fixture, got)
			}
		})
	}
}

// TestRegister_FeatureProfileScopesCohortEnumeration pins that a profile
// omitting mcp_extra:cohort_resources withholds the pulse:// enumeration
// (no startup walk) while the template keeps every cohort readable,
// and that enabling the feature restores the enumeration.
func TestRegister_FeatureProfileScopesCohortEnumeration(t *testing.T) {
	for _, tc := range []struct {
		name     string
		features []string
		wantEnum bool
	}{
		{name: "hidden", features: []string{}, wantEnum: false},
		{name: "enabled", features: []string{"mcp_extra:cohort_resources"}, wantEnum: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fs := &countingFs{Fs: afero.NewMemMapFs()}
			writeTestCohort(t, fs, "demo.pulse")
			p, err := pulse.New(pulse.Options{FS: fs, FeatureProfile: &pulse.FeatureProfile{Features: tc.features}})
			if err != nil {
				t.Fatalf("pulse.New: %v", err)
			}
			srv := newServer()
			fs.reset()
			if err := gosdk.Register(srv, p, gosdk.Config{Version: "9.9.9"}); err != nil {
				t.Fatalf("Register: %v", err)
			}
			if walked := fs.count() > 0; walked != tc.wantEnum {
				t.Errorf("filesystem walked = %v, want %v", walked, tc.wantEnum)
			}

			c, cancel := connect(t, srv)
			defer cancel()
			ctx := context.Background()
			res, err := c.ListResources(ctx, nil)
			if err != nil {
				t.Fatalf("ListResources: %v", err)
			}
			enumerated := false
			for _, r := range res.Resources {
				if r.URI == "pulse://demo.pulse" {
					enumerated = true
				}
			}
			if enumerated != tc.wantEnum {
				t.Errorf("pulse://demo.pulse enumerated = %v, want %v", enumerated, tc.wantEnum)
			}
			read, err := c.ReadResource(ctx, &mcpsdk.ReadResourceParams{URI: "pulse://demo.pulse"})
			if err != nil {
				t.Fatalf("ReadResource(pulse://demo.pulse): %v", err)
			}
			if len(read.Contents) == 0 || !strings.Contains(read.Contents[0].Text, "score") {
				t.Errorf("cohort read did not return the schema: %+v", read.Contents)
			}
		})
	}
}

// TestRegister_BindOnInspectDoesNotResurrectHiddenTools pins that the
// schema-bind-on-inspect rebind (which re-adds tools by name) skips a
// tool the instance hides, while still binding the enabled ones.
func TestRegister_BindOnInspectDoesNotResurrectHiddenTools(t *testing.T) {
	fs := afero.NewMemMapFs()
	writeRichCohort(t, fs, "rich.pulse")
	c, cancel := profiledServer(t, fs, fixtureProfile(t, "minimal"), gosdk.Config{Version: "9.9.9", BindOnInspect: true, DisableCohortScan: true})
	defer cancel()
	ctx := context.Background()

	if _, err := c.CallTool(ctx, &mcpsdk.CallToolParams{Name: toolmeta.ToolInspect, Arguments: map[string]any{"path": "rich.pulse"}}); err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if !processToolBound(t, c) {
		t.Fatal("enabled pulse_process was not rebound: the hidden-tool assertion below would be vacuous")
	}
	if got, want := toolNames(t, c), sortedWith("pulse_process"); !slices.Equal(got, want) {
		t.Errorf("tools after bind = %v\nwant              %v", got, want)
	}
}

// TestRegister_CanonicalListsIgnoreFeatureProfile pins that the global
// canonical lists describe the build, not an instance.
func TestRegister_CanonicalListsIgnoreFeatureProfile(t *testing.T) {
	_, cancel := profiledServer(t, afero.NewMemMapFs(), fixtureProfile(t, "empty"), gosdk.Config{Version: "9.9.9", DisableCohortScan: true})
	defer cancel()
	if got := gosdk.RegisteredTools(); !slices.Equal(got, toolmeta.Names()) {
		t.Errorf("RegisteredTools = %v, want toolmeta.Names()", got)
	}
	if got := gosdk.RegisteredPrompts(); !slices.Equal(got, []string{gosdk.PromptBootstrap, gosdk.PromptAuthorRequest}) {
		t.Errorf("RegisteredPrompts = %v", got)
	}
}
