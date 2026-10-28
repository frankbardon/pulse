package spss

import (
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
)

// projectedSchema lays out the fixture cohort's fields as cols name them:
// each entry "from>as" copies the field from under the name as ("from"
// alone keeps the name).
func projectedSchema(t *testing.T, src *encoding.Schema, spec ...string) (*encoding.Schema, []ProjectedColumn) {
	t.Helper()
	out := &encoding.Schema{}
	cols := make([]ProjectedColumn, 0, len(spec))
	for _, s := range spec {
		from, as, ok := strings.Cut(s, ">")
		if !ok {
			as = from
		}
		f := src.Field(from)
		if f == nil {
			t.Fatalf("fixture has no field %q", from)
		}
		fld := *f
		fld.Name = as
		out.Fields = append(out.Fields, fld)
		cols = append(cols, ProjectedColumn{From: from})
	}
	return out, cols
}

func variableNames(doc *Document) []string {
	var names []string
	for _, v := range doc.Payload.Variables {
		names = append(names, v.Name)
	}
	return names
}

// TestSidecarCarry_ProjectionDropsRenamesDuplicates: the projected
// document holds exactly the written columns in output order — a dropped
// column's variable, variable-set membership and weight gone, a renamed
// one keeping its codes, labels and short name under its new name (the
// registry's sources and the response sets' fields following it), a
// duplicate a copy under a freshly minted short name, indices recounted.
func TestSidecarCarry_ProjectionDropsRenamesDuplicates(t *testing.T) {
	t.Parallel()

	fs, cohort, src := importFixture(t, richSpec())
	c, warn := PrepareSidecarCarry(fs, cohort)
	if warn != nil || c == nil {
		t.Fatalf("prepare: %v", warn)
	}
	full := carrySchema(t, fs, cohort)
	var brands string
	for _, d := range src.Payload.Derived {
		if d.Kind == DerivedKindMultipleDichotomy {
			brands = d.Name
		}
	}
	if brands == "" {
		t.Fatalf("fixture derived no set column: %+v", src.Payload.Derived)
	}
	schema, cols := projectedSchema(t, full,
		"MD1>first", "Satisfaction>sat", "MD2", brands, "Satisfaction>sat_copy")
	doc, err := c.project(schema, cols)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := variableNames(doc), []string{"first", "sat", "MD2", "sat_copy"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("variables = %v, want %v", got, want)
	}
	orig := variableNamed(src, "Satisfaction")
	sat, cp := variableNamed(doc, "sat"), variableNamed(doc, "sat_copy")
	if !reflect.DeepEqual(sat.Categories, orig.Categories) || !reflect.DeepEqual(cp.Categories, orig.Categories) {
		t.Errorf("renamed / copied categories %+v / %+v, want %+v", sat.Categories, cp.Categories, orig.Categories)
	}
	if sat.ShortName != orig.ShortName || sat.LongName != "sat" {
		t.Errorf("renamed short/long = %q/%q, want %q/sat", sat.ShortName, sat.LongName, orig.ShortName)
	}
	if strings.EqualFold(cp.ShortName, orig.ShortName) || cp.LongName != "sat_copy" {
		t.Errorf("copy short/long = %q/%q, want a fresh short name", cp.ShortName, cp.LongName)
	}
	for i, v := range doc.Payload.Variables {
		if i > 0 && v.Index <= doc.Payload.Variables[i-1].Index {
			t.Errorf("indices not recounted: %v", doc.Payload.Variables)
		}
	}
	if doc.Payload.Variables[0].Index != 1 || doc.Payload.Variables[3].Position != 4 {
		t.Errorf("first index %d, sat_copy position %d", doc.Payload.Variables[0].Index, doc.Payload.Variables[3].Position)
	}
	if doc.Payload.Weight != nil {
		t.Errorf("weight %+v survives the dropped WT", doc.Payload.Weight)
	}
	if len(doc.Payload.VariableSets) != 0 {
		t.Errorf("variable sets %+v survive the dropped REGION", doc.Payload.VariableSets)
	}
	if len(doc.Payload.Derived) != 1 || doc.Payload.Derived[0].Position != 3 ||
		!reflect.DeepEqual(doc.Payload.Derived[0].Sources, []string{"first", "MD2"}) {
		t.Errorf("registry %+v, want the set column at 3 reading first, MD2", doc.Payload.Derived)
	}
	for _, set := range doc.Payload.MultipleResponseSets {
		if !reflect.DeepEqual(set.Fields, []string{"first", "MD2"}) {
			t.Errorf("set %s fields %v, want the renamed member", set.Name, set.Fields)
		}
	}
	if len(doc.Payload.MultipleResponseSets) != len(src.Payload.MultipleResponseSets) {
		t.Errorf("response sets %d, want %d", len(doc.Payload.MultipleResponseSets), len(src.Payload.MultipleResponseSets))
	}
	// The source's resolution is never mutated.
	if variableNamed(c.doc, "Satisfaction") == nil || c.doc.Payload.Weight == nil {
		t.Error("the projection mutated the carried document")
	}
}

// TestSidecarCarry_ProjectionWeightAndSetsFollow: a renamed weight stays
// the weight under its new name and index; dropping a response set's
// member drops the set (and its registry column with it).
func TestSidecarCarry_ProjectionWeightAndSetsFollow(t *testing.T) {
	t.Parallel()

	fs, cohort, _ := importFixture(t, richSpec())
	c, _ := PrepareSidecarCarry(fs, cohort)
	schema, cols := projectedSchema(t, carrySchema(t, fs, cohort), "Satisfaction", "WT>weight", "MD1")
	doc, err := c.project(schema, cols)
	if err != nil {
		t.Fatal(err)
	}
	w := doc.Payload.Weight
	if w == nil || w.Variable != "weight" || w.Index != variableNamed(doc, "weight").Index {
		t.Errorf("weight = %+v, want the renamed weight at its index", w)
	}
	if len(doc.Payload.MultipleResponseSets) != 0 || len(doc.Payload.Derived) != 0 {
		t.Errorf("sets %+v / registry %+v survive the dropped MD2", doc.Payload.MultipleResponseSets, doc.Payload.Derived)
	}
}

// TestSidecarCarry_ProjectionRefusals: what a projection cannot describe
// truthfully refuses the carry — a registry column kept without a column
// it reads, a duplicated registry column, a rebuilt column a response
// set names, names SPSS folds together.
func TestSidecarCarry_ProjectionRefusals(t *testing.T) {
	t.Parallel()

	fs, cohort, src := importFixture(t, richSpec())
	c, _ := PrepareSidecarCarry(fs, cohort)
	full := carrySchema(t, fs, cohort)
	brands := src.Payload.Derived[len(src.Payload.Derived)-1].Name
	cases := map[string]func() (*encoding.Schema, []ProjectedColumn){
		"registry source dropped": func() (*encoding.Schema, []ProjectedColumn) {
			return projectedSchema(t, full, "MD1", brands)
		},
		"registry duplicated": func() (*encoding.Schema, []ProjectedColumn) {
			return projectedSchema(t, full, "MD1", "MD2", brands, brands+">again")
		},
		"rebuilt set member": func() (*encoding.Schema, []ProjectedColumn) {
			s, cols := projectedSchema(t, full, "MD1")
			cols[0].Rebuild = true
			return s, cols
		},
		"case fold": func() (*encoding.Schema, []ProjectedColumn) {
			return projectedSchema(t, full, "WT>w", "Satisfaction>W")
		},
	}
	for name, mk := range cases {
		s, cols := mk()
		if _, err := c.project(s, cols); err == nil {
			t.Errorf("%s: projected, want a refusal", name)
		}
	}
}

// TestRebuildVariable_CarriesNoOldLabels: a rebuilt variable — the hook
// a re-coded output column takes — keeps its names and index but none of
// the old variable's value labels, codes or missing specification: a
// numeric rebuild is plain numeric, a categorical one carries its new
// dictionary's entries unlabelled.
func TestRebuildVariable_CarriesNoOldLabels(t *testing.T) {
	t.Parallel()

	_, _, src := importFixture(t, richSpec())
	orig := variableNamed(src, "Satisfaction")
	if len(orig.Categories) == 0 {
		t.Fatalf("fixture Satisfaction carries no categories: %+v", orig)
	}

	num := *orig
	if err := RebuildVariable(&num, &encoding.Field{Name: "Satisfaction", Type: encoding.FieldTypeF64, Nullable: true, Description: "score"}, false); err != nil {
		t.Fatal(err)
	}
	if len(num.Categories) != 0 || num.Missing != nil || num.Kind != "numeric" || num.Label != "score" || num.ShortName != orig.ShortName {
		t.Errorf("numeric rebuild = %+v", num)
	}

	dict := encoding.NewDictionary()
	dict.Add("low")
	dict.Add("high")
	cat := *orig
	fld := &encoding.Field{Name: "Satisfaction", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict, Description: "band"}
	if err := RebuildVariable(&cat, fld, false); err != nil {
		t.Fatal(err)
	}
	if len(cat.Categories) != 2 || cat.Kind != "categorical" || cat.DeclaredWidth != 4 || cat.Missing != nil {
		t.Fatalf("categorical rebuild = %+v", cat)
	}
	for i, want := range []string{"low", "high"} {
		got := cat.Categories[i]
		if got.Labelled || got.Label != "" || got.Code != nil || got.Value != want || got.Text == nil || *got.Text != want {
			t.Errorf("category %d = %+v, want the unlabelled entry %q", i, got, want)
		}
	}

	// Through the projection: a rebuilt column's variable is regenerated.
	fs, cohort, _ := importFixture(t, richSpec())
	c, _ := PrepareSidecarCarry(fs, cohort)
	s, cols := projectedSchema(t, carrySchema(t, fs, cohort), "Satisfaction")
	s.Fields[0] = *fld
	cols[0].Rebuild = true
	doc, err := c.project(s, cols)
	if err != nil {
		t.Fatal(err)
	}
	for _, cat := range variableNamed(doc, "Satisfaction").Categories {
		if cat.Labelled || cat.Label != "" {
			t.Errorf("projected rebuild kept an old label: %+v", cat)
		}
	}
}
