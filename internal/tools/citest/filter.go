package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
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
	if e.Test == "" {
		// Package-level output: result lines (ok / FAIL / no test files),
		// build output and anything printed outside a test.
		if e.Output != "" {
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

// filter copies in to raw (when non-nil), writes the human log to out and
// returns the slow-test table.
func filter(in io.Reader, raw, out io.Writer, title string) string {
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
	return slowest(f.timings, 10, title)
}

// filterMain never exits non-zero: the slow-test report must not fail a job.
func filterMain(args []string) {
	fs := flag.NewFlagSet("filter", flag.ContinueOnError)
	jsonOut := fs.String("json-out", "", "copy the raw -json stream to this file")
	title := fs.String("title", "go test", "slow-test report heading")
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
	report := filter(os.Stdin, raw, os.Stdout, *title)
	if path := os.Getenv("GITHUB_STEP_SUMMARY"); path != "" {
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			fmt.Fprintln(os.Stderr, "citest filter: step summary:", err)
			return
		}
		defer f.Close()
		if _, err := f.WriteString(report); err != nil {
			fmt.Fprintln(os.Stderr, "citest filter: step summary:", err)
		}
	}
}
