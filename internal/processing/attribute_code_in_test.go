package processing

import (
	stderrors "errors"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// codeInSchema carries one column per type family ATTR_CODE_IN judges:
// a nullable categorical (codes are labels), every accepted unsigned
// integer width, and one representative of each refused type.
func codeInSchema(t *testing.T) *encoding.Schema {
	t.Helper()
	dict := encoding.NewDictionary()
	for _, l := range []string{"1", "2", "3", "4", "5", "DK"} {
		if _, err := dict.Add(l); err != nil {
			t.Fatalf("dict.Add: %v", err)
		}
	}
	setDict := encoding.NewDictionary()
	if _, err := setDict.Add("a"); err != nil {
		t.Fatalf("dict.Add: %v", err)
	}
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "cat", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict, Nullable: true},
		{Name: "cat16", Type: encoding.FieldTypeCategoricalU16, Dictionary: dict},
		{Name: "cat32", Type: encoding.FieldTypeCategoricalU32, Dictionary: dict},
		{Name: "n4", Type: encoding.FieldTypeU4},
		{Name: "n8", Type: encoding.FieldTypeU8, Nullable: true},
		{Name: "n16", Type: encoding.FieldTypeU16},
		{Name: "n32", Type: encoding.FieldTypeU32},
		{Name: "n64", Type: encoding.FieldTypeU64},
		{Name: "f32", Type: encoding.FieldTypeF32},
		{Name: "f64", Type: encoding.FieldTypeF64},
		{Name: "d", Type: encoding.FieldTypeDate},
		{Name: "dt", Type: encoding.FieldTypeDateTime},
		{Name: "b", Type: encoding.FieldTypePackedBool},
		{Name: "dec", Type: encoding.FieldTypeDecimal128, Precision: 18, Scale: 2},
		{Name: "s8", Type: encoding.FieldTypeSetU8, Dictionary: setDict},
		{Name: "s256", Type: encoding.FieldTypeSetU256, Dictionary: setDict},
	}}
}

func codeIn(field, params string) *types.Attribute {
	a := &types.Attribute{Type: types.ATTR_CODE_IN, Field: field, Label: "out"}
	if params != "" {
		a.Params = []byte(params)
	}
	return a
}

// codeInRow builds the attribute and evaluates it on one record.
func codeInRow(t *testing.T, schema *encoding.Schema, attr *types.Attribute, rec *Record) float64 {
	t.Helper()
	c, err := newCodeInAttribute(attr, schema)
	if err != nil {
		t.Fatalf("newCodeInAttribute: %v", err)
	}
	rl, ok := c.(RowLocalAttribute)
	if !ok {
		t.Fatalf("ATTR_CODE_IN must implement RowLocalAttribute")
	}
	v, err := rl.Row(rec, attr.Field)
	if err != nil {
		t.Fatalf("Row: %v", err)
	}
	return v
}

func catID(t *testing.T, schema *encoding.Schema, field, label string) float64 {
	t.Helper()
	id, ok := schema.Field(field).Dictionary.IDFor(label)
	if !ok {
		t.Fatalf("label %q not in %s dictionary", label, field)
	}
	return float64(id)
}

func TestAttrCodeIn_Refusals(t *testing.T) {
	schema := codeInSchema(t)
	cases := []struct {
		name string
		attr *types.Attribute
		want string // substring of the message
	}{
		{"missing field", codeIn("", `{"codes":["1"]}`), "requires field"},
		{"missing params", codeIn("cat", ""), "non-empty \"codes\""},
		{"missing codes key", codeIn("cat", `{}`), "non-empty \"codes\""},
		{"empty codes", codeIn("cat", `{"codes":[]}`), "non-empty \"codes\""},
		{"malformed params", codeIn("cat", `{"codes":"1"}`), "parsing ATTR_CODE_IN params"},
		{"bool code", codeIn("cat", `{"codes":[true]}`), "JSON string or a JSON integer"},
		{"null code", codeIn("cat", `{"codes":[null]}`), "JSON string or a JSON integer"},
		{"fractional JSON number", codeIn("n8", `{"codes":[6.5]}`), "JSON string or a JSON integer"},
		{"exponent JSON number", codeIn("n8", `{"codes":[1e2]}`), "JSON string or a JSON integer"},
		{"target set", func() *types.Attribute { a := codeIn("cat", `{"codes":["4"]}`); a.Target = "top2"; return a }(), "label"},
		{"predictors set", func() *types.Attribute {
			a := codeIn("cat", `{"codes":["4"]}`)
			a.Predictors = []string{"x"}
			return a
		}(), "label"},
		{"unknown field", codeIn("nope", `{"codes":["1"]}`), "unknown field"},
		{"u8 non-integer string", codeIn("n8", `{"codes":["DK"]}`), "can never match"},
		{"u8 fractional string", codeIn("n8", `{"codes":["6.5"]}`), "can never match"},
		{"u8 negative string", codeIn("n8", `{"codes":["-1"]}`), "can never match"},
		{"u8 negative JSON integer", codeIn("n8", `{"codes":[-1]}`), "can never match"},
		{"u8 beyond width", codeIn("n8", `{"codes":["300"]}`), "can never match"},
		{"u4 beyond width", codeIn("n4", `{"codes":[16]}`), "can never match"},
		{"u16 beyond width", codeIn("n16", `{"codes":[65536]}`), "can never match"},
		{"u32 beyond width", codeIn("n32", `{"codes":["4294967296"]}`), "can never match"},
		{"u64 beyond width", codeIn("n64", `{"codes":["18446744073709551616"]}`), "can never match"},
		{"one bad code among good", codeIn("n8", `{"codes":[4, 5, "x"]}`), "can never match"},
	}
	for _, f := range []string{"f32", "f64", "d", "dt", "b", "dec", "s8", "s256"} {
		cases = append(cases, struct {
			name string
			attr *types.Attribute
			want string
		}{"refused type " + f, codeIn(f, `{"codes":["1"]}`), "accepts only"})
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := newCodeInAttribute(c.attr, schema)
			if err == nil {
				t.Fatalf("want PROCESSING_CONFIG, got nil")
			}
			var ce *errors.CodedError
			if !stderrors.As(err, &ce) || ce.Code != errors.PROCESSING_CONFIG {
				t.Fatalf("want PROCESSING_CONFIG, got %v", err)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("message %q does not mention %q", err.Error(), c.want)
			}
		})
	}
}

// The width ceiling is inclusive: the largest value each type can hold
// is accepted.
func TestAttrCodeIn_WidthCeilingInclusive(t *testing.T) {
	schema := codeInSchema(t)
	for field, code := range map[string]string{
		"n4": "15", "n8": "255", "n16": "65535", "n32": "4294967295", "n64": "18446744073709551615",
	} {
		if _, err := newCodeInAttribute(codeIn(field, `{"codes":["`+code+`"]}`), schema); err != nil {
			t.Errorf("%s code %s: %v", field, code, err)
		}
	}
}

func TestAttrCodeIn_AcceptedTypes(t *testing.T) {
	schema := codeInSchema(t)
	for _, f := range []string{"cat", "cat16", "cat32", "n4", "n8", "n16", "n32", "n64"} {
		if _, err := newCodeInAttribute(codeIn(f, `{"codes":["1"]}`), schema); err != nil {
			t.Errorf("%s: %v", f, err)
		}
	}
}

func TestAttrCodeIn_CategoricalMatch(t *testing.T) {
	schema := codeInSchema(t)
	attr := codeIn("cat", `{"codes":["4","5"]}`)
	for label, want := range map[string]float64{"1": 0, "3": 0, "4": 1, "5": 1, "DK": 0} {
		rec := NewRecord(schema, map[string]float64{"cat": catID(t, schema, "cat", label)})
		if got := codeInRow(t, schema, attr, rec); got != want {
			t.Errorf("label %q: got %v, want %v", label, got, want)
		}
	}
	// The u16 / u32 categorical widths resolve the same way.
	for _, f := range []string{"cat16", "cat32"} {
		rec := NewRecord(schema, map[string]float64{f: catID(t, schema, f, "DK")})
		if got := codeInRow(t, schema, codeIn(f, `{"codes":["DK"]}`), rec); got != 1 {
			t.Errorf("%s DK: got %v, want 1", f, got)
		}
	}
}

// A code is a dictionary LABEL on a categorical field, never the ID: the
// label "4" sits at ID 3, so a row holding ID 4 (label "5") must not
// match code 4.
func TestAttrCodeIn_CategoricalMatchesLabelNotID(t *testing.T) {
	schema := codeInSchema(t)
	attr := codeIn("cat", `{"codes":[4]}`)
	five := NewRecord(schema, map[string]float64{"cat": catID(t, schema, "cat", "5")})
	four := NewRecord(schema, map[string]float64{"cat": catID(t, schema, "cat", "4")})
	if got := codeInRow(t, schema, attr, five); got != 0 {
		t.Errorf("label 5 matched code 4 (by ID): got %v", got)
	}
	if got := codeInRow(t, schema, attr, four); got != 1 {
		t.Errorf("label 4 with JSON-integer code 4: got %v, want 1", got)
	}
}

func TestAttrCodeIn_IntegerMatchByValue(t *testing.T) {
	schema := codeInSchema(t)
	attr := codeIn("n8", `{"codes":["4", 5]}`)
	for v, want := range map[float64]float64{0: 0, 3: 0, 4: 1, 5: 1, 6: 0, 255: 0} {
		rec := NewRecord(schema, map[string]float64{"n8": v})
		if got := codeInRow(t, schema, attr, rec); got != want {
			t.Errorf("value %v: got %v, want %v", v, got, want)
		}
	}
}

func TestAttrCodeIn_JSONIntegerCodes(t *testing.T) {
	schema := codeInSchema(t)
	rec := NewRecord(schema, map[string]float64{"n16": 300})
	if got := codeInRow(t, schema, codeIn("n16", `{"codes":[300]}`), rec); got != 1 {
		t.Errorf("JSON integer 300 on u16: got %v, want 1", got)
	}
}

func TestAttrCodeIn_DuplicatesDeduped(t *testing.T) {
	schema := codeInSchema(t)
	codes, err := parseCodeInCodes([]byte(`{"codes":["4", 4, "5", "4", 5]}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(codes, ",") != "4,5" {
		t.Errorf("codes = %q, want [4 5]", codes)
	}
	c, err := newCodeInAttribute(codeIn("cat", `{"codes":["4", 4, "4"]}`), schema)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(c.(*codeInAttribute).keys); n != 1 {
		t.Errorf("resolved %d keys, want 1", n)
	}
}

func TestAttrCodeIn_NullInputReadsZero(t *testing.T) {
	schema := codeInSchema(t)
	for field, attr := range map[string]*types.Attribute{
		"cat": codeIn("cat", `{"codes":["1"]}`),
		"n8":  codeIn("n8", `{"codes":[0]}`),
	} {
		// The null row's slot holds the value a code matches, so only
		// the null check keeps it at 0.
		rec := NewRecordWithNulls(schema, map[string]float64{field: 0}, map[string]bool{field: true})
		if got := codeInRow(t, schema, attr, rec); got != 0 {
			t.Errorf("%s null: got %v, want 0", field, got)
		}
	}
}

func TestAttrCodeIn_AbsentCategoricalCodeSilent(t *testing.T) {
	schema := codeInSchema(t)
	// "9" is not in the dictionary: dropped, no error, matches nothing.
	attr := codeIn("cat", `{"codes":["9", "NA"]}`)
	for _, label := range []string{"1", "2", "3", "4", "5", "DK"} {
		rec := NewRecord(schema, map[string]float64{"cat": catID(t, schema, "cat", label)})
		if got := codeInRow(t, schema, attr, rec); got != 0 {
			t.Errorf("label %q matched an absent code: got %v", label, got)
		}
	}
	// A mix keeps the present code working.
	mixed := codeIn("cat", `{"codes":["9", "2"]}`)
	rec := NewRecord(schema, map[string]float64{"cat": catID(t, schema, "cat", "2")})
	if got := codeInRow(t, schema, mixed, rec); got != 1 {
		t.Errorf("present code beside an absent one: got %v, want 1", got)
	}
}

// A categorical field without a dictionary has no labels: a numeric
// code must not be read as a dictionary ID.
func TestAttrCodeIn_CategoricalWithoutDictionaryMatchesNothing(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{{Name: "c", Type: encoding.FieldTypeCategoricalU8}}}
	rec := NewRecord(schema, map[string]float64{"c": 4})
	if got := codeInRow(t, schema, codeIn("c", `{"codes":[4]}`), rec); got != 0 {
		t.Errorf("code 4 matched dictionary ID 4 on a dictionary-less categorical: got %v", got)
	}
}

// The registry probe constructs with a nil schema: it must succeed and
// match nothing — not guess that a code is a number or an ID.
func TestAttrCodeIn_NilSchemaProbeNeverMatches(t *testing.T) {
	schema := codeInSchema(t)
	c, err := newCodeInAttribute(codeIn("n8", `{"codes":[0, 1, 4]}`), nil)
	if err != nil {
		t.Fatalf("nil-schema probe: %v", err)
	}
	for _, v := range []float64{0, 1, 4} {
		got, err := c.(RowLocalAttribute).Row(NewRecord(schema, map[string]float64{"n8": v}), "n8")
		if err != nil || got != 0 {
			t.Errorf("probe matched %v: got (%v, %v)", v, got, err)
		}
	}
}

func TestAttrCodeIn_ComputeMatchesRow(t *testing.T) {
	schema := codeInSchema(t)
	c, err := newCodeInAttribute(codeIn("n8", `{"codes":[2]}`), schema)
	if err != nil {
		t.Fatal(err)
	}
	recs := []*Record{
		NewRecord(schema, map[string]float64{"n8": 2}),
		NewRecord(schema, map[string]float64{"n8": 3}),
		NewRecordWithNulls(schema, map[string]float64{"n8": 2}, map[string]bool{"n8": true}),
	}
	got, err := c.Compute(recs, "n8")
	if err != nil {
		t.Fatal(err)
	}
	if want := []float64{1, 0, 0}; len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Errorf("Compute = %v, want %v", got, want)
	}
}

func TestAttrCodeIn_RegisteredRowLocalNotTwoPass(t *testing.T) {
	factory, ok := attributeRegistry[types.ATTR_CODE_IN]
	if !ok {
		t.Fatal("ATTR_CODE_IN is not registered")
	}
	c, err := factory(codeIn("cat", `{"codes":["1"]}`), codeInSchema(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := c.(RowLocalAttribute); !ok {
		t.Error("ATTR_CODE_IN must be RowLocalAttribute")
	}
	if _, ok := c.(TwoPassAttribute); ok {
		t.Error("ATTR_CODE_IN must not be TwoPassAttribute")
	}
}

// codeInXtabSchema / codeInXtabRecords: a nullable categorical code
// column, a nullable u8 code column and two axis columns.
func codeInXtabSchema(t *testing.T) *encoding.Schema {
	t.Helper()
	mk := func(labels ...string) *encoding.Dictionary {
		d := encoding.NewDictionary()
		for _, l := range labels {
			if _, err := d.Add(l); err != nil {
				t.Fatalf("dict.Add: %v", err)
			}
		}
		return d
	}
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "fam", Type: encoding.FieldTypeCategoricalU8, Dictionary: mk("1", "2", "3", "4", "5", "DK"), Nullable: true},
		{Name: "sat", Type: encoding.FieldTypeU8, Nullable: true},
		{Name: "region", Type: encoding.FieldTypeCategoricalU8, Dictionary: mk("north", "south")},
		{Name: "wave", Type: encoding.FieldTypeCategoricalU8, Dictionary: mk("w1", "w2", "w3")},
	}}
}

func codeInXtabRecords(schema *encoding.Schema) []*Record {
	const n = 60
	out := make([]*Record, n)
	for i := range n {
		nulls := map[string]bool{}
		if i%7 == 0 {
			nulls["fam"] = true
		}
		if i%11 == 0 {
			nulls["sat"] = true
		}
		out[i] = NewRecordWithNulls(schema, map[string]float64{
			"fam":    float64((i * 5) % 6),
			"sat":    float64(1 + (i*3)%10),
			"region": float64(i % 2),
			"wave":   float64((i / 2) % 3),
		}, nulls)
	}
	return out
}

// TestAttrCodeIn_FusedCrosstabMatchesBuffered: ATTR_CODE_IN fuses with
// no gate change, and the fused crosstab answers exactly as the
// buffered one — on null-bearing inputs, for a categorical and an
// integer code field.
func TestAttrCodeIn_FusedCrosstabMatchesBuffered(t *testing.T) {
	schema := codeInXtabSchema(t)
	for name, attr := range map[string]*types.Attribute{
		"categorical": {Type: types.ATTR_CODE_IN, Field: "fam", Label: "top2", Params: []byte(`{"codes":["4","5","9"]}`)},
		"u8":          {Type: types.ATTR_CODE_IN, Field: "sat", Label: "top2", Params: []byte(`{"codes":[9, 10]}`)},
	} {
		t.Run(name, func(t *testing.T) {
			req := &types.Request{
				Attributes: []*types.Attribute{attr},
				Crosstab: &types.CrosstabSpec{
					Rows:    []*types.Group{catGroup("region")},
					Columns: []*types.Group{catGroup("wave")},
					Cell:    &types.Aggregation{Type: types.AGG_AVERAGE, Field: "top2", Label: "share"},
					Shape:   types.CrosstabShapeMatrix,
					Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
				},
			}
			assertFusableCrosstab(t, schema, req)
			buf, err := runBufferedCrosstabWithComponents(t, schema, req, codeInXtabRecords(schema), false)
			if err != nil {
				t.Fatalf("buffered: %v", err)
			}
			fused, err := runFusedCrosstabViaRunner(t, schema, req, codeInXtabRecords(schema), false)
			if err != nil {
				t.Fatalf("fused: %v", err)
			}
			bj, fj := jsonOf(t, buf.Crosstab), jsonOf(t, fused.Crosstab)
			if bj != fj {
				t.Errorf("Crosstab diverges:\nbuffered: %s\nfused:    %s", bj, fj)
			}
			if b, f := jsonOf(t, buf.Components), jsonOf(t, fused.Components); b != f {
				t.Errorf("Components diverge:\nbuffered: %s\nfused:    %s", b, f)
			}
			// Non-degenerate: some cell share strictly between 0 and 1.
			if !strings.Contains(bj, "0.") {
				t.Errorf("degenerate fixture, no fractional share: %s", bj)
			}
		})
	}
}
