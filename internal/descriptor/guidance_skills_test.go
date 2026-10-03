package descriptor

import (
	"slices"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/internal/skills"
)

// TestGuidanceSkills_Registered pins the two virtual skills on the skill
// pack seam: listed with reference kind and a description, served
// through skills.Get as their renderer's output, and listed in the
// manifest's skills.
func TestGuidanceSkills_Registered(t *testing.T) {
	render := map[string]func() string{
		skills.VirtualGlossary: RenderGlossarySkill,
		skills.VirtualIntents:  RenderIntentsSkill,
	}
	list := skills.List()
	var manifest []string
	for _, s := range BuildManifest().Skills {
		manifest = append(manifest, s.Name)
	}
	for name, fn := range render {
		i := slices.IndexFunc(list, func(m skills.Metadata) bool { return m.Name == name })
		if i < 0 {
			t.Fatalf("skills.List lacks %s", name)
		}
		if md := list[i]; md.Kind != skills.KindReference || md.Description == "" {
			t.Errorf("%s metadata = %+v", name, md)
		}
		body, ok := skills.Get(name)
		if !ok || body != fn() {
			t.Errorf("skills.Get(%s) does not serve the rendered body", name)
		}
		if fm := skills.ParseFrontmatter(body); fm["name"] != name || fm["kind"] != skills.KindReference || fm["description"] != list[i].Description {
			t.Errorf("%s frontmatter = %v, disagrees with its listing", name, fm)
		}
		if !slices.Contains(manifest, name) {
			t.Errorf("manifest skills lacks %s", name)
		}
	}
}

// headingsAt returns the text of every markdown heading with the given
// prefix ("## " / "### "), in body order.
func headingsAt(body, prefix string) []string {
	var out []string
	for _, line := range strings.Split(body, "\n") {
		if h, ok := strings.CutPrefix(line, prefix); ok {
			out = append(out, h)
		}
	}
	return out
}

// TestRenderGlossarySkill: one section per term, sorted by ID, each
// carrying its definition and why-it-matters prose; deterministic.
func TestRenderGlossarySkill(t *testing.T) {
	body := RenderGlossarySkill()
	if body != RenderGlossarySkill() {
		t.Fatal("glossary render is not deterministic")
	}
	if got, want := headingsAt(body, "## "), GlossaryIDs(); !slices.Equal(got, want) {
		t.Errorf("glossary sections = %v\nwant sorted IDs %v", got, want)
	}
	for _, term := range Glossary() {
		if !strings.Contains(body, "\n## "+term.ID+"\n\n"+term.Short+"\n") {
			t.Errorf("glossary section %s lacks its definition", term.ID)
		}
		if term.WhyCare != "" && !strings.Contains(body, "Why it matters: "+term.WhyCare) {
			t.Errorf("glossary section %s lacks why-it-matters", term.ID)
		}
	}
}

// TestRenderIntentsSkill: analytic intents, then tooling intents, each
// sorted by ID, every phrasing listed; deterministic.
func TestRenderIntentsSkill(t *testing.T) {
	body := RenderIntentsSkill()
	if body != RenderIntentsSkill() {
		t.Fatal("intents render is not deterministic")
	}
	if got := headingsAt(body, "## "); !slices.Equal(got, []string{"Analytic intents", "Tooling intents"}) {
		t.Fatalf("intents sections = %v", got)
	}
	var analytic, tooling []string
	for _, in := range Intents() {
		if in.Analytic {
			analytic = append(analytic, in.ID)
		} else {
			tooling = append(tooling, in.ID)
		}
		for _, s := range in.Sounds {
			if !strings.Contains(body, "- "+s+"\n") {
				t.Errorf("intent %s lacks phrasing %q", in.ID, s)
			}
		}
	}
	slices.Sort(analytic)
	slices.Sort(tooling)
	if got, want := headingsAt(body, "### "), append(analytic, tooling...); !slices.Equal(got, want) {
		t.Errorf("intent headings = %v\nwant %v", got, want)
	}
	if !strings.Contains(body, "- outcome (numeric|categorical|bool, exactly 1) + group (categorical|bool, exactly 1)\n") {
		t.Error("compare_groups shape not rendered")
	}
}

func TestRoleCount(t *testing.T) {
	for _, c := range []struct {
		min, max int
		want     string
	}{
		{1, -1, "1 or more"},
		{0, 1, "optional"},
		{2, 2, "exactly 2"},
		{1, 3, "1 to 3"},
	} {
		if got := roleCount(role("r", c.min, c.max, kNum)); got != c.want {
			t.Errorf("roleCount(%d,%d) = %q, want %q", c.min, c.max, got, c.want)
		}
	}
}
