package processing

import (
	stderrors "errors"
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// ---------------------------------------------------------------------
// Test groupers.
//
// These fakes exist so the derivation can be exercised without a cohort
// and, crucially, so KeysForRow / KeyFor CALL COUNTS are observable —
// the once-per-record rule is otherwise untestable from the outside
// because its only visible symptom is inflated Components bucket counts
// several layers downstream.
// ---------------------------------------------------------------------

// fakeSingleKeyGrouper is a StreamableGrouper (one key per record) that
// counts derivation calls. It deliberately does NOT implement
// MultiKeyStreamingGrouper, so classifyFusedAxisGrouper resolves it to
// the single-key shape.
type fakeSingleKeyGrouper struct {
	key   string
	null  bool  // report the record as unresolved
	err   error // surface this error instead of a key
	calls int
}

func (g *fakeSingleKeyGrouper) Group([]*Record, string) (map[string][]*Record, error) {
	return map[string][]*Record{}, nil
}

func (g *fakeSingleKeyGrouper) KeyFor(*Record) (string, error) {
	g.calls++
	if g.err != nil {
		return "", g.err
	}
	if g.null {
		return "", ErrGrouperKeyNull
	}
	return g.key, nil
}

// fakeMultiKeyGrouper is a MultiKeyStreamingGrouper (N keys per record)
// that counts derivation calls.
type fakeMultiKeyGrouper struct {
	keys  []string
	ok    bool
	err   error
	calls int
	// seenField records the field name the derivation passed through to
	// KeysForRow — setPerElementGrouper is not field-bound, so passing
	// "" here would silently return nothing.
	seenField string
}

func (g *fakeMultiKeyGrouper) Group([]*Record, string) (map[string][]*Record, error) {
	return map[string][]*Record{}, nil
}

func (g *fakeMultiKeyGrouper) KeysForRow(_ *Record, field string) ([]string, bool, error) {
	g.calls++
	g.seenField = field
	if g.err != nil {
		return nil, false, g.err
	}
	return g.keys, g.ok, nil
}

func multiFake(field string, keys ...string) (*fakeMultiKeyGrouper, fusedAxisGrouper) {
	g := &fakeMultiKeyGrouper{keys: keys, ok: len(keys) > 0}
	return g, classifyFusedAxisGrouper(g, field)
}

func singleFake(field, key string) (*fakeSingleKeyGrouper, fusedAxisGrouper) {
	g := &fakeSingleKeyGrouper{key: key}
	return g, classifyFusedAxisGrouper(g, field)
}

// joinKey composes a composite axis key the same way the derivation and
// the buffered PartitionByAxis recursion do.
func joinKey(parts ...string) string { return strings.Join(parts, crosstabAxisKeySep) }

// dummyRecord is a record the fakes never read. Derivation must not
// depend on record contents when the grouper does not.
func dummyRecord() *Record { return NewRecord(nil, map[string]float64{}) }

// ---------------------------------------------------------------------
// Single-key axes — the byte-identity floor.
// ---------------------------------------------------------------------

// TestFusedAxisKeyer_SingleKeyAxis pins that an axis with no fan-out
// position resolves to exactly one composite key at every depth, with
// the same tuple the pre-fan-out implementation produced. This is the
// shape every existing fused test exercises; the fan-out widening must
// leave it untouched.
func TestFusedAxisKeyer_SingleKeyAxis(t *testing.T) {
	cases := []struct {
		name         string
		keys         []string
		wantFull     string
		wantTuple    types.AxisKey
		wantPrefixes []string
	}{
		{
			name:         "one position",
			keys:         []string{"north"},
			wantFull:     "north",
			wantTuple:    types.AxisKey{"north"},
			wantPrefixes: []string{"north"},
		},
		{
			name:         "two positions",
			keys:         []string{"north", "VISA"},
			wantFull:     joinKey("north", "VISA"),
			wantTuple:    types.AxisKey{"north", "VISA"},
			wantPrefixes: []string{"north", joinKey("north", "VISA")},
		},
		{
			name:         "three positions",
			keys:         []string{"a", "b", "c"},
			wantFull:     joinKey("a", "b", "c"),
			wantTuple:    types.AxisKey{"a", "b", "c"},
			wantPrefixes: []string{"a", joinKey("a", "b"), joinKey("a", "b", "c")},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			entries := make([]fusedAxisGrouper, 0, len(tc.keys))
			for _, k := range tc.keys {
				_, e := singleFake("f", k)
				entries = append(entries, e)
			}
			got, err := newFusedAxisKeyer(entries).derive(dummyRecord())
			if err != nil {
				t.Fatalf("derive: %v", err)
			}
			if !got.ok {
				t.Fatalf("axis must fully resolve, got ok=false depth=%d", got.depth)
			}
			if got.depth != len(tc.keys) {
				t.Fatalf("depth = %d, want %d", got.depth, len(tc.keys))
			}
			if want := []string{tc.wantFull}; !reflect.DeepEqual(got.keys(), want) {
				t.Errorf("keys() = %q, want %q", got.keys(), want)
			}
			if len(got.tuples) != 1 || !reflect.DeepEqual(got.tuples[0], tc.wantTuple) {
				t.Errorf("tuples = %#v, want [%#v]", got.tuples, tc.wantTuple)
			}
			for d, want := range tc.wantPrefixes {
				if p := got.prefixes(d); !reflect.DeepEqual(p, []string{want}) {
					t.Errorf("prefixes(%d) = %q, want [%q]", d, p, want)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------
// Fan-out axes — the cartesian product.
// ---------------------------------------------------------------------

// TestFusedAxisKeyer_CartesianProduct pins the widened contract: an axis
// carrying a MultiKeyStreamingGrouper at ANY position yields the
// cartesian product of every position's key set, in position order, with
// the component order buffered PartitionByAxis produces (parent-major).
func TestFusedAxisKeyer_CartesianProduct(t *testing.T) {
	cases := []struct {
		name         string
		build        func() []fusedAxisGrouper
		wantKeys     []string
		wantTuples   []types.AxisKey
		wantPrefixes [][]string
	}{
		{
			name: "single multi-key position, one label",
			build: func() []fusedAxisGrouper {
				_, m := multiFake("tags", "VISA")
				return []fusedAxisGrouper{m}
			},
			wantKeys:     []string{"VISA"},
			wantTuples:   []types.AxisKey{{"VISA"}},
			wantPrefixes: [][]string{{"VISA"}},
		},
		{
			name: "single multi-key position, three labels",
			build: func() []fusedAxisGrouper {
				_, m := multiFake("tags", "VISA", "MC", "AMEX")
				return []fusedAxisGrouper{m}
			},
			wantKeys:   []string{"VISA", "MC", "AMEX"},
			wantTuples: []types.AxisKey{{"VISA"}, {"MC"}, {"AMEX"}},
			wantPrefixes: [][]string{
				{"VISA", "MC", "AMEX"},
			},
		},
		{
			name: "multi-key at position 0, single-key behind it",
			build: func() []fusedAxisGrouper {
				_, m := multiFake("tags", "VISA", "MC")
				_, s := singleFake("region", "north")
				return []fusedAxisGrouper{m, s}
			},
			wantKeys: []string{
				joinKey("VISA", "north"),
				joinKey("MC", "north"),
			},
			wantTuples: []types.AxisKey{{"VISA", "north"}, {"MC", "north"}},
			wantPrefixes: [][]string{
				{"VISA", "MC"},
				{joinKey("VISA", "north"), joinKey("MC", "north")},
			},
		},
		{
			name: "single-key at position 0, multi-key at position 1",
			build: func() []fusedAxisGrouper {
				_, s := singleFake("region", "north")
				_, m := multiFake("tags", "VISA", "MC", "AMEX")
				return []fusedAxisGrouper{s, m}
			},
			wantKeys: []string{
				joinKey("north", "VISA"),
				joinKey("north", "MC"),
				joinKey("north", "AMEX"),
			},
			wantTuples: []types.AxisKey{
				{"north", "VISA"}, {"north", "MC"}, {"north", "AMEX"},
			},
			wantPrefixes: [][]string{
				// The depth-0 prefix set does NOT fan: one record has one
				// region. Deriving prefixes by truncating the full keys
				// would produce three copies of it.
				{"north"},
				{
					joinKey("north", "VISA"),
					joinKey("north", "MC"),
					joinKey("north", "AMEX"),
				},
			},
		},
		{
			name: "two multi-key positions on one axis",
			build: func() []fusedAxisGrouper {
				_, a := multiFake("tags", "VISA", "MC")
				_, b := multiFake("channels", "web", "app", "store")
				return []fusedAxisGrouper{a, b}
			},
			wantKeys: []string{
				joinKey("VISA", "web"), joinKey("VISA", "app"), joinKey("VISA", "store"),
				joinKey("MC", "web"), joinKey("MC", "app"), joinKey("MC", "store"),
			},
			wantTuples: []types.AxisKey{
				{"VISA", "web"}, {"VISA", "app"}, {"VISA", "store"},
				{"MC", "web"}, {"MC", "app"}, {"MC", "store"},
			},
			wantPrefixes: [][]string{
				{"VISA", "MC"},
				{
					joinKey("VISA", "web"), joinKey("VISA", "app"), joinKey("VISA", "store"),
					joinKey("MC", "web"), joinKey("MC", "app"), joinKey("MC", "store"),
				},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := newFusedAxisKeyer(tc.build()).derive(dummyRecord())
			if err != nil {
				t.Fatalf("derive: %v", err)
			}
			if !got.ok {
				t.Fatalf("axis must fully resolve, got ok=false depth=%d", got.depth)
			}
			if !reflect.DeepEqual(got.keys(), tc.wantKeys) {
				t.Errorf("keys() = %q, want %q", got.keys(), tc.wantKeys)
			}
			if !reflect.DeepEqual(got.tuples, tc.wantTuples) {
				t.Errorf("tuples = %#v, want %#v", got.tuples, tc.wantTuples)
			}
			for d, want := range tc.wantPrefixes {
				if p := got.prefixes(d); !reflect.DeepEqual(p, want) {
					t.Errorf("prefixes(%d) = %q, want %q", d, p, want)
				}
			}
		})
	}
}

// TestFusedAxisKeyer_PassesBoundFieldToMultiKey guards the one argument
// that has no compile-time protection: MultiKeyStreamingGrouper.KeysForRow
// takes the field name because setPerElementGrouper is not field-bound.
// streamableKeyForRow passes "" for the single-key shape (those
// instances bind their field at factory time) and copying that into the
// multi path would make every fan-out axis silently resolve to nothing.
func TestFusedAxisKeyer_PassesBoundFieldToMultiKey(t *testing.T) {
	g, entry := multiFake("tags", "VISA")
	if _, err := newFusedAxisKeyer([]fusedAxisGrouper{entry}).derive(dummyRecord()); err != nil {
		t.Fatalf("derive: %v", err)
	}
	if g.seenField != "tags" {
		t.Fatalf("KeysForRow saw field %q, want %q", g.seenField, "tags")
	}
}

// ---------------------------------------------------------------------
// The once-per-record rule.
// ---------------------------------------------------------------------

// TestFusedAxisKeyer_DerivesEachPositionExactlyOnce is the direct test of
// the sharpest correctness constraint in this epic. KeysForRow has a side
// effect (setPerElementGrouper folds one observation per label into
// liveBuckets, which MetaGrouper.Components emits at Finalize), and
// buffered builds those counts by running each axis position once over
// the full filtered set. Deriving keys inside the product walk would
// call a trailing position once per parent key and inflate its bucket
// counts by the width of the fan in front of it — silently, because the
// cell values would stay correct.
func TestFusedAxisKeyer_DerivesEachPositionExactlyOnce(t *testing.T) {
	// Position 1 sits behind a 3-wide fan and position 2 behind a 12-wide
	// one, so a product-walk derivation would show calls of 1 / 3 / 12
	// rather than 1 / 1 / 1.
	m0 := &fakeMultiKeyGrouper{keys: []string{"a", "b", "c"}, ok: true}
	m1 := &fakeMultiKeyGrouper{keys: []string{"w", "x", "y", "z"}, ok: true}
	s2 := &fakeSingleKeyGrouper{key: "leaf"}
	entries := []fusedAxisGrouper{
		classifyFusedAxisGrouper(m0, "tags"),
		classifyFusedAxisGrouper(m1, "channels"),
		classifyFusedAxisGrouper(s2, "region"),
	}
	keyer := newFusedAxisKeyer(entries)

	const records = 5
	for i := 0; i < records; i++ {
		got, err := keyer.derive(dummyRecord())
		if err != nil {
			t.Fatalf("derive #%d: %v", i, err)
		}
		if n := len(got.keys()); n != 12 {
			t.Fatalf("derive #%d produced %d composite keys, want 12", i, n)
		}
	}

	if m0.calls != records {
		t.Errorf("position 0 derived %d times, want %d (once per record)", m0.calls, records)
	}
	if m1.calls != records {
		t.Errorf("position 1 derived %d times, want %d (once per record); "+
			"a count of %d means keys were derived inside the product walk",
			m1.calls, records, records*len(m0.keys))
	}
	if s2.calls != records {
		t.Errorf("position 2 derived %d times, want %d (once per record); "+
			"a count of %d means keys were derived inside the product walk",
			s2.calls, records, records*len(m0.keys)*len(m1.keys))
	}
}

// TestFusedAxisKeyer_ScratchReuseAcrossRecords pins that consecutive
// derivations on one keyer do not bleed into each other — the scratch is
// reused by design, so a stale tail from a wider previous record would be
// an easy and silent bug.
func TestFusedAxisKeyer_ScratchReuseAcrossRecords(t *testing.T) {
	g := &fakeMultiKeyGrouper{keys: []string{"a", "b", "c"}, ok: true}
	_, s := singleFake("region", "north")
	keyer := newFusedAxisKeyer([]fusedAxisGrouper{
		classifyFusedAxisGrouper(g, "tags"), s,
	})

	wide, err := keyer.derive(dummyRecord())
	if err != nil {
		t.Fatalf("derive wide: %v", err)
	}
	if len(wide.keys()) != 3 {
		t.Fatalf("wide record produced %d keys, want 3", len(wide.keys()))
	}

	g.keys = []string{"z"}
	narrow, err := keyer.derive(dummyRecord())
	if err != nil {
		t.Fatalf("derive narrow: %v", err)
	}
	if want := []string{joinKey("z", "north")}; !reflect.DeepEqual(narrow.keys(), want) {
		t.Errorf("narrow keys() = %q, want %q", narrow.keys(), want)
	}
	if want := []string{"z"}; !reflect.DeepEqual(narrow.prefixes(0), want) {
		t.Errorf("narrow prefixes(0) = %q, want %q", narrow.prefixes(0), want)
	}
	if !reflect.DeepEqual(narrow.tuples, []types.AxisKey{{"z", "north"}}) {
		t.Errorf("narrow tuples = %#v, want [[z north]]", narrow.tuples)
	}
}

// TestFusedAxisKeyer_RowAndColumnScratchAreIndependent pins the reason
// FusedCrosstabState carries two keyers: Update holds the row result
// while it derives the column result, and a fusedAxisKeys value aliases
// its producing keyer's scratch.
func TestFusedAxisKeyer_RowAndColumnScratchAreIndependent(t *testing.T) {
	_, rowEntry := multiFake("tags", "VISA", "MC")
	_, colEntry := multiFake("channels", "web", "app", "store")
	rowKeyer := newFusedAxisKeyer([]fusedAxisGrouper{rowEntry})
	colKeyer := newFusedAxisKeyer([]fusedAxisGrouper{colEntry})

	rowKeys, err := rowKeyer.derive(dummyRecord())
	if err != nil {
		t.Fatalf("derive rows: %v", err)
	}
	if _, err := colKeyer.derive(dummyRecord()); err != nil {
		t.Fatalf("derive columns: %v", err)
	}
	if want := []string{"VISA", "MC"}; !reflect.DeepEqual(rowKeys.keys(), want) {
		t.Fatalf("row keys clobbered by the column derivation: got %q, want %q",
			rowKeys.keys(), want)
	}
}

// TestFusedAxisKeyer_SingleKeyFastPathAllocation guards the hot-path
// budget for the overwhelmingly common shape: one single-key position.
// The pre-fan-out derivation allocated 3 objects / 48 B per record (an
// AxisKey, a partial-key slice, and the interned key); warm scratch on
// the keyer removes one of them, so the fan-out widening must not cost
// more than 2. A regression here means a scratch buffer stopped being
// reused across records.
func TestFusedAxisKeyer_SingleKeyFastPathAllocation(t *testing.T) {
	_, entry := singleFake("region", "north")
	keyer := newFusedAxisKeyer([]fusedAxisGrouper{entry})
	rec := dummyRecord()
	if _, err := keyer.derive(rec); err != nil {
		t.Fatalf("warmup derive: %v", err)
	}
	allocs := testing.AllocsPerRun(200, func() {
		if _, err := keyer.derive(rec); err != nil {
			t.Fatalf("derive: %v", err)
		}
	})
	if allocs > 2 {
		t.Errorf("single-key single-position derive allocated %.0f times per record, want <= 2", allocs)
	}
}

// ---------------------------------------------------------------------
// Null / unresolved semantics.
// ---------------------------------------------------------------------

// TestFusedAxisKeyer_UnresolvedPosition pins the per-position null
// contract across both keying shapes: the axis stops at the first
// unresolved position, ok goes false, and the prefix levels that DID
// resolve are still returned so the partial-depth and cross-axis margin
// consumers keep their inputs.
func TestFusedAxisKeyer_UnresolvedPosition(t *testing.T) {
	cases := []struct {
		name         string
		build        func() []fusedAxisGrouper
		wantDepth    int
		wantPrefixes [][]string
	}{
		{
			name: "single-key null at position 0",
			build: func() []fusedAxisGrouper {
				return []fusedAxisGrouper{
					classifyFusedAxisGrouper(&fakeSingleKeyGrouper{null: true}, "region"),
					mustSingle("tags", "VISA"),
				}
			},
			wantDepth: 0,
		},
		{
			name: "single-key null at position 1",
			build: func() []fusedAxisGrouper {
				return []fusedAxisGrouper{
					mustSingle("region", "north"),
					classifyFusedAxisGrouper(&fakeSingleKeyGrouper{null: true}, "tags"),
				}
			},
			wantDepth:    1,
			wantPrefixes: [][]string{{"north"}},
		},
		{
			name: "multi-key unresolved at position 0 collapses the axis",
			build: func() []fusedAxisGrouper {
				return []fusedAxisGrouper{
					classifyFusedAxisGrouper(&fakeMultiKeyGrouper{ok: false}, "tags"),
					mustSingle("region", "north"),
				}
			},
			wantDepth: 0,
		},
		{
			name: "multi-key unresolved behind a fan collapses the axis",
			build: func() []fusedAxisGrouper {
				return []fusedAxisGrouper{
					mustMulti("tags", "VISA", "MC"),
					classifyFusedAxisGrouper(&fakeMultiKeyGrouper{ok: false}, "channels"),
				}
			},
			wantDepth:    1,
			wantPrefixes: [][]string{{"VISA", "MC"}},
		},
		{
			name: "multi-key ok=true with an empty key slice is unresolved",
			build: func() []fusedAxisGrouper {
				return []fusedAxisGrouper{
					classifyFusedAxisGrouper(&fakeMultiKeyGrouper{keys: []string{}, ok: true}, "tags"),
				}
			},
			wantDepth: 0,
		},
		{
			name: "ErrGrouperKeyNull is normalised to unresolved",
			build: func() []fusedAxisGrouper {
				return []fusedAxisGrouper{
					mustMulti("tags", "VISA"),
					classifyFusedAxisGrouper(&fakeMultiKeyGrouper{err: ErrGrouperKeyNull}, "channels"),
				}
			},
			wantDepth:    1,
			wantPrefixes: [][]string{{"VISA"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := newFusedAxisKeyer(tc.build()).derive(dummyRecord())
			if err != nil {
				t.Fatalf("derive: %v", err)
			}
			if got.ok {
				t.Fatal("axis must not fully resolve")
			}
			if got.keys() != nil {
				t.Errorf("keys() = %q on an unresolved axis, want nil", got.keys())
			}
			if got.tuples != nil {
				t.Errorf("tuples = %#v on an unresolved axis, want nil", got.tuples)
			}
			if got.depth != tc.wantDepth {
				t.Fatalf("depth = %d, want %d", got.depth, tc.wantDepth)
			}
			if len(got.levels) != tc.wantDepth {
				t.Fatalf("len(levels) = %d, want %d", len(got.levels), tc.wantDepth)
			}
			for d, want := range tc.wantPrefixes {
				if p := got.prefixes(d); !reflect.DeepEqual(p, want) {
					t.Errorf("prefixes(%d) = %q, want %q", d, p, want)
				}
			}
		})
	}
}

// TestFusedAxisKeyer_ErrorsPropagate pins that a real grouper error is
// surfaced rather than folded into "unresolved" — only ErrGrouperKeyNull
// is a null signal.
func TestFusedAxisKeyer_ErrorsPropagate(t *testing.T) {
	boom := errors.NewCodedError(errors.PROCESSING_CONFIG, "boom")
	cases := map[string][]fusedAxisGrouper{
		"single-key": {classifyFusedAxisGrouper(&fakeSingleKeyGrouper{err: boom}, "region")},
		"multi-key":  {classifyFusedAxisGrouper(&fakeMultiKeyGrouper{err: boom}, "tags")},
		"behind a fan": {
			mustMulti("tags", "VISA", "MC"),
			classifyFusedAxisGrouper(&fakeSingleKeyGrouper{err: boom}, "region"),
		},
	}
	for name, entries := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := newFusedAxisKeyer(entries).derive(dummyRecord()); err == nil {
				t.Fatal("expected the grouper error to propagate")
			}
		})
	}
}

// TestFusedAxisKeyer_ZeroGroupersIsTyped keeps the defensive guard from
// the single-key predecessor: validateCrosstabSpec rejects a
// zero-grouper axis, so reaching derivation with one is a typed internal
// error rather than a silent empty placement.
func TestFusedAxisKeyer_ZeroGroupersIsTyped(t *testing.T) {
	_, err := newFusedAxisKeyer(nil).derive(dummyRecord())
	if err == nil {
		t.Fatal("expected an error for a zero-grouper axis")
	}
	var coded *errors.CodedError
	if !stderrors.As(err, &coded) || coded.Code != errors.PROCESSING_INTERNAL {
		t.Fatalf("err = %v, want PROCESSING_INTERNAL CodedError", err)
	}
}

// TestFusedAxisGrouper_UnkeyableEntryIsTyped replaces the E2-S1 landmine:
// an axis entry with neither keying shape resolved is a typed error, not
// a nil-interface panic. buildStreamableAxis rejects that shape at
// construction, so this only fires under contract drift.
func TestFusedAxisGrouper_UnkeyableEntryIsTyped(t *testing.T) {
	entry := fusedAxisGrouper{field: "score"}
	_, _, err := entry.keysForRow(dummyRecord(), nil)
	if err == nil {
		t.Fatal("expected a typed error for an unkeyable axis entry")
	}
	var coded *errors.CodedError
	if !stderrors.As(err, &coded) || coded.Code != errors.PROCESSING_INTERNAL {
		t.Fatalf("err = %v, want PROCESSING_INTERNAL CodedError", err)
	}
	if strings.Contains(coded.Message, "not implemented") {
		t.Errorf("the E2-S1 not-implemented landmine is still in place: %q", coded.Message)
	}
}

// ---------------------------------------------------------------------
// Dedup.
// ---------------------------------------------------------------------

// TestFusedAxisKeyer_DedupesPositionKeys pins that a position returning a
// repeated key does not multiply the product. Every prefix level and the
// full key set must stay duplicate-free, because E2-S3 routes one margin
// update per deduped prefix.
func TestFusedAxisKeyer_DedupesPositionKeys(t *testing.T) {
	_, s := singleFake("region", "north")
	entries := []fusedAxisGrouper{
		classifyFusedAxisGrouper(
			&fakeMultiKeyGrouper{keys: []string{"VISA", "MC", "VISA", "MC", "AMEX"}, ok: true}, "tags"),
		s,
	}
	got, err := newFusedAxisKeyer(entries).derive(dummyRecord())
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	if want := []string{"VISA", "MC", "AMEX"}; !reflect.DeepEqual(got.prefixes(0), want) {
		t.Errorf("prefixes(0) = %q, want %q", got.prefixes(0), want)
	}
	want := []string{
		joinKey("VISA", "north"), joinKey("MC", "north"), joinKey("AMEX", "north"),
	}
	if !reflect.DeepEqual(got.keys(), want) {
		t.Errorf("keys() = %q, want %q", got.keys(), want)
	}
}

// ---------------------------------------------------------------------
// Real GROUP_SET_PER_ELEMENT wiring.
// ---------------------------------------------------------------------

// TestFusedAxisKeyer_SetPerElementAxis drives the derivation through the
// real fan-out grouper — built by the real buildStreamableAxis, against
// the real set-typed schema — so the fakes above cannot drift from the
// only built-in MultiKeyStreamingGrouper. Covers a single-label set, a
// multi-label set, an empty mask, a null field, and a set that empties
// after Include filtering.
func TestFusedAxisKeyer_SetPerElementAxis(t *testing.T) {
	schema := crosstabFusedGateSchema()
	// Dictionary order is VISA, MC, AMEX, DISC — bit i maps to entry i.
	const (
		visa = 1 << 0
		mc   = 1 << 1
		amex = 1 << 2
	)
	cases := []struct {
		name     string
		axis     []*types.Group
		mask     uint64
		null     bool
		wantOk   bool
		wantKeys []string
	}{
		{
			name:     "one selected label",
			axis:     []*types.Group{setPerElementGroup()},
			mask:     visa,
			wantOk:   true,
			wantKeys: []string{"VISA"},
		},
		{
			name:     "three selected labels fan out",
			axis:     []*types.Group{setPerElementGroup()},
			mask:     visa | mc | amex,
			wantOk:   true,
			wantKeys: []string{"VISA", "MC", "AMEX"},
		},
		{
			name:   "empty mask resolves to no bucket",
			axis:   []*types.Group{setPerElementGroup()},
			mask:   0,
			wantOk: false,
		},
		{
			name:   "null set field resolves to no bucket",
			axis:   []*types.Group{setPerElementGroup()},
			null:   true,
			wantOk: false,
		},
		{
			name: "empty after Include filtering collapses the axis",
			axis: []*types.Group{
				{Type: types.GROUP_SET_PER_ELEMENT, Field: "tags", Include: []string{"DISC"}},
			},
			mask:   visa | mc,
			wantOk: false,
		},
		{
			name: "Include keeps the surviving labels only",
			axis: []*types.Group{
				{Type: types.GROUP_SET_PER_ELEMENT, Field: "tags", Include: []string{"MC", "AMEX"}},
			},
			mask:     visa | mc | amex,
			wantOk:   true,
			wantKeys: []string{"MC", "AMEX"},
		},
		{
			name: "fan-out behind a single-key position",
			axis: []*types.Group{
				{Type: types.GROUP_CATEGORY, Field: "region"},
				setPerElementGroup(),
			},
			mask:   visa | mc,
			wantOk: true,
			wantKeys: []string{
				joinKey("0", "VISA"), joinKey("0", "MC"),
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			entries, err := buildStreamableAxis(tc.axis, schema, nil, "rows")
			if err != nil {
				t.Fatalf("buildStreamableAxis: %v", err)
			}
			rec := setAxisRecord(schema, tc.mask, tc.null)
			got, derr := newFusedAxisKeyer(entries).derive(rec)
			if derr != nil {
				t.Fatalf("derive: %v", derr)
			}
			if got.ok != tc.wantOk {
				t.Fatalf("ok = %v, want %v (depth %d)", got.ok, tc.wantOk, got.depth)
			}
			if !tc.wantOk {
				return
			}
			if !reflect.DeepEqual(got.keys(), tc.wantKeys) {
				t.Errorf("keys() = %q, want %q", got.keys(), tc.wantKeys)
			}
			if len(got.tuples) != len(tc.wantKeys) {
				t.Errorf("len(tuples) = %d, want %d", len(got.tuples), len(tc.wantKeys))
			}
		})
	}
}

// TestFusedAxisKeyer_SetPerElementComponentsCountOncePerLabel is the
// end-to-end expression of the once-per-record rule against the real
// grouper: liveBuckets must count each record once per selected label,
// cohort-wide — exactly what buffered Processor.axisComponentsFor
// produces by re-running the grouper over the full filtered set. A
// product-walk derivation would multiply these counts.
func TestFusedAxisKeyer_SetPerElementComponentsCountOncePerLabel(t *testing.T) {
	schema := crosstabFusedGateSchema()
	const (
		visa = 1 << 0
		mc   = 1 << 1
	)
	// A trailing single-key position exists so a product-walk derivation
	// would still be observable; the fan sits in FRONT of it.
	entries, err := buildStreamableAxis([]*types.Group{
		setPerElementGroup(),
		{Type: types.GROUP_CATEGORY, Field: "region"},
	}, schema, nil, "rows")
	if err != nil {
		t.Fatalf("buildStreamableAxis: %v", err)
	}
	keyer := newFusedAxisKeyer(entries)

	// Three records, each selecting VISA and MC.
	for i := 0; i < 3; i++ {
		if _, err := keyer.derive(setAxisRecord(schema, visa|mc, false)); err != nil {
			t.Fatalf("derive #%d: %v", i, err)
		}
	}

	meta, ok := entries[0].grouper.(MetaGrouper)
	if !ok {
		t.Fatal("GROUP_SET_PER_ELEMENT must implement MetaGrouper")
	}
	comps, err := meta.Components()
	if err != nil {
		t.Fatalf("Components: %v", err)
	}
	if got := comps["total_label_observations"]; got != 6 {
		t.Errorf("total_label_observations = %v, want 6 (3 records x 2 labels)", got)
	}
	buckets, _ := comps["buckets"].([]map[string]any)
	if len(buckets) != 2 {
		t.Fatalf("buckets = %#v, want 2 entries", buckets)
	}
	for _, b := range buckets {
		if b["count"] != 3 {
			t.Errorf("bucket %v count = %v, want 3 (one per record, not one per composite key)",
				b["label"], b["count"])
		}
	}
}

// ---------------------------------------------------------------------
// Helpers.
// ---------------------------------------------------------------------

func mustSingle(field, key string) fusedAxisGrouper {
	_, e := singleFake(field, key)
	return e
}

func mustMulti(field string, keys ...string) fusedAxisGrouper {
	_, e := multiFake(field, keys...)
	return e
}

// setAxisRecord builds a record carrying the "tags" set mask (and a
// region categorical at index 0) for the fused gate schema.
func setAxisRecord(schema *encoding.Schema, mask uint64, null bool) *Record {
	nulls := map[string]bool{}
	if null {
		nulls["tags"] = true
	}
	return NewRecordWithWide(schema,
		map[string]float64{"region": 0},
		nulls,
		map[string]any{"tags": mask})
}
