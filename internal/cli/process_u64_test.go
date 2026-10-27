package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/frankbardon/pulse/encoding"
)

// TestAPIProcessCLI_U64RecordRowsExact (E4-S6): `pulse api process
// --json` over a windowed request with Request.Sort prints a u64 record
// row cell as its exact integer — 2^53+1 and MaxUint64 keep their own
// digits — on a single file and on a shard archive, in today's
// (float64-echo) sort order; `--stream` NDJSON carries the same digits.
// A WIN_LAG copy of the id prints its source's exact digits (E4-S7).
func TestAPIProcessCLI_U64RecordRowsExact(t *testing.T) {
	// serial: t.Setenv mutates the process environment

	ids := []uint64{1<<53 - 1, 1<<53 + 1, math.MaxUint64, 7}
	// Sort id desc over the echo: MaxUint64, 2^53+1, 2^53-1, 7.
	want := []uint64{math.MaxUint64, 1<<53 + 1, 1<<53 - 1, 7}
	// WIN_LAG over id ascending, per want row ("" = null).
	wantPrev := []string{"9007199254740993", "9007199254740991", "7", ""}
	dir := withTempDataDir(t)
	write := func(name string, ids []uint64) {
		t.Helper()
		schema := &encoding.Schema{Fields: []encoding.Field{{Name: "id", Type: encoding.FieldTypeU64}}}
		var buf bytes.Buffer
		if err := encoding.WriteHeader(&buf); err != nil {
			t.Fatal(err)
		}
		if err := encoding.WriteSchema(&buf, schema); err != nil {
			t.Fatal(err)
		}
		for _, id := range ids {
			if err := encoding.WriteFieldValue(&buf, encoding.FieldTypeU64, id); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(dir, name), buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("single.pulse", ids)
	write("s0.pulse", ids[:2])
	write("s1.pulse", ids[2:])
	p, err := newPulse(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.CreateShardArchive(context.Background(), filepath.Join(dir, "archive.pulse"), []string{filepath.Join(dir, "s0.pulse"), filepath.Join(dir, "s1.pulse")}); err != nil {
		t.Fatal(err)
	}
	for _, cohort := range []string{"single.pulse", "archive.pulse"} {
		b, err := json.Marshal(map[string]any{
			"cohort": map[string]any{"filename": filepath.Join(dir, cohort)},
			"windows": []any{
				map[string]any{"type": "WIN_ROW_NUMBER", "label": "rn", "order_by": []any{map[string]any{"field": "id"}}},
				// E4-S7: a lag copy of the id renders its source's exact word.
				map[string]any{"type": "WIN_LAG", "field": "id", "label": "prev", "order_by": []any{map[string]any{"field": "id"}}},
			},
			"sort": []any{map[string]any{"field": "id", "desc": true}},
		})
		if err != nil {
			t.Fatal(err)
		}
		reqPath := filepath.Join(dir, "req.json")
		if err := os.WriteFile(reqPath, b, 0o644); err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer
		if err := runAPICLI(t, &buf, "process", "--json", "-r", reqPath); err != nil {
			t.Fatalf("process %s: %v\n%s", cohort, err, buf.String())
		}
		// --stream (bare NDJSON) carries the same exact digits.
		var stream bytes.Buffer
		if err := runAPICLI(t, &stream, "process", "--stream", "-r", reqPath); err != nil {
			t.Fatalf("process --stream %s: %v\n%s", cohort, err, stream.String())
		}
		for _, id := range want {
			if !bytes.Contains(stream.Bytes(), []byte(`"id":`+strconv.FormatUint(id, 10))) {
				t.Errorf("%s --stream lacks exact id %d:\n%s", cohort, id, stream.String())
			}
		}
		if !bytes.Contains(stream.Bytes(), []byte(`"prev":9007199254740993`)) {
			t.Errorf("%s --stream lacks the exact lag copy:\n%s", cohort, stream.String())
		}
		var env struct {
			Data struct {
				Data []map[string]json.Number `json:"data"`
			} `json:"data"`
		}
		dec := json.NewDecoder(bytes.NewReader(buf.Bytes()))
		dec.UseNumber()
		if err := dec.Decode(&env); err != nil {
			t.Fatalf("decode: %v\n%s", err, buf.String())
		}
		if len(env.Data.Data) != len(want) {
			t.Fatalf("%s rows = %v, want %d", cohort, env.Data.Data, len(want))
		}
		for j, id := range want {
			if got := env.Data.Data[j]["id"].String(); got != strconv.FormatUint(id, 10) {
				t.Errorf("%s row %d id = %s, want %d\n%s", cohort, j, got, id, buf.String())
			}
			if got := env.Data.Data[j]["prev"].String(); got != wantPrev[j] {
				t.Errorf("%s row %d prev = %q, want %q\n%s", cohort, j, got, wantPrev[j], buf.String())
			}
		}
	}
}
