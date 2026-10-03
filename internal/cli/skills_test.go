package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/internal/skills"
)

func runSkills(t *testing.T, args ...string) string {
	t.Helper()
	cmd := SkillsCommand()
	var buf bytes.Buffer
	cmd.Writer = &buf
	if err := cmd.Run(context.Background(), append([]string{"skills"}, args...)); err != nil {
		t.Fatalf("skills %v: %v", args, err)
	}
	return buf.String()
}

// TestSkillsCommand_GuidanceSkills: pulse skills list (text and --json)
// lists the glossary and intents virtual skills and pulse skills show
// prints their rendered bodies.
func TestSkillsCommand_GuidanceSkills(t *testing.T) {
	text := runSkills(t, "list")
	var env struct {
		Data []skills.Metadata `json:"data"`
	}
	if err := json.Unmarshal([]byte(runSkills(t, "list", "--json")), &env); err != nil {
		t.Fatalf("decode list --json: %v", err)
	}
	for _, name := range skills.ReservedVirtualNames() {
		if !strings.Contains(text, "\n"+name+" ") && !strings.HasPrefix(text, name+" ") {
			t.Errorf("skills list lacks %s", name)
		}
		found := false
		for _, m := range env.Data {
			if m.Name == name {
				found = m.Kind == skills.KindReference
			}
		}
		if !found {
			t.Errorf("skills list --json lacks %s (kind reference)", name)
		}
		want, ok := skills.Get(name)
		if !ok {
			t.Fatalf("skills.Get(%s) not found", name)
		}
		if got := runSkills(t, "show", name); got != want {
			t.Errorf("skills show %s does not print the rendered body", name)
		}
	}
}
