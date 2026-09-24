package cli

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/frankbardon/pulse/encoding"
)

// writeSetCohortFile writes a single-file .pulse with a u32 id and a
// set column at rung ft directly onto the OS filesystem — ShardCommand's
// leaves build their own Pulse over afero.NewOsFs().
func writeSetCohortFile(t *testing.T, path string, ft encoding.FieldType, dictValues []string, records [][2]uint64) {
	t.Helper()
	d := encoding.NewDictionary()
	for _, v := range dictValues {
		if _, err := d.Add(v); err != nil {
			t.Fatalf("dict add %q: %v", v, err)
		}
	}
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU32, ByteOffset: 0, CsvColumnIdx: 0},
		{Name: "opts", Type: ft, ByteOffset: 4, CsvColumnIdx: 1, Dictionary: d},
	}}
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}
	width := ft.ByteSize()
	for _, r := range records {
		rec := make([]byte, 4+width)
		binary.LittleEndian.PutUint32(rec[0:4], uint32(r[0]))
		var scratch [8]byte
		binary.LittleEndian.PutUint64(scratch[:], r[1])
		n := width
		if n > 8 {
			n = 8
		}
		copy(rec[4:], scratch[:n])
		buf.Write(rec)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("WriteFile %s: %v", path, err)
	}
}

func runShardCLI(t *testing.T, buf *bytes.Buffer, args ...string) error {
	t.Helper()
	root := ShardCommand()
	root.Writer = buf
	return root.Run(context.Background(), append([]string{"shard"}, args...))
}

// envelopeShape is the subset of descriptor.Envelope the shard leaves'
// --json assertions read.
type envelopeShape struct {
	FormatVersion string `json:"format_version"`
	Data          struct {
		SetWidthHeadroom []struct {
			Field    string `json:"field"`
			Type     string `json:"type"`
			Used     int    `json:"used"`
			Capacity int    `json:"capacity"`
			Headroom int    `json:"headroom"`
			NextType string `json:"next_type"`
		} `json:"set_width_headroom"`
		Widened []struct {
			Field           string `json:"field"`
			From            string `json:"from"`
			To              string `json:"to"`
			ShardsRewritten int    `json:"shards_rewritten"`
		} `json:"widened"`
	} `json:"data"`
	Warnings []struct {
		Code    string         `json:"code"`
		Message string         `json:"message"`
		Details map[string]any `json:"details"`
	} `json:"warnings"`
}

// shardWidenCLIFixture creates an archive whose set_u8 `opts` field
// holds five members, plus a staged shard contributing four more — a
// union of nine, one past the set_u8 ceiling.
func shardWidenCLIFixture(t *testing.T) (archive, add string) {
	t.Helper()
	dir := t.TempDir()
	seed := filepath.Join(dir, "seed.pulse")
	add = filepath.Join(dir, "add.pulse")
	archive = filepath.Join(dir, "arch.pulse")

	writeSetCohortFile(t, seed, encoding.FieldTypeSetU8,
		[]string{"tv", "radio", "print", "web", "mail"},
		[][2]uint64{{1, 0b00001}, {2, 0b00110}, {3, 0b11000}})
	writeSetCohortFile(t, add, encoding.FieldTypeSetU8,
		[]string{"podcast", "streaming", "sms", "outdoor"},
		[][2]uint64{{4, 0b0001}, {5, 0b1010}})

	var buf bytes.Buffer
	if err := runShardCLI(t, &buf, "create", "-i", seed, archive); err != nil {
		t.Fatalf("shard create: %v (output: %s)", err, buf.String())
	}
	return archive, add
}

// The widen warning must reach the --json envelope's `warnings` array —
// not be buried in `data`, where a generic envelope consumer never
// looks, and not be dropped entirely.
func TestCliShardAddJson_SurfacesTheMandatoryWidenWarning(t *testing.T) {
	archive, add := shardWidenCLIFixture(t)

	var buf bytes.Buffer
	if err := runShardCLI(t, &buf, "add", "--json", archive, add); err != nil {
		t.Fatalf("shard add: %v (output: %s)", err, buf.String())
	}

	var env envelopeShape
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("decoding envelope: %v\n%s", err, buf.String())
	}
	if env.FormatVersion != "1.1" {
		t.Errorf("format_version = %q, want 1.1", env.FormatVersion)
	}

	var found bool
	for _, w := range env.Warnings {
		if w.Code != "PULSE_SHARD_SET_WIDENED" {
			continue
		}
		found = true
		for k, want := range map[string]any{
			"field": "opts", "from": "set_u8", "to": "set_u16", "shards_rewritten": float64(2),
		} {
			if got := w.Details[k]; got != want {
				t.Errorf("warning details[%q] = %v, want %v", k, got, want)
			}
		}
	}
	if !found {
		t.Fatalf("no PULSE_SHARD_SET_WIDENED warning on the envelope: %s", buf.String())
	}

	if len(env.Data.Widened) != 1 || env.Data.Widened[0].To != "set_u16" {
		t.Errorf("data.widened = %+v, want one set_u8 -> set_u16 entry", env.Data.Widened)
	}
}

// The text path must print the warning too — a human running `shard add`
// without --json still has to learn the archive was re-laid-out.
func TestCliShardAddText_PrintsTheWidenWarning(t *testing.T) {
	archive, add := shardWidenCLIFixture(t)

	var buf bytes.Buffer
	if err := runShardCLI(t, &buf, "add", archive, add); err != nil {
		t.Fatalf("shard add: %v (output: %s)", err, buf.String())
	}
	out := buf.String()
	for _, want := range []string{"PULSE_SHARD_SET_WIDENED", "set_u8", "set_u16", "opts"} {
		if !bytes.Contains([]byte(out), []byte(want)) {
			t.Errorf("text output does not name %q: %s", want, out)
		}
	}
}

// `shard verify --json` carries per-set-field headroom so the widen is
// foreseeable before it is billed for.
func TestCliShardVerifyJson_ReportsSetWidthHeadroom(t *testing.T) {
	archive, _ := shardWidenCLIFixture(t)

	var buf bytes.Buffer
	if err := runShardCLI(t, &buf, "verify", "--json", archive); err != nil {
		t.Fatalf("shard verify: %v (output: %s)", err, buf.String())
	}
	var env envelopeShape
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("decoding envelope: %v\n%s", err, buf.String())
	}
	if len(env.Data.SetWidthHeadroom) != 1 {
		t.Fatalf("set_width_headroom = %+v, want one entry", env.Data.SetWidthHeadroom)
	}
	h := env.Data.SetWidthHeadroom[0]
	if h.Field != "opts" || h.Type != "set_u8" || h.Used != 5 || h.Capacity != 8 || h.Headroom != 3 {
		t.Errorf("headroom = %+v, want opts/set_u8 used=5 capacity=8 headroom=3", h)
	}
	if h.NextType != "set_u16" {
		t.Errorf("next_type = %q, want set_u16", h.NextType)
	}
}
