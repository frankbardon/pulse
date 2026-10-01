package pulse

import (
	"errors"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/types"
)

type adaptBase struct{}

func (adaptBase) Aggregate(rows extend.Rows, _ string) (float64, error) {
	return float64(rows.Len()), nil
}

type adaptOnline struct{ adaptBase }

func (adaptOnline) UpdateRow(extend.Record, string) error { return nil }
func (adaptOnline) Finalize() (float64, error)            { return 7, nil }

type adaptRich struct{ adaptBase }

func (adaptRich) Rich() (any, error) { return []string{"r"}, nil }

type adaptOnlineRich struct{ adaptOnline }

func (adaptOnlineRich) Rich() (any, error) { return []string{"r"}, nil }

// TestAdaptAggregator_ForwardsExactlyTheImplementedSiblings walks every
// capability combination and asserts each engine sibling is visible on
// the adapted value iff the embedder value (or, for Meta, the
// registration) supplies it. An interface-embedding wrapper fails the
// Online/Rich rows whenever emit is set.
func TestAdaptAggregator_ForwardsExactlyTheImplementedSiblings(t *testing.T) {
	emit := func(extend.Aggregator) (map[string]any, error) { return map[string]any{"k": 1}, nil }
	cases := []struct {
		name         string
		inner        extend.Aggregator
		online, rich bool
	}{
		{"base", adaptBase{}, false, false},
		{"online", adaptOnline{}, true, false},
		{"rich", adaptRich{}, false, true},
		{"online+rich", adaptOnlineRich{}, true, true},
	}
	for _, c := range cases {
		for _, withEmit := range []bool{false, true} {
			var fn AggregatorComponentsFunc
			if withEmit {
				fn = emit
			}
			got := adaptAggregator(c.inner, fn)
			_, isOnline := got.(processing.OnlineAggregator)
			_, isRich := got.(processing.RichAggregator)
			_, isMeta := got.(processing.MetaAggregator)
			if isOnline != c.online || isRich != c.rich || isMeta != withEmit {
				t.Errorf("%s emit=%v: online=%v rich=%v meta=%v; want %v %v %v",
					c.name, withEmit, isOnline, isRich, isMeta, c.online, c.rich, withEmit)
			}
			if isOnline {
				if v, err := got.(processing.OnlineAggregator).Finalize(); err != nil || v != 7 {
					t.Errorf("%s: Finalize = %v, %v; want 7", c.name, v, err)
				}
			}
			if isRich {
				if v, err := got.(processing.RichAggregator).Rich(); err != nil || v == nil {
					t.Errorf("%s: Rich = %v, %v", c.name, v, err)
				}
			}
			if isMeta {
				if m, err := got.(processing.MetaAggregator).Components(); err != nil || m["k"] != 1 {
					t.Errorf("%s: Components = %v, %v", c.name, m, err)
				}
			}
		}
	}
}

type adaptSelfEmitting struct{ adaptOnline }

func (adaptSelfEmitting) Components() (map[string]any, error) { return map[string]any{"self": 1}, nil }

// TestAdaptAggregator_TypeLevelComponentsKept asserts a value with its
// own Components() method still surfaces as MetaAggregator when the
// registration supplies no ComponentsFunc, and that an explicit
// ComponentsFunc wins over it.
func TestAdaptAggregator_TypeLevelComponentsKept(t *testing.T) {
	got := adaptAggregator(adaptSelfEmitting{}, nil)
	meta, ok := got.(processing.MetaAggregator)
	if !ok {
		t.Fatal("type-level Components() not surfaced as MetaAggregator")
	}
	if m, err := meta.Components(); err != nil || m["self"] != 1 {
		t.Errorf("Components = %v, %v; want self=1", m, err)
	}
	if _, ok := got.(processing.OnlineAggregator); !ok {
		t.Error("OnlineAggregator lost alongside type-level Components()")
	}
	explicit := func(extend.Aggregator) (map[string]any, error) { return map[string]any{"fn": 1}, nil }
	m, _ := adaptAggregator(adaptSelfEmitting{}, explicit).(processing.MetaAggregator).Components()
	if m["fn"] != 1 {
		t.Errorf("explicit ComponentsFunc did not win: %v", m)
	}
}

// TestAdaptAggregator_RowsViewIsZeroCopy asserts the buffered call sees
// the engine's own record pointers through the Rows view.
func TestAdaptAggregator_RowsViewIsZeroCopy(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{{Name: "x", Type: encoding.FieldTypeF64}}}
	recs := []*processing.Record{
		processing.NewRecord(schema, map[string]float64{"x": 1}),
		processing.NewRecord(schema, map[string]float64{"x": 2}),
	}
	view := recordRows(recs)
	if view.Len() != 2 {
		t.Fatalf("Len = %d, want 2", view.Len())
	}
	for i := range recs {
		if view.At(i).(*processing.Record) != recs[i] {
			t.Errorf("At(%d) is not the engine's record", i)
		}
	}
	got, err := adaptAggregator(adaptBase{}, nil).Aggregate(recs, "x")
	if err != nil || got != 2 {
		t.Errorf("Aggregate through adapter = %v, %v; want 2", got, err)
	}
}

// TestAdaptAggregatorFactory_PropagatesErrorAndNil keeps the factory
// adapter transparent for the two non-instance outcomes.
func TestAdaptAggregatorFactory_PropagatesErrorAndNil(t *testing.T) {
	boom := errors.New("boom")
	f := adaptAggregatorFactory(AggregatorRegistration{
		Factory: func(*types.Aggregation, *encoding.Schema) (extend.Aggregator, error) { return nil, boom },
	})
	if _, err := f(&types.Aggregation{}, nil); !errors.Is(err, boom) {
		t.Errorf("err = %v, want boom", err)
	}
	f = adaptAggregatorFactory(AggregatorRegistration{
		Factory: func(*types.Aggregation, *encoding.Schema) (extend.Aggregator, error) { return nil, nil },
	})
	if inst, err := f(&types.Aggregation{}, nil); inst != nil || err != nil {
		t.Errorf("nil instance = %v, %v; want nil, nil", inst, err)
	}
}
