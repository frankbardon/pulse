package feature

import (
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

func makeRecordsForSplit(n int) []Record {
	out := make([]Record, n)
	for i := range out {
		out[i] = newFakeRecord()
	}
	return out
}

func TestTrainTestSplit_RoughRatios(t *testing.T) {
	records := makeRecordsForSplit(1000)
	c, err := newTrainTestSplit(&types.Feature{
		Params: json.RawMessage(`{"ratios":[0.7,0.15,0.15],"seed":42}`),
	}, nil)
	if err != nil {
		t.Fatalf("newTrainTestSplit: %v", err)
	}
	out, err := c.Compute(records, "")
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	values := out["split"].Values

	counts := map[float64]int{}
	for _, v := range values {
		counts[v]++
	}
	// Allow a few rows of slack from rounding.
	if abs(counts[SplitTrain]-700) > 5 {
		t.Errorf("train count=%d, want ~700", counts[SplitTrain])
	}
	if abs(counts[SplitVal]-150) > 5 {
		t.Errorf("val count=%d, want ~150", counts[SplitVal])
	}
	if abs(counts[SplitTest]-150) > 5 {
		t.Errorf("test count=%d, want ~150", counts[SplitTest])
	}
}

func TestTrainTestSplit_Deterministic(t *testing.T) {
	records1 := makeRecordsForSplit(100)
	records2 := makeRecordsForSplit(100)

	c1, _ := newTrainTestSplit(&types.Feature{
		Params: json.RawMessage(`{"ratios":[0.8,0.2],"seed":7}`),
	}, nil)
	c2, _ := newTrainTestSplit(&types.Feature{
		Params: json.RawMessage(`{"ratios":[0.8,0.2],"seed":7}`),
	}, nil)

	out1, _ := c1.Compute(records1, "")
	out2, _ := c2.Compute(records2, "")

	a := out1["split"].Values
	b := out2["split"].Values
	for i := range a {
		if a[i] != b[i] {
			t.Errorf("seeded split diverged at index %d: %f vs %f", i, a[i], b[i])
			break
		}
	}
}

func TestTrainTestSplit_TwoElementRatios(t *testing.T) {
	records := makeRecordsForSplit(100)
	c, err := newTrainTestSplit(&types.Feature{
		Params: json.RawMessage(`{"ratios":[0.5,0.5],"seed":1}`),
	}, nil)
	if err != nil {
		t.Fatalf("newTrainTestSplit: %v", err)
	}
	out, _ := c.Compute(records, "")
	for _, v := range out["split"].Values {
		if v == SplitTest {
			t.Errorf("two-element ratios should never produce test rows")
			break
		}
	}
}

func TestTrainTestSplit_Stratified(t *testing.T) {
	// 100 north + 100 south. With stratify, each class should get a
	// proportional split.
	mk := func(label string) Record {
		return newStringRecord("class", label)
	}
	records := make([]Record, 0, 200)
	for i := 0; i < 100; i++ {
		records = append(records, mk("north"))
	}
	for i := 0; i < 100; i++ {
		records = append(records, mk("south"))
	}

	dict := encoding.NewDictionary()
	_, _ = dict.Add("north")
	_, _ = dict.Add("south")
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "class", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict},
		},
	}

	c, err := newTrainTestSplit(&types.Feature{
		Params: json.RawMessage(`{"ratios":[0.8,0.2],"seed":3,"stratify":"class"}`),
	}, schema)
	if err != nil {
		t.Fatalf("newTrainTestSplit: %v", err)
	}
	out, _ := c.Compute(records, "")
	values := out["split"].Values

	northTrain, northVal := 0, 0
	southTrain, southVal := 0, 0
	for i, v := range values {
		if i < 100 {
			if v == SplitTrain {
				northTrain++
			} else if v == SplitVal {
				northVal++
			}
		} else {
			if v == SplitTrain {
				southTrain++
			} else if v == SplitVal {
				southVal++
			}
		}
	}
	if abs(northTrain-80) > 2 || abs(southTrain-80) > 2 {
		t.Errorf("stratified train counts uneven: north=%d south=%d", northTrain, southTrain)
	}
	if abs(northVal-20) > 2 || abs(southVal-20) > 2 {
		t.Errorf("stratified val counts uneven: north=%d south=%d", northVal, southVal)
	}
}

func TestTrainTestSplit_RejectsBadRatios(t *testing.T) {
	cases := []string{
		`{"ratios":[0.5,0.4]}`,
		`{"ratios":[0.5]}`,
		`{"ratios":[0.5,0.5,0.5]}`,
		`{"ratios":[0.5,-0.3,0.8]}`,
		`{"ratios":[0.5,0.5,0.5,0.5]}`,
	}
	for _, body := range cases {
		_, err := newTrainTestSplit(&types.Feature{
			Params: json.RawMessage(body),
		}, nil)
		if err == nil {
			t.Errorf("expected error for %s", body)
		}
	}
}

func TestTrainTestSplit_RejectsNumericStratify(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "score", Type: encoding.FieldTypeF64},
		},
	}
	_, err := newTrainTestSplit(&types.Feature{
		Params: json.RawMessage(`{"ratios":[0.8,0.2],"stratify":"score"}`),
	}, schema)
	if err == nil {
		t.Error("expected error for numeric stratify field")
	}
}

func TestTrainTestSplit_RequiresParams(t *testing.T) {
	_, err := newTrainTestSplit(&types.Feature{}, nil)
	if err == nil {
		t.Error("expected error for missing params")
	}
}

func TestTrainTestSplit_Registered(t *testing.T) {
	if _, ok := Lookup(types.FEAT_TRAIN_TEST_SPLIT); !ok {
		t.Error("expected FEAT_TRAIN_TEST_SPLIT to be registered")
	}
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
