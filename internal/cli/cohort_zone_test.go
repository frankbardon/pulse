package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
)

// writeZoneCliCohort writes a flat cohort with a categorical `tz` of
// valid zones and a u8 `n`, returning its bytes.
func writeZoneCliCohort(t *testing.T, path string) []byte {
	t.Helper()
	d := encoding.NewDictionary()
	for _, z := range []string{"UTC", "Europe/Berlin"} {
		if _, err := d.Add(z); err != nil {
			t.Fatal(err)
		}
	}
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "tz", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 0, Dictionary: d},
		{Name: "n", Type: encoding.FieldTypeU8, ByteOffset: 1},
	}}
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatal(err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatal(err)
	}
	buf.Write([]byte{0, 7, 1, 9})
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func runCohortZoneCLI(t *testing.T, buf *bytes.Buffer, args ...string) error {
	t.Helper()
	root := CohortCommand()
	root.Writer = buf
	return root.Run(context.Background(), append([]string{"cohort", "zone"}, args...))
}

type cohortZoneEnvelope struct {
	FormatVersion string `json:"format_version"`
	Data          struct {
		Cohort            string `json:"cohort"`
		ZoneField         string `json:"zone_field"`
		PreviousZoneField string `json:"previous_zone_field"`
		Changed           bool   `json:"changed"`
		Layout            string `json:"layout"`
	} `json:"data"`
	Errors []struct {
		Code    string         `json:"code"`
		Details map[string]any `json:"details"`
	} `json:"errors"`
}

// TestCohortZoneCLI_SetClearRoundTrip: `pulse cohort zone set` then
// `clear` round-trip the cohort byte-for-byte through the facade, the
// envelope reporting both sides; a bad field reaches errors[0] with the
// facade's own SERVICE_VALIDATION (details.option zone_field), never
// re-coded CLI_INPUT, and leaves the cohort untouched.
func TestCohortZoneCLI_SetClearRoundTrip(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "c.pulse")
	orig := writeZoneCliCohort(t, path)
	decode := func(buf *bytes.Buffer) cohortZoneEnvelope {
		t.Helper()
		var env cohortZoneEnvelope
		if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
			t.Fatalf("decode %q: %v", buf.String(), err)
		}
		return env
	}

	var buf bytes.Buffer
	if err := runCohortZoneCLI(t, &buf, "set", "--json", path, "tz"); err != nil {
		t.Fatalf("set: %v", err)
	}
	env := decode(&buf)
	if env.FormatVersion != "1.1" || !env.Data.Changed || env.Data.ZoneField != "tz" || env.Data.Layout != "single_file" {
		t.Fatalf("set envelope %+v", env)
	}
	buf.Reset()
	// A fatal envelope is written and the error returned (exit 1).
	if err := runCohortZoneCLI(t, &buf, "set", "--json", path, "n"); err == nil {
		t.Fatal("set n succeeded")
	}
	env = decode(&buf)
	if len(env.Errors) != 1 || env.Errors[0].Code != "SERVICE_VALIDATION" || env.Errors[0].Details["option"] != "zone_field" {
		t.Fatalf("bad field envelope %+v", env)
	}
	buf.Reset()
	if err := runCohortZoneCLI(t, &buf, "clear", path); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if !strings.Contains(buf.String(), `Cleared the default zone field "tz"`) {
		t.Fatalf("clear text %q", buf.String())
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, orig) {
		t.Fatalf("set then clear is not byte-identical (%v)", err)
	}
	buf.Reset()
	_ = runCohortZoneCLI(t, &buf, "set", "--json", path)
	if env = decode(&buf); len(env.Errors) != 1 || env.Errors[0].Code != "CLI_INPUT" {
		t.Fatalf("missing field envelope %+v", env)
	}
}
