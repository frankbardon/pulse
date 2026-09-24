package descriptor

import (
	"slices"
	"testing"
)

// TestCapabilities_IgnoresFieldDeclarationsAreCoherent holds the shape of
// an Operator.IgnoresField declaration.
//
// # The defect it closes
//
// AGG_RATIO declared AcceptsTypes on its Field slot while its own
// Description said "The Aggregation's own Field is ignored" — it reads
// Params.numerator_field / denominator_field. The list therefore told
// every MCP client something about a slot the operator discards, and
// nothing anywhere could tell that apart from a genuine "every type is
// welcome" claim.
//
// IgnoresField makes the distinction explicit. This gate keeps the
// explicit form coherent:
//
//   - An ignored slot cannot REFUSE a type, so AcceptsTypes must list
//     every cohort field type. A narrowed list on an ignored slot is the
//     original defect wearing a flag.
//   - The operator must say where its inputs actually come from: at least
//     one Param of Type "field", each carrying a FieldFilter, so the type
//     constraint sits on the slot that is really read.
//
// The runtime half — that the Field slot really is ignored — is
// processing.TestAggregators_IgnoredFieldSlotIsReallyIgnored.
func TestCapabilities_IgnoresFieldDeclarationsAreCoherent(t *testing.T) {
	all := [][]Operator{
		aggregatorCapabilities(),
		attributeCapabilities(),
		filtererCapabilities(),
		grouperCapabilities(),
		windowCapabilities(),
		featureCapabilities(),
	}

	every := rawCohortFieldTypes()
	wantTypes := make([]string, 0, len(every))
	for _, ft := range every {
		wantTypes = append(wantTypes, ft.Name)
	}

	found := 0
	for _, group := range all {
		for _, op := range group {
			if !op.IgnoresField {
				continue
			}
			found++
			t.Run(op.Name, func(t *testing.T) {
				for _, want := range wantTypes {
					if !slices.Contains(op.AcceptsTypes, want) {
						t.Errorf("%s ignores its Field slot but omits %q from AcceptsTypes; "+
							"an ignored slot refuses nothing, so a narrowed list is a type claim "+
							"about a slot the operator discards", op.Name, want)
					}
				}

				fieldParams := 0
				for _, p := range op.Params {
					if p.Type != "field" {
						continue
					}
					fieldParams++
					if p.FieldFilter == "" {
						t.Errorf("%s param %q names a field but carries no FieldFilter; "+
							"the type constraint belongs on the slot that is actually read",
							op.Name, p.Name)
					}
				}
				if fieldParams == 0 {
					t.Errorf("%s ignores its Field slot and declares no Param of Type \"field\"; "+
						"a client cannot tell where its inputs come from", op.Name)
				}
			})
		}
	}

	if found == 0 {
		t.Fatal("no operator declares IgnoresField; AGG_RATIO should. " +
			"Either the flag was dropped or the capability walk is broken")
	}
}
