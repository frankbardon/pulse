package main

// SHELL COMPLETION (U37).
//
// `pulse completion bash|zsh|fish` prints a script; the script re-invokes
// the binary with the words typed so far plus --generate-shell-completion
// and reads the candidates from stdout. These tests drive that protocol
// in-process through run(), the same entry point main uses.
//
// The tree walk (walkCompletionTree / completionTree) is shared with
// cliLeaves and is the hook a per-flag value-completion gate extends:
// TestCompletion_TreeCoverage visits every leaf and every flag of it.

import (
	"bytes"
	"context"
	"io"
	"os"
	"slices"
	"strings"
	"testing"

	cli "github.com/urfave/cli/v3"
)

const completionMarker = "--generate-shell-completion"

// completionTree returns buildApp()'s root after urfave/cli has set it up
// (one --help run), so the built-in `completion` command and its shell
// leaves are mounted exactly as the binary mounts them.
func completionTree() *cli.Command {
	app := buildApp()
	app.Writer, app.ErrWriter = io.Discard, io.Discard
	_ = app.Run(context.Background(), []string{"pulse", "--help"})
	return app
}

// isHelpCommand reports whether c is urfave/cli's auto-added `help`
// command (mounted under every command at setup). It is framework
// furniture, not a Pulse leaf, so the walks skip it.
func isHelpCommand(c *cli.Command) bool { return c.Name == "help" }

// walkCompletionTree calls fn for every command under root (root
// excluded), depth first, with its invocation path ("pulse" first) and
// its parent. urfave's help commands are skipped.
func walkCompletionTree(root *cli.Command, fn func(path []string, parent, c *cli.Command)) {
	var walk func(path []string, parent *cli.Command)
	walk = func(path []string, parent *cli.Command) {
		for _, c := range parent.Commands {
			if isHelpCommand(c) {
				continue
			}
			p := append(slices.Clone(path), c.Name)
			fn(p, parent, c)
			walk(p, c)
		}
	}
	walk([]string{root.Name}, root)
}

// completeWords runs one completion request for words (program name
// first, the marker appended) and returns stdout, stderr and the exit
// status. The root's default completer reads os.Args, as it does in a
// real shell, so os.Args is set for the call.
func completeWords(t *testing.T, words ...string) (string, string, int) {
	t.Helper()
	args := append(slices.Clone(words), completionMarker)
	saved := os.Args
	os.Args = args
	defer func() { os.Args = saved }()
	var stdout, stderr bytes.Buffer
	status := run(context.Background(), args, &stdout, &stderr)
	return stdout.String(), stderr.String(), status
}

// candidates parses completion output ("name" or "name:usage" lines)
// into the candidate names.
func candidates(out string) []string {
	var names []string
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		name, _, _ := strings.Cut(line, ":")
		names = append(names, name)
	}
	return names
}

// flagCandidate is how the completer spells a flag: "--name", or "-n"
// for a one-letter primary name.
func flagCandidate(f cli.Flag) string {
	name := f.Names()[0]
	if len([]rune(name)) == 1 {
		return "-" + name
	}
	return "--" + name
}

func flagVisible(f cli.Flag) bool {
	vf, ok := f.(cli.VisibleFlag)
	return !ok || vf.IsVisible()
}

// isCompletionLeafPath reports whether path is under urfave/cli's
// built-in `pulse completion` command. Those leaves only print a script:
// urfave tolerates the root persistent flags on them but neither lists
// nor suggests them, and they build no Pulse instance, so the
// persistent-flag checks exempt them.
func isCompletionLeafPath(path []string) bool {
	return len(path) >= 2 && path[1] == "completion"
}

// persistentFlags are the root flags every leaf inherits.
func persistentFlags(root *cli.Command) []cli.Flag {
	var out []cli.Flag
	for _, f := range root.Flags {
		if lf, ok := f.(cli.LocalFlag); ok && lf.IsLocal() {
			continue
		}
		out = append(out, f)
	}
	return out
}

// TestCompletion_Scripts: the completion leaf prints a script for bash,
// zsh and fish, offers no pwsh leaf, and is listed in root help.
func TestCompletion_Scripts(t *testing.T) {
	for _, sh := range []string{"bash", "zsh", "fish"} {
		var stdout, stderr bytes.Buffer
		if st := run(context.Background(), []string{"pulse", "completion", sh}, &stdout, &stderr); st != 0 {
			t.Fatalf("pulse completion %s: status %d, stderr %q", sh, st, stderr.String())
		}
		if !strings.Contains(stdout.String(), completionMarker) {
			t.Errorf("pulse completion %s printed no completion script:\n%s", sh, stdout.String())
		}
	}

	root := completionTree()
	comp := root.Command("completion")
	if comp == nil {
		t.Fatal("pulse completion is not mounted")
	}
	if comp.Hidden {
		t.Error("pulse completion is hidden; it must be listed in help")
	}
	var shells []string
	for _, c := range comp.Commands {
		if !isHelpCommand(c) {
			shells = append(shells, c.Name)
		}
	}
	if want := []string{"bash", "zsh", "fish"}; !slices.Equal(shells, want) {
		t.Errorf("pulse completion shells = %v, want %v (no pwsh)", shells, want)
	}
	if strings.Contains(strings.ToLower(comp.Usage+comp.Description), "powershell") {
		t.Errorf("pulse completion help still mentions PowerShell: %q", comp.Usage)
	}

	var help bytes.Buffer
	run(context.Background(), []string{"pulse", "--help"}, &help, io.Discard)
	if !strings.Contains(help.String(), "completion") {
		t.Errorf("root help does not list completion:\n%s", help.String())
	}
}

// TestCompletion_SubcommandsAndFlags: a partial subcommand completes
// from its parent, and a leaf completes its flags on a "-" word.
func TestCompletion_SubcommandsAndFlags(t *testing.T) {
	out, stderr, st := completeWords(t, "pulse", "api", "pro")
	if st != 0 || stderr != "" {
		t.Fatalf("status %d stderr %q", st, stderr)
	}
	got := candidates(out)
	for _, want := range []string{"process", "process-chain"} {
		if !slices.Contains(got, want) {
			t.Errorf("pulse api pro<TAB> = %v, missing %q", got, want)
		}
	}

	out, _, _ = completeWords(t, "pulse", "api", "process", "--re")
	if got := candidates(out); !slices.Contains(got, "--request") {
		t.Errorf("pulse api process --re<TAB> = %v, missing --request", got)
	}
}

// TestCompletion_QuietOnBadInput: completion never prints to stderr and
// always exits 0, whatever the persistent flags or environment say.
// Without the quiet mode a bad --log-level is a usage error (exit 2,
// stderr) raised by the root Before before any candidate is printed.
func TestCompletion_QuietOnBadInput(t *testing.T) {
	t.Setenv("PULSE_DATA_DIR", "/nonexistent/pulse-completion-data-dir")
	for _, tc := range []struct {
		name  string
		words []string
		want  string // a candidate the output must hold, "" = no check
	}{
		{"bad log level", []string{"pulse", "--log-level", "bogus", "api", "pro"}, "process"},
		{"bad log format", []string{"pulse", "--log-format", "xml", "api", "pro"}, "process"},
		{"negative shard workers", []string{"pulse", "--shard-workers", "-3", "api", "pro"}, "process"},
		{"negative decode workers on leaf", []string{"pulse", "api", "process", "--decode-workers", "-1", "--re"}, "--request"},
		{"malformed worker count", []string{"pulse", "--shard-workers", "abc", "api"}, ""},
		{"unknown flag on leaf", []string{"pulse", "api", "process", "--bogus", "x", "--re"}, ""},
		{"bad data dir", []string{"pulse", "cohort", "inspect", "-"}, ""},
		{"unknown command", []string{"pulse", "nosuch", "x"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, stderr, st := completeWords(t, tc.words...)
			if st != 0 {
				t.Errorf("exit status %d, want 0", st)
			}
			if stderr != "" {
				t.Errorf("stderr %q, want nothing", stderr)
			}
			if strings.Contains(out, "USAGE:") || strings.Contains(out, "Incorrect Usage") {
				t.Errorf("help text leaked into the candidates:\n%s", out)
			}
			if tc.want != "" && !slices.Contains(candidates(out), tc.want) {
				t.Errorf("candidates %v, missing %q", candidates(out), tc.want)
			}
		})
	}
}

// TestCompletion_TreeCoverage walks the mounted command tree: every
// visible command is suggested by its parent, and every visible flag of
// every runnable leaf (its own and the root persistent ones) is
// suggested on a "-" word. A command or flag that completion cannot
// reach fails here.
func TestCompletion_TreeCoverage(t *testing.T) {
	root := completionTree()
	persistent := persistentFlags(root)

	suggested := map[string][]string{} // parent path → its candidates
	childrenOf := func(path []string) []string {
		key := strings.Join(path, " ")
		if got, ok := suggested[key]; ok {
			return got
		}
		out, stderr, st := completeWords(t, path...)
		if st != 0 || stderr != "" {
			t.Errorf("%s<TAB>: status %d stderr %q", key, st, stderr)
		}
		suggested[key] = candidates(out)
		return suggested[key]
	}

	leaves := 0
	walkCompletionTree(root, func(path []string, _, c *cli.Command) {
		if c.Hidden {
			return
		}
		parent := path[:len(path)-1]
		if !slices.Contains(childrenOf(parent), c.Name) {
			t.Errorf("%q is not suggested by %q", strings.Join(path, " "), strings.Join(parent, " "))
		}
		if c.Action == nil {
			return
		}
		leaves++
		out, stderr, st := completeWords(t, append(slices.Clone(path), "-")...)
		if st != 0 || stderr != "" {
			t.Errorf("%s -<TAB>: status %d stderr %q", strings.Join(path, " "), st, stderr)
		}
		got := candidates(out)
		flags := slices.Clone(c.Flags)
		if !isCompletionLeafPath(path) {
			flags = append(flags, persistent...)
		}
		for _, f := range flags {
			if !flagVisible(f) {
				continue
			}
			if want := flagCandidate(f); !slices.Contains(got, want) {
				t.Errorf("%s -<TAB> does not suggest %s (got %v)", strings.Join(path, " "), want, got)
			}
		}
	})
	if leaves == 0 {
		t.Fatal("tree walk found no runnable leaf")
	}
	for _, sh := range []string{"bash", "zsh", "fish"} {
		if root.Command("completion") == nil || root.Command("completion").Command(sh) == nil {
			t.Errorf("pulse completion %s is not in the walked tree", sh)
		}
	}
}
