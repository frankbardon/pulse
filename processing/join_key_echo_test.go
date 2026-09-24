package processing

import (
	stderrors "errors"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// joinSetKeySchemas returns a left/right schema pair whose join key is
// a set column of the given rung.
func joinSetKeySchemas(ft encoding.FieldType) (left, right *encoding.Schema) {
	d := encoding.NewDictionary()
	for _, v := range []string{"a", "b", "c"} {
		_, _ = d.Add(v)
	}
	left = &encoding.Schema{Fields: []encoding.Field{
		{Name: "picks", Type: ft, ByteOffset: 0, CsvColumnIdx: 0, Dictionary: d},
		{Name: "score", Type: encoding.FieldTypeF64, ByteOffset: ft.ByteSize(), CsvColumnIdx: 1},
	}}
	right = &encoding.Schema{Fields: []encoding.Field{
		{Name: "picks", Type: ft, ByteOffset: 0, CsvColumnIdx: 0, Dictionary: d},
		{Name: "bonus", Type: encoding.FieldTypeF64, ByteOffset: ft.ByteSize(), CsvColumnIdx: 1},
	}}
	return left, right
}

// A set column must never be a join key. joinKeyOf stringifies
// Record.values[field] — the LOSSY float echo of the membership
// bitmask, which is truncated to the low 64 bits for set_u128 /
// set_u256 and has already lost mantissa bits above 2^53 for set_u64.
// Two different selections therefore hash to the same key and rows
// join that share no answer at all, silently.
//
// The rejection is the join twin of processing/index_key.go's: a
// multi-select bitmask has no single unambiguous equality value, so
// "does this row's set contain X" is a membership predicate, never a
// key.
func TestJoin_RejectsSetTypedKey(t *testing.T) {
	for _, ft := range []encoding.FieldType{
		encoding.FieldTypeSetU8,
		encoding.FieldTypeSetU16,
		encoding.FieldTypeSetU32,
		encoding.FieldTypeSetU64,
		encoding.FieldTypeSetU128,
		encoding.FieldTypeSetU256,
	} {
		t.Run(ft.String(), func(t *testing.T) {
			leftSchema, rightSchema := joinSetKeySchemas(ft)
			spec := &types.JoinSpec{
				Right: "r.pulse",
				Kind:  "inner",
				As:    "r_",
				On:    []types.OnPair{{LeftField: "picks", RightField: "picks"}},
			}
			_, _, err := NewHashJoinIterator(
				NewSliceIterator(nil), nil, leftSchema, rightSchema, spec)
			if err == nil {
				t.Fatalf("NewHashJoinIterator accepted a %s join key", ft.String())
			}
			var ce *errors.CodedError
			if !stderrors.As(err, &ce) {
				t.Fatalf("error is not coded: %v", err)
			}
			if ce.Code != errors.PULSE_JOIN_TYPE_MISMATCH {
				t.Errorf("code = %s, want %s", ce.Code, errors.PULSE_JOIN_TYPE_MISMATCH)
			}
		})
	}
}

// The rejection must not be collateral damage: a set column that is
// merely CARRIED through the join (not a key) still joins fine, and
// its authoritative mask survives into the joined record.
func TestJoin_SetColumnOffTheKeyStillJoins(t *testing.T) {
	d := encoding.NewDictionary()
	for _, v := range []string{"a", "b", "c"} {
		_, _ = d.Add(v)
	}
	leftSchema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU32, ByteOffset: 0, CsvColumnIdx: 0},
		{Name: "picks", Type: encoding.FieldTypeSetU8, ByteOffset: 4, CsvColumnIdx: 1, Dictionary: d},
	}}
	rightSchema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU32, ByteOffset: 0, CsvColumnIdx: 0},
		{Name: "bonus", Type: encoding.FieldTypeF64, ByteOffset: 4, CsvColumnIdx: 1},
	}}
	spec := &types.JoinSpec{
		Right: "r.pulse",
		Kind:  "inner",
		As:    "r_",
		On:    []types.OnPair{{LeftField: "id", RightField: "id"}},
	}

	leftRec := NewRecordWithWide(leftSchema,
		map[string]float64{"id": 7, "picks": 5},
		nil,
		map[string]any{"picks": uint64(5)})
	rightRec := NewRecordWithWide(rightSchema,
		map[string]float64{"id": 7, "bonus": 100},
		nil, nil)

	it, _, err := NewHashJoinIterator(
		NewSliceIterator([]*Record{leftRec}), []*Record{rightRec},
		leftSchema, rightSchema, spec)
	if err != nil {
		t.Fatalf("NewHashJoinIterator: %v", err)
	}
	if !it.Next() {
		t.Fatal("expected one joined row")
	}
	joined := it.Record()
	mask, ok := joined.SetMaskValue("picks")
	if !ok {
		t.Fatal("joined record lost the set mask")
	}
	if lo, fits := mask.Uint64(); !fits || lo != 5 {
		t.Errorf("joined mask low word = %d (fits=%v), want 5", lo, fits)
	}
}

// decimal128 join keys must compare on their EXACT 128-bit mantissa,
// not on Record.values' Float64(scale) echo. The echo is the same
// lossy projection processing/index_key.go already routes around for
// point-lookup keys: two decimals whose difference falls below
// float64's mantissa spacing collapse to one key and join rows that
// are not equal.
//
// The two mantissas below differ by 2 at a magnitude where float64's
// spacing is 256, so both round to the identical float.
func TestJoinKeyOf_Decimal128DistinguishesBeyondFloat64(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "amount", Type: encoding.FieldTypeDecimal128, ByteOffset: 0, CsvColumnIdx: 0, Scale: 0},
	}}
	spec := &types.JoinSpec{On: []types.OnPair{{LeftField: "amount", RightField: "amount"}}}

	base := int64(1) << 60
	a := encoding.NewDecimal128FromInt(base + 1)
	b := encoding.NewDecimal128FromInt(base + 3)

	if a.Float64(0) != b.Float64(0) {
		t.Fatalf("fixture is wrong: the two decimals must share a float64 echo (%v vs %v)",
			a.Float64(0), b.Float64(0))
	}

	recA := NewRecordWithWide(schema,
		map[string]float64{"amount": a.Float64(0)}, nil,
		map[string]any{"amount": a})
	recB := NewRecordWithWide(schema,
		map[string]float64{"amount": b.Float64(0)}, nil,
		map[string]any{"amount": b})

	keyA, okA := joinKeyOf(recA, schema, spec, false)
	keyB, okB := joinKeyOf(recB, schema, spec, false)
	if !okA || !okB {
		t.Fatalf("joinKeyOf refused a non-null decimal128 key: %v / %v", okA, okB)
	}
	if keyA == keyB {
		t.Errorf("two distinct decimal128 values produced the same join key %q — "+
			"the key is riding the lossy float64 echo", keyA)
	}
}

// The companion direction: two decimal128 values that ARE equal must
// still produce the same key, so the exact-bytes path does not simply
// make every decimal key unique.
func TestJoinKeyOf_Decimal128EqualValuesShareAKey(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "amount", Type: encoding.FieldTypeDecimal128, ByteOffset: 0, CsvColumnIdx: 0, Scale: 2},
	}}
	spec := &types.JoinSpec{On: []types.OnPair{{LeftField: "amount", RightField: "amount"}}}

	d := encoding.NewDecimal128FromInt(12345)
	mk := func() *Record {
		return NewRecordWithWide(schema,
			map[string]float64{"amount": d.Float64(2)}, nil,
			map[string]any{"amount": d})
	}
	keyA, okA := joinKeyOf(mk(), schema, spec, false)
	keyB, okB := joinKeyOf(mk(), schema, spec, false)
	if !okA || !okB {
		t.Fatalf("joinKeyOf refused a non-null decimal128 key")
	}
	if keyA != keyB {
		t.Errorf("equal decimal128 values produced different keys %q vs %q", keyA, keyB)
	}
}
