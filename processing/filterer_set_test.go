package processing

import (
	"testing"

	"github.com/frankbardon/pulse/types"
)

// Reuses makeSetTestSchema + makeSetRecord from aggregator_set_test.go.

func TestFilterSetContainsAny(t *testing.T) {
	schema := makeSetTestSchema(t)
	builder := newSetContainsAnyFilterer()
	fn, err := builder.Build(&types.Filterer{
		Type:   types.FILTER_SET_CONTAINS_ANY,
		Field:  "tags",
		Values: []string{"VISA", "AMEX"},
	}, schema)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	cases := []struct {
		mask uint64
		want bool
	}{
		{0b0001, true},  // VISA
		{0b0100, true},  // AMEX
		{0b1010, false}, // MC + DISC, neither requested
		{0b0000, false}, // empty
	}
	for _, c := range cases {
		got, err := fn(makeSetRecord(schema, c.mask))
		if err != nil {
			t.Fatalf("filter mask %b: %v", c.mask, err)
		}
		if got != c.want {
			t.Errorf("mask %b: got %v, want %v", c.mask, got, c.want)
		}
	}
}

func TestFilterSetContainsAll(t *testing.T) {
	schema := makeSetTestSchema(t)
	builder := newSetContainsAllFilterer()
	fn, _ := builder.Build(&types.Filterer{
		Type:   types.FILTER_SET_CONTAINS_ALL,
		Field:  "tags",
		Values: []string{"VISA", "MC"},
	}, schema)
	cases := []struct {
		mask uint64
		want bool
	}{
		{0b0011, true},  // VISA + MC
		{0b0111, true},  // VISA + MC + AMEX
		{0b0001, false}, // VISA only
		{0b1100, false}, // AMEX + DISC
	}
	for _, c := range cases {
		got, _ := fn(makeSetRecord(schema, c.mask))
		if got != c.want {
			t.Errorf("mask %b: got %v, want %v", c.mask, got, c.want)
		}
	}
}

func TestFilterSetContainsNone(t *testing.T) {
	schema := makeSetTestSchema(t)
	builder := newSetContainsNoneFilterer()
	fn, _ := builder.Build(&types.Filterer{
		Type:   types.FILTER_SET_CONTAINS_NONE,
		Field:  "tags",
		Values: []string{"AMEX"},
	}, schema)
	cases := []struct {
		mask uint64
		want bool
	}{
		{0b0011, true},  // no AMEX
		{0b0100, false}, // AMEX
		{0b0111, false}, // includes AMEX
	}
	for _, c := range cases {
		got, _ := fn(makeSetRecord(schema, c.mask))
		if got != c.want {
			t.Errorf("mask %b: got %v, want %v", c.mask, got, c.want)
		}
	}
}

func TestFilterSetEquals(t *testing.T) {
	schema := makeSetTestSchema(t)
	builder := newSetEqualsFilterer()
	fn, _ := builder.Build(&types.Filterer{
		Type:   types.FILTER_SET_EQUALS,
		Field:  "tags",
		Values: []string{"VISA", "MC"},
	}, schema)
	cases := []struct {
		mask uint64
		want bool
	}{
		{0b0011, true},  // exact match
		{0b0111, false}, // superset
		{0b0001, false}, // subset
	}
	for _, c := range cases {
		got, _ := fn(makeSetRecord(schema, c.mask))
		if got != c.want {
			t.Errorf("mask %b: got %v, want %v", c.mask, got, c.want)
		}
	}
}

func TestFilterSet_UnknownLabelRejected(t *testing.T) {
	schema := makeSetTestSchema(t)
	builder := newSetContainsAnyFilterer()
	_, err := builder.Build(&types.Filterer{
		Type:   types.FILTER_SET_CONTAINS_ANY,
		Field:  "tags",
		Values: []string{"NOT_A_LABEL"},
	}, schema)
	if err == nil {
		t.Fatal("expected build error for unknown label, got nil")
	}
}
