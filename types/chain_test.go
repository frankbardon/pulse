package types

import (
	"encoding/json"
	"testing"
)

// TestChainRequest_ZeroOverlayByteIdentical locks the additive
// contract for the whole-chain Overlays slot landed in E6-S2: a
// ChainRequest with no Overlays slot populated MUST marshal to the
// same JSON bytes the pre-S2 ChainRequest produced. The `omitempty`
// tag on the slot is what makes the round-trip byte-identical;
// dropping it would break every cached chain request authored before
// the slot existed.
func TestChainRequest_ZeroOverlayByteIdentical(t *testing.T) {
	const captured = `{"cohort":{"filename":"a.pulse"},"stages":[{"name":"stage_zero","request":{"aggregations":[{"type":"AGG_SUM","field":"x"}]}}]}`

	base := func() *ChainRequest {
		return &ChainRequest{
			Cohort: &Cohort{Filename: "a.pulse"},
			Stages: []*ChainStage{
				{
					Name: "stage_zero",
					Request: &Request{
						Aggregations: []*Aggregation{
							{Type: AGG_SUM, Field: "x"},
						},
					},
				},
			},
		}
	}

	implicit := base()
	bs, err := json.Marshal(implicit)
	if err != nil {
		t.Fatalf("marshal implicit: %v", err)
	}
	if string(bs) != captured {
		t.Fatalf("implicit-Overlays ChainRequest drifted from captured baseline\nwant: %s\n got: %s", captured, string(bs))
	}

	explicitNil := base()
	explicitNil.Overlays = nil
	bs, err = json.Marshal(explicitNil)
	if err != nil {
		t.Fatalf("marshal nil: %v", err)
	}
	if string(bs) != captured {
		t.Fatalf("explicit-nil Overlays ChainRequest drifted from captured baseline\nwant: %s\n got: %s", captured, string(bs))
	}

	emptySlice := base()
	emptySlice.Overlays = []*ChainOverlaySpec{}
	bs, err = json.Marshal(emptySlice)
	if err != nil {
		t.Fatalf("marshal empty: %v", err)
	}
	if string(bs) != captured {
		t.Fatalf("empty-slice Overlays ChainRequest drifted from captured baseline\nwant: %s\n got: %s", captured, string(bs))
	}
}

// TestChainCanonicalHash_OverlayFreeByteIdentity locks the additive
// contract on the hash surface: adding the Overlays slot (E6-S2) to
// ChainRequest must NOT change the canonical hash for any existing
// overlay-free chain request. The slot is `omitempty`, so
// json.Marshal omits the `overlays` key entirely when the slice is
// nil or empty, and the canonical-hash pipeline therefore produces
// byte-identical output to the pre-Overlays implementation.
//
// The captured constant below is the hash for the exact request
// computed against the canonical-hash routine after this story
// landed. Any change that alters this hash means the additive
// extension broke byte-identity for callers that have pinned dedup
// keys against earlier Pulse versions — bump CanonicalHash with a
// migration plan, do not silently re-hash.
func TestChainCanonicalHash_OverlayFreeByteIdentity(t *testing.T) {
	const captured = "c6e6a6f5d28dc6e4eb3bd712cc1f3b10"
	base := func() *ChainRequest {
		return &ChainRequest{
			Cohort: &Cohort{Filename: "a.pulse"},
			Stages: []*ChainStage{
				{
					Name: "stage_zero",
					Request: &Request{
						Aggregations: []*Aggregation{
							{Type: AGG_SUM, Field: "x"},
						},
					},
				},
			},
		}
	}

	implicit := base()
	if got := implicit.Hash(); got != captured {
		t.Fatalf("overlay-free ChainRequest.Hash() drifted from captured baseline: got %q want %q", got, captured)
	}

	explicitNil := base()
	explicitNil.Overlays = nil
	if got := explicitNil.Hash(); got != captured {
		t.Fatalf("explicit-nil Overlays diverged from baseline: got %q want %q", got, captured)
	}

	emptySlice := base()
	emptySlice.Overlays = []*ChainOverlaySpec{}
	if got := emptySlice.Hash(); got != captured {
		t.Fatalf("empty Overlays diverged from baseline: got %q want %q", got, captured)
	}
}

// TestChainCanonicalHash_OverlaysIncluded verifies the whole-chain
// Overlays slot participates in the hash:
//   - structurally identical overlays → same hash
//   - same overlays differing only in Kind → different hash
//   - same overlays differing only in Ref / Target → different hash
//   - same overlays differing only in spec order → different hash
//     (spec order is load-bearing because each spec produces one
//     ChainResponse.Overlays entry in matching order)
func TestChainCanonicalHash_OverlaysIncluded(t *testing.T) {
	zero := 0
	two := 2
	base := func() *ChainRequest {
		return &ChainRequest{
			Cohort: &Cohort{Filename: "a.pulse"},
			Stages: []*ChainStage{
				{
					Name: "stage_zero",
					Request: &Request{
						Aggregations: []*Aggregation{{Type: AGG_SUM, Field: "x"}},
					},
				},
				{
					Name: "stage_one",
					Request: &Request{
						Aggregations: []*Aggregation{{Type: AGG_SUM, Field: "x"}},
					},
				},
				{
					Name: "stage_two",
					Request: &Request{
						Aggregations: []*Aggregation{{Type: AGG_SUM, Field: "x"}},
					},
				},
			},
			Overlays: []*ChainOverlaySpec{
				{
					Name:   "stage2_vs_stage0",
					Kind:   OverlayKind("OVERLAY_INDEX_VS_STAGE"),
					Ref:    StageRef{Index: &zero},
					Target: StageRef{Index: &two},
					Scope:  OverlayScopeCell,
				},
			},
		}
	}

	a := base()
	b := base()
	if a.Hash() != b.Hash() {
		t.Fatalf("identical overlay specs must share a hash: %q vs %q", a.Hash(), b.Hash())
	}

	// Distinct from an overlay-free chain.
	bare := base()
	bare.Overlays = nil
	if bare.Hash() == a.Hash() {
		t.Fatalf("overlay-bearing ChainRequest must hash distinctly from overlay-free ChainRequest")
	}

	// Differing only in Kind.
	differentKind := base()
	differentKind.Overlays[0].Kind = OverlayKind("OVERLAY_DELTA_VS_STAGE")
	if a.Hash() == differentKind.Hash() {
		t.Fatalf("differing Kind must produce different hash")
	}

	// Differing only in Ref.Index.
	differentRefIdx := base()
	one := 1
	differentRefIdx.Overlays[0].Ref = StageRef{Index: &one}
	if a.Hash() == differentRefIdx.Hash() {
		t.Fatalf("differing Ref.Index must produce different hash")
	}

	// Differing only in Target.Index.
	differentTargetIdx := base()
	differentTargetIdx.Overlays[0].Target = StageRef{Index: &one}
	if a.Hash() == differentTargetIdx.Hash() {
		t.Fatalf("differing Target.Index must produce different hash")
	}

	// Ref by Name vs Ref by Index produce different hashes (the
	// StageRef arms are XOR — the canonical JSON form carries the
	// populated arm only).
	differentArm := base()
	differentArm.Overlays[0].Ref = StageRef{Name: "stage_zero"}
	if a.Hash() == differentArm.Hash() {
		t.Fatalf("Index vs Name StageRef arms must produce different hashes")
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

	// Differing only in Params.
	differentParams := base()
	differentParams.Overlays[0].Params = map[string]any{"window": 3}
	if a.Hash() == differentParams.Hash() {
		t.Fatalf("differing Params must produce different hash")
	}

	// Spec order is load-bearing — two overlays in swapped order
	// must hash differently.
	twoA := base()
	twoA.Overlays = []*ChainOverlaySpec{
		{Name: "first", Kind: OverlayKind("OVERLAY_INDEX_VS_STAGE"), Ref: StageRef{Index: &zero}, Target: StageRef{Index: &two}, Scope: OverlayScopeCell},
		{Name: "second", Kind: OverlayKind("OVERLAY_DELTA_VS_STAGE"), Ref: StageRef{Index: &zero}, Target: StageRef{Index: &two}, Scope: OverlayScopeCell},
	}
	twoB := base()
	twoB.Overlays = []*ChainOverlaySpec{
		{Name: "second", Kind: OverlayKind("OVERLAY_DELTA_VS_STAGE"), Ref: StageRef{Index: &zero}, Target: StageRef{Index: &two}, Scope: OverlayScopeCell},
		{Name: "first", Kind: OverlayKind("OVERLAY_INDEX_VS_STAGE"), Ref: StageRef{Index: &zero}, Target: StageRef{Index: &two}, Scope: OverlayScopeCell},
	}
	if twoA.Hash() == twoB.Hash() {
		t.Fatalf("overlay spec order must be load-bearing — swapped order produced identical hash")
	}
}

// TestCanonicalHash_ChainOverlaysIncluded is the E6-S8 acceptance-list
// alias for the chain canonical-hash gate. The underlying coverage rides
// TestChainCanonicalHash_OverlaysIncluded (above); this wrapper
// re-executes the two load-bearing assertions under the exact name the
// E6-S8 acceptance list demands so the gate is discoverable via the
// promised test identifier.
//
// Asserts:
//   - nil-vs-spec hash differs (overlay-bearing ChainRequest does NOT
//     collide with the overlay-free baseline — overlays participate in
//     the canonical hash so dedup keys see overlay slots as load-bearing).
//   - identical-spec hash equality (two ChainRequests with byte-equal
//     overlay slots produce identical canonical hashes — the hash is
//     deterministic).
func TestCanonicalHash_ChainOverlaysIncluded(t *testing.T) {
	zero := 0
	two := 2
	build := func() *ChainRequest {
		return &ChainRequest{
			Cohort: &Cohort{Filename: "a.pulse"},
			Stages: []*ChainStage{
				{Name: "stage_zero", Request: &Request{
					Aggregations: []*Aggregation{{Type: AGG_SUM, Field: "x"}},
				}},
				{Name: "stage_one", Request: &Request{
					Aggregations: []*Aggregation{{Type: AGG_SUM, Field: "x"}},
				}},
				{Name: "stage_two", Request: &Request{
					Aggregations: []*Aggregation{{Type: AGG_SUM, Field: "x"}},
				}},
			},
			Overlays: []*ChainOverlaySpec{
				{
					Name:   "stage2_vs_stage0",
					Kind:   OverlayKind("OVERLAY_INDEX_VS_STAGE"),
					Ref:    StageRef{Index: &zero},
					Target: StageRef{Index: &two},
					Scope:  OverlayScopeCell,
				},
			},
		}
	}

	// nil-vs-spec hash diff: an overlay-bearing ChainRequest must NOT
	// share a hash with the same ChainRequest minus Overlays. This is
	// the load-bearing assertion the dual-slot design depends on — if
	// overlays did not participate in the hash, two requests that
	// differ only on the whole-chain layer slot would dedup to the
	// same cache key, silently swapping overlay output.
	withSpec := build()
	bare := build()
	bare.Overlays = nil
	if withSpec.Hash() == bare.Hash() {
		t.Fatalf("nil-vs-spec ChainRequest hashes must differ; both produced %q", withSpec.Hash())
	}

	// identical-spec hash equality: two independently-built ChainRequests
	// with byte-equal overlay slots must produce identical hashes — the
	// canonical-hash routine is deterministic over equivalent inputs.
	again := build()
	if got, want := again.Hash(), withSpec.Hash(); got != want {
		t.Fatalf("identical overlay specs must share a hash: got %q vs %q", got, want)
	}
}

// TestStageRef_IndexNamedXOR confirms StageRef's two arms round-trip
// through JSON cleanly and that the canonical hash treats the two
// arms as semantically distinct. The XOR validation (exactly one of
// Index / Name) lives downstream (E6-S3 / E6-S7 / E6-S11) — the
// struct itself simply round-trips both shapes.
func TestStageRef_IndexNamedXOR(t *testing.T) {
	zero := 0
	indexForm := StageRef{Index: &zero}
	bs, err := json.Marshal(indexForm)
	if err != nil {
		t.Fatalf("marshal index form: %v", err)
	}
	if want := `{"index":0}`; string(bs) != want {
		t.Fatalf("index-form JSON drifted: want %q got %q", want, string(bs))
	}
	var rt StageRef
	if err := json.Unmarshal(bs, &rt); err != nil {
		t.Fatalf("unmarshal index form: %v", err)
	}
	if rt.Index == nil || *rt.Index != 0 || rt.Name != "" {
		t.Fatalf("index-form round-trip drifted: %+v", rt)
	}

	nameForm := StageRef{Name: "stage_zero"}
	bs, err = json.Marshal(nameForm)
	if err != nil {
		t.Fatalf("marshal name form: %v", err)
	}
	if want := `{"name":"stage_zero"}`; string(bs) != want {
		t.Fatalf("name-form JSON drifted: want %q got %q", want, string(bs))
	}
	var rt2 StageRef
	if err := json.Unmarshal(bs, &rt2); err != nil {
		t.Fatalf("unmarshal name form: %v", err)
	}
	if rt2.Name != "stage_zero" || rt2.Index != nil {
		t.Fatalf("name-form round-trip drifted: %+v", rt2)
	}

	// Empty StageRef marshals to {} (both arms omitempty) — the
	// downstream validator rejects this shape, but the struct
	// itself round-trips it.
	emptyForm := StageRef{}
	bs, err = json.Marshal(emptyForm)
	if err != nil {
		t.Fatalf("marshal empty form: %v", err)
	}
	if want := `{}`; string(bs) != want {
		t.Fatalf("empty-form JSON drifted: want %q got %q", want, string(bs))
	}

	// Hash distinctness across arms.
	if CanonicalHash("stage", indexForm) == CanonicalHash("stage", nameForm) {
		t.Fatalf("Index vs Name StageRef hashes must differ")
	}
	if CanonicalHash("stage", indexForm) == CanonicalHash("stage", emptyForm) {
		t.Fatalf("Index vs empty StageRef hashes must differ")
	}
}
