package pulse_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// readCohort opens path and returns its schema and every row.
func readCohort(t *testing.T, p *pulse.Pulse, path string) (*encoding.Schema, []pulse.CohortRow) {
	t.Helper()
	c, err := p.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	r, err := c.Reader()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	var rows []pulse.CohortRow
	for i := int64(0); i < r.Len(); i++ {
		row, err := r.RecordAt(i)
		if err != nil {
			t.Fatal(err)
		}
		rows = append(rows, row)
	}
	return r.Schema(), rows
}

// TestPreviousState_MaterializeWritesCategoricalColumn: materializing
// ATTR_PREVIOUS_STATE writes the output as a nullable categorical
// column of the state field's own type and dictionary — the labels read
// back are the previous states, a null rides the bitmap — computed over
// the whole cohort before the request's filter; a numeric attribute
// beside it stays a nullable f64. A crosstab on the derived cohort
// equals the in-request crosstab cell for cell and label for label.
func TestPreviousState_MaterializeWritesCategoricalColumn(t *testing.T) {
	p, _ := prevStateWavesLongAndWide(t, pulse.Options{})
	ctx := context.Background()
	attrs := func() []*types.Attribute {
		return append(prevStateAttr(), &types.Attribute{Type: types.ATTR_ZSCORE, Field: "x", Label: "zx"})
	}
	for _, tc := range []struct {
		name   string
		filter []*types.Filterer
	}{{"unfiltered", nil}, {"wave3", []*types.Filterer{dayWindow(t, 3)}}} {
		t.Run(tc.name, func(t *testing.T) {
			out := "derived_" + tc.name + ".pulse"
			res, err := p.Materialize(ctx, &types.Request{
				Cohort:     &types.Cohort{Filename: "waves.pulse"},
				Attributes: attrs(),
				Filterers:  tc.filter,
			}, out, pulse.MaterializeOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(res.DerivedColumns, []string{"prev", "zx"}) {
				t.Fatalf("derived columns %v", res.DerivedColumns)
			}
			srcSchema, srcRows := readCohort(t, p, "waves.pulse")
			schema, rows := readCohort(t, p, out)
			plan, prev, zx := srcSchema.Field("plan"), schema.Field("prev"), schema.Field("zx")
			if prev == nil || prev.Type != plan.Type || !prev.Nullable {
				t.Fatalf("prev field %+v, want a nullable %s", prev, plan.Type)
			}
			if prev.Dictionary == nil || !reflect.DeepEqual(prev.Dictionary.Values(), plan.Dictionary.Values()) {
				t.Fatalf("prev dictionary %v, want plan's %v", prev.Dictionary, plan.Dictionary.Values())
			}
			if zx == nil || zx.Type != encoding.FieldTypeF64 || !zx.Nullable || zx.Dictionary != nil {
				t.Errorf("zx field %+v, want a nullable f64", zx)
			}
			// Each kept record's previous state: the subject's plan at
			// the wave before (row order is wave-major, subject-minor).
			n := len(prevStateWaves)
			var want []any
			for i, row := range srcRows {
				if tc.filter != nil && row[1] != int32(3) {
					continue
				}
				var v any
				if wave := i / n; wave > 0 {
					v = prevStateWaves[i%n][wave-1]
				}
				want = append(want, v)
			}
			pi := len(srcSchema.Fields)
			var got []any
			for _, row := range rows {
				got = append(got, row[pi])
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("materialized prev\n got %v\nwant %v", got, want)
			}

			in := transitionCrosstab("waves.pulse", "prev", "plan", prevStateAttr())
			in.Filterers = tc.filter
			inResp, err := p.Process(ctx, in)
			if err != nil {
				t.Fatal(err)
			}
			outResp, err := p.Process(ctx, transitionCrosstab(out, "prev", "plan", nil))
			if err != nil {
				t.Fatal(err)
			}
			im, om := inResp.Crosstab.Matrix, outResp.Crosstab.Matrix
			if len(im.RowKeys) == 0 {
				t.Fatal("empty in-request crosstab")
			}
			if !reflect.DeepEqual(im.RowKeys, om.RowKeys) || !reflect.DeepEqual(im.ColumnKeys, om.ColumnKeys) ||
				!reflect.DeepEqual(im.Cells, om.Cells) {
				t.Errorf("crosstab differs:\nin  %v x %v %v\nout %v x %v %v",
					im.RowKeys, im.ColumnKeys, im.Cells, om.RowKeys, om.ColumnKeys, om.Cells)
			}
		})
	}
}

// TestPreviousState_MaterializePackedBoolState: a packed_bool state
// materializes as a nullable packed_bool column of the earlier record's
// flag.
func TestPreviousState_MaterializePackedBoolState(t *testing.T) {
	p, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs()})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	schema := encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU64, Description: "Customer identifier of the panel row"},
		{Name: "seen", Type: encoding.FieldTypeDate, Description: "Calendar day the flag was observed"},
		{Name: "active", Type: encoding.FieldTypePackedBool, Nullable: true, Description: "Whether the customer was active that day"},
	}}
	bld, err := p.NewCohortBuilder(ctx, "flags.pulse", schema, pulse.CohortBuilderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	src := []pulse.CohortRow{
		{uint64(1), int32(1), true}, {uint64(1), int32(2), false}, {uint64(1), int32(3), nil},
		{uint64(2), int32(1), false}, {uint64(2), int32(2), true},
	}
	for _, r := range src {
		if err := bld.Append(r); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := bld.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Materialize(ctx, &types.Request{
		Cohort: &types.Cohort{Filename: "flags.pulse"},
		Attributes: []*types.Attribute{{Type: types.ATTR_PREVIOUS_STATE, Field: "active",
			SubjectField: "id", OrderField: "seen", Label: "prev_active"}},
	}, "flags_derived.pulse", pulse.MaterializeOptions{}); err != nil {
		t.Fatal(err)
	}
	out, rows := readCohort(t, p, "flags_derived.pulse")
	f := out.Field("prev_active")
	if f == nil || f.Type != encoding.FieldTypePackedBool || !f.Nullable {
		t.Fatalf("prev_active field %+v, want a nullable packed_bool", f)
	}
	want := []any{nil, true, false, nil, false}
	for i, row := range rows {
		if row[3] != want[i] {
			t.Errorf("row %d prev_active = %v, want %v", i, row[3], want[i])
		}
	}
}
