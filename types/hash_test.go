package types

import (
	"encoding/json"
	"math"
	"testing"
)

func TestCanonicalHash_Deterministic(t *testing.T) {
	r := &Request{
		Cohort: &Cohort{Filename: "a.pulse"},
		Aggregations: []*Aggregation{
			{Type: AGG_SUM, Field: "x"},
		},
	}
	h1 := r.Hash()
	h2 := r.Hash()
	if h1 != h2 || len(h1) != 32 {
		t.Fatalf("hash not deterministic / wrong width: %q vs %q (len=%d)", h1, h2, len(h1))
	}
}

func TestCanonicalHash_RoundTripJSON(t *testing.T) {
	r := &Request{
		Cohort: &Cohort{Filename: "x.pulse"},
		Aggregations: []*Aggregation{
			{Type: AGG_AVERAGE, Field: "y", Label: "avg_y"},
			{Type: AGG_COUNT, Field: "z"},
		},
		Filterers: []*Filterer{
			{Type: FILTER_INCLUDE, Field: "g", Values: []string{"a", "b"}},
		},
	}
	want := r.Hash()
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var rt Request
	if err := json.Unmarshal(raw, &rt); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	got := rt.Hash()
	if got != want {
		t.Fatalf("round-trip hash diverged: %q vs %q", want, got)
	}
}

func TestCanonicalHash_DefaultNormalization(t *testing.T) {
	a := &Aggregation{Type: AGG_SUM, Field: "x"}
	b := &Aggregation{Type: AGG_SUM, Field: "x", Label: ""}
	if CanonicalHash("agg", a) != CanonicalHash("agg", b) {
		t.Fatalf("explicit default vs omitted should hash identically")
	}
}

func TestCanonicalHash_FieldOrderInvariant(t *testing.T) {
	// Two semantically identical aggregations differing only in
	// struct-field ordering at construction time.
	first := &Aggregation{Type: AGG_SUM, Field: "x", Label: "s"}
	second := &Aggregation{Label: "s", Field: "x", Type: AGG_SUM}
	if CanonicalHash("agg", first) != CanonicalHash("agg", second) {
		t.Fatalf("field-order invariance broken")
	}
}

func TestCanonicalHash_TypeNamespaceSeparates(t *testing.T) {
	r := &Request{Cohort: &Cohort{Filename: "a.pulse"}}
	c := &ComposedRequest{Requests: []*Request{r}}
	if r.Hash() == c.Hash() {
		t.Fatalf("Request and ComposedRequest hashes must namespace-separate")
	}
}

func TestCanonicalHash_NilSafe(t *testing.T) {
	var r *Request
	if got := r.Hash(); len(got) != 32 {
		t.Fatalf("nil Request.Hash() must still return 32-char hex, got %q", got)
	}
	var c *ComposedRequest
	if got := c.Hash(); len(got) != 32 {
		t.Fatalf("nil ComposedRequest.Hash() must still return 32-char hex, got %q", got)
	}
}

func TestCanonicalHash_NumberNormalization(t *testing.T) {
	type holder struct {
		V float64 `json:"v"`
	}
	zero := holder{V: 0}
	negZero := holder{V: math.Copysign(0, -1)}
	if CanonicalHash("t", zero) != CanonicalHash("t", negZero) {
		t.Fatalf("-0.0 must hash identically to 0.0")
	}
}

func TestCanonicalHash_SynthSpec_StableShape(t *testing.T) {
	// Two structurally identical FacetRequests must share a hash.
	a := &FacetRequest{Fields: []string{"a", "b"}}
	b := &FacetRequest{Fields: []string{"a", "b"}}
	if a.Hash() != b.Hash() {
		t.Fatalf("FacetRequest hash unstable")
	}
}
