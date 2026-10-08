package docgen

import (
	"regexp"
	"strings"
)

// placeholderTag matches a bare `<word>` placeholder such as `<field>`
// or `<k>`: a tag-shaped token with no attributes, slash or bang. Real
// HTML docgen emits on purpose (`<a id="…">`, `</a>`, `<!-- … -->`)
// never matches; `<br>` (table-cell line breaks) is allowed by name.
var placeholderTag = regexp.MustCompile(`<([A-Za-z_][A-Za-z0-9_-]*)>`)

// htmlTags names the attribute-free tags docgen emits as real HTML.
var htmlTags = map[string]bool{"br": true}

// escapePlaceholders HTML-escapes every bare `<word>` placeholder that
// sits outside a code fence or inline code span, so mdBook renders it
// as text instead of swallowing it as an unclosed HTML tag. Text inside
// code is left alone. The transform is idempotent.
func escapePlaceholders(md string) string {
	if !strings.Contains(md, "<") {
		return md
	}
	lines := strings.Split(md, "\n")
	fence := ""
	for i, line := range lines {
		trim := strings.TrimSpace(line)
		if fence != "" {
			if strings.HasPrefix(trim, fence) {
				fence = ""
			}
			continue
		}
		if m := fenceOpen(trim); m != "" {
			fence = m
			continue
		}
		if strings.Contains(line, "<") {
			lines[i] = escapeLine(line)
		}
	}
	return strings.Join(lines, "\n")
}

// fenceOpen returns the fence marker (``` or ~~~ run) line opens, or "".
func fenceOpen(trim string) string {
	for _, c := range []byte{'`', '~'} {
		n := 0
		for n < len(trim) && trim[n] == c {
			n++
		}
		if n >= 3 {
			return trim[:n]
		}
	}
	return ""
}

// escapeLine escapes placeholders in line outside its inline code spans.
func escapeLine(line string) string {
	var b strings.Builder
	text := 0 // start of the pending non-code segment
	for i := 0; i < len(line); {
		if line[i] != '`' {
			i++
			continue
		}
		n := runLen(line, i)
		closeAt := findRun(line, i+n, n)
		if closeAt < 0 {
			i += n
			continue
		}
		b.WriteString(escapeText(line[text:i]))
		b.WriteString(line[i : closeAt+n])
		i = closeAt + n
		text = i
	}
	b.WriteString(escapeText(line[text:]))
	return b.String()
}

func runLen(s string, i int) int {
	n := 0
	for i+n < len(s) && s[i+n] == '`' {
		n++
	}
	return n
}

// findRun returns the index of the next backtick run of exactly n at or
// after from, or -1.
func findRun(s string, from, n int) int {
	for i := from; i < len(s); {
		if s[i] != '`' {
			i++
			continue
		}
		m := runLen(s, i)
		if m == n {
			return i
		}
		i += m
	}
	return -1
}

func escapeText(s string) string {
	return placeholderTag.ReplaceAllStringFunc(s, func(tag string) string {
		if htmlTags[strings.ToLower(tag[1:len(tag)-1])] {
			return tag
		}
		return "&lt;" + tag[1:len(tag)-1] + "&gt;"
	})
}
