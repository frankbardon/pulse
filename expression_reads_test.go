package pulse

import (
	stderrors "errors"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
)

// TestExpressionReads pins the facade: a clean parse returns the read
// set and a nil error (not a typed-nil *CodedError), a parse error the
// coded compile error.
func TestExpressionReads(t *testing.T) {
	t.Parallel()
	got, err := ExpressionReads(`wave == params.wave and lookup("t", id) > 0`)
	if err != nil {
		t.Fatalf("ExpressionReads: %v", err)
	}
	want := descriptor.ExprReads{Vars: []string{"wave", "id"}, Params: []string{"wave"}, Funcs: []string{"lookup"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ExpressionReads = %+v, want %+v", got, want)
	}

	_, err = ExpressionReads("wave ==")
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_FORMULA_COMPILE_ERROR {
		t.Fatalf("ExpressionReads(parse error) err = %v, want PULSE_FORMULA_COMPILE_ERROR", err)
	}
}
