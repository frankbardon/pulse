package skills_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/mcp/toolmeta"
	"github.com/frankbardon/pulse/skills"
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
	for _, pattern := range []string{"*.md", "../.claude/reference/*.md", "../README.md"} {
		m, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, m...)
	}
	if err := filepath.WalkDir("../docs/src", func(path string, d fs.DirEntry, err error) error {
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
			if !registered[m[1]] && !notTools[m[1]] {
				t.Errorf("%s: names MCP tool %q, which is not registered (toolmeta.Names)", path, m[1])
			}
		}
	}
	if checked == 0 {
		t.Fatal("no pulse_* mentions found; the scan is broken")
	}
}
