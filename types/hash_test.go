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

func TestLookupRequest_Hash_Deterministic(t *testing.T) {
	a := &LookupRequest{
		Cohort:        &Cohort{Filename: "cohort.pulse"},
		Field:         "id",
		Value:         "3",
		ReturnColumns: []string{"score", "region"},
	}
	b := &LookupRequest{
		Cohort:        &Cohort{Filename: "cohort.pulse"},
		Field:         "id",
		Value:         "3",
		ReturnColumns: []string{"score", "region"},
	}
	if a.Hash() != b.Hash() {
		t.Fatalf("LookupRequest hash unstable across structurally identical requests")
	}
	if a.Hash() == "" {
		t.Fatal("LookupRequest.Hash() returned empty string")
	}

	c := &LookupRequest{
		Cohort: &Cohort{Filename: "cohort.pulse"},
		Field:  "id",
		Value:  "4", // differs
	}
	if a.Hash() == c.Hash() {
		t.Fatal("LookupRequest hash must differ when Value differs")
	}

	if (*LookupRequest)(nil).Hash() == "" {
		t.Fatal("nil LookupRequest.Hash() must still return a stable non-empty hash")
	}
}

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

func TestCanonicalHash_RequestLabelEmptyByteIdentical(t *testing.T) {
	const captured = "a4e259f7ed18dcbcb3e78b76066bcbee"
	noLabel := &Request{
		Cohort: &Cohort{Filename: "a.pulse"},
		Aggregations: []*Aggregation{
			{Type: AGG_SUM, Field: "x"},
		},
	}
	emptyLabel := &Request{
		Label:  "",
		Cohort: &Cohort{Filename: "a.pulse"},
		Aggregations: []*Aggregation{
			{Type: AGG_SUM, Field: "x"},
		},
	}
	if got := noLabel.Hash(); got != captured {
		t.Fatalf("Request without Label drifted from captured baseline: got %q want %q", got, captured)
	}
	if got := emptyLabel.Hash(); got != captured {
		t.Fatalf("Request with empty Label drifted from captured baseline: got %q want %q", got, captured)
	}
}

func TestComposedRequest_OverlayFreeByteIdentity(t *testing.T) {
	base := func() *ComposedRequest {
		return &ComposedRequest{
			Requests: []*Request{
				{
					Cohort: &Cohort{Filename: "a.pulse"},
					Aggregations: []*Aggregation{
						{Type: AGG_SUM, Field: "x"},
					},
				},
			},
		}
	}

	implicit := base()
	captured := implicit.Hash()
	if len(captured) != 32 {
		t.Fatalf("ComposedRequest.Hash() wrong width: %q (len=%d)", captured, len(captured))
	}

	// Nil and empty Overlays must both hash identically to the
	// implicit (omitted slot) form.
	explicitNil := base()
	explicitNil.Overlays = nil
	if got := explicitNil.Hash(); got != captured {
		t.Fatalf("explicit-nil Overlays diverged from baseline: got %q want %q", got, captured)
	}

	emptySlice := base()
	emptySlice.Overlays = []ComposeOverlaySpec{}
	if got := emptySlice.Hash(); got != captured {
		t.Fatalf("empty Overlays diverged from baseline: got %q want %q", got, captured)
	}
}

func TestCanonicalHash_ComposedRequest_LabelStability(t *testing.T) {
	base := func() *ComposedRequest {
		return &ComposedRequest{
			Requests: []*Request{
				{
					Cohort: &Cohort{Filename: "a.pulse"},
					Aggregations: []*Aggregation{
						{Type: AGG_SUM, Field: "x"},
					},
				},
				{
					Cohort: &Cohort{Filename: "a.pulse"},
					Aggregations: []*Aggregation{
						{Type: AGG_SUM, Field: "y"},
					},
				},
			},
		}
	}

	// Empty slot Labels — the auto-default normalizer fills them with
	// `request_1` / `request_2` before hashing.
	empty := base()

	// Explicitly-named slot Labels matching the auto-default
	// synthesis — must hash identically to the empty form.
	autoDefaulted := base()
	autoDefaulted.Requests[0].Label = "request_1"
	autoDefaulted.Requests[1].Label = "request_2"
	if empty.Hash() != autoDefaulted.Hash() {
		t.Fatalf("empty Labels and explicit auto-default Labels must hash identically: %q vs %q", empty.Hash(), autoDefaulted.Hash())
	}

	// Caller-supplied non-default Label — must hash distinctly.
	custom := base()
	custom.Requests[0].Label = "audience_a"
	if empty.Hash() == custom.Hash() {
		t.Fatalf("custom slot Label must produce a distinct hash from auto-default: both %q", empty.Hash())
	}

	// Differing only in the SECOND slot's Label — distinct hashes too.
	customSecond := base()
	customSecond.Requests[1].Label = "audience_b"
	if empty.Hash() == customSecond.Hash() {
		t.Fatalf("custom Label on slot 2 must produce a distinct hash from auto-default: both %q", empty.Hash())
	}
	if custom.Hash() == customSecond.Hash() {
		t.Fatalf("Labels on slot 1 vs slot 2 must produce distinct hashes")
	}

	// Caller pointer is NOT mutated by the hash call — the auto-
	// default lives on the hash-internal clone.
	if empty.Requests[0].Label != "" {
		t.Fatalf("ComposedRequest.Hash() must not mutate the caller's *Request.Label; got %q", empty.Requests[0].Label)
	}
	if empty.Requests[1].Label != "" {
		t.Fatalf("ComposedRequest.Hash() must not mutate slot 2's *Request.Label; got %q", empty.Requests[1].Label)
	}
}

// TestCanonicalHash_ComposedRequest_Overlays verifies the Compose-
// level Overlays slot participates in the canonical hash:
//   - structurally identical overlays → same hash
//   - same overlays differing only in Kind → different hash
//   - same overlays differing only in Reference → different hash
//   - Targets ORDER is significant — swapping Targets[0] and Targets[1]
//     produces a distinct hash
//   - same overlays differing only in spec order → different hash
//     (spec order is load-bearing because each spec produces one
//     ComposedResponse.Overlays entry in matching order)
//   - Level / Within / Params each fold into the canonical hash
func TestCanonicalHash_ComposedRequest_Overlays(t *testing.T) {
	base := func() *ComposedRequest {
		return &ComposedRequest{
			Requests: []*Request{
				{Label: "audience_a", Cohort: &Cohort{Filename: "a.pulse"}, Aggregations: []*Aggregation{{Type: AGG_SUM, Field: "x"}}},
				{Label: "total_pop", Cohort: &Cohort{Filename: "a.pulse"}, Aggregations: []*Aggregation{{Type: AGG_SUM, Field: "x"}}},
			},
			Overlays: []ComposeOverlaySpec{
				{
					Name:      "a_vs_pop",
					Kind:      OverlayKind("OVERLAY_INDEX_VS_REF"),
					Scope:     OverlayScopeCell,
					Reference: "total_pop",
					Targets:   []string{"audience_a"},
				},
			},
		}
	}

	a := base()
	b := base()
	if a.Hash() != b.Hash() {
		t.Fatalf("identical Compose overlay specs must share a hash: %q vs %q", a.Hash(), b.Hash())
	}

	// Distinct from an overlay-free ComposedRequest.
	bare := base()
	bare.Overlays = nil
	if bare.Hash() == a.Hash() {
		t.Fatalf("overlay-bearing ComposedRequest must hash distinctly from overlay-free ComposedRequest")
	}

	// Differing only in Kind.
	differentKind := base()
	differentKind.Overlays[0].Kind = OverlayKind("OVERLAY_DELTA_VS_REF")
	if a.Hash() == differentKind.Hash() {
		t.Fatalf("differing Compose-overlay Kind must produce different hash")
	}

	// Differing only in Reference — "total_pop" vs "audience_a" (per
	// the acceptance list's worked example).
	differentRef := base()
	differentRef.Overlays[0].Reference = "audience_a"
	if a.Hash() == differentRef.Hash() {
		t.Fatalf("differing Reference must produce different hash")
	}

	// Differing only in Targets[0].
	differentTarget := base()
	differentTarget.Overlays[0].Targets = []string{"total_pop"}
	if a.Hash() == differentTarget.Hash() {
		t.Fatalf("differing Targets[0] must produce different hash")
	}

	// Order of Targets is significant — swapping the slice order
	// must produce a distinct hash.
	twoTargetsA := base()
	twoTargetsA.Overlays[0].Targets = []string{"audience_a", "audience_b"}
	twoTargetsB := base()
	twoTargetsB.Overlays[0].Targets = []string{"audience_b", "audience_a"}
	if twoTargetsA.Hash() == twoTargetsB.Hash() {
		t.Fatalf("Targets slice order must be load-bearing — swapped order produced identical hash")
	}

	// Differing only in Scope.
	differentScope := base()
	differentScope.Overlays[0].Scope = OverlayScopeRow
	if a.Hash() == differentScope.Hash() {
		t.Fatalf("differing Scope must produce different hash")
	}

	// Differing only in Name.
	differentName := base()
	differentName.Overlays[0].Name = "renamed"
	if a.Hash() == differentName.Hash() {
		t.Fatalf("differing Name must produce different hash")
	}

	// Differing only in Level.
	differentLevel := base()
	differentLevel.Overlays[0].Level = 1
	if a.Hash() == differentLevel.Hash() {
		t.Fatalf("differing Level must produce different hash")
	}

	// Differing only in Within.
	differentWithin := base()
	differentWithin.Overlays[0].Within = 1
	if a.Hash() == differentWithin.Hash() {
		t.Fatalf("differing Within must produce different hash")
	}

	// Differing only in Params.
	differentParams := base()
	differentParams.Overlays[0].Params = map[string]any{"scale": 2}
	if a.Hash() == differentParams.Hash() {
		t.Fatalf("differing Params must produce different hash")
	}

	// Spec order is load-bearing — two overlays in swapped order
	// must hash differently.
	twoA := base()
	twoA.Overlays = []ComposeOverlaySpec{
		{Name: "first", Kind: OverlayKind("OVERLAY_INDEX_VS_REF"), Scope: OverlayScopeCell, Reference: "total_pop", Targets: []string{"audience_a"}},
		{Name: "second", Kind: OverlayKind("OVERLAY_DELTA_VS_REF"), Scope: OverlayScopeCell, Reference: "total_pop", Targets: []string{"audience_a"}},
	}
	twoB := base()
	twoB.Overlays = []ComposeOverlaySpec{
		{Name: "second", Kind: OverlayKind("OVERLAY_DELTA_VS_REF"), Scope: OverlayScopeCell, Reference: "total_pop", Targets: []string{"audience_a"}},
		{Name: "first", Kind: OverlayKind("OVERLAY_INDEX_VS_REF"), Scope: OverlayScopeCell, Reference: "total_pop", Targets: []string{"audience_a"}},
	}
	if twoA.Hash() == twoB.Hash() {
		t.Fatalf("Compose overlay spec order must be load-bearing — swapped order produced identical hash")
	}
}

func TestCanonicalHash_Request_LabelOmitemptyMatchesDefault(t *testing.T) {
	// Bare Request — empty Label and explicit `Label: ""` must hash
	// identically (the structural baseline locked by
	// TestCanonicalHash_RequestLabelEmptyByteIdentical above).
	r1 := &Request{
		Cohort:       &Cohort{Filename: "a.pulse"},
		Aggregations: []*Aggregation{{Type: AGG_SUM, Field: "x"}},
	}
	r2 := &Request{
		Label:        "",
		Cohort:       &Cohort{Filename: "a.pulse"},
		Aggregations: []*Aggregation{{Type: AGG_SUM, Field: "x"}},
	}
	if r1.Hash() != r2.Hash() {
		t.Fatalf("Bare Request: empty Label must hash identically to omitted Label: %q vs %q", r1.Hash(), r2.Hash())
	}

	// Bare Request with non-empty Label — distinct from the empty
	// form. Locks the field-participation contract (Label is on the
	// canonical walk, not silently dropped).
	r3 := &Request{
		Label:        "audience_a",
		Cohort:       &Cohort{Filename: "a.pulse"},
		Aggregations: []*Aggregation{{Type: AGG_SUM, Field: "x"}},
	}
	if r1.Hash() == r3.Hash() {
		t.Fatalf("Bare Request: non-empty Label must produce a distinct hash from empty Label")
	}

	// Two bare Requests with the same explicit Label must hash
	// identically (deterministic over equivalent inputs).
	r4 := &Request{
		Label:        "audience_a",
		Cohort:       &Cohort{Filename: "a.pulse"},
		Aggregations: []*Aggregation{{Type: AGG_SUM, Field: "x"}},
	}
	if r3.Hash() != r4.Hash() {
		t.Fatalf("identical explicit Labels must share a hash: %q vs %q", r3.Hash(), r4.Hash())
	}

	// ChainRequest stage with non-empty Label — distinct from the
	// same stage with empty Label. The per-stage Request's Label
	// folds into the chain hash through the data-driven JSON walk
	// (no special-cased normalizer at the chain level — only Compose
	// auto-defaults).
	chainEmpty := &ChainRequest{
		Cohort: &Cohort{Filename: "a.pulse"},
		Stages: []*ChainStage{
			{Name: "s0", Request: &Request{Aggregations: []*Aggregation{{Type: AGG_SUM, Field: "x"}}}},
		},
	}
	chainLabeled := &ChainRequest{
		Cohort: &Cohort{Filename: "a.pulse"},
		Stages: []*ChainStage{
			{Name: "s0", Request: &Request{Label: "stage_data", Aggregations: []*Aggregation{{Type: AGG_SUM, Field: "x"}}}},
		},
	}
	if chainEmpty.Hash() == chainLabeled.Hash() {
		t.Fatalf("ChainRequest with stage Label vs without must produce distinct hashes")
	}
}

// TestComposedRequest_AutoDefaultDoesNotMutateCaller verifies the
// hash-time auto-default normalizer never mutates the caller's
// *ComposedRequest or any nested *Request pointer. The validate-time
// normalizer in service/compose_label.go already documents this
// guarantee for the execution path; the hash path mirrors it so
// callers can safely use ComposedRequest.Hash() inside dedup-cache
// lookups without worrying about silent state mutation.
func TestComposedRequest_AutoDefaultDoesNotMutateCaller(t *testing.T) {
	r1 := &Request{Cohort: &Cohort{Filename: "a.pulse"}}
	r2 := &Request{Cohort: &Cohort{Filename: "a.pulse"}}
	composed := &ComposedRequest{Requests: []*Request{r1, r2}}
	// Capture the caller's *Request pointers before hashing.
	beforeR1 := composed.Requests[0]
	beforeR2 := composed.Requests[1]
	beforeR1Label := beforeR1.Label
	beforeR2Label := beforeR2.Label
	_ = composed.Hash()
	// The caller's slot pointers must be unchanged.
	if composed.Requests[0] != beforeR1 {
		t.Fatalf("ComposedRequest.Hash() swapped slot 0 *Request pointer")
	}
	if composed.Requests[1] != beforeR2 {
		t.Fatalf("ComposedRequest.Hash() swapped slot 1 *Request pointer")
	}
	if composed.Requests[0].Label != beforeR1Label {
		t.Fatalf("ComposedRequest.Hash() mutated slot 0 Label: %q -> %q", beforeR1Label, composed.Requests[0].Label)
	}
	if composed.Requests[1].Label != beforeR2Label {
		t.Fatalf("ComposedRequest.Hash() mutated slot 1 Label: %q -> %q", beforeR2Label, composed.Requests[1].Label)
	}
}

func TestCanonicalHash_OverlayLevelDistinct(t *testing.T) {
	base := func() *Request {
		return &Request{
			Cohort: &Cohort{Filename: "a.pulse"},
			Overlays: []OverlaySpec{
				{
					Name:  "row_share",
					Kind:  OverlayKindShareOfRow,
					Scope: OverlayScopeCell,
					Ref:   OverlayRef{Margin: &OverlayMarginRef{Axis: MarginAxisRow}},
				},
			},
		}
	}

	zeroLevel := base()
	level1 := base()
	level1.Overlays[0].Level = 1
	if zeroLevel.Hash() == level1.Hash() {
		t.Fatalf("Level=0 and Level=1 must produce distinct hashes")
	}

	level2 := base()
	level2.Overlays[0].Level = 2
	if level1.Hash() == level2.Hash() {
		t.Fatalf("Level=1 and Level=2 must produce distinct hashes")
	}

	zeroWithin := base()
	within1 := base()
	within1.Overlays[0].Within = 1
	if zeroWithin.Hash() == within1.Hash() {
		t.Fatalf("Within=0 and Within=1 must produce distinct hashes")
	}

	// Mixed Level + Within distinct from each slot individually.
	bothSet := base()
	bothSet.Overlays[0].Level = 1
	bothSet.Overlays[0].Within = 1
	if bothSet.Hash() == level1.Hash() {
		t.Fatalf("Level=1+Within=1 must hash distinctly from Level=1 alone")
	}
	if bothSet.Hash() == within1.Hash() {
		t.Fatalf("Level=1+Within=1 must hash distinctly from Within=1 alone")
	}
}

func TestCanonicalHash_ResponseComponentsByteIdentity(t *testing.T) {
	implicit := Response{}
	explicit := Response{Components: nil}

	if a, b := CanonicalHash("response", implicit), CanonicalHash("response", explicit); a != b {
		t.Fatalf("nil Components diverged from implicit form: implicit=%q explicit=%q", a, b)
	}
}

// TestCanonicalHash_ResponseComponentsOperatorKeyOrderInvariant verifies
// the canonical-hash routine walks the per-aggregator Operator map in
// sorted-key order. Two AggregationComponents differing only in the
// runtime insertion order of their Operator map MUST hash identically;
// otherwise the data-driven dedup-cache contract breaks for
// service-layer emitters that populate the map in their own iteration
// order.
func TestCanonicalHash_ResponseComponentsOperatorKeyOrderInvariant(t *testing.T) {
	a := Response{
		Components: &ResponseComponents{
			Aggregations: []AggregationComponents{
				{
					Label: "avg_score",
					N:     100,
					NNull: 3,
					Operator: map[string]any{
						"mean":     42.5,
						"variance": 7.25,
					},
				},
			},
		},
	}
	b := Response{
		Components: &ResponseComponents{
			Aggregations: []AggregationComponents{
				{
					Label: "avg_score",
					N:     100,
					NNull: 3,
					Operator: map[string]any{
						// Same keys, deliberately added in alternate
						// runtime order; map iteration order is
						// undefined but canonical-hash sorts keys.
						"variance": 7.25,
						"mean":     42.5,
					},
				},
			},
		},
	}
	if ha, hb := CanonicalHash("response", a), CanonicalHash("response", b); ha != hb {
		t.Fatalf("Operator map key-order broke hash equality: %q vs %q", ha, hb)
	}

	// And populated vs nil Components must differ — slot presence is
	// hash-meaningful.
	bare := Response{}
	if ha, hb := CanonicalHash("response", a), CanonicalHash("response", bare); ha == hb {
		t.Fatalf("populated Components must hash distinctly from nil Components")
	}
}
