package docgen

import (
	"errors"
	"strings"
)

// BookSpanBegin and BookSpanEnd delimit the generated span of a host
// book's SUMMARY.md: SpliceSummary replaces every line between them
// with an export's SUMMARY.md fragment. Both are whole lines; the host
// book owns everything outside them (the part title included).
const (
	BookSpanBegin = "<!-- docgen:summary begin -->"
	BookSpanEnd   = "<!-- docgen:summary end -->"
)

// SpliceSummary splices fragment — the SUMMARY.md an export writes at
// its root — into book, the host book's SUMMARY.md, between the
// BookSpanBegin and BookSpanEnd lines. The fragment's title line and
// blank lines are dropped and every link path is prefixed with prefix
// (the export directory relative to the book's src, e.g. "guide/"), so
// the entries resolve from the book root. The splice is idempotent:
// splicing the same fragment into its own output returns it unchanged.
//
// It refuses a book without exactly one begin line followed by exactly
// one end line, and a fragment line that is not a "- [title](path)"
// list entry.
func SpliceSummary(book, fragment []byte, prefix string) ([]byte, error) {
	lines := strings.SplitAfter(string(book), "\n")
	begin, end := -1, -1
	for i, l := range lines {
		switch strings.TrimRight(l, "\r\n") {
		case BookSpanBegin:
			if begin >= 0 {
				return nil, errors.New("docgen: SUMMARY.md carries more than one " + BookSpanBegin + " line")
			}
			begin = i
		case BookSpanEnd:
			if end >= 0 {
				return nil, errors.New("docgen: SUMMARY.md carries more than one " + BookSpanEnd + " line")
			}
			end = i
		}
	}
	if begin < 0 || end < 0 {
		return nil, errors.New("docgen: SUMMARY.md needs a " + BookSpanBegin + " line and a " + BookSpanEnd + " line")
	}
	if end < begin {
		return nil, errors.New("docgen: SUMMARY.md has " + BookSpanEnd + " before " + BookSpanBegin)
	}
	entries, err := summaryEntries(fragment, prefix)
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	for _, l := range lines[:begin+1] {
		b.WriteString(l)
	}
	for _, e := range entries {
		b.WriteString(e)
		b.WriteString("\n")
	}
	for _, l := range lines[end:] {
		b.WriteString(l)
	}
	return []byte(b.String()), nil
}

// summaryEntries returns the fragment's list entries with prefix put in
// front of every link path.
func summaryEntries(fragment []byte, prefix string) ([]string, error) {
	var out []string
	for _, l := range strings.Split(string(fragment), "\n") {
		l = strings.TrimRight(l, "\r")
		if strings.TrimSpace(l) == "" || strings.HasPrefix(l, "# ") {
			continue
		}
		body := strings.TrimLeft(l, " ")
		open := strings.Index(body, "](")
		if !strings.HasPrefix(body, "- [") || open < 0 || !strings.HasSuffix(body, ")") {
			return nil, errors.New("docgen: SUMMARY.md fragment line is not a \"- [title](path)\" entry: " + l)
		}
		cut := len(l) - len(body) + open + 2
		out = append(out, l[:cut]+prefix+l[cut:])
	}
	return out, nil
}
