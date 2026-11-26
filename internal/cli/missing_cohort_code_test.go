package cli

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	perrors "github.com/frankbardon/pulse/errors"
	cli "github.com/urfave/cli/v3"
)

// TestMissingCohort_OneCodeOnEveryLeaf: a cohort path that does not
// exist is SERVICE_RESOURCE on every leaf that opens one — the
// no-execute reads (cohort inspect, api predict), the run (api process),
// export and every shard leaf — under --json (errors[0].code) and in
// text mode, where the error leads with the real code and never with a
// per-leaf label.
func TestMissingCohort_OneCodeOnEveryLeaf(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	missing := filepath.Join(dir, "nope.pulse")
	req, err := json.Marshal(map[string]any{
		"cohort":       map[string]any{"filename": missing},
		"aggregations": []any{map[string]any{"type": "AGG_COUNT", "label": "n"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	reqPath := filepath.Join(dir, "r.json")
	if err := os.WriteFile(reqPath, req, 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out.csv")
	cases := []struct {
		root func() *cli.Command
		args []string
	}{
		{CohortCommand, []string{"cohort", "inspect", missing}},
		{APICommand, []string{"api", "predict", "-r", reqPath}},
		{APICommand, []string{"api", "process", "-r", reqPath}},
		{ExportCommand, []string{"export", "csv", "-i", missing, "-o", out}},
		{ShardCommand, []string{"shard", "list", missing}},
		{ShardCommand, []string{"shard", "verify", missing}},
		{ShardCommand, []string{"shard", "compact", missing}},
		{ShardCommand, []string{"shard", "remove", missing, "a.pulse"}},
		{ShardCommand, []string{"shard", "add", missing, filepath.Join(dir, "b.pulse")}},
	}
	for _, c := range cases {
		name := strings.Join(c.args[:2], " ")
		run := func(args ...string) (string, error) {
			var buf bytes.Buffer
			root := c.root()
			root.Writer = &buf
			root.ErrWriter = &buf
			err := root.Run(context.Background(), args)
			return buf.String(), err
		}

		raw, _ := run(append(append([]string{}, c.args...), "--json")...)
		var env struct {
			Errors []struct {
				Code string `json:"code"`
			} `json:"errors"`
		}
		if err := json.Unmarshal([]byte(raw), &env); err != nil {
			t.Errorf("%s --json: not an envelope: %v\n%s", name, err, raw)
			continue
		}
		if len(env.Errors) != 1 || env.Errors[0].Code != string(perrors.SERVICE_RESOURCE) {
			t.Errorf("%s --json: errors = %+v, want one SERVICE_RESOURCE\n%s", name, env.Errors, raw)
		}

		_, err := run(c.args...)
		if err == nil {
			t.Errorf("%s: text mode returned no error", name)
			continue
		}
		if msg := err.Error(); !strings.HasPrefix(msg, string(perrors.SERVICE_RESOURCE)+": ") {
			t.Errorf("%s: text error = %q, want it to lead with SERVICE_RESOURCE", name, msg)
		}
	}
}

// TestCliErrorFrom_TextPathKeepsTheCode: in text mode cliErrorFrom
// returns a coded error itself (its Error() already reads "CODE:
// message", and errors.As still finds it), and labels only an uncoded
// error with the CLI_ERROR fallback.
func TestCliErrorFrom_TextPathKeepsTheCode(t *testing.T) {
	t.Parallel()
	cmd := &cli.Command{}
	coded := perrors.NewCodedError(perrors.SERVICE_RESOURCE, "opening shard archive: x.pulse")
	got := cliErrorFrom(cmd, false, string(perrors.CLI_ERROR), coded)
	var ce *perrors.CodedError
	if !stderrors.As(got, &ce) || ce.Code != perrors.SERVICE_RESOURCE || got.Error() != "SERVICE_RESOURCE: opening shard archive: x.pulse" {
		t.Errorf("coded: got %q, want the coded error itself", got)
	}
	got = cliErrorFrom(cmd, false, string(perrors.CLI_ERROR), stderrors.New("disk on fire"))
	if got.Error() != "CLI_ERROR: disk on fire" {
		t.Errorf("uncoded: got %q, want CLI_ERROR: disk on fire", got)
	}
}
