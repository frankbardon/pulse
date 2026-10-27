//go:build heavy

package regmmm

import (
	"context"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/extend"
)

// TestMMMBayes_FitMatchesFitContext: Fit is FitContext under a
// background context, byte for byte.
func TestMMMBayes_FitMatchesFitContext(t *testing.T) {
	t.Parallel()

	model, err := BayesFactory(BayesSpec(FastParams), nil)
	if err != nil {
		t.Fatal(err)
	}
	rows := make(probeRows, 0, BayesWeeks)
	for _, r := range BayesRows(11) {
		rows = append(rows, probeRow{"week": r.Week, "tv": r.TV, "price": r.Price, "sales": r.Sales})
	}
	a, errA := model.(extend.BufferedRegression).Fit(rows)
	b, errB := model.(extend.ContextRegression).FitContext(context.Background(), rows)
	if errA != nil || errB != nil {
		t.Fatalf("Fit %v, FitContext %v", errA, errB)
	}
	if !reflect.DeepEqual(a, b) {
		t.Errorf("Fit and FitContext differ:\n%+v\n%+v", a, b)
	}
}
