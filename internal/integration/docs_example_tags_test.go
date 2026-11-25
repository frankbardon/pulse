package integration_test

import (
	"fmt"
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/internal/examples"
)

// exampleLibraryDoc is the book page whose "Tag taxonomy" table lists the
// example library's canonical tags.
const exampleLibraryDoc = "docs/src/examples/library.md"

// docTagRe reads one backticked tag from a taxonomy table row.
var docTagRe = regexp.MustCompile("`([a-z0-9-]+)`")

// TestDocsExampleTagTableMatchesCanonicalTags holds the example-library
// page's tag table to internal/examples.CanonicalTags: every canonical
// tag is listed once, and the table lists nothing else. A tag added to or
// dropped from the taxonomy fails here until the page follows (#758).
func TestDocsExampleTagTableMatchesCanonicalTags(t *testing.T) {
	t.Parallel()

	doc, err := os.ReadFile(exampleLibraryDoc)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range exampleTagTableProblems(string(doc), examples.CanonicalTags) {
		t.Error(p)
	}
}

// TestDocsExampleTagTableMatchesCanonicalTagsRefuses pins the comparison
// on fixtures: a missing tag, an extra tag, a repeated tag and a page
// without the table are each reported.
func TestDocsExampleTagTableMatchesCanonicalTagsRefuses(t *testing.T) {
	t.Parallel()

	canon := []string{"a-tag", "b-tag", "c-tag"}
	table := func(rows ...string) string {
		return "## Tag taxonomy\n\n| Dimension | Tags |\n|---|---|\n" + strings.Join(rows, "\n") + "\n\nAfter.\n"
	}
	good := table("| One | `a-tag`, `b-tag` |", "| Two | `c-tag` |")
	if p := exampleTagTableProblems(good, canon); len(p) != 0 {
		t.Fatalf("clean fixture reported %q", p)
	}
	cases := map[string]struct{ doc, want string }{
		"missing":  {table("| One | `a-tag` |", "| Two | `c-tag` |"), "b-tag"},
		"extra":    {table("| One | `a-tag`, `b-tag` |", "| Two | `c-tag`, `geo-analysis` |"), "geo-analysis"},
		"twice":    {table("| One | `a-tag`, `b-tag` |", "| Two | `c-tag`, `a-tag` |"), "more than once"},
		"no table": {"# Page\n\nNo taxonomy here.\n", "no tag table"},
	}
	for name, c := range cases {
		p := exampleTagTableProblems(c.doc, canon)
		if len(p) == 0 || !strings.Contains(strings.Join(p, "\n"), c.want) {
			t.Errorf("%s: problems %q, want one naming %q", name, p, c.want)
		}
	}
}

// exampleTagTableProblems compares the tags in the page's "Tag taxonomy"
// table (the Tags column of every row) with the canonical list.
func exampleTagTableProblems(doc string, canon []string) []string {
	_, section, ok := strings.Cut(doc, "## Tag taxonomy\n")
	if !ok {
		return []string{exampleLibraryDoc + ": no tag table (heading \"## Tag taxonomy\" missing)"}
	}
	if next := strings.Index(section, "\n## "); next >= 0 {
		section = section[:next]
	}
	seen := map[string]int{}
	rows := 0
	for _, line := range strings.Split(section, "\n") {
		cells := strings.Split(line, "|")
		if len(cells) < 4 || strings.HasPrefix(strings.TrimSpace(cells[1]), "---") || strings.TrimSpace(cells[1]) == "Dimension" {
			continue
		}
		rows++
		for _, m := range docTagRe.FindAllStringSubmatch(cells[2], -1) {
			seen[m[1]]++
		}
	}
	if rows == 0 {
		return []string{exampleLibraryDoc + ": no tag table rows under \"## Tag taxonomy\""}
	}
	var problems []string
	for _, tag := range canon {
		if seen[tag] == 0 {
			problems = append(problems, fmt.Sprintf("%s: canonical tag %q is not in the tag table", exampleLibraryDoc, tag))
		}
	}
	var listed []string
	for tag, n := range seen {
		listed = append(listed, tag)
		if n > 1 {
			problems = append(problems, fmt.Sprintf("%s: tag %q is listed more than once", exampleLibraryDoc, tag))
		}
	}
	sort.Strings(listed)
	for _, tag := range listed {
		if !slices.Contains(canon, tag) {
			problems = append(problems, fmt.Sprintf("%s: tag %q is not in internal/examples.CanonicalTags", exampleLibraryDoc, tag))
		}
	}
	return problems
}
