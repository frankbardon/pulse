package cli

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	perrors "github.com/frankbardon/pulse/errors"
	cli "github.com/urfave/cli/v3"
)

// wantEnvelopeFatal fails t unless err is the envelope-fatal exit:
// status 1, and nothing for main to print (the envelope carries it).
func wantEnvelopeFatal(t *testing.T, err error) {
	t.Helper()
	if status, printErr := ExitStatus(err); status != ExitFatal || printErr {
		t.Fatalf("err = %v: exit (%d, print=%v), want the envelope-fatal exit (1, print=false)", err, status, printErr)
	}
}

// TestWriteJSON_ExitFollowsTheEnvelope: the one choke point every --json
// path ends in. An envelope with an error returns the envelope-fatal
// exit; warnings alone, and any non-envelope payload, return nil. The
// bytes written are the same either way.
func TestWriteJSON_ExitFollowsTheEnvelope(t *testing.T) {
	fatal := descriptor.NewEnvelope(nil)
	fatal.AddError("SERVICE_RESOURCE", "opening cohort file", nil)
	warnOnly := descriptor.NewEnvelope(map[string]int{"n": 1})
	warnOnly.AddWarning("PULSE_ADVISORY_X", "a note", nil)

	cases := []struct {
		name  string
		data  any
		fatal bool
	}{
		{"pointer envelope with an error", fatal, true},
		{"value envelope with an error", *fatal, true},
		{"warnings only", warnOnly, false},
		{"clean envelope", descriptor.NewEnvelope("ok"), false},
		{"bare payload", map[string]string{"k": "v"}, false},
		{"nil envelope pointer", (*descriptor.Envelope)(nil), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			err := writeJSON(&buf, tc.data)
			if tc.fatal {
				wantEnvelopeFatal(t, err)
			} else if err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			want, _ := json.MarshalIndent(tc.data, "", "  ")
			if got := bytes.TrimRight(buf.Bytes(), "\n"); !bytes.Equal(got, want) {
				t.Fatalf("envelope bytes moved:\n%s\nwant\n%s", got, want)
			}
		})
	}
}

// TestWriteCodedErrorEnvelope_ExitKeepsTheCause: the fatal exit still
// unwraps to the coded error, so a caller's errors.As reaches the code.
func TestWriteCodedErrorEnvelope_ExitKeepsTheCause(t *testing.T) {
	cause := perrors.NewCodedError(perrors.SERVICE_RESOURCE, "opening cohort file")
	err := writeCodedErrorEnvelope(&bytes.Buffer{}, "PROCESS_ERROR", cause)
	wantEnvelopeFatal(t, err)
	var ce *perrors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perrors.SERVICE_RESOURCE {
		t.Fatalf("errors.As lost the cause: %v", err)
	}
}

// TestExitStatus_Mapping: nil → 0; a plain (text-mode) error → 1 and
// printed; the envelope-fatal exit → 1 and not printed.
func TestExitStatus_Mapping(t *testing.T) {
	if s, p := ExitStatus(nil); s != ExitOK || p {
		t.Fatalf("nil → (%d, %v)", s, p)
	}
	if s, p := ExitStatus(stderrors.New("boom")); s != ExitFatal || !p {
		t.Fatalf("plain → (%d, %v)", s, p)
	}
	wantEnvelopeFatal(t, errEnvelopeFatal)
}

// TestInstallExitPolicy_UsageIsTwo: every command in the tree, not only
// the root, maps a parser refusal to ExitUsage — an undefined flag and a
// missing required flag alike — and never reaches the Action.
func TestInstallExitPolicy_UsageIsTwo(t *testing.T) {
	ran := false
	for _, args := range [][]string{
		{"root", "group", "leaf", "--req", "x", "--no-such-flag"},
		{"root", "group", "leaf"},
	} {
		leaf := &cli.Command{
			Name:   "leaf",
			Flags:  []cli.Flag{&cli.StringFlag{Name: "req", Required: true}},
			Action: func(context.Context, *cli.Command) error { ran = true; return nil },
		}
		root := InstallExitPolicy(&cli.Command{
			Name:      "root",
			Commands:  []*cli.Command{{Name: "group", Commands: []*cli.Command{leaf}}},
			Writer:    &bytes.Buffer{},
			ErrWriter: &bytes.Buffer{},
		})
		err := root.Run(context.Background(), args)
		if s, p := ExitStatus(err); s != ExitUsage || !p {
			t.Fatalf("%v: err = %v → (%d, %v), want (2, true)", args, err, s, p)
		}
		if ran {
			t.Fatalf("%v: Action ran on a usage error", args)
		}
	}
}
