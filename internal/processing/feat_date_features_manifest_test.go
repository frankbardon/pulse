package processing

import (
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/internal/processing/feature"
	"github.com/frankbardon/pulse/types"
)

// TestFeatDateFeatures_ManifestNamesRealColumns: the manifest entry for
// FEAT_DATE_FEATURES names exactly the columns the operator writes —
// its EmitsTypeNote lists every <label>_<part> column Compute returns,
// and the Description names no part it does not emit.
func TestFeatDateFeatures_ManifestNamesRealColumns(t *testing.T) {
	factory, ok := feature.Lookup(types.FEAT_DATE_FEATURES)
	if !ok {
		t.Fatal("FEAT_DATE_FEATURES not registered")
	}
	comp, err := factory(&types.Feature{Type: types.FEAT_DATE_FEATURES, Field: "enrolled", Label: "d"}, dateSchema())
	if err != nil {
		t.Fatal(err)
	}
	s := dateSchema()
	recs := makeRecords(s, "enrolled", []float64{19000})
	view := []feature.Record{recs[0]}
	out, err := comp.Compute(view, "enrolled")
	if err != nil {
		t.Fatal(err)
	}
	var real []string
	for col := range out {
		real = append(real, strings.TrimPrefix(col, "d_"))
	}
	sort.Strings(real)

	op := manifestOperatorEntries()[string(types.FEAT_DATE_FEATURES)]
	var declared []string
	for _, m := range regexp.MustCompile(`<label>_([a-z_]+)`).FindAllStringSubmatch(op.EmitsTypeNote, -1) {
		declared = append(declared, m[1])
	}
	sort.Strings(declared)
	if strings.Join(declared, ",") != strings.Join(real, ",") {
		t.Errorf("EmitsTypeNote %q declares columns %v; the operator writes %v", op.EmitsTypeNote, declared, real)
	}
	for _, ghost := range []string{"day_of_week", "is_weekend", "weekend"} {
		if strings.Contains(op.Description, ghost) || strings.Contains(op.EmitsTypeNote, ghost) {
			t.Errorf("manifest entry names %q, which FEAT_DATE_FEATURES never emits", ghost)
		}
	}
}
