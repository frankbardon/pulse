package mcp_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/internal/mcp"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/spf13/afero"
)

func TestServer_ListPrompts_RoundTrip(t *testing.T) {
	fs := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	c, cancel := startInProcessClient(t, p)
	defer cancel()

	ctx := context.Background()
	out, err := c.ListPrompts(ctx, mcpgo.ListPromptsRequest{})
	if err != nil {
		t.Fatalf("ListPrompts: %v", err)
	}

	got := make([]string, len(out.Prompts))
	for i, pr := range out.Prompts {
		got[i] = pr.Name
	}
	slices.Sort(got)

	want := append([]string(nil), mcp.RegisteredPrompts()...)
	slices.Sort(want)

	if !slices.Equal(got, want) {
		t.Fatalf("prompt list mismatch\n got: %v\nwant: %v", got, want)
	}
}

func TestServer_GetPrompt_Bootstrap(t *testing.T) {
	fs := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	c, cancel := startInProcessClient(t, p)
	defer cancel()

	ctx := context.Background()
	req := mcpgo.GetPromptRequest{}
	req.Params.Name = mcp.PromptBootstrap
	res, err := c.GetPrompt(ctx, req)
	if err != nil {
		t.Fatalf("GetPrompt(bootstrap): %v", err)
	}
	if len(res.Messages) == 0 {
		t.Fatal("expected at least one message in bootstrap prompt")
	}
	text := extractText(t, res.Messages[0])
	// Sanity: prompt should mention the canonical discovery tools.
	for _, want := range []string{"pulse_manifest", "pulse_examples_search", "pulse_examples_get", "pulse_skills_get", "pulse_errors_lookup"} {
		if !strings.Contains(text, want) {
			t.Errorf("bootstrap prompt missing reference to %s", want)
		}
	}
}

func TestServer_GetPrompt_AuthorRequest(t *testing.T) {
	fs := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	c, cancel := startInProcessClient(t, p)
	defer cancel()

	ctx := context.Background()
	req := mcpgo.GetPromptRequest{}
	req.Params.Name = mcp.PromptAuthorRequest
	req.Params.Arguments = map[string]string{"question": "regression of revenue on advertising spend by region"}
	res, err := c.GetPrompt(ctx, req)
	if err != nil {
		t.Fatalf("GetPrompt(author-request): %v", err)
	}
	if len(res.Messages) == 0 {
		t.Fatal("expected at least one message in author-request prompt")
	}
	text := extractText(t, res.Messages[0])
	if !strings.Contains(text, "regression of revenue on advertising spend by region") {
		t.Errorf("author-request prompt did not interpolate the question argument; got:\n%s", text)
	}
	if !strings.Contains(text, "pulse_examples_search") {
		t.Errorf("author-request prompt missing pulse_examples_search reference")
	}
}

func extractText(t *testing.T, msg mcpgo.PromptMessage) string {
	t.Helper()
	tc, ok := msg.Content.(mcpgo.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", msg.Content)
	}
	return tc.Text
}
