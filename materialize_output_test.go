package pulse

import (
	"context"
	"slices"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/io/spss"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// The materialize-only `output` slot (types.MaterializeOutput): the
// written cohort's columns, order, names, descriptions and nullability,
// applied after every other slot has read the full schema.

func strPtr(s string) *string { return &s }
func boolPtr(b bool) *bool    { return &b }

// fieldNames lists a schema's field names in order.
func fieldNames(s *encoding.Schema) []string {
	out := make([]string, len(s.Fields))
	for i, f := range s.Fields {
		out[i] = f.Name
	}
	return out
}

// matOutputReq is a request over src.pulse with one feature (LOG_spend)
// and one window (rn, row number by id) plus out.
func matOutputReq(out *types.MaterializeOutput) *Request {
	return &Request{
		Cohort:   &types.Cohort{Filename: "src.pulse"},
		Features: []*types.Feature{{Type: types.FEAT_LOG, Field: "spend"}},
		Windows:  []*types.Window{{Type: types.WIN_ROW_NUMBER, Label: "rn", OrderBy: []types.OrderKey{{Field: "id"}}}},
		Output:   out,
	}
}

// TestMaterializeOutput_ColumnsInListOrder: a columns list writes
// exactly those columns in list order — source and derived mixed,
// renamed — each cell read from the column its `from` names.
func TestMaterializeOutput_ColumnsInListOrder(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p, _ := matPulse(t, Options{})
	buildMatSource(t, p, "src.pulse", CohortBuilderOptions{})
	res, err := p.Materialize(ctx, matOutputReq(&types.MaterializeOutput{Columns: []types.MaterializeOutputColumn{
		{From: "rn", As: "row"},
		{From: "region"},
		{From: "id", As: "key"},
		{From: "LOG_spend"},
	}}), "out.pulse", MaterializeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"row", "LOG_spend"}; !slices.Equal(res.DerivedColumns, want) {
		t.Errorf("DerivedColumns = %v, want %v", res.DerivedColumns, want)
	}
	if res.Records != 12 {
		t.Errorf("Records = %d, want 12", res.Records)
	}
	schema, rows := readAll(t, p, "out.pulse")
	if want := []string{"row", "region", "key", "LOG_spend"}; !slices.Equal(fieldNames(schema), want) {
		t.Fatalf("fields = %v, want %v", fieldNames(schema), want)
	}
	_, full := readAll(t, p, "src.pulse")
	for i, r := range rows {
		if r[0] != float64(i+1) {
			t.Errorf("row %d row = %v, want %d", i, r[0], i+1)
		}
		if r[1] != full[i][1] || r[2] != full[i][0] {
			t.Errorf("row %d region/key = %v/%v, want %v/%v", i, r[1], r[2], full[i][1], full[i][0])
		}
	}
}

// TestMaterializeOutput_ExcludeKeepsSourceThenDerivedOrder: exclude
// drops the named columns (a source field and a derived one) and keeps
// every other in source-then-derived order.
func TestMaterializeOutput_ExcludeKeepsSourceThenDerivedOrder(t *testing.T) {
	t.Parallel()

	p, _ := matPulse(t, Options{})
	buildMatSource(t, p, "src.pulse", CohortBuilderOptions{})
	res, err := p.Materialize(context.Background(), matOutputReq(&types.MaterializeOutput{
		Exclude: []string{"tags", "LOG_spend", "level"},
	}), "out.pulse", MaterializeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	schema, _ := readAll(t, p, "out.pulse")
	want := []string{"id", "region", "week", "spend", "flag", "day", "price", "rn"}
	if !slices.Equal(fieldNames(schema), want) {
		t.Fatalf("fields = %v, want %v", fieldNames(schema), want)
	}
	if !slices.Equal(res.DerivedColumns, []string{"rn"}) {
		t.Errorf("DerivedColumns = %v", res.DerivedColumns)
	}
}

// TestMaterializeOutput_InheritsAndOverrides: omitted keys inherit the
// source field's type, nullability, dictionary, decimal shape and
// description; a derived column stays a nullable f64 with its generated
// description; description and nullable override.
func TestMaterializeOutput_InheritsAndOverrides(t *testing.T) {
	t.Parallel()

	p, _ := matPulse(t, Options{})
	buildMatSource(t, p, "src.pulse", CohortBuilderOptions{})
	_, err := p.Materialize(context.Background(), matOutputReq(&types.MaterializeOutput{Columns: []types.MaterializeOutputColumn{
		{From: "region", As: "area"},
		{From: "price"},
		{From: "LOG_spend"},
		{From: "id", Description: strPtr("Row key carried from the source cohort"), Nullable: boolPtr(true)},
		{From: "rn", Nullable: boolPtr(false)},
	}}), "out.pulse", MaterializeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	schema, _ := readAll(t, p, "out.pulse")
	src := matSchema()
	region, price := src.Fields[1], src.Fields[7]
	area := schema.Fields[0]
	if area.Type != region.Type || area.Nullable != region.Nullable || area.Description != region.Description {
		t.Errorf("area = %+v, want region's shape %+v", area, region)
	}
	if area.Dictionary == nil || area.Dictionary.Count() != region.Dictionary.Count() || area.Dictionary.Resolve(3) != "west" {
		t.Errorf("area dictionary not inherited: %v", area.Dictionary)
	}
	if got := schema.Fields[1]; got.Type != price.Type || got.Precision != price.Precision || got.Scale != price.Scale {
		t.Errorf("price = %+v, want decimal %d,%d", got, price.Precision, price.Scale)
	}
	logF := schema.Fields[2]
	if logF.Type != encoding.FieldTypeF64 || !logF.Nullable || logF.Description != "FEAT_LOG of spend — derived from src.pulse" {
		t.Errorf("LOG_spend = %+v", logF)
	}
	if id := schema.Fields[3]; id.Description != "Row key carried from the source cohort" || !id.Nullable || id.Type != encoding.FieldTypeU32 {
		t.Errorf("id override = %+v", id)
	}
	if rn := schema.Fields[4]; rn.Nullable || rn.Type != encoding.FieldTypeF64 {
		t.Errorf("rn = %+v, want non-nullable f64", rn)
	}
}

// TestMaterializeOutput_DuplicatedColumn: the same `from` under two
// `as` names writes the column twice.
func TestMaterializeOutput_DuplicatedColumn(t *testing.T) {
	t.Parallel()

	p, _ := matPulse(t, Options{})
	buildMatSource(t, p, "src.pulse", CohortBuilderOptions{})
	_, err := p.Materialize(context.Background(), matOutputReq(&types.MaterializeOutput{Columns: []types.MaterializeOutputColumn{
		{From: "week"}, {From: "week", As: "week_copy"}, {From: "rn", As: "a"}, {From: "rn", As: "b"},
	}}), "out.pulse", MaterializeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	schema, rows := readAll(t, p, "out.pulse")
	if want := []string{"week", "week_copy", "a", "b"}; !slices.Equal(fieldNames(schema), want) {
		t.Fatalf("fields = %v", fieldNames(schema))
	}
	for i, r := range rows {
		if r[0] != r[1] || r[2] != r[3] {
			t.Errorf("row %d = %v: duplicated columns differ", i, r)
		}
	}
}

// TestMaterializeOutput_StaticRefusals: every malformed output block is
// refused with its own code and details before anything is created.
func TestMaterializeOutput_StaticRefusals(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p, fsys := matPulse(t, Options{})
	buildMatSource(t, p, "src.pulse", CohortBuilderOptions{})
	col := func(from, as string) types.MaterializeOutputColumn {
		return types.MaterializeOutputColumn{From: from, As: as}
	}
	cases := []struct {
		name    string
		out     *types.MaterializeOutput
		code    errors.Code
		details map[string]any
	}{
		{"both", &types.MaterializeOutput{Columns: []types.MaterializeOutputColumn{col("id", "")}, Exclude: []string{"day"}},
			errors.PULSE_MATERIALIZE_OUTPUT_INVALID, map[string]any{"reason": "columns_and_exclude", "path": "output"}},
		{"empty columns", &types.MaterializeOutput{Columns: []types.MaterializeOutputColumn{}},
			errors.PULSE_MATERIALIZE_OUTPUT_INVALID, map[string]any{"reason": "no_columns", "path": "output.columns"}},
		{"exclude every column", &types.MaterializeOutput{Exclude: []string{"id", "region", "week", "spend", "level", "flag", "day", "price", "tags", "LOG_spend", "rn"}},
			errors.PULSE_MATERIALIZE_OUTPUT_INVALID, map[string]any{"reason": "no_columns", "path": "output.exclude"}},
		{"from missing", &types.MaterializeOutput{Columns: []types.MaterializeOutputColumn{col("id", ""), col("", "x")}},
			errors.PULSE_MATERIALIZE_OUTPUT_INVALID, map[string]any{"reason": "from_missing", "path": "output.columns[1].from"}},
		{"unknown from", &types.MaterializeOutput{Columns: []types.MaterializeOutputColumn{col("id", ""), col("spnd", "")}},
			errors.PULSE_MATERIALIZE_OUTPUT_UNKNOWN_COLUMN, map[string]any{"column": "spnd", "path": "output.columns[1].from", "suggestion": "spend"}},
		{"unknown exclude", &types.MaterializeOutput{Exclude: []string{"zzzzzzzzzz"}},
			errors.PULSE_MATERIALIZE_OUTPUT_UNKNOWN_COLUMN, map[string]any{"column": "zzzzzzzzzz", "path": "output.exclude[0]"}},
		{"rename collision", &types.MaterializeOutput{Columns: []types.MaterializeOutputColumn{col("id", ""), col("week", "id")}},
			errors.PULSE_MATERIALIZE_OUTPUT_DUPLICATE, map[string]any{"column": "id", "path": "output.columns[1]", "first": "output.columns[0]"}},
		{"same from, no rename", &types.MaterializeOutput{Columns: []types.MaterializeOutputColumn{col("rn", ""), col("rn", "")}},
			errors.PULSE_MATERIALIZE_OUTPUT_DUPLICATE, map[string]any{"column": "rn", "path": "output.columns[1]", "first": "output.columns[0]"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := p.Materialize(ctx, matOutputReq(c.out), "out.pulse", MaterializeOptions{})
			ce := assertCode(t, err, c.code)
			for k, v := range c.details {
				if ce.Details[k] != v {
					t.Errorf("details.%s = %v, want %v (details %v)", k, ce.Details[k], v, ce.Details)
				}
			}
			if _, ok := c.details["suggestion"]; !ok {
				if _, has := ce.Details["suggestion"]; has {
					t.Errorf("unexpected suggestion %v", ce.Details["suggestion"])
				}
			}
			assertAbsent(t, fsys, "out.pulse")
		})
	}
}

// TestMaterializeOutput_NotNullableNullFails: nullable: false on a
// column holding a null fails the run with the column and the output
// row, nothing written; on a column with no null it writes a
// non-nullable field.
func TestMaterializeOutput_NotNullableNullFails(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p, fsys := matPulse(t, Options{})
	buildMatSource(t, p, "src.pulse", CohortBuilderOptions{})
	// spend is null on source row 4; LOG_spend inherits that null.
	for _, c := range []types.MaterializeOutputColumn{
		{From: "spend", As: "s", Nullable: boolPtr(false)},
		{From: "LOG_spend", Nullable: boolPtr(false)},
	} {
		name := c.As
		if name == "" {
			name = c.From
		}
		t.Run(name, func(t *testing.T) {
			_, err := p.Materialize(ctx, matOutputReq(&types.MaterializeOutput{Columns: []types.MaterializeOutputColumn{{From: "id"}, c}}),
				"out.pulse", MaterializeOptions{})
			ce := assertCode(t, err, errors.PULSE_MATERIALIZE_OUTPUT_NULL)
			if ce.Details["column"] != name || ce.Details["row"] != int64(4) {
				t.Errorf("details = %v, want column %s row 4", ce.Details, name)
			}
			assertAbsent(t, fsys, "out.pulse")
		})
	}
	if _, err := p.Materialize(ctx, matOutputReq(&types.MaterializeOutput{Columns: []types.MaterializeOutputColumn{
		{From: "week", Nullable: boolPtr(false)}, {From: "region", Nullable: boolPtr(false)},
	}}), "ok.pulse", MaterializeOptions{}); err != nil {
		t.Fatalf("no-null columns: %v", err)
	}
	schema, _ := readAll(t, p, "ok.pulse")
	if schema.Fields[1].Nullable || schema.HasBitmap() {
		t.Errorf("region still nullable: %+v", schema.Fields[1])
	}
}

// TestMaterializeOutput_ProjectionAppliedLast: a filter, a sort, a
// window's partition / order and an attribute's weight all read columns
// the output drops; the kept columns hold exactly what the unprojected run writes.
func TestMaterializeOutput_ProjectionAppliedLast(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p, _ := matPulse(t, Options{})
	buildMatSource(t, p, "src.pulse", CohortBuilderOptions{})
	build := func(out *types.MaterializeOutput) *Request {
		return &Request{
			Cohort:    &types.Cohort{Filename: "src.pulse"},
			Filterers: []*types.Filterer{{Type: types.FILTER_INCLUDE, Field: "region", Values: []string{"north", "east"}}},
			Attributes: []*types.Attribute{{Type: types.ATTR_ZSCORE, Field: "spend", Label: "z",
				Weight: types.SlotWeightOf(types.WeightSpec{Field: "week", Kind: types.WeightKindFrequency})}},
			Windows: []*types.Window{{Type: types.WIN_ROW_NUMBER, Label: "rn",
				PartitionBy: []string{"region"}, OrderBy: []types.OrderKey{{Field: "week", Desc: true}}}},
			Sort:   []types.OrderKey{{Field: "week"}, {Field: "level", Desc: true}},
			Output: out,
		}
	}
	if _, err := p.Materialize(ctx, build(nil), "full.pulse", MaterializeOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Materialize(ctx, build(&types.MaterializeOutput{Columns: []types.MaterializeOutputColumn{
		{From: "id"}, {From: "z"}, {From: "rn"},
	}}), "proj.pulse", MaterializeOptions{}); err != nil {
		t.Fatal(err)
	}
	fs, full := readAll(t, p, "full.pulse")
	_, proj := readAll(t, p, "proj.pulse")
	if len(proj) != len(full) || len(full) == 0 {
		t.Fatalf("rows %d vs %d", len(proj), len(full))
	}
	idx := func(name string) int { return slices.Index(fieldNames(fs), name) }
	// rn is absolute, not only equal across the two runs: within a
	// region the weeks are distinct, so a row's number under week desc
	// is one plus the rows of its region with a later week.
	for i := range full {
		want := 1
		for j := range full {
			if full[j][idx("region")] == full[i][idx("region")] && full[j][idx("week")].(uint64) > full[i][idx("week")].(uint64) {
				want++
			}
		}
		if proj[i][2] != float64(want) {
			t.Errorf("row %d rn = %v, want %d", i, proj[i][2], want)
		}
	}
	for i := range full {
		want := CohortRow{full[i][idx("id")], full[i][idx("z")], full[i][idx("rn")]}
		for j := range want {
			if !sameCell(proj[i][j], want[j]) {
				t.Errorf("row %d col %d = %v, want %v", i, j, proj[i][j], want[j])
			}
		}
	}
}

// sameCell compares two cells, NaN equal to NaN.
func sameCell(a, b any) bool {
	fa, okA := a.(float64)
	fb, okB := b.(float64)
	if okA && okB && fa != fa && fb != fb {
		return true
	}
	return a == b
}

// TestMaterializeOutput_PureReshape: with an output block, a request
// with no feature, attribute or window writes the reshaped (filtered)
// cohort; without one it is still PULSE_MATERIALIZE_NO_DERIVED.
func TestMaterializeOutput_PureReshape(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p, _ := matPulse(t, Options{})
	buildMatSource(t, p, "src.pulse", CohortBuilderOptions{})
	req := &Request{
		Cohort:    &types.Cohort{Filename: "src.pulse"},
		Filterers: []*types.Filterer{{Type: types.FILTER_INCLUDE, Field: "region", Values: []string{"south"}}},
		Output:    &types.MaterializeOutput{Columns: []types.MaterializeOutputColumn{{From: "spend", As: "amount"}, {From: "id"}}},
	}
	res, err := p.Materialize(ctx, req, "out.pulse", MaterializeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Records != 4 || res.DerivedColumns == nil || len(res.DerivedColumns) != 0 {
		t.Errorf("result = %+v, want 4 records and no derived column", res)
	}
	schema, rows := readAll(t, p, "out.pulse")
	if want := []string{"amount", "id"}; !slices.Equal(fieldNames(schema), want) {
		t.Fatalf("fields = %v", fieldNames(schema))
	}
	if rows[0][1] != uint64(2) || rows[0][0] != 3.5 {
		t.Errorf("first row = %v, want [3.5 2]", rows[0])
	}
	req.Output = nil
	_, err = p.Materialize(ctx, req, "again.pulse", MaterializeOptions{})
	assertCode(t, err, errors.PULSE_MATERIALIZE_NO_DERIVED)
}

// TestMaterializeOutput_SidecarNotCarriedWhenReshaped: an output block
// over an SPSS-backed source writes the cohort without the sidecar —
// never a stale one — and warns with the sidecar's code.
func TestMaterializeOutput_SidecarNotCarriedWhenReshaped(t *testing.T) {
	t.Parallel()

	afs := afero.NewMemMapFs()
	p := importSav(t, afs, labelledSavSpec(), Options{})
	req := absentOutputRequest()
	// Every source column kept, reordered, one redescribed: a carry
	// keyed to the source's variables would accept it and write a stale
	// label.
	req.Output = &types.MaterializeOutput{Columns: []types.MaterializeOutputColumn{
		{From: "WT"}, {From: "Q1", Description: strPtr("Overall satisfaction score")}, {From: "ID"}, {From: "rn"},
	}}
	res, err := p.Materialize(context.Background(), req, "derived.pulse", MaterializeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Warnings) != 1 || res.Warnings[0].Code != errors.PULSE_SPSS_SIDECAR_INVALID {
		t.Fatalf("warnings = %v, want one PULSE_SPSS_SIDECAR_INVALID", res.Warnings)
	}
	if ok, _ := afero.Exists(afs, spss.SidecarPath("derived.pulse")); ok {
		t.Fatal("a sidecar was carried onto a reshaped cohort")
	}
}
