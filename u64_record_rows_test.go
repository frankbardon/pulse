package pulse_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"math"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/types"
)

// u64_record_rows_test.go pins E4-S6: a u64 cell on a Process RECORD
// row (the window path: recordsToRows → windows → Request.Sort →
// post-tests → finalizeRowCells) renders as the record's EXACT stored
// word — Go uint64, JSON its own digits — while every stage before the
// render sees what it saw before: Request.Sort and the window operators
// order and compute on the float64 echo (so 2^53 ties 2^53+1 and
// MaxUint64-1 ties MaxUint64, keeping input order) and an extension
// window reads a float64 cell — the id and a lag of it alike. E4-S7: a
// WIN_LAG / WIN_LEAD copy of the id renders the SOURCE record's exact
// word too (an out-of-frame cell stays null).

// u64RowIDs lists ids in an input order whose float64-echo order differs
// from their exact order: under Sort id desc the echo keeps 2^53 before
// 2^53+1 and MaxUint64-1 before MaxUint64 (ties, stable).
var u64RowIDs = []uint64{7, 1 << 53, 1<<53 + 1, math.MaxUint64 - 1, math.MaxUint64, 1<<53 - 1}

// u64RowWantOrder is u64RowIDs' input indices under Sort id desc by the
// float64 echo — today's order, which E4-S6 must not move.
var u64RowWantOrder = []int{3, 4, 1, 2, 5, 0}

// u64EchoWindow records the Go type of the id cell, and of the id's lag
// cell where present, an extension window saw.
type u64EchoWindow struct{ seen *[]any }

func (w u64EchoWindow) Compute(rows []map[string]any, partitions [][]int, label string) error {
	for _, part := range partitions {
		for _, i := range part {
			*w.seen = append(*w.seen, rows[i]["id"])
			if lag, ok := rows[i]["prev_id"]; ok && lag != nil {
				*w.seen = append(*w.seen, lag)
			}
			rows[i][label] = 1.0
		}
	}
	return nil
}

func u64RowsRequest(cohort string) *types.Request {
	return &types.Request{
		Cohort: &types.Cohort{Filename: cohort},
		Windows: []*types.Window{
			{Type: types.WIN_ROW_NUMBER, Label: "rn", OrderBy: []types.OrderKey{{Field: "v"}}},
			{Type: types.WIN_LAG, Field: "id", Label: "prev_id", OrderBy: []types.OrderKey{{Field: "v"}}},
			{Type: types.WIN_LEAD, Field: "id", Label: "next2_id", OrderBy: []types.OrderKey{{Field: "v"}},
				Params: json.RawMessage(`{"offset":2}`)},
			{Type: "WIN_ACME_U64_ECHO", Field: "v", Label: "echo", OrderBy: []types.OrderKey{{Field: "v"}}},
		},
		Sort:      []types.OrderKey{{Field: "id", Desc: true}},
		PostTests: []*types.Test{{Type: types.TEST_TREND, Field: "rn", OrderBy: []types.OrderKey{{Field: "v"}}}},
	}
}

func TestArchiveLookup_U64ProcessRowsExact(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fsys := afero.NewMemMapFs()
	writeU64RenderCohort(t, fsys, "single.pulse", u64RowIDs)
	writeU64RowsCohort(t, fsys, "s0.pulse", u64RowIDs[:3], 0)
	writeU64RowsCohort(t, fsys, "s1.pulse", u64RowIDs[3:], 3)

	for _, workers := range []int{1, 4} {
		var seen []any
		p, err := pulse.New(pulse.Options{FS: fsys, ShardWorkers: workers, DecodeWorkers: workers,
			Extensions: pulse.Extensions{Windows: []pulse.WindowRegistration{{
				Name: "WIN_ACME_U64_ECHO",
				Factory: func(*types.Window, extend.WindowOptions) (extend.WindowComputer, error) {
					return u64EchoWindow{seen: &seen}, nil
				},
			}}}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := p.CreateShardArchive(ctx, "archive.pulse", []string{"s0.pulse", "s1.pulse"}); err != nil {
			t.Fatal(err)
		}
		for _, cohort := range []string{"single.pulse", "archive.pulse"} {
			seen = seen[:0]
			resp, err := p.Process(ctx, u64RowsRequest(cohort))
			if err != nil {
				t.Fatalf("workers=%d %s: %v", workers, cohort, err)
			}
			if len(resp.Data) != len(u64RowIDs) {
				t.Fatalf("workers=%d %s: rows = %d, want %d", workers, cohort, len(resp.Data), len(u64RowIDs))
			}
			where := cohort
			for j, src := range u64RowWantOrder {
				row := resp.Data[j]
				assertExactU64Cell(t, where+" process row", row["id"], u64RowIDs[src])
				// rn over v (input order) as before; the lag / lead
				// copies render their source record's exact id.
				if rn, _ := row["rn"].(int64); rn != int64(src+1) {
					if rnf, _ := row["rn"].(float64); rnf != float64(src+1) {
						t.Errorf("%s row %d rn = %T %v, want %d", where, j, row["rn"], row["rn"], src+1)
					}
				}
				if src > 0 {
					assertExactU64Cell(t, where+" prev_id", row["prev_id"], u64RowIDs[src-1])
				} else if row["prev_id"] != nil {
					t.Errorf("%s row %d prev_id = %v, want null", where, j, row["prev_id"])
				}
				if src+2 < len(u64RowIDs) {
					assertExactU64Cell(t, where+" next2_id", row["next2_id"], u64RowIDs[src+2])
				} else if row["next2_id"] != nil {
					t.Errorf("%s row %d next2_id = %v, want null", where, j, row["next2_id"])
				}
			}
			if len(resp.PostTests) != 1 || resp.PostTests[0] == nil || resp.PostTests[0].Error != nil {
				t.Errorf("%s post_tests = %+v, want one result", where, resp.PostTests)
			}
			// The extension window read today's cells: float64 echoes,
			// the id and its lag alike.
			if len(seen) != 2*len(u64RowIDs)-1 {
				t.Fatalf("%s: extension window saw %d cells", where, len(seen))
			}
			for _, cell := range seen {
				if _, ok := cell.(float64); !ok {
					t.Errorf("%s: extension window saw id %T %v, want float64", where, cell, cell)
				}
			}
			raw, err := types.MarshalFinite(resp)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(raw, []byte(`"id":18446744073709551615`)) || !bytes.Contains(raw, []byte(`"id":9007199254740993`)) {
				t.Errorf("%s: MarshalFinite(response) lacks the exact ids: %s", where, raw)
			}
		}
		if err := fsys.Remove("archive.pulse"); err != nil {
			t.Fatal(err)
		}
	}
}

// writeU64RowsCohort is writeU64RenderCohort with v continuing from
// base, so a shard archive's rows keep the single file's v order.
func writeU64RowsCohort(t *testing.T, fsys afero.Fs, path string, ids []uint64, base int) {
	t.Helper()
	var buf []byte
	w := &byteSink{b: &buf}
	if err := encoding.WriteHeader(w); err != nil {
		t.Fatal(err)
	}
	if err := encoding.WriteSchema(w, &encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU64, ByteOffset: 0},
		{Name: "v", Type: encoding.FieldTypeF64, ByteOffset: 8},
	}}); err != nil {
		t.Fatal(err)
	}
	for i, id := range ids {
		buf = binary.LittleEndian.AppendUint64(buf, id)
		buf = binary.LittleEndian.AppendUint64(buf, math.Float64bits(float64(base+i)+0.5))
	}
	if err := afero.WriteFile(fsys, path, buf, 0o644); err != nil {
		t.Fatal(err)
	}
}
