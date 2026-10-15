package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// writeReqFile writes a request JSON file into a temp dir and returns
// its path.
func writeReqFile(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "req.json")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write request: %v", err)
	}
	return p
}

// TestAPILookupCLI_InputOverridesRequestCohort pins #308: `-i` is the
// cohort, as its help text says, even when the request file names
// another one (and a data_dir that would otherwise prefix it).
func TestAPILookupCLI_InputOverridesRequestCohort(t *testing.T) {
	cohort := lookupCLIFixture(t, "id")
	req := writeReqFile(t, `{"cohort":{"filename":"elsewhere.pulse","data_dir":"/nonexistent"},`+
		`"keys":[{"field":"id","value":"2"}],"return_columns":["region"]}`)

	var buf bytes.Buffer
	if err := runAPICLI(t, &buf, "lookup", "-i", cohort, "-r", req, "--json"); err != nil {
		t.Fatalf("run: %v\nraw: %s", err, buf.String())
	}
	var env lookupEnvelope
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("json.Unmarshal: %v\nraw: %s", err, buf.String())
	}
	if len(env.Errors) != 0 {
		t.Fatalf("errors = %v, want none (the -i cohort must be read)", env.Errors)
	}
	if len(env.Data.Rows) != 1 || env.Data.Rows[0]["region"] != "south" {
		t.Errorf("rows = %+v, want one row with region=south", env.Data.Rows)
	}
}

// TestAPILookupCLI_RequestCohortUsedWithoutInput: absent -i, the
// request file's cohort still drives the run.
func TestAPILookupCLI_RequestCohortUsedWithoutInput(t *testing.T) {
	cohort := lookupCLIFixture(t, "id")
	req := writeReqFile(t, `{"cohort":{"filename":"`+cohort+`"},`+
		`"keys":[{"field":"id","value":"2"}],"return_columns":["region"]}`)

	var buf bytes.Buffer
	if err := runAPICLI(t, &buf, "lookup", "-r", req, "--json"); err != nil {
		t.Fatalf("run: %v\nraw: %s", err, buf.String())
	}
	var env lookupEnvelope
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("json.Unmarshal: %v\nraw: %s", err, buf.String())
	}
	if len(env.Errors) != 0 || len(env.Data.Rows) != 1 {
		t.Fatalf("env = %+v, want one row and no errors", env)
	}
}

// TestAPIFacetCLI_InputOverridesRequestCohort is lookup's twin: the
// facet leaf shares the "flags layer on top of the file" precedence.
func TestAPIFacetCLI_InputOverridesRequestCohort(t *testing.T) {
	cohort := lookupCLIFixture(t)
	req := writeReqFile(t, `{"cohort":{"filename":"elsewhere.pulse"},"fields":["region"]}`)

	var buf bytes.Buffer
	if err := runAPICLI(t, &buf, "facet", "-i", cohort, "-r", req, "--json"); err != nil {
		t.Fatalf("run: %v\nraw: %s", err, buf.String())
	}
	var env struct {
		Errors []map[string]any `json:"errors"`
	}
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("json.Unmarshal: %v\nraw: %s", err, buf.String())
	}
	if len(env.Errors) != 0 {
		t.Fatalf("errors = %v, want none (the -i cohort must be read)", env.Errors)
	}
}

// runCohortCLIOut drives CohortCommand capturing stdout.
func runCohortCLIOut(t *testing.T, buf *bytes.Buffer, args ...string) error {
	t.Helper()
	root := CohortCommand()
	root.Writer = buf
	return root.Run(context.Background(), append([]string{"cohort"}, args...))
}

// TestCohortInspectCLI_InputFlagAndPositional pins #308: `cohort
// inspect` takes -i/--input like every other leaf, and the positional
// PATH still works; both read the same cohort.
func TestCohortInspectCLI_InputFlagAndPositional(t *testing.T) {
	cohort := lookupCLIFixture(t)
	for name, args := range map[string][]string{
		"short":      {"inspect", "-i", cohort, "--json"},
		"long":       {"inspect", "--input", cohort, "--json"},
		"positional": {"inspect", cohort, "--json"},
	} {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := runCohortCLIOut(t, &buf, args...); err != nil {
				t.Fatalf("run: %v\nraw: %s", err, buf.String())
			}
			var env struct {
				Data struct {
					Fields []map[string]any `json:"fields"`
				} `json:"data"`
				Errors []map[string]any `json:"errors"`
			}
			if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
				t.Fatalf("json.Unmarshal: %v\nraw: %s", err, buf.String())
			}
			if len(env.Errors) != 0 || len(env.Data.Fields) != 2 {
				t.Fatalf("env = %+v, want the 2-field cohort and no errors", env)
			}
		})
	}
}

// TestCohortInspectCLI_MissingPathIsCliInput: neither -i nor a
// positional is a coded CLI_INPUT envelope, not a bare usage string.
func TestCohortInspectCLI_MissingPathIsCliInput(t *testing.T) {
	var buf bytes.Buffer
	err := runCohortCLIOut(t, &buf, "inspect", "--json")
	if err == nil {
		t.Fatal("want an error for a missing path")
	}
	var env struct {
		Errors []struct {
			Code string `json:"code"`
		} `json:"errors"`
	}
	if jerr := json.Unmarshal(buf.Bytes(), &env); jerr != nil {
		t.Fatalf("json.Unmarshal: %v\nraw: %s", jerr, buf.String())
	}
	if len(env.Errors) != 1 || env.Errors[0].Code != "CLI_INPUT" {
		t.Errorf("errors = %+v, want one CLI_INPUT", env.Errors)
	}
}
