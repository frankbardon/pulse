package processing

import (
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// ---------------------------------------------------------------------
// E2-S5: trailing axis positions are driven for their liveBuckets side
// effect even when a position ahead of them left the axis unresolved.
//
// Buffered (Processor.axisComponentsFor) re-runs EACH axis position
// independently over the full filtered record set, so every position
// observes every record whose own field resolves regardless of what a
// different position did. Stopping the fused walk at the first miss
// undercounts every trailing position's
// Components.crosstab.{row,column}_key_components[].axes[].bucket.count.
// ---------------------------------------------------------------------

// fakeMetaSingleKeyGrouper is fakeSingleKeyGrouper that also implements
// MetaGrouper. The keyer's tail-drive gate is computed at construction
// from MetaGrouper presence, so the plain fakes (which emit no
// components, and therefore have no observable side effect) deliberately
// do NOT turn it on.
type fakeMetaSingleKeyGrouper struct{ fakeSingleKeyGrouper }

func (g *fakeMetaSingleKeyGrouper) Components() (map[string]any, error) {
	return map[string]any{"buckets": []map[string]any{}}, nil
}

// fakeMetaMultiKeyGrouper is fakeMultiKeyGrouper plus MetaGrouper.
type fakeMetaMultiKeyGrouper struct{ fakeMultiKeyGrouper }

func (g *fakeMetaMultiKeyGrouper) Components() (map[string]any, error) {
	return map[string]any{"buckets": []map[string]any{}}, nil
}

// TestFusedAxisKeyer_DrivesTrailingPositionsAfterUnresolved is the
// direct unit test of the new contract: the KEYING walk stops at the
// first unresolved position, but every position behind it is still
// called exactly once so its liveBuckets observes the record.
func TestFusedAxisKeyer_DrivesTrailingPositionsAfterUnresolved(t *testing.T) {
	missAt0 := &fakeMetaSingleKeyGrouper{fakeSingleKeyGrouper{null: true}}
	tail1 := &fakeMetaSingleKeyGrouper{fakeSingleKeyGrouper{key: "north"}}
	tail2 := &fakeMetaMultiKeyGrouper{fakeMultiKeyGrouper{keys: []string{"VISA", "MC"}, ok: true}}

	keyer := newFusedAxisKeyer([]fusedAxisGrouper{
		classifyFusedAxisGrouper(missAt0, "value"),
		classifyFusedAxisGrouper(tail1, "region"),
		classifyFusedAxisGrouper(tail2, "tags"),
	})

	const records = 4
	for i := 0; i < records; i++ {
		got, err := keyer.derive(dummyRecord())
		if err != nil {
			t.Fatalf("derive #%d: %v", i, err)
		}
		// The axis must still report unresolved, with no keys and no
		// tuples: a trailing position's key never contributes to the
		// composite key, the tuple, or any prefix level.
		if got.ok {
			t.Fatalf("derive #%d: axis reported resolved, want unresolved", i)
		}
		if got.depth != 0 {
			t.Fatalf("derive #%d: depth = %d, want 0 (miss at position 0)", i, got.depth)
		}
		if len(got.levels) != 0 {
			t.Fatalf("derive #%d: len(levels) = %d, want 0", i, len(got.levels))
		}
		if got.keys() != nil {
			t.Fatalf("derive #%d: keys() = %q, want nil", i, got.keys())
		}
		if got.tuples != nil {
			t.Fatalf("derive #%d: tuples = %#v, want nil", i, got.tuples)
		}
	}

	if missAt0.calls != records {
		t.Errorf("position 0 called %d times, want %d", missAt0.calls, records)
	}
	if tail1.calls != records {
		t.Errorf("trailing position 1 called %d times, want %d (once per record, "+
			"for its liveBuckets side effect)", tail1.calls, records)
	}
	if tail2.calls != records {
		t.Errorf("trailing position 2 called %d times, want %d (once per record, "+
			"for its liveBuckets side effect)", tail2.calls, records)
	}
}

// TestFusedAxisKeyer_TailDriveKeepsResolvedPrefix pins that a miss in
// the MIDDLE of an axis leaves the resolved prefix untouched: depth,
// levels and the prefix sets reflect only the positions that resolved
// BEFORE the miss, while the positions behind it are still driven.
func TestFusedAxisKeyer_TailDriveKeepsResolvedPrefix(t *testing.T) {
	lead := &fakeMetaMultiKeyGrouper{fakeMultiKeyGrouper{keys: []string{"VISA", "MC"}, ok: true}}
	miss := &fakeMetaSingleKeyGrouper{fakeSingleKeyGrouper{null: true}}
	tail := &fakeMetaSingleKeyGrouper{fakeSingleKeyGrouper{key: "north"}}

	keyer := newFusedAxisKeyer([]fusedAxisGrouper{
		classifyFusedAxisGrouper(lead, "tags"),
		classifyFusedAxisGrouper(miss, "value"),
		classifyFusedAxisGrouper(tail, "region"),
	})
	got, err := keyer.derive(dummyRecord())
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	if got.ok {
		t.Fatal("axis must not fully resolve")
	}
	if got.depth != 1 {
		t.Fatalf("depth = %d, want 1", got.depth)
	}
	if want := []string{"VISA", "MC"}; !reflect.DeepEqual(got.prefixes(0), want) {
		t.Errorf("prefixes(0) = %q, want %q", got.prefixes(0), want)
	}
	// The tail sits behind a 2-wide fan. Once per record, not once per
	// parent key — the once-per-record rule is not weakened by the drive.
	if tail.calls != 1 {
		t.Errorf("trailing position called %d times, want 1; a count of 2 means "+
			"the drive was folded into the product walk", tail.calls)
	}
}

// TestFusedAxisKeyer_TailDriveSkippedWithoutMetaGrouper pins the
// construction-time gate: when no position at index >= 1 implements
// MetaGrouper nothing can read liveBuckets, so the extra per-record
// calls are pure cost and are skipped. The gate is a property of the
// chain, computed once in newFusedAxisKeyer — never re-derived per
// record.
func TestFusedAxisKeyer_TailDriveSkippedWithoutMetaGrouper(t *testing.T) {
	missAt0 := &fakeSingleKeyGrouper{null: true}
	tail := &fakeSingleKeyGrouper{key: "north"}
	keyer := newFusedAxisKeyer([]fusedAxisGrouper{
		classifyFusedAxisGrouper(missAt0, "value"),
		classifyFusedAxisGrouper(tail, "region"),
	})
	if keyer.driveUnresolvedTail {
		t.Fatal("driveUnresolvedTail must be false when no trailing position emits components")
	}
	for i := 0; i < 3; i++ {
		if _, err := keyer.derive(dummyRecord()); err != nil {
			t.Fatalf("derive #%d: %v", i, err)
		}
	}
	if tail.calls != 0 {
		t.Errorf("trailing position called %d times, want 0 (no MetaGrouper on the axis)", tail.calls)
	}

	// One MetaGrouper anywhere behind position 0 turns the drive on.
	metaTail := &fakeMetaSingleKeyGrouper{fakeSingleKeyGrouper{key: "north"}}
	on := newFusedAxisKeyer([]fusedAxisGrouper{
		classifyFusedAxisGrouper(&fakeSingleKeyGrouper{null: true}, "value"),
		classifyFusedAxisGrouper(metaTail, "region"),
	})
	if !on.driveUnresolvedTail {
		t.Fatal("driveUnresolvedTail must be true when a trailing position emits components")
	}
	if _, err := on.derive(dummyRecord()); err != nil {
		t.Fatalf("derive: %v", err)
	}
	if metaTail.calls != 1 {
		t.Errorf("trailing MetaGrouper position called %d times, want 1", metaTail.calls)
	}
}

// TestFusedAxisKeyer_TailDriveErrorsPropagate pins the error semantics
// stated in derive's doc comment: a position driven purely for its side
// effect gets the same treatment as one driven for its key. A null
// signal is absorbed; any other error surfaces. Swallowing it would let
// a request that fails on the buffered path silently succeed on the
// fused one.
func TestFusedAxisKeyer_TailDriveErrorsPropagate(t *testing.T) {
	boom := errors.NewCodedError(errors.PROCESSING_CONFIG, "boom")
	cases := map[string][]fusedAxisGrouper{
		"single-key tail errors": {
			classifyFusedAxisGrouper(&fakeMetaSingleKeyGrouper{fakeSingleKeyGrouper{null: true}}, "value"),
			classifyFusedAxisGrouper(&fakeMetaSingleKeyGrouper{fakeSingleKeyGrouper{err: boom}}, "region"),
		},
		"multi-key tail errors": {
			classifyFusedAxisGrouper(&fakeMetaSingleKeyGrouper{fakeSingleKeyGrouper{null: true}}, "value"),
			classifyFusedAxisGrouper(&fakeMetaMultiKeyGrouper{fakeMultiKeyGrouper{err: boom}}, "tags"),
		},
		"error two positions behind the miss": {
			classifyFusedAxisGrouper(&fakeMetaSingleKeyGrouper{fakeSingleKeyGrouper{null: true}}, "value"),
			classifyFusedAxisGrouper(&fakeMetaSingleKeyGrouper{fakeSingleKeyGrouper{key: "north"}}, "region"),
			classifyFusedAxisGrouper(&fakeMetaSingleKeyGrouper{fakeSingleKeyGrouper{err: boom}}, "segment"),
		},
	}
	for name, entries := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := newFusedAxisKeyer(entries).derive(dummyRecord()); err == nil {
				t.Fatal("expected the trailing position's error to propagate")
			}
		})
	}
}

// TestFusedAxisKeyer_TailDriveAbsorbsNullFromTrailingPosition pins the
// other half of the error rule: a trailing position that is ITSELF
// unresolved is not an error, and does not change the axis result.
func TestFusedAxisKeyer_TailDriveAbsorbsNullFromTrailingPosition(t *testing.T) {
	entries := []fusedAxisGrouper{
		classifyFusedAxisGrouper(&fakeMetaSingleKeyGrouper{fakeSingleKeyGrouper{null: true}}, "value"),
		classifyFusedAxisGrouper(&fakeMetaSingleKeyGrouper{fakeSingleKeyGrouper{err: ErrGrouperKeyNull}}, "region"),
		classifyFusedAxisGrouper(&fakeMetaMultiKeyGrouper{fakeMultiKeyGrouper{ok: false}}, "tags"),
	}
	got, err := newFusedAxisKeyer(entries).derive(dummyRecord())
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	if got.ok || got.depth != 0 {
		t.Fatalf("axis = {ok:%v depth:%d}, want unresolved at depth 0", got.ok, got.depth)
	}
}

// ---------------------------------------------------------------------
// Oracle parity — the shape the bug actually reproduces in.
// ---------------------------------------------------------------------

// trailingMissRecords is the shared cohort for the parity cases: rows
// whose "value" is null leave a leading GROUP_RANGE position unresolved
// while their "region" / "tags" fields resolve perfectly well, so a
// trailing position on the same axis must still count them.
func trailingMissRecords(schema *encoding.Schema) []*Record {
	rows := []fanoutRow{
		{region: 0, tags: tagVISA | tagMC, chans: chWEB, value: 10},
		{region: 1, tags: tagAMEX, chans: chPOS, value: 20},
		// Null "value": the leading GROUP_RANGE position misses, but
		// region and tags both resolve and buffered counts them.
		{region: 0, tags: tagVISA | tagDISC, chans: chATM, nullValue: true},
		{region: 1, tags: tagMC, chans: chWEB, nullValue: true},
		// Null "value" AND null tags: neither position resolves.
		{region: 0, chans: chPOS, nullValue: true, nullTags: true},
		{region: 1, tags: tagDISC, chans: chWEB, value: 25},
	}
	out := make([]*Record, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.build(schema))
	}
	return out
}

// rangeGroup is a single-key GROUP_RANGE position over the nullable
// "value" field — the leading position that rejects records.
func rangeGroup(field string) *types.Group {
	return &types.Group{Type: types.GROUP_RANGE, Field: field, Interval: 10}
}

// TestFusedCrosstab_TrailingAxisPositionComponentsMatchBuffered is the
// E2-S5 headline gate. The first case carries NO fan-out at all — two
// ordinary single-key groupers — because that is the shape the bug
// reproduces in; the fanning cases follow to prove the fix composes with
// E2-S3's routing.
func TestFusedCrosstab_TrailingAxisPositionComponentsMatchBuffered(t *testing.T) {
	cases := []struct {
		name string
		spec *types.CrosstabSpec
	}{
		{
			// The reproducer: two single-key groupers, no fan-out
			// anywhere. GROUP_RANGE on the nullable "value" rejects the
			// null rows, so the trailing GROUP_CATEGORY position never
			// saw them before this fix and its bucket counts came up
			// short against buffered.
			name: "single-key trailing position behind a single-key miss",
			spec: &types.CrosstabSpec{
				Rows:    []*types.Group{rangeGroup("value"), catGroup("region")},
				Columns: []*types.Group{catGroup("region")},
				Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "region", Label: "n"},
				Shape:   types.CrosstabShapeMatrix,
				Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
			},
		},
		{
			// Same shape on the COLUMN axis, so
			// column_key_components[].axes[].bucket.count is covered too.
			name: "single-key trailing position on the column axis",
			spec: &types.CrosstabSpec{
				Rows:    []*types.Group{catGroup("region")},
				Columns: []*types.Group{rangeGroup("value"), catGroup("region")},
				Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "region", Label: "n"},
				Shape:   types.CrosstabShapeMatrix,
				Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
			},
		},
		{
			// A fanning position behind an unresolved leading position:
			// setPerElementGrouper folds one observation per selected
			// label, so a missed drive costs it several counts, not one.
			name: "fanning trailing position behind a single-key miss",
			spec: &types.CrosstabSpec{
				Rows:    []*types.Group{rangeGroup("value"), setGroup("tags")},
				Columns: []*types.Group{catGroup("region")},
				Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "value", Label: "sum"},
				Shape:   types.CrosstabShapeMatrix,
				Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
			},
		},
		{
			// Three positions with the miss in the middle: the resolved
			// prefix keeps keying, the two behind it keep counting.
			name: "miss in the middle of a three-position axis",
			spec: &types.CrosstabSpec{
				Rows:    []*types.Group{catGroup("region"), rangeGroup("value"), setGroup("tags")},
				Columns: []*types.Group{catGroup("region")},
				Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "region", Label: "n"},
				Shape:   types.CrosstabShapeMatrix,
				Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
			},
		},
		{
			// A fanning LEADING position that rejects rows (null set /
			// empty mask), with a single-key position behind it — the
			// mirror of case 3, and the shape E2-S3 had to route around.
			name: "single-key trailing position behind a fanning miss",
			spec: &types.CrosstabSpec{
				Rows:    []*types.Group{setGroup("tags"), catGroup("region")},
				Columns: []*types.Group{catGroup("region")},
				Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "value", Label: "sum"},
				Shape:   types.CrosstabShapeMatrix,
				Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
			},
		},
		{
			// Normalization reads the same axis components; keep the
			// denominators under the oracle too.
			name: "normalize_level with a trailing position behind a miss",
			spec: func() *types.CrosstabSpec {
				zero := 0
				return &types.CrosstabSpec{
					Rows:           []*types.Group{rangeGroup("value"), catGroup("region")},
					Columns:        []*types.Group{catGroup("region")},
					Cell:           &types.Aggregation{Type: types.AGG_COUNT, Field: "region", Label: "n"},
					Shape:          types.CrosstabShapeMatrix,
					Normalize:      types.CrosstabNormalizeRow,
					NormalizeLevel: &zero,
					Margins:        types.CrosstabMargins{Rows: true},
				}
			}(),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			schema := fanoutCrosstabSchema(t)
			assertFusedBufferedParity(t, schema,
				&types.Request{Crosstab: tc.spec}, trailingMissRecords(schema))
		})
	}
}

// TestFusedCrosstab_TrailingPositionBucketCountsAreNotUndercounted is
// the value-level companion to the parity gate above: it names the exact
// slot the bug moved and asserts the arithmetic directly, so a future
// regression that breaks BOTH paths the same way (and therefore keeps
// parity green) still fails here.
//
// Six records, all with a resolving "region", two of them with a null
// "value". The trailing GROUP_CATEGORY position must count all six
// across its buckets — three per region — not just the four the leading
// GROUP_RANGE position let through.
func TestFusedCrosstab_TrailingPositionBucketCountsAreNotUndercounted(t *testing.T) {
	schema := fanoutCrosstabSchema(t)
	req := &types.Request{Crosstab: &types.CrosstabSpec{
		Rows:    []*types.Group{rangeGroup("value"), catGroup("region")},
		Columns: []*types.Group{catGroup("region")},
		Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "region", Label: "n"},
		Shape:   types.CrosstabShapeMatrix,
		Margins: types.CrosstabMargins{Rows: true},
	}}
	resp, err := runFusedCrosstabViaRunner(t, schema, req, trailingMissRecords(schema), false)
	if err != nil {
		t.Fatalf("fused RunCrosstabFused: %v", err)
	}
	if resp.Components == nil || resp.Components.Crosstab == nil {
		t.Fatal("no crosstab components emitted")
	}
	// Every row key carries the composite {axes:[range bucket, region
	// bucket]} layout. Collect the trailing (region) position's counts
	// keyed by its bucket key; the count is cohort-wide per bucket, so
	// distinct row keys sharing a region repeat the same number.
	got := map[string]int{}
	for _, entry := range resp.Components.Crosstab.RowKeyComponents {
		axes, ok := entry["axes"].([]map[string]any)
		if !ok || len(axes) != 2 {
			t.Fatalf("row key components entry = %#v, want a two-axis composite", entry)
		}
		bucket, ok := axes[1]["bucket"].(map[string]any)
		if !ok {
			t.Fatalf("trailing axis entry = %#v, want a bucket map", axes[1])
		}
		key, _ := bucket["key"].(string)
		got[key] = int(coerceFloat64(bucket["count"]))
	}
	want := map[string]int{"north": 3, "south": 3}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("trailing-position bucket counts = %v, want %v "+
			"(the two null-\"value\" rows must still be counted by the region position)", got, want)
	}
}
