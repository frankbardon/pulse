package descriptor

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/internal/skills"
)

func TestFenceFeatureID(t *testing.T) {
	for name, want := range map[string]string{
		"TEST_WELCH":          opID("TEST_WELCH"),
		"capability:crosstab": "capability:crosstab",
		"io_format:csv":       "io_format:csv",
		"normal":              opID("normal"), // a synth distribution: an operator, no feature
	} {
		if got := FenceFeatureID(name); got != want {
			t.Errorf("FenceFeatureID(%s) = %s, want %s", name, got, want)
		}
		if !BaseOntology().Has(FenceFeatureID(name)) {
			t.Errorf("base graph lacks %s", FenceFeatureID(name))
		}
	}
}

func TestValidateSkillFences(t *testing.T) {
	ok := "a\n<!-- feature: TEST_WELCH, capability:crosstab -->\nb\n<!-- /feature -->\nc <!-- feature: io_format:csv -->d<!-- /feature -->\n"
	if err := ValidateSkillFences(ok, nil); err != nil {
		t.Errorf("valid body: %v", err)
	}
	for body, line := range map[string]int{
		"a\n<!-- feature: TEST_WELCH, TEST_NOPE -->\nb\n<!-- /feature -->": 2, // unknown operator
		"x <!-- feature: crosstab -->y<!-- /feature -->":                   1, // capability without its kind
		"<!-- feature: capability:nope -->\nb\n<!-- /feature -->":          1,
		"a\n<!-- /feature -->": 2, // parse error passes through
	} {
		err := ValidateSkillFences(body, nil)
		var fe *skills.FenceError
		if !errors.As(err, &fe) || fe.Line != line {
			t.Errorf("%q: err = %v, want *skills.FenceError at line %d", body, err, line)
		}
	}
}

// TestSkillFences_NamesAreFeatures: every fence in the embedded pack
// parses and names only features (feature-profile spelling) — an unknown
// name is a validation error here, not a silently hidden fence at serve
// time.
func TestSkillFences_NamesAreFeatures(t *testing.T) {
	for _, md := range skills.List() {
		if skills.IsVirtual(md.Name) {
			continue
		}
		raw, _ := skills.Raw(md.Name)
		if err := ValidateSkillFences(raw, nil); err != nil {
			t.Errorf("skills/%s.md: %v", md.Name, err)
		}
	}
}

func TestRenderSeeSection(t *testing.T) {
	body := strings.Join([]string{
		"## Params", "", "`op-gone` outside See stays.", "", "## See", "",
		"- `pulse_examples_search tags=[dead]`",
		"- `pulse_examples_search tags=[live]`",
		"- Skills: `a-design`, `op-gone`, `op-kept`",
		"- Skills: `op-gone`, `op-kept`",
		"- Skills: `op-kept` (note, with comma), `op-gone` (aside)",
		"- Cross-link: `op-kept`, `op-gone`.",
		"- Cross-link: `op-gone` for x; `op-kept` for y.",
		"- `op-gone` / `op-kept` — shared description.",
		"- `op-kept` / `op-gone` — shared description.",
		"- `op-gone` — gone sibling.",
		"- Skills: `op-gone`, `op-gone`",
		"- Skill: `a-design` (Section).",
		"- Error list in `pulse_manifest` (`error_codes` slice).",
		"", "## After", "", "- `op-gone`", "",
	}, "\n")
	keep := func(span string) bool {
		if tags, ok := seeTags(span); ok {
			return slices.Equal(tags, []string{"live"})
		}
		return span != "op-gone"
	}
	want := strings.Join([]string{
		"## Params", "", "`op-gone` outside See stays.", "", "## See", "",
		"- `pulse_examples_search tags=[live]`",
		"- Skills: `a-design`, `op-kept`",
		"- Skills: `op-kept`",
		"- Skills: `op-kept` (note, with comma)",
		"- Cross-link: `op-kept`.",
		"- Cross-link: `op-kept` for y.",
		"- `op-kept` — shared description.",
		"- `op-kept` — shared description.",
		"- Skill: `a-design` (Section).",
		"- Error list in `pulse_manifest` (`error_codes` slice).",
		"", "## After", "", "- `op-gone`", "",
	}, "\n")
	if got := renderSeeSection(body, keep); got != want {
		t.Errorf("renderSeeSection:\n got %q\nwant %q", got, want)
	}
	if got := renderSeeSection(body, func(string) bool { return true }); got != body {
		t.Error("keep-all render changed the body")
	}
}

// TestDiscovery_RendersSeeByEdge: on a profiled instance an atomic
// body's `## See` drops the stems of pruned skills and a tag search no
// visible example answers; nothing outside the section moves.
func TestDiscovery_RendersSeeByEdge(t *testing.T) {
	d := hidingSnapshot("REG_GLM").Discovery()
	full, _ := skills.Get("op-reg-mod-resample")
	if !strings.Contains(seeSection(full), "`op-reg-glm`") {
		t.Fatal("premise: op-reg-mod-resample's See names op-reg-glm")
	}
	got, ok := d.Skill("op-reg-mod-resample")
	if !ok {
		t.Fatal("op-reg-mod-resample pruned")
	}
	if strings.Contains(seeSection(got), "op-reg-glm") {
		t.Errorf("See still names the pruned op-reg-glm:\n%s", seeSection(got))
	}
	if !strings.Contains(seeSection(got), "`op-reg-ols`, `op-reg-mod-selection`") {
		t.Errorf("See lost a visible sibling:\n%s", seeSection(got))
	}
	// Outside ## See only the fences move: the head equals the raw body
	// fence-rendered with REG_GLM alone hidden (its host-table row goes).
	raw, _ := skills.Raw("op-reg-mod-resample")
	fenced, err := skills.RenderFences(raw, func(n string) bool { return n != "REG_GLM" })
	if err != nil {
		t.Fatal(err)
	}
	head := func(s string) string { return s[:strings.Index(s, "## See")] }
	if head(got) != head(fenced) {
		t.Error("rendering changed text outside ## See beyond the fences")
	}
	if strings.Contains(head(got), "REG_GLM") {
		t.Error("the fenced REG_GLM host row survived")
	}

	// tags=[synth] matches no visible example: the ref goes, its line too.
	syn, _ := d.Skill("op-synth-normal")
	if strings.Contains(syn, "tags=[synth]") {
		t.Error("dead tag search kept")
	}
	if !strings.Contains(syn, "`op-synth-lognormal`") {
		t.Error("visible sibling stem lost")
	}
	// A tag search a visible example answers stays.
	if freq, _ := d.Skill("op-agg-frequency"); !strings.Contains(freq, "tags=[cross-tabulation]") {
		t.Error("live tag search dropped")
	}
}

// TestDiscovery_KeepsTableRows is the regression test for U06's line-wise
// scrub: hiding FILTER_INCLUDE used to delete op-agg-frequency's `value`
// parameter row (its one sentence named FILTER_INCLUDE). Fence rendering
// never cuts unfenced prose, so every table row survives. (The pack itself
// no longer names FILTER_INCLUDE there — E2-S3 — so the body is synthetic.)
func TestDiscovery_KeepsTableRows(t *testing.T) {
	raw := "## Params\n\n| Name | Description |\n|---|---|\n| `value` | Matched as `FILTER_INCLUDE` matches |\n| `other` | plain |\n"
	if got := hidingSnapshot("FILTER_INCLUDE").Discovery().renderBody("op-agg-frequency", raw); got != raw {
		t.Errorf("table rows changed:\n got %q\nwant %q", got, raw)
	}
}

// TestDiscovery_RendersFences: an embedded body's fences render against
// the instance's pruned graph — atomic and topical alike — and markers
// never reach the served body.
func TestDiscovery_RendersFences(t *testing.T) {
	d := hidingSnapshot("REG_GLM", featCrosstab).Discovery()
	raw := "intro\n\n| op | use |\n|---|---|\n| `REG_OLS` | linear |\n<!-- feature: REG_GLM -->\n| `REG_GLM` | links |\n<!-- /feature -->\n\n<!-- feature: capability:crosstab -->\nCrosstab prose.\n<!-- /feature -->\n\nPair with <!-- feature: REG_OLS -->`REG_OLS`<!-- /feature --> and<!-- feature: REG_OLS, REG_GLM --> `REG_GLM`<!-- /feature -->.\n"
	want := "intro\n\n| op | use |\n|---|---|\n| `REG_OLS` | linear |\n\nPair with `REG_OLS` and.\n"
	for _, name := range []string{"op-reg-ols", "regression-modeling"} {
		if got := d.renderBody(name, raw); got != want {
			t.Errorf("%s:\n got %q\nwant %q", name, got, want)
		}
	}
	full, err := skills.RenderFences(raw, nil)
	if err != nil || strings.Contains(full, "<!--") || !strings.Contains(full, "| `REG_GLM` | links |") {
		t.Errorf("full render: %q, %v", full, err)
	}
}

// TestDiscovery_NoProseScrubOnBodies: bodies are no longer redacted line
// by line — an unfenced sentence naming a hidden operator is served as
// written (the fence gate, not the renderer, keeps the pack fenced).
func TestDiscovery_NoProseScrubOnBodies(t *testing.T) {
	d := hidingSnapshot("REG_GLM").Discovery()
	if _, ok := d.topical["regression-modeling"]; !ok {
		t.Fatal("premise: regression-modeling is a visible topical skill")
	}
	prose := "Fit a logistic model with `REG_GLM` here.\n| `REG_GLM` | binomial |\n"
	if got := d.renderBody("regression-modeling", prose); got != prose {
		t.Error("an unfenced topical line naming a hidden operator was cut")
	}
	row := "| Host | Accepted? |\n|---|---|\n| `REG_GLM` | yes |\n"
	if got := d.renderBody("op-reg-mod-resample", row); got != row {
		t.Error("an unfenced table row naming a hidden operator was cut")
	}
}

// TestOntology_TopicalFenceEdges: a topical body's fenced names are
// routes_to edges to their feature nodes; an unknown name or a malformed
// fence is a problem; atomic fences emit nothing; a routes_to edge never
// prunes the skill.
func TestOntology_TopicalFenceEdges(t *testing.T) {
	src := ontologySources{
		features: Features(),
		skills: []skills.Metadata{
			{Name: "topic", Kind: "design"},
			{Name: "bad-name", Kind: "design"},
			{Name: "bad-fence", Kind: "design"},
			{Name: "op-x", Kind: "operator"},
		},
		skillBody: func(name string) (string, bool) {
			switch name {
			case "topic":
				return "a\n<!-- feature: TEST_WELCH, capability:crosstab -->\nb\n<!-- /feature -->\nc <!-- feature: io_format:csv -->d<!-- /feature -->\n", true
			case "bad-name":
				return "<!-- feature: capability:nope -->\nb\n<!-- /feature -->\n", true
			case "bad-fence":
				return "<!-- /feature -->\n", true
			case "op-x":
				return "<!-- feature: TEST_T -->\nb\n<!-- /feature -->\n", true
			}
			return "", false
		},
	}
	g := buildOntology(src)
	for _, to := range []string{opID("TEST_WELCH"), "capability:crosstab", "io_format:csv"} {
		if e := ontoEdge(skillID("topic"), to, descriptor.OntologyEdgeRoutesTo); !hasEdge(g, e) {
			t.Errorf("missing %+v", e)
		}
	}
	if len(g.Out(skillID("op-x"), descriptor.OntologyEdgeRoutesTo)) != 0 {
		t.Error("an atomic fence emitted an edge")
	}
	probs := strings.Join(g.Problems(), "\n")
	if !strings.Contains(probs, "capability:nope") || !strings.Contains(probs, "bad-fence") || len(g.Problems()) != 2 {
		t.Errorf("problems = %v", g.Problems())
	}
	if pruned := pruneOntology(g, hidingSnapshot("TEST_WELCH")); !pruned.Has(skillID("topic")) {
		t.Error("a fenced name pruned the topical skill")
	}
}

// TestDiscovery_RendersFrontmatterCovers: a topical body is served with
// its frontmatter, so its `covers:` list renders exactly as the listed
// metadata does (keepVisibleTokens) — a hidden operator never survives
// in the served frontmatter — and a body naming nothing hidden is
// byte-identical.
func TestDiscovery_RendersFrontmatterCovers(t *testing.T) {
	raw, ok := skills.Raw("regression-modeling")
	if !ok || !strings.Contains(raw, "REG_GLM") || !strings.Contains(raw, "FEAT_POLY") {
		t.Fatal("premise: regression-modeling covers REG_GLM and FEAT_POLY")
	}
	d := hidingSnapshot("REG_GLM", "FEAT_POLY").Discovery()
	body, ok := d.Skill("regression-modeling")
	if !ok {
		t.Fatal("regression-modeling not visible")
	}
	fm, _, _ := strings.Cut(strings.TrimPrefix(body, "---\n"), "\n---")
	var covers string
	for _, line := range strings.Split(fm, "\n") {
		if strings.HasPrefix(line, "covers:") {
			covers = line
		}
	}
	want := "covers: [" + strings.Join(d.keepVisibleTokens(skillMeta(t, "regression-modeling").Covers), ", ") + "]"
	if covers != want {
		t.Errorf("served covers = %q, want %q", covers, want)
	}
	for _, tok := range []string{"REG_GLM", "FEAT_POLY"} {
		if strings.Contains(covers, tok) {
			t.Errorf("served covers names hidden %s", tok)
		}
	}
	if !strings.Contains(covers, "REG_OLS") {
		t.Errorf("served covers lost the visible REG_OLS: %q", covers)
	}
	full, _ := skills.Get("regression-modeling")
	if got, _ := hidingSnapshot("TEST_WELCH").Discovery().Skill("regression-modeling"); got != full {
		t.Error("a body whose covers name nothing hidden was rewritten")
	}
}
