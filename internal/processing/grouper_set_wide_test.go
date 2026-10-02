package processing

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// E2-S3 — set groupers on a wide column.
//
// GROUP_SET_VALUE uses the WHOLE mask as a group key, so the key
// derivation is the one place in the set surface where a missing word
// is a silent wrong answer rather than a crash: two masks differing
// only above bit 64 would fold into one bucket and the response would
// still render. Every test here therefore anchors to keys derived
// independently from the bit indices under test, never to a second
// execution path (a buffered-vs-streaming diff shares any decode bug
// and passes vacuously).

// gswLabel is the dictionary label for bit i. Zero-padded so
// lexicographic order — which GROUP_SET_VALUE's sort.Strings key join
// uses — matches bit order, making the expected key strings readable.
func gswLabel(i int) string { return fmt.Sprintf("L%03d", i) }

// gswSchema builds a one-set-field schema at the given rung with n
// dictionary members.
func gswSchema(t testing.TB, ft encoding.FieldType, n int) *encoding.Schema {
	t.Helper()
	dict := encoding.NewDictionary()
	for i := 0; i < n; i++ {
		if _, err := dict.Add(gswLabel(i)); err != nil {
			t.Fatalf("dict.Add(%d): %v", i, err)
		}
	}
	return &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "tags", Type: ft, ByteOffset: 0, CsvColumnIdx: 0, Dictionary: dict, Nullable: true},
		},
	}
}

// gswMask builds a SetMask with exactly the named bits set.
func gswMask(bits ...int) encoding.SetMask {
	var m encoding.SetMask
	for _, b := range bits {
		m = m.WithBit(b)
	}
	return m
}

// gswRecord builds a Record carrying mask on "tags". Wide rungs store an
// encoding.SetMask; narrow rungs keep their historical uint64 storage,
// which is what the decoder produces, so the test exercises the same
// shape the runtime sees.
func gswRecord(schema *encoding.Schema, m encoding.SetMask) *Record {
	var v any = m
	if !schema.Fields[0].Type.IsWideSet() {
		low, _ := m.Uint64()
		v = low
	}
	return NewRecordWithWide(schema, map[string]float64{}, nil, map[string]any{"tags": v})
}

// gswExpectedKey derives the GROUP_SET_VALUE bucket key for a set of
// bits WITHOUT calling the grouper: sorted, pipe-joined dictionary
// labels. This is the generated truth every key assertion compares
// against.
func gswExpectedKey(bits ...int) string {
	labels := make([]string, 0, len(bits))
	for _, b := range bits {
		labels = append(labels, gswLabel(b))
	}
	sort.Strings(labels)
	return strings.Join(labels, "|")
}

func gswSetValueGrouper(t testing.TB, schema *encoding.Schema, include ...string) *setValueGrouper {
	t.Helper()
	g, err := newSetValueGrouper(&types.Group{Type: types.GROUP_SET_VALUE, Field: "tags", Include: include}, schema)
	if err != nil {
		t.Fatalf("newSetValueGrouper: %v", err)
	}
	return g.(*setValueGrouper)
}

func gswPerElementGrouper(t testing.TB, schema *encoding.Schema, include ...string) *setPerElementGrouper {
	t.Helper()
	g, err := newSetPerElementGrouper(&types.Group{Type: types.GROUP_SET_PER_ELEMENT, Field: "tags", Include: include}, schema)
	if err != nil {
		t.Fatalf("newSetPerElementGrouper: %v", err)
	}
	return g.(*setPerElementGrouper)
}

// TestGroupSetValueWide_KeyDistinguishesEveryWord is the mandatory
// collision test. Each pair differs ONLY in a bit at or above 64, 128
// or 192 — exactly the bits a uint64 group key cannot see.
func TestGroupSetValueWide_KeyDistinguishesEveryWord(t *testing.T) {
	schema := gswSchema(t, encoding.FieldTypeSetU256, 206)
	g := gswSetValueGrouper(t, schema)

	cases := []struct {
		name string
		a    []int
		b    []int
	}{
		{"differ above bit 64", []int{3, 100}, []int{3, 101}},
		{"differ at the bit-64 boundary", []int{3}, []int{3, 64}},
		{"differ above bit 128", []int{3, 130}, []int{3, 131}},
		{"differ at the bit-128 boundary", []int{3, 127}, []int{3, 128}},
		{"differ above bit 192", []int{3, 200}, []int{3, 201}},
		{"differ at the bit-192 boundary", []int{3, 191}, []int{3, 192}},
		{"same popcount, different high words", []int{130}, []int{200}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ka, err := g.KeyFor(gswRecord(schema, gswMask(tc.a...)))
			if err != nil {
				t.Fatalf("KeyFor(a): %v", err)
			}
			kb, err := g.KeyFor(gswRecord(schema, gswMask(tc.b...)))
			if err != nil {
				t.Fatalf("KeyFor(b): %v", err)
			}
			if ka == kb {
				t.Fatalf("masks %v and %v collided on key %q", tc.a, tc.b, ka)
			}
			// Anchor to generated truth, not merely to each other: a key
			// derivation that emitted the bit indices in the wrong order
			// would also be "different".
			if want := gswExpectedKey(tc.a...); ka != want {
				t.Errorf("KeyFor(%v) = %q, want %q", tc.a, ka, want)
			}
			if want := gswExpectedKey(tc.b...); kb != want {
				t.Errorf("KeyFor(%v) = %q, want %q", tc.b, kb, want)
			}
		})
	}
}

// TestGroupSetValueWide_EverySingleBitKeyIsUnique sweeps all 206
// dictionary members. A key derivation truncated at any word boundary
// collapses the bits past it onto the empty-mask key.
func TestGroupSetValueWide_EverySingleBitKeyIsUnique(t *testing.T) {
	const members = 206
	schema := gswSchema(t, encoding.FieldTypeSetU256, members)
	g := gswSetValueGrouper(t, schema)

	seen := make(map[string]int, members)
	for bit := 0; bit < members; bit++ {
		key, err := g.KeyFor(gswRecord(schema, gswMask(bit)))
		if err != nil {
			t.Fatalf("KeyFor(bit %d): %v", bit, err)
		}
		if prev, dup := seen[key]; dup {
			t.Fatalf("bit %d and bit %d both keyed to %q", bit, prev, key)
		}
		if want := gswLabel(bit); key != want {
			t.Fatalf("KeyFor(bit %d) = %q, want %q", bit, key, want)
		}
		seen[key] = bit
	}
	if len(seen) != members {
		t.Fatalf("distinct keys = %d, want %d", len(seen), members)
	}
}

// TestGroupSetValueWide_GroupPartitionsByFullMask drives the buffered
// Group() path. Expected bucket keys are derived from the bit lists, not
// from a second execution of the grouper.
func TestGroupSetValueWide_GroupPartitionsByFullMask(t *testing.T) {
	schema := gswSchema(t, encoding.FieldTypeSetU256, 206)
	g := gswSetValueGrouper(t, schema)

	bitsPerRecord := [][]int{
		{1, 70},
		{1, 70},
		{1, 71}, // differs from the first two only above bit 64
		{},      // empty mask: a valid selection, distinct from null
		{5, 130, 200},
	}
	recs := make([]*Record, 0, len(bitsPerRecord)+1)
	for _, bits := range bitsPerRecord {
		recs = append(recs, gswRecord(schema, gswMask(bits...)))
	}
	// A genuinely null row keys nothing at all.
	null := NewRecordWithWide(schema, map[string]float64{}, map[string]bool{"tags": true}, map[string]any{})
	recs = append(recs, null)

	groups, err := g.Group(recs, "tags")
	if err != nil {
		t.Fatalf("Group: %v", err)
	}
	want := map[string]int{
		gswExpectedKey(1, 70):       2,
		gswExpectedKey(1, 71):       1,
		gswExpectedKey():            1,
		gswExpectedKey(5, 130, 200): 1,
	}
	if len(groups) != len(want) {
		keys := make([]string, 0, len(groups))
		for k := range groups {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		t.Fatalf("bucket count = %d (%v), want %d", len(groups), keys, len(want))
	}
	for key, n := range want {
		if got := len(groups[key]); got != n {
			t.Errorf("bucket %q = %d rows, want %d", key, got, n)
		}
	}
}

// TestGroupSetValueWide_ComponentsCarryEveryWord pins the Components
// contract at the wide rungs: a mask that fits 64 bits still emits the
// historical numeric "mask" key, and one that does not emits
// "mask_words" INSTEAD — never a truncated low word dressed up as the
// whole selection.
func TestGroupSetValueWide_ComponentsCarryEveryWord(t *testing.T) {
	schema := gswSchema(t, encoding.FieldTypeSetU256, 206)
	g := gswSetValueGrouper(t, schema)

	narrowBits := []int{1, 5}
	wideBits := []int{1, 70, 130, 200}
	// highOnlyBits has NOTHING in the low word. It is the case that
	// separates "is this mask empty" from "is this mask's low word
	// zero" — exactly one row is the empty-mask row and this is not it.
	highOnlyBits := []int{70}
	recs := []*Record{
		gswRecord(schema, gswMask(narrowBits...)),
		gswRecord(schema, gswMask(wideBits...)),
		gswRecord(schema, gswMask(wideBits...)),
		gswRecord(schema, gswMask(highOnlyBits...)),
		gswRecord(schema, encoding.SetMask{}),
	}
	if _, err := g.Group(recs, "tags"); err != nil {
		t.Fatalf("Group: %v", err)
	}
	comps, err := g.Components()
	if err != nil {
		t.Fatalf("Components: %v", err)
	}
	// Exactly one row carried the empty selection. A high-word-only mask
	// is NOT empty — counting it here would misreport the "picked
	// nothing" figure on every wide cohort.
	if got := comps["n_empty_mask"]; got != 1 {
		t.Errorf("n_empty_mask = %v, want 1", got)
	}
	buckets, ok := comps["buckets"].([]map[string]any)
	if !ok {
		t.Fatalf("buckets = %T, want []map[string]any", comps["buckets"])
	}
	byKey := make(map[string]map[string]any, len(buckets))
	for _, b := range buckets {
		byKey[b["key"].(string)] = b
	}

	narrow := byKey[gswExpectedKey(narrowBits...)]
	if narrow == nil {
		t.Fatalf("narrow bucket %q missing from %v", gswExpectedKey(narrowBits...), byKey)
	}
	if _, present := narrow["mask_words"]; present {
		t.Errorf("a 64-bit-representable mask must not emit mask_words: %v", narrow)
	}
	if got, want := narrow["mask"], uint64(0b100010); got != want {
		t.Errorf("narrow bucket mask = %v (%T), want %v", got, got, want)
	}

	if b := byKey[gswExpectedKey(highOnlyBits...)]; b == nil {
		t.Fatalf("high-word-only bucket %q missing from %v", gswExpectedKey(highOnlyBits...), byKey)
	} else if _, present := b["mask"]; present {
		t.Errorf("a mask with only high-word bits must not emit a uint64 mask: %v", b)
	}

	wide := byKey[gswExpectedKey(wideBits...)]
	if wide == nil {
		t.Fatalf("wide bucket %q missing from %v", gswExpectedKey(wideBits...), byKey)
	}
	if got, present := wide["mask"]; present {
		t.Errorf("a mask reaching bit 64+ must not emit a uint64 mask (got %v)", got)
	}
	words, ok := wide["mask_words"].([encoding.SetMaskWords]uint64)
	if !ok {
		t.Fatalf("mask_words = %T, want [%d]uint64", wide["mask_words"], encoding.SetMaskWords)
	}
	wantWords := gswMask(wideBits...).Words()
	if words != wantWords {
		t.Errorf("mask_words = %v, want %v", words, wantWords)
	}
	if got := wide["count"]; got != 2 {
		t.Errorf("wide bucket count = %v, want 2", got)
	}
	labels, _ := wide["labels"].([]string)
	wantLabels := []string{gswLabel(1), gswLabel(70), gswLabel(130), gswLabel(200)}
	if strings.Join(labels, ",") != strings.Join(wantLabels, ",") {
		t.Errorf("labels = %v, want %v", labels, wantLabels)
	}
}

// TestGroupSetValueWide_StreamingAndBufferedBothMatchTruth drives
// KeyForRow (streaming) and Group (buffered) over the same records and
// compares BOTH to an independently derived key→count map. Comparing
// the two paths to each other would share any decode bug.
func TestGroupSetValueWide_StreamingAndBufferedBothMatchTruth(t *testing.T) {
	schema := gswSchema(t, encoding.FieldTypeSetU128, 120)
	bitsPerRecord := [][]int{{2, 65}, {2, 65}, {2, 66}, {119}, {}}

	truth := map[string]int{}
	for _, bits := range bitsPerRecord {
		truth[gswExpectedKey(bits...)]++
	}

	recs := make([]*Record, 0, len(bitsPerRecord))
	for _, bits := range bitsPerRecord {
		recs = append(recs, gswRecord(schema, gswMask(bits...)))
	}

	streaming := gswSetValueGrouper(t, schema)
	streamCounts := map[string]int{}
	for i, r := range recs {
		key, ok, err := streaming.KeyForRow(r, "tags")
		if err != nil {
			t.Fatalf("KeyForRow(%d): %v", i, err)
		}
		if !ok {
			t.Fatalf("KeyForRow(%d) skipped a non-null record", i)
		}
		streamCounts[key]++
	}
	if len(streamCounts) != len(truth) {
		t.Fatalf("streaming buckets = %v, want %v", streamCounts, truth)
	}
	for k, n := range truth {
		if streamCounts[k] != n {
			t.Errorf("streaming bucket %q = %d, want %d", k, streamCounts[k], n)
		}
	}

	buffered := gswSetValueGrouper(t, schema)
	groups, err := buffered.Group(recs, "tags")
	if err != nil {
		t.Fatalf("Group: %v", err)
	}
	if len(groups) != len(truth) {
		t.Fatalf("buffered buckets = %d, want %d", len(groups), len(truth))
	}
	for k, n := range truth {
		if got := len(groups[k]); got != n {
			t.Errorf("buffered bucket %q = %d, want %d", k, got, n)
		}
	}
}

// TestGroupSetPerElementWide_206MemberDictionary fans a record out to
// one bucket per selected member across all four words of a set_u256
// column, and pins each bucket's dict_index to the bit it came from.
func TestGroupSetPerElementWide_206MemberDictionary(t *testing.T) {
	const members = 206
	schema := gswSchema(t, encoding.FieldTypeSetU256, members)
	g := gswPerElementGrouper(t, schema)

	selected := []int{0, 63, 64, 65, 127, 128, 191, 192, 205}
	recs := []*Record{
		gswRecord(schema, gswMask(selected...)),
		gswRecord(schema, gswMask(64, 205)),
		gswRecord(schema, encoding.SetMask{}), // empty mask fans nowhere
	}
	groups, err := g.Group(recs, "tags")
	if err != nil {
		t.Fatalf("Group: %v", err)
	}
	if len(groups) != len(selected) {
		t.Fatalf("bucket count = %d, want %d", len(groups), len(selected))
	}
	for _, bit := range selected {
		want := 1
		if bit == 64 || bit == 205 {
			want = 2
		}
		if got := len(groups[gswLabel(bit)]); got != want {
			t.Errorf("bucket %q = %d rows, want %d", gswLabel(bit), got, want)
		}
	}

	comps, err := g.Components()
	if err != nil {
		t.Fatalf("Components: %v", err)
	}
	if got, want := comps["total_label_observations"], len(selected)+2; got != want {
		t.Errorf("total_label_observations = %v, want %d", got, want)
	}
	buckets, ok := comps["buckets"].([]map[string]any)
	if !ok {
		t.Fatalf("buckets = %T", comps["buckets"])
	}
	for _, b := range buckets {
		label := b["label"].(string)
		idx := b["dict_index"].(int)
		if got := gswLabel(idx); got != label {
			t.Errorf("bucket %q carries dict_index %d (label %q)", label, idx, got)
		}
	}
}

// TestGroupSetPerElementWide_KeysForRowMatchesTruth pins the streaming
// fan-out key set per record against labels derived from the bit list.
func TestGroupSetPerElementWide_KeysForRowMatchesTruth(t *testing.T) {
	schema := gswSchema(t, encoding.FieldTypeSetU256, 206)
	g := gswPerElementGrouper(t, schema)

	bits := []int{7, 70, 140, 199}
	keys, ok, err := g.KeysForRow(gswRecord(schema, gswMask(bits...)), "tags")
	if err != nil {
		t.Fatalf("KeysForRow: %v", err)
	}
	if !ok {
		t.Fatal("KeysForRow skipped a populated mask")
	}
	want := make([]string, 0, len(bits))
	for _, b := range bits {
		want = append(want, gswLabel(b))
	}
	if strings.Join(keys, ",") != strings.Join(want, ",") {
		t.Fatalf("KeysForRow = %v, want %v", keys, want)
	}

	// An Include list still filters, and still reaches past bit 64.
	inc := gswPerElementGrouper(t, schema, gswLabel(140), gswLabel(7))
	got, ok, err := inc.KeysForRow(gswRecord(schema, gswMask(bits...)), "tags")
	if err != nil {
		t.Fatalf("KeysForRow (include): %v", err)
	}
	if !ok {
		t.Fatal("KeysForRow (include) skipped a matching record")
	}
	if strings.Join(got, ",") != gswLabel(7)+","+gswLabel(140) {
		t.Fatalf("KeysForRow (include) = %v, want [%s %s]", got, gswLabel(7), gswLabel(140))
	}
}

// TestGroupSetWide_NarrowRungsAreUnchanged re-runs the key derivation at
// every rung so widening the group key did not move a narrow cohort's
// bucket strings.
func TestGroupSetWide_NarrowRungsAreUnchanged(t *testing.T) {
	for _, tc := range []struct {
		ft      encoding.FieldType
		members int
		bits    []int
	}{
		{encoding.FieldTypeSetU8, 8, []int{0, 7}},
		{encoding.FieldTypeSetU16, 16, []int{1, 15}},
		{encoding.FieldTypeSetU32, 32, []int{2, 31}},
		{encoding.FieldTypeSetU64, 64, []int{3, 63}},
		{encoding.FieldTypeSetU128, 128, []int{3, 127}},
		{encoding.FieldTypeSetU256, 206, []int{3, 205}},
	} {
		t.Run(tc.ft.String(), func(t *testing.T) {
			schema := gswSchema(t, tc.ft, tc.members)
			g := gswSetValueGrouper(t, schema)
			key, err := g.KeyFor(gswRecord(schema, gswMask(tc.bits...)))
			if err != nil {
				t.Fatalf("KeyFor: %v", err)
			}
			if want := gswExpectedKey(tc.bits...); key != want {
				t.Errorf("KeyFor = %q, want %q", key, want)
			}
		})
	}
}

// TestGrouperNumeric_RejectsSetField guards the Record.NumericValue
// echo. A set column is stored in the wide map AND echoed into the
// numeric map as a float64 — for set_u128 / set_u256 that echo is the
// LOW 64 BITS of the mask, a plausible number that would bucket
// silently and wrongly. GROUP_RANGE / GROUP_ROUNDED / GROUP_QUANTILE all
// declare numeric field types only (descriptor AcceptsTypes:
// numericFieldTypesNoDecimal), so the mismatch is refused at
// construction rather than answered at run time.
func TestGrouperNumeric_RejectsSetField(t *testing.T) {
	rungs := []encoding.FieldType{
		encoding.FieldTypeSetU8, encoding.FieldTypeSetU16,
		encoding.FieldTypeSetU32, encoding.FieldTypeSetU64,
		encoding.FieldTypeSetU128, encoding.FieldTypeSetU256,
	}
	ctors := []struct {
		name string
		typ  types.GroupType
		fn   func(*types.Group, *encoding.Schema) (Grouper, error)
	}{
		{"GROUP_RANGE", types.GROUP_RANGE, newRangeGrouper},
		{"GROUP_ROUNDED", types.GROUP_ROUNDED, newRoundedGrouper},
		{"GROUP_QUANTILE", types.GROUP_QUANTILE, newQuantileGrouper},
	}
	for _, c := range ctors {
		for _, ft := range rungs {
			schema := gswSchema(t, ft, 8)
			if _, err := c.fn(&types.Group{Type: c.typ, Field: "tags", Interval: 10}, schema); err == nil {
				t.Errorf("%s built against a %s field; expected a config error", c.name, ft)
			}
		}
		// A numeric field is unaffected, and so is a nil schema (the
		// registry constructs groupers without one).
		numeric := &encoding.Schema{Fields: []encoding.Field{{Name: "n", Type: encoding.FieldTypeU32}}}
		if _, err := c.fn(&types.Group{Type: c.typ, Field: "n", Interval: 10}, numeric); err != nil {
			t.Errorf("%s on a u32 field rejected: %v", c.name, err)
		}
		if _, err := c.fn(&types.Group{Type: c.typ, Field: "n", Interval: 10}, nil); err != nil {
			t.Errorf("%s with a nil schema rejected: %v", c.name, err)
		}
	}
}

// TestCanStreamRequest_WideSetGroupersHaveNoWidthGate pins the absence
// of a decimal128-style streamability gate on the wide rungs: a wide-set
// grouper streams exactly as a narrow one does.
func TestCanStreamRequest_WideSetGroupersHaveNoWidthGate(t *testing.T) {
	for _, ft := range []encoding.FieldType{
		encoding.FieldTypeSetU8, encoding.FieldTypeSetU64,
		encoding.FieldTypeSetU128, encoding.FieldTypeSetU256,
	} {
		for _, gt := range []types.GroupType{types.GROUP_SET_VALUE, types.GROUP_SET_PER_ELEMENT} {
			schema := gswSchema(t, ft, 8)
			schema.Fields = append(schema.Fields, encoding.Field{Name: "value", Type: encoding.FieldTypeF64, CsvColumnIdx: 1})
			req := &types.Request{
				Groups:       []*types.Group{{Type: gt, Field: "tags"}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "value", Label: "total"}},
			}
			if !CanStreamRequest(req, schema) {
				t.Errorf("%s on %s is not streamable; no width gate was intended", gt, ft)
			}
		}
	}
}

// TestGroupSetWide_StreamingRunMatchesBufferedRunRowForRow drives one
// wide-set request down BOTH orchestrator arms and compares each of them
// to a group→sum map derived from the bit lists. Comparing the two arms
// to each other alone would pass vacuously — they share the same decode
// and the same key derivation — so the generated truth is the anchor and
// the arm-to-arm diff is the extra.
func TestGroupSetWide_StreamingRunMatchesBufferedRunRowForRow(t *testing.T) {
	for _, gt := range []types.GroupType{types.GROUP_SET_VALUE, types.GROUP_SET_PER_ELEMENT} {
		t.Run(string(gt), func(t *testing.T) {
			schema := gswSchema(t, encoding.FieldTypeSetU256, 206)
			schema.Fields = append(schema.Fields, encoding.Field{
				Name: "value", Type: encoding.FieldTypeF64, CsvColumnIdx: 1, Nullable: true,
			})

			rows := []struct {
				bits []int
				val  float64
			}{
				{[]int{2, 70}, 10},
				{[]int{2, 70}, 5},
				{[]int{2, 71}, 7}, // differs from the first two only above bit 64
				{[]int{130, 200}, 3},
				{nil, 11}, // empty mask
			}
			recs := make([]*Record, 0, len(rows))
			for _, r := range rows {
				rec := gswRecord(schema, gswMask(r.bits...))
				rec.SetNumeric("value", r.val)
				recs = append(recs, rec)
			}

			// Generated truth: bucket key -> summed value, derived from
			// the bit lists directly.
			truth := map[string]float64{}
			for _, r := range rows {
				if gt == types.GROUP_SET_VALUE {
					truth[gswExpectedKey(r.bits...)] += r.val
					continue
				}
				for _, b := range r.bits {
					truth[gswLabel(b)] += r.val
				}
			}

			req := &types.Request{
				Groups:       []*types.Group{{Type: gt, Field: "tags"}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "value", Label: "total"}},
			}

			streamP := NewProcessor(schema)
			streamResp, err := streamP.Process(context.Background(), req, NewSliceIterator(recs))
			if err != nil {
				t.Fatalf("streaming Process: %v", err)
			}
			if streamP.LastPath() != PathStreaming {
				t.Fatalf("orchestrator took %v, not the streaming path — the parity claim would be vacuous", streamP.LastPath())
			}

			bufP := NewProcessor(schema)
			bufResp, err := bufP.processRecords(context.Background(), req, recs)
			if err != nil {
				t.Fatalf("buffered processRecords: %v", err)
			}

			// Universal floor {total_n, n_null} plus the operator keys.
			// The claim is WIDTH INVARIANCE: an isomorphic set_u8 cohort
			// (the same selection pattern folded into bits 0..4) must
			// produce the identical floor, so widening the column cannot
			// move a figure. Asserting absolute numbers here would bake
			// the fan-out grouper's pre-existing total_n semantics into
			// this story's contract; asserting invariance does not.
			narrowFloor := gswNarrowMirrorFloor(t, gt)
			for _, arm := range []struct {
				name string
				resp *types.Response
			}{{"streaming", streamResp}, {"buffered", bufResp}} {
				if arm.resp.Components == nil || len(arm.resp.Components.Groupers) != 1 {
					t.Fatalf("%s: expected exactly one grouper components slot, got %+v", arm.name, arm.resp.Components)
				}
				gc := arm.resp.Components.Groupers[0]
				if gc.Field != "tags" {
					t.Errorf("%s: components field = %q, want \"tags\"", arm.name, gc.Field)
				}
				if gc.TotalN != narrowFloor.TotalN {
					t.Errorf("%s: total_n = %d, want %d (the set_u8 mirror's figure)", arm.name, gc.TotalN, narrowFloor.TotalN)
				}
				if gc.NNull != narrowFloor.NNull {
					t.Errorf("%s: n_null = %d, want %d (the set_u8 mirror's figure)", arm.name, gc.NNull, narrowFloor.NNull)
				}
				if gc.Operator == nil {
					t.Fatalf("%s: operator components missing", arm.name)
				}
				key := "n_empty_mask"
				if gt == types.GROUP_SET_PER_ELEMENT {
					key = "total_label_observations"
				}
				if got, want := gc.Operator[key], narrowFloor.Operator[key]; got != want {
					t.Errorf("%s: %s = %v, want %v (the set_u8 mirror's figure)", arm.name, key, got, want)
				}
			}

			collect := func(resp *types.Response) map[string]float64 {
				out := map[string]float64{}
				for _, row := range resp.Data {
					key, _ := row["tags"].(string)
					v, _ := row["total"].(float64)
					out[key] = v
				}
				return out
			}
			for _, arm := range []struct {
				name string
				got  map[string]float64
			}{
				{"streaming", collect(streamResp)},
				{"buffered", collect(bufResp)},
			} {
				if len(arm.got) != len(truth) {
					t.Fatalf("%s buckets = %v, want %v", arm.name, arm.got, truth)
				}
				for k, want := range truth {
					if got := arm.got[k]; got != want {
						t.Errorf("%s bucket %q = %v, want %v", arm.name, k, got, want)
					}
				}
			}
		})
	}
}

// gswNarrowMirrorFloor runs the isomorphic set_u8 cohort behind
// TestGroupSetWide_StreamingRunMatchesBufferedRunRowForRow: the same
// five-row selection pattern with every bit folded into 0..4. Its
// GrouperComponents floor is the reference the wide run must match, so
// the wide assertions test width invariance rather than re-stating the
// fan-out grouper's own counting rules.
func gswNarrowMirrorFloor(t *testing.T, gt types.GroupType) types.GrouperComponents {
	t.Helper()
	schema := gswSchema(t, encoding.FieldTypeSetU8, 8)
	schema.Fields = append(schema.Fields, encoding.Field{
		Name: "value", Type: encoding.FieldTypeF64, CsvColumnIdx: 1, Nullable: true,
	})
	rows := []struct {
		bits []int
		val  float64
	}{
		{[]int{0, 1}, 10},
		{[]int{0, 1}, 5},
		{[]int{0, 2}, 7},
		{[]int{3, 4}, 3},
		{nil, 11},
	}
	recs := make([]*Record, 0, len(rows))
	for _, r := range rows {
		rec := gswRecord(schema, gswMask(r.bits...))
		rec.SetNumeric("value", r.val)
		recs = append(recs, rec)
	}
	p := NewProcessor(schema)
	resp, err := p.processRecords(context.Background(), &types.Request{
		Groups:       []*types.Group{{Type: gt, Field: "tags"}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "value", Label: "total"}},
	}, recs)
	if err != nil {
		t.Fatalf("narrow mirror processRecords: %v", err)
	}
	if resp.Components == nil || len(resp.Components.Groupers) != 1 {
		t.Fatalf("narrow mirror produced no grouper components: %+v", resp.Components)
	}
	return resp.Components.Groupers[0]
}
