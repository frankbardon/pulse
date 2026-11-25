package doccheck

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// Link is one link target found in a markdown document.
type Link struct {
	// Target is the destination as written (angle brackets removed).
	Target string
	// Line is the 1-based source line.
	Line int
}

// Document is what ScanMarkdown extracts from one markdown file.
type Document struct {
	// Links are every inline link, image, reference definition, autolink
	// and HTML href / src outside code, in source order.
	Links []Link
	// Anchors are the fragment ids the rendered page carries: one per
	// heading under mdBook's slug rules (duplicates suffixed -1, -2, …,
	// an explicit {#id} attribute winning), plus every raw HTML id / name.
	Anchors map[string]bool
}

var (
	fenceOpenRe = regexp.MustCompile("^ {0,3}(`{3,}|~{3,})")
	atxRe       = regexp.MustCompile(`^ {0,3}(#{1,6})(?:[ \t]+(.*?))?(?:[ \t]+#+)?[ \t]*$`)
	setextRe    = regexp.MustCompile(`^ {0,3}(=+|-+)[ \t]*$`)
	headingIDRe = regexp.MustCompile(`[ \t]*\{([^}]*)\}[ \t]*$`)
	// inlineLinkRe matches the "](dest title)" tail of an inline link or
	// image. The destination is <…> or a run without spaces that may hold
	// one level of balanced parentheses.
	inlineLinkRe   = regexp.MustCompile(`\]\([ \t]*(<[^>\n]*>|(?:[^()\s]|\([^()\s]*\))+)(?:[ \t]+(?:"[^"]*"|'[^']*'|\([^)]*\)))?[ \t]*\)`)
	refDefRe       = regexp.MustCompile(`^ {0,3}\[[^\]]+\]:[ \t]*(<[^>]*>|\S+)`)
	autolinkRe     = regexp.MustCompile(`<((?:https?|mailto|ftp):[^>\s]+)>`)
	htmlLinkRe     = regexp.MustCompile(`(?i)<(?:a|img|link|script|source|iframe)\b[^>]*?\s(?:href|src)\s*=\s*["']([^"']*)["']`)
	htmlIDRe       = regexp.MustCompile(`(?i)<[a-z][a-z0-9]*\b[^>]*?\s(?:id|name)\s*=\s*["']([^"']+)["']`)
	htmlTagRe      = regexp.MustCompile(`</?[a-zA-Z][^>]*>`)
	codeSpanRe     = regexp.MustCompile("(`+)(.+?)(`+)")
	dashRunRe      = regexp.MustCompile(`-{2,}`)
	underscoreEmRe = regexp.MustCompile(`(^|[^\p{L}\p{N}_\\])(__?)([^\s_](?:[^_]*[^\s_])?)__?($|[^\p{L}\p{N}_])`)
	mdLinkTextRe   = regexp.MustCompile(`!?\[([^\]]*)\]\([^)]*\)|!?\[([^\]]*)\]\[[^\]]*\]`)
)

// Flavor selects the renderer whose heading ids ScanMarkdown reproduces.
type Flavor int

const (
	// MDBook is mdBook (docs/src): smart punctuation on, so a run of two
	// or more hyphens outside code renders as a dash the slug drops.
	MDBook Flavor = iota
	// GitHub is GitHub's markdown view (repo-root *.md): hyphens are
	// literal.
	GitHub
)

// ScanMarkdown extracts links and anchors from markdown source rendered by
// flavor. It skips fenced code blocks, HTML comments and inline code spans,
// which render verbatim.
func ScanMarkdown(src []byte, flavor Flavor) Document {
	doc := Document{Anchors: map[string]bool{}}
	counts := map[string]int{}
	addHeading := func(text string) {
		id, explicit := headingID(text, flavor)
		if explicit {
			doc.Anchors[id] = true
			return
		}
		n := counts[id]
		counts[id] = n + 1
		if n > 0 {
			id = id + "-" + strconv.Itoa(n)
		}
		doc.Anchors[id] = true
	}

	lines := strings.Split(strings.ReplaceAll(string(src), "\r\n", "\n"), "\n")
	var fence string // the open fence's marker run, or "" outside a fence
	inComment := false
	prevText := "" // previous line, when it can be a setext heading's text
	for i, raw := range lines {
		lineNo := i + 1
		if fence != "" {
			t := strings.TrimLeft(raw, " ")
			if strings.HasPrefix(t, fence) && strings.Trim(t, string(fence[0])+" \t") == "" {
				fence = ""
			}
			prevText = ""
			continue
		}
		line := raw
		// HTML comments, possibly spanning lines.
		var kept strings.Builder
		for {
			if inComment {
				end := strings.Index(line, "-->")
				if end < 0 {
					line = ""
					break
				}
				line = line[end+3:]
				inComment = false
				continue
			}
			start := strings.Index(line, "<!--")
			if start < 0 {
				kept.WriteString(line)
				break
			}
			kept.WriteString(line[:start])
			line = line[start+4:]
			inComment = true
		}
		line = kept.String()

		if m := fenceOpenRe.FindStringSubmatch(line); m != nil {
			fence = m[1]
			prevText = ""
			continue
		}
		if m := atxRe.FindStringSubmatch(line); m != nil {
			addHeading(m[2])
			prevText = ""
		} else if setextRe.MatchString(line) && prevText != "" {
			addHeading(strings.TrimSpace(prevText))
			prevText = ""
			continue
		}

		code := stripCodeSpans(line)
		for _, m := range inlineLinkRe.FindAllStringSubmatch(code, -1) {
			doc.Links = append(doc.Links, Link{Target: unangle(m[1]), Line: lineNo})
		}
		if m := refDefRe.FindStringSubmatch(code); m != nil {
			doc.Links = append(doc.Links, Link{Target: unangle(m[1]), Line: lineNo})
		}
		for _, m := range autolinkRe.FindAllStringSubmatch(code, -1) {
			doc.Links = append(doc.Links, Link{Target: m[1], Line: lineNo})
		}
		for _, m := range htmlLinkRe.FindAllStringSubmatch(code, -1) {
			doc.Links = append(doc.Links, Link{Target: m[1], Line: lineNo})
		}
		for _, m := range htmlIDRe.FindAllStringSubmatch(code, -1) {
			doc.Anchors[m[1]] = true
		}

		if canBeSetextText(line) {
			prevText = line
		} else {
			prevText = ""
		}
	}
	return doc
}

// canBeSetextText reports whether a line can be the text line of a setext
// heading: a non-blank paragraph line, not a table row, list item, quote,
// heading or HTML block.
func canBeSetextText(line string) bool {
	t := strings.TrimSpace(line)
	if t == "" || len(line)-len(strings.TrimLeft(line, " ")) > 3 {
		return false
	}
	switch t[0] {
	case '|', '>', '#', '<', '-', '*', '+':
		return false
	}
	if i := strings.IndexAny(t, ".)"); i > 0 && i < 10 {
		if _, err := strconv.Atoi(t[:i]); err == nil {
			return false
		}
	}
	return true
}

func unangle(s string) string {
	if strings.HasPrefix(s, "<") && strings.HasSuffix(s, ">") {
		return s[1 : len(s)-1]
	}
	return s
}

// stripCodeSpans blanks inline code spans so links inside them are not
// scanned.
func stripCodeSpans(line string) string {
	if !strings.Contains(line, "`") {
		return line
	}
	return codeSpanRe.ReplaceAllStringFunc(line, func(m string) string {
		sub := codeSpanRe.FindStringSubmatch(m)
		if len(sub[1]) != len(sub[3]) {
			return m
		}
		return strings.Repeat(" ", len(m))
	})
}

// headingID returns the anchor id a heading's source text renders to, and
// whether it came from an explicit {#id} attribute.
func headingID(text string, flavor Flavor) (string, bool) {
	if m := headingIDRe.FindStringSubmatchIndex(text); m != nil {
		for _, attr := range strings.Fields(text[m[2]:m[3]]) {
			if strings.HasPrefix(attr, "#") && len(attr) > 1 {
				return attr[1:], true
			}
		}
		text = text[:m[0]]
	}
	return Slug(HeadingText(text, flavor)), false
}

// HeadingText reduces a heading's markdown source to the plain text mdBook
// derives its id from: code spans keep their content, links and images keep
// their text, emphasis markers and HTML tags are dropped.
func HeadingText(src string, flavor Flavor) string {
	var b strings.Builder
	rest := src
	for {
		loc := codeSpanRe.FindStringSubmatchIndex(rest)
		if loc == nil || rest[loc[2]:loc[3]] != rest[loc[6]:loc[7]] {
			b.WriteString(plainInline(rest, flavor))
			break
		}
		b.WriteString(plainInline(rest[:loc[0]], flavor))
		code := rest[loc[4]:loc[5]]
		if len(code) >= 2 && code[0] == ' ' && code[len(code)-1] == ' ' && strings.Trim(code, " ") != "" {
			code = code[1 : len(code)-1]
		}
		b.WriteString(code)
		rest = rest[loc[1]:]
	}
	return b.String()
}

// plainInline strips inline markup from text outside code spans.
func plainInline(s string, flavor Flavor) string {
	s = mdLinkTextRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := mdLinkTextRe.FindStringSubmatch(m)
		if sub[1] != "" {
			return sub[1]
		}
		return sub[2]
	})
	s = htmlTagRe.ReplaceAllString(s, "")
	// mdBook renders with smart punctuation: a run of two or more hyphens
	// outside code becomes an en / em dash, which the slug drops.
	if flavor == MDBook {
		s = dashRunRe.ReplaceAllString(s, "")
	}
	// Underscore emphasis: a matched _x_ / __x__ pair whose delimiters are
	// not intraword. An unmatched or intraword underscore stays literal.
	for {
		t := underscoreEmRe.ReplaceAllString(s, "$1$3$4")
		if t == s {
			break
		}
		s = t
	}
	return strings.ReplaceAll(s, `\_`, "_")
}

// Slug is mdBook's heading-id rule (normalize_id): keep alphanumerics, '_'
// and '-', ASCII-lowercased; whitespace becomes '-'; everything else is
// dropped.
func Slug(text string) string {
	var b strings.Builder
	for _, c := range strings.TrimSpace(text) {
		switch {
		case unicode.IsLetter(c) || unicode.IsNumber(c) || c == '_' || c == '-':
			if c < unicode.MaxASCII {
				c = unicode.ToLower(c)
			}
			b.WriteRune(c)
		case unicode.IsSpace(c):
			b.WriteRune('-')
		}
	}
	return b.String()
}
