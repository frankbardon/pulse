package cli

import (
	"context"
	"slices"
	"strings"

	cli "github.com/urfave/cli/v3"
)

// Shell completion. urfave/cli generates the completion scripts and
// answers the completion protocol: an installed script re-invokes the
// binary with the words typed so far followed by completionFlag, and the
// deepest command named prints its candidates (subcommands, or flags
// when the last word starts with "-") one per line on stdout.
//
// A completion request must stay QUIET: nothing on stderr, exit status
// 0, no side effect. The root Before (GlobalBefore) and the exit policy
// (onUsageError) both consult completionRequested for that.

// completionFlag is the marker the generated scripts append to the
// words typed so far. urfave/cli keeps the literal unexported; it is a
// fixed part of the protocol its scripts speak.
const completionFlag = "--generate-shell-completion"

// completionShells are the shells `pulse completion` writes a script
// for, in help order. urfave/cli also builds a PowerShell script; it is
// dropped because that script hands the binary only the word being
// completed, never the words before it, so command and flag context is
// lost.
var completionShells = []string{"bash", "zsh", "fish"}

// CompletionShells returns the shells `pulse completion` supports.
func CompletionShells() []string { return slices.Clone(completionShells) }

// completionKey marks a ctx as serving a completion request.
type completionKey struct{}

// CompletionRequest reports whether args (os.Args form, program name
// first) is a shell-completion request and, if so, returns ctx marked so
// that GlobalBefore and the exit policy stay silent. main hands the
// marked ctx to app.Run, discards stderr and exits 0.
func CompletionRequest(ctx context.Context, args []string) (context.Context, bool) {
	if len(args) == 0 || args[len(args)-1] != completionFlag {
		return ctx, false
	}
	return context.WithValue(ctx, completionKey{}, true), true
}

// completionRequested reports whether ctx serves a completion request.
func completionRequested(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	on, _ := ctx.Value(completionKey{}).(bool)
	return on
}

// EnableCompletion turns on shell completion for root and shapes the
// built-in `completion` command: listed in help, bash / zsh / fish only.
// urfave/cli appends that command when root first runs, after
// InstallExitPolicy has walked the tree, so the configure hook installs
// the exit policy on it too. It returns root for chaining.
func EnableCompletion(root *cli.Command) *cli.Command {
	root.EnableShellCompletion = true
	root.ConfigureShellCompletionCommand = configureCompletionCommand
	return root
}

// configureCompletionCommand unhides the built-in completion command,
// keeps only completionShells and rewrites its help to match.
func configureCompletionCommand(c *cli.Command) {
	c.Hidden = false
	c.Usage = "Print a shell completion script for " + shellList()
	c.Description = completionDescription(c.Root().Name)
	c.Commands = slices.DeleteFunc(c.Commands, func(sub *cli.Command) bool {
		return !slices.Contains(completionShells, sub.Name)
	})
	InstallExitPolicy(c)
}

// shellList renders completionShells as "bash, zsh or fish".
func shellList() string {
	n := len(completionShells)
	if n == 1 {
		return completionShells[0]
	}
	return strings.Join(completionShells[:n-1], ", ") + " or " + completionShells[n-1]
}

func completionDescription(app string) string {
	var b strings.Builder
	b.WriteString("Print a completion script for " + shellList() + " on stdout. Install it once:\n\n")
	for _, sh := range completionShells {
		switch sh {
		case "bash":
			b.WriteString("  bash: source <(" + app + " completion bash)   # e.g. in ~/.bashrc\n")
		case "zsh":
			b.WriteString("  zsh:  source <(" + app + " completion zsh)    # e.g. in ~/.zshrc\n")
		case "fish":
			b.WriteString("  fish: " + app + " completion fish > ~/.config/fish/completions/" + app + ".fish\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}
