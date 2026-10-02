package gosdk_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/facadebridge"
	"github.com/frankbardon/pulse/internal/mcp/toolmeta"
	"github.com/frankbardon/pulse/mcp/gosdk"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/afero"
)

// promptsProfile enables both prompts and the facet capability but no
// request host, so the prompt bodies' pulse_process steps name a hidden
// tool and the scrub has something to remove.
var promptsProfile = []string{
	"mcp_extra:prompt_bootstrap",
	"mcp_extra:prompt_author_request",
	"capability:facet",
}

// hiddenProseTokens derives, independently of the scrub, the tokens an
// instance must never name in prose: every bare (operator) feature it
// does not enable plus every MCP tool whose owning feature is off.
func hiddenProseTokens(t *testing.T, p *pulse.Pulse) map[string]bool {
	t.Helper()
	inst := facadebridge.InstanceSnapshot(p)
	out := map[string]bool{}
	for _, n := range descx.FeatureNames() {
		if !strings.Contains(n, ":") && !inst.Enabled(n) {
			out[n] = true
		}
	}
	for _, b := range descx.MCPToolBindings() {
		if b.Feature != "" && !inst.Enabled(b.Feature) {
			out[b.Tool] = true
		}
	}
	if len(out) == 0 {
		t.Fatal("profile hides nothing: the sweep would be vacuous")
	}
	return out
}

// leakedTokens returns the hidden tokens text names as whole
// [A-Za-z0-9_] runs (the scrub's own token rule).
func leakedTokens(text string, hidden map[string]bool) []string {
	var out []string
	seen := map[string]bool{}
	for _, tok := range strings.FieldsFunc(text, func(r rune) bool {
		return r != '_' && (r < '0' || r > '9') && (r < 'A' || r > 'Z') && (r < 'a' || r > 'z')
	}) {
		if hidden[tok] && !seen[tok] {
			seen[tok] = true
			out = append(out, tok)
		}
	}
	return out
}

// mcpProse renders everything a client can read as prose: tools/list
// (descriptions + input schemas), prompts/list and every prompts/get.
func mcpProse(t *testing.T, c *mcpsdk.ClientSession) map[string]string {
	t.Helper()
	ctx := context.Background()
	out := map[string]string{}
	tools, err := c.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	raw, _ := json.Marshal(tools.Tools)
	out["tools/list"] = string(raw)
	if c.InitializeResult().Capabilities.Prompts == nil {
		return out
	}
	prompts, err := c.ListPrompts(ctx, nil)
	if err != nil {
		t.Fatalf("ListPrompts: %v", err)
	}
	raw, _ = json.Marshal(prompts.Prompts)
	out["prompts/list"] = string(raw)
	for _, pr := range prompts.Prompts {
		got, err := c.GetPrompt(ctx, &mcpsdk.GetPromptParams{Name: pr.Name, Arguments: map[string]string{"question": "q"}})
		if err != nil {
			t.Fatalf("GetPrompt %s: %v", pr.Name, err)
		}
		raw, _ = json.Marshal(got)
		out["prompts/get "+pr.Name] = string(raw)
	}
	return out
}

// TestRegister_ProseNamesNoHiddenFeature pins that, for every fixture
// profile and a prompt-enabling inline one, nothing a client reads —
// tool descriptions, input-schema descriptions (before and after the
// bind-on-inspect rebind), prompt descriptions and prompt bodies —
// names a hidden operator or a tool the instance does not mount.
func TestRegister_ProseNamesNoHiddenFeature(t *testing.T) {
	profiles := map[string]*pulse.FeatureProfile{
		"empty":           fixtureProfile(t, "empty"),
		"minimal":         fixtureProfile(t, "minimal"),
		"survey-crosstab": fixtureProfile(t, "survey-crosstab"),
		"prompts":         {Features: promptsProfile},
		// pulse_dedup's reflected input schema points at pulse_import,
		// which this profile hides: the registration-time schema scrub.
		"dedup": {Features: []string{"capability:dedup"}},
	}
	for name, fp := range profiles {
		t.Run(name, func(t *testing.T) {
			fs := afero.NewMemMapFs()
			writeRichCohort(t, fs, "rich.pulse")
			p, err := pulse.New(pulse.Options{FS: fs, FeatureProfile: fp})
			if err != nil {
				t.Fatalf("pulse.New: %v", err)
			}
			hidden := hiddenProseTokens(t, p)
			srv := newServer()
			if err := gosdk.Register(srv, p, gosdk.Config{Version: "9.9.9", BindOnInspect: true, DisableCohortScan: true}); err != nil {
				t.Fatalf("Register: %v", err)
			}
			c, cancel := connect(t, srv)
			defer cancel()
			check := func(phase string) {
				for surface, text := range mcpProse(t, c) {
					if leaked := leakedTokens(text, hidden); len(leaked) > 0 {
						t.Errorf("%s %s names hidden %v", phase, surface, leaked)
					}
				}
			}
			check("registered")
			if _, err := c.CallTool(context.Background(), &mcpsdk.CallToolParams{Name: toolmeta.ToolInspect, Arguments: map[string]any{"path": "rich.pulse"}}); err != nil {
				t.Fatalf("inspect: %v", err)
			}
			check("rebound")
		})
	}
}

// TestRegister_ProseScrubKeepsEnabledProse pins that the scrub removes
// only what it must: on the prompt-enabling profile the bootstrap body
// keeps its structure and core steps, the author-request body keeps the
// caller's question verbatim, and the process-only sentence is gone.
func TestRegister_ProseScrubKeepsEnabledProse(t *testing.T) {
	c, cancel := profiledServer(t, afero.NewMemMapFs(), &pulse.FeatureProfile{Features: promptsProfile}, gosdk.Config{Version: "9.9.9", DisableCohortScan: true})
	defer cancel()
	ctx := context.Background()
	boot, err := c.GetPrompt(ctx, &mcpsdk.GetPromptParams{Name: gosdk.PromptBootstrap})
	if err != nil {
		t.Fatalf("GetPrompt: %v", err)
	}
	body := boot.Messages[0].Content.(*mcpsdk.TextContent).Text
	for _, want := range []string{"# Recommended flow for a new question", "4. Call `pulse_predict` to validate the assembled request.\n5. On any error code", "pulse_errors_lookup", "\n\n# When the example library"} {
		if !strings.Contains(body, want) {
			t.Errorf("scrubbed bootstrap body lost %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "\n\n\n") || strings.Contains(body, "pulse_process") {
		t.Errorf("scrubbed bootstrap body is malformed or leaks pulse_process:\n%s", body)
	}
	const q = "Why did AGG_MEDIAN rise via pulse_process?"
	author, err := c.GetPrompt(ctx, &mcpsdk.GetPromptParams{Name: gosdk.PromptAuthorRequest, Arguments: map[string]string{"question": q}})
	if err != nil {
		t.Fatalf("GetPrompt: %v", err)
	}
	text := author.Messages[0].Content.(*mcpsdk.TextContent).Text
	if !strings.Contains(text, "> "+q+"\n") {
		t.Errorf("caller's question was altered:\n%s", text)
	}
	if !strings.Contains(text, "5. Submit the assembled request to `pulse_predict` to validate.") || strings.Count(text, "pulse_process") != 1 {
		t.Errorf("author-request flow not scrubbed sentence by sentence:\n%s", text)
	}
}

// TestRegister_ProseProfileFreeUnchanged pins that a profile-free
// server's tool descriptions are the toolmeta text verbatim and its
// prompt bodies keep every tool name.
func TestRegister_ProseProfileFreeUnchanged(t *testing.T) {
	c, cancel := profiledServer(t, afero.NewMemMapFs(), nil, gosdk.Config{Version: "9.9.9", DisableCohortScan: true})
	defer cancel()
	tools, err := c.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	want := map[string]string{}
	for _, m := range toolmeta.Meta() {
		want[m.Name] = m.Description
	}
	for _, tool := range tools.Tools {
		if tool.Description != want[tool.Name] {
			t.Errorf("profile-free %s description changed", tool.Name)
		}
	}
	boot, err := c.GetPrompt(context.Background(), &mcpsdk.GetPromptParams{Name: gosdk.PromptBootstrap})
	if err != nil {
		t.Fatalf("GetPrompt: %v", err)
	}
	if body := boot.Messages[0].Content.(*mcpsdk.TextContent).Text; !strings.Contains(body, "Then call `pulse_process` to execute it.") {
		t.Errorf("profile-free bootstrap body changed:\n%s", body)
	}
}
