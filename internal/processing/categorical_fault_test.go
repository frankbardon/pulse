package processing

import (
	stderrors "errors"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/types"
)

// categorical_fault_test.go: the record-level half of U51 E5-S8 — a
// categorical code / set bit that addresses no dictionary entry is
// refused by every reader that renders or evaluates its label, and ONLY
// by the readers that read that field.

func faultSchema() *encoding.Schema {
	region := encoding.NewDictionary()
	_, _ = region.Add("north")
	_, _ = region.Add("south")
	tags := encoding.NewDictionary()
	_, _ = tags.Add("VISA")
	_, _ = tags.Add("MC")
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "region", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 0, Dictionary: region},
		{Name: "tags", Type: encoding.FieldTypeSetU8, ByteOffset: 1, Dictionary: tags},
		{Name: "x", Type: encoding.FieldTypeF64, ByteOffset: 2},
	}}
}

func faultRecord(region float64, tags uint64) *Record {
	return NewRecordWithWide(faultSchema(),
		map[string]float64{"region": region, "x": 3},
		nil,
		map[string]any{"tags": tags})
}

func wantFault(t *testing.T, err error, field, reason string) {
	t.Helper()
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.ENCODING_INVALID ||
		ce.Details["field"] != field || ce.Details["reason"] != reason {
		t.Fatalf("err = %v, want ENCODING_INVALID on %q (%s)", err, field, reason)
	}
}

// TestCorruptCategorical_AllValuesChecked: AllValuesChecked refuses a
// corrupt categorical or set cell and passes a clean record; AllValues
// itself keeps its no-error shape.
//
// Falsified by deleting noteAllValuesFault in AllValues' wide-slot
// branch (the set case then passes).
func TestCorruptCategorical_AllValuesChecked(t *testing.T) {
	t.Parallel()

	if _, err := faultRecord(1, 0b11).AllValuesChecked(); err != nil {
		t.Fatalf("clean record: %v", err)
	}
	_, err := faultRecord(7, 0b01).AllValuesChecked()
	wantFault(t, err, "region", encx.ReasonCategoricalIndexOutOfRange)
	_, err = faultRecord(0, 0b100).AllValuesChecked()
	wantFault(t, err, "tags", encx.ReasonSetMemberOutOfRange)
	// A rebuilt cache recomputes the fault: a record repaired by a write
	// stops refusing.
	r := faultRecord(7, 0b01)
	_, _ = r.AllValuesChecked()
	r.Set("region", 1)
	if _, err := r.AllValuesChecked(); err != nil {
		t.Fatalf("after repairing the cell: %v", err)
	}
}

// TestCorruptCategorical_ExpressionReadsOnlyItsFields: an expression
// refuses a corrupt cell it READS and ignores one it does not — on the
// per-input path AND on a record whose AllValues map is already cached
// (the buffered path), so the two arms agree row for row.
//
// Falsified by replacing the p.refs walk in runRecord's cached branch
// with an unconditional return of the fault (the "x + 1" cached case
// then refuses).
func TestCorruptCategorical_ExpressionReadsOnlyItsFields(t *testing.T) {
	t.Parallel()

	schema := faultSchema()
	reads, err := newExprProgram(`region == "north" ? 1 : 0`, "formula", schema, nil, nil, (*ExtensionRegistry)(nil).ExprOptions())
	if err != nil {
		t.Fatal(err)
	}
	ignores, err := newExprProgram(`x + 1`, "formula", schema, nil, nil, (*ExtensionRegistry)(nil).ExprOptions())
	if err != nil {
		t.Fatal(err)
	}
	for _, cached := range []bool{false, true} {
		rec := func() *Record {
			r := faultRecord(7, 0b01)
			if cached {
				_ = r.AllValues()
			}
			return r
		}
		if _, _, err := reads.runRecord(rec()); err == nil {
			t.Fatalf("cached=%v: an expression reading the corrupt region answered", cached)
		} else {
			wantFault(t, err, "region", encx.ReasonCategoricalIndexOutOfRange)
		}
		out, _, err := ignores.runRecord(rec())
		if err != nil {
			t.Fatalf("cached=%v: an expression not reading region refused: %v", cached, err)
		}
		if out != 4.0 {
			t.Fatalf("cached=%v: x + 1 = %v, want 4", cached, out)
		}
	}
}

// TestCorruptCategorical_TruthyFilterRefuses: FILTER_TRUE in truthy mode
// reads the label, which AllValues rendered "" (falsy) for a corrupt
// code; it now refuses.
//
// Falsified by restoring the record.AllValues() read in the truthy
// closure (the filter then drops the row as false).
func TestCorruptCategorical_TruthyFilterRefuses(t *testing.T) {
	t.Parallel()

	fns, err := BuildFilters([]*types.Filterer{{Type: types.FILTER_TRUE, Field: "region", Values: []string{"truthy"}}}, faultSchema(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := fns[0](faultRecord(0, 1)); err != nil || !ok {
		t.Fatalf("clean north: ok=%v err=%v", ok, err)
	}
	_, err = fns[0](faultRecord(7, 1))
	wantFault(t, err, "region", encx.ReasonCategoricalIndexOutOfRange)
}

// TestCorruptCategorical_RecordRowsRefuse: the two row renderers
// (Process record rows, Sample / Lookup cells) refuse a corrupt cell.
func TestCorruptCategorical_RecordRowsRefuse(t *testing.T) {
	t.Parallel()

	recs := []*Record{faultRecord(0, 1), faultRecord(7, 1)}
	if _, _, err := recordsToRows(recs[:1], faultSchema()); err != nil {
		t.Fatalf("clean rows: %v", err)
	}
	_, _, err := recordsToRows(recs, faultSchema())
	wantFault(t, err, "region", encx.ReasonCategoricalIndexOutOfRange)
	_, err = RenderRecordCells(faultRecord(0, 0b1000))
	wantFault(t, err, "tags", encx.ReasonSetMemberOutOfRange)
}
