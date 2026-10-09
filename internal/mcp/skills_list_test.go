package mcp

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"slices"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/skills"
)

// TestSkillsList_IntentArg: no intent is byte-identical to p.Skills();
// an intent serves p.SkillsForIntent's ranked list; an unknown one is
// the coded recommend error, not a stringified one.
func TestSkillsList_IntentArg(t *testing.T) {
	p := defaultPulse(t)
	ctx := context.Background()
	all, err := HandleSkillsList(ctx, p, SkillsListIn{})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := json.Marshal(all.Skills)
	want, _ := json.Marshal(p.Skills())
	if string(got) != string(want) {
		t.Fatal("no-intent pulse_skills_list differs from p.Skills()")
	}

	ranked, err := HandleSkillsList(ctx, p, SkillsListIn{Intent: "compare_groups"})
	if err != nil || len(ranked.Skills) == 0 || len(ranked.Skills) >= len(all.Skills) {
		t.Fatalf("intent list: %d of %d, err %v", len(ranked.Skills), len(all.Skills), err)
	}
	facade, _ := p.SkillsForIntent("compare_groups")
	if !slices.EqualFunc(ranked.Skills, facade, func(a, b skills.Metadata) bool { return a.Name == b.Name }) {
		t.Error("handler and facade disagree")
	}
	if ranked.Skills[0].Name != "intents" {
		t.Errorf("first = %s, want intents", ranked.Skills[0].Name)
	}

	_, err = HandleSkillsList(ctx, p, SkillsListIn{Intent: "nope"})
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_RECOMMEND_INTENT_UNKNOWN {
		t.Fatalf("unknown intent: err = %v", err)
	}
}
