package cli

import (
	"context"
	stderrors "errors"
	"fmt"

	"github.com/frankbardon/pulse/descriptor"
	cli "github.com/urfave/cli/v3"
)

// The CLI's exit statuses. Scripts branch on these, so they are a
// contract (docs/src/cli/flags.md "Exit status"): every leaf except
// `mcp` follows the same three values, with no per-domain codes.
const (
	// ExitOK is success. Warnings and advisories ride the envelope (or
	// stderr in text mode) and never move the status.
	ExitOK = 0
	// ExitFatal is a fatal error: under --json the envelope carries
	// it in `errors` (including one escalated by --strict); in text mode
	// it is printed to stderr.
	ExitFatal = 1
	// ExitUsage is a command line refused before any library call: the
	// flag parser's refusals (unknown flag, malformed flag value, missing
	// required flag or argument, mutually exclusive flags) and a bad
	// persistent --log-level / --log-format, or a negative
	// --shard-workers / --decode-workers. No envelope is written.
	ExitUsage = 2
)

// exitStatusError carries an exit status out of a leaf's Action to
// main, which maps it with ExitStatus.
type exitStatusError struct {
	status int
	err    error
	// reported means the failure is already on stdout (the --json
	// envelope's `errors`), so main adds nothing to stderr.
	reported bool
}

func (e *exitStatusError) Error() string { return e.err.Error() }
func (e *exitStatusError) Unwrap() error { return e.err }

// errEnvelopeFatal is what writeJSON returns after writing an envelope
// whose `errors` is non-empty. Every --json failure path ends in
// writeJSON, so this one choke point makes the exit status follow the
// envelope on every leaf, including the ones that build an envelope by
// hand (shard verify, index, features) rather than through
// writeCodedErrorEnvelope.
var errEnvelopeFatal = &exitStatusError{
	status:   ExitFatal,
	err:      stderrors.New("fatal error reported in the --json envelope"),
	reported: true,
}

// reportedFatal turns writeJSON's bare errEnvelopeFatal into one that
// wraps cause, so errors.As still reaches the cause's code while main
// prints nothing (the envelope already carries it). Any other werr — a
// real write failure, or nil — passes through unchanged.
func reportedFatal(werr, cause error) error {
	if werr == errEnvelopeFatal && cause != nil {
		return &exitStatusError{status: ExitFatal, err: cause, reported: true}
	}
	return werr
}

// envelopeIsFatal reports whether data is an envelope that carries at
// least one fatal error. Warnings alone are not fatal.
func envelopeIsFatal(data any) bool {
	switch env := data.(type) {
	case *descriptor.Envelope:
		return env != nil && len(env.Errors) > 0
	case descriptor.Envelope:
		return len(env.Errors) > 0
	}
	return false
}

// ExitStatus maps the error app.Run returned to the process exit status,
// and reports whether main should print the error to stderr (false when
// the failure is already in the --json envelope on stdout).
func ExitStatus(err error) (status int, printErr bool) {
	if err == nil {
		return ExitOK, false
	}
	var se *exitStatusError
	if stderrors.As(err, &se) {
		return se.status, !se.reported
	}
	return ExitFatal, true
}

// InstallExitPolicy wires the exit-status contract onto the command
// tree rooted at root: every command's flag-parser refusals become
// ExitUsage (the parser's OnUsageError is per command, never inherited,
// so the whole tree is walked). main owns the status. It returns root
// for chaining.
func InstallExitPolicy(root *cli.Command) *cli.Command {
	var walk func(c *cli.Command)
	walk = func(c *cli.Command) {
		c.OnUsageError = onUsageError
		for _, sub := range c.Commands {
			walk(sub)
		}
	}
	walk(root)
	return root
}

// usageExit marks err as a usage error (ExitUsage) without hiding it:
// main still prints it and errors.As still reaches its code.
func usageExit(err error) error {
	return &exitStatusError{status: ExitUsage, err: err}
}

// onUsageError keeps the parser's usual stderr report and help text
// and marks the error ExitUsage. Under a shell-completion request it
// prints nothing (help would land on stdout among the candidates) and
// swallows the refusal: completion offers nothing and exits 0.
func onUsageError(ctx context.Context, cmd *cli.Command, err error, isSubcommand bool) error {
	if completionRequested(ctx) {
		return nil
	}
	if errw := cmd.Root().ErrWriter; errw != nil {
		fmt.Fprintf(errw, "Incorrect Usage: %s\n\n", err.Error())
	}
	if isSubcommand {
		_ = cli.ShowSubcommandHelp(cmd)
	} else {
		_ = cli.ShowRootCommandHelp(cmd)
	}
	return usageExit(err)
}
