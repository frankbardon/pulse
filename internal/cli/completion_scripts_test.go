package cli

import (
	"bytes"
	"context"
	"os/exec"
	"slices"
	"strings"
	"testing"

	cli "github.com/urfave/cli/v3"
)

// driveCandidates are candidate shapes the plain `name:usage` split
// breaks: a Windows drive-letter path (C:\... completed as "C" before
// escaping), a path holding a colon and one holding a backslash and a
// space. Injected strings, so the parse is pinned on every OS.
var driveCandidates = []string{
	`C:\Users\runner\data\people.pulse`,
	`D:\arch\wide-archive.pulse`,
	`/data/a:b.pulse`,
	`/data/back\slash dir/x.pulse`,
}

// TestCompletionCandidate_EscapeRoundTrip: every candidate printed
// through escapeCandidate comes back whole from the parse the scripts
// apply (UnescapeCandidate), while ordinary candidates and urfave/cli's
// own `name:usage` lines print and parse as before.
func TestCompletionCandidate_EscapeRoundTrip(t *testing.T) {
	t.Parallel()

	for _, v := range append(slices.Clone(driveCandidates), "plain", "--log-level", `trail\`, `a\:b`) {
		line := escapeCandidate(v)
		if name, usage := UnescapeCandidate(line); name != v || usage != "" {
			t.Errorf("%q printed as %q parses to (%q, %q), want (%q, \"\")", v, line, name, usage, v)
		}
	}
	for _, v := range []string{"plain", "--log-level", "/data/wide.pulse"} {
		if got := escapeCandidate(v); got != v {
			t.Errorf("escapeCandidate(%q) = %q, want it unchanged", v, got)
		}
	}
	// writeCandidates (every value completion's printer) escapes: each
	// printed line parses back to its candidate.
	var out bytes.Buffer
	writeCandidates(&cli.Command{Name: "pulse", Writer: &out}, driveCandidates)
	var got []string
	for _, line := range strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n") {
		name, _ := UnescapeCandidate(line)
		got = append(got, name)
	}
	if want := slices.Sorted(slices.Values(driveCandidates)); !slices.Equal(got, want) {
		t.Errorf("writeCandidates output parses to %q, want %q", got, want)
	}
	if name, usage := UnescapeCandidate("--mode:lookup mode: all or first"); name != "--mode" || usage != "lookup mode: all or first" {
		t.Errorf("name:usage line parsed to (%q, %q)", name, usage)
	}
}

// renderScript renders `pulse completion <shell>` through the real
// completion command.
func renderScript(t *testing.T, shell string) string {
	t.Helper()
	var out, errOut bytes.Buffer
	root := EnableCompletion(&cli.Command{Name: "pulse", Writer: &out, ErrWriter: &errOut})
	if err := root.Run(context.Background(), []string{"pulse", "completion", shell}); err != nil {
		t.Fatalf("completion %s: %v (stderr %q)", shell, err, errOut.String())
	}
	return out.String()
}

// TestCompletionScripts_Patched: the bash and fish scripts carry every
// Pulse rewrite of the candidate parse (zsh's _describe parses the
// escaping natively and is untouched), and a script that lacks a patched
// text is refused rather than shipped unescaped.
func TestCompletionScripts_Patched(t *testing.T) {
	t.Parallel()

	for _, shell := range completionShells {
		script := renderScript(t, shell)
		for _, p := range completionScriptPatches[shell] {
			if !strings.Contains(script, p.new) {
				t.Errorf("%s script does not carry the patch replacing %q", shell, p.old)
			}
		}
	}
	if !strings.Contains(renderScript(t, "zsh"), "_describe 'values' opts") {
		t.Error("zsh script no longer hands the candidates to _describe, which parses the escaping")
	}
	if _, err := patchCompletionScript("bash", "#!/bin/bash\n"); err == nil {
		t.Error("patching a script without the expected text succeeded, want an error")
	}
}

// TestCompletionScript_BashCompletesEscapedCandidates runs the shipped
// bash script against a stub binary printing escaped candidates: each
// reply, as the shell reads it back off the command line, is the whole
// candidate — the drive-letter path included — and a typed prefix up to
// a ":" (a bash word break) is not inserted twice.
func TestCompletionScript_BashCompletesEscapedCandidates(t *testing.T) {
	t.Parallel()

	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Fatalf("bash not found: %v (the completion script tests need bash on PATH)", err)
	}
	var lines strings.Builder
	for _, v := range driveCandidates {
		lines.WriteString(escapeCandidate(v) + "\n")
	}
	lines.WriteString("--log-level:the logging level\n")

	run := func(cur string) []string {
		t.Helper()
		prog := renderScript(t, "bash") + `
pulse() { cat <<'PULSE_CANDIDATES'
` + lines.String() + `PULSE_CANDIDATES
}
_comp_initialize() { :; }
COMP_WORDBREAKS=$' \t\n"'"'"'><=;|&(:'
COMP_WORDS=(pulse widen "$1")
COMP_CWORD=2
COMP_TYPE=9
__pulse_bash_autocomplete
for r in "${COMPREPLY[@]}"; do eval "set -- $r"; printf '%s\n' "$1"; done
`
		cmd := exec.Command(bash, "-s", "--", cur)
		cmd.Stdin = strings.NewReader(prog)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("bash: %v (stderr %q)", err, stderr.String())
		}
		got := strings.Split(strings.TrimSuffix(strings.ReplaceAll(string(out), "\r\n", "\n"), "\n"), "\n")
		if len(got) == 1 && got[0] == "" {
			return nil
		}
		return got
	}

	want := append(slices.Clone(driveCandidates), "--log-level")
	if got := run(""); !slices.Equal(got, want) {
		t.Errorf("empty word completes\n  %q\nwant\n  %q", got, want)
	}
	if got := run("C"); !slices.Equal(got, []string{driveCandidates[0]}) {
		t.Errorf("word C completes %q, want [%q]", got, driveCandidates[0])
	}
	// "C:" is two words to readline (":" breaks words): the reply is
	// what follows the colon.
	if got, want := run("C:"), []string{strings.TrimPrefix(driveCandidates[0], "C:")}; !slices.Equal(got, want) {
		t.Errorf("word C: completes %q, want %q", got, want)
	}
}
