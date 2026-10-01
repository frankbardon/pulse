package encoding

import (
	"bytes"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
)

// TestFlattenCohortBytes_ReproducesTheFlatSource: flattening a grouped
// cohort yields its 0x01 original byte for byte (preamble and every
// record), an ungrouped cohort comes back as the same slice, and a
// grouped payload ending mid-record is ENCODING_INVALID rather than a
// silently shorter twin.
func TestFlattenCohortBytes_ReproducesTheFlatSource(t *testing.T) {
	for _, fx := range groupFixtures(t) {
		t.Run(fx.name, func(t *testing.T) {
			flat := flatCohort(t, fx.schema, fx.rows)
			grouped, gs := groupedTwin(t, flat, fx.specs)
			got, schema, err := FlattenCohortBytes(grouped)
			if err != nil {
				t.Fatalf("FlattenCohortBytes: %v", err)
			}
			if !bytes.Equal(got, flat) {
				t.Fatal("the flattened twin differs from the 0x01 source")
			}
			if schema.HasGroups() || len(schema.Fields) != len(gs.Fields) {
				t.Fatalf("flattened schema: groups=%v fields=%d", schema.HasGroups(), len(schema.Fields))
			}
			same, _, err := FlattenCohortBytes(flat)
			if err != nil || &same[0] != &flat[0] {
				t.Fatalf("an ungrouped cohort must come back as the same slice (err %v)", err)
			}
			if _, _, err := FlattenCohortBytes(grouped[:len(grouped)-1]); !errors.HasCode(err, errors.ENCODING_INVALID) {
				t.Fatalf("truncated grouped cohort: err = %v, want ENCODING_INVALID", err)
			}
		})
	}
}

// TestGroupEncoder_SeedKeepsIndices: an encoder seeded with a grouped
// schema's dictionaries re-encodes the same logical rows to the SAME
// physical rows without growing any dictionary (every tuple is found) —
// the canonical-first union a shard archive relies on. Seeding a
// different layout, after a row, or past the index space is refused.
func TestGroupEncoder_SeedKeepsIndices(t *testing.T) {
	fx := groupFixtures(t)[0]
	flat := flatCohort(t, fx.schema, fx.rows)
	grouped, gs := groupedTwin(t, flat, fx.specs)
	_, physical := recordRegion(t, grouped)

	enc, err := NewGroupEncoder(fx.schema, GroupSpecsOf(gs))
	if err != nil {
		t.Fatal(err)
	}
	if err := enc.Seed(gs); err != nil {
		t.Fatalf("Seed: %v", err)
	}
	// Encode the rows in REVERSE order: an unseeded encoder would number
	// the tuples in the order it meets them, so only the seed can make
	// every row land on its original entry.
	var reversed, wantPhys bytes.Buffer
	ps := gs.RecordByteSize()
	for i := len(fx.rows) - 1; i >= 0; i-- {
		reversed.Write(fx.rows[i])
		wantPhys.Write(physical[i*ps : (i+1)*ps])
	}
	var spool bytes.Buffer
	if _, err := enc.EncodeStream(&spool, bytes.NewReader(reversed.Bytes())); err != nil {
		t.Fatalf("EncodeStream: %v", err)
	}
	if !bytes.Equal(spool.Bytes(), wantPhys.Bytes()) {
		t.Fatal("seeded re-encode did not keep every row on its original entry index")
	}
	out := enc.Schema()
	for g := range gs.Groups {
		if !bytes.Equal(out.Groups[g].Entries, gs.Groups[g].Entries) {
			t.Fatalf("group %d dictionary grew or reordered under a seed that already held every tuple", g)
		}
	}

	// A seed from a DIFFERENT layout (first group only) is refused, as is
	// seeding after a row.
	other, err := NewGroupEncoder(fx.schema, GroupSpecsOf(gs)[:1])
	if err != nil {
		t.Fatal(err)
	}
	if err := other.Seed(gs); err == nil {
		t.Fatal("seeding a different group layout must be refused")
	}
	if err := enc.Seed(gs); err == nil {
		t.Fatal("Seed after EncodeRow must be refused")
	}

	// A seed larger than the index space is PULSE_GROUP_ENTRIES_EXHAUSTED.
	prev := encoding.MaxGroupEntries
	encoding.MaxGroupEntries = uint64(gs.GroupEntryCount(0) - 1)
	defer func() { encoding.MaxGroupEntries = prev }()
	small, _ := NewGroupEncoder(fx.schema, GroupSpecsOf(gs))
	if err := small.Seed(gs); !errors.HasCode(err, errors.PULSE_GROUP_ENTRIES_EXHAUSTED) {
		t.Fatalf("oversized seed: err = %v", err)
	}
}

// TestValidateGroupCohesion_EveryDimension: the group layout is part of
// structural cohesion. Group count, kind, member and key flag each
// refuse with PULSE_SHARD_SCHEMA_MISMATCH naming the dimension; an equal
// layout with DIFFERENT dictionaries passes (dictionaries follow the
// prefix rule).
func TestValidateGroupCohesion_EveryDimension(t *testing.T) {
	fx := groupFixtures(t)[0]
	_, gs := groupedTwin(t, flatCohort(t, fx.schema, fx.rows), fx.specs)
	mutate := func(f func(s *encoding.Schema)) *encoding.Schema {
		c := cloneSchema(gs)
		f(c)
		return c
	}
	cases := map[string]*encoding.Schema{
		"group_count":  mutate(func(s *encoding.Schema) { s.Groups = s.Groups[:1] }),
		"kind":         mutate(func(s *encoding.Schema) { s.Groups[1].Kind = encoding.GroupKindConstant }),
		"member":       mutate(func(s *encoding.Schema) { s.Groups[1].Members[0].Field++ }),
		"key":          mutate(func(s *encoding.Schema) { s.Groups[0].Members[0].Key = !s.Groups[0].Members[0].Key }),
		"member_count": mutate(func(s *encoding.Schema) { s.Groups[1].Members = s.Groups[1].Members[:2] }),
	}
	for dim, inc := range cases {
		_, err := ValidateStructuralCohesion(gs, inc)
		var ce *errors.CodedError
		if !asCoded(err, &ce) || ce.Code != errors.PULSE_SHARD_SCHEMA_MISMATCH || ce.Details["dimension"] != dim {
			t.Fatalf("%s: err = %v", dim, err)
		}
	}
	if _, err := ValidateStructuralCohesion(gs, gs.Logical()); !errors.HasCode(err, errors.PULSE_SHARD_SCHEMA_MISMATCH) {
		t.Fatalf("a 0x01 shard against a 0x02 canonical: err = %v", err)
	}
	fewer := mutate(func(s *encoding.Schema) { s.Groups[0].Entries = s.Groups[0].Entries[:s.GroupEntryWidth(0)] })
	if _, err := ValidateStructuralCohesion(gs, fewer); err != nil {
		t.Fatalf("same layout, shorter dictionary: %v", err)
	}
}

// TestValidateDictPrefixRule_GroupEntries: group dictionaries follow the
// append-only prefix rule byte for byte — a shorter prefix is accepted
// with no change, a longer extension is adopted (a deep copy), and a
// reordered dictionary is PULSE_SHARD_DICT_DIVERGENCE.
func TestValidateDictPrefixRule_GroupEntries(t *testing.T) {
	fx := groupFixtures(t)[0]
	_, gs := groupedTwin(t, flatCohort(t, fx.schema, fx.rows), fx.specs)
	w := gs.GroupEntryWidth(0)
	short := cloneSchema(gs)
	short.Groups[0].Entries = short.Groups[0].Entries[:2*w]

	if got, err := ValidateDictPrefixRule(gs, short); err != nil || got != gs {
		t.Fatalf("prefix shard: got %p (canonical %p), err %v", got, gs, err)
	}
	got, err := ValidateDictPrefixRule(short, gs)
	if err != nil || got == short || !bytes.Equal(got.Groups[0].Entries, gs.Groups[0].Entries) || len(short.Groups[0].Entries) != 2*w {
		t.Fatalf("extension: err %v", err)
	}
	swapped := cloneSchema(gs)
	e := swapped.Groups[0].Entries
	tmp := append([]byte(nil), e[:w]...)
	copy(e[:w], e[w:2*w])
	copy(e[w:2*w], tmp)
	if _, err := ValidateDictPrefixRule(gs, swapped); !errors.HasCode(err, errors.PULSE_SHARD_DICT_DIVERGENCE) {
		t.Fatalf("reordered group dictionary: err = %v", err)
	}
}

// TestGroupIndexHeadroomFor reports entries against the u32 index space
// for an indexed group and zero headroom for a constant group.
func TestGroupIndexHeadroomFor(t *testing.T) {
	fx := groupFixtures(t)[0]
	_, gs := groupedTwin(t, flatCohort(t, fx.schema, fx.rows), fx.specs)
	h := GroupIndexHeadroomFor(gs)
	if len(h) != 3 {
		t.Fatalf("headroom rows = %d", len(h))
	}
	n := gs.GroupEntryCount(0)
	if h[0].Kind != "indexed" || h[0].Entries != n || h[0].Capacity != encoding.MaxGroupEntries || h[0].Headroom != encoding.MaxGroupEntries-uint64(n) {
		t.Fatalf("indexed headroom = %+v", h[0])
	}
	if h[2].Kind != "constant" || h[2].Entries != 1 || h[2].Capacity != 1 || h[2].Headroom != 0 {
		t.Fatalf("constant headroom = %+v", h[2])
	}
	if GroupIndexHeadroomFor(fx.schema) != nil {
		t.Fatal("an ungrouped schema reports group headroom")
	}
}

// TestRewriteShardCategoricalsKeepNulls: a NULL categorical keeps its
// placeholder bytes under the keep-nulls variant (a null's bytes are part
// of a group entry's identity), while a present value is remapped by
// both variants.
func TestRewriteShardCategoricalsKeepNulls(t *testing.T) {
	dict := encoding.NewDictionary()
	for _, v := range []string{"a", "b", "c"} {
		_, _ = dict.Add(v)
	}
	s := &encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU8, ByteOffset: 0},
		{Name: "cat", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 1, Nullable: true, Dictionary: dict},
	}}
	// Row 0: cat = index 0, present. Row 1: cat null, placeholder 0.
	rows := [][]byte{{1, 0, 0}, {2, 0, 0b10}}
	src := flatCohort(t, s, rows)
	remap := map[int]DictRemap{1: {0: 2}}
	for _, tc := range []struct {
		name      string
		fn        func([]byte, *encoding.Schema, map[int]DictRemap) ([]byte, error)
		nullBytes byte
	}{
		{"remap everything", RewriteShardCategoricals, 2},
		{"keep null placeholders", RewriteShardCategoricalsKeepNulls, 0},
	} {
		out, err := tc.fn(src, s, remap)
		if err != nil {
			t.Fatal(err)
		}
		_, recs := recordRegion(t, out)
		if recs[1] != 2 || recs[4] != tc.nullBytes {
			t.Fatalf("%s: present=%d null placeholder=%d, want 2 / %d", tc.name, recs[1], recs[4], tc.nullBytes)
		}
	}
}
