package processing

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// mergeGrouperSchema carries one column per mergeable grouper: a
// categorical, a nullable numeric, a date and a set.
func mergeGrouperSchema(t *testing.T) *encoding.Schema {
	t.Helper()
	dict := func(vals ...string) *encoding.Dictionary {
		d := encoding.NewDictionary()
		for _, v := range vals {
			if _, err := d.Add(v); err != nil {
				t.Fatal(err)
			}
		}
		return d
	}
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "region", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict("north", "south", "east", "west")},
		{Name: "score", Type: encoding.FieldTypeF64, Nullable: true},
		{Name: "enrolled", Type: encoding.FieldTypeDate},
		{Name: "tags", Type: encoding.FieldTypeSetU8, Dictionary: dict("a", "b", "c", "d", "e")},
	}}
}

func mergeGrouperRecords(schema *encoding.Schema, n int) []*Record {
	base := float64(time.Date(2023, 11, 1, 0, 0, 0, 0, time.UTC).Unix() / 86400)
	out := make([]*Record, n)
	for i := range out {
		nulls := map[string]bool{}
		if i%7 == 0 {
			nulls["score"] = true
		}
		mask := uint64(1<<uint(i%5) | 1<<uint((i/2)%5))
		if i%9 == 0 {
			mask = 0
		}
		out[i] = NewRecordWithWide(schema, map[string]float64{
			"region":   float64(i % 4),
			"score":    float64(i%23) * 1.75,
			"enrolled": base + float64(i*3),
		}, nulls, map[string]any{"tags": mask})
	}
	return out
}

// TestMergeableGrouper_MergedComponentsEqualSerial: for every grouper
// types.GroupType.Mergeable() admits, feeding the records through ONE
// instance and feeding them split across three instances then folding
// through MergeGrouperState yield the same Components(). That equality
// is what lets the parallel reducers emit Components.Groupers at all.
func TestMergeableGrouper_MergedComponentsEqualSerial(t *testing.T) {
	schema := mergeGrouperSchema(t)
	recs := mergeGrouperRecords(schema, 211)
	ranges, _ := json.Marshal(map[string]any{"ranges": []map[string]any{
		{"label": "late", "start": "2024-06-01", "end": "2024-12-31"},
		{"label": "early", "start": "2024-01-01", "end": "2024-05-31"},
	}})
	groups := []*types.Group{
		{Type: types.GROUP_CATEGORY, Field: "region"},
		{Type: types.GROUP_CATEGORY, Field: "region", Include: []string{"west", "north"}},
		{Type: types.GROUP_RANGE, Field: "score", Interval: 5},
		{Type: types.GROUP_DATE_RANGES, Field: "enrolled", Params: ranges},
		{Type: types.GROUP_SET_VALUE, Field: "tags"},
		{Type: types.GROUP_SET_PER_ELEMENT, Field: "tags"},
	}
	for _, typ := range types.AllGroupTypes() {
		if !typ.Mergeable() {
			continue
		}
		covered := false
		for _, g := range groups {
			covered = covered || g.Type == typ
		}
		if !covered {
			t.Errorf("mergeable grouper %s has no merge case here", typ)
		}
	}

	build := func(g *types.Group) Grouper {
		inst, err := grouperRegistry[g.Type](g, schema)
		if err != nil {
			t.Fatalf("%s: %v", g.Type, err)
		}
		return inst
	}
	feed := func(g *types.Group, inst Grouper, rs []*Record) {
		keyer, err := NewGroupKeyer(inst)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range rs {
			if _, _, err := keyer.Keys(r, g.Field); err != nil {
				t.Fatalf("%s: %v", g.Type, err)
			}
		}
	}
	components := func(inst Grouper) map[string]any {
		op, err := inst.(MetaGrouper).Components()
		if err != nil {
			t.Fatal(err)
		}
		return op
	}
	for _, g := range groups {
		t.Run(fmt.Sprintf("%s_%s_%d", g.Type, g.Field, len(g.Include)), func(t *testing.T) {
			serial := build(g)
			feed(g, serial, recs)
			want := components(serial)

			cuts := []int{0, 70, 71, 211} // includes an EMPTY partition
			var merged Grouper
			for i := 0; i+1 < len(cuts); i++ {
				part := build(g)
				feed(g, part, recs[cuts[i]:cuts[i+1]])
				if merged == nil {
					merged = part
					continue
				}
				if err := merged.(MergeableGrouper).MergeGrouperState(part); err != nil {
					t.Fatal(err)
				}
			}
			if got := components(merged); !reflect.DeepEqual(got, want) {
				t.Errorf("merged Components differ\n got  %v\n want %v", got, want)
			}
			if b, ok := want["buckets"].([]map[string]any); !ok || len(b) < 2 {
				t.Fatalf("degenerate fixture: %v", want)
			}
		})
	}
}

// A merge across two different grouper kinds is a caller invariant
// violation, never a silent partial fold.
func TestMergeableGrouper_RefusesAForeignInstance(t *testing.T) {
	schema := mergeGrouperSchema(t)
	cat, _ := grouperRegistry[types.GROUP_CATEGORY](&types.Group{Type: types.GROUP_CATEGORY, Field: "region"}, schema)
	rng, _ := grouperRegistry[types.GROUP_RANGE](&types.Group{Type: types.GROUP_RANGE, Field: "score", Interval: 5}, schema)
	if err := cat.(MergeableGrouper).MergeGrouperState(rng); err == nil {
		t.Fatal("a category grouper folded a range grouper")
	}
}
