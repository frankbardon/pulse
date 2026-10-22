package types

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/internal/returnplan"
)

func recordsMatrix() *MatrixValues {
	return &MatrixValues{Kind: MatrixKindSquareSymmetric, Encoding: MatrixEncodingRecords,
		RowKeys: []string{"a", "b"}, ColumnKeys: []string{"a", "b"},
		Values: [][]float64{{1, 0.123456789}, {0.123456789, math.NaN()}}}
}

// TestMatrixRecords_Wire: the records wire is {kind, encoding, columns,
// rows, labels?}, rows in RowKeys order with the key, every column (an
// undefined cell null) and the row's RecordFields; it reads back to the
// same Go value. Records reads an upper layout through its mirror.
func TestMatrixRecords_Wire(t *testing.T) {
	m := recordsMatrix()
	m.RecordFields = []map[string]any{{"free": true}, nil}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"kind":"square_symmetric","encoding":"records","columns":["a","b"],"rows":[{"a":1,"b":0.123456789,"free":true,"key":"a"},{"a":0.123456789,"b":null,"key":"b"}]}`
	if string(b) != want {
		t.Fatalf("wire\n%s\nwant\n%s", b, want)
	}
	var back MatrixValues
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(back.RowKeys, m.RowKeys) || !reflect.DeepEqual(back.ColumnKeys, m.ColumnKeys) ||
		back.Values[0][1] != m.Values[0][1] || !math.IsNaN(back.Values[1][1]) || back.RecordFields[0]["free"] != true || back.RecordFields[1] != nil {
		t.Errorf("read back %+v", back)
	}
	up := MatrixValues{Kind: MatrixKindSquareSymmetric, Encoding: MatrixEncodingUpper,
		RowKeys: []string{"a", "b"}, ColumnKeys: []string{"a", "b"}, Values: [][]float64{{1, 0.5}, {2}}}
	if r := up.Records(); r[1]["a"] != 0.5 || r[1]["b"] != 2.0 {
		t.Errorf("upper records %v", r)
	}
}

// TestMatrixRecords_ReturnPlan: under a Return plan the records rows are
// what the plan shapes — precision rounds their cells, an include
// reaches a column inside them — and an identity-precision plan writes
// the unshaped bytes.
func TestMatrixRecords_ReturnPlan(t *testing.T) {
	resp := func() *Response {
		return &Response{Matrices: []MatrixResult{{Name: "m", Type: "MAT_CORRELATION", Primary: recordsMatrix()}}}
	}
	r := resp()
	r.plan = precisionPlan(4)
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if s := string(b); !strings.Contains(s, `"rows":[{"a":1,"b":0.1235,"key":"a"},{"a":0.1235,"b":null,"key":"b"}]`) || strings.Contains(s, `"values"`) {
		t.Errorf("precision wire %s", s)
	}
	r = resp()
	r.plan = precisionPlan(0)
	shaped, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := json.Marshal(resp())
	if err != nil {
		t.Fatal(err)
	}
	if string(shaped) != string(plain) {
		t.Errorf("identity plan\n%s\nunshaped\n%s", shaped, plain)
	}
	var inc []returnplan.Path
	for _, s := range []string{"matrices[*].primary.rows[*].key", "matrices[*].primary.rows[*].b"} {
		p, err := returnplan.Parse(s)
		if err != nil {
			t.Fatal(err)
		}
		inc = append(inc, p)
	}
	r = resp()
	if _, ok := returnplan.Apply(r, returnplan.New("", inc, nil, nil, 0, []string{"matrices"})); !ok {
		t.Fatal("plan not applied")
	}
	b, err = json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if s := string(b); !strings.Contains(s, `"rows":[{"b":0.123456789,"key":"a"},{"b":null,"key":"b"}]`) {
		t.Errorf("include wire %s", s)
	}
}
