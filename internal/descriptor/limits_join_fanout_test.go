package descriptor

import (
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/limits"
	"github.com/frankbardon/pulse/types"
)

// joinedFanOutSchema is a joined schema (left fields, then the right
// side's under the "r_" prefix): a categorical right key r_k of 4
// entries, a categorical r_c of 2 and a numeric right key r_id.
func joinedFanOutSchema(t *testing.T) *encoding.Schema {
	t.Helper()
	d := encoding.NewDictionary()
	for _, v := range []string{"a", "b", "c", "d"} {
		if _, err := d.Add(v); err != nil {
			t.Fatal(err)
		}
	}
	two := encoding.NewDictionary()
	for _, v := range []string{"x", "y"} {
		if _, err := two.Add(v); err != nil {
			t.Fatal(err)
		}
	}
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "c", Type: encoding.FieldTypeCategoricalU8, Dictionary: two},
		{Name: "r_c", Type: encoding.FieldTypeCategoricalU8, Dictionary: two},
		{Name: "k", Type: encoding.FieldTypeCategoricalU8, Dictionary: d},
		{Name: "id", Type: encoding.FieldTypeU32},
		{Name: "num", Type: encoding.FieldTypeF64},
		{Name: "r_k", Type: encoding.FieldTypeCategoricalU8, Dictionary: d},
		{Name: "r_id", Type: encoding.FieldTypeU32},
		{Name: "r_v", Type: encoding.FieldTypeF64},
	}}
}

func joinIn(left, right int64, on ...types.OnPair) LimitInputs {
	return LimitInputs{Records: left, Join: true, JoinRightRows: right,
		JoinSpec: &types.JoinSpec{Right: "right.pulse", Kind: "inner", As: "r_", On: on}}
}

var (
	onCat = types.OnPair{LeftField: "k", RightField: "k"}
	onNum = types.OnPair{LeftField: "id", RightField: "id"}
	onTwo = types.OnPair{LeftField: "c", RightField: "c"}
)

// TestJoinRowsBound: a right side of at most one row is the only
// provably unique key (no bound); a categorical right key bounds one
// key's matches by right - dictionary + 1 (a composite key by its
// smallest column bound); a key with no dictionary is left x right.
func TestJoinRowsBound(t *testing.T) {
	schema := joinedFanOutSchema(t)
	cases := []struct {
		name string
		in   LimitInputs
		want limits.JoinBound
		ok   bool
	}{
		{"categorical key", joinIn(100, 10, onCat), limits.JoinBound{Rows: 100 * 7, Basis: limits.JoinBoundDictionary}, true},
		{"dictionary covers the right side", joinIn(100, 3, onCat), limits.JoinBound{Rows: 100, Basis: limits.JoinBoundDictionary}, true},
		{"numeric key", joinIn(100, 10, onNum), limits.JoinBound{Rows: 100 * 10, Basis: limits.JoinBoundLeftXRight}, true},
		{"composite key", joinIn(100, 10, onNum, onCat), limits.JoinBound{Rows: 100 * 7, Basis: limits.JoinBoundDictionary}, true},
		{"composite categorical key: smallest bound", joinIn(100, 10, onTwo, onCat, onNum), limits.JoinBound{Rows: 100 * 7, Basis: limits.JoinBoundDictionary}, true},
		{"one right row", joinIn(100, 1, onNum), limits.JoinBound{}, false},
		{"empty right side", joinIn(100, 0, onCat), limits.JoinBound{}, false},
		{"unknown right count", joinIn(100, -1, onCat), limits.JoinBound{}, false},
		{"unknown left count", joinIn(-1, 10, onCat), limits.JoinBound{}, false},
		{"no join", memIn(100), limits.JoinBound{}, false},
	}
	for _, c := range cases {
		got, ok := JoinRowsBound(schema, c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("%s: bound %+v %v, want %+v %v", c.name, got, ok, c.want, c.ok)
		}
	}
}

// TestRequestLimitFindings_JoinFanOutPossible: a join whose certain
// estimate (one match per left row) fits but whose estimate on the
// joined-row bound does not is a POSSIBLE finding carrying the bound —
// never the pre-flight refusal; a certain breach reports no second
// finding; a provably unique right side and a limit at the bound
// estimate report none.
func TestRequestLimitFindings_JoinFanOutPossible(t *testing.T) {
	schema := joinedFanOutSchema(t)
	// The buffered arm: per-row terms scale with the joined rows.
	req := &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_MEDIAN, Field: "r_v", Label: "m"}}}
	for _, c := range []struct {
		name  string
		in    LimitInputs
		basis string
		rows  int64
	}{
		{"categorical key", joinIn(1000, 10, onCat), limits.JoinBoundDictionary, 7000},
		{"numeric key", joinIn(1000, 10, onNum), limits.JoinBoundLeftXRight, 10000},
	} {
		t.Run(c.name, func(t *testing.T) {
			certain, ok := EstimateMemory(req, schema, nil, c.in)
			if !ok {
				t.Fatal("no estimate")
			}
			bound, ok := estimateMemory(req, schema, nil, c.in, c.rows)
			if !ok || bound <= certain {
				t.Fatalf("bound estimate %d (certain %d)", bound, certain)
			}
			l := limits.Defaults()
			l.MaxEstimatedMemory = certain
			fs := RequestLimitFindings(req, schema, nil, l, c.in)
			want := limits.Finding{Limit: limits.MaxEstimatedMemory, Configured: certain, Estimated: bound, Grade: limits.Possible,
				Join: limits.JoinBound{Rows: c.rows, Basis: c.basis}}
			if len(fs) != 1 || fs[0] != want {
				t.Fatalf("findings %+v, want [%+v]", fs, want)
			}
			d := limitFindingsDescriptor(fs)[0].Details
			if d["join"] != 0 || d["join_rows_bound"] != c.rows || d["join_bound"] != c.basis {
				t.Fatalf("details %v", d)
			}
			if err := LimitRefusal(req, schema, nil, l, c.in); err != nil {
				t.Fatalf("a possible finding refused: %v", err)
			}
			// A certain breach: one finding, the certain one.
			l.MaxEstimatedMemory = certain - 1
			if fs := RequestLimitFindings(req, schema, nil, l, c.in); len(fs) != 1 || fs[0].Grade != limits.Certain || fs[0].Join != (limits.JoinBound{}) {
				t.Fatalf("certain breach: findings %+v", fs)
			}
			// At the bound estimate: none.
			l.MaxEstimatedMemory = bound
			if fs := RequestLimitFindings(req, schema, nil, l, c.in); len(fs) != 0 {
				t.Fatalf("at the bound estimate: findings %+v", fs)
			}
			// Provably unique (one right row): none.
			one := c.in
			one.JoinRightRows = 1
			l.MaxEstimatedMemory = certain
			if fs := RequestLimitFindings(req, schema, nil, l, one); len(fs) != 0 {
				t.Fatalf("one right row: findings %+v", fs)
			}
		})
	}
}

// TestJoinFanOutRefusal: the build-side backstop refuses the estimate
// on left x the measured key multiplicity when it exceeds a set
// MaxEstimatedMemory — the memory refusal's details plus join,
// join_rows_bound and right_key_multiplicity — and never when no key
// repeats or the limit is Unlimited.
func TestJoinFanOutRefusal(t *testing.T) {
	schema := joinedFanOutSchema(t)
	req := &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_MEDIAN, Field: "r_v", Label: "m"}}}
	in := joinIn(1000, 10, onCat)
	certain, _ := EstimateMemory(req, schema, nil, in)
	twice, _ := estimateMemory(req, schema, nil, in, 2000)
	l := limits.Defaults()
	if err := JoinFanOutRefusal(req, schema, nil, l, in, 5); err != nil {
		t.Fatalf("unlimited: %v", err)
	}
	l.MaxEstimatedMemory = certain
	if err := JoinFanOutRefusal(req, schema, nil, l, in, 1); err != nil {
		t.Fatalf("unique keys: %v", err)
	}
	err := JoinFanOutRefusal(req, schema, nil, l, in, 2)
	if err == nil {
		t.Fatal("multiplicity 2 over the cap: no refusal")
	}
	for k, v := range map[string]any{"limit": "max_estimated_memory", "configured": certain, "observed": twice,
		"join": 0, "join_rows_bound": int64(2000), "right_key_multiplicity": int64(2)} {
		if err.Details[k] != v {
			t.Fatalf("details[%s] = %v, want %v (%v)", k, err.Details[k], v, err.Details)
		}
	}
	l.MaxEstimatedMemory = twice
	if err := JoinFanOutRefusal(req, schema, nil, l, in, 2); err != nil {
		t.Fatalf("at the estimate: %v", err)
	}
}
