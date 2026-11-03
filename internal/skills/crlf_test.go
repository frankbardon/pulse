package skills

import (
	"reflect"
	"strings"
	"testing"
)

// TestSkillParsing_CRLFMatchesLF: a skill file with Windows line endings
// (authored on Windows, or a core.autocrlf checkout) parses exactly like
// its LF twin — frontmatter, metadata and the frontmatter-stripped body
// the budget gates measure. Before the fix ParseFrontmatter matched only
// "---\n", so a CRLF file had no frontmatter at all.
func TestSkillParsing_CRLFMatchesLF(t *testing.T) {
	t.Parallel()

	lf := "---\nname: op-agg-sum\ndescription: Sum.\nkind: operator\ncategory: AGG\noperator: AGG_SUM\napplies_to: [process, compose]\n---\n\n# AGG_SUM\n\n## Params\nnone\n"
	crlf := strings.ReplaceAll(lf, "\n", "\r\n")

	if got, want := ParseFrontmatter(crlf), ParseFrontmatter(lf); len(want) == 0 || !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseFrontmatter(CRLF) = %v, want %v", got, want)
	}
	gotMD, okCR := ParseMetadata(crlf)
	wantMD, okLF := ParseMetadata(lf)
	if !okCR || !okLF || !reflect.DeepEqual(gotMD, wantMD) {
		t.Fatalf("ParseMetadata(CRLF) = %+v (%v), want %+v (%v)", gotMD, okCR, wantMD, okLF)
	}
	if got, want := StripFrontmatter(crlf), StripFrontmatter(lf); got != want {
		t.Fatalf("StripFrontmatter(CRLF) = %q, want %q", got, want)
	}
	if got := NormalizeNewlines("a\r\nb\rc\n"); got != "a\nb\nc\n" {
		t.Fatalf("NormalizeNewlines = %q", got)
	}
}

// TestEmbeddedPack_ServedLF: the embedded pack is served with LF line
// endings whatever the checkout's, so every scanner downstream of Raw
// sees the same bytes on every platform.
func TestEmbeddedPack_ServedLF(t *testing.T) {
	t.Parallel()

	for _, name := range Names() {
		raw, ok := Raw(name)
		if !ok {
			t.Fatalf("Raw(%q) missing", name)
		}
		if strings.Contains(raw, "\r") {
			t.Fatalf("Raw(%q) carries a CR", name)
		}
	}
}
