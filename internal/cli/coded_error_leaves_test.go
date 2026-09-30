package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	cli "github.com/urfave/cli/v3"

	"github.com/frankbardon/pulse/encoding"
)

// TestLeaves_FatalCodedErrorKeepsItsCode drives every CLI leaf whose
// --json failure path used to stringify the error into a placeholder
// (PROCESS_ERROR, COMPOSE_ERROR, FACET_ERROR, FILTER_ERROR,
// SHARD_*_ERROR, PROFILE_ERROR, ...) against a synthetic cohort path the
// facade refuses with a real code, and asserts errors[0].code is that
// code. A placeholder there makes `pulse errors lookup` useless — the
// CLAUDE.md "Output Format Contract" FATAL coded-error rule.
//
// Two synthetic refusals: a transfer artifact (zstd magic) at the
// cohort path → PULSE_COHORT_COMPRESSED, and a file that starts with
// neither PULSE nor zip magic → ENCODING_INVALID.
func TestLeaves_FatalCodedErrorKeepsItsCode(t *testing.T) {
	fixtures := map[string][]byte{
		"PULSE_COHORT_COMPRESSED": append(encoding.ZstdMagic[:], []byte("synthetic-not-a-real-frame")...),
		"ENCODING_INVALID":        []byte("this is not a pulse cohort at all"),
	}
	type leaf struct {
		name string
		root func() *cli.Command
		args func(t *testing.T, dir, cohort string) []string
		// want, when set, overrides the ENCODING_INVALID fixture's code:
		// the archive-only shard leaves refuse a non-zip path as
		// PULSE_ARCHIVE_MAGIC_INVALID before reading any cohort header.
		// A zstd transfer artifact is PULSE_COHORT_COMPRESSED on every
		// leaf, archive-only ones included (encoding.OpenArchive).
		want string
	}
	reqFile := func(t *testing.T, dir, name string, v any) string {
		t.Helper()
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	request := func(cohort string) map[string]any {
		return map[string]any{
			"cohort":       map[string]any{"filename": cohort},
			"aggregations": []any{map[string]any{"type": "AGG_COUNT", "field": "id"}},
		}
	}
	leaves := []leaf{
		{"api process", APICommand, func(t *testing.T, dir, c string) []string {
			return []string{"api", "process", "--json", "-r", reqFile(t, dir, "r.json", request(c))}
		}, ""},
		{"api process --stream", APICommand, func(t *testing.T, dir, c string) []string {
			return []string{"api", "process", "--json", "--stream", "-r", reqFile(t, dir, "r.json", request(c))}
		}, ""},
		{"api process-chain", APICommand, func(t *testing.T, dir, c string) []string {
			chain := map[string]any{
				"cohort": map[string]any{"filename": c},
				"stages": []any{map[string]any{"request": map[string]any{
					"aggregations": []any{map[string]any{"type": "AGG_COUNT", "field": "id"}},
				}}},
			}
			return []string{"api", "process-chain", "--json", "-r", reqFile(t, dir, "c.json", chain)}
		}, ""},
		{"api compose", APICommand, func(t *testing.T, dir, c string) []string {
			composed := map[string]any{"requests": []any{request(c)}}
			return []string{"api", "compose", "--json", "-r", reqFile(t, dir, "k.json", composed)}
		}, ""},
		{"api compose --parallel", APICommand, func(t *testing.T, dir, c string) []string {
			composed := map[string]any{"requests": []any{request(c), request(c)}}
			return []string{"api", "compose", "--json", "--parallel", "2", "-r", reqFile(t, dir, "k.json", composed)}
		}, ""},
		{"api sample", APICommand, func(t *testing.T, _, c string) []string {
			return []string{"api", "sample", "--json", "-i", c}
		}, ""},
		{"api facet", APICommand, func(t *testing.T, _, c string) []string {
			return []string{"api", "facet", "--json", "-i", c, "-f", "id"}
		}, ""},
		{"api facet rich", APICommand, func(t *testing.T, _, c string) []string {
			return []string{"api", "facet", "--json", "-i", c, "-f", "id", "--top-k", "5"}
		}, ""},
		{"api lookup", APICommand, func(t *testing.T, _, c string) []string {
			return []string{"api", "lookup", "--json", "-i", c, "--key", "id=1"}
		}, ""},
		{"cohort filter", CohortCommand, func(t *testing.T, dir, c string) []string {
			return []string{"cohort", "filter", "--json", "-i", c, "-o", filepath.Join(dir, "out.pulse"), "--filter", "id > 1"}
		}, ""},
		{"shard list", ShardCommand, func(t *testing.T, _, c string) []string {
			return []string{"shard", "list", "--json", c}
		}, ""},
		{"shard verify", ShardCommand, func(t *testing.T, _, c string) []string {
			return []string{"shard", "verify", "--json", c}
		}, "PULSE_ARCHIVE_MAGIC_INVALID"},
		{"shard compact", ShardCommand, func(t *testing.T, _, c string) []string {
			return []string{"shard", "compact", "--json", c}
		}, "PULSE_ARCHIVE_MAGIC_INVALID"},
		{"shard remove", ShardCommand, func(t *testing.T, _, c string) []string {
			return []string{"shard", "remove", "--json", c, "a.pulse"}
		}, "PULSE_ARCHIVE_MAGIC_INVALID"},
		{"shard add", ShardCommand, func(t *testing.T, _, c string) []string {
			return []string{"shard", "add", "--json", c, c}
		}, "PULSE_ARCHIVE_MAGIC_INVALID"},
		{"profile create", ProfileCommand, func(t *testing.T, dir, c string) []string {
			return []string{"profile", "create", "--json", "-i", c, "-o", filepath.Join(dir, "p.json")}
		}, ""},
	}
	for _, lf := range leaves {
		for fixture, raw := range fixtures {
			wantCode := fixture
			if lf.want != "" && fixture != "PULSE_COHORT_COMPRESSED" {
				wantCode = lf.want
			}
			t.Run(lf.name+"/"+fixture, func(t *testing.T) {
				dir := t.TempDir()
				cohort := filepath.Join(dir, "cohort.pulse")
				if err := os.WriteFile(cohort, raw, 0o644); err != nil {
					t.Fatal(err)
				}
				var buf bytes.Buffer
				root := lf.root()
				root.Writer = &buf
				if err := root.Run(context.Background(), lf.args(t, dir, cohort)); err != nil {
					t.Fatalf("hard error instead of an envelope: %v", err)
				}
				var env struct {
					Errors []struct {
						Code    string `json:"code"`
						Message string `json:"message"`
					} `json:"errors"`
				}
				if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
					t.Fatalf("output is not an envelope: %v\n%s", err, buf.String())
				}
				if len(env.Errors) == 0 {
					t.Fatalf("no errors in envelope: %s", buf.String())
				}
				if env.Errors[0].Code != wantCode {
					t.Errorf("errors[0].code = %q (message %q), want %q",
						env.Errors[0].Code, env.Errors[0].Message, wantCode)
				}
			})
		}
	}
}
