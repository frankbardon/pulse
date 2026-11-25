package doccheck

import (
	"sort"
	"strings"
	"testing"

	"github.com/spf13/afero"
)

func TestSlugMatchesMDBook(t *testing.T) {
	t.Parallel()
	// Each want was read off `mdbook build` (v0.5) output for a page of
	// this repo, except where noted.
	cases := []struct {
		heading string
		flavor  Flavor
		want    string
	}{
		{"Structural gates", MDBook, "structural-gates"},
		{"`--json`", MDBook, "--json"},               // code keeps hyphens
		{"--bind-on-open", MDBook, "bind-on-open"},   // smart dash dropped
		{"--bind-on-open", GitHub, "--bind-on-open"}, // GitHub keeps them
		{"\"data directory required: set PULSE_DATA_DIR or pass --data-dir\"", MDBook, "data-directory-required-set-pulse_data_dir-or-pass-data-dir"},
		{"`--conditional`: set_* pair capture (set-categorical / set-numeric / set-set)", MDBook, "--conditional-set_-pair-capture-set-categorical--set-numeric--set-set"},
		{"A *b* and _c_ **d**", MDBook, "a-b-and-c-d"},
		{"snake_case_name", MDBook, "snake_case_name"},
		{"See [the page](x.md) now", MDBook, "see-the-page-now"},
		{"<code>Tag</code> text", MDBook, "tag-text"},
		{"Ünïcode Straße", MDBook, "Ünïcode-straße"}, // only ASCII is lowercased
	}
	for _, tc := range cases {
		if got := Slug(HeadingText(tc.heading, tc.flavor)); got != tc.want {
			t.Errorf("slug(%q, %v) = %q, want %q", tc.heading, tc.flavor, got, tc.want)
		}
	}
}

func TestScanMarkdownAnchors(t *testing.T) {
	t.Parallel()
	src := strings.Join([]string{
		"# Title",
		"## Dup",
		"## Dup",
		"## Dup",
		"### Custom {#my-id}",
		"Setext heading",
		"--------------",
		"",
		"---",
		"",
		"```",
		"# not a heading",
		"```",
		"<!-- # hidden",
		"## also hidden -->",
		`<a id="raw-anchor"></a> <span name="named">`,
		"- list item",
		"---",
	}, "\n")
	got := ScanMarkdown([]byte(src), MDBook).Anchors
	var keys []string
	for k := range got {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	want := []string{"dup", "dup-1", "dup-2", "my-id", "named", "raw-anchor", "setext-heading", "title"}
	if strings.Join(keys, ",") != strings.Join(want, ",") {
		t.Fatalf("anchors = %v, want %v", keys, want)
	}
}

func TestScanMarkdownLinks(t *testing.T) {
	t.Parallel()
	src := strings.Join([]string{
		"[a](one.md) and ![img](pic.png \"title\") and [b](<sp ace.md>)",
		"`[code](skipped.md)` [c](two.md#frag)",
		"```",
		"[fenced](skipped.md)",
		"```",
		"<!-- [commented](skipped.md) -->",
		"[ref]: three.md",
		`<a href="four.md">x</a> <img src="five.png">`,
		"<https://example.com/x> [ext](http://example.com)",
		"[paren](f(1).md) [multi",
		"line](six.md)",
	}, "\n")
	var got []string
	for _, l := range ScanMarkdown([]byte(src), MDBook).Links {
		got = append(got, l.Target)
	}
	want := []string{"one.md", "pic.png", "sp ace.md", "two.md#frag", "three.md", "four.md", "five.png", "http://example.com", "https://example.com/x", "f(1).md", "six.md"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("links = %q\nwant    %q", got, want)
	}
}

func linkFixture(t *testing.T) afero.Fs {
	t.Helper()
	fsys := afero.NewMemMapFs()
	files := map[string]string{
		"docs/src/SUMMARY.md":       "# Summary\n- [A](a.md)\n- [B](sub/b.md)\n- [Sub](sub/README.md)\n- [Draft]()\n",
		"docs/src/a.md":             "# A page\n## `--flag`\n",
		"docs/src/sub/b.md":         "# B\n## Section Two\n",
		"docs/src/sub/README.md":    "# Sub index\n",
		"docs/src/unlisted.md":      "# Unlisted\n",
		"docs/src/img/pic.png":      "png",
		"docs/src/nodir/x.txt":      "x",
		"README.md":                 "# Readme\n## Use --flag\n",
		"internal/thing.go":         "package thing\n",
		"docs/src/links.md":         "",
		"internal/somedir/file.txt": "x",
	}
	for p, c := range files {
		if err := afero.WriteFile(fsys, p, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return fsys
}

func TestCheckLinks(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		file   string
		target string
		want   string // substring of the violation message; "" = resolves
	}{
		{"chapter", "docs/src/links.md", "a.md", ""},
		{"chapter anchor", "docs/src/links.md", "a.md#--flag", ""},
		{"smart-dash anchor is wrong", "docs/src/links.md", "a.md#flag", "anchor #flag"},
		{"subdir anchor", "docs/src/links.md", "sub/b.md#section-two", ""},
		{"relative up", "docs/src/sub/b.md", "../a.md#a-page", ""},
		{"same-page anchor", "docs/src/sub/b.md", "#section-two", ""},
		{"same-page bad anchor", "docs/src/sub/b.md", "#nope", "anchor #nope matches no heading"},
		{"missing file", "docs/src/links.md", "gone.md", "does not exist"},
		{"case exact", "docs/src/links.md", "A.md", "does not exist"},
		{"html maps to md", "docs/src/links.md", "sub/b.html#section-two", ""},
		{"index.html maps to README", "docs/src/links.md", "sub/index.html", ""},
		{"dir with README", "docs/src/links.md", "sub/", ""},
		{"dir without page", "docs/src/links.md", "nodir/", "no README.md / index.md"},
		{"asset", "docs/src/links.md", "img/pic.png", ""},
		{"absolute in book", "docs/src/sub/b.md", "/a.md", ""},
		{"escapes book", "docs/src/links.md", "../../internal/thing.go", "outside the book source"},
		{"escapes repo", "README.md", "../x.md", "outside the repository"},
		{"not a chapter", "docs/src/links.md", "unlisted.md", "not a chapter"},
		{"external", "docs/src/links.md", "https://example.com/nowhere", ""},
		{"mailto", "docs/src/links.md", "mailto:x@example.com", ""},
		{"percent-encoded", "docs/src/links.md", "sub/b.md#section%2Dtwo", ""},
		{"root to repo file", "README.md", "internal/thing.go", ""},
		{"root to repo dir", "README.md", "internal/somedir", ""},
		{"root to book page anchor", "README.md", "docs/src/a.md#--flag", ""},
		{"root github anchor keeps hyphens", "README.md", "#use---flag", ""},
		{"root missing", "README.md", "nope.md", "does not exist"},
	}
	fsys := linkFixture(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c, err := newLinkChecker(fsys, LinkOptions{BookRoot: BookRoot})
			if err != nil {
				t.Fatal(err)
			}
			got := c.resolve(tc.file, tc.target)
			switch {
			case tc.want == "" && got != "":
				t.Fatalf("resolve(%s, %s) = %q, want resolved", tc.file, tc.target, got)
			case tc.want != "" && !strings.Contains(got, tc.want):
				t.Fatalf("resolve(%s, %s) = %q, want containing %q", tc.file, tc.target, got, tc.want)
			}
		})
	}
}

func TestCheckLinksEndToEnd(t *testing.T) {
	t.Parallel()
	fsys := linkFixture(t)
	page := "# Links\n[ok](a.md) [bad](gone.md) [bad](gone.md) [frag](#links) [nofrag](#x)\n"
	if err := afero.WriteFile(fsys, "docs/src/links.md", []byte(page), 0o644); err != nil {
		t.Fatal(err)
	}
	files, err := DocSources(fsys)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(files, ",") != "README.md,docs/src/SUMMARY.md,docs/src/a.md,docs/src/links.md,docs/src/sub/README.md,docs/src/sub/b.md,docs/src/unlisted.md" {
		t.Fatalf("DocSources = %v", files)
	}
	vs, err := CheckLinks(fsys, files, LinkOptions{BookRoot: BookRoot})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, v := range vs {
		got = append(got, v.Path+" "+v.Entry)
	}
	want := "docs/src/links.md #x,docs/src/links.md gone.md,docs/src/links.md gone.md"
	if strings.Join(got, ",") != want {
		t.Fatalf("violations = %q, want %q", got, want)
	}
}
