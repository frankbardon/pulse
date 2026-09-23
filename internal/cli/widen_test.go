package cli

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/frankbardon/pulse/encoding"
)

// CLI coverage for `pulse widen`. newPulse() wires afero.NewOsFs() with no
// DataDir, so these drive real files under t.TempDir() exactly as the binary
// does. The assertions that matter here are the two the leaf owns: the
// envelope shape on success, and — the criterion this file exists for — that
// a fatal coded error reaches errors[0].code with its OWN code, so
// `pulse errors lookup` works on whatever the user just saw.

const widenCliSetOptions = 40

// writeWidenCliCohort writes a two-field cohort (id u32, picks set_u64) at
// path and returns its bytes, for byte-identity assertions after a refusal.
func writeWidenCliCohort(t *testing.T, path string) []byte {
	t.Helper()

	dict := encoding.NewDictionary()
	for i := 0; i < widenCliSetOptions; i++ {
		if _, err := dict.Add(fmt.Sprintf("opt%02d", i)); err != nil {
			t.Fatalf("dict.Add(%d): %v", i, err)
		}
	}
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU32, ByteOffset: 0},
		{Name: "picks", Type: encoding.FieldTypeSetU64, ByteOffset: 4, Dictionary: dict},
	}}

	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}
	for r := 0; r < 3; r++ {
		var id [4]byte
		binary.LittleEndian.PutUint32(id[:], uint32(1+r))
		buf.Write(id[:])
		var word uint64
		for _, bit := range []int{r, 20 + r, 39} {
			word |= 1 << uint(bit)
		}
		var payload [8]byte
		binary.LittleEndian.PutUint64(payload[:], word)
		buf.Write(payload[:])
	}

	data := buf.Bytes()
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("WriteFile cohort: %v", err)
	}
	return data
}

// runWidenCLI drives a fresh WidenCommand, capturing stdout into buf.
func runWidenCLI(t *testing.T, buf *bytes.Buffer, args ...string) error {
	t.Helper()
	root := WidenCommand()
	root.Writer = buf
	return root.Run(context.Background(), append([]string{"widen"}, args...))
}

// widenEnvelope is the envelope subset these tests assert over.
type widenEnvelope struct {
	FormatVersion string `json:"format_version"`
	Data          struct {
		Cohort       string `json:"cohort"`
		Field        string `json:"field"`
		From         string `json:"from"`
		To           string `json:"to"`
		Records      int64  `json:"records"`
		StrideBefore int    `json:"stride_before"`
		StrideAfter  int    `json:"stride_after"`
	} `json:"data"`
	Errors []struct {
		Code    string         `json:"code"`
		Message string         `json:"message"`
		Details map[string]any `json:"details"`
	} `json:"errors"`
}

func decodeWidenEnvelope(t *testing.T, buf *bytes.Buffer) widenEnvelope {
	t.Helper()
	var env widenEnvelope
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("decoding envelope %q: %v", buf.String(), err)
	}
	return env
}

func TestWidenCLI_WidensCohortInPlace(t *testing.T) {
	dir := t.TempDir()
	cohort := filepath.Join(dir, "cohort.pulse")
	before := writeWidenCliCohort(t, cohort)

	var buf bytes.Buffer
	if err := runWidenCLI(t, &buf, cohort, "--field", "picks", "--to", "set_u128"); err != nil {
		t.Fatalf("run: %v", err)
	}

	after, err := os.ReadFile(cohort)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if bytes.Equal(before, after) {
		t.Fatal("cohort bytes unchanged after a successful widen")
	}
	r := bytes.NewReader(after)
	if err := encoding.ReadHeader(r); err != nil {
		t.Fatalf("widened cohort no longer carries a valid header: %v", err)
	}
	schema, err := encoding.ReadSchema(r)
	if err != nil {
		t.Fatalf("ReadSchema: %v", err)
	}
	if got := schema.Fields[1].Type; got != encoding.FieldTypeSetU128 {
		t.Errorf("picks type after widen = %s, want set_u128", got)
	}
	if got := buf.String(); !bytes.Contains([]byte(got), []byte("set_u64 -> set_u128")) {
		t.Errorf("text output %q does not name the rung transition", got)
	}
}

func TestWidenCLI_JSONEnvelopeReportsTheRewrite(t *testing.T) {
	dir := t.TempDir()
	cohort := filepath.Join(dir, "cohort.pulse")
	writeWidenCliCohort(t, cohort)

	var buf bytes.Buffer
	if err := runWidenCLI(t, &buf, cohort, "--field", "picks", "--to", "set_u128", "--json"); err != nil {
		t.Fatalf("run: %v", err)
	}

	env := decodeWidenEnvelope(t, &buf)
	if env.FormatVersion != "1.1" {
		t.Errorf("format_version = %q, want 1.1", env.FormatVersion)
	}
	if len(env.Errors) != 0 {
		t.Errorf("errors = %v, want empty on success", env.Errors)
	}
	if env.Data.Field != "picks" || env.Data.From != "set_u64" || env.Data.To != "set_u128" {
		t.Errorf("data identities = {%s %s->%s}, want {picks set_u64->set_u128}",
			env.Data.Field, env.Data.From, env.Data.To)
	}
	if env.Data.Records != 3 {
		t.Errorf("data.records = %d, want 3", env.Data.Records)
	}
	if env.Data.StrideAfter-env.Data.StrideBefore != 8 {
		t.Errorf("stride delta = %d, want 8", env.Data.StrideAfter-env.Data.StrideBefore)
	}
}

// The criterion: a fatal *errors.CodedError surfaces its OWN code, not the
// WIDEN_ERROR placeholder. A stringified code is unusable with
// `pulse errors lookup`, which is the only reason the envelope carries a
// code field at all.
func TestWidenCLI_JSONRefusalsCarryTheirOwnCode(t *testing.T) {
	cases := []struct {
		name     string
		field    string
		to       string
		wantCode string
		// wantDetails, when non-empty, is asserted against errors[0].details.
		// The unknown-type-name row needs it: with the facade's name guard
		// removed the unresolved name still lands on ENCODING_TYPE_MISMATCH
		// via CheckSetWiden, so the CODE alone cannot tell a user's typo from
		// a legitimately non-set target. The details map is what names the
		// string they actually typed.
		wantDetails map[string]any
	}{
		{"narrower target", "picks", "set_u16", "ENCODING_TYPE_MISMATCH", map[string]any{"to": "set_u16"}},
		{"equal target", "picks", "set_u64", "ENCODING_TYPE_MISMATCH", nil},
		{"non-set field", "id", "set_u128", "ENCODING_TYPE_MISMATCH", nil},
		{"unknown type name", "picks", "set_u512", "ENCODING_TYPE_MISMATCH", map[string]any{"target": "set_u512"}},
		{"missing field", "absent", "set_u128", "ENCODING_INVALID", map[string]any{"field": "absent"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			cohort := filepath.Join(dir, "cohort.pulse")
			before := writeWidenCliCohort(t, cohort)

			var buf bytes.Buffer
			if err := runWidenCLI(t, &buf, cohort, "--field", tc.field, "--to", tc.to, "--json"); err != nil {
				t.Fatalf("run returned a hard error instead of an envelope: %v", err)
			}
			env := decodeWidenEnvelope(t, &buf)
			if len(env.Errors) != 1 {
				t.Fatalf("errors = %v, want exactly one", env.Errors)
			}
			if env.Errors[0].Code != tc.wantCode {
				t.Errorf("errors[0].code = %q, want %q (a placeholder here makes `pulse errors lookup` useless)",
					env.Errors[0].Code, tc.wantCode)
			}
			if env.Errors[0].Message == "" {
				t.Error("errors[0].message is empty")
			}
			for k, want := range tc.wantDetails {
				if got := env.Errors[0].Details[k]; got != want {
					t.Errorf("errors[0].details[%q] = %v, want %v", k, got, want)
				}
			}

			after, err := os.ReadFile(cohort)
			if err != nil {
				t.Fatalf("ReadFile: %v", err)
			}
			if !bytes.Equal(before, after) {
				t.Error("cohort bytes changed under a refused widen")
			}
		})
	}
}

func TestWidenCLI_MissingCohortIsCoded(t *testing.T) {
	dir := t.TempDir()
	var buf bytes.Buffer
	if err := runWidenCLI(t, &buf, filepath.Join(dir, "nope.pulse"),
		"--field", "picks", "--to", "set_u128", "--json"); err != nil {
		t.Fatalf("run returned a hard error instead of an envelope: %v", err)
	}
	env := decodeWidenEnvelope(t, &buf)
	if len(env.Errors) != 1 || env.Errors[0].Code != "SERVICE_RESOURCE" {
		t.Errorf("errors = %v, want one SERVICE_RESOURCE entry", env.Errors)
	}
}

// Pointed at a shard archive the leaf must refuse with a clear coded error
// and leave the archive byte-identical — treating a Zip64 archive as a
// single-file cohort is the one failure mode that would destroy data.
func TestWidenCLI_RefusesShardArchiveWithoutTouchingIt(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "archive.pulse")

	shardA := filepath.Join(dir, "a.pulse")
	shardB := filepath.Join(dir, "b.pulse")
	writeWidenCliCohort(t, shardA)
	writeWidenCliCohort(t, shardB)

	p, err := newPulse()
	if err != nil {
		t.Fatalf("newPulse: %v", err)
	}
	if err := p.CreateShardArchive(context.Background(), archive, []string{shardA, shardB}); err != nil {
		t.Fatalf("CreateShardArchive: %v", err)
	}
	before, err := os.ReadFile(archive)
	if err != nil {
		t.Fatalf("ReadFile archive: %v", err)
	}
	if !bytes.HasPrefix(before, []byte{'P', 'K', 0x03, 0x04}) {
		t.Fatalf("fixture is not a zip archive: prefix %v", before[:4])
	}

	var buf bytes.Buffer
	if err := runWidenCLI(t, &buf, archive, "--field", "picks", "--to", "set_u128", "--json"); err != nil {
		t.Fatalf("run returned a hard error instead of an envelope: %v", err)
	}
	env := decodeWidenEnvelope(t, &buf)
	if len(env.Errors) != 1 {
		t.Fatalf("errors = %v, want exactly one", env.Errors)
	}
	if env.Errors[0].Code != "SERVICE_VALIDATION" {
		t.Errorf("errors[0].code = %q, want SERVICE_VALIDATION", env.Errors[0].Code)
	}

	after, err := os.ReadFile(archive)
	if err != nil {
		t.Fatalf("ReadFile archive after refusal: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("shard archive bytes changed under a refused widen")
	}
	shards, err := p.ListShards(context.Background(), archive)
	if err != nil {
		t.Fatalf("archive no longer lists after a refused widen: %v", err)
	}
	if len(shards) != 2 {
		t.Errorf("archive has %d shards after a refused widen, want 2", len(shards))
	}
}

func TestWidenCLI_MissingPositionalIsCliInput(t *testing.T) {
	var buf bytes.Buffer
	if err := runWidenCLI(t, &buf, "--field", "picks", "--to", "set_u128", "--json"); err != nil {
		t.Fatalf("run returned a hard error instead of an envelope: %v", err)
	}
	env := decodeWidenEnvelope(t, &buf)
	if len(env.Errors) != 1 || env.Errors[0].Code != "CLI_INPUT" {
		t.Errorf("errors = %v, want one CLI_INPUT entry", env.Errors)
	}
}
