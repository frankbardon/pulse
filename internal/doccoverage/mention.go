package doccoverage

import "strings"

// corpus answers "does this identifier appear verbatim, as a whole token?"
// over a fixed body of text. It indexes the start offset of every word run
// ([A-Za-z0-9_]+) so a lookup only visits the occurrences of the
// identifier's rarest word run.
type corpus struct {
	text  string
	words map[string][]int
}

// corpusSep separates joined documents; it holds no word character, so a
// match never spans two documents.
const corpusSep = "\n\x00\n"

func newCorpus(docs []string) *corpus {
	c := &corpus{text: strings.Join(docs, corpusSep), words: map[string][]int{}}
	t := c.text
	for i := 0; i < len(t); {
		if !isWord(t[i]) {
			i++
			continue
		}
		j := i
		for j < len(t) && isWord(t[j]) {
			j++
		}
		w := t[i:j]
		c.words[w] = append(c.words[w], i)
		i = j
	}
	return c
}

func isWord(b byte) bool {
	return b == '_' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

// isTokenChar is what may not touch an identifier's word-character or
// hyphen edge: a letter, digit, '_' or '-'.
func isTokenChar(b byte) bool { return isWord(b) || b == '-' }

// has reports whether id occurs in the corpus as a whole token: when id
// starts with a word character or '-', the byte before the match is not a
// token character; when id ends with a word character, the byte after it
// is not one either.
func (c *corpus) has(id string) bool {
	if id == "" {
		return false
	}
	// The rarest whole word run inside id, and its offset in id.
	best, bestOff := "", -1
	for i := 0; i < len(id); {
		if !isWord(id[i]) {
			i++
			continue
		}
		j := i
		for j < len(id) && isWord(id[j]) {
			j++
		}
		w := id[i:j]
		if bestOff < 0 || len(c.words[w]) < len(c.words[best]) {
			best, bestOff = w, i
		}
		i = j
	}
	if bestOff < 0 {
		return c.scan(id)
	}
	for _, p := range c.words[best] {
		if c.matchAt(id, p-bestOff) {
			return true
		}
	}
	return false
}

func (c *corpus) scan(id string) bool {
	for off := 0; ; {
		i := strings.Index(c.text[off:], id)
		if i < 0 {
			return false
		}
		if c.matchAt(id, off+i) {
			return true
		}
		off += i + 1
	}
}

func (c *corpus) matchAt(id string, s int) bool {
	t := c.text
	e := s + len(id)
	if s < 0 || e > len(t) || t[s:e] != id {
		return false
	}
	if isTokenChar(id[0]) && s > 0 && isTokenChar(t[s-1]) {
		return false
	}
	if isWord(id[len(id)-1]) && e < len(t) && isTokenChar(t[e]) {
		return false
	}
	return true
}
