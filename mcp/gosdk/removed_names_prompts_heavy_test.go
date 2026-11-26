//go:build heavy

package gosdk_test

import (
	"context"
	"io/fs"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/internal/doccheck"
	"github.com/frankbardon/pulse/mcp/gosdk"
)

// removedNamesPromptsAllowlist is the heavy sibling's own shrink-only
// baseline: path = "prompt <name>" or "reference <file>", entry = the
// denied name.
const removedNamesPromptsAllowlist = "removed-names-prompts"

// TestNoRemovedNamesInPromptsAndReference (U32b E1-S2) is the heavy
// sibling of the root TestNoRemovedNamesInDocs: it holds the served text
// that check does not reach to the same denylist (doccheck.RemovedNames,
// whole word, an io.<Name> entry also as pio.<Name>). Sources: every MCP
// prompt a default server mounts (its description, its argument
// descriptions and the body prompts/get returns), and every file the
// default instance's ExportReference writes (catalog, reading pages,
// glossary, skills). Hits are reconciled with the shrink-only allowlist
// internal/doccheck/allowlists/removed-names-prompts.txt.
func TestNoRemovedNamesInPromptsAndReference(t *testing.T) {
	t.Parallel()

	sources := map[string]string{}
	p := newPulse(t, afero.NewMemMapFs())

	srv := newServer()
	if err := gosdk.Register(srv, p, gosdk.Config{Version: "1.0.0"}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	c, cancel := connect(t, srv)
	defer cancel()
	for name, text := range promptSources(t, c) {
		sources["prompt "+name] = text
	}

	ref := afero.NewMemMapFs()
	if err := p.ExportReference(ref, "ref", pulse.ExportReferenceOptions{}); err != nil {
		t.Fatalf("ExportReference: %v", err)
	}
	n := 0
	err := afero.Walk(ref, "ref", func(p string, info fs.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		b, err := afero.ReadFile(ref, p)
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(filepath.ToSlash(p), "ref/")
		sources["reference "+path.Clean(rel)] = string(b)
		n++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n < 100 {
		t.Fatalf("ExportReference wrote only %d files — the reference walk is broken", n)
	}

	allow, err := doccheck.Load(afero.NewBasePathFs(afero.NewOsFs(), "../.."), doccheck.AllowlistFile(removedNamesPromptsAllowlist))
	if err != nil {
		t.Fatal(err)
	}
	for _, msg := range allow.Reconcile(doccheck.CheckRemovedNames(sources, doccheck.RemovedNames)).Problems(allow.File) {
		t.Error(msg)
	}
}

// promptSources returns, per mounted prompt, the text a client reads: the
// description, every argument description and every message of the body
// (each argument given a placeholder value). Every prompt the build can
// mount must be mounted on a default server, or the scan is partial.
func promptSources(t *testing.T, c *mcpsdk.ClientSession) map[string]string {
	t.Helper()
	ctx := context.Background()
	list, err := c.ListPrompts(ctx, nil)
	if err != nil {
		t.Fatalf("ListPrompts: %v", err)
	}
	out := map[string]string{}
	for _, pr := range list.Prompts {
		text := []string{pr.Description}
		args := map[string]string{}
		for _, a := range pr.Arguments {
			text = append(text, a.Description)
			args[a.Name] = "x"
		}
		got, err := c.GetPrompt(ctx, &mcpsdk.GetPromptParams{Name: pr.Name, Arguments: args})
		if err != nil {
			t.Fatalf("GetPrompt %s: %v", pr.Name, err)
		}
		text = append(text, got.Description)
		for _, m := range got.Messages {
			tc, ok := m.Content.(*mcpsdk.TextContent)
			if !ok {
				t.Fatalf("prompt %s message content = %T, want *TextContent", pr.Name, m.Content)
			}
			text = append(text, tc.Text)
		}
		out[pr.Name] = strings.Join(text, "\n")
	}
	want := gosdk.RegisteredPrompts()
	got := make([]string, 0, len(out))
	for name := range out {
		got = append(got, name)
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("default server mounts prompts %v, want every registered prompt %v", got, want)
	}
	return out
}
