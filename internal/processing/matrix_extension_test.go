package processing

import (
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// TestMatrixExtension_CanStream: the runtime routes an extension MAT_*
// spec on its DECLARED tier — the co-moment tier streams, the buffered
// tier and a hidden name do not — never on the built-in
// types.MatrixSpec.Streamable, which knows no extension name.
func TestMatrixExtension_CanStream(t *testing.T) {
	t.Parallel()

	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "a", Type: encoding.FieldTypeF64},
		{Name: "b", Type: encoding.FieldTypeF64, ByteOffset: 8},
	}}
	exts := &ExtensionRegistry{MatrixOps: map[types.MatrixType]MatrixOp{
		"MAT_X_CO":   {Streamable: true},
		"MAT_X_ROWS": {Streamable: false},
	}}
	req := func(t types.MatrixType) *types.Request {
		return &types.Request{Matrices: []types.MatrixSpec{{Type: t, Fields: []string{"a", "b"}}}}
	}
	p := NewProcessorWithExtensions(schema, exts)
	for _, c := range []struct {
		t    types.MatrixType
		want bool
	}{{"MAT_X_CO", true}, {"MAT_X_ROWS", false}, {"MAT_X_NONE", false}, {types.MAT_COVARIANCE, true}} {
		if got := p.canStream(req(c.t)); got != c.want {
			t.Errorf("canStream(%s) = %v, want %v", c.t, got, c.want)
		}
		if got := exts.MatrixSpecStreamable(req(c.t).Matrices[0]); got != c.want {
			t.Errorf("MatrixSpecStreamable(%s) = %v, want %v", c.t, got, c.want)
		}
	}
	hidden := exts.WithHidden(func(n string) bool { return n == "MAT_X_CO" })
	if NewProcessorWithExtensions(schema, hidden).canStream(req("MAT_X_CO")) {
		t.Error("a hidden extension matrix streams")
	}
	if _, ok := hidden.LookupMatrixOp("MAT_X_CO"); ok {
		t.Error("LookupMatrixOp resolves a hidden name")
	}
}
