// Command removednames is the one-shot sweep that seeds the removed-name
// denylist: it lists the exported identifiers, operator type constants and
// error-code constants present at an old ref (default v0.39.0) but absent at
// a new one (default HEAD), and counts how often each still appears in the
// served text of the working tree.
//
//	go run ./internal/tools/removednames [-from v0.39.0] [-to HEAD] [-root .] [-out FILE]
//
// Both trees are read from git (`git ls-tree` + `git cat-file --batch`);
// the served-text counts read the working tree. The output is a markdown
// candidate list the owner prunes by hand before any denylist is committed.
// It is a development tool only (never imported, never shipped).
//
// Kinds:
//   - exported identifier: an exported package-level func, type, var or
//     const in a non-internal, non-main package at -from that the same
//     package no longer declares at -to (methods are not swept);
//   - operator constant: a string const in package types whose type name
//     ends in "Type" and whose value equals its name (AGG_COUNT);
//   - error code: a const of type Code in package errors.
//
// A candidate is flagged (pre-unchecked) when denylisting it whole-word is
// unsafe: the bare name is still exported by another package at -to, it
// still appears as a string literal in non-test Go source at -to, it is
// short, or it is a single common word.
package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/scanner"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/spf13/afero"
)

func main() {
	from := flag.String("from", "v0.39.0", "old git ref")
	to := flag.String("to", "HEAD", "new git ref")
	root := flag.String("root", ".", "repository root")
	out := flag.String("out", "", "output file (default stdout)")
	flag.Parse()

	oldTree, err := gitTree(*root, *from)
	if err != nil {
		fail(err)
	}
	newTree, err := gitTree(*root, *to)
	if err != nil {
		fail(err)
	}
	cands := Sweep(oldTree, newTree)
	absRoot, err := filepath.Abs(*root)
	if err != nil {
		fail(err)
	}
	served, err := ServedText(afero.NewBasePathFs(afero.NewOsFs(), absRoot))
	if err != nil {
		fail(err)
	}
	CountHits(cands, served)
	var migration []string
	if b, err := os.ReadFile(path.Join(*root, MigrationGuide)); err == nil {
		migration = MigrationSeeds(string(b))
	}
	seeds := ClassifySeeds(Seeds, migration, newTree)

	var w io.Writer = os.Stdout
	if *out != "" {
		f, err := os.Create(*out)
		if err != nil {
			fail(err)
		}
		defer f.Close()
		w = f
	}
	if err := Render(w, *from, *to, cands, SeedHits(seeds, served)); err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "removednames:", err)
	os.Exit(2)
}

// gitTree returns every non-test .go file at ref, keyed by repo path.
func gitTree(root, ref string) (map[string][]byte, error) {
	ls, err := exec.Command("git", "-C", root, "ls-tree", "-r", "-z", "--name-only", ref).Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-tree %s: %w", ref, err)
	}
	var paths []string
	for _, p := range strings.Split(string(ls), "\x00") {
		if strings.HasSuffix(p, ".go") && !strings.HasSuffix(p, "_test.go") {
			paths = append(paths, p)
		}
	}
	cmd := exec.Command("git", "-C", root, "cat-file", "--batch")
	var in bytes.Buffer
	for _, p := range paths {
		fmt.Fprintf(&in, "%s:%s\n", ref, p)
	}
	cmd.Stdin = &in
	raw, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git cat-file %s: %w", ref, err)
	}
	r := bufio.NewReader(bytes.NewReader(raw))
	files := make(map[string][]byte, len(paths))
	for _, p := range paths {
		hdr, err := r.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("git cat-file %s:%s: %w", ref, p, err)
		}
		f := strings.Fields(hdr)
		if len(f) != 3 {
			return nil, fmt.Errorf("git cat-file %s:%s: header %q", ref, p, hdr)
		}
		n, err := strconv.Atoi(f[2])
		if err != nil {
			return nil, err
		}
		body := make([]byte, n+1) // content + trailing LF
		if _, err := io.ReadFull(r, body); err != nil {
			return nil, err
		}
		files[p] = body[:n]
	}
	return files, nil
}

// Kind classifies a candidate.
type Kind int

const (
	KindIdent Kind = iota
	KindOperator
	KindErrorCode
)

func (k Kind) String() string {
	switch k {
	case KindOperator:
		return "operator constant"
	case KindErrorCode:
		return "error code"
	}
	return "exported identifier"
}

// Candidate is one name present at the old ref and absent at the new.
type Candidate struct {
	Name     string
	Kind     Kind
	Packages []string // old package dirs that declared it ("." = root pulse)
	Flag     string   // non-empty: why denylisting it whole-word is unsafe
	Hits     int      // whole-word hits of Name in served text
	// QualifiedHits counts `<pkg>.<Name>` hits when the bare name survives
	// elsewhere (the qualified spelling is the safer denylist token).
	QualifiedHits int
}

// decl is one exported package-level declaration.
type decl struct {
	dir  string
	name string
	kind Kind
}

// tree is the parsed view of one ref.
type tree struct {
	decls   []decl
	byName  map[string][]string // name → dirs declaring it (any package)
	literal map[string][]string // string-literal content → files holding it
	methods map[string][]string // exported method name → dirs declaring it
}

func parseTree(files map[string][]byte) tree {
	t := tree{byName: map[string][]string{}, literal: map[string][]string{}, methods: map[string][]string{}}
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	fset := token.NewFileSet()
	for _, p := range paths {
		src := files[p]
		f, err := parser.ParseFile(fset, p, src, parser.SkipObjectResolution)
		if err != nil {
			continue
		}
		dir := path.Dir(p)
		collectLiterals(src, p, t.literal)
		if f.Name.Name == "main" || strings.HasSuffix(f.Name.Name, "_test") {
			continue
		}
		for _, fd := range f.Decls {
			if fn, ok := fd.(*ast.FuncDecl); ok && fn.Recv != nil && fn.Name.IsExported() &&
				!contains(t.methods[fn.Name.Name], dir) {
				t.methods[fn.Name.Name] = append(t.methods[fn.Name.Name], dir)
			}
		}
		for _, d := range fileDecls(f, dir) {
			t.decls = append(t.decls, d)
			if !contains(t.byName[d.name], dir) {
				t.byName[d.name] = append(t.byName[d.name], dir)
			}
		}
	}
	return t
}

func fileDecls(f *ast.File, dir string) []decl {
	var out []decl
	for _, d := range f.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			if d.Recv == nil && d.Name.IsExported() {
				out = append(out, decl{dir, d.Name.Name, KindIdent})
			}
		case *ast.GenDecl:
			for _, s := range d.Specs {
				switch s := s.(type) {
				case *ast.TypeSpec:
					if s.Name.IsExported() {
						out = append(out, decl{dir, s.Name.Name, KindIdent})
					}
				case *ast.ValueSpec:
					for i, n := range s.Names {
						if n.IsExported() {
							out = append(out, decl{dir, n.Name, constKind(d.Tok, s, i, dir)})
						}
					}
				}
			}
		}
	}
	return out
}

// constKind recognises operator and error-code constants.
func constKind(tok token.Token, s *ast.ValueSpec, i int, dir string) Kind {
	if tok != token.CONST || s.Type == nil {
		return KindIdent
	}
	typ, ok := s.Type.(*ast.Ident)
	if !ok {
		return KindIdent
	}
	switch {
	case dir == "errors" && typ.Name == "Code":
		return KindErrorCode
	case dir == "types" && strings.HasSuffix(typ.Name, "Type") && i < len(s.Values):
		if lit, ok := s.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
			if v, err := strconv.Unquote(lit.Value); err == nil && v == s.Names[i].Name {
				return KindOperator
			}
		}
	}
	return KindIdent
}

func collectLiterals(src []byte, p string, into map[string][]string) {
	var s scanner.Scanner
	fset := token.NewFileSet()
	s.Init(fset.AddFile(p, -1, len(src)), src, nil, 0)
	for {
		_, tok, lit := s.Scan()
		if tok == token.EOF {
			return
		}
		if tok != token.STRING {
			continue
		}
		v, err := strconv.Unquote(lit)
		if err != nil {
			continue
		}
		for _, w := range words(v) {
			if !contains(into[w], p) {
				into[w] = append(into[w], p)
			}
		}
	}
}

// words splits s into identifier-shaped tokens.
func words(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return !isWord(r) })
}

func isWord(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

// publicDir reports whether dir held a public (non-internal) package.
func publicDir(dir string) bool {
	for _, seg := range strings.Split(dir, "/") {
		if seg == "internal" || seg == "testdata" || seg == "cmd" {
			return false
		}
	}
	return true
}

// Sweep returns the candidates removed between oldFiles and newFiles.
func Sweep(oldFiles, newFiles map[string][]byte) []*Candidate {
	oldT, newT := parseTree(oldFiles), parseTree(newFiles)
	byKey := map[string]*Candidate{}
	for _, d := range oldT.decls {
		if d.kind == KindIdent && !publicDir(d.dir) {
			continue
		}
		if d.kind != KindIdent {
			// A constant is gone only when no package declares it any more.
			if len(newT.byName[d.name]) > 0 {
				continue
			}
		} else if contains(newT.byName[d.name], d.dir) {
			continue
		}
		key := d.kind.String() + "\x00" + d.name
		c := byKey[key]
		if c == nil {
			c = &Candidate{Name: d.name, Kind: d.kind}
			byKey[key] = c
		}
		if !contains(c.Packages, d.dir) {
			c.Packages = append(c.Packages, d.dir)
		}
	}
	// Operators registered by string literal (overlays at v0.39.0 were):
	// an operator-shaped word in an old literal that no new literal or
	// declaration carries.
	for word, files := range oldT.literal {
		if !operatorName.MatchString(word) || len(newT.literal[word]) > 0 || len(newT.byName[word]) > 0 {
			continue
		}
		key := KindOperator.String() + "\x00" + word
		if byKey[key] != nil {
			continue
		}
		var dirs []string
		for _, f := range files {
			if d := path.Dir(f); !contains(dirs, d) {
				dirs = append(dirs, d)
			}
		}
		byKey[key] = &Candidate{Name: word, Kind: KindOperator, Packages: dirs}
	}
	out := make([]*Candidate, 0, len(byKey))
	for _, c := range byKey {
		sort.Strings(c.Packages)
		c.Flag = flagReason(c, newT)
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// operatorName matches a built-in operator type name.
var operatorName = regexp.MustCompile(`^(AGG|ATTR|FILTER|GROUP|WIN|TEST|FEAT|OVERLAY|REG|MAT)_[A-Z0-9]+(_[A-Z0-9]+)*$`)

func flagReason(c *Candidate, newT tree) string {
	if dirs := newT.byName[c.Name]; len(dirs) > 0 {
		return "still exported as " + qualifyList(dirs, c.Name) + "; denylist the qualified `" +
			pkgName(c.Packages[0]) + "." + c.Name + "` instead if at all"
	}
	if dirs := newT.methods[c.Name]; len(dirs) > 0 {
		return "still an exported method in " + shortList(dirs)
	}
	if files := newT.literal[c.Name]; len(files) > 0 {
		return "still appears as a string literal in " + shortList(files)
	}
	if len(c.Name) <= 4 {
		return "short name"
	}
	if singleWord(c.Name) {
		return "single common word"
	}
	return ""
}

func singleWord(name string) bool {
	for i, r := range name {
		if i > 0 && (unicode.IsUpper(r) || r == '_' || unicode.IsDigit(r)) {
			return false
		}
	}
	return true
}

func pkgName(dir string) string {
	if dir == "." {
		return "pulse"
	}
	return path.Base(dir)
}

func qualifyList(dirs []string, name string) string {
	q := make([]string, 0, len(dirs))
	for _, d := range dirs {
		if d == "." {
			d = "pulse"
		}
		q = append(q, d+"."+name)
	}
	return shortList(q)
}

func shortList(xs []string) string {
	if len(xs) > 2 {
		return strings.Join(xs[:2], ", ") + fmt.Sprintf(" (+%d)", len(xs)-2)
	}
	return strings.Join(xs, ", ")
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

// ServedText returns the served text of the working tree, keyed by path:
// markdown under docs/src (roadmap excluded by construction), the root
// README / CONTRIBUTING / SECURITY, the skill pack, the docgen prose,
// example JSON, and the string literals of the Go files that build
// toolmeta, manifest / guidance and error-fixup text. Tests are excluded.
func ServedText(fsys afero.Fs) (map[string]string, error) {
	out := map[string]string{}
	for _, f := range []string{"README.md", "CONTRIBUTING.md", "SECURITY.md"} {
		if b, err := afero.ReadFile(fsys, f); err == nil {
			out[f] = string(b)
		}
	}
	walks := []struct {
		dir string
		ext string
		lit bool
	}{
		{"docs/src", ".md", false},
		{"internal/skills", ".md", false},
		{"internal/docgen", ".md", false},
		{"internal/examples", ".json", false},
		{"internal/mcp/toolmeta", ".go", true},
		{"errors", ".go", true},
		{"internal/descriptor", ".go", true},
		{"internal/guide", ".go", true},
	}
	for _, w := range walks {
		err := afero.Walk(fsys, w.dir, func(p string, info os.FileInfo, err error) error {
			if err != nil {
				if os.IsNotExist(err) {
					return nil
				}
				return err
			}
			// Slash form, so the testdata exclusion and the map keys
			// match on every OS (afero walks in native separators).
			key := filepath.ToSlash(p)
			if info.IsDir() || !strings.HasSuffix(key, w.ext) || strings.HasSuffix(key, "_test.go") ||
				strings.Contains(key, "/testdata/") {
				return nil
			}
			b, err := afero.ReadFile(fsys, p)
			if err != nil {
				return err
			}
			if w.lit {
				out[key] = goStrings(b)
			} else {
				out[key] = string(b)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// goStrings returns the concatenated string literals of a Go source file.
func goStrings(src []byte) string {
	var s scanner.Scanner
	fset := token.NewFileSet()
	s.Init(fset.AddFile("x.go", -1, len(src)), src, nil, 0)
	var b strings.Builder
	for {
		_, tok, lit := s.Scan()
		if tok == token.EOF {
			return b.String()
		}
		if tok == token.STRING {
			if v, err := strconv.Unquote(lit); err == nil {
				b.WriteString(v)
				b.WriteByte('\n')
			}
		}
	}
}

// CountHits fills Hits (and QualifiedHits for flagged survivors).
func CountHits(cands []*Candidate, served map[string]string) {
	for _, c := range cands {
		c.Hits, c.QualifiedHits = 0, 0
		q := pkgName(c.Packages[0]) + "." + c.Name
		for _, text := range served {
			c.Hits += WholeWordCount(text, c.Name)
			if strings.HasPrefix(c.Flag, "still exported") {
				c.QualifiedHits += WholeWordCount(text, q)
			}
		}
	}
}

// WholeWordCount counts occurrences of needle in text not adjacent to an
// identifier character on either side.
func WholeWordCount(text, needle string) int {
	if needle == "" {
		return 0
	}
	n := 0
	for i := 0; ; {
		j := strings.Index(text[i:], needle)
		if j < 0 {
			return n
		}
		start, end := i+j, i+j+len(needle)
		if (start == 0 || !isWordByte(text[start-1])) && (end == len(text) || !isWordByte(text[end])) {
			n++
		}
		i = start + 1
	}
}

func isWordByte(b byte) bool {
	return b == '_' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= 0x80
}

// Seeds are the fixed denylist seeds named by the story, outside the sweep.
var Seeds = []string{"WelfordTriple", "OVERLAY_CORR_PVALUE", "MCP version 1.0.0"}

// MigrationGuide is the embedder migration guide whose removals tables seed
// the denylist.
const MigrationGuide = "docs/roadmap/v1.0.0-api-and-release/03-embedder-migration.md"

// MigrationSeeds returns the backticked names in the Old column of every
// row of an `| Old | New | Kind | … |` table whose Kind says "removed".
// A call suffix is trimmed (`io.NewConvertJob(…)` yields `io.NewConvertJob`,
// `(*Pulse).Service()` yields `Service`); only identifier-shaped tokens
// holding an upper-case letter are kept, so prose fragments and bare
// package names never seed the denylist.
func MigrationSeeds(guide string) []string {
	var out []string
	seen := map[string]bool{}
	inTable := false
	for _, line := range strings.Split(guide, "\n") {
		cols := strings.Split(line, "|")
		if len(cols) < 5 {
			inTable = false
			continue
		}
		if strings.TrimSpace(cols[1]) == "Old" {
			inTable = strings.TrimSpace(cols[2]) == "New" && strings.TrimSpace(cols[3]) == "Kind"
			continue
		}
		if !inTable || !strings.Contains(cols[3], "removed") {
			continue
		}
		parts := strings.Split(cols[1], "`")
		for i := 1; i < len(parts); i += 2 {
			tok := strings.TrimPrefix(parts[i], "(*Pulse).")
			if k := strings.IndexAny(tok, "({"); k >= 0 {
				tok = tok[:k]
			}
			if !seedToken.MatchString(tok) || !strings.ContainsAny(tok, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") || seen[tok] {
				continue
			}
			seen[tok] = true
			out = append(out, tok)
		}
	}
	return out
}

var seedToken = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)*$`)

// Seed is a denylist seed outside the sweep, with its served-text hit
// count. A flagged seed (from the migration guide only; the fixed theme
// seeds are never flagged) names something still live at the new ref.
type Seed struct {
	Name string
	Flag string
	Hits int
}

// ClassifySeeds returns the fixed seeds (always kept) followed by the
// migration-guide seeds not already listed, each flagged when it is still
// live in newFiles: a bare name still declared by any package or still in a
// Go string literal; a qualified `pkg.Name…` whose public package pkg still
// declares Name; or a short / single-word bare name.
func ClassifySeeds(fixed, migration []string, newFiles map[string][]byte) []Seed {
	newT := parseTree(newFiles)
	out := make([]Seed, 0, len(fixed)+len(migration))
	seen := map[string]bool{}
	for _, s := range fixed {
		seen[s] = true
		out = append(out, Seed{Name: s})
	}
	for _, s := range migration {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, Seed{Name: s, Flag: seedFlag(s, newT)})
	}
	return out
}

func seedFlag(s string, newT tree) string {
	parts := strings.Split(s, ".")
	if len(parts) > 1 {
		for _, d := range newT.byName[parts[1]] {
			if publicDir(d) && pkgName(d) == parts[0] {
				return "still live: " + qualifyList([]string{d}, parts[1])
			}
		}
		return ""
	}
	if dirs := newT.byName[s]; len(dirs) > 0 {
		return "still live: exported as " + qualifyList(dirs, s)
	}
	if dirs := newT.methods[s]; len(dirs) > 0 {
		return "still live: exported method in " + shortList(dirs)
	}
	if files := newT.literal[s]; len(files) > 0 {
		return "still live: string literal in " + shortList(files)
	}
	if len(s) <= 4 || singleWord(s) {
		return "short or single common word"
	}
	return ""
}

// SeedHits counts each seed whole-word in served text.
func SeedHits(seeds []Seed, served map[string]string) []Seed {
	for i := range seeds {
		seeds[i].Hits = 0
		for _, text := range served {
			seeds[i].Hits += WholeWordCount(text, seeds[i].Name)
		}
	}
	return seeds
}

// Render writes the owner-facing candidate list.
func Render(w io.Writer, from, to string, cands []*Candidate, seeds []Seed) error {
	var b strings.Builder
	fmt.Fprintf(&b, "# Removed-name denylist candidates (%s → %s)\n\n", from, to)
	b.WriteString("Owner: a checked line is kept in the denylist; uncheck a line to drop it.\n")
	b.WriteString("Add a line (same shape) to denylist a name the sweep missed.\n")
	b.WriteString("Counts are whole-word hits in today's served text; nonzero hits get baselined, not fixed, by the gate.\n\n")
	b.WriteString("Generated by `go run ./internal/tools/removednames`.\n")
	for _, k := range []Kind{KindIdent, KindOperator, KindErrorCode} {
		fmt.Fprintf(&b, "\n## %s (kept by default)\n\n", strings.ToUpper(k.String()[:1])+k.String()[1:]+"s")
		n := 0
		for _, c := range cands {
			if c.Kind == k && c.Flag == "" {
				fmt.Fprintf(&b, "- [x] %s — %s — %d\n", c.Name, where(c), c.Hits)
				n++
			}
		}
		if n == 0 {
			b.WriteString("_none_\n")
		}
	}
	b.WriteString("\n## Flagged as unsafe to denylist whole-word (dropped by default)\n\n")
	b.WriteString("### Still live in some form, or generic\n\n")
	for _, c := range cands {
		if c.Flag != "" && !strings.HasPrefix(c.Flag, "still exported") {
			fmt.Fprintf(&b, "- [ ] %s — %s — %d — %s\n", c.Name, where(c), c.Hits, c.Flag)
		}
	}
	b.WriteString("\n### Moved: the bare name is still exported by another package\n\n")
	for _, c := range cands {
		if strings.HasPrefix(c.Flag, "still exported") {
			extra := ""
			if strings.HasPrefix(c.Flag, "still exported") {
				extra = fmt.Sprintf(" (qualified hits %d)", c.QualifiedHits)
			}
			fmt.Fprintf(&b, "- [ ] %s — %s — %d%s — %s\n", c.Name, where(c), c.Hits, extra, c.Flag)
		}
	}
	b.WriteString("\n## Seeds (fixed by the story: theme examples + migration-guide removals tables)\n\n")
	for _, s := range seeds {
		if s.Flag == "" {
			fmt.Fprintf(&b, "- [x] %s — seed — %d\n", s.Name, s.Hits)
		}
	}
	b.WriteString("\n### Seeds flagged as still live (dropped by default)\n\n")
	for _, s := range seeds {
		if s.Flag != "" {
			fmt.Fprintf(&b, "- [ ] %s — seed — %d — %s\n", s.Name, s.Hits, s.Flag)
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func where(c *Candidate) string {
	if c.Kind != KindIdent {
		return pkgName(c.Packages[0]) + "/" + c.Kind.String()
	}
	ps := make([]string, len(c.Packages))
	for i, p := range c.Packages {
		ps[i] = pkgName(p)
		if p != "." && p != ps[i] {
			ps[i] = p
		}
	}
	return strings.Join(ps, ", ") + "/" + c.Kind.String()
}
