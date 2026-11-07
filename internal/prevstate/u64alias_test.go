package prevstate

import (
	"maps"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// TestU64Aliases: an ATTR_FORMULA that IS a bare u64 read aliases its
// label to the field — through $env, an optional chain, a let and an
// earlier alias — and nothing else does: a computed formula, a non-u64
// read, another attribute type, a shadowing label, a forward read.
func TestU64Aliases(t *testing.T) {
	t.Parallel()

	schema := keySchemaFixture(t)
	formula := func(label, expr string) *types.Attribute {
		return &types.Attribute{Type: types.ATTR_FORMULA, Label: label, Expression: expr}
	}
	attrs := []*types.Attribute{
		formula("fwd", "b"), // b is a later label: not yet an alias
		formula("a", "id"),
		formula("b", "a"),
		formula("c", "$env?.b"),
		formula("d", "let z = $env.id; z"),
		formula("e", "id + 0"),
		formula("f", "n"),
		formula("id", "id"),
		formula("g", "abs(id)"),
		formula("a", "n"), // a duplicate label keeps the first
		{Type: types.ATTR_ZSCORE, Field: "id", Label: "h"},
	}
	got := U64Aliases(attrs, schema)
	want := map[string]string{"a": "id", "b": "id", "c": "id", "d": "id"}
	if !maps.Equal(got, want) {
		t.Errorf("U64Aliases = %v, want %v", got, want)
	}
	if got := U64Aliases([]*types.Attribute{formula("e", "id * 2")}, schema); got != nil {
		t.Errorf("no alias: %v, want nil", got)
	}
	if got := U64Aliases(attrs, nil); got != nil {
		t.Errorf("nil schema: %v, want nil", got)
	}
}
