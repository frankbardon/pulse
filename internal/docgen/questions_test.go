package docgen_test

import (
	"strings"
	"testing"

	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/docgen"
	"github.com/frankbardon/pulse/internal/examples"
)

// guideParts are a question guide's six part headings, in page order.
var guideParts = []string{
	"## The question",
	"## Pick your analysis",
	"## A worked example",
	"## How to read the result",
	"## Pitfalls",
	"## Next steps",
}

// TestRender_QuestionPages: the default tree carries the "What can
// Pulse answer?" landing page and one guide per analytic intent at
// questions/<id>.md, titled by Intent.Label, with the six parts in
// order; the landing page links every guide with a sample phrasing and
// names the utility intents without a guide; SUMMARY.md lists them all
// under the Analysis Guide.
func TestRender_QuestionPages(t *testing.T) {
	t.Parallel()

	for _, inst := range []*descx.InstanceSnapshot{nil, defaultSnapshot()} {
		files := tree(docgen.Render(inst, docgen.Options{}))
		landing, ok := files["questions.md"]
		if !ok {
			t.Fatal("no questions.md")
		}
		if !strings.HasPrefix(landing, "# What can Pulse answer?\n") {
			t.Errorf("questions.md title: %q", strings.SplitN(landing, "\n", 2)[0])
		}
		if !strings.Contains(files["index.md"], "](questions.md)") {
			t.Error("index.md does not link questions.md")
		}
		summary := files["SUMMARY.md"]
		if !strings.Contains(summary, "  - [What can Pulse answer?](questions.md)\n") {
			t.Error("SUMMARY.md does not list the landing page under the Analysis Guide")
		}
		analytic := 0
		for _, in := range descx.Intents() {
			p := "questions/" + in.ID + ".md"
			page, has := files[p]
			if !in.Analytic {
				if has {
					t.Errorf("utility intent %s has a guide", in.ID)
				}
				if !strings.Contains(landing, "**"+in.Label+"**") {
					t.Errorf("questions.md does not mention utility intent %s", in.ID)
				}
				continue
			}
			analytic++
			if !has {
				t.Errorf("no guide for analytic intent %s", in.ID)
				continue
			}
			if !strings.HasPrefix(page, "# "+in.Label+"\n") {
				t.Errorf("%s: title is not the intent label %q", p, in.Label)
			}
			at := -1
			for _, h := range guideParts {
				i := strings.Index(page, "\n"+h+"\n")
				if i <= at {
					t.Errorf("%s: part %q missing or out of order", p, h)
				}
				at = i
			}
			row := "| [" + in.Label + "](" + p + ") | " + in.Sounds[0] + " |"
			if !strings.Contains(landing, row) {
				t.Errorf("questions.md: no row %q", row)
			}
			if !strings.Contains(summary, "    - ["+in.Label+"]("+p+")\n") {
				t.Errorf("SUMMARY.md does not list %s", p)
			}
		}
		if analytic != 12 {
			t.Errorf("analytic intents = %d, want 12", analytic)
		}
	}
}

// TestRender_GuidePickListsServingOperators: a guide's decision list
// carries exactly the visible operators serving its intent, each
// linked to its catalog entry.
func TestRender_GuidePickListsServingOperators(t *testing.T) {
	t.Parallel()

	inst := defaultSnapshot()
	files := tree(docgen.Render(inst, docgen.Options{}))
	for _, in := range descx.Intents() {
		if !in.Analytic {
			continue
		}
		page := files["questions/"+in.ID+".md"]
		start := strings.Index(page, "\n## Pick your analysis\n")
		end := strings.Index(page, "\n## A worked example\n")
		if start < 0 || end < start {
			t.Fatalf("%s: no decision list", in.ID)
		}
		pick := page[start:end]
		ops := inst.Ontology().OperatorsServing(in.ID)
		if len(ops) == 0 {
			t.Fatalf("%s: no serving operator: vacuous", in.ID)
		}
		for _, op := range ops {
			if !strings.Contains(pick, "| [`"+op+"`](../catalog/") {
				t.Errorf("%s: decision list lacks %s", in.ID, op)
			}
		}
		if n := strings.Count(pick, "| [`"); n != len(ops) {
			t.Errorf("%s: decision list has %d rows, want %d", in.ID, n, len(ops))
		}
	}
}

// TestRender_StoryEmbedEqualsGolden: every story renders on the guide
// of its first intent, its result block is the committed golden byte
// for byte, and its interpretation follows.
func TestRender_StoryEmbedEqualsGolden(t *testing.T) {
	t.Parallel()

	files := tree(docgen.Render(defaultSnapshot(), docgen.Options{}))
	stories := examples.Stories()
	if len(stories) == 0 {
		t.Fatal("no story examples: vacuous")
	}
	for _, name := range stories {
		ex, _ := examples.Get(name)
		golden, ok := examples.StoryGolden(name)
		if !ok {
			t.Fatalf("%s: no golden", name)
		}
		page := files["questions/"+ex.Intents[0]+".md"]
		const lead = "The result:\n\n```json\n"
		i := strings.Index(page, "`pulse examples show "+name+"`")
		if i < 0 {
			t.Errorf("%s: not on the %s guide", name, ex.Intents[0])
			continue
		}
		rest := page[i:]
		j := strings.Index(rest, lead)
		if j < 0 {
			t.Fatalf("%s: no result block", name)
		}
		body := rest[j+len(lead):]
		k := strings.Index(body, "\n```\n")
		if k < 0 {
			t.Fatalf("%s: result block not closed", name)
		}
		if got, want := body[:k], strings.TrimRight(string(golden), "\n"); got != want {
			t.Errorf("%s: embedded result differs from the golden:\n%s\n--- want ---\n%s", name, got, want)
		}
		if !strings.Contains(body[k:], ex.Interpretation) {
			t.Errorf("%s: interpretation not rendered after the result", name)
		}
	}
}

// TestRender_QuestionPagesLint: every question page of every instance
// shape passes the guidance lint's text rules.
func TestRender_QuestionPagesLint(t *testing.T) {
	t.Parallel()

	seen := 0
	for key, inst := range instances(t) {
		for p, body := range tree(docgen.Render(inst, docgen.Options{})) {
			if p != "questions.md" && !strings.HasPrefix(p, "questions/") {
				continue
			}
			seen++
			for _, h := range lintPage(body) {
				t.Errorf("%s: %s: lint %s", key, p, h)
			}
		}
	}
	if seen == 0 {
		t.Fatal("no question pages linted: vacuous")
	}
}
