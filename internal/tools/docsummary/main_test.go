package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/frankbardon/pulse/internal/docgen"
)

func TestRun_SplicesInPlaceAndRefusesUnmarkedBook(t *testing.T) {
	dir := t.TempDir()
	book := filepath.Join(dir, "SUMMARY.md")
	frag := filepath.Join(dir, "fragment.md")
	if err := os.WriteFile(book, []byte("# Summary\n\n"+docgen.BookSpanBegin+"\n"+docgen.BookSpanEnd+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(frag, []byte("# Summary\n\n- [Analysis Guide](index.md)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run(book, frag, "guide/"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(book)
	if err != nil {
		t.Fatal(err)
	}
	want := "# Summary\n\n" + docgen.BookSpanBegin + "\n- [Analysis Guide](guide/index.md)\n" + docgen.BookSpanEnd + "\n"
	if string(got) != want {
		t.Fatalf("book:\n got %q\nwant %q", got, want)
	}
	if err := run(book, frag, "guide/"); err != nil {
		t.Fatalf("second run: %v", err)
	}

	if err := os.WriteFile(book, []byte("# Summary\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run(book, frag, "guide/"); err == nil {
		t.Fatal("want an error for a book without the span markers")
	}
	if err := run(filepath.Join(dir, "missing.md"), frag, "guide/"); err == nil {
		t.Fatal("want an error for a missing book")
	}
}
