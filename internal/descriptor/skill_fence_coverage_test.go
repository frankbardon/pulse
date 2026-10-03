package descriptor

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/internal/skills"
)

// skillFamily is the report's family column.
func skillFamily(name string) string {
	for _, p := range []string{"op", "tool", "type"} {
		if strings.HasPrefix(name, p+"-") {
			return p
		}
	}
	return "topical"
}

func TestFenceCoverage_Scan(t *testing.T) {
	list := fenceScanList()
	fm := func(body string) string { return "---\nname: x\n---\n" + body }
	cases := []struct {
		name   string
		body   string
		desc   string
		guards []string
		want   map[string]int // body counts
		wantD  map[string]int // description counts
	}{
		{name: "unfenced operator", body: "Use AGG_SUM here.\n", want: map[string]int{"AGG_SUM": 1}},
		{name: "operator twice", body: "AGG_SUM and `AGG_SUM`.\n", want: map[string]int{"AGG_SUM": 2}},
		{name: "block fence naming it", body: "<!-- feature: AGG_SUM -->\nUse AGG_SUM.\n<!-- /feature -->\n"},
		{name: "inline fence naming it", body: "See<!-- feature: AGG_SUM --> `AGG_SUM`<!-- /feature -->.\n"},
		{name: "fence naming it among others", body: "<!-- feature: TEST_WELCH, AGG_SUM -->\nAGG_SUM\n<!-- /feature -->\n"},
		{name: "fence naming another feature", body: "<!-- feature: TEST_WELCH -->\nAGG_SUM\n<!-- /feature -->\n", want: map[string]int{"AGG_SUM": 1}},
		{name: "mention outside the inline span", body: "AGG_SUM<!-- feature: AGG_SUM --> x<!-- /feature -->\n", want: map[string]int{"AGG_SUM": 1}},
		{name: "guarded own operator", body: "AGG_SUM\n", desc: "AGG_SUM sums.", guards: []string{"AGG_SUM"}},
		{name: "whole token operator", body: "AGG_SUM_FOO xAGG_SUM\n"},
		{name: "whole token tool", body: "pulse_process_chain\n", want: map[string]int{"pulse_process_chain": 1}},
		{name: "core tool not scanned", body: "pulse_manifest pulse_skills_get\n"},
		{name: "tool fenced by its owning feature", body: "<!-- feature: capability:facet -->\npulse_facet_schema\n<!-- /feature -->\n"},
		{name: "tool fenced by the wrong feature", body: "<!-- feature: capability:crosstab -->\npulse_facet\n<!-- /feature -->\n", want: map[string]int{"pulse_facet": 1}},
		{name: "tool guarded by its owning feature", body: "pulse_facet_schema\n", guards: []string{"capability:facet"}},
		{name: "capability spelling", body: "needs `capability:crosstab`\n", want: map[string]int{"capability:crosstab": 1}},
		{name: "capability fenced", body: "<!-- feature: capability:crosstab -->\ncapability:crosstab\n<!-- /feature -->\n"},
		{name: "bare capability word is prose", body: "a crosstab, a process, an import\n"},
		{name: "colon token split", body: "node operator:AGG_SUM\n", want: map[string]int{"AGG_SUM": 1}},
		{name: "description mention", desc: "Like TEST_WELCH via pulse_facet.", wantD: map[string]int{"TEST_WELCH": 1, "pulse_facet": 1}},
		{name: "frontmatter outside description ignored", body: "clean\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			guards := map[string]bool{}
			for _, g := range tc.guards {
				guards[g] = true
			}
			raw := fm(tc.body)
			if tc.name == "frontmatter outside description ignored" {
				raw = "---\nname: x\noperator: AGG_SUM\ncovers: [TEST_WELCH]\n---\n" + tc.body
			}
			body, desc, err := fenceViolations(nil, raw, tc.desc, list, guards)
			if err != nil {
				t.Fatal(err)
			}
			if tc.want == nil {
				tc.want = map[string]int{}
			}
			if tc.wantD == nil {
				tc.wantD = map[string]int{}
			}
			if !maps.Equal(body, tc.want) {
				t.Errorf("body = %v, want %v", body, tc.want)
			}
			if !maps.Equal(desc, tc.wantD) {
				t.Errorf("description = %v, want %v", desc, tc.wantD)
			}
		})
	}
}

func TestFenceCoverage_MalformedFails(t *testing.T) {
	for _, body := range []string{
		"<!-- feature: AGG_SUM -->\nopen\n",
		"<!-- /feature -->\n",
		"x <!-- feature: AGG_SUM --> never closed\n",
		"<!-- feature: NOT_A_FEATURE -->\nx\n<!-- /feature -->\n",
	} {
		_, _, err := fenceViolations(nil, "---\nname: x\n---\n"+body, "", fenceScanList(), nil)
		var fe *skills.FenceError
		if !errors.As(err, &fe) {
			t.Errorf("%q: err = %v, want *skills.FenceError", body, err)
		}
	}
}

func TestFenceCoverage_Guards(t *testing.T) {
	cases := []struct {
		skill string
		want  []string
	}{
		{"op-agg-sum", []string{"AGG_SUM"}},
		{"tool-facet-schema", []string{"capability:facet"}},
		{"tool-manifest", nil},
		{"op-synth-constant", []string{"capability:synth"}},
		// Hard dependencies guard; any-of host groups never do.
		{"op-overlay-t-cell", []string{"AGG_WELFORD", "OVERLAY_T_CELL", "capability:compose"}},
		{"op-overlay-yoy", []string{"GROUP_DATE", "OVERLAY_YOY", "capability:compose"}},
		{"op-overlay-delta-vs-stage", []string{"OVERLAY_DELTA_VS_STAGE", "capability:process_chain"}},
		{"op-attr-reg-fitted", []string{"ATTR_REG_FITTED", "REG_OLS"}},
		{"response-components", nil},
		// A topical skill is guarded by its requires: targets only.
		{"crosstab-guide", []string{"capability:crosstab"}},
	}
	for _, tc := range cases {
		got := slices.Sorted(maps.Keys(skillGuards(nil, tc.skill)))
		if !slices.Equal(got, tc.want) {
			t.Errorf("skillGuards(%s) = %v, want %v", tc.skill, got, tc.want)
		}
	}
}

// TestSkillsCoverFeatureFences: every mention of a feature name in an
// embedded skill — operator constant, feature-owned `pulse_*` tool,
// `<kind>:<name>` spelling — sits inside a fence naming it, unless the
// skill is pruned with it (guard); a frontmatter description names no
// unguarded feature at all. Every violation and every malformed fence
// fails; the per-file table is logged either way. Print it with
//
//	go test ./internal/descriptor/ -run TestSkillsCoverFeatureFences -v
func TestSkillsCoverFeatureFences(t *testing.T) {
	list := fenceScanList()
	type row struct {
		skill, family string
		body, desc    int
		tokens        map[string]int
	}
	var rows []row
	scanned := 0
	for _, md := range skills.List() {
		if skills.IsVirtual(md.Name) {
			continue
		}
		raw, ok := skills.Raw(md.Name)
		if !ok {
			t.Fatalf("skills.Raw(%s) not found", md.Name)
		}
		scanned++
		body, desc, err := fenceViolations(nil, raw, md.Description, list, skillGuards(nil, md.Name))
		if err != nil {
			t.Errorf("skills/%s.md: malformed feature fence: %v", md.Name, err)
			continue
		}
		r := row{skill: md.Name, family: skillFamily(md.Name), tokens: map[string]int{}}
		for tok, n := range body {
			r.body += n
			r.tokens[tok] += n
		}
		for tok, n := range desc {
			r.desc += n
			r.tokens[tok] += n
		}
		if r.body+r.desc > 0 {
			rows = append(rows, r)
		}
	}
	if scanned == 0 {
		t.Fatal("scanned no embedded skills")
	}
	sort.Slice(rows, func(i, j int) bool {
		ti, tj := rows[i].body+rows[i].desc, rows[j].body+rows[j].desc
		if ti != tj {
			return ti > tj
		}
		return rows[i].skill < rows[j].skill
	})

	var b strings.Builder
	fmt.Fprintf(&b, "unfenced feature mentions (%d of %d skills)\n", len(rows), scanned)
	fmt.Fprintf(&b, "%6s %5s %5s  %-8s %-36s %s\n", "total", "body", "desc", "family", "skill", "top tokens")
	type fam struct{ files, body, desc int }
	fams := map[string]*fam{}
	for _, r := range rows {
		f := fams[r.family]
		if f == nil {
			f = &fam{}
			fams[r.family] = f
		}
		f.files++
		f.body += r.body
		f.desc += r.desc
		toks := slices.Collect(maps.Keys(r.tokens))
		sort.Slice(toks, func(i, j int) bool {
			if r.tokens[toks[i]] != r.tokens[toks[j]] {
				return r.tokens[toks[i]] > r.tokens[toks[j]]
			}
			return toks[i] < toks[j]
		})
		if len(toks) > 4 {
			toks = append(toks[:4], "…")
		}
		parts := make([]string, len(toks))
		for i, tok := range toks {
			if n, ok := r.tokens[tok]; ok {
				parts[i] = fmt.Sprintf("%s×%d", tok, n)
			} else {
				parts[i] = tok
			}
		}
		fmt.Fprintf(&b, "%6d %5d %5d  %-8s %-36s %s\n", r.body+r.desc, r.body, r.desc, r.family, r.skill, strings.Join(parts, " "))
	}
	b.WriteString("per family (files / body / desc):\n")
	for _, name := range slices.Sorted(maps.Keys(fams)) {
		f := fams[name]
		fmt.Fprintf(&b, "  %-8s %4d %6d %5d\n", name, f.files, f.body, f.desc)
	}
	t.Log(b.String())

	for _, r := range rows {
		t.Errorf("skills/%s.md: %d unfenced feature mention(s) in the body, %d in the description: %v", r.skill, r.body, r.desc, r.tokens)
	}
}
