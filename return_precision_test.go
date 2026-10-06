package pulse_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// sigDigits counts the significant digits of a JSON number literal.
func sigDigits(lit string) int {
	mant, _, _ := strings.Cut(strings.ToLower(lit), "e")
	mant = strings.TrimLeft(strings.ReplaceAll(strings.TrimPrefix(mant, "-"), ".", ""), "0")
	return len(mant)
}

// TestReturn_PrecisionProcess: a real Process under return.precision
// rounds measure columns on the wire, writes count columns (AGG_COUNT —
// a count carried as a float64) and a decimal128 sum exact, echoes the
// precision on the marker (alone and with a preset), and leaves the Go
// values identical to the unshaped run.
func TestReturn_PrecisionProcess(t *testing.T) {
	p, _, cohort := acceptanceCohort(t)
	mk := func(ret *types.Return) *types.Request {
		return &types.Request{
			Cohort: &types.Cohort{Filename: cohort},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_AVERAGE, Field: "x", Label: "m"},
				{Type: types.AGG_COUNT, Field: "x", Label: "n"},
				{Type: types.AGG_SUM, Field: "t_decimal128", Label: "d"},
			},
			Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}},
			Return: ret,
		}
	}
	base, baseJSON := processJSON(t, p, mk(nil))
	for name, ret := range map[string]*types.Return{
		"precision only":  {Precision: 1},
		"standard + prec": {Preset: types.ReturnPresetStandard, Precision: 1},
		"minimal + prec":  {Preset: types.ReturnPresetMinimal, Precision: 1},
		"include + prec":  {Include: []string{"data"}, Precision: 1},
	} {
		t.Run(name, func(t *testing.T) {
			resp, b := processJSON(t, p, mk(ret))
			if resp.Returned == nil || resp.Returned.Precision != 1 {
				t.Fatalf("returned = %+v, want precision 1", resp.Returned)
			}
			if !reflect.DeepEqual(resp.Data, base.Data) {
				t.Errorf("Go data changed under precision:\n got %v\nwant %v", resp.Data, base.Data)
			}
			var doc, baseDoc struct {
				Data []map[string]json.RawMessage `json:"data"`
			}
			if err := json.Unmarshal(b, &doc); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(baseJSON, &baseDoc); err != nil {
				t.Fatal(err)
			}
			if len(doc.Data) == 0 || len(doc.Data) != len(baseDoc.Data) {
				t.Fatalf("data rows = %d, want %d", len(doc.Data), len(baseDoc.Data))
			}
			wider := false
			for i, row := range doc.Data {
				if sigDigits(string(baseDoc.Data[i]["m"])) > 1 {
					wider = true
				}
				if got := sigDigits(string(row["m"])); got > 1 {
					t.Errorf("row %d m = %s: %d significant digits, want <= 1", i, row["m"], got)
				}
				if string(row["n"]) != string(baseDoc.Data[i]["n"]) {
					t.Errorf("row %d count n = %s, want exact %s", i, row["n"], baseDoc.Data[i]["n"])
				}
				if string(row["d"]) != string(baseDoc.Data[i]["d"]) || !strings.HasPrefix(string(row["d"]), `"`) {
					t.Errorf("row %d decimal d = %s, want exact %s", i, row["d"], baseDoc.Data[i]["d"])
				}
			}
			if !wider {
				t.Fatalf("baseline means already have <= 1 digit: the gate is vacuous")
			}
		})
	}
}
