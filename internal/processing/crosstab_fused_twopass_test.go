package processing

import (
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

const fusedTwoPassExt types.AttributeType = "ATTR_TESTX_TWOPASS"

// twoPassFusedRegistry declares one extension attribute two_pass, the
// shape pulse.New builds for an AttributeModeTwoPass registration.
func twoPassFusedRegistry() *ExtensionRegistry {
	return &ExtensionRegistry{TwoPassAttributes: map[types.AttributeType]bool{fusedTwoPassExt: true}}
}

func withAttribute(at types.AttributeType) *types.Request {
	req := happyPathCrosstabRequest()
	req.Attributes = []*types.Attribute{{Type: at, Field: "score", Label: "derived"}}
	return req
}

// TestCanFuseCrosstab_TwoPassAttributeDeclines pins the gate on both
// halves of the two-pass set: the built-in table (no registry needed)
// and an extension attribute declared two_pass on the registry. A name
// the registry does NOT mark two_pass stays fusable — the decision is
// keyed by the declaration, not by "is an extension".
func TestCanFuseCrosstab_TwoPassAttributeDeclines(t *testing.T) {
	schema := crosstabFusedGateSchema()
	reg := twoPassFusedRegistry()
	for _, tc := range []struct {
		name string
		at   types.AttributeType
		reg  *ExtensionRegistry
	}{
		{"builtin_nil_registry", types.ATTR_ZSCORE, nil},
		{"builtin_with_registry", types.ATTR_TSCORE, reg},
		{"extension_two_pass", fusedTwoPassExt, reg},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ok, reason := CanFuseCrosstab(withAttribute(tc.at), schema, tc.reg)
			if ok {
				t.Fatalf("CanFuseCrosstab(%s) = true, want false", tc.at)
			}
			if !strings.Contains(reason, "two-pass attribute") || !strings.Contains(reason, string(tc.at)) {
				t.Fatalf("unexpected reason %q", reason)
			}
		})
	}
	// Same extension name, registry that does not declare it two_pass.
	if ok, reason := CanFuseCrosstab(withAttribute(fusedTwoPassExt), schema, &ExtensionRegistry{}); !ok {
		t.Fatalf("undeclared extension attribute declined fusion: %q", reason)
	}
}

// TestAssertCanFuse_TwoPassExtensionAttribute pins the defensive echo:
// a two_pass extension attribute that drifts past the gate fails fast
// with PROCESSING_INTERNAL instead of being valued row-locally.
func TestAssertCanFuse_TwoPassExtensionAttribute(t *testing.T) {
	schema := crosstabFusedGateSchema()
	for _, tc := range []struct {
		name string
		at   types.AttributeType
	}{
		{"builtin", types.ATTR_ZSCORE},
		{"extension", fusedTwoPassExt},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := withAttribute(tc.at)
			state, err := NewFusedCrosstabState(req.Crosstab, schema, twoPassFusedRegistry())
			if err != nil {
				t.Fatalf("NewFusedCrosstabState: %v", err)
			}
			err = state.AssertCanFuse(req)
			ce, ok := err.(*errors.CodedError)
			if !ok || ce.Code != errors.PROCESSING_INTERNAL || !strings.Contains(ce.Message, string(tc.at)) {
				t.Fatalf("AssertCanFuse(%s) = %v, want PROCESSING_INTERNAL naming it", tc.at, err)
			}
		})
	}
}
