// Command docsummary splices the SUMMARY.md fragment a `pulse docs
// export` writes into the book's own SUMMARY.md, between the
// docgen.BookSpanBegin / BookSpanEnd lines, prefixing every path with
// the export directory. `make docs` runs it after the export; it is a
// development tool only (never imported, never shipped).
//
//	go run ./internal/tools/docsummary -book docs/src/SUMMARY.md \
//	    -fragment docs/src/guide/SUMMARY.md -prefix guide/
//
// The book file is rewritten only when the splice changes it.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"

	"github.com/frankbardon/pulse/internal/docgen"
)

func main() {
	book := flag.String("book", "docs/src/SUMMARY.md", "the book's SUMMARY.md, rewritten in place")
	fragment := flag.String("fragment", "docs/src/guide/SUMMARY.md", "the SUMMARY.md fragment the export wrote")
	prefix := flag.String("prefix", "guide/", "path prefix from the book's src to the export directory")
	flag.Parse()
	if err := run(*book, *fragment, *prefix); err != nil {
		fmt.Fprintln(os.Stderr, "docsummary:", err)
		os.Exit(1)
	}
}

func run(bookPath, fragmentPath, prefix string) error {
	book, err := os.ReadFile(bookPath)
	if err != nil {
		return err
	}
	frag, err := os.ReadFile(fragmentPath)
	if err != nil {
		return err
	}
	out, err := docgen.SpliceSummary(book, frag, prefix)
	if err != nil {
		return fmt.Errorf("%s: %w", bookPath, err)
	}
	if bytes.Equal(out, book) {
		return nil
	}
	return os.WriteFile(bookPath, out, 0o644)
}
