package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
)

// runAPICLI drives a fresh APICommand with the supplied args, capturing
// stdout into buf so --json assertions can decode it. Mirrors
// runIndexCLI (index_test.go) for the sibling `pulse api` command group.
func runAPICLI(t *testing.T, buf *bytes.Buffer, args ...string) error {
	t.Helper()
	root := APICommand()
	root.Writer = buf
	return root.Run(context.Background(), append([]string{"api"}, args...))
}

// lookupCLIFixture writes the shared "id"/"region" test cohort
// (writeIndexTestCohort, index_test.go — id=1..3, region north/south/
// north with a deliberate duplicate on "north") and builds the
// requested sidecar index(es) over it via `pulse index build`,
// returning the cohort path.
func lookupCLIFixture(t *testing.T, keySpecs ...string) string {
	t.Helper()
	dir := t.TempDir()
	cohort := filepath.Join(dir, "cohort.pulse")
	writeIndexTestCohort(t, cohort)

	for _, keys := range keySpecs {
		var buildBuf bytes.Buffer
		if err := runIndexCLI(t, &buildBuf, "build", "--input", cohort, "--key", keys); err != nil {
			t.Fatalf("index build --key %s: %v", keys, err)
		}
	}
	return cohort
}

type lookupEnvelope struct {
	FormatVersion string           `json:"format_version"`
	Data          lookupResultJSON `json:"data"`
	Errors        []lookupErrJSON  `json:"errors"`
	Warnings      []map[string]any `json:"warnings"`
}

type lookupResultJSON struct {
	Rows []map[string]any `json:"rows"`
}

type lookupErrJSON struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details"`
}

func TestAPILookupCLI_HitReturnsSelectedColumns(t *testing.T) {
	cohort := lookupCLIFixture(t, "id")

	var buf bytes.Buffer
	if err := runAPICLI(t, &buf, "lookup", "--input", cohort, "--key", "id=2", "--return", "region", "--json"); err != nil {
		t.Fatalf("run: %v", err)
	}

	var env lookupEnvelope
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("json.Unmarshal: %v\nraw: %s", err, buf.String())
	}
	if env.FormatVersion != "1.1" {
		t.Errorf("format_version = %q, want %q", env.FormatVersion, "1.1")
	}
	if len(env.Errors) != 0 {
		t.Fatalf("errors = %v, want none", env.Errors)
	}
	if len(env.Data.Rows) != 1 {
		t.Fatalf("rows = %+v, want exactly 1", env.Data.Rows)
	}
	row := env.Data.Rows[0]
	if len(row) != 1 {
		t.Fatalf("row = %+v, want exactly 1 projected column (region)", row)
	}
	if got, ok := row["region"]; !ok || got != "south" {
		t.Errorf("region = %v, want %q (id=2 -> south)", got, "south")
	}
}

func TestAPILookupCLI_MissErrorEnvelope(t *testing.T) {
	cohort := lookupCLIFixture(t, "id")

	var buf bytes.Buffer
	err := runAPICLI(t, &buf, "lookup", "--input", cohort, "--key", "id=999", "--json")
	if err != nil {
		t.Fatalf("run (json mode should not return a Go error): %v", err)
	}

	var env lookupEnvelope
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("json.Unmarshal: %v\nraw: %s", err, buf.String())
	}
	if len(env.Errors) != 1 {
		t.Fatalf("errors = %v, want exactly 1 entry", env.Errors)
	}
	if env.Errors[0].Code != "PULSE_LOOKUP_NOT_FOUND" {
		t.Errorf("errors[0].code = %q, want PULSE_LOOKUP_NOT_FOUND", env.Errors[0].Code)
	}
}

func TestAPILookupCLI_AmbiguousDefaultModeErrorEnvelope(t *testing.T) {
	cohort := lookupCLIFixture(t, "region")

	var buf bytes.Buffer
	// region "north" matches id=1 and id=3 (writeIndexTestCohort);
	// default multiplicity mode (assert-unique) must reject this.
	err := runAPICLI(t, &buf, "lookup", "--input", cohort, "--key", "region=north", "--json")
	if err != nil {
		t.Fatalf("run (json mode should not return a Go error): %v", err)
	}

	var env lookupEnvelope
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("json.Unmarshal: %v\nraw: %s", err, buf.String())
	}
	if len(env.Errors) != 1 {
		t.Fatalf("errors = %v, want exactly 1 entry", env.Errors)
	}
	if env.Errors[0].Code != "PULSE_LOOKUP_AMBIGUOUS" {
		t.Errorf("errors[0].code = %q, want PULSE_LOOKUP_AMBIGUOUS", env.Errors[0].Code)
	}
}

func TestAPILookupCLI_ModeFirstReturnsLowestRowID(t *testing.T) {
	cohort := lookupCLIFixture(t, "region")

	var buf bytes.Buffer
	if err := runAPICLI(t, &buf, "lookup", "--input", cohort, "--key", "region=north", "--return", "id", "--mode", "first", "--json"); err != nil {
		t.Fatalf("run: %v", err)
	}

	var env lookupEnvelope
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("json.Unmarshal: %v\nraw: %s", err, buf.String())
	}
	if len(env.Errors) != 0 {
		t.Fatalf("errors = %v, want none", env.Errors)
	}
	if len(env.Data.Rows) != 1 {
		t.Fatalf("rows = %+v, want exactly 1 (mode=first)", env.Data.Rows)
	}
	if got := env.Data.Rows[0]["id"]; got != float64(1) {
		t.Errorf("id = %v, want 1 (lowest row-id on the north duplicate)", got)
	}
}

func TestAPILookupCLI_ModeAllReturnsEveryMatch(t *testing.T) {
	cohort := lookupCLIFixture(t, "region")

	var buf bytes.Buffer
	if err := runAPICLI(t, &buf, "lookup", "--input", cohort, "--key", "region=north", "--return", "id", "--mode", "all", "--json"); err != nil {
		t.Fatalf("run: %v", err)
	}

	var env lookupEnvelope
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("json.Unmarshal: %v\nraw: %s", err, buf.String())
	}
	if len(env.Errors) != 0 {
		t.Fatalf("errors = %v, want none", env.Errors)
	}
	if len(env.Data.Rows) != 2 {
		t.Fatalf("rows = %+v, want exactly 2 (mode=all, ids 1 and 3)", env.Data.Rows)
	}
	if got := env.Data.Rows[0]["id"]; got != float64(1) {
		t.Errorf("rows[0].id = %v, want 1", got)
	}
	if got := env.Data.Rows[1]["id"]; got != float64(3) {
		t.Errorf("rows[1].id = %v, want 3", got)
	}
}

func TestAPILookupCLI_CompositeKeyHit(t *testing.T) {
	cohort := lookupCLIFixture(t, "region,id")

	var buf bytes.Buffer
	if err := runAPICLI(t, &buf, "lookup", "--input", cohort,
		"--key", "region=north", "--key", "id=3",
		"--return", "region,id", "--json"); err != nil {
		t.Fatalf("run: %v", err)
	}

	var env lookupEnvelope
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("json.Unmarshal: %v\nraw: %s", err, buf.String())
	}
	if len(env.Errors) != 0 {
		t.Fatalf("errors = %v, want none", env.Errors)
	}
	if len(env.Data.Rows) != 1 {
		t.Fatalf("rows = %+v, want exactly 1", env.Data.Rows)
	}
	row := env.Data.Rows[0]
	if got := row["region"]; got != "north" {
		t.Errorf("region = %v, want north", got)
	}
	if got := row["id"]; got != float64(3) {
		t.Errorf("id = %v, want 3", got)
	}
}

func TestAPILookupCLI_MissingIndexErrorEnvelope(t *testing.T) {
	dir := t.TempDir()
	cohort := filepath.Join(dir, "cohort.pulse")
	writeIndexTestCohort(t, cohort)

	var buf bytes.Buffer
	err := runAPICLI(t, &buf, "lookup", "--input", cohort, "--key", "id=1", "--json")
	if err != nil {
		t.Fatalf("run (json mode should not return a Go error): %v", err)
	}

	var env lookupEnvelope
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("json.Unmarshal: %v\nraw: %s", err, buf.String())
	}
	if len(env.Errors) != 1 {
		t.Fatalf("errors = %v, want exactly 1 entry", env.Errors)
	}
	if env.Errors[0].Code != "PULSE_INDEX_MISSING" {
		t.Errorf("errors[0].code = %q, want PULSE_INDEX_MISSING", env.Errors[0].Code)
	}
}

func TestAPILookupCLI_MissingKeyFlagErrors(t *testing.T) {
	cohort := lookupCLIFixture(t, "id")

	var buf bytes.Buffer
	err := runAPICLI(t, &buf, "lookup", "--input", cohort)
	if err == nil {
		t.Fatal("expected non-JSON error for missing --key")
	}

	buf.Reset()
	err = runAPICLI(t, &buf, "lookup", "--input", cohort, "--json")
	if err != nil {
		t.Fatalf("run (json mode should not return a Go error): %v", err)
	}
	var env lookupEnvelope
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("json.Unmarshal: %v\nraw: %s", err, buf.String())
	}
	if len(env.Errors) != 1 || env.Errors[0].Code != "CLI_INPUT" {
		t.Errorf("errors = %v, want exactly 1 CLI_INPUT entry", env.Errors)
	}
}

func TestAPILookupCLI_InvalidModeFlagErrors(t *testing.T) {
	cohort := lookupCLIFixture(t, "id")

	var buf bytes.Buffer
	err := runAPICLI(t, &buf, "lookup", "--input", cohort, "--key", "id=1", "--mode", "bogus", "--json")
	if err != nil {
		t.Fatalf("run (json mode should not return a Go error): %v", err)
	}
	var env lookupEnvelope
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("json.Unmarshal: %v\nraw: %s", err, buf.String())
	}
	if len(env.Errors) != 1 || env.Errors[0].Code != "CLI_INPUT" {
		t.Errorf("errors = %v, want exactly 1 CLI_INPUT entry", env.Errors)
	}
}

func TestAPILookupCLI_EchoRequestIncludesResolvedRequest(t *testing.T) {
	cohort := lookupCLIFixture(t, "id")

	var buf bytes.Buffer
	if err := runAPICLI(t, &buf, "lookup", "--input", cohort, "--key", "id=2", "--json", "--echo-request"); err != nil {
		t.Fatalf("run: %v", err)
	}

	var env struct {
		Request struct {
			Keys []struct {
				Field string `json:"field"`
				Value string `json:"value"`
			} `json:"keys"`
		} `json:"request"`
	}
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("json.Unmarshal: %v\nraw: %s", err, buf.String())
	}
	if len(env.Request.Keys) != 1 || env.Request.Keys[0].Field != "id" || env.Request.Keys[0].Value != "2" {
		t.Errorf("request.keys = %+v, want [{id 2}]", env.Request.Keys)
	}
}

func TestAPILookupCLI_ShardArchiveRejected(t *testing.T) {
	dir := t.TempDir()
	shard1 := filepath.Join(dir, "shard1.pulse")
	shard2 := filepath.Join(dir, "shard2.pulse")
	writeIndexTestCohort(t, shard1)
	writeIndexTestCohort(t, shard2)

	archive := filepath.Join(dir, "archive.pulse")
	p, err := newPulse()
	if err != nil {
		t.Fatalf("newPulse: %v", err)
	}
	if err := p.CreateShardArchive(context.Background(), archive, []string{shard1, shard2}); err != nil {
		t.Fatalf("CreateShardArchive: %v", err)
	}

	var buf bytes.Buffer
	err = runAPICLI(t, &buf, "lookup", "--input", archive, "--key", "id=1", "--json")
	if err != nil {
		t.Fatalf("run (json mode should not return a Go error): %v", err)
	}
	var env lookupEnvelope
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("json.Unmarshal: %v\nraw: %s", err, buf.String())
	}
	if len(env.Errors) != 1 {
		t.Fatalf("errors = %v, want exactly 1 entry", env.Errors)
	}
	if env.Errors[0].Code != "PULSE_INDEX_UNSUPPORTED_SHARDED" {
		t.Errorf("errors[0].code = %q, want PULSE_INDEX_UNSUPPORTED_SHARDED", env.Errors[0].Code)
	}
}
