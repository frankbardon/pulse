package examples

import (
	"embed"
	"encoding/json"
	"fmt"
	"path"
	"strings"
)

// StoryPrecision is the `return.precision` (significant digits) every
// story request sets, so its golden is identical on every architecture:
// a few-ulp difference in a float's tail never reaches the wire.
const StoryPrecision = 4

// StoryGoldenDir is the directory, relative to this package, holding
// one harness-produced golden per story: <name>.json, the story
// response as json.MarshalIndent writes it. The root package's
// TestExamples_StoryGoldens writes it (-update) and compares it; never
// hand-edit one.
const StoryGoldenDir = "testdata/stories"

//go:embed testdata/stories/*.json
var storyGoldens embed.FS

// Stories returns the name of every embedded story example,
// sorted alphabetically.
func Stories() []string {
	idx := loadIndex()
	var out []string
	for _, n := range idx.all {
		if isStory(idx.byName[n]) {
			out = append(out, n)
		}
	}
	return out
}

// StoryGolden returns the committed golden response of the named story
// example — read by the generated docs, which never execute a request.
// Returns false when no golden carries the name.
func StoryGolden(name string) ([]byte, bool) {
	data, err := storyGoldens.ReadFile(path.Join(StoryGoldenDir, name+".json"))
	if err != nil {
		return nil, false
	}
	return data, true
}

// StoryGoldenNames returns the name of every embedded story golden,
// sorted alphabetically.
func StoryGoldenNames() []string {
	entries, _ := storyGoldens.ReadDir(StoryGoldenDir)
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if n, ok := strings.CutSuffix(e.Name(), ".json"); ok {
			out = append(out, n)
		}
	}
	return out
}

// StoryProblems returns every way an example's _meta and request body
// break the story rules, empty when they hold. An example is a story
// iff it declares both question and interpretation; one without the
// other is a problem. A story's interpretation is qualitative — it
// carries no digit, the figures live in the golden — and its request
// sets `return.precision` to StoryPrecision. A non-story is never a
// problem.
func StoryProblems(m Meta, body []byte) []string {
	q := strings.TrimSpace(m.Question) != ""
	in := strings.TrimSpace(m.Interpretation) != ""
	switch {
	case q && !in:
		return []string{"_meta.question without _meta.interpretation: a story declares both"}
	case in && !q:
		return []string{"_meta.interpretation without _meta.question: a story declares both"}
	case !q:
		return nil
	}
	var out []string
	if strings.ContainsFunc(m.Interpretation, func(r rune) bool { return r >= '0' && r <= '9' }) {
		out = append(out, "_meta.interpretation carries a digit: keep it qualitative, the figures live in the golden")
	}
	var req struct {
		Return *struct {
			Precision int `json:"precision"`
		} `json:"return"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		out = append(out, "body does not parse: "+err.Error())
	} else if req.Return == nil || req.Return.Precision != StoryPrecision {
		got := 0
		if req.Return != nil {
			got = req.Return.Precision
		}
		out = append(out, fmt.Sprintf("a story request sets return.precision %d (got %d) so its golden is identical on every architecture", StoryPrecision, got))
	}
	return out
}
