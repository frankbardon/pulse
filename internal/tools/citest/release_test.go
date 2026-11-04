package main

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// The release gate (release.yml): a fresh `make vulncheck` that the
// verify job's CI reuse (scripts/release-ci-reuse.sh) can never satisfy,
// and that every publishing job waits on. The checks read the workflow
// line by line, as the other citest workflow checks do: jobs sit at two
// spaces under `jobs:`, job keys at four.

const releaseWorkflow = ".github/workflows/release.yml"

type wfJob struct {
	name  string
	lines []string // the job's body, comments stripped, blank lines dropped
}

var jobHeaderRe = regexp.MustCompile(`^  ([A-Za-z0-9_-]+):\s*$`)

// parseJobs splits a workflow's `jobs:` block into its jobs.
func parseJobs(src string) []wfJob {
	var jobs []wfJob
	in := false
	for _, raw := range strings.Split(src, "\n") {
		line := stripComment(raw)
		if strings.TrimSpace(line) == "" {
			continue
		}
		if !strings.HasPrefix(line, " ") {
			in = strings.TrimSpace(line) == "jobs:"
			continue
		}
		if !in {
			continue
		}
		if m := jobHeaderRe.FindStringSubmatch(line); m != nil {
			jobs = append(jobs, wfJob{name: m[1]})
			continue
		}
		if len(jobs) > 0 {
			jobs[len(jobs)-1].lines = append(jobs[len(jobs)-1].lines, line)
		}
	}
	return jobs
}

// stripComment drops a `#` comment that is not inside a quoted string or
// a `${{ }}` expression (workflow expressions never contain `#`).
func stripComment(line string) string {
	var quote byte
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '#' && (i == 0 || line[i-1] == ' '):
			return strings.TrimRight(line[:i], " ")
		}
	}
	return line
}

// key returns the value of the job-level (four-space) key k and whether
// it is present.
func (j wfJob) key(k string) (string, bool) {
	for _, l := range j.lines {
		if v, ok := strings.CutPrefix(l, "    "+k+":"); ok && !strings.HasPrefix(l, "     ") {
			return strings.TrimSpace(v), true
		}
	}
	return "", false
}

// needs lists the job's `needs:` in scalar, flow-list or block-list form.
func (j wfJob) needs() []string {
	v, ok := j.key("needs")
	if !ok {
		return nil
	}
	if v != "" {
		var out []string
		for _, f := range strings.Split(strings.Trim(v, "[]"), ",") {
			if f = strings.Trim(strings.TrimSpace(f), `"'`); f != "" {
				out = append(out, f)
			}
		}
		return out
	}
	var out []string
	started := false
	for _, l := range j.lines {
		if strings.HasPrefix(l, "    needs:") {
			started = true
			continue
		}
		if !started {
			continue
		}
		item, ok := strings.CutPrefix(strings.TrimSpace(l), "- ")
		if !ok || !strings.HasPrefix(l, "     ") {
			break
		}
		out = append(out, strings.Trim(strings.TrimSpace(item), `"'`))
	}
	return out
}

// anyKey reports whether key k appears at ANY depth of the job (a job- or
// step-level `if:`, a `continue-on-error:` anywhere).
func (j wfJob) anyKey(k string) bool {
	for _, l := range j.lines {
		t := strings.TrimPrefix(strings.TrimSpace(l), "- ")
		if strings.HasPrefix(t, k+":") {
			return true
		}
	}
	return false
}

func (j wfJob) body() string { return strings.Join(j.lines, "\n") }

var writeContentsRe = regexp.MustCompile(`(?m)^      contents:\s*write\s*$`)

// releaseVulncheckProblems: the vulncheck job exists, runs `make vulncheck`
// (the single-sourced pin, both GOOS) with nothing that can skip or
// soften it, and starts independently of verify (so reuse never decides
// it); every job with `contents: write` (the publish) needs it, and every
// job that needs it and replaces the implicit success() with its own
// `if:` checks its result.
func releaseVulncheckProblems(src string) []string {
	jobs := parseJobs(src)
	var probs []string
	i := slices.IndexFunc(jobs, func(j wfJob) bool { return j.name == "vulncheck" })
	if i < 0 {
		return []string{releaseWorkflow + ": no `vulncheck` job"}
	}
	vc := jobs[i]
	if n := vc.needs(); len(n) > 0 {
		probs = append(probs, releaseWorkflow+": vulncheck needs "+strings.Join(n, ", ")+"; it must start unconditionally, not after verify's reuse decision")
	}
	if vc.anyKey("if") {
		probs = append(probs, releaseWorkflow+": vulncheck has an `if:`; nothing (CI reuse included) may skip the scan")
	}
	if vc.anyKey("continue-on-error") {
		probs = append(probs, releaseWorkflow+": vulncheck has `continue-on-error`; a finding must fail the job")
	}
	if !strings.Contains(vc.body(), "make vulncheck") {
		probs = append(probs, releaseWorkflow+": vulncheck does not run `make vulncheck` (the GOVULNCHECK_VERSION pin, GOOS linux + windows)")
	}
	publishers := 0
	for _, j := range jobs {
		needsVC := slices.Contains(j.needs(), "vulncheck")
		if writeContentsRe.MatchString(j.body()) {
			publishers++
			if !needsVC {
				probs = append(probs, releaseWorkflow+": "+j.name+" can write contents (publishes) but does not need vulncheck")
			}
		}
		if cond, ok := j.key("if"); ok && needsVC && !strings.Contains(cond, "needs.vulncheck.result == 'success'") {
			probs = append(probs, releaseWorkflow+": "+j.name+" needs vulncheck but its `if:` does not require `needs.vulncheck.result == 'success'`, so a finding would not block it")
		}
	}
	if publishers == 0 {
		probs = append(probs, releaseWorkflow+": no job with `contents: write`; cannot find the publish job")
	}
	return probs
}

// releaseFixture is the shape release.yml must keep.
const releaseFixture = `name: Release
jobs:
  verify:
    runs-on: ubuntu-latest
    outputs:
      found: ${{ steps.match.outputs.found }}
    steps:
      - run: ./scripts/release-ci-reuse.sh a b # a # comment
  ci:
    needs: verify
    if: needs.verify.outputs.found != 'true'
    uses: ./.github/workflows/ci.yml
  vulncheck:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7
      - name: govulncheck
        run: |
          make vulncheck
  dist:
    needs: [verify, ci, vulncheck]
    if: ${{ !cancelled() && needs.vulncheck.result == 'success' && (needs.verify.outputs.found == 'true' || needs.ci.result == 'success') }}
    steps:
      - run: make dist
  release:
    needs:
      - dist
      - vulncheck
    if: ${{ !cancelled() && needs.dist.result == 'success' && needs.vulncheck.result == 'success' }}
    permissions:
      contents: write
    steps:
      - run: gh release create
`

func TestReleaseVulncheckProblems_Fixture(t *testing.T) {
	t.Parallel()
	if probs := releaseVulncheckProblems(releaseFixture); len(probs) > 0 {
		t.Fatalf("good fixture: %v", probs)
	}
	for name, c := range map[string]struct{ old, new, want string }{
		"reuse-gated if":   {"  vulncheck:\n    runs-on: ubuntu-latest\n", "  vulncheck:\n    if: needs.verify.outputs.found != 'true'\n    runs-on: ubuntu-latest\n", "has an `if:`"},
		"needs verify":     {"  vulncheck:\n    runs-on: ubuntu-latest\n", "  vulncheck:\n    needs: verify\n    runs-on: ubuntu-latest\n", "vulncheck needs verify"},
		"step if":          {"      - name: govulncheck\n", "      - name: govulncheck\n        if: ${{ needs.verify.outputs.found != 'true' }}\n", "has an `if:`"},
		"soft fail":        {"      - name: govulncheck\n", "      - name: govulncheck\n        continue-on-error: true\n", "continue-on-error"},
		"unpinned":         {"          make vulncheck\n", "          go run golang.org/x/vuln/cmd/govulncheck@latest ./...\n", "does not run `make vulncheck`"},
		"no job":           {"  vulncheck:\n", "  vulnscan:\n", "no `vulncheck` job"},
		"dist ignores":     {"!cancelled() && needs.vulncheck.result == 'success' && (", "!cancelled() && (", "dist needs vulncheck but its `if:`"},
		"release ignores":  {"needs.dist.result == 'success' && needs.vulncheck.result == 'success' }}", "needs.dist.result == 'success' }}", "release needs vulncheck but its `if:`"},
		"publish unneeded": {"      - dist\n      - vulncheck\n    if: ${{ !cancelled() && needs.dist.result == 'success' && needs.vulncheck.result == 'success' }}", "      - dist\n    if: ${{ !cancelled() && needs.dist.result == 'success' }}", "release can write contents (publishes) but does not need vulncheck"},
		"no publisher":     {"      contents: write\n", "      contents: read\n", "no job with `contents: write`"},
	} {
		src := strings.Replace(releaseFixture, c.old, c.new, 1)
		if src == releaseFixture {
			t.Fatalf("%s: mutation did not apply", name)
		}
		probs := releaseVulncheckProblems(src)
		if !strings.Contains(strings.Join(probs, "\n"), c.want) {
			t.Errorf("%s: problems %q, want one containing %q", name, probs, c.want)
		}
	}
}

// TestRepoReleaseVulncheck_Gated: the committed release.yml scans afresh
// on every tag, reuse cannot skip it, and a finding blocks the publish.
func TestRepoReleaseVulncheck_Gated(t *testing.T) {
	t.Parallel()
	src, err := os.ReadFile(filepath.Join("..", "..", "..", releaseWorkflow))
	if err != nil {
		t.Fatal(err)
	}
	if probs := releaseVulncheckProblems(string(src)); len(probs) > 0 {
		t.Fatalf("release vulncheck gate broken:\n%s", strings.Join(probs, "\n"))
	}
	// The reuse script decides only whether CI re-runs; it must never
	// learn about the scan (a reuse path that mentions it is one that
	// could skip it).
	script, err := os.ReadFile(filepath.Join("..", "..", "..", "scripts", "release-ci-reuse.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(script), "vulncheck") {
		t.Fatal("scripts/release-ci-reuse.sh mentions vulncheck; reuse must not decide the release scan")
	}
}
