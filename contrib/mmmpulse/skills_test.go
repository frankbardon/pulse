package mmmpulse_test

import (
	"strings"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/contrib/mmmpulse"
)

// TestMMMPack_SkillsServed: the pack's skills pass the embedder skill
// validation at pulse.New and are served through the facade beside the
// shipped pack (the surface pulse_skills_list reads), which carries the
// built-in REG_MMM_HILL's op-reg-mmm-hill.
func TestMMMPack_SkillsServed(t *testing.T) {
	p, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(), Extensions: mmmpulse.Extensions()})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	listed := map[string]bool{}
	for _, s := range p.Skills() {
		listed[s.Name] = true
	}
	for _, name := range []string{"op-reg-mmm-hill", "op-reg-mmm-bayes", "ext-mmm-workflow"} {
		if !listed[name] {
			t.Errorf("skill %s not listed", name)
		}
	}
	// op-reg-mmm-hill is the built-in REG_MMM_HILL's, shipped with Pulse.
	body, ok := p.Skill("op-reg-mmm-hill")
	if !ok {
		t.Fatal("op-reg-mmm-hill not served")
	}
	for _, want := range []string{"k:<channel>", "## Use when", "## Contract"} {
		if !strings.Contains(body, want) {
			t.Errorf("served op-reg-mmm-hill lacks %q", want)
		}
	}
	bayes, ok := p.Skill("op-reg-mmm-bayes")
	if !ok {
		t.Fatal("op-reg-mmm-bayes not served")
	}
	for _, want := range []string{"## Use when", "## Contract", "PROCESSING_REGRESSION_DIAGNOSTIC", "time_gap", "holdout_from"} {
		if !strings.Contains(bayes, want) {
			t.Errorf("served op-reg-mmm-bayes lacks %q", want)
		}
	}
	flow, _ := p.Skill("ext-mmm-workflow")
	for _, want := range []string{"Bayesian route vs LM/sweep route", "lift test"} {
		if !strings.Contains(flow, want) {
			t.Errorf("served ext-mmm-workflow lacks %q", want)
		}
	}
}
