package descriptor

import (
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/internal/skills"
)

// fenceCoverageFail is THE switch of TestSkillsCoverFeatureFences. While
// false the gate is REPORT-ONLY: it logs the per-file violation table
// and passes. E4-S3 flips it to true once the pack is fenced, and every
// unfenced mention then fails. Malformed fences fail in BOTH modes.
const fenceCoverageFail = false

// Fence-coverage rules (.claude/reference/skill-pack.md, Fence coverage):
//
//   - SCAN LIST (fenceScanList): every operator feature (bare constant),
//     every non-operator feature spelled `<kind>:<name>`
//     (capability:crosstab, io_format:spss, mcp_extra:…) and every MCP
//     tool a FEATURE owns (pulse_facet → capability:facet). Core tools
//     (pulse_manifest, pulse_skills_get, …) are never hidden and are not
//     scanned; bare words that merely share a capability's name
//     ("crosstab", "process", "import") are prose, not feature names, and
//     are not scanned either — only the kind-prefixed spelling is.
//   - TOKENS are whole: a run of [A-Za-z0-9_] optionally joined by ONE
//     colon to another run. pulse_process_chain is never a mention of
//     pulse_process, AGG_SUM_X never of AGG_SUM. A colon token that is no
//     feature (operator:AGG_SUM, the ontology node ID) is split and each
//     half looked up.
//   - A BODY mention of feature F is fenced iff it disappears when the
//     body is rendered with F alone hidden (skills.RenderFences) — i.e. it
//     sits inside a fence whose name list includes F.
//   - The frontmatter DESCRIPTION cannot hold a fence: every mention
//     there is a violation.
//   - GUARD exemption (skillGuards): a mention of F is exempt when the
//     skill itself is pruned whenever F is hidden — an atomic skill's own
//     `operator:` plus its HARD dependencies (every feature named alone
//     in a DependsOn group, transitively: a valid feature profile cannot
//     hide one and keep the operator, so OVERLAY_T_CELL's skill never
//     renders without AGG_WELFORD), for a synth distribution
//     capability:synth, the feature owning a tool skill's MCP tool, a
//     topical skill's `requires:`. Read from the base graph exactly as
//     pruneOntology reads it.

// fenceToken is one whole token (see the rules above).
var fenceToken = regexp.MustCompile(`[A-Za-z0-9_]+(?::[A-Za-z0-9_]+)?`)

// fenceScanList maps each scanned token to the feature a fence must name.
func fenceScanList() map[string]string {
	m := map[string]string{}
	for _, f := range Features() {
		m[f.Name] = f.Name
	}
	for _, b := range MCPToolBindings() {
		if b.Feature != "" {
			m[b.Tool] = b.Feature
		}
	}
	return m
}

// fenceMentions returns every scanned token in text, in order.
func fenceMentions(text string, list map[string]string) []string {
	var out []string
	for _, tok := range fenceToken.FindAllString(text, -1) {
		if _, ok := list[tok]; ok {
			out = append(out, tok)
			continue
		}
		if a, b, ok := strings.Cut(tok, ":"); ok {
			for _, half := range []string{a, b} {
				if _, ok := list[half]; ok {
					out = append(out, half)
				}
			}
		}
	}
	return out
}

// skillGuards returns the features whose hiding prunes the named skill
// (pruneOntology pass 3 over the base graph).
func skillGuards(name string) map[string]bool {
	g := BaseOntology()
	id := OntologyID(descriptor.OntologyNodeSkill, name)
	guards := map[string]bool{}
	requires := func(nodeID string) {
		for _, e := range g.Out(nodeID, descriptor.OntologyEdgeRequiresCapability) {
			if n, ok := g.Node(e.To); ok {
				guards[n.Name] = true
			}
		}
	}
	for _, e := range g.In(id, descriptor.OntologyEdgeDocumentedBy) {
		n, ok := g.Node(e.From)
		if !ok {
			continue
		}
		switch n.Kind {
		case descriptor.OntologyNodeOperator:
			if _, isFeature := FeatureKindOf(n.Name); isFeature {
				// A feature node's flattened any-of edges are never read;
				// its HARD dependencies (single-name groups) are: a
				// profile cannot keep the operator while hiding them.
				hardDependencies(n.Name, guards)
				continue
			}
			requires(n.ID) // a synth distribution: capability:synth
		case descriptor.OntologyNodeMCPTool:
			requires(n.ID)
		}
	}
	requires(id)
	return guards
}

// hardDependencies adds name and, transitively, every feature named
// alone in one of its DependsOn groups — a dependency no profile can
// hide while keeping name (PULSE_FEATURE_PROFILE_DEPENDENCY). An any-of
// group of two or more names guards nothing: either may be hidden.
func hardDependencies(name string, guards map[string]bool) {
	if guards[name] {
		return
	}
	guards[name] = true
	groups, _ := FeatureDependencies(name)
	for _, g := range groups {
		if len(g) == 1 {
			hardDependencies(g[0], guards)
		}
	}
}

// splitFrontmatter returns raw's body (after the closing `---` line).
func splitFrontmatter(raw string) string {
	if !strings.HasPrefix(raw, "---\n") {
		return raw
	}
	end := strings.Index(raw[4:], "\n---")
	if end < 0 {
		return raw
	}
	rest := raw[4+end+len("\n---"):]
	if i := strings.IndexByte(rest, '\n'); i >= 0 {
		return rest[i+1:]
	}
	return ""
}

// fenceViolations counts, per token, the unfenced body mentions and the
// description mentions of raw (the embedded file) not exempted by
// guards. A malformed fence (or an unknown fence name) is the error.
func fenceViolations(raw, description string, list map[string]string, guards map[string]bool) (body, desc map[string]int, err error) {
	if err := ValidateSkillFences(raw, nil); err != nil {
		return nil, nil, err
	}
	body, desc = map[string]int{}, map[string]int{}
	text := splitFrontmatter(raw)
	features := map[string]bool{}
	for _, tok := range fenceMentions(text, list) {
		if f := list[tok]; !guards[f] {
			features[f] = true
		}
	}
	for _, f := range slices.Sorted(maps.Keys(features)) {
		rendered, err := skills.RenderFences(text, func(n string) bool { return n != f })
		if err != nil {
			return nil, nil, err
		}
		for _, tok := range fenceMentions(rendered, list) {
			if list[tok] == f {
				body[tok]++
			}
		}
	}
	for _, tok := range fenceMentions(description, list) {
		if !guards[list[tok]] {
			desc[tok]++
		}
	}
	return body, desc, nil
}

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
			body, desc, err := fenceViolations(raw, tc.desc, list, guards)
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
		_, _, err := fenceViolations("---\nname: x\n---\n"+body, "", fenceScanList(), nil)
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
		got := slices.Sorted(maps.Keys(skillGuards(tc.skill)))
		if !slices.Equal(got, tc.want) {
			t.Errorf("skillGuards(%s) = %v, want %v", tc.skill, got, tc.want)
		}
	}
}

// TestSkillsCoverFeatureFences: every mention of a feature name in an
// embedded skill — operator constant, feature-owned `pulse_*` tool,
// `<kind>:<name>` spelling — sits inside a fence naming it, unless the
// skill is pruned with it (guard); a frontmatter description names no
// unguarded feature at all. REPORT-ONLY until fenceCoverageFail flips
// (E4-S3); a malformed fence fails now. Print the table with
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
		body, desc, err := fenceViolations(raw, md.Description, list, skillGuards(md.Name))
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
	fmt.Fprintf(&b, "unfenced feature mentions (%d of %d skills; fail=%v)\n", len(rows), scanned, fenceCoverageFail)
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

	if fenceCoverageFail {
		for _, r := range rows {
			t.Errorf("skills/%s.md: %d unfenced feature mention(s) in the body, %d in the description: %v", r.skill, r.body, r.desc, r.tokens)
		}
	}
}
