package service

import (
	"archive/zip"
	"bytes"
	"fmt"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/types"
)

// matrix_grouped_invariance_test.go holds the grouped matrix family's
// cohort (groupedInvSchema — the co-moment columns plus a categorical
// region with a one-row thin bucket and a set-valued tags column) and
// its groupers; matrix_invariance_test.go runs every grouped shape on
// the shared worker-invariance harness.

// groupedInvSchema is coMomentSchema plus a categorical region (a / b /
// c round-robin, "thin" on global row 5 alone) and a set-valued tags
// column (a non-empty mask over four labels — the fan-out grouper).
func groupedInvSchema(t testing.TB) *encoding.Schema {
	t.Helper()
	dict := func(vals ...string) *encoding.Dictionary {
		d := encoding.NewDictionary()
		for _, v := range vals {
			if _, err := d.Add(v); err != nil {
				t.Fatalf("dict.Add: %v", err)
			}
		}
		return d
	}
	s := coMomentSchema()
	s.Fields = append(s.Fields,
		encoding.Field{Name: "region", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 32, CsvColumnIdx: 4, Dictionary: dict("a", "b", "c", "thin")},
		encoding.Field{Name: "tags", Type: encoding.FieldTypeSetU8, ByteOffset: 33, CsvColumnIdx: 5, Dictionary: dict("T0", "T1", "T2", "T3")},
	)
	return s
}

// groupedInvThinRow is the one global row in region "thin".
const groupedInvThinRow = 5

// groupedInvRows is coMomentRows (offset..offset+n-1 of the one global
// sequence) plus the region and tags columns.
func groupedInvRows(n, offset int) ([][]uint64, func(r, f int) bool) {
	recs, nullAt := coMomentRows(n, offset)
	for i := range recs {
		g := offset + i
		region := uint64(g % 3)
		if g == groupedInvThinRow {
			region = 3
		}
		recs[i] = append(recs[i], region, uint64(g%15)+1)
	}
	return recs, nullAt
}

func groupedInvArchive(t *testing.T, schema *encoding.Schema, shardRows []int) []byte {
	t.Helper()
	var doc bytes.Buffer
	total, offset := 0, 0
	for _, n := range shardRows {
		total += n
	}
	if err := encx.WriteSchemaDoc(&doc, schema, uint64(total), uint16(len(shardRows))); err != nil {
		t.Fatalf("WriteSchemaDoc: %v", err)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	write := func(name string, b []byte) {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		if err != nil {
			t.Fatalf("zip.CreateHeader(%q): %v", name, err)
		}
		if _, err := w.Write(b); err != nil {
			t.Fatalf("zip write: %v", err)
		}
	}
	write(encx.ReservedSchemaName, doc.Bytes())
	for i, n := range shardRows {
		recs, nullAt := groupedInvRows(n, offset)
		write(fmt.Sprintf("s%d.pulse", i), writeNullablePulse(t, schema, recs, nullAt))
		offset += n
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip.Close: %v", err)
	}
	return buf.Bytes()
}

// groupedInvGrouper is one grouper of the gate with the bucket keys it
// must emit, how many of them every partition sees (common) and how
// many only the partition holding row 5 sees (thin) — so a run over k
// partitions feeds exactly k·common + thin vehicle instances.
type groupedInvGrouper struct {
	name   string
	group  *types.Group
	keys   []any
	common int64
	thin   int64
}

var groupedInvGroupers = []groupedInvGrouper{
	{"category_with_thin_bucket", &types.Group{Type: types.GROUP_CATEGORY, Field: "region"}, []any{"a", "b", "c", "thin"}, 3, 1},
	{"set_fan_out", &types.Group{Type: types.GROUP_SET_PER_ELEMENT, Field: "tags"}, []any{"T0", "T1", "T2", "T3"}, 4, 0},
}

// sameKeySet reports whether a and b hold the same keys, any order.
func sameKeySet(a, b []any) bool {
	if len(a) != len(b) {
		return false
	}
	seen := make(map[any]int, len(a))
	for _, k := range a {
		seen[k]++
	}
	for _, k := range b {
		if seen[k]--; seen[k] < 0 {
			return false
		}
	}
	return true
}
