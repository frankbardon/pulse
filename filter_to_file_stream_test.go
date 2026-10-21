package pulse

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	pio "github.com/frankbardon/pulse/io"
	"github.com/spf13/afero"
)

// U51 E4-S5: the single-file and anchored filter-to-file arms stream
// rows from input to output and publish by rename. Their output is the
// contract they always wrote: the source's header + schema bytes
// verbatim, then every kept physical row's bytes in source order (a
// predicate-free run keeps every WHOLE row). These twins pin that
// definition over every layout the byte copy must carry — the null
// bitmap, categorical and set dictionaries, a vector, a u64 past 2^53,
// the bit-packed types, and a grouped (0x02) cohort's physical rows.

// streamTwinSchema covers the per-record layouts the copy carries.
func streamTwinSchema() encoding.Schema {
	return encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU32},
		{Name: "score", Type: encoding.FieldTypeF64, Nullable: true},
		{Name: "cat", Type: encoding.FieldTypeCategoricalU8, Nullable: true},
		{Name: "tags", Type: encoding.FieldTypeSetU16, Nullable: true},
		{Name: "emb", Type: encoding.FieldTypeVecF32, Nullable: true},
		{Name: "big", Type: encoding.FieldTypeU64},
		{Name: "flag", Type: encoding.FieldTypePackedBool},
		{Name: "nib", Type: encoding.FieldTypeU4},
	}, Vectors: []encoding.VectorField{{FieldIndex: 4, Dim: 3}}}
}

func streamTwinRows(n int) []CohortRow {
	cats := []string{"a", "b", "c"}
	rows := make([]CohortRow, n)
	for i := range rows {
		row := CohortRow{uint64(i + 1), float64(i) * 1.5, cats[i%3],
			[]string{fmt.Sprintf("t%d", i%5), fmt.Sprintf("t%d", (i+2)%5)},
			[]float32{float32(i), -float32(i) / 2, 0.25},
			uint64(1)<<60 + uint64(i), i%2 == 0, uint64(i % 16)}
		if i%5 == 2 {
			row[1] = nil
		}
		if i%7 == 4 {
			row[2] = nil
		}
		if i%6 == 1 {
			row[3] = nil
		}
		if i%4 == 1 {
			row[4] = nil
		}
		rows[i] = row
	}
	return rows
}

// streamTwinExpected is the filter-to-file output contract over src:
// the preamble verbatim, then every kept physical row in order.
func streamTwinExpected(t *testing.T, src []byte, keep func(i int) bool) ([]byte, int64) {
	t.Helper()
	br := bytes.NewReader(src)
	v, err := encoding.ReadHeader(br)
	if err != nil {
		t.Fatal(err)
	}
	s, err := encoding.ReadSchema(br, v)
	if err != nil {
		t.Fatal(err)
	}
	pre := len(src) - br.Len()
	stride := s.RecordByteSize()
	out := append([]byte(nil), src[:pre]...)
	var n int64
	for i := 0; pre+(i+1)*stride <= len(src); i++ {
		if keep(i) {
			out = append(out, src[pre+i*stride:pre+(i+1)*stride]...)
			n++
		}
	}
	return out, n
}

// TestShardStream_FilterSingleFileByteTwin: every single-file and
// anchored filter-to-file entry point writes exactly the contract
// bytes, over the rich layout and a grouped cohort, with a predicate
// and without (a flatten no-op, a truncated tail dropped).
func TestShardStream_FilterSingleFileByteTwin(t *testing.T) {
	ctx := context.Background()
	p, fsys := memBuilderEngine(t)
	buildCohort(t, p, "rich.pulse", streamTwinSchema(), CohortBuilderOptions{}, streamTwinRows(97))
	gRows, _ := groupedData()
	buildCohort(t, p, "grouped.pulse", groupedSchema(), CohortBuilderOptions{Groups: []pio.GroupDecl{custGroup}}, gRows)
	if g := readFile(t, fsys, "grouped.pulse"); g[8] != encoding.FormatVersionV2 {
		t.Fatalf("grouped fixture is format 0x%02x, want 0x02", g[8])
	}
	if _, err := p.CreateShardArchive(ctx, "arch.pulse", []string{"rich.pulse"}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.CreateShardArchive(ctx, "garch.pulse", []string{"grouped.pulse"}); err != nil {
		t.Fatal(err)
	}
	// A truncated tail: half a record past the last whole one.
	rich := readFile(t, fsys, "rich.pulse")
	torn := append(append([]byte(nil), rich...), make([]byte, 7)...)
	if err := afero.WriteFile(fsys, "torn.pulse", torn, 0o644); err != nil {
		t.Fatal(err)
	}

	richKeep := func(i int) bool { id := i + 1; return id <= 20 || id > 60 }
	grpKeep := func(i int) bool { order := i + 1; return order%2 == 0 || order > 100 }
	all := func(int) bool { return true }
	cases := []struct {
		name, src, fixture, expr string
		keep                     func(int) bool
		flatten                  bool
	}{
		{"rich", "rich.pulse", "rich.pulse", "id <= 20 || id > 60", richKeep, false},
		{"rich_anchored", "arch.pulse#rich.pulse", "rich.pulse", "id <= 20 || id > 60", richKeep, false},
		{"rich_none_kept", "rich.pulse", "rich.pulse", "id > 1000", func(int) bool { return false }, false},
		{"rich_no_predicate", "rich.pulse", "rich.pulse", "", all, true},
		{"rich_anchored_no_predicate", "arch.pulse#rich.pulse", "rich.pulse", "", all, true},
		{"torn", "torn.pulse", "torn.pulse", "id <= 20 || id > 60", richKeep, false},
		{"torn_no_predicate", "torn.pulse", "torn.pulse", "", all, true},
		{"grouped", "grouped.pulse", "grouped.pulse", "order % 2 == 0 || order > 100", grpKeep, false},
		{"grouped_anchored", "garch.pulse#grouped.pulse", "grouped.pulse", "order % 2 == 0 || order > 100", grpKeep, false},
		{"grouped_no_predicate", "grouped.pulse", "grouped.pulse", "", all, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want, wantN := streamTwinExpected(t, readFile(t, fsys, tc.fixture), tc.keep)
			dst := tc.name + ".out.pulse"
			rep, err := p.FilterToFileWithOptions(ctx, tc.src, dst, FilterToFileOptions{Expression: tc.expr, Flatten: tc.flatten})
			if err != nil {
				t.Fatal(err)
			}
			if rep.Records != wantN || rep.Flattened {
				t.Fatalf("report = %+v, want %d records, not flattened", rep, wantN)
			}
			if got := readFile(t, fsys, dst); !bytes.Equal(got, want) {
				t.Fatalf("output (%d B) differs from the contract bytes (%d B)", len(got), len(want))
			}
			if tc.expr == "" {
				return
			}
			// The expression entry points write the same bytes.
			n, err := p.FilterToFile(ctx, tc.src, "plain."+dst, tc.expr)
			if err != nil || n != wantN {
				t.Fatalf("FilterToFile = %d, %v; want %d", n, err, wantN)
			}
			if got := readFile(t, fsys, "plain."+dst); !bytes.Equal(got, want) {
				t.Fatal("FilterToFile output differs from the contract bytes")
			}
			res, err := p.FilterToFileWithRequest(ctx, &FilterToFileRequest{SourcePath: tc.src, Expression: tc.expr, OutputDir: "req/" + tc.name})
			if err != nil || res.RowCount != wantN || res.Reused {
				t.Fatalf("FilterToFileWithRequest = %+v, %v; want %d rows, fresh", res, err, wantN)
			}
			if got := readFile(t, fsys, res.OutputPath); !bytes.Equal(got, want) {
				t.Fatal("FilterToFileWithRequest output differs from the contract bytes")
			}
			// Reuse-match is unchanged: the same request returns the
			// existing output without re-running.
			again, err := p.FilterToFileWithRequest(ctx, &FilterToFileRequest{SourcePath: tc.src, Expression: tc.expr, OutputDir: "req/" + tc.name})
			if err != nil || !again.Reused || again.OutputHash != res.OutputHash {
				t.Fatalf("second request = %+v, %v; want a reuse of %s", again, err, res.OutputHash)
			}
		})
	}
	for _, f := range listFiles(t, fsys, ".") {
		if bytes.Contains([]byte(f), []byte(".pulse.tmp-")) {
			t.Errorf("stage left behind: %s", f)
		}
	}
}
