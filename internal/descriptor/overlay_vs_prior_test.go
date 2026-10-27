package descriptor

import (
	"testing"

	"github.com/frankbardon/pulse/internal/overlayhost"
	"github.com/frankbardon/pulse/types"
)

// TestPriorWelfordMeasure_MatchesComponentSchemas pins
// overlayhost.PriorWelfordMeasure (the runtime's and predict's shared
// built-in rule) to the manifest: a built-in aggregation is a T / Z
// prior-period measure iff its declared ComponentSchema carries both
// `mean` and `variance` — the keys the leg is read from.
func TestPriorWelfordMeasure_MatchesComponentSchemas(t *testing.T) {
	t.Parallel()

	schemas := aggregatorComponentSchemaIndex(nil)
	admitted := 0
	for _, at := range types.AllAggregationTypes() {
		schema, ok := schemas[string(at)]
		if !ok {
			t.Fatalf("%s has no component schema entry", at)
		}
		have := map[string]bool{}
		for _, k := range schema.Keys {
			have[k.Name] = true
		}
		want := have["mean"] && have["variance"]
		if got := overlayhost.PriorWelfordMeasure(at); got != want {
			t.Errorf("%s: PriorWelfordMeasure = %v, schema carries mean+variance = %v", at, got, want)
		}
		if want {
			admitted++
			if welfordTripleCellRefused(at, nil) {
				t.Errorf("%s: the extension-path schema check refuses a measure the built-in rule admits", at)
			}
		}
	}
	if admitted == 0 {
		t.Fatal("no built-in admitted: the pin proves nothing")
	}
}
