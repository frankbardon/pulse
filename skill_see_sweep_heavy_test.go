//go:build heavy

package pulse

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/afero"

	descx "github.com/frankbardon/pulse/internal/descriptor"
)

// TestProfileRenderedSeeNoHiddenSkill (U76 E6-S5, #737): under every
// published example feature profile, every private fixture and, per
// capability that prunes a skill, the largest profile hiding it, no
// visible skill as p.Skill serves it points its `## See` section at a
// skill the instance hides — an item annotated with a backtick span
// ("`synth-fidelity-report` (the `_synthetic` tagged output)") included.
// Non-vacuous: the full instance's See sections name some skill a swept
// profile hides, and all-but/capability:synth serves cohort-union.
func TestProfileRenderedSeeNoHiddenSkill(t *testing.T) {
	t.Parallel()

	full, err := New(Options{FS: afero.NewMemMapFs()})
	if err != nil {
		t.Fatal(err)
	}
	all := map[string]bool{}
	for _, md := range full.Skills() {
		all[md.Name] = true
	}
	pulses := shippedProfilePulses(t)
	for _, f := range descx.Features() {
		if !strings.HasPrefix(f.Name, "capability:") {
			continue
		}
		p, err := New(Options{FS: afero.NewMemMapFs(), FeatureProfile: &FeatureProfile{Features: allFeaturesBut(f.Name)}})
		if err != nil {
			continue // a capability no profile can drop alone
		}
		if len(p.Skills()) < len(all) {
			pulses["all-but/"+f.Name] = p
		}
	}
	if _, ok := pulses["all-but/capability:synth"]; !ok {
		t.Fatal("premise: hiding capability:synth prunes a skill")
	}

	fullSee := map[string]string{}
	for name := range all {
		body, _ := full.Skill(name)
		fullSee[name] = skillSeeSection(body)
	}
	cutSeen := 0
	for _, key := range slices.Sorted(maps.Keys(pulses)) {
		p := pulses[key]
		hidden := maps.Clone(all)
		for _, md := range p.Skills() {
			delete(hidden, md.Name)
		}
		for _, md := range p.Skills() {
			body, _ := p.Skill(md.Name)
			for _, span := range seeSpans(skillSeeSection(body)) {
				if hidden[span] {
					t.Errorf("profile %s: skill %s's See points at hidden skill %s", key, md.Name, span)
				}
			}
			for _, span := range seeSpans(fullSee[md.Name]) {
				if hidden[span] {
					cutSeen++
				}
			}
		}
	}
	if cutSeen == 0 {
		t.Error("no full-instance See names a skill any swept profile hides: vacuous")
	}
	if body, ok := pulses["all-but/capability:synth"].Skill("cohort-union"); !ok || !strings.Contains(body, "`tool-lookup`") {
		t.Error("all-but/capability:synth does not serve cohort-union's See: the #737 case is not swept")
	}
}

// skillSeeSection returns a served body's `## See` section text.
func skillSeeSection(body string) string {
	var b strings.Builder
	in := false
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "## ") {
			in = strings.TrimSpace(line) == "## See"
			continue
		}
		if in {
			b.WriteString(line + "\n")
		}
	}
	return b.String()
}

// seeSpans returns every backticked span of s.
func seeSpans(s string) []string {
	var out []string
	for {
		i := strings.IndexByte(s, '`')
		if i < 0 {
			return out
		}
		j := strings.IndexByte(s[i+1:], '`')
		if j < 0 {
			return out
		}
		out = append(out, s[i+1:i+1+j])
		s = s[i+1+j+1:]
	}
}
