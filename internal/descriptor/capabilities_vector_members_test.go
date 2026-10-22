package descriptor

import (
	"slices"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/types"
)

// TestCapabilities_VectorInputAcceptsMemberRule pins every vector-input
// operator's declared accepts_types to the vector member rule
// (vectors.MemberTypeAllowed, the check predict and the runtime both
// run on each member): the integer and float types plus packed_bool
// (admitted under coerce "binary") — the set the matrix operators
// declare. A `date` member is refused PULSE_VECTOR_MEMBER_TYPE, so a
// declaration listing it overstates what the operator takes.
func TestCapabilities_VectorInputAcceptsMemberRule(t *testing.T) {
	var want []string
	for _, ft := range knownScalarTypes {
		if vectors.MemberTypeAllowed(ft, types.VectorCoerceBinary) {
			want = append(want, ft.String())
		}
	}
	slices.Sort(want)

	n := 0
	for _, fam := range [][]descriptor.Operator{
		aggregatorCapabilities(), attributeCapabilities(), grouperCapabilities(),
		filtererCapabilities(), windowCapabilities(), featureCapabilities(),
	} {
		for _, op := range fam {
			if !isVectorInput(op.Name) {
				continue
			}
			n++
			got := slices.Clone(op.AcceptsTypes)
			slices.Sort(got)
			if !slices.Equal(got, want) {
				t.Errorf("%s: accepts_types %v, want the vector member rule %v", op.Name, got, want)
			}
		}
	}
	for _, m := range matrixCapabilities() {
		n++
		got := slices.Clone(m.AcceptsTypes)
		slices.Sort(got)
		if !slices.Equal(got, want) {
			t.Errorf("%s: accepts_types %v, want the vector member rule %v", m.Name, got, want)
		}
	}
	if n == 0 {
		t.Fatal("no vector-input operator walked")
	}
}
