package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"go/build/constraint"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	shardFile    = ".github/heavy-shards.txt"
	ciWorkflow   = ".github/workflows/ci.yml"
	catchAll     = "catchall"
	heavyTag     = "heavy"
	shardMatrixK = "shard"
)

// testSet maps a package path as go test spells it ("." or "./internal/x")
// to its top-level Test function names.
type testSet map[string]map[string]bool

func (s testSet) add(pkg, name string) {
	if s[pkg] == nil {
		s[pkg] = map[string]bool{}
	}
	s[pkg][name] = true
}

// tree is what a scan of the module's test files finds.
type tree struct {
	heavy testSet // tests compiled only with -tags heavy
	all   testSet // every test compiled with -tags heavy (both tiers)
}

var (
	testFuncRe  = regexp.MustCompile(`(?m)^func (Test\w*)\(`)
	otherFuncRe = regexp.MustCompile(`(?m)^func ((?:Example|Fuzz)\w*)\(`)
	matrixRe    = regexp.MustCompile(`(?m)^\s*` + shardMatrixK + `:\s*\[([^\]]*)\]`)
	shardIDRe   = regexp.MustCompile(`^[0-9]+$`)
)

// isTestName mirrors go test: "Test" followed by nothing or a non-lowercase rune.
func isTestName(name, prefix string) bool {
	rest := strings.TrimPrefix(name, prefix)
	if rest == "" {
		return true
	}
	r, _ := utf8.DecodeRuneInString(rest)
	return !unicode.IsLower(r)
}

// buildExpr returns the file's //go:build expression, or nil when it has none.
func buildExpr(src []byte) (constraint.Expr, error) {
	for _, line := range bytes.Split(src, []byte("\n")) {
		l := strings.TrimSpace(string(line))
		if strings.HasPrefix(l, "package ") {
			break
		}
		if constraint.IsGoBuild(l) {
			return constraint.Parse(l)
		}
	}
	return nil, nil
}

// tagged evaluates a build expression the way the CI runner (linux/amd64,
// gc, no race) builds it, with or without the heavy tag.
func tagged(x constraint.Expr, heavy bool) bool {
	if x == nil {
		return true
	}
	return x.Eval(func(tag string) bool {
		switch tag {
		case heavyTag:
			return heavy
		case "gc", "linux", "amd64", "unix":
			return true
		}
		return strings.HasPrefix(tag, "go1.")
	})
}

// scan walks the root module's test files (see walkTestFiles).
func scan(root string) (tree, error) {
	t := tree{heavy: testSet{}, all: testSet{}}
	err := walkTestFiles(root, func(path string) error {
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		x, err := buildExpr(src)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		withTag, withoutTag := tagged(x, true), tagged(x, false)
		if !withTag {
			return nil
		}
		isHeavy := !withoutTag
		if isHeavy {
			if m := otherFuncRe.FindSubmatch(src); m != nil {
				return fmt.Errorf("%s: %s in a heavy file cannot be sharded by -run; make it a Test", path, m[1])
			}
		}
		rel, err := filepath.Rel(root, filepath.Dir(path))
		if err != nil {
			return err
		}
		pkg := "."
		if rel != "." {
			pkg = "./" + filepath.ToSlash(rel)
		}
		for _, m := range testFuncRe.FindAllSubmatch(src, -1) {
			name := string(m[1])
			if !isTestName(name, "Test") {
				continue
			}
			t.all.add(pkg, name)
			if isHeavy {
				t.heavy.add(pkg, name)
			}
		}
		return nil
	})
	return t, err
}

type entry struct {
	shard, pkg, name string
	line             int
}

func parseShards(r io.Reader) ([]entry, error) {
	var out []entry
	sc := bufio.NewScanner(r)
	n := 0
	for sc.Scan() {
		n++
		line := sc.Text()
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		if len(f) != 3 {
			return nil, fmt.Errorf("%s:%d: want `<shard> <package> <Test>`, got %q", shardFile, n, sc.Text())
		}
		if !shardIDRe.MatchString(f[0]) {
			return nil, fmt.Errorf("%s:%d: shard id %q must be a number (%q is implicit)", shardFile, n, f[0], catchAll)
		}
		out = append(out, entry{shard: f[0], pkg: f[1], name: f[2], line: n})
	}
	return out, sc.Err()
}

func parseMatrix(src []byte) ([]string, error) {
	m := matrixRe.FindSubmatch(src)
	if m == nil {
		return nil, fmt.Errorf("%s: no `%s: [...]` matrix line", ciWorkflow, shardMatrixK)
	}
	var ids []string
	for _, f := range strings.Split(string(m[1]), ",") {
		f = strings.Trim(strings.TrimSpace(f), `"'`)
		if f != "" {
			ids = append(ids, f)
		}
	}
	return ids, nil
}

// plans maps shard id -> owned tests. catchall owns every heavy test the
// list does not name.
func plans(t tree, entries []entry) map[string]testSet {
	p := map[string]testSet{catchAll: {}}
	listed := testSet{}
	for _, e := range entries {
		if p[e.shard] == nil {
			p[e.shard] = testSet{}
		}
		p[e.shard].add(e.pkg, e.name)
		listed.add(e.pkg, e.name)
	}
	for pkg, names := range t.heavy {
		for name := range names {
			if !listed[pkg][name] {
				p[catchAll].add(pkg, name)
			}
		}
	}
	return p
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// pattern is a -run argument selecting every name in names, anchored.
func pattern(names map[string]bool) string {
	return "^(" + strings.Join(sortedKeys(names), "|") + ")$"
}

// groups partitions a shard's packages so that, within a group, the -run
// pattern built from the group's owned names selects exactly those tests: a
// package joins the first group where none of its tests shares a name with a
// test another member owns. Usually one group; a name that is heavy in one
// package and fast (or owned by another shard) in another forces a split.
func groups(owned testSet, all testSet) [][]string {
	type group struct {
		pkgs  []string
		names map[string]bool // owned names
		every map[string]bool // every test name in the member packages
	}
	var gs []*group
	for _, pkg := range sortedKeys(owned) {
		placed := false
		for _, g := range gs {
			clash := false
			for n := range all[pkg] {
				if g.names[n] && !owned[pkg][n] {
					clash = true
					break
				}
			}
			for n := range owned[pkg] {
				if g.every[n] {
					clash = true
					break
				}
			}
			if clash {
				continue
			}
			g.pkgs = append(g.pkgs, pkg)
			for n := range owned[pkg] {
				g.names[n] = true
			}
			for n := range all[pkg] {
				g.every[n] = true
			}
			placed = true
			break
		}
		if !placed {
			g := &group{pkgs: []string{pkg}, names: map[string]bool{}, every: map[string]bool{}}
			for n := range owned[pkg] {
				g.names[n] = true
			}
			for n := range all[pkg] {
				g.every[n] = true
			}
			gs = append(gs, g)
		}
	}
	out := make([][]string, len(gs))
	for i, g := range gs {
		out[i] = g.pkgs
	}
	return out
}

// validate returns every problem with the list: stale or duplicate entries,
// and shard ids missing from (or extra in) the matrix.
func validate(t tree, entries []entry, matrix []string) []string {
	var probs []string
	seen := map[string]int{}
	for _, e := range entries {
		key := e.pkg + " " + e.name
		if !t.heavy[e.pkg][e.name] {
			probs = append(probs, fmt.Sprintf("%s:%d: %s %s is not a heavy test (stale entry)", shardFile, e.line, e.pkg, e.name))
		}
		if prev, ok := seen[key]; ok {
			probs = append(probs, fmt.Sprintf("%s:%d: %s %s already listed on line %d", shardFile, e.line, e.pkg, e.name, prev))
		}
		seen[key] = e.line
	}
	inMatrix := map[string]bool{}
	for _, id := range matrix {
		inMatrix[id] = true
	}
	p := plans(t, entries)
	for _, id := range sortedKeys(p) {
		if !inMatrix[id] {
			probs = append(probs, fmt.Sprintf("shard %s is not in the %s test-heavy matrix", id, ciWorkflow))
		}
	}
	for _, id := range matrix {
		if p[id] == nil {
			probs = append(probs, fmt.Sprintf("matrix shard %s has no entries in %s", id, shardFile))
		}
	}
	return probs
}

func load(root string) (tree, []entry, []string, error) {
	t, err := scan(root)
	if err != nil {
		return t, nil, nil, err
	}
	f, err := os.Open(filepath.Join(root, shardFile))
	if err != nil {
		return t, nil, nil, err
	}
	defer f.Close()
	entries, err := parseShards(f)
	if err != nil {
		return t, nil, nil, err
	}
	wf, err := os.ReadFile(filepath.Join(root, ciWorkflow))
	if err != nil {
		return t, nil, nil, err
	}
	matrix, err := parseMatrix(wf)
	return t, entries, matrix, err
}

func heavyCheck(root string) error {
	t, entries, matrix, err := load(root)
	if err != nil {
		return err
	}
	if probs := validate(t, entries, matrix); len(probs) > 0 {
		return errors.New("heavy shard list invalid:\n  " + strings.Join(probs, "\n  "))
	}
	p := plans(t, entries)
	for _, id := range sortedKeys(p) {
		n := 0
		for _, set := range p[id] {
			n += len(set)
		}
		fmt.Printf("heavy shard %s: %d tests in %d packages\n", id, n, len(p[id]))
	}
	return nil
}

// heavyList prints every heavy test with its shard, one per line.
func heavyList(root string, w io.Writer) error {
	t, entries, _, err := load(root)
	if err != nil {
		return err
	}
	p := plans(t, entries)
	for _, id := range sortedKeys(p) {
		for _, pkg := range sortedKeys(p[id]) {
			for _, n := range sortedKeys(p[id][pkg]) {
				fmt.Fprintf(w, "%s %s %s\n", id, pkg, n)
			}
		}
	}
	return nil
}

// shardArgs is the go test invocations (argument lists) for one shard, run
// in order; none when the shard owns no test.
func shardArgs(t tree, entries []entry, shard string, flags []string) ([][]string, error) {
	p := plans(t, entries)
	owned, ok := p[shard]
	if !ok {
		return nil, fmt.Errorf("unknown shard %q", shard)
	}
	var out [][]string
	for _, pkgs := range groups(owned, t.all) {
		names := map[string]bool{}
		for _, pkg := range pkgs {
			for n := range owned[pkg] {
				names[n] = true
			}
		}
		args := []string{"test", "-tags", heavyTag, "-run", pattern(names)}
		args = append(args, flags...)
		out = append(out, append(args, pkgs...))
	}
	return out, nil
}

func heavyRun(root, shard string, flags []string) (int, error) {
	t, entries, matrix, err := load(root)
	if err != nil {
		return 1, err
	}
	if probs := validate(t, entries, matrix); len(probs) > 0 {
		return 1, errors.New("heavy shard list invalid:\n  " + strings.Join(probs, "\n  "))
	}
	runs, err := shardArgs(t, entries, shard, flags)
	if err != nil {
		return 1, err
	}
	if len(runs) == 0 {
		fmt.Fprintf(os.Stderr, "heavy shard %s: no tests\n", shard)
		return 0, nil
	}
	code := 0
	for _, args := range runs {
		fmt.Fprintf(os.Stderr, "heavy shard %s: go %s\n", shard, strings.Join(args, " "))
		cmd := exec.Command("go", args...)
		cmd.Dir = root
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			var ee *exec.ExitError
			if !errors.As(err, &ee) {
				return 1, err
			}
			if code == 0 {
				code = ee.ExitCode()
			}
		}
	}
	return code, nil
}
