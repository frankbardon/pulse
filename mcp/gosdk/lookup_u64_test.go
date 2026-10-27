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

// TestLookupTool_U64RowsExact (E4-S4): a pulse_lookup call over the
// go-sdk returns a u64 cell as its exact integer — 2^53+1 and MaxUint64
// keep their own digits — on a single file and a shard archive.
func TestLookupTool_U64RowsExact(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fs := afero.NewMemMapFs()
	seed, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	ids := []uint64{1<<53 - 1, 1<<53 + 1, math.MaxUint64, 7}
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
	for _, c := range []string{"single.pulse", "archive.pulse"} {
		if _, err := seed.BuildIndex(ctx, c, []string{"id"}); err != nil {
			t.Fatalf("BuildIndex(%s): %v", c, err)
		}
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
		for _, id := range ids {
			digits := strconv.FormatUint(id, 10)
			res, err := client.CallTool(ctx, &mcpsdk.CallToolParams{Name: toolmeta.ToolLookup, Arguments: map[string]any{
				"cohort": map[string]any{"filename": cohort}, "field": "id", "value": digits,
			}})
			if err != nil || res.IsError {
				t.Fatalf("call %s id=%s: err %v result %+v", cohort, digits, err, res)
			}
			var out struct {
				Rows []map[string]json.Number `json:"rows"`
			}
			dec := json.NewDecoder(bytes.NewReader([]byte(res.Content[0].(*mcpsdk.TextContent).Text)))
			dec.UseNumber()
			if err := dec.Decode(&out); err != nil {
				t.Fatal(err)
			}
			if len(out.Rows) != 1 || out.Rows[0]["id"].String() != digits {
				t.Errorf("%s id=%s rows = %v, want the exact id", cohort, digits, out.Rows)
			}
		}
	}
}
