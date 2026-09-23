package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
)

// CLI coverage for the `pulse widen` sidecar hint.
//
// A widen changes the cohort's byte length, so a point-lookup index and
// an SPSS metadata sidecar beside it both self-invalidate through their
// own size fingerprints — neither will serve stale data. Nothing told
// the user, though, so a corpus simply stopped answering lookups. The
// leaf now names what went stale and the command that rebuilds it, and
// rebuilds NOTHING: an automatic rebuild would turn a fast metadata
// operation into an arbitrarily long one.

// widenSidecarEnvelope decodes the envelope subset these tests assert.
type widenSidecarEnvelope struct {
	Data struct {
		Cohort              string               `json:"cohort"`
		InvalidatedSidecars []pulse.StaleSidecar `json:"invalidated_sidecars"`
	} `json:"data"`
	Errors []map[string]any `json:"errors"`
}

// buildWidenIndex builds a point-lookup index over the id column of the
// cohort at path, through the real `pulse index build` leaf so the
// discovery manifest is written exactly as a user would produce it.
func buildWidenIndex(t *testing.T, cohort string) {
	t.Helper()
	var buf bytes.Buffer
	root := IndexCommand()
	root.Writer = &buf
	if err := root.Run(t.Context(), []string{"index", "build", cohort, "--key", "id"}); err != nil {
		t.Fatalf("index build: %v (%s)", err, buf.String())
	}
}

// With no sidecars beside the cohort the leaf must print NOTHING extra.
// An empty section is noise on the overwhelmingly common path.
func TestWidenCLI_NoSidecarsPrintsNoHint(t *testing.T) {
	dir := t.TempDir()
	cohort := filepath.Join(dir, "cohort.pulse")
	writeWidenCliCohort(t, cohort)

	var buf bytes.Buffer
	if err := runWidenCLI(t, &buf, cohort, "--field", "picks", "--to", "set_u128"); err != nil {
		t.Fatalf("run: %v", err)
	}
	// Assert on the lines BELOW the result line: the temp dir carries
	// the test's own name, which contains "Sidecar".
	out := buf.String()
	_, tail, _ := strings.Cut(out, "\n")
	if strings.TrimSpace(tail) != "" {
		t.Errorf("text output printed a hint section with no sidecars present:\n%s", tail)
	}
	for _, absent := range []string{"Invalidated sidecars", "not rebuilt", "pulse index build"} {
		if strings.Contains(out, absent) {
			t.Errorf("text output %q mentions %q with no sidecars present", out, absent)
		}
	}
}

// With an index beside the cohort, the text arm names the sidecar, says
// it was NOT rebuilt, and prints the exact rebuild command including the
// key tuple — which the sidecar's hashed filename cannot yield.
func TestWidenCLI_TextArmPrintsTheSidecarHint(t *testing.T) {
	dir := t.TempDir()
	cohort := filepath.Join(dir, "cohort.pulse")
	writeWidenCliCohort(t, cohort)
	buildWidenIndex(t, cohort)

	var buf bytes.Buffer
	if err := runWidenCLI(t, &buf, cohort, "--field", "picks", "--to", "set_u128"); err != nil {
		t.Fatalf("run: %v", err)
	}
	out := buf.String()
	for _, want := range []string{".idx", "not rebuilt", "pulse index build", "--key id"} {
		if !strings.Contains(out, want) {
			t.Errorf("text output does not contain %q:\n%s", want, out)
		}
	}
}

// The --json arm carries the hint as STRUCTURED data in the envelope,
// not as prose: a consumer must not have to parse a sentence to learn
// which file to rebuild.
func TestWidenCLI_JSONArmCarriesTheSidecarHintAsData(t *testing.T) {
	dir := t.TempDir()
	cohort := filepath.Join(dir, "cohort.pulse")
	writeWidenCliCohort(t, cohort)
	buildWidenIndex(t, cohort)

	var buf bytes.Buffer
	if err := runWidenCLI(t, &buf, cohort, "--field", "picks", "--to", "set_u128", "--json"); err != nil {
		t.Fatalf("run: %v", err)
	}
	var env widenSidecarEnvelope
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("decoding envelope %q: %v", buf.String(), err)
	}
	if len(env.Errors) != 0 {
		t.Fatalf("errors = %v, want none", env.Errors)
	}
	if len(env.Data.InvalidatedSidecars) != 1 {
		t.Fatalf("invalidated_sidecars = %+v, want exactly one", env.Data.InvalidatedSidecars)
	}
	s := env.Data.InvalidatedSidecars[0]
	if s.Kind != pulse.SidecarKindPointLookupIndex {
		t.Errorf("kind = %q, want %q", s.Kind, pulse.SidecarKindPointLookupIndex)
	}
	if !strings.HasSuffix(s.Path, ".idx") {
		t.Errorf("path = %q, want the .idx sidecar", s.Path)
	}
	if strings.Join(s.Keys, ",") != "id" {
		t.Errorf("keys = %v, want [id]", s.Keys)
	}
	if !strings.Contains(s.Rebuild, "pulse index build") || !strings.Contains(s.Rebuild, "--key id") {
		t.Errorf("rebuild = %q, want a runnable index build naming the key tuple", s.Rebuild)
	}
}

// The key must be OMITTED, not emitted empty, when there is nothing to
// report — the same rule the text arm follows.
func TestWidenCLI_JSONArmOmitsTheHintWhenThereAreNoSidecars(t *testing.T) {
	dir := t.TempDir()
	cohort := filepath.Join(dir, "cohort.pulse")
	writeWidenCliCohort(t, cohort)

	var buf bytes.Buffer
	if err := runWidenCLI(t, &buf, cohort, "--field", "picks", "--to", "set_u128", "--json"); err != nil {
		t.Fatalf("run: %v", err)
	}
	var raw struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(buf.Bytes(), &raw); err != nil {
		t.Fatalf("decoding envelope %q: %v", buf.String(), err)
	}
	if _, present := raw.Data["invalidated_sidecars"]; present {
		t.Errorf("data carries invalidated_sidecars with no sidecars present: %s", buf.String())
	}
}

// The hint must NOT rebuild anything. That is the decision, not an
// oversight: rebuilding is unbounded work attached to a bounded
// operation, and the user did not ask for it.
func TestWidenCLI_HintDoesNotRebuildTheIndex(t *testing.T) {
	dir := t.TempDir()
	cohort := filepath.Join(dir, "cohort.pulse")
	writeWidenCliCohort(t, cohort)
	buildWidenIndex(t, cohort)

	entries, err := filepath.Glob(filepath.Join(dir, "*.idx"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("glob before widen = %v (%v), want exactly one index", entries, err)
	}
	beforeStat, err := os.Stat(entries[0])
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	beforeBytes, err := os.ReadFile(entries[0])
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	var buf bytes.Buffer
	if err := runWidenCLI(t, &buf, cohort, "--field", "picks", "--to", "set_u128"); err != nil {
		t.Fatalf("run: %v", err)
	}

	afterBytes, err := os.ReadFile(entries[0])
	if err != nil {
		t.Fatalf("ReadFile after widen: %v", err)
	}
	if !bytes.Equal(beforeBytes, afterBytes) {
		t.Error("the widen rewrote the sidecar index; the hint must report, never rebuild")
	}
	afterStat, err := os.Stat(entries[0])
	if err != nil {
		t.Fatalf("Stat after widen: %v", err)
	}
	if afterStat.Size() != beforeStat.Size() {
		t.Errorf("index size moved %d -> %d", beforeStat.Size(), afterStat.Size())
	}
}
