package processing

import (
	stderrors "errors"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestPlanRowTests_HiddenVectorTestRefusedAsUnknown: planRowTests
// routes a hidden row test to the unknown route before the vector
// rules run (the p.exts.isHidden resolver callback handed to
// vectors.ResolveTests), so a hidden vector test is refused as
// PULSE_TEST_UNKNOWN_TYPE, the refusal a never-registered name gets.
// It never reaches its vector-input rules. The same slot on an
// unscoped registry fails on those rules (SERVICE_VALIDATION,
// no_vector_or_fields), which shows the hidden refusal comes from the
// hiding.
func TestPlanRowTests_HiddenVectorTestRefusedAsUnknown(t *testing.T) {
	t.Parallel()

	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "a", Type: encoding.FieldTypeF64},
		{Name: "b", Type: encoding.FieldTypeF64},
	}}
	req := func(typ types.TestType) *types.Request {
		// Neither vector nor fields: a vector test's own rules refuse it.
		return &types.Request{Tests: []*types.Test{{Type: typ, Label: "probe"}}}
	}
	code := func(t *testing.T, p *Processor, typ types.TestType) (errors.Code, map[string]any) {
		t.Helper()
		_, err := p.planRowTests(req(typ))
		if err == nil {
			t.Fatalf("%s planned without a refusal", typ)
		}
		var ce *errors.CodedError
		if !stderrors.As(err, &ce) {
			t.Fatalf("%s: uncoded error %v", typ, err)
		}
		return ce.Code, ce.Details
	}

	for _, typ := range []types.TestType{types.TEST_BOX_M, types.TEST_MARDIA} {
		t.Run(string(typ), func(t *testing.T) {
			t.Parallel()

			open := NewProcessorWithExtensions(schema, nil)
			if got, details := code(t, open, typ); got != errors.SERVICE_VALIDATION || details["reason"] != "no_vector_or_fields" {
				t.Fatalf("unscoped %s: got %s %v, want SERVICE_VALIDATION no_vector_or_fields (the case is vacuous otherwise)", typ, got, details)
			}

			scoped := NewProcessorWithExtensions(schema, (*ExtensionRegistry)(nil).WithHidden(hiddenSet(string(typ))))
			got, details := code(t, scoped, typ)
			if got != errors.PULSE_TEST_UNKNOWN_TYPE {
				t.Errorf("hidden %s: got %s %v, want PULSE_TEST_UNKNOWN_TYPE", typ, got, details)
			}
			if details["type"] != string(typ) || details["tier"] != "tests" {
				t.Errorf("hidden %s: details %v, want type=%s tier=tests", typ, details, typ)
			}
			never := types.TestType(string(typ) + "_NEVER_REGISTERED")
			if nc, _ := code(t, scoped, never); nc != got {
				t.Errorf("hidden %s refused as %s, never-registered as %s", typ, got, nc)
			}
		})
	}
}
