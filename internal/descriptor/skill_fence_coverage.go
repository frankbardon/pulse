package descriptor

import (
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/internal/skills"
)

// Fence coverage — the scanner behind TestSkillsCoverFeatureFences (the
// embedded pack) and the embedder skill validation at pulse.New
// (extension_skills.go: an embedder skill with an unfenced feature name
// is refused).
//
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
// (pruneOntology pass 3 over g; nil = the base graph).
func skillGuards(g *OntologyGraph, name string) map[string]bool {
	if g == nil {
		g = BaseOntology()
	}
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
// description mentions of raw (the skill file) not exempted by guards.
// A malformed fence, or a fence name g (nil = the base graph) does not
// carry, is the error.
func fenceViolations(g *OntologyGraph, raw, description string, list map[string]string, guards map[string]bool) (body, desc map[string]int, err error) {
	if err := ValidateSkillFences(raw, g); err != nil {
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
