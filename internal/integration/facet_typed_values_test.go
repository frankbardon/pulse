package integration_test

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
)

// TestFacet_RendersValuesByFieldType (#785): pulse_facet's streamed
// path lists each distinct value as the typed string Sample renders —
// a date as YYYY-MM-DD, a datetime as RFC 3339 UTC, a decimal128 at
// its column scale, a u64 past 2^53 as its exact word — in first-seen
// order, a null never listed; a plain number keeps its shortest float.
// Before, every value went through FormatFloat: an epoch-day count,
// epoch seconds, "87.5" for 87.50, and two u64 words whose float64
// echoes collide folded into one rounded value.
func TestFacet_RendersValuesByFieldType(t *testing.T) {
	t.Parallel()
	fsys := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	schema := encoding.Schema{Fields: []encoding.Field{
		{Name: "d", Type: encoding.FieldTypeDate, Nullable: true, Description: "Day the account was opened"},
		{Name: "ts", Type: encoding.FieldTypeDateTime, Description: "Moment the account last changed"},
		{Name: "amt", Type: encoding.FieldTypeDecimal128, Precision: 18, Scale: 2, Description: "Fee charged on the account"},
		{Name: "big", Type: encoding.FieldTypeU64, Description: "External account identifier"},
		{Name: "x", Type: encoding.FieldTypeF64, Description: "Balance outstanding on the account"},
	}}
	b, err := p.NewCohortBuilder(ctx, "typed.pulse", schema, pulse.CohortBuilderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	const big = uint64(1)<<53 + 1 // its float64 echo is 2^53
	rows := []pulse.CohortRow{
		{int32(19723), int64(1704067200), encoding.NewDecimal128FromInt(8750), big, 2.5},
		{int32(-1), int64(-1), encoding.NewDecimal128FromInt(-325), big - 1, -1.0},
		{nil, int64(1704067200), encoding.NewDecimal128FromInt(8750), big, 2.5},
		{int32(0), int64(0), encoding.NewDecimal128FromInt(100000), uint64(7), 0.0},
	}
	for _, r := range rows {
		if err := b.Append(r); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.Close(); err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{
		"d":   {"2024-01-01", "1969-12-31", "1970-01-01"},
		"ts":  {"2024-01-01T00:00:00Z", "1969-12-31T23:59:59Z", "1970-01-01T00:00:00Z"},
		"amt": {"87.50", "-3.25", "1000.00"},
		"big": {fmt.Sprint(big), fmt.Sprint(big - 1), "7"},
		"x":   {"2.5", "-1", "0"},
	}
	for field, w := range want {
		got, err := p.Facet(ctx, "typed.pulse", field)
		if err != nil {
			t.Fatalf("%s: %v", field, err)
		}
		if !reflect.DeepEqual(got, w) {
			t.Errorf("Facet %s = %q, want %q", field, got, w)
		}
	}
	// The same spelling Sample hands back for the first row.
	sample, err := p.Sample(ctx, "typed.pulse", 1)
	if err != nil {
		t.Fatal(err)
	}
	for field, w := range want {
		if field == "x" {
			continue
		}
		if got := fmt.Sprint(sample[0][field]); got != w[0] {
			t.Errorf("Sample %s = %q, Facet lists %q", field, got, w[0])
		}
	}
}
