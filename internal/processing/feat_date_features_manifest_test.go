package processing

import (
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/processing/feature"
	"github.com/frankbardon/pulse/types"
)

// TestFeatDateFeatures_ManifestNamesRealColumns: the manifest entry for
// FEAT_DATE_FEATURES names exactly the columns the operator writes —
// its EmitsTypeNote lists every <label>_<part> column Compute returns
// over a `datetime` (which adds `hour`), a `date` writes all of them but
// `hour`, and the Description names no part it does not emit.
func TestFeatDateFeatures_ManifestNamesRealColumns(t *testing.T) {
	factory, ok := feature.Lookup(types.FEAT_DATE_FEATURES)
	if !ok {
		t.Fatal("FEAT_DATE_FEATURES not registered")
	}
	columns := func(s *encoding.Schema, v float64) []string {
		t.Helper()
		comp, err := factory(&types.Feature{Type: types.FEAT_DATE_FEATURES, Field: "enrolled", Label: "d"}, s)
		if err != nil {
			t.Fatal(err)
		}
		recs := makeRecords(s, "enrolled", []float64{v})
		out, err := comp.Compute([]feature.Record{recs[0]}, "enrolled")
		if err != nil {
			t.Fatal(err)
		}
		var real []string
		for col := range out {
			real = append(real, strings.TrimPrefix(col, "d_"))
		}
		sort.Strings(real)
		return real
	}
	real := columns(&encoding.Schema{Fields: []encoding.Field{{Name: "enrolled", Type: encoding.FieldTypeDateTime}}}, 1.7e9)
	dateReal := columns(dateSchema(), 19000)

	op := manifestOperatorEntries()[string(types.FEAT_DATE_FEATURES)]
	var declared []string
	for _, m := range regexp.MustCompile(`<label>_([a-z_]+)`).FindAllStringSubmatch(op.EmitsTypeNote, -1) {
		declared = append(declared, m[1])
	}
	sort.Strings(declared)
	if strings.Join(declared, ",") != strings.Join(real, ",") {
		t.Errorf("EmitsTypeNote %q declares columns %v; the operator writes %v over a datetime", op.EmitsTypeNote, declared, real)
	}
	if want := slices.DeleteFunc(slices.Clone(real), func(c string) bool { return c == "hour" }); !slices.Equal(dateReal, want) {
		t.Errorf("over a date the operator writes %v, want %v (every datetime column but hour)", dateReal, want)
	}
	for _, ghost := range []string{"day_of_week", "is_weekend", "weekend"} {
		if strings.Contains(op.Description, ghost) || strings.Contains(op.EmitsTypeNote, ghost) {
			t.Errorf("manifest entry names %q, which FEAT_DATE_FEATURES never emits", ghost)
		}
	}
}
