package doccheck

import (
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/spf13/afero"
)

// LinkOptions configures CheckLinks. Paths are slash-separated and relative
// to the root of the filesystem CheckLinks reads (the repository root).
type LinkOptions struct {
	// BookRoot is the mdBook source directory (docs/src). A page under it
	// is rendered by mdBook: its links may not leave BookRoot (the
	// published site serves nothing else), a page it links to must be a
	// SUMMARY.md chapter, and its anchors follow mdBook's slug rules.
	// Every other file is read as GitHub renders it.
	BookRoot string
}

// schemeRe matches a URL scheme ("https:", "mailto:") or a
// protocol-relative "//host" prefix: an external target, out of scope.
var schemeRe = regexp.MustCompile(`^(?:[a-zA-Z][a-zA-Z0-9+.-]*:|//)`)

// IsExternal reports whether a link target carries a scheme or is
// protocol-relative.
func IsExternal(target string) bool { return schemeRe.MatchString(target) }

// CheckLinks resolves every relative link and #anchor in files (markdown,
// slash-separated, relative to fsys's root) and returns one violation per
// (file, target) that does not resolve. Existence is case-exact, whatever
// the host filesystem's case rules, because the published site is not
// case-insensitive.
func CheckLinks(fsys afero.Fs, files []string, opts LinkOptions) ([]Violation, error) {
	c, err := newLinkChecker(fsys, opts)
	if err != nil {
		return nil, err
	}
	var out []Violation
	for _, file := range files {
		doc, err := c.doc(file)
		if err != nil {
			return nil, err
		}
		for _, l := range doc.Links {
			if msg := c.resolve(file, l.Target); msg != "" {
				out = append(out, Violation{Path: file, Entry: l.Target, Message: fmt.Sprintf("line %d: %s", l.Line, msg)})
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Entry < out[j].Entry
	})
	return out, nil
}

type linkChecker struct {
	fs       afero.Fs
	opts     LinkOptions
	docs     map[string]Document
	dirs     map[string]map[string]os.FileInfo
	chapters map[string]bool
}

func newLinkChecker(fsys afero.Fs, opts LinkOptions) (*linkChecker, error) {
	c := &linkChecker{fs: fsys, opts: opts, docs: map[string]Document{}, dirs: map[string]map[string]os.FileInfo{}}
	if opts.BookRoot != "" {
		chapters, err := c.summaryChapters()
		if err != nil {
			return nil, err
		}
		c.chapters = chapters
	}
	return c, nil
}

func (c *linkChecker) inBook(p string) bool {
	return c.opts.BookRoot != "" && (p == c.opts.BookRoot || strings.HasPrefix(p, c.opts.BookRoot+"/"))
}

func (c *linkChecker) flavor(p string) Flavor {
	if c.inBook(p) {
		return MDBook
	}
	return GitHub
}

func (c *linkChecker) doc(file string) (Document, error) {
	if d, ok := c.docs[file]; ok {
		return d, nil
	}
	raw, err := afero.ReadFile(c.fs, file)
	if err != nil {
		return Document{}, fmt.Errorf("doccheck: read %s: %w", file, err)
	}
	d := ScanMarkdown(raw, c.flavor(file))
	c.docs[file] = d
	return d, nil
}

// stat looks p up case-exactly through its parent's directory listing.
func (c *linkChecker) stat(p string) (os.FileInfo, bool) {
	if p == "." || p == "" {
		return nil, true
	}
	dir, base := path.Split(p)
	dir = strings.TrimSuffix(dir, "/")
	if dir == "" {
		dir = "."
	}
	entries, ok := c.dirs[dir]
	if !ok {
		entries = map[string]os.FileInfo{}
		if infos, err := afero.ReadDir(c.fs, dir); err == nil {
			for _, fi := range infos {
				entries[fi.Name()] = fi
			}
		}
		c.dirs[dir] = entries
	}
	fi, ok := entries[base]
	return fi, ok
}

// summaryChapters lists the pages BookRoot/SUMMARY.md renders.
func (c *linkChecker) summaryChapters() (map[string]bool, error) {
	summary := path.Join(c.opts.BookRoot, "SUMMARY.md")
	d, err := c.doc(summary)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{summary: true}
	for _, l := range d.Links {
		if l.Target == "" || IsExternal(l.Target) {
			continue
		}
		p, _, _ := strings.Cut(l.Target, "#")
		if dec, err := url.PathUnescape(p); err == nil {
			p = dec
		}
		out[path.Join(c.opts.BookRoot, p)] = true
	}
	return out, nil
}

// resolve returns "" when target resolves from file, else what is wrong.
func (c *linkChecker) resolve(file, target string) string {
	if target == "" || IsExternal(target) {
		return ""
	}
	p, frag, hasFrag := strings.Cut(target, "#")
	if q := strings.IndexByte(p, '?'); q >= 0 {
		p = p[:q]
	}
	if dec, err := url.PathUnescape(p); err == nil {
		p = dec
	}
	if dec, err := url.PathUnescape(frag); err == nil {
		frag = dec
	}

	book := c.inBook(file)
	dest := file
	if p != "" {
		switch {
		case strings.HasPrefix(p, "/") && book:
			dest = path.Join(c.opts.BookRoot, p)
		case strings.HasPrefix(p, "/"):
			dest = path.Clean(strings.TrimPrefix(p, "/"))
		default:
			dest = path.Join(path.Dir(file), p)
		}
		if dest == ".." || strings.HasPrefix(dest, "../") {
			return "target is outside the repository"
		}
		if book && !c.inBook(dest) {
			return fmt.Sprintf("target %s is outside the book source %s, so the published site cannot serve it — link the GitHub URL instead", dest, c.opts.BookRoot)
		}
		fi, ok := c.stat(dest)
		if !ok && book && strings.HasSuffix(dest, ".html") {
			// mdBook rewrites chapter.md to chapter.html; README.md is
			// rendered as index.html.
			md := strings.TrimSuffix(dest, ".html") + ".md"
			if path.Base(dest) == "index.html" {
				if _, ok2 := c.stat(path.Join(path.Dir(dest), "README.md")); ok2 {
					md = path.Join(path.Dir(dest), "README.md")
				}
			}
			if fi2, ok2 := c.stat(md); ok2 {
				dest, fi, ok = md, fi2, true
			}
		}
		if !ok {
			return fmt.Sprintf("target %s does not exist (paths are case-exact)", dest)
		}
		if fi != nil && fi.IsDir() {
			if book {
				for _, idx := range []string{"README.md", "index.md"} {
					if _, ok := c.stat(path.Join(dest, idx)); ok {
						dest = path.Join(dest, idx)
						break
					}
				}
				if !strings.HasSuffix(dest, ".md") {
					return fmt.Sprintf("target %s is a directory with no README.md / index.md page", dest)
				}
			} else {
				return "" // a directory on GitHub: a listing, no anchors.
			}
		}
		if book && strings.HasSuffix(dest, ".md") && !c.chapters[dest] {
			return fmt.Sprintf("target %s is not a chapter in %s/SUMMARY.md, so mdBook does not render it", dest, c.opts.BookRoot)
		}
	}
	if !hasFrag || frag == "" || !strings.HasSuffix(dest, ".md") {
		return ""
	}
	d, err := c.doc(dest)
	if err != nil {
		return err.Error()
	}
	if !d.Anchors[frag] {
		return fmt.Sprintf("anchor #%s matches no heading or id in %s", frag, dest)
	}
	return ""
}

// BookRoot is the repo-relative mdBook source directory.
const BookRoot = "docs/src"

// DocSources lists the markdown files the link checks cover, sorted: every
// *.md under BookRoot (the generated docs/src/guide included) and every
// repo-root *.md. .claude/reference/ is not among them:
// TestReferenceDocPointersResolve owns its pointers.
func DocSources(fsys afero.Fs) ([]string, error) {
	var files []string
	err := afero.Walk(fsys, BookRoot, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.HasSuffix(p, ".md") {
			files = append(files, filepath.ToSlash(p))
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("doccheck: walk %s: %w", BookRoot, err)
	}
	root, err := afero.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("doccheck: list repo root: %w", err)
	}
	for _, fi := range root {
		if !fi.IsDir() && strings.HasSuffix(fi.Name(), ".md") {
			files = append(files, fi.Name())
		}
	}
	sort.Strings(files)
	return files, nil
}
