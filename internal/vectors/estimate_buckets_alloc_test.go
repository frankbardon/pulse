package vectors

import (
	"fmt"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// TestEstimateBuckets_DictionaryNotCopied (U51 E3-S4): a dictionary
// grouper's bound is the dictionary's size, read without copying its
// values — predict and the limit pre-flight size every grouper per call,
// over dictionaries of any size — and an include list is still checked
// against the dictionary's membership.
//
// Falsified by restoring the Values() copy (one allocation per call).
func TestEstimateBuckets_DictionaryNotCopied(t *testing.T) {
	dict := encoding.NewDictionary()
	for i := range 100_000 {
		if _, err := dict.Add(fmt.Sprintf("k%06d", i)); err != nil {
			t.Fatal(err)
		}
	}
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "c", Type: encoding.FieldTypeCategoricalU32, Dictionary: dict},
	}}
	groups := []*types.Group{{Type: types.GROUP_CATEGORY, Field: "c"}}
	if n, basis, ok := EstimateBuckets(groups, schema); !ok || n != 100_000 || basis != BucketBasisDictionary {
		t.Fatalf("EstimateBuckets = %d, %q, %v; want 100000 dictionary", n, basis, ok)
	}
	if a := testing.AllocsPerRun(20, func() { EstimateBuckets(groups, schema) }); a != 0 {
		t.Errorf("EstimateBuckets allocated %.0f times per call over a 100k-entry dictionary, want 0", a)
	}
	inc := []*types.Group{{Type: types.GROUP_CATEGORY, Field: "c", Include: []string{"k000001", "k000002", "k000002", "absent"}}}
	if n, basis, ok := EstimateBuckets(inc, schema); !ok || n != 2 || basis != BucketBasisInclude {
		t.Errorf("include = %d, %q, %v; want 2 include (dedup, absent key dropped)", n, basis, ok)
	}
}
