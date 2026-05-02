package feature

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

func TestFrequencyEncode_Compute(t *testing.T) {
	records := []Record{
		newStringRecord("region", "north"),
		newStringRecord("region", "north"),
		newStringRecord("region", "north"),
		newStringRecord("region", "south"),
	}

	c, err := newFrequencyEncode(&types.Feature{Field: "region"}, nil)
	if err != nil {
		t.Fatalf("newFrequencyEncode: %v", err)
	}
	out, err := c.Compute(records, "region")
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	got := out["FREQ_region"]
	wantNorth := 0.75
	wantSouth := 0.25
	for i := 0; i < 3; i++ {
		if math.Abs(got.Values[i]-wantNorth) > 1e-9 {
			t.Errorf("record %d (north) freq=%f, want %f", i, got.Values[i], wantNorth)
		}
	}
	if math.Abs(got.Values[3]-wantSouth) > 1e-9 {
		t.Errorf("record 3 (south) freq=%f, want %f", got.Values[3], wantSouth)
	}
}

func TestFrequencyEncode_NullInput(t *testing.T) {
	r := newFakeRecord()
	r.nulls["region"] = true
	records := []Record{r, newStringRecord("region", "x")}

	c, _ := newFrequencyEncode(&types.Feature{Field: "region"}, nil)
	out, _ := c.Compute(records, "region")

	if !out["FREQ_region"].Nulls[0] {
		t.Error("expected null at index 0 (null input)")
	}
	if out["FREQ_region"].Nulls[1] {
		t.Error("did not expect null at index 1")
	}
}

func TestFrequencyEncode_RequiresField(t *testing.T) {
	_, err := newFrequencyEncode(&types.Feature{}, nil)
	if err == nil {
		t.Error("expected error for missing field")
	}
}

func TestTargetEncode_Compute_NoSmoothing(t *testing.T) {
	// Two categories: north has target values [10, 20] (mean=15);
	// south has target values [4, 6] (mean=5).
	mk := func(region string, target float64) Record {
		r := newStringRecord("region", region)
		r.num["price"] = target
		return r
	}
	records := []Record{
		mk("north", 10),
		mk("north", 20),
		mk("south", 4),
		mk("south", 6),
	}

	c, err := newTargetEncode(&types.Feature{
		Field:  "region",
		Params: json.RawMessage(`{"target":"price"}`),
	}, nil)
	if err != nil {
		t.Fatalf("newTargetEncode: %v", err)
	}
	out, err := c.Compute(records, "region")
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	got := out["TARGET_region"]
	wantNorth := 15.0
	wantSouth := 5.0
	for i := 0; i < 2; i++ {
		if math.Abs(got.Values[i]-wantNorth) > 1e-9 {
			t.Errorf("north[%d]=%f, want %f", i, got.Values[i], wantNorth)
		}
	}
	for i := 2; i < 4; i++ {
		if math.Abs(got.Values[i]-wantSouth) > 1e-9 {
			t.Errorf("south[%d]=%f, want %f", i, got.Values[i], wantSouth)
		}
	}
}

func TestTargetEncode_Smoothing(t *testing.T) {
	mk := func(region string, target float64) Record {
		r := newStringRecord("region", region)
		r.num["price"] = target
		return r
	}
	records := []Record{
		mk("north", 10),
		mk("north", 10),
		mk("north", 10),
		mk("rare", 100),
	}

	c, _ := newTargetEncode(&types.Feature{
		Field:  "region",
		Params: json.RawMessage(`{"target":"price","smoothing":2.0}`),
	}, nil)
	out, _ := c.Compute(records, "region")
	got := out["TARGET_region"]

	// global mean = (10+10+10+100)/4 = 32.5
	// rare with smoothing=2: (1*100 + 2*32.5) / (1+2) = 165/3 = 55
	wantRare := 55.0
	if math.Abs(got.Values[3]-wantRare) > 1e-9 {
		t.Errorf("smoothed rare=%f, want %f", got.Values[3], wantRare)
	}
}

func TestTargetEncode_RequiresParams(t *testing.T) {
	_, err := newTargetEncode(&types.Feature{Field: "region"}, nil)
	if err == nil {
		t.Error("expected error when params missing")
	}
}

func TestTargetEncode_RequiresTargetInParams(t *testing.T) {
	_, err := newTargetEncode(&types.Feature{
		Field:  "region",
		Params: json.RawMessage(`{}`),
	}, nil)
	if err == nil {
		t.Error("expected error when target missing")
	}
}

func TestTargetEncode_RejectsNegativeSmoothing(t *testing.T) {
	_, err := newTargetEncode(&types.Feature{
		Field:  "region",
		Params: json.RawMessage(`{"target":"price","smoothing":-1}`),
	}, nil)
	if err == nil {
		t.Error("expected error for negative smoothing")
	}
}

func TestTargetEncode_RejectsCategoricalTarget(t *testing.T) {
	dict := encoding.NewDictionary()
	_, _ = dict.Add("a")
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "region", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict},
			{Name: "label", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict},
		},
	}
	_, err := newTargetEncode(&types.Feature{
		Field:  "region",
		Params: json.RawMessage(`{"target":"label"}`),
	}, schema)
	if err == nil {
		t.Error("expected error when target is categorical")
	}
}

func TestGlobalPassFeatures_Registered(t *testing.T) {
	for _, ft := range []types.FeatureType{types.FEAT_FREQUENCY_ENCODE, types.FEAT_TARGET_ENCODE} {
		if _, ok := Lookup(ft); !ok {
			t.Errorf("expected %s to be registered", ft)
		}
	}
}
