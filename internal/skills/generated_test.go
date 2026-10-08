package skills

import (
	"strings"
	"testing"
)

func TestMarkerSection(t *testing.T) {
	for line, want := range map[string]string{
		"<!-- generated: use-when -->":            SectionUseWhen,
		"  <!--generated:reading-the-output-->  ": SectionReadingTheOutput,
		"<!-- generated: nope -->":                "",
		"text <!-- generated: use-when -->":       "",
		"<!-- feature: TEST_WELCH -->":            "",
	} {
		got, ok := MarkerSection(line)
		if got != want || ok != (want != "") {
			t.Errorf("MarkerSection(%q) = %q, %v; want %q", line, got, ok, want)
		}
	}
	for _, s := range GeneratedSections() {
		if got, ok := MarkerSection(GeneratedMarker(s)); !ok || got != s {
			t.Errorf("GeneratedMarker(%s) does not round-trip", s)
		}
	}
}

// TestRenderGenerated: each lone marker line becomes its rendered
// section; an empty render removes the marker and the blank line it
// would leave doubled; anything else is untouched.
func TestRenderGenerated(t *testing.T) {
	render := func(sec string) string {
		if sec == SectionUseWhen {
			return "## Use when\n\nRendered.\n"
		}
		return ""
	}
	cases := []struct{ name, in, want string }{
		{"no marker", "a\n\n## Params\n", "a\n\n## Params\n"},
		{"replaced", "a\n\n<!-- generated: use-when -->\n\n## Params\n", "a\n\n## Use when\n\nRendered.\n\n## Params\n"},
		{"removed", "a\n\n<!-- generated: reading-the-output -->\n\n## Gotchas\n", "a\n\n## Gotchas\n"},
		{"removed at end", "## Output\n\nx\n<!-- generated: reading-the-output -->\n", "## Output\n\nx\n"},
		{"unknown section kept", "a\n<!-- generated: other -->\nb", "a\n<!-- generated: other -->\nb"},
		{"inline kept", "a <!-- generated: use-when -->\nb", "a <!-- generated: use-when -->\nb"},
	}
	for _, tc := range cases {
		if got := RenderGenerated(tc.in, render); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
	if got := RenderGenerated("a\n<!-- generated: use-when -->\nb", nil); got != "a\nb" {
		t.Errorf("nil render: got %q", got)
	}
}

// TestRenderGenerated_Coexists: a marker body goes through the fence
// pass first (a marker inside a hidden fence is gone with it), and the
// rendered sections satisfy HasHeading's exact-line match and survive
// StripFrontmatter.
func TestRenderGenerated_Coexists(t *testing.T) {
	body := "---\nname: op-x\noperator: X\n---\n\nLead.\n\n<!-- generated: use-when -->\n\n## Params\n\n" +
		"<!-- feature: TEST_WELCH -->\n<!-- generated: reading-the-output -->\n<!-- /feature -->\n\n## See\n"
	render := func(sec string) string { return "## " + sec + "\n\nBody." }
	for _, tc := range []struct {
		name    string
		keep    func(string) bool
		reading bool
	}{
		{"fence kept", nil, true},
		{"fence hidden", func(string) bool { return false }, false},
	} {
		fenced, err := RenderFences(body, tc.keep)
		if err != nil {
			t.Fatal(err)
		}
		out := RenderGenerated(fenced, render)
		if !HasHeading(out, "## use-when") || HasHeading(out, "## reading-the-output") != tc.reading {
			t.Errorf("%s: headings wrong:\n%s", tc.name, out)
		}
		if !strings.HasPrefix(strings.TrimSpace(StripFrontmatter(out)), "Lead.") {
			t.Errorf("%s: StripFrontmatter lost the lead:\n%s", tc.name, out)
		}
		if strings.Contains(out, "<!--") {
			t.Errorf("%s: a marker survived:\n%s", tc.name, out)
		}
	}
}
