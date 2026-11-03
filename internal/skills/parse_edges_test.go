package skills

import (
	"reflect"
	"testing"
)

// TestParseList_Forms: a frontmatter list reads the same in its bracket
// and comma forms; quotes and blank fragments are dropped, and a list
// that holds nothing is nil (absent), never an empty non-nil slice.
func TestParseList_Forms(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"   ", nil},
		{"[]", nil},
		{`[ , "", ' ' ]`, nil},
		{"a, b", []string{"a", "b"}},
		{`["a", 'b', c]`, []string{"a", "b", "c"}},
		{"a,,b,", []string{"a", "b"}},
		{`[" spaced "]`, []string{"spaced"}},
	}
	for _, tc := range cases {
		if got := parseList(tc.in); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("parseList(%q) = %#v, want %#v", tc.in, got, tc.want)
		}
	}
}

// TestStripFrontmatter_Edges: only a leading, closed `---` block is
// stripped; a file without one, or with an unclosed one, is returned
// whole (the budget gates must measure a malformed file in full).
func TestStripFrontmatter_Edges(t *testing.T) {
	t.Parallel()

	cases := []struct{ in, want string }{
		{"no frontmatter\n", "no frontmatter\n"},
		{"---\nname: x\nnever closed\n", "---\nname: x\nnever closed\n"},
		{"---\nname: x\n---\nbody\n", "body\n"},
		{"---\nname: x\n---\n\n## H\n", "\n## H\n"},
		{"text\n---\nname: x\n---\n", "text\n---\nname: x\n---\n"},
	}
	for _, tc := range cases {
		if got := StripFrontmatter(tc.in); got != tc.want {
			t.Errorf("StripFrontmatter(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestIsBareLine: a line a fence cut left with only a list, heading or
// numbered-list marker is bare (and dropped); one with any content is
// not.
func TestIsBareLine(t *testing.T) {
	t.Parallel()

	bare := []string{"", "   ", "-", " * ", "+", "#", "###", "1.", " 12. "}
	kept := []string{"- item", "## Head", "1. step", "1.5", ".", "x", "12"}
	for _, s := range bare {
		if !isBareLine(s) {
			t.Errorf("isBareLine(%q) = false, want true", s)
		}
	}
	for _, s := range kept {
		if isBareLine(s) {
			t.Errorf("isBareLine(%q) = true, want false", s)
		}
	}
}
