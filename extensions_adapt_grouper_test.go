package pulse

import (
	stderrors "errors"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/processing"
	"github.com/frankbardon/pulse/types"
)

// ---- grouper fixtures --------------------------------------------------

// adaptGrpBase returns whatever index map it was built with.
type adaptGrpBase struct{ idx map[string][]int }

func (g adaptGrpBase) Group(extend.Rows, string) (map[string][]int, error) { return g.idx, nil }

type adaptGrpStreaming struct {
	adaptGrpBase
	key  string
	null bool
	n    *int
}

func (g adaptGrpStreaming) KeyForRow(extend.Record, string) (string, bool, error) {
	if g.n != nil {
		*g.n++
	}
	if g.null {
		return "", true, extend.ErrGrouperKeyNull
	}
	return g.key, true, nil
}

type adaptGrpMulti struct {
	adaptGrpBase
	null bool
}

func (g adaptGrpMulti) KeysForRow(extend.Record, string) ([]string, bool, error) {
	if g.null {
		return []string{"x"}, true, extend.ErrGrouperKeyNull
	}
	return []string{"a", "b"}, true, nil
}

type adaptGrpBoth struct {
	adaptGrpStreaming
}

func (adaptGrpBoth) KeysForRow(extend.Record, string) ([]string, bool, error) {
	return []string{"a", "b"}, true, nil
}

type adaptGrpSelfEmitting struct{ adaptGrpBase }

func (adaptGrpSelfEmitting) Components() (map[string]any, error) {
	return map[string]any{"self": true}, nil
}

func twoRecords() []*processing.Record {
	schema := &encoding.Schema{Fields: []encoding.Field{{Name: "x", Type: encoding.FieldTypeF64}}}
	return []*processing.Record{
		processing.NewRecord(schema, map[string]float64{"x": 1}),
		processing.NewRecord(schema, map[string]float64{"x": 2}),
	}
}

// TestAdaptGrouper_ForwardsExactlyTheImplementedSiblings walks every
// capability combination: StreamingGrouper / MultiKeyStreamingGrouper
// are visible on the adapted value iff the embedder value implements
// the extend sibling, MetaGrouper iff a ComponentsFunc is supplied. An
// interface-embedding wrapper (the R6 bug) fails every keying row
// whenever emit is set.
func TestAdaptGrouper_ForwardsExactlyTheImplementedSiblings(t *testing.T) {
	emit := func(extend.Grouper) (map[string]any, error) { return map[string]any{"k": 1}, nil }
	cases := []struct {
		name             string
		inner            extend.Grouper
		streaming, multi bool
	}{
		{"base", adaptGrpBase{}, false, false},
		{"streaming", adaptGrpStreaming{key: "k"}, true, false},
		{"multi", adaptGrpMulti{}, false, true},
		{"streaming+multi", adaptGrpBoth{adaptGrpStreaming{key: "k"}}, true, true},
	}
	for _, c := range cases {
		for _, withEmit := range []bool{false, true} {
			var e GrouperComponentsFunc
			if withEmit {
				e = emit
			}
			got := adaptGrouper("GROUP_ACME_T", c.inner, e)
			_, isS := got.(processing.StreamingGrouper)
			_, isK := got.(processing.MultiKeyStreamingGrouper)
			_, isM := got.(processing.MetaGrouper)
			if isS != c.streaming || isK != c.multi || isM != withEmit {
				t.Errorf("%s emit=%v: streaming=%v multi=%v meta=%v; want %v %v %v",
					c.name, withEmit, isS, isK, isM, c.streaming, c.multi, withEmit)
			}
			if isM {
				if m, err := got.(processing.MetaGrouper).Components(); err != nil || m["k"] != 1 {
					t.Errorf("%s: Components = %v, %v", c.name, m, err)
				}
			}
		}
	}
}

// TestAdaptGrouper_StreamingWithComponentsDrivesKeyForRow is the
// single-key half of R6 at the engine seam: a streaming grouper with a
// ComponentsFunc is accepted by processing.NewGroupKeyer (the ONE key
// dispatch every streaming grouped run uses) and keyed through the
// embedder's KeyForRow, not refused as "neither keying interface".
func TestAdaptGrouper_StreamingWithComponentsDrivesKeyForRow(t *testing.T) {
	n := 0
	emit := func(extend.Grouper) (map[string]any, error) { return nil, nil }
	adapted := adaptGrouper("GROUP_ACME_T", adaptGrpStreaming{key: "hi", n: &n}, emit)
	keyer, err := processing.NewGroupKeyer(adapted)
	if err != nil {
		t.Fatalf("NewGroupKeyer refused the adapted streaming grouper: %v", err)
	}
	keys, ok, err := keyer.Keys(twoRecords()[0], "x")
	if err != nil || !ok || len(keys) != 1 || keys[0] != "hi" || n != 1 {
		t.Errorf("Keys = %v, %v, %v (KeyForRow calls %d); want [hi], true, nil, 1", keys, ok, err, n)
	}
}

// TestAdaptGrouper_NullSentinelMapsToSkip: extend.ErrGrouperKeyNull
// from either key method is the engine's ok=false, not a run failure.
func TestAdaptGrouper_NullSentinelMapsToSkip(t *testing.T) {
	rec := twoRecords()[0]
	s := adaptGrouper("GROUP_ACME_T", adaptGrpStreaming{null: true}, nil).(processing.StreamingGrouper)
	if key, ok, err := s.KeyForRow(rec, "x"); key != "" || ok || err != nil {
		t.Errorf("KeyForRow = %q, %v, %v; want \"\", false, nil", key, ok, err)
	}
	k := adaptGrouper("GROUP_ACME_T", adaptGrpMulti{null: true}, nil).(processing.MultiKeyStreamingGrouper)
	if keys, ok, err := k.KeysForRow(rec, "x"); keys != nil || ok || err != nil {
		t.Errorf("KeysForRow = %v, %v, %v; want nil, false, nil", keys, ok, err)
	}
	// A non-sentinel error still propagates.
	boom := stderrors.New("boom")
	k2 := grpMulti{multi: errMulti{boom}}
	if _, _, err := k2.KeysForRow(rec, "x"); !stderrors.Is(err, boom) {
		t.Errorf("non-sentinel err = %v, want boom", err)
	}
	s2 := grpStreaming{streaming: errStreaming{boom}}
	if _, _, err := s2.KeyForRow(rec, "x"); !stderrors.Is(err, boom) {
		t.Errorf("non-sentinel err = %v, want boom", err)
	}
}

type errMulti struct{ err error }

func (e errMulti) KeysForRow(extend.Record, string) ([]string, bool, error) { return nil, false, e.err }

type errStreaming struct{ err error }

func (e errStreaming) KeyForRow(extend.Record, string) (string, bool, error) { return "", false, e.err }

// TestAdaptGrouper_TranslatesIndexMap: indices come back as the
// engine's own record pointers; an out-of-range index is a coded error.
func TestAdaptGrouper_TranslatesIndexMap(t *testing.T) {
	recs := twoRecords()
	got, err := adaptGrouper("GROUP_ACME_T", adaptGrpBase{idx: map[string][]int{"a": {1, 0}, "b": {1}}}, nil).Group(recs, "x")
	if err != nil {
		t.Fatalf("Group: %v", err)
	}
	if len(got) != 2 || len(got["a"]) != 2 || got["a"][0] != recs[1] || got["a"][1] != recs[0] ||
		len(got["b"]) != 1 || got["b"][0] != recs[1] {
		t.Errorf("translated map wrong: %v", got)
	}
	for _, bad := range []int{2, -1} {
		_, err := adaptGrouper("GROUP_ACME_T", adaptGrpBase{idx: map[string][]int{"a": {0, bad}}}, nil).Group(recs, "x")
		var ce *perr.CodedError
		if !stderrors.As(err, &ce) || ce.Code != perr.PROCESSING_INTERNAL {
			t.Errorf("index %d: err = %v, want PROCESSING_INTERNAL", bad, err)
		}
	}
	if got, err := adaptGrouper("GROUP_ACME_T", adaptGrpBase{}, nil).Group(recs, "x"); got != nil || err != nil {
		t.Errorf("nil map = %v, %v; want nil, nil", got, err)
	}
}

// TestAdaptGrouper_SelfEmittingComponentsKept: with no ComponentsFunc a
// value carrying its own Components() still emits (MetaGrouper), and
// an explicit ComponentsFunc wins over it.
func TestAdaptGrouper_SelfEmittingComponentsKept(t *testing.T) {
	got := adaptGrouper("GROUP_ACME_T", adaptGrpSelfEmitting{}, nil)
	meta, ok := got.(processing.MetaGrouper)
	if !ok {
		t.Fatal("self-emitting grouper lost MetaGrouper")
	}
	if m, _ := meta.Components(); m["self"] != true {
		t.Errorf("Components = %v, want self=true", m)
	}
	explicit := func(extend.Grouper) (map[string]any, error) { return map[string]any{"explicit": 1}, nil }
	m, _ := adaptGrouper("GROUP_ACME_T", adaptGrpSelfEmitting{}, explicit).(processing.MetaGrouper).Components()
	if m["explicit"] != 1 {
		t.Errorf("explicit ComponentsFunc did not win: %v", m)
	}
}

// TestAdaptGrouperFactory_PropagatesErrorAndNil keeps the factory
// adapter transparent for the two non-instance outcomes.
func TestAdaptGrouperFactory_PropagatesErrorAndNil(t *testing.T) {
	boom := stderrors.New("boom")
	f := adaptGrouperFactory(GrouperRegistration{
		Factory: func(*types.Group, *encoding.Schema) (extend.Grouper, error) { return nil, boom },
	})
	if _, err := f(&types.Group{}, nil); !stderrors.Is(err, boom) {
		t.Errorf("err = %v, want boom", err)
	}
	f = adaptGrouperFactory(GrouperRegistration{
		Factory: func(*types.Group, *encoding.Schema) (extend.Grouper, error) { return nil, nil },
	})
	if inst, err := f(&types.Group{}, nil); inst != nil || err != nil {
		t.Errorf("nil instance = %v, %v; want nil, nil", inst, err)
	}
}

// ---- filterers ---------------------------------------------------------

type adaptFlt struct{ seen *extend.Record }

func (f adaptFlt) Build(spec *types.Filterer, _ *encoding.Schema) (extend.FilterFunc, error) {
	if spec != nil && spec.Field == "fail" {
		return nil, stderrors.New("build failed")
	}
	return func(r extend.Record) (bool, error) {
		*f.seen = r
		v, _ := r.NumericValue("x")
		return v > 1, nil
	}, nil
}

type adaptFltSelfEmitting struct{ adaptFlt }

func (adaptFltSelfEmitting) Components() (map[string]any, error) {
	return map[string]any{"self": true}, nil
}

// TestAdaptFilterer_BuildForwardsRecordAndMeta: the built FilterFunc
// sees the engine's own record and returns the embedder's verdict;
// MetaFilterer is present iff a ComponentsFunc (or self-emission) is.
func TestAdaptFilterer_BuildForwardsRecordAndMeta(t *testing.T) {
	recs := twoRecords()
	var seen extend.Record
	emit := func(extend.FiltererBuilder) (map[string]any, error) { return map[string]any{"k": 1}, nil }
	for _, withEmit := range []bool{false, true} {
		var e FiltererComponentsFunc
		if withEmit {
			e = emit
		}
		b := adaptFilterer(adaptFlt{seen: &seen}, e)
		fn, err := b.Build(&types.Filterer{}, nil)
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		for i, want := range []bool{false, true} {
			got, err := fn(recs[i])
			if err != nil || got != want {
				t.Errorf("emit=%v row %d: %v, %v; want %v", withEmit, i, got, err, want)
			}
			if seen.(*processing.Record) != recs[i] {
				t.Errorf("emit=%v row %d: FilterFunc did not see the engine record", withEmit, i)
			}
		}
		meta, isM := b.(processing.MetaFilterer)
		if isM != withEmit {
			t.Errorf("emit=%v: MetaFilterer = %v", withEmit, isM)
		}
		if isM {
			if m, err := meta.Components(); err != nil || m["k"] != 1 {
				t.Errorf("Components = %v, %v", m, err)
			}
		}
		if _, err := b.Build(&types.Filterer{Field: "fail"}, nil); err == nil {
			t.Error("Build error not propagated")
		}
	}
	m, _ := adaptFilterer(adaptFltSelfEmitting{adaptFlt{seen: &seen}}, nil).(processing.MetaFilterer).Components()
	if m["self"] != true {
		t.Errorf("self-emitting filterer lost Components: %v", m)
	}
	if f := adaptFiltererFactory(FiltererRegistration{Factory: func() extend.FiltererBuilder { return nil }}); f() != nil {
		t.Error("nil builder not passed through")
	}
}
