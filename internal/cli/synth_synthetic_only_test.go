package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/frankbardon/pulse/internal/synth"
)

// E3-S1 (FR-19, FR-20): `synth from-profile --synthetic-only
// --constraints --max-rejection-rate` driven through the real leaf.

// TestSynthFromProfileCLI_SyntheticOnlyWithConstraints: the output holds
// the generated rows alone, every one honouring the loaded constraint,
// the fidelity report still scores them against the source, and
// --emit-spec carries the constraints and the ceiling so from-schema
// reproduces the run.
func TestSynthFromProfileCLI_SyntheticOnlyWithConstraints(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	const sourceRows, newRows = 120, 80
	source, profile := richFromProfileFixture(t, dir, sourceRows, 3)
	constraints := filepath.Join(dir, "constraints.json")
	if err := os.WriteFile(constraints, []byte(`[{"expr": "region != \"apac\""}, {"expr": "isnull(spend) || spend > 0"}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(dir, "twin.pulse")
	report := filepath.Join(dir, "report.json")
	emitted := filepath.Join(dir, "spec.json")
	var buf bytes.Buffer
	if err := runSynthCLI(t, &buf, "from-profile",
		"--profile", profile, "--source", source, "--output", output,
		"--rows", "80", "--seed", "4", "--synthetic-only",
		"--constraints", constraints, "--max-rejection-rate", "0.6",
		"--fidelity-report", report, "--emit-spec", emitted, "--json"); err != nil {
		t.Fatalf("synth from-profile: %v\n%s", err, buf.String())
	}
	var env struct {
		Data struct {
			RowsGenerated int `json:"rows_generated"`
			RowsRejected  int `json:"rows_rejected"`
		} `json:"data"`
	}
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("envelope: %v\n%s", err, buf.String())
	}
	if env.Data.RowsGenerated != newRows || env.Data.RowsRejected == 0 {
		t.Errorf("envelope data = %+v, want %d accepted and some rejected", env.Data, newRows)
	}

	rows, _ := readCohortRows(t, output)
	if len(rows) != newRows {
		t.Fatalf("output rows = %d, want the %d generated rows alone (no source rows)", len(rows), newRows)
	}
	for i, row := range rows {
		if _, tagged := row[synth.SyntheticFieldName]; tagged {
			t.Fatalf("row %d carries a %s column under --synthetic-only", i, synth.SyntheticFieldName)
		}
		if row["region"] == "apac" {
			t.Fatalf("row %d: region = apac violates the --constraints file", i)
		}
	}

	raw, err := os.ReadFile(report)
	if err != nil {
		t.Fatalf("fidelity report: %v", err)
	}
	var rep synth.FidelityReport
	if err := json.Unmarshal(raw, &rep); err != nil {
		t.Fatal(err)
	}
	if rep.SourceRows != sourceRows || rep.SyntheticRows != newRows || len(rep.Fields) == 0 {
		t.Errorf("report partitions = %d / %d over %d fields, want %d / %d", rep.SourceRows, rep.SyntheticRows, len(rep.Fields), sourceRows, newRows)
	}

	spec, err := os.ReadFile(emitted)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := synth.ParseSpec(spec)
	if err != nil {
		t.Fatalf("emitted spec: %v", err)
	}
	if len(parsed.Constraints) != 2 || parsed.Constraints[0].Expr != `region != "apac"` || parsed.MaxRejectionRate != 0.6 {
		t.Errorf("emitted spec constraints = %+v, max_rejection_rate = %v", parsed.Constraints, parsed.MaxRejectionRate)
	}
}

// TestSynthFromProfileCLI_ConstraintsRefusalsKeepTheirCode: a bad
// constraints file or rate reaches the envelope under its own code,
// names the file, and writes nothing.
func TestSynthFromProfileCLI_ConstraintsRefusalsKeepTheirCode(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	source, profile := richFromProfileFixture(t, dir, 40, 6)
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte(`[{"expr": "uniform() < 0.5"}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	ok := filepath.Join(dir, "ok.json")
	if err := os.WriteFile(ok, []byte(`[{"expr": "spend > 0"}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name, code, path string
		args             []string
	}{
		{"draw", "PULSE_SYNTH_RULE_DRAW_INVALID", bad, []string{"--constraints", bad}},
		{"rate", "SERVICE_VALIDATION", "", []string{"--constraints", ok, "--max-rejection-rate", "1"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			out := filepath.Join(dir, c.name+".pulse")
			emitted := filepath.Join(dir, c.name+"-spec.json")
			var buf bytes.Buffer
			args := append([]string{"from-profile", "--profile", profile, "--source", source, "--output", out,
				"--rows", "5", "--synthetic-only", "--emit-spec", emitted, "--json"}, c.args...)
			wantEnvelopeFatal(t, runSynthCLI(t, &buf, args...))
			var env struct {
				Errors []struct {
					Code    string         `json:"code"`
					Details map[string]any `json:"details"`
				} `json:"errors"`
			}
			if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
				t.Fatalf("envelope: %v\n%s", err, buf.String())
			}
			if len(env.Errors) != 1 || env.Errors[0].Code != c.code {
				t.Fatalf("errors = %+v, want one %s", env.Errors, c.code)
			}
			if c.path != "" && env.Errors[0].Details["path"] != c.path {
				t.Errorf("details.path = %v, want %q", env.Errors[0].Details["path"], c.path)
			}
			for _, p := range []string{out, emitted} {
				if _, err := os.Stat(p); err == nil {
					t.Errorf("a refused run wrote %s", p)
				}
			}
		})
	}
}
