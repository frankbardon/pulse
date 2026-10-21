package gosdk_test

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"strconv"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/mcp/toolmeta"
	"github.com/frankbardon/pulse/mcp/gosdk"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/afero"
)

// TestProcessTool_U64RecordRowsExact (E4-S6): a pulse_process call with a
// window and Request.Sort returns a u64 record-row cell as its exact
// integer — 2^53+1 and MaxUint64 keep their own digits — on a single
// file and a shard archive, in today's (float64-echo) sort order.
func TestProcessTool_U64RecordRowsExact(t *testing.T) {
	ctx := context.Background()
	fs := afero.NewMemMapFs()
	seed, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	ids := []uint64{1<<53 - 1, 1<<53 + 1, math.MaxUint64, 7}
	want := []uint64{math.MaxUint64, 1<<53 + 1, 1<<53 - 1, 7}
	// WIN_LAG over id ascending, per want row ("" = null).
	wantPrev := []string{"9007199254740993", "9007199254740991", "7", ""}
	build := func(path string, ids []uint64) {
		t.Helper()
		b, err := seed.NewCohortBuilder(ctx, path, encoding.Schema{Fields: []encoding.Field{
			{Name: "id", Type: encoding.FieldTypeU64, Description: "Customer account identifier"},
		}}, pulse.CohortBuilderOptions{})
		if err != nil {
			t.Fatal(err)
		}
		for _, id := range ids {
			if err := b.Append(pulse.CohortRow{id}); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := b.Close(); err != nil {
			t.Fatal(err)
		}
	}
	build("single.pulse", ids)
	build("s0.pulse", ids[:2])
	build("s1.pulse", ids[2:])
	if _, err := seed.CreateShardArchive(ctx, "archive.pulse", []string{"s0.pulse", "s1.pulse"}); err != nil {
		t.Fatal(err)
	}

	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	srv := newServer()
	if err := gosdk.Register(srv, p, gosdk.Config{Version: "9.9.9", DisableCohortScan: true}); err != nil {
		t.Fatal(err)
	}
	client, cancel := connect(t, srv)
	defer cancel()
	for _, cohort := range []string{"single.pulse", "archive.pulse"} {
		res, err := client.CallTool(ctx, &mcpsdk.CallToolParams{Name: toolmeta.ToolProcess, Arguments: map[string]any{
			"cohort": map[string]any{"filename": cohort},
			"windows": []any{
				map[string]any{"type": "WIN_ROW_NUMBER", "label": "rn", "order_by": []any{map[string]any{"field": "id"}}},
				// E4-S7: a lag copy of the id renders its source's exact word.
				map[string]any{"type": "WIN_LAG", "field": "id", "label": "prev", "order_by": []any{map[string]any{"field": "id"}}},
			},
			"sort": []any{map[string]any{"field": "id", "desc": true}},
		}})
		if err != nil || res.IsError {
			t.Fatalf("call %s: err %v result %+v", cohort, err, res)
		}
		text := res.Content[0].(*mcpsdk.TextContent).Text
		var out struct {
			Data []map[string]json.Number `json:"data"`
		}
		dec := json.NewDecoder(bytes.NewReader([]byte(text)))
		dec.UseNumber()
		if err := dec.Decode(&out); err != nil {
			t.Fatal(err)
		}
		if len(out.Data) != len(want) {
			t.Fatalf("%s rows = %v, want %d: %s", cohort, out.Data, len(want), text)
		}
		for j, id := range want {
			if got := out.Data[j]["id"].String(); got != strconv.FormatUint(id, 10) {
				t.Errorf("%s row %d id = %s, want %d", cohort, j, got, id)
			}
			if got := out.Data[j]["prev"].String(); got != wantPrev[j] {
				t.Errorf("%s row %d prev = %q, want %q", cohort, j, got, wantPrev[j])
			}
		}
	}
}
