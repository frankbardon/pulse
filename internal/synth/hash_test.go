package synth

import "testing"

func TestSpecHash_Deterministic(t *testing.T) {
	s := &Spec{
		RowCount: 100,
		Fields: []FieldSpec{
			{Name: "a", Type: "u32", Distribution: "uniform"},
		},
	}
	h1 := s.Hash()
	h2 := s.Hash()
	if h1 != h2 || len(h1) != 32 {
		t.Fatalf("synth.Spec.Hash unstable / wrong width: %q vs %q (len=%d)", h1, h2, len(h1))
	}
}

func TestSpecHash_NilSafe(t *testing.T) {
	var s *Spec
	if got := s.Hash(); len(got) != 32 {
		t.Fatalf("nil Spec.Hash must return 32-char hex, got %q", got)
	}
}

func TestSpecHash_SemanticEquivalence(t *testing.T) {
	a := &Spec{RowCount: 10, Fields: []FieldSpec{{Name: "x", Type: "u8", Distribution: "uniform"}}}
	b := &Spec{RowCount: 10, Fields: []FieldSpec{{Name: "x", Type: "u8", Distribution: "uniform"}}}
	if a.Hash() != b.Hash() {
		t.Fatalf("structurally identical specs hashed differently")
	}
}
