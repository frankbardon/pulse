package main

import (
	"path"
	"strings"
)

// matcher maps tokens to operator indexes. Matching is by whole token,
// never substring, so MAT_PCA never matches MAT_PCA_X and TEST_T never
// matches TEST_TREND:
//
//   - words: identifier tokens [A-Za-z0-9_]+ (wire names, Go consts);
//   - stems: skill-stem tokens [A-Za-z0-9_-]+ (op-mat-pca);
//   - quoted: an identifier token immediately wrapped in double quotes
//     (synth distribution names, which are ordinary English words).
type matcher struct {
	words  map[string][]int
	stems  map[string][]int
	quoted map[string][]int
}

// hit is one occurrence of an operator's alias.
type hit struct {
	Path string
	Op   int
	Line int
	Off  int
}

func isWord(b byte) bool {
	return b == '_' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

// scan tokenizes every selected file once and returns the hits.
func (r *repo) scan(m *matcher, keep func(string) bool) []hit {
	var out []hit
	for _, p := range r.files {
		if keep != nil && !keep(p) {
			continue
		}
		src := r.read(p)
		if src == nil {
			continue
		}
		out = append(out, scanBytes(p, src, m)...)
	}
	return out
}

func scanBytes(p string, src []byte, m *matcher) []hit {
	var out []hit
	line := 1
	lineAt := 0 // offset up to which line is counted
	lineOf := func(off int) int {
		for ; lineAt < off; lineAt++ {
			if src[lineAt] == '\n' {
				line++
			}
		}
		return line
	}
	emit := func(ops []int, off int) {
		l := lineOf(off)
		for _, op := range ops {
			out = append(out, hit{Path: p, Op: op, Line: l, Off: off})
		}
	}
	n := len(src)
	if len(m.words) > 0 || len(m.quoted) > 0 {
		for i := 0; i < n; {
			if !isWord(src[i]) {
				i++
				continue
			}
			j := i
			for j < n && isWord(src[j]) {
				j++
			}
			if ops, ok := m.words[string(src[i:j])]; ok {
				emit(ops, i)
			}
			if ops, ok := m.quoted[string(src[i:j])]; ok && i > 0 && src[i-1] == '"' && j < n && src[j] == '"' {
				emit(ops, i)
			}
			i = j
		}
	}
	if len(m.stems) > 0 {
		line, lineAt = 1, 0
		isStem := func(b byte) bool { return isWord(b) || b == '-' }
		for i := 0; i < n; {
			if !isStem(src[i]) {
				i++
				continue
			}
			j := i
			for j < n && isStem(src[j]) {
				j++
			}
			if ops, ok := m.stems[string(src[i:j])]; ok {
				emit(ops, i)
			}
			i = j
		}
	}
	return out
}

// symHit is one resolved symbol in a file.
type symHit struct {
	Name        string
	Lines       []int
	CommentOnly bool
}

// resolve groups a file's hits by enclosing symbol, in first-line
// order. Go: declaration (comment-only symbols flagged); Markdown: the
// heading; other files: one anonymous group.
func (r *repo) resolve(p string, hs []hit) []symHit {
	var out []symHit
	idx := map[string]int{}
	var g *goFile
	isGo := strings.HasSuffix(p, ".go")
	if isGo {
		g = r.parse(p)
	}
	isMD := path.Ext(p) == ".md"
	src := r.read(p)
	for _, h := range hs {
		name, comment := "", false
		switch {
		case g != nil:
			name = g.symbolAt(h.Off)
			comment = g.inComment(h.Off)
		case isMD:
			name = mdHeading(src, h.Line)
		}
		i, ok := idx[name]
		if !ok {
			i = len(out)
			idx[name] = i
			out = append(out, symHit{Name: name, CommentOnly: isGo})
		}
		s := &out[i]
		if len(s.Lines) == 0 || s.Lines[len(s.Lines)-1] != h.Line {
			s.Lines = append(s.Lines, h.Line)
		}
		if !comment {
			s.CommentOnly = false
		}
	}
	return out
}
