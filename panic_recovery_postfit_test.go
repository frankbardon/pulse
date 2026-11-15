package pulse

import (
	"testing"

	perr "github.com/frankbardon/pulse/errors"
)

// TestPostFit_PanicRecovered: a panicking post-fit operator is
// PROCESSING_PANIC recovered by the post-fit phase itself (details.op
// compose_post_fit), on both orchestrators, with recovery on (TestMain
// turns it off for the suite).
func TestPostFit_PanicRecovered(t *testing.T) {
	t.Parallel()

	p := enablePanicRecovery(postFitPulse(t, Options{}), Options{})
	for name, run := range composeRuns(p) {
		resp, err := run(postFitComposed(t, `[{"label":"p","type":"POSTFIT_TEST_ECHO","fits":["m"],"params":{"mode":"panic"}}]`))
		ce := requireCoded(t, err, perr.PROCESSING_PANIC)
		if resp != nil || ce.Details["op"] != "compose_post_fit" {
			t.Fatalf("%s: %v %+v", name, ce, ce.Details)
		}
	}
}
