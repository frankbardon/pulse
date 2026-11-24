// Package doccheck holds the shared machinery behind the documentation
// checks: a shrink-only baseline allowlist every check reads, and the
// markdown scanning (links, heading anchors) the link checker uses.
//
// The allowlist contract: each check reads one committed allowlist file
// (see AllowlistFile). An entry names a known violation by the file it sits
// in (path) and what is wrong there (entry), and says why it is tolerated
// (reason). A check reconciles the violations it finds against the list
// with Allowlist.Reconcile: a violation the list does not name is NEW and
// fails; an entry that no longer matches any violation is STALE and fails
// too, so an allowlist only ever shrinks.
package doccheck

import (
	"bufio"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"

	"github.com/spf13/afero"
)

// AllowlistDir is the repo-relative directory holding one allowlist file per
// documentation check.
const AllowlistDir = "internal/doccheck/allowlists"

// AllowlistFile is the repo-relative path of the named check's allowlist.
func AllowlistFile(check string) string {
	return path.Join(AllowlistDir, check+".txt")
}

// Violation is one problem a check found: the file it is in, the offending
// item (a link target, a flag name, an identifier — whatever the check keys
// on) and a human message saying what is wrong and how to fix it.
type Violation struct {
	Path    string
	Entry   string
	Message string
}

// Key is the allowlist key the violation matches on.
func (v Violation) Key() Key { return Key{Path: v.Path, Entry: v.Entry} }

// Key identifies an allowlisted violation: (path, entry).
type Key struct {
	Path  string
	Entry string
}

// Entry is one allowlist line.
type Entry struct {
	Key
	Reason string
	Line   int
}

// Allowlist is one check's parsed allowlist file.
type Allowlist struct {
	// File is where the list was read from, for messages.
	File    string
	entries []Entry
	byKey   map[Key]Entry
}

// Entries returns the entries in file order.
func (a *Allowlist) Entries() []Entry { return append([]Entry(nil), a.entries...) }

// Load reads and parses an allowlist file from fsys. A missing file is an
// error: every check commits its list, even an empty one.
func Load(fsys afero.Fs, file string) (*Allowlist, error) {
	f, err := fsys.Open(file)
	if err != nil {
		return nil, fmt.Errorf("doccheck: open allowlist: %w", err)
	}
	defer f.Close()
	return Parse(file, f)
}

// Parse reads an allowlist. The format is line-oriented: blank lines and
// lines starting with '#' are ignored; every other line is three
// TAB-separated fields, path, entry and reason, none of them empty. A
// duplicate (path, entry) is an error.
func Parse(file string, r io.Reader) (*Allowlist, error) {
	a := &Allowlist{File: file, byKey: map[Key]Entry{}}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	n := 0
	for sc.Scan() {
		n++
		line := strings.TrimRight(sc.Text(), "\r")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) != 3 {
			return nil, fmt.Errorf("%s:%d: want 3 TAB-separated fields (path, entry, reason), got %d", file, n, len(parts))
		}
		e := Entry{Key: Key{Path: strings.TrimSpace(parts[0]), Entry: strings.TrimSpace(parts[1])}, Reason: strings.TrimSpace(parts[2]), Line: n}
		switch {
		case e.Path == "":
			return nil, fmt.Errorf("%s:%d: empty path", file, n)
		case e.Entry == "":
			return nil, fmt.Errorf("%s:%d: empty entry", file, n)
		case e.Reason == "":
			return nil, fmt.Errorf("%s:%d: empty reason — every allowlisted violation says why it is tolerated", file, n)
		}
		if prev, dup := a.byKey[e.Key]; dup {
			return nil, fmt.Errorf("%s:%d: duplicate entry %s %q (first at line %d)", file, n, e.Path, e.Entry, prev.Line)
		}
		a.byKey[e.Key] = e
		a.entries = append(a.entries, e)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	return a, nil
}

// Result is the outcome of reconciling violations against an allowlist.
type Result struct {
	// New are violations the allowlist does not name, sorted by key.
	New []Violation
	// Stale are allowlist entries no violation matches, in file order.
	Stale []Entry
	// Allowed counts the violations the allowlist covered.
	Allowed int
}

// OK reports whether the check passes: nothing new, nothing stale.
func (r Result) OK() bool { return len(r.New) == 0 && len(r.Stale) == 0 }

// Reconcile splits violations into new and allowlisted, and reports every
// entry no violation matched as stale. Several violations sharing one key
// (the same broken link twice in a file) match one entry.
func (a *Allowlist) Reconcile(vs []Violation) Result {
	var res Result
	seen := map[Key]bool{}
	reported := map[Key]bool{}
	for _, v := range vs {
		k := v.Key()
		if _, ok := a.byKey[k]; ok {
			if !seen[k] {
				res.Allowed++
			}
			seen[k] = true
			continue
		}
		if reported[k] {
			continue
		}
		reported[k] = true
		res.New = append(res.New, v)
	}
	sort.SliceStable(res.New, func(i, j int) bool {
		if res.New[i].Path != res.New[j].Path {
			return res.New[i].Path < res.New[j].Path
		}
		return res.New[i].Entry < res.New[j].Entry
	})
	for _, e := range a.entries {
		if !seen[e.Key] {
			res.Stale = append(res.Stale, e)
		}
	}
	return res
}

// Problems renders the result as one message per failure, ready for
// t.Error: each new violation with its fix, each stale entry with the
// instruction to delete it.
func (r Result) Problems(file string) []string {
	var out []string
	for _, v := range r.New {
		out = append(out, fmt.Sprintf("%s: %s — %s (fix it; never add new violations to %s)", v.Path, v.Entry, v.Message, file))
	}
	for _, e := range r.Stale {
		out = append(out, fmt.Sprintf("%s:%d: stale allowlist entry %s %q no longer violates — delete the line (allowlists only shrink)", file, e.Line, e.Path, e.Entry))
	}
	return out
}

// Format renders violations as allowlist lines carrying reason, sorted and
// de-duplicated by key. It seeds a new check's baseline; a check never
// regenerates its list after that.
func Format(vs []Violation, reason string) string {
	seen := map[Key]bool{}
	var keys []Key
	for _, v := range vs {
		if !seen[v.Key()] {
			seen[v.Key()] = true
			keys = append(keys, v.Key())
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Path != keys[j].Path {
			return keys[i].Path < keys[j].Path
		}
		return keys[i].Entry < keys[j].Entry
	})
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "%s\t%s\t%s\n", k.Path, k.Entry, reason)
	}
	return b.String()
}
