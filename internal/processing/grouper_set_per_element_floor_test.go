package processing

import (
	"context"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// grouper_set_per_element_floor_test.go pins the GROUP_SET_PER_ELEMENT
// universal floor (#406): total_n counts the post-filter RECORDS the
// grouper keyed into at least one bucket — never the label observations
// a fan-out record contributes once per selected label — and n_null
// counts the null / skip path (a null set, or a non-empty set whose
// every label the include list rejects). An empty mask is skipped but
// lands in neither figure (op-group-set-per-element Gotchas).

func spfSchema(t *testing.T) *encoding.Schema {
	t.Helper()
	dict := encoding.NewDictionary()
	for _, v := range []string{"app", "web", "store"} {
		if _, err := dict.Add(v); err != nil {
			t.Fatalf("dict.Add: %v", err)
		}
	}
	region := encoding.NewDictionary()
	for _, v := range []string{"north", "south"} {
		if _, err := region.Add(v); err != nil {
			t.Fatalf("dict.Add: %v", err)
		}
	}
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "channels", Type: encoding.FieldTypeSetU8, Dictionary: dict, Nullable: true},
		{Name: "region", Type: encoding.FieldTypeCategoricalU8, Dictionary: region, ByteOffset: 1, CsvColumnIdx: 1},
	}}
}

// spfFixture is the issue's cohort: n records, the first nNull with a
// null set, the next nEmpty with an empty mask, the rest answered with
// a cycling 1-, 2- or 3-label selection. It returns the records and the
// label-observation count derived from the masks.
func spfFixture(schema *encoding.Schema, n, nNull, nEmpty int) ([]*Record, int) {
	masks := []uint64{0b001, 0b010, 0b011, 0b110, 0b111, 0b100, 0b010}
	recs := make([]*Record, 0, n)
	obs := 0
	for i := 0; i < n; i++ {
		region := float64(i % 2)
		switch {
		case i < nNull:
			recs = append(recs, NewRecordWithWide(schema, map[string]float64{"region": region}, map[string]bool{"channels": true}, nil))
		case i < nNull+nEmpty:
			recs = append(recs, NewRecordWithWide(schema, map[string]float64{"region": region}, nil, map[string]any{"channels": uint64(0)}))
		default:
			m := masks[i%len(masks)]
			for b := m; b != 0; b &= b - 1 {
				obs++
			}
			recs = append(recs, NewRecordWithWide(schema, map[string]float64{"region": region}, nil, map[string]any{"channels": m}))
		}
	}
	return recs, obs
}

func TestGroupSetPerElement_FloorCountsRecords(t *testing.T) {
	t.Parallel()

	schema := spfSchema(t)
	cases := []struct {
		name                 string
		nEmpty               int
		include              []string
		wantTotalN, wantNull int
	}{
		// The #406 repro: 300 records, 11 null, 289 answered.
		{name: "issue-406", wantTotalN: 289, wantNull: 11},
		// Empty masks are skipped yet are not nulls.
		{name: "empty-masks", nEmpty: 7, wantTotalN: 282, wantNull: 11},
		// include ["store"]: an answered record selecting no "store"
		// label is keyed into no bucket — the skip path, so n_null.
		{name: "include-rejects", include: []string{"store"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			recs, obs := spfFixture(schema, 300, 11, tc.nEmpty)
			wantTotalN, wantNull, wantObs := tc.wantTotalN, tc.wantNull, obs
			if tc.include != nil {
				// Truth from the masks: bit 2 is "store".
				wantTotalN, wantObs = 0, 0
				for _, r := range recs {
					if m, ok := r.SetMaskValue("channels"); ok && m.PopCount() > 0 && m.Has(2) {
						wantTotalN++
						wantObs++
					}
				}
				wantNull = 300 - tc.nEmpty - wantTotalN
			}
			if wantObs <= wantTotalN && tc.include == nil {
				t.Fatalf("fixture: %d observations over %d records does not fan out", wantObs, wantTotalN)
			}

			set := &types.Group{Type: types.GROUP_SET_PER_ELEMENT, Field: "channels", Include: tc.include}
			region := &types.Group{Type: types.GROUP_CATEGORY, Field: "region"}
			aggs := []*types.Aggregation{{Type: types.AGG_COUNT, Field: "region", Label: "orders"}}
			check := func(arm string, gc types.GrouperComponents) {
				t.Helper()
				if gc.Field != "channels" {
					t.Fatalf("%s: grouper field = %q", arm, gc.Field)
				}
				if gc.TotalN != wantTotalN || gc.NNull != wantNull {
					t.Errorf("%s: total_n/n_null = %d/%d, want %d/%d", arm, gc.TotalN, gc.NNull, wantTotalN, wantNull)
				}
				if got, _ := gc.Operator["total_label_observations"].(int); got != wantObs {
					t.Errorf("%s: total_label_observations = %d, want %d", arm, got, wantObs)
				}
			}

			for _, shape := range []struct {
				name   string
				groups []*types.Group
				at     int
			}{
				{"single", []*types.Group{set}, 0},
				{"composite", []*types.Group{region, set}, 1},
			} {
				req := &types.Request{Groups: shape.groups, Aggregations: aggs}

				sp := NewProcessor(schema)
				sresp, err := sp.Process(context.Background(), req, NewSliceIterator(recs))
				if err != nil {
					t.Fatalf("%s streaming Process: %v", shape.name, err)
				}
				if sp.LastPath() != PathStreaming {
					t.Fatalf("%s: took %v, not the streaming path", shape.name, sp.LastPath())
				}
				check(shape.name+"/streaming", sresp.Components.Groupers[shape.at])

				bp := NewProcessor(schema)
				bresp, err := bp.processRecords(context.Background(), req, recs)
				if err != nil {
					t.Fatalf("%s buffered processRecords: %v", shape.name, err)
				}
				check(shape.name+"/buffered", bresp.Components.Groupers[shape.at])
			}

			// Parallel / sharded reducers: per-partition groupers keyed
			// independently, then folded through MergeGrouperState (the
			// empty-mask tally must merge); the reducers sum the keyed
			// record count themselves and hand it over.
			parts := [][]*Record{recs[:5], recs[5:150], recs[150:]}
			var merged Grouper
			for _, part := range parts {
				g, err := newSetPerElementGrouper(set, schema)
				if err != nil {
					t.Fatalf("newSetPerElementGrouper: %v", err)
				}
				for _, r := range part {
					if _, _, err := g.(MultiKeyStreamingGrouper).KeysForRow(r, "channels"); err != nil {
						t.Fatalf("KeysForRow: %v", err)
					}
				}
				if merged == nil {
					merged = g
					continue
				}
				if err := merged.(MergeableGrouper).MergeGrouperState(g); err != nil {
					t.Fatalf("MergeGrouperState: %v", err)
				}
			}
			entry, err := buildStreamingGrouperComponents(merged, set, len(recs), int64(wantTotalN))
			if err != nil {
				t.Fatalf("buildStreamingGrouperComponents: %v", err)
			}
			check("merged", entry)
		})
	}
}
