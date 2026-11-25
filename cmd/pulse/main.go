// Package main is the entry point for the pulse CLI binary.
package main

import (
	"context"
	"fmt"
	"io"
	"os"

	pcli "github.com/frankbardon/pulse/internal/cli"

	cli "github.com/urfave/cli/v3"
)

func main() {
	os.Exit(run(context.Background(), os.Args, os.Stdout, os.Stderr))
}

// run executes the CLI and returns the process exit status: 0 success
// (warnings allowed), 1 a fatal error (in the --json envelope, or printed
// to stderr in text mode), 2 a usage error the flag parser refused.
// pcli.ExitStatus owns the mapping; main only applies it.
//
// A shell-completion request (pcli.CompletionRequest) is quiet: stderr
// is discarded and the status is always 0, so a half-typed or invalid
// command line never disturbs the shell.
func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	ctx, completing := pcli.CompletionRequest(ctx, args)
	app := buildApp()
	app.Writer, app.ErrWriter = stdout, stderr
	if completing {
		app.ErrWriter = io.Discard
		_ = app.Run(ctx, args)
		return pcli.ExitOK
	}
	err := app.Run(ctx, args)
	status, printErr := pcli.ExitStatus(err)
	if printErr {
		fmt.Fprintf(stderr, "error: %v\n", err)
	}
	return status
}

// buildApp returns the root command; internal/cli owns the tree so
// documentation tooling walks the same one.
func buildApp() *cli.Command { return pcli.App() }
