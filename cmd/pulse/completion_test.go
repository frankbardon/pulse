package main

// SHELL COMPLETION (U37).
//
// `pulse completion bash|zsh|fish` prints a script; the script re-invokes
// the binary with the words typed so far plus --generate-shell-completion
// and reads the candidates from stdout. These tests drive that protocol
// in-process through run(), the same entry point main uses.
//
// The tree walk (walkCompletionTree / completionTree) is shared with
// cliLeaves: TestCompletion_TreeCoverage visits every leaf and every
// flag of it, and requires every value-taking flag to be classified in
// internal/cli/completion_values.go (vocab, file, cohort, field, free).

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	pcli "github.com/frankbardon/pulse/internal/cli"

	"github.com/frankbardon/pulse"
	perrors "github.com/frankbardon/pulse/errors"
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/types"
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

	leaves, vocabFlags := 0, 0
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
			if isCompletionLeafPath(path) || !flagTakesValue(f) {
				continue
			}
			// Every value-taking flag is classified for value
			// completion; a closed vocabulary completes non-empty,
			// sorted and quiet.
			class, ok := pcli.CompletionFlagClass(c, f.Names()[0])
			if !ok {
				t.Errorf("%s %s is not classified for value completion (add it to internal/cli/completion_values.go)",
					strings.Join(path, " "), flagCandidate(f))
				continue
			}
			if class != "vocab" {
				continue
			}
			vocabFlags++
			vout, vstderr, vst := completeWords(t, append(slices.Clone(path), flagCandidate(f))...)
			vals := candidates(vout)
			if vst != 0 || vstderr != "" || len(vals) == 0 || !slices.IsSorted(vals) {
				t.Errorf("%s %s <TAB>: status %d stderr %q candidates %v (want non-empty, sorted)",
					strings.Join(path, " "), flagCandidate(f), vst, vstderr, vals)
			}
		}
	})
	if leaves == 0 {
		t.Fatal("tree walk found no runnable leaf")
	}
	if vocabFlags == 0 {
		t.Fatal("tree walk found no flag with a closed vocabulary")
	}
	for scope, flags := range pcli.CompletionFlagScopes() {
		cmd := commandAt(root, scope)
		if cmd == nil {
			t.Errorf("completion scope %q is not a command", scope)
			continue
		}
		for _, name := range flags {
			if !scopeHasFlag(cmd, name) {
				t.Errorf("completion scope %q reclassifies --%s, which no command under it has", scope, name)
			}
		}
	}
	for leaf, classes := range pcli.CompletionPositionalClasses() {
		cmd := commandAt(root, leaf)
		if cmd == nil || cmd.Action == nil {
			t.Errorf("positional completion for %q: not a leaf", leaf)
			continue
		}
		if n := len(strings.Fields(cmd.ArgsUsage)); n < len(classes) {
			t.Errorf("positional completion for %q lists %d positionals; ArgsUsage %q has %d", leaf, len(classes), cmd.ArgsUsage, n)
		}
	}
	for _, sh := range []string{"bash", "zsh", "fish"} {
		if root.Command("completion") == nil || root.Command("completion").Command(sh) == nil {
			t.Errorf("pulse completion %s is not in the walked tree", sh)
		}
	}
}

// flagTakesValue reports whether f needs a value (every non-bool flag).
func flagTakesValue(f cli.Flag) bool {
	df, ok := f.(cli.DocGenerationFlag)
	return ok && df.TakesValue()
}

// commandAt returns the command at path ("pulse a b"), or nil.
func commandAt(root *cli.Command, path string) *cli.Command {
	words := strings.Fields(path)
	if len(words) == 0 || words[0] != root.Name {
		return nil
	}
	c := root
	for _, w := range words[1:] {
		if c = c.Command(w); c == nil {
			return nil
		}
	}
	return c
}

// scopeHasFlag reports whether c or a command under it has a flag
// whose primary name is name.
func scopeHasFlag(c *cli.Command, name string) bool {
	for _, f := range c.Flags {
		if f.Names()[0] == name {
			return true
		}
	}
	for _, sub := range c.Commands {
		if scopeHasFlag(sub, name) {
			return true
		}
	}
	return false
}

// TestCompletion_FlagValues: a value-taking flag typed without its value
// completes from the library's own vocabulary, sorted; the lookup alias
// assert-unique is accepted but never offered.
func TestCompletion_FlagValues(t *testing.T) {
	for _, tc := range []struct {
		words []string
		want  []string
	}{
		{[]string{"pulse", "api", "lookup", "--mode"}, []string{"all", "assert_unique", "first"}},
		{[]string{"pulse", "import", "csv", "--dst-policy"}, []string{"earlier", "error", "later"}},
		{[]string{"pulse", "--log-level"}, []string{"debug", "error", "info", "off", "warn"}},
		{[]string{"pulse", "api", "process", "--log-format"}, []string{"json", "text"}},
		{[]string{"pulse", "errors", "list", "--domain"}, sortedCopy(perrors.AllDomains())},
		{[]string{"pulse", "import", "predict", "--format"}, sortedStrs(pio.Formats())},
		{[]string{"pulse", "convert", "--to"}, sortedStrs(pio.Formats())},
		{[]string{"pulse", "widen", "--to"}, sortedCopy(pulse.WidenSetFieldTargets())},
		{[]string{"pulse", "features", "init", "--from"}, sortedCopy(pulse.ExampleFeatureProfiles())},
		{[]string{"pulse", "mcp", "--return"}, sortedStrs(types.AllReturnPresets())},
		{[]string{"pulse", "mcp", "--limit"}, limitAssignments()},
	} {
		t.Run(strings.Join(tc.words[1:], " "), func(t *testing.T) {
			out, stderr, st := completeWords(t, tc.words...)
			if st != 0 || stderr != "" {
				t.Fatalf("status %d stderr %q", st, stderr)
			}
			if got := candidates(out); !slices.Equal(got, tc.want) {
				t.Errorf("candidates %v, want %v", got, tc.want)
			}
		})
	}

	// Repeated runs answer byte-identically.
	first, _, _ := completeWords(t, "pulse", "examples", "search", "--category")
	if first == "" {
		t.Fatal("examples search --category completed nothing")
	}
	for range 3 {
		if again, _, _ := completeWords(t, "pulse", "examples", "search", "--category"); again != first {
			t.Fatalf("completion output differs across runs:\n%s\n---\n%s", first, again)
		}
	}

	// A flag that prefixes a longer one also offers the longer one, so a
	// word still being typed completes to it.
	out, _, _ := completeWords(t, "pulse", "import", "csv", "--source-tz")
	if got := candidates(out); !slices.Equal(got, []string{"--source-tz-field"}) {
		t.Errorf("import csv --source-tz<TAB> = %v, want [--source-tz-field]", got)
	}
	// A free-text flag value completes nothing (the shell falls back).
	if out, _, _ := completeWords(t, "pulse", "api", "sample", "--count"); out != "" {
		t.Errorf("api sample --count<TAB> = %q, want nothing", out)
	}
}

// TestCompletion_Positionals: named positionals complete from the
// library (error codes, skills, examples, an archive's shards).
func TestCompletion_Positionals(t *testing.T) {
	out, stderr, st := completeWords(t, "pulse", "errors", "lookup")
	if st != 0 || stderr != "" {
		t.Fatalf("errors lookup: status %d stderr %q", st, stderr)
	}
	got := candidates(out)
	if !slices.Equal(got, sortedCopy(perrors.SortedCodeNames())) {
		t.Errorf("errors lookup <TAB> does not list exactly the error codes (got %d)", len(got))
	}
	var mat []string
	for _, c := range got {
		if strings.HasPrefix(c, "PULSE_MAT") {
			mat = append(mat, c)
		}
	}
	if len(mat) == 0 {
		t.Error("errors lookup PULSE_MAT<TAB> has no matrix code to offer")
	}
	// One positional only: nothing after the code.
	if out, _, _ := completeWords(t, "pulse", "errors", "lookup", "PULSE_MATRIX_DIM"); out != "" {
		t.Errorf("errors lookup CODE <TAB> = %q, want nothing", out)
	}

	out, _, _ = completeWords(t, "pulse", "skills", "show")
	if got := candidates(out); !slices.Contains(got, "session-bootstrap") || !slices.IsSorted(got) {
		t.Errorf("skills show <TAB> = %v, want sorted skill names incl. session-bootstrap", got)
	}
	out, _, _ = completeWords(t, "pulse", "examples", "show")
	if got := candidates(out); len(got) == 0 || !slices.IsSorted(got) {
		t.Errorf("examples show <TAB> = %v, want sorted example names", got)
	}

	dir := t.TempDir()
	shard := createTestPulseFile(t, dir)
	archive := filepath.Join(dir, "archive.pulse")
	var so, se bytes.Buffer
	if st := run(context.Background(), []string{"pulse", "shard", "create", archive, "--include", shard}, &so, &se); st != 0 {
		t.Fatalf("shard create: status %d stderr %s", st, se.String())
	}
	before, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PULSE_DATA_DIR", "") // no data dir: cohort slots fall back to files
	for _, leaf := range []string{"remove", "extract"} {
		out, stderr, st := completeWords(t, "pulse", "shard", leaf, archive)
		if st != 0 || stderr != "" {
			t.Fatalf("shard %s ARCHIVE: status %d stderr %q", leaf, st, stderr)
		}
		if got := candidates(out); !slices.Equal(got, []string{filepath.Base(shard)}) {
			t.Errorf("shard %s ARCHIVE <TAB> = %v, want [%s]", leaf, got, filepath.Base(shard))
		}
		// The archive slot itself is a path: the shell completes it.
		if out, _, _ := completeWords(t, "pulse", "shard", leaf); out != "" {
			t.Errorf("shard %s <TAB> = %q, want nothing (file fallback)", leaf, out)
		}
	}
	after, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Errorf("completion changed the directory: %d entries before, %d after", len(before), len(after))
	}
}

// TestCompletion_ProviderFailureIsQuiet: a provider that cannot answer
// (missing archive, unbuildable Pulse) prints nothing, exits 0 and
// writes nothing to stderr.
func TestCompletion_ProviderFailureIsQuiet(t *testing.T) {
	bad := t.TempDir()
	if err := os.WriteFile(filepath.Join(bad, "broken.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		env   map[string]string
		words []string
	}{
		{"missing archive", nil, []string{"pulse", "shard", "remove", filepath.Join(bad, "nope.pulse")}},
		{"unbuildable pulse", map[string]string{"PULSE_LABEL_TABLES_DIR": bad}, []string{"pulse", "skills", "show"}},
		{"unbuildable pulse, manifest vocab", map[string]string{"PULSE_LABEL_TABLES_DIR": bad}, []string{"pulse", "examples", "search", "--tag"}},
		{"garbled flag", nil, []string{"pulse", "api", "lookup", "--mode", "--bogus", "--mo"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			out, stderr, st := completeWords(t, tc.words...)
			if st != 0 || stderr != "" {
				t.Errorf("status %d stderr %q, want 0 and nothing", st, stderr)
			}
			if tc.name != "garbled flag" && out != "" {
				t.Errorf("output %q, want nothing", out)
			}
		})
	}
}

func sortedCopy(vs []string) []string {
	out := slices.Clone(vs)
	slices.Sort(out)
	return out
}

func sortedStrs[T ~string](vs []T) []string {
	out := make([]string, len(vs))
	for i, v := range vs {
		out[i] = string(v)
	}
	slices.Sort(out)
	return out
}

func limitAssignments() []string {
	var out []string
	for _, n := range pulse.LimitNames() {
		out = append(out, n+"=")
	}
	slices.Sort(out)
	return out
}

// cohortDataDir builds a PULSE_DATA_DIR holding a single-file cohort, a
// shard archive (in a subdirectory), a .pulse.zst transfer artifact and
// a non-cohort file, sets the env var, and returns the two cohort paths
// as completion prints them.
func cohortDataDir(t *testing.T) (single, archive string) {
	t.Helper()
	stage := t.TempDir()
	shard := createTestPulseFile(t, stage)
	data := t.TempDir()
	single = filepath.Join(data, "people.pulse")
	archive = filepath.Join(data, "sub", "people-archive.pulse")
	if err := os.MkdirAll(filepath.Dir(archive), 0o755); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(shard)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(single, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	var so, se bytes.Buffer
	if st := run(context.Background(), []string{"pulse", "shard", "create", archive, "--include", shard}, &so, &se); st != 0 {
		t.Fatalf("shard create: status %d stderr %s", st, se.String())
	}
	var tr bytes.Buffer
	if st := run(context.Background(), []string{"pulse", "export", "transfer", "-i", single}, &tr, &se); st != 0 {
		t.Fatalf("export transfer: status %d stderr %s", st, se.String())
	}
	if _, err := os.Stat(single + ".zst"); err != nil {
		t.Fatalf("transfer artifact: %v", err)
	}
	if err := os.WriteFile(filepath.Join(data, "notes.csv"), []byte("a\n1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PULSE_DATA_DIR", data)
	return single, archive
}

// TestCompletion_Cohorts: a cohort flag or positional lists the .pulse
// cohorts under PULSE_DATA_DIR — single files and shard archives, never
// a .pulse.zst — as paths the leaf opens.
func TestCompletion_Cohorts(t *testing.T) {
	single, archive := cohortDataDir(t)
	want := sortedCopy([]string{single, archive})
	for _, words := range [][]string{
		{"pulse", "api", "facet", "--input"},
		{"pulse", "api", "lookup", "-i"},
		{"pulse", "recommend", "--cohort"},
		{"pulse", "synth", "from-profile", "--source"},
		{"pulse", "cohort", "inspect"},
		{"pulse", "widen"},
		{"pulse", "dedup"},
		{"pulse", "rake"},
		{"pulse", "index", "build"},
		{"pulse", "index", "drop"},
		{"pulse", "shard", "list"},
		{"pulse", "shard", "remove"},
	} {
		t.Run(strings.Join(words[1:], " "), func(t *testing.T) {
			out, stderr, st := completeWords(t, words...)
			if st != 0 || stderr != "" {
				t.Fatalf("status %d stderr %q", st, stderr)
			}
			if got := candidates(out); !slices.Equal(got, want) {
				t.Errorf("candidates %v, want %v", got, want)
			}
		})
	}
	// import reads source files: its --input stays the shell's.
	if out, _, _ := completeWords(t, "pulse", "import", "csv", "--input"); out != "" {
		t.Errorf("import csv --input<TAB> = %q, want nothing (file fallback)", out)
	}
}

// TestCompletion_Fields: a field-taking flag lists the schema fields of
// the cohort the leaf names, single file or shard archive.
func TestCompletion_Fields(t *testing.T) {
	single, archive := cohortDataDir(t)
	want := []string{"age", "name"}
	for _, words := range [][]string{
		{"pulse", "api", "facet", "--input", single, "--field"},
		{"pulse", "api", "facet", "-i", archive, "--additive"},
		{"pulse", "api", "lookup", "--input", archive, "--key"},
		{"pulse", "api", "lookup", "--input", single, "--return"},
		{"pulse", "api", "sample", "--input", single, "--labels"},
		{"pulse", "export", "csv", "--input", archive, "--include"},
		{"pulse", "export", "csv", "--input", single, "--labels"},
		{"pulse", "cohort", "filter", "--input", single, "--include-field"},
		{"pulse", "widen", archive, "--field"},
		{"pulse", "dedup", single, "--group"},
		{"pulse", "rake", single, "--by"},
		{"pulse", "rake", "--input", archive, "--base-weight"},
		{"pulse", "index", "build", archive, "--key"},
		{"pulse", "index", "verify", "--input", single, "--key"},
		{"pulse", "recommend", "--cohort", archive, "--field"},
	} {
		t.Run(strings.Join(words[1:], " "), func(t *testing.T) {
			out, stderr, st := completeWords(t, words...)
			if st != 0 || stderr != "" {
				t.Fatalf("status %d stderr %q", st, stderr)
			}
			if got := candidates(out); !slices.Equal(got, want) {
				t.Errorf("candidates %v, want %v", got, want)
			}
		})
	}
}

// TestCompletion_CohortFailuresAreQuiet: a missing or corrupt cohort, an
// unreadable or missing data dir and an unset PULSE_DATA_DIR complete
// nothing, write nothing to stderr and exit 0 — the shell falls back to
// its file completion.
func TestCompletion_CohortFailuresAreQuiet(t *testing.T) {
	dir := t.TempDir()
	corrupt := filepath.Join(dir, "corrupt.pulse")
	if err := os.WriteFile(corrupt, []byte("PULSE\x00\x00\x00\x09garbage"), 0o644); err != nil {
		t.Fatal(err)
	}
	locked := filepath.Join(dir, "locked")
	if err := os.MkdirAll(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(locked, "hidden.pulse"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	for _, tc := range []struct {
		name    string
		dataDir string
		words   []string
	}{
		{"missing cohort", dir, []string{"pulse", "api", "facet", "--input", filepath.Join(dir, "nope.pulse"), "--field"}},
		{"corrupt cohort", dir, []string{"pulse", "widen", corrupt, "--field"}},
		{"not a cohort", dir, []string{"pulse", "recommend", "--cohort", dir, "--field"}},
		{"unset data dir", "", []string{"pulse", "api", "facet", "--input"}},
		{"missing data dir", filepath.Join(dir, "nope"), []string{"pulse", "widen"}},
		{"unreadable data dir", locked, []string{"pulse", "cohort", "inspect"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("PULSE_DATA_DIR", tc.dataDir)
			if tc.dataDir == "" {
				// An unset data dir never falls back to the working
				// directory, even one holding a cohort.
				t.Chdir(dir)
			}
			out, stderr, st := completeWords(t, tc.words...)
			if st != 0 || stderr != "" {
				t.Errorf("status %d stderr %q, want 0 and nothing", st, stderr)
			}
			if tc.name == "unreadable data dir" && os.Geteuid() == 0 {
				return // root reads a 000 directory anyway
			}
			if out != "" {
				t.Errorf("output %q, want nothing", out)
			}
		})
	}
}
