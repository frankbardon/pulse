package skills_test

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/internal/mcp/toolmeta"
	"github.com/frankbardon/pulse/internal/obsprom"
	"github.com/frankbardon/pulse/internal/skills"
)

func TestSkillsCoverAllMCPTools(t *testing.T) {
	// Build the membership set from skills.List() — the loader is the
	// canonical embed surface and avoids re-embedding here.
	skillSet := make(map[string]bool, len(skills.List()))
	for _, m := range skills.List() {
		skillSet[m.Name] = true
	}
	for _, name := range toolmeta.Names() {
		stem := "tool-" + strings.ReplaceAll(strings.ToLower(strings.TrimPrefix(name, "pulse_")), "_", "-")
		if !skillSet[stem] {
			t.Errorf("MCP tool %q: missing atomic skill skills/%s.md", name, stem)
		}
	}
}

// TestToolDescriptionsLeadWithWhenToUse ties every MCP tool's description
// lead to its skill: the first line of the tool skill's `## When to use`
// section is ONE `CALL …` trigger sentence, and the toolmeta description
// opens with exactly that sentence. An agent choosing a tool from the
// tool list reads when to call it first, in the same words the skill uses.
// The lead carries no fence (it is served whole on every instance) and no
// internal sentence break (it is one sentence, so "first sentence" is
// unambiguous).
func TestToolDescriptionsLeadWithWhenToUse(t *testing.T) {
	for _, m := range toolmeta.Meta() {
		stem := "tool-" + strings.ReplaceAll(strings.TrimPrefix(m.Name, "pulse_"), "_", "-")
		lead, err := whenToUseLead(stem)
		if err != nil {
			t.Errorf("%s: %v", m.Name, err)
			continue
		}
		switch {
		case !strings.HasPrefix(lead, "CALL "):
			t.Errorf("%s: skills/%s.md `## When to use` first line must open with \"CALL \": %q", m.Name, stem, lead)
		case !strings.HasSuffix(lead, "."):
			t.Errorf("%s: skills/%s.md `## When to use` first line must be one sentence ending in \".\": %q", m.Name, stem, lead)
		case strings.Contains(strings.TrimSuffix(lead, "."), ". "):
			t.Errorf("%s: skills/%s.md `## When to use` first line must be ONE sentence; put the rest on the next line: %q", m.Name, stem, lead)
		case strings.Contains(lead, "<!--"):
			t.Errorf("%s: skills/%s.md `## When to use` first line must carry no feature fence: %q", m.Name, stem, lead)
		}
		rest, ok := strings.CutPrefix(m.Description, lead)
		if !ok || (rest != "" && rest[0] != ' ' && rest[0] != '\n') {
			t.Errorf("%s: toolmeta description must open with the skill's when-to-use sentence %q; got %q", m.Name, lead, firstChars(m.Description, len(lead)+20))
		}
	}
}

// whenToUseLead returns the first non-blank line under a skill's
// `## When to use` heading, read from the raw embedded body.
func whenToUseLead(stem string) (string, error) {
	raw, ok := skills.Raw(stem)
	if !ok {
		return "", fmt.Errorf("missing skill %s", stem)
	}
	lines := strings.Split(skills.StripFrontmatter(raw), "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) != "## When to use" {
			continue
		}
		for _, next := range lines[i+1:] {
			if strings.HasPrefix(next, "## ") {
				break
			}
			if s := strings.TrimSpace(next); s != "" {
				return s, nil
			}
		}
		return "", fmt.Errorf("skills/%s.md `## When to use` is empty", stem)
	}
	return "", fmt.Errorf("skills/%s.md has no `## When to use` section", stem)
}

func firstChars(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// TestMCPToolMentionsAreRegistered is the reverse direction: every
// backticked `pulse_<name>` token in the skill pack, the mdBook source,
// .claude/reference and the README must name a registered MCP tool. The
// docs once pointed agents at `pulse_profile`, `pulse_profile_create` and
// `pulse_synth_from_*` — tools that never existed.
func TestMCPToolMentionsAreRegistered(t *testing.T) {
	registered := map[string]bool{}
	for _, name := range toolmeta.Names() {
		registered[name] = true
	}
	// Backticked pulse_* tokens that are JSON keys, not tools.
	notTools := map[string]bool{"pulse_format_version": true, "pulse_type": true}

	var files []string
	for _, pattern := range []string{"*.md", "../../.claude/reference/*.md", "../../README.md"} {
		m, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, m...)
	}
	if err := filepath.WalkDir("../../docs/src", func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(path, ".md") {
			files = append(files, path)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	span := regexp.MustCompile("`(pulse_[a-z0-9_]*[a-z0-9])\\b[^`\n]*`")
	checked := 0
	for _, path := range files {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range span.FindAllStringSubmatch(string(raw), -1) {
			checked++
			// Pulse's documented metric names (pulse_operations_total,
			// …) are wire names, not tools. obsprom.Help is the HELP
			// table pinned to the root metric set, so this exemption
			// covers exactly the documented metrics and cannot drift.
			if _, isMetric := obsprom.Help(m[1]); isMetric {
				continue
			}
			if !registered[m[1]] && !notTools[m[1]] {
				t.Errorf("%s: names MCP tool %q, which is not registered (toolmeta.Names)", path, m[1])
			}
		}
	}
	if checked == 0 {
		t.Fatal("no pulse_* mentions found; the scan is broken")
	}
}
