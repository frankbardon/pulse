package cli

import (
	"bytes"
	"context"
	stderrors "errors"
	"testing"

	cli "github.com/urfave/cli/v3"
)

// TestCompletionRequest_DetectsTheMarker: only a trailing
// --generate-shell-completion marks the ctx.
func TestCompletionRequest_DetectsTheMarker(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want bool
	}{
		{[]string{"pulse", "api", completionFlag}, true},
		{[]string{"pulse", completionFlag}, true},
		{[]string{"pulse", completionFlag, "api"}, false},
		{[]string{"pulse", "api"}, false},
		{nil, false},
	} {
		ctx, got := CompletionRequest(context.Background(), tc.args)
		if got != tc.want || completionRequested(ctx) != tc.want {
			t.Errorf("CompletionRequest(%q) = %v (ctx marked %v), want %v",
				tc.args, got, completionRequested(ctx), tc.want)
		}
	}
}

// TestCompletion_UsageErrorIsQuiet: under a completion request the exit
// policy's usage-error hook prints nothing (no stderr report, no help on
// stdout among the candidates) and swallows the refusal; outside one it
// still reports and exits 2.
func TestCompletion_UsageErrorIsQuiet(t *testing.T) {
	newRoot := func(out, errOut *bytes.Buffer) *cli.Command {
		root := &cli.Command{Name: "pulse", Writer: out, ErrWriter: errOut}
		return root
	}
	parseErr := stderrors.New("flag provided but not defined: -bogus")

	var out, errOut bytes.Buffer
	ctx, _ := CompletionRequest(context.Background(), []string{"pulse", completionFlag})
	if err := onUsageError(ctx, newRoot(&out, &errOut), parseErr, false); err != nil {
		t.Errorf("completion: onUsageError = %v, want nil", err)
	}
	if out.Len() != 0 || errOut.Len() != 0 {
		t.Errorf("completion: printed stdout %q stderr %q, want nothing", out.String(), errOut.String())
	}

	out.Reset()
	errOut.Reset()
	err := onUsageError(context.Background(), newRoot(&out, &errOut), parseErr, false)
	if status, _ := ExitStatus(err); status != ExitUsage || errOut.Len() == 0 {
		t.Errorf("normal run: exit %d stderr %q, want 2 with a report", status, errOut.String())
	}
}

// TestCompletion_GlobalBeforeSkipsValidation: a completion request skips
// the persistent-flag checks a normal run refuses with a usage error.
func TestCompletion_GlobalBeforeSkipsValidation(t *testing.T) {
	var got error
	root := &cli.Command{
		Name:   "pulse",
		Flags:  append(LogFlags(), WorkerFlags()...),
		Writer: &bytes.Buffer{}, ErrWriter: &bytes.Buffer{},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			_, got = GlobalBefore(ctx, cmd)
			return nil
		},
	}
	args := []string{"pulse", "--log-level", "bogus", "--shard-workers", "-2"}

	ctx, _ := CompletionRequest(context.Background(), append(args, completionFlag))
	if err := root.Run(ctx, args); err != nil || got != nil {
		t.Fatalf("completion: Run %v, GlobalBefore %v; want both nil", err, got)
	}
	if err := root.Run(context.Background(), args); err != nil || got == nil {
		t.Fatalf("normal run: GlobalBefore = nil, want the usage refusal (Run %v)", err)
	}
}
