package pulse_test

import (
	"testing"

	"github.com/frankbardon/pulse"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/exprenv"
)

// TestExtensions_ExprFunctionReservedBuiltins: an embedder ExprFunction
// named after any reserved vector / scalar-maths built-in is refused at
// pulse.New with PULSE_EXTENSION_NAME_RESERVED; a near-miss name and a
// name the reservation predates (abs) still register.
func TestExtensions_ExprFunctionReservedBuiltins(t *testing.T) {
	fn := func(x float64) float64 { return x }
	for _, name := range exprenv.Names() {
		t.Run(name, func(t *testing.T) {
			_, err := pulse.New(newTestOptions(pulse.Extensions{
				ExprFunctions: []pulse.ExprFunction{{Name: name, Fn: fn}},
			}))
			assertCodedError(t, err, perr.PULSE_EXTENSION_NAME_RESERVED)
		})
	}
	for _, name := range []string{"vsum2", "my_sqrt", "abs"} {
		if _, err := pulse.New(newTestOptions(pulse.Extensions{
			ExprFunctions: []pulse.ExprFunction{{Name: name, Fn: fn}},
		})); err != nil {
			t.Errorf("embedder %q: %v, want accepted", name, err)
		}
	}
}
