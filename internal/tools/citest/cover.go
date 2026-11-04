package main

import (
	"bufio"
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/internal/tools/citest/coverfloors"
)

// pkgCover is one package's statement totals in a (merged) profile.
type pkgCover struct {
	stmts, covered int
}

func (c pkgCover) percent() float64 {
	if c.stmts == 0 {
		return 100
	}
	return 100 * float64(c.covered) / float64(c.stmts)
}

// coverBlock is one profile block (`file:start,end`): its statement count
// and whether any listing of it was executed.
type coverBlock struct {
	stmts   int
	covered bool
}

// mergeProfiles reads one or more `go test -coverprofile` files into one
// block set keyed by `file:startLine.startCol,endLine.endCol`. A block
// listed more than once (every test binary built with -coverpkg reports
// every listed package, and profiles of several runs or jobs are
// concatenated) counts once, covered if any listing has a non-zero count.
func mergeProfiles(names []string, rs []io.Reader) (map[string]*coverBlock, error) {
	blocks := map[string]*coverBlock{}
	for i, r := range rs {
		name := names[i]
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
		n := 0
		sawMode := false
		for sc.Scan() {
			n++
			line := sc.Bytes()
			if bytes.HasPrefix(line, []byte("mode: ")) {
				// Concatenated profiles repeat the header.
				sawMode = true
				continue
			}
			if !sawMode {
				return nil, fmt.Errorf("%s:%d: coverage profile must start with a `mode:` line", name, n)
			}
			if len(bytes.TrimSpace(line)) == 0 {
				continue
			}
			// file:startLine.startCol,endLine.endCol numStmts count
			f := bytes.Fields(line)
			if len(f) != 3 || !bytes.Contains(f[0], []byte(":")) || !bytes.Contains(f[0], []byte(",")) {
				return nil, fmt.Errorf("%s:%d: malformed profile line %q", name, n, line)
			}
			stmts, err1 := strconv.Atoi(string(f[1]))
			count, err2 := strconv.ParseInt(string(f[2]), 10, 64)
			if err1 != nil || err2 != nil || stmts < 0 || count < 0 {
				return nil, fmt.Errorf("%s:%d: malformed profile line %q", name, n, line)
			}
			b := blocks[string(f[0])]
			if b == nil {
				b = &coverBlock{stmts: stmts}
				blocks[string(f[0])] = b
			}
			if count > 0 {
				b.covered = true
			}
		}
		if err := sc.Err(); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		if !sawMode {
			return nil, fmt.Errorf("%s: empty coverage profile (no `mode:` line)", name)
		}
	}
	return blocks, nil
}

// summarize returns per-package statement coverage keyed by import path.
func summarize(blocks map[string]*coverBlock) map[string]pkgCover {
	out := map[string]pkgCover{}
	for key, b := range blocks {
		file := key[:strings.LastIndexByte(key, ':')]
		pkg := path.Dir(file)
		c := out[pkg]
		c.stmts += b.stmts
		if b.covered {
			c.covered += b.stmts
		}
		out[pkg] = c
	}
	return out
}

// parseProfiles is mergeProfiles + summarize.
func parseProfiles(names []string, rs []io.Reader) (map[string]pkgCover, error) {
	blocks, err := mergeProfiles(names, rs)
	if err != nil {
		return nil, err
	}
	return summarize(blocks), nil
}

// writeMerged writes blocks as one `mode: set` profile, one sorted line
// per block. A -coverpkg profile of the whole tree repeats every block
// once per test binary (~500 MB for the fast tier); merged it is a
// few MB, small enough to hand between CI jobs, and still a valid input
// to cover-check and `go tool cover`.
func writeMerged(w io.Writer, blocks map[string]*coverBlock) error {
	keys := make([]string, 0, len(blocks))
	for k := range blocks {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	bw := bufio.NewWriter(w)
	bw.WriteString("mode: set\n")
	for _, k := range keys {
		c := 0
		if blocks[k].covered {
			c = 1
		}
		fmt.Fprintf(bw, "%s %d %d\n", k, blocks[k].stmts, c)
	}
	return bw.Flush()
}

// mergeProfileFiles merges the named profiles into dst (writeMerged).
// A missing input (its go test failed before writing) is skipped; with no
// input at all dst is a header-only profile, so a job always has one.
// removeInputs deletes the inputs once merged.
func mergeProfileFiles(dst string, inputs []string, removeInputs bool) error {
	var names []string
	var rs []io.Reader
	var files []*os.File
	// Every input is closed before any is removed: Windows refuses to
	// delete a file that is still open.
	closeAll := func() {
		for _, f := range files {
			f.Close()
		}
		files = nil
	}
	defer closeAll()
	for _, p := range inputs {
		f, err := os.Open(p)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		files = append(files, f)
		names = append(names, p)
		rs = append(rs, f)
	}
	blocks, err := mergeProfiles(names, rs)
	closeAll()
	if err != nil {
		return err
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if err := writeMerged(out, blocks); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	if removeInputs {
		for _, p := range names {
			_ = os.Remove(p)
		}
	}
	return nil
}

// coverMergeMain implements `citest cover-merge -o OUT IN...`.
func coverMergeMain(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("cover-merge", flag.ContinueOnError)
	fs.SetOutput(stderr)
	out := fs.String("o", "", "merged profile to write")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *out == "" || fs.NArg() == 0 {
		fmt.Fprintln(stderr, "usage: citest cover-merge -o OUT IN...")
		return 2
	}
	for _, p := range fs.Args() {
		if _, err := os.Stat(p); err != nil {
			fmt.Fprintln(stderr, "citest cover-merge:", err)
			return 2
		}
	}
	if err := mergeProfileFiles(*out, fs.Args(), false); err != nil {
		fmt.Fprintln(stderr, "citest cover-merge:", err)
		return 2
	}
	return 0
}

// coverRow is one line of the cover-check report.
type coverRow struct {
	dir     string
	floor   float64
	got     float64
	present bool
}

func (r coverRow) status() string {
	switch {
	case !r.present:
		return "MISSING"
	case r.got < r.floor:
		return "BELOW"
	default:
		return "ok"
	}
}

// coverCheck compares measured coverage against the floors. It returns the
// report rows in table order and the number of failing rows (below the
// floor, or absent from the profile — a package the profile never
// instrumented cannot be shown to meet its floor).
func coverCheck(module string, floors []coverfloors.Floor, got map[string]pkgCover) ([]coverRow, int) {
	rows := make([]coverRow, 0, len(floors))
	fails := 0
	for _, f := range floors {
		imp := module
		if f.Dir != "." {
			imp = module + "/" + f.Dir
		}
		c, ok := got[imp]
		r := coverRow{dir: f.Dir, floor: f.Percent, present: ok}
		if ok {
			r.got = c.percent()
		}
		if r.status() != "ok" {
			fails++
		}
		rows = append(rows, r)
	}
	return rows, fails
}

func formatCoverTable(rows []coverRow, fails int) string {
	var b strings.Builder
	w := len("package")
	for _, r := range rows {
		w = max(w, len(r.dir))
	}
	fmt.Fprintf(&b, "%-*s  %6s  %8s  %s\n", w, "package", "floor", "coverage", "status")
	for _, r := range rows {
		got := "-"
		if r.present {
			got = fmt.Sprintf("%.2f%%", r.got)
		}
		fmt.Fprintf(&b, "%-*s  %5.4g%%  %8s  %s\n", w, r.dir, r.floor, got, r.status())
	}
	if fails > 0 {
		fmt.Fprintf(&b, "cover-check: %d of %d package(s) below floor or missing from the profile\n", fails, len(rows))
	} else {
		fmt.Fprintf(&b, "cover-check: all %d package(s) at or above floor\n", len(rows))
	}
	return b.String()
}

func formatCoverSummary(rows []coverRow, fails int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "### coverage floors (%d package(s), %d failing)\n\n", len(rows), fails)
	b.WriteString("| package | floor | coverage | status |\n|---|---:|---:|---|\n")
	for _, r := range rows {
		got := "-"
		if r.present {
			got = fmt.Sprintf("%.2f%%", r.got)
		}
		fmt.Fprintf(&b, "| `%s` | %.4g%% | %s | %s |\n", r.dir, r.floor, got, r.status())
	}
	return b.String()
}

// modulePath reads the module line of root/go.mod.
func modulePath(root string) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(data), "\n") {
		if f := strings.Fields(line); len(f) == 2 && f[0] == "module" {
			return strings.Trim(f[1], `"`), nil
		}
	}
	return "", errors.New("go.mod: no module line")
}

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(s string) error { *m = append(*m, s); return nil }

// coverCheckMain implements `citest cover-check [-root DIR] [-floors FILE]
// -profile FILE [-profile FILE ...] [FILE ...]`: exit 0 when every floor
// package meets its floor, 1 on a miss, 2 on a usage, table or profile
// error.
func coverCheckMain(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("cover-check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "module root")
	floorsPath := fs.String("floors", "", "floor table (default <root>/"+coverfloors.File+")")
	var profiles multiFlag
	fs.Var(&profiles, "profile", "coverage profile (repeatable; trailing args are profiles too)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	profiles = append(profiles, fs.Args()...)
	if len(profiles) == 0 {
		fmt.Fprintln(stderr, "citest cover-check: no -profile given")
		return 2
	}
	floors, err := loadFloors(*root, *floorsPath)
	if err != nil {
		fmt.Fprintln(stderr, "citest cover-check:", err)
		return 2
	}
	module, err := modulePath(*root)
	if err != nil {
		fmt.Fprintln(stderr, "citest cover-check:", err)
		return 2
	}
	var rs []io.Reader
	for _, p := range profiles {
		f, err := os.Open(p)
		if err != nil {
			fmt.Fprintln(stderr, "citest cover-check:", err)
			return 2
		}
		defer f.Close()
		rs = append(rs, f)
	}
	got, err := parseProfiles(profiles, rs)
	if err != nil {
		fmt.Fprintln(stderr, "citest cover-check:", err)
		return 2
	}
	rows, fails := coverCheck(module, floors, got)
	fmt.Fprint(stdout, formatCoverTable(rows, fails))
	if p := os.Getenv("GITHUB_STEP_SUMMARY"); p != "" {
		if f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
			fmt.Fprint(f, formatCoverSummary(rows, fails))
			f.Close()
		}
	}
	if fails > 0 {
		return 1
	}
	return 0
}
