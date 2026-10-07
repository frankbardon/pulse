// Package returnplan is the schema-free core of response shaping
// (`Request.Return`): the path grammar, the compiled selection plan and
// its digest, and the per-node verdict a pruner or wire encoder asks
// while walking a response.
//
// It imports the standard library only (TestReturnPlan_ImportBoundary),
// so the public `types` package can carry a *Plan in an unexported
// field without growing a dependency, and both the no-execute
// descriptor layer and the engine can share it. Schema knowledge — which
// paths exist, which an instance hides, which data columns a request
// produces — lives in internal/descriptor (ResolveReturn), never here.
package returnplan

import "strings"

// Segment is one step of a path: an object or map key (Name, optionally
// a suffix-glob prefix when Glob is set) or an array step (Index, the
// `[*]` wildcard over every element).
type Segment struct {
	// Name is the JSON key, or the glob PREFIX when Glob is set (so
	// `effect_*` is Name "effect_" with Glob true; `*` alone is Name ""
	// with Glob true). Empty for an Index segment.
	Name string
	// Glob marks a suffix `*` map-key glob.
	Glob bool
	// Index marks the `[*]` array wildcard.
	Index bool
}

// Key is a concrete object / map key segment.
func Key(name string) Segment { return Segment{Name: name} }

// Elem is the concrete array-element segment.
func Elem() Segment { return Segment{Index: true} }

// String renders the segment in path syntax.
func (s Segment) String() string {
	switch {
	case s.Index:
		return "[*]"
	case s.Glob:
		return s.Name + "*"
	default:
		return s.Name
	}
}

// Path is a parsed Return path.
type Path struct {
	Segments []Segment
	// Open is set by the schema resolver when the path steps through a
	// map key or an open (`any`) value, whose keys predict cannot know:
	// at runtime such an include may match nothing (the
	// PULSE_RETURN_PATH_UNMATCHED warning). Not part of the canonical
	// form.
	Open bool
}

// String is the canonical spelling: names joined by `.`, `[*]` attached
// to the preceding segment.
func (p Path) String() string {
	var b strings.Builder
	for i, s := range p.Segments {
		if i > 0 && !s.Index {
			b.WriteByte('.')
		}
		b.WriteString(s.String())
	}
	return b.String()
}

// SyntaxError reports a malformed path. Reason is short stable prose.
type SyntaxError struct {
	Path   string
	Reason string
}

func (e *SyntaxError) Error() string {
	return "malformed return path " + `"` + e.Path + `": ` + e.Reason
}

// Parse reads one path. The grammar:
//
//	path    = name *( "." name / "[*]" )
//	name    = 1*char [ "*" ] / "*"        ; char: anything but . [ ] *
//
// A path starts with a name (the Response root is an object). `[*]` is
// the only array step — no numeric index. `*` may appear only as the
// last character of a name, where it makes that segment a prefix glob.
// Whitespace is part of a name; nothing is trimmed.
func Parse(s string) (Path, error) {
	bad := func(reason string) (Path, error) { return Path{}, &SyntaxError{Path: s, Reason: reason} }
	if s == "" {
		return bad("empty path")
	}
	var segs []Segment
	i := 0
	expectName := true
	for i < len(s) {
		if expectName {
			j := i
			for j < len(s) && s[j] != '.' && s[j] != '[' && s[j] != ']' && s[j] != '*' {
				j++
			}
			seg := Segment{Name: s[i:j]}
			if j < len(s) && s[j] == '*' {
				seg.Glob = true
				j++
			} else if seg.Name == "" {
				return bad("empty name segment")
			}
			if j < len(s) && s[j] == ']' {
				return bad("unexpected `]`")
			}
			segs = append(segs, seg)
			i = j
			expectName = false
			continue
		}
		switch {
		case strings.HasPrefix(s[i:], "[*]"):
			segs = append(segs, Segment{Index: true})
			i += 3
		case s[i] == '[':
			return bad("only `[*]` may index an array")
		case s[i] == '.':
			i++
			if i == len(s) {
				return bad("trailing `.`")
			}
			expectName = true
		default:
			return bad("unexpected `" + string(s[i]) + "`")
		}
	}
	return Path{Segments: segs}, nil
}

// segCovers reports whether pattern segment p matches every concrete
// segment pattern segment q can match.
func segCovers(p, q Segment) bool {
	switch {
	case p.Index || q.Index:
		return p.Index && q.Index
	case p.Glob:
		return strings.HasPrefix(q.Name, p.Name)
	default:
		return !q.Glob && p.Name == q.Name
	}
}

// segOverlaps reports whether pattern segments p and q can match a
// common concrete segment.
func segOverlaps(p, q Segment) bool {
	switch {
	case p.Index || q.Index:
		return p.Index && q.Index
	case p.Glob && q.Glob:
		return strings.HasPrefix(p.Name, q.Name) || strings.HasPrefix(q.Name, p.Name)
	case p.Glob:
		return strings.HasPrefix(q.Name, p.Name)
	case q.Glob:
		return strings.HasPrefix(p.Name, q.Name)
	default:
		return p.Name == q.Name
	}
}

// covers reports whether p selects everything q selects: p is no longer
// than q and each of its segments covers q's.
func covers(p, q Path) bool {
	if len(p.Segments) > len(q.Segments) {
		return false
	}
	for i, s := range p.Segments {
		if !segCovers(s, q.Segments[i]) {
			return false
		}
	}
	return true
}

// overlaps reports whether p and q agree on their common prefix: one
// may select (part of) the other's subtree.
func overlaps(p, q Path) bool {
	n := min(len(p.Segments), len(q.Segments))
	for i := 0; i < n; i++ {
		if !segOverlaps(p.Segments[i], q.Segments[i]) {
			return false
		}
	}
	return true
}

// matchPrefix reports whether the pattern matches the first
// len(pattern) segments of the concrete path c (requires
// len(pattern) <= len(c)).
func matchPrefix(pattern Path, c []Segment) bool {
	if len(pattern.Segments) > len(c) {
		return false
	}
	for i, s := range pattern.Segments {
		if !segCovers(s, c[i]) {
			return false
		}
	}
	return true
}

// isAncestor reports whether the concrete path c is a strict ancestor of
// something the (longer) pattern can match.
func isAncestor(c []Segment, pattern Path) bool {
	if len(pattern.Segments) <= len(c) {
		return false
	}
	for i, s := range c {
		if !segCovers(pattern.Segments[i], s) {
			return false
		}
	}
	return true
}

// Matches reports whether the pattern selects exactly the concrete node
// at c: same depth, every segment covering c's.
func (p Path) Matches(c []Segment) bool {
	return len(p.Segments) == len(c) && matchPrefix(p, c)
}

// Selects reports whether the pattern matches the concrete node at c or
// one of its ancestors: c lies in a subtree the pattern names.
func (p Path) Selects(c []Segment) bool {
	return matchPrefix(p, c)
}
