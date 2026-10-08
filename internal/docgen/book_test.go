package docgen

import (
	"strings"
	"testing"
)

const testBook = "# Summary\n\n[Introduction](README.md)\n\n# Getting Started\n\n- [Install](install.md)\n\n# Analysis Guide\n\n" +
	BookSpanBegin + "\n- [stale](guide/old.md)\n" + BookSpanEnd + "\n\n# Internals\n\n- [Arch](arch.md)\n"

const testFragment = "# Summary\n\n- [Analysis Guide](index.md)\n  - [Operator catalog](catalog.md)\n    - [Aggregators](catalog/aggregator.md)\n  - [Glossary](glossary.md)\n"

func TestSpliceSummary_ReplacesSpanAndPrefixesPaths(t *testing.T) {
	got, err := SpliceSummary([]byte(testBook), []byte(testFragment), "guide/")
	if err != nil {
		t.Fatal(err)
	}
	want := "# Summary\n\n[Introduction](README.md)\n\n# Getting Started\n\n- [Install](install.md)\n\n# Analysis Guide\n\n" +
		BookSpanBegin + "\n" +
		"- [Analysis Guide](guide/index.md)\n" +
		"  - [Operator catalog](guide/catalog.md)\n" +
		"    - [Aggregators](guide/catalog/aggregator.md)\n" +
		"  - [Glossary](guide/glossary.md)\n" +
		BookSpanEnd + "\n\n# Internals\n\n- [Arch](arch.md)\n"
	if string(got) != want {
		t.Fatalf("splice:\n got %q\nwant %q", got, want)
	}
	again, err := SpliceSummary(got, []byte(testFragment), "guide/")
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != string(got) {
		t.Fatalf("splice is not idempotent:\n got %q\nwant %q", again, got)
	}
}

func TestSpliceSummary_RenderedFragmentSplices(t *testing.T) {
	var frag []byte
	for _, f := range Render(nil, Options{}) {
		if f.Path == "SUMMARY.md" {
			frag = f.Body
		}
	}
	got, err := SpliceSummary([]byte(testBook), frag, "guide/")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "- [Analysis Guide](guide/index.md)\n") || strings.Contains(string(got), "guide/old.md") {
		t.Fatalf("rendered fragment did not splice:\n%s", got)
	}
}

func TestSpliceSummary_Refusals(t *testing.T) {
	cases := map[string]struct{ book, frag string }{
		"no markers":      {"# Summary\n", testFragment},
		"no end":          {BookSpanBegin + "\n", testFragment},
		"two begins":      {BookSpanBegin + "\n" + BookSpanBegin + "\n" + BookSpanEnd + "\n", testFragment},
		"two ends":        {BookSpanBegin + "\n" + BookSpanEnd + "\n" + BookSpanEnd + "\n", testFragment},
		"end before":      {BookSpanEnd + "\n" + BookSpanBegin + "\n", testFragment},
		"prose fragment":  {testBook, "# Summary\n\nsome prose\n"},
		"unlinked bullet": {testBook, "- just text\n"},
	}
	for name, c := range cases {
		if _, err := SpliceSummary([]byte(c.book), []byte(c.frag), "guide/"); err == nil {
			t.Errorf("%s: want an error, got none", name)
		}
	}
}
