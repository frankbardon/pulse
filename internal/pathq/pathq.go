// Package pathq quotes a filesystem path for a human-readable message.
//
// fmt's %q (strconv.Quote) escapes every backslash, so on Windows a path
// in an error or warning message reads `"C:\\Users\\me\\cohort.pulse"`.
// Quote keeps the double quotes and the escaping of a double quote or a
// non-printable rune, but writes each backslash once:
// `"C:\Users\me\cohort.pulse"`. A path without a backslash (every
// ordinary Unix path) quotes byte-identically to %q.
//
// Quote is for message text only. A structured field (details.path, a
// JSON key) carries the raw path, never a quoted one. Every message
// builder that formats a path uses Quote (or %s of an already-quoted
// value); TestNoQuotedPathVerbs refuses a new %q of a path.
//
// The package is a stdlib-only leaf so every package in the module,
// the no-execute descriptor included, can import it.
package pathq

import (
	"strconv"
	"strings"
)

// Quote returns p double-quoted for a message: strconv.Quote, except that
// a backslash is written once instead of escaped.
func Quote(p string) string {
	if !strings.Contains(p, `\`) {
		return strconv.Quote(p)
	}
	parts := strings.Split(p, `\`)
	var b strings.Builder
	b.Grow(len(p) + 2)
	b.WriteByte('"')
	for i, part := range parts {
		if i > 0 {
			b.WriteByte('\\')
		}
		q := strconv.Quote(part)
		b.WriteString(q[1 : len(q)-1])
	}
	b.WriteByte('"')
	return b.String()
}
