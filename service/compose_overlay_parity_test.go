package service

import (
	"context"
	"encoding/json"
	"math"
	"reflect"
	"runtime"
	"sort"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/fs"
	"github.com/frankbardon/pulse/processing"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

var composeHostOverlayKinds = []types.OverlayKind{
	types.OverlayKindIndexVsRef,
	types.OverlayKindDeltaVsRef,
	types.OverlayKindPropZCell,
	types.OverlayKindPropZPanel,
	types.OverlayKindTCell,
	types.OverlayKindTVsRef,
	types.OverlayKindZCell,
	types.OverlayKindZVsRef,
	types.OverlayKindChiSqVsRef,
	types.OverlayKindRank,
	types.OverlayKindPanelIndexVsRef,
}

// parityCohortSchema builds the shared categorical schema used across
// every parity fixture: two dictionary-backed dimensions (region,
// segment) plus an f64 value column. Reuses the convention from
// service/crosstab_test.go::crosstabSchema so the parity fixtures align
// with the in-tree crosstab tests' deterministic 3×2 grid.
func parityCohortSchema() *encoding.Schema {
	regionDict := encoding.NewDictionary()
	regionDict.Add("north")
	regionDict.Add("south")
	regionDict.Add("east")

	segmentDict := encoding.NewDictionary()
	segmentDict.Add("retail")
	segmentDict.Add("wholesale")

	return &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "region", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 0, CsvColumnIdx: 0, Dictionary: regionDict},
			{Name: "segment", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 1, CsvColumnIdx: 1, Dictionary: segmentDict},
			{Name: "value", Type: encoding.FieldTypeF64, ByteOffset: 2, CsvColumnIdx: 2},
		},
	}
}

// parityCohortRecords builds deterministic records that populate every
// (region, segment) bucket so the resulting Crosstab matrices have no
// absent cells for the (north, retail) / (north, wholesale) /
// (south, retail) / (south, wholesale) / (east, retail) / (east,
// wholesale) coordinates. Every bucket carries at least two records so
// AGG_COUNT yields ≥ 2 and the per-kind handlers exercise their finite
// branches.
func parityCohortRecords() [][]uint64 {
	return [][]uint64{
		{0, 0, math.Float64bits(10)},
		{0, 0, math.Float64bits(20)},
		{0, 0, math.Float64bits(30)},
		{0, 1, math.Float64bits(100)},
		{0, 1, math.Float64bits(110)},
		{1, 0, math.Float64bits(5)},
		{1, 0, math.Float64bits(15)},
		{1, 1, math.Float64bits(40)},
		{1, 1, math.Float64bits(50)},
		{2, 0, math.Float64bits(1)},
		{2, 0, math.Float64bits(2)},
		{2, 1, math.Float64bits(60)},
		{2, 1, math.Float64bits(70)},
	}
}

// parityCohortRecordsTreatment builds the same buckets as
// parityCohortRecords but with each value scaled by 1.5 so the
// treatment slot diverges deterministically from the baseline. The
// COUNT shape stays identical (every kind's key-set / schema-match gate
// is satisfied) while the value arithmetic in INDEX_VS_REF /
// DELTA_VS_REF / PROP_Z_CELL / T_CELL / Z_CELL etc. produces non-degen
// outputs.
func parityCohortRecordsTreatment() [][]uint64 {
	return [][]uint64{
		{0, 0, math.Float64bits(15)},
		{0, 0, math.Float64bits(30)},
		{0, 0, math.Float64bits(45)},
		{0, 1, math.Float64bits(150)},
		{0, 1, math.Float64bits(165)},
		{1, 0, math.Float64bits(7.5)},
		{1, 0, math.Float64bits(22.5)},
		{1, 1, math.Float64bits(60)},
		{1, 1, math.Float64bits(75)},
		{2, 0, math.Float64bits(1.5)},
		{2, 0, math.Float64bits(3)},
		{2, 1, math.Float64bits(90)},
		{2, 1, math.Float64bits(105)},
	}
}

// parityCohortRecordsExtra builds a third / fourth slot scaling factor
// for the multi-target panel kinds (PROP_Z_PANEL, PANEL_INDEX_VS_REF).
// The arg selects a multiplier so each panel target produces a
// distinct, deterministic value vector against the shared baseline.
func parityCohortRecordsExtra(scale float64) [][]uint64 {
	base := parityCohortRecords()
	out := make([][]uint64, len(base))
	for i, rec := range base {
		clone := make([]uint64, len(rec))
		copy(clone, rec)
		// Scale only the value column (last field).
		v := math.Float64frombits(clone[2])
		clone[2] = math.Float64bits(v * scale)
		out[i] = clone
	}
	return out
}

// parityServiceWithCohorts creates an in-memory service whose afero FS
// carries every cohort needed by the parity tests: a baseline cohort
// and three treatment cohorts whose values are scaled by 1.5×, 2.0×,
// and 0.5× respectively. Returns the live service. Hermetic — all I/O
// goes through fs.NewMemMap().
func parityServiceWithCohorts(t *testing.T) *Service {
	t.Helper()
	cfg := fs.NewMemMap()
	schema := parityCohortSchema()

	writeCohort := func(path string, records [][]uint64) {
		data := writePulseFile(t, schema, records)
		if err := afero.WriteFile(cfg.Fs(), path, data, 0644); err != nil {
			t.Fatalf("WriteFile %q: %v", path, err)
		}
	}
	writeCohort("baseline.pulse", parityCohortRecords())
	writeCohort("treatment.pulse", parityCohortRecordsTreatment())
	// Two additional treatment cohorts for multi-target panel kinds.
	writeCohort("t1.pulse", parityCohortRecordsExtra(2.0))
	writeCohort("t2.pulse", parityCohortRecordsExtra(0.5))
	return New(cfg)
}

// parityRequestMatrix builds a per-slot Request that produces a
// 3-region × 2-segment crosstab using AGG_SUM on the value column. The
// CrosstabShape stays MATRIX so the resulting Response.Crosstab.Matrix
// is the shape every matrix-host overlay handler expects.
func parityRequestMatrix(label, filename string) *types.Request {
	return &types.Request{
		Label:  label,
		Cohort: &types.Cohort{Filename: filename},
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "segment"}},
			Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "value", Label: "v"},
			Shape:   types.CrosstabShapeMatrix,
			Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
		},
	}
}

// parityRequestSeries builds a per-slot Request that produces a SERIES-
// shape grouped Process result. The grouper buckets by region (5 keys
// across baseline + treatment); the aggregator is AGG_SUM on the value
// column. The resulting Response.Data is the canonical SERIES-host
// shape the series-only overlays (TVsRef, ZVsRef, plus the SERIES arm
// of INDEX_VS_REF / DELTA_VS_REF) consume.
func parityRequestSeries(label, filename string) *types.Request {
	return &types.Request{
		Label:  label,
		Cohort: &types.Cohort{Filename: filename},
		Groups: []*types.Group{
			{Type: types.GROUP_CATEGORY, Field: "region"},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "value", Label: "v"},
		},
	}
}

// parityFixture describes one overlay-kind parity scenario. Each kind's
// parity test materialises a fresh service + composed request from the
// fixture; the runner then executes the facade-vs-internal byte-equal
// check.
type parityFixture struct {
	// kind is the COMPOSE-host overlay kind under test. Drives the
	// generated ComposeOverlaySpec.Kind slot.
	kind types.OverlayKind
	// scope selects the OverlayScope written into the spec.
	scope types.OverlayScope
	// targets carries the additional treatment-slot count. 1 ⇒ baseline
	// + treatment (2 slots); 3 ⇒ baseline + t0 + t1 + t2 (4 slots) for
	// panel kinds.
	targets int
	// shape declares the per-slot host shape. "matrix" → crosstab
	// fixture; "series" → grouped Process fixture.
	shape string
	// params is the per-spec params bag, forwarded verbatim into the
	// ComposeOverlaySpec.Params slot.
	params map[string]any
}

// parityComposed builds the parity ComposedRequest for a fixture. The
// returned request carries the overlay spec already populated — callers
// strip Overlays via withoutOverlays to obtain the parity baseline.
func parityComposed(fx parityFixture) *types.ComposedRequest {
	makeReq := parityRequestMatrix
	if fx.shape == "series" {
		makeReq = parityRequestSeries
	}
	var requests []*types.Request
	var spec types.ComposeOverlaySpec
	switch fx.targets {
	case 3:
		requests = []*types.Request{
			makeReq("baseline", "baseline.pulse"),
			makeReq("t0", "treatment.pulse"),
			makeReq("t1", "t1.pulse"),
			makeReq("t2", "t2.pulse"),
		}
		spec = types.ComposeOverlaySpec{
			Name:      string(fx.kind) + "_parity",
			Kind:      fx.kind,
			Scope:     fx.scope,
			Reference: "baseline",
			Targets:   []string{"t0", "t1", "t2"},
			Params:    fx.params,
		}
	default:
		requests = []*types.Request{
			makeReq("baseline", "baseline.pulse"),
			makeReq("treatment", "treatment.pulse"),
		}
		spec = types.ComposeOverlaySpec{
			Name:      string(fx.kind) + "_parity",
			Kind:      fx.kind,
			Scope:     fx.scope,
			Reference: "baseline",
			Targets:   []string{"treatment"},
			Params:    fx.params,
		}
	}
	return &types.ComposedRequest{
		Requests: requests,
		Overlays: []types.ComposeOverlaySpec{spec},
	}
}

// withoutOverlays returns a shallow clone of composed whose Overlays
// slot is nil — the parity baseline computation pulls per-slot
// Responses from this clone so the comparison is anchored on identical
// inputs.
func withoutOverlays(composed *types.ComposedRequest) *types.ComposedRequest {
	clone := *composed
	clone.Overlays = nil
	return &clone
}

// runComposeOverlayParity is the per-kind parity runner. Drives two
// passes:
//
//  1. Facade pass: svc.Compose(ctx, composed_with_overlay) → the
//     facade-surfaced ComposedResponse.Overlays slice.
//  2. Internal pass: svc.Compose(ctx, composed_without_overlay) →
//     per-slot Responses (no overlays computed), then call
//     processing.ApplyComposeOverlays + distributeComposeWarnings
//     directly against the same responses + spec set.
//
// Both passes operate on parallel deep-equal *Response inputs because
// service.Compose drives the same Process pipeline each call (the
// cohort + request shape is deterministic). Marshalling both layer
// slices via encoding/json then assertingbyte-equal is the
// load-bearing acceptance check.
//
// Failure modes the assertion catches:
//
//   - Service-layer wrapping that transforms the layer struct shape.
//   - Warning re-routing that mutates layers differently in facade vs
//     internal path.
//   - Field-order drift if any layer-side json encoder emits
//     non-deterministic key order (json.Marshal is map-key-sorted, so
//     this branch would only fire on Marshaler interfaces).
//   - Accidental copy elision that drops Summary / Warnings slots in
//     one path.
func runComposeOverlayParity(t *testing.T, fx parityFixture) {
	t.Helper()
	svc := parityServiceWithCohorts(t)
	ctx := context.Background()

	// Facade pass — the full Compose pipeline (per-slot Process +
	// applyComposeOverlays + distributeComposeWarnings).
	composedWithOverlay := parityComposed(fx)
	facadeResp, err := svc.Compose(ctx, composedWithOverlay)
	if err != nil {
		t.Fatalf("facade Compose(%s): %v", fx.kind, err)
	}
	facadeLayers := facadeResp.Overlays
	if len(facadeLayers) == 0 {
		t.Fatalf("facade Compose(%s): no overlays surfaced", fx.kind)
	}

	// Internal pass — same Compose pipeline, no overlay computation,
	// then drive processing.ApplyComposeOverlays directly against the
	// per-slot Responses the facade observed.
	composedWithoutOverlay := withoutOverlays(composedWithOverlay)
	baselineResp, err := svc.Compose(ctx, composedWithoutOverlay)
	if err != nil {
		t.Fatalf("baseline Compose(%s): %v", fx.kind, err)
	}
	if len(baselineResp.Overlays) != 0 {
		t.Fatalf("baseline Compose(%s): expected no overlays, got %d", fx.kind, len(baselineResp.Overlays))
	}
	// applyComposeLabelDefaults is the canonical normaliser the
	// service-layer hook uses to map slot Label → Index. Replicating it
	// here keeps the internal-pass label set byte-identical to the
	// facade pass.
	normalised, err := applyComposeLabelDefaults(composedWithoutOverlay)
	if err != nil {
		t.Fatalf("applyComposeLabelDefaults(%s): %v", fx.kind, err)
	}
	labels := make([]string, len(normalised))
	for i, r := range normalised {
		if r == nil {
			continue
		}
		labels[i] = r.Label
	}
	internalLayers, internalWarnings, err := processing.ApplyComposeOverlays(
		composedWithOverlay.Overlays,
		baselineResp.Responses,
		labels,
	)
	if err != nil {
		t.Fatalf("processing.ApplyComposeOverlays(%s): %v", fx.kind, err)
	}
	// Mirror the service-layer warning fold so the comparison is apples-
	// to-apples. distributeComposeWarnings mutates layers in place and
	// returns the same slice for chained-expression convenience.
	internalLayers = distributeComposeWarnings(internalLayers, internalWarnings)

	// Byte-equal marshal check. Both slices serialise via encoding/json
	// (json.Marshal sorts map keys deterministically, so any drift
	// surfaces as a byte-level diff).
	facadeBytes, err := json.Marshal(facadeLayers)
	if err != nil {
		t.Fatalf("json.Marshal facadeLayers(%s): %v", fx.kind, err)
	}
	internalBytes, err := json.Marshal(internalLayers)
	if err != nil {
		t.Fatalf("json.Marshal internalLayers(%s): %v", fx.kind, err)
	}
	if string(facadeBytes) != string(internalBytes) {
		t.Errorf("parity mismatch (%s):\n  facade   = %s\n  internal = %s", fx.kind, facadeBytes, internalBytes)
	}
}

// TestComposeOverlayParity_OVERLAY_INDEX_VS_REF locks the facade /
// internal byte-equal parity for the MATRIX arm of OVERLAY_INDEX_VS_REF.
func TestComposeOverlayParity_OVERLAY_INDEX_VS_REF(t *testing.T) {
	runComposeOverlayParity(t, parityFixture{
		kind:    types.OverlayKindIndexVsRef,
		scope:   types.OverlayScopeCell,
		targets: 1,
		shape:   "matrix",
	})
}

// TestComposeOverlayParity_OVERLAY_DELTA_VS_REF locks the parity for
// OVERLAY_DELTA_VS_REF (additive per-cell delta).
func TestComposeOverlayParity_OVERLAY_DELTA_VS_REF(t *testing.T) {
	runComposeOverlayParity(t, parityFixture{
		kind:    types.OverlayKindDeltaVsRef,
		scope:   types.OverlayScopeCell,
		targets: 1,
		shape:   "matrix",
	})
}

// TestComposeOverlayParity_OVERLAY_PROP_Z_CELL locks the parity for
// OVERLAY_PROP_Z_CELL. The fixture uses crosstab row margins so the
// per-cell two-proportion z reads non-degenerate sample sizes.
func TestComposeOverlayParity_OVERLAY_PROP_Z_CELL(t *testing.T) {
	runComposeOverlayParity(t, parityFixture{
		kind:    types.OverlayKindPropZCell,
		scope:   types.OverlayScopeCell,
		targets: 1,
		shape:   "matrix",
	})
}

// TestComposeOverlayParity_OVERLAY_PROP_Z_PANEL locks the parity for
// the multi-target single-layer PROP_Z_PANEL. The fixture carries 3
// treatment slots; the single emitted layer holds the pairwise z-panel
// matrix.
func TestComposeOverlayParity_OVERLAY_PROP_Z_PANEL(t *testing.T) {
	runComposeOverlayParity(t, parityFixture{
		kind:    types.OverlayKindPropZPanel,
		scope:   types.OverlayScopeCell,
		targets: 3,
		shape:   "matrix",
	})
}

// TestComposeOverlayParity_OVERLAY_T_CELL locks the parity for the
// per-cell Welch t-test variant.
func TestComposeOverlayParity_OVERLAY_T_CELL(t *testing.T) {
	runComposeOverlayParity(t, parityFixture{
		kind:    types.OverlayKindTCell,
		scope:   types.OverlayScopeCell,
		targets: 1,
		shape:   "matrix",
	})
}

// TestComposeOverlayParity_OVERLAY_T_VS_REF locks the parity for the
// SERIES-host per-group Welch t-test. The fixture uses grouped Process
// (no Crosstab) so the per-slot Response carries the canonical
// Response.Data series.
func TestComposeOverlayParity_OVERLAY_T_VS_REF(t *testing.T) {
	runComposeOverlayParity(t, parityFixture{
		kind:    types.OverlayKindTVsRef,
		scope:   types.OverlayScopeGroup,
		targets: 1,
		shape:   "series",
	})
}

// TestComposeOverlayParity_OVERLAY_Z_CELL locks the parity for the
// per-cell two-sample z-test variant.
func TestComposeOverlayParity_OVERLAY_Z_CELL(t *testing.T) {
	runComposeOverlayParity(t, parityFixture{
		kind:    types.OverlayKindZCell,
		scope:   types.OverlayScopeCell,
		targets: 1,
		shape:   "matrix",
	})
}

// TestComposeOverlayParity_OVERLAY_Z_VS_REF locks the parity for the
// SERIES-host per-group two-sample z-test.
func TestComposeOverlayParity_OVERLAY_Z_VS_REF(t *testing.T) {
	runComposeOverlayParity(t, parityFixture{
		kind:    types.OverlayKindZVsRef,
		scope:   types.OverlayScopeGroup,
		targets: 1,
		shape:   "series",
	})
}

// TestComposeOverlayParity_OVERLAY_CHISQ_VS_REF locks the parity for
// the whole-matrix χ² scalar-p test against a reference matrix.
func TestComposeOverlayParity_OVERLAY_CHISQ_VS_REF(t *testing.T) {
	runComposeOverlayParity(t, parityFixture{
		kind:    types.OverlayKindChiSqVsRef,
		scope:   types.OverlayScopeMatrix,
		targets: 1,
		shape:   "matrix",
	})
}

// TestComposeOverlayParity_OVERLAY_RANK locks the parity for the
// per-cell descending-rank overlay (Params.population = "matrix").
func TestComposeOverlayParity_OVERLAY_RANK(t *testing.T) {
	runComposeOverlayParity(t, parityFixture{
		kind:    types.OverlayKindRank,
		scope:   types.OverlayScopeCell,
		targets: 1,
		shape:   "matrix",
		params:  map[string]any{"population": "matrix"},
	})
}

// TestComposeOverlayParity_OVERLAY_PANEL_INDEX_VS_REF locks the parity
// for the multi-LAYER PANEL_INDEX_VS_REF kind. One spec → N target
// layers, each carrying the per-target INDEX_VS_REF math.
func TestComposeOverlayParity_OVERLAY_PANEL_INDEX_VS_REF(t *testing.T) {
	runComposeOverlayParity(t, parityFixture{
		kind:    types.OverlayKindPanelIndexVsRef,
		scope:   types.OverlayScopeCell,
		targets: 3,
		shape:   "matrix",
	})
}

// TestComposeOverlayParity_CoversAllComposeHostKinds is the coverage-
// check gate. Asserts that every entry in composeHostOverlayKinds has a
// matching TestComposeOverlayParity_<KIND> test registered in this
// package. A future-added COMPOSE-host kind without a parity sibling
// fails this test loudly, preventing silent drift.
//
// The check walks the package's test-symbol surface via the standard
// reflect / runtime introspection trick used in skill-coverage gates
// elsewhere in the tree: enumerate the locally-declared parity helpers
// by name and assert each expected name is present.
//
// Drift mode: when a new entry lands in composeHostOverlayKinds without
// the matching TestComposeOverlayParity_<KIND> function in this file,
// the assertion below fails with the canonical name of the missing
// gate so the author knows exactly what to add.
func TestComposeOverlayParity_CoversAllComposeHostKinds(t *testing.T) {
	// Build the set of expected test-function names from the canonical
	// kind list.
	expected := make(map[string]bool, len(composeHostOverlayKinds))
	for _, k := range composeHostOverlayKinds {
		expected["TestComposeOverlayParity_"+string(k)] = true
	}

	// Sanity gate: the canonical list itself must agree with the
	// composeHostOverlayKinds slice. Catches typos where a kind was
	// added to the slice but the constant value was wrong.
	for _, k := range composeHostOverlayKinds {
		name := string(k)
		if name == "" {
			t.Errorf("composeHostOverlayKinds carries an empty kind constant")
		}
	}

	// Enumerate the registered TestComposeOverlayParity_* functions by
	// reflection over the function-name set declared in this file. The
	// runtime package's FuncForPC trick lets us read every locally-
	// declared test function name without an init-time registry.
	// runtime.FuncForPC(reflect.ValueOf(fn).Pointer()).Name() returns
	// the canonical name; we collect names by iterating a table of
	// every parity test function below.
	parityTestFns := []func(*testing.T){
		TestComposeOverlayParity_OVERLAY_INDEX_VS_REF,
		TestComposeOverlayParity_OVERLAY_DELTA_VS_REF,
		TestComposeOverlayParity_OVERLAY_PROP_Z_CELL,
		TestComposeOverlayParity_OVERLAY_PROP_Z_PANEL,
		TestComposeOverlayParity_OVERLAY_T_CELL,
		TestComposeOverlayParity_OVERLAY_T_VS_REF,
		TestComposeOverlayParity_OVERLAY_Z_CELL,
		TestComposeOverlayParity_OVERLAY_Z_VS_REF,
		TestComposeOverlayParity_OVERLAY_CHISQ_VS_REF,
		TestComposeOverlayParity_OVERLAY_RANK,
		TestComposeOverlayParity_OVERLAY_PANEL_INDEX_VS_REF,
	}
	registered := make(map[string]bool, len(parityTestFns))
	for _, fn := range parityTestFns {
		pc := reflect.ValueOf(fn).Pointer()
		fullName := runtime.FuncForPC(pc).Name()
		// runtime.FuncForPC returns the fully-qualified name
		// "github.com/.../service.TestComposeOverlayParity_OVERLAY_X" —
		// strip everything up through the last dot to get the bare
		// function name.
		bare := fullName
		for i := len(fullName) - 1; i >= 0; i-- {
			if fullName[i] == '.' {
				bare = fullName[i+1:]
				break
			}
		}
		registered[bare] = true
	}

	// Missing-expected check: every entry in composeHostOverlayKinds
	// must have a matching registered parity test. Fail loudly so the
	// drift surface is unambiguous.
	missing := make([]string, 0)
	for name := range expected {
		if !registered[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("composeHostOverlayKinds drift: missing parity sibling tests:\n  %v\n\n"+
			"Add the following test function(s) to service/compose_overlay_parity_test.go "+
			"AND append the matching kind constant to composeHostOverlayKinds in the same PR.",
			missing)
	}

	// Extra-registered check: every registered parity test must
	// correspond to a kind in composeHostOverlayKinds (catches stale
	// tests for kinds that have been removed from the dispatch tables).
	extra := make([]string, 0)
	for name := range registered {
		if !expected[name] {
			extra = append(extra, name)
		}
	}
	if len(extra) > 0 {
		sort.Strings(extra)
		t.Fatalf("composeHostOverlayKinds drift: extra parity tests with no matching kind constant:\n  %v\n\n"+
			"Either add the kind constant to composeHostOverlayKinds OR delete the orphan parity test.",
			extra)
	}
}
