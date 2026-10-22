package docgen

import (
	"io/fs"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	descx "github.com/frankbardon/pulse/internal/descriptor"
)

// withProse swaps the prose source for the duration of a test.
func withProse(t *testing.T, files map[string]string) {
	t.Helper()
	m := fstest.MapFS{}
	for name, body := range files {
		m["guides/"+name] = &fstest.MapFile{Data: []byte(body)}
	}
	prev := proseFS
	proseFS = m
	t.Cleanup(func() { proseFS = prev })
}

func renderTree(inst *descx.InstanceSnapshot) map[string]string {
	out := map[string]string{}
	for _, f := range Render(inst, Options{}) {
		out[f.Path] = string(f.Body)
	}
	return out
}

func span(page, id string) (string, bool) {
	begin := preserveBeginPrefix + id + preserveSuffix + "\n"
	end := preserveEndPrefix + id + preserveSuffix + "\n"
	i := strings.Index(page, begin)
	j := strings.Index(page, end)
	if i < 0 || j < i {
		return "", false
	}
	return page[i+len(begin) : j], true
}

// TestProse_InjectedIntoPreserveSpans: each named section of a prose
// file lands verbatim between its page's preserve markers.
func TestProse_InjectedIntoPreserveSpans(t *testing.T) {
	withProse(t, map[string]string{
		"index.md":          "<!-- section intro -->\nLanding words.\n",
		"compare_groups.md": "<!-- section question -->\n\nAre two groups apart?\n\n<!-- section pitfalls -->\n- One pitfall.\n- Another.\n",
	})
	files := renderTree(nil)
	cases := []struct{ page, id, want string }{
		{"questions.md", "questions-intro", "Landing words.\n"},
		{"questions/compare_groups.md", "guide-compare_groups-question", "Are two groups apart?\n"},
		{"questions/compare_groups.md", "guide-compare_groups-pitfalls", "- One pitfall.\n- Another.\n"},
	}
	for _, c := range cases {
		got, ok := span(files[c.page], c.id)
		if !ok {
			t.Errorf("%s: no %s span", c.page, c.id)
			continue
		}
		if got != c.want {
			t.Errorf("%s: span %s = %q, want %q", c.page, c.id, got, c.want)
		}
	}
}

// TestProse_MissingFileFallsBack: a guide or landing page without a
// prose file (or without a section) renders its generated fallback and
// no preserve span; the page still carries all six parts.
func TestProse_MissingFileFallsBack(t *testing.T) {
	withProse(t, map[string]string{
		"describe.md": "<!-- section question -->\nOnly the question.\n",
	})
	files := renderTree(nil)
	landing := files["questions.md"]
	if strings.Contains(landing, preserveBeginPrefix) {
		t.Error("questions.md renders a preserve span without a prose file")
	}
	if !strings.Contains(landing, "Each guide below starts from one kind of question") {
		t.Error("questions.md lacks the generated intro")
	}
	page := files["questions/compare_groups.md"]
	if strings.Contains(page, preserveBeginPrefix) {
		t.Error("compare_groups guide renders a preserve span without a prose file")
	}
	in := descx.Intents()[1]
	if !strings.Contains(page, "- "+in.Sounds[0]+"\n") {
		t.Error("compare_groups guide does not fall back to the intent's Sounds")
	}
	if !strings.Contains(page, "Each operator's catalog entry lists its assumptions") {
		t.Error("compare_groups guide lacks the generated pitfalls fallback")
	}
	describe := files["questions/describe.md"]
	if _, ok := span(describe, "guide-describe-question"); !ok {
		t.Error("describe question span missing")
	}
	if _, ok := span(describe, "guide-describe-pitfalls"); ok {
		t.Error("describe renders a pitfalls span its prose file lacks")
	}
}

// TestProse_ScrubbedForInstance: prose naming an operator the instance
// hides loses that sentence, the rest of the span stays.
func TestProse_ScrubbedForInstance(t *testing.T) {
	withProse(t, map[string]string{
		"describe.md": "<!-- section question -->\nKeep this line. Read AGG_MEDIAN beside the mean.\n",
	})
	var enabled []string
	for _, n := range descx.FeatureNames() {
		if n != "AGG_MEDIAN" {
			enabled = append(enabled, n)
		}
	}
	inst := descx.NewInstanceSnapshot(nil, descx.FeatureSet{Enabled: enabled, Hidden: []string{"AGG_MEDIAN"}})
	got, ok := span(renderTree(inst)["questions/describe.md"], "guide-describe-question")
	if !ok || !strings.Contains(got, "Keep this line.") || strings.Contains(got, "AGG_MEDIAN") {
		t.Errorf("scrubbed span = %q", got)
	}
	full, _ := span(renderTree(nil)["questions/describe.md"], "guide-describe-question")
	if !strings.Contains(full, "AGG_MEDIAN") {
		t.Errorf("unscoped span lost the operator: %q", full)
	}
}

// TestParseProse: sections split on whole marker lines, blank edges
// trimmed; text before the first marker and duplicate sections are
// problems.
func TestParseProse(t *testing.T) {
	pf := parseProse("<!-- section a -->\n\nA.\n\n<!-- section b -->\nB1\n\nB2\n")
	if pf.sections["a"] != "A.\n" || pf.sections["b"] != "B1\n\nB2\n" || len(pf.bad) != 0 {
		t.Errorf("parsed %#v", pf)
	}
	if bad := parseProse("stray\n<!-- section a -->\nA\n").bad; len(bad) != 1 {
		t.Errorf("stray text: bad = %v", bad)
	}
	if bad := parseProse("<!-- section a -->\nA\n<!-- section a -->\nB\n").bad; len(bad) != 1 {
		t.Errorf("duplicate: bad = %v", bad)
	}
}

// TestProse_EmbeddedFilesWellFormed: every embedded prose file is the
// landing page's or an analytic intent's, carries only the sections its
// page reads, parses without a problem, and the landing page and every
// analytic intent's guide are fully written.
func TestProse_EmbeddedFilesWellFormed(t *testing.T) {
	analytic := map[string]bool{}
	for _, in := range descx.Intents() {
		if in.Analytic {
			analytic[in.ID] = true
		}
	}
	entries, err := fs.ReadDir(embeddedProse, proseDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		stem, ok := strings.CutSuffix(e.Name(), ".md")
		if !ok {
			t.Errorf("guides/%s: not a .md file", e.Name())
			continue
		}
		known := guideSections
		switch {
		case stem == landingProseFile:
			known = landingSections
		case !analytic[stem]:
			t.Errorf("guides/%s: names neither the landing page nor an analytic intent", e.Name())
			continue
		}
		data, _ := fs.ReadFile(embeddedProse, proseDir+"/"+e.Name())
		pf := parseProse(string(data))
		for _, b := range pf.bad {
			t.Errorf("guides/%s: %s", e.Name(), b)
		}
		for _, name := range pf.order {
			if !slices.Contains(known, name) {
				t.Errorf("guides/%s: unknown section %q (want one of %v)", e.Name(), name, known)
			}
		}
	}
	complete := map[string][]string{landingProseFile: landingSections}
	for id := range analytic {
		complete[id] = guideSections
	}
	rendered := renderTree(nil)
	for stem, want := range complete {
		pf, ok := readProse(stem)
		if !ok {
			t.Errorf("guides/%s.md missing", stem)
			continue
		}
		page, id := "questions.md", landingSpanID
		if stem != landingProseFile {
			page, id = questionPath(stem), func(s string) string { return guideSpanID(stem, s) }
		}
		for _, s := range want {
			if pf.sections[s] == "" {
				t.Errorf("guides/%s.md: section %q not written", stem, s)
			}
			if got, _ := span(rendered[page], id(s)); got != pf.sections[s] {
				t.Errorf("%s: span %s is not guides/%s.md's %q section verbatim", page, id(s), stem, s)
			}
		}
	}
}

// TestGuides_EveryAnalyticIntentHasAStory: on the full instance every
// analytic intent's guide shows a worked story — an example listing the
// intent first, with question, interpretation and a committed golden —
// so no guide falls back to the bare pulse recommend pointer.
func TestGuides_EveryAnalyticIntentHasAStory(t *testing.T) {
	g := newGen(nil, Options{})
	seen := 0
	for _, in := range descx.Intents() {
		if !in.Analytic {
			continue
		}
		seen++
		if _, ok := g.guideStory(in.ID); !ok {
			t.Errorf("%s: no story example (one whose first _meta.intents entry is %s, with question, interpretation and a golden)", in.ID, in.ID)
		}
	}
	if seen == 0 {
		t.Fatal("no analytic intents: vacuous")
	}
}
