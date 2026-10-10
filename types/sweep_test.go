package types

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

// preSweepComposedRequest mirrors ComposedRequest as it stood before the
// `sweep` slot: same fields, same tags. A sweep-free request must
// marshal and hash byte-identically to it.
type preSweepComposedRequest struct {
	Requests     []*Request           `json:"requests"`
	Overlays     []ComposeOverlaySpec `json:"overlays,omitempty"`
	Multiplicity *Multiplicity        `json:"multiplicity,omitempty"`
	Return       *Return              `json:"return,omitempty"`
}

// TestComposedRequest_SweepFreeByteIdentity: a ComposedRequest without a
// sweep marshals and hashes exactly as the pre-sweep shape.
func TestComposedRequest_SweepFreeByteIdentity(t *testing.T) {
	reqs := []*Request{{
		Label:        "a",
		Cohort:       &Cohort{Filename: "a.pulse"},
		Aggregations: []*Aggregation{{Type: AGG_SUM, Field: "x"}},
	}}
	ov := []ComposeOverlaySpec{{Kind: OverlayKindRank}}
	cur := &ComposedRequest{Requests: reqs, Overlays: ov}
	legacy := &preSweepComposedRequest{Requests: reqs, Overlays: ov}

	got, err := json.Marshal(cur)
	if err != nil {
		t.Fatal(err)
	}
	want, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("wire diverged:\n got %s\nwant %s", got, want)
	}
	if h, w := cur.Hash(), CanonicalHash("composed", legacy); h != w {
		t.Fatalf("Hash diverged: got %s want %s", h, w)
	}

	// A set sweep moves both.
	swept := *cur
	swept.Sweep = &SweepSpec{Axes: []SweepAxis{{Name: "k", Values: []any{1}}}, Request: json.RawMessage(`{}`)}
	if swept.Hash() == cur.Hash() {
		t.Fatal("a sweep does not reach the hash")
	}
	b, _ := json.Marshal(&swept)
	if !strings.Contains(string(b), `"sweep":{`) {
		t.Fatalf("sweep absent from the wire: %s", b)
	}
}

// TestComposedResponse_RankingFreeByteIdentity: an unranked response
// writes no `ranking` key; a ranked one writes it, a NaN value as null.
func TestComposedResponse_RankingFreeByteIdentity(t *testing.T) {
	b, err := json.Marshal(&ComposedResponse{Responses: []*Response{}})
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"responses":[]}` {
		t.Fatalf("got %s", b)
	}
	b, err = json.Marshal(&ComposedResponse{
		Responses: []*Response{},
		Ranking:   []RankEntry{{Label: "a", Value: 1.5, Rank: 1}, {Label: "b", Value: math.NaN(), Rank: 2}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"responses":[],"ranking":[{"label":"a","value":1.5,"rank":1},{"label":"b","value":null,"rank":2}]}`
	if string(b) != want {
		t.Fatalf("got  %s\nwant %s", b, want)
	}
}

// TestSweepSpec_WireShape pins the JSON keys and the as-written number
// text of axis values through a decode / encode round trip.
func TestSweepSpec_WireShape(t *testing.T) {
	in := `{"axes":[{"name":"tv","values":[0.10,70,12345678901234567890,"x",true]}],` +
		`"mode":"zip","label":"tv{{tv}}","request":{"cohort":{"filename":"{{tv}}"}},` +
		`"overlays":[{"kind":"OVERLAY_RANK"}],"rank":{"by":"data[0].v","order":"desc","top":3}}`
	var s SweepSpec
	if err := json.Unmarshal([]byte(in), &s); err != nil {
		t.Fatal(err)
	}
	if n, ok := s.Axes[0].Values[0].(json.Number); !ok || n.String() != "0.10" {
		t.Fatalf("number text not kept: %#v", s.Axes[0].Values[0])
	}
	if n, ok := s.Axes[0].Values[2].(json.Number); !ok || n.String() != "12345678901234567890" {
		t.Fatalf("big integer not kept: %#v", s.Axes[0].Values[2])
	}
	if s.Mode != SweepModeZip || s.Rank == nil || s.Rank.Order != SweepRankDesc || s.Rank.Top == nil || *s.Rank.Top != 3 {
		t.Fatalf("decoded %+v", s)
	}
	out, err := json.Marshal(&s)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != in {
		t.Fatalf("round trip diverged:\n got %s\nwant %s", out, in)
	}
}

func TestSweepVocabularies(t *testing.T) {
	if got := AllSweepModes(); len(got) != 2 || got[0] != "grid" || got[1] != "zip" {
		t.Errorf("modes %v", got)
	}
	if got := AllSweepRankOrders(); len(got) != 2 || got[0] != "asc" || got[1] != "desc" {
		t.Errorf("orders %v", got)
	}
}
