package processing

import (
	"fmt"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// Wide-set coverage for the four set filterers. The narrow cases live in
// filterer_set_test.go and must keep passing unmodified; everything here
// exercises a dictionary that runs past bit 63, which is where a
// uint64-shaped operator silently returns a plausible wrong answer.
//
// The bits under test — 0, 63, 64, 127, 128, 191, 192, 255 — are the
// word boundaries of encoding.SetMask's [4]uint64 backing array plus the
// first and last bit of the whole address space.

// setOpBoundaryBits are the indices a [4]uint64 mask gets wrong when a
// word index is computed with the wrong divisor or a shift overflows.
var setOpBoundaryBits = []int{0, 63, 64, 127, 128, 191, 192, 255}

// setOpLabel is the dictionary label assigned to bit i by
// makeWideSetSchema (record_set_mask_test.go), which is also the
// wide-rung schema builder used throughout this file.
func setOpLabel(i int) string { return fmt.Sprintf("T%d", i) }

// setOpNullRecord builds a Record whose "tags" field is null — the
// per-record bitmap signal, not an empty mask.
func setOpNullRecord(schema *encoding.Schema) *Record {
	return NewRecordWithNulls(schema,
		map[string]float64{"tags": 0},
		map[string]bool{"tags": true})
}

func buildSetFilterFn(t *testing.T, b FiltererBuilder, typ types.FiltererType, schema *encoding.Schema, values []string) FilterFunc {
	t.Helper()
	fn, err := b.Build(&types.Filterer{Type: typ, Field: "tags", Values: values}, schema)
	if err != nil {
		t.Fatalf("Build(%s, %v): %v", typ, values, err)
	}
	return fn
}

func TestFilterSetWide_ContainsAny_WordBoundaries(t *testing.T) {
	schema := makeWideSetSchema(t, encoding.FieldTypeSetU256, 256)
	for _, bit := range setOpBoundaryBits {
		fn := buildSetFilterFn(t, newSetContainsAnyFilterer(), types.FILTER_SET_CONTAINS_ANY,
			schema, []string{setOpLabel(bit)})

		// The requested bit alone matches.
		got, err := fn(makeWideSetRecord(schema, maskWithBits(bit)))
		if err != nil {
			t.Fatalf("bit %d: %v", bit, err)
		}
		if !got {
			t.Errorf("bit %d: mask with only that bit did not match CONTAINS_ANY", bit)
		}

		// Every OTHER boundary bit must not match — this is what catches a
		// word-index or shift error that lands the query on the wrong word.
		for _, other := range setOpBoundaryBits {
			if other == bit {
				continue
			}
			got, err := fn(makeWideSetRecord(schema, maskWithBits(other)))
			if err != nil {
				t.Fatalf("bit %d vs %d: %v", bit, other, err)
			}
			if got {
				t.Errorf("query bit %d matched a mask holding only bit %d", bit, other)
			}
		}

		// Empty mask and null both fail CONTAINS_ANY.
		if got, _ := fn(makeWideSetRecord(schema, encoding.SetMask{})); got {
			t.Errorf("bit %d: empty mask matched CONTAINS_ANY", bit)
		}
		if got, _ := fn(setOpNullRecord(schema)); got {
			t.Errorf("bit %d: null matched CONTAINS_ANY", bit)
		}
	}
}

func TestFilterSetWide_ContainsAny_EmptyQueryMatchesNothing(t *testing.T) {
	schema := makeWideSetSchema(t, encoding.FieldTypeSetU256, 256)
	fn := buildSetFilterFn(t, newSetContainsAnyFilterer(), types.FILTER_SET_CONTAINS_ANY, schema, nil)
	for _, bit := range setOpBoundaryBits {
		if got, _ := fn(makeWideSetRecord(schema, maskWithBits(bit))); got {
			t.Errorf("empty query matched a mask holding bit %d", bit)
		}
	}
}

func TestFilterSetWide_ContainsAll_206Members(t *testing.T) {
	schema := makeWideSetSchema(t, encoding.FieldTypeSetU256, 256)
	const members = 206
	values := make([]string, 0, members)
	all := make([]int, 0, members)
	for i := 0; i < members; i++ {
		values = append(values, setOpLabel(i))
		all = append(all, i)
	}
	fn := buildSetFilterFn(t, newSetContainsAllFilterer(), types.FILTER_SET_CONTAINS_ALL, schema, values)

	full := maskWithBits(all...)
	if full.PopCount() != members {
		t.Fatalf("test fixture: full mask popcount = %d, want %d", full.PopCount(), members)
	}
	if got, err := fn(makeWideSetRecord(schema, full)); err != nil || !got {
		t.Errorf("exact 206-member mask: got %v (err %v), want true", got, err)
	}

	// A superset still passes.
	superset := full
	for b := members; b < 256; b++ {
		superset = superset.WithBit(b)
	}
	if got, _ := fn(makeWideSetRecord(schema, superset)); !got {
		t.Error("superset of the 206 requested members did not pass CONTAINS_ALL")
	}

	// Dropping any one requested member fails — including members that sit
	// above bit 64, which is the case a uint64 query mask gets wrong.
	for _, missing := range []int{0, 63, 64, 100, 127, 128, 191, 192, 205} {
		if got, err := fn(makeWideSetRecord(schema, full.WithoutBit(missing))); err != nil || got {
			t.Errorf("mask missing requested bit %d: got %v (err %v), want false", missing, got, err)
		}
	}

	// A member the request never asked for is irrelevant to the verdict.
	if got, _ := fn(makeWideSetRecord(schema, full.WithBit(255))); !got {
		t.Error("an extra unrequested member at bit 255 broke CONTAINS_ALL")
	}

	if got, _ := fn(makeWideSetRecord(schema, encoding.SetMask{})); got {
		t.Error("empty mask passed a 206-member CONTAINS_ALL")
	}
	if got, _ := fn(setOpNullRecord(schema)); got {
		t.Error("null passed a 206-member CONTAINS_ALL")
	}
}

func TestFilterSetWide_ContainsNone_WordBoundaries(t *testing.T) {
	schema := makeWideSetSchema(t, encoding.FieldTypeSetU256, 256)
	for _, bit := range setOpBoundaryBits {
		fn := buildSetFilterFn(t, newSetContainsNoneFilterer(), types.FILTER_SET_CONTAINS_NONE,
			schema, []string{setOpLabel(bit)})

		if got, err := fn(makeWideSetRecord(schema, maskWithBits(bit))); err != nil || got {
			t.Errorf("bit %d present: got %v (err %v), want false", bit, got, err)
		}
		for _, other := range setOpBoundaryBits {
			if other == bit {
				continue
			}
			if got, _ := fn(makeWideSetRecord(schema, maskWithBits(other))); !got {
				t.Errorf("query bit %d rejected a mask holding only bit %d", bit, other)
			}
		}
		// Empty mask and null both pass CONTAINS_NONE.
		if got, _ := fn(makeWideSetRecord(schema, encoding.SetMask{})); !got {
			t.Errorf("bit %d: empty mask failed CONTAINS_NONE", bit)
		}
		if got, _ := fn(setOpNullRecord(schema)); !got {
			t.Errorf("bit %d: null failed CONTAINS_NONE", bit)
		}
	}
}

// FILTER_SET_EQUALS has to keep three states apart that a truncating
// operator collapses: an empty mask (respondent picked nothing), a null
// (no answer at all) and a single-member mask above bit 63.
func TestFilterSetWide_Equals_EmptyNullSingleMember(t *testing.T) {
	cases := []struct {
		name    string
		ft      encoding.FieldType
		entries int
		member  int
	}{
		{"set_u128 at bit 127", encoding.FieldTypeSetU128, 128, 127},
		{"set_u128 at bit 64", encoding.FieldTypeSetU128, 128, 64},
		{"set_u256 at bit 255", encoding.FieldTypeSetU256, 256, 255},
		{"set_u256 at bit 128", encoding.FieldTypeSetU256, 256, 128},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			schema := makeWideSetSchema(t, c.ft, c.entries)
			fn := buildSetFilterFn(t, newSetEqualsFilterer(), types.FILTER_SET_EQUALS,
				schema, []string{setOpLabel(c.member)})

			if got, err := fn(makeWideSetRecord(schema, maskWithBits(c.member))); err != nil || !got {
				t.Errorf("single-member mask: got %v (err %v), want true", got, err)
			}
			if got, _ := fn(makeWideSetRecord(schema, encoding.SetMask{})); got {
				t.Error("empty mask equalled a single-member query")
			}
			if got, _ := fn(setOpNullRecord(schema)); got {
				t.Error("null equalled a single-member query")
			}
			// A superset is not an equal.
			if got, _ := fn(makeWideSetRecord(schema, maskWithBits(c.member, 0))); got {
				t.Error("a two-member mask equalled a single-member query")
			}
			// The low word alone is not an equal — the exact failure mode a
			// uint64 query mask produces for a member above bit 63.
			if c.member >= 64 {
				if got, _ := fn(makeWideSetRecord(schema, maskWithBits(c.member%64))); got {
					t.Errorf("mask holding only bit %d equalled a query for bit %d",
						c.member%64, c.member)
				}
			}

			// An empty query (no Values) equals an empty mask and nothing else,
			// and still does not equal a null.
			empty := buildSetFilterFn(t, newSetEqualsFilterer(), types.FILTER_SET_EQUALS, schema, nil)
			if got, err := empty(makeWideSetRecord(schema, encoding.SetMask{})); err != nil || !got {
				t.Errorf("empty query vs empty mask: got %v (err %v), want true", got, err)
			}
			if got, _ := empty(makeWideSetRecord(schema, maskWithBits(c.member))); got {
				t.Error("empty query equalled a single-member mask")
			}
			if got, _ := empty(setOpNullRecord(schema)); got {
				t.Error("empty query equalled a null — an empty mask is not a null")
			}
		})
	}
}

// The construction-time guard used to reject any dictionary ID at or
// above 64, which made a perfectly valid wide-rung label unusable.
func TestFilterSetWide_BuildAcceptsLabelAboveBit63(t *testing.T) {
	for _, tc := range []struct {
		ft      encoding.FieldType
		entries int
		bit     int
	}{
		{encoding.FieldTypeSetU128, 128, 64},
		{encoding.FieldTypeSetU128, 128, 127},
		{encoding.FieldTypeSetU256, 256, 200},
		{encoding.FieldTypeSetU256, 256, 255},
	} {
		schema := makeWideSetSchema(t, tc.ft, tc.entries)
		for _, b := range []FiltererBuilder{
			newSetContainsAnyFilterer(), newSetContainsAllFilterer(),
			newSetContainsNoneFilterer(), newSetEqualsFilterer(),
		} {
			if _, err := b.Build(&types.Filterer{
				Type:   types.FILTER_SET_CONTAINS_ANY,
				Field:  "tags",
				Values: []string{setOpLabel(tc.bit)},
			}, schema); err != nil {
				t.Errorf("%s bit %d: Build rejected a valid label: %v", tc.ft, tc.bit, err)
			}
		}
	}
}

// Lifting the 64-bit guard must not lift the RUNG guard: a label whose
// bit the declared field type cannot store is still a config error,
// because such a row can never match and the request is meaningless.
func TestFilterSetWide_BuildRejectsLabelBeyondRungCapacity(t *testing.T) {
	// A set_u8 field whose dictionary has outgrown its rung.
	schema := makeWideSetSchema(t, encoding.FieldTypeSetU8, 16)
	_, err := newSetContainsAnyFilterer().Build(&types.Filterer{
		Type:   types.FILTER_SET_CONTAINS_ANY,
		Field:  "tags",
		Values: []string{setOpLabel(10)},
	}, schema)
	if err == nil {
		t.Fatal("expected a config error for a label beyond the rung's capacity, got nil")
	}
	// The label within capacity still builds.
	if _, err := newSetContainsAnyFilterer().Build(&types.Filterer{
		Type:   types.FILTER_SET_CONTAINS_ANY,
		Field:  "tags",
		Values: []string{setOpLabel(7)},
	}, schema); err != nil {
		t.Fatalf("label at bit 7 of a set_u8 rejected: %v", err)
	}
}

// The universal filterer floor on a wide column. n_null_input counts
// TRUE nulls only — an empty mask is a present value that simply fails
// the predicate, and must land in n_in minus n_out, never in
// n_null_input.
func TestFilterSetWide_ComponentsFloor_EmptyMaskIsNotNull(t *testing.T) {
	schema := makeWideSetSchema(t, encoding.FieldTypeSetU256, 256)
	filter := &types.Filterer{
		Type:   types.FILTER_SET_CONTAINS_ANY,
		Field:  "tags",
		Values: []string{setOpLabel(200)},
	}
	fn := buildSetFilterFn(t, newSetContainsAnyFilterer(), filter.Type, schema, filter.Values)

	records := []*Record{
		makeWideSetRecord(schema, maskWithBits(200)),      // pass
		makeWideSetRecord(schema, maskWithBits(200, 255)), // pass
		makeWideSetRecord(schema, maskWithBits(3)),        // present, fails
		makeWideSetRecord(schema, encoding.SetMask{}),     // empty mask: present, fails
		setOpNullRecord(schema),                           // null: fails, and counts as null input
		setOpNullRecord(schema),
	}

	filterers := []*types.Filterer{filter}
	counters := newFilterPassCounters(filterers)
	for _, r := range records {
		if _, err := applyFilterPass(r, filterers, []FilterFunc{fn}, counters); err != nil {
			t.Fatalf("applyFilterPass: %v", err)
		}
	}
	comps := buildFiltererComponents(filterers, counters)
	if len(comps) != 1 {
		t.Fatalf("components len = %d, want 1", len(comps))
	}
	got := comps[0]
	if got.NIn != 6 {
		t.Errorf("n_in = %d, want 6", got.NIn)
	}
	if got.NOut != 2 {
		t.Errorf("n_out = %d, want 2", got.NOut)
	}
	if got.NNullInput != 2 {
		t.Errorf("n_null_input = %d, want 2 (true nulls only; an empty mask is not a null)", got.NNullInput)
	}
}

// A numeric filterer reading a set column through Record.NumericValue
// sees the low 64 bits of the mask re-expressed as a float64 — a
// plausible number, never an error. FILTER_RANGE declares numeric field
// types only, so the mismatch is refused at build time rather than
// answered wrongly at run time.
func TestFilterRange_RejectsSetField(t *testing.T) {
	for _, ft := range []encoding.FieldType{
		encoding.FieldTypeSetU8, encoding.FieldTypeSetU64,
		encoding.FieldTypeSetU128, encoding.FieldTypeSetU256,
	} {
		schema := makeWideSetSchema(t, ft, 8)
		_, err := newRangeFilterer().Build(&types.Filterer{
			Type:   types.FILTER_RANGE,
			Field:  "tags",
			Values: []string{"0", "100"},
		}, schema)
		if err == nil {
			t.Errorf("%s: FILTER_RANGE built against a set field; expected a config error", ft)
		}
	}

	// A numeric field is unaffected.
	numeric := &encoding.Schema{Fields: []encoding.Field{{Name: "n", Type: encoding.FieldTypeU32}}}
	if _, err := newRangeFilterer().Build(&types.Filterer{
		Type:   types.FILTER_RANGE,
		Field:  "n",
		Values: []string{"0", "100"},
	}, numeric); err != nil {
		t.Fatalf("FILTER_RANGE on a u32 field rejected: %v", err)
	}
}
