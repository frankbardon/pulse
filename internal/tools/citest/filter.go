package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"
)

// event is one test2json record (`go test -json`).
type event struct {
	Action  string
	Package string
	Test    string
	Elapsed float64
	Output  string
}

type timing struct {
	pkg, test string
	secs      float64
	failed    bool
}

// filterState turns a test2json stream into go test's non-verbose log.
type filterState struct {
	out     io.Writer
	buf     map[string]*strings.Builder // pkg + "\x00" + top-level test -> output
	order   []string                    // buffered keys, first-seen order
	timings []timing
	failed  bool
}

func topLevel(test string) string {
	if i := strings.IndexByte(test, '/'); i >= 0 {
		return test[:i]
	}
	return test
}

func (f *filterState) line(raw string) {
	var e event
	if !strings.HasPrefix(raw, "{") || json.Unmarshal([]byte(raw), &e) != nil {
		fmt.Fprintln(f.out, raw) // not test2json (e.g. a stray tool line): pass through
		return
	}
	if e.Action == "fail" {
		f.failed = true
	}
	if e.Test == "" {
		// Package-level output: result lines (ok / FAIL / no test files),
		// build output and anything printed outside a test.
		// A passing package's bare "PASS" line and the -run "no tests to
		// run" warning are dropped: go test's non-verbose log shows only
		// the package's "ok" line.
		if e.Output != "" && e.Output != "PASS\n" && e.Output != "testing: warning: no tests to run\n" {
			fmt.Fprint(f.out, e.Output)
		}
		return
	}
	key := e.Package + "\x00" + topLevel(e.Test)
	b := f.buf[key]
	if b == nil {
		b = &strings.Builder{}
		f.buf[key] = b
		f.order = append(f.order, key)
	}
	b.WriteString(e.Output)
	if e.Test != topLevel(e.Test) {
		return
	}
	switch e.Action {
	case "pass", "skip":
		if e.Action == "pass" {
			f.timings = append(f.timings, timing{pkg: e.Package, test: e.Test, secs: e.Elapsed})
		}
		delete(f.buf, key)
	case "fail":
		f.timings = append(f.timings, timing{pkg: e.Package, test: e.Test, secs: e.Elapsed, failed: true})
		fmt.Fprint(f.out, b.String())
		delete(f.buf, key)
	}
}

// flush prints the output of tests that never finished (a timeout or a
// crashed binary), so the cause stays in the log.
func (f *filterState) flush() {
	for _, key := range f.order {
		if b, ok := f.buf[key]; ok && b.Len() > 0 {
			fmt.Fprint(f.out, b.String())
		}
	}
	f.buf = map[string]*strings.Builder{}
}

// slowest renders the n slowest top-level tests as a Markdown table.
func slowest(ts []timing, n int, title string) string {
	sorted := append([]timing(nil), ts...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].secs > sorted[j].secs })
	if len(sorted) > n {
		sorted = sorted[:n]
	}
	var b strings.Builder
	fmt.Fprintf(&b, "### %s: %d slowest tests\n\n", title, len(sorted))
	if len(sorted) == 0 {
		b.WriteString("No test results.\n\n")
		return b.String()
	}
	b.WriteString("| seconds | package | test |\n|---:|---|---|\n")
	for _, t := range sorted {
		name := t.test
		if t.failed {
			name += " (FAIL)"
		}
		fmt.Fprintf(&b, "| %.2f | %s | %s |\n", t.secs, t.pkg, name)
	}
	b.WriteString("\n")
	return b.String()
}

// filterResult is what one pass over a test2json stream found.
type filterResult struct {
	timings []timing // top-level tests that passed or failed
	failed  bool     // any test or package failed (build failures included)
}

// filter copies in to raw (when non-nil) and writes the human log to out.
func filter(in io.Reader, raw, out io.Writer) filterResult {
	f := &filterState{out: out, buf: map[string]*strings.Builder{}}
	r := bufio.NewReaderSize(in, 1<<20)
	for {
		line, err := r.ReadString('\n')
		if len(line) > 0 {
			if raw != nil {
				_, _ = io.WriteString(raw, line)
			}
			f.line(strings.TrimRight(line, "\r\n"))
		}
		if err != nil {
			break
		}
	}
	f.flush()
	return filterResult{timings: f.timings, failed: f.failed}
}

// overBudget returns the top-level tests that took at least budget, slowest
// first.
func overBudget(ts []timing, budget time.Duration) []timing {
	var out []timing
	for _, t := range ts {
		if t.secs >= budget.Seconds() {
			out = append(out, t)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].secs > out[j].secs })
	return out
}

// recheckFunc re-times offenders alone and returns their new seconds keyed
// by pkg + "\x00" + test.
type recheckFunc func(off []timing) (map[string]float64, error)

// budgetReport renders the per-test budget verdict: the offenders with their
// time and the fix, then the slowest five with their margin to the budget.
// With recheck set, an offender whose isolated rerun is under the budget was
// slowed by contention, not by its own cost, and is cleared.
func budgetReport(ts []timing, budget time.Duration, recheck recheckFunc) (string, int) {
	off := overBudget(ts, budget)
	var b strings.Builder
	alone := map[string]float64{}
	if recheck != nil && len(off) > 0 {
		var err error
		if alone, err = recheck(off); err != nil {
			fmt.Fprintf(&b, "test budget: isolated rerun failed (%v); judging on the first times\n", err)
			alone = map[string]float64{}
		}
	}
	var kept []timing
	for _, t := range off {
		secs, ok := alone[t.pkg+"\x00"+t.test]
		switch {
		case !ok:
			kept = append(kept, t)
		case secs < budget.Seconds():
			fmt.Fprintf(&b, "test budget: %s %s took %.2fs, %.2fs alone: contention, not counted\n", t.pkg, t.test, t.secs, secs)
		default:
			kept = append(kept, timing{pkg: t.pkg, test: t.test, secs: secs, failed: t.failed})
		}
	}
	if len(kept) == 0 {
		fmt.Fprintf(&b, "test budget: every top-level test ran in under %s\n", budget)
	} else {
		fmt.Fprintf(&b, "test budget: %d top-level test(s) took %s or more:\n", len(kept), budget)
		for _, t := range kept {
			fmt.Fprintf(&b, "  %6.2fs  %s  %s\n", t.secs, t.pkg, t.test)
		}
		b.WriteString("move to a *_heavy_test.go file (//go:build heavy) or make it cheaper\n")
	}
	sorted := append([]timing(nil), ts...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].secs > sorted[j].secs })
	if len(sorted) > 5 {
		sorted = sorted[:5]
	}
	fmt.Fprintf(&b, "test budget: slowest %d (margin to %s):\n", len(sorted), budget)
	for _, t := range sorted {
		fmt.Fprintf(&b, "  %6.2fs  %+6.2fs  %s  %s\n", t.secs, budget.Seconds()-t.secs, t.pkg, t.test)
	}
	return b.String(), len(kept)
}

// goRecheck reruns the offenders package by package, one at a time, with
// `go test -count=1 -json -parallel 1 -run '^(A|B)$' <pkg>`.
func goRecheck(off []timing) (map[string]float64, error) {
	byPkg := map[string][]string{}
	var pkgs []string
	for _, t := range off {
		if byPkg[t.pkg] == nil {
			pkgs = append(pkgs, t.pkg)
		}
		byPkg[t.pkg] = append(byPkg[t.pkg], t.test)
	}
	out := map[string]float64{}
	for _, pkg := range pkgs {
		cmd := exec.Command("go", "test", "-count=1", "-json", "-parallel", "1", "-run", "^("+strings.Join(byPkg[pkg], "|")+")$", pkg)
		cmd.Stderr = os.Stderr
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return nil, err
		}
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		res := filter(stdout, nil, io.Discard)
		_ = cmd.Wait() // a failing rerun still reports its time; the main run already judged pass/fail
		for _, t := range res.timings {
			out[t.pkg+"\x00"+t.test] = t.secs
		}
	}
	return out, nil
}

// filterMain returns the exit status. Without -budget, or with
// -report-only, it is always 0: the CI report must not fail a job (run it
// under `set -o pipefail` so go test's status wins). With an enforced
// -budget it is 1 when a test failed or any top-level test took the budget
// or longer.
func filterMain(args []string, stdin io.Reader, stdout io.Writer) int {
	fs := flag.NewFlagSet("filter", flag.ContinueOnError)
	jsonOut := fs.String("json-out", "", "copy the raw -json stream to this file")
	title := fs.String("title", "go test", "slow-test report heading")
	budget := fs.Duration("budget", 0, "per-top-level-test time budget (0 = none)")
	reportOnly := fs.Bool("report-only", false, "print the -budget verdict but never fail on it")
	recheck := fs.Bool("recheck", false, "rerun each -budget offender alone and clear it if it is then under budget")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, "citest filter:", err)
	}
	var raw io.Writer
	if *jsonOut != "" {
		if f, err := os.Create(*jsonOut); err != nil {
			fmt.Fprintln(os.Stderr, "citest filter:", err)
		} else {
			defer f.Close()
			bw := bufio.NewWriter(f)
			defer bw.Flush()
			raw = bw
		}
	}
	res := filter(stdin, raw, stdout)
	report := slowest(res.timings, 10, *title)
	code := 0
	if *budget > 0 {
		var rc recheckFunc
		if *recheck {
			rc = goRecheck
		}
		text, n := budgetReport(res.timings, *budget, rc)
		fmt.Fprint(stdout, text)
		report += "```\n" + text + "```\n\n"
		if !*reportOnly && (n > 0 || res.failed) {
			code = 1
		}
	}
	if path := os.Getenv("GITHUB_STEP_SUMMARY"); path != "" {
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			fmt.Fprintln(os.Stderr, "citest filter: step summary:", err)
			return code
		}
		defer f.Close()
		if _, err := f.WriteString(report); err != nil {
			fmt.Fprintln(os.Stderr, "citest filter: step summary:", err)
		}
	}
	return code
}
