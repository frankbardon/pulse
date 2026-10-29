package descriptor

import (
	"strings"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// TestMaterializeGroupSpecs_KeyIsAlwaysAMember pins why the cohort
// writer's "key is not a member of the group" refusal (encx.NewGroupEncoder)
// cannot be reached through materialize (cohort-reshape E6-S3):
// materializeGroupSpecs lowers a declaration to Members = Key ∪ Members, key
// first, so every key names a member. The encoder keeps the refusal — its
// GroupSpec contract admits a stray key, and without it the key would be
// ignored — and tests it directly (TestGroupDeclarationRefusalsAreCoded).
// The walk covers every key choice against every member subset: some
// declarations are planned, others refused (an unknown name, the key
// repeated as a member), and none is refused as a non-member key.
func TestMaterializeGroupSpecs_KeyIsAlwaysAMember(t *testing.T) {
	t.Parallel()

	s := groupedSourceSchema()
	layout := []MaterializeOutputField{srcCol(s, 0, "k"), srcCol(s, 1, "a"), srcCol(s, 5, "x")}
	names := []string{"k", "a", "x", "nope"}
	planned, refused := 0, 0
	for _, key := range append([]string{""}, names...) {
		for mask := 0; mask < 1<<len(names); mask++ {
			d := types.MaterializeGroup{Members: []string{}}
			if key != "" {
				d.Key = []string{key}
			}
			for i, n := range names {
				if mask&(1<<i) != 0 {
					d.Members = append(d.Members, n)
				}
			}
			spec := materializeGroupSpecs([]types.MaterializeGroup{d})[0]
			for _, k := range spec.Key {
				if len(spec.Members) == 0 || spec.Members[0] != k {
					t.Errorf("%+v lowered to members %v: key %q is not the leading member", d, spec.Members, k)
				}
			}
			_, ce := MaterializeGroupPlan(&types.MaterializeOutput{Groups: []types.MaterializeGroup{d}}, s, layout)
			if ce == nil {
				planned++
				continue
			}
			refused++
			if strings.Contains(ce.Message, "is not a member of the group") {
				t.Errorf("%+v refused as a non-member key: %v", d, ce)
			}
		}
	}
	if planned == 0 || refused == 0 {
		t.Fatalf("planned %d, refused %d; the walk must exercise both", planned, refused)
	}
}
