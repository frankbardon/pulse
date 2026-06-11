package processing

import (
	"encoding/json"
	stderrors "errors"
	"math/rand"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// Equivalence tests for StreamableGrouper.KeyFor.
//
// For every streamable grouper, KeyFor(rec) MUST produce the same
// bucket key that Group([]rec, field) places that record under. The
// fused crosstab path in E4-S3 builds on this invariant: it walks the
// decode stream calling KeyFor per record, then routes the record to
// the (rowKey, colKey) cell accumulator. If any drift exists between
// KeyFor and Group, the fused output would diverge from the legacy
// buffered crosstab output byte-for-byte.
//
// Each test synthesises ~1000 random records and asserts:
//   - every record present in Group's bucket has its key produced by
//     KeyFor unchanged;
//   - every record absent from Group's buckets (null / missing field)
//     produces ErrGrouperKeyNull from KeyFor.
//
// Quantile grouper and the multi-key set-per-element grouper are NOT
// in scope — they do not implement StreamableGrouper.

const keyForEquivalenceN = 1024

// assertStreamableEquivalence pumps records through Group() and KeyFor
// and asserts every record's bucket assignment matches.
func assertStreamableEquivalence(t *testing.T, g Grouper, field string, records []*Record) {
	t.Helper()

	sg, ok := g.(StreamableGrouper)
	if !ok {
		t.Fatalf("grouper %T does not implement StreamableGrouper", g)
	}

	groups, err := g.Group(records, field)
	if err != nil {
		t.Fatalf("Group: %v", err)
	}

	// Reverse-index: record pointer -> bucket key.
	recToBucket := make(map[*Record]string, len(records))
	for k, bucket := range groups {
		for _, r := range bucket {
			recToBucket[r] = k
		}
	}

	for i, r := range records {
		key, err := sg.KeyFor(r)
		bucket, present := recToBucket[r]
		switch {
		case present:
			if err != nil {
				t.Fatalf("record %d: KeyFor returned err=%v but record was placed in bucket %q", i, err, bucket)
			}
			if key != bucket {
				t.Fatalf("record %d: KeyFor=%q but Group placed it in %q", i, key, bucket)
			}
		default:
			// Record skipped by Group — KeyFor must surface ErrGrouperKeyNull.
			if err == nil {
				t.Fatalf("record %d: KeyFor returned key=%q nil err, but Group skipped the record (expected ErrGrouperKeyNull)", i, key)
			}
			if !stderrors.Is(err, ErrGrouperKeyNull) {
				t.Fatalf("record %d: KeyFor returned err=%v, expected ErrGrouperKeyNull", i, err)
			}
		}
	}
}

func TestGrouper_Category_KeyFor_EquivalentToGroup(t *testing.T) {
	schema := numericSchema()
	g := makeGrouper(t, types.GROUP_CATEGORY, "age", 0, schema)

	rnd := rand.New(rand.NewSource(0xC47E60))
	records := make([]*Record, keyForEquivalenceN)
	for i := range records {
		// Mix nulls (~15%) into the synth set so the null-skip path is covered.
		if rnd.Intn(100) < 15 {
			records[i] = NewRecordWithNulls(schema,
				map[string]float64{"age": 0},
				map[string]bool{"age": true})
			continue
		}
		// Integer-valued ages in a small range — exercises the integer-format branch.
		records[i] = NewRecord(schema, map[string]float64{"age": float64(rnd.Intn(80))})
	}
	assertStreamableEquivalence(t, g, "age", records)
}

func TestGrouper_Category_KeyFor_CategoricalDict(t *testing.T) {
	// Categorical field path — KeyFor must resolve dictionary indices to
	// labels, identical to Group's resolution.
	schema := categoricalSchema()
	g := makeGrouper(t, types.GROUP_CATEGORY, "brand", 0, schema)

	rnd := rand.New(rand.NewSource(0xCA1E60))
	records := make([]*Record, keyForEquivalenceN)
	for i := range records {
		if rnd.Intn(100) < 10 {
			records[i] = NewRecordWithNulls(schema,
				map[string]float64{"brand": 0},
				map[string]bool{"brand": true})
			continue
		}
		records[i] = NewRecord(schema, map[string]float64{"brand": float64(rnd.Intn(3))})
	}
	assertStreamableEquivalence(t, g, "brand", records)
}

func TestGrouper_Category_KeyFor_FloatNonCategorical(t *testing.T) {
	// Non-categorical numeric field with fractional values — exercises
	// the FormatFloat branch in formatRoundedNumeric.
	schema := numericSchema()
	g := makeGrouper(t, types.GROUP_CATEGORY, "score", 0, schema)

	rnd := rand.New(rand.NewSource(0xF10A77))
	records := make([]*Record, keyForEquivalenceN)
	for i := range records {
		if rnd.Intn(100) < 10 {
			records[i] = NewRecordWithNulls(schema,
				map[string]float64{"score": 0},
				map[string]bool{"score": true})
			continue
		}
		// Floats with rotating two decimals so most are fractional, some
		// happen to land exactly on integers (covers both branches).
		records[i] = NewRecord(schema, map[string]float64{"score": float64(rnd.Intn(40)) + rnd.Float64()})
	}
	assertStreamableEquivalence(t, g, "score", records)
}

func TestGrouper_Rounded_KeyFor_EquivalentToGroup(t *testing.T) {
	schema := numericSchema()
	g := makeGrouper(t, types.GROUP_ROUNDED, "age", 10, schema)

	rnd := rand.New(rand.NewSource(0xC0FFEE))
	records := make([]*Record, keyForEquivalenceN)
	for i := range records {
		if rnd.Intn(100) < 12 {
			records[i] = NewRecordWithNulls(schema,
				map[string]float64{"age": 0},
				map[string]bool{"age": true})
			continue
		}
		records[i] = NewRecord(schema, map[string]float64{"age": rnd.Float64() * 100})
	}
	assertStreamableEquivalence(t, g, "age", records)
}

func TestGrouper_Range_KeyFor_EquivalentToGroup(t *testing.T) {
	schema := numericSchema()
	g := makeGrouper(t, types.GROUP_RANGE, "age", 25, schema)

	rnd := rand.New(rand.NewSource(0xBEEF))
	records := make([]*Record, keyForEquivalenceN)
	for i := range records {
		if rnd.Intn(100) < 10 {
			records[i] = NewRecordWithNulls(schema,
				map[string]float64{"age": 0},
				map[string]bool{"age": true})
			continue
		}
		// Mix positive and negative values to cover the negative-floor
		// branch in formatRoundedNumeric.
		val := (rnd.Float64() - 0.5) * 200
		records[i] = NewRecord(schema, map[string]float64{"age": val})
	}
	assertStreamableEquivalence(t, g, "age", records)
}

func TestGrouper_Range_KeyFor_FractionalInterval(t *testing.T) {
	// Fractional Interval — KeyFor's "low-high" output is reconstructed
	// from formatRoundedNumeric; this asserts it stays byte-equal to
	// Group's keys even when low/high are non-integer floats.
	schema := numericSchema()
	factory, ok := grouperRegistry[types.GROUP_RANGE]
	if !ok {
		t.Fatalf("no grouper registered for GROUP_RANGE")
	}
	g, err := factory(&types.Group{Type: types.GROUP_RANGE, Field: "score", Interval: 0.5}, schema)
	if err != nil {
		t.Fatalf("create grouper: %v", err)
	}
	rnd := rand.New(rand.NewSource(0xFEED))
	records := make([]*Record, keyForEquivalenceN)
	for i := range records {
		if rnd.Intn(100) < 10 {
			records[i] = NewRecordWithNulls(schema,
				map[string]float64{"score": 0},
				map[string]bool{"score": true})
			continue
		}
		records[i] = NewRecord(schema, map[string]float64{"score": rnd.Float64() * 10})
	}
	assertStreamableEquivalence(t, g, "score", records)
}

func TestGrouper_Date_KeyFor_EquivalentToGroup(t *testing.T) {
	// Sweep every component the buffered Group() supports; KeyFor must
	// match for each one.
	components := []string{"year", "quarter", "month", "week", "day", "day_of_week"}
	for _, comp := range components {
		t.Run(comp, func(t *testing.T) {
			schema := dateSchema()
			g := makeDateGrouper(t, comp, schema)

			rnd := rand.New(rand.NewSource(int64(0xDA7E00 + len(comp))))
			records := make([]*Record, keyForEquivalenceN)
			for i := range records {
				if rnd.Intn(100) < 10 {
					records[i] = NewRecordWithNulls(schema,
						map[string]float64{"enrolled": 0},
						map[string]bool{"enrolled": true})
					continue
				}
				// Random date in a ~10-year window centred on 2024.
				// epochDays = day count since unix epoch.
				day := float64(rnd.Intn(3650) + 19000)
				records[i] = NewRecord(schema, map[string]float64{"enrolled": day})
			}
			assertStreamableEquivalence(t, g, "enrolled", records)
		})
	}
}

func TestGrouper_Date_KeyFor_FiscalOffset(t *testing.T) {
	// Fiscal-offset variants — KeyFor must apply the same fiscal-year
	// translation Group does. Sweep year + quarter at a couple of
	// offsets to catch both the FY-only and FY-Q paths.
	schema := dateSchema()
	cases := []struct {
		name      string
		component string
		offset    int
	}{
		{"year_april_start", "year", 3},
		{"year_october_start", "year", 9},
		{"year_negative", "year", -3},
		{"quarter_april_start", "quarter", 3},
		{"quarter_october_start", "quarter", -3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			factory := grouperRegistry[types.GROUP_DATE]
			params := json.RawMessage(
				`{"component":"` + tc.component + `","fiscal_offset":` +
					itoa(tc.offset) + `}`)
			g, err := factory(&types.Group{Type: types.GROUP_DATE, Field: "enrolled", Params: params}, schema)
			if err != nil {
				t.Fatalf("create grouper: %v", err)
			}
			rnd := rand.New(rand.NewSource(int64(0xF15CA1 + tc.offset)))
			records := make([]*Record, keyForEquivalenceN)
			for i := range records {
				if rnd.Intn(100) < 8 {
					records[i] = NewRecordWithNulls(schema,
						map[string]float64{"enrolled": 0},
						map[string]bool{"enrolled": true})
					continue
				}
				day := float64(rnd.Intn(3650) + 19000)
				records[i] = NewRecord(schema, map[string]float64{"enrolled": day})
			}
			assertStreamableEquivalence(t, g, "enrolled", records)
		})
	}
}

func TestGrouper_SetValue_KeyFor_EquivalentToGroup(t *testing.T) {
	// SET_VALUE atomic-mask path — bound field is "tags"; empty mask is
	// a legitimate "" bucket key, only null/missing triggers the
	// sentinel.
	schema := makeSetTestSchema(t)
	g, err := newSetValueGrouper(&types.Group{Type: types.GROUP_SET_VALUE, Field: "tags"}, schema)
	if err != nil {
		t.Fatalf("newSetValueGrouper: %v", err)
	}

	rnd := rand.New(rand.NewSource(0x5E700))
	records := make([]*Record, keyForEquivalenceN)
	for i := range records {
		if rnd.Intn(100) < 10 {
			// Null mask — KeyFor must signal ErrGrouperKeyNull.
			records[i] = NewRecordWithWide(schema,
				map[string]float64{},
				map[string]bool{"tags": true},
				map[string]any{"tags": uint64(0)})
			continue
		}
		// Random masks across the 4-bit dictionary range, including 0
		// (empty mask → legitimate "" key).
		mask := uint64(rnd.Intn(16))
		records[i] = makeSetRecord(schema, mask)
	}
	assertStreamableEquivalence(t, g, "tags", records)
}

// TestGrouper_StreamableInterface_Coverage asserts which groupers DO
// and DO NOT implement StreamableGrouper. Pins the interface
// membership so future grouper additions surface in this test.
func TestGrouper_StreamableInterface_Coverage(t *testing.T) {
	schema := numericSchema()
	catSchema := categoricalSchema()
	dSchema := dateSchema()
	sSchema := makeSetTestSchema(t)

	cases := []struct {
		name        string
		make        func(t *testing.T) Grouper
		wantStream  bool
		description string
	}{
		{"GROUP_CATEGORY", func(t *testing.T) Grouper {
			return makeGrouper(t, types.GROUP_CATEGORY, "brand", 0, catSchema)
		}, true, "key derivable per record"},
		{"GROUP_RANGE", func(t *testing.T) Grouper {
			return makeGrouper(t, types.GROUP_RANGE, "age", 10, schema)
		}, true, "key derivable per record"},
		{"GROUP_ROUNDED", func(t *testing.T) Grouper {
			return makeGrouper(t, types.GROUP_ROUNDED, "age", 10, schema)
		}, true, "key derivable per record"},
		{"GROUP_DATE", func(t *testing.T) Grouper {
			return makeDateGrouper(t, "month", dSchema)
		}, true, "key derivable per record (Streamability flag is for Process orchestrator, not key derivation)"},
		{"GROUP_QUANTILE", func(t *testing.T) Grouper {
			factory := grouperRegistry[types.GROUP_QUANTILE]
			g, err := factory(&types.Group{Type: types.GROUP_QUANTILE, Field: "age", Interval: 4}, schema)
			if err != nil {
				t.Fatalf("create QUANTILE: %v", err)
			}
			return g
		}, false, "bucket assignment requires sorted view of full set"},
		{"GROUP_SET_VALUE", func(t *testing.T) Grouper {
			g, err := newSetValueGrouper(&types.Group{Type: types.GROUP_SET_VALUE, Field: "tags"}, sSchema)
			if err != nil {
				t.Fatalf("create SET_VALUE: %v", err)
			}
			return g
		}, true, "atomic-mask path is single-key per record"},
		{"GROUP_SET_PER_ELEMENT", func(t *testing.T) Grouper {
			g, err := newSetPerElementGrouper(&types.Group{Type: types.GROUP_SET_PER_ELEMENT, Field: "tags"}, sSchema)
			if err != nil {
				t.Fatalf("create SET_PER_ELEMENT: %v", err)
			}
			return g
		}, false, "multi-key fan-out cannot fit single-key StreamableGrouper signature"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := tc.make(t)
			_, ok := g.(StreamableGrouper)
			if ok != tc.wantStream {
				t.Fatalf("%s: StreamableGrouper assertion = %v, want %v (%s)",
					tc.name, ok, tc.wantStream, tc.description)
			}
		})
	}
}

// itoa is a tiny inline base-10 int formatter used to compose JSON
// params without depending on strconv inside the test.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
