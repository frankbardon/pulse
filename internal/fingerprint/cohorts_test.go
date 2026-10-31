package fingerprint_test

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"math"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
)

// generatedCohorts maps a cohort filename (as named in a request) to a
// builder of its bytes, for a shape no in-repo fixture has. Each is
// derived from a checked-in fixture's CSV, so its records are fixed.
var generatedCohorts = map[string]func(*testing.T) []byte{
	"mtcars_am.pulse": mtcarsAMCohort,
}

// mtcarsAMCohort is mtcars' mpg (f64), am as a categorical_u8 split
// ("0" automatic, "1" manual) and w_prob (f64): the two-sample (Welch)
// TEST_T shape, which needs a categorical split_by mtcars lacks.
func mtcarsAMCohort(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile("../processing/testdata/reference/fixtures/mtcars.csv")
	if err != nil {
		t.Fatal(err)
	}
	sc := bufio.NewScanner(bytes.NewReader(raw))
	var header []string
	var rows [][]string
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		cells := strings.Split(line, ",")
		if header == nil {
			header = cells
			continue
		}
		rows = append(rows, cells)
	}
	col := func(name string) int {
		for i, h := range header {
			if h == name {
				return i
			}
		}
		t.Fatalf("mtcars.csv has no column %q", name)
		return -1
	}
	mpg, am, wp := col("mpg"), col("am"), col("w_prob")

	dict := encoding.NewDictionary()
	for _, v := range []string{"0", "1"} {
		if _, err := dict.Add(v); err != nil {
			t.Fatal(err)
		}
	}
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "mpg", Type: encoding.FieldTypeF64, ByteOffset: 0},
		{Name: "am", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 8, Dictionary: dict},
		{Name: "w_prob", Type: encoding.FieldTypeF64, ByteOffset: 9},
	}}
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatal(err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatal(err)
	}
	num := func(s string) float64 {
		v, err := strconv.ParseFloat(s, 64)
		if err != nil {
			t.Fatalf("mtcars.csv: %v", err)
		}
		return v
	}
	row := make([]byte, 17)
	for _, r := range rows {
		binary.LittleEndian.PutUint64(row[0:], math.Float64bits(num(r[mpg])))
		switch r[am] {
		case "0":
			row[8] = 0
		case "1":
			row[8] = 1
		default:
			t.Fatalf("mtcars.csv: am %q", r[am])
		}
		binary.LittleEndian.PutUint64(row[9:], math.Float64bits(num(r[wp])))
		buf.Write(row)
	}
	return buf.Bytes()
}
