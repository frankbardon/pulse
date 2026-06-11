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

// TestCanonicalHash_OverlayFreeByteIdentity locks the additive contract:
// adding the Overlays slot (E1-S1) to Request must NOT change the hash
// for any existing overlay-free request — the slot is `omitempty` so
// `json.Marshal` omits the `overlays` key entirely when the slice is
// nil or empty, and the canonical-hash pipeline therefore produces
// byte-identical output to the pre-Overlays implementation.
//
// The captured constant below is the hash for this exact Request
// computed against the canonical-hash routine as it existed before
// E1-S8. Any change that alters this hash means the additive
// extension broke byte-identity for callers that have already pinned
// dedup keys against earlier Pulse versions — bump CanonicalHash with
// migration plan, do not silently re-hash.
func TestCanonicalHash_OverlayFreeByteIdentity(t *testing.T) {
	const captured = "a4e259f7ed18dcbcb3e78b76066bcbee"
	r := &Request{
		Cohort: &Cohort{Filename: "a.pulse"},
		Aggregations: []*Aggregation{
			{Type: AGG_SUM, Field: "x"},
		},
	}
	if got := r.Hash(); got != captured {
		t.Fatalf("overlay-free Request.Hash() drifted from captured baseline: got %q want %q", got, captured)
	}
	// Nil and empty Overlays must both hash identically to the
	// implicit (omitted slot) form.
	rNil := &Request{
		Cohort:       &Cohort{Filename: "a.pulse"},
		Aggregations: []*Aggregation{{Type: AGG_SUM, Field: "x"}},
		Overlays:     nil,
	}
	rEmpty := &Request{
		Cohort:       &Cohort{Filename: "a.pulse"},
		Aggregations: []*Aggregation{{Type: AGG_SUM, Field: "x"}},
		Overlays:     []OverlaySpec{},
	}
	if rNil.Hash() != captured {
		t.Fatalf("nil Overlays diverged from baseline: got %q want %q", rNil.Hash(), captured)
	}
	if rEmpty.Hash() != captured {
		t.Fatalf("empty Overlays diverged from baseline: got %q want %q", rEmpty.Hash(), captured)
	}
}

// TestCanonicalHash_OverlaysIncluded verifies the Overlays slot
// participates in the hash:
//   - structurally identical overlays → same hash
//   - same overlays differing only in Kind → different hash
//   - same overlays differing only in spec order → different hash
//     (spec order is load-bearing because each spec produces one
//     Response.Overlays entry in matching order)
//   - populating a different OverlayRef family arm → different hash
func TestCanonicalHash_OverlaysIncluded(t *testing.T) {
	base := func() *Request {
		return &Request{
			Cohort: &Cohort{Filename: "a.pulse"},
			Overlays: []OverlaySpec{
				{
					Name:  "row_index",
					Kind:  OverlayKindIndexVsMargin,
					Scope: OverlayScopeCell,
					Ref:   OverlayRef{Margin: &OverlayMarginRef{Axis: MarginAxisRow}},
				},
			},
		}
	}

	a := base()
	b := base()
	if a.Hash() != b.Hash() {
		t.Fatalf("identical overlay specs must share a hash: %q vs %q", a.Hash(), b.Hash())
	}

	// Distinct from an overlay-free request.
	bare := &Request{Cohort: &Cohort{Filename: "a.pulse"}}
	if bare.Hash() == a.Hash() {
		t.Fatalf("overlay-bearing Request must hash distinctly from overlay-free Request")
	}

	// Differing only in Kind — synthesise a second kind value (E1
	// ships only INDEX_VS_MARGIN, so use a non-canonical kind string
	// to drive the comparison; OverlayKind is a string newtype so any
	// distinct value differentiates the canonical JSON form).
	differentKind := base()
	differentKind.Overlays[0].Kind = OverlayKind("OVERLAY_DIFFERENT_KIND")
	if a.Hash() == differentKind.Hash() {
		t.Fatalf("differing Kind must produce different hash")
	}

	// Differing only in Scope.
	differentScope := base()
	differentScope.Overlays[0].Scope = OverlayScopeRow
	if a.Hash() == differentScope.Hash() {
		t.Fatalf("differing Scope must produce different hash")
	}

	// Differing only in populated Ref arm — Margin.Axis swap.
	differentAxis := base()
	differentAxis.Overlays[0].Ref = OverlayRef{Margin: &OverlayMarginRef{Axis: MarginAxisColumn}}
	if a.Hash() == differentAxis.Hash() {
		t.Fatalf("differing Margin.Axis must produce different hash")
	}

	// Differing only in OverlayRef family arm — Margin vs Sibling.
	// (Sibling is reserved E1, but the canonical-hash routine still
	// covers it because OverlayRef carries the pointer slot today.)
	differentArm := base()
	differentArm.Overlays[0].Ref = OverlayRef{Sibling: &OverlaySiblingRef{Field: "brand", Value: "acme"}}
	if a.Hash() == differentArm.Hash() {
		t.Fatalf("differing OverlayRef family arm must produce different hash")
	}

	// Spec order is load-bearing — two overlays in swapped order
	// must hash differently.
	twoA := &Request{
		Cohort: &Cohort{Filename: "a.pulse"},
		Overlays: []OverlaySpec{
			{Name: "first", Kind: OverlayKindIndexVsMargin, Scope: OverlayScopeCell, Ref: OverlayRef{Margin: &OverlayMarginRef{Axis: MarginAxisRow}}},
			{Name: "second", Kind: OverlayKindIndexVsMargin, Scope: OverlayScopeCell, Ref: OverlayRef{Margin: &OverlayMarginRef{Axis: MarginAxisColumn}}},
		},
	}
	twoB := &Request{
		Cohort: &Cohort{Filename: "a.pulse"},
		Overlays: []OverlaySpec{
			{Name: "second", Kind: OverlayKindIndexVsMargin, Scope: OverlayScopeCell, Ref: OverlayRef{Margin: &OverlayMarginRef{Axis: MarginAxisColumn}}},
			{Name: "first", Kind: OverlayKindIndexVsMargin, Scope: OverlayScopeCell, Ref: OverlayRef{Margin: &OverlayMarginRef{Axis: MarginAxisRow}}},
		},
	}
	if twoA.Hash() == twoB.Hash() {
		t.Fatalf("overlay spec order must be load-bearing — swapped order produced identical hash")
	}

	// Differing only in Name.
	differentName := base()
	differentName.Overlays[0].Name = "col_index"
	if a.Hash() == differentName.Hash() {
		t.Fatalf("differing Name must produce different hash")
	}
}
